package webterm

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"web-terminal/internal/terminal"
)

const ShutdownTimeout = 5 * time.Second

var ErrServerClosed = http.ErrServerClosed

var noCacheHeaders = map[string]string{
	"Cache-Control": "no-store, no-cache, must-revalidate",
	"Pragma":        "no-cache",
	"Expires":       "0",
}

//go:embed web/*.html
var templateFS embed.FS

type Server struct {
	cfg Config

	htmlTemplates *template.Template
	httpServer    *http.Server
	upgrader      websocket.Upgrader

	mu                     sync.RWMutex
	configuredDefaultLabel map[string]string
	defaultIDs             map[string]struct{}
	tabs                   []*Tab
	tabTemplates           []*Tab
	states                 map[string]*TabState

	clientsMu sync.RWMutex
	clients   map[*Client]struct{}
}

type Client struct {
	conn       *websocket.Conn
	sendMu     sync.Mutex
	subsMu     sync.RWMutex
	subscribed map[string]struct{}
}

type TabState struct {
	mu         sync.Mutex
	session    terminal.Session
	scrollback []byte
	status     string
	args       []string
	cols       int
	rows       int
}

type wsRequest struct {
	Type        string `json:"type"`
	Tab         string `json:"tab"`
	Data        string `json:"data"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
	ID          string `json:"id"`
	Label       string `json:"label"`
	SourceTabID string `json:"sourceTabId"`
	TemplateID  string `json:"templateId"`
}

type TabView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	TemplateID string `json:"templateId"`
	BaseLabel  string `json:"baseLabel"`
	IsDefault  bool   `json:"isDefault"`
}

type terminalPageData struct {
	TabsJSON      template.JS
	TemplatesJSON template.JS
	MaxUploadMB   int64
}

type runtimeTabsFile struct {
	Version       int               `json:"version"`
	DefaultLabels map[string]string `json:"defaultLabels"`
	RuntimeTabs   []runtimeTab      `json:"runtimeTabs"`
}

type runtimeTab struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Cmd        string   `json:"cmd"`
	Args       []string `json:"args"`
	TemplateID string   `json:"templateId"`
	BaseLabel  string   `json:"baseLabel"`
}

func NewServer(cfg Config) (*Server, error) {
	htmlTemplates, err := template.ParseFS(templateFS, "web/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:                    cfg,
		htmlTemplates:          htmlTemplates,
		configuredDefaultLabel: map[string]string{},
		defaultIDs:             map[string]struct{}{},
		states:                 map[string]*TabState{},
		clients:                map[*Client]struct{}{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}

	configured := prepareTabs(cfg.ConfiguredTabs, true)
	for _, tab := range configured {
		tab.IsDefault = true
		s.tabs = append(s.tabs, tab)
		s.tabTemplates = append(s.tabTemplates, cloneTabPtr(tab))
		s.defaultIDs[tab.ID] = struct{}{}
		s.configuredDefaultLabel[tab.ID] = tab.Label
	}
	for _, extra := range prepareTabs(cfg.ExtraTemplates, false) {
		if s.findTemplateLocked(extra.ID) != nil {
			continue
		}
		s.tabTemplates = append(s.tabTemplates, extra)
	}

	defaultLabels, runtimeTabs := s.loadPersistedRuntimeTabs()
	for _, tab := range s.tabs {
		if label, ok := defaultLabels[tab.ID]; ok {
			tab.Label = label
		}
	}
	for _, rt := range runtimeTabs {
		if s.findTabLocked(rt.ID) != nil {
			continue
		}
		rt.IsDefault = false
		s.tabs = append(s.tabs, cloneTabPtr(&rt))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/manifest.json", s.handleManifest)
	mux.HandleFunc("/auth", s.handleAuth)
	mux.HandleFunc("/terminal", s.handleTerminal)
	mux.HandleFunc("/upload", s.handleUpload)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/", s.handleRoot)

	s.httpServer = &http.Server{
		Addr:    net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)),
		Handler: mux,
	}
	return s, nil
}

func (s *Server) ListenAndServe() error {
	addr := net.JoinHostPort(s.cfg.Bind, strconv.Itoa(s.cfg.Port))
	log.Printf("[web-terminal] Listening on http://%s", addr)
	log.Printf("[web-terminal] Token: %s", s.cfg.Token)
	log.Printf("[web-terminal] Local URL: http://%s/t/%s", addr, s.cfg.Token)
	log.Printf("[web-terminal] Tabs: %s", strings.Join(s.tabLabels(), ", "))
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.closeAllStates()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) tabLabels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	labels := make([]string, 0, len(s.tabs))
	for _, tab := range s.tabs {
		labels = append(labels, tab.Label)
	}
	return labels
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (s *Server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	setNoCache(w)
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":             "CC Terminal",
		"short_name":       "CC",
		"start_url":        "/terminal",
		"display":          "standalone",
		"background_color": "#0b1020",
		"theme_color":      "#0b1020",
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/login" {
		if s.isAuthed(r) {
			http.Redirect(w, r, "/terminal", http.StatusFound)
			return
		}
		setNoCache(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.renderTemplate(w, "login.html", nil)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/t/") {
		token := strings.TrimPrefix(r.URL.Path, "/t/")
		if token == s.cfg.Token {
			http.SetCookie(w, &http.Cookie{
				Name:     "cct",
				Value:    s.cfg.Token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/terminal", http.StatusFound)
			return
		}
	}
	http.Error(w, "Forbidden", http.StatusForbidden)
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	setNoCache(w)
	defer r.Body.Close()
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if payload.Token != s.cfg.Token {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false}`)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cct",
		Value:    s.cfg.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthed(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	setNoCache(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := terminalPageData{
		TabsJSON:      mustJSONJS(s.serializeTabs()),
		TemplatesJSON: mustJSONJS(s.serializeTemplates()),
		MaxUploadMB:   s.cfg.MaxUploadSize / 1024 / 1024,
	}
	s.renderTemplate(w, "terminal.html", data)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isAuthed(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "Missing name parameter", http.StatusBadRequest)
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if strings.Contains(name, "..") || strings.Contains(dir, "..") || filepath.IsAbs(name) || filepath.IsAbs(dir) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	uploadDir := filepath.Join(s.cfg.CWD, ".claude", "uploads")
	targetDir := filepath.Clean(filepath.Join(uploadDir, dir))
	targetPath := filepath.Clean(filepath.Join(targetDir, name))
	rootClean := filepath.Clean(uploadDir)
	if targetPath != rootClean && !strings.HasPrefix(targetPath, rootClean+string(os.PathSeparator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if contentLength := r.ContentLength; contentLength > s.cfg.MaxUploadSize {
		http.Error(w, fmt.Sprintf("File too large (max %dMB)", s.cfg.MaxUploadSize/1024/1024), http.StatusRequestEntityTooLarge)
		return
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	file, err := os.Create(targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	limited := io.LimitReader(r.Body, s.cfg.MaxUploadSize+1)
	written, err := io.Copy(file, limited)
	if err != nil {
		_ = os.Remove(targetPath)
		http.Error(w, "Write error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if written > s.cfg.MaxUploadSize {
		_ = os.Remove(targetPath)
		http.Error(w, fmt.Sprintf("File too large (max %dMB)", s.cfg.MaxUploadSize/1024/1024), http.StatusRequestEntityTooLarge)
		return
	}
	relPath, _ := filepath.Rel(s.cfg.CWD, targetPath)
	resp := map[string]any{
		"ok":           true,
		"path":         filepath.ToSlash(relPath),
		"absolutePath": filepath.ToSlash(targetPath),
		"size":         written,
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(".json"))
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthed(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &Client{conn: conn, subscribed: map[string]struct{}{}}
	s.addClient(client)
	defer func() {
		s.removeClient(client)
		_ = conn.Close()
	}()

	for {
		var msg wsRequest
		if err := conn.ReadJSON(&msg); err != nil {
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) && !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[web-terminal] websocket read error: %v", err)
			}
			return
		}
		s.handleWSMessage(client, msg)
	}
}

func (s *Server) handleWSMessage(client *Client, msg wsRequest) {
	switch msg.Type {
	case "activate":
		tabID := sanitizeTabID(msg.Tab)
		if tabID == "" {
			return
		}
		tab := s.findTab(tabID)
		if tab == nil {
			return
		}
		state := s.getOrCreateState(tab)
		firstSubscription := client.subscribe(tabID)
		if state.hasSession() || state.statusValue() == "error" {
			if firstSubscription {
				if scrollback := state.scrollbackString(); scrollback != "" {
					client.send(map[string]any{"type": "scrollback", "tab": tabID, "data": scrollback})
				}
			}
			if state.hasSession() && msg.Cols > 0 && msg.Rows > 0 {
				state.resize(msg.Cols, msg.Rows)
			}
		} else {
			if err := s.spawnTab(tab, state, msg.Cols, msg.Rows); err != nil {
				client.send(map[string]any{"type": "tab_error", "id": tabID, "message": err.Error()})
			}
		}
		client.send(map[string]any{"type": "status", "tab": tabID, "status": state.statusValue()})
	case "input":
		tabID := sanitizeTabID(msg.Tab)
		state := s.getState(tabID)
		if state == nil {
			return
		}
		state.write(msg.Data)
	case "resize":
		tabID := sanitizeTabID(msg.Tab)
		state := s.getState(tabID)
		if state == nil {
			return
		}
		state.resize(msg.Cols, msg.Rows)
	case "create_tab":
		s.handleCreateTab(client, msg)
	case "delete_tab":
		s.handleDeleteTab(client, msg)
	case "rename_tab":
		s.handleRenameTab(client, msg)
	}
}

func (s *Server) handleCreateTab(client *Client, msg wsRequest) {
	var source *Tab
	if msg.SourceTabID != "" {
		source = s.findTab(sanitizeTabID(msg.SourceTabID))
	}
	if source == nil && msg.TemplateID != "" {
		source = s.findTemplate(sanitizeTabID(msg.TemplateID))
	}
	if source == nil {
		client.send(map[string]any{"type": "tab_error", "id": sanitizeTabID(msg.ID), "message": "Missing tab source"})
		return
	}
	created, err := s.registerRuntimeTab(source, msg.ID, msg.Label)
	if err != nil {
		client.send(map[string]any{"type": "tab_error", "id": sanitizeTabID(msg.ID), "message": err.Error()})
		return
	}
	s.broadcast(map[string]any{"type": "tab_added", "tab": serializeTab(*created)})
}

func (s *Server) handleDeleteTab(client *Client, msg wsRequest) {
	tabID := sanitizeTabID(msg.Tab)
	removed, nextTab, err := s.removeRuntimeTab(tabID)
	if err != nil {
		client.send(map[string]any{"type": "tab_error", "id": tabID, "message": err.Error()})
		return
	}
	s.broadcast(map[string]any{"type": "tab_removed", "tab": removed, "nextTab": nextTab})
}

func (s *Server) handleRenameTab(client *Client, msg wsRequest) {
	tabID := sanitizeTabID(msg.Tab)
	renamed, err := s.renameTab(tabID, msg.Label)
	if err != nil {
		client.send(map[string]any{"type": "tab_error", "id": tabID, "message": err.Error()})
		return
	}
	s.broadcast(map[string]any{"type": "tab_renamed", "tab": serializeTab(*renamed)})
}

func (s *Server) spawnTab(tab *Tab, state *TabState, cols, rows int) error {
	size := normalizeTerminalSize(cols, rows, state.colsValue(), state.rowsValue())
	env := withTerminalEnv(os.Environ())
	sess, err := terminal.Start(terminal.Command{Cmd: tab.Cmd, Args: append([]string(nil), state.argsValue()...), Cwd: s.cfg.CWD, Env: env}, terminal.Size{Cols: size.cols, Rows: size.rows})
	if err != nil {
		state.markError(err)
		return err
	}
	state.attach(sess, size.cols, size.rows)
	s.broadcastToTab(tab.ID, map[string]any{"type": "status", "tab": tab.ID, "status": "running"})
	go s.pumpOutput(tab.ID, state, sess)
	go s.waitSession(tab.ID, state, sess)
	log.Printf("[PTY] Spawned %s: %s %s", tab.Label, tab.Cmd, strings.Join(state.argsValue(), " "))
	return nil
}

func (s *Server) pumpOutput(tabID string, state *TabState, sess terminal.Session) {
	buf := make([]byte, 4096)
	for {
		n, err := sess.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			state.appendScrollback([]byte(data), s.cfg.MaxScrollback)
			s.broadcastToTab(tabID, map[string]any{"type": "output", "tab": tabID, "data": data})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("[PTY] read error on %s: %v", tabID, err)
			}
			return
		}
	}
}

func (s *Server) waitSession(tabID string, state *TabState, sess terminal.Session) {
	_, err := sess.Wait(context.Background())
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, os.ErrClosed) {
		log.Printf("[PTY] wait error on %s: %v", tabID, err)
	}
	state.detach(sess)
	s.broadcastToTab(tabID, map[string]any{"type": "exit", "tab": tabID})
	s.broadcastToTab(tabID, map[string]any{"type": "status", "tab": tabID, "status": "stopped"})
}

func (s *Server) closeAllStates() {
	s.mu.Lock()
	states := make([]*TabState, 0, len(s.states))
	for _, st := range s.states {
		states = append(states, st)
	}
	s.mu.Unlock()
	for _, st := range states {
		st.close()
	}
}

func (s *Server) isAuthed(r *http.Request) bool {
	cookie, err := r.Cookie("cct")
	return err == nil && cookie.Value == s.cfg.Token
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	if err := s.htmlTemplates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func setNoCache(w http.ResponseWriter) {
	for key, value := range noCacheHeaders {
		w.Header().Set(key, value)
	}
}

func mustJSONJS(v any) template.JS {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return template.JS(data)
}

func withTerminalEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+2)
	for _, entry := range env {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "TERM=") || strings.HasPrefix(upper, "COLORTERM=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered, "TERM=xterm-256color", "COLORTERM=truecolor")
	return filtered
}

type termSize struct{ cols, rows int }

func normalizeTerminalSize(cols, rows, fallbackCols, fallbackRows int) termSize {
	if cols < 10 {
		cols = fallbackCols
	}
	if rows < 5 {
		rows = fallbackRows
	}
	if cols < 10 {
		cols = 120
	}
	if rows < 5 {
		rows = 40
	}
	return termSize{cols: cols, rows: rows}
}

func prepareTabs(raw []Tab, isDefault bool) []*Tab {
	seen := map[string]struct{}{}
	prepared := make([]*Tab, 0, len(raw))
	for _, item := range raw {
		tab, ok := sanitizeTab(item, isDefault)
		if !ok {
			continue
		}
		if _, exists := seen[tab.ID]; exists {
			continue
		}
		seen[tab.ID] = struct{}{}
		prepared = append(prepared, &tab)
	}
	return prepared
}

func sanitizeTab(raw Tab, isDefault bool) (Tab, bool) {
	id := sanitizeTabID(raw.ID)
	cmd := strings.TrimSpace(raw.Cmd)
	if id == "" || cmd == "" {
		return Tab{}, false
	}
	label := sanitizeTabLabel(raw.Label, raw.BaseLabel, id)
	baseLabel := sanitizeTabLabel(raw.BaseLabel, raw.Label, label)
	templateID := sanitizeTabID(raw.TemplateID)
	if templateID == "" {
		templateID = id
	}
	return Tab{
		ID:         id,
		Label:      label,
		Cmd:        cmd,
		Args:       normalizeTabArgs(raw.Args),
		TemplateID: templateID,
		BaseLabel:  baseLabel,
		IsDefault:  isDefault,
	}, true
}

func cloneTabPtr(tab *Tab) *Tab {
	cp := *tab
	cp.Args = append([]string(nil), tab.Args...)
	return &cp
}

func serializeTab(tab Tab) TabView {
	return TabView{
		ID:         tab.ID,
		Label:      tab.Label,
		TemplateID: firstNonEmpty(tab.TemplateID, tab.ID),
		BaseLabel:  firstNonEmpty(tab.BaseLabel, tab.Label),
		IsDefault:  tab.IsDefault,
	}
}

func (s *Server) serializeTabs() []TabView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	views := make([]TabView, 0, len(s.tabs))
	for _, tab := range s.tabs {
		views = append(views, serializeTab(*tab))
	}
	return views
}

func (s *Server) serializeTemplates() []TabView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	views := make([]TabView, 0, len(s.tabTemplates))
	for _, tab := range s.tabTemplates {
		views = append(views, serializeTab(*tab))
	}
	return views
}

