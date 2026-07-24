package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Flagsmith/flagsmith-cli/internal/bug"

	"github.com/Flagsmith/flagsmith-cli/internal/httpx"
	"github.com/Flagsmith/flagsmith-cli/internal/version"
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

// Client is the Flagsmith Admin API client. It owns the HTTP client, base URL,
// auth scheme, and User-Agent, so callers issue requests without repeating any
// of them on every call.
type Client struct {
	httpClient *http.Client
	baseURL    string
	auth       Auth
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects the underlying *http.Client (transport, timeouts,
// retries). Defaults to httpx.New with the client's User-Agent.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithUserAgent sets the User-Agent sent on every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// NewClient builds a Client for one instance base URL and auth scheme. The base
// URL's trailing slash is trimmed once here so paths join cleanly.
func NewClient(baseURL string, auth Auth, opts ...Option) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		auth:      auth,
		userAgent: version.UserAgent(),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = httpx.New(c.userAgent)
	}
	return c
}

// newRequest builds a request against the client's base URL with the User-Agent
// and auth applied. body may be nil.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	c.auth.Apply(req)
	return req, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError(http.MethodGet, req.URL.String(), resp)
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

func (c *Client) UsersMe(ctx context.Context) (*User, error) {
	user := &User{}
	if err := c.get(ctx, "/api/v1/auth/users/me/", user); err != nil {
		return nil, err
	}
	return user, nil
}

// rawItem holds the raw API JSON for a resource so JSON output can mirror the
// server's full field set rather than a curated subset. Embedders capture it in
// UnmarshalJSON and return it from MarshalJSON.
type rawItem struct {
	raw json.RawMessage
}

// Organisation carries the id/name the CLI needs plus the raw API item, so
// JSON output mirrors the server's full field set.
type Organisation struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	rawItem
}

func (o Organisation) MarshalJSON() ([]byte, error) {
	if len(o.raw) > 0 {
		return o.raw, nil
	}
	type alias Organisation
	return json.Marshal(alias(o))
}

func (o *Organisation) UnmarshalJSON(b []byte) error {
	type alias Organisation
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*o = Organisation(a)
	o.raw = append([]byte(nil), b...)
	return nil
}

func (c *Client) Organisations(ctx context.Context) ([]Organisation, error) {
	var orgs []Organisation
	if err := c.getList(ctx, "/api/v1/organisations/", &orgs); err != nil {
		return nil, err
	}
	return orgs, nil
}

// GetOrganisation fetches one organisation.
func (c *Client) GetOrganisation(ctx context.Context, orgID int) (*Organisation, error) {
	o := &Organisation{}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/organisations/%d/", orgID), o); err != nil {
		return nil, err
	}
	return o, nil
}

// CreateOrganisation creates an organisation from a flat field body.
func (c *Client) CreateOrganisation(ctx context.Context, body map[string]any) (*Organisation, error) {
	o := &Organisation{}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/organisations/", body, o); err != nil {
		return nil, err
	}
	return o, nil
}

// UpdateOrganisation patches an organisation's fields.
func (c *Client) UpdateOrganisation(ctx context.Context, orgID int, body map[string]any) (*Organisation, error) {
	o := &Organisation{}
	path := fmt.Sprintf("/api/v1/organisations/%d/", orgID)
	if err := c.sendJSON(ctx, http.MethodPatch, path, body, o); err != nil {
		return nil, err
	}
	return o, nil
}

// DeleteOrganisation removes an organisation.
func (c *Client) DeleteOrganisation(ctx context.Context, orgID int) error {
	return c.sendJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/organisations/%d/", orgID), nil, nil)
}

// getList decodes a list endpoint that may respond paginated
// ({count, next, results}) or as a bare array. Paginated responses are
// followed across every page via the DRF "next" link, so callers always see
// the full result set regardless of page size. The "next" URL's scheme and
// host are discarded and only its path + query are reused against the base URL,
// which keeps pagination working behind proxies that rewrite the host.
func (c *Client) getList(ctx context.Context, path string, out any) error {
	var items []json.RawMessage
	for path != "" {
		var raw json.RawMessage
		if err := c.get(ctx, path, &raw); err != nil {
			return err
		}
		trimmed := bytes.TrimLeft(raw, " \t\r\n")
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// Bare array: not paginated, decode directly.
			return json.Unmarshal(raw, out)
		}
		var page struct {
			Next    string            `json:"next"`
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		items = append(items, page.Results...)

		if page.Next == "" {
			break
		}
		next, err := url.Parse(page.Next)
		if err != nil {
			return bug.Mark(fmt.Errorf("parsing pagination next link %q: %w", page.Next, err))
		}
		path = next.Path
		if next.RawQuery != "" {
			path += "?" + next.RawQuery
		}
	}
	combined, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(combined, out)
}

