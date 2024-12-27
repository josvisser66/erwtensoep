package fs

import (
	"hash/crc64"
	"os"
)

type Symlink struct {
	FileStats
	Target string `json:"target"`
}

func FromJsonSL(o map[string]interface{}) *Symlink {
	target, ok := o["target"].(string)
	if !ok {
		return nil
	}
	sl := &Symlink{Target: target}
	sl.FileStats.FromJson(o)
	return sl
}

func (sl *Symlink) isEntry() {}

func (sl *Symlink) Hash(path string) uint64 {
	h := crc64.New(crc64.MakeTable(crc64.ECMA))

	h.Write([]byte("symlink"))
	h.Write([]byte(path))
	sl.FileStats.hash(h)
	h.Write([]byte(sl.Target))

	return h.Sum64()
}

func (sb *Superblock) mergeSymlink(fullPath string, info os.FileInfo, relativePath string) (int, error) {
	target, err := os.Readlink(fullPath)
	if err != nil {
		return 0, err
	}
	sl := &Symlink{
		FileStats: newFileStats(info),
		Target:    target,
	}
	h := sl.Hash(relativePath)
	entries, ok := sb.Entries[relativePath]
	if !ok {
		sb.Entries[relativePath] = make(map[uint64]interface{})
		sb.Entries[relativePath][h] = sl
		return 1, nil
	}

	if _, ok := entries[h]; ok {
		return 0, nil
	}

	entries[h] = sl
	return 1, nil
}
