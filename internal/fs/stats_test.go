package fs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

func statsFixture(t *testing.T, backend storage.Backend) *Superblock {
	t.Helper()
	sb := newSuperblock(&Options{Storage: backend})
	for id, data := range map[string]string{"a": "aaaa", "b": "bbbb", "c": "cc", "d": "dddd", "e": "e", "unused": "unused"} {
		sb.Blocks[id] = &Block{ID: id, Size: uint32(len(data))}
		if err := sb.dataWrite(sb.blocksDir, id, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	sb.Blobs = map[string]*Blob{
		"ab":     {ID: "ab", Size: "8", Blocks: []string{"a", "b"}},
		"ac":     {ID: "ac", Size: "6", Blocks: []string{"a", "c"}},
		"dd":     {ID: "dd", Size: "8", Blocks: []string{"d", "d"}},
		"de":     {ID: "de", Size: "5", Blocks: []string{"d", "e"}},
		"empty":  {ID: "empty", Size: "0"},
		"unused": {ID: "unused", Size: "6", Blocks: []string{"unused"}},
	}
	file := func(path, blobID string) *RegularFile {
		return &RegularFile{Path: path, BlobID: blobID, FileStats: FileStats{Size: sb.Blobs[blobID].Size}}
	}
	sb.Entries = map[string]map[string]interface{}{
		"same-a":  {"v1": file("same-a", "ab"), "v2": file("same-a", "ab")},
		"same-b":  {"v1": file("same-b", "ab")},
		"partial": {"v1": file("partial", "ac")},
		"repeat":  {"v1": file("repeat", "dd"), "v2": file("repeat", "de")},
		"empty":   {"v1": file("empty", "empty")},
		"link":    {"v1": &Symlink{Path: "link", Target: "same-a", FileStats: FileStats{Size: "6"}}},
	}
	return sb
}

func TestStatsSharingAndStoredSizes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "local"} {
		t.Run(name, func(t *testing.T) {
			var backend storage.Backend = storage.NewMemoryBackend()
			if name == "local" {
				backend = storage.NewLocalBackend(t.TempDir())
			}
			sb := statsFixture(t, backend)
			var stored int64
			for _, id := range []string{"a", "b", "c", "d", "e"} {
				r, err := backend.GetBlob("blocks/" + id)
				if err != nil {
					t.Fatal(err)
				}
				n, err := io.Copy(io.Discard, r)
				closeErr := r.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("read stored bytes: %v, close: %v", err, closeErr)
				}
				stored += n
			}
			got, err := sb.Stats()
			if err != nil {
				t.Fatal(err)
			}
			want := &ArchiveStats{
				Paths: 6, Versions: 8, RegularVersions: 7, SymlinkVersions: 1,
				MaxVersions: 2, Blobs: 5, Blocks: 5,
				UnreferencedBlobs: 1, UnreferencedBlocks: 1,
				BlockReferences: 12, ReusedBlocks: 3, CrossFileBlocks: 2, MaxBlockReferences: 4,
				LogicalBytes: 43, BlobBytes: 27, BlockBytes: 15, StoredBytes: stored,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("stats = %+v; want %+v", got, want)
			}
			if got.StoredBytes <= got.BlockBytes {
				t.Fatal("expected gzip overhead to expand these tiny blocks")
			}
		})
	}
}

func TestStatsEmpty(t *testing.T) {
	t.Parallel()
	sb := newSuperblock(&Options{Storage: storage.NewMemoryBackend()})
	got, err := sb.Stats()
	if err != nil || *got != (ArchiveStats{}) {
		t.Fatalf("empty stats = %+v, %v", got, err)
	}
}

func TestStatsRejectsMissingOrInconsistentData(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*Superblock)
		want   string
	}{
		{"missing blob", func(sb *Superblock) { delete(sb.Blobs, "ac") }, "missing blob ac"},
		{"missing block metadata", func(sb *Superblock) { delete(sb.Blocks, "c") }, "missing block c"},
		{"missing stored block", func(sb *Superblock) { _ = sb.storage.DeleteBlob("blocks/c") }, "measuring stored block c"},
		{"invalid file size", func(sb *Superblock) { sb.Entries["partial"]["v1"].(*RegularFile).Size = "invalid" }, "invalid file size"},
		{"negative file size", func(sb *Superblock) { sb.Entries["partial"]["v1"].(*RegularFile).Size = "-6" }, "invalid file size"},
		{"mismatched file size", func(sb *Superblock) { sb.Entries["partial"]["v1"].(*RegularFile).Size = "7" }, "does not match blob"},
		{"mismatched block sizes", func(sb *Superblock) { sb.Blocks["c"].Size++ }, "does not match its blocks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := statsFixture(t, storage.NewMemoryBackend())
			tt.modify(sb)
			_, err := sb.Stats()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stats error = %v, want %q", err, tt.want)
			}
			if tt.name == "missing stored block" && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("missing-block error lost its cause: %v", err)
			}
		})
	}
}

func TestStatsCompression(t *testing.T) {
	t.Parallel()
	sb := newSuperblock(&Options{Storage: storage.NewMemoryBackend()})
	data := bytes.Repeat([]byte("a"), 4096)
	sb.Blocks["a"] = &Block{ID: "a", Size: uint32(len(data))}
	sb.Blobs["a"] = &Blob{ID: "a", Size: "4096", Blocks: []string{"a"}}
	sb.Entries["file"] = map[string]interface{}{
		"v1": &RegularFile{Path: "file", BlobID: "a", FileStats: FileStats{Size: "4096"}},
	}
	if err := sb.dataWrite(sb.blocksDir, "a", data); err != nil {
		t.Fatal(err)
	}
	got, err := sb.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if got.LogicalBytes != 4096 || got.BlobBytes != 4096 || got.BlockBytes != 4096 || got.StoredBytes <= 0 || got.StoredBytes >= 4096 {
		t.Fatalf("expected only compression savings: %+v", got)
	}
}

func TestLoadRejectsMissingBlockMetadata(t *testing.T) {
	t.Parallel()
	backend := storage.NewMemoryBackend()
	sb := statsFixture(t, backend)
	delete(sb.Blocks, "c")
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	_, err := GetSuperblock(&Options{ArchiveDir: t.TempDir(), Storage: backend})
	if err == nil || !strings.Contains(err.Error(), "missing block c") {
		t.Fatalf("GetSuperblock error = %v, want missing block c", err)
	}
}
