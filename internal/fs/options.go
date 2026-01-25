package fs

import "josvisser.nl/erwtensoep/internal/storage"

type Options struct {
	ArchiveDir               string
	MinSize                  int64
	MaxSize                  int64
	SuperblockWriteThreshold int64
	DryRun                   bool
	// Storage is optional; when nil, the local filesystem backend is used.
	Storage storage.Backend
}
