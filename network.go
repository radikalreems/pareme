package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Peer represents a network peer with its connection details and status
type Peer struct {
	Address    string
	Port       string
	Conn       net.Conn
	Status     int
	IsOutbound bool
	Version    int
	LastSeen   time.Time
	ID         int
	SendChan   chan MessageRequest
}

// Constants for peer connection status
const (
	StatusDisconnect = 0
	StatusConnecting = 1
	StatusActive     = 2
)

var AllPeers []Peer

type Message struct {
	Size        uint16 // Total length of the message, 2 bytes
	Kind        uint8  // Request(0) or Response(1), 1 bytes
	Command     uint8  // Specific action (e.g., if a request then 1=latest height, 2=block request), 1 byte
	Reference   uint16 // Unique ID set by requester, echoed in response, 2 bytes
	PayloadSize uint16 // Length of the payload only, 2 bytes
	Payload     []byte // Variable-length data (heights, blocks, etc.)
}

type MessageRequest struct {
	Message         Message
	MsgResponseChan chan Message
}

func newMessage(kind uint8, command uint8, reference uint16, payload []byte) Message {
	payloadSize := uint16(len(payload))
	totalSize := uint16(8 + len(payload))

	return Message{
		Size:        totalSize,
		Kind:        kind,
		Command:     command,
		Reference:   reference,
		PayloadSize: payloadSize,
		Payload:     payload,
	}
}

// newPeer creates and initializes a new Peer instance
func newPeer(address string, port string, conn net.Conn, isOutbound bool) Peer {
	peer := Peer{
		Address:    address,
		Port:       port,
		Conn:       conn,
		Status:     StatusConnecting,
		IsOutbound: isOutbound,
		Version:    0,
		LastSeen:   time.Now(),
		ID:         0,
		SendChan:   make(chan MessageRequest, 10),
	}
	return peer
}

// networkManager manages network connections and peer communications
func networkManager(ctx context.Context, wg *sync.WaitGroup) chan string {
	printToLog("\nStarting up network manager...")
	dialIPChan := make(chan string) // Channel for receiving IPs to connect to

	pendingPeerChan := peerMaker(ctx, wg) // Channel for handling new peer connections

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				printToLog("Network shutting down")
				return

			case ip := <-dialIPChan:
				// Handle new connection request from console
				wg.Add(1)
				go connectToPeer(wg, ip, pendingPeerChan)

			case pendingPeer := <-pendingPeerChan:
				// Add peer to global slice
				AllPeers = append(AllPeers, pendingPeer)
				printToLog(fmt.Sprintf("Added peer %s to AllPeers (total: %d)",
					pendingPeer.Address, len(AllPeers)))
				// Handle new peer connection
				managePeer(ctx, wg, pendingPeer)
			}
		}
	}()

	return dialIPChan
}

// peerMaker listens for incoming connections and creates new peers
func peerMaker(ctx context.Context, wg *sync.WaitGroup) chan Peer {
	acceptChan := make(chan Peer)      // Channel for accepted connections
	pendingPeerChan := make(chan Peer) // Channel for pending peer connections

	printToLog("Network listening on :8080")
	wg.Add(1)
	go func() {
		defer wg.Done()

		listener, err := net.Listen("tcp", ":8080")
		if err != nil {
			printToLog(fmt.Sprintf("Network error: %v", err))
			return
		}
		defer listener.Close()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := listener.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						printToLog("Listener closed")
						return
					}
					printToLog(fmt.Sprintf("Accept error: %v", err))
					continue
				}
				peer := newPeer(conn.RemoteAddr().String(), conn.RemoteAddr().String(), conn, false)
				acceptChan <- peer
			}
		}()

		for {
			select {
			case <-ctx.Done():
				printToLog("peerMaker Shutting Down")
				return

			case peer := <-acceptChan:
				printToLog(fmt.Sprintf("Connected to peer: %s", peer.Address))

				pendingPeerChan <- peer
			}
		}
	}()

	return pendingPeerChan
}

