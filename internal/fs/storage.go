package fs

import (
	"fmt"
	"path/filepath"
	"strings"

	"josvisser.nl/erwtensoep/internal/storage"
)

func ensureStorage(options *Options) error {
	if storage.IsS3Archive(options.ArchiveDir) {
		if _, _, err := storage.ParseS3Archive(options.ArchiveDir); err != nil {
			return err
		}
		if options.Storage == nil {
			backend, err := storage.NewS3Backend(options.ArchiveDir, options.EndpointURL, options.Region)
			if err != nil {
				return err
			}
			options.Storage = backend
		}
		return nil
	}
	if strings.Contains(options.ArchiveDir, "://") {
		return fmt.Errorf("unsupported archive URL scheme")
	}
	var err error
	options.ArchiveDir, err = filepath.Abs(options.ArchiveDir)
	if err != nil {
		return err
	}
	if options.Storage == nil {
		options.Storage = storage.NewLocalBackend(options.ArchiveDir)
	}
	return nil
}
