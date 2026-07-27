package index

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTransitionAllowsOnlyLifecycleEdges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to Status
		wantErr  bool
	}{
		{"uploaded", OnFileNotRemote, OnFileAndRemote, false},
		{"removed", OnFileAndRemote, RemoteOnly, false},
		{"skip verification", OnFileNotRemote, RemoteOnly, true},
		{"restore remote", RemoteOnly, OnFileNotRemote, true},
		{"same state", OnFileNotRemote, OnFileNotRemote, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTransition(tc.from, tc.to)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTransition(%q, %q) error = %v, wantErr %v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

func TestOpenBuildsMissingIndexOnce(t *testing.T) {
	workspace := t.TempDir()
	indexPath := filepath.Join(workspace, "file-index.json")
	mustIndexWrite(t, filepath.Join(workspace, "b.TXT"), "bravo")
	mustIndexWrite(t, filepath.Join(workspace, "nested", "a.pdf"), "alpha")
	mustIndexWrite(t, filepath.Join(workspace, ".mcp-copy-ignored.tmp"), "temp")
	mustIndexWrite(t, filepath.Join(workspace, ".mcp-rclone-files-ignored.txt"), "temp")
	mustIndexWrite(t, filepath.Join(workspace, "logs", "add_directory", "run.csv"), "audit")

	store, err := Open(indexPath, workspace, testFingerprint)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got := store.Snapshot()
	want := []Entry{
		{RelativePath: "b.TXT", FileName: "b.TXT", FileType: ".txt", Checksum: "xxh64:0000000000000005", Status: OnFileNotRemote},
		{RelativePath: "nested/a.pdf", FileName: "a.pdf", FileType: ".pdf", Checksum: "xxh64:0000000000000005", Status: OnFileNotRemote},
	}
	if got.Version != 1 || !reflect.DeepEqual(got.Files, want) {
		t.Fatalf("Snapshot() = %#v, want version 1 files %#v", got, want)
	}

	mustIndexWrite(t, filepath.Join(workspace, "later.txt"), "later")
	reopened, err := Open(indexPath, workspace, testFingerprint)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if len(reopened.Snapshot().Files) != 2 {
		t.Fatalf("existing index was unexpectedly rescanned: %#v", reopened.Snapshot())
	}
}

func TestOpenRejectsCorruptExistingIndex(t *testing.T) {
	workspace := t.TempDir()
	indexPath := filepath.Join(workspace, "file-index.json")
	original := []byte("{broken")
	if err := os.WriteFile(indexPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(indexPath, workspace, testFingerprint)
	if err == nil {
		t.Fatal("Open() error = nil, want corrupt index error")
	}
	after, readErr := os.ReadFile(indexPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(after, original) {
		t.Fatalf("corrupt index was modified: %q", after)
	}
}

func TestMutationPersistsValidJSONImmediately(t *testing.T) {
	workspace := t.TempDir()
	indexPath := filepath.Join(workspace, "file-index.json")
	store, err := Open(indexPath, workspace, testFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		RelativePath: "nested/a.txt", FileName: "a.txt", FileType: ".txt",
		Checksum: "xxh64:0123456789abcdef", Status: OnFileNotRemote,
	}
	if err := store.Add(entry); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	assertDiskStatus(t, indexPath, OnFileNotRemote)

	if err := store.Transition(entry.RelativePath, OnFileNotRemote, OnFileAndRemote); err != nil {
		t.Fatalf("first Transition() error = %v", err)
	}
	assertDiskStatus(t, indexPath, OnFileAndRemote)

	if err := store.Transition(entry.RelativePath, OnFileAndRemote, RemoteOnly); err != nil {
		t.Fatalf("second Transition() error = %v", err)
	}
	assertDiskStatus(t, indexPath, RemoteOnly)
}

func TestStoreRejectsDuplicatePathsAndWrongExpectedStatus(t *testing.T) {
	workspace := t.TempDir()
	store, err := Open(filepath.Join(workspace, "index.json"), workspace, testFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{
		RelativePath: "a.txt", FileName: "a.txt", FileType: ".txt",
		Checksum: "xxh64:0123456789abcdef", Status: OnFileNotRemote,
	}
	if err := store.Add(entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(entry); err == nil {
		t.Fatal("duplicate Add() error = nil")
	}
	err = store.Transition("a.txt", OnFileAndRemote, RemoteOnly)
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("Transition() error = %v, want expected-status error", err)
	}
}

func testFingerprint(path string) (string, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("not regular")
	}
	return strings.ToLower(filepath.Ext(path)), "xxh64:" + leftPadHex(info.Size()), nil
}

func leftPadHex(value int64) string {
	s := strings.ToLower(string("0123456789abcdef"[value&15]))
	return strings.Repeat("0", 16-len(s)) + s
}

func assertDiskStatus(t *testing.T, path string, want Status) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("persisted index is invalid JSON: %v", err)
	}
	if len(doc.Files) != 1 || doc.Files[0].Status != want {
		t.Fatalf("persisted files = %#v, want status %q", doc.Files, want)
	}
}

func mustIndexWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
