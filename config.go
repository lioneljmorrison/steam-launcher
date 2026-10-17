package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
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
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "steam-launcher")
}

func loadConfig() (Config, error) {
	cfg := Config{Port: 8787, ScannerBaud: 9600, ScannerMatch: `(?i)scan|barcode`}
	path := filepath.Join(configDir(), "config.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(configDir(), 0o755); err != nil {
			return cfg, err
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		return cfg, os.WriteFile(path, b, 0o644)
	}
	if err != nil {
		return cfg, err
	}
	// Notepad and Windows PowerShell may save UTF-8 with a BOM.
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	return cfg, json.Unmarshal(b, &cfg)
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