// managePeer handles individual peer connections and communication
func managePeer(ctx context.Context, wg *sync.WaitGroup, peer Peer) {
	cancelChan := make(chan struct{}) // Channel for cancellation signals

	var wgFirst2 sync.WaitGroup // Incoming/Outgoing needs to finish BEFORE closing the connection
	var wgLast1 sync.WaitGroup  // Incoming listener closes AFTER connection closes

	// Manages both incoming and outgoing goroutines for a clean shutdown
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				printToLog(fmt.Sprintf("Context canceled, closing connection for peer %s", peer.Address))
				close(cancelChan)
				wgFirst2.Wait()
				peer.Conn.Close()
				wgLast1.Wait()
				return
			default:
				time.Sleep(1 * time.Second) // Prevent tight loop
			}
		}
	}()

	// Used by the incoming/outgoing go routines to coordinate getting the message back to the requester
	RequestResponseChan := make(chan MessageRequest)

	// Handle incoming messages
	wgFirst2.Add(1)
	go func() {
		defer wgFirst2.Done()

		buff := make([]byte, 1024) // Buffer for reading messages
		readChan := make(chan Message)

		// Listen to incoming messages
		wgLast1.Add(1)
		go func() {
			defer wgLast1.Done()

			var leftover []byte // Accumulate partials
			for {
				n, err := peer.Conn.Read(buff)
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						printToLog("Error reading from peer")
					}
					return
				}
				data := append(leftover, buff[:n]...) // Combine with leftovers
				for len(data) >= 2 {                  // Need at least messageSize
					msgSize := int(data[0])<<8 | int(data[1]) // uint16 big-endian
					if len(data) < msgSize {
						break
					} // Partial, wait
					msg := data[:msgSize] // Full message in bytes

					message := Message{
						Size:        uint16(msg[0])<<8 | uint16(msg[1]),
						Kind:        msg[2],
						Command:     msg[3],
						Reference:   uint16(msg[4])<<8 | uint16(msg[5]),
						PayloadSize: uint16(msg[6])<<8 | uint16(msg[7]),
						Payload:     msg[8:msgSize],
					}

					readChan <- message   // Full message
					data = data[msgSize:] // Move to next
				}
				leftover = data // Store partial
			}
		}()

		var pendingRequests []MessageRequest       // Slice to store pending MessageRequests
		ticker := time.NewTicker(10 * time.Second) // Purge pendingRequests every 10 seconds
		defer ticker.Stop()
		// Process the message
		for {
			select {
			case <-cancelChan:
				for _, req := range pendingRequests {
					close(req.MsgResponseChan)
				}
				return
			case msgReq := <-RequestResponseChan:
				// Add new requests to the slice
				pendingRequests = append(pendingRequests, msgReq)
				printToLog(fmt.Sprintf("Added request %d to pending list (total: %d)",
					msgReq.Message.Reference, len(pendingRequests)))

			case received := <-readChan:
				printToLog("Recieved from peer " + peer.Address + ": " + describeMessage(received))
				peer.LastSeen = time.Now()

				if received.Kind == 1 { // This is a response
					// Find matching request by Reference
					for i, req := range pendingRequests {
						if req.Message.Reference == received.Reference {
							// Ensure channel is still open (not failed)
							select {
							case req.MsgResponseChan <- received:
								close(req.MsgResponseChan)
								// Remove from slice (swap and truncate)
								pendingRequests[i] = pendingRequests[len(pendingRequests)-1]
								pendingRequests = pendingRequests[:len(pendingRequests)-1]
								printToLog(fmt.Sprintf("Matched response ref %d, removed from pending (remaining: %d)",
									received.Reference, len(pendingRequests)))
							case <-req.MsgResponseChan:
								// Channel already closed, just remove
								pendingRequests[i] = pendingRequests[len(pendingRequests)-1]
								pendingRequests = pendingRequests[:len(pendingRequests)-1]
								printToLog(fmt.Sprintf("Dropped response ref %d for failed request (remaining: %d)",
									received.Reference, len(pendingRequests)))
							}
							break
						}
					}
				} else { // This is a request
					response := respondToMessage(received)
					sendResp := MessageRequest{
						Message:         response,
						MsgResponseChan: nil,
					}
					peer.SendChan <- sendResp
				}
			case <-ticker.C:
				// Purge pendingRequests every 10 seconds
				for i := 0; i < len(pendingRequests); i++ {
					select {
					case <-pendingRequests[i].MsgResponseChan:
						// Channel closed, remove
						printToLog(fmt.Sprintf("Detected closed request ref %d, removing (remaining: %d)",
							pendingRequests[i].Message.Reference, len(pendingRequests)-1))
						pendingRequests[i] = pendingRequests[len(pendingRequests)-1]
						pendingRequests = pendingRequests[:len(pendingRequests)-1]
						i--
					default:
						// Still open, keep it
					}
				}
			}
		}
	}()

	// Handle outgoing messages
	wgFirst2.Add(1)
	go func() {
		defer wgFirst2.Done()
		for {
			select {
			case <-cancelChan:
				return

			case msgReq := <-peer.SendChan:
				if msgReq.MsgResponseChan != nil {
					// Add to pending request BEFORE sending
					RequestResponseChan <- msgReq
					// Send the message to the peer
					_, err := peer.Conn.Write(SerializeMessage(msgReq.Message))
					if err != nil {
						if errors.Is(err, net.ErrClosed) {
							return
						}
						printToLog(fmt.Sprintf("Error writing message %d to peer", msgReq.Message.Reference))
						close(msgReq.MsgResponseChan)
						continue
					}
				} else {
					// Send the message to the peer
					_, err := peer.Conn.Write(SerializeMessage(msgReq.Message))
					if err != nil {
						if errors.Is(err, net.ErrClosed) {
							return
						}
						printToLog(fmt.Sprintf("Error writing message %d to peer", msgReq.Message.Reference))
						continue
					}
				}
				printToLog("Sent to peer " + peer.Address + ": " + string(describeMessage(msgReq.Message)))
				peer.LastSeen = time.Now()
			}
		}
	}()
}

