package fs

import "josvisser.nl/erwtensoep/internal/storage"

func ensureStorage(options *Options) {
	// Default to local filesystem storage to preserve existing behavior.
	if options.Storage == nil {
		options.Storage = storage.NewLocalBackend(options.ArchiveDir)
	}
}
