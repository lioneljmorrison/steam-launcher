package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const scannerPollInterval = 5 * time.Second

// ScannerManager finds QR scanners in serial mode (USB COM / Bluetooth SPP) and
// reads scans from each, one code per line. With no fixed port it watches for
// paired Bluetooth serial devices whose name matches, so a newly paired scanner
// is picked up without any configuration.
type ScannerManager struct {
	launcher  *Launcher
	fixedPort string
	match     *regexp.Regexp
	baud      int
	onStatus  func(string)

	mu      sync.Mutex
	known   map[string]string // port -> device name, from the last poll
	active  map[string]bool   // port -> connected (present while a session runs)
	failing map[string]bool   // port -> last open failed; logs only the first failure
	status  string
}

func NewScannerManager(l *Launcher, cfg Config, onStatus func(string)) (*ScannerManager, error) {
	m := &ScannerManager{
		launcher: l,
		baud:     cfg.ScannerBaud,
		onStatus: onStatus,
		known:    map[string]string{},
		active:   map[string]bool{},
		failing:  map[string]bool{},
	}
	if !strings.EqualFold(cfg.ScannerPort, "auto") {
		m.fixedPort = cfg.ScannerPort
	}
	re, err := regexp.Compile(cfg.ScannerMatch)
	if err != nil {
		return nil, fmt.Errorf("scannerMatch: %w", err)
	}
	m.match = re
	return m, nil
}

func (m *ScannerManager) Run() {
	for {
		targets := m.targets()
		m.mu.Lock()
		for port, name := range targets {
			if _, ok := m.known[port]; !ok {
				log.Printf("scanner found: %s on %s", name, port)
			}
		}
		m.known = targets
		for port, name := range targets {
			if _, running := m.active[port]; !running {
				m.active[port] = false
				go m.session(port, name)
			}
		}
		m.mu.Unlock()
		m.publish()
		time.Sleep(scannerPollInterval)
	}
}

func (m *ScannerManager) targets() map[string]string {
	if m.fixedPort != "" {
		return map[string]string{m.fixedPort: m.fixedPort}
	}
	ports, err := bluetoothSerialPorts()
	if err != nil {
		log.Printf("scanner: listing Bluetooth ports: %v", err)
		return nil
	}
	targets := map[string]string{}
	for _, p := range ports {
		if m.match.MatchString(p.Device) {
			targets[p.Port] = p.Device
		}
	}
	return targets
}

func (m *ScannerManager) session(port, name string) {
	defer func() {
		m.mu.Lock()
		delete(m.active, port)
		m.mu.Unlock()
		m.publish()
	}()

	p, err := serial.Open(port, &serial.Mode{BaudRate: m.baud})
	if err != nil {
		m.mu.Lock()
		if !m.failing[port] {
			log.Printf("scanner %s (%s): %v (will keep retrying)", name, port, err)
		}
		m.failing[port] = true
		m.mu.Unlock()
		return
	}
	defer p.Close()

	m.mu.Lock()
	m.active[port] = true
	m.failing[port] = false
	m.mu.Unlock()
	m.publish()
	log.Printf("scanner connected: %s on %s", name, port)

	sc := bufio.NewScanner(p)
	sc.Split(scanLines)
	for sc.Scan() {
		m.handleScan(sc.Text())
	}
	log.Printf("scanner disconnected: %s on %s: %v", name, port, sc.Err())
}

func (m *ScannerManager) handleScan(text string) {
	appID, ok := parseScan(text)
	if !ok {
		log.Printf("scanner: ignoring %q", text)
		return
	}
	if _, res, err := m.launcher.Launch(appID, "scanner"); err != nil {
		log.Print(err)
	} else if res == NotInstalled {
		log.Printf("scanner: app %s not installed", appID)
	}
}

func (m *ScannerManager) publish() {
	m.mu.Lock()
	var parts []string
	for port, name := range m.known {
		state := "not connected"
		if m.active[port] {
			state = "connected"
		}
		if name == port {
			parts = append(parts, fmt.Sprintf("%s – %s", port, state))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s) – %s", name, port, state))
		}
	}
	sort.Strings(parts)
	status := "Scanner: none found"
	if len(parts) > 0 {
		status = "Scanner: " + strings.Join(parts, ", ")
	}
	changed := status != m.status
	m.status = status
	m.mu.Unlock()
	if changed && m.onStatus != nil {
		m.onStatus(status)
	}
}

// scanLines splits on \r, \n or \r\n, since scanners vary in their suffix.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		j := i + 1
		if data[i] == '\r' && j < len(data) && data[j] == '\n' {
			j++
		}
		return j, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
