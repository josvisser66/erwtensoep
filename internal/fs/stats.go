package fs

import (
	"fmt"
	"strconv"

	"josvisser.nl/erwtensoep/internal/storage"
)

// ArchiveStats describes all saved versions. Byte totals cover referenced
// regular-file data only; metadata and unreferenced objects are excluded.
type ArchiveStats struct {
	Paths, Versions, RegularVersions, SymlinkVersions int
	MaxVersions, Blobs, Blocks                        int
	UnreferencedBlobs, UnreferencedBlocks             int
	BlockReferences, ReusedBlocks, CrossFileBlocks    int
	MaxBlockReferences                                int
	LogicalBytes, BlobBytes, BlockBytes, StoredBytes  int64
}

// dataUsage counts references without retaining a set of every referring path.
// Multiple versions or repeated blocks at one path do not imply cross-file use.
type dataUsage struct {
	references int
	firstPath  string
	crossFile  bool
}

// Stats measures deduplication in two stages (whole-file contents, then blocks)
// and compression of the remaining unique blocks. Local storage uses file sizes
// without reading or decompressing block contents.
func (sb *Superblock) Stats() (*ArchiveStats, error) {
	s := &ArchiveStats{Paths: len(sb.Entries)}
	blobs := make(map[string]*dataUsage)
	for path, versions := range sb.Entries {
		s.Versions += len(versions)
		s.MaxVersions = max(s.MaxVersions, len(versions))
		for _, version := range versions {
			switch v := version.(type) {
			case *Symlink:
				s.SymlinkVersions++
			case *RegularFile:
				if v == nil {
					return nil, fmt.Errorf("stats: missing file metadata for %q", path)
				}
				blob := sb.Blobs[v.BlobID]
				if blob == nil {
					return nil, fmt.Errorf("stats: file %q references missing blob %s", path, v.BlobID)
				}
				n, err := strconv.ParseInt(v.Size, 10, 64)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("stats: invalid file size %q for %q", v.Size, path)
				}
				blobSize, err := strconv.ParseInt(blob.Size, 10, 64)
				if err != nil || blobSize != n {
					return nil, fmt.Errorf("stats: file %q size %s does not match blob %s size %s", path, v.Size, v.BlobID, blob.Size)
				}
				s.RegularVersions++
				s.LogicalBytes += n
				usage := blobs[v.BlobID]
				if usage == nil {
					usage = &dataUsage{firstPath: path}
					blobs[v.BlobID] = usage
					s.BlobBytes += n
				}
				usage.references++
				usage.crossFile = usage.crossFile || usage.firstPath != path
			default:
				return nil, fmt.Errorf("stats: unsupported entry type %T for %q", version, path)
			}
		}
	}

	blocks := make(map[string]*dataUsage)
	for blobID, blobUsage := range blobs {
		blob := sb.Blobs[blobID]
		var blockBytes int64
		for _, blockID := range blob.Blocks {
			block := sb.Blocks[blockID]
			if block == nil {
				return nil, fmt.Errorf("stats: blob %s references missing block %s", blobID, blockID)
			}
			blockBytes += int64(block.Size)
			usage := blocks[blockID]
			if usage == nil {
				usage = &dataUsage{firstPath: blobUsage.firstPath}
				blocks[blockID] = usage
			}
			usage.references += blobUsage.references
			usage.crossFile = usage.crossFile || blobUsage.crossFile || usage.firstPath != blobUsage.firstPath
		}
		blobSize, _ := strconv.ParseInt(blob.Size, 10, 64) // Validated above.
		if blockBytes != blobSize {
			return nil, fmt.Errorf("stats: blob %s size %d does not match its blocks (%d bytes)", blobID, blobSize, blockBytes)
		}
	}

	for blockID, usage := range blocks {
		n, err := storage.BlobSize(sb.storage, blobKey(sb.blocksDir, blockID))
		if err != nil {
			return nil, fmt.Errorf("stats: measuring stored block %s: %w", blockID, err)
		}
		if n < 0 {
			return nil, fmt.Errorf("stats: negative stored size for block %s", blockID)
		}
		s.StoredBytes += n
		s.BlockBytes += int64(sb.Blocks[blockID].Size)
		s.BlockReferences += usage.references
		s.MaxBlockReferences = max(s.MaxBlockReferences, usage.references)
		if usage.references > 1 {
			s.ReusedBlocks++
		}
		if usage.crossFile {
			s.CrossFileBlocks++
		}
	}
	s.Blobs, s.Blocks = len(blobs), len(blocks)
	s.UnreferencedBlobs = len(sb.Blobs) - s.Blobs
	s.UnreferencedBlocks = len(sb.Blocks) - s.Blocks
	return s, nil
}
