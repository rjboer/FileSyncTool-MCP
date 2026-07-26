package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureFileCreatesIndentedTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	created, err := EnsureFile(path)
	if err != nil || !created {
		t.Fatalf("EnsureFile() = %v, %v; want true, nil", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"source_root\": \"\",\n  \"workspace_root\": \"\",\n  \"index_path\": \"\",\n  \"remote\": {\n    \"endpoint\": \"\",\n    \"region\": \"\",\n    \"bucket\": \"\",\n    \"prefix\": \"\",\n    \"access_key_id_env\": \"MCP_S3_ACCESS_KEY_ID\",\n    \"secret_access_key_env\": \"MCP_S3_SECRET_ACCESS_KEY\"\n  }\n}\n"
	if string(data) != want {
		t.Fatalf("config = %q, want %q", data, want)
	}
}

func TestEnsureFileNeverOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureFile(path)
	if err != nil || created {
		t.Fatalf("EnsureFile() = %v, %v; want false, nil", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep-me" {
		t.Fatalf("existing config changed to %q", data)
	}
}

func TestEnsureFileConcurrentCallsCreateExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := EnsureFile(path)
			results <- result{created: created, err: err}
		}()
	}
	wg.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Errorf("EnsureFile() concurrent error = %v", result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("created config is invalid JSON: %q", data)
	}
}

func TestEnsureFileReturnsCreateError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "config.json")

	created, err := EnsureFile(path)
	if err == nil || created {
		t.Fatalf("EnsureFile() = %v, %v; want false and create error", created, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial config exists or stat failed: %v", statErr)
	}
}
