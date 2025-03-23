package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// readRequest represents a request to read a block by height
type readRequest struct {
	Heights  []int
	Response chan [][]Block
}

// blockWriter processes incoming blocks and read requests, updating the blockchain files
func blockWriter(ctx context.Context, wg *sync.WaitGroup) (chan Block, error) {
	printToLog("\nStarting up blockWriter...")
	// Open .dat file for reading and appending
	datFile, err := os.OpenFile("blocks/pareme0000.dat", os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open DAT file %v", err)
	}

	// Check and initialize DIR file if it doesn't exist
	dirFilePath := "blocks/dir0000.idx"
	dirFile, err := os.OpenFile(dirFilePath, os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open DIR file %v", err)
	}

	// Check and initialize OFF file if it doesn't exist
	offFilePath := "blocks/off0000.idx"
	offFile, err := os.OpenFile(offFilePath, os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open OFF file %v", err)
	}

	newBlockChan := make(chan Block, 10) // Send new blocks to miner

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer datFile.Close()
		defer dirFile.Close()
		defer offFile.Close()

		for {
			select {
			case <-ctx.Done():
				printToLog("Block writer shutting down")
				return

			case respChan := <-indexRequestChan:
				printToLog("Recieved request for chain stats")
				// Handle index read request
				height, totalBlocks, err := getChainStats(datFile, dirFile)
				if err != nil {
					printToLog(fmt.Sprintf("Error retreiving chain stats: %v", err))
				}
				respChan <- [2]uint32{uint32(height), uint32(totalBlocks)}

			case req := <-requestChan:
				printToLog(fmt.Sprintf("writer: Recieved request for blocks: %v", req.Heights))
				// Handle block read request
				response, err := readBlocksFromFile(datFile, dirFile, offFile, req.Heights)
				if err != nil {
					printToLog(fmt.Sprintf("Error reading blocks: %v", err))
				}
				req.Response <- response

			case block := <-blockChan:
				// Verify and write new block
				printToLog(fmt.Sprintf("writer: Recieved new block %d, verifying...", block.Height))
				verified, err := verifyBlock(datFile, dirFile, offFile, block)
				if err != nil {
					printToLog(fmt.Sprintf("Error verifying block at height %d: %v", block.Height, err))
				}
				if !verified {
					printToLog(fmt.Sprintf("Invalid block at height %d", block.Height))
					continue
				}
				if writeBlock(datFile, block) == -1 {
					printToLog(fmt.Sprintf("failed to write block %d", block.Height))
					continue
				}
				err = updateIndexFiles(datFile, dirFile, offFile, block)
				if err != nil {
					printToLog(fmt.Sprintf("failed to update index: %v", err))
				} else {
					printToLog("Successfully updated index\n")
					newBlockChan <- block // Notify miners of new block
				}

			}
		}
	}()

	return newBlockChan, nil
}

//---------------- DIRECT I/O FUNCTIONS

// writeBlock appends a block to the .dat file
func writeBlock(datFile *os.File, b Block) int {
	// Serialize block to 116-byte array
	data := make([]byte, 0, 116)

	magic := []byte("PARE")

	data = append(data, magic[:]...)
	data = append(data,
		byte(b.Height>>24), byte(b.Height>>16), byte(b.Height>>8), byte(b.Height),
		byte(b.Timestamp>>56), byte(b.Timestamp>>48), byte(b.Timestamp>>40), byte(b.Timestamp>>32),
		byte(b.Timestamp>>24), byte(b.Timestamp>>16), byte(b.Timestamp>>8), byte(b.Timestamp))
	data = append(data, b.PrevHash[:]...)
	data = append(data, byte(b.Nonce>>24), byte(b.Nonce>>16), byte(b.Nonce>>8), byte(b.Nonce))
	data = append(data, b.Difficulty[:]...)
	data = append(data, b.BodyHash[:]...)

	// Write serialized data to file
	if _, err := datFile.Write(data); err != nil {
		printToLog(fmt.Sprintf("Error writing block %d: %v", b.Height, err))
		return -1
	}
	printToLog(fmt.Sprintf("Wrote Block %d to file. Timestamp: %v secs.", b.Height, b.Timestamp/1000))
	return 1
}

