package webterm

import "testing"

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
