package main

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Details is store information about a game, cached in
// %LOCALAPPDATA%\steam-launcher\details.json.
type Details struct {
	Developer   string   `json:"developer,omitempty"`
	Year        string   `json:"year,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Description string   `json:"description,omitempty"`
}

// DetailsStore fetches details from the Steam store once per app and keeps them.
type DetailsStore struct {
	mu      sync.Mutex
	path    string
	details map[string]*Details // nil value: the store has no page for the app
	client  *http.Client
}

var yearRe = regexp.MustCompile(`\b(19|20)\d\d\b`)

func LoadDetails() *DetailsStore {
	s := &DetailsStore{
		path:    filepath.Join(logDir(), "details.json"),
		details: map[string]*Details{},
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.details)
	}
	return s
}

func (s *DetailsStore) Get(appID string) *Details {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.details[appID]
}

// FetchMissing looks up apps not yet cached, one at a time to stay well
// under the store API's rate limit.
func (s *DetailsStore) FetchMissing(apps []App) {
	fetched := 0
	for _, a := range apps {
		s.mu.Lock()
		_, have := s.details[a.ID]
		s.mu.Unlock()
		if have {
			continue
		}
		d, err := s.fetch(a.ID)
		if err != nil {
			log.Printf("details for %s: %v", a.Name, err)
			continue // offline or rate limited; try again next start
		}
		s.mu.Lock()
		s.details[a.ID] = d
		s.mu.Unlock()
		fetched++
		time.Sleep(300 * time.Millisecond)
	}
	if fetched > 0 {
		s.save()
		log.Printf("fetched store details for %d apps", fetched)
	}
}

func (s *DetailsStore) fetch(appID string) (*Details, error) {
	resp, err := s.client.Get("https://store.steampowered.com/api/appdetails?l=english&cc=us&appids=" + appID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store returned %s", resp.Status)
	}
	var body map[string]struct {
		Success bool `json:"success"`
		Data    struct {
			Developers  []string                       `json:"developers"`
			Genres      []struct{ Description string } `json:"genres"`
			ReleaseDate struct{ Date string }          `json:"release_date"`
			ShortDesc   string                         `json:"short_description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	entry := body[appID]
	if !entry.Success {
		return nil, nil // no store page (tools, redistributables); cached as nil
	}
	d := &Details{
		Year:        yearRe.FindString(entry.Data.ReleaseDate.Date),
		Description: strings.TrimSpace(html.UnescapeString(entry.Data.ShortDesc)),
	}
	if len(entry.Data.Developers) > 0 {
		d.Developer = entry.Data.Developers[0]
	}
	for _, g := range entry.Data.Genres {
		d.Genres = append(d.Genres, g.Description)
	}
	return d, nil
}

func (s *DetailsStore) save() {
	s.mu.Lock()
	b, err := json.MarshalIndent(s.details, "", "  ")
	s.mu.Unlock()
	if err == nil {
		os.WriteFile(s.path, b, 0o644)
	}
}
