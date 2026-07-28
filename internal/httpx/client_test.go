package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc is a stub base transport for exercising retry/cancel paths
// without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetriesIdempotentReads(t *testing.T) {
	t.Run("GET retries 5xx then succeeds", func(t *testing.T) {
		// Given
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&hits, 1) <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		// When
		resp, err := New("ua").Get(srv.URL)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if got := atomic.LoadInt32(&hits); got != 3 {
			t.Errorf("hits = %d, want 3 (1 + 2 retries)", got)
		}
	})

	t.Run("writes are never retried", func(t *testing.T) {
		// Given
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		// When
		resp, err := New("ua").Post(srv.URL, "text/plain", nil)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := atomic.LoadInt32(&hits); got != 1 {
			t.Errorf("hits = %d, want 1 (no retry on POST)", got)
		}
	})

	t.Run("gives up after maxRetries", func(t *testing.T) {
		// Given
		var calls int32
		tr := &transport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return nil, errors.New("dial tcp: connection refused")
		})}
		req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)

		// When
		_, err := tr.RoundTrip(req)

		// Then
		if err == nil {
			t.Fatal("want an error after exhausting retries")
		}
		if got := atomic.LoadInt32(&calls); got != maxRetries+1 {
			t.Errorf("calls = %d, want %d", got, maxRetries+1)
		}
	})
}

func TestSetsUserAgent(t *testing.T) {
	t.Run("stamps the configured agent", func(t *testing.T) {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
		defer srv.Close()

		resp, err := New("flagsmith-cli/test").Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got != "flagsmith-cli/test" {
			t.Errorf("User-Agent = %q, want flagsmith-cli/test", got)
		}
	})

	// The CLI's identity wins over a caller-set one — including the one the
	// Flagsmith SDK sets on every request of its own.
	t.Run("overrides a caller-set agent", func(t *testing.T) {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
		}))
		defer srv.Close()

		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		req.Header.Set("User-Agent", "custom/1.0")
		resp, err := New("flagsmith-cli/test").Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got != "flagsmith-cli/test" {
			t.Errorf("User-Agent = %q, want flagsmith-cli/test", got)
		}
		// The caller's own request must come back untouched.
		if req.Header.Get("User-Agent") != "custom/1.0" {
			t.Errorf("caller's request was mutated: %q", req.Header.Get("User-Agent"))
		}
	})
}

func TestContextCancellationStopsBackoff(t *testing.T) {
	// Given
	tr := &transport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("temporary failure")
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the backoff wait must abort immediately
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/", nil)

	// When
	start := time.Now()
	_, err := tr.RoundTrip(req)

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > baseBackoff {
		t.Errorf("waited %v, want near-immediate return", elapsed)
	}
}

func TestDebugTracing(t *testing.T) {
	stubOK := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})

	t.Run("logs method, url and status for each call", func(t *testing.T) {
		// Given
		var buf bytes.Buffer
		tr := &tracer{base: stubOK, out: &buf}
		req, _ := http.NewRequest(http.MethodGet, "https://api.flagsmith.com/api/v1/environments/", nil)

		// When
		resp, err := tr.RoundTrip(req)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		line := buf.String()
		for _, want := range []string{"GET", "https://api.flagsmith.com/api/v1/environments/", "200"} {
			if !strings.Contains(line, want) {
				t.Errorf("trace %q missing %q", line, want)
			}
		}
	})

	t.Run("traces every retry attempt", func(t *testing.T) {
		// Given
		var buf bytes.Buffer
		var hits int32
		base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			code := http.StatusOK
			if atomic.AddInt32(&hits, 1) <= 2 {
				code = http.StatusServiceUnavailable
			}
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
		})
		tr := &transport{base: &tracer{base: base, out: &buf}}
		req, _ := http.NewRequest(http.MethodGet, "https://api.flagsmith.com/x", nil)

		// When
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		// Then
		if got := strings.Count(buf.String(), "GET https://api.flagsmith.com/x"); got != 3 {
			t.Errorf("traced attempts = %d, want 3\n%s", got, buf.String())
		}
	})

	t.Run("New enables tracing only when FLAGSMITH_DEBUG is truthy", func(t *testing.T) {
		tracing := func() bool {
			_, ok := New("ua").Transport.(*transport).base.(*tracer)
			return ok
		}
		for _, off := range []string{"", "0", "false", "off"} {
			t.Setenv("FLAGSMITH_DEBUG", off)
			if tracing() {
				t.Errorf("tracing must be off for FLAGSMITH_DEBUG=%q", off)
			}
		}
		for _, on := range []string{"1", "true", "yes"} {
			t.Setenv("FLAGSMITH_DEBUG", on)
			if !tracing() {
				t.Errorf("tracing must be on for FLAGSMITH_DEBUG=%q", on)
			}
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"2", 2 * time.Second},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"-1", 0},
		{"garbage", 0},
		{time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.in); got != c.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	// A future HTTP-date yields a positive, roughly-correct delay.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 31*time.Second {
		t.Errorf("parseRetryAfter(future) = %v, want ~30s", got)
	}
}

