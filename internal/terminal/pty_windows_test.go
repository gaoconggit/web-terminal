//go:build windows

package terminal

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPowerShellBootstrapScriptEnablesCtrlC(t *testing.T) {
	script := buildPowerShellBootstrapScript()
	if !strings.Contains(script, "SetConsoleCtrlHandler") {
		t.Fatalf("bootstrap script should restore Ctrl+C handling, got %q", script)
	}
}

func TestPowerShellCtrlCInterruptsSleepingCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY integration test in short mode")
	}

	sess, err := Start(Command{Cmd: "pwsh", Cwd: t.TempDir()}, Size{Cols: 120, Rows: 40})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer func() { _ = sess.Close() }()

	outputCh := make(chan string, 64)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				outputCh <- string(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(2 * time.Second)
	_, _ = sess.Write([]byte("Start-Sleep -Seconds 300\r"))
	time.Sleep(500 * time.Millisecond)
	_, _ = sess.Write([]byte{3})
	time.Sleep(300 * time.Millisecond)

	marker := "__WT_CTRL_C_OK__"
	_, _ = sess.Write([]byte("Write-Output " + marker + "\r"))

	var output strings.Builder
	deadline := time.After(8 * time.Second)
	for {
		select {
		case chunk := <-outputCh:
			output.WriteString(chunk)
			if strings.Contains(output.String(), marker) {
				return
			}
		case <-deadline:
			got := output.String()
			if len(got) > 1200 {
				got = got[len(got)-1200:]
			}
			t.Fatalf("timed out waiting for interrupt marker; recent output:\n%s", got)
		case <-readDone:
			got := output.String()
			t.Fatalf("session ended before interrupt marker appeared; output:\n%s", got)
		}
	}
}
