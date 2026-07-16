package main

import (
	"bytes"
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
)

func testApp(t *testing.T) (*app, http.Handler) {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "linkshare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := newApp(db, slog.New(slog.NewTextHandler(io.Discard, nil)), "Me")
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
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

func TestLinkLifecycle(t *testing.T) {
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
	if item.ID == 0 || item.Target != "agents" || item.ReadAt != nil {
		t.Fatalf("unexpected created link: %+v", item)
	}

	unread := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&state=unread", "")
	if unread.Code != http.StatusOK || !strings.Contains(unread.Body.String(), `"total":1`) {
		t.Fatalf("unread response = %d %s", unread.Code, unread.Body.String())
	}

	read := requestJSON(t, handler, http.MethodPatch, "/api/v1/links/1", `{"action":"mark_read","actor":"codex-research"}`)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"read_by":"codex-research"`) {
		t.Fatalf("read response = %d %s", read.Code, read.Body.String())
	}

	archived := requestJSON(t, handler, http.MethodPatch, "/api/v1/links/1", `{"action":"archive","actor":"codex-research"}`)
	if archived.Code != http.StatusOK {
		t.Fatalf("archive response = %d %s", archived.Code, archived.Body.String())
	}
	restored := requestJSON(t, handler, http.MethodPatch, "/api/v1/links/1", `{"action":"restore","actor":"Me"}`)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"read_by":"codex-research"`) {
		t.Fatalf("restore should preserve read receipt: %d %s", restored.Code, restored.Body.String())
	}
}

func TestValidationAndOriginProtection(t *testing.T) {
	_, handler := testApp(t)

	invalid := requestJSON(t, handler, http.MethodPost, "/api/v1/links", `{"url":"javascript:alert(1)","target":"owner","submitted_by":"agent"}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid URL status = %d; body = %s", invalid.Code, invalid.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/links", strings.NewReader(`{"url":"https://example.com","target":"owner","submitted_by":"agent"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", response.Code)
	}
}

func TestPaginationAndTargetIsolation(t *testing.T) {
	_, handler := testApp(t)
	for _, target := range []string{"agents", "agents", "owner"} {
		body := `{"url":"https://example.com/` + target + `","target":"` + target + `","submitted_by":"tester"}`
		if response := requestJSON(t, handler, http.MethodPost, "/api/v1/links", body); response.Code != http.StatusCreated {
			t.Fatalf("create %s: %s", target, response.Body.String())
		}
	}

	first := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&state=all&limit=1", "")
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
	second := requestJSON(t, handler, http.MethodGet, "/api/v1/links?target=agents&state=all&limit=1&before_id="+strconv.FormatInt(*page.Cursor, 10), "")
	if !strings.Contains(second.Body.String(), `"total":2`) {
		t.Fatalf("unexpected second page: %s", second.Body.String())
	}
}

func TestIndexEscapesOwnerName(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "linkshare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := newApp(db, slog.New(slog.NewTextHandler(io.Discard, nil)), `"><script>alert(1)</script>`)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("owner name was not escaped: %s", response.Body.String())
	}
}
