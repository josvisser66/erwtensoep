package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
	"josvisser.nl/erwtensoep/internal/fs"
)

func init() { rootCmd.AddCommand(cliCmd) }

var cliCmd = &cobra.Command{
	Use:   "cli",
	Short: "Browse an archive with an interactive Unix-style shell",
	Long:  "Browse the latest archived versions using cd, pwd, ls, cat, more, and du.\nPaths and symbolic links are resolved inside the archive. Type help for commands.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sb, err := fs.GetSuperblock(&options)
		if err != nil {
			return err
		}
		return newArchiveShell(sb, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()).run()
	},
}

type archiveShell struct {
	sb          *fs.Superblock
	nodes       map[string]*archiveNode
	cwd         string
	previous    string
	in          *bufio.Reader
	out, errOut io.Writer
	terminal    *os.File
	editor      *term.Terminal
	paging      bool
	pageLines   int
}

func newArchiveShell(sb *fs.Superblock, in io.Reader, out, errOut io.Writer) *archiveShell {
	s := &archiveShell{
		sb: sb, nodes: archiveTree(sb), cwd: "/", in: bufio.NewReader(in),
		out: out, errOut: errOut, pageLines: 23,
	}
	if input, ok := in.(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		if output, ok := out.(*os.File); ok && term.IsTerminal(int(output.Fd())) {
			s.terminal, s.paging = input, true
			if _, height, err := term.GetSize(int(output.Fd())); err == nil && height > 1 {
				s.pageLines = height - 1
			}
		}
	}
	return s
}

var errShellExit = errors.New("exit shell")
var errShellHelp = errors.New("help displayed")

