package fs

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (sb *Superblock) merge(fullPath string, info os.FileInfo, options *Options) (int, error) {
	if info == nil {
		return 0, errors.New("file info is nil")
	}
	if info.IsDir() {
		return 0, nil
	}
	relativePath, err := filepath.Rel(sb.srcDir, fullPath)
	if err != nil {
		return 0, err
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return sb.mergeSymlink(fullPath, info, relativePath)
	}
	if mode&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return 0, nil
	}
	n, err := sb.mergeRegularfile(fullPath, info, relativePath, options)
	if err != nil {
		return n, err
	}
	return n, sb.MaybeWrite()
}

type ignorePatterns struct {
	patterns []string
	prev     *ignorePatterns
}

func (ip *ignorePatterns) match(name string) bool {
	for ip != nil {
		for _, pattern := range ip.patterns {
			if m, err := path.Match(pattern, name); err != nil {
				fmt.Println("Bad match pattern", pattern)
				continue
			} else if m {
				return true
			}
		}

		ip = ip.prev
	}

	return false
}

func readIgnorePatterns(fullPath string, prev *ignorePatterns) (*ignorePatterns, error) {
	ip := &ignorePatterns{prev: prev}
	filename := path.Join(fullPath, ".erwtensoep")
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return ip, nil
		}
		return nil, err
	}

	fmt.Println("Reading", filename)
	ip.patterns = strings.Split(string(data), "\n")
	fmt.Println("Patterns", ip.patterns)
	return ip, nil
}

func (sb *Superblock) walkDir(fullPath string, ip *ignorePatterns, options *Options) (changes int, errors int, err error) {
	// Skip the archive directory.
	if strings.HasPrefix(fullPath, sb.archiveDir) {
		fmt.Println("WARNING: the archive directory is in the source directory (skipped)")
		return
	}

	fmt.Println("Enter", fullPath)
	ip, err = readIgnorePatterns(fullPath, ip)
	if err != nil {
		return
	}

	var de []os.DirEntry

	de, err = os.ReadDir(fullPath)
	if err != nil {
		if os.IsPermission(err) {
			err = nil
		}
		return
	}

	for _, e := range de {
		if ip.match(e.Name()) {
			fmt.Println("Skipping", e.Name())
			continue
		}

		newFullPath := path.Join(fullPath, e.Name())

		if e.IsDir() {
			c, e, err := sb.walkDir(newFullPath, ip, options)
			changes += c
			errors += e
			if err != nil {
				return changes, errors, err
			}
			continue
		}

		info, err := e.Info()
		if err != nil {
			errors++
			fmt.Printf("Error reading file info for %s: %v\n", newFullPath, err)
		} else {
			fmt.Printf("%s [%d bytes]\n", newFullPath, info.Size())
			n, err := sb.merge(newFullPath, info, options)
			if err != nil {
				errors++
				fmt.Printf("Error processing file %s: %v\n", newFullPath, err)
			} else {
				changes += n
			}
		}
	}

	fmt.Println("Leaving", fullPath)
	return
}

func (sb *Superblock) Merge(srcDir string, options *Options) (changes int, errors int, err error) {
	sb.srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		return 0, 1, err
	}

	return sb.walkDir(sb.srcDir, nil, options)
}
