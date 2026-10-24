package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.bug.st/serial"
)

//go:embed settings.html
var settingsHTML string

//go:embed help.html
var helpHTML []byte

var (
	settingsTmpl = template.Must(template.New("settings").Parse(settingsHTML))
	comPortRe    = regexp.MustCompile(`(?i)^COM\d+$`)
)

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		result(w, http.StatusForbidden, "Forbidden", "Open settings on the PC itself.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := settingsTmpl.Execute(w, map[string]any{"Key": s.key}); err != nil {
		log.Printf("render settings: %v", err)
	}
}

func helpPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(helpHTML)
}

type portInfo struct {
	Port   string `json:"port"`
	Device string `json:"device,omitempty"` // Bluetooth device name, when known
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	ports := map[string]string{}
	if list, err := serial.GetPortsList(); err == nil {
		for _, p := range list {
			ports[p] = ""
		}
	}
	if bt, err := bluetoothSerialPorts(); err == nil {
		for _, p := range bt {
			ports[p.Port] = p.Device
		}
	}
	var portList []portInfo
	for p, d := range ports {
		portList = append(portList, portInfo{Port: p, Device: d})
	}
	sort.Slice(portList, func(i, j int) bool { return portList[i].Port < portList[j].Port })

	writeJSON(w, map[string]any{
		"config":     s.cfg,
		"defaults":   defaultConfig(),
		"version":    version,
		"configPath": configPath(),
		"detected":   map[string]any{"hostIP": lanIP(), "addresses": lanAddrs(), "ports": portList},
	})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "bad settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateConfig(&cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A port the restarted app can't open would stop it at startup.
	s.tests.stop()
	if err := s.portFree(cfg.Port); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := saveConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Print("settings saved; restarting")
	if _, err := relaunch("/settings"); err != nil {
		http.Error(w, "saved, but couldn't restart: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"restarting": true, "port": cfg.Port})
	s.quitSoon()
}

func validateConfig(cfg *Config) error {
	cfg.HostIP = strings.TrimSpace(cfg.HostIP)
	cfg.ScannerPort = strings.TrimSpace(cfg.ScannerPort)
	cfg.UpdateURL = strings.TrimSpace(cfg.UpdateURL)
	switch {
	case cfg.Port < 1 || cfg.Port > 65535:
		return fmt.Errorf("port must be between 1 and 65535")
	case cfg.HostIP != "" && net.ParseIP(cfg.HostIP) == nil:
		return fmt.Errorf("host IP %q isn't an IP address", cfg.HostIP)
	case cfg.ScannerPort != "" && !strings.EqualFold(cfg.ScannerPort, "off") && !comPortRe.MatchString(cfg.ScannerPort):
		return fmt.Errorf("scanner port must be blank (auto), off, or a COM port like COM5")
	case cfg.ScannerBaud < 1:
		return fmt.Errorf("baud rate must be positive")
	case cfg.NFCBase != "" && cfg.NFCBase != "off":
		return fmt.Errorf(`NFC base must be "" or "off"`)
	case cfg.DriveLaunch != "" && cfg.DriveLaunch != "off":
		return fmt.Errorf(`drive launch must be "" or "off"`)
	case cfg.UpdateChannel != channelStable && cfg.UpdateChannel != channelBeta:
		return fmt.Errorf(`update channel must be "" (stable) or "beta"`)
	}
	if _, err := regexp.Compile(cfg.ScannerMatch); err != nil {
		return fmt.Errorf("scanner name match: %v", err)
	}
	if cfg.UpdateURL != "" {
		u, err := url.Parse(cfg.UpdateURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("update URL must be an http(s) address")
		}
	}
	return nil
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	m, newer, err := checkForUpdate(s.cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"current": version, "latest": m.Version, "newer": newer})
}

func (s *Server) updateInstall(w http.ResponseWriter, r *http.Request) {
	m, newer, err := checkForUpdate(s.cfg)
	if err == nil && !newer {
		err = fmt.Errorf("already on the latest version (%s)", version)
	}
	if err == nil {
		err = applyUpdate(m, "/settings")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"restarting": true, "version": m.Version})
	s.quitSoon()
}

// quitSoon exits shortly, after the response has gone out.
func (s *Server) quitSoon() {
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.quit()
	}()
}
