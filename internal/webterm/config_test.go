package webterm

import "testing"

func TestIsTUICommand(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"codex bare", "codex", true},
		{"codex exe", "codex.exe", true},
		{"codex with path", `C:\Program Files\codex\codex.exe`, true},
		{"codex unix path", "/usr/local/bin/codex", true},
		{"codex uppercase", "CODEX", true},
		{"codex with whitespace", "  codex  ", true},
		{"claude bare", "claude", true},
		{"claude exe", "claude.exe", true},
		{"pwsh", "pwsh", false},
		{"bash", "bash", false},
		{"empty", "", false},
		{"codex-wrapper is not codex", "codex-wrapper", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTUICommand(tc.cmd); got != tc.want {
				t.Fatalf("isTUICommand(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestDefaultConfiguredTabsStartFreshCodexSession(t *testing.T) {
	tabs := defaultConfiguredTabs()

	var codex *Tab
	for i := range tabs {
		if tabs[i].ID == "codex" {
			codex = &tabs[i]
			break
		}
	}

	if codex == nil {
		t.Fatal("default tabs missing codex preset")
	}

	if codex.Cmd != "codex" {
		t.Fatalf("codex preset command = %q, want %q", codex.Cmd, "codex")
	}

	if len(codex.Args) != 1 || codex.Args[0] != "--yolo" {
		t.Fatalf("codex preset args = %v, want [--yolo]", codex.Args)
	}
}
