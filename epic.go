package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Epic games get an "epic-" prefix on their Epic app name.
const epicPrefix = "epic-"

const defaultEpicData = `C:\ProgramData\Epic\EpicGamesLauncher\Data`

func isEpic(appID string) bool { return strings.HasPrefix(appID, epicPrefix) }

// epicDataDir is the launcher's data folder, which holds a manifest per install.
func epicDataDir() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Epic Games\EpicGamesLauncher`, registry.QUERY_VALUE)
	if err != nil {
		return defaultEpicData
	}
	defer k.Close()
	p, _, err := k.GetStringValue("AppDataPath")
	if err != nil || p == "" {
		return defaultEpicData
	}
	return filepath.Clean(p)
}

// installedEpicApps reads the launcher's install manifests, skipping DLC,
// engines and unfinished installs.
func installedEpicApps() ([]App, error) {
	return epicAppsIn(filepath.Join(epicDataDir(), "Manifests"))
}

func epicAppsIn(dir string) ([]App, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	manifests, _ := filepath.Glob(filepath.Join(dir, "*.item"))
	var apps []App
	for _, path := range manifests {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m struct {
			DisplayName, AppName, CatalogNamespace, CatalogItemId, MainGameAppName, InstallLocation string
			AppCategories                                                                           []string
			BIsIncompleteInstall                                                                    bool `json:"bIsIncompleteInstall"`
		}
		if json.Unmarshal(b, &m) != nil || m.AppName == "" || m.BIsIncompleteInstall {
			continue
		}
		if !slices.Contains(m.AppCategories, "games") || (m.MainGameAppName != "" && m.MainGameAppName != m.AppName) {
			continue
		}
		apps = append(apps, App{
			ID: epicPrefix + m.AppName, Name: m.DisplayName, Store: "epic",
			catalogNS: m.CatalogNamespace, catalogID: m.CatalogItemId, home: m.InstallLocation,
			// What the launcher's own desktop shortcuts open.
			exe: "com.epicgames.launcher://apps/" + url.PathEscape(m.CatalogNamespace+":"+m.CatalogItemId+":"+m.AppName) + "?action=launch&silent=true",
		})
	}
	return apps, nil
}

// epicGraphQL is the store endpoint the Epic launcher itself queries. It
// needs no login, but Cloudflare only lets the launcher's user agent through.
const (
	epicGraphQL   = "https://launcher.store.epicgames.com/graphql"
	epicUserAgent = "EpicGamesLauncher/17.0.0-1+++Portal+Release-Live Windows/10.0.26100.1.256.64bit"
	epicItemQuery = `query($ns: String!, $id: String!) { Catalog { catalogItem(namespace: $ns, id: $id) {
		developer description keyImages { type url } } } }`
)

// epicArtTypes maps an artwork kind to Epic's key image types, best first.
var epicArtTypes = map[string][]string{
	"capsule": {"DieselGameBoxTall", "OfferImageTall"},
	"header":  {"DieselGameBox", "OfferImageWide", "Thumbnail"},
	"hero":    {"DieselGameBox", "OfferImageWide"},
	"logo":    {"DieselGameBoxLogo"},
}

// fetchEpic reads a game's details and art from Epic's store catalog.
func (s *DetailsStore) fetchEpic(namespace, itemID string) (*Details, error) {
	body, _ := json.Marshal(map[string]any{"query": epicItemQuery, "variables": map[string]string{"ns": namespace, "id": itemID}})
	req, err := http.NewRequest("POST", epicGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", epicUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epic store returned %s", resp.Status)
	}
	var out struct {
		Data struct {
			Catalog struct {
				CatalogItem *epicCatalogItem `json:"catalogItem"`
			} `json:"Catalog"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Data.Catalog.CatalogItem == nil {
		return nil, nil // not in the catalog; cached as nil
	}
	return epicDetails(*out.Data.Catalog.CatalogItem), nil
}

type epicCatalogItem struct {
	Developer   string `json:"developer"`
	Description string `json:"description"`
	KeyImages   []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"keyImages"`
}

func epicDetails(it epicCatalogItem) *Details {
	d := &Details{Developer: it.Developer, Description: strings.TrimSpace(it.Description), Art: map[string]string{}, Version: detailsVersion}
	for kind, types := range epicArtTypes {
	types:
		for _, t := range types {
			for _, img := range it.KeyImages {
				if img.Type == t && img.URL != "" {
					d.Art[kind] = img.URL
					if kind == "header" && !strings.Contains(img.URL, "?") {
						d.Art[kind] += "?w=640&h=360&resize=1" // tile size; the CDN scales it
					}
					break types
				}
			}
		}
	}
	return d
}
