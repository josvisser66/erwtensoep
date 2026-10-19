package fs

import "josvisser.nl/erwtensoep/internal/storage"

type Options struct {
	ArchiveDir               string
	EndpointURL              string
	Region                   string
	MinSize                  int64
	MaxSize                  int64
	SuperblockWriteThreshold int64
	DryRun                   bool
	// Storage is optional; when nil, ArchiveDir selects local or S3 storage.
	Storage storage.Backend
}
