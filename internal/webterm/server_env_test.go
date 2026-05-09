package webterm

import (
	"strings"
	"testing"
)

func TestWithTerminalEnvSanitizesParentColorControls(t *testing.T) {
	env := []string{
		"PATH=C:\\Windows\\System32",
		"TERM=dumb",
		"COLORTERM=",
		"NO_COLOR=1",
		"CLICOLOR=0",
		"CLICOLOR_FORCE=0",
		"FORCE_COLOR=0",
		"KEEP_ME=yes",
	}

	got := withTerminalEnv(env)
	joined := "\n" + strings.Join(got, "\n") + "\n"

	for _, banned := range []string{
		"\nTERM=dumb\n",
		"\nCOLORTERM=\n",
		"\nNO_COLOR=1\n",
		"\nCLICOLOR=0\n",
		"\nCLICOLOR_FORCE=0\n",
		"\nFORCE_COLOR=0\n",
	} {
		if strings.Contains(joined, banned) {
			t.Fatalf("withTerminalEnv kept inherited color control %q in %v", strings.TrimSpace(banned), got)
		}
	}

	for _, required := range []string{
		"\nPATH=C:\\Windows\\System32\n",
		"\nKEEP_ME=yes\n",
		"\nTERM=xterm-256color\n",
		"\nCOLORTERM=truecolor\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("withTerminalEnv missing %q in %v", strings.TrimSpace(required), got)
		}
	}
}
