package main

import (
	"context"
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
	SendChan   chan []byte
}

// Constants for peer connection status
const (
	StatusDisconnect = 0
	StatusConnecting = 1
	StatusActive     = 2
)

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
		SendChan:   make(chan []byte, 10),
	}
	return peer
}

// networkManager manages network connections and peer communications
func networkManager(ctx context.Context, wg *sync.WaitGroup) chan string {
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

	wg.Add(1)
	go func() {
		defer wg.Done()

		listener, err := net.Listen("tcp", ":8080")
		if err != nil {
			printToLog(fmt.Sprintf("Network error: %v", err))
			return
		}
		defer listener.Close()

		printToLog("Network listening on :8080")

		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := listener.Accept()
				if err != nil {
					if strings.Contains(err.Error(), "use of closed network connection") {
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

	// Handle incoming messages
	wgFirst2.Add(1)
	go func() {
		defer wgFirst2.Done()

		buff := make([]byte, 1024) // Buffer for reading messages
		readChan := make(chan []byte)

		wgLast1.Add(1)
		go func() {
			defer wgLast1.Done()
			for {
				n, err := peer.Conn.Read(buff)
				if err != nil {
					if err != net.ErrClosed {
						printToLog("Error reading from peer")
					}
					return
				}
				if n > 0 {
					received := buff[:n]
					readChan <- received
				}
			}
		}()

		for {
			select {
			case <-cancelChan:
				return

			case received := <-readChan:
				printToLog("Recieved from peer " + peer.Address + ": " + string(received))
				peer.LastSeen = time.Now()

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

			case msg := <-peer.SendChan:
				_, err := peer.Conn.Write(msg)
				if err != nil {
					if err != net.ErrClosed {
						printToLog("Error writing to peer")
					}
					return
				}
				printToLog("Sent to peer " + peer.Address + ": " + string(msg))
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
