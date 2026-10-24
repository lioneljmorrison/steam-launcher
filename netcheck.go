package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// Settings checks a port and address before they're saved: the address is
// this PC's, the port is free, and a phone can reach them. Only a phone's
// visit proves the firewall and Wi-Fi let it through; the PC reaching itself
// skips the firewall, so its own visits don't count.

const (
	netTestFor   = 2 * time.Minute
	freePortFrom = 8787
	freePortTo   = 8887
)

type lanAddr struct {
	IP   string `json:"ip"`
	Name string `json:"name"` // the network adapter
}

// lanAddrs lists this PC's IPv4 addresses a phone could reach: up, not
// loopback or link-local, and not the virtual adapters WSL and Hyper-V add.
func lanAddrs() []lanAddr {
	var out []lanAddr
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		name := strings.ToLower(ifc.Name)
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			strings.Contains(name, "vethernet") || strings.Contains(name, "wsl") || strings.Contains(name, "hyper-v") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, lanAddr{IP: n.IP.String(), Name: ifc.Name})
			}
		}
	}
	return out
}

// isThisPC reports whether ip is loopback or any address of this PC,
// virtual adapters included.
func isThisPC(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// portFree reports whether the app could have port to itself after a
// restart. Its own port counts as free. Listening the way main does isn't
// enough: Windows lets that dual-stack listener share a port another
// program holds on IPv4 only, and phones would reach that program. So IPv4
// is tried alone too, and so is connecting to it.
func (s *Server) portFree(port int) error {
	if port == s.cfg.Port {
		return nil
	}
	p := strconv.Itoa(port)
	for _, network := range []string{"tcp4", "tcp"} {
		ln, err := net.Listen(network, ":"+p)
		if err != nil {
			if errors.Is(err, windows.WSAEACCES) {
				return fmt.Errorf("port %d is reserved by Windows", port)
			}
			return fmt.Errorf("port %d is in use by another program", port)
		}
		ln.Close()
	}
	if c, err := net.DialTimeout("tcp4", "127.0.0.1:"+p, 300*time.Millisecond); err == nil {
		c.Close()
		return fmt.Errorf("port %d is in use by another program", port)
	}
	return nil
}

// netTest is one phone test: a token the phone's visit must carry, and, for
// a port the app isn't on yet, a listener serving only the test page.
type netTest struct {
	token   string
	ln      net.Listener // nil when the test uses the running server
	reached string       // the phone's address, once it visits
	local   bool         // this PC visited, which proves nothing
	timer   *time.Timer
}

type netTests struct {
	mu  sync.Mutex
	cur *netTest // one test at a time
}

func (t *netTests) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

func (t *netTests) stopLocked() {
	if t.cur == nil {
		return
	}
	t.cur.timer.Stop()
	if t.cur.ln != nil {
		t.cur.ln.Close()
	}
	t.cur = nil
}

// checkNetwork checks an address and port from the Settings form and, when
// both can work, starts a phone test of them.
func (s *Server) checkNetwork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HostIP string `json:"hostIP"`
		Port   int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.tests.stop() // frees the port a previous test held
	out := map[string]any{}

	ip := strings.TrimSpace(req.HostIP)
	auto := ip == ""
	if auto {
		ip = lanIP()
	}
	parsed := net.ParseIP(ip)
	switch {
	case parsed == nil:
		out["address"] = map[string]any{"ok": false, "message": fmt.Sprintf("%q isn't an IP address.", ip)}
	case !isThisPC(parsed):
		out["address"] = map[string]any{"ok": false, "warn": true, "message": ip + " isn't an address of this PC, so phones can only reach it if something forwards it here."}
	case parsed.IsLoopback():
		out["address"] = map[string]any{"ok": false, "message": ip + " only works on this PC; phones can't reach it."}
	default:
		msg := ip + " is this PC."
		if auto {
			msg = "Detected " + ip + ", this PC."
		}
		out["address"] = map[string]any{"ok": true, "message": msg}
	}

	portOK := req.Port >= 1 && req.Port <= 65535
	if !portOK {
		out["port"] = map[string]any{"ok": false, "message": "The port must be between 1 and 65535."}
	} else if err := s.portFree(req.Port); err != nil {
		portOK = false
		out["port"] = map[string]any{"ok": false, "message": err.Error() + ". Pick another, or use Find a free port."}
	} else if req.Port == s.cfg.Port {
		out["port"] = map[string]any{"ok": true, "message": fmt.Sprintf("Port %d is the one the app uses now.", req.Port)}
	} else {
		out["port"] = map[string]any{"ok": true, "message": fmt.Sprintf("Port %d is free.", req.Port)}
	}

	if parsed != nil && !parsed.IsLoopback() && portOK {
		test, err := s.startNetTest(req.Port)
		if err != nil {
			out["test"] = map[string]any{"error": err.Error()}
		} else {
			out["test"] = map[string]any{"token": test, "url": fmt.Sprintf("http://%s/ping?t=%s", net.JoinHostPort(ip, strconv.Itoa(req.Port)), test), "seconds": int(netTestFor.Seconds())}
		}
	}
	writeJSON(w, out)
}

