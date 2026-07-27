package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"mcp-file-tool/internal/index"
)

type AddDirectoryResult struct {
	Scanned             int    `json:"scanned"`
	Added               int    `json:"added"`
	RetainedNotRemote   int    `json:"retained_on_file_not_remote"`
	RetainedOnAndRemote int    `json:"retained_on_file_and_remote"`
	RemovedRemoteOnly   int    `json:"removed_remote_only"`
	Skipped             int    `json:"skipped"`
	Failed              int    `json:"failed"`
	ErrorLogPath        string `json:"error_log_path"`
}

func (s *AddService) AddDirectory(ctx context.Context, sourceDir string) (result AddDirectoryResult, err error) {
	root, err := DirectoryWithin(s.sourceRoot, sourceDir)
	if err != nil {
		return result, err
	}
	errorLog, err := s.newDirectoryLog(s.workspaceRoot)
	if err != nil {
		return result, err
	}
	result.ErrorLogPath = errorLog.Path()
	defer func() {
		err = errors.Join(err, errorLog.Close())
	}()

	s.opMu.Lock()
	defer s.opMu.Unlock()

	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if logErr := s.recordDirectoryFailure(
				errorLog, &result, current, "walk", walkErr,
			); logErr != nil {
				return logErr
			}
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.store.IsIndexPath(current) {
			result.Skipped++
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			result.Skipped++
			return nil
		}

		result.Scanned++
		added, err := s.addLocked(ctx, current)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return s.recordDirectoryFailure(errorLog, &result, current, "add", err)
		}
		if added.New {
			result.Added++
			return nil
		}
		switch added.Entry.Status {
		case index.OnFileNotRemote:
			result.RetainedNotRemote++
		case index.OnFileAndRemote:
			result.RetainedOnAndRemote++
		case index.RemoteOnly:
			fileType, checksum, err := s.revalidateSource(current)
			if err != nil {
				return s.recordDirectoryFailure(
					errorLog, &result, current, "revalidate",
					fmt.Errorf("fingerprint source: %w", err),
				)
			}
			if fileType != added.Entry.FileType || checksum != added.Entry.Checksum {
				return s.recordDirectoryFailure(
					errorLog, &result, current, "revalidate",
					fmt.Errorf("source identity changed before removal"),
				)
			}
			if err := s.removeSource(current); err != nil {
				return s.recordDirectoryFailure(
					errorLog, &result, current, "remove",
					fmt.Errorf("remove RemoteOnly source: %w", err),
				)
			}
			result.RemovedRemoteOnly++
		}
		return nil
	})
	return result, err
}

func (s *AddService) recordDirectoryFailure(
	errorLog directoryErrorLog,
	result *AddDirectoryResult,
	sourcePath string,
	phase string,
	failure error,
) error {
	relative, relativeErr := RelativeWithin(s.sourceRoot, sourcePath)
	if relativeErr != nil {
		relative = ""
		if candidate, err := filepath.Rel(s.sourceRoot, sourcePath); err == nil &&
			candidate != "." &&
			!filepath.IsAbs(candidate) &&
			candidate != ".." &&
			!strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
			relative = filepath.ToSlash(candidate)
		}
	}
	if err := errorLog.Record(directoryFailure{
		SourcePath: sourcePath, RelativePath: relative,
		Phase: phase, Err: failure,
	}); err != nil {
		return err
	}
	result.Failed++
	return nil
}
