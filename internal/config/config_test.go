package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesAndValidates(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(base, "workspace")
	exeDir := filepath.Join(base, "bin")
	mustMkdirAll(t, source)
	mustMkdirAll(t, workspace)
	mustMkdirAll(t, exeDir)
	mustWriteFile(t, filepath.Join(exeDir, "rclone.exe"), "binary")

	t.Setenv("TEST_S3_ID", "id-value")
	t.Setenv("TEST_S3_SECRET", "secret-value")
	configPath := filepath.Join(exeDir, "config.json")
	writeConfig(t, configPath, testFileConfig{
		SourceRoot:    source,
		WorkspaceRoot: workspace,
		IndexPath:     filepath.Join(workspace, "file-index.json"),
		Remote: testRemoteConfig{
			Endpoint:           "https://s3.example.com",
			Region:             "eu-central-1",
			Bucket:             "documents",
			Prefix:             "/archive/2026/",
			AccessKeyIDEnv:     "TEST_S3_ID",
			SecretAccessKeyEnv: "TEST_S3_SECRET",
		},
	})

	got, err := Load(configPath, exeDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceRoot != filepath.Clean(wantSource) {
		t.Fatalf("SourceRoot = %q, want %q", got.SourceRoot, filepath.Clean(wantSource))
	}
	if got.WorkspaceRoot != filepath.Clean(wantWorkspace) {
		t.Fatalf("WorkspaceRoot = %q, want %q", got.WorkspaceRoot, filepath.Clean(wantWorkspace))
	}
	if got.RclonePath != filepath.Join(exeDir, "rclone.exe") {
		t.Fatalf("RclonePath = %q", got.RclonePath)
	}
	if got.Remote.Prefix != "archive/2026" {
		t.Fatalf("Prefix = %q, want archive/2026", got.Remote.Prefix)
	}
	if got.Remote.AccessKeyID != "id-value" || got.Remote.SecretAccessKey != "secret-value" {
		t.Fatal("credentials were not loaded from the configured environment variables")
	}
}

func TestLoadRejectsMissingSecretEnvironmentVariableWithoutLeakingCredentials(t *testing.T) {
	base, configPath, exeDir, cfg := validFixture(t)
	_ = base
	t.Setenv("TEST_S3_ID", "sensitive-id")
	cfg.Remote.SecretAccessKeyEnv = "MISSING_SECRET_FOR_TEST"
	writeConfig(t, configPath, cfg)

	_, err := Load(configPath, exeDir)
	if err == nil {
		t.Fatal("Load() error = nil, want missing secret error")
	}
	if !strings.Contains(err.Error(), "MISSING_SECRET_FOR_TEST") {
		t.Fatalf("error %q does not identify the missing environment variable", err)
	}
	if strings.Contains(err.Error(), "sensitive-id") {
		t.Fatalf("error leaked credential: %q", err)
	}
}

func TestLoadRejectsWorkspaceInsideSourceRoot(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(source, "staging")
	exeDir := filepath.Join(base, "bin")
	mustMkdirAll(t, workspace)
	mustMkdirAll(t, exeDir)
	mustWriteFile(t, filepath.Join(exeDir, "rclone.exe"), "binary")
	t.Setenv("TEST_S3_ID", "id")
	t.Setenv("TEST_S3_SECRET", "secret")
	configPath := filepath.Join(exeDir, "config.json")
	writeConfig(t, configPath, testFileConfig{
		SourceRoot: source, WorkspaceRoot: workspace,
		IndexPath: filepath.Join(workspace, "file-index.json"),
		Remote: testRemoteConfig{
			Endpoint: "https://s3.example.com", Region: "eu-central-1", Bucket: "documents",
			AccessKeyIDEnv: "TEST_S3_ID", SecretAccessKeyEnv: "TEST_S3_SECRET",
		},
	})

	_, err := Load(configPath, exeDir)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "overlap") {
		t.Fatalf("Load() error = %v, want overlap error", err)
	}
}

func TestLoadRejectsUnknownJSONField(t *testing.T) {
	_, configPath, exeDir, _ := validFixture(t)
	t.Setenv("TEST_S3_ID", "id")
	t.Setenv("TEST_S3_SECRET", "secret")
	mustWriteFile(t, configPath, `{
		"source_root":"x","workspace_root":"y","index_path":"z.json",
		"remote":{"endpoint":"e","region":"r","bucket":"b","access_key_id_env":"TEST_S3_ID","secret_access_key_env":"TEST_S3_SECRET"},
		"unexpected":true
	}`)

	_, err := Load(configPath, exeDir)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

type testFileConfig struct {
	SourceRoot    string           `json:"source_root"`
	WorkspaceRoot string           `json:"workspace_root"`
	IndexPath     string           `json:"index_path"`
	Remote        testRemoteConfig `json:"remote"`
}

type testRemoteConfig struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix,omitempty"`
	AccessKeyIDEnv     string `json:"access_key_id_env"`
	SecretAccessKeyEnv string `json:"secret_access_key_env"`
}

func validFixture(t *testing.T) (base, configPath, exeDir string, cfg testFileConfig) {
	t.Helper()
	base = t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(base, "workspace")
	exeDir = filepath.Join(base, "bin")
	mustMkdirAll(t, source)
	mustMkdirAll(t, workspace)
	mustMkdirAll(t, exeDir)
	mustWriteFile(t, filepath.Join(exeDir, "rclone.exe"), "binary")
	configPath = filepath.Join(exeDir, "config.json")
	cfg = testFileConfig{
		SourceRoot: source, WorkspaceRoot: workspace,
		IndexPath: filepath.Join(workspace, "file-index.json"),
		Remote: testRemoteConfig{
			Endpoint: "https://s3.example.com", Region: "eu-central-1", Bucket: "documents",
			AccessKeyIDEnv: "TEST_S3_ID", SecretAccessKeyEnv: "TEST_S3_SECRET",
		},
	}
	return base, configPath, exeDir, cfg
}

func writeConfig(t *testing.T, path string, cfg testFileConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
