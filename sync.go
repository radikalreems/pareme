package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

// syncChain synchronizes the blockchain from files and starts the block writer
func syncChain(ctx context.Context, wg *sync.WaitGroup, blockChan chan Block, newBlockChan chan Block) (int, chan readRequest) {
	printToLog("Syncing Pareme from peers...")

	// Sync blockchain files and retrieve current height and total blocks
	height, totalBlocks := syncFiles() // Sync folder, dat, index file
	if height == -1 || height == 0 {   // Sync failed
		return -1, nil
	}

	// Verify existing chain if not starting from genesis
	if height != 1 {
		f, err := os.Open("blocks/pareme0000.dat")
		if err != nil {
			printToLog(fmt.Sprintf("Error opening dat file in read: %v", err))
			return -1, nil
		}
		defer f.Close()

		// Verify each block in the chain
		for i := 1; i <= getTotalBlocksFromFile("blocks/pareme0000.dat"); i++ {
			block := readBlockFromFile(f, i)
			if block.Height == 0 || !verifyBlock(block) {
				printToLog("Invalid block or failed block reading, resetting chain")
				resetChain()
			}
		}
	}

	// Chain is valid; start the block writer goroutine
	printToLog("Starting up blockWriter...")
	requestChan := make(chan readRequest)
	blockWriter(ctx, wg, blockChan, totalBlocks, newBlockChan, requestChan)

	printToLog(fmt.Sprintf("Synced chain at height %d\n", height))
	return height, requestChan
}

// syncFiles initializes or synchronizes blockchain data files (.dat and .idx)
func syncFiles() (int, int) {
	// Ensure blocks directory exists
	if err := os.MkdirAll("blocks", 0755); err != nil {
		printToLog(fmt.Sprintf("Error creating blocks folder: %v", err))
		return -1, -1
	}

	// Check and initialize .dat file if it doesn't exist
	filePath := "blocks/pareme0000.dat"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		f, err := os.Create(filePath)
		if err != nil {
			printToLog(fmt.Sprintf("Error creating %s: %v", filePath, err))
			return -1, -1
		}
		defer f.Close()

		magic := []byte{0x50, 0x41, 0x52, 0x45} // PARE magic bytes
		if _, err := f.Write(magic); err != nil {
			printToLog(fmt.Sprintf("Error writing magic bytes to %s: %v", filePath, err))
			return -1, -1
		}
		writeBlock(f, genesisBlock())
		printToLog(fmt.Sprintf("Created %s with magic bytes and genesis", filePath))
	}

	// Sync the index file with the .dat file
	height, totalBlocks := syncIndex()
	if height == -1 || totalBlocks == -1 {
		return -1, -1
	}

	return height, totalBlocks

}

// syncIndex synchronizes the index file with the .dat file
func syncIndex() (int, int) {
	idxPath := "blocks/pareme.idx"
	datPath := "blocks/pareme0000.dat"
	height := 0
	totalBlocks := 0

	// Open .dat file for reading
	fd, err := os.Open("blocks/pareme0000.dat")
	if err != nil {
		printToLog(fmt.Sprintf("Error opening dat file in read: %v", err))
		return -1, -1
	}
	defer fd.Close()

	// Open or create index file
	f, err := os.OpenFile(idxPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		printToLog(fmt.Sprintf("Failed to open index file %s: %v", idxPath, err))
		return -1, -1
	}
	defer f.Close()

	// Calculate total blocks in .dat file (size - magic) / block size
	datInfo, err := os.Stat(datPath)
	if err != nil {
		printToLog(fmt.Sprintf("Error stating dat file %s: %v", datPath, err))
		return -1, -1
	}
	datBlocks := int((datInfo.Size() - 4) / 112) // 112 bytes per block

	// Check and sync index file
	fi, _ := f.Stat()
	if fi.Size() == 0 {
		printToLog(fmt.Sprintf("No existing index, found %d blocks in dat. resyncing...", datBlocks))
		if datBlocks > 0 {
			lastBlock := readBlockFromFile(fd, datBlocks)
			height = lastBlock.Height
			totalBlocks = datBlocks
		}
		writeIndex(height, totalBlocks)
	} else {
		// Read existing index data
		buf := make([]byte, 8)
		if _, err := f.ReadAt(buf, 0); err != nil {
			printToLog(fmt.Sprintf("Failed to read index file %s: %v", idxPath, err))
			return -1, -1
		}
		height = int(binary.BigEndian.Uint32(buf[0:4]))
		totalBlocks = int(binary.BigEndian.Uint32(buf[4:8]))
		printToLog(fmt.Sprintf("Found existing index, height = %d, totalBlocks = %d", height, totalBlocks))

		// Resync if index doesn't match .dat file
		if totalBlocks != datBlocks {
			printToLog("Index mismatch, resyncing...")
			height = readBlockFromFile(fd, datBlocks).Height
			totalBlocks = datBlocks
			writeIndex(height, totalBlocks)
		}
	}
	printToLog(fmt.Sprintf("Index synced: height = %d, blocks = %d", height, totalBlocks))
	return height, totalBlocks
}

// writeIndex writes height and total block count to the index file
func writeIndex(height, totalBlocks int) {
	f, err := os.Create("blocks/pareme.idx")
	if err != nil {
		printToLog(fmt.Sprintf("Failed to create index file: %v", err))
		return
	}
	defer f.Close()

	// Write index: height (4), totalBlocks(4), filename(10), checksum(2)
	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf[0:4], uint32(height))
	binary.BigEndian.PutUint32(buf[4:8], uint32(totalBlocks))
	copy(buf[8:18], []byte("pareme0000"))
	binary.BigEndian.PutUint16(buf[18:20], uint16(totalBlocks))
	if _, err := f.Write(buf); err != nil {
		printToLog(fmt.Sprintf("Failed to write index: %v", err))
	}
}

// resetChain resets the blockchain to the genesis block
func resetChain() {
	// Reset .dat file to magic bytes and genesis block
	f, err := os.Create("blocks/pareme0000.dat")
	if err != nil {
		printToLog(fmt.Sprintf("Error resetting dat file: %v", err))
		return
	}
	defer f.Close()
	magic := []byte{0x50, 0x41, 0x52, 0x45}
	if _, err := f.Write(magic); err != nil {
		printToLog(fmt.Sprintf("Error writing magic bytes during reset: %v", err))
		return
	}

	startBlock := genesisBlock()
	writeBlock(f, startBlock)

	// Reset index file to reflect genesis state
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
		return
	}

	printToLog("Chain reset to genesis block")
}

// getTotalBlocksFromFile calculates the number of blocks in the .dat file
func getTotalBlocksFromFile(filepath string) int {
	f, err := os.Open(filepath)
	if err != nil {
		printToLog(fmt.Sprintf("Error opening dat file %s: %v", filepath, err))
		return -1
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		printToLog(fmt.Sprintf("Error stating dat file %s: %v", filepath, err))
		return -1
	}

	return int((fi.Size() - 4) / 112) // Subtract magic bytes, divide by block size
}
