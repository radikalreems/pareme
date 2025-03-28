package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Global channels for communication between components
var blockChan = make(chan []Block, 100)             // Send a Block to have it verified and written to file
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

	// Sync chain data from own files
	err := syncChain()
	if err != nil {
		printToLog(fmt.Sprintf("Sync failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	// Chain is valid; start the block writer goroutine
	newBlockChan, err := blockWriter(ctx, &wg)
	if err != nil {
		printToLog(fmt.Sprintf("Blockwriter failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	/*
		// Connect to a peer
		dialIPChan <- "192.168.86.98"
		time.Sleep(2 * time.Second)

		// Sync chain data from peers
		err = syncToPeers()
		if err != nil {
			printToLog(fmt.Sprintf("Syncing chain from peers failed: %v", err))
			cancel()
			wg.Wait()
			return
		}
	*/

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
			response := requestAMessage(0, nil) // Ping | Payload:nil
			println(describeMessage(response))
		case input == "height":
			printToLog("Received 'height' command")
			response := requestAMessage(1, nil) // Height | Payload:nil
			println(describeMessage(response))
		case input[0:7] == "request":
			if len(input) <= 8 {
				fmt.Println("Please provide a range after 'request'")
				continue
			}
			heightRange := strings.TrimSpace(input[8:])
			parts := strings.Split(heightRange, "-")
			result := make([]byte, 8)
			num1, _ := strconv.ParseUint(parts[0], 10, 32)
			num2, _ := strconv.ParseUint(parts[1], 10, 32)
			printToLog(fmt.Sprintf("Asking for blocks %d to %d", num1, num2))
			binary.BigEndian.PutUint32(result[0:4], uint32(num1))
			binary.BigEndian.PutUint32(result[4:8], uint32(num2))
			response := requestAMessage(2, result) // Height | Payload:nil
			println(describeMessage(response))
		default:
			fmt.Println("Unknown command. Try 'stop'")
		}
	}
}
