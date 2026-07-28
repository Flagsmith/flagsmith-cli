package cmd

import (
	"net/http"
	"sync"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/httpx"
	"github.com/Flagsmith/flagsmith-cli/internal/version"
)

// userAgent identifies the CLI on every outbound request.
func userAgent() string {
	return version.UserAgent()
}

// sharedHTTPClient is the process-wide HTTP client. It is stateless with respect
// to the target instance, so one client serves every request and its connection
// pool is reused across calls.
var (
	httpClientOnce sync.Once
	httpClientMemo *http.Client
)

func sharedHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		httpClientMemo = httpx.New(userAgent())
	})
	return httpClientMemo
}

// newAPIClient builds an Admin API client for the resolved instance and the
// given auth scheme, over the shared HTTP client.
func newAPIClient(a api.Auth) *api.Client {
	return api.NewClient(apiURL, a, api.WithHTTPClient(sharedHTTPClient()), api.WithUserAgent(userAgent()))
}
