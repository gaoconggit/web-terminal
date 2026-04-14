package webterm

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultPort          = 7681
	defaultBind          = "127.0.0.1"
	defaultMaxScrollback = 50 * 1024
	defaultMaxUploadSize = 50 * 1024 * 1024
)

type Config struct {
	Bind           string
	Port           int
	Token          string
	CWD            string
	StatePath      string
	MaxScrollback  int
	MaxUploadSize  int64
	ConfiguredTabs []Tab
	ExtraTemplates []Tab
}

type Tab struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Cmd        string   `json:"cmd,omitempty"`
	Args       []string `json:"args,omitempty"`
	TemplateID string   `json:"templateId,omitempty"`
	BaseLabel  string   `json:"baseLabel,omitempty"`
	IsDefault  bool     `json:"isDefault,omitempty"`
}

func LoadConfig() (Config, error) {
	loadDotEnv(".env")

	cwd := strings.TrimSpace(os.Getenv("WEB_TERMINAL_CWD"))
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Config{}, err
		}
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return Config{}, err
	}

	port := defaultPort
	if raw := strings.TrimSpace(os.Getenv("WEB_TERMINAL_PORT")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		} else {
			log.Printf("[web-terminal] invalid WEB_TERMINAL_PORT=%q, use %d", raw, defaultPort)
		}
	}

	bind := strings.TrimSpace(os.Getenv("WEB_TERMINAL_BIND"))
	if bind == "" {
		bind = defaultBind
	}

	token := strings.TrimSpace(os.Getenv("WEB_TERMINAL_TOKEN"))
	if token == "" {
		token, err = randomHex(12)
		if err != nil {
			return Config{}, err
		}
	}

	configuredTabs := defaultConfiguredTabs()
	if raw := strings.TrimSpace(os.Getenv("WEB_TERMINAL_TABS")); raw != "" {
		custom, err := parseTabsJSON(raw)
		if err != nil {
			log.Printf("[web-terminal] invalid WEB_TERMINAL_TABS, use defaults: %v", err)
		} else {
			configuredTabs = custom
		}
	}

	cfg := Config{
		Bind:           bind,
		Port:           port,
		Token:          token,
		CWD:            absCWD,
		StatePath:      filepath.Join(absCWD, ".claude", "skills", "web-terminal", "runtime-tabs.json"),
		MaxScrollback:  defaultMaxScrollback,
		MaxUploadSize:  defaultMaxUploadSize,
		ConfiguredTabs: configuredTabs,
		ExtraTemplates: extraTabTemplates(),
	}
	return cfg, nil
}

func defaultConfiguredTabs() []Tab {
	return []Tab{
		{ID: "claude", Label: "Claude Code", Cmd: "claude", Args: []string{"--continue", "--dangerously-skip-permissions", "--effort=max"}},
		{ID: "codex", Label: "Codex", Cmd: "codex", Args: []string{"--yolo"}},
	}
}

func extraTabTemplates() []Tab {
	return []Tab{{ID: "pwsh", Label: "pwsh", Cmd: "pwsh", Args: []string{}}}
}

func parseTabsJSON(raw string) ([]Tab, error) {
	var tabs []Tab
	if err := json.Unmarshal([]byte(raw), &tabs); err != nil {
		return nil, err
	}
	return tabs, nil
}

// loadDotEnv reads a .env file and sets variables not already present in the environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