// Given a new block, updates DIR & OFF
func updateIndexFiles(datFile, directoryFile, offsetFile *os.File, newBlock Block) error {
	printToLog(fmt.Sprintf("\nUpdating index files after block %d was written", newBlock.Height))
	// Calculate the new block's offset
	datStat, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat DAT file: %v", err)
	}
	numBlocks := datStat.Size() / 116  // Total number of blocks in DAT
	newOffset := uint32(numBlocks - 1) // Offset the new block was written at

	// Check if directory has the height we want
	dirIndex := (newBlock.Height - 1) * 4 // The directory index to read given blocks height
	var dirSize int64
	dirStat, err := directoryFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat DIR file: %v", err)
	}
	dirSize = dirStat.Size()
	if dirSize < int64(dirIndex+4) {
		var lastValue uint32 // last value in the directory in case we need it
		_, err = directoryFile.Seek(-4, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("failed to seek DIR file: %v", err)
		}
		err = binary.Read(directoryFile, binary.BigEndian, &lastValue)
		if err != nil {
			return fmt.Errorf("failed to read DIR file: %v", err)
		}

		_, err = directoryFile.Seek(0, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("failed to seek DIR file: %v", err)
		}

		dirStat, err := directoryFile.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat DIR file: %v", err)
		}
		dirSize = dirStat.Size()
		// Loop until the directory is filled to our height
		for dirSize < int64(dirIndex+4) {
			err = binary.Write(directoryFile, binary.BigEndian, lastValue)
			if err != nil {
				return fmt.Errorf("failed to write to DIR file: %v", err)
			}
			dirStat, err := directoryFile.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat DIR file: %v", err)
			}
			dirSize = dirStat.Size()
		}
	}

	// Read the value at the desired offset and then add 1 to it and the rest
	_, err = directoryFile.Seek(int64(dirIndex), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek DIR file: %v", err)
	}
	var dirValue uint32
	err = binary.Read(directoryFile, binary.BigEndian, &dirValue)
	if err != nil {
		return fmt.Errorf("failed to read DIR file: %v", err)
	}
	dirValueBytes := int64(dirValue * 4)

	// Come back and increment it by 1
	_, err = directoryFile.Seek(int64(dirIndex), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek DIR file: %v", err)
	}
	err = binary.Write(directoryFile, binary.BigEndian, dirValue+1)
	if err != nil {
		return fmt.Errorf("failed to write to DIR file: %v", err)
	}
	for {
		// Increment all the rest by 1
		pos, err := directoryFile.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("failed to seek DIR file: %v", err)
		}

		dirStat, err := directoryFile.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat DIR file: %v", err)
		}
		if pos >= dirStat.Size() {
			break
		}

		var temp uint32
		err = binary.Read(directoryFile, binary.BigEndian, &temp)
		if err != nil {
			return fmt.Errorf("failed to read DIR file: %v", err)
		}
		err = binary.Write(directoryFile, binary.BigEndian, temp+1)
		if err != nil {
			return fmt.Errorf("failed to write to DIR file: %v", err)
		}
	}

	// Check if we are just appending to the OFFSET file
	offStat, err := offsetFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat OFF file")
	}
	offSize := offStat.Size()

	if dirValueBytes > offSize {
		return fmt.Errorf("offset %d beyond end of file (%d bytes)", dirValueBytes, offSize)
	}

	if dirValueBytes == offSize {
		// Can be directly appened to OFFSET file
		_, err = offsetFile.Seek(0, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("failed to seek OFF file: %v", err)
		}
		err = binary.Write(offsetFile, binary.BigEndian, newOffset)
		if err != nil {
			return fmt.Errorf("failed to write to OFF file: %v", err)
		}
	} else {
		// Save data from OFFSET to be moved over
		var buf []uint32
		_, err = offsetFile.Seek(dirValueBytes, io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek OFF file: %v", err)
		}
		for {
			var temp uint32
			err := binary.Read(offsetFile, binary.BigEndian, &temp)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read OFF file: %v", err)
			}
			buf = append(buf, temp)
		}

		// Write the newOffset at the dirValue offset
		_, err = offsetFile.Seek(dirValueBytes, io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek OFF file: %v", err)
		}
		err = binary.Write(offsetFile, binary.BigEndian, newOffset)
		if err != nil {
			return fmt.Errorf("failed to write to OFF file: %v", err)
		}

		// Append back the rest of the data
		for _, v := range buf {
			err = binary.Write(offsetFile, binary.BigEndian, v)
			if err != nil {
				return fmt.Errorf("failed to write to OFF file: %v", err)
			}
		}
	}

	datSlice, dirSlice, offSlice, err := displayIndexFiles(datFile, directoryFile, offsetFile)
	if err != nil {
		return fmt.Errorf("failed to display index files: %v", err)
	}
	// Print updated chain stats but limit to last 8
	if len(datSlice) < 9 {
		printToLog(fmt.Sprintf("Dat file: %v", datSlice))
	} else {
		printToLog(fmt.Sprintf("Dat file: %v", datSlice[len(datSlice)-8:]))
	}
	if len(dirSlice) < 9 {
		printToLog(fmt.Sprintf("Directory file: %v", dirSlice))
	} else {
		printToLog(fmt.Sprintf("Directory file: %v", dirSlice[len(dirSlice)-8:]))
	}
	if len(offSlice) < 9 {
		printToLog(fmt.Sprintf("Offset file: %v", offSlice))
	} else {
		printToLog(fmt.Sprintf("Offset file: %v", offSlice[len(offSlice)-8:]))
	}
	return nil
}

