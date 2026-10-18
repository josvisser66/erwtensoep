package fs

import "os"

const BlockSize = 1024 * 1024

type Block struct {
	ID string `json:"id"`
	// We do not need to store size as a string because it is always leq the BlockSize.
	// Also: This is the original size of the block before compression.
	Size uint32 `json:"size"`

	blobs []*Blob // back pointers to the blobs that contain this block.
}

func (sb *Superblock) blockExists(b *Block) (bool, error) {
	key := blobKey(sb.blocksDir, b.ID)
	rdr, err := sb.storage.GetBlob(key)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	_ = rdr.Close()
	// Note: We do not validate size because the on-disk block is gzip-compressed and we store the original size in the extra field.
	return true, nil
}