func TestShouldRetry(t *testing.T) {
	if _, ok := shouldRetry(nil, context.Canceled); ok {
		t.Error("context.Canceled must not be retried")
	}
	if _, ok := shouldRetry(nil, context.DeadlineExceeded); ok {
		t.Error("context.DeadlineExceeded must not be retried")
	}
	if _, ok := shouldRetry(nil, errors.New("boom")); !ok {
		t.Error("a generic transport error should be retried")
	}
	if _, ok := shouldRetry(&http.Response{StatusCode: 200}, nil); ok {
		t.Error("200 must not be retried")
	}
	if _, ok := shouldRetry(&http.Response{StatusCode: 404}, nil); ok {
		t.Error("404 must not be retried")
	}
	if _, ok := shouldRetry(&http.Response{StatusCode: 500, Header: http.Header{}}, nil); !ok {
		t.Error("500 should be retried")
	}
}

// Go's stdlib strips Authorization when a redirect crosses hosts, but copies
// custom headers verbatim — and X-Environment-Key can carry a server-side
// (ser.) secret via `flagsmith api --sdk`. It must get the same treatment.
func TestRedirectStripsEnvironmentKeyAcrossHosts(t *testing.T) {
	// Given
	var got http.Header
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/self":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			got = r.Header.Clone()
		default:
			http.Redirect(w, r, other.URL, http.StatusFound)
		}
	}))
	defer origin.Close()
	c := New("test-agent")

	fetch := func(u string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Environment-Key", "ser.secret")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// When
	fetch(origin.URL)
	if v := got.Get("X-Environment-Key"); v != "" {
		t.Errorf("X-Environment-Key = %q at the cross-host target, want it stripped", v)
	}

	// When
	fetch(origin.URL + "/self")
	if v := got.Get("X-Environment-Key"); v != "ser.secret" {
		t.Errorf("X-Environment-Key = %q after a same-host redirect, want it kept", v)
	}
}

func TestCheckRedirectStripsSecrets(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		wantKept bool
	}{
		{"same origin", "https://api.example/a", "https://api.example/b", true},
		{"upgrade to https is fine", "http://api.example/a", "https://api.example/b", true},
		{"scheme downgrade on the same host", "https://api.example/a", "http://api.example/b", false},
		{"different host", "https://api.example/a", "https://evil.example/b", false},
		{"subdomain of the original host", "https://api.example/a", "https://evil.api.example/b", false},
		{"parent of the original host", "https://api.example/a", "https://example/b", false},
		{"different port", "https://api.example/a", "https://api.example:8443/b", false},
		{"host case is not a difference", "https://API.example/a", "https://api.example/b", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Given
			from, err := http.NewRequest(http.MethodGet, c.from, nil)
			if err != nil {
				t.Fatal(err)
			}
			to, err := http.NewRequest(http.MethodGet, c.to, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Literal names, not secretHeaders: the test must fail if a
			// header is dropped from the production list.
			for _, h := range []string{"Authorization", "X-Environment-Key"} {
				to.Header.Set(h, "secret")
			}

			// When
			if err := checkRedirect(to, []*http.Request{from}); err != nil {
				t.Fatal(err)
			}

			// Then
			for _, h := range []string{"Authorization", "X-Environment-Key"} {
				if kept := to.Header.Get(h) != ""; kept != c.wantKept {
					t.Errorf("%s kept = %v, want %v", h, kept, c.wantKept)
				}
			}
		})
	}
}

func TestCheckRedirectCapsHops(t *testing.T) {
	// Given
	req, err := http.NewRequest(http.MethodGet, "https://api.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = req
	}

	// When
	err = checkRedirect(req, via)

	// Then
	if err == nil {
		t.Fatal("err = nil, want the redirect chain to stop")
	}
	if got := err.Error(); got != "stopped after 10 redirects" {
		t.Errorf("err = %q, want it to name the cap in words", got)
	}
}
