package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	//"path/filepath"
)

type readRequest struct {
	Height   int
	Response chan Block
}

func blockWriter(blockChan <-chan Block, totalBlocks int, newBlockChan chan int, requestChan <-chan readRequest) {

	for {
		select {
		case block := <-blockChan:
			if !verifyBlock(block) {
				printToLog(fmt.Sprintf("Invalid block at height %d", block.Height))
				continue
			}
			if writeBlock(block) == -1 {
				printToLog(fmt.Sprintf("failed to write block %d", block.Height))
				continue
			}
			updateIndex(block.Height, totalBlocks+1)
			newBlockChan <- block.Height // Notify miners
			totalBlocks++
		case req := <-requestChan:
			block := readBlockFromFile(req.Height)
			req.Response <- block
		}
	}
}

func syncIndex() (int, int) {
	idxPath := "blocks/pareme.idx"
	datPath := "blocks/pareme0000.dat"
	height := 0
	totalBlocks := 0

	// Determine how many blocks are in .dat
	datInfo, err := os.Stat(datPath)
	if err != nil {
		printToLog("No .dat file found during index sync")
		return 0, 0
	}
	datBlocks := int((datInfo.Size() - 4) / 112)

	// Check index file
	f, err := os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file: %v", err))
		return 0, 0
	}
	defer f.Close()

	// Get index file info
	fi, _ := f.Stat()
	if fi.Size() == 0 {
		// No index, create from .dat
		printToLog(fmt.Sprintf("No existing index, %d blocks in dat. resyncing...", datBlocks))
		if datBlocks > 0 {
			lastBlock := readBlockFromFile(datBlocks)
			height = lastBlock.Height
			totalBlocks = datBlocks
		}
		writeIndex(height, totalBlocks) // Initial full write
	} else {
		// Read existing index
		buf := make([]byte, 8)
		if _, err := f.ReadAt(buf, 0); err != nil {
			printToLog(fmt.Sprintf("Failed to read index: %v", err))
			return 0, 0
		}
		height = int(binary.BigEndian.Uint32(buf[0:4]))
		totalBlocks = int(binary.BigEndian.Uint32(buf[4:8]))
		printToLog(fmt.Sprintf("Found existing index, height = %d, totalBlocks = %d", height, totalBlocks))

		if totalBlocks != datBlocks {
			printToLog("Index mismatch, resyncing...")
			height = readBlockFromFile(datBlocks).Height
			totalBlocks = datBlocks
			writeIndex(height, totalBlocks) // resync full write
		}
	}
	printToLog(fmt.Sprintf("Index synced: height=%d, blocks=%d", height, totalBlocks))
	return height, totalBlocks
}

func writeIndex(height, totalBlocks int) {
	f, err := os.Create("blocks/pareme.idx")
	if err != nil {
		printToLog(fmt.Sprintf("Failed to create index file: %v", err))
		return
	}
	defer f.Close()

	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf[0:4], uint32(height))
	binary.BigEndian.PutUint32(buf[4:8], uint32(totalBlocks))
	copy(buf[8:18], []byte("pareme0000"))
	binary.BigEndian.PutUint16(buf[18:20], uint16(totalBlocks))
	if _, err := f.Write(buf); err != nil {
		printToLog(fmt.Sprintf("Failed to write index: %v", err))
	}
}

/*
func readIndex() (int, int) {
	f, err := os.Open("blocks/pareme.idx")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var height, totalBlocks int32
	binary.Read(f, binary.BigEndian, &height)
	binary.Read(f, binary.BigEndian, &totalBlocks)
	// Skip "pareme0000:count" for now, assume one file
	return int(height), int(totalBlocks)
}
*/

func updateIndex(height, totalBlocks int) {
	//writeIndex(height, totalBlocks)
	f, err := os.OpenFile("blocks/pareme.idx", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file: %v", err))
		return
	}
	defer f.Close()

	// Buffer for height and total blocks (8 bytes)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], uint32(height))
	binary.BigEndian.PutUint32(buf[4:8], uint32(totalBlocks))

	// Write at offset 0 (start of file)
	if _, err := f.WriteAt(buf, 0); err != nil {
		printToLog(fmt.Sprintf("Failed to update index: %v", err))
	}
}

func readBlockFromFile(height int) Block {
	f, err := os.Open("blocks/pareme0000.dat")
	if err != nil {
		printToLog(fmt.Sprintf("Error opening dat file in read: %v", err))
		return Block{}
	}
	defer f.Close()

	if _, err := f.Seek(4, 0); err != nil {
		printToLog(fmt.Sprintf("Error seeking past magic: %v", err))
		return Block{}
	}

	buf := make([]byte, 112)

	for {
		n, err := f.Read(buf)
		if err != nil || n != 112 {
			if err == io.EOF {
				break
			}
			printToLog(fmt.Sprintf("Error reading block: %v", err))
			return Block{}
		}

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
