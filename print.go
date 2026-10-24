package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

//go:embed print.js
var printJS []byte

// PrintPrefs are the print settings last chosen in Settings, saved in
// %APPDATA%\steam-launcher\print.json. Settings and the games page check
// them against the templates in print.js.
type PrintPrefs struct {
	Template string `json:"template,omitempty"`
	Sides    string `json:"sides,omitempty"` // "ab", "a" or "b"
	ShowQR   bool   `json:"showQR"`
	Rounded  bool   `json:"rounded"`
	OneSheet bool   `json:"oneSheet"`
}

type PrintStore struct {
	mu    sync.Mutex
	path  string
	prefs *PrintPrefs // nil until saved; the page then uses its defaults
}

func LoadPrintPrefs() *PrintStore {
	s := &PrintStore{path: filepath.Join(configDir(), "print.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		var p PrintPrefs
		if json.Unmarshal(b, &p) == nil {
			s.prefs = &p
		}
	}
	return s
}

func (s *PrintStore) Get() *PrintPrefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs
}

func (s *PrintStore) Set(p PrintPrefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0o644); err != nil {
		return err
	}
	s.prefs = &p
	return nil
}

func (s *Server) getPrint(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.print.Get())
}

// savePrint stores the print settings; they apply without a restart.
func (s *Server) savePrint(w http.ResponseWriter, r *http.Request) {
	var p PrintPrefs
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad print settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	if p.Sides != "ab" && p.Sides != "a" && p.Sides != "b" {
		http.Error(w, `sides must be "ab", "a" or "b"`, http.StatusBadRequest)
		return
	}
	if err := s.print.Set(p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, p)
}

func servePrintJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(printJS)
}
