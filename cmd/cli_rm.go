package main

import (
	"errors"
	"fmt"
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
	allVersions := f.Bool("all-versions", false, "Remove every saved version without a version-selection prompt")
	promptMode := ""
	f.VarP(rmPromptFlag{&promptMode, "force"}, "force", "f", "Ignore missing paths, remove all versions, and never prompt")
	f.Lookup("force").NoOptDefVal = "true"
	f.VarP(rmPromptFlag{&promptMode, "interactive"}, "interactive", "i", "Also confirm single-version paths or --all-versions removals")
	f.Lookup("interactive").NoOptDefVal = "true"
	if err := s.parseFlags(f, args, "rm [-rRfiv] [--all-versions] PATH... (choose versions when multiple are saved; -f/-i: last wins)"); err != nil {
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
	// Cooked-mode prompts share the shell's buffered reader; answers are not
	// commands and must not consume input belonging to the next command.
	choices, err := selectRemovalVersions(s.sb, targets, s.in, s.out, *allVersions || force, promptMode == "interactive")
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if len(choices) == 0 {
		return errors.Join(errs...)
	}
	removed, err := s.sb.RemoveVersions(choices)
	if err != nil {
		errs = append(errs, err)
	}
	if removed > 0 {
		s.refreshAfterRemoval()
		if *verbose {
			for _, name := range targets {
				if ids := choices[name]; len(ids) > 0 {
					if remaining := len(s.sb.Entries[name]); remaining == 0 {
						fmt.Fprintf(s.out, "removed %q (all saved versions)\n", "/"+filepath.ToSlash(name))
					} else {
						fmt.Fprintf(s.out, "removed %q (%d version(s); %d remaining)\n", "/"+filepath.ToSlash(name), len(ids), remaining)
					}
				}
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
