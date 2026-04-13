//go:build windows

package terminal

import (
	"context"
	"encoding/base64"
	"strings"
	"unicode/utf16"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type windowsSession struct {
	cpty *conpty.ConPty
}

func Start(cmd Command, size Size) (Session, error) {
	size = normalizeSize(size)
	commandLine := buildWindowsCommandLine(cmd)
	cpty, err := conpty.Start(
		commandLine,
		conpty.ConPtyDimensions(size.Cols, size.Rows),
		conpty.ConPtyWorkDir(cmd.Cwd),
		conpty.ConPtyEnv(cmd.Env),
	)
	if err != nil {
		return nil, err
	}
	return &windowsSession{cpty: cpty}, nil
}

func (s *windowsSession) Read(p []byte) (int, error) {
	return s.cpty.Read(p)
}

func (s *windowsSession) Write(p []byte) (int, error) {
	return s.cpty.Write(p)
}

func (s *windowsSession) Close() error {
	return s.cpty.Close()
}

func (s *windowsSession) Resize(cols, rows int) error {
	return s.cpty.Resize(cols, rows)
}

func (s *windowsSession) Wait(ctx context.Context) (int, error) {
	exitCode, err := s.cpty.Wait(ctx)
	return int(exitCode), err
}

func buildWindowsCommandLine(cmd Command) string {
	if isPowerShellCommand(cmd.Cmd) {
		return joinWindowsArgs("pwsh.exe", "-NoLogo", "-NoExit")
	}
	script := buildPowerShellWrapper(cmd.Cmd, cmd.Args)
	encoded := encodePowerShell(script)
	return joinWindowsArgs("pwsh.exe", "-NoLogo", "-NoExit", "-EncodedCommand", encoded)
}

func buildPowerShellWrapper(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, psQuote(command))
	for _, arg := range args {
		parts = append(parts, psQuote(arg))
	}
	return strings.Join([]string{
		"[Console]::InputEncoding = [System.Text.Encoding]::UTF8",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$OutputEncoding = [System.Text.Encoding]::UTF8",
		"Clear-Host",
		"& " + strings.Join(parts, " "),
	}, "; ")
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func joinWindowsArgs(values ...string) string {
	escaped := make([]string, 0, len(values))
	for _, value := range values {
		escaped = append(escaped, windows.EscapeArg(value))
	}
	return strings.Join(escaped, " ")
}

func encodePowerShell(script string) string {
	utf16Data := utf16.Encode([]rune(script))
	buf := make([]byte, len(utf16Data)*2)
	for i, v := range utf16Data {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}
