package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"josvisser.nl/erwtensoep/internal/storage"
)

type CheckIssue struct {
	Kind, Key, Detail, Action string
}

// CheckReport holds a read-only inspection and the exact proposed repair.
// Keep the archive idle between checking and applying the plan.
type CheckReport struct {
	Issues                 []CheckIssue
	Blocks, Blobs, Entries int
	Recovered              []string
	RecreatedEntries       int
	EmptyDirectories       int

	sb            *Superblock
	original      []byte
	hadSuperblock bool
	newEntries    map[string]Entry
	remove        map[string]bool
	invalidPaths  []string
	blocked       error
}

func (r *CheckReport) add(kind, key, detail, action string) {
	r.Issues = append(r.Issues, CheckIssue{kind, key, detail, action})
}

func readRawObject(b storage.Backend, key string) ([]byte, error) {
	reader, err := b.GetBlob(key)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(reader)
	return data, errors.Join(err, reader.Close())
}

type checkIndex struct {
	entries       map[string]map[string]json.RawMessage
	blobs, blocks map[string]json.RawMessage
}

func (r *CheckReport) readIndex() (*checkIndex, error) {
	index := &checkIndex{}
	data, err := readRawObject(r.sb.storage, "superblock.json.gz")
	if errors.Is(err, os.ErrNotExist) {
		r.add("missing", "superblock.json.gz", "archive index is absent", "rebuild from valid stored objects")
		return index, nil
	}
	if err != nil {
		return nil, err
	}
	r.original, r.hadSuperblock = data, true
	var fields map[string]json.RawMessage
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err == nil {
		err = decodeCheckedJSON(gz, &fields)
		_ = gz.Close()
	}
	if err != nil || fields == nil {
		r.add("corrupt", "superblock.json.gz", fmt.Sprintf("unreadable index: %v", err), "rebuild from valid stored objects")
		return index, nil
	}
	// Do not rewrite an archive produced by an unsupported future format.
	if err := json.Unmarshal(fields["version"], &r.sb.Version); err == nil && r.sb.Version != 0 {
		return nil, fmt.Errorf("unsupported archive version %d", r.sb.Version)
	} else if err != nil {
		r.add("corrupt", "superblock.json.gz", "invalid or missing format version", "rebuild version 0 index")
		r.sb.Version = 0
	}
	if err := json.Unmarshal(fields["generation"], &r.sb.Generation); err != nil || r.sb.Generation < 0 || r.sb.Generation == int(^uint(0)>>1) {
		r.sb.Generation = 0
		r.add("corrupt", "superblock.json.gz", "invalid generation", "reset generation")
	}
	if err := json.Unmarshal(fields["last_update"], &r.sb.LastUpdate); err != nil {
		r.add("corrupt", "superblock.json.gz", "invalid last-update timestamp", "replace timestamp")
	}
	for _, section := range []struct {
		name string
		dst  interface{}
	}{
		{"entries", &index.entries}, {"blobs", &index.blobs}, {"blocks", &index.blocks},
	} {
		if err := json.Unmarshal(fields[section.name], section.dst); err != nil || string(fields[section.name]) == "null" {
			r.add("corrupt", "superblock.json.gz/"+section.name, "invalid index section", "rebuild section from disk")
		}
	}
	return index, nil
}

