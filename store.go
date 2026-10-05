package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	s := &Store{
		links:    make(map[string]*Link),
		filePath: filePath,
		saveCh:   make(chan struct{}, 1),
	}
	s.load()
	s.wg.Add(1)
	go s.saveWorker()
	return s
}

func (s *Store) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}

	var persisted []persistedLink
	if err := json.Unmarshal(data, &persisted); err != nil {
		return
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
}

func (s *Store) saveWorker() {
	defer s.wg.Done()
	for range s.saveCh {
		s.persist()
	}
}

func (s *Store) persist() {
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
		return
	}

	dir := filepath.Dir(s.filePath)
	tmpFile, err := os.CreateTemp(dir, "links.*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		return
	}
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
}

func (s *Store) Get(code string) *Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	link := s.links[code]
	if link == nil {
		return nil
	}
	// Return a copy so internal state doesn't escape
	copy := *link
	return &copy
}

func (s *Store) Exists(code string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.links[code]
	return ok
}

func (s *Store) Save(link *Link) {
	s.mu.Lock()
	s.links[link.Code] = link
	s.mu.Unlock()
	s.requestSave()
}

func (s *Store) Delete(code string) {
	s.mu.Lock()
	delete(s.links, code)
	s.mu.Unlock()
	s.requestSave()
}

func (s *Store) IncrementClicks(code string) {
	s.mu.Lock()
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
		// Return copies so internal state doesn't escape
		copy := *link
		list = append(list, &copy)
	}
	return list
}
