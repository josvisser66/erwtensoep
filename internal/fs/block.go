package fs

import (
	"os"
)

const BlockSize = 1024 * 1024

type Block struct {
	ID   string `json:"id"`
	Size uint32 `json:"size"`
}

func (sb *Superblock) blockExists(b *Block) (bool, error) {
	fname, err := filename(sb.blocksDir, b.ID)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(fname)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	// Note: We are not checking the size because GZip.
	return true, nil
}
