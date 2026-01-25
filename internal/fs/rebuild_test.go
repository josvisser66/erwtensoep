package fs

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRebuildSuperblock(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	archiveDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "alpha.txt"), []byte("alpha"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "beta.txt"), []byte("beta"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	backend := storage.NewMemoryBackend()
	options := Options{
		ArchiveDir:               archiveDir,
		MinSize:                  0,
		MaxSize:                  math.MaxInt64,
		SuperblockWriteThreshold: math.MaxInt64,
		Storage:                  backend,
	}

	sb, err := GetSuperblock(&options)
	if err != nil {
		t.Fatalf("GetSuperblock: %v", err)
	}
	if _, _, err := sb.Merge(srcDir); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := sb.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Rebuild using the same backend and archive directory.
	rebuildOptions := Options{
		ArchiveDir: archiveDir,
		Storage:    backend,
	}
	if err := RebuildSuperblock(&rebuildOptions); err != nil {
		t.Fatalf("RebuildSuperblock: %v", err)
	}

	sb2, err := GetSuperblock(&rebuildOptions)
	if err != nil {
		t.Fatalf("GetSuperblock after rebuild: %v", err)
	}

	if len(sb2.Entries) != len(sb.Entries) {
		t.Fatalf("entries mismatch: %d vs %d", len(sb2.Entries), len(sb.Entries))
	}
	if len(sb2.Blobs) != len(sb.Blobs) {
		t.Fatalf("blobs mismatch: %d vs %d", len(sb2.Blobs), len(sb.Blobs))
	}
	if len(sb2.Blocks) != len(sb.Blocks) {
		t.Fatalf("blocks mismatch: %d vs %d", len(sb2.Blocks), len(sb.Blocks))
	}
}
