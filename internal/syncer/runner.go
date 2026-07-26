package syncer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(ctx context.Context, executable string, args, env []string) (CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, executable string, args, env []string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}
