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

var errUpdatesNotConfigured = errors.New("this build has no GitHub repo; add an update URL in Settings")

// Update channels. Stable sees only full releases; beta also sees GitHub
// pre-releases (tags like v0.3.0-beta.1), and moves onto the next stable
// release once that is newer.
const (
	channelStable = ""
	channelBeta   = "beta"
)

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// checkForUpdate returns the newest manifest on the configured channel and
// whether it's newer than this build. A custom update URL overrides the channel.
func checkForUpdate(cfg Config) (*Manifest, bool, error) {
	url := cfg.UpdateURL
	if url == "" {
		if updateRepo == "" {
			return nil, false, errUpdatesNotConfigured
		}
		var err error
		if url, err = channelManifestURL(cfg.UpdateChannel); err != nil {
			return nil, false, err
		}
	}
	var m Manifest
	if err := getJSON(url, &m); err != nil {
		return nil, false, fmt.Errorf("latest.json: %w", err)
	}
	if m.Version == "" || m.URL == "" || len(m.SHA256) != 64 {
		return nil, false, errors.New("latest.json is missing version, url or sha256")
	}
	return &m, newerVersion(m.Version, version), nil
}

// channelManifestURL finds latest.json for the newest release on a channel.
// GitHub's /releases/latest skips pre-releases, so beta lists the releases.
func channelManifestURL(channel string) (string, error) {
	if channel != channelBeta {
		return "https://github.com/" + updateRepo + "/releases/latest/download/latest.json", nil
	}
	var releases []struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := getJSON("https://api.github.com/repos/"+updateRepo+"/releases?per_page=30", &releases); err != nil {
		return "", fmt.Errorf("list releases: %w", err)
	}
	best, bestTag := "", ""
	for _, r := range releases {
		if r.Draft || parseVersion(r.Tag) == nil || (bestTag != "" && !newerVersion(r.Tag, bestTag)) {
			continue
		}
		for _, a := range r.Assets {
			if a.Name == "latest.json" {
				best, bestTag = a.URL, r.Tag
			}
		}
	}
	if best == "" {
		return "", errors.New("no releases with latest.json found")
	}
	return best, nil
}

func getJSON(url string, v any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
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
	pid, err := relaunch()
	if err != nil {
		os.Rename(exe, newPath)
		os.Rename(oldPath, exe)
		return fmt.Errorf("start new version: %w", err)
	}
	log.Printf("updated %s -> %s; handing over to pid %d", version, m.Version, pid)
	return nil
}

// relaunch starts the exe at our path, which waits for this process to exit
// before taking over. The caller should quit right after.
func relaunch() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, afterUpdateFlag, strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
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

var (
	semverRe   = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+))?`)
	describeRe = regexp.MustCompile(`(-\d+-g[0-9a-f]+)?(-dirty)?$`)
)

type semver struct {
	nums [3]int
	pre  []string // e.g. ["beta", "1"]; empty for a full release
}

// newerVersion reports whether a is a later release than b, ordering
// pre-releases before their release (v0.3.0-beta.1 < v0.3.0-beta.2 < v0.3.0).
// Builds that aren't a release version (e.g. "dev") count as older than any
// release, and local builds between tags (v0.3.0-2-gabc123-dirty) count as
// the tag they're built on.
func newerVersion(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil {
		return false
	}
	if pb == nil {
		return true
	}
	for i := range pa.nums {
		if pa.nums[i] != pb.nums[i] {
			return pa.nums[i] > pb.nums[i]
		}
	}
	return comparePre(pa.pre, pb.pre) > 0
}

func parseVersion(v string) *semver {
	m := semverRe.FindStringSubmatch(describeRe.ReplaceAllString(v, ""))
	if m == nil {
		return nil
	}
	var s semver
	for i := range s.nums {
		s.nums[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] != "" {
		s.pre = strings.Split(m[4], ".")
	}
	return &s
}

// comparePre orders pre-release identifiers by semver rules.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		na, aErr := strconv.Atoi(a[i])
		nb, bErr := strconv.Atoi(b[i])
		switch {
		case aErr == nil && bErr == nil:
			if na != nb {
				return cmpInt(na, nb)
			}
		case aErr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case bErr == nil:
			return 1
		case a[i] != b[i]:
			return strings.Compare(a[i], b[i])
		}
	}
	return cmpInt(len(a), len(b))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
