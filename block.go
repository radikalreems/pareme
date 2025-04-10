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

type Block struct { // FIELDS: 84 BYTES | IN FILE: 4 MAGIC + 84 = 88 BYTES
	Height    int      // 4 bytes
	Timestamp int64    // 8 bytes
	PrevHash  [32]byte // 32 bytes
	NBits     [4]byte  // 4 bytes
	Nonce     int      // 4 bytes
	BodyHash  [32]byte // 32 bytes
}

const (
	BlockSize int = 84 // Size of a block (not including 4 byte magic when written)
)

var (
	MaxTarget = [32]byte{0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
)

func newBlock(height int, prevHash [32]byte, nBits [4]byte, bodyHash [32]byte) Block {
	block := Block{
		Height:    height,
		Timestamp: time.Now().UnixMilli(),
		PrevHash:  prevHash,
		Nonce:     0,
		NBits:     nBits,
		BodyHash:  bodyHash,
	}
	return block
}

func genesisBlock() Block {
	block := Block{
		Height:    1,
		Timestamp: 1230940800000,
		PrevHash:  [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Nonce:     0,
		NBits:     [4]byte{0x1e, 0xFF, 0xFF, 0x00},
		BodyHash:  [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	return block
}

// Given a potential block, calculate the expected difficulty target.
func calculateDifficulty(nextBlock Block) ([32]byte, [4]byte, error) {
	printToLog("Calculating Difficulty...")

	// Get adjustment ratio for difficulty
	ratio, err := determineRatio(nextBlock)
	if err != nil {
		return [32]byte{}, [4]byte{}, fmt.Errorf("ratio calculation failed: %v", err)
	}

	// Convert previous nBits to target
	prevTarget := nBitsToTarget(nextBlock.NBits)

	// Perform target adjustment calculation
	prevInt := new(big.Int).SetBytes(prevTarget[:])
	ratioFloat := big.NewFloat(ratio)
	newTargetFloat := new(big.Float).Mul(big.NewFloat(0).SetInt(prevInt), ratioFloat)
	newTargetInt, _ := newTargetFloat.Int(nil)
	newTargetBytes := newTargetInt.Bytes()

	// Fit target into 32-byte array
	var newTarget [32]byte
	if len(newTargetBytes) > 32 {
		// Truncate to rightmost 32 bytes if too large
		copy(newTarget[:], newTargetBytes[len(newTargetBytes)-32:])
	} else {
		// Right-align bytes if 32 or fewer
		copy(newTarget[32-len(newTargetBytes):], newTargetBytes)
	}

	// Ensure target doesn't exceed maximum
	if bytes.Compare(newTarget[:], MaxTarget[:]) > 0 {
		newTarget = MaxTarget
	}

	// Convert target back to nBits
	nBits := targetToNBits(newTarget)

	return newTarget, nBits, nil

}

// Complete verfication steps against given blocks
func verifyBlocks(datFile, dirFile, offFile *os.File, blocks []Block) ([]Block, []Block, []Block, error) {
	var verified, failed, orphaned []Block

	// Handle genesis block case
	if len(blocks) == 1 && blocks[0].Height == 1 {
		genesis := genesisBlock()
		if hashBlock(genesis) != hashBlock(blocks[0]) {
			printToLog("Block 1 failed verification: genesis block check")
			failed = append(failed, blocks[0])
		} else {
			verified = append(verified, blocks[0])
		}
		return verified, failed, orphaned, nil
	}

	// Sort blocks by height, ascending
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Height < blocks[j].Height
	})

	// Batched verification requires continuous heights. Check continuity here
	if len(blocks) > 1 {
		for i := 1; i < len(blocks); i++ {
			if blocks[i].Height == blocks[i-1].Height {
				continue
			}
			if blocks[i].Height != blocks[i-1].Height+1 {
				return nil, nil, nil, fmt.Errorf("discontinuous blocks, cannot verify")
			}
		}
	}

	// Calculate cutoff for initial chain blocks
	cutoff := 0
	if maxAB(1, blocks[0].Height-11) == 1 {
		cutoff = 1 - (blocks[0].Height - 11)
	}

	// Fetch last 11 blocks from chain
	fileHeights := make([]int, 0, 11-cutoff)
	for i := maxAB(1, blocks[0].Height-11); i < blocks[0].Height; i++ { // Gather heights
		fileHeights = append(fileHeights, i)
	}
	fileBlocksResp, err := readBlocks(datFile, dirFile, offFile, fileHeights) // Read blocks from file
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch inFileBlocks")
	}
	var fileBlocks []Block
	for _, group := range fileBlocksResp {
		fileBlocks = append(fileBlocks, group...) // flatten the response
	}

	// Prepare blocks for verification
	type pendingBlock struct {
		block    Block
		verified bool
		original bool
		hash     [32]byte
	}

	pending := make([]pendingBlock, 0, len(fileBlocks)+len(blocks))
	for _, b := range fileBlocks {
		pending = append(pending, pendingBlock{block: b, verified: true, original: true, hash: hashBlock(b)})
	}
	for _, b := range blocks {
		pending = append(pending, pendingBlock{block: b, verified: false, original: false, hash: hashBlock(b)})
	}

	// Verify each non-original block
	for i, pb := range pending {
		if pb.original { // Skip originals
			continue
		}

		// Difficulty check
		target := nBitsToTarget(pb.block.NBits)
		if bytes.Compare(pb.hash[:], target[:]) >= 0 {
			printToLog(fmt.Sprintf("block %d failed difficulty check", pb.block.Height))
			failed = append(failed, pb.block)
			continue
		}

		// PrevHash & Height Check
		var prevBlock pendingBlock
		for _, candidate := range pending {
			if candidate.verified && candidate.hash == pb.block.PrevHash {
				prevBlock = candidate
				break
			}
		}
		var zero pendingBlock
		if prevBlock == zero {
			printToLog(fmt.Sprintf("block %d is orphaned", pb.block.Height))
			orphaned = append(orphaned, pb.block)
			continue
		}
		if prevBlock.block.Height+1 != pb.block.Height {
			printToLog(fmt.Sprintf("block %d failed height check", pb.block.Height))
			failed = append(failed, pb.block)
			continue
		}

		// Timestamp check 1: check against median of last 11 blocks
		timestamps := make([]int64, 0, 11-cutoff)
		refBlock := pb.block
		for len(timestamps) < 11-cutoff {
			found := false
			for _, prev := range pending {
				if prev.verified && prev.hash == refBlock.PrevHash {
					timestamps = append(timestamps, prev.block.Timestamp)
					refBlock = prev.block
					found = true
					break
				}
			}
			if !found {
				return nil, nil, nil, fmt.Errorf("failed to locate prevHash chain for block %d at reference block %d",
					pb.block.Height, refBlock.Height)
			}
		}
		if pb.block.Timestamp < medianTimestamp(timestamps) { // Make this "<=" for final version to limit stagnation
			printToLog(fmt.Sprintf("failed verification: timestamp check #1 at block %d | timestamp is %v | median is %v", pb.block.Height, pb.block.Timestamp, medianTimestamp(timestamps)))
			failed = append(failed, pb.block)
			continue
		}

		// Timestamp check 2: < now + 2 minutes
		maxTime := time.Now().UnixMilli() + 120000
		if pb.block.Timestamp > maxTime {
			printToLog(fmt.Sprintf("failed verification: timestamp check #2 at block %d | timestamp is %v | maxTime is %v", pb.block.Height, pb.block.Timestamp, maxTime))
			failed = append(failed, pb.block)
			continue
		}

		verified = append(verified, pb.block)
		pending[i].verified = true

	}

	return verified, failed, orphaned, nil
}

//--------------- QOL FUNCTIONS

func byteToBlock(data [BlockSize]byte) Block {
	var block Block
	block.Height = int(binary.BigEndian.Uint32(data[:4]))
	block.Timestamp = int64(binary.BigEndian.Uint64(data[4:12]))
	copy(block.PrevHash[:], data[12:44])
	copy(block.NBits[:], data[44:48])
	block.Nonce = int(binary.BigEndian.Uint32(data[48:52]))
	copy(block.BodyHash[:], data[52:84])
	return block
}

func blockToByte(b Block) [BlockSize]byte {
	var result [BlockSize]byte

	// Height: int (4 bytes)
	binary.BigEndian.PutUint32(result[0:4], uint32(b.Height))

	// Timestamp: int 64 (8 bytes)
	binary.BigEndian.PutUint64(result[4:12], uint64(b.Timestamp))

	// PrevHash: [32]byte (32 bytes)
	copy(result[12:44], b.PrevHash[:])

	// NBits: [4]byte (4 bytes)
	copy(result[44:48], b.NBits[:])

	// Nonce: int (4 bytes)
	binary.BigEndian.PutUint32(result[48:52], uint32(b.Nonce))

	// BodyHash: [32]byte (32 bytes)
	copy(result[52:84], b.BodyHash[:])

	return result
}

func nBitsToTarget(nBits [4]byte) [32]byte {
	size := int(nBits[0])
	mantissa := uint32(nBits[1])<<16 | uint32(nBits[2])<<8 | uint32(nBits[3])

	target := new(big.Int).SetUint64(uint64(mantissa))
	if size > 3 {
		target.Lsh(target, uint(8*(size-3)))
	} else if size < 3 {
		target.Rsh(target, uint(8*(3-size)))
	}

	bytes := target.Bytes()
	result := [32]byte{}
	if len(bytes) > 32 {
		copy(result[:], bytes[len(bytes)-32:])
	} else {
		copy(result[32-len(bytes):], bytes)
	}

	return result
}

func targetToNBits(target [32]byte) [4]byte {
	t := new(big.Int).SetBytes(target[:])
	bytes := t.Bytes()
	size := min(len(bytes), 255)

	var mantissa uint32
	if size == 0 {
		return [4]byte{}
	} else if size <= 3 {
		mantissa = uint32(t.Uint64()) << (8 * (3 - size))
	} else {
		shifted := new(big.Int).Rsh(t, uint(8*(size-3)))
		mantissa = uint32(shifted.Uint64())
	}

	return [4]byte{
		byte(size),
		byte(mantissa >> 16),
		byte(mantissa >> 8),
		byte(mantissa),
	}
}

func hashBlock(b Block) [32]byte {
	buf := blockToByte(b)
	return sha256.Sum256(buf[:])
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

func determineRatio(block Block) (float64, error) {
	// Gather the last 2016 blocks
	var heights []int
	for i := block.Height - 2017; i < block.Height; i++ {
		heights = append(heights, i)
	}
	printToLog(fmt.Sprintf("length of requested heights: %v | first & last height: %v, %v",
		len(heights), heights[0], heights[len(heights)-1]))

	unfilteredPrevBlocks := requestBlocks(heights)

	if len(heights) != len(unfilteredPrevBlocks) {
		return 0, fmt.Errorf("failed equality check on requested heights to blocks")
	}

	blocks := []Block{block}
	prevBlocks, err := filterBlocks(unfilteredPrevBlocks, blocks)
	if err != nil {
		return 0, fmt.Errorf("failed to filter previous blocks: %v", err)
	}

	printToLog(fmt.Sprintf("len of filtered heights: %v, first & last heights: %v, %v",
		len(prevBlocks), prevBlocks[0][0].Height, prevBlocks[len(prevBlocks)-1][0].Height))

	firstTime := prevBlocks[len(prevBlocks)-1][0].Timestamp

	sum := prevBlocks[0][0].Timestamp - firstTime

	avg := float64(sum) / 2016

	printToLog(fmt.Sprintf("average time: %v | ratio: %v", avg/1000, (avg/1000)/5))
	printToLog(fmt.Sprintf("firstTime: %v | sum: %v", firstTime, sum))

	ratio := float64(sum) / 20160000

	if ratio < 0.25 {
		ratio = 0.25
	}
	if ratio > 4 {
		ratio = 4
	}

	return ratio, nil
}
