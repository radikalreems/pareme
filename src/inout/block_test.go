package inout

import (
	"os"
	"path/filepath"
	"testing"

	"pareme/src/common"
)

func TestReadOneBlockFixedSize(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "t.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	block := common.GenesisBlock()
	record := append([]byte(MagicBytes), block.ToByte()...)
	if _, err := f.Write(append(append([]byte{}, record...), record...)); err != nil {
		t.Fatal(err)
	}

	gotBytes, got, err := ReadOneBlock(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBytes) != common.BlockSize {
		t.Fatalf("block bytes %d", len(gotBytes))
	}
	if got.Hash() != block.Hash() {
		t.Fatal("hash mismatch")
	}
	if got.BodyHash != block.BodyHash {
		t.Fatal("body hash mismatch")
	}

	_, second, err := ReadOneBlock(f, int64(len(record)))
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash() != block.Hash() {
		t.Fatal("second hash mismatch")
	}
}
