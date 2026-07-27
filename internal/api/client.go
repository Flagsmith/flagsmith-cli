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
	"strconv"
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

// The typed CRUD helpers live at package level because Go methods cannot
// take type parameters. Every resource method is one of these three shapes.

// getOne fetches a single resource.
func getOne[T any](ctx context.Context, c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.get(ctx, path, out); err != nil {
		return nil, err
	}
	return out, nil
}

// getMany fetches a list resource, following pagination.
func getMany[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var out []T
	if err := c.getList(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// send issues a mutating request and decodes the response.
func send[T any](ctx context.Context, c *Client, method, path string, body any) (*T, error) {
	out := new(T)
	if err := c.sendJSON(ctx, method, path, body, out); err != nil {
		return nil, err
	}
	return out, nil
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
	return getOne[User](ctx, c, "/api/v1/auth/users/me/")
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
	return getMany[Organisation](ctx, c, "/api/v1/organisations/")
}

// GetOrganisation fetches one organisation.
func (c *Client) GetOrganisation(ctx context.Context, orgID int) (*Organisation, error) {
	return getOne[Organisation](ctx, c, fmt.Sprintf("/api/v1/organisations/%d/", orgID))
}

// CreateOrganisation creates an organisation from a flat field body.
func (c *Client) CreateOrganisation(ctx context.Context, body map[string]any) (*Organisation, error) {
	return send[Organisation](ctx, c, http.MethodPost, "/api/v1/organisations/", body)
}

// UpdateOrganisation patches an organisation's fields.
func (c *Client) UpdateOrganisation(ctx context.Context, orgID int, body map[string]any) (*Organisation, error) {
	return send[Organisation](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/organisations/%d/", orgID), body)
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
	path = withPageSize(path)
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

// withPageSize asks a list endpoint for its largest page: the backend's
// CustomPagination caps page_size at 999 and clamps larger asks (the edge
// endpoints clamp at 100), so one round-trip replaces a sequential walk at
// the server's default page size. Endpoints that don't paginate ignore the
// parameter. getList's next-link loop stays as the safety net either way.
func withPageSize(path string) string {
	if strings.Contains(path, "page_size=") {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "page_size=999"
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
	return getOne[Project](ctx, c, fmt.Sprintf("/api/v1/projects/%d/", projectID))
}

// Projects lists an organisation's projects. organisationID 0 lists all
// accessible projects (the endpoint's organisation filter is optional).
func (c *Client) Projects(ctx context.Context, organisationID int) ([]Project, error) {
	path := "/api/v1/projects/"
	if organisationID != 0 {
		path += fmt.Sprintf("?organisation=%d", organisationID)
	}
	return getMany[Project](ctx, c, path)
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
	return getOne[SubscriptionMetadata](ctx, c, fmt.Sprintf("/api/v1/organisations/%d/get-subscription-metadata/", orgID))
}

// CreateProject creates a project from a flat field body (name + organisation
// required).
//
// A project-cap denial arrives as a bare 403, identical to an RBAC "you
// lack CREATE_PROJECT" denial. So on a 403 we check whether the org is actually
// at its plan's project limit and, only then, surface it as ErrPlanGated.
func (c *Client) CreateProject(ctx context.Context, body map[string]any) (*Project, error) {
	p, err := send[Project](ctx, c, http.MethodPost, "/api/v1/projects/", body)
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
	return send[Project](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d/", projectID), body)
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
	path := fmt.Sprintf("/api/v1/projects/%d/features/", projectID)
	if !includeArchived {
		path += "?is_archived=false"
	}
	return getMany[Feature](ctx, c, path)
}

// GetFeature fetches one project feature (with its multivariate options).
func (c *Client) GetFeature(ctx context.Context, projectID, featureID int) (*Feature, error) {
	return getOne[Feature](ctx, c, fmt.Sprintf("/api/v1/projects/%d/features/%d/", projectID, featureID))
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
	return send[Feature](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/features/", projectID), in)
}

// UpdateFeature patches the mutable fields of a feature (name, initial value,
// and default-enabled are read-only server-side and ignored if sent).
func (c *Client) UpdateFeature(ctx context.Context, projectID, featureID int, in FeatureWrite) (*Feature, error) {
	return send[Feature](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d/features/%d/", projectID, featureID), in)
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
	return send[MultivariateOption](ctx, c, http.MethodPost, mvOptionsPath(projectID, featureID), in)
}

// UpdateMVOption patches a multivariate option in place (preserving the id, so
// per-environment weight overrides survive).
func (c *Client) UpdateMVOption(ctx context.Context, projectID, featureID, optionID int, in MultivariateOption) (*MultivariateOption, error) {
	in.Feature = featureID // even a partial update must carry feature: the serializer reads it in validate() (else 500)
	path := fmt.Sprintf("%s%d/", mvOptionsPath(projectID, featureID), optionID)
	return send[MultivariateOption](ctx, c, http.MethodPatch, path, in)
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
// for that segment. A non-empty search narrows the list server-side — a
// contains match on the name, so callers still pick their exact match — which
// keeps single-feature commands off the full (expensive) project list.
func (c *Client) Features(ctx context.Context, projectID, environmentID, segmentID int, search string) ([]Feature, error) {
	q := url.Values{}
	if environmentID != 0 {
		q.Set("environment", strconv.Itoa(environmentID))
	}
	if segmentID != 0 {
		q.Set("segment", strconv.Itoa(segmentID))
	}
	if search != "" {
		q.Set("search", search)
	}
	path := fmt.Sprintf("/api/v1/projects/%d/features/", projectID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return getMany[Feature](ctx, c, path)
}

// FeatureSegment links a feature to one segment override in an environment:
// the override's priority plus the segment's id and name. The endpoint returns
// rows in priority order and reflects the live version transparently.
type FeatureSegment struct {
	ID          int    `json:"id"`
	Segment     int    `json:"segment"`
	SegmentName string `json:"segment_name"`
	Priority    int    `json:"priority"`
}

// FeatureSegments lists a feature's segment overrides in one environment, in
// priority order. Both filters are required by the endpoint.
func (c *Client) FeatureSegments(ctx context.Context, environmentID, featureID int) ([]FeatureSegment, error) {
	return getMany[FeatureSegment](ctx, c, fmt.Sprintf("/api/v1/features/feature-segments/?environment=%d&feature=%d", environmentID, featureID))
}

// TypedValue is the nested feature_state_value wire form the admin
// featurestates endpoints return: {type, string_value, integer_value,
// boolean_value} with type one of "unicode", "int", "bool".
type TypedValue struct {
	Type         string  `json:"type"`
	StringValue  *string `json:"string_value"`
	IntegerValue *int    `json:"integer_value"`
	BooleanValue *bool   `json:"boolean_value"`
}

// Scalar converts the typed wire form to the bare scalar the curated views show.
func (v TypedValue) Scalar() any {
	switch v.Type {
	case "int":
		if v.IntegerValue != nil {
			return *v.IntegerValue
		}
	case "bool":
		if v.BooleanValue != nil {
			return *v.BooleanValue
		}
	default: // "unicode"
		if v.StringValue != nil {
			return *v.StringValue
		}
	}
	return nil
}

// EnvironmentFeatureState is one row of the admin featurestates list: a
// feature's state for the environment default (feature_segment null), one
// segment override, or (in v2-versioned environments) an identity override.
type EnvironmentFeatureState struct {
	ID             int        `json:"id"`
	Enabled        bool       `json:"enabled"`
	FeatureSegment *int       `json:"feature_segment"`
	Identity       *int       `json:"identity"`
	Value          TypedValue `json:"feature_state_value"`
}

// FeatureStates lists a feature's live states in one environment. Callers join
// segment overrides onto FeatureSegments rows via FeatureSegment.
func (c *Client) FeatureStates(ctx context.Context, environmentID, featureID int) ([]EnvironmentFeatureState, error) {
	return getMany[EnvironmentFeatureState](ctx, c, fmt.Sprintf("/api/v1/features/featurestates/?environment=%d&feature=%d", environmentID, featureID))
}

// IdentityOverrideRow is one identity's override of a feature, as listed by
// the core or edge override endpoints. Value is a bare scalar.
type IdentityOverrideRow struct {
	Identifier string
	Enabled    bool
	Value      any
}

// CoreIdentityOverrides lists every core (Postgres) identity override for a
// feature, via the environment featurestates list's identity mode.
func (c *Client) CoreIdentityOverrides(ctx context.Context, envKey string, featureID int) ([]IdentityOverrideRow, error) {
	type coreRow struct {
		Identity struct {
			Identifier string `json:"identifier"`
		} `json:"identity"`
		Enabled bool `json:"enabled"`
		Value   any  `json:"feature_state_value"`
	}
	raw, err := getMany[coreRow](ctx, c, fmt.Sprintf("/api/v1/environments/%s/featurestates/?anyIdentity=1&feature=%d", envKey, featureID))
	if err != nil {
		return nil, err
	}
	rows := make([]IdentityOverrideRow, len(raw))
	for i, r := range raw {
		rows[i] = IdentityOverrideRow{Identifier: r.Identity.Identifier, Enabled: r.Enabled, Value: r.Value}
	}
	return rows, nil
}

// EdgeIdentityOverrides lists every edge (DynamoDB) identity override for a
// feature. The endpoint has no trailing slash and no pagination — replicated
// verbatim.
func (c *Client) EdgeIdentityOverrides(ctx context.Context, envKey string, featureID int) ([]IdentityOverrideRow, error) {
	var raw struct {
		Results []struct {
			Identifier   string `json:"identifier"`
			FeatureState struct {
				Enabled bool `json:"enabled"`
				Value   any  `json:"feature_state_value"`
			} `json:"feature_state"`
		} `json:"results"`
	}
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identity-overrides?feature=%d", envKey, featureID)
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	rows := make([]IdentityOverrideRow, len(raw.Results))
	for i, r := range raw.Results {
		rows[i] = IdentityOverrideRow{Identifier: r.Identifier, Enabled: r.FeatureState.Enabled, Value: r.FeatureState.Value}
	}
	return rows, nil
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

// SegmentOverride is one segment's state in the update-flag-v2 body. Priority,
// when set, moves the override to that position — the server renumbers the
// others around it, preserving their relative order.
type SegmentOverride struct {
	SegmentID int          `json:"segment_id"`
	Enabled   bool         `json:"enabled"`
	Value     FeatureValue `json:"value"`
	Priority  *int         `json:"priority,omitempty"`
}

// UpdateFlagRequest is the update-flag-v2 body. environment_default is always
// required; segment_overrides only creates/updates the segments listed and
// never removes others. This endpoint does not manage identity overrides.
type UpdateFlagRequest struct {
	Feature            FeatureRef         `json:"feature"`
	EnvironmentDefault EnvironmentDefault `json:"environment_default"`
	SegmentOverrides   []SegmentOverride  `json:"segment_overrides,omitempty"`
}

// postUpdateFlags posts a body to one of the experimental flags-update
// endpoints (update-flag-v2, delete-segment-override), which share their
// status protocol: 403 means the environment is workflow-gated, 404 maps to
// notFound when the caller supplies one, and 204/200 are success.
func (c *Client) postUpdateFlags(ctx context.Context, path string, payload any, notFound error) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
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
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return ErrWorkflowGated
	case resp.StatusCode == http.StatusNotFound && notFound != nil:
		return notFound
	case resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK:
		return responseError(http.MethodPost, req.URL.String(), resp)
	}
	return nil
}

// DeleteSegmentOverride removes a feature's override for one segment, via the
// experimental delete-segment-override endpoint keyed by the environment key.
func (c *Client) DeleteSegmentOverride(ctx context.Context, environmentKey string, feature FeatureRef, segmentID int) error {
	payload := map[string]any{
		"feature": feature,
		"segment": map[string]int{"id": segmentID},
	}
	return c.postUpdateFlags(ctx,
		"/api/experiments/environments/"+environmentKey+"/delete-segment-override/",
		payload,
		fmt.Errorf("no override exists for segment %d", segmentID))
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
	return c.postUpdateFlags(ctx,
		"/api/experiments/environments/"+environmentKey+"/update-flag-v2/",
		in, nil)
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
	return getMany[Environment](ctx, c, fmt.Sprintf("/api/v1/environments/?project=%d", projectID))
}

// GetEnvironment fetches one environment by its client-side api_key.
func (c *Client) GetEnvironment(ctx context.Context, apiKey string) (*Environment, error) {
	return getOne[Environment](ctx, c, "/api/v1/environments/"+apiKey+"/")
}

// CreateEnvironment creates an environment from a flat field body (name +
// project required). The client-side api_key is minted server-side.
func (c *Client) CreateEnvironment(ctx context.Context, body map[string]any) (*Environment, error) {
	return send[Environment](ctx, c, http.MethodPost, "/api/v1/environments/", body)
}

// UpdateEnvironment patches an environment (project is immutable and ignored).
func (c *Client) UpdateEnvironment(ctx context.Context, apiKey string, body map[string]any) (*Environment, error) {
	return send[Environment](ctx, c, http.MethodPatch, "/api/v1/environments/"+apiKey+"/", body)
}

// DeleteEnvironment removes an environment by api_key.
func (c *Client) DeleteEnvironment(ctx context.Context, apiKey string) error {
	return c.sendJSON(ctx, http.MethodDelete, "/api/v1/environments/"+apiKey+"/", nil, nil)
}

// CloneEnvironment clones an environment into a new one named by body["name"].
func (c *Client) CloneEnvironment(ctx context.Context, apiKey string, body map[string]any) (*Environment, error) {
	return send[Environment](ctx, c, http.MethodPost, "/api/v1/environments/"+apiKey+"/clone/", body)
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
	return getMany[EnvironmentAPIKey](ctx, c, "/api/v1/environments/"+envKey+"/api-keys/")
}

// CreateEnvironmentAPIKey mints a server-side key; the response includes the
// full key value (shown once).
func (c *Client) CreateEnvironmentAPIKey(ctx context.Context, envKey string, body map[string]any) (*EnvironmentAPIKey, error) {
	return send[EnvironmentAPIKey](ctx, c, http.MethodPost, "/api/v1/environments/"+envKey+"/api-keys/", body)
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
	ids, err := getMany[identity](ctx, c, fmt.Sprintf("/api/v1/environments/%s/identities/?%s", envKey, exactQuery(identifier)))
	if err != nil {
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
	path := fmt.Sprintf("/api/v1/environments/%s/identities/", envKey)
	out, err := send[identity](ctx, c, http.MethodPost, path, map[string]any{"identifier": identifier})
	if err != nil {
		return 0, err
	}
	return out.ID, nil
}

// IdentityOverride returns a core identity's override for a feature, or nil.
func (c *Client) IdentityOverride(ctx context.Context, envKey string, identityID, featureID int) (*IdentityFeatureState, error) {
	path := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/?feature=%d", envKey, identityID, featureID)
	states, err := getMany[IdentityFeatureState](ctx, c, path)
	if err != nil {
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
	ids, err := getMany[edgeIdentity](ctx, c, fmt.Sprintf("/api/v1/environments/%s/edge-identities/?%s", envKey, exactQuery(identifier)))
	if err != nil {
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
	path := fmt.Sprintf("/api/v1/environments/%s/edge-identities/%s/edge-featurestates/?feature=%d", envKey, identityUUID, featureID)
	states, err := getMany[IdentityFeatureState](ctx, c, path)
	if err != nil {
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
	return getMany[Segment](ctx, c, fmt.Sprintf("/api/v1/projects/%d/segments/?include_feature_specific=%t", projectID, include))
}

// GetSegment fetches one segment with its full rule tree.
func (c *Client) GetSegment(ctx context.Context, projectID, segmentID int) (*Segment, error) {
	return getOne[Segment](ctx, c, fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID))
}

// CreateSegment creates a segment (project taken from the URL).
func (c *Client) CreateSegment(ctx context.Context, projectID int, in Segment) (*Segment, error) {
	in.Project = projectID // the serializer requires project in the body, not just the URL
	return send[Segment](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/segments/", projectID), in)
}

// UpdateSegment replaces a segment's rule tree and fields (PUT).
func (c *Client) UpdateSegment(ctx context.Context, projectID, segmentID int, in Segment) (*Segment, error) {
	in.Project = projectID // the serializer requires project in the body, not just the URL
	return send[Segment](ctx, c, http.MethodPut, fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID), in)
}

// DeleteSegment removes a segment.
func (c *Client) DeleteSegment(ctx context.Context, projectID, segmentID int) error {
	path := fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}
