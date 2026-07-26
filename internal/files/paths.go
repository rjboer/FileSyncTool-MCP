package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func RelativeWithin(root, path string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", fmt.Errorf("make relative path: %w", err)
	}
	if relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside configured root")
	}
	return filepath.ToSlash(relative), nil
}

func WorkspacePath(workspaceRoot, relativePath string) (string, error) {
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("invalid relative path")
	}
	native := filepath.Clean(filepath.FromSlash(relativePath))
	if native == "." || native == ".." || strings.HasPrefix(native, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe relative path %q", relativePath)
	}
	target := filepath.Join(workspaceRoot, native)
	relative, err := filepath.Rel(workspaceRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return target, nil
}

func RemoveEmptyParents(startDir, workspaceRoot string) error {
	current := filepath.Clean(startDir)
	root := filepath.Clean(workspaceRoot)
	for {
		if strings.EqualFold(current, root) {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("cleanup path escapes workspace")
		}
		entries, err := os.ReadDir(current)
		if os.IsNotExist(err) {
			current = filepath.Dir(current)
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return nil
		}
		if err := os.Remove(current); err != nil {
			return err
		}
		current = filepath.Dir(current)
	}
}
