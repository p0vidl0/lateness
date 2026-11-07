package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

func TestProbeMethodAndStatus(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if r.URL.Path == "/down" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(srv.Close)
	h := New(true)

	res := h.Probe(context.Background(), domain.Probe{Method: http.MethodPost, URL: srv.URL + "/up", Timeout: time.Second})
	if !res.OK || method != http.MethodPost || !strings.Contains(res.Note, "→ 200") {
		t.Fatalf("%+v method=%s", res, method)
	}
	res = h.Probe(context.Background(), domain.Probe{Method: http.MethodGet, URL: srv.URL + "/down", Timeout: time.Second})
	if res.OK || !strings.Contains(res.Note, "503") || !strings.Contains(res.Note, "expected 2xx") {
		t.Fatalf("%+v", res)
	}
	res = h.Probe(context.Background(), domain.Probe{Method: http.MethodGet, URL: srv.URL + "/down", ExpectStatus: 503, Timeout: time.Second})
	if !res.OK {
		t.Fatalf("expected status should match: %+v", res)
	}
}

func TestProbeTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	res := New(true).Probe(context.Background(), domain.Probe{Method: http.MethodGet, URL: srv.URL, Timeout: 200 * time.Millisecond})
	if res.OK || res.Note != "timeout" {
		t.Fatalf("%+v", res)
	}
}

func TestProbeRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	res := New(false).Probe(context.Background(), domain.Probe{Method: http.MethodGet, URL: srv.URL, Timeout: time.Second})
	if res.OK || !strings.Contains(res.Note, "not allowed") {
		t.Fatalf("loopback must be refused: %+v", res)
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2a00:1450:4001::1": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.0.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "::1": false, "fd00::1": false, "0.0.0.0": false, "::ffff:127.0.0.1": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: got %v want %v", addr, got, want)
		}
	}
}
