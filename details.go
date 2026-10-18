package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
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
	Publisher   string   `json:"publisher,omitempty"`
	Year        string   `json:"year,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Tags        []string `json:"tags,omitempty"` // popular user-defined tags, most votes first
	Description string   `json:"description,omitempty"`
	Version     int      `json:"v,omitempty"`
}

// detailsVersion is bumped when Details gains fields, so cached entries are fetched again.
const (
	detailsVersion = 2
	maxTags        = 5
)

// DetailsStore fetches details from the Steam store once per app and keeps them.
type DetailsStore struct {
	mu      sync.Mutex
	path    string
	details map[string]*Details // nil value: the store has no page for the app
	client  *http.Client
}

var (
	yearRe = regexp.MustCompile(`\b(19|20)\d\d\b`)
	// The store page passes the app's tags to its tag dialog as JSON.
	tagsRe = regexp.MustCompile(`InitAppTagModal\(\s*\d+,\s*(\[.*?\])\s*,`)
)

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
		d, have := s.details[a.ID]
		s.mu.Unlock()
		if have && (d == nil || d.Version >= detailsVersion) {
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
			Publishers  []string                       `json:"publishers"`
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
	if len(entry.Data.Publishers) > 0 {
		d.Publisher = entry.Data.Publishers[0]
	}
	for _, g := range entry.Data.Genres {
		d.Genres = append(d.Genres, g.Description)
	}
	d.Tags, err = s.fetchTags(appID)
	if err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	d.Version = detailsVersion
	return d, nil
}

// fetchTags reads the popular user-defined tags from the app's store page,
// which isn't in the appdetails API.
func (s *DetailsStore) fetchTags(appID string) ([]string, error) {
	req, err := http.NewRequest("GET", "https://store.steampowered.com/app/"+appID+"/?l=english", nil)
	if err != nil {
		return nil, err
	}
	// Skip the age check on mature games.
	req.Header.Set("Cookie", "birthtime=0; lastagecheckage=1-0-1900; wants_mature_content=1")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store page returned %s", resp.Status)
	}
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	m := tagsRe.FindSubmatch(page)
	if m == nil {
		return nil, nil
	}
	var tags []struct{ Name string }
	if err := json.Unmarshal(m[1], &tags); err != nil {
		return nil, err
	}
	var names []string
	for _, t := range tags[:min(len(tags), maxTags)] {
		names = append(names, t.Name)
	}
	return names, nil
}

func (s *DetailsStore) save() {
	s.mu.Lock()
	b, err := json.MarshalIndent(s.details, "", "  ")
	s.mu.Unlock()
	if err == nil {
		os.WriteFile(s.path, b, 0o644)
	}
}
