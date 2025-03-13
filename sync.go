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
		writeBlock(initBlock(1))
		printToLog(fmt.Sprintf("Created %s with magic bytes + genesis", filePath))
	}

	// Check for the index file
	height, totalBlocks := syncIndex()
	printToLog(fmt.Sprintf("Index synced: height = %d, totalBlocks = %d", height, totalBlocks))

	if height == 0 || totalBlocks == 0 {
		return -1, -1
	}

	return height, totalBlocks

}

func writeBlock(b Block) int {
	f, err := os.OpenFile("blocks/pareme0000.dat", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return -1
	}
	defer f.Close()

	// Write Block Data (112 bytes)
	data := make([]byte, 0, 112)
	data = append(data, byte(b.Height>>24), byte(b.Height>>16), byte(b.Height>>8), byte(b.Height))
	data = append(data, byte(b.Timestamp>>56), byte(b.Timestamp>>48), byte(b.Timestamp>>40), byte(b.Timestamp>>32),
		byte(b.Timestamp>>24), byte(b.Timestamp>>16), byte(b.Timestamp>>8), byte(b.Timestamp)) // 8 bytes
	data = append(data, b.PrevHash[:]...)                                                      // 32 bytes
	data = append(data, byte(b.Nonce>>24), byte(b.Nonce>>16), byte(b.Nonce>>8), byte(b.Nonce)) // 4 bytes
	data = append(data, b.Difficulty[:]...)                                                    // 32 bytes
	data = append(data, b.BodyHash[:]...)

	if _, err := f.Write(data); err != nil {
		return -1
	}
	printToLog(fmt.Sprintf("Wrote Block %d to file. Difficulty: %x", b.Height, b.Difficulty[:8]))
	return 1
}

func readBlock(height int) Block {

	responseChan := make(chan Block)
	requestChan <- readRequest{Height: height, Response: responseChan}
	return <-responseChan

	/*
		f, err := os.Open("blocks/pareme0000.dat")
		if err != nil {
			return Block{}
		}
		defer f.Close()

		// Skip 4-byte magic
		if _, err := f.Seek(4, 0); err != nil {
			return Block{}
		}

		// Buffer for one block
		buf := make([]byte, 112)
		for {
			n, err := f.Read(buf)
			if err != nil || n != 112 {
				return Block{}
			}
			height := int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
			if height == i {
				return Block{
					Height: height,
					Timestamp: int64(buf[4])<<56 | int64(buf[5])<<48 | int64(buf[6])<<40 | int64(buf[7])<<32 |
						int64(buf[8])<<24 | int64(buf[9])<<16 | int64(buf[10])<<8 | int64(buf[11]),
					PrevHash:   *(*[32]byte)(buf[12:44]),
					Nonce:      int(buf[44])<<24 | int(buf[45])<<16 | int(buf[46])<<8 | int(buf[47]),
					Difficulty: *(*[32]byte)(buf[48:80]),
					BodyHash:   *(*[32]byte)(buf[80:112]),
				}
			}
		}
	*/
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
	startBlock := initBlock(1)
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
