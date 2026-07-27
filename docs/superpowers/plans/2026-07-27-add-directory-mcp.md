# Recursive `add_directory` MCP Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a recursive, best-effort `add_directory` MCP tool that stages unknown files, removes only revalidated `RemoteOnly` source files, preserves every directory, and writes failures to a per-run CSV log.

**Architecture:** Extend `files.AddService` with a directory batch operation that holds the existing shared operation mutex and reuses a lock-free extraction of the current single-file add logic. Keep CSV audit logging in a focused file, expose the method through the existing MCP server, and leave `sync_files` explicit.

**Tech Stack:** Go 1.26, standard library (`context`, `encoding/csv`, `io/fs`, `path/filepath`), Model Context Protocol Go SDK, existing xxHash64 fingerprinting and JSON index.

## Global Constraints

- Only an absolute directory equal to or below `source_root` is accepted.
- Recursive traversal is deterministic and does not follow symbolic links or junctions.
- Only regular files count as scanned; unsupported non-regular entries count as skipped.
- Unknown content is staged and indexed as `OnFileNotRemote`.
- Known `OnFileNotRemote` and `OnFileAndRemote` content is retained without another add.
- Source files are removed only when type plus checksum is revalidated against a `RemoteOnly` entry.
- Source directories are never removed.
- Ordinary file-level failures preserve the source, are durably logged, and do not stop the batch.
- An invalid root, unavailable log, log write failure, or cancellation stops the batch.
- `sync_files` is never called by `add_directory`.

---

### Task 1: Extract the reusable add operation and validate directory roots

**Files:**
- Modify: `internal/files/add.go`
- Modify: `internal/files/paths.go`
- Modify: `internal/files/add_test.go`

**Interfaces:**
- Produces: `func (s *AddService) addLocked(ctx context.Context, sourcePath string) (AddResult, error)`
- Produces: `func DirectoryWithin(root, directory string) (string, error)`
- Preserves: `func (s *AddService) Add(ctx context.Context, sourcePath string) (AddResult, error)`

- [ ] **Step 1: Write failing path-validation tests**

Add table-driven tests that assert `DirectoryWithin` accepts `source_root`
itself and a nested directory, and rejects a relative path, regular file,
missing directory, outside directory, and symlink root:

```go
func TestDirectoryWithinAcceptsRootAndNestedDirectory(t *testing.T) {
    root := t.TempDir()
    nested := filepath.Join(root, "a", "b")
    if err := os.MkdirAll(nested, 0o755); err != nil { t.Fatal(err) }
    for _, candidate := range []string{root, nested} {
        resolved, err := DirectoryWithin(root, candidate)
        if err != nil { t.Fatalf("DirectoryWithin(%q) error = %v", candidate, err) }
        if !filepath.IsAbs(resolved) { t.Fatalf("resolved = %q", resolved) }
    }
}

func TestDirectoryWithinRejectsInvalidRoots(t *testing.T) {
    root := t.TempDir()
    file := filepath.Join(root, "file.txt")
    mustFilesWrite(t, file, "x")
    cases := []string{"relative", file, filepath.Join(root, "missing"), t.TempDir()}
    for _, candidate := range cases {
        if _, err := DirectoryWithin(root, candidate); err == nil {
            t.Errorf("DirectoryWithin(%q) error = nil", candidate)
        }
    }
}
```

Create a symlink test only when `os.Symlink` succeeds; otherwise call
`t.Skip`.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```powershell
go test ./internal/files -run "TestDirectoryWithin" -v
```

Expected: build failure because `DirectoryWithin` is undefined.

- [ ] **Step 3: Implement safe directory containment**

In `paths.go`, use `filepath.IsAbs`, `os.Lstat`, `ModeSymlink`,
`filepath.EvalSymlinks`, `os.Stat`, and `filepath.Rel`. Return the resolved
absolute directory only when the relative result is `.` or safely below the
resolved root.

- [ ] **Step 4: Refactor `Add` without changing behavior**