// responseError builds an error from a non-2xx response. It surfaces the API's
// own message (DRF returns {"detail": "..."} or {"field": ["..."]}) and
// classifies plan limits as ErrPlanGated (upgrade) or ErrQuotaExceeded (raise on
// request). Flagsmith ships no machine-readable error code, so limits can only be
// told apart from RBAC denials by their message text — see classifyLimit. As a
// result, 403 caps (project count, experiment tier) are wire-identical to
// permission denials and intentionally not classified here.
func responseError(method, u string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := apiMessage(body)
	if e := classifyLimit(msg); e != nil {
		return e
	}
	return bug.Mark(&statusError{code: resp.StatusCode, status: resp.Status, message: msg, method: method, url: u})
}

// statusError is a non-2xx response the CLI didn't classify as a plan limit. It
// carries the HTTP status so callers can special-case it (e.g. a 403 that is
// really a project cap). bug.Mark wraps it, so it still reads as unexpected.
type statusError struct {
	code    int
	status  string // e.g. "403 Forbidden"
	message string // the API's detail, if any
	method  string
	url     string
}

func (e *statusError) Error() string {
	if e.message != "" {
		return fmt.Sprintf("%s %s returned %s: %s", e.method, e.url, e.status, e.message)
	}
	return fmt.Sprintf("%s %s returned %s", e.method, e.url, e.status)
}

// statusOf returns the HTTP status carried by an API error, or 0 if it has none.
func statusOf(err error) int {
	var e *statusError
	if errors.As(err, &e) {
		return e.code
	}
	return 0
}

// apiMessage extracts a human-readable message from a DRF error body, or "".
func apiMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return ""
	}
	var detail struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &detail) == nil && detail.Detail != "" {
		return detail.Detail
	}
	// Field-keyed validation errors: {"field": ["msg"]} or {"field": "msg"}.
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return ""
	}
	var msgs []string
	for _, raw := range fields {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			msgs = append(msgs, s)
			continue
		}
		var arr []string
		if json.Unmarshal(raw, &arr) == nil {
			msgs = append(msgs, arr...)
		}
	}
	sort.Strings(msgs) // map order is random; keep the message stable
	return strings.Join(msgs, "; ")
}

// quotaPhrases mark a resource cap that can be raised on request → ErrQuotaExceeded.
// upgradePhrases mark a self-serve plan limit → ErrPlanGated. Matched case-
// insensitively against the API's detail message — the only signal on the wire.
// Both derive from the backend's exception detail strings.
var (
	quotaPhrases   = []string{"maximum allowed"}                                           // feature / segment / segment-override caps
	upgradePhrases = []string{"upgrade your plan", "payment issue", "has no subscription"} // seats / payment / no subscription
)

// classifyLimit returns a plan-limit error for a recognised message, or nil.
// Quota caps are checked first; they are the enterprise-negotiable ones.
func classifyLimit(msg string) error {
	m := strings.ToLower(msg)
	for _, p := range quotaPhrases {
		if strings.Contains(m, p) {
			return &planLimited{msg: msg, target: ErrQuotaExceeded}
		}
	}
	for _, p := range upgradePhrases {
		if strings.Contains(m, p) {
			return &planLimited{msg: msg, target: ErrPlanGated}
		}
	}
	return nil
}

// planLimited carries the API's own reason and matches (via errors.Is) exactly
// one sentinel — ErrQuotaExceeded or ErrPlanGated — so the CLI can pick the right
// recovery hint while still showing the specific limit.
type planLimited struct {
	msg    string
	target error
}

func (e *planLimited) Error() string        { return e.msg }
func (e *planLimited) Is(target error) bool { return target == e.target }

