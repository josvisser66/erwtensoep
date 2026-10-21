# erwtensoep — a homegrown backup tool

Build with `make build` and run tests with `make test`.

## Manuals

- [User manual](docs/user-manual.md): setup, common tasks, configuration, every
  subcommand, and the interactive shell.
- [Technical manual](docs/technical-manual.md): repository structure, archive
  format, implementation, storage, integrity checking, and development.

## S3-compatible storage

An `s3://bucket/prefix` archive selects S3 storage automatically. Set
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` in the environment, then provide
the provider's service endpoint and signing region:

```sh
erwtensoep -a s3://your-bucket/erwtensoep/work \
  --endpoint-url https://s3.us-east-1.amazonaws.com --region us-east-1 \
  merge /path/to/source
```

The endpoint is required, including for AWS. `--endpoint_url` is an alias;
the JSON configuration key is `endpoint_url`. AWS, IDrive e2, Backblaze B2,
Wasabi, and other compatible services use the same backend with their own
endpoint and credentials. Buckets must already exist. All archive commands
work with the S3 location, including `cli`, `stats`, and `fsck`.

See [S3 setup and provider examples](docs/user-manual.md#use-s3-compatible-storage)
and the [sample S3 configuration](erwtensoep-s3.json). Credentials are not stored
in the configuration or archive.

## Merging tar archives

Pass one or more `.tar`, `.tar.gz`, or `.tgz` files as merge sources to import
their contents. You can mix tar sources and directories in the same command:

```sh
erwtensoep -a /path/to/archive merge backup.tar another.tar.gz third.tgz /path/to/source
```

For every explicitly named tar source, erwtensoep prints a warning on stderr:

```text
WARNING: merging files from tar archive "backup.tar"; the tar file itself will not be archived.
```

Members retain their paths inside the tar; the tar filename is not prepended.
Nothing is extracted onto the host filesystem. File contents use the normal
block sharing, compression, and saved-version rules. Modes, modification times,
and owner/group metadata come from tar headers. Symlinks retain their targets;
hard links become regular-file entries sharing their target's data.

Size limits, `--dry-run`, and regular `.erwtensoep` exclusion files inside the tar
are honored. The complete input is checked first, then read again for import,
so exclusions work regardless of member order and damaged tar/gzip input is
reported before importing that source. Unsafe member paths are rejected.
Empty directories are not stored; unsupported special members are skipped with
a warning. Only explicitly named tar sources are expanded: a tar file inside
a source directory, or inside another tar, is stored as a regular file. Both
local and S3 archive destinations support tar imports. See the
[tar import task](docs/user-manual.md#import-files-from-tar-archives) for details.

## Checking and repairing an archive

```sh
erwtensoep --archive-dir /path/to/archive fsck       # Report, then ask to repair
erwtensoep --archive-dir /path/to/archive fsck -n    # Check only; never write
erwtensoep --archive-dir /path/to/archive fsck -nv   # Check only, with progress
erwtensoep --archive-dir /path/to/archive fsck -y    # Report and apply all repairs
```

`fsck` reads the superblock and independently scans stored entries, blobs, and
blocks. It fully decompresses objects to check gzip checksums and stored lengths,
validates metadata and content hashes, and checks every file-to-blob and
blob-to-block reference. It can inspect an archive whose superblock is missing,
corrupt, or too inconsistent for ordinary commands to load. Checking large
archives reads all block data; keep the archive idle throughout the check and
repair. Add `-v` (or `--verbose`) to show scan phases, completed/total object
counts, and percentage done per phase on stderr, with progress updates about
every two seconds. The initial directory scan shows `counting...` until totals
are known. The discrepancy report and repair prompt are shown regardless of
verbosity.

The report lists discrepancies and the proposed action for each. Unless `-y` is
given, repairs require a `y` or `yes` response; declining or reaching EOF makes
no changes. `-n` and `-y` cannot be combined. The command exits nonzero when
discrepancies remain unrepaired or checking/repairing fails.

Repairs use valid stored objects to rebuild the complete index:

- Corrupt entries, blobs, and blocks are removed. A blob with a missing or
  corrupt block is removed together with entries referring to it. Entries with
  missing blobs are removed too. Other versions and shared blocks remain when
  they are still valid and referenced.
- Valid entries missing from the superblock are added. A missing on-disk entry
  record is recreated from valid superblock metadata when its blob is intact;
  corrupt on-disk entry records are discarded instead.
- Blobs without surviving entries become files named `lost+found/<blob-hash>`.
  Recovery entries use mode `0600`, unknown owner/group, and the recovery time.
  A numeric suffix avoids overwriting an existing name. If `lost+found` itself
  is an archived file or symlink, the checker reports that conflict and refuses
  repair until it is resolved.
- Blocks unused by any surviving blob, invalid objects in the storage layout,
  and empty storage directories are removed. Filesystem symlinks in storage
  directories are removed as links, never followed.

Before repair, the previous index is saved as `superblock.json.gz.fsck.bak`.
The repaired superblock is replaced atomically before unusable object files are
deleted. I/O or permission errors abort checking rather than treating unreadable
objects as corrupt. Repair failures are reported, and a changed superblock
invalidates a pending repair plan.

Ordinary archive deletion also prunes empty parent directories recursively,
including empty `blocks`, `blobs`, and `entries` folders. The archive root is
preserved, and storage folders are recreated automatically when needed.

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
Directory listings show the latest version of each archived path and infer
directories from those paths. Empty directories and original directory metadata are not
stored. Browsing does not modify the archive or extract files onto disk.
Use `versions FILE` to list every saved version of a file in the current directory.
`ls` appends `(N versions)` to files and symlinks with more than one saved
version, for example `notes.txt (3 versions)`, including in long listings.
The `rm` command can remove individual saved versions or all versions of a path.

If a file and directory share a name, the CLI, top-level `rm`, and `restore`
treat `a` as the file and `a/` as the directory; `cd a` enters the directory. Listings and Tab
completion show both. `rm -r a/` leaves the file `a` intact. Restore rejects
selections containing both a file and its descendants before writing output.
See the manual's [detailed rules and examples](docs/user-manual.md#files-and-directories-with-the-same-name).

`cat`, `more`, `du`, and `ls FILE` display the numbered version list when a
file has multiple versions and ask which one to use. Choose one number, press
Enter to skip that file, or enter `q` to cancel. Versions are ordered by saved
modification time, newest first. `du` asks about files inside directories too;
skipped files are excluded from its totals. `--latest` uses the newest versions
without asking, for example `cat --latest notes.txt` or `du --latest -sh .`.
Directory listings remain an overview of the newest versions with version counts.

Top-level `restore` uses the same chooser for each matching file or symlink;
`restore --latest -o DESTINATION PATTERN...` restores the newest versions without
asking. Top-level `ls FILE` also offers a choice, while no-argument, directory,
and pattern listings show the newest versions. Single-version files need no
selection. Choices apply only to the current command and are collected before
printing file contents or restoring files. `q` or EOF without an answer cancels
the operation; no version is silently chosen on EOF. Selection answers are not
saved in command history. `stats`, `verify`, `fsck`, and `rebuild` continue to
process the entire archive, including every saved version.

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
erwtensoep:/my documents/ versions notes.txt
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
| `ls [PATH...]` | `--latest` skips version selection for file operands; `-a` includes hidden entries and `.`/`..`; `-A` includes only hidden entries; `-l` shows metadata; `-h` formats sizes; `-R` recurses; `-d` lists directories themselves; `-F` marks entry types; `-t` sorts newest first; `-S` sorts largest first; `-r` reverses sorting; `-1` lists one entry per line (default). |
| `versions FILE` | List all saved versions, newest modification time first, with entry ID, time, size, permissions, and path (including symlink targets). `-h` formats sizes; `-r` shows oldest first. |
| `cat FILE...` | `--latest` reads the newest versions without asking; `-n` numbers lines; `-b` numbers nonempty lines; `-s` squeezes repeated empty lines; `-E` marks line endings; `-T` shows tabs. |
| `more FILE...` | `--latest` reads the newest versions without asking; `-n N` or `-N` sets page length; `+N` starts at line N; `-s` squeezes repeated empty lines. Space advances a page, Enter advances a line, and `q` stops. Input or output redirected away from a terminal disables paging. |
| `du [PATH...]` | `--latest` counts the newest versions without asking; `-s` summarizes; `-a` includes individual files; `-h` formats sizes; `-b` uses bytes; `-k` uses KiB (default); `-m` uses MiB; `-c` prints a grand total; `-d N` / `--max-depth N` limits displayed depth. |
| `rm PATH...` | Choose from a numbered list for files with multiple versions. `--all-versions` selects all; `-r` / `-R` / `--recursive` includes directory contents; `-f` / `--force` ignores missing paths and removes all versions without prompts; `-i` / `--interactive` also confirms single-version or `--all-versions` removals; `-v` / `--verbose` lists removals. The last `-f` or `-i` wins. |

Use `help` or `help COMMAND` for details and long option names. Short flags can
be combined, and `--` ends options for filenames beginning with `-`. `exit`,
`quit`, or EOF leaves the shell. Command errors return to the prompt.

Relative paths, absolute paths, `.` and `..` all refer to the archive. Symbolic
links are resolved inside the archive, including links with absolute targets.
Quotes and backslash escapes support names containing spaces. Unquoted `*`, `?`,
and `[...]` expand against archive entries; hidden names require a leading dot.
Pipes, redirection, variable expansion, and host shell commands are not supported.

`du` reports the logical, uncompressed size of the selected file versions,
including hidden files. It counts each archived pathname separately even when contents are shared,
counts symlink metadata without following the target, and assigns zero bytes to
inferred directories. Numeric units round up after summing a directory's bytes.
It does not measure the archive's compressed storage size.

For paths with multiple saved versions, `rm` displays a numbered table with
version IDs, modification times, sizes, and permissions, newest first. Enter
numbers such as `1,3`, a range such as `2-4`, or `all`. Enter alone skips the
path; `q` or EOF without an answer cancels the command before any deletions.
Choices are collected for all paths before the archive is changed.

`rm --all-versions PATH...` selects every version without the version chooser.
`rm -f PATH...` also removes every version and never prompts. Single-version
paths are removed immediately unless `-i` is specified. With `-i`, selecting
versions is itself confirmation; single-version paths and `--all-versions`
removals get a yes/no prompt. The top-level `erwtensoep rm` uses the same version
chooser, then asks for final confirmation of the selection; it also accepts
`--all-versions`. Top-level `rm a` selects only the file when it exists;
`rm a/` selects descendants while preserving that file, without needing `-r`.
Use `rm a a/` to select both. If there is no exact file or symlink, a bare name
selects directory contents. See the manual's
[top-level removal examples](docs/user-manual.md#removing-a-collision-with-top-level-rm).

Interactive `rm` accepts relative or
absolute archive paths, quotes, globs, and `--` for names beginning with `-`.
Directories require `-r` or `-R`, which includes hidden descendants. Symlinks are
removed themselves; recursive deletion does not follow symlinks within a
directory. Intermediate symlinks in a path such as `link/file.txt` resolve inside
the archive. A final symlink with a trailing slash is rejected; omit the slash
to remove the link. Tab completion for `rm` leaves symlinks without a slash.

The archive root and operands ending in `.` or `..` cannot be removed, even with
`-f`. Shared contents and blocks remain stored while another saved file needs
them. Listings and completion refresh after deletion; if the current inferred
directory disappears, the prompt moves to its nearest surviving ancestor.
If the newest version is removed, directory browsing and `--latest` operations
use the newest remaining version. Other file operations still offer a choice
when multiple versions remain. A path disappears only when its last saved
version is removed.
Confirmation answers are not added to command history. Source files on the host
filesystem are unaffected.

The usual `--archive-dir` / `-a`, `--storage-backend`, and `~/.erwtensoep.json`
configuration apply. A `cli` section can provide command-specific configuration.
