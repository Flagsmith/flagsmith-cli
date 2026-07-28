// Package httpx builds the *http.Client the CLI uses for every outbound request.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// maxRetries is the number of extra attempts (beyond the first) for a
	// retryable read request.
	maxRetries = 2

	baseBackoff = 200 * time.Millisecond
	maxBackoff  = 5 * time.Second

	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

// New returns an *http.Client whose transport applies sane per-connection
// timeouts, identifies the CLI with userAgent, and retries idempotent read
// requests on transient failures.
//
// It deliberately sets no overall Client.Timeout: the bound comes from the
// request context, so cancellation works and a per-request deadline does not
// have to fight a fixed cap.
func New(userAgent string) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	base.TLSHandshakeTimeout = tlsHandshakeTimeout
	base.ResponseHeaderTimeout = responseHeaderTimeout
	var rt http.RoundTripper = base
	// FLAGSMITH_DEBUG turns on per-request tracing to stderr. Wrapping the base
	// transport (rather than the retrying one) means every attempt is traced
	// individually, retries included.
	if envBool("FLAGSMITH_DEBUG") {
		rt = &tracer{base: rt, out: os.Stderr}
	}
	return &http.Client{
		Transport:     &transport{base: rt, userAgent: userAgent},
		CheckRedirect: checkRedirect,
	}
}

// maxRedirects mirrors the cap in Go's default policy, which setting
// CheckRedirect replaces.
const maxRedirects = 10

// secretHeaders never travel to an origin the request did not start at.
// Authorization carries the Admin credential; X-Environment-Key can carry a
// server-side (ser.) environment key, also a secret.
var secretHeaders = []string{"Authorization", "X-Environment-Key"}

// checkRedirect strips credentials when a redirect leaves the origin the
// request began at. Go's own rule is laxer in two ways: it forwards
// Authorization to any subdomain of the initial host (shouldCopyHeaderOnRedirect
// → isDomainOrSubdomain, so api.example → evil.api.example keeps it), and it
// applies to Authorization only, copying custom headers verbatim. It also
// ignores the scheme, so https → http on the same host would re-send both
// secrets in cleartext. Require the same host and no downgrade instead.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if !sameOrigin(via[0].URL, req.URL) {
		for _, h := range secretHeaders {
			req.Header.Del(h)
		}
	}
	return nil
}

// sameOrigin reports whether credentials may follow a redirect from → to:
// the same host (exactly — not a subdomain), over the same scheme or an
// upgrade to https, never a downgrade away from it.
func sameOrigin(from, to *url.URL) bool {
	if !strings.EqualFold(from.Host, to.Host) {
		return false
	}
	if strings.EqualFold(from.Scheme, "https") && !strings.EqualFold(to.Scheme, "https") {
		return false
	}
	return true
}

// envBool reads a boolean switch from the environment: presence alone is not
// truth, so FLAGSMITH_DEBUG=0 leaves tracing off. Duplicated because this
// package deliberately imports nothing internal.
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// tracer logs one line per HTTP round trip — method, URL, status, wall-clock
// duration, and time-to-first-byte — to out. It never logs request headers or
// bodies, so the Authorization credential stays out of the trace. Enabled via
// FLAGSMITH_DEBUG.
type tracer struct {
	base http.RoundTripper
	out  io.Writer
}

func (tr *tracer) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	var ttfb time.Duration
	ctx := httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotFirstResponseByte: func() { ttfb = time.Since(start) },
	})
	resp, err := tr.base.RoundTrip(req.WithContext(ctx))
	total := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Fprintf(tr.out, "[flagsmith http] %s %s -> error: %v (%s)\n", req.Method, req.URL, err, total)
		return resp, err
	}
	fmt.Fprintf(tr.out, "[flagsmith http] %s %s -> %d (%s, ttfb %s)\n",
		req.Method, req.URL, resp.StatusCode, total, ttfb.Round(time.Millisecond))
	return resp, err
}

// transport sets the User-Agent and retries reads, wrapping a base RoundTripper.
type transport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// User-Agent is not negotiable
	if t.userAgent != "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.userAgent)
	}
	if !isIdempotent(req.Method) {
		return t.base.RoundTrip(req)
	}
	return t.retry(req)
}

// retry re-sends a read request on transient failures. Only bodyless idempotent
// methods are retried, so the request replays without rewinding a body.
func (t *transport) retry(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if attempt >= maxRetries {
			return resp, err
		}
		retryAfter, ok := shouldRetry(resp, err)
		if !ok {
			return resp, err
		}
		// Drain and close the body so the connection can be reused before the
		// next attempt.
		if resp != nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)) //nolint:errcheck // best-effort drain
			resp.Body.Close()
		}
		select {
		case <-time.After(backoff(attempt, retryAfter)):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}

// isIdempotent reports whether a method is a safe read that may be retried.
func isIdempotent(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// shouldRetry reports whether a response/error pair is transient and worth
// retrying, plus any server-specified delay parsed from Retry-After. Context
// cancellation is never transient.
func shouldRetry(resp *http.Response, err error) (retryAfter time.Duration, ok bool) {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, false
		}
		return 0, true
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return parseRetryAfter(resp.Header.Get("Retry-After")), true
	}
	return 0, false
}

// backoff is the wait before the next attempt: an honoured Retry-After when the
// server sent one, else capped exponential backoff. Retry-After itself is not
// capped: the request context bounds an over-long wait.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	d := baseBackoff << attempt
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// parseRetryAfter reads a Retry-After header value in either delta-seconds or
// HTTP-date form, returning 0 when absent, malformed, or in the past.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
