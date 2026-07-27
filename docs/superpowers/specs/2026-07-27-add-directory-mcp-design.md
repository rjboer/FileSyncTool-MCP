# Recursive `add_directory` MCP Tool

## Goal

Add an MCP tool that recursively processes a directory inside the configured
`source_root`. It stages and indexes previously unknown regular files, removes
source files only when identical content is already recorded as `RemoteOnly`,
preserves the complete directory structure, records per-file failures in a
durable CSV log, and continues after ordinary file-level errors.

`sync_files` remains a separate, explicit MCP call.

## MCP interface

The server exposes a new tool:

```text
add_directory
```

Input:

```json
{
  "path": "C:\\AI-organisatie\\KlantA"
}
```

`path` must be an absolute directory path. The directory must resolve to
`source_root` itself or a directory below it.

The structured result contains:

- `scanned`: number of regular source files considered;
- `added`: number of previously unknown files staged and indexed;
- `retained_on_file_not_remote`: number of known files retained because their
  index entry is `OnFileNotRemote`;
- `retained_on_file_and_remote`: number of known files retained because their
  index entry is `OnFileAndRemote`;
- `removed_remote_only`: number of source files removed after their identity
  was confirmed as `RemoteOnly`;
- `skipped`: number of unsupported non-regular entries, including symbolic
  links and junctions, plus the live index file if it is encountered below
  `source_root`;
- `failed`: number of file-level failures recorded in the CSV log;
- `error_log_path`: absolute path of the CSV log for this invocation.

Directories are not included in `scanned` or `skipped`.

## Architecture

`add_directory` is a batch layer in the existing `files.AddService`. The
single-file logic is split into an internal lock-free operation that is shared
by `Add` and the new directory operation. `Add` retains its existing public
behavior and obtains the shared operation mutex before using that operation.

The directory operation obtains the same operation mutex for the entire batch.
This prevents `add_file` or `sync_files` from changing the index or workspace
while the directory status decisions are being made. It does not claim to
prevent external programs from changing source files; source identity is
revalidated immediately before destructive removal.

The directory tree is walked recursively in deterministic lexical order.
Symbolic links and junctions are not followed. Regular files are processed one
at a time, so the existing atomic index writes make completed work resumable
after interruption.

The live index file is treated as reserved internal state and is skipped if a
valid configuration places it below `source_root`. This prevents a directory
batch from staging and then mutating a snapshot of its own index.

## Per-file decision table

Identity is the existing normalized lowercase extension plus xxHash64 checksum.

| Identity lookup | Action |
| --- | --- |
| Unknown | Use the shared single-file operation to create a verified workspace copy and persist an `OnFileNotRemote` entry. |
| Known as `OnFileNotRemote` | Do not add or remove the source file. Increment `retained_on_file_not_remote`. |
| Known as `OnFileAndRemote` | Do not add or remove the source file. Increment `retained_on_file_and_remote`. |
| Known as `RemoteOnly` | Recompute the source identity immediately before removal. Remove only the source file when the identity still matches. Increment `removed_remote_only`. |

If two previously unknown source files in the same invocation have identical
identities, the first is added as `OnFileNotRemote`. The second then follows the
`OnFileNotRemote` rule and remains in the source tree.

A source path whose destination or index relative path conflicts with different
content fails without overwriting either copy.

No directory is ever removed, including directories that become empty after
`RemoteOnly` source files are deleted. This deliberately preserves the source
tree as an organizational framework.

## Validation and traversal

Before any file is processed, the tool validates that:

1. the path is absolute;
2. it exists and is a directory;
3. its resolved location is `source_root` or is contained by `source_root`;
4. the per-invocation error log can be created.

An invalid root request returns a tool error without changing the workspace or
index. Failure to create the error log also returns a tool error before file
processing begins.

Unreadable descendants, disappearing files, unsupported entries, and other
entry-level traversal conditions do not invalidate the root request.
Unsupported entries increment `skipped`. Actual I/O failures increment
`failed`, are logged, and do not stop later entries from being processed.

Context cancellation stops further traversal. Work already persisted remains
valid and resumable. The log is flushed and closed before returning whenever
the process still has the opportunity to do so.

