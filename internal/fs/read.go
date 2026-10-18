package fs

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
)

// OpenRegularFile streams an archived version without extracting it to disk.
// Only one compressed block is open at a time. The caller must close the reader,
// including when it stops reading before EOF.
func (sb *Superblock) OpenRegularFile(file *RegularFile) (io.ReadCloser, error) {
	blob, ok := sb.Blobs[file.BlobID]
	if !ok {
		return nil, fmt.Errorf("%s: blob %s: %w", file.Path, file.BlobID, os.ErrNotExist)
	}
	return &fileReader{sb: sb, blocks: blob.Blocks}, nil
}

type fileReader struct {
	sb     *Superblock
	blocks []string
	source io.ReadCloser
	gzip   *gzip.Reader
	closed bool
}

func (r *fileReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.gzip == nil {
			if len(r.blocks) == 0 {
				return 0, io.EOF
			}
			key := blobKey(r.sb.blocksDir, r.blocks[0])
			source, err := r.sb.storage.GetBlob(key)
			if err != nil {
				return 0, fmt.Errorf("block %s: %w", r.blocks[0], err)
			}
			zr, err := gzip.NewReader(source)
			if err != nil {
				_ = source.Close()
				return 0, fmt.Errorf("block %s: %w", r.blocks[0], err)
			}
			r.source, r.gzip = source, zr
			r.blocks = r.blocks[1:]
		}
		n, err := r.gzip.Read(p)
		if err != io.EOF {
			return n, err
		}
		if err := r.closeBlock(); err != nil {
			return n, err
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (r *fileReader) closeBlock() error {
	if r.gzip == nil {
		return nil
	}
	err := errors.Join(r.gzip.Close(), r.source.Close())
	r.gzip, r.source = nil, nil
	return err
}

func (r *fileReader) Close() error {
	r.closed = true
	return r.closeBlock()
}
