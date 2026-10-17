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
	byID       map[string]string
	lastLaunch map[string]time.Time
}

func NewLauncher() *Launcher {
	return &Launcher{lastLaunch: map[string]time.Time{}}
}

func (l *Launcher) Rescan() error {
	apps, err := installedApps()
	if err != nil {
		return err
	}
	byID := make(map[string]string, len(apps))
	for _, a := range apps {
		byID[a.ID] = a.Name
	}
	l.mu.Lock()
	l.apps, l.byID = apps, byID
	l.mu.Unlock()
	log.Printf("found %d installed apps", len(apps))
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
	name, ok := l.byID[appID]
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
	if err := shellOpen("steam://rungameid/" + appID); err != nil {
		return name, Launched, fmt.Errorf("launch %s: %w", appID, err)
	}
	return name, Launched, nil
}

var (
	scanURLRe   = regexp.MustCompile(`/launch/(\d+)`)
	scanSteamRe = regexp.MustCompile(`^steam://rungameid/(\d+)`)
	scanBareRe  = regexp.MustCompile(`^\d+$`)
)

// parseScan pulls an appid out of scanned text: the phone launch URL,
// a steam://rungameid URL, or bare digits.
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
