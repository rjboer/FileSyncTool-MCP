package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type fileConfig struct {
	SourceRoot    string       `json:"source_root"`
	WorkspaceRoot string       `json:"workspace_root"`
	IndexPath     string       `json:"index_path"`
	Remote        remoteConfig `json:"remote"`
}

type remoteConfig struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	AccessKeyIDEnv     string `json:"access_key_id_env"`
	SecretAccessKeyEnv string `json:"secret_access_key_env"`
}

type Remote struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
}

type Resolved struct {
	SourceRoot    string
	WorkspaceRoot string
	IndexPath     string
	RclonePath    string
	Remote        Remote
}

func Load(path, exeDir string) (Resolved, error) {
	var zero Resolved
	f, err := os.Open(path)
	if err != nil {
		return zero, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var raw fileConfig
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return zero, fmt.Errorf("decode config: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return zero, err
	}

	sourceRoot, err := resolveDirectory(exeDir, raw.SourceRoot, "source_root")
	if err != nil {
		return zero, err
	}
	workspaceRoot, err := resolveDirectory(exeDir, raw.WorkspaceRoot, "workspace_root")
	if err != nil {
		return zero, err
	}
	if pathsOverlap(sourceRoot, workspaceRoot) {
		return zero, fmt.Errorf("source_root and workspace_root overlap")
	}

	indexPath, err := resolvePath(exeDir, raw.IndexPath, "index_path")
	if err != nil {
		return zero, err
	}
	if !strings.EqualFold(filepath.Ext(indexPath), ".json") {
		return zero, fmt.Errorf("index_path must end in .json")
	}

	rclonePath := filepath.Join(exeDir, "rclone.exe")
	info, err := os.Stat(rclonePath)
	if err != nil {
		return zero, fmt.Errorf("rclone.exe: %w", err)
	}
	if !info.Mode().IsRegular() {
		return zero, fmt.Errorf("rclone.exe is not a regular file")
	}

	remote, err := resolveRemote(raw.Remote)
	if err != nil {
		return zero, err
	}
	return Resolved{
		SourceRoot: sourceRoot, WorkspaceRoot: workspaceRoot,
		IndexPath: indexPath, RclonePath: rclonePath, Remote: remote,
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("decode config: multiple JSON values")
	}
	return fmt.Errorf("decode config: %w", err)
}

func resolveRemote(raw remoteConfig) (Remote, error) {
	required := map[string]string{
		"remote.endpoint":              raw.Endpoint,
		"remote.region":                raw.Region,
		"remote.bucket":                raw.Bucket,
		"remote.access_key_id_env":     raw.AccessKeyIDEnv,
		"remote.secret_access_key_env": raw.SecretAccessKeyEnv,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return Remote{}, fmt.Errorf("%s is required", name)
		}
	}
	accessKeyID, ok := os.LookupEnv(raw.AccessKeyIDEnv)
	if !ok || accessKeyID == "" {
		return Remote{}, fmt.Errorf("environment variable %s is required", raw.AccessKeyIDEnv)
	}
	secretAccessKey, ok := os.LookupEnv(raw.SecretAccessKeyEnv)
	if !ok || secretAccessKey == "" {
		return Remote{}, fmt.Errorf("environment variable %s is required", raw.SecretAccessKeyEnv)
	}
	prefix := strings.Trim(strings.ReplaceAll(raw.Prefix, `\`, "/"), "/")
	return Remote{
		Endpoint: strings.TrimSpace(raw.Endpoint), Region: strings.TrimSpace(raw.Region),
		Bucket: strings.TrimSpace(raw.Bucket), Prefix: prefix,
		AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey,
	}, nil
}

func resolveDirectory(base, value, name string) (string, error) {
	path, err := resolvePath(base, value, name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", name)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return filepath.Clean(resolved), nil
}

func resolvePath(base, value, name string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return filepath.Clean(absolute), nil
}

func pathsOverlap(a, b string) bool {
	return isWithin(a, b) || isWithin(b, a)
}

func isWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
