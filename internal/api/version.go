package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ServerVersion reports the version of the Flagsmith serving apiURL, from the
// unauthenticated /version endpoint — it answers what an instance supports
// before there is any credential to ask with. The value is the deployed image
// tag, which self-hosted instances often set to something that is not a version
// at all ("latest", a commit sha), so callers must read an unparseable result as
// unknown rather than old.
func ServerVersion(ctx context.Context, httpClient *http.Client, apiURL string) (string, error) {
	u := strings.TrimRight(apiURL, "/") + "/version/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("reaching %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", u, resp.Status)
	}
	var doc struct {
		ImageTag string `json:"image_tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("decoding %s: %w", u, err)
	}
	if doc.ImageTag == "" {
		return "", errors.New(u + " carries no image tag")
	}
	return doc.ImageTag, nil
}
