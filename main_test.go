package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func testApp(t *testing.T) (*app, http.Handler) {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "linkshare.db"), testNow, defaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := newApp(db, slog.New(slog.NewTextHandler(io.Discard, nil)), "Me", defaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testNow }
	return a, a.routes()
}

func requestJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestOneTimeLinkLifecycle(t *testing.T) {
	_, handler := testApp(t)

	created := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{
		"url":"http://nas.local/article","title":"Homelab article","note":"Read this","target":"agents","submitted_by":"Me"
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body = %s", created.Code, created.Body.String())
	}
	var item link
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	wantExpiry := testNow.Add(defaultTTL).Format(time.RFC3339Nano)
	if item.ID == 0 || item.Target != "agents" || item.ExpiresAt != wantExpiry {
		t.Fatalf("unexpected created link: %+v", item)
	}

	waiting := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents", "")
	if waiting.Code != http.StatusOK || !strings.Contains(waiting.Body.String(), `"total":1`) {
		t.Fatalf("waiting response = %d %s", waiting.Code, waiting.Body.String())
	}

	consumed := requestJSON(t, handler, http.MethodDelete, "/api/v1/links/1", "")
	if consumed.Code != http.StatusNoContent || consumed.Body.Len() != 0 {
		t.Fatalf("consume response = %d %s", consumed.Code, consumed.Body.String())
	}
	missing := requestJSON(t, handler, http.MethodDelete, "/api/v1/links/1", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("second consume = %d %s", missing.Code, missing.Body.String())
	}
}

func TestExpiryHidesAndDeletesLinks(t *testing.T) {
	a, handler := testApp(t)
	if response := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{"url":"https://example.com","target":"owner","submitted_by":"agent"}`); response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	a.now = func() time.Time { return testNow.Add(defaultTTL) }

	response := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=owner", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":0`) {
		t.Fatalf("expired list = %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM links").Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired rows remaining = %d, err = %v", count, err)
	}
}

func TestLegacySchemaMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `CREATE TABLE links (
		id INTEGER PRIMARY KEY AUTOINCREMENT, url TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '', target TEXT NOT NULL, submitted_by TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, read_at TEXT, read_by TEXT,
		archived_at TEXT, archived_by TEXT)`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	for _, values := range []string{
		`(1,'https://example.com/waiting','','','owner','agent','2026-07-10T00:00:00Z','2026-07-10T00:00:00Z',NULL,NULL,NULL,NULL)`,
		`(2,'https://example.com/read','','','owner','agent','2026-07-10T00:00:00Z','2026-07-10T00:00:00Z','2026-07-11T00:00:00Z','Me',NULL,NULL)`,
		`(3,'https://example.com/archived','','','agents','agent','2026-07-10T00:00:00Z','2026-07-10T00:00:00Z',NULL,NULL,'2026-07-11T00:00:00Z','Me')`,
	} {
		if _, err := db.Exec(`INSERT INTO links VALUES ` + values); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openDB(path, testNow, defaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var item link
	if err := migrated.QueryRow(`SELECT id, url, title, note, target, submitted_by, created_at, expires_at FROM links`).Scan(
		&item.ID, &item.URL, &item.Title, &item.Note, &item.Target, &item.SubmittedBy, &item.CreatedAt, &item.ExpiresAt,
	); err != nil {
		t.Fatal(err)
	}
	if item.ID != 1 || item.ExpiresAt != testNow.Add(defaultTTL).Format(time.RFC3339Nano) {
		t.Fatalf("unexpected migrated link: %+v", item)
	}
	var count, historyRows int
	if err := migrated.QueryRow("SELECT COUNT(*) FROM links").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := migrated.QueryRow("SELECT COUNT(*) FROM links WHERE read_at IS NOT NULL OR archived_at IS NOT NULL").Scan(&historyRows); err != nil {
		t.Fatal(err)
	}
	if count != 1 || historyRows != 0 {
		t.Fatalf("migration count=%d historyRows=%d", count, historyRows)
	}
	if _, err := migrated.Exec(`INSERT INTO links (url,title,note,target,submitted_by,created_at,expires_at) VALUES ('https://example.com/new','','','owner','agent',?,?)`, testNow.Format(time.RFC3339Nano), testNow.Add(defaultTTL).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	var maxID int64
	if err := migrated.QueryRow("SELECT MAX(id) FROM links").Scan(&maxID); err != nil || maxID <= item.ID {
		t.Fatalf("autoincrement max id=%d err=%v", maxID, err)
	}
}

func TestLegacyAPICompatibility(t *testing.T) {
	_, handler := testApp(t)
	if response := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{"url":"https://example.com","target":"agents","submitted_by":"Me"}`); response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	unread := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&state=unread", "")
	if unread.Code != http.StatusOK || !strings.Contains(unread.Body.String(), `"total":1`) {
		t.Fatalf("legacy unread = %d %s", unread.Code, unread.Body.String())
	}
	read := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&state=read", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"total":0`) {
		t.Fatalf("legacy read = %d %s", read.Code, read.Body.String())
	}
	consumed := requestJSON(t, handler, http.MethodPatch, "/api/v1/links/1", `{"action":"mark_read","actor":"codex"}`)
	if consumed.Code != http.StatusOK || !strings.Contains(consumed.Body.String(), `"consumed":true`) {
		t.Fatalf("legacy consume = %d %s", consumed.Code, consumed.Body.String())
	}
}

func TestDiscoveryDocument(t *testing.T) {
	_, handler := testApp(t)
	response := requestJSON(t, handler, http.MethodGet, "/api/v1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("discovery status = %d; body = %s", response.Code, response.Body.String())
	}
	var document struct {
		Service        string                `json:"service"`
		APIVersion     string                `json:"api_version"`
		LinkTTLSeconds int64                 `json:"link_ttl_seconds"`
		Endpoints      []endpointDescription `json:"endpoints"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Service != "linkshare" || document.APIVersion != "v1" || document.LinkTTLSeconds != 604800 {
		t.Fatalf("unexpected discovery metadata: %+v", document)
	}
	found := make(map[string]string)
	for _, endpoint := range document.Endpoints {
		found[endpoint.Method+" "+endpoint.Path] = endpoint.Description
	}
	for _, expected := range []string{
		"GET /api/v1", "GET /api/v1/links", "POST /api/v1/links", "DELETE /api/v1/links/{id}",
		"PATCH /api/v1/links/{id}", "GET /healthz", "GET /", "GET /guide",
	} {
		if found[expected] == "" {
			t.Errorf("discovery document is missing %s", expected)
		}
	}
}

func TestValidationAndOriginProtection(t *testing.T) {
	_, handler := testApp(t)
	invalid := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{"url":"javascript:alert(1)","target":"owner","submitted_by":"agent"}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid URL status = %d; body = %s", invalid.Code, invalid.Body.String())
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/links/1", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", response.Code)
	}
}

func TestDuplicateLinkReplacesOriginal(t *testing.T) {
	_, handler := testApp(t)

	first := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{
		"url":"https://example.com/page","title":"Old title","note":"old note","target":"agents","submitted_by":"codex"
	}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s", first.Code, first.Body.String())
	}
	var original link
	if err := json.Unmarshal(first.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}

	second := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{
		"url":"https://example.com/page","title":"New title","note":"new note","target":"agents","submitted_by":"codex"
	}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("second create = %d %s", second.Code, second.Body.String())
	}
	var replacement link
	if err := json.Unmarshal(second.Body.Bytes(), &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.ID <= original.ID {
		t.Fatalf("replacement id %d should be greater than original id %d", replacement.ID, original.ID)
	}
	if replacement.Title != "New title" || replacement.Note != "new note" {
		t.Fatalf("replacement fields not updated: %+v", replacement)
	}

	if stale := requestJSON(t, handler, http.MethodDelete, "/api/v1/links/"+strconv.FormatInt(original.ID, 10), ""); stale.Code != http.StatusNotFound {
		t.Fatalf("consuming replaced id = %d; want 404", stale.Code)
	}
	waiting := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents", "")
	if waiting.Code != http.StatusOK || !strings.Contains(waiting.Body.String(), `"total":1`) {
		t.Fatalf("list after replace = %d %s", waiting.Code, waiting.Body.String())
	}
}

func TestDuplicateLinkAcrossTargetsIsIndependent(t *testing.T) {
	_, handler := testApp(t)
	for _, target := range []string{"agents", "owner"} {
		body := `{"url":"https://example.com/shared","target":"` + target + `","submitted_by":"codex"}`
		if response := requestJSON(t, handler, http.MethodPost, "/api/v1/links", body); response.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", target, response.Code, response.Body.String())
		}
	}
	for _, target := range []string{"agents", "owner"} {
		response := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target="+target, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":1`) {
			t.Fatalf("list %s = %d %s", target, response.Code, response.Body.String())
		}
	}
}

func TestPaginationAndTargetIsolation(t *testing.T) {
	_, handler := testApp(t)
	for i, target := range []string{"agents", "agents", "owner"} {
		body := `{"url":"https://example.com/` + target + `/` + strconv.Itoa(i) + `","target":"` + target + `","submitted_by":"tester"}`
		if response := requestJSON(t, handler, http.MethodPost, "/api/v1/links", body); response.Code != http.StatusCreated {
			t.Fatalf("create %s: %s", target, response.Body.String())
		}
	}
	first := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&limit=1", "")
	var page struct {
		Items  []link `json:"items"`
		Total  int    `json:"total"`
		Cursor *int64 `json:"next_before_id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Cursor == nil {
		t.Fatalf("unexpected first page: %+v", page)
	}
	second := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&limit=1&before_id="+strconv.FormatInt(*page.Cursor, 10), "")
	if !strings.Contains(second.Body.String(), `"total":2`) {
		t.Fatalf("unexpected second page: %s", second.Body.String())
	}
}

func TestIndexEscapesOwnerName(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "linkshare.db"), testNow, defaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := newApp(db, slog.New(slog.NewTextHandler(io.Discard, nil)), `"><script>alert(1)</script>`, defaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("owner name was not escaped: %s", response.Body.String())
	}
}
