package fs

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"josvisser.nl/erwtensoep/internal/utils"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

type Entry interface {
	isEntry()
	hash() string // 16 character hexadecimal
	GetPath() string
	GetFileStats() *FileStats
}

type Superblock struct {
	Version    int                               `json:"version"`
	Generation int                               `json:"generation"`
	Entries    map[string]map[string]interface{} `json:"entries"`
	Blocks     map[string]*Block                 `json:"blocks"`
	Blobs      map[string]*Blob                  `json:"blobs"`
	LastUpdate time.Time                         `json:"last_update"` // will be encoded as a string

	options      *Options
	srcDir       string
	sbFile       string
	readFromDisk bool

	blocksDir  string
	entriesDir string
	blobsDir   string

	bytesWritten      uint64
	totalBytesWritten uint64

	sigReceived atomic.Bool
}

func (sb *Superblock) Write() error {
	sw := utils.NewStopwatch("Writing the super block")
	defer sw.Stop()

	if sb.options.DryRun {
		return nil
	}
	if sb.readFromDisk {
		if err := os.Rename(sb.sbFile, fmt.Sprintf("%s.bak", sb.sbFile)); err != nil {
			return err
		}
	}
	sb.Generation++
	sb.LastUpdate = time.Now()
	fout, err := os.Create(sb.sbFile)
	if err != nil {
		return err
	}
	defer fout.Close()
	wr := gzip.NewWriter(fout)
	defer wr.Close()
	encoder := json.NewEncoder(wr)
	encoder.Encode(sb)
	sb.bytesWritten = 0
	return nil
}

func (sb *Superblock) maybeWrite() error {
	if sb.bytesWritten >= uint64(sb.options.SuperblockWriteThreshold) {
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

func newSuperblock(options *Options) *Superblock {
	return &Superblock{
		options:    options,
		Version:    0,
		Generation: 0,
		Entries:    make(map[string]map[string]interface{}),
		Blobs:      make(map[string]*Blob),
		Blocks:     make(map[string]*Block),
		LastUpdate: time.Time{},
	}
}

func (sb *Superblock) fix(sbFile string) (err error) {
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

	// Fix the blob size cache and the block back pointers to the blobs they are in.
	for _, blob := range sb.Blobs {
		blob.size, err = strconv.ParseInt(blob.Size, 10, 64)
		if err != nil {
			return
		}

		for _, blockID := range blob.Blocks {
			sb.Blocks[blockID].blobs = append(sb.Blocks[blockID].blobs, blob)
		}
	}

	sb.sbFile = sbFile

	// Makes sure that the metadata directories exists.
	if err = mkdir(&sb.blocksDir, sb.options.ArchiveDir, "blocks"); err != nil {
		return
	}
	if err = mkdir(&sb.entriesDir, sb.options.ArchiveDir, "entries"); err != nil {
		return
	}
	return mkdir(&sb.blobsDir, sb.options.ArchiveDir, "blobs")
}

func GetSuperblock(options *Options) (sb *Superblock, err error) {
	if options.ArchiveDir == "" {
		return nil, errors.New("Please specify the location of the archive directory")
	}

	options.ArchiveDir, err = filepath.Abs(options.ArchiveDir)
	if err != nil {
		return nil, err
	}
	sbFile := path.Join(options.ArchiveDir, "superblock.json.gz")
	var fin *os.File
	if fin, err = os.Open(sbFile); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Creating new super block")
			sb = newSuperblock(options)
		} else {
			return nil, err
		}
	} else {
		var rdr *gzip.Reader

		rdr, err = gzip.NewReader(fin)
		if err != nil {
			return nil, err
		}
		if err = json.NewDecoder(rdr).Decode(&sb); err != nil {
			return nil, err
		}

		sb.readFromDisk = true
		sb.options = options
	}

	return sb, sb.fix(sbFile)
}

func (sb *Superblock) jsonWrite(baseDir, hash string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return sb.dataWrite(baseDir, hash, b)
}

func (sb *Superblock) dataWrite(baseDir, hash string, data []byte) error {
	if sb.options.DryRun {
		sb.totalBytesWritten += uint64(len(data))
		return nil
	}
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
	sb.bytesWritten += uint64(len(data))
	sb.totalBytesWritten += uint64(len(data))
	return nil
}
