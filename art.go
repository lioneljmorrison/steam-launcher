package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ArtChoice is the art picked for a game's printed side A, in place of the
// automatic choice: an image URL, the point of it kept in view when it's
// cropped to the card ("50% 30%"), and whether the logo goes over it.
type ArtChoice struct {
	Src  string `json:"src"`
	Pos  string `json:"pos,omitempty"`
	Logo bool   `json:"logo,omitempty"`
}

// ArtChoices keeps the picked art per appid, saved in
// %APPDATA%\cardtridge\art.json.
type ArtChoices struct {
	mu      sync.Mutex
	path    string
	choices map[string]ArtChoice
}

func LoadArtChoices() *ArtChoices {
	s := &ArtChoices{path: filepath.Join(configDir(), "art.json"), choices: map[string]ArtChoice{}}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.choices)
	}
	return s
}

func (s *ArtChoices) All() map[string]ArtChoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ArtChoice, len(s.choices))
	for k, v := range s.choices {
		out[k] = v
	}
	return out
}

// Set saves the choice for an app; an empty Src goes back to automatic.
func (s *ArtChoices) Set(appID string, c ArtChoice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Src == "" {
		delete(s.choices, appID)
	} else {
		s.choices[appID] = c
	}
	b, err := json.MarshalIndent(s.choices, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

type artCandidate struct {
	Src   string `json:"src"`
	Label string `json:"label"`
}

// steamCDN serves the public art; newer apps' art also sits in hashed folders
// the client caches, so both are offered.
const steamCDN = "https://cdn.cloudflare.steamstatic.com/steam/apps/"

// cachedArt lists the Steam client's cached images for an app, as paths
// relative to its librarycache folder, leaving out blurred backdrops, logos
// and icons. Only names in this list are ever served.
func cachedArt(appID string) []string {
	dir := filepath.Join(steamRoot(), "appcache", "librarycache", appID)
	var out []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		ext := filepath.Ext(name)
		if (ext != ".jpg" && ext != ".png") || strings.Contains(name, "blur") || strings.HasPrefix(name, "logo") {
			return nil
		}
		if fi, err := d.Info(); err != nil || fi.Size() < 8<<10 {
			return nil
		}
		if rel, err := filepath.Rel(dir, path); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func cachedArtLabel(rel string) string {
	name := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasPrefix(name, "library_600x900"), strings.HasPrefix(name, "library_capsule"):
		return "Portrait art, cached on this PC"
	case strings.HasPrefix(name, "library_hero"):
		return "Hero art, cached on this PC"
	case strings.Contains(name, "header"):
		return "Header, cached on this PC"
	}
	return "Library art, cached on this PC"
}

// artCandidates lists the images a game's side A can use. The page loads
// each one and drops those that fail, so CDN guesses are fine.
func (s *Server) artCandidates(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appid")
	out := []artCandidate{}
	if isGOG(appID) || isEpic(appID) {
		if d := s.details.Get(appID); d != nil {
			labels := map[string]string{"capsule": "Box art", "hero": "Background art", "header": "Store header"}
			seen := map[string]bool{}
			for _, kind := range []string{"capsule", "hero", "header"} {
				if src := d.Art[kind]; src != "" && !seen[src] {
					seen[src] = true
					out = append(out, artCandidate{src, labels[kind]})
				}
			}
		}
		writeJSON(w, out)
		return
	}
	if _, err := strconv.Atoi(appID); err != nil {
		http.NotFound(w, r)
		return
	}
	out = append(out,
		artCandidate{steamCDN + appID + "/library_600x900_2x.jpg", "Portrait art, high resolution"},
		artCandidate{steamCDN + appID + "/library_600x900.jpg", "Portrait art"},
		artCandidate{steamCDN + appID + "/library_hero_2x.jpg", "Hero art, high resolution"},
		artCandidate{steamCDN + appID + "/library_hero.jpg", "Hero art"},
		artCandidate{steamCDN + appID + "/header.jpg", "Store header"},
	)
	for _, rel := range cachedArt(appID) {
		out = append(out, artCandidate{"/art/" + appID + "/file?f=" + rel, cachedArtLabel(rel)})
	}
	writeJSON(w, out)
}

// artScreenshots lists the game's store screenshots, fetched when asked
// since they aren't kept with the details.
func (s *Server) artScreenshots(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appid")
	out := []artCandidate{}
	if _, err := strconv.Atoi(appID); err != nil {
		writeJSON(w, out)
		return
	}
	resp, err := s.details.client.Get("https://store.steampowered.com/api/appdetails?filters=screenshots&appids=" + appID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var body map[string]struct {
		Data struct {
			Screenshots []struct {
				PathFull string `json:"path_full"`
			} `json:"screenshots"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for i, sc := range body[appID].Data.Screenshots {
		out = append(out, artCandidate{sc.PathFull, "Screenshot " + strconv.Itoa(i+1)})
	}
	writeJSON(w, out)
}

// saveArt stores the picked art for a game; an empty src clears it.
func (s *Server) saveArt(w http.ResponseWriter, r *http.Request) {
	var c ArtChoice
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.artChoices.Set(r.PathValue("appid"), c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.artChoices.All())
}

// serveCachedArt serves /art/{appid}/file?f=<path>, but only a path that
// cachedArt lists, so it can't reach anything else on disk.
func serveCachedArt(w http.ResponseWriter, r *http.Request, appID string) {
	want := r.URL.Query().Get("f")
	for _, rel := range cachedArt(appID) {
		if rel == want {
			w.Header().Set("Cache-Control", "max-age=3600")
			http.ServeFile(w, r, filepath.Join(steamRoot(), "appcache", "librarycache", appID, filepath.FromSlash(rel)))
			return
		}
	}
	http.NotFound(w, r)
}
