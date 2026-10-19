package main

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/spf13/pflag"
	"josvisser.nl/erwtensoep/internal/fs"
)

func (s *archiveShell) cd(args []string) error {
	f := pflag.NewFlagSet("cd", pflag.ContinueOnError)
	physical := false
	f.BoolVarP(&physical, "physical", "P", false, "Resolve symbolic links before ..")
	logical := f.BoolP("logical", "L", false, "Keep logical symbolic link paths (default)")
	if err := s.parseFlags(f, args, "cd [-L|-P] [DIR|-]"); err != nil {
		return err
	}
	if f.NArg() > 1 {
		return fmt.Errorf("too many arguments")
	}
	if physical && *logical {
		return fmt.Errorf("-L and -P cannot be combined")
	}
	target := "/"
	if f.NArg() == 1 {
		target = f.Arg(0)
	}
	printPath := target == "-"
	if printPath {
		if s.previous == "" {
			return fmt.Errorf("previous directory is not set")
		}
		target = s.previous
	}
	if target == "" {
		return fmt.Errorf("empty directory name")
	}
	lookup := target
	if !physical {
		lookup = s.cwd
		if path.IsAbs(target) {
			lookup = "/"
		}
		// Validate each logical component before cleaning .., so file/.. and
		// missing/.. cannot silently succeed, while link/.. uses its parent.
		for _, component := range strings.Split(target, "/") {
			lookup = path.Join(lookup, component)
			n, err := s.resolve(lookup, true)
			if err != nil {
				return err
			}
			if !n.isDir() {
				return fmt.Errorf("%s: not a directory", target)
			}
		}
	}
	n, err := s.resolve(lookup, true)
	if err != nil {
		return err
	}
	if !n.isDir() {
		return fmt.Errorf("%s: not a directory", target)
	}
	newPath := s.absolute(target)
	if physical {
		newPath = n.path
	}
	s.previous, s.cwd = s.cwd, newPath
	if printPath {
		_, err = fmt.Fprintln(s.out, s.cwd)
	}
	return err
}

func (s *archiveShell) pwd(args []string) error {
	f := pflag.NewFlagSet("pwd", pflag.ContinueOnError)
	physical := f.BoolP("physical", "P", false, "Print the resolved directory path")
	logical := f.BoolP("logical", "L", false, "Print the logical directory path (default)")
	if err := s.parseFlags(f, args, "pwd [-L|-P]"); err != nil {
		return err
	}
	if f.NArg() > 0 || (*physical && *logical) {
		return fmt.Errorf("usage: pwd [-L|-P]")
	}
	p := s.cwd
	if *physical {
		n, err := s.resolve(p, true)
		if err != nil {
			return err
		}
		p = n.path
	}
	_, err := fmt.Fprintln(s.out, p)
	return err
}

func nodeSize(n *archiveNode) int64 {
	size, _ := strconv.ParseInt(n.stats().Size, 10, 64)
	return size
}

type listingOptions struct {
	all, almostAll, long, human, recursive, directory, classify, reverse, byTime, bySize bool
}

