package fs

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"josvisser.nl/erwtensoep/internal/storage"
)

type corruptObjectError struct{ err error }

func (e *corruptObjectError) Error() string { return e.err.Error() }
func (e *corruptObjectError) Unwrap() error { return e.err }

// Keep operating-system/backend read failures separate from damaged contents:
// a permission or device error must never be offered as a deletion repair.
type checkedReader struct {
	io.Reader
	count   int64
	readErr error
}

func (r *checkedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.count += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}
	return n, err
}

func readCheckedObject(backend storage.Backend, key string, extra bool, decode func(io.Reader) error) error {
	file, err := backend.GetBlob(key)
	if err != nil {
		return err
	}
	raw := &checkedReader{Reader: file}
	gz, contentErr := gzip.NewReader(raw)
	if contentErr == nil {
		data := &checkedReader{Reader: gz}
		contentErr = decode(data)
		if contentErr == nil && extra && (len(gz.Extra) != 4 || int64(binary.LittleEndian.Uint32(gz.Extra)) != data.count) {
			contentErr = fmt.Errorf("gzip original-size field does not match contents")
		}
		_ = gz.Close()
	}
	closeErr := file.Close()
	if raw.readErr != nil || closeErr != nil {
		return errors.Join(raw.readErr, closeErr)
	}
	if contentErr != nil {
		return &corruptObjectError{contentErr}
	}
	return nil
}

// Reading a second value forces the gzip stream through its CRC/trailer, and
// rejects valid JSON followed by garbage or another JSON document.
func decodeCheckedJSON(r io.Reader, dst interface{}) error {
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func validArchiveHash(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func readCheckedBlock(backend storage.Backend, key, id string) (*Block, error) {
	block := &Block{ID: id}
	err := readCheckedObject(backend, key, true, func(r io.Reader) error {
		h := crc64.New(crc64.MakeTable(crc64.ECMA))
		n, err := io.Copy(h, io.LimitReader(r, BlockSize+1))
		if err != nil {
			return err
		}
		if n > BlockSize {
			return fmt.Errorf("block exceeds %d bytes", BlockSize)
		}
		if actual := fmt.Sprintf("%016x", h.Sum64()); actual != id {
			return fmt.Errorf("content hash is %s, expected %s", actual, id)
		}
		block.Size = uint32(n)
		return nil
	})
	return block, err
}

func parseCheckedBlob(raw json.RawMessage, id string) (*Blob, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"id", "size", "blocks"} {
		if fields[name] == nil {
			return nil, fmt.Errorf("missing blob field %s", name)
		}
	}
	var blob Blob
	if err := json.Unmarshal(raw, &blob); err != nil {
		return nil, err
	}
	if blob.ID != id {
		return nil, fmt.Errorf("blob ID %q does not match object name %s", blob.ID, id)
	}
	n, err := strconv.ParseInt(blob.Size, 10, 64)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid blob size %q", blob.Size)
	}
	blob.size = n
	h := crc64.New(crc64.MakeTable(crc64.ECMA))
	for _, blockID := range blob.Blocks {
		if !validArchiveHash(blockID) {
			return nil, fmt.Errorf("invalid block ID %q", blockID)
		}
		_, _ = io.WriteString(h, blockID)
	}
	if actual := fmt.Sprintf("%016x", h.Sum64()); actual != id {
		return nil, fmt.Errorf("block-list hash is %s, expected %s", actual, id)
	}
	return &blob, nil
}

func parseCheckedEntry(raw json.RawMessage, id string) (Entry, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"path", "owner", "group", "size", "mode", "mtime"} {
		if fields[name] == nil || string(fields[name]) == "null" {
			return nil, fmt.Errorf("missing entry field %s", name)
		}
	}
	var entry Entry
	_, file := fields["blob_id"]
	_, link := fields["target"]
	if file == link {
		return nil, fmt.Errorf("entry must be either a regular file or a symlink")
	}
	if file {
		rf := &RegularFile{}
		if err := json.Unmarshal(raw, rf); err != nil {
			return nil, err
		}
		if !validArchiveHash(rf.BlobID) || !rf.Mode.IsRegular() {
			return nil, fmt.Errorf("invalid file blob ID or mode")
		}
		entry = rf
	} else {
		sl := &Symlink{}
		if err := json.Unmarshal(raw, sl); err != nil {
			return nil, err
		}
		if sl.Target == "" || sl.Mode.Type() != os.ModeSymlink {
			return nil, fmt.Errorf("invalid symlink target or mode")
		}
		entry = sl
	}
	name := entry.GetPath()
	if name == "" || name == "." || name != path.Clean(name) || path.IsAbs(name) || strings.HasPrefix(name, "../") || strings.ContainsRune(name, 0) {
		return nil, fmt.Errorf("invalid archive path %q", name)
	}
	stats := entry.GetFileStats()
	n, err := strconv.ParseInt(stats.Size, 10, 64)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid entry size %q", stats.Size)
	}
	stats.size = n
	if actual := entry.hash(); actual != id {
		return nil, fmt.Errorf("entry hash is %s, expected %s", actual, id)
	}
	return entry, nil
}
