package fs

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"strconv"
	"strings"
)

func isTarSource(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz")
}

type tarMember struct {
	header  *tar.Header
	name    string
	target  *tarMember // hard-link target in this tar, never a host path
	file    *RegularFile
	include bool
	needed  bool // regular-file data needed by this member or a hard link
}

func tarRegular(h *tar.Header) bool {
	return h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA || h.Typeflag == tar.TypeGNUSparse
}

func tarPath(name string, directory bool) (string, error) {
	if name == "" || path.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe tar member path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe tar member path %q", name)
		}
	}
	clean := path.Clean(name)
	if clean == "." && !directory {
		return "", fmt.Errorf("invalid tar file path %q", name)
	}
	return clean, nil
}

func tarStats(h *tar.Header) FileStats {
	owner, group := h.Uname, h.Gname
	if owner == "" {
		owner = strconv.Itoa(h.Uid)
	}
	if group == "" {
		group = strconv.Itoa(h.Gid)
	}
	size := h.Size
	if h.Typeflag == tar.TypeSymlink {
		size = int64(len(h.Linkname))
	}
	return FileStats{Owner: owner, Group: group, Size: strconv.FormatInt(size, 10), size: size,
		Mode: h.FileInfo().Mode(), MTime: h.ModTime}
}

func tarIgnored(name string, patterns map[string][]string) bool {
	var inherited *ignorePatterns
	dir := "."
	for _, component := range strings.Split(name, "/") {
		inherited = &ignorePatterns{patterns: patterns[dir], prev: inherited}
		if inherited.match(component) {
			return true
		}
		dir = path.Join(dir, component)
	}
	return false
}

type mergeSourceReader struct {
	io.Reader
	sb *Superblock
}

func (r mergeSourceReader) Read(p []byte) (int, error) {
	if r.sb.sigReceived.Load() {
		return 0, errors.New("merge interrupted")
	}
	return r.Reader.Read(p)
}

// Read through all members and the gzip trailer, including ignored data. Tar
// EOF alone can precede the gzip checksum; Close does not validate that checksum.
func (sb *Superblock) readTar(source *os.File, visit func(*tar.Header, *tar.Reader) error) error {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var input io.Reader = source
	if !strings.HasSuffix(strings.ToLower(source.Name()), ".tar") {
		gz, err := gzip.NewReader(source)
		if err != nil {
			return err
		}
		defer gz.Close()
		input = gz
	}
	input = mergeSourceReader{Reader: input, sb: sb}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			_, err = io.Copy(io.Discard, input)
			return err
		}
		if err != nil {
			return err
		}
		if err := visit(header, reader); err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return fmt.Errorf("%s: %w", header.Name, err)
		}
	}
}

