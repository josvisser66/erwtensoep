package storage

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// Hiding the optional BlobSize method exercises the compatibility fallback.
type unsizedBackend struct{ Backend }

func TestBlobSize(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"local", "memory", "fallback"} {
		t.Run(name, func(t *testing.T) {
			var backend Backend
			switch name {
			case "local":
				backend = NewLocalBackend(t.TempDir())
			case "memory":
				backend = NewMemoryBackend()
			default:
				backend = unsizedBackend{NewMemoryBackend()}
			}
			key := "blocks/0123456789abcdef"
			if err := backend.PutBlob(key, []byte("stored data")); err != nil {
				t.Fatal(err)
			}
			if got, err := BlobSize(backend, key); err != nil || got != 11 {
				t.Fatalf("BlobSize = %d, %v; want 11", got, err)
			}
			if _, err := BlobSize(backend, "missing"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing object error = %v", err)
			}
			if _, err := BlobSize(backend, "../outside"); err == nil {
				t.Fatal("expected invalid path error")
			}
		})
	}
}

type sizeReader struct {
	io.Reader
	closed   bool
	closeErr error
}

func (r *sizeReader) Close() error {
	r.closed = true
	return r.closeErr
}

type sizeReaderBackend struct {
	Backend
	r *sizeReader
}

func (b sizeReaderBackend) GetBlob(string) (io.ReadCloser, error) { return b.r, nil }

type failedSizeReader struct{ err error }

func (r failedSizeReader) Read([]byte) (int, error) { return 0, r.err }

func TestBlobSizeFallbackClosesOnErrors(t *testing.T) {
	t.Parallel()
	readErr, closeErr := errors.New("read failed"), errors.New("close failed")
	for _, failRead := range []bool{false, true} {
		r := &sizeReader{Reader: strings.NewReader("abc"), closeErr: closeErr}
		if failRead {
			r.Reader = failedSizeReader{readErr}
		}
		_, err := BlobSize(sizeReaderBackend{r: r}, "key")
		if !r.closed || !errors.Is(err, closeErr) || (failRead && !errors.Is(err, readErr)) {
			t.Fatalf("closed = %t, error = %v", r.closed, err)
		}
	}
}