func (s *archiveShell) run() error {
	for {
		line, readErr := s.readCommand()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(line) > 0 {
			if err := s.execute(line); errors.Is(err, errShellExit) {
				return nil
			} else if err != nil && !errors.Is(err, errShellHelp) {
				fmt.Fprintln(s.errOut, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			fmt.Fprintln(s.out)
			return nil
		}
	}
}

const shellHelp = `Commands (use COMMAND --help for flags):
  cd [-L|-P] [DIR|-]      Change directory; no argument returns to /
  pwd [-L|-P]             Print the current directory
  ls [-alhARdFtrS1] [PATH...]  List files and directories
  cat [-nbsET] FILE...    Print file contents
  more [-s] [-n LINES] [+LINE] FILE...  Page through file contents
  du [-ahskbmc] [-d DEPTH] [PATH...]   Show uncompressed sizes
  help [COMMAND]         Show help
  exit, quit             Leave the shell (EOF also exits)

Tab completes commands and archive paths, extending a shared prefix or listing
matches. Up/Down recall the last 100 commands in this session and restore the
unfinished line when returning past the newest command.

Paths are archive-relative; / is the archive root. Quotes, backslash escapes,
and unquoted *, ?, [...] globs are supported. Pipes, redirects, variables,
and host commands are not interpreted. Only the latest file versions appear.
Directories are inferred; empty directories and directory metadata are absent.
du counts logical file bytes, including hidden files, without deduplicating.
more uses Space for a page, Enter for a line, and q to stop; redirected output
is printed without paging. All commands leave the archive unchanged.
`

func (s *archiveShell) execute(line string) error {
	words, err := splitShellWords(line)
	if err != nil || len(words) == 0 {
		return err
	}
	name := words[0].text
	var args []string
	for _, word := range words[1:] {
		args = append(args, s.expand(word)...)
	}
	switch name {
	case "exit", "quit":
		if len(args) > 0 {
			return fmt.Errorf("%s: no arguments expected", name)
		}
		return errShellExit
	case "help":
		if len(args) == 0 {
			_, err = fmt.Fprint(s.out, shellHelp)
		} else if len(args) == 1 && args[0] != "help" {
			err = s.execute(args[0] + " --help")
		} else {
			err = fmt.Errorf("usage: help [COMMAND]")
		}
	case "cd":
		err = s.cd(args)
	case "pwd":
		err = s.pwd(args)
	case "ls":
		err = s.ls(args)
	case "cat":
		err = s.cat(args)
	case "more":
		err = s.more(args)
	case "du":
		err = s.du(args)
	default:
		err = fmt.Errorf("unknown command; type help for available commands")
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (s *archiveShell) parseFlags(flags *pflag.FlagSet, args []string, usage string) error {
	flags.SetOutput(io.Discard)
	help := flags.Bool("help", false, "Show help")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *help {
		fmt.Fprintln(s.out, "Usage:", usage)
		flags.SetOutput(s.out)
		flags.PrintDefaults()
		return errShellHelp
	}
	return nil
}

type shellWord struct {
	text, pattern string
	glob          bool
	start, end    int // byte offsets in the input, including quotes and escapes
}

// Preserve quoting in the glob pattern so "*.txt" refers to a literal filename.
func splitShellWords(line string) ([]shellWord, error) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	return parseShellWords(line, false)
}

// Completion also parses partial words, including unfinished quotes/escapes.
func parseShellWords(line string, partial bool) ([]shellWord, error) {
	var words []shellWord
	var word shellWord
	var quote rune
	started, escaped := false, false
	appendRune := func(r rune, literal bool) {
		word.text += string(r)
		if literal && strings.ContainsRune(`*?[]\`, r) {
			word.pattern += `\`
		}
		word.pattern += string(r)
		if !literal && strings.ContainsRune("*?[", r) {
			word.glob = true
		}
	}
	for offset, r := range line {
		if !started && !unicode.IsSpace(r) {
			word.start = offset
		}
		switch {
		case escaped:
			// In double quotes, backslash is literal except before shell escapes.
			if quote == '"' && !strings.ContainsRune("\\\"$`", r) {
				appendRune('\\', true)
			}
			appendRune(r, true)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				appendRune(r, true)
			}
		case r == '\'' || r == '"':
			quote, started = r, true
		case unicode.IsSpace(r):
			if started {
				word.end = offset
				words = append(words, word)
				word, started = shellWord{}, false
			}
		default:
			started = true
			appendRune(r, false)
		}
	}
	if !partial && (escaped || quote != 0) {
		return nil, fmt.Errorf("unfinished quote or escape")
	}
	if started {
		word.end = len(line)
		words = append(words, word)
	}
	return words, nil
}

func (s *archiveShell) expand(word shellWord) []string {
	if !word.glob {
		return []string{word.text}
	}
	candidates := []string{"."}
	if path.IsAbs(word.pattern) {
		candidates[0] = "/"
	}
	join := func(prefix, component string) string {
		if prefix == "." {
			return component
		}
		return strings.TrimSuffix(prefix, "/") + "/" + component
	}
	for _, part := range strings.Split(word.pattern, "/") {
		if part == "" {
			continue
		}
		var next []string
		for _, candidate := range candidates {
			n, err := s.resolve(candidate, true)
			if err != nil || !n.isDir() {
				continue
			}
			if part == "." || part == ".." {
				next = append(next, join(candidate, part))
				continue
			}
			for _, child := range n.children {
				base := path.Base(child.path)
				if strings.HasPrefix(base, ".") && !strings.HasPrefix(part, ".") {
					continue
				}
				if match, _ := path.Match(part, base); match {
					// Keep .. in the result: cleaning it here would change paths
					// that traverse symbolic links before expanding the next part.
					next = append(next, join(candidate, base))
				}
			}
		}
		candidates = next
	}
	if strings.HasSuffix(word.pattern, "/") {
		var directories []string
		for _, candidate := range candidates {
			if n, err := s.resolve(candidate, true); err == nil && n.isDir() {
				directories = append(directories, candidate+"/")
			}
		}
		candidates = directories
	}
	if len(candidates) == 0 {
		return []string{word.text}
	}
	return candidates
}
