package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type LocalBackend struct {
	root string
}

// NewLocalBackend returns a filesystem-backed storage implementation.
func NewLocalBackend(root string) *LocalBackend {
	return &LocalBackend{root: filepath.Clean(root)}
}

func (b *LocalBackend) GetBlob(key string) (io.ReadCloser, error) {
	full, err := b.keyPath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (b *LocalBackend) BlobSize(key string) (int64, error) {
	full, err := b.keyPath(key)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, pathInvalid("stat", key)
	}
	return info.Size(), nil
}

func (b *LocalBackend) PutBlob(key string, data []byte) error {
	full, err := b.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		return err
	}
	// Replace objects atomically, including the superblock. A failed write must
	// not truncate the existing index before a repair or removal can roll back.
	f, err := os.CreateTemp(filepath.Dir(full), ".erwtensoep-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), full)
}

func (b *LocalBackend) DeleteBlob(key string) error {
	full, err := b.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	return b.pruneParents(full)
}

func (b *LocalBackend) pruneParents(full string) error {
	rel, err := filepath.Rel(b.root, full)
	if err != nil || !archiveSection(strings.Split(filepath.ToSlash(rel), "/")[0]) {
		return err
	}
	for dir := filepath.Dir(full); dir != b.root; dir = filepath.Dir(dir) {
		err := syscall.Rmdir(dir) // Only directories; never unlink a replacement file.
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return &os.PathError{Op: "prune", Path: dir, Err: err}
		}
	}
	return nil
}

func (b *LocalBackend) ListBlobs(prefix string, fn func(key string) error) error {
	relPrefix, err := cleanRelPath(prefix)
	if err != nil {
		return pathInvalid("list", prefix)
	}
	normalizedPrefix := relPrefix
	if normalizedPrefix == "." {
		normalizedPrefix = ""
	}
	if normalizedPrefix != "" && !strings.HasSuffix(normalizedPrefix, "/") {
		normalizedPrefix += "/"
	}

	fullPrefix := filepath.Join(b.root, relPrefix)
	if _, err := os.Stat(fullPrefix); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if isFanoutPrefix(normalizedPrefix) {
		var keys []string
		err := filepath.WalkDir(fullPrefix, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(fullPrefix, path)
			if err != nil {
				return err
			}
			hash, ok := hashFromFanout(rel)
			if !ok {
				return nil
			}
			keys = append(keys, normalizedPrefix+hash)
			return nil
		})
		if err != nil {
			return err
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := fn(key); err != nil {
				return err
			}
		}
		return nil
	}

	var keys []string
	err = filepath.WalkDir(fullPrefix, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.root, path)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(key, normalizedPrefix) {
			if err := fn(key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *LocalBackend) keyPath(key string) (string, error) {
	rel, err := cleanRelPath(key)
	if err != nil {
		return "", pathInvalid("key", key)
	}
	prefix, hash, ok := splitFanoutKey(rel)
	if ok {
		return filepath.Join(b.root, fanoutPath(prefix, hash)), nil
	}
	return filepath.Join(b.root, rel), nil
}

func splitFanoutKey(key string) (prefix string, hash string, ok bool) {
	key = filepath.ToSlash(key)
	if strings.HasPrefix(key, "blocks/") || strings.HasPrefix(key, "entries/") || strings.HasPrefix(key, "blobs/") {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		prefix = parts[0]
		hash = parts[1]
		if strings.Contains(hash, "/") || len(hash) != 16 {
			return "", "", false
		}
		return prefix, hash, true
	}
	return "", "", false
}

func isFanoutPrefix(prefix string) bool {
	return prefix == "blocks/" || prefix == "entries/" || prefix == "blobs/"
}

func fanoutPath(prefix, hash string) string {
	return filepath.Join(prefix, hash[0:4], hash[4:8], hash[8:12], hash[12:16])
}

func hashFromFanout(rel string) (string, bool) {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) != 4 {
		return "", false
	}
	for _, p := range parts {
		if len(p) != 4 {
			return "", false
		}
	}
	return parts[0] + parts[1] + parts[2] + parts[3], true
}
