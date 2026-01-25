package fs

import (
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	"hash/crc64"
	"io"
	"josvisser.nl/erwtensoep/internal/utils"
	"os"
	"sync"
)

type RegularFile struct {
	FileStats
	Path   string `json:"path"`
	BlobID string `json:"blob_id"`
}

// Buffer pool for block reads; protects shared reuse across concurrent merges.
var bufPool = &bufferPool{}

type bufferPool struct {
	mu   sync.Mutex
	pool [][]byte
}

func (bp *bufferPool) Get() []byte {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	n := len(bp.pool)
	if n == 0 {
		return make([]byte, BlockSize)
	}
	buf := bp.pool[n-1]
	bp.pool[n-1] = nil
	bp.pool = bp.pool[:n-1]
	return buf
}

func (bp *bufferPool) Put(buf []byte) {
	if len(buf) != BlockSize {
		return
	}
	bp.mu.Lock()
	bp.pool = append(bp.pool, buf)
	bp.mu.Unlock()
}

func (rf *RegularFile) isEntry()                 {}
func (rf *RegularFile) GetFileStats() *FileStats { return &rf.FileStats }
func (rf *RegularFile) GetPath() string          { return rf.Path }

func FromJsonRF(o map[string]interface{}) *RegularFile {
	blobID, ok := o["blob_id"].(string)
	if !ok {
		return nil
	}
	path, ok := o["path"].(string)
	if !ok {
		return nil
	}
	rf := &RegularFile{Path: path, BlobID: blobID}
	rf.FileStats.FromJson(o)
	return rf
}

func (rf *RegularFile) blobify(sb *Superblock, path string) (*Blob, error) {
	sw := utils.NewStopwatch("")
	fmt.Printf("%s [%s bytes]\n", path, humanize.Comma(rf.size))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := bufPool.Get()
	defer bufPool.Put(buf)

	blob := &Blob{
		Blocks: make([]string, 0, 1+rf.size/BlockSize),
		Size:   rf.Size,
		size:   rf.size,
	}

	// The blob ID is derived from the sequence of block IDs to make it content-addressed.
	blobIDHash := crc64.New(crc64.MakeTable(crc64.ECMA))
	var n int
	var countBytesRead int64

	for err != io.EOF {
		if sb.sigReceived.Load() {
			return nil, errors.New("signal received")
		}
		fmt.Printf("%d\r", countBytesRead)
		if n, err = f.Read(buf); err != nil && err != io.EOF {
			return nil, err
		} else if n == 0 {
			// EOF coming up...
			continue
		}

		countBytesRead += int64(n)
		// Block IDs are CRC64 of the block content.
		h := crc64.New(crc64.MakeTable(crc64.ECMA))
		h.Write(buf[0:n])
		id := fmt.Sprintf("%016x", h.Sum64())

		var block *Block
		var ok bool

		if block, ok = sb.Blocks[id]; !ok {
			// Creates a new block.
			block = &Block{ID: id, Size: uint32(n)}
			if exists, err := sb.blockExists(block); err != nil {
				return nil, err
			} else if !exists {
				if err := sb.dataWrite(sb.blocksDir, block.ID, buf[0:n]); err != nil {
					return nil, err
				}
			} else {
				fmt.Printf("Block already exists on disk: %v\n", block)
				// TODO: Add an option to always rewrite the block on disk
			}

			sb.Blocks[id] = block
		}
		blob.Blocks = append(blob.Blocks, id)
		blobIDHash.Write([]byte(block.ID))
	}

	fmt.Printf("%d bytes took %s (%s/sec)\n", countBytesRead, sw.Elapsed(), humanize.Bytes(uint64(float64(countBytesRead)/sw.Elapsed().Seconds())))
	blob.ID = fmt.Sprintf("%016x", blobIDHash.Sum64())
	if _, ok := sb.Blobs[blob.ID]; !ok {
		sb.Blobs[blob.ID] = blob
	}
	return blob, sb.jsonWrite(sb.blobsDir, blob.ID, blob)
}

func (rf *RegularFile) hash() string {
	h := crc64.New(crc64.MakeTable(crc64.ECMA))
	h.Write([]byte("regularfile"))
	h.Write([]byte(rf.Path))
	rf.FileStats.hash(h)

	// Note: BlobID is not part of the hash because that would
	// mean that we would have to read the entire file just
	// to see if it is in the map.

	return fmt.Sprintf("%016x", h.Sum64())
}

func (sb *Superblock) mergeRegularfile(fullPath string, info os.FileInfo, relativePath string) (int, error) {
	size := info.Size()
	if size < sb.options.MinSize || size > sb.options.MaxSize {
		fmt.Printf("%s skipped: Size %d not in [%d, %d]\n", relativePath, size, sb.options.MinSize, sb.options.MaxSize)
		return 0, nil
	}
	rf := &RegularFile{Path: relativePath, FileStats: newFileStats(info)}
	ID := rf.hash()
	versions, ok := sb.Entries[relativePath]
	if !ok {
		// File does not exist in the metadata at all.
		if blob, err := rf.blobify(sb, fullPath); err != nil {
			return 0, err
		} else {
			rf.BlobID = blob.ID
		}
		sb.Entries[relativePath] = make(map[string]interface{})
		sb.Entries[relativePath][ID] = rf
		return 1, sb.jsonWrite(sb.entriesDir, ID, rf)
	}

	if _, ok := versions[ID]; ok {
		// File exists, and _this_ version exists.
		// We assume that if the mtime did not change, the content did not change either.
		// TODO: Make this an option to override this behavior so that we
		// can rewrite the entire archive.
		return 0, nil
	}

	// New version.
	if blob, err := rf.blobify(sb, fullPath); err != nil {
		return 0, err
	} else {
		rf.BlobID = blob.ID
	}

	versions[ID] = rf
	return 1, sb.jsonWrite(sb.entriesDir, ID, rf)
}
