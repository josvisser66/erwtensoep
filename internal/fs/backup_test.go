package fs

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestBackupRestoreAndBlobSharing(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	archiveDir := t.TempDir()

	payload := bytes.Repeat([]byte("a"), BlockSize+10)
	diffPayload := bytes.Repeat([]byte("b"), BlockSize+10)

	fileA := filepath.Join(srcDir, "same-a.txt")
	fileB := filepath.Join(srcDir, "same-b.txt")
	fileC := filepath.Join(srcDir, "diff.txt")

	if err := os.WriteFile(fileA, payload, 0600); err != nil {
		t.Fatalf("write %s: %v", fileA, err)
	}
	if err := os.WriteFile(fileB, payload, 0600); err != nil {
		t.Fatalf("write %s: %v", fileB, err)
	}
	if err := os.WriteFile(fileC, diffPayload, 0600); err != nil {
		t.Fatalf("write %s: %v", fileC, err)
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

	rfA := getSingleRegularFile(t, sb, "same-a.txt")
	rfB := getSingleRegularFile(t, sb, "same-b.txt")
	rfC := getSingleRegularFile(t, sb, "diff.txt")

	if rfA.BlobID != rfB.BlobID {
		t.Fatalf("expected identical files to share blob ID, got %s and %s", rfA.BlobID, rfB.BlobID)
	}
	if rfA.BlobID == rfC.BlobID {
		t.Fatalf("expected different file to have different blob ID")
	}

	blob := sb.Blobs[rfA.BlobID]
	if blob == nil {
		t.Fatalf("missing blob %s", rfA.BlobID)
	}
	if len(blob.Blocks) == 0 {
		t.Fatalf("expected blob to have blocks")
	}

	restoredA, err := restoreFromBlob(sb, rfA.BlobID)
	if err != nil {
		t.Fatalf("restore A: %v", err)
	}
	if !bytes.Equal(restoredA, payload) {
		t.Fatalf("restored A does not match original payload")
	}

	restoredB, err := restoreFromBlob(sb, rfB.BlobID)
	if err != nil {
		t.Fatalf("restore B: %v", err)
	}
	if !bytes.Equal(restoredB, payload) {
		t.Fatalf("restored B does not match original payload")
	}

	restoredC, err := restoreFromBlob(sb, rfC.BlobID)
	if err != nil {
		t.Fatalf("restore C: %v", err)
	}
	if !bytes.Equal(restoredC, diffPayload) {
		t.Fatalf("restored C does not match original payload")
	}
}

func getSingleRegularFile(t *testing.T, sb *Superblock, relPath string) *RegularFile {
	t.Helper()

	versions, ok := sb.Entries[relPath]
	if !ok || len(versions) != 1 {
		t.Fatalf("expected 1 version for %s, got %d", relPath, len(versions))
	}
	for _, v := range versions {
		rf, ok := v.(*RegularFile)
		if !ok {
			t.Fatalf("expected RegularFile for %s, got %T", relPath, v)
		}
		return rf
	}
	t.Fatalf("no versions for %s", relPath)
	return nil
}

func restoreFromBlob(sb *Superblock, blobID string) ([]byte, error) {
	blob, ok := sb.Blobs[blobID]
	if !ok {
		return nil, os.ErrNotExist
	}

	var out bytes.Buffer
	for _, blockID := range blob.Blocks {
		key := blobKey(sb.blocksDir, blockID)
		fin, err := sb.storage.GetBlob(key)
		if err != nil {
			return nil, err
		}
		rdr, err := gzip.NewReader(fin)
		if err != nil {
			_ = fin.Close()
			return nil, err
		}
		if _, err := io.Copy(&out, rdr); err != nil {
			_ = rdr.Close()
			_ = fin.Close()
			return nil, err
		}
		if err := rdr.Close(); err != nil {
			_ = fin.Close()
			return nil, err
		}
		if err := fin.Close(); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}
