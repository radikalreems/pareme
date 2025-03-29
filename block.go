package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"os"
	"sort"
	"time"
)

type Block struct { // FIELDS: 112 BYTES | IN FILE: 4 MAGIC + 112 = 116 BYTES
	Height     int      // 4 bytes
	Timestamp  int64    // 8 bytes
	PrevHash   [32]byte // 32 bytes
	Nonce      int      // 4 bytes
	Difficulty [32]byte // 32 bytes
	BodyHash   [32]byte // 32 bytes
}

func newBlock(height int, prevHash [32]byte, difficulty [32]byte, bodyHash [32]byte) Block {
	block := Block{
		Height:     height,
		Timestamp:  time.Now().UnixMilli(),
		PrevHash:   prevHash,
		Nonce:      0,
		Difficulty: difficulty,
		BodyHash:   bodyHash,
	}
	return block
}

func genesisBlock() Block {
	block := Block{
		Height:     1,
		Timestamp:  1230940800000,
		PrevHash:   [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Nonce:      0,
		Difficulty: [32]byte{0, 0, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		BodyHash:   [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	return block
}

func hashBlock(b Block) [32]byte {

	// Buffer Size:
	// 4(height) + 8(timestamp) + 32(PrevHash) + 4(Nonce) + 32(Difficulty) + 32(BodyHash)
	buf := make([]byte, 0, 112)
	buf = binary.BigEndian.AppendUint32(buf, uint32(b.Height))
	buf = binary.BigEndian.AppendUint64(buf, uint64(b.Timestamp))
	buf = append(buf, b.PrevHash[:]...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(b.Nonce))
	buf = append(buf, b.Difficulty[:]...)
	buf = append(buf, b.BodyHash[:]...)
	return sha256.Sum256(buf)
}

func adjustDifficulty(i int) [32]byte {

	printToLog("Adjusting Difficulty...")

	prev10 := requestBlocks([]int{i - 10})[0][0]
	currentBlock := requestBlocks([]int{i})[0][0]
	actualTime := float64(currentBlock.Timestamp-prev10.Timestamp) / 10
	targetTime := float64(5000)
	ratio := actualTime / targetTime

	if ratio < 0.25 {
		ratio = 0.25
	} else if ratio > 4 {
		ratio = 4
	}

	printToLog(fmt.Sprintf("%f - Ratio", ratio))

	diffInt := new(big.Int).SetBytes(currentBlock.Difficulty[:])
	ratioFloat := new(big.Float).SetFloat64(ratio)
	newDiffFloat := new(big.Float).SetInt(diffInt)
	newDiffFloat.Mul(newDiffFloat, ratioFloat)
	newDiffInt, _ := newDiffFloat.Int(nil)

	result := newDiffInt.Bytes()
	var difficulty [32]byte
	if len(result) > 32 {
		copy(difficulty[:], result[len(result)-32:])
	} else {
		copy(difficulty[32-len(result):], result)
	}

	printToLog(fmt.Sprintf("%x - Old Difficulty", currentBlock.Difficulty[:8]))
	printToLog(fmt.Sprintf("%x - New Difficulty\n", difficulty[:8]))

	return difficulty
}

func verifyBlocks(datFile, dirFile, offFile *os.File, blocks []Block) ([]Block, []Block, []Block, error) {

	var verified []Block
	var failed []Block
	var orphaned []Block

	// Check for genesis
	if len(blocks) == 1 && blocks[0].Height == 1 {
		genesis := genesisBlock()
		genesisHash := hashBlock(genesis)
		blockHash := hashBlock(blocks[0])
		if genesisHash != blockHash {
			printToLog("Block 1 failed verification: genesis block check")
			failed = append(failed, blocks[0])

		} else {
			verified = append(verified, blocks[0])
		}
		return verified, failed, orphaned, nil
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Height < blocks[j].Height
	})

	// Check if it is continuous
	if len(blocks) > 1 {
		for i := 1; i < len(blocks); i++ {
			prevHeight := blocks[i-1].Height
			currHeight := blocks[i].Height
			if currHeight != prevHeight && currHeight != prevHeight+1 {
				return nil, nil, nil, fmt.Errorf("block slice is not continuous, cannot verifiy")
			}
		}
	}

	// If we can't get the last 11 blocks (due to being at the start of the chain) record the amount missing
	var cutoffAmt int
	if maxAB(1, blocks[0].Height-11) == 1 {
		cutoffAmt = 1 - (blocks[0].Height - 11)
	} else {
		cutoffAmt = 0
	}

	// Fetch the last 11 blocks from chain
	var inFileHeights []int
	for i := maxAB(1, blocks[0].Height-11); i < blocks[0].Height; i++ {
		inFileHeights = append(inFileHeights, i)
	}
	response, err := readBlocksFromFile(datFile, dirFile, offFile, inFileHeights)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch inFileBlocks")
	}
	var inFileBlocks []Block
	for _, group := range response {
		inFileBlocks = append(inFileBlocks, group...)
	}

	type pendBlock struct {
		Block    Block
		Verified bool
		Original bool
		Hash     [32]byte
	}

	var pendingBlocks []pendBlock

	for _, b := range inFileBlocks {
		bloc := pendBlock{Block: b, Verified: true, Original: true, Hash: hashBlock(b)}
		pendingBlocks = append(pendingBlocks, bloc)
	}
	for _, b := range blocks {
		bloc := pendBlock{Block: b, Verified: false, Original: false, Hash: hashBlock(b)}
		pendingBlocks = append(pendingBlocks, bloc)
	}

	for i, b := range pendingBlocks {
		if b.Original {
			continue
		}

		// Difficulty check
		blockHash := hashBlock(b.Block)
		diffVerified := bytes.Compare(blockHash[:], b.Block.Difficulty[:]) < 0
		if !diffVerified {
			printToLog(fmt.Sprintf("failed verification: difficulty check at block %d", b.Block.Height))
			failed = append(failed, b.Block)
			continue
		}

		// PrevHash & Height Check
		var prevBlock []pendBlock
		for _, prevBloc := range pendingBlocks {
			if prevBloc.Verified && prevBloc.Hash == b.Block.PrevHash {
				prevBlock = append(prevBlock, prevBloc)
				break
			}
		}
		if len(prevBlock) == 0 {
			printToLog(fmt.Sprintf("failed verification: orphan at block %d", b.Block.Height))
			orphaned = append(orphaned, b.Block)
			continue
		}
		if prevBlock[0].Block.Height+1 != b.Block.Height {
			printToLog(fmt.Sprintf("failed verification: height check at block %d", b.Block.Height))
			failed = append(failed, b.Block)
			continue
		}

		// Timestamp check 1: > median of last 11 blocks (minus cutoff if we are at the start of the chain)
		var timestamps []int64
		referenceBlock := b.Block
		for {
			if len(timestamps) == (11 - cutoffAmt) {
				break
			}
			found := false
			for _, prevBloc := range pendingBlocks {
				if prevBloc.Verified && prevBloc.Hash == referenceBlock.PrevHash {
					found = true
					timestamps = append(timestamps, prevBloc.Block.Timestamp)
					referenceBlock = prevBloc.Block
				}
			}
			if !found {
				return nil, nil, nil, fmt.Errorf("failed to located prevHash chain for block %d at reference block %d", b.Block.Height, referenceBlock.Height)
			}
		}

		median := medianTimestamp(timestamps)
		if b.Block.Timestamp <= median {
			printToLog(fmt.Sprintf("failed verification: timestamp check #1 at block %d", b.Block.Height))
			failed = append(failed, b.Block)
			continue
		}

		// Timestamp check 2: < now + 2 minutes
		maxTime := time.Now().UnixMilli() + 120000
		if b.Block.Timestamp > maxTime {
			printToLog(fmt.Sprintf("failed verification: timestamp check #2 at block %d", b.Block.Height))
			failed = append(failed, b.Block)
			continue
		}

		verified = append(verified, b.Block)
		pendingBlocks[i].Verified = true

	}

	return verified, failed, orphaned, nil
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

func medianTimestamp(ts []int64) int64 {
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	n := len(ts)
	if n == 0 {
		return 0
	}
	if n%2 == 0 {
		return (ts[n/2-1] + ts[n/2]) / 2
	}
	return ts[n/2]
}

func maxAB(a, b int) int {
	if a > b {
		return a
	}
	return b
}
