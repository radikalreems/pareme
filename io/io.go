package io

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"pareme/common"
	"pareme/network"
	"path/filepath"
	"sort"
	"sync"
)

const (
	blocksDir      = "blocks"                     // Directory for block data
	dirIndexFile   = "dir0000.idx"                // Directory index file
	offIndexFile   = "off0000.idx"                // Offset index file
	dataFile       = "pareme0000.dat"             // Block data file
	indexEntrySize = 4                            // Size of entries in index files
	magicBytes     = "PARE"                       // Magic bytes embeded before every block entry
	magicSize      = 4                            // Size of magic bytes
	blockEntrySize = common.BlockSize + magicSize // Size of entries in block files
	dirPerm        = 0755
	filePerm       = 0666
	dataFilePerm   = 0644
)

// syncChain synchronizes the blockchain from files
// Returns: Error if synchronization fails, nil otherwise
func SyncChain() error {
	common.PrintToLog("\nSyncing blockchain from files...")

	// Init blockchain files
	err := initFiles() // Set up folder, dat, index file
	if err != nil {
		return fmt.Errorf("failed syncing files: %v", err)
	}

	// Get file stats for index and data
	dirPath := filepath.Join(blocksDir, dirIndexFile)
	dirStat, err := os.Stat(dirPath)
	if err != nil {
		return fmt.Errorf("failed stating index file %s: %v", dirPath, err)
	}
	datPath := filepath.Join(blocksDir, dataFile)
	datStat, err := os.Stat(datPath)
	if err != nil {
		return fmt.Errorf("failed stating data file %s: %v", datPath, err)
	}

	// Calculate blockchain height and total blocks
	if dirStat.Size()%indexEntrySize != 0 {
		return fmt.Errorf("invalid index file size: %d bytes", dirStat.Size())
	}
	height := int(dirStat.Size() / indexEntrySize)
	if datStat.Size()%blockEntrySize != 0 {
		return fmt.Errorf("invalid data file size: %d bytes", datStat.Size())
	}
	totalBlocks := int(datStat.Size() / blockEntrySize)

	common.PrintToLog(fmt.Sprintf("Synced to height %d with %d total blocks", height, totalBlocks))
	return nil
}

// blockWriter processes incoming blocks and read requests, updating the blockchain files
// Returns: Channel for new block heights, error if setup fails
func BlockWriter(ctx context.Context, wg *sync.WaitGroup) (chan []int, error) {
	common.PrintToLog("\nStarting up blockWriter...")

	// Open DAT file
	datFilePath := filepath.Join(blocksDir, dataFile)
	datFile, err := os.OpenFile(datFilePath, os.O_APPEND|os.O_RDWR, dataFilePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to open DAT file %s: %v", datFilePath, err)
	}

	// Open DIR file
	dirFilePath := filepath.Join(blocksDir, dirIndexFile)
	dirFile, err := os.OpenFile(dirFilePath, os.O_RDWR, filePerm)
	if err != nil {
		datFile.Close()
		return nil, fmt.Errorf("failed to open DIR file %s: %v", dirFilePath, err)
	}

	// Open OFF file
	offFilePath := filepath.Join(blocksDir, offIndexFile)
	offFile, err := os.OpenFile(offFilePath, os.O_RDWR, filePerm)
	if err != nil {
		datFile.Close()
		dirFile.Close()
		return nil, fmt.Errorf("failed to open OFF file %s: %v", offFilePath, err)
	}

	// Channel for notifying miners
	newHeightsChan := make(chan []int, 10) // Send new blocks to miner

	// Process blocks and requests
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer datFile.Close()
		defer dirFile.Close()
		defer offFile.Close()

		orphaned := make([]common.Block, 0)

		for {
			select {
			case <-ctx.Done():
				common.PrintToLog("Block writer shutting down")
				return

			case respChan := <-common.IndexRequestChan:
				// Handle chain stats request
				height, totalBlocks, err := readChainStats(datFile, dirFile)
				if err != nil {
					common.PrintToLog(fmt.Sprintf("Error retreiving chain stats: %v", err))
				}
				respChan <- [2]uint32{uint32(height), uint32(totalBlocks)}
				close(respChan)

			case req := <-common.RequestChan:
				// Handle block read request
				response, err := readBlocks(datFile, dirFile, offFile, req.Heights)
				if err != nil {
					common.PrintToLog(fmt.Sprintf("Error reading blocks for heights %v: %v", req.Heights, err))
				}
				req.Response <- response
				close(req.Response)

			case writeBlockReq := <-common.WriteBlockChan:
				// Verify and write new blocks
				common.PrintToLog(fmt.Sprintf("\nwriter: Recieved %d new blocks. verifying...", len(writeBlockReq.Blocks)))
				blocksToVerify := make([]common.Block, 0, len(writeBlockReq.Blocks)+len(orphaned))
				blocksToVerify = append(blocksToVerify, writeBlockReq.Blocks...)
				blocksToVerify = append(blocksToVerify, orphaned...)
				verified, failed, orphans, err := VerifyBlocks(datFile, dirFile, offFile, blocksToVerify)
				if err != nil {
					common.PrintToLog(fmt.Sprintf("Error verifying blocks: %v", err))
				}
				common.PrintToLog(fmt.Sprintf("%d verified | %d failed | %d orphaned", len(verified), len(failed), len(orphans)))
				orphaned = orphans
				if len(verified) == 0 {
					continue
				}
				err = writeBlocks(datFile, dirFile, offFile, verified)
				if err != nil {
					common.PrintToLog(fmt.Sprintf("failed to write %d blocks: %v", len(verified), err))
					continue
				}
				common.PrintToLog(fmt.Sprintf("Successfully wrote %d new blocks!", len(verified)))

				// Broadcast to peers
				var exclusion []*common.Peer
				if writeBlockReq.Type == "sync" {
					for i := range common.AllPeers {
						exclusion = append(exclusion, common.AllPeers[i])
					}
				} else if writeBlockReq.Type == "received" || writeBlockReq.Type == "mined" {
					exclusion = []*common.Peer{writeBlockReq.From}
				}

				for _, block := range verified {
					go network.BroadcastBlock(block, exclusion)
				}

				// Notify miners
				if common.MiningState.Active {
					heights := make([]int, 0, len(verified))
					for _, v := range verified {
						heights = append(heights, v.Height)
					}
					newHeightsChan <- heights
				}

			}
		}
	}()

	return newHeightsChan, nil
}

