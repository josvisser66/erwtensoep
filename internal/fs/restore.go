package fs

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Restore extracts the newest versions matching patterns into outDir, without
// prompting. Use RestoreVersions to restore explicitly selected older versions.
func (sb *Superblock) Restore(patterns []string, outDir string) (int, error) {
	paths, err := sb.RestoreTargets(patterns)
	if err != nil {
		return 0, err
	}
	selected := make(map[string]string, len(paths))
	for _, name := range paths {
		selected[name] = latestVersionID(sb.Entries[name])
	}
	return sb.RestoreVersions(selected, outDir)
}

// RestoreTargets returns matching index paths in sorted order.
func (sb *Superblock) RestoreTargets(patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, errors.New("no restore patterns specified")
	}
	paths := sb.matchingPaths(patterns)
	return paths, validateRestorePaths(paths)
}

// A regular file or symlink cannot also be a parent directory in one restore.
// Check every ancestor (not just adjacent sorted names) before any output write.
func validateRestorePaths(paths []string) error {
	selected := make(map[string]bool, len(paths))
	for _, name := range paths {
		selected[name] = true
	}
	for _, name := range paths {
		for parent := filepath.Dir(name); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			if selected[parent] {
				return fmt.Errorf("restore path conflict: %q is selected as a file or symlink and as a directory containing %q; select %q for the file or %q for the directory, narrow broad patterns, or restore them separately into different output directories", parent, name, parent, filepath.ToSlash(parent)+"/")
			}
		}
	}
	return nil
}

// matchingPaths is shared by restore and removal. Bare file names select only
// that entry; directories select descendants. Results are sorted and unique.
func (sb *Superblock) matchingPaths(patterns []string) []string {
	matched := make(map[string]bool)
	for _, pattern := range patterns {
		p := normalizePattern(pattern)
		// Exact file operands take precedence over same-named directories.
		// Slash-suffixed operands select descendants only; globs remain broad.
		if !strings.ContainsAny(p, "*?[") && !strings.HasSuffix(p, "/") && len(sb.Entries[p]) > 0 {
			matched[p] = true
			continue
		}
		for name := range sb.Entries {
			if matchesPattern(filepath.ToSlash(name), p) {
				matched[name] = true
			}
		}
	}
	paths := make([]string, 0, len(matched))
	for name := range matched {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

// RestoreVersions restores one selected version ID per exact index path.
// Validate the entire selection before writing any output. Selection and
// cancellation belong to the caller; library operations never prompt.
func (sb *Superblock) RestoreVersions(selected map[string]string, outDir string) (int, error) {
	if outDir == "" {
		return 0, errors.New("output directory not specified")
	}

	outDir, err := filepath.Abs(outDir)
	if err != nil {
		return 0, err
	}

	paths := make([]string, 0, len(selected))
	entries := make(map[string]Entry, len(selected))
	for p, id := range selected {
		entry, ok := sb.Entries[p][id].(Entry)
		if !ok || entry.GetPath() != p {
			return 0, fmt.Errorf("%s: invalid saved version %s", p, id)
		}
		switch entry.(type) {
		case *RegularFile, *Symlink:
		default:
			return 0, fmt.Errorf("%s: unknown entry type", p)
		}
		entries[p] = entry
		paths = append(paths, p)
	}
	sort.Strings(paths)

	if err := validateRestorePaths(paths); err != nil {
		return 0, err
	}
	restored := 0
	for _, p := range paths {
		entry := entries[p]
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

func latestVersionID(versions map[string]interface{}) string {
	var (
		maxEntry Entry
		maxID    string
	)

	for id, v := range versions {
		e, ok := v.(Entry)
		if !ok {
			continue
		}
		if maxEntry == nil || e.GetFileStats().MTime.After(maxEntry.GetFileStats().MTime) ||
			(e.GetFileStats().MTime.Equal(maxEntry.GetFileStats().MTime) && id > maxID) {
			maxEntry, maxID = e, id
		}
	}

	return maxID
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
