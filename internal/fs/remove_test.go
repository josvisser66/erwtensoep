package fs

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRemoveSharedBlobs(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	archiveDir := t.TempDir()
	outDir := t.TempDir()

	payload := bytes.Repeat([]byte("z"), BlockSize+32)
	fileA := filepath.Join(srcDir, "a.txt")
	fileB := filepath.Join(srcDir, "b.txt")
	if err := os.WriteFile(fileA, payload, 0600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(fileB, payload, 0600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	options := Options{
		ArchiveDir:               archiveDir,
		MinSize:                  0,
		MaxSize:                  math.MaxInt64,
		SuperblockWriteThreshold: math.MaxInt64,
		Storage:                  storage.NewMemoryBackend(),
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

	rfA := getSingleRegularFile(t, sb, "a.txt")
	rfB := getSingleRegularFile(t, sb, "b.txt")
	if rfA.BlobID != rfB.BlobID {
		t.Fatalf("expected shared blob, got %s and %s", rfA.BlobID, rfB.BlobID)
	}

	if _, err := sb.Remove([]string{"a.txt"}); err != nil {
		t.Fatalf("Remove a: %v", err)
	}
	if _, ok := sb.Blobs[rfA.BlobID]; !ok {
		t.Fatalf("blob removed while still referenced")
	}
	if len(sb.Blocks) == 0 {
		t.Fatalf("blocks removed while still referenced")
	}

	if _, err := sb.Restore([]string{"b.txt"}, outDir); err != nil {
		t.Fatalf("Restore b: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(outDir, "b.txt"))
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatalf("restored content mismatch")
	}

	if _, err := sb.Remove([]string{"b.txt"}); err != nil {
		t.Fatalf("Remove b: %v", err)
	}
	if len(sb.Entries) != 0 || len(sb.Blobs) != 0 || len(sb.Blocks) != 0 {
		t.Fatalf("expected empty superblock after removing all entries")
	}
}