//---------------- DIRECT I/O FUNCTIONS

// initFiles initializes data files (.dat and .idx)
// Returns: Error if initialization fails, nil otherwise
func initFiles() error {
	common.PrintToLog("Initializing files")

	// Ensure blocks directory exists
	if err := os.MkdirAll(blocksDir, dirPerm); err != nil {
		return fmt.Errorf("failed creating directory %s: %v", blocksDir, err)
	}

	// Initialize DAT file
	datFilePath := filepath.Join(blocksDir, dataFile)
	datFile, err := os.OpenFile(datFilePath, os.O_CREATE|os.O_APPEND|os.O_RDWR, filePerm)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", datFilePath, err)
	}
	defer datFile.Close()

	// Initialize DIR file (wipe if exists)
	dirFilePath := filepath.Join(blocksDir, dirIndexFile)
	dirFile, err := os.Create(dirFilePath)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", dirFilePath, err)
	}
	defer dirFile.Close()

	// Initialize OFF file (wipe if exists)
	offFilePath := filepath.Join(blocksDir, offIndexFile)
	offFile, err := os.Create(offFilePath)
	if err != nil {
		return fmt.Errorf("failed creating %s: %v", offFilePath, err)
	}
	defer offFile.Close()

	// Check if DAT file is empty and write genesis block
	datFileStat, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed stating %s: %v", datFilePath, err)
	}
	if datFileStat.Size() == 0 {
		blocks := []common.Block{common.GenesisBlock()}
		err := writeBlocks(datFile, dirFile, offFile, blocks)
		if err != nil {
			return fmt.Errorf("failed to write genesis: %v", err)
		}
		common.PrintToLog(fmt.Sprintf("Created %s with genesis", datFilePath))
	}

	// Initialize DIR and OFF files based on DAT
	err = initIndexFiles(datFile, dirFile, offFile)
	if err != nil {
		return fmt.Errorf("failed to init index files: %v", err)
	}

	return nil

}

