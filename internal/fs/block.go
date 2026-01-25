package fs

import "os"

const BlockSize = 1024 * 1024

type Block struct {
	ID   string `json:"id"`
	Size uint32 `json:"size"`

	blobs []*Blob // back pointers
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
	// Note: We do not validate size because the on-disk block is gzip-compressed.
	return true, nil
}
