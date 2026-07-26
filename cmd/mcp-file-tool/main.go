package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-file-tool/internal/config"
	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/index"
	"mcp-file-tool/internal/mcpserver"
	"mcp-file-tool/internal/syncer"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(context.Background(), executable, os.Stderr); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, executablePath string, stderr io.Writer) error {
	exeDir := filepath.Dir(executablePath)
	configPath := filepath.Join(exeDir, "config.json")
	created, err := config.EnsureFile(configPath)
	if err != nil {
		return fmt.Errorf("ensure config: %w", err)
	}
	if created {
		_, err := fmt.Fprintln(stderr, "config.json is aangemaakt; vul het configuratiebestand in en start de applicatie opnieuw.")
		return err
	}
	cfg, err := config.Load(configPath, exeDir)
	if err != nil {
		return err
	}
	store, err := index.Open(cfg.IndexPath, cfg.WorkspaceRoot, files.Fingerprint)
	if err != nil {
		return err
	}
	opMu := &sync.Mutex{}
	addService := files.NewAddService(cfg.SourceRoot, cfg.WorkspaceRoot, store, opMu)
	remote := syncer.NewRcloneRemote(cfg, syncer.ExecRunner{})
	syncService := syncer.NewService(cfg.WorkspaceRoot, store, remote, opMu)
	server := mcpserver.New(addService, syncService)
	return server.Run(ctx, &mcp.StdioTransport{})
}