// initIndexFiles syncs DIR and OFF index files with DAT file
// Returns: Error if initialization fails, nil otherwise
func initIndexFiles(datFile, directoryFile, offsetFile *os.File) error {
	common.PrintToLog("Syncing index files to dat...")

	// Get DAT file stats
	datStat, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to get DAT file stats: %v", err)
	}
	if datStat.Size()%blockEntrySize != 0 {
		return fmt.Errorf("invalid DAT file size: %d bytes", datStat.Size())
	}
	numBlocks := datStat.Size() / int64(blockEntrySize)
	common.PrintToLog(fmt.Sprintf("%d blocks in DAT", numBlocks))

	// Group offsets by height
	heightToOffsets := make(map[int][]uint32)
	for offset := uint32(0); offset < uint32(numBlocks); offset++ {
		// Seek to the block start
		_, err := datFile.Seek(int64(offset*uint32(blockEntrySize)), 0)
		if err != nil {
			return fmt.Errorf("failed to seek to offset %d in DAT: %v", offset, err)
		}

		// Verify magic bytes
		magicCheck := make([]byte, magicSize)
		_, err = datFile.Read(magicCheck)
		if err != nil {
			return fmt.Errorf("failed to read magic byte at offset %d: %v", offset, err)
		}
		if string(magicCheck) != magicBytes {
			return fmt.Errorf("invalid magic bytes at offset %d: got %v, want %v", offset, magicCheck, magicBytes)
		}

		// Read height
		heightBytes := make([]byte, 4)
		_, err = datFile.Read(heightBytes)
		if err != nil {
			return fmt.Errorf("failed to read height at offset %d: %v", offset, err)
		}
		height := int(binary.BigEndian.Uint32(heightBytes))

		// Store offset for height
		heightToOffsets[height] = append(heightToOffsets[height], offset)
	}

	// Sort heights
	heights := make([]int, 0, len(heightToOffsets))
	for height := range heightToOffsets {
		heights = append(heights, height)
	}
	sort.Ints(heights)

	// Build offsets and directory
	offsets := make([]uint32, 0, numBlocks)
	directory := make([]uint32, 0, len(heights))
	for _, height := range heights {
		offsets = append(offsets, heightToOffsets[height]...)
		directory = append(directory, uint32(len(offsets)))
	}

	// Log last 8 entries for debugging
	if len(directory) < 9 {
		common.PrintToLog(fmt.Sprintf("Directory values: %v", directory))
	} else {
		common.PrintToLog(fmt.Sprintf("Directory values: %v", directory[len(directory)-8:]))
	}
	if len(offsets) < 9 {
		common.PrintToLog(fmt.Sprintf("Offset values: %v", offsets))
	} else {
		common.PrintToLog(fmt.Sprintf("Offset values: %v", offsets[len(offsets)-8:]))
	}

	// Write OFF file
	if err := offsetFile.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate OFF file: %v", err)
	}
	if _, err := offsetFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek OFF file: %v", err)
	}
	for _, offset := range offsets {
		if err := binary.Write(offsetFile, binary.BigEndian, offset); err != nil {
			return fmt.Errorf("failed to write to offset %d to OFF file: %v", offset, err)
		}
	}

	// Write DIR file
	if err := directoryFile.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate DIR file: %v", err)
	}
	if _, err := directoryFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek DIR file: %v", err)
	}
	for _, endPos := range directory {
		if err := binary.Write(directoryFile, binary.BigEndian, endPos); err != nil {
			return fmt.Errorf("failed to write end position %d to DIR file: %v", endPos, err)
		}
	}

	return nil
}