func (s *Server) findTab(id string) *Tab {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tab := s.findTabLocked(id); tab != nil {
		return cloneTabPtr(tab)
	}
	return nil
}

func (s *Server) findTemplate(id string) *Tab {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tab := s.findTemplateLocked(id); tab != nil {
		return cloneTabPtr(tab)
	}
	return nil
}

func (s *Server) findTabLocked(id string) *Tab {
	for _, tab := range s.tabs {
		if tab.ID == id {
			return tab
		}
	}
	return nil
}

func (s *Server) findTemplateLocked(id string) *Tab {
	for _, tab := range s.tabTemplates {
		if tab.ID == id {
			return tab
		}
	}
	return nil
}

func (s *Server) registerRuntimeTab(source *Tab, id, label string) (*Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextID := sanitizeTabID(id)
	if nextID == "" {
		return nil, fmt.Errorf("invalid tab id")
	}
	if s.findTabLocked(nextID) != nil {
		return nil, fmt.Errorf("tab already exists")
	}
	nextLabel := sanitizeTabLabel(label, source.BaseLabel, source.Label)
	tab := &Tab{
		ID:         nextID,
		Label:      nextLabel,
		Cmd:        source.Cmd,
		Args:       stripRuntimeResumeArgs(source.Args),
		TemplateID: firstNonEmpty(source.TemplateID, source.ID),
		BaseLabel:  sanitizeTabLabel(source.BaseLabel, source.Label, nextLabel),
		IsDefault:  false,
	}
	s.tabs = append(s.tabs, tab)
	s.persistRuntimeTabsLocked()
	return cloneTabPtr(tab), nil
}

