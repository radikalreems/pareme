package main

import (
	"context"
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
	Context    context.Context
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

var AllPeers map[int]*Peer = make(map[int]*Peer)

// newPeer creates and initializes a new Peer instance
func newPeer(address string, port string, conn net.Conn, isOutbound bool) Peer {
	peer := Peer{
		Address:    address,
		Port:       port,
		Conn:       conn,
		Context:    nil,
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
				// Call from console to shut down
				printToLog("Network shutting down")
				return

			case ip := <-dialIPChan:
				// Connect to request peer
				wg.Add(1)
				go connectToPeer(wg, ip, pendingPeerChan)

			case pendingPeer := <-pendingPeerChan:
				// Create peer child context
				peerCtx, cancel := context.WithCancel(ctx)
				pendingPeer.Context = peerCtx

				// Add peer to global map
				AllPeers[0] = &pendingPeer
				printToLog(fmt.Sprintf("Added peer %s to AllPeers (total: %d)",
					pendingPeer.Address, len(AllPeers)))

				// Handle the peer
				managePeer(ctx, peerCtx, cancel, wg, AllPeers[0])
			default: // Clean up All Peers
				for position, peer := range AllPeers {
					select {
					case <-peer.Context.Done():
						delete(AllPeers, position)
					default:
						continue
					}
				}
			}
		}
	}()

	return dialIPChan
}

// peerMaker listens for incoming connections and creates new peers
func peerMaker(ctx context.Context, wg *sync.WaitGroup) chan Peer {
	acceptChan := make(chan Peer, 10)  // Channel for accepted connections
	pendingPeerChan := make(chan Peer) // Channel for pending peer connections

	printToLog("Network listening on :8080")
	wg.Add(1)
	go func() { // Main goroutine to manage the listener and peer flow
		defer wg.Done()

		listener, err := net.Listen("tcp", ":8080")
		if err != nil {
			printToLog(fmt.Sprintf("Network error: %v", err))
			return
		}

		// Spawn a goroutine to handle incoming connections concurrently
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

		// Main loop: process accepted peers or handle shutdown.
		for {
			select {
			case <-ctx.Done():
				printToLog("peerMaker Shutting Down")
				if err := listener.Close(); err != nil {
					printToLog(fmt.Sprintf("Failed to close listener: %v", err))
				}
				return

			case peer := <-acceptChan:
				printToLog(fmt.Sprintf("Connected to peer: %s", peer.Address))

				pendingPeerChan <- peer
			}
		}
	}()

	return pendingPeerChan
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

// managePeer handles individual peer connections and communication
func managePeer(ctx context.Context, peerCtx context.Context, cancel context.CancelFunc, wg *sync.WaitGroup, peer *Peer) {

	var peerWg sync.WaitGroup
	var listenerWg sync.WaitGroup

	// Manages both incoming and outgoing goroutines for a clean shutdown
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-peerCtx.Done(): // Child context canceled
				select {
				case <-ctx.Done(): // Program context canceled
					printToLog(fmt.Sprintf("Closing connection for peer %s", peer.Address))
					peerWg.Wait()
					peer.Conn.Close()
					listenerWg.Wait()
					return
				default: // Peer connection is closed, shutdown peer manager
					printToLog(fmt.Sprintf("Connection closed for %v", peer.Address))
					peerWg.Wait()
					return
				}
			default:
				time.Sleep(1 * time.Second) // Prevent tight loop
			}
		}
	}()

	// Used by the incoming/outgoing go routines to coordinate getting the message back to the requester
	RequestResponseChan := make(chan MessageRequest)

	peerWg.Add(2)
	go handleIncoming(peerCtx, cancel, &peerWg, &listenerWg, peer, RequestResponseChan)
	go handleOutgoing(peerCtx, &peerWg, peer, RequestResponseChan)
}

func peerListener(peerCtx context.Context, cancel context.CancelFunc, listenerWg *sync.WaitGroup, peer *Peer, readChan chan Message) {

	// Listen to incoming messages
	buff := make([]byte, 1024) // Buffer for reading messages

	listenerWg.Add(1)
	go func() {
		defer listenerWg.Done()
		var leftover []byte // Accumulate partials
		for {
			n, err := peer.Conn.Read(buff)
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					printToLog(fmt.Sprintf("Error reading from peer: %v", err))
				}
				select { // Determine reason for closed connection
				case <-peerCtx.Done(): // Context was closed; exit
					return
				default: // Peer closed connection; close peer context and exit
					cancel()
					return
				}
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

}

func handleIncoming(peerCtx context.Context, cancel context.CancelFunc, peerWg *sync.WaitGroup, listenerWg *sync.WaitGroup, peer *Peer, RequestResponseChan chan MessageRequest) {
	// Handle incoming messages
	defer peerWg.Done()

	readChan := make(chan Message)

	peerListener(peerCtx, cancel, listenerWg, peer, readChan)

	var pendingRequests []MessageRequest       // Slice to store pending MessageRequests
	ticker := time.NewTicker(10 * time.Second) // Purge pendingRequests every 10 seconds
	defer ticker.Stop()
	// Process the message
	for {
		select {
		case <-peerCtx.Done():
			for _, req := range pendingRequests {
				close(req.MsgResponseChan)
			}
			printToLog("handleIncoming Closing.")
			return
		case msgReq := <-RequestResponseChan:
			// Add new requests to the slice
			pendingRequests = append(pendingRequests, msgReq)
			//printToLog(fmt.Sprintf("Added request %d to pending list (total: %d)",
			//msgReq.Message.Reference, len(pendingRequests)))

		case received := <-readChan:
			printToLog("Recieved: " + describeMessage(received))
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
							//printToLog(fmt.Sprintf("Matched response ref %d, removed from pending (remaining: %d)",
							//	received.Reference, len(pendingRequests)))
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
}

func handleOutgoing(peerCtx context.Context, peerWg *sync.WaitGroup, peer *Peer, RequestResponseChan chan MessageRequest) {
	// Handle outgoing messages
	defer peerWg.Done()
	for {
		select {
		case <-peerCtx.Done():
			printToLog("handleOutgoing Closing.")
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
			printToLog("Sent: " + describeMessage(msgReq.Message))
			peer.LastSeen = time.Now()
		}
	}

}

//--------------- QOL FUNCTIONS

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
