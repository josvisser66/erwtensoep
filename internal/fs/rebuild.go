package fs

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"josvisser.nl/erwtensoep/internal/storage"
	"josvisser.nl/erwtensoep/internal/utils"
)

// readUnzipUnmarshal reads a gzip-compressed JSON blob and decodes it into any.
func readUnzipUnmarshal(backend storage.Backend, path string, any interface{}) error {
	fin, err := backend.GetBlob(path)
	if err != nil {
		return err
	}
	defer fin.Close()

	rdr, err := gzip.NewReader(fin)
	if err != nil {
		return err
	}
	defer rdr.Close()

	return json.NewDecoder(rdr).Decode(any)
}

func RebuildSuperblock(options *Options) error {
	sw := utils.NewStopwatch("Rebuilding the super block")
	defer sw.Stop()

	if err := ensureStorage(options); err != nil {
		return err
	}
	sb := newSuperblock(options)
	sb.fix("rebuilt_superblock.json")

	n := 0
	// Rebuild the block list by scanning stored blobs.
	fmt.Println("Parsing blobs")
	if err := sb.storage.ListBlobs("blobs/", func(path string) error {
		blob := &Blob{}
		if err := readUnzipUnmarshal(sb.storage, path, blob); err != nil {
			return err
		}
		// Maybe parse blob.Size into blob.size?

		for _, blockID := range blob.Blocks {
			if _, ok := sb.Blocks[blockID]; ok {
				continue
			}
			key := blobKey(sb.blocksDir, blockID)
			// Extract original block size from the gzip extra field.
			size, err := func() (uint32, error) {
				fin, err := sb.storage.GetBlob(key)
				if err != nil {
					return 0, err
				}
				defer fin.Close()
				rdr, err := gzip.NewReader(fin)
				if err != nil {
					return 0, err
				}
				return binary.LittleEndian.Uint32(rdr.Extra), nil
			}()
			if err != nil {
				return err
			}
			sb.Blocks[blockID] = &Block{blockID, size, nil}
		}

		sb.Blobs[blob.ID] = blob

		n++
		fmt.Printf("%s %7d\r", blob.ID, n)
		return nil
	}); err != nil {
		return err
	}

	n = 0
	// Rebuild entries by reading each stored entry version.
	fmt.Println("\nParsing entries")
	if err := sb.storage.ListBlobs("entries/", func(path string) error {
		obj := make(map[string]interface{})
		if err := readUnzipUnmarshal(sb.storage, path, &obj); err != nil {
			return err
		}
		var e Entry
		if sl := FromJsonSL(obj); sl != nil {
			e = sl
		} else if rf := FromJsonRF(obj); rf != nil {
			e = rf
		} else {
			panic(fmt.Sprintf("Cannot convert: %v", obj))
		}
		h := e.hash()
		entryPath := e.GetPath()
		if versions, ok := sb.Entries[entryPath]; !ok {
			versions = make(map[string]interface{})
			versions[h] = e
			sb.Entries[entryPath] = versions
		} else {
			sb.Entries[entryPath][h] = e
		}
		n++
		fmt.Printf("%s %7d\r", h, n)
		return nil
	}); err != nil {
		return err
	}

	fmt.Println()
	return sb.Write()
}
