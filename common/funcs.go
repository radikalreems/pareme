package common

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"time"
)

// METHODS
func (b Block) ToByte() [BlockSize]byte {
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

func (b Block) Hash() [32]byte {
	buf := b.ToByte()
	return sha256.Sum256(buf[:])
}

// FUNCTIONS

// requestBlocks requests blocks from the writer by heights
// Returns: Blocks request by height
func RequestBlocks(heights []int) [][]Block {
	// Create channel to receive response
	responseChan := make(chan [][]Block)

	// Request heights from writer
	RequestChan <- ReadRequest{Heights: heights, Response: responseChan}

	// Return response after waiting
	return <-responseChan
}

// requestChainStats requests chain stats from the writer
// Returns: Latest height and total blocks
func RequestChainStats() (int, int) {
	// Create channel to receive response
	responseChan := make(chan [2]uint32)

	// Send request
	IndexRequestChan <- responseChan

	// Wait for response and return
	result := <-responseChan
	return int(result[0]), int(result[1])
}

func NewBlock(height int, prevHash [32]byte, nBits [4]byte, bodyHash [32]byte) Block {
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

func GenesisBlock() Block {
	block := Block{
		Height:    1,
		Timestamp: 1230940800000,
		PrevHash:  [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Nonce:     0,
		NBits:     MaxTargetNBits,
		BodyHash:  [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	return block
}

func ByteToBlock(data [BlockSize]byte) Block {
	var block Block
	block.Height = int(binary.BigEndian.Uint32(data[:4]))
	block.Timestamp = int64(binary.BigEndian.Uint64(data[4:12]))
	copy(block.PrevHash[:], data[12:44])
	copy(block.NBits[:], data[44:48])
	block.Nonce = int(binary.BigEndian.Uint32(data[48:52]))
	copy(block.BodyHash[:], data[52:84])
	return block
}

func BlockToByte(b Block) [BlockSize]byte {
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

func NBitsToTarget(nBits [4]byte) [32]byte {
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

func TargetToNBits(target [32]byte) [4]byte {
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
