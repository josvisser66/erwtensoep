package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

type Entry interface {
	isEntry()
	hash() string // 16 character hexadecimal
	getPath() string
}

type Superblock struct {
	Version    int                               `json:"version"`
	Generation int                               `json:"generation"`
	Entries    map[string]map[string]interface{} `json:"entries"`
	Blocks     map[string]*Block                 `json:"blocks"`
	Blobs      map[string]*Blob                  `json:"blobs"`
	LastUpdate time.Time                         `json:"last_update"` // will be encoded as a string

	srcDir       string
	archiveDir   string
	sbFile       string
	readFromDisk bool
	blocksDir    string
	entriesDir   string
	blobsDir     string

	bytesWritten int
	// TODO: Add total bytes written.
}

func (sb *Superblock) Write() error {
	fmt.Printf("Saving super block (%d)\n", sb.bytesWritten)
	if sb.readFromDisk {
		if err := os.Rename(sb.sbFile, fmt.Sprintf("%s.bak", sb.sbFile)); err != nil {
			return err
		}
	}
	sb.Generation++
	sb.LastUpdate = time.Now()
	b, err := json.Marshal(&sb)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "\t"); err != nil {
		return err
	}
	if err := os.WriteFile(sb.sbFile, buf.Bytes(), 0644); err != nil {
		return err
	}
	sb.bytesWritten = 0
	return nil
}

func (sb *Superblock) MaybeWrite() error {
	if sb.bytesWritten >= 10*1024*1024 {
		return sb.Write()
	}
	return nil
}

func mkdir(save *string, archiveDir, subDir string) (err error) {
	*save = path.Join(archiveDir, subDir)
	_, err = os.Stat(*save)
	if err == nil {
		return
	}
	if os.IsNotExist(err) {
		return os.MkdirAll(*save, 0700)
	}
	return err
}

func newSuperblock() *Superblock {
	return &Superblock{
		Version:    0,
		Generation: 0,
		Entries:    make(map[string]map[string]interface{}),
		Blobs:      make(map[string]*Blob),
		Blocks:     make(map[string]*Block),
		LastUpdate: time.Time{},
	}
}

func (sb *Superblock) fix(archiveDir, sbFile string) (err error) {
	// Fixes the interface{} back to actual objects.
	for _, m := range sb.Entries {
		for k, o := range m {
			if sl := FromJsonSL(o.(map[string]interface{})); sl != nil {
				m[k] = sl
			} else if rf := FromJsonRF(o.(map[string]interface{})); rf != nil {
				m[k] = rf
			} else {
				panic(fmt.Sprintf("Cannot convert: %v", o))
			}
		}
	}

	// Fix the blob size cache.
	for _, b := range sb.Blobs {
		b.size, err = strconv.ParseInt(b.Size, 10, 64)
		if err != nil {
			return
		}
	}

	sb.archiveDir = archiveDir
	sb.sbFile = sbFile

	// Makes sure that the metadata directories exists.
	if err = mkdir(&sb.blocksDir, archiveDir, "blocks"); err != nil {
		return
	}
	if err = mkdir(&sb.entriesDir, archiveDir, "entries"); err != nil {
		return
	}
	return mkdir(&sb.blobsDir, archiveDir, "blobs")
}

func ReadSuperblock(archiveDir string) (*Superblock, error) {
	if archiveDir == "" {
		return nil, errors.New("Please specify the location of the archive directory")
	}
	var sb *Superblock
	var err error

	archiveDir, err = filepath.Abs(archiveDir)
	if err != nil {
		return nil, err
	}
	sbFile := path.Join(archiveDir, "superblock.json")
	b, err := os.ReadFile(sbFile)
	if err == nil {
		if err = json.Unmarshal(b, &sb); err != nil {
			return nil, err
		}
		sb.readFromDisk = true
	} else if os.IsNotExist(err) {
		fmt.Println("Creating new super block")
		sb = newSuperblock()
	} else {
		return nil, err
	}

	return sb, sb.fix(archiveDir, sbFile)
}

func (sb *Superblock) jsonWrite(baseDir, hash string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return sb.dataWrite(baseDir, hash, b)
}

func (sb *Superblock) dataWrite(baseDir, hash string, data []byte) error {
	fname, err := filename(baseDir, hash)
	if err != nil {
		return err
	}
	fout, err := os.OpenFile(fname, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer fout.Close()
	gzipWriter := gzip.NewWriter(fout)
	gzipWriter.Extra = binary.LittleEndian.AppendUint32(gzipWriter.Extra, uint32(len(data)))
	defer gzipWriter.Close()
	n, err := gzipWriter.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("unexpected number of bytes written: %d, expected %d", n, len(data))
	}
	sb.bytesWritten += len(data)
	return nil
}
