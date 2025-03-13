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

func updateIndex(height, totalBlocks int) {
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

func readBlock(height int) Block {

	responseChan := make(chan Block)
	requestChan <- readRequest{Height: height, Response: responseChan}
	return <-responseChan
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
	printToLog(fmt.Sprintf("Wrote Block %d to file. Difficulty: %x\n", b.Height, b.Difficulty[:8]))
	return 1
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
