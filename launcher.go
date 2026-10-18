package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

const debounce = 10 * time.Second

// Launcher holds the installed app list and launches games, shared by the
// HTTP server and the scanner.
type Launcher struct {
	mu         sync.Mutex
	apps       []App
	byID       map[string]App
	lastLaunch map[string]time.Time
}

func NewLauncher() *Launcher {
	return &Launcher{lastLaunch: map[string]time.Time{}}
}

// Rescan reads the Steam, GOG and Epic libraries. It fails only when none
// can be read, so a PC with just one of them still works.
func (l *Launcher) Rescan() error {
	steam, steamErr := installedApps()
	gog, gogErr := installedGOGApps()
	epic, epicErr := installedEpicApps()
	if steamErr != nil && gogErr != nil && epicErr != nil {
		return steamErr
	}
	if steamErr != nil {
		log.Printf("steam library: %v", steamErr)
	}
	apps := append(append(steam, gog...), epic...)
	sortApps(apps)
	byID := make(map[string]App, len(apps))
	for _, a := range apps {
		byID[a.ID] = a
	}
	l.mu.Lock()
	l.apps, l.byID = apps, byID
	l.mu.Unlock()
	log.Printf("found %d installed apps (%d Steam, %d GOG, %d Epic)", len(apps), len(steam), len(gog), len(epic))
	return nil
}

func (l *Launcher) Apps() []App {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]App(nil), l.apps...)
}

type LaunchResult int

const (
	Launched LaunchResult = iota
	AlreadyLaunching
	NotInstalled
)

func (l *Launcher) Launch(appID, source string) (string, LaunchResult, error) {
	l.mu.Lock()
	app, ok := l.byID[appID]
	name := app.Name
	if !ok {
		l.mu.Unlock()
		return "", NotInstalled, nil
	}
	if time.Since(l.lastLaunch[appID]) < debounce {
		l.mu.Unlock()
		return name, AlreadyLaunching, nil
	}
	l.lastLaunch[appID] = time.Now()
	l.mu.Unlock()

	log.Printf("launching %s (%s) via %s", name, appID, source)
	var err error
	switch app.Store {
	case "gog":
		err = shellRun(app.exe, app.args, app.dir)
		bringToFront(app.home)
	case "epic":
		err = shellOpen(app.exe)
		bringToFront(app.home)
	default:
		err = shellOpen("steam://rungameid/" + appID)
	}
	if err != nil {
		return name, Launched, fmt.Errorf("launch %s: %w", appID, err)
	}
	return name, Launched, nil
}

var (
	scanURLRe   = regexp.MustCompile(`/launch/((?:gog-)?\d+|epic-[\w.-]+)`)
	scanSteamRe = regexp.MustCompile(`^steam://rungameid/(\d+)`)
	scanBareRe  = regexp.MustCompile(`^(?:(?:gog-)?\d+|epic-[\w.-]+)$`)
)

// parseScan pulls an appid out of scanned text: the phone launch URL,
// a steam://rungameid URL, or a bare appid (digits, gog-<digits> or epic-<name>).
func parseScan(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if m := scanURLRe.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	if m := scanSteamRe.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	if scanBareRe.MatchString(text) {
		return text, true
	}
	return "", false
}
