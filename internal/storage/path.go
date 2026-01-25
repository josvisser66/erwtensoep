package storage

import (
	"os"
	"path/filepath"
	"strings"
)

func cleanRelPath(path string) (string, error) {
	if path == "" {
		return ".", nil
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return "", os.ErrInvalid
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", os.ErrInvalid
	}
	return clean, nil
}

func pathInvalid(op, path string) error {
	return &os.PathError{Op: op, Path: path, Err: os.ErrInvalid}
}
