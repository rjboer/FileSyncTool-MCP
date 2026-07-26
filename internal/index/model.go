package index

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

type Status string

const (
	OnFileNotRemote Status = "OnFileNotRemote"
	OnFileAndRemote Status = "OnFileAndRemote"
	RemoteOnly      Status = "RemoteOnly"
)

type Entry struct {
	RelativePath string `json:"relative_path"`
	FileName     string `json:"file_name"`
	FileType     string `json:"file_type"`
	Checksum     string `json:"checksum"`
	Status       Status `json:"status"`
}

type Document struct {
	Version int     `json:"version"`
	Files   []Entry `json:"files"`
}

var checksumPattern = regexp.MustCompile(`^xxh64:[0-9a-f]{16}$`)

func ValidateTransition(from, to Status) error {
	if from == OnFileNotRemote && to == OnFileAndRemote {
		return nil
	}
	if from == OnFileAndRemote && to == RemoteOnly {
		return nil
	}
	return fmt.Errorf("invalid status transition %q -> %q", from, to)
}

func validateDocument(doc Document) error {
	if doc.Version != 1 {
		return fmt.Errorf("unsupported index version %d", doc.Version)
	}
	seen := make(map[string]struct{}, len(doc.Files))
	for i, entry := range doc.Files {
		if err := validateEntry(entry); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
		key := strings.ToLower(entry.RelativePath)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate relative_path %q", entry.RelativePath)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateEntry(entry Entry) error {
	if entry.RelativePath == "" || entry.RelativePath == "." || strings.Contains(entry.RelativePath, `\`) {
		return fmt.Errorf("invalid relative_path %q", entry.RelativePath)
	}
	clean := path.Clean(entry.RelativePath)
	if clean != entry.RelativePath || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("unsafe relative_path %q", entry.RelativePath)
	}
	if entry.FileName != path.Base(entry.RelativePath) {
		return fmt.Errorf("file_name %q does not match relative_path", entry.FileName)
	}
	if entry.FileType != strings.ToLower(entry.FileType) {
		return fmt.Errorf("file_type must be lowercase")
	}
	if !checksumPattern.MatchString(entry.Checksum) {
		return fmt.Errorf("invalid checksum %q", entry.Checksum)
	}
	switch entry.Status {
	case OnFileNotRemote, OnFileAndRemote, RemoteOnly:
		return nil
	default:
		return fmt.Errorf("invalid status %q", entry.Status)
	}
}
