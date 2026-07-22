package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// sendJSON issues a request with an optional JSON body and decodes an optional
// JSON response. It treats any non-2xx status as an error.
func sendJSON(ctx context.Context, apiURL, method, path string, auth Auth, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	u := strings.TrimRight(apiURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %s", method, u, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Project is the subset of the projects API the CLI uses.
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	UseEdgeIdentities bool   `json:"use_edge_identities"`
}

// GetProject fetches a single project — notably its use_edge_identities flag,
// which decides whether identity overrides use the core or edge endpoints.
func GetProject(ctx context.Context, apiURL string, auth Auth, projectID int) (*Project, error) {
	p := &Project{}
	if err := get(ctx, apiURL, fmt.Sprintf("/api/v1/projects/%d/", projectID), auth, p); err != nil {
		return nil, err
	}
	return p, nil
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

// FeatureState is a feature's state in one environment: its on/off and typed
// value. In the project features list, feature_state_value is a bare scalar.
type FeatureState struct {
	Enabled bool `json:"enabled"`
	Value   any  `json:"feature_state_value"`
}

// CodeReferenceCount is a per-repository count of code references to a feature.
type CodeReferenceCount struct {
	Count int `json:"count"`
}

// Feature is a project feature with its state in the requested environment.
// The CLI projects it into a curated shape for output; the fields here are the
// subset those views need.
type Feature struct {
	ID                   int                  `json:"id"`
	Name                 string               `json:"name"`
	Type                 string               `json:"type"`
	Description          string               `json:"description"`
	NumSegmentOverrides  int                  `json:"num_segment_overrides"`
	NumIdentityOverrides *int                 `json:"num_identity_overrides"`
	LifecycleStage       string               `json:"lifecycle_stage"`
	CodeReferencesCounts []CodeReferenceCount `json:"code_references_counts"`
	EnvironmentState     *FeatureState        `json:"environment_feature_state"`
	SegmentState         *FeatureState        `json:"segment_feature_state"`
}

// CodeReferences totals the per-repository code reference counts.
func (f Feature) CodeReferences() int {
	total := 0
	for _, c := range f.CodeReferencesCounts {
		total += c.Count
	}
	return total
}

// Features lists a project's features with their state in one environment, via
// the Admin API. The environment is identified by its numeric ID. When
// segmentID is non-zero, each feature also carries its segment_feature_state
// for that segment.
func Features(ctx context.Context, apiURL string, auth Auth, projectID, environmentID, segmentID int) ([]Feature, error) {
	var features []Feature
	path := fmt.Sprintf("/api/v1/projects/%d/features/?environment=%d", projectID, environmentID)
	if segmentID != 0 {
		path += fmt.Sprintf("&segment=%d", segmentID)
	}
	if err := getList(ctx, apiURL, path, auth, &features); err != nil {
		return nil, err
	}
	return features, nil
}

// FeatureRef targets a feature by name or id (exactly one) in update-flag-v2.
type FeatureRef struct {
	Name string `json:"name,omitempty"`
	ID   int    `json:"id,omitempty"`
}

// FeatureValue is a typed flag value in the update-flag-v2 wire form: the type
// as a word and the value always as a string.
type FeatureValue struct {
	Type  string `json:"type"`  // "integer" | "string" | "boolean"
	Value string `json:"value"` // always a string; parsed server-side per type
}

// EnvironmentDefault is the environment-wide state update-flag-v2 requires in
// full on every call.
type EnvironmentDefault struct {
	Enabled bool         `json:"enabled"`
	Value   FeatureValue `json:"value"`
}

// SegmentOverride is one segment's state in the update-flag-v2 body.
type SegmentOverride struct {
	SegmentID int          `json:"segment_id"`
	Enabled   bool         `json:"enabled"`
	Value     FeatureValue `json:"value"`
}

// UpdateFlagRequest is the update-flag-v2 body. environment_default is always
// required; segment_overrides only creates/updates the segments listed and
// never removes others. This endpoint does not manage identity overrides.
type UpdateFlagRequest struct {
	Feature            FeatureRef         `json:"feature"`
	EnvironmentDefault EnvironmentDefault `json:"environment_default"`
	SegmentOverrides   []SegmentOverride  `json:"segment_overrides,omitempty"`
}

// DeleteSegmentOverride removes a feature's override for one segment, via the
// experimental delete-segment-override endpoint keyed by the environment key.
func DeleteSegmentOverride(ctx context.Context, apiURL string, auth Auth, environmentKey, featureName string, segmentID int) error {
	body, err := json.Marshal(map[string]any{
		"feature": FeatureRef{Name: featureName},
		"segment": map[string]int{"id": segmentID},
	})
	if err != nil {
		return err
	}
	u := strings.TrimRight(apiURL, "/") + "/api/experiments/environments/" + environmentKey + "/delete-segment-override/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return ErrWorkflowGated
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no override exists for segment %d", segmentID)
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s returned %s", u, resp.Status)
	}
	return nil
}

// ErrWorkflowGated is returned when update-flag-v2 refuses because the
// environment has change-request workflows enabled.
var ErrWorkflowGated = fmt.Errorf("this environment uses change-request workflows; direct updates are disabled")

// UpdateFlag applies an environment-default change via the experimental
// update-flag-v2 endpoint, keyed by the environment's client-side key. The
// endpoint returns 204 No Content on success.
func UpdateFlag(ctx context.Context, apiURL string, auth Auth, environmentKey string, in UpdateFlagRequest) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	u := strings.TrimRight(apiURL, "/") + "/api/experiments/environments/" + environmentKey + "/update-flag-v2/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.Apply(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return ErrWorkflowGated
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s returned %s", u, resp.Status)
	}
	return nil
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

