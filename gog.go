package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// GOG games get a "gog-" prefix on their GOG product ID, so they can't
// collide with Steam appids in cards, QR links and details.
const gogPrefix = "gog-"

// gogGamesKey is where GOG Galaxy and the offline installers register each
// installed game (32-bit view).
const gogGamesKey = `SOFTWARE\WOW6432Node\GOG.com\Games`

func isGOG(appID string) bool { return strings.HasPrefix(appID, gogPrefix) }

// installedGOGApps lists GOG games from the registry, skipping DLC (which
// names its base game in dependsOn). GOG games are DRM-free, so each one
// carries the exe to run directly instead of going through Galaxy.
func installedGOGApps() ([]App, error) {
	games, err := registry.OpenKey(registry.LOCAL_MACHINE, gogGamesKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer games.Close()
	ids, err := games.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	var apps []App
	for _, id := range ids {
		k, err := registry.OpenKey(games, id, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val := func(name string) string {
			s, _, _ := k.GetStringValue(name)
			return strings.TrimSpace(s)
		}
		if val("dependsOn") != "" {
			k.Close()
			continue
		}
		dir := val("path")
		a := App{ID: gogPrefix + id, Name: val("gameName"), Store: "gog", home: dir}
		a.exe, a.args, a.dir = gogPlayTask(dir, id)
		if a.exe == "" {
			a.exe, a.args, a.dir = val("exe"), val("launchParam"), val("workingDir")
		}
		k.Close()
		if a.Name == "" || a.exe == "" {
			continue
		}
		if a.dir == "" {
			a.dir = filepath.Dir(a.exe)
		}
		apps = append(apps, a)
	}
	return apps, nil
}

// gogPlayTask reads the primary play task from the game's goggame-<id>.info,
// which is what Galaxy itself runs. Paths in it are relative to the install folder.
func gogPlayTask(installDir, id string) (exe, args, dir string) {
	b, err := os.ReadFile(filepath.Join(installDir, "goggame-"+id+".info"))
	if err != nil {
		return "", "", ""
	}
	var info struct {
		PlayTasks []struct {
			IsPrimary  bool   `json:"isPrimary"`
			Type       string `json:"type"`
			Path       string `json:"path"`
			Arguments  string `json:"arguments"`
			WorkingDir string `json:"workingDir"`
		} `json:"playTasks"`
	}
	if json.Unmarshal(b, &info) != nil {
		return "", "", ""
	}
	for _, t := range info.PlayTasks {
		if !t.IsPrimary || t.Type != "FileTask" || t.Path == "" {
			continue
		}
		exe = filepath.Join(installDir, t.Path)
		dir = installDir
		if t.WorkingDir != "" {
			dir = filepath.Join(installDir, t.WorkingDir)
		}
		return exe, t.Arguments, dir
	}
	return "", "", ""
}

// shellRun starts an exe the way Explorer would, so games that need
// elevation still get the UAC prompt.
func shellRun(exe, args, dir string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	var params, wd *uint16
	if args != "" {
		if params, err = windows.UTF16PtrFromString(args); err != nil {
			return err
		}
	}
	if dir != "" {
		if wd, err = windows.UTF16PtrFromString(dir); err != nil {
			return err
		}
	}
	return windows.ShellExecute(0, verb, file, params, wd, windows.SW_SHOWNORMAL)
}