// CheckArchive validates all stored objects, including objects absent from the
// superblock. It never mutates storage. progress may be nil; otherwise it receives
// the phase, completed object count, and total (-1 while discovering the layout).
func CheckArchive(options *Options, progress func(phase string, count, total int)) (*CheckReport, error) {
	if options.ArchiveDir == "" {
		return nil, fmt.Errorf("archive-dir is required")
	}
	opts := *options
	var err error
	opts.ArchiveDir, err = filepath.Abs(opts.ArchiveDir)
	if err != nil {
		return nil, err
	}
	ensureStorage(&opts)
	r := &CheckReport{sb: newSuperblock(&opts), newEntries: make(map[string]Entry), remove: make(map[string]bool)}
	index, err := r.readIndex()
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress("directory layout", 0, -1)
	}
	inventory, err := storage.InspectArchive(opts.Storage)
	if err != nil {
		return nil, err
	}
	totals := make(map[string]int)
	if progress != nil {
		count := len(inventory.Objects) + len(inventory.InvalidPaths) + len(inventory.EmptyDirs)
		progress("directory layout", count, count)
		for _, key := range inventory.Objects {
			section, _, _ := strings.Cut(key, "/")
			totals[section]++
		}
	}
	seen := make(map[string]bool)
	for _, physical := range inventory.InvalidPaths {
		r.invalidPaths = append(r.invalidPaths, physical)
		r.add("invalid layout", physical, "not a regular object at a valid storage location", "remove object without following symlinks")
		parts := strings.Split(physical, "/")
		if len(parts) == 5 && len(parts[1]) == 4 && len(parts[2]) == 4 && len(parts[3]) == 4 && len(parts[4]) == 4 {
			seen[parts[0]+"/"+strings.Join(parts[1:], "")] = true
		}
	}
	r.EmptyDirectories = len(inventory.EmptyDirs)
	for _, dir := range inventory.EmptyDirs {
		r.add("empty directory", dir, "contains no objects", "remove directory")
	}
	entries := make(map[string]Entry)
	for _, section := range []string{"blocks", "blobs", "entries"} {
		count := 0
		if progress != nil {
			progress(section, count, totals[section])
		}
		for _, key := range inventory.Objects {
			if !strings.HasPrefix(key, section+"/") {
				continue
			}
			seen[key] = true
			id := strings.TrimPrefix(key, section+"/")
			var contentErr error
			if !validArchiveHash(id) {
				contentErr = &corruptObjectError{fmt.Errorf("invalid object hash %q", id)}
			} else if section == "blocks" {
				var block *Block
				block, contentErr = readCheckedBlock(opts.Storage, key, id)
				if contentErr == nil {
					r.sb.Blocks[id] = block
				}
			} else {
				contentErr = readCheckedObject(opts.Storage, key, true, func(reader io.Reader) error {
					var raw json.RawMessage
					if err := decodeCheckedJSON(reader, &raw); err != nil {
						return err
					}
					if section == "blobs" {
						blob, err := parseCheckedBlob(raw, id)
						if err == nil {
							r.sb.Blobs[id] = blob
						}
						return err
					}
					entry, err := parseCheckedEntry(raw, id)
					if err == nil {
						entries[id] = entry
					}
					return err
				})
			}
			if contentErr != nil {
				// Validation may fail after JSON was decoded (gzip trailer/size).
				switch section {
				case "blocks":
					delete(r.sb.Blocks, id)
				case "blobs":
					delete(r.sb.Blobs, id)
				case "entries":
					delete(entries, id)
				}
				var corrupt *corruptObjectError
				if errors.As(contentErr, &corrupt) {
					r.add("corrupt", key, contentErr.Error(), "remove object")
					r.remove[key] = true
				} else if errors.Is(contentErr, os.ErrNotExist) {
					return nil, fmt.Errorf("archive changed while scanning %s; run fsck again", key)
				} else {
					return nil, fmt.Errorf("read %s: %w", key, contentErr)
				}
			}
			count++
			if progress != nil {
				progress(section, count, totals[section])
			}
		}
	}

	// A valid blob descriptor is still unusable if any data block is absent.
	for id, blob := range r.sb.Blobs {
		var total int64
		problem := ""
		for _, blockID := range blob.Blocks {
			block := r.sb.Blocks[blockID]
			if block == nil {
				problem = "missing or corrupt block " + blockID
				break
			}
			total += int64(block.Size)
		}
		if problem == "" && total != blob.size {
			problem = fmt.Sprintf("declared size %s differs from block data size %d", blob.Size, total)
		}
		if problem != "" {
			r.add("broken blob", "blobs/"+id, problem, "remove blob and referring entries")
			r.remove["blobs/"+id] = true
			delete(r.sb.Blobs, id)
		}
	}

	// Valid entry records missing from disk can be restored from the index.
	// Corrupt records on disk are deliberately not resurrected from the index.
	indexedEntries := make(map[string]Entry)
	for name, versions := range index.entries {
		if len(versions) == 0 {
			r.add("index mismatch", "superblock.json.gz/entries/"+name, "empty version group", "remove empty group")
		}
		for id, raw := range versions {
			entry, err := parseCheckedEntry(raw, id)
			if err != nil || entry.GetPath() != name {
				r.add("index mismatch", "entries/"+id, "invalid entry metadata in superblock", "use valid disk metadata or remove index record")
				continue
			}
			indexedEntries[id] = entry
			if disk := entries[id]; disk != nil {
				if !sameEntry(entry, disk) {
					r.add("index mismatch", "entries/"+id, "disk entry differs from superblock", "use validated disk entry")
				}
			} else if !seen["entries/"+id] {
				entries[id] = entry
			}
		}
	}
	for id, entry := range entries {
		key := "entries/" + id
		if rf, ok := entry.(*RegularFile); ok {
			blob := r.sb.Blobs[rf.BlobID]
			if blob == nil || blob.size != rf.size {
				detail := "missing or corrupt blob " + rf.BlobID
				if blob != nil {
					detail = "file size differs from blob size"
				}
				r.add("broken entry", key, fmt.Sprintf("%q: %s", entry.GetPath(), detail), "remove entry and all its index references")
				if seen[key] {
					r.remove[key] = true
				}
				delete(entries, id)
				continue
			}
		}
		if !seen[key] {
			r.newEntries[id] = entry
			r.RecreatedEntries++
			r.add("missing entry", key, fmt.Sprintf("%q exists only in the superblock", entry.GetPath()), "recreate entry metadata from the valid index record")
		} else if indexedEntries[id] == nil {
			r.add("unindexed entry", key, fmt.Sprintf("valid stored entry for %q", entry.GetPath()), "add to superblock")
		}
	}

	for id := range indexedEntries {
		if entries[id] == nil && !seen["entries/"+id] {
			r.add("missing entry", "entries/"+id, "entry data is not recoverable", "remove index record")
		}
	}
	for _, section := range []struct {
		name    string
		indexed map[string]json.RawMessage
	}{
		{"blocks", index.blocks}, {"blobs", index.blobs},
	} {
		for id := range section.indexed {
			if !seen[section.name+"/"+id] {
				r.add("missing", section.name+"/"+id, "indexed object is absent from disk", "remove index record and invalid dependants")
			}
		}
	}

	for _, entry := range entries {
		r.addEntry(entry)
	}
	if err := r.recoverBlobs(entries); err != nil {
		r.blocked = err
	}
	blockRefs := make(map[string]bool)
	for id, blob := range r.sb.Blobs {
		for _, blockID := range blob.Blocks {
			blockRefs[blockID] = true
		}
		indexed, err := parseCheckedBlob(index.blobs[id], id)
		if err != nil || !reflect.DeepEqual(indexed, blob) {
			r.add("index mismatch", "blobs/"+id, "valid disk blob is absent or different in the superblock", "rebuild blob index record")
		}
	}
	for id, block := range r.sb.Blocks {
		if !blockRefs[id] {
			r.add("unused block", "blocks/"+id, "no surviving blob references this block", "remove block")
			r.remove["blocks/"+id] = true
			delete(r.sb.Blocks, id)
			continue
		}
		var indexed *Block
		err := json.Unmarshal(index.blocks[id], &indexed)
		if err != nil || indexed == nil || indexed.ID != block.ID || indexed.Size != block.Size {
			r.add("index mismatch", "blocks/"+id, "valid block differs from or is absent from the superblock", "rebuild block index record")
		}
	}
	r.Blocks, r.Blobs = len(r.sb.Blocks), len(r.sb.Blobs)
	for _, versions := range r.sb.Entries {
		r.Entries += len(versions)
	}
	sort.Slice(r.Issues, func(i, j int) bool {
		a, b := r.Issues[i], r.Issues[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
	return r, nil
}

func sameEntry(a, b Entry) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func (r *CheckReport) addEntry(entry Entry) {
	name := entry.GetPath()
	if r.sb.Entries[name] == nil {
		r.sb.Entries[name] = make(map[string]interface{})
	}
	r.sb.Entries[name][entry.hash()] = entry
}

func (r *CheckReport) recoverBlobs(entries map[string]Entry) error {
	refs := make(map[string]bool)
	occupied := make(map[string]bool)
	for _, entry := range entries {
		for p := entry.GetPath(); p != "."; p = path.Dir(p) {
			occupied[p] = true
		}
		if file, ok := entry.(*RegularFile); ok {
			refs[file.BlobID] = true
		}
	}
	var ids []string
	for id := range r.sb.Blobs {
		if !refs[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > 0 && r.sb.Entries["lost+found"] != nil {
		r.add("recovery blocked", "lost+found", "an archived file or symlink occupies the recovery directory", "move or remove that entry before repairing")
		return fmt.Errorf("cannot recover orphan blobs: lost+found is an archived file or symlink")
	}
	for _, id := range ids {
		name := "lost+found/" + id
		for suffix := 1; occupied[name]; suffix++ {
			name = fmt.Sprintf("lost+found/%s-%d", id, suffix)
		}
		blob := r.sb.Blobs[id]
		entry := &RegularFile{Path: name, BlobID: id, FileStats: FileStats{
			Owner: "-", Group: "-", Size: blob.Size, size: blob.size, Mode: 0600, MTime: time.Now().UTC().Truncate(time.Second),
		}}
		for entries[entry.hash()] != nil {
			entry.MTime = entry.MTime.Add(time.Second)
		}
		entries[entry.hash()] = entry
		r.newEntries[entry.hash()] = entry
		delete(r.remove, "entries/"+entry.hash())
		r.addEntry(entry)
		occupied[name] = true
		r.Recovered = append(r.Recovered, name)
		r.add("orphan blob", "blobs/"+id, "no valid entry refers to this blob", "recover as "+name)
	}
	return nil
}

// Repair applies the reviewed plan, preserves the old superblock, and writes a
// complete replacement index. Failures are reported; no repair is called clean
// if object deletion or directory pruning fails.
func (r *CheckReport) Repair() error {
	if r.blocked != nil {
		return r.blocked
	}
	if r.sb.options.DryRun {
		return fmt.Errorf("cannot repair in dry-run mode")
	}
	if len(r.Issues) == 0 {
		return nil
	}
	current, err := readRawObject(r.sb.storage, "superblock.json.gz")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("re-read superblock before repair: %w", err)
	}
	if r.hadSuperblock {
		if err != nil || !bytes.Equal(current, r.original) {
			return fmt.Errorf("superblock changed since inspection; run fsck again")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("superblock changed since inspection; run fsck again")
	}
	if r.hadSuperblock {
		if err := r.sb.storage.PutBlob("superblock.json.gz.fsck.bak", r.original); err != nil {
			return fmt.Errorf("back up superblock: %w", err)
		}
	}
	if inspector, ok := r.sb.storage.(storage.ArchiveInspector); ok {
		for _, physical := range r.invalidPaths {
			if err := inspector.RemoveInvalidArchivePath(physical); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	var ids []string
	for id := range r.newEntries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := r.sb.jsonWrite("entries", id, r.newEntries[id]); err != nil {
			return fmt.Errorf("write recovered entry: %w", err)
		}
	}
	if err := r.sb.Write(); err != nil {
		return fmt.Errorf("write repaired superblock: %w", err)
	}
	var errs []error
	var keys []string
	for key := range r.remove {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, section := range []string{"entries/", "blobs/", "blocks/"} {
		for _, key := range keys {
			if !strings.HasPrefix(key, section) {
				continue
			}
			if err := r.sb.storage.DeleteBlob(key); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove %s: %w", key, err))
			}
		}
	}
	if inspector, ok := r.sb.storage.(storage.ArchiveInspector); ok {
		if _, err := inspector.PruneEmptyArchiveDirs(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
