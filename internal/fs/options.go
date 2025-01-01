package fs

type Options struct {
	ArchiveDir               string
	MinSize                  int64
	MaxSize                  int64
	SuperblockWriteThreshold int64
	DryRun                   bool
}