// Given heights, returns Blocks
func readBlocksFromFile(datFile, directoryFile, offsetFile *os.File, heights []int) ([][]Block, error) {
	// Obtain height group locations
	var heightGroupStarts []uint32
	var heightGroupSizes []uint32
	var heightGroupStart uint32
	var heightGroupEnd uint32

	for _, height := range heights {
		if height == 1 {
			heightGroupStarts = append(heightGroupStarts, 0)
			heightGroupSizes = append(heightGroupSizes, 1)
		} else {
			_, err := directoryFile.Seek(int64((height-2)*4), 0)
			if err != nil {
				return nil, fmt.Errorf("failed to seek DIR file: %v", err)
			}
			err = binary.Read(directoryFile, binary.BigEndian, &heightGroupStart)
			if err != nil {
				return nil, fmt.Errorf("failed to read DIR file: %v", err)
			}
			err = binary.Read(directoryFile, binary.BigEndian, &heightGroupEnd)
			if err != nil {
				return nil, fmt.Errorf("failed to read DIR file: %v", err)
			}
			heightGroupSize := (heightGroupEnd - heightGroupStart)
			heightGroupStarts = append(heightGroupStarts, heightGroupStart)
			heightGroupSizes = append(heightGroupSizes, heightGroupSize)
		}
	}
	// Obtain block location offsets
	blockLocs := make([][]uint32, len(heights))
	for i, hgstart := range heightGroupStarts {
		_, err := offsetFile.Seek(int64(hgstart*4), 0)
		if err != nil {
			return nil, fmt.Errorf("failed to seek OFF file: %v", err)
		}
		var blockLoc uint32
		for range heightGroupSizes[i] {
			err = binary.Read(offsetFile, binary.BigEndian, &blockLoc)
			if err != nil {
				return nil, fmt.Errorf("failed to read OFF file: %v", err)
			}
			blockLocs[i] = append(blockLocs[i], blockLoc)
		}
	}
	// Obtain blocks
	blocks := make([][]Block, len(heights))
	for i := range blockLocs {
		blockByte := make([]byte, 116)
		for _, loc := range blockLocs[i] {
			_, err := datFile.Seek(int64(loc*116), 0)
			if err != nil {
				return nil, fmt.Errorf("failed to seek OFF file: %v", err)
			}
			n, err := datFile.Read(blockByte)
			if n != 116 || err != nil {
				return nil, fmt.Errorf("failed to read DAT file: %v", err)
			}
			if !bytes.Equal(blockByte[:4], []byte("PARE")) {
				return nil, fmt.Errorf("failed to find magic bytes")
			}
			block := byteToBlock([112]byte(blockByte[4:]))
			blocks[i] = append(blocks[i], block)
		}
	}
	return blocks, nil
}

