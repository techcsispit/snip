package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// README: "Custom aliases are 3 to 20 letters, digits, `-` or `_`."
func TestAliasLengthBounds(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for _, tc := range []struct {
		alias string
		want  int
	}{
		{"ab", http.StatusBadRequest},
		{"abc", http.StatusCreated},
		{"a_b-1", http.StatusCreated},
		{strings.Repeat("x", 20), http.StatusCreated},
		{strings.Repeat("y", 21), http.StatusBadRequest},
	} {
		body := fmt.Sprintf(`{"url": "https://go.dev", "alias": %q}`, tc.alias)
		if rec := do(t, srv, "POST", "/api/links", body); rec.Code != tc.want {
			t.Errorf("alias %q (%d chars): got %d, want %d", tc.alias, len(tc.alias), rec.Code, tc.want)
		}
	}
}

// README: "A code that doesn't exist is a `404`."
func TestUnknownCodeIsNotFound(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/nope123"},
		{"DELETE", "/api/links/nope123"},
	} {
		if rec := do(t, srv, tc.method, tc.path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

// README: "A link with an expiry works until it expires, then returns `410`."
func TestExpiredLinkReturnsGone(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev", "alias": "short-lived", "expires_in": 1}`)

	rec := do(t, srv, "GET", "/short-lived", "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("before expiry: got %d to %q, want 302 to https://go.dev", rec.Code, rec.Header().Get("Location"))
	}

	time.Sleep(1100 * time.Millisecond) // expires_in is whole seconds
	if rec := do(t, srv, "GET", "/short-lived", ""); rec.Code != http.StatusGone {
		t.Fatalf("after expiry: got %d, want 410", rec.Code)
	}
}

// createLinks makes link-00, link-01, ... one after another, pausing so each
// has a different creation time (the list is ordered by it).
func createLinks(t *testing.T, srv *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		create(t, srv, fmt.Sprintf(`{"url": "https://go.dev", "alias": "link-%02d"}`, i))
		time.Sleep(5 * time.Millisecond)
	}
}

func listCodes(t *testing.T, srv *Server, path string) []string {
	t.Helper()
	rec := do(t, srv, "GET", path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: got %d, want 200", path, rec.Code)
	}
	var links []Link
	if err := json.Unmarshal(rec.Body.Bytes(), &links); err != nil {
		t.Fatalf("GET %s: bad JSON: %v", path, err)
	}
	codes := make([]string, len(links))
	for i, l := range links {
		codes[i] = l.Code
	}
	return codes
}

// README: "`GET /api/links?limit=N` | Newest links first, 20 by default"
func TestListIsNewestFirst(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	createLinks(t, srv, 5)

	got := strings.Join(listCodes(t, srv, "/api/links"), " ")
	if want := "link-04 link-03 link-02 link-01 link-00"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestListDefaultsToTwenty(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	createLinks(t, srv, 25)

	codes := listCodes(t, srv, "/api/links")
	if len(codes) != 20 {
		t.Fatalf("got %d links, want 20", len(codes))
	}
	if codes[0] != "link-24" || codes[19] != "link-05" {
		t.Fatalf("got %s ... %s, want the 20 newest, link-24 ... link-05", codes[0], codes[19])
	}
}

func TestListLimit(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	createLinks(t, srv, 25)

	got := strings.Join(listCodes(t, srv, "/api/links?limit=3"), " ")
	if want := "link-24 link-23 link-22"; got != want {
		t.Fatalf("limit=3: got %s, want %s", got, want)
	}
	if codes := listCodes(t, srv, "/api/links?limit=25"); len(codes) != 25 {
		t.Fatalf("limit=25: got %d links, want 25", len(codes))
	}
}
