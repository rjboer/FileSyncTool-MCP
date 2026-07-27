package files

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"mcp-file-tool/internal/index"
)

type AddResult struct {
	Entry index.Entry `json:"entry"`
	New   bool        `json:"new"`
}

type AddService struct {
	sourceRoot    string
	workspaceRoot string
	store         *index.Store
	opMu          *sync.Mutex
	removeSource  func(string) error
}

func NewAddService(sourceRoot, workspaceRoot string, store *index.Store, opMu *sync.Mutex) *AddService {
	return &AddService{
		sourceRoot: sourceRoot, workspaceRoot: workspaceRoot,
		store: store, opMu: opMu, removeSource: os.Remove,
	}
}

func (s *AddService) Add(ctx context.Context, sourcePath string) (AddResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.addLocked(ctx, sourcePath)
}

func (s *AddService) addLocked(ctx context.Context, sourcePath string) (AddResult, error) {
	if err := ctx.Err(); err != nil {
		return AddResult{}, err
	}
	if !filepath.IsAbs(sourcePath) {
		return AddResult{}, fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return AddResult{}, fmt.Errorf("stat source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return AddResult{}, fmt.Errorf("source is not a regular file")
	}
	relative, err := RelativeWithin(s.sourceRoot, sourcePath)
	if err != nil {
		return AddResult{}, err
	}
	fileType, checksum, err := Fingerprint(sourcePath)
	if err != nil {
		return AddResult{}, fmt.Errorf("fingerprint source: %w", err)
	}
	if known, found := s.store.FindIdentity(fileType, checksum); found {
		return AddResult{Entry: known, New: false}, nil
	}

	target, err := WorkspacePath(s.workspaceRoot, relative)
	if err != nil {
		return AddResult{}, err
	}
	created, err := s.ensureVerifiedCopy(sourcePath, target, checksum)
	if err != nil {
		return AddResult{}, err
	}
	entry := index.Entry{
		RelativePath: relative, FileName: filepath.Base(sourcePath),
		FileType: fileType, Checksum: checksum, Status: index.OnFileNotRemote,
	}
	if err := s.store.Add(entry); err != nil {
		if created {
			_ = os.Remove(target)
			_ = RemoveEmptyParents(filepath.Dir(target), s.workspaceRoot)
		}
		return AddResult{}, fmt.Errorf("index copied file: %w", err)
	}
	return AddResult{Entry: entry, New: true}, nil
}

func (s *AddService) ensureVerifiedCopy(sourcePath, targetPath, sourceChecksum string) (bool, error) {
	if info, err := os.Stat(targetPath); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("destination exists and is not a regular file")
		}
		_, targetChecksum, fingerprintErr := Fingerprint(targetPath)
		if fingerprintErr != nil {
			return false, fmt.Errorf("fingerprint destination: %w", fingerprintErr)
		}
		if targetChecksum != sourceChecksum {
			return false, fmt.Errorf("destination already exists with different content")
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat destination: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return false, fmt.Errorf("create destination directories: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(targetPath), ".mcp-copy-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temporary copy: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	source, err := os.Open(sourcePath)
	if err != nil {
		return false, fmt.Errorf("open source: %w", err)
	}
	_, copyErr := io.Copy(temp, source)
	closeSourceErr := source.Close()
	if copyErr != nil {
		return false, fmt.Errorf("copy source: %w", copyErr)
	}
	if closeSourceErr != nil {
		return false, fmt.Errorf("close source: %w", closeSourceErr)
	}
	if err := temp.Sync(); err != nil {
		return false, fmt.Errorf("flush temporary copy: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("close temporary copy: %w", err)
	}
	_, copiedChecksum, err := Fingerprint(tempPath)
	if err != nil {
		return false, fmt.Errorf("fingerprint temporary copy: %w", err)
	}
	if copiedChecksum != sourceChecksum {
		return false, fmt.Errorf("copied checksum does not match source")
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return false, fmt.Errorf("publish copied file: %w", err)
	}
	cleanup = false
	return true, nil
}