## Error log

Every accepted invocation creates a separate CSV file below:

```text
<workspace_root>\logs\add_directory\
```

The filename contains a UTC timestamp and collision-resistant run ID. It does
not contain an unsanitized source directory name. The result always returns the
absolute log path, including when no file-level errors occurred.

Before creating a log, every existing `logs/add_directory` path component is
inspected without following links. Symbolic links, Windows junctions, and other
reparse points are rejected, and the physically resolved log directory must
remain below the physically resolved `workspace_root`.

The CSV begins with this header:

```text
timestamp_utc,run_id,source_path,relative_path,phase,error
```

Each file-level or traversal failure is appended and flushed before processing
continues. The fields mean:

- `timestamp_utc`: RFC 3339 UTC time of the failure;
- `run_id`: identifier shared by all rows from the invocation;
- `source_path`: absolute path involved in the failure;
- `relative_path`: slash-normalized path relative to `source_root`, when it can
  be determined, otherwise empty;
- `phase`: stable phase name such as `walk`, `fingerprint`, `add`, `revalidate`,
  or `remove`;
- `error`: human-readable error with no S3 credentials.

If writing or flushing the log fails after processing has begun, the operation
stops because further best-effort processing could no longer satisfy the audit
guarantee. Already completed adds or removals are not rolled back.

The log directory is in `workspace_root`, which cannot overlap `source_root`
under existing configuration validation. Log files are not inserted into the
index and are therefore not selected by `sync_files`.

## Failure behavior

For ordinary file-level failures:

- retain the source file;
- do not create or change an index entry for that failed operation;
- append and flush a CSV row;
- increment `failed`;
- continue with the next entry.

Expected file-level failures include:

- access denied while walking, opening, fingerprinting, copying, or removing;
- a file that disappears or changes during processing;
- a conflicting workspace destination or index relative path;
- failure to create, flush, verify, or publish a staging copy;
- failure to atomically persist an index addition;
- failure to delete a confirmed `RemoteOnly` source file.

When an attempted new add created a staging copy but index persistence fails,
the existing single-file rollback behavior removes only that newly created
staging copy and cleans its empty workspace parents. Source directories remain
untouched.

## MCP and documentation changes

The MCP server registers `add_directory` alongside `add_file` and `sync_files`.
Its input schema requires only `path`. The server implementation version is
incremented from `1.0.0` to `1.1.0`.

The README documents the new input, status decisions, result counters, error
log location, non-following of links, preservation of directories, and the
requirement to invoke `sync_files` separately.

## Test strategy

Tests are written before production changes and cover:

1. recursive staging while preserving relative subdirectories;
2. accepting `source_root` itself and a nested directory;
3. rejecting relative paths, files, missing roots, and directories outside
   `source_root` before processing;
4. an empty directory producing zero counts while remaining present;
5. every identity/status decision in the table;
6. two identical files encountered in one batch;
7. revalidation before `RemoteOnly` deletion;
8. preserving all directories after successful source deletion;
9. destination and index conflicts without overwrite;
10. continuing after readable, fingerprint, copy, index, and deletion failures;
11. deterministic traversal and stable counters;
12. skipping and not following symbolic links or junctions where supported by
    the test platform;
13. creation, escaping, append, and flush behavior of the per-run CSV;
14. aborting before mutation when the log cannot be created;
15. stopping safely on cancellation or a mid-run log failure;
16. MCP discovery, input schema, structured result, and version;
17. unchanged behavior of `add_file` and `sync_files`;
18. full `go test ./...`, `go vet ./...`, and production build verification.

## Success criteria

The feature is complete when:

- unknown regular files are staged and indexed as `OnFileNotRemote`;
- known `OnFileNotRemote` and `OnFileAndRemote` source files remain untouched;
- only identity-revalidated `RemoteOnly` source files are removed;
- no source directory is removed;
- ordinary per-file failures preserve their source, are durably logged, and do
  not prevent later files from being processed;
- the structured counters accurately describe the completed batch;
- `sync_files` is never invoked implicitly;
- existing tools remain compatible and all verification commands pass.
