package syncer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/index"
)

func TestLocalRcloneRoundTrip(t *testing.T) {
	rclonePath := os.Getenv("MCP_TEST_RCLONE_EXE")
	if rclonePath == "" {
		t.Skip("set MCP_TEST_RCLONE_EXE to run the rclone integration test")
	}
	if info, err := os.Stat(rclonePath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("MCP_TEST_RCLONE_EXE is not a regular file: %v", err)
	}

	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	workspaceRoot := filepath.Join(base, "workspace")
	remoteRoot := filepath.Join(base, "remote")
	for _, directory := range []string{sourceRoot, workspaceRoot, remoteRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := index.Open(filepath.Join(workspaceRoot, "file-index.json"), workspaceRoot, files.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	opMu := &sync.Mutex{}
	addService := files.NewAddService(sourceRoot, workspaceRoot, store, opMu)
	remote := &RcloneRemote{
		workspaceRoot: workspaceRoot,
		rclonePath:    rclonePath,
		remoteName:    "mcp-local",
		remoteConfig:  "[mcp-local]\ntype = local\n",
		destination:   "mcp-local:" + filepath.ToSlash(remoteRoot),
		runner:        ExecRunner{},
	}
	syncService := NewService(workspaceRoot, store, remote, opMu)

	sourcePath := filepath.Join(sourceRoot, "KlantA", "nested", "a.txt")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("round-trip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := addService.Add(context.Background(), sourcePath); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	result := syncService.Sync(context.Background())
	if result.Failed != 0 || result.Verified != 1 || result.Deleted != 1 {
		t.Fatalf("Sync() = %#v", result)
	}
	remotePath := filepath.Join(remoteRoot, "KlantA", "nested", "a.txt")
	content, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatalf("read remote file: %v", err)
	}
	if string(content) != "round-trip" {
		t.Fatalf("remote content = %q", content)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "KlantA", "nested", "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("local staging copy still exists or stat failed: %v", err)
	}
	if got := store.Snapshot().Files[0].Status; got != index.RemoteOnly {
		t.Fatalf("index status = %q, want RemoteOnly", got)
	}
}