// updateIndexFiles updates DIR and OFF files for new blocks
// Returns: Error if update fails, nil otherwise
func updateIndexFiles(datFile, directoryFile, offsetFile *os.File, newBlocks []common.Block) error {
	// Calculate the new block's offset
	datStat, err := datFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat DAT file: %v", err)
	}
	if datStat.Size()%blockEntrySize != 0 {
		return fmt.Errorf("invalid DAT file size: %d bytes", datStat.Size())
	}
	numBlocks := datStat.Size() / blockEntrySize              // Total number of blocks in DAT
	startOffset := uint32(numBlocks) - uint32(len(newBlocks)) // Offset where the new blocks batch started

	for i, newBlock := range newBlocks {
		offset := startOffset + uint32(i) // Offset the new block was written at

		// Update DIR file
		dirIndex := (newBlock.Height - 1) * indexEntrySize
		dirStat, err := directoryFile.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat DIR file: %v", err)
		}
		if dirStat.Size() == 0 {
			return fmt.Errorf("failed to update DIR file: DIR is empty")
		}

		// Extend DIR file if needed
		if dirStat.Size() < int64(dirIndex+indexEntrySize) {
			// Determine last height
			lastValue := uint32(0)
			_, err = directoryFile.Seek(-indexEntrySize, io.SeekEnd)
			if err != nil {
				return fmt.Errorf("failed to seek DIR file: %v", err)
			}
			err = binary.Read(directoryFile, binary.BigEndian, &lastValue)
			if err != nil {
				return fmt.Errorf("failed to read DIR file: %v", err)
			}

			// Fill added indexes with last height
			for dirStat.Size() < int64(dirIndex+indexEntrySize) {
				err = binary.Write(directoryFile, binary.BigEndian, lastValue)
				if err != nil {
					return fmt.Errorf("failed to write to DIR file: %v", err)
				}
				dirStat, err = directoryFile.Stat()
				if err != nil {
					return fmt.Errorf("failed to stat DIR file: %v", err)
				}
			}
		}

		// Read and increment DIR value
		_, err = directoryFile.Seek(int64(dirIndex), io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek DIR file to %d: %v", dirIndex, err)
		}
		var dirValue uint32
		err = binary.Read(directoryFile, binary.BigEndian, &dirValue)
		if err != nil {
			return fmt.Errorf("failed to read DIR file at %d: %v", dirIndex, err)
		}
		dirValueBytes := int64(dirValue * indexEntrySize)

		// Write incremented DIR value
		_, err = directoryFile.Seek(int64(dirIndex), io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek DIR file to %d: %v", dirIndex, err)
		}
		err = binary.Write(directoryFile, binary.BigEndian, dirValue+1)
		if err != nil {
			return fmt.Errorf("failed to write to DIR file at %d: %v", dirIndex, err)
		}

		// Increment subsequent DIR values
		for pos := int64(dirIndex + indexEntrySize); ; pos += int64(indexEntrySize) {
			dirStat, err := directoryFile.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat DIR file: %v", err)
			}
			if pos >= dirStat.Size() {
				break
			}
			_, err = directoryFile.Seek(pos, io.SeekStart)
			if err != nil {
				return fmt.Errorf("failed to seek DIR file to %d: %v", pos, err)
			}
			var temp uint32
			err = binary.Read(directoryFile, binary.BigEndian, &temp)
			if err != nil {
				return fmt.Errorf("failed to read DIR file at %d: %v", pos, err)
			}
			_, err = directoryFile.Seek(pos, io.SeekStart)
			if err != nil {
				return fmt.Errorf("failed to seek DIR file to %d: %v", pos, err)
			}

			err = binary.Write(directoryFile, binary.BigEndian, temp+1)
			if err != nil {
				return fmt.Errorf("failed to write to DIR file at %d: %v", pos, err)
			}
		}

		// Update OFF file
		offStat, err := offsetFile.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat OFF file: %v", err)
		}
		offSize := offStat.Size()
		if dirValueBytes > offSize {
			return fmt.Errorf("offset %d beyond OFF file size %d", dirValueBytes, offSize)
		}

		if dirValueBytes == offSize {
			// Append to OFF file
			_, err = offsetFile.Seek(0, io.SeekEnd)
			if err != nil {
				return fmt.Errorf("failed to seek OFF file: %v", err)
			}
			err = binary.Write(offsetFile, binary.BigEndian, offset)
			if err != nil {
				return fmt.Errorf("failed to write offset %d to OFF file: %v", offset, err)
			}
		} else {
			// Shift and insert in OFF file
			buf := make([]uint32, 0)
			_, err = offsetFile.Seek(dirValueBytes, io.SeekStart)
			if err != nil {
				return fmt.Errorf("failed to seek OFF file to %d: %v", dirValueBytes, err)
			}
			for {
				var temp uint32
				err := binary.Read(offsetFile, binary.BigEndian, &temp)
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("failed to read OFF file at %d: %v", dirValueBytes, err)
				}
				buf = append(buf, temp)
			}

			// Insert new offset
			_, err = offsetFile.Seek(dirValueBytes, io.SeekStart)
			if err != nil {
				return fmt.Errorf("failed to seek OFF file to %d: %v", dirValueBytes, err)
			}
			err = binary.Write(offsetFile, binary.BigEndian, offset)
			if err != nil {
				return fmt.Errorf("failed to write offset %d to OFF file: %v", offset, err)
			}

			// Append remaining offsets
			for _, v := range buf {
				err = binary.Write(offsetFile, binary.BigEndian, v)
				if err != nil {
					return fmt.Errorf("failed to write offset %d to OFF file: %v", v, err)
				}
			}
		}
	}

	// Log updated chain stats
	datSlice, _, _, err := displayIndexFiles(datFile, directoryFile, offsetFile)
	if err != nil {
		return fmt.Errorf("failed to display index files: %v", err)
	}
	// Print updated chain stats but limit to last 8
	if len(datSlice) <= 8 {
		common.PrintToLog(fmt.Sprintf("Dat file: %v", datSlice))
	} else {
		common.PrintToLog(fmt.Sprintf("Dat file: %v", datSlice[len(datSlice)-8:]))
	}

	/*
		if len(dirSlice) < 9 {
			common.PrintToLog(fmt.Sprintf("Directory file: %v", dirSlice))
		} else {
			common.PrintToLog(fmt.Sprintf("Directory file: %v", dirSlice[len(dirSlice)-8:]))
		}
		if len(offSlice) < 9 {
			common.PrintToLog(fmt.Sprintf("Offset file: %v", offSlice))
		} else {
			common.PrintToLog(fmt.Sprintf("Offset file: %v", offSlice[len(offSlice)-8:]))
		}
	*/
	return nil
}

