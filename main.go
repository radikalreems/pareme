package main

import (
	"context"
	"fmt"
	"sync"
	"time"
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

	// Start the miner manager with the current chain height
	consoleMineChan := minerManager(ctx, &wg, newBlockChan)

	runUI(ctx, cancel, &wg, consoleMineChan, dialIPChan)

}
