package fs

import (
	"fmt"
	"os"
	"path"
)

func dirname(hash string) string {
	if len(hash) != 16 {
		panic(fmt.Sprintf("Hash length mismatch: len(%s) = %d != 16", hash, len(hash)))
	}

	s := ""

	for i := 0; i < 12; i += 4 {
		s = path.Join(s, hash[i:i+4])
	}

	return s
}

func filename(base, hash string) (string, error) {
	d := path.Join(base, dirname(hash))
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return path.Join(d, hash[12:16]), nil
}
