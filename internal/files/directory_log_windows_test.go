//go:build windows

package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDirectoryErrorLogRejectsJunctionComponent(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	logsLink := filepath.Join(workspace, "logs")
	command := exec.Command("cmd", "/c", "mklink", "/J", logsLink, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junctions unavailable: %v: %s", err, output)
	}

	log, err := newDirectoryErrorLog(workspace)
	if err == nil {
		_ = log.Close()
		t.Fatal("newDirectoryErrorLog() error = nil, want junction rejection")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("log escaped workspace through junction: %#v", entries)
	}
}

func TestAddDirectorySkipsJunctionWithoutFollowingIt(t *testing.T) {
	source, workspace, _, service := newAddFixture(t)
	selected := filepath.Join(source, "selected")
	outside := t.TempDir()
	mustFilesWrite(t, filepath.Join(outside, "outside.txt"), "outside")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(selected, "junction")
	command := exec.Command("cmd", "/c", "mklink", "/J", junction, outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junctions unavailable: %v: %s", err, output)
	}

	result, err := service.AddDirectory(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 0 || result.Skipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(workspace, "selected", "junction", "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("junction target was staged or stat failed: %v", err)
	}
}
