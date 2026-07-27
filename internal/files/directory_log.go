package files

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type directoryFailure struct {
	SourcePath   string
	RelativePath string
	Phase        string
	Err          error
}

type directoryErrorLog interface {
	Path() string
	Record(directoryFailure) error
	Close() error
}

type csvDirectoryErrorLog struct {
	path   string
	runID  string
	file   *os.File
	writer *csv.Writer
}

func newDirectoryErrorLog(workspaceRoot string) (directoryErrorLog, error) {
	logDirectory, err := ensureSafeLogDirectory(workspaceRoot)
	if err != nil {
		return nil, err
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("create directory run ID: %w", err)
	}
	runID := hex.EncodeToString(random)
	filename := fmt.Sprintf(
		"%s_%s.csv",
		time.Now().UTC().Format("20060102T150405.000000000Z"),
		runID,
	)
	path, err := filepath.Abs(filepath.Join(logDirectory, filename))
	if err != nil {
		return nil, fmt.Errorf("resolve directory error log path: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create directory error log: %w", err)
	}
	log := &csvDirectoryErrorLog{
		path: path, runID: runID, file: file, writer: csv.NewWriter(file),
	}
	header := []string{
		"timestamp_utc", "run_id", "source_path",
		"relative_path", "phase", "error",
	}
	if err := log.write(header); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write directory error log header: %w", err)
	}
	return log, nil
}

func ensureSafeLogDirectory(workspaceRoot string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root for directory error log: %w", err)
	}
	current := filepath.Clean(resolvedRoot)
	for _, name := range []string{"logs", "add_directory"} {
		current = filepath.Join(current, name)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if mkdirErr := os.Mkdir(current, 0o755); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return "", fmt.Errorf("create directory error log folder: %w", mkdirErr)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return "", fmt.Errorf("inspect directory error log folder: %w", err)
		}
		if isUnsafeDirectoryLink(info) {
			return "", fmt.Errorf("directory error log folder contains a symbolic link or junction")
		}
		if !info.IsDir() {
			return "", fmt.Errorf("directory error log path component is not a directory")
		}
	}
	resolvedDirectory, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve directory error log folder: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedDirectory)
	if err != nil ||
		relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("directory error log folder escapes workspace root")
	}
	return resolvedDirectory, nil
}

func (l *csvDirectoryErrorLog) Path() string {
	return l.path
}

func (l *csvDirectoryErrorLog) Record(failure directoryFailure) error {
	row := []string{
		time.Now().UTC().Format(time.RFC3339Nano),
		l.runID,
		failure.SourcePath,
		failure.RelativePath,
		failure.Phase,
		failure.Err.Error(),
	}
	if err := l.write(row); err != nil {
		return fmt.Errorf("write directory error log failure: %w", err)
	}
	return nil
}

func (l *csvDirectoryErrorLog) Close() error {
	l.writer.Flush()
	if err := l.writer.Error(); err != nil {
		_ = l.file.Close()
		return fmt.Errorf("flush directory error log: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close directory error log: %w", err)
	}
	return nil
}

func (l *csvDirectoryErrorLog) write(row []string) error {
	if err := l.writer.Write(row); err != nil {
		return err
	}
	l.writer.Flush()
	if err := l.writer.Error(); err != nil {
		return err
	}
	return l.file.Sync()
}
