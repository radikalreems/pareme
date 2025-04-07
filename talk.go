package main

import (
	"encoding/binary"
	"fmt"
	"sort"
)

type Message struct {
	Size        uint16 // Total length of the message, 2 bytes
	Kind        uint8  // Request(0) or Response(1), 1 bytes
	Command     uint8  // Specific action (e.g., if a request then 1=latest height, 2=block request), 1 byte
	Reference   uint16 // Unique ID set by requester, echoed in response, 2 bytes
	PayloadSize uint16 // Length of the payload only, 2 bytes
	Payload     []byte // Variable-length data (heights, blocks, etc.)
}

type MessageRequest struct {
	Message         Message
	MsgResponseChan chan Message
}

var nextReferenceNumber uint16 = 0 // Next reference number to use for request messages

func newMessage(kind uint8, command uint8, reference uint16, payload []byte) Message {
	payloadSize := uint16(len(payload))
	totalSize := uint16(8 + len(payload))

	return Message{
		Size:        totalSize,
		Kind:        kind,
		Command:     command,
		Reference:   reference,
		PayloadSize: payloadSize,
		Payload:     payload,
	}
}

func syncToPeers() error {
	if len(AllPeers) == 0 {
		return nil
	}

	// Compare local latest height to peers latest height
	latestHeight, _ := requestChainStats()
	var peerChoice int             // Which peer to ask
	for pos, _ := range AllPeers { // Pick random peer
		peerChoice = pos
		break
	}
	heightReqResponse := requestAMessage(peerChoice, 1, nil) // Height | payload: nil
	printToLog(describeMessage(heightReqResponse))
	latestHeightFromPeer := int(binary.BigEndian.Uint32(heightReqResponse.Payload))
	difference := latestHeightFromPeer - latestHeight
	if difference < 10 {
		printToLog("Already synced with peer!")
		return nil
	}

	printToLog(fmt.Sprintf("Chain is out of sync with peer: latest: %d, peers latest: %d", latestHeight, latestHeightFromPeer))

	chunks := (difference / 100) + 1

	for i := range chunks {
		//Determine chunk range
		startHeight := (latestHeight + 1) + i*100
		endHeight := (latestHeight) + (i+1)*100
		if i == chunks-1 {
			endHeight = latestHeightFromPeer
		}

		var heights []int
		for j := startHeight; j < endHeight+1; j++ {
			heights = append(heights, j)
		}

		// Create / Send / Recieve Message for blocks from peer
		heightsPayload := make([]byte, len(heights)*4)
		for j, value := range heights {
			binary.BigEndian.PutUint32(heightsPayload[j*4:j*4+4], uint32(value))
		}

		var peerChoice int             // Which peer to ask
		for pos, _ := range AllPeers { // Pick random peer
			peerChoice = pos
			break
		}

		blocksResponse := requestAMessage(peerChoice, 2, heightsPayload)

		// Sanity check on recieved Message from peer
		if int(blocksResponse.PayloadSize) != len(blocksResponse.Payload) {
			return fmt.Errorf("response payloadsize is not equal to calculated size")
		}

		// Separate the payload into a slice of blockBytes
		numOfBlocks := int(blocksResponse.PayloadSize / uint16(BlockSize))
		responseBlockBytes := make([][BlockSize]byte, numOfBlocks)
		for j := range numOfBlocks {
			copy(responseBlockBytes[j][:], blocksResponse.Payload[j*BlockSize:(j+1)*BlockSize])
		}

		// Convert the blockBytes into Blocks
		var responseBlocks []Block
		for _, blockByte := range responseBlockBytes {
			responseBlocks = append(responseBlocks, byteToBlock(blockByte))
		}

		blockChan <- responseBlocks

	}

	return nil
}

func broadcastBlock(block Block) {
	printToLog(fmt.Sprintf("Broadcasting Block %d to Pareme....", block.Height))

	// Convert block to a [84]byte
	bb := blockToByte(block)

	// Convert [84]byte to a []byte
	blockByte := bb[:]

	// Send message and wait for response
	var peerChoice int             // Which peer to ask
	for pos, _ := range AllPeers { // Send to all peers
		peerChoice = pos
		response := requestAMessage(peerChoice, 3, blockByte)
		printToLog(describeMessage(response))
	}
}

func respondToMessage(request Message) Message {
	switch request.Command {
	case 0: // Ping request
		// No payload, simple pong response
		return newMessage(1, 0, request.Reference, nil)

	case 1: // Height request
		// No payload, return current chain height as 4-byte uint32
		height, _ := requestChainStats() // Ignore totalBlocks
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(height))
		return newMessage(1, 1, request.Reference, buf)

	case 2: // Block range request
		// Check validity of payload size
		if int(request.PayloadSize) != len(request.Payload) {
			printToLog(fmt.Sprintf("Invalid payload size for ref %d. Says it's %v but it's %v",
				request.Reference, request.PayloadSize, len(request.Payload)))
			return Message{}
		}
		if request.PayloadSize == 0 {
			printToLog(fmt.Sprintf("Empty payload for ref %d", request.Reference))
			return Message{}
		}

		// Extract heights from payload
		len := int(request.PayloadSize / 4)
		heights := make([]int, len)
		for i := range heights {
			value := binary.BigEndian.Uint32(request.Payload[i*4 : i*4+4])
			heights[i] = int(int32(value))
		}

		// Gather requested blocks
		response := requestBlocks(heights)
		var blocks []Block
		for _, heightGroup := range response {
			blocks = append(blocks, heightGroup...)
		}

		// Package blocks
		var payload []byte
		for _, block := range blocks {
			blockByte := blockToByte(block)
			payload = append(payload, blockByte[:]...)
		}
		// Return response with all block data
		return newMessage(1, 2, request.Reference, payload)
	case 3: // Block Broadcast
		// Check validity of payload size
		if int(request.PayloadSize) != len(request.Payload) {
			printToLog(fmt.Sprintf("Invalid payload size for ref %d. Says it's %v but it's %v",
				request.Reference, request.PayloadSize, len(request.Payload)))
			return Message{}
		}
		if request.PayloadSize == 0 {
			printToLog(fmt.Sprintf("Empty payload for ref %d", request.Reference))
			return Message{}
		}

		// Extract block from payload
		block := byteToBlock([BlockSize]byte(request.Payload))

		// Check if duplicate block
		height, _ := requestChainStats()
		if height >= block.Height {
			blocks := requestBlocks([]int{block.Height})[0]
			for _, bloc := range blocks {
				if hashBlock(bloc) == hashBlock(block) {
					// Duplicate block
					// Return a pong Message to send confirmation
					return newMessage(1, 0, request.Reference, nil)
				}
			}
		}

		// Send to writer
		blockPkg := []Block{block}
		blockChan <- blockPkg

		// Return a pong Message to send confirmation
		return newMessage(1, 0, request.Reference, nil)
	}

	// Unknown command
	printToLog(fmt.Sprintf("Unknown command %d for ref %d", request.Command, request.Reference))
	return Message{}
}

