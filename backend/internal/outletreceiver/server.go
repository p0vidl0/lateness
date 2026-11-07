package outletreceiver

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type call struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

// Server is the e2e stand-in for outside services. It records POST bodies (webhook and
// Telegram outlets), answers Telegram-style replies on /bot…/sendMessage, and serves
// /status/{code} as a probe target.
type Server struct {
	mu    sync.Mutex
	calls []call
}

func New() *Server {
	return &Server{}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/calls", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		snapshot := append([]call(nil), s.calls...)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"calls": snapshot})
	})
	mux.HandleFunc("/status/{code}", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.PathValue("code"))
		if err != nil || code < 200 || code > 599 {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, call{Path: r.URL.Path, Body: string(raw)})
		s.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}
