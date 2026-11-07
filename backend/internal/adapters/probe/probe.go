// Package probe performs HTTP(S) probes for expectations with source "probe".
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"github.com/p0vidl0/lateness/internal/domain"
)

const (
	maxRedirects = 5
	maxBody      = 1 << 16
	userAgent    = "Lateness-Probe/1"
)

var errBlocked = errors.New("address is not allowed")

// HTTP probes URLs. Unless AllowPrivate is set, it refuses loopback, private,
// link-local and other non-public addresses, including after DNS resolution and redirects.
type HTTP struct {
	AllowPrivate bool
	client       *http.Client
}

func New(allowPrivate bool) *HTTP {
	h := &HTTP{AllowPrivate: allowPrivate}
	dialer := &net.Dialer{Timeout: domain.MaxProbeTimeout, Control: h.control}
	transport := &http.Transport{
		Proxy:                 nil, // a proxy would bypass the address check
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: domain.MaxProbeTimeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
	h.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}
			return nil
		},
	}
	return h
}

// control runs after DNS resolution, right before connect, so it sees the real address.
func (h *HTTP) control(_, address string, _ syscall.RawConn) error {
	if h.AllowPrivate {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errBlocked
	}
	if !public(ap.Addr()) {
		return fmt.Errorf("%w: %s", errBlocked, ap.Addr())
	}
	return nil
}

func public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() &&
		!cgnat.Contains(a) && !a.IsUnspecified()
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func (h *HTTP) Probe(ctx context.Context, p domain.Probe) domain.ProbeResult {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = domain.DefaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, p.Method, p.URL, nil)
	if err != nil {
		return domain.ProbeResult{Note: "bad request: " + err.Error()}
	}
	req.Header.Set("User-Agent", userAgent)
	start := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		return domain.ProbeResult{Note: describe(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	elapsed := time.Since(start).Round(time.Millisecond)
	note := fmt.Sprintf("%s %s → %d, %d ms", p.Method, p.URL, resp.StatusCode, elapsed.Milliseconds())
	if !p.Accepts(resp.StatusCode) {
		want := "2xx"
		if p.ExpectStatus != 0 {
			want = fmt.Sprint(p.ExpectStatus)
		}
		note += ", expected " + want
	}
	return domain.ProbeResult{OK: p.Accepts(resp.StatusCode), Note: note}
}

func describe(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errBlocked):
		return "address is not allowed for probes"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
