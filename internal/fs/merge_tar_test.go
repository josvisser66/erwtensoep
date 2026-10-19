package fs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"josvisser.nl/erwtensoep/internal/storage"
)

type tarTestEntry struct {
	header tar.Header
	data   string
}

func tarFile(name, data string) tarTestEntry {
	return tarTestEntry{tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0640, ModTime: time.Unix(100, 0),
		Uname: "tar-user", Gname: "tar-group", Uid: 123, Gid: 456}, data}
}

func tarLink(name, target string, kind byte) tarTestEntry {
	entry := tarFile(name, "")
	entry.header.Typeflag, entry.header.Linkname = kind, target
	return entry
}

func tarTestBytes(t *testing.T, entries []tarTestEntry, compressed bool) []byte {
	t.Helper()
	var out bytes.Buffer
	var destination io.Writer = &out
	var gz *gzip.Writer
	if compressed {
		gz = gzip.NewWriter(&out)
		destination = gz
	}
	w := tar.NewWriter(destination)
	for _, entry := range entries {
		h := entry.header
		h.Size = int64(len(entry.data))
		if err := w.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func writeTestTar(t *testing.T, suffix string, entries []tarTestEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "source"+suffix)
	data := tarTestBytes(t, entries, !strings.EqualFold(suffix, ".tar"))
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func newTarArchive(t *testing.T) (*Superblock, *Options) {
	t.Helper()
	opts := &Options{ArchiveDir: t.TempDir(), Storage: storage.NewMemoryBackend(), MaxSize: math.MaxInt64, SuperblockWriteThreshold: math.MaxInt64}
	sb, err := GetSuperblock(opts)
	if err != nil {
		t.Fatal(err)
	}
	return sb, opts
}

func TestMergeTarFormatsMetadataAndSharing(t *testing.T) {
	payload := strings.Repeat("x", BlockSize) + "last block"
	for _, suffix := range []string{".tar", ".tar.gz", ".tgz", ".TAR.GZ"} {
		t.Run(suffix, func(t *testing.T) {
			sb, opts := newTarArchive(t)
			root := tarTestEntry{header: tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0755}}
			emptyDir := tarTestEntry{header: tar.Header{Name: "empty-dir/", Typeflag: tar.TypeDir, Mode: 0755}}
			longName := strings.Repeat("long-directory/", 12) + "file.txt"
			long := tarFile(longName, "long path")
			long.header.Format = tar.FormatPAX
			long.header.ModTime = time.Unix(123, 987654321)
			long.header.Uname, long.header.Gname = "", ""
			file := writeTestTar(t, suffix, []tarTestEntry{root, emptyDir,
				tarLink("forward", "./folder/file.txt", tar.TypeLink), tarFile("./folder/file.txt", payload),
				tarLink("backward", "folder/file.txt", tar.TypeLink), tarLink("chain", "forward", tar.TypeLink),
				tarFile("copy", payload), tarFile("empty", ""), tarFile("nested.tar", "ordinary tar member"),
				tarLink("symbolic", "/outside/the/archive", tar.TypeSymlink), long})
			if n, failures, err := sb.Merge(file); err != nil || failures != 0 || n != 9 {
				t.Fatalf("merge tar: %d changes, %d failures, %v", n, failures, err)
			}
			if sb.Entries[filepath.Base(file)] != nil || sb.Entries["empty-dir"] != nil {
				t.Fatal("archived the source container or an empty directory")
			}
			original := getSingleRegularFile(t, sb, "folder/file.txt")
			if original.Owner != "tar-user" || original.Group != "tar-group" || original.Mode != 0640 || !original.MTime.Equal(time.Unix(100, 0)) {
				t.Fatalf("tar metadata lost: %+v", original.FileStats)
			}
			for _, name := range []string{"folder/file.txt", "copy", "forward", "backward", "chain"} {
				rf := getSingleRegularFile(t, sb, name)
				if rf.BlobID != original.BlobID || rf.size != int64(len(payload)) {
					t.Fatalf("%s did not share payload", name)
				}
				data, err := restoreFromBlob(sb, rf.BlobID)
				if err != nil || string(data) != payload {
					t.Fatalf("%s contents: %v", name, err)
				}
			}
			metadata := getSingleRegularFile(t, sb, longName)
			if metadata.Owner != "123" || metadata.Group != "456" || !metadata.MTime.Equal(long.header.ModTime) {
				t.Fatalf("PAX metadata lost: %+v", metadata.FileStats)
			}
			for _, value := range sb.Entries["symbolic"] {
				link := value.(*Symlink)
				if link.Target != "/outside/the/archive" || link.size != int64(len(link.Target)) {
					t.Fatalf("symlink changed: %+v", link)
				}
			}
			// Local and tar readers must use identical block boundaries.
			local := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(local, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(local)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sb.mergeRegularfile(local, info, "local-copy"); err != nil {
				t.Fatal(err)
			}
			if getSingleRegularFile(t, sb, "local-copy").BlobID != original.BlobID {
				t.Fatal("local/tar blob IDs differ")
			}
			if n, failures, err := sb.Merge(file); err != nil || failures != 0 || n != 0 {
				t.Fatalf("repeat merge: %d, %d, %v", n, failures, err)
			}
			if err := sb.Write(); err != nil {
				t.Fatal(err)
			}
			report, err := CheckArchive(opts, nil)
			if err != nil || len(report.Issues) != 0 {
				t.Fatalf("tar archive inconsistent: %+v, %v", report, err)
			}
			loaded, err := GetSuperblock(opts)
			if err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			if n, err := loaded.Restore([]string{"folder", "symbolic", "empty"}, destination); err != nil || n != 3 {
				t.Fatalf("restore: %d, %v", n, err)
			}
			if data, err := os.ReadFile(filepath.Join(destination, "folder/file.txt")); err != nil || string(data) != payload {
				t.Fatalf("restored file: %v", err)
			}
			if target, err := os.Readlink(filepath.Join(destination, "symbolic")); err != nil || target != "/outside/the/archive" {
				t.Fatalf("restored symlink: %s, %v", target, err)
			}
		})
	}
}

func TestMergeTarExclusionsLimitsAndDryRun(t *testing.T) {
	sb, opts := newTarArchive(t)
	opts.MinSize, opts.MaxSize = 3, 20
	file := writeTestTar(t, ".tgz", []tarTestEntry{
		tarFile("dir/kept", "keep"), tarFile("dir/ignored.tmp", "ignore"), tarFile("dir/local", "ignore"),
		tarFile("omitted/sub/file", "ignore"), tarFile("tiny", "x"), tarFile("big", strings.Repeat("x", 21)),
		tarFile("hidden-data", "linked data"), tarLink("alias", "hidden-data", tar.TypeLink),
		tarLink("big-alias", "big", tar.TypeLink), tarLink("link", "tiny", tar.TypeSymlink),
		tarFile("dir/.erwtensoep", "local\n"), tarFile(".erwtensoep", "*.tmp\nomitted\nhidden-data\n"),
	})
	opts.DryRun = true
	if n, failures, err := sb.Merge(file); err != nil || failures != 0 || n != 4 {
		t.Fatalf("dry run: %d, %d, %v", n, failures, err)
	}
	if err := opts.Storage.ListBlobs("", func(key string) error { t.Errorf("dry run wrote %s", key); return nil }); err != nil {
		t.Fatal(err)
	}
	// Use a fresh superblock after a dry run, whose working maps are ephemeral.
	opts.DryRun = false
	sb, _ = GetSuperblock(opts)
	if n, failures, err := sb.Merge(file); err != nil || failures != 0 || n != 4 {
		t.Fatalf("merge: %d, %d, %v", n, failures, err)
	}
	for _, name := range []string{"dir/ignored.tmp", "dir/local", "omitted/sub/file", "tiny", "big", "hidden-data", "big-alias", ".erwtensoep"} {
		if sb.Entries[name] != nil {
			t.Errorf("unexpected imported path %s", name)
		}
	}
	data, err := restoreFromBlob(sb, getSingleRegularFile(t, sb, "alias").BlobID)
	if err != nil || string(data) != "linked data" {
		t.Fatalf("excluded hard-link target: %q, %v", data, err)
	}
	if err := sb.Write(); err != nil {
		t.Fatal(err)
	}
	report, err := CheckArchive(opts, nil)
	if err != nil || len(report.Issues) != 0 {
		t.Fatalf("inconsistent: %+v, %v", report, err)
	}
}

func TestMergeTarVersionsAndExplicitSources(t *testing.T) {
	sb, _ := newTarArchive(t)
	old, newer := tarFile("same", "old"), tarFile("same", "new")
	newer.header.ModTime = time.Unix(200, 0)
	for _, entries := range [][]tarTestEntry{{old, newer}, {newer}} {
		if _, failures, err := sb.Merge(writeTestTar(t, ".tar", entries)); err != nil || failures != 0 {
			t.Fatal(err)
		}
	}
	if len(sb.Entries["same"]) != 2 {
		t.Fatal("did not preserve member versions")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nested.tgz"), tarTestBytes(t, []tarTestEntry{tarFile("not-expanded", "data")}, true), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sb.Merge(dir); err != nil {
		t.Fatal(err)
	}
	if sb.Entries["nested.tgz"] == nil || sb.Entries["not-expanded"] != nil {
		t.Fatal("recursively expanded a tar file in a directory")
	}
	namedTarDirectory := filepath.Join(dir, "directory.tar")
	if err := os.Mkdir(namedTarDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namedTarDirectory, "inside"), []byte("dir"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, _, err := sb.Merge(namedTarDirectory); err != nil || n != 1 {
		t.Fatalf("directory named .tar: %d, %v", n, err)
	}
}

func TestMergeTarRejectsBadSourcesBeforeWriting(t *testing.T) {
	plain := tarTestBytes(t, []tarTestEntry{tarFile("good", "data")}, false)
	compressed := tarTestBytes(t, []tarTestEntry{tarFile("good", "data")}, true)
	badChecksum := bytes.Clone(compressed)
	badChecksum[len(badChecksum)-8] ^= 1
	cases := map[string][]byte{
		"bad.tar": []byte("not tar"), "bad.tar.gz": []byte("not gzip"),
		"truncated.tar": plain[:514], "truncated.tgz": compressed[:len(compressed)-4],
		"checksum.tar.gz": badChecksum,
	}
	for _, name := range []string{"/absolute", "../escape", "safe/../../escape"} {
		cases["path-"+strings.ReplaceAll(name, "/", "_")+".tar"] = tarTestBytes(t, []tarTestEntry{tarFile("good", "data"), tarFile(name, "bad")}, false)
	}
	for name, entries := range map[string][]tarTestEntry{
		"missing-link.tar":    {tarFile("good", "data"), tarLink("link", "missing", tar.TypeLink)},
		"unsafe-link.tar":     {tarLink("link", "../outside", tar.TypeLink)},
		"cycle.tar":           {tarLink("a", "b", tar.TypeLink), tarLink("b", "a", tar.TypeLink)},
		"link-to-symlink.tar": {tarLink("symbolic", "elsewhere", tar.TypeSymlink), tarLink("hard", "symbolic", tar.TypeLink)},
	} {
		cases[name] = tarTestBytes(t, entries, false)
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			sb, opts := newTarArchive(t)
			file := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			if n, failures, err := sb.Merge(file); err == nil || n != 0 || failures != 1 {
				t.Fatalf("bad tar accepted: %d, %d, %v", n, failures, err)
			}
			if len(sb.Entries)+len(sb.Blobs)+len(sb.Blocks) != 0 {
				t.Fatal("bad source changed the archive")
			}
			if err := opts.Storage.ListBlobs("", func(key string) error { t.Errorf("bad source wrote %s", key); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type shortTarReader struct{ io.Reader }

func (r shortTarReader) Read(p []byte) (int, error) {
	if len(p) > 137 {
		p = p[:137]
	}
	return r.Reader.Read(p)
}

func TestBlobifyStreamingBlockBoundaries(t *testing.T) {
	sb, _ := newTarArchive(t)
	payload := strings.Repeat("ab", BlockSize) + "end"
	file := &RegularFile{Path: "test", FileStats: FileStats{Size: "2097155", size: int64(len(payload))}}
	a, err := file.blobifyReader(sb, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	b, err := file.blobifyReader(sb, shortTarReader{strings.NewReader(payload)})
	if err != nil || a.ID != b.ID || len(b.Blocks) != 3 {
		t.Fatalf("reader affected block boundaries: %v", err)
	}
	if _, err := file.blobifyReader(sb, strings.NewReader("truncated")); err == nil {
		t.Fatal("short payload accepted")
	}
}
