package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const defaultSteamPath = `C:\Program Files (x86)\Steam`

type App struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Store string `json:"store"` // "steam", "gog" or "epic"

	exe, args, dir string // GOG: the game's own exe, run directly; Epic: its launcher URL
	home           string // GOG, Epic: install folder, to find the game's window
	catalogNS      string // Epic: catalog namespace and item ID, for details and art
	catalogID      string
}

var (
	vdfPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)
	acfIDRe   = regexp.MustCompile(`"appid"\s+"(\d+)"`)
	acfNameRe = regexp.MustCompile(`"name"\s+"([^"]*)"`)
)

func steamRoot() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE)
	if err != nil {
		return defaultSteamPath
	}
	defer k.Close()
	p, _, err := k.GetStringValue("SteamPath")
	if err != nil || p == "" {
		return defaultSteamPath
	}
	return filepath.Clean(p)
}

// installedApps reads every Steam library's app manifests.
func installedApps() ([]App, error) {
	libs, err := steamLibraries()
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, lib := range libs {
		for id, name := range libraryApps(lib) {
			seen[id] = name
		}
	}
	apps := make([]App, 0, len(seen))
	for id, name := range seen {
		apps = append(apps, App{ID: id, Name: name, Store: "steam"})
	}
	return apps, nil
}

// steamLibraries lists the library folders Steam knows about, including ones
// on drives that aren't plugged in right now.
func steamLibraries() ([]string, error) {
	vdf, err := os.ReadFile(filepath.Join(steamRoot(), "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return nil, err
	}
	var libs []string
	for _, m := range vdfPathRe.FindAllStringSubmatch(string(vdf), -1) {
		libs = append(libs, strings.ReplaceAll(m[1], `\\`, `\`))
	}
	return libs, nil
}

// libraryApps maps appid to name for the games installed in one library.
func libraryApps(lib string) map[string]string {
	apps := map[string]string{}
	manifests, _ := filepath.Glob(filepath.Join(lib, "steamapps", "appmanifest_*.acf"))
	for _, acf := range manifests {
		b, err := os.ReadFile(acf)
		if err != nil {
			continue
		}
		id, name := acfIDRe.FindSubmatch(b), acfNameRe.FindSubmatch(b)
		if id != nil && name != nil {
			apps[string(id[1])] = string(name[1])
		}
	}
	return apps
}

func sortApps(apps []App) {
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
}

// artFiles maps an artwork kind to the file names the Steam client caches it as.
var artFiles = map[string][]string{
	"capsule": {"library_600x900_2x.jpg", "library_600x900.jpg", "library_capsule.jpg"}, // portrait library art
	"header":  {"library_header.jpg", "header.jpg"},
	"hero":    {"library_hero.jpg"},
	"logo":    {"logo.png"},
}

// artPath finds the Steam client's cached artwork for an app. Newer apps keep
// it under a hashed folder (librarycache/<appid>/<hash>/library_header.jpg),
// which the public CDN can't serve without that hash.
func artPath(appID, kind string) (string, bool) {
	dir := filepath.Join(steamRoot(), "appcache", "librarycache", appID)
	for _, name := range artFiles[kind] {
		var best string
		var bestTime int64
		for _, pattern := range []string{name, "*/" + name} {
			matches, _ := filepath.Glob(filepath.Join(dir, pattern))
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && fi.ModTime().UnixNano() > bestTime {
					best, bestTime = m, fi.ModTime().UnixNano()
				}
			}
		}
		if best != "" {
			return best, true
		}
	}
	return "", false
}

func shellOpen(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
