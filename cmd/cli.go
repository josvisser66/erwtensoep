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
	Long:  "Browse archived files using cd, pwd, ls, cat, more, and du.\nFile commands offer version selection; --latest uses the newest without prompting.\nDirectory listings show the newest versions; versions FILE lists every version.\nUse rm to choose versions to remove, or rm --all-versions to remove them all.\nPaths and symbolic links are resolved inside the archive. Type help for commands.",
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
	history     *commandHistory
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
	if s.terminal != nil {
		if s.history == nil {
			s.loadHistory(defaultHistoryPath)
		}
		defer s.saveHistory()
	}
	for {
		line, readErr := s.readCommand()
		// History is recorded by the editor, but saved after raw mode has been
		// restored, so any filesystem warning can be printed normally.
		s.saveHistory()
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
  ls [-alhARdFtrS1] [--latest] [PATH...]  List files and directories
  versions [-hr] FILE     List all saved versions, newest first
  cat [-nbsET] [--latest] FILE...  Print file contents
  more [-s] [-n LINES] [+LINE] [--latest] FILE...  Page through file contents
  du [-ahskbmc] [-d DEPTH] [--latest] [PATH...]  Show uncompressed sizes
  rm [-rRfiv] [--all-versions] PATH...  Choose saved versions to remove
  help [COMMAND]         Show help
  exit, quit             Leave the shell (EOF also exits)

Tab completes commands and archive paths, extending a shared prefix or listing
matches. Up/Down recall the last 100 commands across sessions and restore the
unfinished line when returning past the newest command. Interactive history is
saved in ~/.erwtensoep_history; piped commands are not recorded.

Paths are archive-relative; / is the archive root. Quotes, backslash escapes,
and unquoted *, ?, [...] globs are supported. Pipes, redirects, variables,
and host commands are not interpreted. Directory browsing shows newest versions.
cat, more, du, and ls with file operands offer a numbered version choice when
several versions are saved. Choose one number, Enter to skip, or q to cancel;
--latest skips the chooser. Choices apply only to that command. du also asks
for files within directories; skipped files contribute nothing to the total.
versions FILE lists every saved version of that path, including symlinks.
ls appends (N versions) to files and symlinks with multiple saved versions.
If a file and directory share a name, a selects the file and a/ selects the
directory. cd a enters the directory. ls and Tab show both a and a/.
rm a removes file versions; rm -r a/ removes descendants, leaving a intact.
Directories are inferred; empty directories and directory metadata are absent.
du counts logical file bytes, including hidden files, without deduplicating.
more uses Space for a page, Enter for a line, and q to stop; redirected output
is printed without paging. rm lists numbered versions when several are saved;
enter numbers/ranges, all, Enter to skip, or q to cancel the command.
--all-versions skips version selection; -i also confirms single-version/all-version
removals. -r/-R includes directory contents; -f skips missing paths, removes all
versions without prompts; -v lists removals. Other commands leave the
archive unchanged. Shared file data is retained while another entry needs it.
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
	case "versions":
		err = s.versions(args)
	case "cat":
		err = s.cat(args)
	case "more":
		err = s.more(args)
	case "du":
		err = s.du(args)
	case "rm":
		err = s.rm(args)
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
		seen := make(map[string]bool)
		add := func(name string) {
			if !seen[name] {
				next = append(next, name)
				seen[name] = true
			}
		}
		for _, candidate := range candidates {
			n, err := s.resolveDirectory(candidate)
			if err != nil || !n.isDir() {
				continue
			}
			if part == "." || part == ".." {
				add(join(candidate, part))
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
					name := join(candidate, base)
					if child.isDir() && !s.nodes[child.path].isDir() {
						name = directoryKey(name)
					}
					add(name)
				}
			}
		}
		candidates = next
	}
	if strings.HasSuffix(word.pattern, "/") {
		var directories []string
		seen := make(map[string]bool)
		for _, candidate := range candidates {
			name := directoryKey(candidate)
			if n, err := s.resolveDirectory(candidate); err == nil && n.isDir() && !seen[name] {
				directories = append(directories, name)
				seen[name] = true
			}
		}
		candidates = directories
	}
	if len(candidates) == 0 {
		return []string{word.text}
	}
	return candidates
}
