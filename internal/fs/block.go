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

func dirName(hash string) string {
	if len(hash) != 16 {
		panic(fmt.Sprintf("Hash length mismatch: len(%s) = %d != 16", hash, len(hash)))
	}

	s := ""

	for i := 0; i < 14; i += 2 {
		s = path.Join(s, hash[i:i+2])
	}

	return s
}