func (s *Server) renameTab(tabID, nextLabel string) (*Tab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tab := s.findTabLocked(tabID)
	if tab == nil {
		return nil, fmt.Errorf("tab not found")
	}
	tab.Label = sanitizeTabLabel(nextLabel, tab.Label)
	s.persistRuntimeTabsLocked()
	return cloneTabPtr(tab), nil
}

func (s *Server) removeRuntimeTab(tabID string) (removed string, nextTab string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.defaultIDs[tabID]; ok {
		return "", "", fmt.Errorf("default tab cannot be deleted")
	}
	index := -1
	for i, tab := range s.tabs {
		if tab.ID == tabID {
			index = i
			break
		}
	}
	if index == -1 {
		return "", "", fmt.Errorf("tab not found")
	}
	fallback := s.chooseFallbackLocked(index, tabID)
	s.tabs = append(s.tabs[:index], s.tabs[index+1:]...)
	if st, ok := s.states[tabID]; ok {
		st.close()
		delete(s.states, tabID)
	}
	s.persistRuntimeTabsLocked()
	return tabID, fallback, nil
}

func (s *Server) chooseFallbackLocked(index int, removedID string) string {
	for i := index + 1; i < len(s.tabs); i++ {
		if _, ok := s.defaultIDs[s.tabs[i].ID]; ok {
			return s.tabs[i].ID
		}
	}
	for i := index - 1; i >= 0; i-- {
		if _, ok := s.defaultIDs[s.tabs[i].ID]; ok {
			return s.tabs[i].ID
		}
	}
	for _, tab := range s.tabs {
		if tab.ID != removedID {
			return tab.ID
		}
	}
	return ""
}

