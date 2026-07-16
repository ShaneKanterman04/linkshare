package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

const (
	maxBodyBytes = 16 << 10
	defaultLimit = 50
	maxLimit     = 200
)

//go:embed web/* web/assets/*
var webFiles embed.FS

type config struct {
	Addr      string
	DBPath    string
	OwnerName string
}

type app struct {
	db        *sql.DB
	log       *slog.Logger
	ownerName string
	index     *template.Template
	static    http.Handler
	now       func() time.Time
}

type link struct {
	ID          int64   `json:"id"`
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	Note        string  `json:"note"`
	Target      string  `json:"target"`
	SubmittedBy string  `json:"submitted_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	ReadAt      *string `json:"read_at"`
	ReadBy      *string `json:"read_by"`
	ArchivedAt  *string `json:"archived_at"`
	ArchivedBy  *string `json:"archived_by"`
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type endpointDescription struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

var publicEndpoints = []endpointDescription{
	{Method: http.MethodGet, Path: "/api/v1", Description: "List available Linkshare endpoints."},
	{Method: http.MethodGet, Path: "/api/v1/links", Description: "List and filter links in an inbox."},
	{Method: http.MethodPost, Path: "/api/v1/links", Description: "Create a link for the owner or agents."},
	{Method: http.MethodPatch, Path: "/api/v1/links/{id}", Description: "Mark a link read or unread, archive it, or restore it."},
	{Method: http.MethodGet, Path: "/healthz", Description: "Check service and database health."},
	{Method: http.MethodGet, Path: "/", Description: "Open the Linkshare web interface."},
	{Method: http.MethodGet, Path: "/guide", Description: "Open the human-readable agent guide."},
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	db, err := openDB(cfg.DBPath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	a, err := newApp(db, logger, cfg.OwnerName)
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown", "error", err)
		}
	}()

	logger.Info("linkshare listening", "address", cfg.Addr, "database", cfg.DBPath)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{
		Addr:      envOr("LINKSHARE_ADDR", ":8080"),
		DBPath:    envOr("LINKSHARE_DB", "./linkshare.db"),
		OwnerName: envOr("LINKSHARE_OWNER_NAME", "Me"),
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		`CREATE TABLE IF NOT EXISTS links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL CHECK (target IN ('owner', 'agents')),
			submitted_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			read_at TEXT,
			read_by TEXT,
			archived_at TEXT,
			archived_by TEXT
		)`,
		"CREATE INDEX IF NOT EXISTS links_target_state_id ON links(target, archived_at, read_at, id DESC)",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("database setup: %w", err)
		}
	}
	return db, nil
}

func newApp(db *sql.DB, logger *slog.Logger, ownerName string) (*app, error) {
	indexBytes, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	index, err := template.New("index").Parse(string(indexBytes))
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(webFiles, "web/assets")
	if err != nil {
		return nil, err
	}
	return &app{
		db: db, log: logger, ownerName: ownerName, index: index,
		static: http.StripPrefix("/assets/", http.FileServer(http.FS(assets))),
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.indexPage)
	mux.HandleFunc("GET /guide", a.guidePage)
	mux.Handle("GET /assets/", a.static)
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/v1", a.discovery)
	mux.HandleFunc("GET /api/v1/links", a.listLinks)
	mux.HandleFunc("POST /api/v1/links", a.createLink)
	mux.HandleFunc("PATCH /api/v1/links/{id}", a.patchLink)
	return a.logging(mux)
}

func (a *app) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		a.log.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

func (a *app) discovery(w http.ResponseWriter, _ *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{
		"service":     "linkshare",
		"api_version": "v1",
		"description": "Two-way link inbox for an owner and coding agents.",
		"endpoints":   publicEndpoints,
	})
}

func (a *app) indexPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.index.Execute(w, struct{ OwnerName string }{a.ownerName}); err != nil {
		a.log.Error("render index", "error", err)
	}
}

func (a *app) guidePage(w http.ResponseWriter, r *http.Request) {
	data, err := webFiles.ReadFile("web/guide.html")
	if err != nil {
		http.Error(w, "guide unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		a.writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) createLink(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		a.writeError(w, http.StatusForbidden, "cross_origin_denied", "cross-origin requests are not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		a.writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return
	}
	var input struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Note        string `json:"note"`
		Target      string `json:"target"`
		SubmittedBy string `json:"submitted_by"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.URL = strings.TrimSpace(input.URL)
	input.Title = strings.TrimSpace(input.Title)
	input.Note = strings.TrimSpace(input.Note)
	input.SubmittedBy = strings.TrimSpace(input.SubmittedBy)
	if message := validateLink(input.URL, input.Title, input.Note, input.Target, input.SubmittedBy); message != "" {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_link", message)
		return
	}

	now := a.now().Format(time.RFC3339Nano)
	result, err := a.db.ExecContext(r.Context(), `
		INSERT INTO links (url, title, note, target, submitted_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, input.URL, input.Title, input.Note, input.Target, input.SubmittedBy, now, now)
	if err != nil {
		a.log.Error("create link", "error", err)
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not save link")
		return
	}
	id, _ := result.LastInsertId()
	item, err := a.getLink(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not load saved link")
		return
	}
	a.writeJSON(w, http.StatusCreated, item)
}

func (a *app) listLinks(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target != "owner" && target != "agents" {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_target", "target must be owner or agents")
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "active"
	}
	condition, ok := stateCondition(state)
	if !ok {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_state", "state must be active, unread, read, archived, or all")
		return
	}
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLimit {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			a.writeError(w, http.StatusUnprocessableEntity, "invalid_cursor", "before_id must be a positive integer")
			return
		}
		beforeID = parsed
	}

	baseWhere := "target = ? AND " + condition
	var total int
	if err := a.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM links WHERE "+baseWhere, target).Scan(&total); err != nil {
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not count links")
		return
	}
	query := `SELECT id, url, title, note, target, submitted_by, created_at, updated_at,
		read_at, read_by, archived_at, archived_by FROM links WHERE ` + baseWhere
	args := []any{target}
	if beforeID > 0 {
		query += " AND id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not load links")
		return
	}
	defer rows.Close()
	items := make([]link, 0, limit)
	for rows.Next() {
		item, err := scanLink(rows)
		if err != nil {
			a.writeError(w, http.StatusInternalServerError, "database_error", "could not read links")
			return
		}
		items = append(items, item)
	}
	var next *int64
	if len(items) > limit {
		items = items[:limit]
		cursor := items[len(items)-1].ID
		next = &cursor
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "next_before_id": next})
}

func (a *app) patchLink(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		a.writeError(w, http.StatusForbidden, "cross_origin_denied", "cross-origin requests are not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		a.writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		a.writeError(w, http.StatusBadRequest, "invalid_id", "link id must be a positive integer")
		return
	}
	var input struct {
		Action string `json:"action"`
		Actor  string `json:"actor"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.Actor = strings.TrimSpace(input.Actor)
	if input.Actor == "" || len(input.Actor) > 80 {
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_actor", "actor is required and must be at most 80 characters")
		return
	}
	now := a.now().Format(time.RFC3339Nano)
	var query string
	var args []any
	switch input.Action {
	case "mark_read":
		query = "UPDATE links SET read_at = ?, read_by = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL"
		args = []any{now, input.Actor, now, id}
	case "mark_unread":
		query = "UPDATE links SET read_at = NULL, read_by = NULL, updated_at = ? WHERE id = ? AND archived_at IS NULL"
		args = []any{now, id}
	case "archive":
		query = "UPDATE links SET archived_at = ?, archived_by = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL"
		args = []any{now, input.Actor, now, id}
	case "restore":
		query = "UPDATE links SET archived_at = NULL, archived_by = NULL, updated_at = ? WHERE id = ? AND archived_at IS NOT NULL"
		args = []any{now, id}
	default:
		a.writeError(w, http.StatusUnprocessableEntity, "invalid_action", "action must be mark_read, mark_unread, archive, or restore")
		return
	}
	result, err := a.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not update link")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var exists int
		err := a.db.QueryRowContext(r.Context(), "SELECT 1 FROM links WHERE id = ?", id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			a.writeError(w, http.StatusNotFound, "not_found", "link not found")
		} else {
			a.writeError(w, http.StatusConflict, "invalid_transition", "action is not valid for the link's current state")
		}
		return
	}
	item, err := a.getLink(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "database_error", "could not load updated link")
		return
	}
	a.writeJSON(w, http.StatusOK, item)
}

