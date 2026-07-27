package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/index"
	"mcp-file-tool/internal/syncer"
)

func TestServerListsExpectedTools(t *testing.T) {
	session := newTestSession(t)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"add_directory", "add_file", "sync_files"}) {
		t.Fatalf("tools = %#v", names)
	}
}

func TestAddFileToolRequiresOnlyPathAndReturnsStructuredResult(t *testing.T) {
	fixture := newMCPFixture(t)
	sourcePath := filepath.Join(fixture.source, "KlantA", "rapport.txt")
	mustMCPWrite(t, sourcePath, "report")
	session := connectTestServer(t, New(fixture.add, fixture.sync))

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "add_file", Arguments: map[string]any{"path": sourcePath},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("add_file returned tool error: %#v", result.Content)
	}
	var output files.AddResult
	decodeStructured(t, result.StructuredContent, &output)
	if !output.New || output.Entry.RelativePath != "KlantA/rapport.txt" {
		t.Fatalf("structured output = %#v", output)
	}
}

func TestAddDirectoryToolRequiresOnlyPathAndReturnsStructuredResult(t *testing.T) {
	fixture := newMCPFixture(t)
	selected := filepath.Join(fixture.source, "KlantA")
	sourcePath := filepath.Join(selected, "nested", "rapport.txt")
	mustMCPWrite(t, sourcePath, "report")
	session := connectTestServer(t, New(fixture.add, fixture.sync))

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "add_directory", Arguments: map[string]any{"path": selected},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("add_directory returned tool error: %#v", result.Content)
	}
	var output files.AddDirectoryResult
	decodeStructured(t, result.StructuredContent, &output)
	if output.Scanned != 1 || output.Added != 1 || output.Failed != 0 {
		t.Fatalf("structured output = %#v", output)
	}
	if output.ErrorLogPath == "" {
		t.Fatalf("error_log_path = empty")
	}
}

func TestSyncFilesToolAcceptsEmptyObjectAndReturnsCounts(t *testing.T) {
	fixture := newMCPFixture(t)
	session := connectTestServer(t, New(fixture.add, fixture.sync))

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sync_files", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("sync_files returned tool error: %#v", result.Content)
	}
	var output syncer.SyncResult
	decodeStructured(t, result.StructuredContent, &output)
	if output.Selected != 0 || output.Failed != 0 {
		t.Fatalf("structured output = %#v", output)
	}
}

type mcpFixture struct {
	source string
	add    *files.AddService
	sync   *syncer.Service
}

func newMCPFixture(t *testing.T) mcpFixture {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "source")
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(workspace, "index.json"), workspace, files.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	opMu := &sync.Mutex{}
	return mcpFixture{
		source: source,
		add:    files.NewAddService(source, workspace, store, opMu),
		sync:   syncer.NewService(workspace, store, emptyRemote{}, opMu),
	}
}

type emptyRemote struct{}

func (emptyRemote) Copy(context.Context, []string) error { return nil }

func (emptyRemote) Check(_ context.Context, paths []string) (syncer.CheckReport, error) {
	return syncer.CheckReport{Files: make(map[string]syncer.CheckState, len(paths))}, nil
}

func newTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	fixture := newMCPFixture(t)
	return connectTestServer(t, New(fixture.add, fixture.sync))
}

func connectTestServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func decodeStructured(t *testing.T, value any, target any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func mustMCPWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
