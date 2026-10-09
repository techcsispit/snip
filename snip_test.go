package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://go.dev"}`)
	rec := do(t, srv, "GET", "/"+link["code"].(string), "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestURLWithoutScheme(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "go.dev"}`)

	rec := do(t, srv, "GET", "/"+link["code"].(string), "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCustomAlias(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	if link["code"] != "hack-2026" {
		t.Fatalf("got code %v", link["code"])
	}
}

func TestBadInputIsRejected(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	for _, body := range []string{`{"url": ""}`, `not json`, `{"url": "https://go.dev", "alias": "a!"}`} {
		if rec := do(t, srv, "POST", "/api/links", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", body, rec.Code)
		}
	}
}

func TestStatsForUnknownLink(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	if rec := do(t, srv, "GET", "/api/links/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestDeleteWithToken(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
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
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	create(t, srv, `{"url": "https://go.dev"}`)
	create(t, srv, `{"url": "https://github.com"}`)
	var links []Link
	json.Unmarshal(do(t, srv, "GET", "/api/links", "").Body.Bytes(), &links)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}
}

func TestPersistence(t *testing.T) {
	storePath := t.TempDir() + "/links.json"

	// Create first server and add links
	srv1 := NewServer(storePath)
	create(t, srv1, `{"url": "https://go.dev", "alias": "persist1"}`)
	link2 := create(t, srv1, `{"url": "https://github.com", "alias": "persist2"}`)

	// Click one link to increment counter
	do(t, srv1, "GET", "/persist1", "")

	srv1.store.Close()

	// Create second server with same path
	srv2 := NewServer(storePath)
	defer srv2.store.Close()

	// Verify links were restored
	rec1 := do(t, srv2, "GET", "/api/links/persist1", "")
	if rec1.Code != http.StatusOK {
		t.Fatalf("persist1 not found after restart")
	}

	var restored Link
	json.Unmarshal(rec1.Body.Bytes(), &restored)
	if restored.Code != "persist1" || restored.URL != "https://go.dev" {
		t.Errorf("link data not restored correctly")
	}
	if restored.Clicks != 1 {
		t.Errorf("click count: got %d, want 1", restored.Clicks)
	}

	// Verify delete token was restored
	rec2 := do(t, srv2, "DELETE", "/api/links/persist2", "", "X-Delete-Token", link2["delete_token"].(string))
	if rec2.Code != http.StatusNoContent {
		t.Errorf("delete with restored token failed: got %d", rec2.Code)
	}
}

func TestConcurrentAccess(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()

	link := create(t, srv, `{"url": "https://example.com", "alias": "concurrent"}`)
	token := link["delete_token"].(string)

	// Concurrent clicks, creates, reads, and a delete
	done := make(chan bool)

	// 20 concurrent clicks
	for i := 0; i < 20; i++ {
		go func() {
			do(t, srv, "GET", "/concurrent", "")
			done <- true
		}()
	}

	// 10 concurrent creates
	for i := 0; i < 10; i++ {
		go func(n int) {
			create(t, srv, `{"url": "https://example.com"}`)
			done <- true
		}(i)
	}

	// 10 concurrent stats reads
	for i := 0; i < 10; i++ {
		go func() {
			do(t, srv, "GET", "/api/links/concurrent", "")
			done <- true
		}()
	}

	// Wait for all operations to complete
	for i := 0; i < 40; i++ {
		<-done
	}

	// Verify the link still exists and has correct click count
	rec := do(t, srv, "GET", "/api/links/concurrent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("concurrent link not found")
	}

	var finalLink Link
	json.Unmarshal(rec.Body.Bytes(), &finalLink)
	if finalLink.Clicks != 20 {
		t.Errorf("clicks after concurrent access: got %d, want 20", finalLink.Clicks)
	}

	// Verify delete token still matches
	rec2 := do(t, srv, "DELETE", "/api/links/concurrent", "", "X-Delete-Token", token)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("delete after concurrent access failed: got %d", rec2.Code)
	}

	// Verify it's actually deleted
	rec3 := do(t, srv, "GET", "/api/links/concurrent", "")
	if rec3.Code != http.StatusNotFound {
		t.Errorf("link should be deleted: got %d", rec3.Code)
	}
}

func TestRepeatedPersistence(t *testing.T) {
	storePath := t.TempDir() + "/links.json"
	srv := NewServer(storePath)

	// Create links and trigger multiple saves
	for i := 0; i < 10; i++ {
		create(t, srv, `{"url": "https://example.com"}`)
	}

	// Force some clicks to trigger more saves
	links := srv.store.List()
	if len(links) != 10 {
		t.Fatalf("expected 10 links, got %d", len(links))
	}

	for i := 0; i < 5; i++ {
		do(t, srv, "GET", "/"+links[i].Code, "")
	}

	srv.store.Close()

	// Reload and verify all data persisted correctly
	srv2 := NewServer(storePath)
	defer srv2.store.Close()

	links2 := srv2.store.List()
	if len(links2) != 10 {
		t.Fatalf("after reload: expected 10 links, got %d", len(links2))
	}

	clickCount := 0
	for _, link := range links2 {
		clickCount += link.Clicks
	}
	if clickCount != 5 {
		t.Errorf("after reload: expected 5 total clicks, got %d", clickCount)
	}
}

func TestConcurrentFollows(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	wg.Add(concurrentRequests)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	wg.Wait()

	rec := do(t, srv, "GET", "/api/links/"+link["code"].(string), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("stats: got status %d, want 200", rec.Code)
	}
	var stats Link
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.Clicks != concurrentRequests {
		t.Fatalf("clicks mismatch: got %d, want %d", stats.Clicks, concurrentRequests)
	}
}

func TestConcurrentFollowAndList(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	listRequests := 100
	wg.Add(concurrentRequests + listRequests)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	for i := 0; i < listRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/api/links", "")

			if rec.Code != http.StatusOK {
				t.Errorf("list: got status %d, want 200", rec.Code)
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentFollowAndDelete(t *testing.T) {
	srv := NewServer(t.TempDir() + "/links.json")
	defer srv.store.Close()
	link := create(t, srv, `{"url": "https://hacktoberfest.com", "alias": "hack-2026"}`)
	var wg sync.WaitGroup
	concurrentRequests := 10000
	wg.Add(concurrentRequests + 1)

	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			rec := do(t, srv, "GET", "/"+link["code"].(string), "")

			if rec.Code != http.StatusFound && rec.Code != http.StatusNotFound {
				t.Errorf("got %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}()
	}

	go func() {
		defer wg.Done()

		rec := do(t, srv, "DELETE", "/api/links/"+link["code"].(string), "", "X-Delete-Token", link["delete_token"].(string))

		if rec.Code != http.StatusNoContent && rec.Code != http.StatusNotFound {
			t.Errorf("delete: got status %d", rec.Code)
		}
	}()

	wg.Wait()

	rec := do(t, srv, "GET", "/api/links/"+link["code"].(string), "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: got status %d, want 404", rec.Code)
	}
}
