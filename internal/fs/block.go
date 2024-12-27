package fs

import (
	"fmt"
	"path"
)

const BlockSize = 1024 * 1024

type Block struct {
	ID   string `json:"id"`
	Size uint32 `json:"size"`
}

func (b *Block) dirName() string {
	if len(b.ID) != 16 {
		panic(fmt.Sprintf("Block ID length mismatch: %d != 16", len(b.ID)))
	}

	s := ""

	for i := 0; i < 14; i += 2 {
		s = path.Join(s, b.ID[i:i+2])
	}

	return s
}