// connectToPeer establishes an outbound connection to a peer
func connectToPeer(wg *sync.WaitGroup, ip string, pendingPeerChan chan Peer) {
	defer wg.Done()

	if !strings.Contains(ip, ":") {
		ip = ip + ":8080" // Append default port if not specified
	}

	conn, err := net.DialTimeout("tcp", ip, 5*time.Second)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to connect to %s: %v", ip, err))
		return
	}

	printToLog(fmt.Sprintf("Connected to peer: %s", ip))

	peer := newPeer(conn.RemoteAddr().String(), conn.RemoteAddr().String(), conn, true)
	pendingPeerChan <- peer

}

func broadcastBlock(height int) {
	printToLog(fmt.Sprintf("Broadcasting Block %d to Pareme....", height))
}

func SerializeMessage(msg Message) []byte {
	totalLength := 8 + len(msg.Payload)

	if int(msg.Size) != totalLength {
		msg.Size = uint16(totalLength)
	}

	result := make([]byte, totalLength)

	result[0] = byte(msg.Size >> 8)
	result[1] = byte(msg.Size & 0xFF)

	result[2] = msg.Kind

	result[3] = msg.Command

	result[4] = byte(msg.Reference >> 8)
	result[5] = byte(msg.Reference & 0xFF)

	result[6] = byte(msg.PayloadSize >> 8)
	result[7] = byte(msg.PayloadSize & 0xFF)

	copy(result[8:], msg.Payload)

	return result
}

func respondToMessage(request Message) Message {
	switch request.Command {
	case 0: // Ping request
		// No payload, simple pong response
		return newMessage(1, 0, request.Reference, nil)

	case 1: // Height request
		// No payload, return current chain height as 4-byte uint32
		height, _ := requestChainStats() // Ignore totalBlocks
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(height))
		return newMessage(1, 1, request.Reference, buf)

	case 2: // Block range request
		if len(request.Payload) != 8 {
			printToLog(fmt.Sprintf("Invalid payload size for ref %d: got %d, want 8",
				request.Reference, len(request.Payload)))
			return Message{}
		}

		startHeight := int(binary.BigEndian.Uint32(request.Payload[0:4]))
		endHeight := int(binary.BigEndian.Uint32(request.Payload[4:8]))

		if startHeight > endHeight {
			printToLog(fmt.Sprintf("Invalid range for ref %d: start %d > end %d",
				request.Reference, startHeight, endHeight))
			return Message{}
		}

		// Collect blocks in range (inclusive)
		var heights []int
		for i := startHeight; i <= endHeight; i++ {
			heights = append(heights, i)
		}
		printToLog(fmt.Sprintf("Recieved request for blocks %d to %d: %v", startHeight, endHeight, heights))
		response := requestBlocks(heights)
		var blocks []Block
		for _, heightGroup := range response {
			blocks = append(blocks, heightGroup...)
		}
		var payload []byte
		for _, block := range blocks {
			blockByte := blockToByte(block)
			payload = append(payload, blockByte[:]...)
		}

		// Return response with all block data
		return newMessage(1, 2, request.Reference, payload)
	}

	// Unknown command
	printToLog(fmt.Sprintf("Unknown command %d for ref %d", request.Command, request.Reference))
	return Message{}
}

var nextReferenceNumber uint16 = 0 // Next reference number to use for request messages

func requestAMessage(command uint8, payload []byte) Message {
	msg := newMessage(0, command, nextReferenceNumber, payload)
	nextReferenceNumber++

	msgChan := make(chan Message)
	msgReq := MessageRequest{
		Message:         msg,
		MsgResponseChan: msgChan,
	}
	AllPeers[0].SendChan <- msgReq
	response := <-msgChan

	return response
}
