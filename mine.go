package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

func minerManager(ctx context.Context, wg *sync.WaitGroup, startHeight int, blockChan chan Block, newBlockChan chan int, requestChan chan readRequest) {
	currentHeight := startHeight // Receive latest height, start mining

	// Mining channels
	miningStartChan := make(chan Block)           // Passes block to start mining
	miningInterruptChan := make(chan struct{}, 1) // Trigger interrupt
	miningResultChan := make(chan Block)          // Passes back mined block

	// Starts mining...
	go mining(miningStartChan, miningInterruptChan, miningResultChan)

	var newHeights []int // slice for new block heights

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Start miner on the lastest initialized height
		nextBlock := buildBlockForMining(currentHeight, requestChan)
		miningStartChan <- nextBlock

		for {
			select {
			// Shutoff miner
			case <-ctx.Done():
				miningInterruptChan <- struct{}{}
				block := <-miningResultChan
				if block.Nonce == -1 {
					printToLog("Mining interrupted")
				} else { // rare but possible
					printToLog("Mining completed block before stopping")
				}
				printToLog("Miner shutting down")
				return
			// When miner finds a block, send it off to writer and broadcast
			case block := <-miningResultChan:
				hash := hashBlock(block)
				printToLog(fmt.Sprintf("Mined Block %d with Hash: %x", block.Height, hash[:8]))
				blockChan <- block
				broadcastBlock(block.Height)
			// New blocks appeared, keep track to give miner most recent
			case height := <-newBlockChan:
				newHeights = append(newHeights, height)
				maxHeight := max(newHeights)
				if maxHeight >= currentHeight+2 { // Scrap if 2+ blocks ahead
					miningInterruptChan <- struct{}{}
					<-miningResultChan // Wait for result and clear it
				}
				currentHeight = maxHeight
				nextBlock := buildBlockForMining(currentHeight, requestChan)
				miningStartChan <- nextBlock // Start new mining job
				newHeights = nil             // Wipe slice
			}
		}
	}()
}

func mining(startChan <-chan Block, interruptChan <-chan struct{}, resultChan chan<- Block) {
	for {
		block := <-startChan // Wait for a block to mine
		nonce := findNonce(block, interruptChan)

		block.Nonce = nonce
		resultChan <- block // Send mined block back

		// If interrupted, just loop back to wait for next task
	}

}

func findNonce(b Block, interruptChan <-chan struct{}) int {
	start := time.Now().Unix()
	printToLog(fmt.Sprintf("Finding Nonce for Block %d, Difficulty: %x", b.Height, b.Difficulty[:8]))

	for i := 1; i <= 1000000000; i++ {
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

func buildBlockForMining(height int, requestChan chan readRequest) Block {
	currentBlock := readBlock(height, requestChan)
	prevHash := hashBlock(currentBlock)
	nextBlock := newBlock(height+1, prevHash, currentBlock.Difficulty, currentBlock.BodyHash)
	if (height-1)%10 == 0 && height != 1 {
		nextBlock.Difficulty = adjustDifficulty(height, requestChan)
	}
	return nextBlock
}
