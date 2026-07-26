package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/index"
)

func TestSyncVerifiedFilesTransitionAndDelete(t *testing.T) {
	workspace, store := newSyncStore(t)
	addSyncEntry(t, workspace, store, "a.txt", index.OnFileNotRemote)
	addSyncEntry(t, workspace, store, "nested/b.txt", index.OnFileNotRemote)
	remote := &fakeRemote{states: map[string]CheckState{
		"a.txt": Match, "nested/b.txt": Match,
	}}
	service := NewService(workspace, store, remote, &sync.Mutex{})

	result := service.Sync(context.Background())

	if result.Selected != 2 || result.Uploaded != 2 || result.Verified != 2 || result.Deleted != 2 || result.Failed != 0 {
		t.Fatalf("Sync() = %#v", result)
	}
	for _, entry := range store.Snapshot().Files {
		if entry.Status != index.RemoteOnly {
			t.Errorf("%q status = %q, want RemoteOnly", entry.RelativePath, entry.Status)
		}
		local, _ := files.WorkspacePath(workspace, entry.RelativePath)
		if _, err := os.Stat(local); !os.IsNotExist(err) {
			t.Errorf("local %q still exists or stat failed: %v", local, err)
		}
	}
}

func TestSyncKeepsUnverifiedFilesLocal(t *testing.T) {
	workspace, store := newSyncStore(t)
	for _, path := range []string{"missing.txt", "different.txt", "error.txt"} {
		addSyncEntry(t, workspace, store, path, index.OnFileNotRemote)
	}
	remote := &fakeRemote{states: map[string]CheckState{
		"missing.txt": Missing, "different.txt": Different, "error.txt": CheckError,
	}}
	service := NewService(workspace, store, remote, &sync.Mutex{})

	result := service.Sync(context.Background())

	if result.Verified != 0 || result.Deleted != 0 || result.Skipped != 3 {
		t.Fatalf("Sync() = %#v", result)
	}
	for _, entry := range store.Snapshot().Files {
		if entry.Status != index.OnFileNotRemote {
			t.Errorf("%q status = %q", entry.RelativePath, entry.Status)
		}
		local, _ := files.WorkspacePath(workspace, entry.RelativePath)
		if _, err := os.Stat(local); err != nil {
			t.Errorf("local %q missing: %v", local, err)
		}
	}
}

func TestSyncKeepsOnFileAndRemoteWhenRemoveFails(t *testing.T) {
	workspace, store := newSyncStore(t)
	addSyncEntry(t, workspace, store, "locked.txt", index.OnFileNotRemote)
	remote := &fakeRemote{states: map[string]CheckState{"locked.txt": Match}}
	service := NewService(workspace, store, remote, &sync.Mutex{})
	service.removeFile = func(string) error { return errors.New("locked") }

	result := service.Sync(context.Background())

	if result.Verified != 1 || result.Deleted != 0 || result.Failed != 1 {
		t.Fatalf("Sync() = %#v", result)
	}
	if got := store.Snapshot().Files[0].Status; got != index.OnFileAndRemote {
		t.Fatalf("status = %q, want OnFileAndRemote", got)
	}
}

func TestSyncRetriesCleanupWithoutUploadingOnFileAndRemote(t *testing.T) {
	workspace, store := newSyncStore(t)
	addSyncEntry(t, workspace, store, "cleanup.txt", index.OnFileAndRemote)
	remote := &fakeRemote{states: map[string]CheckState{}}
	service := NewService(workspace, store, remote, &sync.Mutex{})

	result := service.Sync(context.Background())

	if len(remote.copied) != 0 || result.Selected != 0 || result.Deleted != 1 {
		t.Fatalf("Sync() = %#v, copied = %#v", result, remote.copied)
	}
	if got := store.Snapshot().Files[0].Status; got != index.RemoteOnly {
		t.Fatalf("status = %q, want RemoteOnly", got)
	}
}

func TestSyncChecksPartialCopyResults(t *testing.T) {
	workspace, store := newSyncStore(t)
	addSyncEntry(t, workspace, store, "uploaded.txt", index.OnFileNotRemote)
	addSyncEntry(t, workspace, store, "failed.txt", index.OnFileNotRemote)
	remote := &fakeRemote{
		copyErr: errors.New("partial transfer"),
		states: map[string]CheckState{
			"uploaded.txt": Match, "failed.txt": Missing,
		},
	}
	service := NewService(workspace, store, remote, &sync.Mutex{})

	result := service.Sync(context.Background())

	if !remote.checked || result.Uploaded != 0 || result.Verified != 1 || result.Deleted != 1 || result.Skipped != 1 {
		t.Fatalf("Sync() = %#v, checked=%v", result, remote.checked)
	}
	statuses := map[string]index.Status{}
	for _, entry := range store.Snapshot().Files {
		statuses[entry.RelativePath] = entry.Status
	}
	if statuses["uploaded.txt"] != index.RemoteOnly || statuses["failed.txt"] != index.OnFileNotRemote {
		t.Fatalf("statuses = %#v", statuses)
	}
}

type fakeRemote struct {
	copied   []string
	checked  bool
	copyErr  error
	checkErr error
	states   map[string]CheckState
}

func (f *fakeRemote) Copy(_ context.Context, paths []string) error {
	f.copied = append([]string(nil), paths...)
	return f.copyErr
}

func (f *fakeRemote) Check(_ context.Context, paths []string) (CheckReport, error) {
	f.checked = true
	report := CheckReport{Files: make(map[string]CheckState, len(paths))}
	for _, path := range paths {
		report.Files[path] = f.states[path]
	}
	return report, f.checkErr
}

func newSyncStore(t *testing.T) (string, *index.Store) {
	t.Helper()
	workspace := t.TempDir()
	store, err := index.Open(filepath.Join(workspace, "file-index.json"), workspace, files.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return workspace, store
}

func addSyncEntry(t *testing.T, workspace string, store *index.Store, relative string, status index.Status) {
	t.Helper()
	local, err := files.WorkspacePath(workspace, relative)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("content-"+relative), 0o600); err != nil {
		t.Fatal(err)
	}
	fileType, checksum, err := files.Fingerprint(local)
	if err != nil {
		t.Fatal(err)
	}
	entry := index.Entry{
		RelativePath: relative, FileName: filepath.Base(local),
		FileType: fileType, Checksum: checksum, Status: index.OnFileNotRemote,
	}
	if err := store.Add(entry); err != nil {
		t.Fatal(err)
	}
	if status == index.OnFileAndRemote {
		if err := store.Transition(relative, index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
			t.Fatal(err)
		}
	}
}
