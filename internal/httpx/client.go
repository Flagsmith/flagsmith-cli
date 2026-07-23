// Package httpx builds the *http.Client the CLI uses for every outbound
// request. It centralises the transport concerns that were previously missing
// from http.DefaultClient: sane per-connection timeouts, a versioned
// User-Agent, and bounded retries for idempotent reads.
package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
// timeouts, sets a default User-Agent when the request carries none, and
// retries idempotent read requests on transient failures.
//
// It deliberately sets no overall Client.Timeout: the overall bound comes from
// the request context, so cancellation works and callers (e.g. interactive
// commands) can impose their own deadline without fighting a fixed cap.
func New(userAgent string) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	base.TLSHandshakeTimeout = tlsHandshakeTimeout
	base.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{
		Transport: &transport{base: base, userAgent: userAgent},
	}
}

// transport sets the User-Agent and retries reads. It wraps a base
// RoundTripper (a configured *http.Transport in production, swappable in tests).
type transport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A RoundTripper must not mutate the caller's request, so clone before
	// stamping the default User-Agent.
	if t.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.userAgent)
	}
	if !isIdempotent(req.Method) {
		return t.base.RoundTrip(req)
	}
	return t.retry(req)
}

// retry re-sends a read request on transient failures. Only bodyless idempotent
// methods reach here, so the same request can be replayed without rewinding a
// body.
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
// server sent one, else capped exponential backoff. Retry-After is not capped
// here — the request context bounds an over-long wait.
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
