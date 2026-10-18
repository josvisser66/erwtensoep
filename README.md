# erwtensoep

Build with `make build` and run tests with `make test`.

## Browsing an archive

```sh
erwtensoep --archive-dir /path/to/archive cli
```

The interactive prompt is `erwtensoep:<pwd>`, starting at `erwtensoep:/`.
The shell shows the latest version of each archived path and infers directories
from those paths. Empty directories and original directory metadata are not
stored. Browsing does not modify the archive or extract files onto disk.

In a terminal, **Tab** completes command names and archive paths. A unique file
or command adds a trailing space; a directory adds `/` so you can continue into
it. For multiple matches, Tab extends their shared prefix or lists the choices.
Completion handles quoted/escaped names, relative and absolute paths, and
symlinked directories. `cd` offers directories only; hidden names require a dot.

**Up/Down** browse the last 100 commands in the current session. Down past the
newest command restores the line you were typing. Recalled commands are editable
with Left/Right, Backspace, and the usual terminal editing keys. Blank lines and
consecutive duplicates are omitted. History is kept in memory for this session.
Piped input continues to use plain command lines without interactive editing.

```text
erwtensoep:/ ls -lah
erwtensoep:/ cd "my documents"
erwtensoep:/my documents ls -R
erwtensoep:/my documents cat -n notes.txt
erwtensoep:/my documents more -n 20 notes.txt
erwtensoep:/my documents du -sh .
erwtensoep:/my documents cd -
erwtensoep:/ exit
```

| Command | Supported options |
| --- | --- |
| `cd [DIR]` | No argument goes to `/`; `-` returns to the previous directory; `-L` keeps logical paths (default), `-P` resolves symlinks. |
| `pwd` | `-L` prints the logical path (default); `-P` prints the resolved path. |
| `ls [PATH...]` | `-a` includes hidden entries and `.`/`..`; `-A` includes only hidden entries; `-l` shows metadata; `-h` formats sizes; `-R` recurses; `-d` lists directories themselves; `-F` marks entry types; `-t` sorts newest first; `-S` sorts largest first; `-r` reverses sorting; `-1` lists one entry per line (default). |
| `cat FILE...` | `-n` numbers lines; `-b` numbers nonempty lines; `-s` squeezes repeated empty lines; `-E` marks line endings; `-T` shows tabs. |
| `more FILE...` | `-n N` or `-N` sets page length; `+N` starts at line N; `-s` squeezes repeated empty lines. Space advances a page, Enter advances a line, and `q` stops. Input or output redirected away from a terminal disables paging. |
| `du [PATH...]` | `-s` summarizes; `-a` includes individual files; `-h` formats sizes; `-b` uses bytes; `-k` uses KiB (default); `-m` uses MiB; `-c` prints a grand total; `-d N` / `--max-depth N` limits displayed depth. |

Use `help` or `help COMMAND` for details and long option names. Short flags can
be combined, and `--` ends options for filenames beginning with `-`. `exit`,
`quit`, or EOF leaves the shell. Command errors return to the prompt.

Relative paths, absolute paths, `.` and `..` all refer to the archive. Symbolic
links are resolved inside the archive, including links with absolute targets.
Quotes and backslash escapes support names containing spaces. Unquoted `*`, `?`,
and `[...]` expand against archive entries; hidden names require a leading dot.
Pipes, redirection, variable expansion, and host shell commands are not supported.

`du` reports the logical, uncompressed size of the latest files, including hidden
files. It counts each archived pathname separately even when contents are shared,
counts symlink metadata without following the target, and assigns zero bytes to
inferred directories. Numeric units round up after summing a directory's bytes.
It does not measure the archive's compressed storage size.

The usual `--archive-dir` / `-a`, `--storage-backend`, and `~/.erwtensoep.json`
configuration apply. A `cli` section can provide command-specific configuration.
