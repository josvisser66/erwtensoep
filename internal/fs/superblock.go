package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"josvisser.nl/erwtensoep/internal/storage"
	"josvisser.nl/erwtensoep/internal/utils"
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
	storage      storage.Backend

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
		// Keep a backup of the previous superblock for manual recovery.
		if fin, err := sb.storage.GetBlob(sb.sbFile); err == nil {
			defer fin.Close()
			if data, err := io.ReadAll(fin); err == nil {
				_ = sb.storage.PutBlob(fmt.Sprintf("%s.bak", sb.sbFile), data)
			}
		}
	}
	sb.Generation++
	sb.LastUpdate = time.Now()
	var buf bytes.Buffer
	wr := gzip.NewWriter(&buf)
	encoder := json.NewEncoder(wr)
	if err := encoder.Encode(sb); err != nil {
		_ = wr.Close()
		return err
	}
	if err := wr.Close(); err != nil {
		return err
	}
	if err := sb.storage.PutBlob(sb.sbFile, buf.Bytes()); err != nil {
		return err
	}
	sb.bytesWritten = 0
	return nil
}

func (sb *Superblock) maybeWrite() error {
	// Periodically persist the superblock based on accumulated bytes written.
	if sb.bytesWritten >= uint64(sb.options.SuperblockWriteThreshold) {
		return sb.Write()
	}
	return nil
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
		storage:    options.Storage,
		sbFile:     "superblock.json.gz",
		blocksDir:  "blocks",
		entriesDir: "entries",
		blobsDir:   "blobs",
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
	for blobID, blob := range sb.Blobs {
		if blob == nil {
			return fmt.Errorf("missing metadata for blob %s", blobID)
		}
		blob.size, err = strconv.ParseInt(blob.Size, 10, 64)
		if err != nil {
			return
		}

		for _, blockID := range blob.Blocks {
			block := sb.Blocks[blockID]
			if block == nil {
				return fmt.Errorf("blob %s references missing block %s", blobID, blockID)
			}
			block.blobs = append(block.blobs, blob)
		}
	}

	sb.sbFile = sbFile
	sb.blocksDir = "blocks"
	sb.entriesDir = "entries"
	sb.blobsDir = "blobs"
	return nil
}

func GetSuperblock(options *Options) (sb *Superblock, err error) {
	if options.ArchiveDir == "" {
		return nil, errors.New("Please specify the location of the archive directory")
	}

	sw := utils.NewStopwatch("Reading the superblock")
	defer sw.Stop()

	if err = ensureStorage(options); err != nil {
		return nil, err
	}
	sbFile := "superblock.json.gz"
	var fin io.ReadCloser
	if fin, err = options.Storage.GetBlob(sbFile); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Creating new super block in", options.ArchiveDir)
			sb = newSuperblock(options)
		} else {
			return nil, err
		}
	} else {
		defer fin.Close()
		var rdr *gzip.Reader

		rdr, err = gzip.NewReader(fin)
		if err != nil {
			return nil, err
		}
		defer rdr.Close()
		if err = json.NewDecoder(rdr).Decode(&sb); err != nil {
			return nil, err
		}

		sb.readFromDisk = true
		sb.options = options
		sb.storage = options.Storage
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
	key := blobKey(baseDir, hash)
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	// Store original length in the gzip extra field to avoid full decompression for size.
	gzipWriter.Extra = binary.LittleEndian.AppendUint32(gzipWriter.Extra, uint32(len(data)))
	n, err := gzipWriter.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("unexpected number of bytes written: %d, expected %d", n, len(data))
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	if err := sb.storage.PutBlob(key, buf.Bytes()); err != nil {
		return err
	}
	sb.bytesWritten += uint64(len(data))
	sb.totalBytesWritten += uint64(len(data))
	return nil
}
