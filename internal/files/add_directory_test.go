package files

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mcp-file-tool/internal/index"
)

func TestAddDirectoryRecursivelyAddsUnknownFilesAndPreservesDirectories(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "KlantA")
	mustFilesWrite(t, filepath.Join(selected, "a.txt"), "a")
	mustFilesWrite(t, filepath.Join(selected, "nested", "b.txt"), "b")
	empty := filepath.Join(selected, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Added != 2 || result.Failed != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(store.Snapshot().Files) != 2 {
		t.Fatalf("index = %#v", store.Snapshot())
	}
	if info, err := os.Stat(empty); err != nil || !info.IsDir() {
		t.Fatalf("empty directory changed: info=%v err=%v", info, err)
	}
	for _, relative := range []string{"KlantA/a.txt", "KlantA/nested/b.txt"} {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(relative))); err != nil {
			t.Errorf("staged %q: %v", relative, err)
		}
	}
}

func TestAddDirectoryStatusDecisionsPreserveOrRemoveSource(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "KlantA")
	paths := map[string]string{
		"pending.txt": "pending",
		"both.txt":    "both",
		"remote.txt":  "remote",
	}
	for name, content := range paths {
		sourcePath := filepath.Join(selected, name)
		mustFilesWrite(t, sourcePath, content)
		if _, err := service.Add(context.Background(), sourcePath); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Transition("KlantA/both.txt", index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("KlantA/remote.txt", index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("KlantA/remote.txt", index.OnFileAndRemote, index.RemoteOnly); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "KlantA", "remote.txt")); err != nil {
		t.Fatal(err)
	}
	mustFilesWrite(t, filepath.Join(selected, "unknown.txt"), "unknown")
	empty := filepath.Join(selected, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 ||
		result.RetainedNotRemote != 1 ||
		result.RetainedOnAndRemote != 1 ||
		result.RemovedRemoteOnly != 1 {
		t.Fatalf("result = %#v", result)
	}
	for _, name := range []string{"pending.txt", "both.txt", "unknown.txt"} {
		if _, err := os.Stat(filepath.Join(selected, name)); err != nil {
			t.Errorf("retained source %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(selected, "remote.txt")); !os.IsNotExist(err) {
		t.Fatalf("RemoteOnly source still exists or stat failed: %v", err)
	}
	for _, directory := range []string{selected, empty} {
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			t.Errorf("directory changed: path=%q info=%v err=%v", directory, info, err)
		}
	}
}

func TestAddDirectoryDuplicateWithinBatchRetainsSecondSource(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "duplicates")
	first := filepath.Join(selected, "a.txt")
	second := filepath.Join(selected, "b.txt")
	mustFilesWrite(t, first, "same")
	mustFilesWrite(t, second, "same")

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Added != 1 || result.RetainedNotRemote != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(store.Snapshot().Files) != 1 {
		t.Fatalf("index = %#v", store.Snapshot())
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first source missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("second source missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "duplicates", "a.txt")); err != nil {
		t.Fatalf("first file was not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "duplicates", "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("duplicate was staged or stat failed: %v", err)
	}
}

func TestAddDirectoryContinuesAfterRemoveFailureAndLogsError(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	remoteSource := filepath.Join(selected, "a-remote.txt")
	laterSource := filepath.Join(selected, "z-later.txt")
	mustFilesWrite(t, remoteSource, "remote")
	if _, err := service.Add(context.Background(), remoteSource); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("batch/a-remote.txt", index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("batch/a-remote.txt", index.OnFileAndRemote, index.RemoteOnly); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "batch", "a-remote.txt")); err != nil {
		t.Fatal(err)
	}
	mustFilesWrite(t, laterSource, "later")
	service.removeSource = func(string) error { return errors.New("locked") }

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Added != 1 || result.RemovedRemoteOnly != 0 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(remoteSource); err != nil {
		t.Fatalf("failed source was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "batch", "z-later.txt")); err != nil {
		t.Fatalf("later source was not staged: %v", err)
	}
	rows := readDirectoryLog(t, result.ErrorLogPath)
	if len(rows) != 2 || rows[1][3] != "batch/a-remote.txt" || rows[1][4] != "remove" {
		t.Fatalf("log rows = %#v", rows)
	}
}

func TestAddDirectoryContinuesAfterDestinationConflictAndLogsError(t *testing.T) {
	source, workspace, _, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	conflict := filepath.Join(selected, "a-conflict.txt")
	later := filepath.Join(selected, "z-later.txt")
	mustFilesWrite(t, conflict, "source")
	mustFilesWrite(t, later, "later")
	mustFilesWrite(t, filepath.Join(workspace, "batch", "a-conflict.txt"), "different")

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Added != 1 {
		t.Fatalf("result = %#v", result)
	}
	if content, err := os.ReadFile(conflict); err != nil || string(content) != "source" {
		t.Fatalf("conflicting source changed: content=%q err=%v", content, err)
	}
	rows := readDirectoryLog(t, result.ErrorLogPath)
	if len(rows) != 2 || rows[1][3] != "batch/a-conflict.txt" || rows[1][4] != "add" {
		t.Fatalf("log rows = %#v", rows)
	}
}

func TestAddDirectoryLoggerCreationFailurePreventsMutation(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	mustFilesWrite(t, filepath.Join(selected, "file.txt"), "content")
	service.newDirectoryLog = func(string) (directoryErrorLog, error) {
		return nil, errors.New("log unavailable")
	}

	if _, err := service.AddDirectory(context.Background(), selected); err == nil {
		t.Fatal("AddDirectory() error = nil")
	}
	if len(store.Snapshot().Files) != 0 {
		t.Fatalf("index changed: %#v", store.Snapshot())
	}
	if _, err := os.Stat(filepath.Join(workspace, "batch", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("workspace changed or stat failed: %v", err)
	}
}

func TestAddDirectoryLogWriteFailureStopsLaterProcessing(t *testing.T) {
	source, workspace, _, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	mustFilesWrite(t, filepath.Join(selected, "a-conflict.txt"), "source")
	mustFilesWrite(t, filepath.Join(selected, "z-later.txt"), "later")
	mustFilesWrite(t, filepath.Join(workspace, "batch", "a-conflict.txt"), "different")
	service.newDirectoryLog = func(string) (directoryErrorLog, error) {
		return &failingDirectoryLog{recordErr: errors.New("disk full")}, nil
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err == nil {
		t.Fatal("AddDirectory() error = nil")
	}
	if result.Failed != 0 {
		t.Fatalf("result = %#v, failed error was not durably logged", result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "batch", "z-later.txt")); !os.IsNotExist(err) {
		t.Fatalf("later file was processed or stat failed: %v", err)
	}
}

func TestAddDirectoryCanceledContextStopsProcessing(t *testing.T) {
	source, _, store, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	mustFilesWrite(t, filepath.Join(selected, "file.txt"), "content")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := service.AddDirectory(ctx, selected)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AddDirectory() error = %v", err)
	}
	if result.Scanned != 0 || len(store.Snapshot().Files) != 0 {
		t.Fatalf("result = %#v index = %#v", result, store.Snapshot())
	}
}

func TestAddDirectoryEmptyRunCreatesHeaderOnlyLog(t *testing.T) {
	source, _, _, service := newAddFixture(t)
	selected := filepath.Join(source, "empty")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	rows := readDirectoryLog(t, result.ErrorLogPath)
	if len(rows) != 1 {
		t.Fatalf("log rows = %#v", rows)
	}
}

func TestAddDirectorySkipsSymbolicLinks(t *testing.T) {
	source, workspace, _, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	target := filepath.Join(source, "outside-selection.txt")
	mustFilesWrite(t, target, "target")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(selected, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || result.Scanned != 0 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "batch", "link.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was staged or stat failed: %v", err)
	}
}

func TestAddDirectoryChangedRemoteOnlyIdentityIsRetainedAndLogged(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	selected := filepath.Join(source, "batch")
	sourcePath := filepath.Join(selected, "remote.txt")
	mustFilesWrite(t, sourcePath, "remote")
	if _, err := service.Add(context.Background(), sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("batch/remote.txt", index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition("batch/remote.txt", index.OnFileAndRemote, index.RemoteOnly); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspace, "batch", "remote.txt")); err != nil {
		t.Fatal(err)
	}
	service.revalidateSource = func(string) (string, string, error) {
		return ".txt", "xxh64:0000000000000000", nil
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.RemovedRemoteOnly != 0 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("changed source was not retained: %v", err)
	}
	rows := readDirectoryLog(t, result.ErrorLogPath)
	if len(rows) != 2 || rows[1][4] != "revalidate" {
		t.Fatalf("log rows = %#v", rows)
	}
}

func TestAddDirectorySkipsLiveIndexInsideSourceRoot(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(base, "workspace")
	for _, directory := range []string{source, workspace} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	indexPath := filepath.Join(source, "file-index.json")
	store, err := index.Open(indexPath, workspace, Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAddService(source, workspace, store, &sync.Mutex{})
	mustFilesWrite(t, filepath.Join(source, "document.txt"), "document")

	result, err := service.AddDirectory(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Added != 1 || result.Skipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "file-index.json")); !os.IsNotExist(err) {
		t.Fatalf("live index was staged or stat failed: %v", err)
	}
	if len(store.Snapshot().Files) != 1 ||
		store.Snapshot().Files[0].RelativePath != "document.txt" {
		t.Fatalf("index = %#v", store.Snapshot())
	}
}

type failingDirectoryLog struct {
	recordErr error
}

func (*failingDirectoryLog) Path() string { return `C:\fake\log.csv` }

func (l *failingDirectoryLog) Record(directoryFailure) error { return l.recordErr }

func (*failingDirectoryLog) Close() error { return nil }

func readDirectoryLog(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
