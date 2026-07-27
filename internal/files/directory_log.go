package files

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
	logDirectory := filepath.Join(workspaceRoot, "logs", "add_directory")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create directory error log folder: %w", err)
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
	return l.writer.Error()
}
