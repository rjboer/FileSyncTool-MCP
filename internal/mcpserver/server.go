package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/syncer"
)

type AddFileInput struct {
	Path string `json:"path" jsonschema:"absolute path to a regular file inside source_root"`
}

type SyncFilesInput struct{}

func New(add *files.AddService, syncService *syncer.Service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "mcp-file-tool", Version: "1.0.0"},
		nil,
	)
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "add_file",
			Description: "Copy and index one file from the configured source_root while preserving its relative path.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AddFileInput) (*mcp.CallToolResult, files.AddResult, error) {
			result, err := add.Add(ctx, input.Path)
			return nil, result, err
		},
	)
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "sync_files",
			Description: "Upload, verify, and locally remove all indexed OnFileNotRemote files.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ SyncFilesInput) (*mcp.CallToolResult, syncer.SyncResult, error) {
			return nil, syncService.Sync(ctx), nil
		},
	)
	return server
}
