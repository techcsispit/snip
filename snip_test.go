package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(t *testing.T, srv *Server, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func create(t *testing.T, srv *Server, body string) map[string]any {
	t.Helper()
	rec := do(t, srv, "POST", "/api/links", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201: %s", rec.Code, rec.Body)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestShortenAndFollow(t *testing.T) {
	srv := NewServer()
	link := create(t, srv, `{"url": "https://go.dev"}`)
	rec := do(t, srv, "GET", "/"+link["code"].(string), "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestURLWithoutScheme(t *testing.T) {
        srv := NewServer()
        link := create(t, srv, `{"url": "go.dev"}`)

        rec := do(t, srv, "GET", "/"+link["code"].(string), "")
        if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
                t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
        }
}

func TestCustomAlias(t *testing.T) {
	srv := NewServer()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	if link["code"] != "hack-2026" {
		t.Fatalf("got code %v", link["code"])
	}
}

func TestBadInputIsRejected(t *testing.T) {
	srv := NewServer()
	for _, body := range []string{`{"url": ""}`, `not json`, `{"url": "https://go.dev", "alias": "a!"}`} {
		if rec := do(t, srv, "POST", "/api/links", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", body, rec.Code)
		}
	}
}

func TestStatsForUnknownLink(t *testing.T) {
	if rec := do(t, NewServer(), "GET", "/api/links/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestDeleteWithToken(t *testing.T) {
	srv := NewServer()
	link := create(t, srv, `{"url": "https://go.dev", "alias": "gone"}`)
	rec := do(t, srv, "DELETE", "/api/links/gone", "", "X-Delete-Token", link["delete_token"].(string))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/links/gone", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: got %d", rec.Code)
	}
}

func TestListLinks(t *testing.T) {
	srv := NewServer()
	create(t, srv, `{"url": "https://go.dev"}`)
	create(t, srv, `{"url": "https://github.com"}`)
	var links []Link
	json.Unmarshal(do(t, srv, "GET", "/api/links", "").Body.Bytes(), &links)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
}
