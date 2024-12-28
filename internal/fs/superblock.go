package fs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

type Entry interface {
	isEntry()
	Hash(path string) string // 16 character hexadecimal
}

type Superblock struct {
	Version    int                               `json:"version"`
	Generation int                               `json:"generation"`
	Entries    map[string]map[string]interface{} `json:"entries"`
	Blocks     map[string]*Block
	Blobs      map[string]*Blob
	LastUpdate time.Time `json:"last_update"` // will be encoded as a string

	srcDir       string
	archiveDir   string
	sbFile       string
	readFromDisk bool
	blocksDir    string
}

func (sb *Superblock) Write() error {
	if sb.readFromDisk {
		if err := os.Rename(sb.sbFile, fmt.Sprintf("%s.bak", sb.sbFile)); err != nil {
			return err
		}
	}
	sb.Generation++
	sb.LastUpdate = time.Now()
	b, err := json.Marshal(&sb)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "\t"); err != nil {
		return err
	}
	if err := os.WriteFile(sb.sbFile, buf.Bytes(), 0644); err != nil {
		return err
	}
	return nil
}

func ReadSuperblock(archiveDir string) (*Superblock, error) {
	if archiveDir == "" {
		return nil, errors.New("Please specify the location of the archive directory")
	}
	var sb *Superblock
	var err error

	archiveDir, err = filepath.Abs(archiveDir)
	if err != nil {
		return nil, err
	}
	sbFile := path.Join(archiveDir, "superblock.json")
	b, err := os.ReadFile(sbFile)
	if err == nil {
		if err = json.Unmarshal(b, &sb); err != nil {
			return nil, err
		}
		sb.readFromDisk = true
	} else if os.IsNotExist(err) {
		fmt.Println("Creating new super block")
		sb = &Superblock{
			Version:    0,
			Generation: 0,
			Entries:    make(map[string]map[string]interface{}),
			Blobs:      make(map[string]*Blob),
			Blocks:     make(map[string]*Block),
			LastUpdate: time.Time{},
		}
	} else {
		return nil, err
	}

	// Fixes the interface{} back to actual objects.
	for _, m := range sb.Entries {
		for k, o := range m {
			if sl := FromJsonSL(o.(map[string]interface{})); sl != nil {
				m[k] = sl
			} else if rf := FromJsonRF(o.(map[string]interface{})); rf != nil {
				m[k] = rf
			} else {
				panic(fmt.Sprintf("Cannot convert: %v", o))
			}
		}
	}

	// Fix the blob size cache.
	for _, b := range sb.Blobs {
		b.size, err = strconv.ParseInt(b.Size, 10, 64)
		if err != nil {
			return nil, err
		}
	}

	sb.archiveDir = archiveDir
	sb.sbFile = sbFile

	// Makes sure that the blocks directory exists.
	sb.blocksDir = path.Join(archiveDir, "blocks")
	_, err = os.Stat(sb.blocksDir)
	if err == nil {
		return sb, nil
	}
	if os.IsNotExist(err) {
		if err := os.Mkdir(sb.blocksDir, 0750); err != nil {
			return nil, err
		}
		return sb, nil
	}

	return nil, err
}

func (sb *Superblock) blockWrite(block *Block, data []byte) error {
	if block.Size != uint32(len(data)) {
		panic(fmt.Sprintf("Data/Block size mismatch, %d != %d", block.Size, len(data)))
	}

	dirName := path.Join(sb.blocksDir, block.dirName())
	if err := os.MkdirAll(dirName, 0750); err != nil {
		return err
	}

	fileName := path.Join(dirName, block.ID[14:16])
	fout, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer fout.Close()
	gzipWriter := gzip.NewWriter(fout)
	defer gzipWriter.Close()
	n, err := gzipWriter.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("unexpected number of bytes written: %d, expected %d", n, len(data))
	}
	return nil
}
