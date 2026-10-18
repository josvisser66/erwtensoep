package fs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"josvisser.nl/erwtensoep/internal/storage"
)

type trackedBackend struct {
	storage.Backend
	opens, closes int
}

func (b *trackedBackend) GetBlob(key string) (io.ReadCloser, error) {
	r, err := b.Backend.GetBlob(key)
	if err != nil {
		return nil, err
	}
	b.opens++
	return &trackedReader{ReadCloser: r, backend: b}, nil
}

type trackedReader struct {
	io.ReadCloser
	backend *trackedBackend
}

func (r *trackedReader) Close() error {
	r.backend.closes++
	return r.ReadCloser.Close()
}

func TestOpenRegularFileStreamsAndClosesBlocks(t *testing.T) {
	backend := &trackedBackend{Backend: storage.NewMemoryBackend()}
	sb := newSuperblock(&Options{Storage: backend})
	first, second := bytes.Repeat([]byte("a"), BlockSize), []byte("last block\x00\xff")
	for id, data := range map[string][]byte{"first": first, "second": second} {
		if err := sb.dataWrite(sb.blocksDir, id, data); err != nil {
			t.Fatal(err)
		}
	}
	sb.Blobs["file"] = &Blob{Blocks: []string{"first", "second"}}
	file := &RegularFile{BlobID: "file"}
	r, err := sb.OpenRegularFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if backend.opens != 0 {
		t.Fatal("opened blocks before the first read")
	}
	prefix := make([]byte, 7)
	if _, err := io.ReadFull(r, prefix); err != nil {
		t.Fatal(err)
	}
	if backend.opens != 1 {
		t.Fatalf("opened %d blocks for a partial read", backend.opens)
	}
	if err := r.Close(); err != nil || backend.closes != 1 {
		t.Fatalf("early close: closed %d blocks, err %v", backend.closes, err)
	}
	if _, err := r.Read(prefix); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if err := r.Close(); err != nil || backend.closes != 1 {
		t.Fatalf("repeated close: closed %d blocks, err %v", backend.closes, err)
	}
	r, err = sb.OpenRegularFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, append(first, second...)) {
		t.Fatalf("streamed data mismatch: length %d, err %v", len(got), err)
	}
	if err := r.Close(); err != nil || backend.opens != backend.closes {
		t.Fatalf("open/close imbalance: %d/%d, err %v", backend.opens, backend.closes, err)
	}
}

func TestOpenRegularFileEmptyMissingAndCorrupt(t *testing.T) {
	backend := &trackedBackend{Backend: storage.NewMemoryBackend()}
	sb := newSuperblock(&Options{Storage: backend})
	if _, err := sb.OpenRegularFile(&RegularFile{BlobID: "missing"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing blob: %v", err)
	}
	sb.Blobs["empty"] = &Blob{}
	sb.Blobs["missing-block"] = &Blob{Blocks: []string{"missing"}}
	sb.Blobs["bad-header"] = &Blob{Blocks: []string{"bad"}}
	sb.Blobs["truncated"] = &Blob{Blocks: []string{"truncated"}}
	if err := backend.PutBlob("blocks/bad", []byte("not gzip")); err != nil {
		t.Fatal(err)
	}
	if err := sb.dataWrite(sb.blocksDir, "truncated", []byte("data")); err != nil {
		t.Fatal(err)
	}
	raw, err := backend.Backend.GetBlob("blocks/truncated")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if err := backend.PutBlob("blocks/truncated", data[:len(data)-4]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"empty", "missing-block", "bad-header", "truncated"} {
		t.Run(id, func(t *testing.T) {
			r, err := sb.OpenRegularFile(&RegularFile{BlobID: id})
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if id == "empty" {
				if err != nil || len(data) != 0 {
					t.Fatalf("empty file: %q, %v", data, err)
				}
			} else if err == nil {
				t.Fatal("expected read error")
			}
			if backend.opens != backend.closes {
				t.Fatalf("leaked block reader: %d opens, %d closes", backend.opens, backend.closes)
			}
		})
	}
}
