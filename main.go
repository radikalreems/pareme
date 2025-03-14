package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

var blockChan chan Block
var newBlockChan chan int

func main() {
	fmt.Println("Starting Pareme...")
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Initializes and starts printer who manages prints to .log
	initPrinter(ctx, &wg)

	// Writer Channels
	blockChan = make(chan Block, 100) // Blocks to be verified and written
	newBlockChan = make(chan int, 10) // Newly written blocks to sync miner

	// Syncs dat & index file and retrieves latest height
	// Starts goroutine that verifies, writes, and indexes new blocks
	height, requestChan := syncChain(ctx, &wg, blockChan, newBlockChan)
	if height == -1 {
		fmt.Println("Sync failed, exiting...")
		cancel()
		wg.Wait()
		return
	}

	printToLog("Initializing Miner...")

	minerManager(ctx, &wg, height, blockChan, newBlockChan, requestChan)

	printToLog(fmt.Sprintf("Starting miner at Block %d", height))

	// Console loop watching for commands ('stop')
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
			cancel()
			wg.Wait()
			fmt.Println("Stopping Pareme...")
			return
		default:
			fmt.Println("Unknown command. Try 'stop'")
		}
	}
}
