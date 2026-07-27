package files

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

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

func (s *AddService) AddDirectory(ctx context.Context, sourceDir string) (AddDirectoryResult, error) {
	var result AddDirectoryResult
	root, err := DirectoryWithin(s.sourceRoot, sourceDir)
	if err != nil {
		return result, err
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
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
			return err
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
			fileType, checksum, err := Fingerprint(current)
			if err != nil {
				return fmt.Errorf("revalidate source: %w", err)
			}
			if fileType != added.Entry.FileType || checksum != added.Entry.Checksum {
				return fmt.Errorf("source identity changed before removal")
			}
			if err := s.removeSource(current); err != nil {
				return fmt.Errorf("remove RemoteOnly source: %w", err)
			}
			result.RemovedRemoteOnly++
		}
		return nil
	})
	return result, err
}
