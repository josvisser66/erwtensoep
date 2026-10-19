package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"golang.org/x/term"
	"josvisser.nl/erwtensoep/internal/fs"
)

func (s *archiveShell) cat(args []string) error {
	f := pflag.NewFlagSet("cat", pflag.ContinueOnError)
	number := f.BoolP("number", "n", false, "Number all output lines")
	nonblank := f.BoolP("number-nonblank", "b", false, "Number nonempty lines (overrides -n)")
	squeeze := f.BoolP("squeeze-blank", "s", false, "Suppress repeated empty lines")
	ends := f.BoolP("show-ends", "E", false, "Display $ at each line ending")
	tabs := f.BoolP("show-tabs", "T", false, "Display tabs as ^I")
	latest := f.Bool("latest", false, "Use the newest saved version without prompting")
	if err := s.parseFlags(f, args, "cat [-nbsET] [--latest] FILE... (choose a version when multiple are saved)"); err != nil {
		return err
	}
	if f.NArg() == 0 {
		return fmt.Errorf("at least one file is required")
	}
	rows, errs, err := s.selectContentOperands(f.Args(), *latest)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(s.out)
	lineNumber, lineStart, previousBlank := 1, true, false
	for _, row := range rows {
		name := row.name
		file, err := s.sb.OpenRegularFile(row.node.entry.(*fs.RegularFile))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !*number && !*nonblank && !*squeeze && !*ends && !*tabs {
			_, err = io.Copy(out, file)
		} else {
			reader := bufio.NewReader(file)
			for {
				var b byte
				b, err = reader.ReadByte()
				if err == io.EOF {
					err = nil
					break
				}
				if err != nil {
					break
				}
				if lineStart {
					blank := b == '\n'
					if *squeeze && blank && previousBlank {
						continue
					}
					if (*number || *nonblank) && (!*nonblank || !blank) {
						fmt.Fprintf(out, "%6d\t", lineNumber)
						lineNumber++
					}
					lineStart, previousBlank = false, blank
				}
				switch {
				case b == '\n':
					if *ends {
						out.WriteByte('$')
					}
					err = out.WriteByte(b)
					lineStart = true
				case b == '\t' && *tabs:
					_, err = out.WriteString("^I")
				default:
					err = out.WriteByte(b)
				}
				if err != nil {
					break
				}
			}
		}
		err = errors.Join(err, file.Close())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(append(errs, out.Flush())...)
}

func (s *archiveShell) more(args []string) error {
	// Accept traditional more -20 and +10 in addition to --lines/-n.
	var normalized []string
	start := 1
	options := true
	for _, arg := range args {
		if options && arg == "--" {
			options = false
		} else if options && len(arg) > 1 {
			if count, err := strconv.Atoi(arg[1:]); err == nil {
				switch arg[0] {
				case '-':
					normalized = append(normalized, "--lines="+arg[1:])
					continue
				case '+':
					start = count
					continue
				}
			}
		}
		normalized = append(normalized, arg)
	}
	f := pflag.NewFlagSet("more", pflag.ContinueOnError)
	lines := f.IntP("lines", "n", s.pageLines, "Number of lines per page (also -NUMBER)")
	squeeze := f.BoolP("squeeze-blank", "s", false, "Suppress repeated empty lines")
	latest := f.Bool("latest", false, "Use the newest saved version without prompting")
	if err := s.parseFlags(f, normalized, "more [-s] [-n LINES|-LINES] [+LINE] [--latest] FILE... (choose a version when multiple are saved)"); err != nil {
		return err
	}
	if *lines < 1 || start < 1 {
		return fmt.Errorf("page length and starting line must be positive")
	}
	if f.NArg() == 0 {
		return fmt.Errorf("at least one file is required")
	}
	rows, errs, err := s.selectContentOperands(f.Args(), *latest)
	if err != nil {
		return err
	}
	for _, row := range rows {
		name := row.name
		file, err := s.sb.OpenRegularFile(row.node.entry.(*fs.RegularFile))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if f.NArg() > 1 {
			fmt.Fprintf(s.out, "::::::::::::::\n%s\n::::::::::::::\n", name)
		}
		quit, readErr := s.page(file, *lines, start, *squeeze)
		if err := errors.Join(readErr, file.Close()); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		if quit {
			break
		}
	}
	return errors.Join(errs...)
}

func (s *archiveShell) page(file io.Reader, pageSize, start int, squeeze bool) (bool, error) {
	r := bufio.NewReader(file)
	line, remaining := 1, pageSize
	previousBlank, fragment := false, false
	for {
		data, readErr := r.ReadSlice('\n')
		blank := !fragment && len(data) == 1 && data[0] == '\n'
		show := line >= start && !(squeeze && blank && previousBlank)
		if show && len(data) > 0 {
			if _, err := s.out.Write(data); err != nil {
				return false, err
			}
		}
		fragment = readErr == bufio.ErrBufferFull
		if fragment {
			continue
		}
		line++
		previousBlank = blank
		if readErr != nil {
			if readErr == io.EOF {
				return false, nil
			}
			return false, readErr
		}
		if show {
			remaining--
		}
		if s.paging && remaining == 0 {
			if _, err := r.Peek(1); err == io.EOF {
				return false, nil
			} else if err != nil {
				return false, err
			}
			n, err := s.morePrompt(pageSize)
			if err != nil || n == 0 {
				return true, err
			}
			remaining = n
		}
	}
}

func (s *archiveShell) morePrompt(pageSize int) (int, error) {
	const prompt = "--More-- (Space: page, Enter: line, q: quit)"
	if _, err := fmt.Fprint(s.out, prompt); err != nil {
		return 0, err
	}
	defer fmt.Fprintf(s.out, "\r%s\r", strings.Repeat(" ", len(prompt)))
	for {
		key, err := s.pagerKey()
		if err == io.EOF {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		switch key {
		case 'q', 'Q', 3, 4, 27: // q, Ctrl-C, Ctrl-D, Escape
			return 0, nil
		case ' ', 'f':
			return pageSize, nil
		case '\r', '\n':
			return 1, nil
		}
	}
}

func (s *archiveShell) pagerKey() (byte, error) {
	if s.terminal == nil {
		return s.in.ReadByte()
	}
	fd := int(s.terminal.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return 0, err
	}
	key, readErr := s.in.ReadByte()
	return key, errors.Join(readErr, term.Restore(fd, state))
}
