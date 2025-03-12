package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

var blockChan chan Block
var newBlockChan chan int

func main() {
	fmt.Println("Starting Pareme...")

	// Starts goroutine that prints to .log
	initPrinter()

	// Writer Channels
	blockChan = make(chan Block, 100) // Blocks to be verified and written
	newBlockChan = make(chan int, 10) // Newly written blocks to sync miner

	// Syncs dat & index file and retrieves latest height
	// Starts goroutine that verifies, writes, and indexes new blocks
	height := syncChain(blockChan, newBlockChan)

	// Miner Channels
	startChan := make(chan int)     // Triggers miner to start with latest height
	stopChan := make(chan struct{}) // Triggers miner to stop
	doneChan := make(chan struct{}) // Verifies miner stopped

	// Starts miner manager
	// Starts goroutine that mines for nonce
	printToLog("Initializing Miner...")
	go minerManager(startChan, stopChan, doneChan, blockChan, newBlockChan)
	printToLog(fmt.Sprintf("Starting miner at Block %d", height))
	startChan <- height

	// Console loop watching for commands ('stop')
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Pareme> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "stop":
			printToLog("Recieved 'stop' command")
			stopChan <- struct{}{} // Send stop to miner
			<-doneChan             // Wait for miner stop confirmation
			fmt.Println("Stopping Pareme...")
			time.Sleep(1 * time.Second) // Give a sec for prints to clear
			return
		default:
			fmt.Println("Unknown command. Try 'stop'")
		}
	}
}
