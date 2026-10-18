package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/storage"
)

type checkFixture struct {
	sb   *Superblock
	opts *Options
	t    *testing.T
}

func newCheckFixture(t *testing.T, backend storage.Backend) *checkFixture {
	t.Helper()
	opts := &Options{ArchiveDir: t.TempDir(), Storage: backend}
	return &checkFixture{newSuperblock(opts), opts, t}
}
func checkHash(data []byte) string {
	return fmt.Sprintf("%016x", crc64.Checksum(data, crc64.MakeTable(crc64.ECMA)))
}
func (f *checkFixture) block(data string) string {
	f.t.Helper()
	id := checkHash([]byte(data))
	f.sb.Blocks[id] = &Block{ID: id, Size: uint32(len(data))}
	if err := f.sb.dataWrite("blocks", id, []byte(data)); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *checkFixture) blob(blocks ...string) string {
	f.t.Helper()
	id := checkHash([]byte(strings.Join(blocks, "")))
	var size int64
	for _, block := range blocks {
		size += int64(f.sb.Blocks[block].Size)
	}
	blob := &Blob{ID: id, Size: strconv.FormatInt(size, 10), size: size, Blocks: blocks}
	f.sb.Blobs[id] = blob
	if err := f.sb.jsonWrite("blobs", id, blob); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *checkFixture) entry(name, blob string, second int64) string {
	f.t.Helper()
	entry := &RegularFile{Path: name, BlobID: blob, FileStats: FileStats{Owner: "user", Group: "group", Mode: 0600, Size: f.sb.Blobs[blob].Size, MTime: time.Unix(second, 0).UTC()}}
	id := entry.hash()
	if f.sb.Entries[name] == nil {
		f.sb.Entries[name] = make(map[string]interface{})
	}
	f.sb.Entries[name][id] = entry
	if err := f.sb.jsonWrite("entries", id, entry); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *checkFixture) write() {
	f.t.Helper()
	if err := f.sb.Write(); err != nil {
		f.t.Fatal(err)
	}
}
func (f *checkFixture) delete(key string) {
	f.t.Helper()
	if err := f.sb.storage.DeleteBlob(key); err != nil {
		f.t.Fatal(err)
	}
}
func (f *checkFixture) check() *CheckReport {
	f.t.Helper()
	r, err := CheckArchive(f.opts, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}
func (f *checkFixture) requireClean() {
	f.t.Helper()
	r := f.check()
	if len(r.Issues) != 0 {
		f.t.Fatalf("archive still inconsistent: %+v", r.Issues)
	}
}
func snapshotObjects(t *testing.T, b storage.Backend) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	if err := b.ListBlobs("", func(key string) error {
		data, err := readRawObject(b, key)
		result[key] = data
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFSCKProgressUsesStoredObjectTotals(t *testing.T) {
	t.Parallel()
	backend := storage.NewMemoryBackend()
	f := newCheckFixture(t, backend)
	badBlock := f.block("damaged data")
	blob := f.blob(f.block("good data"), badBlock)
	f.entry("file", blob, 100)
	f.entry("copy", blob, 100)
	f.write()
	f.block("unindexed block") // Stored after writing the index; must count too.
	if err := backend.PutBlob("blocks/"+badBlock, []byte("not gzip")); err != nil {
		t.Fatal(err)
	}

	type update struct {
		phase        string
		count, total int
	}
	var got []update
	if _, err := CheckArchive(f.opts, func(phase string, count, total int) {
		got = append(got, update{phase, count, total})
	}); err != nil {
		t.Fatal(err)
	}
	want := []update{
		{"directory layout", 0, -1},
		{"directory layout", 6, 6},
		{"blocks", 0, 3},
		{"blocks", 1, 3},
		{"blocks", 2, 3},
		{"blocks", 3, 3},
		{"blobs", 0, 1},
		{"blobs", 1, 1},
		{"entries", 0, 2},
		{"entries", 1, 2},
		{"entries", 2, 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progress updates = %+v, want %+v", got, want)
	}
}

func TestFSCKRepairsCascadesAndRecoversOrphans(t *testing.T) {
	t.Parallel()
	backend := storage.NewMemoryBackend()
	f := newCheckFixture(t, backend)
	shared := f.block("shared data")
	goodBlob := f.blob(shared, f.block("good tail"))
	f.entry("keep.txt", goodBlob, 100)
	badBlock := f.block("broken tail")
	brokenBlob := f.blob(shared, badBlock)
	f.entry("broken.txt", brokenBlob, 100)
	f.entry("broken-copy.txt", brokenBlob, 101)
	missingBlock := f.block("missing block")
	missingBlockBlob := f.blob(missingBlock)
	f.entry("missing-block.txt", missingBlockBlob, 100)
	corruptBlob := f.blob(f.block("bad blob content"))
	f.entry("bad-blob.txt", corruptBlob, 100)
	missingBlob := f.blob(f.block("missing blob content"))
	f.entry("missing-blob.txt", missingBlob, 100)
	entryOrphan := f.blob(f.block("entry corruption preserves these bytes"))
	corruptEntry := f.entry("bad-entry.txt", entryOrphan, 100)
	orphan := f.blob(f.block("orphaned data"))
	missingEntry := f.entry("restore-metadata.txt", goodBlob, 100)
	f.entry("unindexed.txt", goodBlob, 100)
	delete(f.sb.Entries, "unindexed.txt")
	delete(f.sb.Blocks, shared) // The normal loader cannot open this index.
	f.write()
	if err := backend.PutBlob("blocks/"+badBlock, []byte("not gzip")); err != nil {
		t.Fatal(err)
	}
	if err := f.sb.dataWrite("blobs", corruptBlob, []byte(`{"id":"wrong"}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.sb.dataWrite("entries", corruptEntry, []byte(`{"path":17}`)); err != nil {
		t.Fatal(err)
	}
	f.delete("blocks/" + missingBlock)
	f.delete("blobs/" + missingBlob)
	f.delete("entries/" + missingEntry)
	before := snapshotObjects(t, backend)
	r := f.check()
	if !reflect.DeepEqual(before, snapshotObjects(t, backend)) {
		t.Fatal("check modified archive")
	}
	if len(r.Recovered) != 2 || r.RecreatedEntries != 1 {
		t.Fatalf("recovery plan: %+v", r)
	}
	if err := r.Repair(); err != nil {
		t.Fatal(err)
	}
	f.requireClean()
	sb, err := GetSuperblock(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"keep.txt", "unindexed.txt", "restore-metadata.txt", "lost+found/" + orphan, "lost+found/" + entryOrphan}
	if len(sb.Entries) != len(wantPaths) {
		t.Fatalf("unexpected paths: %v", sb.Entries)
	}
	for _, name := range wantPaths {
		file := getSingleRegularFile(t, sb, name)
		reader, err := sb.OpenRegularFile(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || len(data) == 0 {
			t.Fatalf("recovered file %s is unreadable: %v", name, err)
		}
	}
	if sb.Blocks[shared] == nil {
		t.Fatal("removed shared block")
	}
	if saved, err := readRawObject(backend, "superblock.json.gz.fsck.bak"); err != nil || !bytes.Equal(saved, before["superblock.json.gz"]) {
		t.Fatal("original superblock backup missing")
	}
}

func TestFSCKDetectsChecksumsHashesAndMalformedMetadata(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"gzip checksum", "wrong content hash", "wrong original size", "truncated gzip", "blob hash", "blob size", "entry hash", "entry size", "unsafe entry path", "trailing JSON"} {
		t.Run(fault, func(t *testing.T) {
			f := newCheckFixture(t, storage.NewMemoryBackend())
			block := f.block("payload")
			blob := f.blob(block)
			entry := f.entry("file", blob, 100)
			f.write()
			switch fault {
			case "gzip checksum", "truncated gzip":
				data, _ := readRawObject(f.sb.storage, "blocks/"+block)
				if fault == "gzip checksum" {
					data[len(data)-8] ^= 1
				} else {
					data = data[:len(data)-3]
				}
				if err := f.sb.storage.PutBlob("blocks/"+block, data); err != nil {
					t.Fatal(err)
				}
			case "wrong content hash":
				if err := f.sb.dataWrite("blocks", block, []byte("changed")); err != nil {
					t.Fatal(err)
				}
			case "wrong original size":
				data, _ := readRawObject(f.sb.storage, "blocks/"+block)
				data[12] ^= 1
				if err := f.sb.storage.PutBlob("blocks/"+block, data); err != nil {
					t.Fatal(err)
				}
			case "blob hash", "blob size":
				b := *f.sb.Blobs[blob]
				if fault == "blob hash" {
					b.Blocks = nil
				} else {
					b.Size = "999"
				}
				if err := f.sb.jsonWrite("blobs", blob, &b); err != nil {
					t.Fatal(err)
				}
			case "entry hash", "entry size", "unsafe entry path":
				e := *f.sb.Entries["file"][entry].(*RegularFile)
				if fault == "entry hash" {
					e.Owner = "wrong"
				} else if fault == "entry size" {
					e.Size = "999"
				} else {
					e.Path = "../escape"
				}
				if err := f.sb.jsonWrite("entries", entry, &e); err != nil {
					t.Fatal(err)
				}
			case "trailing JSON":
				data, _ := json.Marshal(f.sb.Entries["file"][entry])
				if err := f.sb.dataWrite("entries", entry, append(data, []byte(" {}")...)); err != nil {
					t.Fatal(err)
				}
			}
			r := f.check()
			if len(r.Issues) == 0 {
				t.Fatal("corruption went undetected")
			}
			if err := r.Repair(); err != nil {
				t.Fatal(err)
			}
			f.requireClean()
		})
	}
}

func TestFSCKRebuildsMissingAndCorruptSuperblocks(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "gzip", "JSON", "entry map", "blob map", "trailer"} {
		t.Run(fault, func(t *testing.T) {
			f := newCheckFixture(t, storage.NewMemoryBackend())
			blob := f.blob(f.block("keep"))
			f.entry("keep.txt", blob, 100)
			f.write()
			if fault == "missing" {
				f.delete("superblock.json.gz")
			} else {
				var raw []byte
				switch fault {
				case "gzip":
					raw = []byte("broken")
				case "trailer":
					raw, _ = readRawObject(f.sb.storage, "superblock.json.gz")
					raw[len(raw)-8] ^= 1
				default:
					payload := []byte("not json")
					if fault != "JSON" {
						data, _ := json.Marshal(f.sb)
						var fields map[string]json.RawMessage
						_ = json.Unmarshal(data, &fields)
						field := "entries"
						if fault == "blob map" {
							field = "blobs"
						}
						fields[field] = json.RawMessage(`"wrong type"`)
						payload, _ = json.Marshal(fields)
					}
					var compressed bytes.Buffer
					gz := gzip.NewWriter(&compressed)
					_, _ = gz.Write(payload)
					_ = gz.Close()
					raw = compressed.Bytes()
				}
				if err := f.sb.storage.PutBlob("superblock.json.gz", raw); err != nil {
					t.Fatal(err)
				}
			}
			r := f.check()
			if len(r.Issues) == 0 {
				t.Fatal("broken superblock considered healthy")
			}
			if err := r.Repair(); err != nil {
				t.Fatal(err)
			}
			f.requireClean()
		})
	}
}

func TestFSCKLocalLayoutAndDirectoryCleanup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	backend := storage.NewLocalBackend(root)
	f := newCheckFixture(t, backend)
	blob := f.blob(f.block("keep"))
	f.entry("keep", blob, 100)
	f.write()
	for _, prefix := range []string{"blocks", "blobs", "entries"} {
		if err := os.MkdirAll(filepath.Join(root, prefix, "old", "empty", "leaf"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "blocks", "misplaced"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "entries", "linked")); err != nil {
		t.Fatal(err)
	}
	r := f.check()
	if r.EmptyDirectories != 9 {
		t.Fatalf("empty directory count: %d", r.EmptyDirectories)
	}
	if err := r.Repair(); err != nil {
		t.Fatal(err)
	}
	f.requireClean()
	if got, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(got) != "outside" {
		t.Fatal("fsck followed symlink")
	}
}

func TestFSCKRefusesChangedSuperblockAndFailedWrites(t *testing.T) {
	t.Parallel()
	backend := &failingRemovalBackend{Backend: storage.NewMemoryBackend()}
	f := newCheckFixture(t, backend)
	f.blob(f.block("orphan"))
	f.write()
	r := f.check()
	f.write()
	if err := r.Repair(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale plan: %v", err)
	}
	r = f.check()
	original, _ := readRawObject(backend, "superblock.json.gz")
	backend.writeErr = errors.New("disk failure")
	if err := r.Repair(); !errors.Is(err, backend.writeErr) {
		t.Fatalf("failed index write: %v", err)
	}
	if backend.deletes != 0 {
		t.Fatal("deleted objects before saving repaired index")
	}
	if current, _ := readRawObject(backend, "superblock.json.gz"); !bytes.Equal(original, current) {
		t.Fatal("failed save changed old index")
	}
}

func TestFSCKRepairsIndexMetadataWithoutDeletingValidData(t *testing.T) {
	t.Parallel()
	f := newCheckFixture(t, storage.NewMemoryBackend())
	block := f.block("keep original")
	blob := f.blob(block)
	id := f.entry("keep", blob, 100)
	f.sb.Blocks[block].Size++
	f.sb.Blobs[blob].Size = "999"
	f.sb.Entries["keep"][id].(*RegularFile).Owner = "damaged index"
	f.write()
	r := f.check()
	if len(r.Issues) == 0 || len(r.remove) != 0 {
		t.Fatalf("bad index should preserve data: %+v", r.Issues)
	}
	if err := r.Repair(); err != nil {
		t.Fatal(err)
	}
	f.requireClean()
}

func TestFSCKLostFoundCollision(t *testing.T) {
	t.Parallel()
	f := newCheckFixture(t, storage.NewMemoryBackend())
	orphan := f.blob(f.block("orphan"))
	other := f.blob(f.block("existing file"))
	f.entry("lost+found/"+orphan, other, 100)
	f.write()
	r := f.check()
	if !reflect.DeepEqual(r.Recovered, []string{"lost+found/" + orphan + "-1"}) {
		t.Fatalf("unexpected collision resolution: %v", r.Recovered)
	}
	if err := r.Repair(); err != nil {
		t.Fatal(err)
	}
	f.requireClean()
}

func TestFSCKLostFoundParentConflictDoesNotModifyArchive(t *testing.T) {
	t.Parallel()
	f := newCheckFixture(t, storage.NewMemoryBackend())
	f.blob(f.block("orphan"))
	other := f.blob(f.block("existing file"))
	f.entry("lost+found", other, 100)
	f.write()
	before := snapshotObjects(t, f.sb.storage)
	if err := f.check().Repair(); err == nil {
		t.Fatal("expected lost+found conflict")
	}
	if !reflect.DeepEqual(before, snapshotObjects(t, f.sb.storage)) {
		t.Fatal("conflict modified archive")
	}
}

type failingCheckReader struct{ err error }

func (r failingCheckReader) Read([]byte) (int, error) { return 0, r.err }
func (r failingCheckReader) Close() error             { return nil }

type failingCheckBackend struct {
	storage.Backend
	key string
	err error
}

func (b failingCheckBackend) GetBlob(key string) (io.ReadCloser, error) {
	if key == b.key {
		return failingCheckReader{b.err}, nil
	}
	return b.Backend.GetBlob(key)
}

func TestFSCKReadFailureIsNotClassifiedAsCorruption(t *testing.T) {
	t.Parallel()
	backend := storage.NewMemoryBackend()
	f := newCheckFixture(t, backend)
	block := f.block("data")
	blob := f.blob(block)
	f.entry("file", blob, 100)
	f.write()
	before := snapshotObjects(t, backend)
	failure := errors.New("device read failed")
	f.opts.Storage = failingCheckBackend{backend, "blocks/" + block, failure}
	if report, err := CheckArchive(f.opts, nil); report != nil || !errors.Is(err, failure) {
		t.Fatalf("read failure returned a repair plan: %v, %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotObjects(t, backend)) {
		t.Fatal("failed read modified archive")
	}
}

func TestFSCKCleanupFailureIsReported(t *testing.T) {
	t.Parallel()
	backend := &failingRemovalBackend{Backend: storage.NewMemoryBackend()}
	f := newCheckFixture(t, backend)
	f.block("unreferenced")
	f.write()
	backend.deleteErr = errors.New("cannot delete")
	if err := f.check().Repair(); !errors.Is(err, backend.deleteErr) {
		t.Fatalf("cleanup error was hidden: %v", err)
	}
	backend.deleteErr = nil
	if err := f.check().Repair(); err != nil {
		t.Fatal(err)
	}
	f.requireClean()
}
