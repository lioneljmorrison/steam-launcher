package main

import (
	"crypto/subtle"
	_ "embed"
	"html/template"
	"log"
	"net"
	"net/http"
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
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /launch/{appid}", s.launch)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		result(w, http.StatusNotFound, "Not found", "")
	})
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	// The index exposes the key, so only serve it to this PC.
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		result(w, http.StatusForbidden, "Forbidden", "Open this page on the PC itself.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	err := indexTmpl.Execute(w, map[string]any{
		"Apps": s.launcher.Apps(),
		"Base": s.base,
		"Key":  s.key,
	})
	if err != nil {
		log.Printf("render index: %v", err)
	}
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
		result(w, http.StatusInternalServerError, name, "Couldn't start Steam.")
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
