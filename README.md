# erwtensoep

Build with `make build` and run tests with `make test`.

## Archive statistics

```sh
erwtensoep --archive-dir /path/to/archive stats
```

`stats` shows how sharing and compression reduce the storage needed for **all
saved regular-file versions**, including older versions:

1. **Logical file data** counts every file version's original size separately.
2. **After whole-file sharing** counts each distinct file content (blob) once.
   Identical files and unchanged contents across versions share a blob.
3. **After block sharing** counts each referenced block once. Different file
   contents can share blocks, and a single file can contain repeated blocks.
4. **Stored blocks (gzip)** measures the actual compressed block object sizes,
   including gzip headers and trailers. Local archives use file sizes without
   reading or decompressing the block contents.

Savings are shown in bytes and percentages with the baseline named on each line.
Whole-file and additional block savings combine into the total sharing subtotal;
compression applies to the unique blocks that remain. Overall savings compares
stored blocks with logical data. Small or incompressible blocks may expand under
gzip; this is reported as an increase. Empty archives have no size ratio.

Block reuse counts include every occurrence in every saved file version.
The report separately counts blocks shared between **distinct file paths**;
repeated blocks or multiple versions of a single path alone do not qualify.
Symlinks contribute to version counts but not file-data sizes. Metadata,
superblock backups, unreferenced objects, and filesystem allocation overhead
are excluded from the storage breakdown, so it is not a total disk-usage report.
Unreferenced blobs and blocks present in the index are counted separately.

## Browsing an archive

```sh
erwtensoep --archive-dir /path/to/archive cli
```

The interactive prompt is `erwtensoep:<pwd>/`, starting at `erwtensoep:/`.
The shell shows the latest version of each archived path and infers directories
from those paths. Empty directories and original directory metadata are not
stored. Browsing does not modify the archive or extract files onto disk.

In a terminal, **Tab** completes command names and archive paths. A unique file
or command adds a trailing space; a directory adds `/` so you can continue into
it. For multiple matches, Tab extends their shared prefix or lists the choices.
Completion handles quoted/escaped names, relative and absolute paths, and
symlinked directories. `cd` offers directories only; hidden names require a dot.

**Up/Down** browse the last 100 commands across CLI sessions. Down past the
newest command restores the line you were typing. Recalled commands are editable
with Left/Right, Backspace, and the usual terminal editing keys. Blank lines and
consecutive duplicates are omitted. Interactive commands are saved as they are
entered in `~/.erwtensoep_history`, shared across archives, with owner-only file
permissions. The file contains a JSON array of commands, oldest first. New
commands from overlapping sessions are merged when saving; the file is replaced
atomically and limited to 100 entries. A sibling `.erwtensoep_history.lock` file
coordinates writes. Delete the history file while CLI sessions are closed to
clear saved history.

History-file errors produce a warning while in-memory history remains usable;
pending commands are retried on the next command and on exit. Piped input uses
plain command lines without interactive editing and is not recorded in history.

```text
erwtensoep:/ ls -lah
erwtensoep:/ cd "my documents"
erwtensoep:/my documents/ ls -R
erwtensoep:/my documents/ cat -n notes.txt
erwtensoep:/my documents/ more -n 20 notes.txt
erwtensoep:/my documents/ du -sh .
erwtensoep:/my documents/ cd -
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
