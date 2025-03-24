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
		Difficulty: [32]byte{0, 0, 50, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
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
	targetTime := float64(2000)
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

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Height < blocks[j].Height
	})

	var verified []Block
	var failed []Block
	var orphaned []Block

	for _, b := range blocks {
		// Genesis block check
		if b.Height == 1 {
			genesis := genesisBlock()
			genesisHash := hashBlock(genesis)
			blockHash := hashBlock(b)
			if genesisHash != blockHash {
				printToLog(fmt.Sprintf("Block %d failed verification: genesis block check", b.Height))
				failed = append(failed, b)
				continue
			}
			verified = append(verified, b)
			continue
		}

		// Difficulty check
		blockHash := hashBlock(b)
		diffVerified := bytes.Compare(blockHash[:], b.Difficulty[:]) < 0
		if !diffVerified {
			printToLog(fmt.Sprintf("failed verification: difficulty check at block %d", b.Height))
			failed = append(failed, b)
			continue
		}

		// Fetch all prior blocks needed (11 prior blocks) by matching hashes
		var heights []int
		for i := b.Height - 1; i >= maxAB(1, b.Height-11); i-- {
			heights = append(heights, i)
		}
		printToLog(fmt.Sprintf("Reading %d blocks from file", len(heights)))
		allPriors, err := readBlocksFromFile(datFile, dirFile, offFile, heights)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read blocks from file: %v", err)
		}
		printToLog(fmt.Sprintf("Recieved blocks from file. first block: %d", allPriors[0][0].Height))

		// Skim down allPriors to just the hash chain. If the first fails it's orphaned, if any other fails it's an error
		var selectPriors []Block
		hashCheck := b.PrevHash
		var orph bool
		for i := 0; i < len(allPriors); i++ {
			found := false
			for _, prior := range allPriors[i] {
				if hashCheck == hashBlock(prior) {
					selectPriors = append(selectPriors, prior)
					hashCheck = prior.PrevHash
					found = true
					break
				}
			}
			if found {
				continue
			}
			if i == 0 {
				orph = true
				break
			}

			return nil, nil, nil, fmt.Errorf("verification error: can't verify hash chain at block %d", b.Height)
		}

		// Orphan check
		if orph {
			printToLog(fmt.Sprintf("failed verification: can't find prevHash - orphan at %d", b.Height))
			orphaned = append(orphaned, b)
			continue
		}

		// Height check
		heightVerified := b.Height == selectPriors[0].Height+1
		if !heightVerified {
			printToLog(fmt.Sprintf("failed verification: height check at block %d", b.Height))
			failed = append(failed, b)
			continue
		}

		// Timestamp check 1: > median of last 11 blocks
		timestamps := []int64{}
		for _, prior := range selectPriors {
			timestamps = append(timestamps, prior.Timestamp)
		}

		median := medianTimestamp(timestamps)
		if b.Timestamp <= median {
			printToLog(fmt.Sprintf("failed verification: timestamp check #1 at block %d", b.Height))
			failed = append(failed, b)
			continue
		}

		// Timestamp check 2: < now + 2 minutes
		maxTime := time.Now().UnixMilli() + 120000
		if b.Timestamp > maxTime {
			printToLog(fmt.Sprintf("failed verification: timestamp check #2 at block %d", b.Height))
			failed = append(failed, b)
			continue
		}

		verified = append(verified, b)

	}

	return verified, failed, orphaned, nil
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