func stateCondition(state string) (string, bool) {
	switch state {
	case "active":
		return "archived_at IS NULL", true
	case "unread":
		return "archived_at IS NULL AND read_at IS NULL", true
	case "read":
		return "archived_at IS NULL AND read_at IS NOT NULL", true
	case "archived":
		return "archived_at IS NOT NULL", true
	case "all":
		return "1 = 1", true
	default:
		return "", false
	}
}

func validateLink(rawURL, title, note, target, submittedBy string) string {
	if rawURL == "" || len(rawURL) > 2048 {
		return "url is required and must be at most 2048 characters"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "url must be a valid http or https address with a host"
	}
	if len(title) > 200 {
		return "title must be at most 200 characters"
	}
	if len(note) > 4000 {
		return "note must be at most 4000 characters"
	}
	if target != "owner" && target != "agents" {
		return "target must be owner or agents"
	}
	if submittedBy == "" || len(submittedBy) > 80 {
		return "submitted_by is required and must be at most 80 characters"
	}
	return ""
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(s scanner) (link, error) {
	var item link
	var readAt, readBy, archivedAt, archivedBy sql.NullString
	err := s.Scan(&item.ID, &item.URL, &item.Title, &item.Note, &item.Target, &item.SubmittedBy,
		&item.CreatedAt, &item.UpdatedAt, &readAt, &readBy, &archivedAt, &archivedBy)
	if err != nil {
		return item, err
	}
	item.ReadAt = nullString(readAt)
	item.ReadBy = nullString(readBy)
	item.ArchivedAt = nullString(archivedAt)
	item.ArchivedBy = nullString(archivedBy)
	return item, nil
}

func nullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func (a *app) getLink(ctx context.Context, id int64) (link, error) {
	row := a.db.QueryRowContext(ctx, `SELECT id, url, title, note, target, submitted_by, created_at, updated_at,
		read_at, read_by, archived_at, archived_by FROM links WHERE id = ?`, id)
	return scanLink(row)
}

func (a *app) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		a.log.Error("write json", "error", err)
	}
}

func (a *app) writeError(w http.ResponseWriter, status int, code, message string) {
	var response apiError
	response.Error.Code = code
	response.Error.Message = message
	a.writeJSON(w, status, response)
}
