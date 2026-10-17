package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Set at build time: -ldflags "-X main.version=v0.3.0 -X main.updateRepo=owner/repo".
var (
	version    = "dev"
	updateRepo = ""
)

const afterUpdateFlag = "--after-update"

// Manifest is latest.json, published alongside each release.
type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

var errUpdatesNotConfigured = errors.New("this build has no update source; set updateURL in config.json")

func manifestURL(cfg Config) (string, error) {
	if cfg.UpdateURL != "" {
		return cfg.UpdateURL, nil
	}
	if updateRepo == "" {
		return "", errUpdatesNotConfigured
	}
	return "https://github.com/" + updateRepo + "/releases/latest/download/latest.json", nil
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// checkForUpdate returns the latest manifest and whether it's newer than this build.
func checkForUpdate(cfg Config) (*Manifest, bool, error) {
	url, err := manifestURL(cfg)
	if err != nil {
		return nil, false, err
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%s: %s", url, resp.Status)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, false, fmt.Errorf("bad latest.json: %w", err)
	}
	if m.Version == "" || m.URL == "" || len(m.SHA256) != 64 {
		return nil, false, errors.New("latest.json is missing version, url or sha256")
	}
	return &m, newerVersion(m.Version, version), nil
}

// applyUpdate downloads and verifies the new exe, swaps it in place of the
// running one (Windows allows renaming a running exe, not overwriting it), and
// starts it. The caller should exit right after so the new copy can take over.
func applyUpdate(m *Manifest) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	newPath := strings.TrimSuffix(exe, ".exe") + ".new.exe"
	oldPath := strings.TrimSuffix(exe, ".exe") + ".old.exe"

	if err := download(m.URL, newPath, m.SHA256); err != nil {
		os.Remove(newPath)
		return err
	}
	os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("move current exe aside: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(oldPath, exe) // put the running version back
		return fmt.Errorf("install new exe: %w", err)
	}
	cmd := exec.Command(exe, afterUpdateFlag, strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		os.Rename(exe, newPath)
		os.Rename(oldPath, exe)
		return fmt.Errorf("start new version: %w", err)
	}
	log.Printf("updated %s -> %s; handing over to pid %d", version, m.Version, cmd.Process.Pid)
	return nil
}

func download(url, path, wantSHA string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA) {
		return fmt.Errorf("download checksum mismatch (got %s, want %s)", got, wantSHA)
	}
	return nil
}

// finishUpdate runs at startup. When launched by applyUpdate it waits for the
// previous version to exit (it holds the single-instance lock and the port),
// then removes the old exe left beside this one.
func finishUpdate() {
	for i, arg := range os.Args {
		if arg == afterUpdateFlag && i+1 < len(os.Args) {
			if pid, err := strconv.Atoi(os.Args[i+1]); err == nil {
				waitForExit(uint32(pid), 15*time.Second)
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := strings.TrimSuffix(exe, ".exe") + ".old.exe"
	for i := 0; i < 10; i++ {
		if err := os.Remove(old); err == nil || errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func waitForExit(pid uint32, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// newerVersion reports whether a is a later release than b. Builds that
// aren't a release version (e.g. "dev") count as older than any release.
func newerVersion(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil {
		return false
	}
	if pb == nil {
		return true
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) []int {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}