func (s *Server) getOrCreateState(tab *Tab) *TabState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[tab.ID]; ok {
		return st
	}
	st := &TabState{status: "idle", args: append([]string(nil), tab.Args...)}
	s.states[tab.ID] = st
	return st
}

func (s *Server) getState(tabID string) *TabState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[tabID]
}

func (s *Server) addClient(client *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[client] = struct{}{}
}

func (s *Server) removeClient(client *Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, client)
}

func (s *Server) broadcast(msg any) {
	payload, _ := json.Marshal(msg)
	s.clientsMu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.clientsMu.RUnlock()
	for _, client := range clients {
		client.sendRaw(payload)
	}
}

func (s *Server) broadcastToTab(tabID string, msg any) {
	payload, _ := json.Marshal(msg)
	s.clientsMu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for client := range s.clients {
		if client.isSubscribed(tabID) {
			clients = append(clients, client)
		}
	}
	s.clientsMu.RUnlock()
	for _, client := range clients {
		client.sendRaw(payload)
	}
}

func (c *Client) subscribe(tabID string) bool {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	_, exists := c.subscribed[tabID]
	c.subscribed[tabID] = struct{}{}
	return !exists
}

func (c *Client) isSubscribed(tabID string) bool {
	c.subsMu.RLock()
	defer c.subsMu.RUnlock()
	_, ok := c.subscribed[tabID]
	return ok
}

