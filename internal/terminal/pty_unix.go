//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

type unixSession struct {
	file *os.File
	cmd  *exec.Cmd
}

func Start(command Command, size Size) (Session, error) {
	size = normalizeSize(size)
	cmd := buildUnixCommand(command)
	cmd.Env = command.Env
	cmd.Dir = command.Cwd
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(size.Cols), Rows: uint16(size.Rows)})
	if err != nil {
		return nil, err
	}
	return &unixSession{file: ptmx, cmd: cmd}, nil
}

func (s *unixSession) Read(p []byte) (int, error) {
	return s.file.Read(p)
}

func (s *unixSession) Write(p []byte) (int, error) {
	return s.file.Write(p)
}

func (s *unixSession) Close() error {
	return s.file.Close()
}

func (s *unixSession) Resize(cols, rows int) error {
	return pty.Setsize(s.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *unixSession) Wait(ctx context.Context) (int, error) {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.cmd.Wait()
	}()
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case err := <-errCh:
		code := 0
		if s.cmd.ProcessState != nil {
			code = s.cmd.ProcessState.ExitCode()
		}
		return code, err
	}
}

func buildUnixCommand(command Command) *exec.Cmd {
	if isPowerShellCommand(command.Cmd) {
		return exec.Command("pwsh", "-NoLogo")
	}
	if strings.TrimSpace(command.Cmd) == "" {
		return exec.Command("bash", "-l")
	}
	return exec.Command(command.Cmd, command.Args...)
}
