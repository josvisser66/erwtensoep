package main

import (
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// Share the shell's buffered input with the pager. Limit the editor's reads to
// one byte so it cannot retain pager keys or subsequent commands internally.
// The bufio.Reader still buffers the underlying terminal reads.
type shellTerminalIO struct {
	io.Reader
	io.Writer
}

func (rw shellTerminalIO) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return rw.Reader.Read(p)
}

func (s *archiveShell) prompt() string {
	return "erwtensoep:" + strings.TrimSuffix(s.cwd, "/") + "/ "
}

func (s *archiveShell) lineEditor() *term.Terminal {
	if s.editor == nil {
		s.editor = term.NewTerminal(shellTerminalIO{s.in, s.out}, "")
		if s.history == nil {
			s.history = &commandHistory{}
		}
		s.editor.History = s.history
		s.editor.AutoCompleteCallback = func(line string, pos int, key rune) (string, int, bool) {
			if key != '\t' {
				return "", 0, false
			}
			completed, cursor, matches := s.complete(line, pos)
			if len(matches) > 1 && completed == line {
				fmt.Fprintln(s.editor, strings.Join(matches, "\n"))
			}
			return completed, cursor, true
		}
	}
	s.editor.SetPrompt(s.prompt())
	return s.editor
}

func (s *archiveShell) readCommand() (line string, err error) {
	if s.terminal == nil {
		if _, err = fmt.Fprint(s.out, s.prompt()); err != nil {
			return "", err
		}
		return s.in.ReadString('\n')
	}
	fd := int(s.terminal.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	// Restore cooked mode before running commands, including more's own pager.
	defer func() { err = errors.Join(err, term.Restore(fd, state)) }()
	editor := s.lineEditor()
	if width, height, err := term.GetSize(fd); err == nil && width > 0 && height > 0 {
		editor.SetSize(width, height)
	}
	line, err = editor.ReadLine()
	if errors.Is(err, term.ErrPasteIndicator) {
		err = nil
	}
	return line, err
}

var shellCommands = []string{"cat", "cd", "du", "exit", "help", "ls", "more", "pwd", "quit", "rm", "versions"}

// complete replaces the word at the cursor, preserving later arguments. It
// returns escaped display candidates when the prefix is ambiguous.
func (s *archiveShell) complete(line string, pos int) (string, int, []string) {
	if pos < 0 || pos > len(line) || !utf8.ValidString(line[:pos]) {
		return line, pos, nil
	}
	words, _ := parseShellWords(line[:pos], true)
	word := shellWord{start: pos, end: pos}
	index := len(words)
	if index > 0 && words[index-1].end == pos {
		index--
		word = words[index]
	}
	var matches []string
	if index == 0 || (index == 1 && words[0].text == "help") {
		for _, command := range shellCommands {
			if strings.HasPrefix(command, word.text) {
				matches = append(matches, command)
			}
		}
	} else {
		command := words[0].text
		switch command {
		case "cd", "ls", "cat", "more", "du", "rm", "versions":
			endOptions := false
			for _, arg := range words[1:index] {
				endOptions = endOptions || arg.text == "--"
			}
			if !endOptions {
				if strings.HasPrefix(word.text, "-") {
					return line, pos, nil
				}
				previous := words[index-1].text
				if (command == "more" && (previous == "-n" || previous == "--lines")) ||
					(command == "du" && (previous == "-d" || previous == "--max-depth")) {
					return line, pos, nil
				}
			}
			matches = s.completePath(word.text, command == "cd", command != "rm" && command != "versions")
			if !endOptions {
				for i, match := range matches {
					if strings.HasPrefix(match, "-") || (command == "more" && strings.HasPrefix(match, "+")) {
						matches[i] = "./" + match
					}
				}
			}
		}
	}
	if len(matches) == 0 {
		return line, pos, nil
	}
	sort.Strings(matches)
	completed := commonCompletionPrefix(matches)
	if len(matches) > 1 && len(completed) <= len(word.text) {
		for i := range matches {
			matches[i] = quoteCompletion(matches[i], 0)
		}
		return line, pos, matches
	}
	// The cursor may be in the middle of a word; replace its remaining suffix
	// too, rather than duplicating an existing filename or closing quote.
	allWords, _ := parseShellWords(line, true)
	for _, full := range allWords {
		if full.start == word.start {
			word.end = full.end
			break
		}
	}
	var quote byte
	if word.start < len(line) && (line[word.start] == '\'' || line[word.start] == '"') {
		quote = line[word.start]
	}
	replacement := quoteCompletion(completed, quote)
	suffix := line[word.end:]
	cursor := word.start + len(replacement)
	if len(matches) == 1 && !strings.HasSuffix(completed, "/") {
		if r, size := utf8.DecodeRuneInString(suffix); unicode.IsSpace(r) {
			cursor += size
		} else {
			replacement += " "
			cursor++
		}
	}
	return line[:word.start] + replacement + suffix, cursor, nil
}

func (s *archiveShell) completePath(prefix string, directoriesOnly, followLinks bool) []string {
	slash := strings.LastIndex(prefix, "/")
	parent, base := prefix[:slash+1], prefix[slash+1:]
	lookup := parent
	if lookup == "" {
		lookup = "."
	}
	n, err := s.resolveDirectory(lookup)
	if err != nil || !n.isDir() {
		return nil
	}
	var matches []string
	if base == "." || base == ".." {
		for _, dot := range []string{".", ".."} {
			if strings.HasPrefix(dot, base) {
				matches = append(matches, parent+dot+"/")
			}
		}
	}
	for _, child := range n.children {
		name := path.Base(child.path)
		if !strings.HasPrefix(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) ||
			strings.IndexFunc(name, unicode.IsControl) >= 0 {
			continue
		}
		isDir := child.isDir()
		if followLinks && !isDir && s.nodes[directoryKey(child.path)] == nil {
			if resolved, err := s.resolve(parent+name, true); err == nil {
				isDir = resolved.isDir()
			}
		}
		if directoriesOnly && !isDir {
			continue
		}
		match := parent + name
		if isDir {
			match += "/"
		}
		matches = append(matches, match)
	}
	return matches
}

func commonCompletionPrefix(matches []string) string {
	prefix := []rune(matches[0])
	for _, match := range matches[1:] {
		runes := []rune(match)
		i := 0
		for i < len(prefix) && i < len(runes) && prefix[i] == runes[i] {
			i++
		}
		prefix = prefix[:i]
	}
	return string(prefix)
}

// Return a word that splitShellWords will interpret literally, even for spaces,
// quotes, backslashes, and glob metacharacters. Closed quotes allow completed
// directories to be used immediately or followed by more path components.
func quoteCompletion(word string, quote byte) string {
	switch quote {
	case '\'':
		return "'" + strings.ReplaceAll(word, "'", "'\\''") + "'"
	case '"':
		return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(word) + "\""
	default:
		var out strings.Builder
		for _, r := range word {
			if unicode.IsSpace(r) || strings.ContainsRune("\\'\"*?[]", r) {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
		return out.String()
	}
}