func requestAMessage(peerPos int, command uint8, payload []byte) Message {
	msg := newMessage(0, command, nextReferenceNumber, payload)
	nextReferenceNumber++

	msgChan := make(chan Message)
	msgReq := MessageRequest{
		Message:         msg,
		MsgResponseChan: msgChan,
	}

	AllPeers[peerPos].SendChan <- msgReq
	//AllPeers[0].SendChan <- msgReq
	response := <-msgChan

	return response
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
		for _, ref := range referenceBlocks { // REDUNDANCY HERE AND LINE 152?
			refHashes[ref.PrevHash] = true
		}
	}

	// Filter blocks iteratively
	filteredBlocks := make([][]Block, 0, len(sortedBlocks))
	for i, group := range sortedBlocks {
		// Filter group based on current reference hashes
		filteredGroup := make([]Block, 0)
		for _, block := range group {
			if refHashes[hashBlock(block)] || (referenceBlocks == nil && i == 0) { // REDUNDANCY HERE AND LINE 141?
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
		} else {
			return nil, fmt.Errorf("failed to filter height group %d", group[0].Height)
		}
	}

	return filteredBlocks, nil
}

// Later upgrade to syncing
/*

func syncToPeers() error {

	// Only sync if there are peers
	if len(AllPeers) == 0 {
		return nil
	}

	// Compare local latest height to peers latest height
	latestHeight, _ := requestChainStats()
	response := requestAMessage(1, nil) // Height | Payload:nil
	printToLog(describeMessage(response))
	latestHeightFromPeer := int(binary.BigEndian.Uint32(response.Payload))
	difference := latestHeightFromPeer - latestHeight
	if difference > 10 {
		printToLog(fmt.Sprintf("Chain is out of sync with peer: lastest %d | latest from peer %d", latestHeight, latestHeightFromPeer))
	} else {
		printToLog("Already synced with peer!")
		return nil
	}

	// Loop through 100 blocks at a time, starting with the highest - decreasing
	var referenceBlocks []Block
	for i := difference / 100; i >= 0; i-- {
		var heights []int

		// First batch is only up to peers latest height
		cap := min(latestHeight+100*(i+1), latestHeightFromPeer)
		for j := latestHeight + 100*i; j < cap; j++ {
			heights = append(heights, j)
		}

		// Create / Send / Recieve Message for blocks from peer
		heightsPayload := make([]byte, len(heights)*4)
		for i, value := range heights {
			binary.BigEndian.PutUint32(heightsPayload[i*4:i*4+4], uint32(value))
		}
		blocksResponse := requestAMessage(3, heightsPayload)

		// Sanity check on recieved Message from peer
		if int(blocksResponse.PayloadSize) != len(blocksResponse.Payload) {
			return fmt.Errorf("response payloadsize is not equal to calculated size")
		}

		// Separate the payload into a slice of blockBytes
		numOfBlocks := int(blocksResponse.PayloadSize / 84)
		responseBlockBytes := make([][84]byte, numOfBlocks)
		for i := range numOfBlocks {
			copy(responseBlockBytes[i][:], blocksResponse.Payload[i*84:(i+1)*84])
		}

		// Convert the blockBytes into Blocks
		var responseBlocks []Block
		for _, blockByte := range responseBlockBytes {
			responseBlocks = append(responseBlocks, byteToBlock(blockByte))
		}

		// Groups blocks by height into a map
		heightMap := make(map[int][]Block)
		for _, block := range responseBlocks {
			heightMap[block.Height] = append(heightMap[block.Height], block)
		}
		// Creates hgBlocks (height-grouped Blocks) - unordered
		hgBlocks := make([][]Block, 0, len(heightMap))
		for _, group := range heightMap {
			hgBlocks = append(hgBlocks, group)
		}

		// Creates filtered and ordered hgBlocks
		hgBlocks_filtered, err := filterBlocks(hgBlocks, referenceBlocks)
		if err != nil {
			return fmt.Errorf("failed to filter blocks: %v", err)
		}

		// Updates the reference blocks to use next cylce
		referenceBlocks = append(referenceBlocks, hgBlocks_filtered[len(hgBlocks_filtered)-1]...)

		// Send the filtered blocks to be verified and written
		var blocksToWrite []Block
		for _, bloc := range hgBlocks_filtered {
			blocksToWrite = append(blocksToWrite, bloc...)
		}

		blockChan <- blocksToWrite
	}

	return nil
}

*/
