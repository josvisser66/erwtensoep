package fs

import (
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	"josvisser.nl/erwtensoep/internal/utils"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

func (sb *Superblock) merge(fullPath string, info os.FileInfo) (int, error) {
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
	// Treat symlinks separately to preserve targets.
	if mode&os.ModeSymlink != 0 {
		return sb.mergeSymlink(fullPath, info, relativePath)
	}
	// Skip special files like sockets, pipes, and devices.
	if mode&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return 0, nil
	}
	n, err := sb.mergeRegularfile(fullPath, info, relativePath)
	if err != nil {
		return n, err
	}
	return n, sb.maybeWrite()
}

type ignorePatterns struct {
	patterns []string
	prev     *ignorePatterns
}

func (ip *ignorePatterns) match(name string) bool {
	// Patterns are inherited from parent directories.
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

func (sb *Superblock) walkDir(fullPath string, ip *ignorePatterns) (changes int, errors int, err error) {
	// Skip the archive directory to avoid self-ingesting metadata.
	if strings.HasPrefix(fullPath, sb.options.ArchiveDir) {
		fmt.Println("WARNING: the archive directory is in the source directory (skipped)")
		return
	}

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
		if sb.sigReceived.Load() {
			return
		}
		if ip.match(e.Name()) {
			// Maybe for verbose option?
			// fmt.Println("Skipping", e.Name())
			continue
		}

		newFullPath := path.Join(fullPath, e.Name())

		if e.IsDir() {
			c, e, err := sb.walkDir(newFullPath, ip)
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
			n, err := sb.merge(newFullPath, info)
			if err != nil {
				errors++
				fmt.Printf("Error processing file %s: %v\n", newFullPath, err)
			} else {
				changes += n
			}
		}
	}

	return
}

func (sb *Superblock) Merge(srcDir string) (changes int, errors int, err error) {
	sw := utils.NewStopwatch(fmt.Sprintf("Merging of %s", srcDir))
	defer sw.Stop()

	sb.srcDir, err = filepath.Abs(srcDir)
	if err != nil {
		return 0, 1, err
	}

	defer func() {
		fmt.Printf("Total bytes written: %s\n", humanize.Comma(int64(sb.totalBytesWritten)))
	}()

	// Stop processing cleanly on SIGINT/SIGTERM to reduce archive corruption risk.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-ch
		fmt.Println("\nReceived signal:", sig)
		fmt.Println("Please wait for the program to end to prevent archive corruption...")
		sb.sigReceived.Store(true)
	}()

	return sb.walkDir(sb.srcDir, nil)
}
