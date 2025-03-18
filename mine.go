package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

func minerManager(ctx context.Context, wg *sync.WaitGroup, newBlockChan chan Block) chan int {
	printToLog("Initializing Miner...")

	// Channels for mining coordination
	miningStartChan := make(chan Block)           // Sends blocks to start mining
	miningInterruptChan := make(chan struct{}, 1) // Signals mining interruption
	miningStopChan := make(chan int)              // Signals mining to stop
	miningResultChan := make(chan Block)          // Revieves mined blocks
	consoleMineChan := make(chan int)             // Connect with console

	// Start the mining goroutine
	go mining(miningStartChan, miningInterruptChan, miningResultChan, miningStopChan)

	var newHeights []int // Stores incoming block heights for chain updates

	wg.Add(1)
	go func() {
		defer wg.Done()

		isMining := false

		for {
			select {
			case <-ctx.Done():
				// Shutdown triggered by context cancellation
				if !isMining {
					printToLog("Miner shutting down")
					miningStopChan <- 1
					return
				}
				miningInterruptChan <- struct{}{}
				block := <-miningResultChan
				if block.Nonce == -1 {
					printToLog("Mining interrupted")
				} else { // rare but possible
					printToLog("Mining completed block before stopping")
				}
				printToLog("Miner shutting down")
				return

			case consoleReq := <-consoleMineChan:
				if consoleReq == 1 {
					if isMining {
						printToLog("Already Mining!")
					} else {
						// Start mining the next block based on the inital height
						currentHeight := getLatestBlock().Height
						nextBlock := buildBlockForMining(currentHeight)

						printToLog(fmt.Sprintf("Starting miner at Block %d", currentHeight))
						miningStartChan <- nextBlock

						isMining = true
					}
				} else {
					if !isMining {
						printToLog("Already Not Mining!")
					} else {
						miningInterruptChan <- struct{}{}
						<-miningResultChan

						isMining = false
					}
				}
			case block := <-miningResultChan:
				// Successfully mined a block; send it to the writer and broadcast
				hash := hashBlock(block)
				printToLog(fmt.Sprintf("Mined Block %d with Hash: %x", block.Height, hash[:8]))
				blockChan <- block
				broadcastBlock(block.Height)

			case block := <-newBlockChan:
				// Handle new block heights from the chain
				currentHeight := getLatestBlock().Height

				newHeights = append(newHeights, block.Height)
				maxHeight := max(newHeights)
				if maxHeight >= currentHeight+2 {
					// Chain is 2+ blocks ahead; interrupt and discard current mining
					miningInterruptChan <- struct{}{}
					<-miningResultChan // Discard interrupted result
				}
				currentHeight = maxHeight
				nextBlock := buildBlockForMining(currentHeight)
				miningStartChan <- nextBlock
				newHeights = nil // Reset height tracking
			}
		}
	}()
	return consoleMineChan
}

// mining runs a loop to process blocks for mining
func mining(startChan <-chan Block, interruptChan <-chan struct{}, resultChan chan<- Block, miningStopChan <-chan int) {
	for {
		select {
		case block := <-startChan: // Wait for a block to mine
			nonce := findNonce(block, interruptChan)
			block.Nonce = nonce
			resultChan <- block // Send mined block back
		case <-miningStopChan:
			return
		}
	}

}

// findNonce searches for a nonce that satifies the block's difficulty
func findNonce(b Block, interruptChan <-chan struct{}) int {
	start := time.Now().Unix()
	printToLog(fmt.Sprintf("Finding Nonce for Block %d, Difficulty: %x", b.Height, b.Difficulty[:8]))

	for i := 1; i <= 1_000_000_000; i++ {
		if i%1000 == 0 {
			select {
			case <-interruptChan:
				printToLog(fmt.Sprintf("Nonce search interrupted for Block %d", b.Height))
				return -1
			default:
			}
		}
		b.Nonce = i
		hash := hashBlock(b)
		if bytes.Compare(hash[:], b.Difficulty[:]) < 0 {
			printToLog(fmt.Sprintf("Nonce %d found in %d seconds", b.Nonce, time.Now().Unix()-start))
			return b.Nonce
		}
	}
	printToLog(fmt.Sprintf("No nonce found in %d seconds", time.Now().Unix()-start))
	return -1
}

// max returns the maximum value in a slice of integers
func max(heights []int) int {
	if len(heights) == 0 {
		return 0
	}
	maxH := heights[0]
	for _, h := range heights {
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

// buildBlockForMining constructs a new block for mining based on the current height
func buildBlockForMining(height int) Block {
	currentBlock := readBlock(height)[0].(Block)
	prevHash := hashBlock(currentBlock)
	nextBlock := newBlock(height+1, prevHash, currentBlock.Difficulty, currentBlock.BodyHash)
	if (height-1)%10 == 0 && height != 1 {
		// Adjust difficulty every 10 blocks (except genesis)
		nextBlock.Difficulty = adjustDifficulty(height)
	}
	return nextBlock
}
