package fs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (sb *Superblock) merge(fullPath string, info os.FileInfo) (int, error) {
	if info == nil {
		return 0, errors.New("file info is nil")
	}
	if info.IsDir() {
		return 0, nil
	}

	relativePath, err := filepath.Rel(sb.srcDir, fullPath)
	if err != nil {
		return 0, err
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return sb.mergeSymlink(fullPath, info, relativePath)
	}
	if mode&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return 0, nil
	}
	return sb.mergeRegularfile(fullPath, info, relativePath)
}

func (sb *Superblock) Walk(srcDir string) (changes int, errors int, err error) {
	sb.srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		return 0, 1, err
	}

	filepath.Walk(sb.srcDir, func(fullPath string, info os.FileInfo, err error) error {
		// Skip the output directory of the backup.
		if strings.HasPrefix(fullPath, sb.backupDir) {
			return nil
		}

		n, err := sb.merge(fullPath, info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", fullPath, err)
			errors++
		}
		changes += n
		return nil
	})

	return
}
