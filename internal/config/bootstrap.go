package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func EnsureFile(path string) (created bool, err error) {
	template := fileConfig{
		Remote: remoteConfig{
			AccessKeyIDEnv:     "MCP_S3_ACCESS_KEY_ID",
			SecretAccessKeyEnv: "MCP_S3_SECRET_ACCESS_KEY",
		},
	}
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal config template: %w", err)
	}
	data = append(data, '\n')

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create config: %w", err)
	}
	removeOnError := true
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if removeOnError {
			_ = os.Remove(path)
		}
	}()

	written, err := file.Write(data)
	if err != nil {
		return false, fmt.Errorf("write config template: %w", err)
	}
	if written != len(data) {
		return false, fmt.Errorf("write config template: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return false, fmt.Errorf("sync config template: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close config template: %w", err)
	}
	file = nil
	removeOnError = false
	return true, nil
}
