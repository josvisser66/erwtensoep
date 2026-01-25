package fs

import (
	"errors"
	"path/filepath"
	"sort"
)

// RemoveTargets returns archive paths that match the provided patterns.
func (sb *Superblock) RemoveTargets(patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, errors.New("no remove patterns specified")
	}

	normalized := make([]string, 0, len(patterns))
	for _, p := range patterns {
		normalized = append(normalized, normalizePattern(p))
	}

	var targets []string
	for path := range sb.Entries {
		if matchesAnyPattern(filepath.ToSlash(path), normalized) {
			targets = append(targets, filepath.ToSlash(path))
		}
	}

	sort.Strings(targets)
	return targets, nil
}

// Remove deletes entries matching patterns and prunes unreferenced blobs and blocks.
func (sb *Superblock) Remove(patterns []string) (int, error) {
	targets, err := sb.RemoveTargets(patterns)
	if err != nil {
		return 0, err
	}

	if len(targets) == 0 {
		return 0, nil
	}

	for _, path := range targets {
		versions := sb.Entries[path]
		for id := range versions {
			if !sb.options.DryRun {
				_ = sb.storage.DeleteBlob(blobKey(sb.entriesDir, id))
			}
		}
		delete(sb.Entries, path)
	}

	blobRefs := make(map[string]struct{})
	blockRefs := make(map[string]struct{})

	for _, versions := range sb.Entries {
		for _, v := range versions {
			if rf, ok := v.(*RegularFile); ok {
				blobRefs[rf.BlobID] = struct{}{}
			}
		}
	}

	for blobID := range blobRefs {
		if blob, ok := sb.Blobs[blobID]; ok {
			for _, blockID := range blob.Blocks {
				blockRefs[blockID] = struct{}{}
			}
		}
	}

	for blobID, blob := range sb.Blobs {
		if _, ok := blobRefs[blobID]; ok {
			continue
		}
		if !sb.options.DryRun {
			_ = sb.storage.DeleteBlob(blobKey(sb.blobsDir, blobID))
		}
		delete(sb.Blobs, blobID)
		for _, blockID := range blob.Blocks {
			if _, ok := blockRefs[blockID]; !ok {
				if !sb.options.DryRun {
					_ = sb.storage.DeleteBlob(blobKey(sb.blocksDir, blockID))
				}
				delete(sb.Blocks, blockID)
			}
		}
	}

	for blockID := range sb.Blocks {
		if _, ok := blockRefs[blockID]; ok {
			continue
		}
		if !sb.options.DryRun {
			_ = sb.storage.DeleteBlob(blobKey(sb.blocksDir, blockID))
		}
		delete(sb.Blocks, blockID)
	}

	if err := sb.Write(); err != nil {
		return len(targets), err
	}

	return len(targets), nil
}
