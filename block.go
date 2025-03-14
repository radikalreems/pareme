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

type Block struct {
	Height     int
	Timestamp  int64
	PrevHash   [32]byte
	Nonce      int
	Difficulty [32]byte
	BodyHash   [32]byte
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

func adjustDifficulty(i int, requestChan chan readRequest) [32]byte {

	printToLog("Adjusting Difficulty...")

	prev10 := readBlock(i-10, requestChan)
	currentBlock := readBlock(i, requestChan)
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

func verifyBlock(b Block) bool {
	// Genesis block check
	if b.Height == 1 {
		genesis := genesisBlock()
		genesisHash := hashBlock(genesis)
		blockHash := hashBlock(b)
		verified := genesisHash == blockHash
		if !verified {
			printToLog(fmt.Sprintf("Block %d failed verification: genesis block check", b.Height))
		}
		return verified
	}

	f, err := os.Open("blocks/pareme0000.dat")
	if err != nil {
		printToLog(fmt.Sprintf("Error opening dat file in read: %v", err))
		return false
	}
	defer f.Close()

	// Normal block: fetch prior and validate
	prior := readBlockFromFile(f, b.Height-1)
	if prior.Height == 0 {
		return false
	}
	priorHash := hashBlock(prior)
	blockHash := hashBlock(b)

	diffVerified := bytes.Compare(blockHash[:], b.Difficulty[:]) < 0
	prevHashVerified := b.PrevHash == priorHash
	heightVerified := b.Height == prior.Height+1

	if !diffVerified {
		printToLog(fmt.Sprintf("Block %d failed verification: Difficulty check", b.Height))
		return false
	}
	if !prevHashVerified {
		printToLog(fmt.Sprintf("Block %d failed verification: Previous Hash check", b.Height))
		return false
	}
	if !heightVerified {
		printToLog(fmt.Sprintf("Block %d failed verification: Height check", b.Height))
		return false
	}

	// Timestamp check 1: > median of last 11 blocks
	timestamps := []int64{}
	for i := b.Height - 1; i >= maxAB(1, b.Height-11); i-- {
		blk := readBlockFromFile(f, i)
		timestamps = append(timestamps, blk.Timestamp)
	}
	median := medianTimestamp(timestamps)
	if b.Timestamp <= median {
		printToLog(fmt.Sprintf("Block %d failed verification: Timestamp check 1", b.Height))
		return false
	}

	// Timestamp check 2: < now + 2 minutes
	maxTime := time.Now().UnixMilli() + 120000

	time2Verified := b.Timestamp <= maxTime

	if !time2Verified {
		printToLog(fmt.Sprintf("Block %d failed verification: Timestamp check 2", b.Height))
	}

	return time2Verified
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
