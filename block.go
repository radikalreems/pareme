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
	BlockSize int = 84
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
func determineDifficulty(nextBlock Block) ([32]byte, [4]byte, error) {
	printToLog("Determining Difficulty...")

	ratio, err := determineRatio(nextBlock)
	if err != nil {
		return [32]byte{}, [4]byte{}, fmt.Errorf("failed determining ratio: %v", err)
	}

	prevTarget := nBitsToTarget(nextBlock.NBits) // NBits -> Target

	prevBig := new(big.Int).SetBytes(prevTarget[:])                            // Target -> BigInt Target
	ratioBig := big.NewFloat(ratio)                                            // Ratio -> BigFloat Ratio
	targetBig := new(big.Float).Mul(big.NewFloat(0).SetInt(prevBig), ratioBig) // BigTarget * BigRatio
	newTargetInt, _ := targetBig.Int(nil)                                      // BigFloat Target -> BigInt Target
	newTargetBytes := newTargetInt.Bytes()                                     // BigInt Target -> []byte Target

	// Fit []byte Target into [32]byte
	newTarget := [32]byte{}
	if len(newTargetBytes) > 32 {
		// length of Target is > 32, truncate to the rightmost
		copy(newTarget[:], newTargetBytes[len(newTargetBytes)-32:])
	} else {
		// length of Target is <= 32, align by rightmost
		copy(newTarget[32-len(newTargetBytes):], newTargetBytes)
	}

	if bytes.Compare(newTarget[:], MaxTarget[:]) > 0 {
		newTarget = MaxTarget
	}

	nBits := targetToNBits(newTarget) // Target -> NBits

	return newTarget, nBits, nil

}

// Complete verfication steps against given blocks
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
		blockDifficulty := nBitsToTarget(b.Block.NBits)
		diffVerified := bytes.Compare(blockHash[:], blockDifficulty[:]) < 0
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
