package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeleteBlobPrunesEmptyParents(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"blocks", "blobs", "entries"} {
		t.Run(prefix, func(t *testing.T) {
			root := t.TempDir()
			b := NewLocalBackend(root)
			first, sibling := prefix+"/aaaabbbbccccdddd", prefix+"/aaaabbbbeeeeffff"
			for _, key := range []string{first, sibling, "superblock.json.gz"} {
				if err := b.PutBlob(key, []byte("keep")); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.DeleteBlob(first); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, prefix, "aaaa", "bbbb", "cccc")); !os.IsNotExist(err) {
				t.Fatalf("empty leaf remains: %v", err)
			}
			if _, err := b.BlobSize(sibling); err != nil {
				t.Fatalf("sibling was removed: %v", err)
			}
			if err := b.DeleteBlob(sibling); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, prefix)); !os.IsNotExist(err) {
				t.Fatalf("empty ancestors remain: %v", err)
			}
			if _, err := b.BlobSize("superblock.json.gz"); err != nil {
				t.Fatalf("archive root was removed: %v", err)
			}
			if err := b.PutBlob(first, []byte("new")); err != nil {
				t.Fatalf("recreate pruned directories: %v", err)
			}
		})
	}
}

func TestInventoryAndSweepDoNotFollowSymlinks(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	b := NewLocalBackend(root)
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "blocks", "empty", "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "blocks", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := b.PutBlob("blocks/0123456789abcdef", []byte("valid layout")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blocks", "wrong-layout"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := b.InspectArchive()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventory.Objects, []string{"blocks/0123456789abcdef"}) || !reflect.DeepEqual(inventory.InvalidPaths, []string{"blocks/linked", "blocks/wrong-layout"}) {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
	if !reflect.DeepEqual(inventory.EmptyDirs, []string{"blocks/empty/child", "blocks/empty"}) {
		t.Fatalf("empty dirs: %v", inventory.EmptyDirs)
	}
	if count, err := b.PruneEmptyArchiveDirs(); err != nil || count != 2 {
		t.Fatalf("prune = %d, %v", count, err)
	}
	for _, name := range inventory.InvalidPaths {
		if err := b.RemoveInvalidArchivePath(name); err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "outside" {
		t.Fatal("followed symlink outside archive")
	}
	if err := b.RemoveInvalidArchivePath("../outside"); err == nil {
		t.Fatal("accepted path outside archive")
	}
	if err := b.RemoveInvalidArchivePath("superblock.json.gz"); err == nil {
		t.Fatal("accepted path outside storage sections")
	}
}
