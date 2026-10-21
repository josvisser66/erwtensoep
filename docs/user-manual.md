# erwtensoep user manual

[README](../README.md) · [Technical manual](technical-manual.md)

erwtensoep stores files in a local directory or S3-compatible object storage.
It keeps saved versions,
shares identical file contents and data blocks, and compresses stored objects
with gzip. You can merge source directories or tar contents, browse archived files, restore
them, remove paths, and check or repair the archive.

## Contents

- [Build and run](#build-and-run)
- [Understand archive paths and versions](#understand-archive-paths-and-versions)
- [Files and directories with the same name](#files-and-directories-with-the-same-name)
- [Common tasks](#common-tasks)
- [Configuration](#configuration)
- [Subcommand reference](#subcommand-reference)
- [Interactive command reference](#interactive-command-reference)
- [Troubleshooting](#troubleshooting)

## Build and run

From the repository, with a Go toolchain compatible with [go.mod](../go.mod):

```sh
make build
./bin/erwtensoep --help
```

Without `make`, use `go build -o bin/erwtensoep ./cmd`. The module declares Go
1.23.2. The implementation uses Unix filesystem and terminal facilities; macOS
and Linux are the intended environments.

The examples below use `erwtensoep`, assuming the binary is on your `PATH`.
Otherwise replace it with `./bin/erwtensoep` from the repository directory.

The general command form is:

```text
erwtensoep --archive-dir ARCHIVE SUBCOMMAND [OPTIONS] [ARGUMENTS...]
```

| Global option | Meaning |
| --- | --- |
| `-a`, `--archive-dir LOCATION` | Local directory or `s3://bucket/prefix`. Required for archive operations unless configured. |
| `--storage-backend NAME` | `auto` by default: local paths use local storage and S3 URLs use S3. Also accepts `local`, `s3`, and temporary `memory`. An S3 URL selects S3 even with the older `local` setting; `memory` cannot be combined with an S3 URL. |
| `--endpoint-url URL`, `--endpoint_url URL` | S3 provider's service endpoint. Required for S3, including AWS. |
| `--region REGION` | S3 signing region. Defaults to `AWS_REGION`, then `AWS_DEFAULT_REGION`, then `us-east-1`. |
| `-h`, `--help` | Help for the selected command. |

Use `erwtensoep help COMMAND` or `erwtensoep COMMAND --help` for built-in help.
Inside `cli`, use `help COMMAND` or `COMMAND --help`; there, `-h` can mean
human-readable sizes.

## Understand archive paths and versions

The archive location contains erwtensoep's storage objects. Its physical
directory layout or S3 object keys are separate from the file paths displayed
when browsing.

Merging `/home/alice/work` stores `/home/alice/work/documents/notes.txt` as
`documents/notes.txt`. The source directory's own name is not prepended. Merging
several source directories places their contents in the same archive namespace;
matching relative paths become versions of the same path. To retain a parent
directory name, merge its parent, or use a separate archive for each source.

Explicit `.tar`, `.tar.gz`, and `.tgz` sources contribute their member paths,
also without a source filename prefix. A member named `./documents/notes.txt`
becomes `documents/notes.txt`. Sources share the same archive namespace, so
matching paths from different tar files can add versions of the same file.

Regular files and symbolic links are stored. Directories are inferred from file
paths, so empty directories and original directory metadata are absent. Sockets,
pipes, and device files are skipped. Hard links are stored as file entries and
may share data, but are not restored as hard links.

Merge adds versions; it does not mirror deletions from the source. Removing a
source file does not remove it from the archive. Files whose identifying
metadata has not changed are skipped without rereading their contents. In
particular, changing content while preserving size and modification time can
make a change go undetected.

Directory browsing shows the version with the greatest saved modification
time, not the time of the merge. `cat`, `more`, `du`, `restore`, and `ls` with
explicit file operands offer a numbered list when a file has multiple versions.
Choose one number, Enter to skip the file, or `q` to cancel the command. Versions
are sorted newest first, with equal times ordered by version ID descending.
Use `--latest` on these commands to use the newest versions without prompting.
Single-version files need no selection. Choices last only for that command;
`versions FILE` displays all saved metadata without making a choice.

All choices are collected before printing file contents or restoring files.
Cancellation or EOF without an answer abandons the whole operation. Invalid
choices prompt again. Selection answers are not stored in command history.
`rm` additionally accepts multiple versions or `all`. Directory listings and
top-level pattern listings remain overviews of newest versions. Inside the CLI,
globs expand to individual operands, so `ls *.txt` offers choices for matching
files. Archive-wide `stats`, `verify`,
`fsck`, and `rebuild` include every saved version and do not ask for selections.

Keep an archive idle while checking or repairing it, and use one modifying
process at a time. The archive does not have a lock coordinating separate
merge, removal, repair, and CLI processes.

## Files and directories with the same name

**In the interactive CLI, top-level `rm`, and `restore`, `a` selects the file;
`a/` selects only the directory and its contents.** A bare name falls back to a directory
only when there is no file or symlink at that exact path. `cd a` is the exception:
`cd` always requests a directory, so it enters `a/` even when the file exists.

This situation is valid in an archive. One backup might contain a file named
`a`, while another contains `a/child.txt`. Since merges retain older entries,
both remain accessible. It can also happen when merging different source
directories or tar archives into the same archive. The directory is inferred
from `a/child.txt`; it is not another saved version of the file `a`.

### How to select the object you want

Suppose the archive contains `a` with two saved versions and `a/child.txt`.
A CLI listing of their parent displays both, even without `ls -F`:

```text
a (2 versions)
a/
```

| Interactive command | What it selects |
| --- | --- |
| `ls a` | The file `a`; asks which file version to display. |
| `ls a/` | The directory's contents, such as `child.txt`. |
| `ls -d a/` | The directory itself. |
| `cat a` or `more a` | A saved version of the file `a`. |
| `cat a/` or `more a/` | An error: the operand is a directory. |
| `versions a` | All saved versions of the file `a`. |
| `versions a/` | An error: directories have no saved file versions of their own. |
| `du a` | The size of one selected version of the file `a`. |
| `du a/` | The sum of selected file sizes below `a/`, excluding the file `a`. |
| `cd a` or `cd a/` | Enter the directory `a/`. |
| `rm a` | Remove selected versions of the file `a`; keep `a/child.txt`. |
| `rm -r a` | Also selects only the file `a`; `-r` does not change name resolution. |
| `rm -r a/` | Remove selected versions below `a/`; keep every version of the file `a`. |

**The trailing slash selects the object before any version question is asked.**
For example, `cat a/` never asks about versions of the file `a`, and
`rm -r a/` never offers those versions for deletion. `--latest`,
`--all-versions`, and `-f` do not change which object the name selects.

These rules apply at every level: `parent/a` selects the file, `parent/a/`
selects the directory, and `parent/a/child.txt` traverses the directory. Relative
and absolute CLI paths follow the same rules. `.` and `..` refer to directories.
Removing the last descendant makes the inferred directory disappear, while a
same-named file can remain. Removing the last file version leaves a same-named
directory accessible, and the bare name then falls back to that directory.

Tab completion offers both `a` and `a/`; `cd` completion offers the directory.
CLI globs also preserve the distinction: `ls a*` can expand to both `a` and
`a/`, while `ls a*/` selects directories only. Consequently, `rm -r a*` can
select both the file and the directory. Use explicit operands when you intend
to select only one. Colliding directories carry `/` in listings and `du` output;
`ls -F` marks all directories, including those without a naming collision.

A saved symlink counts as a file entry for these rules. Commands that normally
follow symlinks still follow the selected link. When a symlink `a` and an
inferred directory `a/` coexist, `a/` chooses that inferred directory.
`rm a` removes the symlink itself; `rm -r a/` removes the inferred directory's
descendants. In the interactive CLI, when no inferred directory exists at the
link's name, `rm a/` rejects the trailing slash rather than deleting through
the final symlink. Top-level `rm a/` instead reports no matching entries in
that case. Neither removal command follows the final symlink to delete its target.

### Restoring a collision

Choose one object per destination:

```sh
# Restore the file as restored-file/a.
erwtensoep -a "$HOME/archives/work" restore -o "$HOME/restored-file" a

# Restore descendants such as restored-directory/a/child.txt.
erwtensoep -a "$HOME/archives/work" restore -o "$HOME/restored-directory" a/
```

The same version chooser applies after resolving the requested object;
`--latest` skips that chooser without changing the file/directory rule.
An ordinary output filesystem cannot hold both a file `a` and a directory `a`
at the same location. If the selection contains both, restore reports a
`restore path conflict` **before asking for versions or creating, replacing,
or deleting any output files**. This also applies to symlinks and to nested
collisions found while selecting a larger directory.

For example, these selections conflict when the named entries exist:

```sh
erwtensoep -a ARCHIVE restore -o OUTPUT a a/
erwtensoep -a ARCHIVE restore -o OUTPUT parent/
erwtensoep -a ARCHIVE restore -o OUTPUT 'parent/*' 'parent/a/*'
```

The second and third examples conflict if both `parent/a` and
`parent/a/child.txt` are selected. A trailing slash excludes the file at the
operand's own path; it does not discard conflicting files deeper in the tree.
Narrow the selection, or restore the file and directory into separate output
directories as shown above. Repeating the same file operand is harmless.
`--latest` does not override a conflict. Do not restore both objects into the
same destination in separate runs: restoring the file can replace an existing
directory there. Top-level restore globs match whole archive paths; `*` does
not cross `/`, and globs should be quoted in your host shell.

### Removing a collision with top-level `rm`

Top-level `erwtensoep rm` follows the same file/directory selection rules:

```sh
# Select only versions of the file a; keep all entries below a/.
erwtensoep -a ARCHIVE rm a

# Select only versions below a/; keep every version of the file a.
erwtensoep -a ARCHIVE rm a/

# Explicitly select both the file and all descendants.
erwtensoep -a ARCHIVE rm a a/
```

No `-r` flag is needed or accepted by top-level `rm`. Each command offers
version selection for the selected files, prints a removal summary, and then
asks `Proceed with removal? [y/N]:`. `--all-versions` skips the version chooser
but still requires final confirmation and does not broaden the path selection.
In particular, `rm --all-versions a` preserves all descendants while a file or
symlink named `a` exists.

A bare name selects descendants only if no file or symlink has that exact name.
This means that after removing the last version of the file `a`, a later
`rm a` selects the remaining directory contents. Repeating an operand within
one command does not change its selection or prompt twice.

Top-level globs match whole archive-relative paths, and `*` does not cross `/`.
Quote them in your host shell. For example, `rm 'a*'` can select the file `a`
but does not select `a/child.txt`; `rm 'a/*'` selects matching direct children.
Use `a/` for all descendants, including hidden files and deeper paths.
A larger directory operand includes all entries below it, including nested
file/directory collisions. Unlike restore, removal allows a file and its
descendants to be selected together. Symlink entries are selected as files;
top-level pattern matching never follows their targets.

## Common tasks

### Create an archive and add files

Suppose your source contains `documents/`, `photos/`, and `README.txt`:

```sh
mkdir -p "$HOME/archives/work"
erwtensoep -a "$HOME/archives/work" merge "$HOME/backup-source"
erwtensoep -a "$HOME/archives/work" ls
```

Keep local archives outside the source tree. If the archive directory does not
exist, ordinary archive commands offer to create it; answer `y` or `yes`.
Creating it in advance avoids that prompt. `fsck` requires an existing directory
and never creates one.

Run the same merge again to add changed or new files:

```sh
erwtensoep -a "$HOME/archives/work" merge "$HOME/backup-source"
```

The result reports changes and processing errors. Review those counts: some
per-file failures are reported in the summary without making the process exit
nonzero. Permission-denied source directories may also be skipped.

### Use S3-compatible storage

Create a bucket with your provider first, then choose a dedicated archive
prefix, such as `s3://my-backups/erwtensoep/work`. A prefix does not need to
exist in advance; erwtensoep creates objects as it writes the archive. A trailing
slash is optional. `s3://my-backups` uses the bucket root, but separate prefixes
make it easier to keep archives apart. Empty internal path components and `.`
or `..` components are rejected rather than normalized to a different archive.

Set the credentials in the environment of the erwtensoep process:

```sh
export AWS_ACCESS_KEY_ID='YOUR_PROVIDER_ACCESS_KEY'
export AWS_SECRET_ACCESS_KEY='YOUR_PROVIDER_SECRET_KEY'
```

Use your provider's values, not the placeholders. Credentials must come from
these variables: erwtensoep does not read AWS profiles, credential files, or
instance-role metadata. `AWS_SESSION_TOKEN` is also supported for temporary
credentials. No credential flags or JSON credential settings are provided, and
credentials are not serialized into the archive. Unset an old session token
when switching to a provider that uses ordinary access/secret keys.

Pass the service endpoint separately from the archive location:

```sh
erwtensoep -a s3://my-backups/erwtensoep/work \
  --endpoint-url https://s3.us-east-1.amazonaws.com --region us-east-1 \
  merge "$HOME/backup-source"

erwtensoep -a s3://my-backups/erwtensoep/work \
  --endpoint-url https://s3.us-east-1.amazonaws.com --region us-east-1 cli

erwtensoep -a s3://my-backups/erwtensoep/work \
  --endpoint-url https://s3.us-east-1.amazonaws.com --region us-east-1 fsck -nv
```

`--endpoint_url` and `--endpoint-url` are equivalent. The endpoint must be an
HTTP(S) service URL, without a bucket, prefix, credentials, query, or fragment.
Use HTTPS for hosted services; HTTP is supported for local S3 servers. Endpoint
redirects are not followed, so use the endpoint for the bucket's actual region.
The backend uses Signature Version 4 and path-style bucket addressing.

Examples for specific regions are shown below. Obtain the endpoint and signing
region for **your bucket** from the provider; these are not universal endpoints.

| Provider | Example `--endpoint-url` | Example `--region` | Provider reference |
| --- | --- | --- | --- |
| AWS S3 | `https://s3.us-east-1.amazonaws.com` | `us-east-1` | [AWS service endpoints](https://docs.aws.amazon.com/general/latest/gr/s3.html) |
| IDrive e2 | `https://s3.us-east-1.idrivee2.com` | `us-east-1` | [IDrive S3 API and regional endpoints](https://www.idrive.com/s3-storage-e2/s3-compatible-api) |
| Backblaze B2 | `https://s3.us-west-004.backblazeb2.com` | `us-west-004` | [Backblaze endpoint and key setup](https://www.backblaze.com/docs/en/cloud-storage-get-started-with-a-backblaze-integration) |
| Wasabi | `https://s3.us-east-1.wasabisys.com` | `us-east-1` | [Wasabi regional service URLs](https://docs.wasabi.com/docs/service-urls-for-wasabis-storage-regions) |

For Backblaze, the application key ID is `AWS_ACCESS_KEY_ID` and the application
key is `AWS_SECRET_ACCESS_KEY`; use an S3-compatible application key, not the
master key. [Backblaze S3 authentication](https://www.backblaze.com/apidocs/introduction-to-the-s3-compatible-api)

The region can instead come from `AWS_REGION` or `AWS_DEFAULT_REGION`, in that
order. Explicit `--region` or configured `region` takes precedence. The final
fallback is `us-east-1`; it is not automatic discovery of your provider's region.

To avoid repeating connection options, merge settings like these into
`~/.erwtensoep.json` (also see [the sample file](../erwtensoep-s3.json)):

```json
{
  "archive-dir": "s3://my-backups/erwtensoep/work",
  "storage-backend": "auto",
  "endpoint_url": "https://s3.us-east-1.amazonaws.com",
  "region": "us-east-1"
}
```

Then `erwtensoep merge SOURCE`, `erwtensoep cli`, and other commands use that
archive. Restore's output directory and merge's source directories or tar files remain local
host paths. `--storage-backend s3` is optional and requires an S3 archive URL.
An older configuration with `"storage-backend": "local"` does not prevent an
explicit S3 URL from selecting S3.

The required S3 operations are `GetObject`, `PutObject`, `DeleteObject`,
`HeadObject`, and `ListObjectsV2`. Grant read/list permissions for checking and
browsing, plus write/delete permissions for modification and repair. The tool
does not create buckets, list all buckets, or manage provider policies. A
missing bucket, invalid credentials, and access-denied responses are errors;
they are not treated as corrupt data to delete.

All archive commands use the same data format in S3. Keys under the chosen
prefix retain the local archive's layout; for example, the index is
`erwtensoep/work/superblock.json.gz`. S3 has no real directories, so removing
objects leaves no empty directories to prune. `fsck` can remove invalid keys
and folder-marker objects under `blocks/`, `blobs/`, and `entries/`. Use a
dedicated archive prefix; arbitrary objects in these reserved categories may
be classified as invalid storage objects.

S3 usage differs from local disk usage:

- A full check downloads stored objects. `stats` measures compressed block
  sizes with HEAD requests; listing and other operations make API calls.
- `stats` describes current referenced block objects, not a provider billing
  total. Request traffic, metadata, and retained object versions are excluded.
- In a versioned bucket, deletion can create a delete marker while older
  physical versions remain. Reclaiming their space depends on provider version
  and lifecycle settings; erwtensoep does not delete individual S3 version IDs.
  [S3 DeleteObject behavior](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html)
- Use only one archive writer at a time. S3 does not add an
  archive lock or make a multi-object update transactional. Keep it idle during
  `fsck` and `rebuild` as with local storage.

### Import files from tar archives

Supply one or more local tar files as merge arguments. Supported filename
suffixes are `.tar`, `.tar.gz`, and `.tgz`, matched without case sensitivity.
No additional flag is required. Tar sources and directories can be mixed:

```sh
erwtensoep -a "$HOME/archives/work" merge backup.tar photos.tar.gz documents.tgz
erwtensoep -a "$HOME/archives/work" merge backup.tar "$HOME/current-files"
erwtensoep -a "$HOME/archives/work" merge --dry-run backup.tar.gz
```

For each explicit tar source, including in dry runs, stderr contains:

```text
WARNING: merging files from tar archive "backup.tar"; the tar file itself will not be archived.
```

Erwtensoep reads member contents directly into its archive storage; it does not
extract temporary files or create the member paths on your host. The tar's own
filename is not added to the saved path. Prefixes such as `./` are normalized,
while absolute member names and names containing a `..` component are rejected.
A directory whose name ends in `.tar` still behaves as a directory source.

Regular files retain their tar permissions and modification times. Owner/group
names come from the header; absent names become decimal UID/GID strings.
Symlink targets are stored literally and are never followed during import.
Hard links, including links whose targets appear later, become regular files
sharing the target's content. Their targets must resolve to a regular member
within the same tar; missing targets and cycles are errors. Hard-link identities
are not recreated when restoring. Empty directories, ACLs, and extended
attributes are not represented. Device files, FIFOs, and other unsupported
special members are skipped with a warning.

`--min-size` and `--max-size` apply to each member's logical file size, including
hard links, rather than the tar file's size. Regular `.erwtensoep` members apply
the same inherited basename exclusion rules as source directories, even when
they appear after the files they exclude. These control files are themselves
eligible for import unless excluded or outside the size limits. If a directory
contains several `.erwtensoep` members, the last one's rules apply to the whole
tar source. Symlink and hard-link `.erwtensoep` members are not read as rules.
An included hard link may still import data from a target excluded by name.

Tar input is read twice: first to validate it and collect exclusion rules, then
to import contents. This also checks gzip checksums and truncated input before
the first archive write for that source. Keep source files unchanged during
the operation. An import can still be partial if a storage failure, interruption,
or source change occurs during the second pass. Completed imports from earlier
sources are saved if a later source fails; the command returns an error.

Only tar files explicitly passed as sources are expanded. A tar found while
walking a directory, or a tar member within another tar, is stored as an ordinary
file. To store a tar container itself, merge its containing directory. Restore,
CLI browsing, version selection, and sharing work normally for imported entries,
whether the erwtensoep archive is stored locally or on S3.

### Preview or limit a merge

```sh
erwtensoep -a "$HOME/archives/work" merge --dry-run "$HOME/backup-source"
erwtensoep -a "$HOME/archives/work" merge --max-size 104857600 "$HOME/backup-source"
```

Size limits are inclusive, in bytes, and apply to regular files. A dry run skips
archive object writes but can still read and compress source data; it is not
necessarily fast. The archive-directory creation prompt still applies.

To exclude names, put a file called `.erwtensoep` in a source directory:

```text
.git
node_modules
*.tmp
```

Each line is a case-sensitive glob matched against an individual child name.
Patterns apply in that directory and its descendants; a matched directory is
skipped entirely. This is not `.gitignore` syntax: there are no comments,
negation rules, recursive `**` rules, or whitespace trimming. The `.erwtensoep`
file itself is archived unless excluded, for example by a `.erwtensoep` line.

### Browse and read archived files

```sh
erwtensoep -a "$HOME/archives/work" cli
```

An example session:

```text
erwtensoep:/ ls -lah
erwtensoep:/ cd documents
erwtensoep:/documents/ cat -n notes.txt
erwtensoep:/documents/ more -n 20 notes.txt
erwtensoep:/documents/ du -sh .
erwtensoep:/documents/ cd -
erwtensoep:/ exit
```

The prompt is `erwtensoep:<pwd>/`. Paths, including absolute paths and symlink
targets, resolve inside the archive. Reading files does not extract them to the
host filesystem. Use Tab for commands and paths, and Up/Down for command
history. See the [interactive reference](#interactive-command-reference).

To inspect a file's history, run `versions notes.txt`. When you then run
`cat notes.txt`, the command displays a numbered table if multiple versions
exist. For a file with three versions, selecting the second newest looks like:

```text
Choose version (1-3; Enter to skip; q to cancel): 2
```

The selection applies only to that command. `more notes.txt` asks again;
`cat --latest notes.txt` reads the newest without asking. Use `du --latest -sh .`
for a directory total without individual version questions. Files with only
one saved version are processed immediately without a chooser.

### Restore selected files or directories

Restore into a fresh output directory:

```sh
erwtensoep -a "$HOME/archives/work" restore -o "$HOME/restored-work" documents
erwtensoep -a "$HOME/archives/work" restore -o "$HOME/restored-notes" 'documents/*.txt'
```

Output retains archive paths: `documents/notes.txt` becomes
`$HOME/restored-work/documents/notes.txt`. A directory name selects all its
descendants when no same-named file exists; append `/` to select the directory
explicitly. See
[files and directories with the same name](#files-and-directories-with-the-same-name).
To restore the entire example archive, supply each top-level directory and file:

```sh
erwtensoep -a "$HOME/archives/work" restore -o "$HOME/restored-all" documents photos README.txt
```

There is no restore-all switch, and `'*'` does not match paths containing `/`.
Use `ls` or the interactive `ls -A /` to inspect the names first, including hidden
names. Restoring with no patterns is an error.

For each matching file or symlink with multiple versions, choose one numbered
version from the displayed list. Enter skips that path; `q` or EOF without an
answer cancels before any output files are changed. To restore the newest
versions without questions, add `--latest`:

```sh
erwtensoep -a "$HOME/archives/work" restore --latest -o "$HOME/restored-work" documents
```

Restore replaces existing output paths without confirmation, including removing
an existing directory at a selected file path. Regular-file permission bits and
modification times are reapplied on a best-effort basis; ownership, ACLs,
extended attributes, and directory metadata are not restored. Symlinks are
recreated with their saved target text, including absolute targets. Use a fresh
output tree, and inspect symlinks before restoring descendants through them;
restore does not provide the CLI's archive-only path resolution.

### Measure sharing and compression

```sh
erwtensoep -a "$HOME/archives/work" stats
```

Read the storage breakdown from top to bottom:

| Stage | What is counted |
| --- | --- |
| Logical file data | Original bytes of every saved regular-file version. |
| After whole-file sharing | Each distinct referenced file content, or blob, once. |
| After block sharing | Each referenced data block once, even if several blobs use it. |
| Stored blocks (gzip) | Actual compressed block object sizes, including gzip overhead. |

For example, 100 MB of logical data becoming 60 MB of unique file contents,
40 MB of unique blocks, and 20 MB on disk represents 40 MB saved by whole-file
sharing, another 20 MB by block sharing, and another 20 MB by compression.
The percentages in the report name their baselines; do not add percentages
with different baselines.

The report also separates general block reuse from sharing between distinct
file paths. Repeated blocks within one file and versions of one path can cause
reuse without cross-file sharing. Gzip can increase the size of small or
incompressible data; that appears as an increase.

`stats` excludes metadata, superblock backups, unreferenced objects, and
filesystem allocation overhead. Use your host's `du` on a local archive directory
for disk usage, or your provider's storage reports for S3. The CLI's `du`
instead measures logical sizes of the selected file versions, without
deduplication; `du --latest` uses the newest versions without asking.

### Remove archived paths

The top-level command lists matching paths and asks once before removal:

```sh
erwtensoep -a "$HOME/archives/work" rm documents/obsolete.txt
erwtensoep -a "$HOME/archives/work" rm 'documents/*.tmp'
```

A bare name selects only the exact file or symlink when it exists, otherwise
it selects directory descendants. Append `/` to select descendants explicitly
and preserve any same-named file. Top-level `rm` does not require `-r`. See
[removing a collision](#removing-a-collision-with-top-level-rm) for examples.
The interactive shell has Unix-style options instead:

```text
erwtensoep:/ rm -i documents/obsolete.txt
erwtensoep:/ rm -riv old-project
```

For a path with multiple versions, both commands show a numbered version list
and ask which ones to remove. Enter numbers/ranges such as `1,3-5`, or `all`.
Enter alone skips that path; `q` cancels the command. Use `--all-versions` to
select every version without the chooser. The top-level command asks for final
confirmation; the interactive command removes single-version paths immediately
unless `-i` is specified. Its directory removal requires `-r` or `-R`.
Shared blobs and blocks remain as long as a surviving file version needs
them. Unused objects and their empty physical parent directories are removed
recursively. Source files are unaffected.

### Check and repair an archive

Start with a check that never applies changes:

```sh
erwtensoep -a "$HOME/archives/work" fsck -nv
```

For a report followed by a repair prompt:

```sh
erwtensoep -a "$HOME/archives/work" fsck -v
```

Keep the archive idle throughout the scan, review, and repair. Read the proposed
actions before accepting: repair discards unusable objects and entries. After
repair, run `fsck -nv` again to confirm consistency. Recoverable files with no
surviving pathname appear under `lost+found/<blob-hash>` and can be browsed or
restored normally.

Choose the maintenance command according to the task:

| Command | Purpose | Writes |
| --- | --- | --- |
| `verify --shallow` | Compare stored object names with a loadable index; skip decompression. | None. |
| `verify` | Also decompress stored objects to detect gzip corruption. | None. |
| `fsck -n` | Validate layout, hashes, metadata, and references, even with a damaged or missing index; list proposed repairs. | None. |
| `fsck` | Perform the full check and offer repairs. | Only after confirmation. |
| `fsck -y` | Perform the full check and apply the reported repairs. | Automatic repair. |
| `rebuild` | Construct a separate index from existing descriptors. | `rebuilt_superblock.json`; does not replace the active index. |

## Configuration

Configuration is read from `~/.erwtensoep.json`. The similarly named file in
the repository is an example, not an automatically loaded project configuration.
Edit or merge settings into your home configuration as appropriate:

```json
{
  "archive-dir": "/absolute/path/to/archive",
  "storage-backend": "auto",
  "merge": {
    "min-size": 0,
    "max-size": 104857600,
    "sb-write-threshold": 1000000000
  },
  "restore": {
    "output-dir": "/absolute/path/to/restored-files"
  },
  "verify": {
    "shallow": false
  }
}
```

Explicit command-line flags override configuration. A command's section
overrides top-level configuration; top-level values override built-in defaults.
Values only apply where the command has a corresponding flag. Recognized
command sections are `merge`, `ls`, `cli`, `rm`, `restore`, `verify`, `fsck`,
`stats`, `rebuild`, `hello`, and `recipe`.

The usable settings are `archive-dir`, `storage-backend`, `endpoint_url`, `region`,
`min-size`, `max-size`,
`dry-run`, `sb-write-threshold`, `output-dir`, and `shallow`. JSON sizes are
integer byte counts, not strings such as `100MB`. Paths are used literally:
JSON values do not expand `~` or environment variables. Fsck's `yes`, `no`, and
`verbose` flags are not configurable in this file. History has its own file.

## Subcommand reference

The options below are in addition to global options and `--help`.

### `merge SOURCE...`

Add files and symlinks from one or more source directories or the contents of
explicit `.tar`, `.tar.gz`, and `.tgz` files. Print a `WARNING` for every tar
source to explain that its contents will be imported. New metadata versions are
retained alongside earlier ones; matching metadata is skipped. Source paths are
local host paths. At least one source is required; ordinary standalone files
are not accepted as sources. See [tar imports](#import-files-from-tar-archives).

| Option | Default | Meaning |
| --- | --- | --- |
| `--min-size BYTES` | `0` | Smallest regular file to include. |
| `--max-size BYTES` | `9223372036854775807` | Largest regular file to include. |
| `--dry-run` | `false` | Process without writing archive objects or the index. |
| `--sb-write-threshold BYTES` | `1000000000` | Flush the index after this many uncompressed object bytes have been written. |

A final index write occurs when the command reports changes, including completed
work before a later source error. The threshold is
not a compressed disk-space limit. See [merge tasks](#create-an-archive-and-add-files)
and [exclusion rules](#preview-or-limit-a-merge).

### `ls [PATTERN...]`

Print modification time, byte size, and path for each matching entry. An explicit
file operand offers version selection; `--latest` skips the chooser. Directory,
pattern, and no-argument listings show newest versions. Append `(N versions)`
when that path has multiple saved versions, for example
`documents/notes.txt (3 versions)`. With no patterns,
list all paths. Output is a flat list and is not guaranteed to be sorted.
There are no command-specific flags; `-l`, `-R`,
and `-a` as a hidden-file switch belong to the interactive `ls`, not this command.

Use archive-relative names such as `documents`, without a leading or trailing
slash. A plain name matches itself and descendants. A glob matches the full
archived path; `*` does not cross `/`. Quote globs in your host shell, for example
`erwtensoep -a ARCHIVE ls 'documents/*.txt'`.

### `cli`

Open the interactive archive shell at `/`. No positional arguments or
command-specific flags are accepted. Directory browsing shows the latest versions;
[`versions FILE`](#versions-file) lists every saved version of a file relative
to the current archive directory. Reopen the CLI to see changes made by another
process. Its own `rm` refreshes the view immediately. See the
[interactive reference](#interactive-command-reference).

### `restore -o DIRECTORY PATTERN...`

Restore matching files and symlinks, choosing one saved version per path when
multiple versions exist. `--latest` uses the newest versions without asking.
`-o` / `--output-dir` is required unless configured. At least one pattern is
required. No matches is
a successful operation that restores nothing.

A plain name selects the exact file or symlink when it exists; otherwise it
selects descendants. A trailing `/` selects descendants only. Globs match full
archive-relative paths and do not cross `/`. Selecting both a file and any of
its descendants fails before prompting or writing output. See
[files and directories with the same name](#files-and-directories-with-the-same-name).
Choices are collected before any files are written; Enter skips a file and `q`
or unanswered EOF cancels the operation. There is no overwrite prompt or
dry-run flag.
See the [restore task](#restore-selected-files-or-directories).

### `rm PATTERN...`

For each matching path with multiple saved versions, show a numbered table and
ask which versions to remove. The choices and cancellation behavior are the
same as [interactive `rm`](#rm-path). Single-version paths are selected
automatically. `--all-versions` selects every version without the chooser.
After selection, print a summary and ask `Proceed with removal? [y/N]:`.
Only `y` or `yes`, case-insensitively, confirms. Declining returns an error;
no matches or no selected versions prints a message and succeeds. At least one
pattern is required.

Matching uses the same path-selection rules as `restore`: a plain name selects
the exact file or symlink if it exists, otherwise its descendants. A trailing
`/` selects descendants only. Globs match full archive-relative paths without
crossing `/`. Unlike restore, removal permits selecting both a file and its
descendants together (for example, `rm a a/`). Only the selected versions are
removed. There are no `-r`, `-f`, `-i`, or `-v` options on this top-level command;
those belong to interactive `rm`. See
[removing a collision](#removing-a-collision-with-top-level-rm).

### `stats`

Report paths, versions, unique referenced blobs and blocks, storage savings,
and block reuse. No positional arguments or command-specific flags. All saved
versions contribute, including older versions. See the
[statistics explanation](#measure-sharing-and-compression).

### `verify`

Check stored blocks, blob descriptors, and entry records against the loaded
superblock. By default, fully decompress objects to check gzip integrity.
`--shallow` skips decompression and only checks object presence and index
membership. If configured as shallow, `--shallow=false` requests the full check.

Progress is printed without a verbosity flag. Missing, unreferenced, or corrupt
objects are reported, and the command exits nonzero on failure. This command
does not offer repair or perform fsck's content-hash and metadata validation.
It depends on loading the existing index; use `fsck` if that fails.

### `fsck`

Read the superblock, inventory storage, and independently validate all stored
blocks, blobs, and entries. No positional arguments are accepted.

| Option | Meaning |
| --- | --- |
| `-n`, `--no` | Check only; never apply repairs or ask for confirmation. |
| `-y`, `--yes` | Print the report and apply repairs without prompting. |
| `-v`, `--verbose` | Show scan progress on stderr. |

`-n` and `-y` are mutually exclusive. Without either, discrepancies are listed
with proposed actions, followed by a confirmation prompt. Declining or EOF
makes no changes. An already consistent archive needs no confirmation.

Verbose progress includes completed/total objects and percentage done for each
phase, such as `Checking blocks: 500/1000 objects (50.0%)`. The directory
inventory initially shows `counting...`; percentages become available once
totals are known. Updates occur at phase changes and about every two seconds
while objects are checked. These are object-count percentages per phase, not
an overall time estimate or repair progress. Empty phases report 100%.

Repair follows these rules:

- Remove corrupt entries, blobs, and blocks. Remove any blob with a missing or
  corrupt block, and entries referring to an unusable or missing blob.
- Use valid disk records to reconstruct the index. Recreate a missing entry
  record from valid indexed metadata when its blob is usable; do not resurrect
  a corrupt on-disk entry record from the index.
- Recover each valid blob without surviving entries as
  `lost+found/<blob-hash>`, adding a numeric suffix if necessary. New entries
  use mode `0600`, unknown owner/group, and the recovery time. If `lost+found`
  itself is an archived file or symlink, repair is blocked until resolved.
- Remove blocks unused by surviving blobs, invalid objects in the storage
  layout, and empty storage directories, recursively. Physical storage
  symlinks are removed as links, never followed.
- Rebuild and rewrite the complete superblock.

Before repair, the original index, if present, is saved as
`superblock.json.gz.fsck.bak`. This is an index backup, not a backup of removed
file data. The command aborts if the index changed after inspection. I/O and
permission errors abort checking rather than treating unreadable data as
corrupt. A repair can fail after making some changes; read the error and check
again after resolving it.

Exit status is zero for a consistent archive or completed repair, and nonzero
for unrepaired discrepancies, an unsuccessful check, or an incomplete repair.
Thus `fsck -n` finding a problem is expected to exit nonzero.

### `rebuild`

Scan stored blob and entry descriptors and write a reconstructed index to
`ARCHIVE/rebuilt_superblock.json`. Despite its name, that file is gzip-compressed.
The active `superblock.json.gz` is not replaced. No arguments or additional
options are accepted.

This is an index reconstruction tool, not a full corruption checker. It reads
block size headers, assumes usable stored descriptors, and can fail on damaged
objects. Use `fsck` for validated repair and recovery. Keep the archive idle
while rebuilding.

### `hello`

Print `Hello World!`. This smoke-test command does not require an archive.

### `recipe`

Print a recipe for Dutch pea soup. No arguments or archive are required.

### `help [COMMAND]`

Print general or command-specific help. Help does not require an archive.

### `completion SHELL`

Generate a host-shell completion script for `bash`, `zsh`, `fish`, or
`powershell`. Use `erwtensoep completion SHELL --help` for installation examples.
The generated commands and flag completions are separate from the archive-path
completion already built into `cli`.

The current command initialization requires `--archive-dir` (or a configured
archive location) when generating a script. A local directory must already
exist; an S3 location requires its endpoint and credentials. For example:

```sh
erwtensoep -a "$HOME/archives/work" completion zsh > /tmp/_erwtensoep
```

Generation prints the script and does not edit your shell startup files. All
four shell subcommands accept `--no-descriptions` to omit descriptions. Check
the selected shell's help for its installation instructions.

## Interactive command reference

### Paths, quoting, completion, and history

Inside `cli`, `/` is the archive root; relative paths start at the current
directory. `.` and `..` work as usual. Symlinks, including absolute targets,
resolve within the archive. Symlink loops produce an error. When a file and
directory share a name, `a` selects the file and `a/` selects the directory;
`cd a` enters the directory. See the detailed
[collision rules and examples](#files-and-directories-with-the-same-name).

Single/double quotes and backslash escapes protect spaces or literal glob
characters. Unquoted `*`, `?`, and `[...]` expand against archive paths. Hidden
names need an explicit leading dot. For example, `cat *.txt` expands a glob,
while `cat '*.txt'` requests a file literally named `*.txt`. This differs from
quoting a pattern passed through the host shell to top-level `ls`, `rm`, or `restore`.

Short options can be combined. Use `--` before operands beginning with `-`.
There are no pipes, redirections, variables, host commands, or command chaining;
enter one command per line.

Tab completes command names and archive paths. A unique command or file adds a
space; a directory adds `/`. Ambiguous completion extends the common prefix
or lists matches. `cd` completes directories only. Completion supports quoted
names and symlinked directories; `rm` and `versions` complete symlinks without
adding `/` so the commands act on the link itself.

Up/Down recalls the last 100 commands, shared across archives and CLI sessions.
Down past the newest entry restores the unfinished line. Left/Right and
Backspace edit it. Blank lines and consecutive duplicates are omitted. History
is saved after input to `~/.erwtensoep_history`, a JSON array with owner-only
permissions. Concurrent history writers merge their new commands using a
separate `.erwtensoep_history.lock` file. This lock protects history only.

Close CLI sessions before deleting the history file to clear it. History errors
produce a warning; in-memory history remains available and saving is retried.
Removal confirmations and all version selections are not commands and are not saved.

When input or output is not a terminal, interactive editing and paging are
disabled and commands are not saved in history. You can feed command lines
through stdin, but prompts and diagnostic output remain; this is not a raw file
export or machine-readable interface. A command error returns to the prompt
and does not by itself give the CLI process a failing exit status.

### `cd [DIR|-]` and `pwd`

`cd` without an argument returns to `/`; `cd -` returns to the previous
directory and prints it. `pwd` prints the current path.

Both accept `-L` / `--logical` (the default) and `-P` / `--physical`. Logical
navigation retains a symlink's path; physical navigation resolves it before
handling `..`. `pwd -P` prints the resolved path. `-L` and `-P` cannot be combined.

### `ls [PATH...]`

List the current directory by default, sorted by name, one entry per line.
Explicit file operands offer version selection; `--latest` chooses the newest
without prompting. Directory listings, including `-R`, show newest versions.
Files and symlinks with multiple saved versions have a count appended, such as
`notes.txt (3 versions)`. This also appears in long and recursive listings.
For symlinks, the count describes the link's own versions. Single-version
entries and inferred directories have no count suffix. Use `versions FILE`
to inspect the saved versions.

| Options | Effect |
| --- | --- |
| `-a`, `--all` | Include hidden entries and `.` / `..`. |
| `-A`, `--almost-all` | Include hidden entries without `.` / `..`. |
| `-l`, `--long` | Show mode, owner, group, size, time, and symlink target. |
| `-h`, `--human-readable` | Format sizes in binary units. |
| `-R`, `--recursive` | List subdirectories recursively. |
| `-d`, `--directory` | List directories themselves rather than contents. |
| `-F`, `--classify` | Add `/` for directories, `@` for links, `*` for executables. |
| `-t`, `--time` | Sort by newest modification time first. |
| `--latest` | Use newest versions for file operands without asking. |
| `-S`, `--size` | Sort largest first; overrides `-t`. |
| `-r`, `--reverse` | Reverse sorting. |
| `-1`, `--one-per-line` | One entry per line, already the default. |

Inferred directories display synthetic metadata: read-only directory mode,
unknown owner/group, size zero, and no modification time.

### `versions FILE`

List every saved version of one file, resolving its name from the current
archive directory. Absolute archive paths and paths through subdirectories also
work. For example:

```text
erwtensoep:/ cd documents
erwtensoep:/documents/ versions notes.txt
erwtensoep:/documents/ versions -hr "meeting notes.txt"
```

Each row shows the entry's version ID, saved modification time (including
timezone and fractional seconds when present), logical size in bytes,
permissions, and archive path. Versions appear newest modification time first;
equal timestamps are ordered by version ID descending, matching the CLI's
latest-version selection. These are source-file modification times, not the
time the file was backed up. Versions with shared contents still get separate
rows.

| Options | Effect |
| --- | --- |
| `-h`, `--human-readable` | Format sizes in binary units. |
| `-r`, `--reverse` | Reverse sorting to show oldest versions first. |

Use `help versions` or `versions --help` inside the CLI to see usage and flags.

A final symlink shows its own saved versions and each target; intermediate
symlinks are resolved inside the archive. `versions a` lists the file history
even when a directory `a/` also exists. `versions a/` explicitly selects the
directory and produces an error. Exactly one operand
is required after glob expansion. This command only displays metadata; it does
not change the archive or select a version for another command. File commands
present their own numbered chooser when needed.

### `cat FILE...`

Stream and concatenate archived file contents. At least one file is required.
Choose one saved version per file when prompted, or use `--latest` to select the
newest automatically. Enter skips a file and `q` cancels before any contents are
printed. A versioned symlink is selected before following its saved target; a
versioned target then gets its own choice. Repeated paths or aliases reuse the
same choice within a command. If a file and directory share the path, `cat a`
reads the file and `cat a/` rejects the directory.

| Options | Effect |
| --- | --- |
| `-n`, `--number` | Number all lines. |
| `-b`, `--number-nonblank` | Number nonempty lines; overrides `-n`. |
| `-s`, `--squeeze-blank` | Suppress repeated empty lines. |
| `-E`, `--show-ends` | Display `$` at line endings. |
| `-T`, `--show-tabs` | Display tabs as `^I`. |
| `--latest` | Use newest versions without asking. |

### `more FILE...`

Page through file contents. `-n N` / `--lines N` or `-N` sets the page length;
`+N` begins at line N. `-s` / `--squeeze-blank` suppresses repeated empty lines.
The default page length follows terminal height when available. Lengths and
starting lines must be positive.
Version selection and `--latest` work as for `cat`; choices are collected before
paging starts. Pager keys are separate from version-selection answers.

Space advances a page, Enter advances one line, and `q` stops. Without terminal
input and output, contents are printed without paging.

### `du [PATH...]`

Show uncompressed logical usage, defaulting to the current directory. Hidden
descendants are counted, symlink targets are not followed, and every pathname
counts separately even if its data is shared. Symlinks contribute their saved
size; inferred directories contribute zero bytes of their own.
Choose one version of each file with multiple saved versions, including hidden
descendants. Enter skips that file and excludes its size; `q` cancels before
printing totals. Use `--latest` to calculate newest-version usage without asking.

| Options | Effect |
| --- | --- |
| `-s`, `--summarize` | Show only each operand's total. |
| `-a`, `--all` | Show individual files as well as directories. |
| `-h`, `--human-readable` | Format sizes in binary units. |
| `-b`, `--bytes` | Exact byte counts. |
| `-k`, `--kilobytes` | 1024-byte units, the default. |
| `-m`, `--megabytes` | 1048576-byte units. |
| `-c`, `--total` | Add a grand total across operands. |
| `-d N`, `--max-depth N` | Limit displayed depth relative to each operand. |
| `--latest` | Use newest versions throughout the selected directories without asking. |

Directory totals include deeper descendants even when display depth is limited.
Numeric units round up after summing bytes. `-s` conflicts with a depth other
than zero. Overlapping operands contribute separately to the grand total.

### `rm PATH...`

Remove saved versions of selected paths. When a path has multiple versions,
show a numbered table with version IDs, modification times, sizes, permissions,
and symlink targets, newest first. Enter one or more numbers separated by spaces
or commas (`1,3`), inclusive ranges (`2-4`), or `all`. Combinations such as
`1,3-5` work; duplicates are ignored. Invalid selections prompt again without
removing anything. Numbers refer to this displayed list, not permanent IDs.

Enter alone skips the current path; `q` or EOF without an answer cancels the
entire command. All choices are collected before any deletion, including for
recursive removal. Unselected versions remain available. If the newest is
removed, directory browsing and `--latest` operations use the newest remaining
version. Other file commands still offer a choice when multiple versions
remain. A path disappears when its last saved version is removed.
Single-version paths are removed immediately unless `-i` is specified.

| Options | Effect |
| --- | --- |
| `-r`, `-R`, `--recursive` | Remove directories and their descendants, including hidden files. |
| `--all-versions` | Select every saved version without the version chooser. |
| `-i`, `--interactive` | Also confirm single-version paths or `--all-versions` removals; choosing versions already confirms that selection. |
| `-f`, `--force` | Ignore missing paths and remove all versions without prompts. |
| `-v`, `--verbose` | Print removed paths and the remaining count after partial removal. |

The last `-f` or `-i` wins, including in combined flags. Force does not suppress
other errors. Root, `.` and `..` operands are refused. Final symlinks are
removed themselves; a trailing slash on a final symlink is rejected unless it
selects a separate inferred directory at that name. Intermediate
symlinks are resolved inside the archive, and recursion does not traverse
symlink targets. If removal empties the current inferred directory, the prompt
moves to its nearest surviving ancestor.

### `help [COMMAND]`, `exit`, and `quit`

`help` lists commands; `help COMMAND` shows the command's options. `exit`,
`quit`, or EOF leaves the shell. `exit` and `quit` take no arguments.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| `archive-dir is required` | Supply `-a` or configure `archive-dir` in `~/.erwtensoep.json`. |
| S3 endpoint is required | Pass `--endpoint-url` / `--endpoint_url`, or configure `endpoint_url`; it is required even for AWS. |
| S3 signing or access error | Check both credential environment variables, any stale session token, bucket permissions, and the endpoint/signing region. |
| S3 removal does not reduce provider usage | Check retained object versions and lifecycle/retention settings; erwtensoep removes current objects, not historical provider versions. |
| Files appear under unexpected paths | Source roots are stripped during merge; multiple sources share one namespace. |
| A tar source expands instead of being stored as a file | Explicit `.tar`, `.tar.gz`, and `.tgz` sources import their members and print a warning. Merge the containing directory to store the tar file itself. |
| An updated file was skipped | Merge identifies versions from metadata. Check size and modification time. |
| A glob matches too little | Top-level patterns match whole paths and `*` does not cross `/`; use a trailing `/` to select a subtree with `rm` or `restore`. |
| `ls -l` fails | Start `cli` for Unix-style listing flags; top-level `ls` has fixed output. |
| `du` and `stats` differ | CLI `du` measures selected logical file versions; `stats` measures all versions and sharing/compression stages. |
| Loading the superblock fails | Try `fsck -nv`, which can inspect a missing or damaged index independently. |
| Empty hash directories remain | `fsck` reports them and can prune them; new deletions prune empty parents automatically. |
| History does not persist | Check warnings, file permissions, and whether stdin/stdout are terminals. |
| `lost+found` recovery is blocked | An archived file or symlink occupies that name; restore it elsewhere if needed, then remove it before retrying. |

The archive is gzip-compressed, not encrypted. An index backup alone cannot
restore deleted blocks. Keep a separate copy of the archive when you need an
independent backup; see the [technical manual](technical-manual.md) for storage
and consistency details.
