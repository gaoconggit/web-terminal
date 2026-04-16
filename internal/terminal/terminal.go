package terminal

import (
	"context"
	"io"
	"strings"
)

type Command struct {
	Cmd  string
	Args []string
	Cwd  string
	Env  []string
}

type Size struct {
	Cols int
	Rows int
}

type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait(ctx context.Context) (int, error)
}

func normalizeSize(size Size) Size {
	if size.Cols < 10 {
		size.Cols = 120
	}
	if size.Rows < 5 {
		size.Rows = 40
	}
	return size
}

func isPowerShellCommand(cmd string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(cmd, "\\", "/")))
	base := normalized
	if i := strings.LastIndex(normalized, "/"); i >= 0 {
		base = normalized[i+1:]
	}
	return base == "pwsh" || base == "pwsh.exe" || base == "powershell" || base == "powershell.exe"
}
