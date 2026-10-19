package fs

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestRestoreSelectedVersions(t *testing.T) {
	t.Parallel()
	source, destination := t.TempDir(), t.TempDir()
	opts := Options{ArchiveDir: t.TempDir(), Storage: storage.NewMemoryBackend(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
	sb, err := GetSuperblock(&opts)
	if err != nil {
		t.Fatal(err)
	}
	for i, contents := range []string{"old", "new data"} {
		file := filepath.Join(source, "literal*.txt")
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(100*(i+1)), 0)
		if err := os.Chtimes(file, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(source, "link")
		if i > 0 {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(contents, link); err != nil {
			t.Fatal(err)
		}
		if _, failures, err := sb.Merge(source); err != nil || failures != 0 {
			t.Fatalf("merge: %d failures, %v", failures, err)
		}
	}
	selected := make(map[string]string)
	for name, versions := range sb.Entries {
		for id, value := range versions {
			switch entry := value.(type) {
			case *RegularFile:
				if entry.MTime.Equal(time.Unix(100, 0)) {
					selected[name] = id
				}
			case *Symlink:
				if entry.Target == "old" {
					selected[name] = id
				}
			}
		}
	}
	if n, err := sb.RestoreVersions(selected, destination); err != nil || n != 2 {
		t.Fatalf("restore selected versions: count %d, %v", n, err)
	}
	file := filepath.Join(destination, "literal*.txt")
	if data, err := os.ReadFile(file); err != nil || string(data) != "old" {
		t.Fatalf("selected content: %q, %v", data, err)
	}
	if info, err := os.Stat(file); err != nil || !info.ModTime().Equal(time.Unix(100, 0)) || info.Mode().Perm() != 0600 {
		t.Fatalf("selected metadata: %v, %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(destination, "link")); err != nil || target != "old" {
		t.Fatalf("selected symlink: %q, %v", target, err)
	}
	// Every ID is validated before any selected destination gets replaced.
	selected["literal*.txt"] = latestVersionID(sb.Entries["literal*.txt"])
	selected["missing"] = "bad-id"
	if n, err := sb.RestoreVersions(selected, destination); err == nil || n != 0 {
		t.Fatalf("invalid selection accepted: count %d, %v", n, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "old" {
		t.Fatalf("invalid selection partially restored: %q, %v", data, err)
	}
	if n, err := sb.RestoreVersions(nil, destination); err != nil || n != 0 {
		t.Fatalf("empty selection: count %d, %v", n, err)
	}
}

func TestRestoreLatestVersionOrdering(t *testing.T) {
	versions := map[string]interface{}{
		"a": &RegularFile{FileStats: FileStats{MTime: time.Unix(-100, 0)}},
		"b": &RegularFile{FileStats: FileStats{MTime: time.Unix(-100, 0)}},
		"c": &RegularFile{FileStats: FileStats{MTime: time.Unix(-200, 0)}},
	}
	if got := latestVersionID(versions); got != "b" {
		t.Fatalf("latest version = %q, want b", got)
	}
}
