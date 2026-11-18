package inout

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"pareme/src/common"
	"pareme/src/network"
	"path/filepath"
	"sync"
)

// Chain owns the block files, the index, and the orphan list.
// Submit, Read, and Stats take the lock. Consensus checks never see these files.
type Chain struct {
	mu      sync.Mutex
	dat     *os.File
	dir     *os.File
	off     *os.File
	orphans []common.Block
	heights chan []int
	dirPath string
	offPath string
}

// Open creates the chain files in dir when they are missing, writes genesis into an empty data file, and keeps the files open.
// heights receives accepted heights while MiningState.Active is set. It may be nil.
func Open(dir string, heights chan []int) (*Chain, error) {
	common.PrintToLog("Initializing files")

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("failed creating directory %s: %v", dir, err)
	}

	datPath := filepath.Join(dir, common.DataFile)
	datFile, err := os.OpenFile(datPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, filePerm)
	if err != nil {
		return nil, fmt.Errorf("failed creating %s: %v", datPath, err)
	}

	dirPath := filepath.Join(dir, common.DirIndexFile)
	dirFile, err := os.OpenFile(dirPath, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		datFile.Close()
		return nil, fmt.Errorf("failed creating %s: %v", dirPath, err)
	}

	offPath := filepath.Join(dir, common.OffIndexFile)
	offFile, err := os.OpenFile(offPath, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		datFile.Close()
		dirFile.Close()
		return nil, fmt.Errorf("failed creating %s: %v", offPath, err)
	}

	c := &Chain{
		dat:     datFile,
		dir:     dirFile,
		off:     offFile,
		heights: heights,
		dirPath: dirPath,
		offPath: offPath,
	}

	datStat, err := datFile.Stat()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("failed stating %s: %v", datPath, err)
	}
	if datStat.Size() == 0 {
		if err := c.writeGenesis(datPath); err != nil {
			c.Close()
			return nil, err
		}
	}

	tip, total, err := readChainStats(dirFile, offFile)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("failed to read chain stats: %v", err)
	}
	common.PrintToLog(fmt.Sprintf("Synced to height %d with %d total blocks", tip, total))
	return c, nil
}

func (c *Chain) writeGenesis(datPath string) error {
	blocks := []common.Block{common.GenesisBlock()}
	if err := writeBlocks(c.dat, c.dir, c.off, blocks); err != nil {
		return fmt.Errorf("failed to write genesis: %v", err)
	}
	common.PrintToLog(fmt.Sprintf("Created %s with genesis", datPath))

	if err := os.Truncate(c.dirPath, 0); err != nil {
		return fmt.Errorf("failed to truncate DIR file: %v", err)
	}
	if err := os.Truncate(c.offPath, 0); err != nil {
		return fmt.Errorf("failed to truncate OFF file: %v", err)
	}

	if _, err := c.dir.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek DIR file: %v", err)
	}
	if err := binary.Write(c.dir, binary.BigEndian, uint32(1)); err != nil {
		return fmt.Errorf("failed to write the init of DIR file: %v", err)
	}

	if _, err := c.off.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek OFF file: %v", err)
	}
	if err := binary.Write(c.off, binary.BigEndian, []byte{0, 0, 0, 0, 0}); err != nil {
		return fmt.Errorf("failed to write the init of OFF file: %v", err)
	}
	return nil
}

// Close closes the chain files.
func (c *Chain) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if c.dat != nil {
		err = c.dat.Close()
		c.dat = nil
	}
	if c.dir != nil {
		if closeErr := c.dir.Close(); err == nil {
			err = closeErr
		}
		c.dir = nil
	}
	if c.off != nil {
		if closeErr := c.off.Close(); err == nil {
			err = closeErr
		}
		c.off = nil
	}
	return err
}

// Read returns one group of blocks per height. A failed read is logged and comes back empty.
func (c *Chain) Read(heights []int) [][]common.Block {
	c.mu.Lock()
	defer c.mu.Unlock()
	groups, err := readBlocks(c.dat, c.dir, c.off, heights)
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Error reading blocks for heights %v: %v", heights, err))
		return nil
	}
	return groups
}

// Stats returns the latest height and the number of stored blocks. A failed read is logged and comes back as zeros.
func (c *Chain) Stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	height, total, err := readChainStats(c.dir, c.off)
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Error retreiving chain stats: %v", err))
		return 0, 0
	}
	return height, total
}

// Submit verifies blocks, appends the ones that pass, and broadcasts them.
// kind is "sync", "received", or "mined", and selects who is skipped on broadcast.
// A file error is returned. A block that fails verification is not an error.
func (c *Chain) Submit(blocks []common.Block, kind string, from *common.Peer) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	toVerify := make([]common.Block, 0, len(blocks)+len(c.orphans))
	toVerify = append(toVerify, blocks...)
	toVerify = append(toVerify, c.orphans...)

	common.PrintToLog(fmt.Sprintf("\nwriter: Recieved %d new blocks. verifying...", len(blocks)))

	fileBlocks, err := c.ancestorBlocks(toVerify)
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Error verifying blocks: %v", err))
		return err
	}

	verified, failed, orphans, err := verifyBlocks(fileBlocks, toVerify)
	c.orphans = orphans
	if err != nil {
		common.PrintToLog(fmt.Sprintf("Error verifying blocks: %v", err))
	}
	common.PrintToLog(fmt.Sprintf("%d verified | %d failed | %d orphaned", len(verified), len(failed), len(orphans)))
	if len(verified) == 0 {
		return nil
	}

	if err := writeBlocks(c.dat, c.dir, c.off, verified); err != nil {
		common.PrintToLog(fmt.Sprintf("failed to write %d blocks: %v", len(verified), err))
		return err
	}
	common.PrintToLog(fmt.Sprintf("Successfully wrote %d new blocks!", len(verified)))

	var exclusion []*common.Peer
	if kind == "sync" {
		for i := range common.AllPeers {
			exclusion = append(exclusion, common.AllPeers[i])
		}
	} else if kind == "received" || kind == "mined" {
		exclusion = []*common.Peer{from}
	}
	for _, block := range verified {
		go network.BroadcastBlock(block, exclusion)
	}

	if common.MiningState.Active && c.heights != nil {
		note := make([]int, 0, len(verified))
		for _, v := range verified {
			note = append(note, v.Height)
		}
		// The miner may be the caller of Submit, so the send cannot run on this goroutine.
		go func() { c.heights <- note }()
	}
	return nil
}

// ancestorBlocks loads the same prior heights verifyBlocks used to read itself.
// A lone genesis block needs none. Any other batch is read, including an empty height list, so the old read error still surfaces.
func (c *Chain) ancestorBlocks(blocks []common.Block) ([]common.Block, error) {
	if len(blocks) == 1 && blocks[0].Height == 1 {
		return nil, nil
	}
	minH := blocks[0].Height
	for _, b := range blocks[1:] {
		if b.Height < minH {
			minH = b.Height
		}
	}
	fileHeights := make([]int, 0, 11)
	for i := maxAB(1, minH-11); i < minH; i++ {
		fileHeights = append(fileHeights, i)
	}
	groups, err := readBlocks(c.dat, c.dir, c.off, fileHeights)
	if err != nil {
		return nil, fmt.Errorf("failed to read blocks: %v", err)
	}
	var flat []common.Block
	for _, group := range groups {
		flat = append(flat, group...)
	}
	return flat, nil
}
