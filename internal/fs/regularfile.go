package fs

import (
	"fmt"
	"hash/crc64"
	"io"
	"os"
)

type RegularFile struct {
	FileStats
	BlobID string `json:"blob_id"`
}

// Note: global buffer! Single threaded!
var buf = make([]byte, BlockSize)

func (rf *RegularFile) isEntry() {}

func FromJsonRF(o map[string]interface{}) *RegularFile {
	blobID, ok := o["blob_id"].(string)
	if !ok {
		return nil
	}
	rf := &RegularFile{BlobID: blobID}
	rf.FileStats.FromJson(o)
	return rf
}

func (rf *RegularFile) blobify(sb *Superblock, path string) (*Blob, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	blob := &Blob{
		Blocks: make([]*Block, 0, 1+rf.size/BlockSize),
		Size:   rf.Size,
		size:   rf.size,
	}

	blobID := crc64.New(crc64.MakeTable(crc64.ECMA))
	var n int

	for err != io.EOF {
		n, err = f.Read(buf)
		if err != nil && err != io.EOF {
			return nil, err
		}

		if n > 0 {
			h := crc64.New(crc64.MakeTable(crc64.ECMA))
			h.Write(buf[0:n])
			block := &Block{
				ID:   fmt.Sprintf("%016x", h.Sum64()),
				Size: uint32(n),
			}
			if err := sb.blockWrite(block, buf[0:n]); err != nil {
				return nil, err
			}
			blob.Blocks = append(blob.Blocks, block)
			blobID.Write([]byte(block.ID))
		}
	}

	blob.ID = fmt.Sprintf("%d", blobID.Sum64())
	sb.Blobs[blob.ID] = blob
	return blob, nil
}

func (rf *RegularFile) Hash(path string) uint64 {
	h := crc64.New(crc64.MakeTable(crc64.ECMA))

	h.Write([]byte("regularfile"))
	h.Write([]byte(path))
	rf.FileStats.hash(h)

	// Note: BlobID is not part of the hash because that would
	// mean that we would have to read the entire file just
	// to see if it is in the map.

	return h.Sum64()
}

func (sb *Superblock) mergeRegularfile(fullPath string, info os.FileInfo, relativePath string) (int, error) {
	rf := &RegularFile{FileStats: newFileStats(info)}
	h := rf.Hash(relativePath)
	entries, ok := sb.Entries[relativePath]
	if !ok {
		// File does not exist in the map yet.
		if blob, err := rf.blobify(sb, fullPath); err != nil {
			return 0, err
		} else {
			rf.BlobID = blob.ID
		}
		sb.Entries[relativePath] = make(map[uint64]interface{})
		sb.Entries[relativePath][h] = rf
		return 1, nil
	}

	if _, ok := entries[h]; ok {
		// File exists, and _this_ version exists.
		// We assume that if the mtime did not change, the content did not change either.
		// TODO: Make this an option to override this behavior.
		return 0, nil
	}

	// Updated file exists.
	if blob, err := rf.blobify(sb, fullPath); err != nil {
		return 0, err
	} else {
		rf.BlobID = blob.ID
	}
	entries[h] = rf
	return 1, nil
}
