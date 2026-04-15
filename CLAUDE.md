# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
go build -o web-terminal.exe .          # build (Windows binary; drop .exe on Unix)
go run .                                # run from source
go test ./...                           # run all tests
go test ./internal/webterm -run TestIsTUICommand   # single test
```

There is no linter or formatter wired up beyond `go vet` / `gofmt` defaults.

To restart the running compiled server, prefer the `restart-web-terminal` skill (`.claude/skills/restart-web-terminal/SKILL.md`) — it spawns a **detached** worker so killing the old process does not also kill an in-process Claude session being served by it. Do not naively `Stop-Process` the server from a tool call if Claude itself is running through web-terminal.

## Architecture

Single Go binary that serves an embedded terminal UI over HTTP/WebSocket and brokers PTY sessions. The browser is a thin xterm.js client; **all session state lives on the server**.

### Layers

- `main.go` — loads config, builds server, wires signal-based shutdown.
- `internal/webterm/config.go` — env + `.env` parsing, default tab presets, runtime defaults.
- `internal/webterm/server.go` — HTTP handlers, WebSocket loop, tab/state lifecycle, scrollback, persistence.
- `internal/terminal/` — cross-platform PTY abstraction. `terminal.go` defines `Session`; `pty_windows.go` (ConPTY) and `pty_unix.go` (creack/pty) are selected by build tags.
- `internal/webterm/web/` — `login.html` and `terminal.html` are `//go:embed`-ed; the frontend is one 80KB HTML file (vanilla JS + xterm.js from CDN). Embedded assets are baked into the binary at build time — frontend changes require a rebuild.

### Session model — non-obvious

A **tab** is a config entry (id, label, cmd, args). A **TabState** is the live PTY for that tab. Tabs are spawned lazily on first `activate` and outlive WebSocket disconnects. On reconnect:

1. Client sends `activate` with viewport size.
2. Server replays raw `scrollback` to that client only.
3. If the tab runs a known full-screen TUI (`isTUICommand` → `codex` / `claude`), server forces a SIGWINCH (`resizeWithRepaint`) so the app repaints its current frame against the new viewport. This cleans up artifacts left by truncated mid-frame scrollback. Don't add similar logic for non-TUI commands — for shells, the raw replay is correct.

Multiple browser tabs can subscribe to the same PTY; output is broadcast to all subscribed clients (`broadcastToTab`).

### Tab taxonomy

There are three kinds of tabs, all sharing the `Tab` struct but with different lifetimes:

- **Configured (default)** — from `WEB_TERMINAL_TABS` or `defaultConfiguredTabs()`. Immutable IDs, cannot be deleted (`defaultIDs` set in `Server`). Label changes persist via `defaultLabels`.
- **Extra templates** — additional spawn presets (e.g. `pwsh`) shown in the "new tab" menu but not opened by default. From `extraTabTemplates()`.
- **Runtime** — created via `create_tab` WebSocket message (clone of a template or another tab). Persisted to `<CWD>/.claude/skills/web-terminal/runtime-tabs.json`. The persistence file format is versioned (`version: 2`); legacy v1 (a bare array) is still parsed for backward-compat on read.

`stripRuntimeResumeArgs` strips `--continue` from cloned runtime tabs — a fresh clone shouldn't try to resume the source's session.

### WebSocket protocol

Inbound (client → server): `activate`, `input`, `resize`, `create_tab`, `delete_tab`, `rename_tab`.
Outbound (server → client): `scrollback`, `output`, `status`, `exit`, `tab_added`, `tab_removed`, `tab_renamed`, `tab_error`.

### Windows PTY wrapping

On Windows, every spawned command (except bare `pwsh`) is wrapped in `pwsh.exe -NoLogo -NoExit -EncodedCommand <base64>`. The wrapper sets UTF-8 console encoding before invoking the real command. This is why `Tab.Cmd` is not handed to ConPTY directly — see `buildWindowsCommandLine` and `buildPowerShellWrapper`. Do not assume the OS-level process tree matches `Tab.Cmd`; it's `pwsh → user-cmd`.

### Auth

Cookie name is `cct`; value is the token from `WEB_TERMINAL_TOKEN` (or random hex if unset, logged at startup). The `/t/<token>` URL sets the cookie and redirects — used for mobile bookmarks and the printed "no-input" link.

### Non-obvious defaults

- `defaultMaxScrollback = 8 MB`. The previous 50 KB truncated the initial full-screen paint of TUI apps, leaving blank rows on reconnect. Don't lower this without understanding the TUI replay path.
- Codex preset defaults to `--yolo` only, **not** `--continue` — PTY persistence already keeps the session across reconnects, and `resume --last` can immediately exit if the prior conversation no longer exists. See `TestDefaultConfiguredTabsStartFreshCodexSession`.
- Uploads land in `<CWD>/.claude/uploads/`; path traversal is rejected in `handleUpload` (`..`, absolute paths, escaping the upload root).

## Config

Env vars (all optional): `WEB_TERMINAL_BIND`, `WEB_TERMINAL_PORT`, `WEB_TERMINAL_TOKEN`, `WEB_TERMINAL_CWD`, `WEB_TERMINAL_TABS` (JSON array of `{id,label,cmd,args}`). `.env` in the project root is loaded but does not override existing env vars.
