package main

import (
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

// The last -f or -i wins, including within combined flags such as -fi.
type rmPromptFlag struct {
	mode  *string
	value string
}

func (f rmPromptFlag) String() string { return strconv.FormatBool(*f.mode == f.value) }
func (f rmPromptFlag) Type() string   { return "bool" }
func (f rmPromptFlag) Set(value string) error {
	on, err := strconv.ParseBool(value)
	if err == nil {
		if on {
			*f.mode = f.value
		} else if *f.mode == f.value {
			*f.mode = ""
		}
	}
	return err
}

func (s *archiveShell) rm(args []string) error {
	f := pflag.NewFlagSet("rm", pflag.ContinueOnError)
	var recursive bool
	f.BoolVarP(&recursive, "recursive", "r", false, "Remove directories and their contents")
	f.BoolVarP(&recursive, "recursive-uppercase", "R", false, "Same as -r")
	_ = f.MarkHidden("recursive-uppercase")
	verbose := f.BoolP("verbose", "v", false, "Print each removed archive path")
	promptMode := ""
	f.VarP(rmPromptFlag{&promptMode, "force"}, "force", "f", "Ignore missing paths and never prompt")
	f.Lookup("force").NoOptDefVal = "true"
	f.VarP(rmPromptFlag{&promptMode, "interactive"}, "interactive", "i", "Ask before removing each path and all its versions")
	f.Lookup("interactive").NoOptDefVal = "true"
	if err := s.parseFlags(f, args, "rm [-rRfiv] PATH... (removes all saved versions; -f/-i: last wins)"); err != nil {
		return err
	}
	force := promptMode == "force"
	if f.NArg() == 0 {
		if force {
			return nil
		}
		return fmt.Errorf("missing operand")
	}

	// Resolve operands once, but select exact index keys. Passing them through
	// the archive's pattern-based Remove would reinterpret quoted * or [ names.
	selected := make(map[string]bool) // true for recursive directory operands
	var errs []error
	for _, name := range f.Args() {
		trimmed := strings.TrimRight(name, "/")
		if name != "" && trimmed == "" {
			trimmed = "/"
		}
		if base := path.Base(trimmed); base == "." || base == ".." {
			errs = append(errs, fmt.Errorf("refusing to remove %q: final component is %s", name, base))
			continue
		}
		n, err := s.resolve(trimmed, false)
		if err != nil {
			if !force || !errors.Is(err, errArchiveNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if n.path == "/" {
			errs = append(errs, fmt.Errorf("refusing to remove the archive root"))
			continue
		}
		if strings.HasSuffix(name, "/") && !n.isDir() {
			// Never turn a final symlink into recursive deletion of its target.
			errs = append(errs, fmt.Errorf("%s: not a directory (remove the trailing slash to remove the entry)", name))
			continue
		}
		if n.isDir() && !recursive {
			errs = append(errs, fmt.Errorf("%s: is a directory (use -r)", name))
			continue
		}
		selected[n.path] = n.isDir()
	}
	var targets []string
	for name := range s.sb.Entries {
		p := path.Join("/", filepath.ToSlash(name))
		_, chosen := selected[p]
		for parent := path.Dir(p); !chosen && parent != "/"; parent = path.Dir(parent) {
			chosen = selected[parent]
		}
		if chosen {
			targets = append(targets, name)
		}
	}
	sort.Strings(targets)
	if promptMode == "interactive" {
		var confirmed []string
		for _, name := range targets {
			if _, err := fmt.Fprintf(s.out, "Remove %q and all %d saved version(s)? [y/N]: ", "/"+filepath.ToSlash(name), len(s.sb.Entries[name])); err != nil {
				return errors.Join(append(errs, err)...)
			}
			// readCommand restores cooked mode before executing commands. Reuse
			// its buffered reader so confirmations never eat the next command.
			answer, err := s.in.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return errors.Join(append(errs, err)...)
			}
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer == "y" || answer == "yes" {
				confirmed = append(confirmed, name)
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		targets = confirmed
	}
	if len(targets) == 0 {
		return errors.Join(errs...)
	}
	removed, err := s.sb.RemovePaths(targets)
	if err != nil {
		errs = append(errs, err)
	}
	if removed > 0 {
		s.refreshAfterRemoval()
		if *verbose {
			for _, name := range targets {
				fmt.Fprintf(s.out, "removed %q (all saved versions)\n", "/"+filepath.ToSlash(name))
			}
		}
	}
	return errors.Join(errs...)
}

func (s *archiveShell) refreshAfterRemoval() {
	s.nodes = archiveTree(s.sb)
	// Deleting the last file also removes its inferred directory. Keep the
	// shell usable by walking back to the nearest surviving logical ancestor.
	for {
		if n, err := s.resolve(s.cwd, true); err == nil && n.isDir() {
			break
		}
		s.cwd = path.Dir(s.cwd)
	}
	if s.previous != "" {
		if n, err := s.resolve(s.previous, true); err != nil || !n.isDir() {
			s.previous = ""
		}
	}
}
