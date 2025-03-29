package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

var miningState struct {
	Active    bool
	isFinding bool
	Height    int
}

func minerManager(ctx context.Context, wg *sync.WaitGroup, newBlockChan chan []int) chan int {
	printToLog("\nInitializing Miner...")

	// Channels for mining coordination
	blockToMineChan := make(chan Block)           // Sends blocks to start mining
	interruptMiningChan := make(chan struct{}, 1) // Signals mining interruption
	stopMiningChan := make(chan int)              // Signals mining to stop
	minedBlockChan := make(chan Block)            // Revieves mined blocks
	consoleChan := make(chan int)                 // Connect with console

	// Start the mining goroutine
	go mining(blockToMineChan, minedBlockChan, interruptMiningChan, stopMiningChan)

	var newBlocks []int // Stores incoming block heights for chain updates

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				// Shutdown triggered by context cancellation
				if !miningState.Active {
					printToLog("Miner shutting down")
					stopMiningChan <- 1
					return
				}
				interruptMiningChan <- struct{}{}
				block := <-minedBlockChan
				if block.Nonce == -1 {
					printToLog("Mining interrupted")
				} else { // rare but possible
					printToLog("Mining completed block before stopping")
				}
				printToLog("Miner shutting down")
				return

			case consoleReq := <-consoleChan:
				if consoleReq == 1 {
					if miningState.Active {
						printToLog("Already Mining!")
					} else {
						// Start mining the next block based on the inital height
						currentHeight, _ := requestChainStats()
						nextBlock := buildBlockForMining(currentHeight)

						printToLog(fmt.Sprintf("\nStarting miner at Block %d", nextBlock.Height))
						blockToMineChan <- nextBlock

						miningState.Active = true
						miningState.Height = nextBlock.Height
					}
				} else {
					if !miningState.Active {
						printToLog("Already Not Mining!")
					} else {
						interruptMiningChan <- struct{}{}
						<-minedBlockChan

						miningState.Active = false
						miningState.Height = 0
					}
				}
			case block := <-minedBlockChan:
				// Successfully mined a block; send it to the writer and broadcast
				hash := hashBlock(block)
				printToLog(fmt.Sprintf("Mined Block %d with Hash: %x | ID: %x", block.Height, hash[:8], hash[30:]))
				blocks := []Block{block}
				blockChan <- blocks // Send to writer

				broadcastBlock(block)

			case blocks := <-newBlockChan:
				newBlocks = append(newBlocks, blocks...)
				maxHeight := max(newBlocks)
				printToLog(fmt.Sprintf("newBlocks: %v", newBlocks))

				if maxHeight > miningState.Height+2 {
					// Chain is 2+ blocks ahead; interrupt and discard current mining
					printToLog(fmt.Sprintf("Chain is ahead of miner. Chain: %d | Miner: %d. Scrapping...", maxHeight, miningState.Height))
					if miningState.isFinding {
						interruptMiningChan <- struct{}{}
						<-minedBlockChan
					}
				}
				if !miningState.isFinding {
					b := buildBlockForMining(maxHeight)
					printToLog(fmt.Sprintf("\n----- Starting mining on block %d at %v -----", b.Height, time.Now()))
					blockToMineChan <- b
					miningState.Height = maxHeight
					newBlocks = nil
				}
			}
		}
	}()
	return consoleChan
}

// mining runs a loop to process blocks for mining
func mining(blockToMineChan <-chan Block, minedBlockChan chan<- Block, interruptMiningChan <-chan struct{}, stopMiningChan <-chan int) {
	for {
		select {
		case block := <-blockToMineChan: // Wait for a block to mine
			miningState.isFinding = true
			nonce := findNonce(block, interruptMiningChan)
			miningState.isFinding = false
			block.Nonce = nonce
			minedBlockChan <- block // Send mined block back
		case <-stopMiningChan:
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
	var currentBlock Block
	if height == 1 {
		currentBlock = genesisBlock()
	} else {
		currentBlock = requestBlocks([]int{height})[0][0]
	}
	prevHash := hashBlock(currentBlock)
	nextBlock := newBlock(height+1, prevHash, currentBlock.Difficulty, currentBlock.BodyHash)
	if (height-1)%10 == 0 && height != 1 {
		// Adjust difficulty every 10 blocks (except genesis)
		nextBlock.Difficulty = adjustDifficulty(height)
	}
	return nextBlock
}