func (c *Client) send(msg any) {
	payload, _ := json.Marshal(msg)
	c.sendRaw(payload)
}

func (c *Client) sendRaw(payload []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *Server) persistRuntimeTabsLocked() {
	defaultLabels := map[string]string{}
	for _, tab := range s.tabs {
		if _, ok := s.defaultIDs[tab.ID]; !ok {
			continue
		}
		original := s.configuredDefaultLabel[tab.ID]
		if tab.Label != original {
			defaultLabels[tab.ID] = tab.Label
		}
	}
	runtimeTabs := make([]runtimeTab, 0)
	for _, tab := range s.tabs {
		if _, ok := s.defaultIDs[tab.ID]; ok {
			continue
		}
		runtimeTabs = append(runtimeTabs, runtimeTab{
			ID:         tab.ID,
			Label:      tab.Label,
			Cmd:        tab.Cmd,
			Args:       append([]string(nil), tab.Args...),
			TemplateID: firstNonEmpty(tab.TemplateID, tab.ID),
			BaseLabel:  firstNonEmpty(tab.BaseLabel, tab.Label),
		})
	}
	payload := runtimeTabsFile{Version: 2, DefaultLabels: defaultLabels, RuntimeTabs: runtimeTabs}
	if err := os.MkdirAll(filepath.Dir(s.cfg.StatePath), 0o755); err != nil {
		log.Printf("[web-terminal] failed to persist runtime tabs: %v", err)
		return
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		log.Printf("[web-terminal] failed to persist runtime tabs: %v", err)
		return
	}
	if err := os.WriteFile(s.cfg.StatePath, append(data, '\n'), 0o644); err != nil {
		log.Printf("[web-terminal] failed to persist runtime tabs: %v", err)
	}
}