Keep lock ownership in the public method and move its current body into
`addLocked`:

```go
func (s *AddService) Add(ctx context.Context, sourcePath string) (AddResult, error) {
    s.opMu.Lock()
    defer s.opMu.Unlock()
    return s.addLocked(ctx, sourcePath)
}

func (s *AddService) addLocked(ctx context.Context, sourcePath string) (AddResult, error) {
    // Existing Add body, beginning with ctx.Err(), without another lock.
}
```

- [ ] **Step 5: Run files tests and verify GREEN**

Run:

```powershell
go test ./internal/files -v
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```powershell
git add internal/files/add.go internal/files/paths.go internal/files/add_test.go
git commit -m "refactor: prepare directory file imports"
```

---

### Task 2: Implement recursive status decisions and directory preservation

**Files:**
- Create: `internal/files/add_directory.go`
- Create: `internal/files/add_directory_test.go`
- Modify: `internal/files/add.go`

**Interfaces:**
- Consumes: `(*AddService).addLocked(context.Context, string) (AddResult, error)`
- Consumes: `DirectoryWithin(string, string) (string, error)`
- Produces:

```go
type AddDirectoryResult struct {
    Scanned                int    `json:"scanned"`
    Added                  int    `json:"added"`
    RetainedNotRemote      int    `json:"retained_on_file_not_remote"`
    RetainedOnAndRemote    int    `json:"retained_on_file_and_remote"`
    RemovedRemoteOnly      int    `json:"removed_remote_only"`
    Skipped                int    `json:"skipped"`
    Failed                 int    `json:"failed"`
    ErrorLogPath           string `json:"error_log_path"`
}

func (s *AddService) AddDirectory(ctx context.Context, sourceDir string) (AddDirectoryResult, error)
```

- [ ] **Step 1: Write the failing recursive happy-path test**

Create nested regular files and an empty directory. Call `AddDirectory` and
assert both files are staged with source-root-relative paths, `Added == 2`,
the empty source directory still exists, and no sync/delete occurred.

```go
func TestAddDirectoryRecursivelyAddsUnknownFilesAndPreservesDirectories(t *testing.T) {
    source, workspace, store, service := newAddFixture(t)
    selected := filepath.Join(source, "KlantA")
    mustFilesWrite(t, filepath.Join(selected, "a.txt"), "a")
    mustFilesWrite(t, filepath.Join(selected, "nested", "b.txt"), "b")
    empty := filepath.Join(selected, "empty")
    if err := os.MkdirAll(empty, 0o755); err != nil { t.Fatal(err) }

    result, err := service.AddDirectory(context.Background(), selected)
    if err != nil { t.Fatal(err) }
    if result.Scanned != 2 || result.Added != 2 || result.Failed != 0 {
        t.Fatalf("result = %#v", result)
    }
    if len(store.Snapshot().Files) != 2 { t.Fatalf("index = %#v", store.Snapshot()) }
    if info, err := os.Stat(empty); err != nil || !info.IsDir() {
        t.Fatalf("empty directory changed: info=%v err=%v", info, err)
    }
    for _, relative := range []string{"KlantA/a.txt", "KlantA/nested/b.txt"} {
        if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(relative))); err != nil {
            t.Errorf("staged %q: %v", relative, err)
        }
    }
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
go test ./internal/files -run TestAddDirectoryRecursively -v
```

Expected: build failure because `AddDirectory` is undefined.

- [ ] **Step 3: Implement the minimal recursive batch**

Add `removeSource func(string) error` to `AddService`, initialized to
`os.Remove`. Implement `AddDirectory` with the operation mutex,
`DirectoryWithin`, and `filepath.WalkDir`. Call `addLocked` for each regular
file and classify `AddResult.Entry.Status`. Before removing `RemoteOnly`,
re-run `Fingerprint` and require both type and checksum to match.

The initial implementation may use a temporary in-memory failure collector;
Task 3 replaces it with durable CSV logging.

- [ ] **Step 4: Add failing status-table tests**

Seed entries for all three statuses, then assert:

```go
if result.Added != 1 ||
    result.RetainedNotRemote != 1 ||
    result.RetainedOnAndRemote != 1 ||
    result.RemovedRemoteOnly != 1 {
    t.Fatalf("result = %#v", result)
}
```

Also assert:

- `OnFileNotRemote` source remains;
- `OnFileAndRemote` source remains;
- `RemoteOnly` source is absent;
- every parent and empty directory remains;
- two identical unknown files result in one add and one retained
  `OnFileNotRemote` file.

- [ ] **Step 5: Run status tests and verify RED**

Run:

```powershell
go test ./internal/files -run "TestAddDirectory(Status|Duplicate|RemoteOnly)" -v
```

Expected: at least one assertion fails until all status branches exist.

- [ ] **Step 6: Complete status classification and revalidation**

Use one helper to translate an existing entry status into the exact result
counter. For `RemoteOnly`, fingerprint immediately before `removeSource`.
Return a file-level failure when the identity changed or removal failed.
Never call `RemoveEmptyParents` for a source path.

- [ ] **Step 7: Run all files tests and verify GREEN**

Run:

```powershell
go test ./internal/files -v
```

Expected: all tests pass.

- [ ] **Step 8: Commit**

```powershell
git add internal/files/add.go internal/files/add_directory.go internal/files/add_directory_test.go
git commit -m "feat: add recursive directory processing"
```

---

### Task 3: Add durable per-run CSV logging and best-effort failures

**Files:**
- Create: `internal/files/directory_log.go`
- Create: `internal/files/directory_log_test.go`
- Modify: `internal/files/add_directory.go`
- Modify: `internal/files/add_directory_test.go`

**Interfaces:**
- Produces:

```go
type directoryFailure struct {
    SourcePath   string
    RelativePath string
    Phase        string
    Err          error
}