// IdentityFeatureState is a feature's override for one identity. ID is the
// (core) feature-state id used to update/delete it; it is unset for edge reads.
type IdentityFeatureState struct {
	ID      int  `json:"id"`
	Enabled bool `json:"enabled"`
	Value   any  `json:"feature_state_value"`
	Feature int  `json:"feature"`
}

// exactQuery builds the ?q="<value>" exact-match query for identity searches.
func exactQuery(value string) string {
	return url.Values{"q": {`"` + value + `"`}}.Encode()
}

// --- Core identities (Postgres) ---

type identity struct {
	ID         int    `json:"id"`
	Identifier string `json:"identifier"`
}

// IdentityByIdentifier resolves an identifier to its numeric identity id via
// the core Admin API. found is false when no identity matches.
func IdentityByIdentifier(ctx context.Context, apiURL string, auth Auth, envKey, identifier string) (id int, found bool, err error) {
	var ids []identity
	path := fmt.Sprintf("/api/v1/environments/%s/identities/?%s", envKey, exactQuery(identifier))
	if err := getList(ctx, apiURL, path, auth, &ids); err != nil {
		return 0, false, err
	}
	for _, i := range ids {
		if i.Identifier == identifier {
			return i.ID, true, nil
		}
	}
	return 0, false, nil
}

// CreateIdentity creates a core identity and returns its id.
func CreateIdentity(ctx context.Context, apiURL string, auth Auth, envKey, identifier string) (int, error) {
	out := &identity{}
	path := fmt.Sprintf("/api/v1/environments/%s/identities/", envKey)
	if err := sendJSON(ctx, apiURL, http.MethodPost, path, auth, map[string]any{"identifier": identifier}, out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// IdentityOverride returns a core identity's override for a feature, or nil.
func IdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey string, identityID, featureID int) (*IdentityFeatureState, error) {
	var states []IdentityFeatureState
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/?feature=%d", envKey, identityID, featureID)
	if err := getList(ctx, apiURL, path, auth, &states); err != nil {
		return nil, err
	}
	if len(states) > 0 {
		return &states[0], nil
	}
	return nil, nil
}

// SetIdentityOverride creates (fsID == 0) or updates a core identity override.
// value is a native scalar (string/int/bool); the server infers its type.
func SetIdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey string, identityID, featureID, fsID int, enabled bool, value any) error {
	if fsID == 0 {
		path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/", envKey, identityID)
		body := map[string]any{"feature": featureID, "enabled": enabled, "feature_state_value": value}
		return sendJSON(ctx, apiURL, http.MethodPost, path, auth, body, nil)
	}
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/%d/", envKey, identityID, fsID)
	body := map[string]any{"enabled": enabled, "feature_state_value": value}
	return sendJSON(ctx, apiURL, http.MethodPut, path, auth, body, nil)
}

// DeleteIdentityOverride removes a core identity override by feature-state id.
func DeleteIdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey string, identityID, fsID int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/%d/", envKey, identityID, fsID)
	return sendJSON(ctx, apiURL, http.MethodDelete, path, auth, nil, nil)
}

// --- Edge identities (DynamoDB) ---

type edgeIdentity struct {
	IdentityUUID string `json:"identity_uuid"`
	Identifier   string `json:"identifier"`
}

// EdgeIdentityUUID resolves an identifier to its edge identity uuid, or found
// false when none exists yet.
func EdgeIdentityUUID(ctx context.Context, apiURL string, auth Auth, envKey, identifier string) (uuid string, found bool, err error) {
	var ids []edgeIdentity
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identities/?%s", envKey, exactQuery(identifier))
	if err := getList(ctx, apiURL, path, auth, &ids); err != nil {
		return "", false, err
	}
	for _, i := range ids {
		if i.Identifier == identifier {
			return i.IdentityUUID, true, nil
		}
	}
	return "", false, nil
}

// EdgeIdentityOverride returns an edge identity's override for a feature, or
// nil. It is keyed by the identity uuid (the identifier endpoint has no GET).
func EdgeIdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey, identityUUID string, featureID int) (*IdentityFeatureState, error) {
	var states []IdentityFeatureState
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identities/%s/edge-featurestates/?feature=%d", envKey, identityUUID, featureID)
	if err := getList(ctx, apiURL, path, auth, &states); err != nil {
		return nil, err
	}
	if len(states) > 0 {
		return &states[0], nil
	}
	return nil, nil
}

// edgeIdentityFeatureStatesPath is the identifier-based edge endpoint. The
// backend nests it under a second literal "environments/" segment and omits
// the trailing slash — replicated verbatim here.
func edgeIdentityFeatureStatesPath(envKey string) string {
	return "/api/v1/environments/environments/" + envKey + "/edge-identities-featurestates"
}

// SetEdgeIdentityOverride creates-or-updates an edge identity override in one
// call (the identity is created if it does not exist). value is a native scalar.
func SetEdgeIdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey, identifier string, featureID int, enabled bool, value any) error {
	body := map[string]any{"identifier": identifier, "feature": featureID, "enabled": enabled, "feature_state_value": value}
	return sendJSON(ctx, apiURL, http.MethodPut, edgeIdentityFeatureStatesPath(envKey), auth, body, nil)
}

// DeleteEdgeIdentityOverride removes an edge identity override.
func DeleteEdgeIdentityOverride(ctx context.Context, apiURL string, auth Auth, envKey, identifier string, featureID int) error {
	body := map[string]any{"identifier": identifier, "feature": featureID}
	return sendJSON(ctx, apiURL, http.MethodDelete, edgeIdentityFeatureStatesPath(envKey), auth, body, nil)
}