// writeBlock appends blocks to the .dat file and updates indexes
// Returns: Error if writing fails, nil otherwise
func writeBlocks(datFile, dirFile, offFile *os.File, blocks []common.Block) error {
	if len(blocks) < 1 {
		return fmt.Errorf("cannot write %d blocks: unsupported amount", len(blocks))
	}

	for _, block := range blocks {
		// Serialize block to 88-byte array
		data := make([]byte, 0, blockEntrySize)

		data = append(data, []byte(magicBytes)...)
		data = append(data,
			byte(block.Height>>24), byte(block.Height>>16), byte(block.Height>>8), byte(block.Height),
			byte(block.Timestamp>>56), byte(block.Timestamp>>48), byte(block.Timestamp>>40), byte(block.Timestamp>>32),
			byte(block.Timestamp>>24), byte(block.Timestamp>>16), byte(block.Timestamp>>8), byte(block.Timestamp))
		data = append(data, block.PrevHash[:]...)
		data = append(data, block.NBits[:]...)
		data = append(data, byte(block.Nonce>>24), byte(block.Nonce>>16), byte(block.Nonce>>8), byte(block.Nonce))
		data = append(data, block.BodyHash[:]...)

		// Write to DAT file
		if _, err := datFile.Write(data); err != nil {
			return fmt.Errorf("failed writing block %d: %v", block.Height, err)
		}
		common.PrintToLog(fmt.Sprintf("Wrote Block %d to file. Timestamp: %v secs.", block.Height, block.Timestamp/1000))
	}

	// Update index files
	if blocks[0].Height == 1 {
		if len(blocks) != 1 {
			return fmt.Errorf("invalid genesis write: %d blocks provided", len(blocks))
		}
		err := initIndexFiles(datFile, dirFile, offFile)
		if err != nil {
			return fmt.Errorf("failed initializing index files for genesis: %v", err)
		}
	} else {
		err := updateIndexFiles(datFile, dirFile, offFile, blocks)
		if err != nil {
			return fmt.Errorf("failed updating index files: %v", err)
		}
	}
	return nil
}

