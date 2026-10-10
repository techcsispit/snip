package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// STORE_PATH may point into a folder that does not exist yet. The README says
// links are persisted to that file and restored on restart, so the first save
// has to create the folder.
func TestPersistsIntoMissingDirectory(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "data", "links.json")

	srv1 := NewServer(storePath)
	create(t, srv1, `{"url": "https://go.dev", "alias": "godev"}`)
	srv1.store.Close()

	srv2 := NewServer(storePath)
	defer srv2.store.Close()
	rec := do(t, srv2, "GET", "/godev", "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://go.dev" {
		t.Fatalf("after restart: got %d to %q, want 302 to https://go.dev", rec.Code, rec.Header().Get("Location"))
	}
}

// lockedBuffer lets the save worker log while the test reads the output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// If the file really cannot be written (here its parent "folder" is a regular
// file, so it can never be created), the failure must be reported, not dropped.
func TestSaveFailureIsLogged(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}

	var logged lockedBuffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	srv := NewServer(filepath.Join(blocker, "links.json"))
	create(t, srv, `{"url": "https://go.dev", "alias": "godev"}`)
	srv.store.Close() // waits for the save worker to finish

	if !strings.Contains(logged.String(), "links.json") {
		t.Fatalf("a failed save left no log line, got %q", logged.String())
	}
}