func getChainStats(datFile, directoryFile *os.File) (int, int, error) {
	// Get latest height & total blocks
	datStat, err := datFile.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("failed stating files: %v", err)
	}
	dirStat, err := directoryFile.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("failed stating files: %v", err)
	}
	height := int(dirStat.Size() / 4)
	totalBlocks := int(datStat.Size() / 116)
	return height, totalBlocks, nil
}

//----------------- REQUEST FILE INFO FUNCTIONS

// readBlock requests blocks from the writer by heights
func requestBlocks(heights []int) [][]Block {
	responseChan := make(chan [][]Block)
	requestChan <- readRequest{Heights: heights, Response: responseChan}
	return <-responseChan
}

func requestChainStats() (int, int) {
	responseChan := make(chan [2]uint32)
	indexRequestChan <- responseChan
	result := <-responseChan
	return int(result[0]), int(result[1])
}

//--------------- QOL FUNCTIONS

func byteToBlock(data [112]byte) Block {
	var block Block
	block.Height = int(binary.BigEndian.Uint32(data[:4]))
	block.Timestamp = int64(binary.BigEndian.Uint64(data[4:12]))
	copy(block.PrevHash[:], data[12:44])
	block.Nonce = int(binary.BigEndian.Uint32(data[44:48]))
	copy(block.Difficulty[:], data[48:80])
	copy(block.BodyHash[:], data[80:112])
	return block
}

func blockToByte(b Block) [112]byte {
	var result [112]byte
	var offset int

	// Height: int (4 bytes)
	binary.BigEndian.PutUint32(result[offset:offset+4], uint32(b.Height))
	offset += 4

	// Timestamp: int 64 (8 bytes)
	binary.BigEndian.PutUint64(result[offset:offset+8], uint64(b.Timestamp))
	offset += 8

	// PrevHash: [32]byte (32 bytes)
	copy(result[offset:offset+32], b.PrevHash[:])
	offset += 32

	// Nonce: int (4 bytes)
	binary.BigEndian.PutUint32(result[offset:offset+4], uint32(b.Nonce))
	offset += 4

	// Difficulty: [32]byte (32 bytes)
	copy(result[offset:offset+32], b.Difficulty[:])
	offset += 32

	// BodyHash: [32]byte (32 bytes)
	copy(result[offset:offset+32], b.BodyHash[:])

	return result
}

func displayIndexFiles(datFile, directoryFile, offsetFile *os.File) ([]int, []uint32, []uint32, error) {
	readAllUint32 := func(f *os.File) ([]uint32, error) {
		// Seek to start just in case
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}

		var values []uint32
		for {
			var v uint32
			err := binary.Read(f, binary.BigEndian, &v)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return values, nil
	}

	readAllBlocks := func(f *os.File) ([]Block, error) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}

		fileStat, err := f.Stat()
		if err != nil {
			return nil, err
		}
		fileSize := fileStat.Size()
		if fileSize%116 != 0 {
			return nil, fmt.Errorf("fileSize not a multiple of 116")
		}

		numOfBlocks := int(fileSize / 116)
		var values [][112]byte
		for i := 0; i < numOfBlocks; i++ {
			_, err := f.Seek(4, io.SeekCurrent)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			var v [112]byte
			err = binary.Read(f, binary.BigEndian, &v)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		var blocks []Block
		for i := 0; i < len(values); i++ {
			blocks = append(blocks, byteToBlock(values[i]))
		}
		return blocks, nil
	}

	a1, err1 := readAllBlocks(datFile)
	if err1 != nil {
		return nil, nil, nil, err1
	}
	var heights []int
	for a := range a1 {
		heights = append(heights, a1[a].Height)
	}

	a2, err2 := readAllUint32(directoryFile)
	if err2 != nil {
		return nil, nil, nil, err2
	}

	a3, err3 := readAllUint32(offsetFile)
	if err3 != nil {
		return nil, nil, nil, err3
	}

	return heights, a2, a3, nil
}
