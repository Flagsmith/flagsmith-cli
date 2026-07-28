package cmd

import (
	"net/url"
	"os"
	"strings"
)

// Credential env vars name no host, so on their own they cannot say which
// instance they belong to — and flagsmith.json is discovered by walking up
// from the working directory, so any checkout can name an apiUrl the user
// never chose. Every secret-bearing variable therefore has a host-scoped
// form, and the unscoped form is trusted only for the surface's default host
// (see docs/design/03-authentication.md §6).

// envBool reads a boolean switch from the environment. Presence alone is not
// truth: `FLAGSMITH_NO_INPUT=false` must leave prompting on, and
// `FLAGSMITH_JSON_OUTPUT=0` must leave human output on — a CI job that sets
// these from a template variable would otherwise get the opposite of what it
// wrote. Unset or empty is false; 0/false/no/off (any case) are false;
// anything else is true, so `=1` and `=anything` keep working.
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// scopedEnvName is the host-scoped form of a credential variable for an
// instance URL: the host and port with `-` written `__` and `.` and `:`
// written `_`. The scheme is not part of the scope.
func scopedEnvName(base, rawURL string) string {
	host := urlHost(rawURL)
	host = strings.ReplaceAll(host, "-", "__")
	host = strings.ReplaceAll(host, ".", "_")
	host = strings.ReplaceAll(host, ":", "_")
	return base + "_" + host
}

// urlHost is a URL's host and port, lowercased. A value that does not parse
// as a URL is treated as a bare host.
func urlHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return strings.ToLower(strings.Trim(rawURL, "/"))
}

// envCredential resolves a credential variable for the instance at rawURL,
// returning the exact variable it came from (so output can name it) and its
// value. The host-scoped form wins wherever it applies; the unscoped form is
// used only when rawURL is the surface's default host, so a redirected host
// never receives a credential that was not scoped to it. No credential
// reports an empty name, leaving resolution to fall through to the next
// source (e.g. the keychain, which is already keyed per instance).
func envCredential(base, rawURL, defaultURL string) (name, value string) {
	if k, v := lookupEnvFold(scopedEnvName(base, rawURL)); v != "" {
		return k, v
	}
	if urlHost(rawURL) == urlHost(defaultURL) {
		if v := os.Getenv(base); v != "" {
			return base, v
		}
	}
	return "", ""
}

// lookupEnvFold finds an environment variable by case-insensitive name,
// returning the name as actually set. Host-scoped variable names embed a
// hostname, which is itself case-insensitive.
func lookupEnvFold(name string) (string, string) {
	if v := os.Getenv(name); v != "" {
		return name, v
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && strings.EqualFold(k, name) {
			return k, v
		}
	}
	return "", ""
}