func (s *Server) loadPersistedRuntimeTabs() (map[string]string, []Tab) {
	defaultLabels := map[string]string{}
	data, err := os.ReadFile(s.cfg.StatePath)
	if err != nil {
		return defaultLabels, nil
	}

	var legacy []runtimeTab
	if err := json.Unmarshal(data, &legacy); err == nil {
		runtimeTabs := make([]Tab, 0, len(legacy))
		for _, item := range legacy {
			tab, ok := normalizePersistedRuntimeTab(item, s.defaultIDs)
			if ok {
				runtimeTabs = append(runtimeTabs, tab)
			}
		}
		return defaultLabels, runtimeTabs
	}

	var file runtimeTabsFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("[web-terminal] failed to load runtime tabs: %v", err)
		return defaultLabels, nil
	}
	for id, label := range file.DefaultLabels {
		if _, ok := s.defaultIDs[id]; ok {
			defaultLabels[id] = sanitizeTabLabel(label, label)
		}
	}
	runtimeTabs := make([]Tab, 0, len(file.RuntimeTabs))
	for _, item := range file.RuntimeTabs {
		tab, ok := normalizePersistedRuntimeTab(item, s.defaultIDs)
		if ok {
			runtimeTabs = append(runtimeTabs, tab)
		}
	}
	return defaultLabels, runtimeTabs
}