// sendJSON issues a request with an optional JSON body and decodes an optional
// JSON response. It treats any non-2xx status as an error.
func (c *Client) sendJSON(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(method, req.URL.String(), resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Project carries the fields the CLI needs plus the raw API item, so JSON
// output mirrors the server's full field set.
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Organisation      int    `json:"organisation"`
	UseEdgeIdentities bool   `json:"use_edge_identities"`
	rawItem
}

func (p *Project) UnmarshalJSON(b []byte) error {
	type alias Project
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Project(a)
	p.raw = append([]byte(nil), b...)
	return nil
}

func (p Project) MarshalJSON() ([]byte, error) {
	if len(p.raw) > 0 {
		return p.raw, nil
	}
	type alias Project
	return json.Marshal(alias(p))
}

// GetProject fetches a single project — notably its use_edge_identities flag,
// which decides whether identity overrides use the core or edge endpoints.
func (c *Client) GetProject(ctx context.Context, projectID int) (*Project, error) {
	p := &Project{}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/projects/%d/", projectID), p); err != nil {
		return nil, err
	}
	return p, nil
}

// Projects lists an organisation's projects. organisationID 0 lists all
// accessible projects (the endpoint's organisation filter is optional).
func (c *Client) Projects(ctx context.Context, organisationID int) ([]Project, error) {
	var projects []Project
	path := "/api/v1/projects/"
	if organisationID != 0 {
		path += fmt.Sprintf("?organisation=%d", organisationID)
	}
	if err := c.getList(ctx, path, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

// SubscriptionMetadata is an organisation's plan limits. A nil field means the
// plan sets no limit for it.
type SubscriptionMetadata struct {
	MaxSeats    *int `json:"max_seats"`
	MaxAPICalls *int `json:"max_api_calls"`
	MaxProjects *int `json:"max_projects"`
}

// GetSubscriptionMetadata returns the organisation's plan limits.
func (c *Client) GetSubscriptionMetadata(ctx context.Context, orgID int) (*SubscriptionMetadata, error) {
	m := &SubscriptionMetadata{}
	path := fmt.Sprintf("/api/v1/organisations/%d/get-subscription-metadata/", orgID)
	if err := c.get(ctx, path, m); err != nil {
		return nil, err
	}
	return m, nil
}

// CreateProject creates a project from a flat field body (name + organisation
// required).
//
// A project-cap denial arrives as a bare 403, identical to an RBAC "you
// lack CREATE_PROJECT" denial. So on a 403 we check whether the org is actually
// at its plan's project limit and, only then, surface it as ErrPlanGated.
func (c *Client) CreateProject(ctx context.Context, body map[string]any) (*Project, error) {
	p := &Project{}
	err := c.sendJSON(ctx, http.MethodPost, "/api/v1/projects/", body, p)
	if err == nil {
		return p, nil
	}
	if statusOf(err) == http.StatusForbidden {
		if orgID, ok := body["organisation"].(int); ok {
			if limit, count, reached := c.projectCapReached(ctx, orgID); reached {
				return nil, &planLimited{
					msg:    fmt.Sprintf("this organisation's plan allows %d project(s), and it already has %d", limit, count),
					target: ErrPlanGated,
				}
			}
		}
	}
	return nil, err
}

// projectCapReached reports whether orgID is at its plan's project limit. It
// fails safe: if the limit or the count can't be read, it returns reached=false,
// so CreateProject keeps the original error rather than guess a plan limit.
func (c *Client) projectCapReached(ctx context.Context, orgID int) (limit, count int, reached bool) {
	meta, err := c.GetSubscriptionMetadata(ctx, orgID)
	if err != nil || meta.MaxProjects == nil {
		return 0, 0, false
	}
	projects, err := c.Projects(ctx, orgID)
	if err != nil {
		return 0, 0, false
	}
	return *meta.MaxProjects, len(projects), len(projects) >= *meta.MaxProjects
}

// UpdateProject patches a project's fields (organisation is immutable and
// ignored if sent).
func (c *Client) UpdateProject(ctx context.Context, projectID int, body map[string]any) (*Project, error) {
	p := &Project{}
	path := fmt.Sprintf("/api/v1/projects/%d/", projectID)
	if err := c.sendJSON(ctx, http.MethodPatch, path, body, p); err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProject removes a project.
func (c *Client) DeleteProject(ctx context.Context, projectID int) error {
	return c.sendJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d/", projectID), nil, nil)
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

	// Project-level definition fields (feature CRUD).
	InitialValue        *string              `json:"initial_value"`
	DefaultEnabled      bool                 `json:"default_enabled"`
	IsArchived          bool                 `json:"is_archived"`
	MultivariateOptions []MultivariateOption `json:"multivariate_options"`
}

// MultivariateOption is a variant of a multivariate feature. The value is a
// typed struct (type is "unicode"/"int"/"bool"); default_percentage_allocation
// is the variant's weight.
type MultivariateOption struct {
	ID                          int      `json:"id,omitempty"`
	Type                        string   `json:"type,omitempty"`
	StringValue                 *string  `json:"string_value,omitempty"`
	IntegerValue                *int     `json:"integer_value,omitempty"`
	BooleanValue                *bool    `json:"boolean_value,omitempty"`
	DefaultPercentageAllocation *float64 `json:"default_percentage_allocation,omitempty"`
	Key                         string   `json:"key,omitempty"`
	Feature                     int      `json:"feature,omitempty"`
}

// ProjectFeatures lists a project's features (no environment context).
// includeArchived controls whether archived features are returned.
func (c *Client) ProjectFeatures(ctx context.Context, projectID int, includeArchived bool) ([]Feature, error) {
	var features []Feature
	path := fmt.Sprintf("/api/v1/projects/%d/features/", projectID)
	if !includeArchived {
		path += "?is_archived=false"
	}
	if err := c.getList(ctx, path, &features); err != nil {
		return nil, err
	}
	return features, nil
}

// GetFeature fetches one project feature (with its multivariate options).
func (c *Client) GetFeature(ctx context.Context, projectID, featureID int) (*Feature, error) {
	f := &Feature{}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/projects/%d/features/%d/", projectID, featureID), f); err != nil {
		return nil, err
	}
	return f, nil
}

// FeatureWrite is the create/update body. Pointer fields distinguish "unset"
// from a zero value; the API ignores fields that are read-only for the action.
type FeatureWrite struct {
	Name                string               `json:"name,omitempty"`
	Description         *string              `json:"description,omitempty"`
	InitialValue        *string              `json:"initial_value,omitempty"`
	DefaultEnabled      *bool                `json:"default_enabled,omitempty"`
	IsArchived          *bool                `json:"is_archived,omitempty"`
	MultivariateOptions []MultivariateOption `json:"multivariate_options,omitempty"`
}

// CreateFeature creates a project feature (project taken from the URL).
func (c *Client) CreateFeature(ctx context.Context, projectID int, in FeatureWrite) (*Feature, error) {
	out := &Feature{}
	path := fmt.Sprintf("/api/v1/projects/%d/features/", projectID)
	if err := c.sendJSON(ctx, http.MethodPost, path, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateFeature patches the mutable fields of a feature (name, initial value,
// and default-enabled are read-only server-side and ignored if sent).
func (c *Client) UpdateFeature(ctx context.Context, projectID, featureID int, in FeatureWrite) (*Feature, error) {
	out := &Feature{}
	path := fmt.Sprintf("/api/v1/projects/%d/features/%d/", projectID, featureID)
	if err := c.sendJSON(ctx, http.MethodPatch, path, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteFeature removes a feature.
func (c *Client) DeleteFeature(ctx context.Context, projectID, featureID int) error {
	path := fmt.Sprintf("/api/v1/projects/%d/features/%d/", projectID, featureID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}

func mvOptionsPath(projectID, featureID int) string {
	return fmt.Sprintf("/api/v1/projects/%d/features/%d/mv-options/", projectID, featureID)
}

// CreateMVOption adds a multivariate option (variant) to a feature.
func (c *Client) CreateMVOption(ctx context.Context, projectID, featureID int, in MultivariateOption) (*MultivariateOption, error) {
	out := &MultivariateOption{}
	if err := c.sendJSON(ctx, http.MethodPost, mvOptionsPath(projectID, featureID), in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateMVOption patches a multivariate option in place (preserving the id, so
// per-environment weight overrides survive).
func (c *Client) UpdateMVOption(ctx context.Context, projectID, featureID, optionID int, in MultivariateOption) (*MultivariateOption, error) {
	out := &MultivariateOption{}
	in.Feature = featureID // even a partial update must carry feature: the serializer reads it in validate() (else 500)
	path := fmt.Sprintf("%s%d/", mvOptionsPath(projectID, featureID), optionID)
	if err := c.sendJSON(ctx, http.MethodPatch, path, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteMVOption removes a multivariate option.
func (c *Client) DeleteMVOption(ctx context.Context, projectID, featureID, optionID int) error {
	path := fmt.Sprintf("%s%d/", mvOptionsPath(projectID, featureID), optionID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
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
func (c *Client) Features(ctx context.Context, projectID, environmentID, segmentID int) ([]Feature, error) {
	var features []Feature
	path := fmt.Sprintf("/api/v1/projects/%d/features/", projectID)
	sep := "?"
	if environmentID != 0 {
		path += fmt.Sprintf("%senvironment=%d", sep, environmentID)
		sep = "&"
	}
	if segmentID != 0 {
		path += fmt.Sprintf("%ssegment=%d", sep, segmentID)
	}
	if err := c.getList(ctx, path, &features); err != nil {
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
func (c *Client) DeleteSegmentOverride(ctx context.Context, environmentKey, featureName string, segmentID int) error {
	body, err := json.Marshal(map[string]any{
		"feature": FeatureRef{Name: featureName},
		"segment": map[string]int{"id": segmentID},
	})
	if err != nil {
		return err
	}
	path := "/api/experiments/environments/" + environmentKey + "/delete-segment-override/"
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
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
		return responseError(http.MethodPost, req.URL.String(), resp)
	}
	return nil
}

// ErrWorkflowGated is returned when update-flag-v2 refuses because the
// environment has change-request workflows enabled.
var ErrWorkflowGated = fmt.Errorf("this environment uses change-request workflows; direct updates are disabled")

// ErrPlanGated matches (via errors.Is) a self-serve plan limit a user lifts by
// upgrading — seats, a payment problem, or no subscription. The CLI hints at
// pricing. The error's own message is the API's specific reason.
var ErrPlanGated = errors.New("not available on your organisation's current plan")

// ErrQuotaExceeded matches (via errors.Is) a resource cap that is raised on
// request rather than by self-serve upgrade — the feature, segment, and
// segment-override limits, which enterprise plans can relax. The CLI hints at
// support, not pricing. Distinguishing the two is by message text only (see
// classifyLimit); Flagsmith ships no machine-readable error code.
var ErrQuotaExceeded = errors.New("resource limit reached on your organisation's current plan")

// UpdateFlag applies an environment-default change via the experimental
// update-flag-v2 endpoint, keyed by the environment's client-side key. The
// endpoint returns 204 No Content on success.
func (c *Client) UpdateFlag(ctx context.Context, environmentKey string, in UpdateFlagRequest) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	path := "/api/experiments/environments/" + environmentKey + "/update-flag-v2/"
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return ErrWorkflowGated
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return responseError(http.MethodPost, req.URL.String(), resp)
	}
	return nil
}

// Environment carries the fields the CLI needs plus the raw API item, so JSON
// output mirrors the server's full field set. Identified by APIKey, not id.
type Environment struct {
	ID                     int    `json:"id"`
	Name                   string `json:"name"`
	APIKey                 string `json:"api_key"`
	Project                int    `json:"project"`
	Description            string `json:"description"`
	UseV2FeatureVersioning bool   `json:"use_v2_feature_versioning"`
	rawItem
}

func (e *Environment) UnmarshalJSON(b []byte) error {
	type alias Environment
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = Environment(a)
	e.raw = append([]byte(nil), b...)
	return nil
}

func (e Environment) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return e.raw, nil
	}
	type alias Environment
	return json.Marshal(alias(e))
}

func (c *Client) Environments(ctx context.Context, projectID int) ([]Environment, error) {
	var envs []Environment
	path := fmt.Sprintf("/api/v1/environments/?project=%d", projectID)
	if err := c.getList(ctx, path, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}

// GetEnvironment fetches one environment by its client-side api_key.
func (c *Client) GetEnvironment(ctx context.Context, apiKey string) (*Environment, error) {
	e := &Environment{}
	if err := c.get(ctx, "/api/v1/environments/"+apiKey+"/", e); err != nil {
		return nil, err
	}
	return e, nil
}

// CreateEnvironment creates an environment from a flat field body (name +
// project required). The client-side api_key is minted server-side.
func (c *Client) CreateEnvironment(ctx context.Context, body map[string]any) (*Environment, error) {
	e := &Environment{}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/environments/", body, e); err != nil {
		return nil, err
	}
	return e, nil
}

// UpdateEnvironment patches an environment (project is immutable and ignored).
func (c *Client) UpdateEnvironment(ctx context.Context, apiKey string, body map[string]any) (*Environment, error) {
	e := &Environment{}
	if err := c.sendJSON(ctx, http.MethodPatch, "/api/v1/environments/"+apiKey+"/", body, e); err != nil {
		return nil, err
	}
	return e, nil
}

// DeleteEnvironment removes an environment by api_key.
func (c *Client) DeleteEnvironment(ctx context.Context, apiKey string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/environments/"+apiKey+"/", nil, nil)
}

// CloneEnvironment clones an environment into a new one named by body["name"].
func (c *Client) CloneEnvironment(ctx context.Context, apiKey string, body map[string]any) (*Environment, error) {
	e := &Environment{}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/environments/"+apiKey+"/clone/", body, e); err != nil {
		return nil, err
	}
	return e, nil
}

// EnvironmentDocument fetches the environment document (the offline-evaluation
// payload) as raw JSON, via the admin document action.
func (c *Client) EnvironmentDocument(ctx context.Context, apiKey string) (json.RawMessage, error) {
	var doc json.RawMessage
	if err := c.get(ctx, "/api/v1/environments/"+apiKey+"/document/", &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// EnvironmentAPIKey is a server-side (ser.) SDK key for an environment. Key is
// returned in full only on create.
type EnvironmentAPIKey struct {
	ID        int     `json:"id"`
	Key       string  `json:"key,omitempty"`
	Name      string  `json:"name"`
	Active    bool    `json:"active"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at"`
}

// EnvironmentAPIKeys lists an environment's server-side keys.
func (c *Client) EnvironmentAPIKeys(ctx context.Context, envKey string) ([]EnvironmentAPIKey, error) {
	var keys []EnvironmentAPIKey
	if err := c.getList(ctx, "/api/v1/environments/"+envKey+"/api-keys/", &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// CreateEnvironmentAPIKey mints a server-side key; the response includes the
// full key value (shown once).
func (c *Client) CreateEnvironmentAPIKey(ctx context.Context, envKey string, body map[string]any) (*EnvironmentAPIKey, error) {
	k := &EnvironmentAPIKey{}
	if err := c.sendJSON(ctx, http.MethodPost, "/api/v1/environments/"+envKey+"/api-keys/", body, k); err != nil {
		return nil, err
	}
	return k, nil
}

// DeleteEnvironmentAPIKey removes a server-side key by id.
func (c *Client) DeleteEnvironmentAPIKey(ctx context.Context, envKey string, keyID int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/api-keys/%d/", envKey, keyID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
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
func (c *Client) IdentityByIdentifier(ctx context.Context, envKey, identifier string) (id int, found bool, err error) {
	var ids []identity
	path := fmt.Sprintf("/api/v1/environments/%s/identities/?%s", envKey, exactQuery(identifier))
	if err := c.getList(ctx, path, &ids); err != nil {
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
func (c *Client) CreateIdentity(ctx context.Context, envKey, identifier string) (int, error) {
	out := &identity{}
	path := fmt.Sprintf("/api/v1/environments/%s/identities/", envKey)
	if err := c.sendJSON(ctx, http.MethodPost, path, map[string]any{"identifier": identifier}, out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// IdentityOverride returns a core identity's override for a feature, or nil.
func (c *Client) IdentityOverride(ctx context.Context, envKey string, identityID, featureID int) (*IdentityFeatureState, error) {
	var states []IdentityFeatureState
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/?feature=%d", envKey, identityID, featureID)
	if err := c.getList(ctx, path, &states); err != nil {
		return nil, err
	}
	if len(states) > 0 {
		return &states[0], nil
	}
	return nil, nil
}

// SetIdentityOverride creates (fsID == 0) or updates a core identity override.
// value is a native scalar (string/int/bool); the server infers its type.
func (c *Client) SetIdentityOverride(ctx context.Context, envKey string, identityID, featureID, fsID int, enabled bool, value any) error {
	if fsID == 0 {
		path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/", envKey, identityID)
		body := map[string]any{"feature": featureID, "enabled": enabled, "feature_state_value": value}
		return c.sendJSON(ctx, http.MethodPost, path, body, nil)
	}
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/%d/", envKey, identityID, fsID)
	body := map[string]any{"enabled": enabled, "feature_state_value": value}
	return c.sendJSON(ctx, http.MethodPut, path, body, nil)
}

// DeleteIdentityOverride removes a core identity override by feature-state id.
func (c *Client) DeleteIdentityOverride(ctx context.Context, envKey string, identityID, fsID int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/%d/", envKey, identityID, fsID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}

// --- Edge identities (DynamoDB) ---

type edgeIdentity struct {
	IdentityUUID string `json:"identity_uuid"`
	Identifier   string `json:"identifier"`
}

// EdgeIdentityUUID resolves an identifier to its edge identity uuid, or found
// false when none exists yet.
func (c *Client) EdgeIdentityUUID(ctx context.Context, envKey, identifier string) (uuid string, found bool, err error) {
	var ids []edgeIdentity
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identities/?%s", envKey, exactQuery(identifier))
	if err := c.getList(ctx, path, &ids); err != nil {
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
func (c *Client) EdgeIdentityOverride(ctx context.Context, envKey, identityUUID string, featureID int) (*IdentityFeatureState, error) {
	var states []IdentityFeatureState
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identities/%s/edge-featurestates/?feature=%d", envKey, identityUUID, featureID)
	if err := c.getList(ctx, path, &states); err != nil {
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
func (c *Client) SetEdgeIdentityOverride(ctx context.Context, envKey, identifier string, featureID int, enabled bool, value any) error {
	body := map[string]any{"identifier": identifier, "feature": featureID, "enabled": enabled, "feature_state_value": value}
	return c.sendJSON(ctx, http.MethodPut, edgeIdentityFeatureStatesPath(envKey), body, nil)
}

// DeleteEdgeIdentityOverride removes an edge identity override.
func (c *Client) DeleteEdgeIdentityOverride(ctx context.Context, envKey, identifier string, featureID int) error {
	body := map[string]any{"identifier": identifier, "feature": featureID}
	return c.sendJSON(ctx, http.MethodDelete, edgeIdentityFeatureStatesPath(envKey), body, nil)
}

// SegmentCondition is one condition in a segment rule. On the wire `value` is
// a plain string (or null); the CLI maps IN arrays to/from a JSON-array string.
type SegmentCondition struct {
	Property string `json:"property,omitempty"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

// SegmentRule is a node in a segment's rule tree (ALL/ANY/NONE over conditions
// and sub-rules).
type SegmentRule struct {
	Type       string             `json:"type"`
	Conditions []SegmentCondition `json:"conditions,omitempty"`
	Rules      []SegmentRule      `json:"rules,omitempty"`
}

// Segment is a project segment. Feature is set for feature-specific segments.
type Segment struct {
	ID          int           `json:"id,omitempty"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Project     int           `json:"project,omitempty"`
	Feature     *int          `json:"feature,omitempty"`
	Rules       []SegmentRule `json:"rules"`
}

// Segments lists a project's segments. include controls whether
// feature-specific segments are returned.
func (c *Client) Segments(ctx context.Context, projectID int, include bool) ([]Segment, error) {
	var segs []Segment
	path := fmt.Sprintf("/api/v1/projects/%d/segments/?include_feature_specific=%t", projectID, include)
	if err := c.getList(ctx, path, &segs); err != nil {
		return nil, err
	}
	return segs, nil
}

// GetSegment fetches one segment with its full rule tree.
func (c *Client) GetSegment(ctx context.Context, projectID, segmentID int) (*Segment, error) {
	s := &Segment{}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID), s); err != nil {
		return nil, err
	}
	return s, nil
}

// CreateSegment creates a segment (project taken from the URL).
func (c *Client) CreateSegment(ctx context.Context, projectID int, in Segment) (*Segment, error) {
	out := &Segment{}
	in.Project = projectID // the serializer requires project in the body, not just the URL
	path := fmt.Sprintf("/api/v1/projects/%d/segments/", projectID)
	if err := c.sendJSON(ctx, http.MethodPost, path, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSegment replaces a segment's rule tree and fields (PUT).
func (c *Client) UpdateSegment(ctx context.Context, projectID, segmentID int, in Segment) (*Segment, error) {
	out := &Segment{}
	in.Project = projectID // the serializer requires project in the body, not just the URL
	path := fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID)
	if err := c.sendJSON(ctx, http.MethodPut, path, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteSegment removes a segment.
func (c *Client) DeleteSegment(ctx context.Context, projectID, segmentID int) error {
	path := fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}
