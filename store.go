package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Link struct {
	Code      string     `json:"code"`
	URL       string     `json:"url"`
	Clicks    int        `json:"clicks"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	deleteToken string
}

type Store struct {
	mu       sync.RWMutex
	links    map[string]*Link
	filePath string
	saveCh   chan struct{}
	wg       sync.WaitGroup
	closed   bool
	lockFile *os.File
}

type persistedLink struct {
	Code        string     `json:"code"`
	URL         string     `json:"url"`
	Clicks      int        `json:"clicks"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	DeleteToken string     `json:"delete_token"`
}

func NewStore(filePath string) *Store {
	s, err := OpenStore(filePath)
	if err != nil {
		panic(err)
	}
	return s
}

// OpenStore opens a store for exclusive use by this process. Keeping the lock
// for the lifetime of the store prevents two server instances from replacing
// the same JSON file with independent, stale snapshots.
func OpenStore(filePath string) (*Store, error) {
	lockFile, err := acquireStoreLock(storeLockPath(filePath))
	if err != nil {
		return nil, fmt.Errorf("could not acquire exclusive lock for store %q (another server may be using it): %w", filePath, err)
	}

	s := &Store{
		links:    make(map[string]*Link),
		filePath: filePath,
		saveCh:   make(chan struct{}, 1),
		lockFile: lockFile,
	}

	if err := s.load(); err != nil {
		if releaseErr := releaseStoreLock(lockFile); releaseErr != nil {
			log.Printf("could not release lock for %s: %v", filePath, releaseErr)
		}
		return nil, err
	}

	s.wg.Add(1)
	go s.saveWorker()
	return s, nil
}

func storeLockPath(filePath string) string {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filepath.Clean(filePath)
	}
	if resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(absPath)); err == nil {
		absPath = filepath.Join(resolvedDir, filepath.Base(absPath))
	}
	if runtime.GOOS == "windows" {
		absPath = strings.ToLower(absPath)
	}
	sum := sha256.Sum256([]byte(absPath))
	return filepath.Join(os.TempDir(), fmt.Sprintf("snip-store-%x.lock", sum))
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // genuinely missing file is not an error
		}
		return err // unreadable file is an error
	}

	var persisted []persistedLink
	if err := json.Unmarshal(data, &persisted); err != nil {
		// Quarantine corrupt data; preserve original for inspection.
		corruptPath := s.filePath + ".corrupt"
		if _, statErr := os.Stat(corruptPath); statErr == nil {
			return fmt.Errorf("cannot quarantine corrupt storage: target already exists: %s: %w", corruptPath, err)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("cannot check corrupt storage path %s: %w", corruptPath, statErr)
		}
		if renameErr := os.Rename(s.filePath, corruptPath); renameErr != nil {
			return fmt.Errorf("cannot quarantine corrupt storage %s: %w (original parse error: %v)", s.filePath, renameErr, err)
		}
		return fmt.Errorf("corrupt storage quarantined at %s: %w", corruptPath, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pl := range persisted {
		s.links[pl.Code] = &Link{
			Code:        pl.Code,
			URL:         pl.URL,
			Clicks:      pl.Clicks,
			CreatedAt:   pl.CreatedAt,
			ExpiresAt:   pl.ExpiresAt,
			deleteToken: pl.DeleteToken,
		}
	}
	return nil
}

func (s *Store) saveWorker() {
	defer s.wg.Done()
	for range s.saveCh {
		if err := s.persist(); err != nil {
			log.Printf("could not save %s: %v", s.filePath, err)
		}
	}
}

func (s *Store) persist() error {
	s.mu.RLock()
	persisted := make([]persistedLink, 0, len(s.links))
	for _, link := range s.links {
		persisted = append(persisted, persistedLink{
			Code:        link.Code,
			URL:         link.URL,
			Clicks:      link.Clicks,
			CreatedAt:   link.CreatedAt,
			ExpiresAt:   link.ExpiresAt,
			DeleteToken: link.deleteToken,
		})
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}

	// STORE_PATH may point into a folder that does not exist yet.
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, "links.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func (s *Store) requestSave() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return
	}

	select {
	case s.saveCh <- struct{}{}:
	default:
	}
}

func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	close(s.saveCh)
	s.wg.Wait()
	if err := releaseStoreLock(s.lockFile); err != nil {
		log.Printf("could not release lock for %s: %v", s.filePath, err)
	}
}

func (s *Store) Get(code string) *Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	link := s.links[code]
	if link == nil {
		return nil
	}
	// Return a copy so internal state doesn't escape.
	copy := *link
	return &copy
}

// Create stores the link only if its code is not taken. The check and the
// insert happen under one lock, so of any number of concurrent calls with the
// same code exactly one returns true.
func (s *Store) Create(link *Link) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if _, taken := s.links[link.Code]; taken {
		s.mu.Unlock()
		return false
	}
	s.links[link.Code] = link
	s.mu.Unlock()
	s.requestSave()
	return true
}

func (s *Store) Delete(code string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	delete(s.links, code)
	s.mu.Unlock()
	s.requestSave()
}

func (s *Store) IncrementClicks(code string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if link, ok := s.links[code]; ok {
		link.Clicks++
	}
	s.mu.Unlock()
	s.requestSave()
}

func (s *Store) List() []*Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Link, 0, len(s.links))
	for _, link := range s.links {
		// Return copies so internal state doesn't escape.
		copy := *link
		list = append(list, &copy)
	}
	return list
}
