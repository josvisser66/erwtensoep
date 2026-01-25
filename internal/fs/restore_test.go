package fs

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRestoreLatestAndPatterns(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	archiveDir := t.TempDir()
	outDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(srcDir, "dir"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "dir", "file.txt"), []byte("one"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "root.txt"), []byte("root"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink("dir/file.txt", filepath.Join(srcDir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
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

	if _, err := sb.Restore([]string{"dir/"}, outDir); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := filepath.Join(outDir, "dir", "file.txt")
	data, err := os.ReadFile(restored)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(data, []byte("one")) {
		t.Fatalf("restored content mismatch: %q", string(data))
	}

	linkPath := filepath.Join(outDir, "link")
	if _, err := os.Lstat(linkPath); err == nil {
		t.Fatalf("unexpected symlink restored from directory-only pattern")
	}

	if _, err := sb.Restore([]string{"link"}, outDir); err != nil {
		t.Fatalf("Restore link: %v", err)
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "dir/file.txt" {
		t.Fatalf("unexpected link target: %q", target)
	}
}
