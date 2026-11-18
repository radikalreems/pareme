package inout

import (
	"bytes"
	"testing"
	"time"

	"pareme/src/common"
)

func TestVerifyGenesisWithoutFiles(t *testing.T) {
	verified, failed, orphans, err := verifyBlocks(nil, []common.Block{common.GenesisBlock()})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 || len(failed) != 0 || len(orphans) != 0 {
		t.Fatalf("verified %d failed %d orphans %d", len(verified), len(failed), len(orphans))
	}
}

func TestVerifyRejectsUnworkableBlock(t *testing.T) {
	genesis := common.GenesisBlock()
	child := common.NewBlock(2, genesis.Hash(), genesis.NBits, [32]byte{})
	child.Nonce = 1
	child.Timestamp = time.Now().UnixMilli()
	// A one-byte target cannot be met by a SHA-256 hash.
	child.NBits = [4]byte{0x01, 0x00, 0x00, 0x01}

	verified, failed, orphans, err := verifyBlocks([]common.Block{genesis}, []common.Block{child})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 0 || len(failed) != 1 || len(orphans) != 0 {
		t.Fatalf("verified %d failed %d orphans %d", len(verified), len(failed), len(orphans))
	}
}

func TestCalculateDifficultyNeedsTheFullWindow(t *testing.T) {
	next := common.GenesisBlock()
	next.Height = 4033
	if _, _, err := CalculateDifficulty(next, nil); err == nil {
		t.Fatal("expected a short history to fail")
	}
}

func TestChainSubmitReleasesTheLock(t *testing.T) {
	chain, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { chain.Close() })

	height, total := chain.Stats()
	if height != 1 || total != 1 {
		t.Fatalf("stats height %d total %d", height, total)
	}
	got := chain.Read([]int{1})
	if len(got) != 1 || len(got[0]) != 1 || got[0][0].Hash() != common.GenesisBlock().Hash() {
		t.Fatal("genesis read mismatch")
	}

	genesis := got[0][0]
	bad := common.NewBlock(2, genesis.Hash(), genesis.NBits, [32]byte{1})
	bad.Nonce = 1
	bad.NBits = [4]byte{0x01, 0x00, 0x00, 0x01}
	if err := chain.Submit([]common.Block{bad}, "mined", nil); err != nil {
		t.Fatal(err)
	}
	if _, total := chain.Stats(); total != 1 {
		t.Fatalf("rejected block was stored, total %d", total)
	}

	child := mineChild(t, genesis)
	if err := chain.Submit([]common.Block{child}, "mined", nil); err != nil {
		t.Fatal(err)
	}
	stored := chain.Read([]int{2})
	if len(stored) != 1 || len(stored[0]) != 1 || stored[0][0].Hash() != child.Hash() {
		t.Fatal("stored child mismatch")
	}
	if h, total := chain.Stats(); h != 2 || total != 2 {
		t.Fatalf("stats height %d total %d", h, total)
	}
}

func mineChild(t *testing.T, parent common.Block) common.Block {
	t.Helper()
	block := common.NewBlock(parent.Height+1, parent.Hash(), parent.NBits, [32]byte{9})
	target := common.NBitsToTarget(block.NBits)
	for i := 1; i <= 2_000_000; i++ {
		block.Nonce = i
		sum := block.Hash()
		if bytes.Compare(sum[:], target[:]) < 0 {
			return block
		}
	}
	t.Fatal("no nonce under the genesis target")
	return common.Block{}
}