// Preflight discovers exclusion files independent of member order, validates
// paths/links and the complete input stream, and retains only metadata. Import
// then streams each needed payload directly into the normal block/blob store.
func (sb *Superblock) mergeTar(filename string) (changes, failures int, resultErr error) {
	source, err := os.Open(filename)
	if err != nil {
		return 0, 1, err
	}
	defer source.Close()
	defer func() {
		if resultErr != nil {
			failures++
			resultErr = fmt.Errorf("tar source %s: %w", filename, resultErr)
		}
	}()
	var members []*tarMember
	seen := make(map[string]*tarMember)
	pending := make(map[string][]*tarMember)
	patterns := make(map[string][]string)
	err = sb.readTar(source, func(h *tar.Header, data *tar.Reader) error {
		member := &tarMember{header: h}
		members = append(members, member)
		if h.Typeflag == tar.TypeXGlobalHeader {
			return nil
		}
		name, err := tarPath(h.Name, h.Typeflag == tar.TypeDir)
		if err != nil {
			return err
		}
		member.name = name
		if h.Typeflag == tar.TypeSymlink && (h.Linkname == "" || strings.ContainsRune(h.Linkname, 0)) {
			return fmt.Errorf("%s: invalid symlink target", name)
		}
		if h.Typeflag == tar.TypeLink {
			target, err := tarPath(h.Linkname, false)
			if err != nil {
				return fmt.Errorf("%s: hard link: %w", name, err)
			}
			if previous := seen[target]; previous != nil {
				member.target = previous
			} else {
				pending[target] = append(pending[target], member)
			}
		}
		seen[name] = member
		for _, link := range pending[name] {
			link.target = member
		}
		delete(pending, name)
		if tarRegular(h) && path.Base(name) == ".erwtensoep" {
			contents, err := io.ReadAll(data)
			if err != nil {
				return err
			}
			patterns[path.Dir(name)] = strings.Split(string(contents), "\n")
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	for _, member := range members {
		h := member.header
		member.include = member.name != "" && !tarIgnored(member.name, patterns)
		if tarRegular(h) {
			member.file = &RegularFile{Path: member.name, FileStats: tarStats(h)}
			member.needed = member.include && h.Size >= sb.options.MinSize && h.Size <= sb.options.MaxSize
		}
	}
	for _, member := range members {
		if member.header.Typeflag != tar.TypeLink || !member.include {
			continue
		}
		target := member
		visited := make(map[*tarMember]bool)
		for target != nil && target.header.Typeflag == tar.TypeLink {
			if visited[target] {
				return 0, 0, fmt.Errorf("%s: cyclic tar hard link", member.name)
			}
			visited[target] = true
			target = target.target
		}
		if target == nil || !tarRegular(target.header) {
			return 0, 0, fmt.Errorf("%s: hard-link target %q is not a regular file in this tar", member.name, member.header.Linkname)
		}
		member.target = target
		member.file = &RegularFile{Path: member.name, FileStats: tarStats(member.header)}
		member.file.Mode &^= os.ModeType
		member.file.Size, member.file.size = target.file.Size, target.file.size
		_, alreadySaved := sb.Entries[member.name][member.file.hash()].(*RegularFile)
		if !alreadySaved && member.file.size >= sb.options.MinSize && member.file.size <= sb.options.MaxSize {
			target.needed = true
		}
	}
	index := 0
	err = sb.readTar(source, func(h *tar.Header, data *tar.Reader) error {
		if index >= len(members) || !reflect.DeepEqual(h, members[index].header) {
			return errors.New("tar source changed between validation and import")
		}
		member := members[index]
		index++
		var n int
		var err error
		switch {
		case tarRegular(h):
			if member.include {
				n, err = sb.mergeRegularEntry(member.file, func() (*Blob, error) { return member.file.blobifyReader(sb, data) })
			} else if member.needed {
				// An included hard link may refer to a file excluded by name.
				var blob *Blob
				blob, err = member.file.blobifyReader(sb, data)
				if err == nil {
					member.file.BlobID = blob.ID
				}
			}
		case !member.include, h.Typeflag == tar.TypeDir, h.Typeflag == tar.TypeLink, h.Typeflag == tar.TypeXGlobalHeader:
		case h.Typeflag == tar.TypeSymlink:
			n, err = sb.mergeSymlinkEntry(&Symlink{Path: member.name, Target: h.Linkname, FileStats: tarStats(h)})
		default:
			fmt.Fprintf(os.Stderr, "WARNING: skipping unsupported tar member %q (type %q).\n", h.Name, h.Typeflag)
		}
		changes += n
		if err != nil {
			return fmt.Errorf("%s: %w", member.name, err)
		}
		return sb.maybeWrite()
	})
	if err != nil {
		return changes, 0, err
	}
	if index != len(members) {
		return changes, 0, errors.New("tar source changed between validation and import")
	}
	for _, member := range members {
		if member.header.Typeflag != tar.TypeLink || !member.include {
			continue
		}
		n, err := sb.mergeRegularEntry(member.file, func() (*Blob, error) {
			blob := sb.Blobs[member.target.file.BlobID]
			if blob == nil {
				return nil, fmt.Errorf("missing data for hard-link target %q", member.header.Linkname)
			}
			return blob, nil
		})
		changes += n
		if err != nil {
			return changes, 0, err
		}
		if err := sb.maybeWrite(); err != nil {
			return changes, 0, err
		}
	}
	return changes, 0, nil
}
