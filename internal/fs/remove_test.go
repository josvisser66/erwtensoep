package fs

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func removalArchive(t *testing.T, backend storage.Backend) (*Superblock, *Options) {
	t.Helper()
	src := t.TempDir()
	common := bytes.Repeat([]byte("a"), BlockSize)
	for name, tail := range map[string]string{"literal*.txt": "same", "literal-other.txt": "same", "partial.txt": "different"} {
		data := append(append([]byte(nil), common...), tail...)
		if err := os.WriteFile(filepath.Join(src, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	opts := &Options{ArchiveDir: t.TempDir(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64, Storage: backend}
	sb, err := GetSuperblock(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb.Merge(src); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(src, "literal*.txt")
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filename, info.ModTime(), info.ModTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb.Merge(src); err != nil {
		t.Fatal(err)
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	sb, err = GetSuperblock(opts)
	if err != nil {
		t.Fatal(err)
	}
	return sb, opts
}

func TestRemoveExactPathsPersistsAndPreservesSharedBlocks(t *testing.T) {
	t.Parallel()
	backend := storage.NewMemoryBackend()
	sb, opts := removalArchive(t, backend)
	if len(sb.Entries["literal*.txt"]) != 2 {
		t.Fatal("expected two saved versions")
	}
	if count, err := sb.RemovePaths([]string{"literal*.txt", "literal*.txt", "missing"}); err != nil || count != 1 {
		t.Fatalf("RemovePaths = %d, %v", count, err)
	}
	sb, err := GetSuperblock(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Entries) != 2 || sb.Entries["literal*.txt"] != nil || sb.Entries["literal-other.txt"] == nil {
		t.Fatalf("literal removal changed the wrong paths: %v", sb.Entries)
	}
	if count, err := sb.RemovePaths([]string{"partial.txt"}); err != nil || count != 1 {
		t.Fatalf("RemovePaths partial = %d, %v", count, err)
	}
	if len(sb.Blobs) != 1 || len(sb.Blocks) != 2 {
		t.Fatalf("unexpected shared data after pruning: %d blobs, %d blocks", len(sb.Blobs), len(sb.Blocks))
	}
	file := getSingleRegularFile(t, sb, "literal-other.txt")
	got, err := restoreFromBlob(sb, file.BlobID)
	want := append(bytes.Repeat([]byte("a"), BlockSize), "same"...)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("remaining shared file is unreadable: %v", err)
	}
	for _, prefix := range []string{"entries/", "blobs/", "blocks/"} {
		var count int
		if err := backend.ListBlobs(prefix, func(string) error { count++; return nil }); err != nil {
			t.Fatal(err)
		}
		want := 1
		if prefix == "blocks/" {
			want = 2
		}
		if count != want {
			t.Errorf("%s contains %d objects, want %d", prefix, count, want)
		}
	}
}

type failingRemovalBackend struct {
	storage.Backend
	writeErr, deleteErr error
	deletes             int
}

func (b *failingRemovalBackend) PutBlob(key string, data []byte) error {
	if key == "superblock.json.gz" && b.writeErr != nil {
		return b.writeErr
	}
	return b.Backend.PutBlob(key, data)
}
func (b *failingRemovalBackend) DeleteBlob(key string) error {
	b.deletes++
	if b.deleteErr != nil {
		return b.deleteErr
	}
	return b.Backend.DeleteBlob(key)
}

func TestRemoveFailureKeepsReferencedData(t *testing.T) {
	t.Parallel()
	backend := &failingRemovalBackend{Backend: storage.NewMemoryBackend()}
	sb, opts := removalArchive(t, backend)
	originalGeneration, originalUpdate := sb.Generation, sb.LastUpdate
	failure := errors.New("storage failure")
	backend.writeErr = failure
	if count, err := sb.RemovePaths([]string{"partial.txt"}); count != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed removal = %d, %v", count, err)
	}
	if backend.deletes != 0 || sb.Generation != originalGeneration || sb.LastUpdate != originalUpdate || sb.Entries["partial.txt"] == nil {
		t.Fatal("failed save changed the index or deleted stored objects")
	}
	reloaded, err := GetSuperblock(opts)
	if err != nil || len(reloaded.Entries) != 3 {
		t.Fatalf("failed save damaged stored index: %v", err)
	}
	for _, versions := range sb.Entries {
		for _, value := range versions {
			r, err := sb.OpenRegularFile(value.(*RegularFile))
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, r)
			closeErr := r.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("failed removal damaged file data: %v, %v", err, closeErr)
			}
		}
	}

	backend.writeErr, backend.deleteErr = nil, failure
	if count, err := sb.RemovePaths([]string{"partial.txt"}); count != 1 || !errors.Is(err, failure) {
		t.Fatalf("cleanup failure = %d, %v", count, err)
	}
	reloaded, err = GetSuperblock(opts)
	if err != nil || len(reloaded.Entries) != 2 || reloaded.Entries["partial.txt"] != nil {
		t.Fatalf("cleanup failure lost committed removal: %v", err)
	}
}

func TestRemoveDryRunDoesNotChangeIndex(t *testing.T) {
	t.Parallel()
	backend := &failingRemovalBackend{Backend: storage.NewMemoryBackend()}
	sb, opts := removalArchive(t, backend)
	opts.DryRun = true
	if count, err := sb.RemovePaths([]string{"partial.txt"}); count != 1 || err != nil {
		t.Fatalf("dry run = %d, %v", count, err)
	}
	if backend.deletes != 0 || sb.Entries["partial.txt"] == nil {
		t.Fatal("dry run changed archive")
	}
}
