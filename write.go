package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	//"path/filepath"
)

// readRequest represents a request to read a block by height
type readRequest struct {
	Height   int
	Response chan Block
}

// blockWriter processes incoming blocks and read requests, updating the blockchain files
func blockWriter(ctx context.Context, wg *sync.WaitGroup, blockChan <-chan Block, totalBlocks int, newBlockChan chan Block, requestChan <-chan readRequest) {
	// Open .dat file for reading and appending
	f, err := os.OpenFile("blocks/pareme0000.dat", os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open dat file: %v", err))
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer f.Close()

		currentBlocks := totalBlocks // Track total blocks in the chain

		for {
			select {
			case block := <-blockChan:
				// Verify and write new block
				if !verifyBlock(block) {
					printToLog(fmt.Sprintf("Invalid block at height %d", block.Height))
					continue
				}
				if writeBlock(f, block) == -1 {
					printToLog(fmt.Sprintf("failed to write block %d", block.Height))
					continue
				}
				currentBlocks++
				updateIndex(block.Height, currentBlocks)
				newBlockChan <- block // Notify miners of new block

			case req := <-requestChan:
				// Handle block read request
				block := readBlockFromFile(f, req.Height)
				req.Response <- block

			case respChan := <-indexRequestChan:
				// Handle index read request
				height, totalBlocks := readIndexFromFile()
				respChan <- [2]uint32{uint32(height), uint32(totalBlocks)}

			case <-ctx.Done():
				printToLog("Block writer shutting down")
				return
			}
		}
	}()
}

// updateIndex updates the index file with the latest height and total block count
func updateIndex(height, totalBlocks int) {
	f, err := os.OpenFile("blocks/pareme.idx", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file: %v", err))
		return
	}
	defer f.Close()

	// Write height and totalBlocks (8 bytes) at the start of the file
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], uint32(height))
	binary.BigEndian.PutUint32(buf[4:8], uint32(totalBlocks))
	if _, err := f.WriteAt(buf, 0); err != nil {
		printToLog(fmt.Sprintf("Failed to update index: %v", err))
	}
}

// readBlockFromFile reads a block from the .dat file by height
func readBlockFromFile(f *os.File, height int) Block {
	// Seek past magic bytes (4 bytes)
	if _, err := f.Seek(4, 0); err != nil {
		printToLog(fmt.Sprintf("Error seeking past magic bytes: %v", err))
		return Block{}
	}

	buf := make([]byte, 112) // Fixed block size
	for {
		n, err := f.Read(buf)
		if err != nil || n != 112 {
			if err == io.EOF {
				break
			}
			printToLog(fmt.Sprintf("Error reading block at height %d: %v", height, err))
			return Block{}
		}

		// Parse block height from first 4 bytes
		blockHeight := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		if blockHeight == height {
			return Block{
				Height: blockHeight,
				Timestamp: int64(buf[4])<<56 | int64(buf[5])<<48 | int64(buf[6])<<40 | int64(buf[7])<<32 |
					int64(buf[8])<<24 | int64(buf[9])<<16 | int64(buf[10])<<8 | int64(buf[11]),
				PrevHash:   *(*[32]byte)(buf[12:44]),
				Nonce:      int(buf[44])<<24 | int(buf[45])<<16 | int(buf[46])<<8 | int(buf[47]),
				Difficulty: *(*[32]byte)(buf[48:80]),
				BodyHash:   *(*[32]byte)(buf[80:112]),
			}
		}

	}
	printToLog(fmt.Sprintf("No block found for height %d", height))
	return Block{}
}

// readBlock requests a block from the writer by height
func readBlock(height int, requestChan chan readRequest) Block {
	responseChan := make(chan Block)
	requestChan <- readRequest{Height: height, Response: responseChan}
	return <-responseChan
}

// writeBlock appends a block to the .dat file
func writeBlock(f *os.File, b Block) int {
	// Serialize block to 112-byte array
	data := make([]byte, 0, 112)

	data = append(data,
		byte(b.Height>>24), byte(b.Height>>16), byte(b.Height>>8), byte(b.Height),
		byte(b.Timestamp>>56), byte(b.Timestamp>>48), byte(b.Timestamp>>40), byte(b.Timestamp>>32),
		byte(b.Timestamp>>24), byte(b.Timestamp>>16), byte(b.Timestamp>>8), byte(b.Timestamp))

	//data = append(data, byte(b.Height>>24), byte(b.Height>>16), byte(b.Height>>8), byte(b.Height))
	//data = append(data, byte(b.Timestamp>>56), byte(b.Timestamp>>48), byte(b.Timestamp>>40), byte(b.Timestamp>>32),
	//	byte(b.Timestamp>>24), byte(b.Timestamp>>16), byte(b.Timestamp>>8), byte(b.Timestamp)) // 8 bytes
	data = append(data, b.PrevHash[:]...)                                                      // 32 bytes
	data = append(data, byte(b.Nonce>>24), byte(b.Nonce>>16), byte(b.Nonce>>8), byte(b.Nonce)) // 4 bytes
	data = append(data, b.Difficulty[:]...)                                                    // 32 bytes
	data = append(data, b.BodyHash[:]...)

	// Write serialized data to file
	if _, err := f.Write(data); err != nil {
		printToLog(fmt.Sprintf("Error writing block %d: %v", b.Height, err))
		return -1
	}
	printToLog(fmt.Sprintf("Wrote Block %d to file. Difficulty: %x\n", b.Height, b.Difficulty[:8]))
	return 1
}

// readIndexFromFile reads the height and total block count directly from the index file
func readIndexFromFile() (int, int) {
	f, err := os.Open("blocks/pareme.idx")
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file for reading: %v", err))
		return 0, 0
	}
	defer f.Close()

	// Check file size to ensure at least 8 bytes are present
	fi, err := f.Stat()
	if err != nil {
		printToLog(fmt.Sprintf("Failed to stat index file: %v", err))
		return 0, 0
	}
	if fi.Size() < 8 {
		printToLog("Index file too shore, expected at least 8 bytes")
		return 0, 0
	}

	// Read first 8 bytes: height(8) and totalBlocks(4)
	buf := make([]byte, 8)
	if _, err := f.Read(buf); err != nil {
		printToLog(fmt.Sprintf("Failed to read index file: %v", err))
		return 0, 0
	}

	height := binary.BigEndian.Uint32(buf[0:4])
	totalBlocks := binary.BigEndian.Uint32(buf[4:8])
	// Skip "pareme0000:count" for now, assume one file
	return int(height), int(totalBlocks)
}

func readIndex() (int, int) {
	responseChan := make(chan [2]uint32)
	indexRequestChan <- responseChan
	result := <-responseChan
	return int(result[0]), int(result[1])
}

func getLatestBlock(requestChan chan readRequest) Block {
	// Get the latest height from the index
	height, _ := readIndex() // Ignore totalBlocks, we only need height
	if height == 0 {
		printToLog("No blocks available, returning empty block")
		return Block{}
	}

	// Request the block at the latest height from the writer
	return readBlock(height, requestChan)
}
