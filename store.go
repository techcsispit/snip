package main

import (
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
	mu    sync.Mutex
	links map[string]*Link
}

func NewStore() *Store {
	return &Store{links: make(map[string]*Link)}
}

func (s *Store) Get(code string) *Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links[code]
}

func (s *Store) Exists(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.links[code]
	return ok
}

func (s *Store) Save(link *Link) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[link.Code] = link
}

func (s *Store) Delete(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.links, code)
}

func (s *Store) List() []*Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*Link, 0, len(s.links))
	for _, link := range s.links {
		list = append(list, link)
	}
	return list
}

func (s *Store) Click(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[code].Clicks++
}
