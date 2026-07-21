package api

import (
	"bytes"
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
	var orgs []Organisation
	if err := getList(ctx, apiURL, "/api/v1/organisations/", auth, &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// getList decodes a list endpoint that may respond paginated
// ({count, results}) or as a bare array.
func getList(ctx context.Context, apiURL, path string, auth Auth, out any) error {
	var raw json.RawMessage
	if err := get(ctx, apiURL, path, auth, &raw); err != nil {
		return err
	}
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(raw, out)
	}
	var page struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return err
	}
	return json.Unmarshal(page.Results, out)
}

// Project is the subset of the projects API the CLI uses.
type Project struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func Projects(ctx context.Context, apiURL string, auth Auth, organisationID int) ([]Project, error) {
	var projects []Project
	path := fmt.Sprintf("/api/v1/projects/?organisation=%d", organisationID)
	if err := getList(ctx, apiURL, path, auth, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func CreateProject(ctx context.Context, apiURL string, auth Auth, name string, organisationID int) (*Project, error) {
	body, err := json.Marshal(map[string]any{"name": name, "organisation": organisationID})
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(apiURL, "/") + "/api/v1/projects/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST %s returned %s", u, resp.Status)
	}
	project := &Project{}
	if err := json.NewDecoder(resp.Body).Decode(project); err != nil {
		return nil, err
	}
	return project, nil
}

// Environment is the subset of the environments API the CLI uses.
type Environment struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	APIKey string `json:"api_key"`
}

func Environments(ctx context.Context, apiURL string, auth Auth, projectID int) ([]Environment, error) {
	var envs []Environment
	path := fmt.Sprintf("/api/v1/environments/?project=%d", projectID)
	if err := getList(ctx, apiURL, path, auth, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}

func CreateEnvironment(ctx context.Context, apiURL string, auth Auth, name string, projectID int) (*Environment, error) {
	body, err := json.Marshal(map[string]any{"name": name, "project": projectID})
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(apiURL, "/") + "/api/v1/environments/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST %s returned %s", u, resp.Status)
	}
	env := &Environment{}
	if err := json.NewDecoder(resp.Body).Decode(env); err != nil {
		return nil, err
	}
	return env, nil
}
