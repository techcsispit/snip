package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
)

//go:embed static/index.html
var static embed.FS

const codeChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type Server struct {
	store *Store
	mux   *http.ServeMux
}

func NewServer(storePath string) *Server {
	s := &Server{store: NewStore(storePath), mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("POST /api/links", s.createLink)
	s.mux.HandleFunc("GET /api/links", s.listLinks)
	s.mux.HandleFunc("GET /api/links/{code}", s.linkStats)
	s.mux.HandleFunc("DELETE /api/links/{code}", s.deleteLink)
	s.mux.HandleFunc("GET /{code}", s.follow)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func randomString(chars string, n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func (s *Server) newCode() string {
	for {
		code := randomString(codeChars, 6)
		if !s.store.Exists(code) {
			return code
		}
	}
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	page, _ := static.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

type createRequest struct {
	URL       string `json:"url"`
	Alias     string `json:"alias"`
	ExpiresIn int    `json:"expires_in"` // seconds, 0 = never
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Request body must be JSON.")
		return
	}

	target := normalizeURL(req.URL)
	if !validURL(target) {
		writeError(w, http.StatusBadRequest, "Please enter a valid link.")
		return
	}

	code := req.Alias
	if code != "" {
		if !validAlias(code) {
			writeError(w, http.StatusBadRequest, "Custom names must be 3-20 letters, numbers, - or _.")
			return
		}
		if s.store.Exists(code) {
			writeError(w, http.StatusConflict, "That custom name is already taken.")
			return
		}
	} else {
		code = s.newCode()
	}

	link := &Link{
		Code:        code,
		URL:         target,
		CreatedAt:   time.Now(),
		deleteToken: newToken(),
	}
	if req.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		link.ExpiresAt = &expires
	}
	s.store.Save(link)

	writeJSON(w, http.StatusCreated, map[string]any{
		"code":         link.Code,
		"short_url":    "http://" + r.Host + "/" + link.Code,
		"delete_token": link.deleteToken,
		"expires_at":   link.ExpiresAt,
	})
}

func (s *Server) follow(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	link := s.store.Get(code)

	if link == nil {
		writeError(w, http.StatusNotFound, "This link was not found.")
		return
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeError(w, http.StatusGone, "This link has expired.")
		return
	}
	s.store.IncrementClicks(code)
	http.Redirect(w, r, link.URL, http.StatusFound)
}

func (s *Server) linkStats(w http.ResponseWriter, r *http.Request) {
	link := s.store.Get(r.PathValue("code"))
	if link == nil {
		writeError(w, http.StatusNotFound, "No such link.")
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	link := s.store.Get(r.PathValue("code"))
	if link == nil {
		writeError(w, http.StatusNotFound, "No such link.")
		return
	}
	token := r.Header.Get("X-Delete-Token")
	if token != "" && token != link.deleteToken {
		writeError(w, http.StatusForbidden, "Wrong delete token.")
		return
	}
	s.store.Delete(link.Code)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listLinks(w http.ResponseWriter, r *http.Request) {
	links := s.store.List()
	sort.Slice(links, func(i, j int) bool { return links[i].CreatedAt.After(links[j].CreatedAt) })
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive number.")
			return
		}
		limit = n
	}
	if limit < len(links) {
		links = links[:limit]
	}
	writeJSON(w, http.StatusOK, links)
}
