package storage

import (
	"bytes"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryBackend stores objects in memory. It is intended for tests only.
type MemoryBackend struct {
	mu    sync.RWMutex
	files map[string]*memEntry
}

type memEntry struct {
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{files: make(map[string]*memEntry)}
}

func (b *MemoryBackend) GetBlob(key string) (io.ReadCloser, error) {
	orig := key
	key, err := cleanRelPath(key)
	if err != nil {
		return nil, pathInvalid("get", orig)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if entry, ok := b.files[key]; ok {
		return io.NopCloser(bytes.NewReader(entry.data)), nil
	}
	return nil, pathNotExist("get", key)
}

func (b *MemoryBackend) PutBlob(key string, data []byte) error {
	orig := key
	key, err := cleanRelPath(key)
	if err != nil {
		return pathInvalid("put", orig)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[key] = &memEntry{
		data:    append([]byte(nil), data...),
		mode:    0600,
		modTime: time.Now(),
	}
	return nil
}

func (b *MemoryBackend) DeleteBlob(key string) error {
	orig := key
	key, err := cleanRelPath(key)
	if err != nil {
		return pathInvalid("delete", orig)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.files[key]; ok {
		delete(b.files, key)
		return nil
	}
	return pathNotExist("delete", key)
}

func (b *MemoryBackend) ListBlobs(prefix string, fn func(key string) error) error {
	orig := prefix
	prefix, err := cleanRelPath(prefix)
	if err != nil {
		return pathInvalid("list", orig)
	}
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var keys []string
	b.mu.RLock()
	for key := range b.files {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	b.mu.RUnlock()
	sort.Strings(keys)
	for _, key := range keys {
		if err := fn(key); err != nil {
			return err
		}
	}
	return nil
}

func pathNotExist(op, path string) error {
	return &os.PathError{Op: op, Path: path, Err: os.ErrNotExist}
}