// readBlocks returns blocks requested by height from file
// Returns: Slice of block groups by height, error if reading fails
func readBlocks(datFile, directoryFile, offsetFile *os.File, heights []int) ([][]common.Block, error) {
	if len(heights) == 0 {
		return nil, fmt.Errorf("failed reading blocks: no blocks given")
	}

	// Get height group locations
	heightGroupStarts := make([]uint32, 0, len(heights))
	heightGroupSizes := make([]uint32, 0, len(heights))
	for _, height := range heights {
		if height == 1 {
			heightGroupStarts = append(heightGroupStarts, 0)
			heightGroupSizes = append(heightGroupSizes, 1)
		} else {
			seekPos := int64((height - 2) * indexEntrySize)
			if _, err := directoryFile.Seek(seekPos, 0); err != nil {
				return nil, fmt.Errorf("failed to seek DIR file to %d: %v", seekPos, err)
			}
			var start, end uint32
			if err := binary.Read(directoryFile, binary.BigEndian, &start); err != nil {
				return nil, fmt.Errorf("failed to read DIR file at %d: %v", seekPos, err)
			}
			if err := binary.Read(directoryFile, binary.BigEndian, &end); err != nil {
				return nil, fmt.Errorf("failed to read DIR file at %d: %v", seekPos+indexEntrySize, err)
			}
			heightGroupStarts = append(heightGroupStarts, start)
			heightGroupSizes = append(heightGroupSizes, end-start)
		}
	}

	// Get block offsets
	blockOffsets := make([][]uint32, len(heights))
	for i, start := range heightGroupStarts {
		if _, err := offsetFile.Seek(int64(start*indexEntrySize), 0); err != nil {
			return nil, fmt.Errorf("failed to seek OFF file to %d: %v", start*indexEntrySize, err)
		}
		for range heightGroupSizes[i] {
			var offset uint32
			if err := binary.Read(offsetFile, binary.BigEndian, &offset); err != nil {
				return nil, fmt.Errorf("failed to read OFF file: %v", err)
			}
			blockOffsets[i] = append(blockOffsets[i], offset)
		}
	}

	// Read blocks
	blocks := make([][]common.Block, len(heights))
	for i, offsets := range blockOffsets {
		blockByte := make([]byte, blockEntrySize)
		for _, offset := range offsets {
			if _, err := datFile.Seek(int64(offset*blockEntrySize), 0); err != nil {
				return nil, fmt.Errorf("failed to seek DAT file to %d: %v", offset*blockEntrySize, err)
			}
			n, err := datFile.Read(blockByte)
			if n != blockEntrySize || err != nil {
				return nil, fmt.Errorf("failed to read %d bytes from DAT file at %d: %v", blockEntrySize, offset*blockEntrySize, err)
			}
			if !bytes.Equal(blockByte[:magicSize], []byte(magicBytes)) {
				return nil, fmt.Errorf("invalid magic bytes at offset %d", offset)
			}
			block := common.ByteToBlock([common.BlockSize]byte(blockByte[magicSize:]))
			blocks[i] = append(blocks[i], block)
		}
	}
	return blocks, nil
}

// getChainStats returns the latest height and total blocks from file
// Returns: Latest height and total blocks in file, error if reading fails
func readChainStats(datFile, directoryFile *os.File) (int, int, error) {
	// Stat DAT file to determine total blocks
	datStat, err := datFile.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("failed stating DAT file: %v", err)
	}
	totalBlocks := int(datStat.Size() / blockEntrySize)

	// Stat DIR file to determine latest height
	dirStat, err := directoryFile.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("failed stating DIR file: %v", err)
	}
	height := int(dirStat.Size() / indexEntrySize)

	return height, totalBlocks, nil
}

//--------------- QOL FUNCTIONS

// displayIndexFiles creates a visualization of the files for debugging
func displayIndexFiles(datFile, directoryFile, offsetFile *os.File) ([][2]int, [][2]int, [][2]int, error) {
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

	readAllBlocks := func(f *os.File) ([]common.Block, error) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}

		fileStat, err := f.Stat()
		if err != nil {
			return nil, err
		}
		fileSize := fileStat.Size()
		if fileSize%(int64(common.BlockSize)+4) != 0 {
			return nil, fmt.Errorf("fileSize not a multiple of 88")
		}

		numOfBlocks := int(fileSize / (int64(common.BlockSize) + 4))
		var values [][common.BlockSize]byte
		for i := 0; i < numOfBlocks; i++ {
			_, err := f.Seek(4, io.SeekCurrent)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			var v [common.BlockSize]byte
			err = binary.Read(f, binary.BigEndian, &v)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		var blocks []common.Block
		for i := 0; i < len(values); i++ {
			blocks = append(blocks, common.ByteToBlock(values[i]))
		}
		return blocks, nil
	}

	a1, err1 := readAllBlocks(datFile)
	if err1 != nil {
		return nil, nil, nil, err1
	}
	var idxDat [][2]int
	for a := range a1 {
		internal := a1[a].Height
		idxDat = append(idxDat, [2]int{a, internal})
	}

	a2, err2 := readAllUint32(directoryFile)
	if err2 != nil {
		return nil, nil, nil, err2
	}
	var idxDir [][2]int
	for a := range a2 {
		internal := int(a2[a])
		idxDir = append(idxDir, [2]int{a, internal})
	}

	a3, err3 := readAllUint32(offsetFile)
	if err3 != nil {
		return nil, nil, nil, err3
	}

	var idxOff [][2]int
	for a := range a3 {
		internal := int(a3[a])
		idxOff = append(idxOff, [2]int{a, internal})
	}

	return idxDat, idxDir, idxOff, nil
}
