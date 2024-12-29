package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

func readUnzipUnmarshal(path string, any interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	rdr, err := gzip.NewReader(bytes.NewBuffer(b))
	if err != nil {
		return err
	}

	b, err = io.ReadAll(rdr)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, any)
}

func RebuildSuperblock(archiveDir string) error {
	sb := newSuperblock()
	sb.fix(archiveDir, path.Join(archiveDir, "rebuilt_superblock.json"))

	n := 0
	fmt.Println("Parsing blobs")
	if err := filepath.Walk(sb.blobsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		blob := &Blob{}
		if err = readUnzipUnmarshal(path, blob); err != nil {
			return err
		}
		// Maybe parse blob.Size into blob.size?
		blob.RefCount = 0

		for _, blockID := range blob.Blocks {
			if _, ok := sb.Blocks[blockID]; ok {
				continue
			}
			fname, err := filename(sb.blocksDir, blockID)
			if err != nil {
				return err
			}
			size, err := func() (uint32, error) {
				fin, err := os.Open(fname)
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
			sb.Blocks[blockID] = &Block{blockID, size}
		}

		sb.Blobs[blob.ID] = blob

		n++
		fmt.Printf("%s %7d\r", blob.ID, n)
		return nil
	}); err != nil {
		return err
	}

	n = 0
	fmt.Println("\nParsing entries")
	if err := filepath.Walk(sb.entriesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		obj := make(map[string]interface{})
		if err = readUnzipUnmarshal(path, &obj); err != nil {
			return err
		}
		var e Entry
		if sl := FromJsonSL(obj); sl != nil {
			e = sl
		} else if rf := FromJsonRF(obj); rf != nil {
			e = rf
			sb.Blobs[rf.BlobID].RefCount++
		} else {
			panic(fmt.Sprintf("Cannot convert: %v", obj))
		}
		h := e.hash()
		if versions, ok := sb.Entries[e.getPath()]; !ok {
			versions = make(map[string]interface{})
			versions[h] = e
			sb.Entries[e.getPath()] = versions
		} else {
			sb.Entries[e.getPath()][h] = e
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
