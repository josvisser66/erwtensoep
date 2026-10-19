package fs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func TestRemoveVersionsPreservesOtherVersionsAndSharedData(t *testing.T) {
	backend := storage.NewMemoryBackend()
	sb, opts := removalArchive(t, backend)
	name := "literal*.txt"
	var oldIDs []string
	for id := range sb.Entries[name] {
		oldIDs = append(oldIDs, id)
	}
	// Add different contents at the same path, sharing the first block with
	// earlier versions and other files, but with its own final block/blob.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, name), append(bytes.Repeat([]byte("a"), BlockSize), "new"...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, failures, err := sb.Merge(src); err != nil || failures != 0 {
		t.Fatalf("Merge: %d failures, %v", failures, err)
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	var newID string
	var newFile *RegularFile
	for id, value := range sb.Entries[name] {
		if id != oldIDs[0] && id != oldIDs[1] {
			newID, newFile = id, value.(*RegularFile)
		}
	}
	if newFile == nil || len(sb.Entries[name]) != 3 {
		t.Fatal("expected three versions")
	}
	uniqueBlock := sb.Blobs[newFile.BlobID].Blocks[1]
	if count, err := sb.RemoveVersions(map[string][]string{name: {newID, newID, "missing"}}); err != nil || count != 1 {
		t.Fatalf("RemoveVersions = %d, %v", count, err)
	}
	for _, key := range []string{blobKey("entries", newID), blobKey("blobs", newFile.BlobID), blobKey("blocks", uniqueBlock)} {
		if r, err := backend.GetBlob(key); !errors.Is(err, os.ErrNotExist) {
			if r != nil {
				r.Close()
			}
			t.Fatalf("unused object remains: %s, %v", key, err)
		}
	}
	sb, err := GetSuperblock(opts)
	if err != nil || len(sb.Entries[name]) != 2 {
		t.Fatalf("partial removal did not persist: %v", err)
	}
	if count, err := sb.RemoveVersions(map[string][]string{name: {oldIDs[0]}}); err != nil || count != 1 {
		t.Fatalf("remove shared-content version: %d, %v", count, err)
	}
	if _, err := sb.RemovePaths([]string{"literal-other.txt", "partial.txt"}); err != nil {
		t.Fatal(err)
	}
	// With other paths gone, the single remaining version still owns its data.
	file := getSingleRegularFile(t, sb, name)
	if len(sb.Blobs) != 1 || len(sb.Blocks) != 2 {
		t.Fatalf("remaining data: %d blobs, %d blocks", len(sb.Blobs), len(sb.Blocks))
	}
	data, err := restoreFromBlob(sb, file.BlobID)
	if err != nil || !bytes.Equal(data, append(bytes.Repeat([]byte("a"), BlockSize), "same"...)) {
		t.Fatalf("remaining version unreadable: %v", err)
	}
	check, err := CheckArchive(opts, nil)
	if err != nil || len(check.Issues) != 0 {
		t.Fatalf("partial removal left inconsistencies: %v, %+v", err, check)
	}
	if count, err := sb.RemoveVersions(map[string][]string{name: {oldIDs[1]}}); err != nil || count != 1 {
		t.Fatalf("remove last version: %d, %v", count, err)
	}
	if len(sb.Entries)+len(sb.Blobs)+len(sb.Blocks) != 0 {
		t.Fatal("last-version removal did not remove the path and unused data")
	}
}

func TestRemoveVersionsFailureAndDryRun(t *testing.T) {
	backend := &failingRemovalBackend{Backend: storage.NewMemoryBackend()}
	sb, opts := removalArchive(t, backend)
	name := "literal*.txt"
	var id string
	for key := range sb.Entries[name] {
		id = key
		break
	}
	selection := map[string][]string{name: {id}}
	originalGeneration := sb.Generation
	for _, choice := range []map[string][]string{nil, {name: nil}, {name: {"missing"}}, {"absent": {id}}} {
		if count, err := sb.RemoveVersions(choice); count != 0 || err != nil {
			t.Fatalf("empty/missing selection: %d, %v", count, err)
		}
	}
	opts.DryRun = true
	if count, err := sb.RemoveVersions(selection); count != 1 || err != nil {
		t.Fatalf("dry run: %d, %v", count, err)
	}
	opts.DryRun = false
	if len(sb.Entries[name]) != 2 || backend.deletes != 0 || sb.Generation != originalGeneration {
		t.Fatal("empty selection or dry run changed the archive")
	}
	failure := errors.New("storage failure")
	backend.writeErr = failure
	if count, err := sb.RemoveVersions(selection); count != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed removal: %d, %v", count, err)
	}
	if len(sb.Entries[name]) != 2 || backend.deletes != 0 || sb.Generation != originalGeneration {
		t.Fatal("failed partial removal changed nested version maps or stored data")
	}
	reloaded, err := GetSuperblock(opts)
	if err != nil || len(reloaded.Entries[name]) != 2 {
		t.Fatalf("failed save changed stored versions: %v", err)
	}
	for _, value := range reloaded.Entries[name] {
		r, err := reloaded.OpenRegularFile(value.(*RegularFile))
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, r)
		if closeErr := r.Close(); readErr != nil || closeErr != nil {
			t.Fatalf("retained version unreadable: %v, %v", readErr, closeErr)
		}
	}
	backend.writeErr, backend.deleteErr = nil, failure
	if count, err := sb.RemoveVersions(selection); count != 1 || !errors.Is(err, failure) {
		t.Fatalf("cleanup failure: %d, %v", count, err)
	}
	reloaded, err = GetSuperblock(opts)
	if err != nil || len(reloaded.Entries[name]) != 1 || reloaded.Entries[name][id] != nil {
		t.Fatalf("cleanup failure lost committed version removal: %v", err)
	}
}