func (s *archiveShell) ls(args []string) error {
	f := pflag.NewFlagSet("ls", pflag.ContinueOnError)
	var o listingOptions
	f.BoolVarP(&o.all, "all", "a", false, "Include hidden entries, . and ..")
	f.BoolVarP(&o.almostAll, "almost-all", "A", false, "Include hidden entries, excluding . and ..")
	f.BoolVarP(&o.long, "long", "l", false, "Show permissions, owner, group, size, and modification time")
	f.BoolVarP(&o.human, "human-readable", "h", false, "Show sizes in human-readable units")
	f.BoolVarP(&o.recursive, "recursive", "R", false, "List subdirectories recursively")
	f.BoolVarP(&o.directory, "directory", "d", false, "List directories themselves")
	f.BoolVarP(&o.classify, "classify", "F", false, "Append / to directories, @ to symlinks, * to executables")
	f.BoolVarP(&o.reverse, "reverse", "r", false, "Reverse the sort order")
	f.BoolVarP(&o.byTime, "time", "t", false, "Sort by modification time, newest first")
	f.BoolVarP(&o.bySize, "size", "S", false, "Sort by size, largest first (takes precedence over -t)")
	f.BoolP("one-per-line", "1", false, "List one entry per line (default)")
	latest := f.Bool("latest", false, "Use the newest saved version for file operands without prompting")
	if err := s.parseFlags(f, args, "ls [-alhARdFtrS1] [--latest] [PATH...] (choose versions for file operands; directories list newest versions)"); err != nil {
		return err
	}
	names := f.Args()
	if len(names) == 0 {
		names = []string{"."}
	}
	var errs []error
	var rows []listingRow
	v := newVersionSelector(s.sb, s.in, s.out, *latest)
	for _, name := range names {
		n, err := v.resolve(s, name, false, false)
		if v.err != nil {
			return v.err
		}
		if errors.Is(err, errVersionSkipped) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !o.long && !o.directory {
			// Follow the chosen symlink to a directory, but do not ask about
			// target files whose metadata is not part of this listing.
			resolved, err := s.resolveWithSelection(name, true, func(target *archiveNode, _ bool) (*archiveNode, error) {
				if !target.isDir() {
					if choice := v.choices[target.entry.GetPath()]; choice != nil {
						return &archiveNode{path: target.path, entry: choice.entry}, nil
					}
				}
				return target, nil
			})
			if err == nil && resolved.isDir() {
				n = resolved
			}
		}
		rows = append(rows, listingRow{n, name})
	}
	sortListingRows(rows, o)
	// Like ls, print file operands before directory contents.
	if !o.directory {
		sort.SliceStable(rows, func(i, j int) bool { return !rows[i].node.isDir() && rows[j].node.isDir() })
	}
	for i, row := range rows {
		n, name := row.node, row.name
		if o.directory || !n.isDir() {
			s.printNode(n, name, o)
			continue
		}
		if i > 0 {
			fmt.Fprintln(s.out)
		}
		s.listDirectory(n, name, o, len(names) > 1 || o.recursive)
	}
	return errors.Join(errs...)
}

type listingRow struct {
	node *archiveNode
	name string
}

func (s *archiveShell) listDirectory(n *archiveNode, name string, o listingOptions, header bool) {
	if header {
		fmt.Fprintf(s.out, "%s:\n", name)
	}
	var rows []listingRow
	if o.all {
		rows = append(rows, listingRow{n, "."}, listingRow{s.nodes[path.Dir(n.path)], ".."})
	}
	for _, child := range n.children {
		base := path.Base(child.path)
		if !o.all && !o.almostAll && strings.HasPrefix(base, ".") {
			continue
		}
		rows = append(rows, listingRow{child, base})
	}
	sortListingRows(rows, o)
	for _, row := range rows {
		s.printNode(row.node, row.name, o)
	}
	if o.recursive {
		for _, row := range rows {
			if row.node.isDir() && row.name != "." && row.name != ".." {
				fmt.Fprintln(s.out)
				s.listDirectory(row.node, path.Join(name, row.name), o, true)
			}
		}
	}
}

func sortListingRows(rows []listingRow, o listingOptions) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if o.reverse {
			a, b = b, a
		}
		if o.bySize && nodeSize(a.node) != nodeSize(b.node) {
			return nodeSize(a.node) > nodeSize(b.node)
		}
		if !o.bySize && o.byTime && !nodeTime(a.node).Equal(nodeTime(b.node)) {
			return nodeTime(a.node).After(nodeTime(b.node))
		}
		return a.name < b.name
	})
}

func (s *archiveShell) printNode(n *archiveNode, name string, o listingOptions) {
	stats := n.stats()
	var versionSuffix string
	if !n.isDir() {
		versionSuffix = versionCountSuffix(len(s.sb.Entries[n.entry.GetPath()]))
	}
	if o.classify {
		switch {
		case n.isDir():
			name += "/"
		case stats.Mode&0o111 != 0 && stats.Mode.IsRegular():
			name += "*"
		default:
			if _, ok := n.entry.(*fs.Symlink); ok {
				name += "@"
			}
		}
	}
	if !o.long {
		fmt.Fprintln(s.out, name+versionSuffix)
		return
	}
	sizeText := stats.Size
	if o.human {
		sizeText = humanize.IBytes(uint64(nodeSize(n)))
	}
	mtime := "-"
	if !stats.MTime.IsZero() {
		mtime = stats.MTime.Format("2006-01-02 15:04")
	}
	if link, ok := n.entry.(*fs.Symlink); ok {
		name += " -> " + link.Target
	}
	fmt.Fprintf(s.out, "%s %s %s %8s %16s %s%s\n", stats.Mode, stats.Owner, stats.Group, sizeText, mtime, name, versionSuffix)
}

