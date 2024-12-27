package fs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

type Entry interface {
	isEntry()
	Hash(path string) uint64
}

type Superblock struct {
	Version    int                               `json:"version"`
	Generation int                               `json:"generation"`
	Entries    map[string]map[uint64]interface{} `json:"entries"`
	Blocks     map[string]*Block
	Blobs      map[string]*Blob
	LastUpdate time.Time `json:"last_update"` // will be encoded as a string

	srcDir       string
	backupDir    string
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

func ReadSuperblock(backupDir string) (*Superblock, error) {
	var sb *Superblock
	var err error

	backupDir, err = filepath.Abs(backupDir)
	if err != nil {
		return nil, err
	}
	sbFile := path.Join(backupDir, "superblock.json")
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
			Entries:    make(map[string]map[uint64]interface{}),
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

	sb.backupDir = backupDir
	sb.sbFile = sbFile

	// Makes sure that the blocks directory exists.
	sb.blocksDir = path.Join(backupDir, "blocks")
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
	return os.WriteFile(fileName, data, 0640)
}
