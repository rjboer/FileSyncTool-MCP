package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCreatesConfigAndExitsBeforeServerInitialization(t *testing.T) {
	exeDir := t.TempDir()
	var stderr bytes.Buffer

	err := run(context.Background(), filepath.Join(exeDir, "mcp-file-tool.exe"), &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	const wantMessage = "config.json is aangemaakt; vul het configuratiebestand in en start de applicatie opnieuw.\n"
	if stderr.String() != wantMessage {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantMessage)
	}
	if _, err := os.Stat(filepath.Join(exeDir, "config.json")); err != nil {
		t.Fatalf("config was not created: %v", err)
	}
	// Er staat geen rclone.exe en er bestaan geen source/workspacepaden.
	// Verdere initialisatie zou deze test laten falen.
}

func TestRunLoadsExistingBootstrapConfigOnSecondStart(t *testing.T) {
	exeDir := t.TempDir()
	executable := filepath.Join(exeDir, "mcp-file-tool.exe")
	if err := run(context.Background(), executable, io.Discard); err != nil {
		t.Fatalf("first run error = %v", err)
	}

	err := run(context.Background(), executable, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "source_root is required") {
		t.Fatalf("second run error = %v, want source_root validation error", err)
	}
}

func TestRunDoesNotOverwriteExistingConfig(t *testing.T) {
	exeDir := t.TempDir()
	executable := filepath.Join(exeDir, "mcp-file-tool.exe")
	configPath := filepath.Join(exeDir, "config.json")
	const existing = `{"existing":true}`
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(context.Background(), executable, io.Discard)
	if err == nil {
		t.Fatal("run() error = nil, want invalid existing config error")
	}
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != existing {
		t.Fatalf("existing config changed to %q", data)
	}
}
