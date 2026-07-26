package syncer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcp-file-tool/internal/config"
)

type CheckState string

const (
	Match      CheckState = "match"
	Missing    CheckState = "missing"
	Different  CheckState = "different"
	CheckError CheckState = "error"
)

type CheckReport struct {
	Files map[string]CheckState
}

type Remote interface {
	Copy(context.Context, []string) error
	Check(context.Context, []string) (CheckReport, error)
}

type RcloneRemote struct {
	workspaceRoot string
	rclonePath    string
	remoteName    string
	remoteConfig  string
	destination   string
	runner        CommandRunner
	redactions    []string
	configErr     error
}

func NewRcloneRemote(cfg config.Resolved, runner CommandRunner) *RcloneRemote {
	destination := "mcp-s3:" + cfg.Remote.Bucket
	if cfg.Remote.Prefix != "" {
		destination += "/" + cfg.Remote.Prefix
	}
	remoteConfig := fmt.Sprintf(
		"[mcp-s3]\ntype = s3\nprovider = Other\nenv_auth = false\naccess_key_id = %s\nsecret_access_key = %s\nregion = %s\nendpoint = %s\n",
		cfg.Remote.AccessKeyID, cfg.Remote.SecretAccessKey, cfg.Remote.Region, cfg.Remote.Endpoint,
	)
	remote := &RcloneRemote{
		workspaceRoot: cfg.WorkspaceRoot, rclonePath: cfg.RclonePath,
		remoteName: "mcp-s3", remoteConfig: remoteConfig,
		destination: destination, runner: runner,
		redactions: []string{cfg.Remote.AccessKeyID, cfg.Remote.SecretAccessKey},
	}
	for _, value := range []string{
		cfg.Remote.Endpoint, cfg.Remote.Region, cfg.Remote.Bucket, cfg.Remote.Prefix,
		cfg.Remote.AccessKeyID, cfg.Remote.SecretAccessKey,
	} {
		if strings.ContainsAny(value, "\r\n\x00") {
			remote.configErr = fmt.Errorf("rclone configuration value contains a forbidden control character")
			break
		}
	}
	return remote
}

func (r *RcloneRemote) Copy(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	inputs, err := r.prepare(paths, false)
	if err != nil {
		return err
	}
	defer inputs.cleanup()
	args := []string{
		"copy", r.workspaceRoot, r.destination,
		"--files-from-raw", inputs.listPath,
		"--config", inputs.configPath,
		"--no-traverse",
	}
	result, runErr := r.runner.Run(ctx, r.rclonePath, args, nil)
	if runErr != nil {
		return r.commandError("rclone copy", result, runErr)
	}
	return nil
}

func (r *RcloneRemote) Check(ctx context.Context, paths []string) (CheckReport, error) {
	report := CheckReport{Files: make(map[string]CheckState, len(paths))}
	if len(paths) == 0 {
		return report, nil
	}
	inputs, err := r.prepare(paths, true)
	if err != nil {
		return report, err
	}
	defer inputs.cleanup()
	args := []string{
		"check", r.workspaceRoot, r.destination,
		"--files-from-raw", inputs.listPath,
		"--config", inputs.configPath,
		"--one-way",
		"--combined", inputs.reportPath,
	}
	result, runErr := r.runner.Run(ctx, r.rclonePath, args, nil)
	parsed, parseErr := parseCombined(inputs.reportPath, paths)
	if parseErr == nil {
		report = parsed
	}
	if runErr != nil {
		return report, r.commandError("rclone check", result, runErr)
	}
	if parseErr != nil {
		return report, fmt.Errorf("parse rclone check report: %w", parseErr)
	}
	return report, nil
}

type rcloneInputs struct {
	configPath string
	listPath   string
	reportPath string
}

func (i rcloneInputs) cleanup() {
	for _, path := range []string{i.configPath, i.listPath, i.reportPath} {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

func (r *RcloneRemote) prepare(paths []string, withReport bool) (rcloneInputs, error) {
	var inputs rcloneInputs
	if r.configErr != nil {
		return inputs, r.configErr
	}
	configPath, err := writeTemporary(r.workspaceRoot, ".mcp-rclone-*.conf", r.remoteConfig)
	if err != nil {
		return inputs, fmt.Errorf("create rclone config: %w", err)
	}
	inputs.configPath = configPath
	var list strings.Builder
	for _, path := range paths {
		normalized := filepath.ToSlash(path)
		if normalized == "" || strings.ContainsAny(normalized, "\r\n") {
			inputs.cleanup()
			return rcloneInputs{}, fmt.Errorf("invalid rclone relative path")
		}
		list.WriteString(normalized)
		list.WriteByte('\n')
	}
	listPath, err := writeTemporary(r.workspaceRoot, ".mcp-rclone-files-*.txt", list.String())
	if err != nil {
		inputs.cleanup()
		return rcloneInputs{}, fmt.Errorf("create rclone file list: %w", err)
	}
	inputs.listPath = listPath
	if withReport {
		report, err := os.CreateTemp(r.workspaceRoot, ".mcp-rclone-check-*.txt")
		if err != nil {
			inputs.cleanup()
			return rcloneInputs{}, fmt.Errorf("create rclone check report: %w", err)
		}
		inputs.reportPath = report.Name()
		if err := report.Close(); err != nil {
			inputs.cleanup()
			return rcloneInputs{}, fmt.Errorf("close rclone check report: %w", err)
		}
	}
	return inputs, nil
}

func writeTemporary(directory, pattern, content string) (path string, err error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err = file.WriteString(content); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func parseCombined(path string, requested []string) (CheckReport, error) {
	report := CheckReport{Files: make(map[string]CheckState, len(requested))}
	for _, requestedPath := range requested {
		report.Files[filepath.ToSlash(requestedPath)] = CheckError
	}
	file, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if len(line) < 3 || line[1] != ' ' {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return report, fmt.Errorf("invalid combined line %q", line)
		}
		relative := filepath.ToSlash(line[2:])
		switch line[0] {
		case '=':
			report.Files[relative] = Match
		case '+':
			report.Files[relative] = Missing
		case '*':
			report.Files[relative] = Different
		case '!':
			report.Files[relative] = CheckError
		default:
			return report, fmt.Errorf("unknown combined status %q", line[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func (r *RcloneRemote) commandError(action string, result CommandResult, runErr error) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = runErr.Error()
	} else {
		message += ": " + runErr.Error()
	}
	for _, secret := range r.redactions {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return fmt.Errorf("%s failed: %s", action, message)
}
