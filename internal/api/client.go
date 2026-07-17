package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Auth applies an Authorization scheme to a request.
type Auth interface {
	Apply(req *http.Request)
}

// Bearer authenticates with an OAuth access token.
type Bearer string

func (b Bearer) Apply(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+string(b))
}

// APIKey authenticates with an organisation Master API key.
type APIKey string

func (k APIKey) Apply(req *http.Request) {
	req.Header.Set("Authorization", "Api-Key "+string(k))
}

func get(ctx context.Context, apiURL, path string, auth Auth, out any) error {
	u := strings.TrimRight(apiURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %s", u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// User is the subset of GET /api/v1/auth/users/me/ the CLI shows.
type User struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	UUID      string `json:"uuid"`
}

func UsersMe(ctx context.Context, apiURL string, auth Auth) (*User, error) {
	user := &User{}
	if err := get(ctx, apiURL, "/api/v1/auth/users/me/", auth, user); err != nil {
		return nil, err
	}
	return user, nil
}

// Organisation is the subset of GET /api/v1/organisations/ the CLI shows.
type Organisation struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func Organisations(ctx context.Context, apiURL string, auth Auth) ([]Organisation, error) {
	var page struct {
		Results []Organisation `json:"results"`
	}
	if err := get(ctx, apiURL, "/api/v1/organisations/", auth, &page); err != nil {
		return nil, err
	}
	return page.Results, nil
}
