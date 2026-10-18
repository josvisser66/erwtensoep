package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Inventory includes the physical layout that ListBlobs intentionally hides.
// InvalidPaths are literal filesystem-relative paths, never canonical blob keys.
type Inventory struct {
	Objects      []string
	InvalidPaths []string
	EmptyDirs    []string // Includes parents whose entire subtree is empty.
}

type ArchiveInspector interface {
	InspectArchive() (*Inventory, error)
	RemoveInvalidArchivePath(string) error
	PruneEmptyArchiveDirs() (int, error)
}

func InspectArchive(b Backend) (*Inventory, error) {
	if inspector, ok := b.(ArchiveInspector); ok {
		return inspector.InspectArchive()
	}
	result := &Inventory{}
	for _, prefix := range []string{"blocks/", "blobs/", "entries/"} {
		if err := b.ListBlobs(prefix, func(key string) error {
			result.Objects = append(result.Objects, key)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(result.Objects)
	return result, nil
}

func archiveSection(name string) bool {
	return name == "blocks" || name == "blobs" || name == "entries"
}

// InspectArchive never follows symlinks. Oddly placed files, symlinks, and
// special files are reported separately so fsck cannot read through them.
func (b *LocalBackend) InspectArchive() (*Inventory, error) {
	result := &Inventory{}
	var walk func(string, string) (bool, error)
	walk = func(prefix, rel string) (bool, error) {
		full := filepath.Join(b.root, rel)
		info, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if !info.IsDir() {
			hash, valid := hashFromFanout(strings.TrimPrefix(filepath.ToSlash(rel), prefix+"/"))
			if valid && info.Mode().IsRegular() {
				result.Objects = append(result.Objects, prefix+"/"+hash)
			} else {
				result.InvalidPaths = append(result.InvalidPaths, filepath.ToSlash(rel))
			}
			return false, nil
		}
		children, err := os.ReadDir(full)
		if err != nil {
			return false, err
		}
		empty := true
		for _, child := range children {
			childEmpty, err := walk(prefix, filepath.Join(rel, child.Name()))
			if err != nil {
				return false, err
			}
			empty = empty && childEmpty
		}
		if empty {
			result.EmptyDirs = append(result.EmptyDirs, filepath.ToSlash(rel))
		}
		return empty, nil
	}
	for _, prefix := range []string{"blocks", "blobs", "entries"} {
		if _, err := walk(prefix, prefix); err != nil {
			return nil, err
		}
	}
	sort.Strings(result.Objects)
	sort.Strings(result.InvalidPaths)
	return result, nil
}

func (b *LocalBackend) RemoveInvalidArchivePath(name string) error {
	rel, err := cleanRelPath(name)
	if err != nil || !archiveSection(strings.Split(filepath.ToSlash(rel), "/")[0]) {
		return pathInvalid("repair", name)
	}
	full := filepath.Join(b.root, rel)
	// Validate parents again before removing a physical path from an old scan.
	for dir := filepath.Dir(full); dir != b.root; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("repair: parent is not a directory: %s", dir)
		}
	}
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("repair: expected an invalid object, found directory %s", name)
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	return b.pruneParents(full)
}

func (b *LocalBackend) PruneEmptyArchiveDirs() (int, error) {
	inventory, err := b.InspectArchive()
	if err != nil {
		return 0, err
	}
	n := 0
	// InspectArchive lists children before parents. rmdir also checks that each
	// directory is still empty, so new objects can never be removed by the sweep.
	for _, name := range inventory.EmptyDirs {
		err := syscall.Rmdir(filepath.Join(b.root, filepath.FromSlash(name)))
		if err == nil {
			n++
		} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return n, &os.PathError{Op: "prune", Path: name, Err: err}
		}
	}
	return n, nil
}
