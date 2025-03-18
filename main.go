package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Global channels for communication between components
var blockChan = make(chan Block, 100)               // Send a Block to have it verified and written to file
var requestChan = make(chan readRequest)            // Send a readRequest to retrieve a specific block
var indexRequestChan = make(chan chan [2]uint32, 1) // Send a channel to receive index info in it

// Initializes and runs the Pareme blockchain node
func main() {
	fmt.Println("Starting Pareme...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure canellation on exit
	var wg sync.WaitGroup

	// Initialize the printer for logging to .log file
	initPrinter(ctx, &wg)

	// Start the networking goroutine
	dialIPChan := networkManager(ctx, &wg)

	// Sync chain data and start block verification/writing goroutine
	height, newBlockChan := syncChain(ctx, &wg)
	if height == -1 {
		fmt.Println("Sync failed, exiting...")
		wg.Wait()
		return
	}

	// Start the miner manager with the current chain height
	consoleMineChan := minerManager(ctx, &wg, newBlockChan)

	// Console command loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Pareme> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			printToLog(fmt.Sprintf("Input error: %v", err))
			break
		}
		input = strings.TrimSpace(input)

		switch {
		case input == "stop":
			printToLog("Received 'stop' command")
			cancel()  // Signal all goroutines to stop
			wg.Wait() // Wait for all goroutines to finish
			fmt.Println("Stopping Pareme...")
			return
		case input == "start mine":
			printToLog("Received 'start mine' command")
			consoleMineChan <- 1
		case input == "stop mine":
			printToLog("Received 'stop mine' command")
			consoleMineChan <- 0
		case len(input) >= 10 && input[0:10] == "connect to":
			if len(input) <= 11 {
				fmt.Println("Please provide an IP address after 'connect to'")
				continue
			}
			ip := strings.TrimSpace(input[11:])
			printToLog(fmt.Sprintf("Connecting to IP: %s", ip))
			dialIPChan <- ip
		case input == "ping":
			printToLog("Received 'ping' command")
			msg := newMessage(0, 0, 0, nil) // Request | Ping | Ref:0 | payload:nil
			msgChan := make(chan Message)
			msgReq := MessageRequest{
				Message:         msg,
				MsgResponseChan: msgChan,
			}
			AllPeers[0].SendChan <- msgReq
			response := <-msgChan
			println(describeMessage(response))
		default:
			fmt.Println("Unknown command. Try 'stop'")
		}
	}
}
