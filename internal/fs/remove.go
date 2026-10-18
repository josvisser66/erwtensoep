package fs

import (
	"errors"
	"fmt"
	"maps"
	"os"
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
	return sb.RemovePaths(targets)
}

// RemovePaths removes all versions of exact archive paths, without interpreting
// glob characters. Missing paths and duplicate operands are ignored.
// The updated index is saved before deleting unreferenced objects, so a failed
// index write does not first destroy the data it still references.
func (sb *Superblock) RemovePaths(paths []string) (int, error) {
	entries := maps.Clone(sb.Entries)
	var garbage []string
	removed := 0
	for _, name := range paths {
		versions, ok := entries[name]
		if !ok {
			continue
		}
		for id := range versions {
			garbage = append(garbage, blobKey(sb.entriesDir, id))
		}
		delete(entries, name)
		removed++
	}
	if removed == 0 || sb.options.DryRun {
		return removed, nil
	}

	blobRefs := make(map[string]bool)
	blockRefs := make(map[string]bool)
	entryRefs := make(map[string]bool)
	for _, versions := range entries {
		for id, v := range versions {
			entryRefs[blobKey(sb.entriesDir, id)] = true
			if rf, ok := v.(*RegularFile); ok {
				blobRefs[rf.BlobID] = true
			}
		}
	}
	for blobID := range blobRefs {
		blob := sb.Blobs[blobID]
		if blob == nil {
			return 0, fmt.Errorf("cannot remove: remaining entry references missing blob %s", blobID)
		}
		for _, blockID := range blob.Blocks {
			if sb.Blocks[blockID] == nil {
				return 0, fmt.Errorf("cannot remove: remaining blob references missing block %s", blockID)
			}
			blockRefs[blockID] = true
		}
	}
	blobs := maps.Clone(sb.Blobs)
	blocks := maps.Clone(sb.Blocks)
	for id := range blobs {
		if !blobRefs[id] {
			garbage = append(garbage, blobKey(sb.blobsDir, id))
			delete(blobs, id)
		}
	}
	for id := range blocks {
		if !blockRefs[id] {
			garbage = append(garbage, blobKey(sb.blocksDir, id))
			delete(blocks, id)
		}
	}

	oldEntries, oldBlobs, oldBlocks := sb.Entries, sb.Blobs, sb.Blocks
	oldGeneration, oldUpdate := sb.Generation, sb.LastUpdate
	sb.Entries, sb.Blobs, sb.Blocks = entries, blobs, blocks
	if err := sb.Write(); err != nil {
		sb.Entries, sb.Blobs, sb.Blocks = oldEntries, oldBlobs, oldBlocks
		sb.Generation, sb.LastUpdate = oldGeneration, oldUpdate
		return 0, err
	}
	for _, block := range blocks {
		block.blobs = nil
	}
	for _, blob := range blobs {
		for _, blockID := range blob.Blocks {
			blocks[blockID].blobs = append(blocks[blockID].blobs, blob)
		}
	}

	var errs []error
	for _, key := range garbage {
		if entryRefs[key] {
			continue
		}
		if err := sb.storage.DeleteBlob(key); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removed entries, but could not delete unused object %s: %w", key, err))
		}
	}
	return removed, errors.Join(errs...)
}
