package fs

import (
	"fmt"
	"hash/crc64"
	"io"
	"os"
)

type RegularFile struct {
	FileStats
	Path   string `json:"path"`
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
	path, ok := o["path"].(string)
	if !ok {
		return nil
	}
	rf := &RegularFile{Path: path, BlobID: blobID}
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
		Blocks:   make([]*Block, 0, 1+rf.size/BlockSize),
		Size:     rf.Size,
		size:     rf.size,
		RefCount: 1,
	}

	blobIDHash := crc64.New(crc64.MakeTable(crc64.ECMA))
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
			if err := sb.dataWrite(sb.blocksDir, block.ID, buf[0:n]); err != nil {
				return nil, err
			}
			blob.Blocks = append(blob.Blocks, block)
			blobIDHash.Write([]byte(block.ID))
		}
	}

	blob.ID = fmt.Sprintf("%016x", blobIDHash.Sum64())
	if b, ok := sb.Blobs[blob.ID]; ok {
		b.RefCount++
	} else {
		sb.Blobs[blob.ID] = blob
	}
	return blob, sb.jsonWrite(sb.blobsDir, blob.ID, blob)
}

func (rf *RegularFile) Hash() string {
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
	rf := &RegularFile{Path: relativePath, FileStats: newFileStats(info)}
	ID := rf.Hash()
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
