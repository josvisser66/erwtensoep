package fs

import (
	"errors"
	"fmt"
	"maps"
	"os"
)

// RemoveTargets returns sorted, unique paths using the same selection rules as
// RestoreTargets: exact files take precedence; a trailing slash selects only
// descendants. Removal permits selecting a file and its descendants together.
func (sb *Superblock) RemoveTargets(patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, errors.New("no remove patterns specified")
	}

	return sb.matchingPaths(patterns), nil
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
	selected := make(map[string][]string, len(paths))
	for _, name := range paths {
		for id := range sb.Entries[name] {
			selected[name] = append(selected[name], id)
		}
	}
	pathsRemoved, _, err := sb.removeVersions(selected)
	return pathsRemoved, err
}

// RemoveVersions removes only the selected IDs at exact archive paths and
// returns the number of versions removed. Empty selections, missing IDs/paths,
// and duplicates are ignored. Other versions keep their data references.
func (sb *Superblock) RemoveVersions(selected map[string][]string) (int, error) {
	_, versionsRemoved, err := sb.removeVersions(selected)
	return versionsRemoved, err
}

func (sb *Superblock) removeVersions(selected map[string][]string) (int, int, error) {
	entries := maps.Clone(sb.Entries)
	var garbage []string
	pathsRemoved, versionsRemoved := 0, 0
	for name, ids := range selected {
		// Clone the nested map too: a failed index write must leave every
		// version in the live index intact, not just restore the outer map.
		versions := maps.Clone(entries[name])
		removed := 0
		for _, id := range ids {
			if _, exists := versions[id]; !exists {
				continue
			}
			delete(versions, id)
			garbage = append(garbage, blobKey(sb.entriesDir, id))
			removed++
		}
		if removed == 0 {
			continue
		}
		if len(versions) == 0 {
			delete(entries, name)
		} else {
			entries[name] = versions
		}
		pathsRemoved++
		versionsRemoved += removed
	}
	if versionsRemoved == 0 || sb.options.DryRun {
		return pathsRemoved, versionsRemoved, nil
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
			return 0, 0, fmt.Errorf("cannot remove: remaining entry references missing blob %s", blobID)
		}
		for _, blockID := range blob.Blocks {
			if sb.Blocks[blockID] == nil {
				return 0, 0, fmt.Errorf("cannot remove: remaining blob references missing block %s", blockID)
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
		return 0, 0, err
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
	return pathsRemoved, versionsRemoved, errors.Join(errs...)
}