func (s *archiveShell) du(args []string) error {
	f := pflag.NewFlagSet("du", pflag.ContinueOnError)
	all := f.BoolP("all", "a", false, "Show files as well as directories")
	human := f.BoolP("human-readable", "h", false, "Show human-readable sizes")
	summary := f.BoolP("summarize", "s", false, "Show only each argument's total")
	kilo := f.BoolP("kilobytes", "k", false, "Use 1024-byte units (default)")
	mega := f.BoolP("megabytes", "m", false, "Use 1048576-byte units")
	bytes := f.BoolP("bytes", "b", false, "Show exact logical byte counts")
	total := f.BoolP("total", "c", false, "Print a grand total")
	depth := f.IntP("max-depth", "d", -1, "Show entries at most this many levels below each argument")
	latest := f.Bool("latest", false, "Use the newest saved version of every file without prompting")
	if err := s.parseFlags(f, args, "du [-ahskbmc] [-d DEPTH] [--latest] [PATH...] (choose a version of each file counted)"); err != nil {
		return err
	}
	if *depth < -1 || (f.Changed("max-depth") && *depth < 0) {
		return fmt.Errorf("depth must be non-negative")
	}
	if *summary && f.Changed("max-depth") && *depth != 0 {
		return fmt.Errorf("-s conflicts with --max-depth other than 0")
	}
	if *summary {
		*depth = 0
	}
	unit := int64(1024)
	if *mega {
		unit = 1024 * 1024
	}
	if *kilo {
		unit = 1024
	}
	if *bytes {
		unit = 1
	}
	printSize := func(size int64, name string) {
		value := strconv.FormatInt(size/unit, 10)
		if size%unit != 0 {
			value = strconv.FormatInt(size/unit+1, 10)
		}
		if *human {
			value = humanize.IBytes(uint64(size))
		}
		fmt.Fprintf(s.out, "%s\t%s\n", value, name)
	}
	var walk func(*archiveNode, string, int) int64
	walk = func(n *archiveNode, name string, level int) int64 {
		size := nodeSize(n)
		for _, child := range n.children {
			size += walk(child, path.Join(name, path.Base(child.path)), level+1)
		}
		if (*depth < 0 || level <= *depth) && (n.isDir() || *all || level == 0) {
			printSize(size, name)
		}
		return size
	}
	names := f.Args()
	if len(names) == 0 {
		names = []string{"."}
	}
	v := newVersionSelector(s.sb, s.in, s.out, *latest)
	var selectTree func(*archiveNode) (*archiveNode, error)
	selectTree = func(n *archiveNode) (*archiveNode, error) {
		if !n.isDir() {
			return v.node(n)
		}
		copy := &archiveNode{path: n.path}
		for _, child := range n.children {
			selected, err := selectTree(child)
			if errors.Is(err, errVersionSkipped) {
				continue
			}
			if err != nil {
				return nil, err
			}
			copy.children = append(copy.children, selected)
		}
		return copy, nil
	}
	var errs []error
	var rows []listingRow
	for _, name := range names {
		n, err := v.resolve(s, name, false, false)
		if err == nil {
			n, err = selectTree(n)
		}
		if v.err != nil {
			return v.err
		}
		if errors.Is(err, errVersionSkipped) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rows = append(rows, listingRow{n, name})
	}
	var grandTotal int64
	for _, row := range rows {
		grandTotal += walk(row.node, row.name, 0)
	}
	if *total {
		printSize(grandTotal, "total")
	}
	return errors.Join(errs...)
}
