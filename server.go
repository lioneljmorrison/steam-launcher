package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
)

//go:embed index.html
var indexHTML string

var (
	indexTmpl  = template.Must(template.New("index").Parse(indexHTML))
	resultTmpl = template.Must(template.New("result").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>body{font-family:system-ui,sans-serif;background:#1b2838;color:#fff;display:grid;place-items:center;min-height:90vh;margin:0;text-align:center;padding:16px}</style>
</head><body><div><h1>{{.Title}}</h1><p>{{.Message}}</p></div></body></html>`))
)

type Server struct {
	launcher *Launcher
	key      string
	base     string
	nfc      *NFCManager // nil when the NFC base is disabled
	details  *DetailsStore
	cfg      Config
	quit     func() // exits the app (after a relaunch or update has started)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /launch/{appid}", s.launch)
	mux.HandleFunc("GET /cover/{appid}", s.art)
	mux.HandleFunc("GET /art/{appid}/{kind}", s.art)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("GET /api/settings", s.localAPI(s.getSettings))
	mux.HandleFunc("POST /api/settings", s.localAPI(s.saveSettings))
	mux.HandleFunc("POST /api/update/check", s.localAPI(s.updateCheck))
	mux.HandleFunc("POST /api/update/install", s.localAPI(s.updateInstall))
	mux.HandleFunc("GET /api/cards", s.localAPI(s.withNFC(s.listCards)))
	mux.HandleFunc("POST /api/cards/link", s.localAPI(s.withNFC(s.linkCard)))
	mux.HandleFunc("POST /api/cards/unlink", s.localAPI(s.withNFC(s.unlinkCard)))
	mux.HandleFunc("POST /api/cards/cancel", s.localAPI(s.withNFC(func(w http.ResponseWriter, r *http.Request) {
		s.nfc.CancelPending()
		s.listCards(w, r)
	})))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		result(w, http.StatusNotFound, "Not found", "")
	})
	return mux
}

func isLoopback(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	// The index exposes the key, so only serve it to this PC.
	if !isLoopback(r) {
		result(w, http.StatusForbidden, "Forbidden", "Open this page on the PC itself.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	apps := s.launcher.Apps()
	details := map[string]*Details{}
	for _, a := range apps {
		if d := s.details.Get(a.ID); d != nil {
			details[a.ID] = d
		}
	}
	err := indexTmpl.Execute(w, map[string]any{
		"Apps":    apps,
		"Details": details,
		"Base":    s.base,
		"Key":     s.key,
		"NFC":     s.nfc != nil,
	})
	if err != nil {
		log.Printf("render index: %v", err)
	}
}

// art serves cached Steam artwork: /art/{appid}/{capsule|header|hero|logo}
// (/cover/{appid} is the header). GOG art redirects to GOG's image CDN.
func (s *Server) art(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	appID, kind := r.PathValue("appid"), r.PathValue("kind")
	if kind == "" {
		kind = "header"
	}
	if isGOG(appID) {
		if d := s.details.Get(appID); d != nil && d.Art[kind] != "" {
			http.Redirect(w, r, d.Art[kind], http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	if _, err := strconv.Atoi(appID); err != nil || artFiles[kind] == nil {
		http.NotFound(w, r)
		return
	}
	path, ok := artPath(appID, kind)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, path)
}

// localAPI guards the API: this PC only, and the key in X-Key so other web
// pages open in the browser can't call it.
func (s *Server) localAPI(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Key")), []byte(s.key)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) withNFC(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.nfc == nil {
			http.Error(w, "NFC base disabled", http.StatusNotFound)
			return
		}
		h(w, r)
	}
}

type cardInfo struct {
	UID   string `json:"uid"`
	AppID string `json:"appid"`
	Name  string `json:"name"`
}

func (s *Server) listCards(w http.ResponseWriter, r *http.Request) {
	names := map[string]string{}
	for _, a := range s.launcher.Apps() {
		names[a.ID] = a.Name
	}
	cards := []cardInfo{}
	for uid, appID := range s.nfc.cards.All() {
		cards = append(cards, cardInfo{UID: uid, AppID: appID, Name: names[appID]})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
	writeJSON(w, map[string]any{"pending": s.nfc.Pending(), "cards": cards})
}

func (s *Server) linkCard(w http.ResponseWriter, r *http.Request) {
	var req struct{ UID, AppID string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UID == "" || req.AppID == "" {
		http.Error(w, "need uid and appid", http.StatusBadRequest)
		return
	}
	if err := s.nfc.Link(req.UID, req.AppID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.listCards(w, r)
}

func (s *Server) unlinkCard(w http.ResponseWriter, r *http.Request) {
	var req struct{ UID string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UID == "" {
		http.Error(w, "need uid", http.StatusBadRequest)
		return
	}
	if err := s.nfc.Unlink(req.UID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.listCards(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("k")), []byte(s.key)) != 1 {
		result(w, http.StatusForbidden, "Forbidden", "Bad or missing key.")
		return
	}
	appID := r.PathValue("appid")
	name, res, err := s.launcher.Launch(appID, "phone "+r.RemoteAddr)
	switch {
	case res == NotInstalled:
		result(w, http.StatusNotFound, "Not installed", "App "+appID+" is not installed on this PC.")
	case err != nil:
		log.Print(err)
		result(w, http.StatusInternalServerError, name, "Couldn't start the game.")
	case res == AlreadyLaunching:
		result(w, http.StatusOK, name, "Already launching.")
	default:
		result(w, http.StatusOK, name, "Launching on your PC…")
	}
}

func result(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	resultTmpl.Execute(w, map[string]string{"Title": title, "Message": message})
}

func lanIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
