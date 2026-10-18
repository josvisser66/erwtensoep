package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"josvisser.nl/erwtensoep/internal/fs"
)

type archiveNode struct {
	path     string
	entry    fs.Entry // nil for inferred directories
	children []*archiveNode
}

func (n *archiveNode) isDir() bool { return n.entry == nil }

func (n *archiveNode) stats() *fs.FileStats {
	if n.isDir() {
		return &fs.FileStats{Mode: os.ModeDir | 0555, Size: "0", Owner: "-", Group: "-"}
	}
	return n.entry.GetFileStats()
}

// A snapshot of the latest versions. Directories are implicit in archive paths;
// empty directories and their original metadata are not stored in the archive.
func archiveTree(sb *fs.Superblock) map[string]*archiveNode {
	nodes := map[string]*archiveNode{"/": {path: "/"}}
	for name, versions := range sb.Entries {
		// Break equal-mtime ties by version ID, including zero/negative mtimes.
		var newest fs.Entry
		var newestID string
		for id, value := range versions {
			e, ok := value.(fs.Entry)
			if !ok {
				continue
			}
			if newest == nil || e.GetFileStats().MTime.After(newest.GetFileStats().MTime) ||
				(e.GetFileStats().MTime.Equal(newest.GetFileStats().MTime) && id > newestID) {
				newest, newestID = e, id
			}
		}
		if newest != nil {
			p := path.Join("/", filepath.ToSlash(name))
			if p != "/" {
				nodes[p] = &archiveNode{path: p, entry: newest}
			}
		}
	}
	// A path may have been a file in an older backup and a directory in a
	// newer one. Prefer the directory when archived descendants exist.
	for p := range nodes {
		for p = path.Dir(p); p != "/"; p = path.Dir(p) {
			if n := nodes[p]; n == nil || !n.isDir() {
				nodes[p] = &archiveNode{path: p}
			}
		}
	}
	for p, n := range nodes {
		if p != "/" {
			parent := nodes[path.Dir(p)]
			parent.children = append(parent.children, n)
		}
	}
	for _, n := range nodes {
		sort.Slice(n.children, func(i, j int) bool { return n.children[i].path < n.children[j].path })
	}
	return nodes
}

func (s *archiveShell) absolute(name string) string {
	if !path.IsAbs(name) {
		name = s.cwd + "/" + name
	}
	return path.Clean(name)
}

// Resolve component by component so file/.. is an error, and symlink targets
// (including absolute targets) are always interpreted inside the archive.
func (s *archiveShell) resolve(name string, followFinal bool) (*archiveNode, error) {
	if name == "" {
		return nil, fmt.Errorf("empty path")
	}
	pending := name
	if !path.IsAbs(pending) {
		pending = s.cwd + "/" + pending
	}
	parts := strings.Split(pending, "/")
	current := s.nodes["/"]
	links := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		if !current.isDir() {
			return nil, fmt.Errorf("%s: not a directory", name)
		}
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			current = s.nodes[path.Dir(current.path)]
			continue
		}
		n := s.nodes[path.Join(current.path, part)]
		if n == nil {
			return nil, fmt.Errorf("%s: no such file or directory", name)
		}
		if link, ok := n.entry.(*fs.Symlink); ok && (followFinal || len(parts) > 0) {
			links++
			if links > 40 {
				return nil, fmt.Errorf("%s: too many levels of symbolic links", name)
			}
			if link.Target == "" {
				return nil, fmt.Errorf("%s: empty symbolic link target", name)
			}
			if path.IsAbs(link.Target) {
				current = s.nodes["/"]
			}
			parts = append(strings.Split(link.Target, "/"), parts...)
			continue
		}
		current = n
	}
	return current, nil
}

func nodeTime(n *archiveNode) time.Time { return n.stats().MTime }
