package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

var miningState struct {
	Active    bool
	isFinding bool
	Height    int
}

func minerManager(ctx context.Context, wg *sync.WaitGroup, newHeightsChan chan []int) chan string {
	printToLog("\nInitializing Miner...")

	// Channels for mining coordination
	blockToMineChan := make(chan Block)           // Sends blocks to start mining
	interruptMiningChan := make(chan struct{}, 1) // Signals mining interruption
	stopMiningChan := make(chan int)              // Signals mining to stop
	minedBlockChan := make(chan Block)            // Revieves mined blocks
	consoleChan := make(chan string)              // Connect with console

	var hashToMine string

	// Start the mining goroutine
	go mining(blockToMineChan, minedBlockChan, interruptMiningChan, stopMiningChan)

	var newHeights []int // Stores incoming block heights for chain updates

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				// Shutdown triggered by context cancellation
				if miningState.isFinding {
					interruptMiningChan <- struct{}{}
					block := <-minedBlockChan
					if block.Nonce == -1 {
						printToLog("Mining interrupted")
					} else { // rare but possible
						printToLog("Mining completed block before stopping")
					}
					printToLog("Miner shutting down")
					return
				} else {
					printToLog("Miner shutting down")
					stopMiningChan <- 1
					return
				}

			case consoleReq := <-consoleChan:
				if consoleReq != "" {
					if miningState.Active {
						printToLog("Already Mining!")
					} else {
						hashToMine = consoleReq
						// Start mining the next block based on the inital height
						currentHeight, _ := requestChainStats()
						nextBlock, err := buildBlockForMining(currentHeight, hashToMine)
						if err != nil {
							printToLog(fmt.Sprintf("Failed to build next block: %v", err))
							continue
						}

						printToLog(fmt.Sprintf("\nStarting miner at Block %d", nextBlock.Height))
						miningState.isFinding = true
						blockToMineChan <- nextBlock

						miningState.Active = true
						miningState.Height = nextBlock.Height
					}
				} else {
					if !miningState.Active {
						printToLog("Already Not Mining!")
					} else {
						if miningState.isFinding {
							interruptMiningChan <- struct{}{}
							<-minedBlockChan

							miningState.Active = false
							miningState.Height = 0
						} else {
							miningState.Active = false
							miningState.Height = 0
						}
					}
				}
			case block := <-minedBlockChan:
				// Successfully mined a block; send it to the writer and broadcast
				hash := hashBlock(block)
				printToLog(fmt.Sprintf("Mined Block %d with Hash: %x | ID: %x", block.Height, hash[:8], hash[30:]))
				blocks := []Block{block}
				writeBlockReq := writeBlockRequest{
					Blocks: blocks,
					Type:   "mined",
					From:   nil,
				}
				writeBlockChan <- writeBlockReq // Send to writer

			case heights := <-newHeightsChan:
				newHeights = append(newHeights, heights...)
				maxHeight := max(newHeights)
				printToLog(fmt.Sprintf("newHeights: %v", newHeights))
				if !miningState.Active {
					continue
				}

				if maxHeight > miningState.Height+2 {
					// Chain is 2+ blocks ahead; interrupt and discard current mining
					printToLog(fmt.Sprintf("Chain is ahead of miner. Chain: %d | Miner: %d. Scrapping...", maxHeight, miningState.Height))
					if miningState.isFinding {
						interruptMiningChan <- struct{}{}
						<-minedBlockChan
						miningState.isFinding = false
					}
				}
				if !miningState.isFinding {
					b, err := buildBlockForMining(maxHeight, hashToMine)
					if err != nil {
						printToLog(fmt.Sprintf("Failed to build next block: %v", err))
						continue
					}
					printToLog(fmt.Sprintf("\n----- Starting mining on block %d at %v -----", b.Height, time.Now()))
					miningState.isFinding = true
					blockToMineChan <- b
					miningState.Height = maxHeight
					newHeights = nil
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
			nonce := findNonce(block, interruptMiningChan)
			block.Nonce = nonce
			minedBlockChan <- block // Send mined block back
			miningState.isFinding = false
		case <-stopMiningChan:
			return
		}
	}

}

// findNonce searches for a nonce that satifies the block's difficulty
func findNonce(b Block, interruptChan <-chan struct{}) int {
	start := time.Now().Unix()
	blockDifficulty := nBitsToTarget(b.NBits)
	printToLog(fmt.Sprintf("Finding Nonce for Block %d, Difficulty: %x", b.Height, blockDifficulty[:8]))

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
		if bytes.Compare(hash[:], blockDifficulty[:]) < 0 {
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
func buildBlockForMining(height int, hashToMine string) (Block, error) {
	var currentBlock Block
	if height == 1 {
		currentBlock = genesisBlock()
	} else {
		currentBlock = requestBlocks([]int{height})[0][0]
	}
	prevHash := hashBlock(currentBlock)

	hashToMineBytes, err := hashStringToByte32(hashToMine)

	if err != nil {
		return Block{}, fmt.Errorf("failed to convert hash to bytes: %v", err)
	}

	nextBlock := newBlock(height+1, prevHash, currentBlock.NBits, hashToMineBytes)
	if (height)%2016 == 0 && height != 2016 { // Skip first ever adjustment
		// Adjust difficulty every 2016 blocks (except genesis)
		//nextBlock.Difficulty = adjustDifficulty(nextBlock)
		_, nBits, err := calculateDifficulty(nextBlock)
		if err != nil {
			return Block{}, fmt.Errorf("failed to determine difficulty: %v", err)
		}
		nextBlock.NBits = nBits
	}
	return nextBlock, nil
}

func hashStringToByte32(hashStr string) ([32]byte, error) {
	// Remove any "0x" prefix if present
	hashStr = strings.TrimPrefix(hashStr, "0x")

	// Decode the hex string into a byte slice
	hashBytes, err := hex.DecodeString(hashStr)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to decode hex string: %v", err)
	}

	// Check if the length is exactly 32 bytes
	if len(hashBytes) != 32 {
		return [32]byte{}, fmt.Errorf("hash length must be 32 bytes, got %d", len(hashBytes))
	}

	// Convert slice to fixed-size array
	var result [32]byte
	copy(result[:], hashBytes)
	return result, nil
}
