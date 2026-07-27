package files

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDirectoryErrorLogWritesEscapedCSV(t *testing.T) {
	workspace := t.TempDir()
	log, err := newDirectoryErrorLog(workspace)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = log.Close()
		}
	}()
	path := log.Path()
	if !filepath.IsAbs(path) {
		t.Fatalf("Path() = %q, want absolute path", path)
	}
	wantParent := filepath.Join(workspace, "logs", "add_directory")
	resolvedWantParent, err := filepath.EvalSymlinks(wantParent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(filepath.Dir(path)), filepath.Clean(resolvedWantParent)) {
		t.Fatalf("Path() parent = %q, want %q", filepath.Dir(path), wantParent)
	}

	failure := directoryFailure{
		SourcePath:   `C:\source\a,b.txt`,
		RelativePath: "a,b.txt",
		Phase:        "remove",
		Err:          testDirectoryError(`locked, says "owner"`),
	}
	if err := log.Record(failure); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{
		"timestamp_utc", "run_id", "source_path",
		"relative_path", "phase", "error",
	}
	if len(rows) != 2 || !slices.Equal(rows[0], wantHeader) {
		t.Fatalf("rows = %#v", rows)
	}
	if _, err := time.Parse(time.RFC3339Nano, rows[1][0]); err != nil {
		t.Fatalf("timestamp = %q: %v", rows[1][0], err)
	}
	if rows[1][1] == "" ||
		rows[1][2] != failure.SourcePath ||
		rows[1][3] != failure.RelativePath ||
		rows[1][4] != failure.Phase ||
		rows[1][5] != failure.Err.Error() {
		t.Fatalf("failure row = %#v", rows[1])
	}
}

type testDirectoryError string

func (e testDirectoryError) Error() string { return string(e) }
