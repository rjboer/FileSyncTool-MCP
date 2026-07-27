package files

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mcp-file-tool/internal/index"
)

func TestFingerprintUsesLowercaseExtensionAndXXHash64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Report.PDF")
	mustFilesWrite(t, path, "abc")

	fileType, checksum, err := Fingerprint(path)
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	if fileType != ".pdf" {
		t.Fatalf("fileType = %q, want .pdf", fileType)
	}
	if checksum != "xxh64:44bc2cf5ad770999" {
		t.Fatalf("checksum = %q, want known xxHash64 vector", checksum)
	}
}

func TestAddPreservesRelativeTreeAndPersistsEntry(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	sourcePath := filepath.Join(source, "KlantA", "rapport.pdf")
	mustFilesWrite(t, sourcePath, "report-body")

	result, err := service.Add(context.Background(), sourcePath)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !result.New || result.Entry.RelativePath != "KlantA/rapport.pdf" {
		t.Fatalf("Add() result = %#v", result)
	}
	if result.Entry.Status != index.OnFileNotRemote {
		t.Fatalf("status = %q", result.Entry.Status)
	}
	copied, err := os.ReadFile(filepath.Join(workspace, "KlantA", "rapport.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != "report-body" {
		t.Fatalf("copied content = %q", copied)
	}
	if len(store.Snapshot().Files) != 1 {
		t.Fatalf("index files = %#v", store.Snapshot().Files)
	}
}

func TestAddReturnsKnownIdentityWithoutSecondCopy(t *testing.T) {
	source, workspace, _, service := newAddFixture(t)
	first := filepath.Join(source, "first", "a.txt")
	second := filepath.Join(source, "second", "b.txt")
	mustFilesWrite(t, first, "duplicate")
	mustFilesWrite(t, second, "duplicate")

	firstResult, err := service.Add(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := service.Add(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.New {
		t.Fatalf("second Add() = %#v, want existing identity", secondResult)
	}
	if secondResult.Entry.RelativePath != firstResult.Entry.RelativePath {
		t.Fatalf("duplicate returned %q, want %q", secondResult.Entry.RelativePath, firstResult.Entry.RelativePath)
	}
	if _, err := os.Stat(filepath.Join(workspace, "second", "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("duplicate destination exists or stat failed: %v", err)
	}
}

func TestAddRejectsPathOutsideSourceRoot(t *testing.T) {
	_, _, store, service := newAddFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mustFilesWrite(t, outside, "outside")

	if _, err := service.Add(context.Background(), outside); err == nil {
		t.Fatal("Add() error = nil, want outside-root rejection")
	}
	if len(store.Snapshot().Files) != 0 {
		t.Fatalf("index was changed: %#v", store.Snapshot())
	}
}

func TestAddRejectsConflictingDestinationWithoutOverwrite(t *testing.T) {
	source, workspace, store, service := newAddFixture(t)
	sourcePath := filepath.Join(source, "same.txt")
	targetPath := filepath.Join(workspace, "same.txt")
	mustFilesWrite(t, sourcePath, "new-content")
	mustFilesWrite(t, targetPath, "existing-content")

	if _, err := service.Add(context.Background(), sourcePath); err == nil {
		t.Fatal("Add() error = nil, want destination conflict")
	}
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "existing-content" {
		t.Fatalf("destination was overwritten: %q", content)
	}
	if len(store.Snapshot().Files) != 0 {
		t.Fatalf("index was changed: %#v", store.Snapshot())
	}
}

func TestRemoveEmptyParentsStopsAtWorkspace(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEmptyParents(nested, workspace); err != nil {
		t.Fatalf("RemoveEmptyParents() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "a")); !os.IsNotExist(err) {
		t.Fatalf("nested parent still exists or stat failed: %v", err)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("workspace was removed: info=%v err=%v", info, err)
	}
}

func TestDirectoryWithinAcceptsRootAndNestedDirectory(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{root, nested} {
		resolved, err := DirectoryWithin(root, candidate)
		if err != nil {
			t.Fatalf("DirectoryWithin(%q) error = %v", candidate, err)
		}
		if !filepath.IsAbs(resolved) {
			t.Fatalf("DirectoryWithin(%q) = %q, want absolute path", candidate, resolved)
		}
	}
}

func TestDirectoryWithinRejectsInvalidRoots(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	mustFilesWrite(t, file, "x")

	cases := []string{
		"relative",
		file,
		filepath.Join(root, "missing"),
		t.TempDir(),
	}
	for _, candidate := range cases {
		if _, err := DirectoryWithin(root, candidate); err == nil {
			t.Errorf("DirectoryWithin(%q) error = nil", candidate)
		}
	}
}

func TestDirectoryWithinRejectsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := DirectoryWithin(root, link); err == nil {
		t.Fatal("DirectoryWithin() error = nil, want symlink rejection")
	}
}

func newAddFixture(t *testing.T) (string, string, *index.Store, *AddService) {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(workspace, "file-index.json"), workspace, Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAddService(source, workspace, store, &sync.Mutex{})
	return source, workspace, store, service
}

func mustFilesWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
