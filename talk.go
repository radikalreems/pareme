package main

import (
	"encoding/binary"
	"fmt"
	"sort"
)

func syncToPeers() error {

	if len(AllPeers) == 0 {
		return nil
	}

	latestHeight, _ := requestChainStats()
	response := requestAMessage(1, nil) // Height | Payload:nil
	printToLog(describeMessage(response))
	latestHeightFromPeer := int(binary.BigEndian.Uint32(response.Payload))

	difference := latestHeightFromPeer - latestHeight
	if difference > 3 {
		printToLog(fmt.Sprintf("Chain is out of sync with peer: lastest %d | latest from peer %d", latestHeight, latestHeightFromPeer))
	}

	var referenceBlocks []Block
	for i := difference / 100; i >= 0; i-- {
		var heights []int
		for j := latestHeight + 100*i; j < latestHeight+100*(i+1); j++ {
			heights = append(heights, j)
		}
		blocksResponse := requestBlocks(heights)
		blocks, err := filterBlocks(blocksResponse, referenceBlocks)
		if err != nil {
			return fmt.Errorf("failed to filter blocks: %v", err)
		}
		referenceBlocks = append(referenceBlocks, blocks[len(blocks)-1]...)
		var blocksToWrite []Block
		for _, bloc := range blocks {
			blocksToWrite = append(blocksToWrite, bloc...)
		}

		blockChan <- blocksToWrite
	}

	return nil
}

func filterBlocks(allBlocks [][]Block, referenceBlocks []Block) ([][]Block, error) {

	// Create a copy to avoid modifying the original
	sortedBlocks := make([][]Block, len(allBlocks))
	copy(sortedBlocks, allBlocks)

	// Sort by the height of the first block in each group (assuming all blocks in a group have the same height)
	sort.Slice(sortedBlocks, func(i, j int) bool {
		return sortedBlocks[i][0].Height > sortedBlocks[j][0].Height // Decreasing order
	})

	// Check for height gaps
	for i := 1; i < len(sortedBlocks); i++ {
		if sortedBlocks[i-1][0].Height-sortedBlocks[i][0].Height != 1 {
			return nil, fmt.Errorf("height gap detected between %d and %d",
				sortedBlocks[i-1][0].Height, sortedBlocks[i][0].Height)
		}
	}

	// Get reference PrevHashes as a map of [32]byte
	refHashes := make(map[[32]byte]bool)
	if referenceBlocks == nil {
		// Use all blocks from the first (highest) group as reference
		for _, block := range sortedBlocks[0] {
			refHashes[hashBlock(block)] = true
		}
	} else {
		for _, ref := range referenceBlocks {
			refHashes[ref.PrevHash] = true
		}
	}

	// Filter blocks iteratively
	filteredBlocks := make([][]Block, 0, len(sortedBlocks))
	for i, group := range sortedBlocks {
		// Filter group based on current reference hashes
		filteredGroup := make([]Block, 0)
		for _, block := range group {
			if refHashes[hashBlock(block)] || (referenceBlocks == nil && i == 0) {
				filteredGroup = append(filteredGroup, block)
			}
		}

		// If we got any blocks, add to result and update reference hashes
		if len(filteredGroup) > 0 {
			filteredBlocks = append(filteredBlocks, filteredGroup)
			// Update refHashes with PrevHashes from this group for the next iteration
			if i < len(sortedBlocks)-1 { // only if there's a next group
				refHashes = make(map[[32]byte]bool)
				for _, block := range filteredGroup {
					refHashes[block.PrevHash] = true
				}
			}
		}
	}

	return filteredBlocks, nil
}