func normalizePersistedRuntimeTab(raw runtimeTab, defaultIDs map[string]struct{}) (Tab, bool) {
	id := sanitizeTabID(raw.ID)
	if id == "" {
		return Tab{}, false
	}
	if _, ok := defaultIDs[id]; ok {
		return Tab{}, false
	}
	cmd := strings.TrimSpace(raw.Cmd)
	if cmd == "" {
		return Tab{}, false
	}
	label := sanitizeTabLabel(raw.Label, raw.BaseLabel, raw.TemplateID, cmd)
	baseLabel := sanitizeTabLabel(raw.BaseLabel, label)
	templateID := sanitizeTabID(raw.TemplateID)
	if templateID == "" {
		templateID = id
	}
	return Tab{ID: id, Label: label, Cmd: cmd, Args: stripRuntimeResumeArgs(raw.Args), TemplateID: templateID, BaseLabel: baseLabel, IsDefault: false}, true
}

func sanitizeTabID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if len(cleaned) > 48 {
		cleaned = cleaned[:48]
	}
	return cleaned
}

func sanitizeTabLabel(values ...string) string {
	for _, value := range values {
		label := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if label == "" {
			continue
		}
		runes := []rune(label)
		if len(runes) > 40 {
			return string(runes[:40])
		}
		return label
	}
	return "New Tab"
}

func normalizeTabArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func stripRuntimeResumeArgs(args []string) []string {
	clean := normalizeTabArgs(args)
	out := make([]string, 0, len(clean))
	for _, arg := range clean {
		if arg == "--continue" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (t *TabState) attach(sess terminal.Session, cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.session = sess
	t.status = "running"
	t.cols = cols
	t.rows = rows
}

func (t *TabState) detach(sess terminal.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == sess {
		t.session = nil
	}
	t.status = "stopped"
}

func (t *TabState) markError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = "error"
	msg := "\r\n[spawn failed] " + err.Error() + "\r\n"
	t.scrollback = append(t.scrollback, []byte(msg)...)
}

func (t *TabState) appendScrollback(data []byte, max int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scrollback = append(t.scrollback, data...)
	if len(t.scrollback) > max {
		t.scrollback = append([]byte(nil), t.scrollback[len(t.scrollback)-max:]...)
	}
}

func (t *TabState) scrollbackString() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.scrollback)
}

func (t *TabState) hasSession() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session != nil
}

func (t *TabState) statusValue() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == "" {
		return "idle"
	}
	return t.status
}

func (t *TabState) colsValue() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols
}

func (t *TabState) rowsValue() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rows
}

func (t *TabState) argsValue() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.args...)
}

func (t *TabState) resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session == nil {
		return
	}
	size := normalizeTerminalSize(cols, rows, t.cols, t.rows)
	if size.cols == t.cols && size.rows == t.rows {
		return
	}
	if err := t.session.Resize(size.cols, size.rows); err == nil {
		t.cols = size.cols
		t.rows = size.rows
	}
}

func (t *TabState) write(data string) {
	t.mu.Lock()
	sess := t.session
	t.mu.Unlock()
	if sess == nil || data == "" {
		return
	}
	_, _ = io.WriteString(sess, data)
}

func (t *TabState) close() {
	t.mu.Lock()
	sess := t.session
	t.session = nil
	t.status = "stopped"
	t.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
}
