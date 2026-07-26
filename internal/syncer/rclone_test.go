package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mcp-file-tool/internal/config"
)

func TestRcloneCopyUsesFilesFromRawAndDirectExecutable(t *testing.T) {
	cfg := rcloneTestConfig(t)
	runner := &recordingRunner{}
	remote := NewRcloneRemote(cfg, runner)

	if err := remote.Copy(context.Background(), []string{"Klant A/report one.pdf", "nested/two.txt"}); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.executable != cfg.RclonePath {
		t.Fatalf("executable = %q, want %q", call.executable, cfg.RclonePath)
	}
	wantPrefix := []string{"copy", cfg.WorkspaceRoot, "mcp-s3:documents/archive"}
	if !slices.Equal(call.args[:3], wantPrefix) {
		t.Fatalf("argument prefix = %#v, want %#v", call.args[:3], wantPrefix)
	}
	if !hasFlag(call.args, "--files-from-raw") || !hasFlag(call.args, "--config") || !slices.Contains(call.args, "--no-traverse") {
		t.Fatalf("copy arguments missing safe flags: %#v", call.args)
	}
	if runner.fileList != "Klant A/report one.pdf\nnested/two.txt\n" {
		t.Fatalf("files-from content = %q", runner.fileList)
	}
	if !strings.Contains(runner.config, "access_key_id = id-value") ||
		!strings.Contains(runner.config, "secret_access_key = secret-value") {
		t.Fatalf("temporary config did not contain runtime credentials: %q", runner.config)
	}
	for _, path := range runner.temporaryPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary file %q still exists or stat failed: %v", path, err)
		}
	}
}

func TestRcloneCheckParsesCombinedReportPerFile(t *testing.T) {
	cfg := rcloneTestConfig(t)
	runner := &recordingRunner{
		combined: "= a.txt\n+ b.txt\n* c.txt\n! d.txt\n",
	}
	remote := NewRcloneRemote(cfg, runner)

	report, err := remote.Check(context.Background(), []string{"a.txt", "b.txt", "c.txt", "d.txt", "unreported.txt"})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	want := map[string]CheckState{
		"a.txt": Match, "b.txt": Missing, "c.txt": Different,
		"d.txt": CheckError, "unreported.txt": CheckError,
	}
	for path, state := range want {
		if report.Files[path] != state {
			t.Errorf("state[%q] = %q, want %q", path, report.Files[path], state)
		}
	}
}

func TestRcloneTemporaryFilesAreRemovedAndErrorsAreRedacted(t *testing.T) {
	cfg := rcloneTestConfig(t)
	runner := &recordingRunner{
		result: CommandResult{Stderr: "failed id-value secret-value"},
		err:    errors.New("process secret-value failed"),
	}
	remote := NewRcloneRemote(cfg, runner)

	err := remote.Copy(context.Background(), []string{"a.txt"})
	if err == nil {
		t.Fatal("Copy() error = nil")
	}
	if strings.Contains(err.Error(), "id-value") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("Copy() leaked credentials: %v", err)
	}
	for _, path := range runner.temporaryPaths {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("temporary file %q still exists or stat failed: %v", path, statErr)
		}
	}
}

func TestRcloneRejectsConfigurationLineInjectionBeforeRunning(t *testing.T) {
	cfg := rcloneTestConfig(t)
	cfg.Remote.Endpoint = "https://s3.example.com\nsecret_access_key = injected"
	runner := &recordingRunner{}
	remote := NewRcloneRemote(cfg, runner)

	err := remote.Copy(context.Background(), []string{"a.txt"})
	if err == nil {
		t.Fatal("Copy() error = nil, want unsafe configuration rejection")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner was called with unsafe configuration: %#v", runner.calls)
	}
}

type runnerCall struct {
	executable string
	args       []string
	env        []string
}

type recordingRunner struct {
	calls          []runnerCall
	result         CommandResult
	err            error
	combined       string
	config         string
	fileList       string
	temporaryPaths []string
}

func (r *recordingRunner) Run(_ context.Context, executable string, args, env []string) (CommandResult, error) {
	r.calls = append(r.calls, runnerCall{
		executable: executable, args: append([]string(nil), args...), env: append([]string(nil), env...),
	})
	if path := flagValue(args, "--config"); path != "" {
		data, _ := os.ReadFile(path)
		r.config = string(data)
		r.temporaryPaths = append(r.temporaryPaths, path)
	}
	if path := flagValue(args, "--files-from-raw"); path != "" {
		data, _ := os.ReadFile(path)
		r.fileList = string(data)
		r.temporaryPaths = append(r.temporaryPaths, path)
	}
	if path := flagValue(args, "--combined"); path != "" {
		r.temporaryPaths = append(r.temporaryPaths, path)
		if writeErr := os.WriteFile(path, []byte(r.combined), 0o600); writeErr != nil {
			return CommandResult{}, writeErr
		}
	}
	return r.result, r.err
}

func rcloneTestConfig(t *testing.T) config.Resolved {
	t.Helper()
	workspace := t.TempDir()
	rclonePath := filepath.Join(t.TempDir(), "rclone.exe")
	if err := os.WriteFile(rclonePath, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Resolved{
		WorkspaceRoot: workspace, RclonePath: rclonePath,
		Remote: config.Remote{
			Endpoint: "https://s3.example.com", Region: "eu-central-1",
			Bucket: "documents", Prefix: "archive",
			AccessKeyID: "id-value", SecretAccessKey: "secret-value",
		},
	}
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func flagValue(args []string, flag string) string {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
