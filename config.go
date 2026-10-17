package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Config lives in %APPDATA%\steam-launcher\config.json.
type Config struct {
	Port   int    `json:"port"`
	HostIP string `json:"hostIP"` // LAN IP in the QR codes; auto-detected when empty
	// ScannerPort is "" to auto-detect Bluetooth scanners, "off" to disable,
	// or a fixed port such as "COM5".
	ScannerPort  string `json:"scannerPort"`
	ScannerBaud  int    `json:"scannerBaud"`
	ScannerMatch string `json:"scannerMatch"` // regexp for auto-detected Bluetooth device names
	NFCBase      string `json:"nfcBase"`      // "off" disables the Disney Infinity base
	UpdateURL    string `json:"updateURL"`    // latest.json to check for updates
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "steam-launcher")
}

func defaultConfig() Config {
	url, _ := defaultUpdateURL()
	return Config{Port: 8787, ScannerBaud: 9600, ScannerMatch: `(?i)scan|barcode`, UpdateURL: url}
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

// loadConfig reads config.json over the defaults, then writes it back so the
// file always lists every setting with its current value.
func loadConfig() (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(configPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}
	if err == nil {
		// Notepad and Windows PowerShell may save UTF-8 with a BOM.
		b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, err
		}
	}
	if cfg.UpdateURL == "" {
		cfg.UpdateURL = defaultConfig().UpdateURL
	}
	if normalized, _ := json.MarshalIndent(cfg, "", "  "); !bytes.Equal(normalized, bytes.TrimSpace(b)) {
		if err := saveConfig(cfg); err != nil {
			log.Printf("write config: %v", err)
		}
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o644)
}

// loadKey returns the secret embedded in the QR codes, creating it on first run.
func loadKey() (string, error) {
	path := filepath.Join(configDir(), "key")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 12)
	rand.Read(buf)
	key := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return "", err
	}
	return key, os.WriteFile(path, []byte(key), 0o600)
}
