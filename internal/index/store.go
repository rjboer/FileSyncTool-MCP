package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type FingerprintFunc func(path string) (fileType, checksum string, err error)

type Store struct {
	mu   sync.RWMutex
	path string
	doc  Document
}

func Open(indexPath, workspaceRoot string, fingerprint FingerprintFunc) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}
	info, err := os.Stat(indexPath)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("index path is not a regular file")
		}
		doc, loadErr := load(indexPath)
		if loadErr != nil {
			return nil, loadErr
		}
		return &Store{path: indexPath, doc: doc}, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("stat index: %w", err)
	}

	doc, err := scan(workspaceRoot, indexPath, fingerprint)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(indexPath, doc); err != nil {
		return nil, fmt.Errorf("write initial index: %w", err)
	}
	return &Store{path: indexPath, doc: doc}, nil
}

func (s *Store) Snapshot() Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneDocument(s.doc)
}

func (s *Store) FindIdentity(fileType, checksum string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.doc.Files {
		if entry.FileType == fileType && entry.Checksum == checksum {
			return entry, true
		}
	}
	return Entry{}, false
}

func (s *Store) Add(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateEntry(entry); err != nil {
		return err
	}
	for _, current := range s.doc.Files {
		if strings.EqualFold(current.RelativePath, entry.RelativePath) {
			return fmt.Errorf("relative_path %q already exists", entry.RelativePath)
		}
	}
	next := cloneDocument(s.doc)
	next.Files = append(next.Files, entry)
	sort.Slice(next.Files, func(i, j int) bool {
		return next.Files[i].RelativePath < next.Files[j].RelativePath
	})
	if err := writeAtomic(s.path, next); err != nil {
		return fmt.Errorf("persist added entry: %w", err)
	}
	s.doc = next
	return nil
}

func (s *Store) Transition(relativePath string, from, to Status) error {
	if err := ValidateTransition(from, to); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneDocument(s.doc)
	for i := range next.Files {
		if next.Files[i].RelativePath != relativePath {
			continue
		}
		if next.Files[i].Status != from {
			return fmt.Errorf("entry %q has status %q, expected %q", relativePath, next.Files[i].Status, from)
		}
		next.Files[i].Status = to
		if err := writeAtomic(s.path, next); err != nil {
			return fmt.Errorf("persist status transition: %w", err)
		}
		s.doc = next
		return nil
	}
	return fmt.Errorf("entry %q not found", relativePath)
}

func load(path string) (Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return Document{}, fmt.Errorf("open index: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode index: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Document{}, fmt.Errorf("decode index: multiple JSON values")
		}
		return Document{}, fmt.Errorf("decode index: %w", err)
	}
	if err := validateDocument(doc); err != nil {
		return Document{}, fmt.Errorf("validate index: %w", err)
	}
	return doc, nil
}

func scan(workspaceRoot, indexPath string, fingerprint FingerprintFunc) (Document, error) {
	doc := Document{Version: 1, Files: []Entry{}}
	err := filepath.WalkDir(workspaceRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || excludedScanFile(current, indexPath, entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(workspaceRoot, current)
		if err != nil {
			return err
		}
		fileType, checksum, err := fingerprint(current)
		if err != nil {
			return fmt.Errorf("fingerprint %q: %w", relative, err)
		}
		relative = filepath.ToSlash(relative)
		doc.Files = append(doc.Files, Entry{
			RelativePath: relative, FileName: filepath.Base(current),
			FileType: fileType, Checksum: checksum, Status: OnFileNotRemote,
		})
		return nil
	})
	if err != nil {
		return Document{}, fmt.Errorf("scan workspace: %w", err)
	}
	sort.Slice(doc.Files, func(i, j int) bool {
		return doc.Files[i].RelativePath < doc.Files[j].RelativePath
	})
	if err := validateDocument(doc); err != nil {
		return Document{}, fmt.Errorf("validate scanned index: %w", err)
	}
	return doc, nil
}

func excludedScanFile(current, indexPath, name string) bool {
	if strings.EqualFold(filepath.Clean(current), filepath.Clean(indexPath)) {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, ".mcp-file-index-") ||
		strings.HasPrefix(lower, ".mcp-copy-") ||
		strings.HasPrefix(lower, ".mcp-rclone-")
}

func writeAtomic(path string, doc Document) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".mcp-file-index-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(doc); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = replaceFile(tempPath, path); err != nil {
		return err
	}
	return nil
}

func cloneDocument(doc Document) Document {
	copyDoc := doc
	copyDoc.Files = append([]Entry(nil), doc.Files...)
	return copyDoc
}
