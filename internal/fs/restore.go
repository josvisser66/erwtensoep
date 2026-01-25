package fs

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Restore extracts files and symlinks that match the provided patterns into outDir.
func (sb *Superblock) Restore(patterns []string, outDir string) (int, error) {
	if len(patterns) == 0 {
		return 0, errors.New("no restore patterns specified")
	}
	if outDir == "" {
		return 0, errors.New("output directory not specified")
	}

	outDir, err := filepath.Abs(outDir)
	if err != nil {
		return 0, err
	}

	normalized := make([]string, 0, len(patterns))
	for _, p := range patterns {
		normalized = append(normalized, normalizePattern(p))
	}

	selected := make(map[string]Entry)
	for path, versions := range sb.Entries {
		entryPath := filepath.ToSlash(path)
		if !matchesAnyPattern(entryPath, normalized) {
			continue
		}
		selected[entryPath] = latestVersion(versions)
	}

	paths := make([]string, 0, len(selected))
	for p := range selected {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	restored := 0
	for _, p := range paths {
		entry := selected[p]
		switch e := entry.(type) {
		case *RegularFile:
			if err := sb.restoreRegularFile(e, outDir); err != nil {
				return restored, err
			}
		case *Symlink:
			if err := sb.restoreSymlink(e, outDir); err != nil {
				return restored, err
			}
		default:
			return restored, errors.New("unknown entry type")
		}
		restored++
	}

	return restored, nil
}

func latestVersion(versions map[string]interface{}) Entry {
	var (
		maxEntry Entry
		maxTime  time.Time
	)

	for _, v := range versions {
		e := v.(Entry)
		if t := e.GetFileStats().MTime; t.After(maxTime) {
			maxTime = t
			maxEntry = e
		}
	}

	return maxEntry
}

func normalizePattern(pattern string) string {
	if pattern == "" {
		return pattern
	}
	// Preserve a trailing slash to indicate directory prefix matches.
	trailing := strings.HasSuffix(pattern, "/")
	p := filepath.ToSlash(filepath.Clean(pattern))
	if trailing && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func matchesAnyPattern(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchesPattern(path, pattern) {
			return true
		}
	}
	return false
}

func matchesPattern(path string, pattern string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(path, pattern)
	}
	if strings.ContainsAny(pattern, "*?[") {
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		return false
	}
	if path == pattern {
		return true
	}
	if strings.HasPrefix(path, pattern+"/") {
		return true
	}
	return false
}

func (sb *Superblock) restoreRegularFile(rf *RegularFile, outDir string) error {
	blob, ok := sb.Blobs[rf.BlobID]
	if !ok {
		return os.ErrNotExist
	}

	outPath := filepath.Join(outDir, filepath.FromSlash(rf.Path))
	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		return err
	}
	_ = os.RemoveAll(outPath)
	fout, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, rf.Mode.Perm())
	if err != nil {
		return err
	}
	defer fout.Close()

	for _, blockID := range blob.Blocks {
		key := blobKey(sb.blocksDir, blockID)
		fin, err := sb.storage.GetBlob(key)
		if err != nil {
			return err
		}
		rdr, err := gzip.NewReader(fin)
		if err != nil {
			_ = fin.Close()
			return err
		}
		if _, err := io.Copy(fout, rdr); err != nil {
			_ = rdr.Close()
			_ = fin.Close()
			return err
		}
		if err := rdr.Close(); err != nil {
			_ = fin.Close()
			return err
		}
		if err := fin.Close(); err != nil {
			return err
		}
	}

	_ = os.Chtimes(outPath, rf.MTime, rf.MTime)
	_ = os.Chmod(outPath, rf.Mode.Perm())
	return nil
}

func (sb *Superblock) restoreSymlink(sl *Symlink, outDir string) error {
	outPath := filepath.Join(outDir, filepath.FromSlash(sl.Path))
	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		return err
	}
	_ = os.RemoveAll(outPath)
	if err := os.Symlink(sl.Target, outPath); err != nil {
		return err
	}
	return nil
}
