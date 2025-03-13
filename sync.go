package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

var requestChan chan readRequest

func syncChain(blockChan chan Block, newBlockChan chan int) int {
	printToLog("Syncing Pareme from peers...")

	height, totalBlocks := syncFiles() // Sync folder, dat, index file

	if height == -1 || height == 0 { // Sync failed
		return -1
	} else if height != 1 { // File existed, needs verification, skip genesis
		// Verify each block
		for i := 1; i <= getTotalBlocksFromFile("blocks/pareme0000.dat"); i++ {
			block := readBlockFromFile(i)
			if block.Height == 0 || !verifyBlock(block) {
				printToLog("Invalid block or failed block reading, resetting chain")
				resetChain()
			}
		}
	}

	// Chain is valid, start blockWriter and return height
	printToLog("Starting up blockWriter...")
	requestChan = make(chan readRequest)
	go blockWriter(blockChan, totalBlocks, newBlockChan, requestChan)
	printToLog(fmt.Sprintf("Synced chain at height %d\n", height))
	return height
}

func syncFiles() (int, int) {
	// Check for the blocks folder
	if err := os.MkdirAll("blocks", 0755); err != nil {
		printToLog(fmt.Sprintf("Error creating blocks folder: %v", err))
		return -1, -1
	}

	// Check for the .dat file
	filePath := "blocks/pareme0000.dat"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// File doesn't exist, create with magic bytes
		f, err := os.Create(filePath)
		if err != nil {
			printToLog(fmt.Sprintf("Error creating %s: %v", filePath, err))
			return -1, -1
		}
		defer f.Close()

		magic := []byte{0x50, 0x41, 0x52, 0x45} // 0xPAREM
		if _, err := f.Write(magic); err != nil {
			printToLog(fmt.Sprintf("Error writing magic bytes: %v", err))
		}
		writeBlock(genesisBlock())
		printToLog(fmt.Sprintf("Created %s with magic bytes + genesis", filePath))
	}

	// Check for the index file
	height, totalBlocks := syncIndex()

	if height == -1 || totalBlocks == -1 {
		return -1, -1
	}

	return height, totalBlocks

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
		return -1, -1
	}
	datBlocks := int((datInfo.Size() - 4) / 112)

	// Check index file
	f, err := os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file: %v", err))
		return -1, -1
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
			return -1, -1
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
	printToLog(fmt.Sprintf("Index synced: height = %d, blocks = %d", height, totalBlocks))
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

func resetChain() {
	// Reset dat file
	f, err := os.Create("blocks/pareme0000.dat")
	if err != nil {
		printToLog(fmt.Sprintf("Error resetting dat file: %v", err))
		return
	}
	defer f.Close()
	magic := []byte{0x50, 0x41, 0x52, 0x45}
	f.Write(magic)
	startBlock := genesisBlock()
	writeBlock(startBlock)

	// Reset index file
	idx, err := os.Create("blocks/pareme.idx")
	if err != nil {
		printToLog(fmt.Sprintf("Error resetting index file: %v", err))
		return
	}
	defer idx.Close()
	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf[0:4], 1)
	binary.BigEndian.PutUint32(buf[4:8], 1)
	copy(buf[8:18], []byte("pareme0000"))
	binary.BigEndian.PutUint16(buf[18:20], 1)
	if _, err := idx.Write(buf); err != nil {
		printToLog(fmt.Sprintf("Error writing reset index: %v", err))
	}

	printToLog("Chain reset to genesis block")
}

func getTotalBlocksFromFile(filepath string) int {
	// Calculate total blocks from file size
	f, err := os.Open(filepath)
	if err != nil {
		printToLog(fmt.Sprintf("Error opening dat file: %v", err))
		return -1
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		printToLog(fmt.Sprintf("Error stating dat file: %v", err))
		return -1
	}
	totalBlocks := int((fi.Size() - 4) / 112)
	return totalBlocks
}
