package files

import (
	"context"
	"os"
	"path/filepath"
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
