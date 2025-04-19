package main

import (
	"context"
	"fmt"
	"pareme/common"
	"pareme/io"
	"pareme/mine"
	"pareme/network"
	"pareme/ui"
	"sync"
)

// Initializes and runs the Pareme blockchain node
func main() {
	fmt.Println("Starting Pareme...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure canellation on exit
	var wg sync.WaitGroup

	// Initialize the printer for logging to .log file
	common.InitPrinter(ctx, &wg)

	// Start the networking goroutine
	network.NetworkManager(ctx, &wg)

	// Sync chain data from own files
	err := io.SyncChain()
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Sync failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	// Chain is valid; start the block writer goroutine
	newHeightsChan, err := io.BlockWriter(ctx, &wg)
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Blockwriter failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	// Connect to a peer
	//network.DialIPChan <- "192.168.86.98"
	//time.Sleep(2 * time.Second)

	network.FindPeers()

	// Sync chain data from peers
	err = network.SyncToPeers()
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Syncing chain from peers failed: %v", err))
		cancel()
		wg.Wait()
		return
	}

	// Start the miner manager with the current chain height
	consoleMineChan := mine.MinerManager(ctx, &wg, newHeightsChan)

	ui.RunUI(ctx, cancel, &wg, consoleMineChan)

}
