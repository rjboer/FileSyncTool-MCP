package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"mcp-file-tool/internal/files"
	"mcp-file-tool/internal/index"
)

type FileFailure struct {
	RelativePath string `json:"relative_path"`
	Error        string `json:"error"`
}

type SyncResult struct {
	Selected int           `json:"selected"`
	Uploaded int           `json:"uploaded"`
	Verified int           `json:"verified"`
	Deleted  int           `json:"deleted"`
	Skipped  int           `json:"skipped"`
	Failed   int           `json:"failed"`
	Failures []FileFailure `json:"failures,omitempty"`
}

type Service struct {
	workspaceRoot string
	store         *index.Store
	remote        Remote
	opMu          *sync.Mutex
	removeFile    func(string) error
}

func NewService(workspaceRoot string, store *index.Store, remote Remote, opMu *sync.Mutex) *Service {
	return &Service{
		workspaceRoot: workspaceRoot, store: store, remote: remote,
		opMu: opMu, removeFile: os.Remove,
	}
}

func (s *Service) Sync(ctx context.Context) SyncResult {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	var result SyncResult
	failures := make(map[string]string)
	if err := ctx.Err(); err != nil {
		addFailure(failures, "<sync>", err)
		return finishResult(result, failures)
	}

	snapshot := s.store.Snapshot()
	var pending, cleanup []index.Entry
	for _, entry := range snapshot.Files {
		switch entry.Status {
		case index.OnFileNotRemote:
			pending = append(pending, entry)
		case index.OnFileAndRemote:
			cleanup = append(cleanup, entry)
		}
	}
	sortEntries(pending)
	sortEntries(cleanup)
	result.Selected = len(pending)

	for _, entry := range cleanup {
		if s.removeAndFinalize(entry, &result, failures) {
			continue
		}
	}

	var candidates []index.Entry
	var paths []string
	for _, entry := range pending {
		local, err := files.WorkspacePath(s.workspaceRoot, entry.RelativePath)
		if err != nil {
			addFailure(failures, entry.RelativePath, err)
			continue
		}
		info, err := os.Stat(local)
		if err != nil {
			addFailure(failures, entry.RelativePath, fmt.Errorf("local file unavailable: %w", err))
			continue
		}
		if !info.Mode().IsRegular() {
			addFailure(failures, entry.RelativePath, fmt.Errorf("local path is not a regular file"))
			continue
		}
		candidates = append(candidates, entry)
		paths = append(paths, entry.RelativePath)
	}
	if len(paths) == 0 {
		return finishResult(result, failures)
	}

	copyErr := s.remote.Copy(ctx, paths)
	if copyErr == nil {
		result.Uploaded = len(paths)
	}
	report, checkErr := s.remote.Check(ctx, paths)
	for _, entry := range candidates {
		state, exists := report.Files[entry.RelativePath]
		if !exists {
			state = CheckError
		}
		if state != Match {
			result.Skipped++
			switch {
			case checkErr != nil:
				addFailure(failures, entry.RelativePath, fmt.Errorf("remote verification %s: %w", state, checkErr))
			case copyErr != nil:
				addFailure(failures, entry.RelativePath, fmt.Errorf("remote verification %s after copy error: %w", state, copyErr))
			default:
				addFailure(failures, entry.RelativePath, fmt.Errorf("remote verification result: %s", state))
			}
			continue
		}
		result.Verified++
		if err := s.store.Transition(entry.RelativePath, index.OnFileNotRemote, index.OnFileAndRemote); err != nil {
			addFailure(failures, entry.RelativePath, err)
			continue
		}
		entry.Status = index.OnFileAndRemote
		s.removeAndFinalize(entry, &result, failures)
	}
	return finishResult(result, failures)
}

func (s *Service) removeAndFinalize(entry index.Entry, result *SyncResult, failures map[string]string) bool {
	local, err := files.WorkspacePath(s.workspaceRoot, entry.RelativePath)
	if err != nil {
		addFailure(failures, entry.RelativePath, err)
		return false
	}
	removed := false
	err = s.removeFile(local)
	if err == nil {
		removed = true
	} else if os.IsNotExist(err) {
		removed = true
	} else {
		addFailure(failures, entry.RelativePath, fmt.Errorf("remove local file: %w", err))
		return false
	}
	if err := files.RemoveEmptyParents(filepath.Dir(local), s.workspaceRoot); err != nil {
		addFailure(failures, entry.RelativePath, fmt.Errorf("remove empty directories: %w", err))
	}
	if err := s.store.Transition(entry.RelativePath, index.OnFileAndRemote, index.RemoteOnly); err != nil {
		addFailure(failures, entry.RelativePath, err)
		return false
	}
	if removed {
		result.Deleted++
	}
	return true
}

func sortEntries(entries []index.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelativePath < entries[j].RelativePath
	})
}

func addFailure(failures map[string]string, path string, err error) {
	if _, exists := failures[path]; !exists {
		failures[path] = err.Error()
	}
}

func finishResult(result SyncResult, failures map[string]string) SyncResult {
	paths := make([]string, 0, len(failures))
	for path := range failures {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		result.Failures = append(result.Failures, FileFailure{RelativePath: path, Error: failures[path]})
	}
	result.Failed = len(result.Failures)
	return result
}
