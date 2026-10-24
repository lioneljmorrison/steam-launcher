package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// CardStore maps NFC tag UIDs (hex) and SD card volume serials ("SD-" + hex) to appids, saved in
// %APPDATA%\cardtridge\cards.json.
type CardStore struct {
	mu    sync.Mutex
	path  string
	cards map[string]string
}

func LoadCards() (*CardStore, error) {
	s := &CardStore{path: filepath.Join(configDir(), "cards.json"), cards: map[string]string{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(b, &s.cards)
}

func (s *CardStore) Get(uid string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	appID, ok := s.cards[uid]
	return appID, ok
}

func (s *CardStore) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.cards))
	for k, v := range s.cards {
		out[k] = v
	}
	return out
}

func (s *CardStore) Link(uid, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cards[uid] = appID
	return s.save()
}

func (s *CardStore) Unlink(uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cards, uid)
	return s.save()
}

func (s *CardStore) save() error {
	b, err := json.MarshalIndent(s.cards, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}