func (s *Server) startNetTest(port int) (string, error) {
	b := make([]byte, 8)
	rand.Read(b)
	t := &netTest{token: hex.EncodeToString(b)}
	if port != s.cfg.Port {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			return "", fmt.Errorf("couldn't open port %d for the test: %v", port, err)
		}
		t.ln = ln
		mux := http.NewServeMux()
		mux.HandleFunc("GET /ping", s.ping)
		go http.Serve(ln, mux)
	}
	s.tests.mu.Lock()
	defer s.tests.mu.Unlock()
	s.tests.stopLocked()
	t.timer = time.AfterFunc(netTestFor, func() {
		s.tests.mu.Lock()
		defer s.tests.mu.Unlock()
		if s.tests.cur == t {
			s.tests.stopLocked()
		}
	})
	s.tests.cur = t
	return t.token, nil
}

// ping is the page the test QR code opens. It's public, so it shows nothing
// but whether this visit counted.
func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	local := isThisPC(net.ParseIP(host))
	s.tests.mu.Lock()
	t := s.tests.cur
	valid := t != nil && r.URL.Query().Get("t") == t.token
	if valid {
		if local {
			t.local = true
		} else if t.reached == "" {
			t.reached = host
			if t.ln != nil {
				ln := t.ln
				time.AfterFunc(time.Second, func() { ln.Close() }) // after this page is sent
			}
		}
	}
	s.tests.mu.Unlock()
	switch {
	case !valid:
		result(w, http.StatusNotFound, "Test over", "This connection test has ended. Start a new one from Settings.")
	case local:
		result(w, http.StatusOK, "That was this PC", "Opened on the PC itself, this doesn't test the network. Scan the code with your phone.")
	default:
		result(w, http.StatusOK, "Connected", productName+" can be reached from this device. You can close this page.")
	}
}

// netTestStatus tells Settings whether the phone has visited.
func (s *Server) netTestStatus(w http.ResponseWriter, r *http.Request) {
	s.tests.mu.Lock()
	defer s.tests.mu.Unlock()
	t := s.tests.cur
	if t == nil || t.token != r.PathValue("token") {
		writeJSON(w, map[string]any{"over": true})
		return
	}
	writeJSON(w, map[string]any{"reached": t.reached, "local": t.local})
}

func (s *Server) stopNetTest(w http.ResponseWriter, r *http.Request) {
	s.tests.stop()
	writeJSON(w, map[string]any{})
}

// freePort finds the first free port from 8787 up; numbers near the
// default read better in printed QR codes than a random one. The port the
// app is on is skipped, since asking means wanting another.
func (s *Server) freePort(w http.ResponseWriter, r *http.Request) {
	s.tests.stop()
	for p := freePortFrom; p <= freePortTo; p++ {
		if p != s.cfg.Port && s.portFree(p) == nil {
			writeJSON(w, map[string]any{"port": p})
			return
		}
	}
	http.Error(w, fmt.Sprintf("no free port between %d and %d", freePortFrom, freePortTo), http.StatusConflict)
}
