package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"josvisser.nl/erwtensoep/internal/fs"
)

var errVersionSelectionCanceled = errors.New("version selection canceled")
var errVersionSkipped = errors.New("file skipped")

// A selection lasts for one command. Repeated operands and symlink aliases use
// the same choice, without changing the shell's latest-version browsing tree.
type versionSelector struct {
	sb      *fs.Superblock
	in      *bufio.Reader
	out     io.Writer
	latest  bool
	choices map[string]*archiveVersion // nil means the user skipped this path
	err     error                      // prompt failure or cancellation stops the whole command
}

func newVersionSelector(sb *fs.Superblock, in *bufio.Reader, out io.Writer, latest bool) *versionSelector {
	return &versionSelector{sb: sb, in: in, out: out, latest: latest, choices: make(map[string]*archiveVersion)}
}

func (v *versionSelector) choose(name string) (*archiveVersion, error) {
	if v.err != nil {
		return nil, v.err
	}
	if choice, ok := v.choices[name]; ok {
		return choice, nil
	}
	rows, err := sortedArchiveVersions(v.sb.Entries[name])
	if err != nil {
		v.err = fmt.Errorf("%s: %w", name, err)
		return nil, v.err
	}
	var choice *archiveVersion
	if len(rows) > 0 {
		choice = &rows[0]
		if len(rows) > 1 && !v.latest {
			choice, err = promptFileVersion(v.in, v.out, "/"+filepath.ToSlash(name), rows)
			if err != nil {
				v.err = err
				return nil, err
			}
		}
	}
	v.choices[name] = choice
	return choice, nil
}

func writeVersionChoices(out io.Writer, name string, rows []archiveVersion) error {
	if _, err := fmt.Fprintf(out, "Saved versions of %q (newest first):\n", name); err != nil {
		return err
	}
	return writeArchiveVersions(out, name, rows, false, true)
}

func promptFileVersion(in *bufio.Reader, out io.Writer, name string, rows []archiveVersion) (*archiveVersion, error) {
	if err := writeVersionChoices(out, name, rows); err != nil {
		return nil, err
	}
	for {
		if _, err := fmt.Fprintf(out, "Choose version (1-%d; Enter to skip; q to cancel): ", len(rows)); err != nil {
			return nil, err
		}
		answer, readErr := in.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		switch answer {
		case "q", "quit", "cancel":
			return nil, errVersionSelectionCanceled
		case "", "n", "no", "none", "skip":
			if answer == "" && errors.Is(readErr, io.EOF) {
				return nil, errVersionSelectionCanceled
			}
			return nil, nil
		}
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= len(rows) {
			return &rows[n-1], nil
		}
		if errors.Is(readErr, io.EOF) {
			return nil, errVersionSelectionCanceled
		}
		if _, err := fmt.Fprintf(out, "Invalid selection: choose one number from 1 to %d, Enter to skip, or q to cancel.\n", len(rows)); err != nil {
			return nil, err
		}
	}
}

func (v *versionSelector) node(n *archiveNode) (*archiveNode, error) {
	key := filepath.FromSlash(strings.TrimPrefix(n.path, "/"))
	if n.entry != nil {
		key = n.entry.GetPath()
	}
	choice, err := v.choose(key)
	if err != nil {
		return nil, err
	}
	if choice == nil {
		return nil, errVersionSkipped
	}
	return &archiveNode{path: n.path, entry: choice.entry}, nil
}

func (v *versionSelector) resolve(s *archiveShell, name string, followFinal bool) (*archiveNode, error) {
	return s.resolveWithSelection(name, followFinal, func(n *archiveNode, _ bool) (*archiveNode, error) {
		if n.isDir() {
			return n, nil
		}
		return v.node(n)
	})
}

// Collect choices before streaming any file, so cancellation cannot print a
// partial set of contents. Regular path errors still allow other operands.
func (s *archiveShell) selectContentOperands(names []string, latest bool) ([]listingRow, []error, error) {
	v := newVersionSelector(s.sb, s.in, s.out, latest)
	var rows []listingRow
	var errs []error
	for _, name := range names {
		n, err := v.resolve(s, name, true)
		if v.err != nil {
			return nil, nil, v.err
		}
		if errors.Is(err, errVersionSkipped) {
			continue
		}
		if err == nil {
			if _, ok := n.entry.(*fs.RegularFile); !ok {
				err = fmt.Errorf("%s: is a directory", name)
			}
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rows = append(rows, listingRow{n, name})
	}
	return rows, errs, nil
}
