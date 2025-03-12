package main

import (
	"bytes"
	"fmt"
	"time"
)

func minerManager(startChan chan int, stopChan chan struct{}, doneChan chan struct{}, blockChan chan Block, newBlockChan chan int) {
	currentHeight := <-startChan // Receive latest height, start mining

	// Mining channels
	miningStartChan := make(chan Block)           // Passes block to start mining
	miningInterruptChan := make(chan struct{}, 1) // Trigger interrupt
	miningResultChan := make(chan Block)          // Passes back mined block

	// Starts mining...
	go mining(miningStartChan, miningInterruptChan, miningResultChan)

	var newHeights []int // slice for new block heights

	// Start on first block
	nextBlock := buildBlockForMining(currentHeight)
	miningStartChan <- nextBlock

	for {
		select {
		// If stop trigger recieved, close out miner
		case <-stopChan:
			printToLog("Sending interrupt command to miner...")
			miningInterruptChan <- struct{}{} // Buffered
			block := <-miningResultChan       // Wait for finished miner
			if block.Nonce == -1 {
				printToLog("Mining interrupted and stopped")
			} else { // rare but possible
				printToLog("Mining completed block before stopping")
			}
			doneChan <- struct{}{} // Inform main that mining has closed out
			return
		// Listen for new blocks being written to give latest to miner when ready
		case height := <-newBlockChan:
			newHeights = append(newHeights, height)
			maxHeight := max(newHeights)
			if maxHeight >= currentHeight+2 { // Scrap if 2+ blocks ahead
				miningInterruptChan <- struct{}{}
				<-miningResultChan // Wait for result and clear it
			}
			currentHeight = maxHeight
			nextBlock := buildBlockForMining(currentHeight)
			miningStartChan <- nextBlock // Start new mining job
			newHeights = nil             // Wipe slice
		// When miner finds a block, send it off to writer and broadcast
		case block := <-miningResultChan:
			hash := hashBlock(block)
			printToLog(fmt.Sprintf("Mined Block %d with Hash: %x", block.Height, hash[:8]))
			blockChan <- block
			broadcastBlock(block.Height)
		}
	}
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

func buildBlockForMining(height int) Block {
	currentBlock := readBlock(height)
	prevHash := hashBlock(currentBlock)
	nextBlock := newBlock(height+1, prevHash, currentBlock.Difficulty, currentBlock.BodyHash)
	printToLog(fmt.Sprintf("Building Block %d with timestamp %d", nextBlock.Height, nextBlock.Timestamp))
	if (height-1)%10 == 0 && height != 1 {
		nextBlock.Difficulty = adjustDifficulty(height)
	}
	return nextBlock
}
