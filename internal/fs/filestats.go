package fs

import (
	"encoding/binary"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

type FileStats struct {
	Owner string      `json:"owner"`
	Group string      `json:"group"`
	Size  string      `json:"size"` // string because of JSON encoding issues with large numbers
	size  int64       // cache for Size
	Mode  fs.FileMode `json:"mode"`  // is a small number
	MTime time.Time   `json:"mtime"` // will be encoded as a string
}

func (f *FileStats) FromJson(o map[string]interface{}) {
	var ok bool
	var err error

	owner := o["owner"]
	if f.Owner, ok = owner.(string); !ok {
		panic(fmt.Sprintf("Cannot convert owner: %v (%T)", owner, owner))
	}
	group := o["group"]
	if f.Group, ok = group.(string); !ok {
		panic(fmt.Sprintf("Cannot convert group: %v (%T)", group, group))
	}
	size := o["size"]
	if sz, ok := o["size"].(string); !ok {
		panic(fmt.Sprintf("Cannot convert size: %v (%T)", size, size))
	} else {
		f.Size = sz
		f.size, err = strconv.ParseInt(sz, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("Cannot convert size to uint64: %v", size))
		}
	}
	mode := o["mode"]
	if fm, ok := mode.(float64); !ok {
		panic(fmt.Sprintf("Cannot convert file mode: %v (%T)", mode, mode))
	} else {
		f.Mode = fs.FileMode(fm)
	}
	mtime := o["mtime"]
	if s, ok := mtime.(string); !ok {
		panic(fmt.Sprintf("Cannot convert mtime: %v (%T)", mtime, mtime))
	} else {
		if f.MTime, err = time.Parse(time.RFC3339Nano, s); err != nil {
			panic(fmt.Sprintf("Cannot parse mtime: %s", s))
		}
	}
}

func newFileStats(info os.FileInfo) FileStats {
	fs := FileStats{
		size:  info.Size(),
		Size:  fmt.Sprintf("%d", info.Size()),
		Mode:  info.Mode(),
		MTime: info.ModTime(),
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fs
	}

	if usr, err := user.LookupId(fmt.Sprintf("%d", stat.Uid)); err == nil {
		fs.Owner = usr.Username
	}

	if grp, err := user.LookupGroupId(fmt.Sprintf("%d", stat.Gid)); err == nil {
		fs.Group = grp.Name
	}

	return fs
}

func (fs *FileStats) hash(h hash.Hash64) {
	h.Write([]byte(fs.Owner))
	h.Write([]byte(fs.Group))
	h.Write([]byte(fs.Size))
	b4 := make([]byte, 4)
	binary.LittleEndian.PutUint32(b4, uint32(fs.Mode))
	h.Write(b4)
	binary.LittleEndian.PutUint32(b4, uint32(fs.MTime.Unix()))
	h.Write(b4)
}
