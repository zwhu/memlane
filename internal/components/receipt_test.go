package components

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledReceiptMeasuresFilesWithoutFollowingLinks(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "component")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "component.json"), []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	receipt := InstalledReceipt(context.Background(), Descriptor{ID: "test", Bytes: 100, UnpackedBytes: 900}, Installed{Directory: directory, Reused: true})
	if receipt.OccupiedBytes == nil || *receipt.OccupiedBytes != int64(len("metadata")+len("../outside")) || !receipt.Reused || receipt.Downloaded != 0 {
		t.Fatal("measurement used expected/downloaded bytes or followed a link", receipt)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt = InstalledReceipt(ctx, Descriptor{ID: "test"}, Installed{Directory: directory})
	if receipt.OccupiedBytes != nil {
		t.Fatal("interrupted measurement was reported as zero")
	}
}
