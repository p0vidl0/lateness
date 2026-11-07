package domain

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultProbeTimeout = 10 * time.Second
	MaxProbeTimeout     = 60 * time.Second
	ProbeRetry          = 30 * time.Second
)

// ProbeMethods are the HTTP methods a probe may use, in the order shown in forms.
var ProbeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// Probe is an HTTP(S) request the service sends on schedule. A matching
// response counts as an ok signal.
type Probe struct {
	Method       string        `json:"method"`
	URL          string        `json:"url"`
	ExpectStatus int           `json:"expect_status,omitempty"` // 0 accepts any 2xx
	Timeout      time.Duration `json:"timeout"`
}

// ProbeResult is what one probe attempt saw.
type ProbeResult struct {
	OK   bool
	Note string
}

func (p *Probe) Validate() error {
	p.Method = strings.ToUpper(strings.TrimSpace(p.Method))
	if p.Method == "" {
		p.Method = http.MethodGet
	}
	known := false
	for _, m := range ProbeMethods {
		known = known || m == p.Method
	}
	if !known {
		return fmt.Errorf("%w: probe method", ErrInvalid)
	}
	u, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: probe url", ErrInvalid)
	}
	p.URL = u.String()
	if p.ExpectStatus != 0 && (p.ExpectStatus < 100 || p.ExpectStatus > 599) {
		return fmt.Errorf("%w: probe status", ErrInvalid)
	}
	if p.Timeout == 0 {
		p.Timeout = DefaultProbeTimeout
	}
	if p.Timeout < time.Second || p.Timeout > MaxProbeTimeout {
		return fmt.Errorf("%w: probe timeout", ErrInvalid)
	}
	return nil
}

// Accepts reports whether a response status satisfies the probe.
func (p Probe) Accepts(status int) bool {
	if p.ExpectStatus == 0 {
		return status >= 200 && status < 300
	}
	return status == p.ExpectStatus
}

// ProbeRetryAfter is the pause after a failed attempt: short, but never longer than the period.
func ProbeRetryAfter(spec Schedule) time.Duration {
	if spec.Kind == ScheduleInterval && spec.Period > 0 && spec.Period < ProbeRetry {
		return spec.Period
	}
	return ProbeRetry
}
