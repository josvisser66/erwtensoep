package fs

import (
	"fmt"
	"hash/crc64"
	"os"
)

type Symlink struct {
	FileStats
	Target string `json:"target"`
	Path   string `json:"path"`
}

func (sl *Symlink) isEntry()        {}
func (sl *Symlink) getPath() string { return sl.Path }

func FromJsonSL(o map[string]interface{}) *Symlink {
	target, ok := o["target"].(string)
	if !ok {
		return nil
	}
	path, ok := o["path"].(string)
	if !ok {
		return nil
	}
	sl := &Symlink{Path: path, Target: target}
	sl.FileStats.FromJson(o)
	return sl
}

func (sl *Symlink) hash() string {
	h := crc64.New(crc64.MakeTable(crc64.ECMA))

	h.Write([]byte("symlink"))
	h.Write([]byte(sl.Path))
	sl.FileStats.hash(h)
	h.Write([]byte(sl.Target))

	return fmt.Sprintf("%016x", h.Sum64())
}

func (sb *Superblock) mergeSymlink(fullPath string, info os.FileInfo, relativePath string) (int, error) {
	target, err := os.Readlink(fullPath)
	if err != nil {
		return 0, err
	}
	sl := &Symlink{
		FileStats: newFileStats(info),
		Target:    target,
		Path:      relativePath,
	}
	h := sl.hash()
	entries, ok := sb.Entries[relativePath]
	if !ok {
		fmt.Printf("%s -> %s\n", fullPath, sl.Target)
		sb.Entries[relativePath] = make(map[string]interface{})
		sb.Entries[relativePath][h] = sl
		return 1, sb.jsonWrite(sb.entriesDir, h, sl)
	}

	if _, ok := entries[h]; ok {
		return 0, nil
	}

	if sb.options.DryRun {
		return 1, nil
	}

	entries[h] = sl
	return 1, sb.jsonWrite(sb.entriesDir, h, sl)
}
