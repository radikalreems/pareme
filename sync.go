package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// syncChain synchronizes the blockchain from files
func syncChain() error {
	printToLog("\nSyncing blockchain from files...")

	// Init blockchain files
	err := initFiles() // Sync folder, dat, index file
	if err != nil {
		return fmt.Errorf("failed syncing files: %v", err)
	}

	// Get latest height & total blocks
	dirStat, err := os.Stat("blocks/dir0000.idx")
	if err != nil {
		return fmt.Errorf("failed stating files: %v", err)
	}
	datStat, err := os.Stat("blocks/pareme0000.dat")
	if err != nil {
		return fmt.Errorf("failed stating files: %v", err)
	}
	height := int(dirStat.Size() / 4)
	totalBlocks := int(datStat.Size() / 116)
	printToLog(fmt.Sprintf("Synced to height %d with %d total blocks", height, totalBlocks))

	return nil
}

//------------------- DIRECT I/O FUNCTIONS

// syncFiles initializes data files (.dat and .idx)
func initFiles() error {
	printToLog("Initializing files")
	// Ensure blocks directory exists
	if err := os.MkdirAll("blocks", 0755); err != nil {
		printToLog(fmt.Sprintf("Error creating blocks folder: %v", err))
	}

	// Check and initialize DAT file if it doesn't exist
	datFilePath := "blocks/pareme0000.dat"
	datFile, err := os.OpenFile(datFilePath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", datFilePath, err)
	}
	defer datFile.Close()

	// Create DIR file / Wipe if it exists
	dirFilePath := "blocks/dir0000.idx"
	dirFile, err := os.Create(dirFilePath)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", dirFilePath, err)
	}
	defer dirFile.Close()

	// Create OFF file / Wipe if it exists
	offFilePath := "blocks/off0000.idx"
	offFile, err := os.Create(offFilePath)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", offFilePath, err)
	}
	defer offFile.Close()

	datFileStat, err := os.Stat(datFilePath)
	if err != nil {
		return fmt.Errorf("failed stating datFile %v", err)
	}
	if datFileStat.Size() == 0 {
		blocks := []Block{genesisBlock()}
		err := writeBlocks(datFile, dirFile, offFile, blocks)
		if err != nil {
			return fmt.Errorf("failed to write genesis: %v", err)
		}
		printToLog(fmt.Sprintf("Created %s with genesis", datFilePath))
	}

	// Init DIR & OFF files based on DAT
	err = initializeIndexFiles(datFile, dirFile, offFile)
	if err != nil {
		return fmt.Errorf("failed to init index files: %v", err)
	}

	return nil

}

// Reads DAT file and writes DIR & OFF
func initializeIndexFiles(datFile, directoryFile, offsetFile *os.File) error {
	printToLog("Syncing index files to dat...")
	// Get the size of the DAT file to calculate the number of blocks
	datStat, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to get DAT file stats: %v", err)
	}
	numBlocks := datStat.Size() / 116 // Each block is stored as 4 + 112 = 116 bytes
	printToLog(fmt.Sprintf("%d blocks in DAT", numBlocks))

	// Map to group offsets by height
	heightToOffsets := make(map[int][]uint32)

	// Read each block's height and record its offset
	for offset := uint32(0); offset < uint32(numBlocks); offset++ {
		// Seek to the block's start
		_, err := datFile.Seek(int64(offset*116), 0)
		if err != nil {
			return fmt.Errorf("failed to seek in DAT file: %v", err)
		}

		// Verify the first 4 MAGIC bytes
		magicCheck := make([]byte, 4)
		_, err = datFile.Read(magicCheck)
		if err != nil {
			return fmt.Errorf("failed to read magic byte for block offset %d", offset/116)
		}
		if string(magicCheck) != "PARE" {
			return fmt.Errorf("failed to verify magic byte for block offset %d", offset/116)
		}

		// Read the next 4 bytes (Height)
		heightBytes := make([]byte, 4)
		_, err = datFile.Read(heightBytes)
		if err != nil {
			return fmt.Errorf("failed to read height from DAT file: %v", err)
		}
		height := int(binary.BigEndian.Uint32(heightBytes))

		// Group the offset by height
		heightToOffsets[height] = append(heightToOffsets[height], offset)
	}

	var heights []int
	for height := range heightToOffsets {
		heights = append(heights, height)
	}
	sort.Ints(heights)

	// Build OFFSETS and DIRECTORY
	var offsets []uint32
	directory := make([]uint32, 0)

	for _, height := range heights {
		// Add all offsets for this height
		offsets = append(offsets, heightToOffsets[height]...)
		// Record the end position of this height group
		directory = append(directory, uint32(len(offsets)))
	}

	// Print DIR & OFF but limit to last 8
	if len(directory) < 9 {
		printToLog(fmt.Sprintf("Directory values: %v", directory))
	} else {
		printToLog(fmt.Sprintf("Directory values: %v", directory[len(directory)-8:]))
	}
	if len(offsets) < 9 {
		printToLog(fmt.Sprintf("Offset values: %v", offsets))
	} else {
		printToLog(fmt.Sprintf("Offset values: %v", offsets[len(offsets)-8:]))
	}
	// Write OFFFSETS file
	offsetFile.Seek(0, 0)
	for _, offset := range offsets {
		err := binary.Write(offsetFile, binary.BigEndian, offset)
		if err != nil {
			return fmt.Errorf("failed to write to OFFSETS file: %v", err)
		}
	}

	// Write DIRECTORY file
	directoryFile.Seek(0, 0)
	for _, endPos := range directory {
		err := binary.Write(directoryFile, binary.BigEndian, endPos)
		if err != nil {
			return fmt.Errorf("failed to write to directory file: %v", err)
		}
	}

	return nil
}