type directoryErrorLog interface {
    Path() string
    Record(directoryFailure) error
    Close() error
}
```

- `AddService` receives an internal logger factory initialized by
  `newDirectoryErrorLog(workspaceRoot)`.

- [ ] **Step 1: Write failing CSV format tests**

Create a log, record an error containing commas and quotes, close it, parse it
with `csv.NewReader`, and assert the exact header:

```go
wantHeader := []string{
    "timestamp_utc", "run_id", "source_path",
    "relative_path", "phase", "error",
}
```

Assert the created path is absolute and below
`workspace_root/logs/add_directory`.

- [ ] **Step 2: Run logger tests and verify RED**

Run:

```powershell
go test ./internal/files -run TestDirectoryErrorLog -v
```

Expected: build failure because the logger does not exist.

- [ ] **Step 3: Implement the CSV logger**

Use `os.MkdirAll`, `os.OpenFile` with `O_CREATE|O_EXCL|O_WRONLY`,
`encoding/csv`, `time.Now().UTC()`, and random bytes from `crypto/rand`.
Write and flush the header before returning. `Record` writes one row and
flushes immediately; it checks `csv.Writer.Error()` after every flush.

- [ ] **Step 4: Write failing continuation and audit tests**

Inject a `removeSource` failure for one `RemoteOnly` file followed
lexicographically by an unknown file. Assert the first source remains, the
second file is added, `Failed == 1`, and the CSV row has phase `remove`.

Add tests for:

- destination conflict logged with phase `add` while a later file succeeds;
- symlink skipped and not followed;
- logger factory failure causes zero mutations;
- injected `Record` failure stops later processing;
- canceled context stops processing and returns `context.Canceled`;
- an empty successful invocation creates a header-only log.

- [ ] **Step 5: Run the new tests and verify RED**

Run:

```powershell
go test ./internal/files -run "TestAddDirectory.*(Failure|Log|Cancel|Symlink|Conflict)" -v
```

Expected: assertions fail because durable logging and continuation are not yet
connected.

- [ ] **Step 6: Connect logging and best-effort traversal**

Create the log after root validation and before acquiring mutations. In the
walk callback:

- stop immediately on `ctx.Err()`;
- increment `Skipped` for non-regular, non-directory entries;
- convert ordinary walk and file errors into `directoryFailure`;
- call `Record` before continuing;
- increment `Failed` only for durably recorded failures;
- return log errors so traversal stops.

Always close the log and propagate a close/flush error. Preserve all completed
atomic additions and successful removals when a run-level error terminates the
walk.

- [ ] **Step 7: Run package tests and verify GREEN**

Run:

```powershell
go test ./internal/files -v
```

Expected: all tests pass.

- [ ] **Step 8: Commit**

```powershell
git add internal/files/add.go internal/files/add_directory.go internal/files/add_directory_test.go internal/files/directory_log.go internal/files/directory_log_test.go
git commit -m "feat: log directory import failures"
```

---

### Task 4: Expose the MCP tool and document it

**Files:**
- Modify: `internal/mcpserver/server.go`
- Modify: `internal/mcpserver/server_test.go`
- Modify: `README.md`

**Interfaces:**
- Produces:

```go
type AddDirectoryInput struct {
    Path string `json:"path" jsonschema:"absolute path to a directory inside source_root"`
}
```

- Registers MCP tool `add_directory`.
- Changes server implementation version to `1.1.0`.

- [ ] **Step 1: Write failing MCP discovery and call tests**

Change the expected tool list to:

```go
[]string{"add_directory", "add_file", "sync_files"}
```

Add a call test that creates a nested file, invokes:

```go
session.CallTool(context.Background(), &mcp.CallToolParams{
    Name: "add_directory",
    Arguments: map[string]any{"path": selected},
})
```

Decode `files.AddDirectoryResult` and assert one scanned and added file plus a
non-empty log path.

- [ ] **Step 2: Run MCP tests and verify RED**

Run:

```powershell
go test ./internal/mcpserver -v
```

Expected: tool-list and unknown-tool failures.

- [ ] **Step 3: Register the MCP method**

Add the input type and `mcp.AddTool` handler. Return the structured directory
result and any run-level error. Increment the implementation version to
`1.1.0`.

- [ ] **Step 4: Run MCP tests and verify GREEN**

Run:

```powershell
go test ./internal/mcpserver -v
```

Expected: all tests pass.

- [ ] **Step 5: Document exact user-facing behavior**

Add an `add_directory` section to `README.md` with input JSON, the status
decision table, recursive link policy, counters, CSV location, directory
preservation, best-effort behavior, and a reminder that `sync_files` remains a
separate call.

- [ ] **Step 6: Run full verification**

Run:

```powershell
go test ./...
go vet ./...
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
```

Expected: every command exits 0. The ignored executable must not appear in
`git status`.

- [ ] **Step 7: Commit**

```powershell
git add internal/mcpserver/server.go internal/mcpserver/server_test.go README.md
git commit -m "feat: expose add_directory MCP tool"
```

---

### Task 5: Final requirements audit and pull request preparation

**Files:**
- Review: `docs/superpowers/specs/2026-07-27-add-directory-mcp-design.md`
- Review: all files changed by Tasks 1–4

**Interfaces:**
- Consumes: all interfaces and constraints defined above.
- Produces: a verified branch ready for a draft pull request.

- [ ] **Step 1: Audit the diff against the specification**

Use:

```powershell
git diff origin/main...HEAD --check
git diff origin/main...HEAD --stat
git status --short
```

Read every changed production and test file. Confirm each success criterion in
the specification has a corresponding test or explicit implementation.

- [ ] **Step 2: Run fresh verification**

Run:

```powershell
go test -count=1 ./...
go vet ./...
go build -trimpath -o .\dist\mcp-file-tool.exe .\cmd\mcp-file-tool
```

Expected: all commands exit 0 with no failures.

- [ ] **Step 3: Publish**

Push the `codex/add-directory-mcp` branch and create a draft pull request whose
summary describes recursive import, status-safe deletion, durable CSV logging,
and verification evidence.
