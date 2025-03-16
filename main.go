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
var (
	blockChan        chan Block // Blocks to be verified and written
	newBlockChan     chan Block // Signals newly written blocks to sync miner
	indexRequestChan chan chan [2]uint32
)

// Initializes and runs the Pareme blockchain node
func main() {
	fmt.Println("Starting Pareme...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure canellation on exit
	var wg sync.WaitGroup

	// Initialize the printer for logging to .log file
	initPrinter(ctx, &wg)

	// Set up channels for block writing and synchronization
	blockChan = make(chan Block, 100)
	newBlockChan = make(chan Block, 10)
	indexRequestChan = make(chan chan [2]uint32, 1)

	// Sync chain data and start block verification/writing goroutine
	height, requestChan := syncChain(ctx, &wg, blockChan, newBlockChan)
	if height == -1 {
		fmt.Println("Sync failed, exiting...")
		wg.Wait()
		return
	}

	printToLog("Initializing Miner...")

	// Start the miner manager with the current chain height
	consoleMineChan := minerManager(ctx, &wg, blockChan, newBlockChan, requestChan)

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

		switch input {
		case "stop":
			printToLog("Recieved 'stop' command")
			cancel()  // Signal all goroutines to stop
			wg.Wait() // Wait for all goroutines to finish
			fmt.Println("Stopping Pareme...")
			return
		case "start mine":
			printToLog("Recieved 'start mine' command")
			consoleMineChan <- 1
		case "stop mine":
			printToLog("Recieved 'stop mine' command")
			consoleMineChan <- 0
		default:
			fmt.Println("Unknown command. Try 'stop'")
		}
	}
}
