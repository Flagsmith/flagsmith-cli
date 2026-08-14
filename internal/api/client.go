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

	"github.com/Flagsmith/flagsmith-cli/v2/internal/bug"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/httpx"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
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

// Client is the Flagsmith Admin API client.
type Client struct {
	httpClient *http.Client
	baseURL    string
	auth       Auth
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects the underlying *http.Client. Defaults to httpx.New
// with the client's User-Agent.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithUserAgent sets the User-Agent sent on every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// NewClient builds a Client for one instance base URL and auth scheme. The base
// URL's trailing slash is trimmed so paths join cleanly.
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

// The typed CRUD helpers live at package level because Go methods cannot take
// type parameters.

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

// User is a subset of GET /api/v1/auth/users/me/.
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
// server's full field set rather than a curated subset. Embedding can't supply
// the marshal methods (they need the outer type), so each embedder defines two
// one-liners over marshalOr / unmarshalRaw with a method-less local alias.
type rawItem struct {
	raw json.RawMessage
}

// marshalOr returns the captured raw item, or marshals v when nothing was
// captured — e.g. a value built in code rather than decoded from the API.
func (r rawItem) marshalOr(v any) ([]byte, error) {
	if len(r.raw) > 0 {
		return r.raw, nil
	}
	return json.Marshal(v)
}

// unmarshalRaw decodes b into out and captures a copy of b as the raw item.
func unmarshalRaw[T any](b []byte, out *T, raw *rawItem) error {
	if err := json.Unmarshal(b, out); err != nil {
		return err
	}
	raw.raw = append([]byte(nil), b...)
	return nil
}

// Organisation carries the id/name plus the raw API item, so JSON output
// mirrors the server's full field set.
type Organisation struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	rawItem
}

func (o Organisation) MarshalJSON() ([]byte, error) {
	type alias Organisation
	return o.marshalOr(alias(o))
}

func (o *Organisation) UnmarshalJSON(b []byte) error {
	type alias Organisation
	return unmarshalRaw(b, (*alias)(o), &o.rawItem)
}

func (c *Client) Organisations(ctx context.Context) ([]Organisation, error) {
	return getMany[Organisation](ctx, c, "/api/v1/organisations/")
}

func (c *Client) GetOrganisation(ctx context.Context, orgID int) (*Organisation, error) {
	return getOne[Organisation](ctx, c, fmt.Sprintf("/api/v1/organisations/%d/", orgID))
}

// CreateOrganisation creates an organisation from a flat field body.
func (c *Client) CreateOrganisation(ctx context.Context, body map[string]any) (*Organisation, error) {
	return send[Organisation](ctx, c, http.MethodPost, "/api/v1/organisations/", body)
}

func (c *Client) UpdateOrganisation(ctx context.Context, orgID int, body map[string]any) (*Organisation, error) {
	return send[Organisation](ctx, c, http.MethodPatch, fmt.Sprintf("/api/v1/organisations/%d/", orgID), body)
}

func (c *Client) DeleteOrganisation(ctx context.Context, orgID int) error {
	return c.sendJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/organisations/%d/", orgID), nil, nil)
}

// getList decodes a list endpoint that may respond paginated
// ({count, next, results}) or as a bare array. Paginated responses are followed
// across every page via the DRF "next" link. The "next" URL's scheme and host
// are discarded and only its path + query are reused against the base URL,
// which keeps pagination working behind proxies that rewrite the host.
// maxPages bounds the pagination walk. At the page size lists ask for this is
// ~1M items, so it only ever trips on a server whose next link cycles — which
// would otherwise spin the commands that opt out of the invocation deadline.
const maxPages = 1000

func (c *Client) getList(ctx context.Context, path string, out any) error {
	path = withPageSize(path)
	var items []json.RawMessage
	for pages := 0; path != ""; pages++ {
		if pages >= maxPages {
			return bug.Mark(fmt.Errorf("pagination did not terminate after %d pages", maxPages))
		}
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
	// The list contract is [] when empty; a nil slice would marshal to null
	// and leave the caller's slice nil, rendering null downstream.
	if items == nil {
		items = []json.RawMessage{}
	}
	combined, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return json.Unmarshal(combined, out)
}

// withPageSize asks a list endpoint for its largest page: the backend's
// CustomPagination caps page_size at 999 and clamps larger asks (the edge
// endpoints clamp at 100), so one round-trip replaces a sequential walk at the
// server's default page size. Endpoints that don't paginate ignore the parameter.
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
// classifies plan limits as ErrPlanGated or ErrQuotaExceeded. Flagsmith ships no
// machine-readable error code, so limits can only be told apart from RBAC denials
// by their message text — see classifyLimit. 403 caps are wire-identical to
// permission denials and so stay unclassified.
func responseError(method, u string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := apiMessage(body)
	if e := classifyLimit(msg); e != nil {
		return e
	}
	return bug.Mark(&statusError{code: resp.StatusCode, status: resp.Status, message: msg, method: method, url: u})
}

// statusError is a non-2xx response that wasn't classified as a plan limit. It
// carries the HTTP status so it can be special-cased. bug.Mark wraps it, so it
// still reads as unexpected.
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
	// Field-keyed validation errors: {"field": ["msg"]} or {"field": "msg"},
	// nested to whatever depth the field's own shape has — update-flag reports
	// {"segment_overrides": [{"segment": {"id": ["Segment not found."]}}]}.
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return ""
	}
	var msgs []string
	for _, raw := range fields {
		msgs = append(msgs, errorStrings(raw)...)
	}
	sort.Strings(msgs) // map order is random; keep the message stable
	return strings.Join(msgs, "; ")
}

// errorStrings collects every string leaf of a DRF error value.
func errorStrings(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		var msgs []string
		for _, item := range list {
			msgs = append(msgs, errorStrings(item)...)
		}
		return msgs
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		var msgs []string
		for _, value := range fields {
			msgs = append(msgs, errorStrings(value)...)
		}
		sort.Strings(msgs) // map order is random; keep the message stable
		return msgs
	}
	return nil
}

// Verbatim backend exception detail strings, matched case-insensitively against
// the API's detail message — the only signal on the wire.
var (
	quotaPhrases   = []string{"maximum allowed"}                                           // feature / segment / segment-override caps
	upgradePhrases = []string{"upgrade your plan", "payment issue", "has no subscription"} // seats / payment / no subscription
)

// classifyLimit returns a plan-limit error for a recognised message, or nil.
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

// planLimited carries the API's own reason and matches exactly one sentinel —
// ErrQuotaExceeded or ErrPlanGated — so the right recovery hint can be picked
// while still showing the specific limit.
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

// Project carries the curated fields plus the raw API item, so JSON output
// mirrors the server's full field set. UseEdgeIdentities decides whether
// identity overrides live on the core or the edge endpoints.
type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Organisation      int    `json:"organisation"`
	UseEdgeIdentities bool   `json:"use_edge_identities"`
	rawItem
}

func (p *Project) UnmarshalJSON(b []byte) error {
	type alias Project
	return unmarshalRaw(b, (*alias)(p), &p.rawItem)
}

func (p Project) MarshalJSON() ([]byte, error) {
	type alias Project
	return p.marshalOr(alias(p))
}

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

func (c *Client) GetSubscriptionMetadata(ctx context.Context, orgID int) (*SubscriptionMetadata, error) {
	return getOne[SubscriptionMetadata](ctx, c, fmt.Sprintf("/api/v1/organisations/%d/get-subscription-metadata/", orgID))
}

// CreateProject creates a project from a flat field body (name + organisation
// required).
//
// A project-cap denial arrives as a bare 403, identical to an RBAC "you lack
// CREATE_PROJECT" denial, so a 403 is only reported as ErrPlanGated once the
// org is confirmed to be at its plan's project limit.
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
// fails safe: if the limit or the count can't be read, reached is false.
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

func (c *Client) DeleteProject(ctx context.Context, projectID int) error {
	return c.sendJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d/", projectID), nil, nil)
}

// FeatureState is a feature's state in one environment. In the project features
// list, feature_state_value is a bare scalar.
type FeatureState struct {
	Enabled bool `json:"enabled"`
	Value   any  `json:"feature_state_value"`
}

// CodeReferenceCount is a per-repository count of code references to a feature.
type CodeReferenceCount struct {
	Count int `json:"count"`
}

// Feature is a project feature with its state in the requested environment.
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

	// Project-level definition fields.
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

// ProjectFeatures lists a project's features. includeArchived controls whether
// archived features are returned.
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

// Features lists a project's features with their state in one environment,
// identified by its numeric ID. When segmentID is non-zero, each feature also
// carries its segment_feature_state for that segment. A non-empty search
// narrows the list server-side: a contains match on the name, not an exact one.
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

// Scalar converts the typed wire form to a bare scalar.
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

// MultivariateStateValue is one variant's weight in a single feature state —
// the per-scope allocation, which starts from the option's project-level
// default and drifts as environments and segments are re-weighted.
type MultivariateStateValue struct {
	OptionID   int     `json:"multivariate_feature_option"`
	Allocation float64 `json:"percentage_allocation"`
}

// EnvironmentFeatureState is one row of the admin featurestates list: a
// feature's state for the environment default (feature_segment null), one
// segment override, or (in v2-versioned environments) an identity override.
type EnvironmentFeatureState struct {
	ID             int                      `json:"id"`
	Enabled        bool                     `json:"enabled"`
	FeatureSegment *int                     `json:"feature_segment"`
	Identity       *int                     `json:"identity"`
	Value          TypedValue               `json:"feature_state_value"`
	Multivariate   []MultivariateStateValue `json:"multivariate_feature_state_values"`
}

// FeatureStates lists a feature's live states in one environment.
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

// FeatureValue is a typed flag value in the update-flag wire form: the type as
// a word and the value always as a string.
type FeatureValue struct {
	Type  string `json:"type"`  // "integer" | "string" | "boolean"
	Value string `json:"value"` // always a string; parsed server-side per type
}

// Variant is one multivariate variant's share of a scope's traffic, as a
// percentage between 0 and 100. Weights that don't add up to 100 leave the
// remainder serving the flag's own value.
type Variant struct {
	ID     int     `json:"id"`
	Weight float64 `json:"weight"`
}

// SegmentTarget names the segment an override belongs to.
type SegmentTarget struct {
	ID int `json:"id"`
}

// FlagStateUpdate is a change to a flag's state in one scope. Every field is
// optional: PATCH leaves an omitted one unchanged, and PUT resets it to the
// server's default — so a partial PUT is a destructive write.
//
// Variants is all-or-nothing: the endpoint rejects a list that doesn't name
// every variant of the feature, whatever the verb.
type FlagStateUpdate struct {
	Enabled  *bool         `json:"enabled,omitempty"`
	Value    *FeatureValue `json:"value,omitempty"`
	Variants []Variant     `json:"variants,omitempty"`
}

// SegmentOverrideUpdate is a change to one segment's override. Priority, when
// set, moves the override to that position — the server renumbers the others
// around it, preserving their relative order.
type SegmentOverrideUpdate struct {
	Segment  SegmentTarget `json:"segment"`
	Enabled  *bool         `json:"enabled,omitempty"`
	Priority *int          `json:"priority,omitempty"`
	Value    *FeatureValue `json:"value,omitempty"`
	Variants []Variant     `json:"variants,omitempty"`
}

// UpdateFlagRequest is the update-flag body. Under PATCH the overrides listed
// are created or updated and the rest are left alone; under PUT the list
// replaces the whole set, so an override missing from it is deleted — which is
// why SegmentOverrides distinguishes nil (absent) from empty (delete them all).
// The endpoint does not manage identity overrides.
type UpdateFlagRequest struct {
	EnvironmentDefault *FlagStateUpdate        `json:"environment_default,omitempty"`
	SegmentOverrides   []SegmentOverrideUpdate `json:"segment_overrides,omitzero"`
}

// FlagState is a flag's resulting state in one scope, as the update-flag
// endpoint reports it. Value is null for a feature with no value at all.
type FlagState struct {
	Enabled  bool          `json:"enabled"`
	Value    *FeatureValue `json:"value"`
	Variants []Variant     `json:"variants"`
}

// SegmentOverrideState is one segment override's resulting state.
type SegmentOverrideState struct {
	Segment  SegmentTarget `json:"segment"`
	Priority int           `json:"priority"`
	Enabled  bool          `json:"enabled"`
	Value    *FeatureValue `json:"value"`
	Variants []Variant     `json:"variants"`
}

// UpdateFlagResponse is the flag's complete state in the environment after the
// write, whichever properties the request carried.
type UpdateFlagResponse struct {
	EnvironmentDefault FlagState              `json:"environment_default"`
	SegmentOverrides   []SegmentOverrideState `json:"segment_overrides"`
}

// Override returns the resulting state of one segment's override, or nil when
// the response carries none for it.
func (r *UpdateFlagResponse) Override(segmentID int) *SegmentOverrideState {
	for i := range r.SegmentOverrides {
		if r.SegmentOverrides[i].Segment.ID == segmentID {
			return &r.SegmentOverrides[i]
		}
	}
	return nil
}

func updateFlagPath(environmentKey string, featureID int) string {
	return fmt.Sprintf("/api/__future__/environments/%s/features/%d/", environmentKey, featureID)
}

// UpdateFlag applies a partial change to a flag's state in one environment,
// keyed by the environment's client-side key and the feature's id. Properties
// the request omits are left as they are.
func (c *Client) UpdateFlag(ctx context.Context, environmentKey string, featureID int, in UpdateFlagRequest) (*UpdateFlagResponse, error) {
	return c.writeFlag(ctx, http.MethodPatch, environmentKey, featureID, in)
}

// ReplaceFlag replaces each property the request carries in full — the only way
// to delete a segment override, by PUTting the overrides that survive it.
// Anything a replaced property leaves out is reset, so callers must echo the
// state they mean to keep.
func (c *Client) ReplaceFlag(ctx context.Context, environmentKey string, featureID int, in UpdateFlagRequest) (*UpdateFlagResponse, error) {
	return c.writeFlag(ctx, http.MethodPut, environmentKey, featureID, in)
}

func (c *Client) writeFlag(ctx context.Context, method, environmentKey string, featureID int, in UpdateFlagRequest) (*UpdateFlagResponse, error) {
	out, err := send[UpdateFlagResponse](ctx, c, method, updateFlagPath(environmentKey, featureID), in)
	if err != nil {
		return nil, classifyFlagWrite(err)
	}
	return out, nil
}

// changeRequestPhrase is the verbatim backend detail for a refused write —
// matched case-insensitively, since the status (400) is shared with every
// validation error.
const changeRequestPhrase = "change requests enabled"

// classifyFlagWrite recognises the one update-flag failure the user can act on.
func classifyFlagWrite(err error) error {
	var e *statusError
	if errors.As(err, &e) && e.code == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(e.message), changeRequestPhrase) {
		return ErrWorkflowGated
	}
	return err
}

// ErrWorkflowGated is returned when update-flag refuses because the environment
// has change-request workflows enabled.
var ErrWorkflowGated = fmt.Errorf("this environment uses change-request workflows; direct updates are disabled")

// ErrPlanGated marks a self-serve plan limit a user lifts by upgrading — seats,
// a payment problem, or no subscription. The error's own message is the API's
// specific reason.
var ErrPlanGated = errors.New("not available on your organisation's current plan")

// ErrQuotaExceeded marks a resource cap that is raised on request rather than by
// self-serve upgrade — the feature, segment, and segment-override limits, which
// enterprise plans can relax. Telling the two apart is by message text only (see
// classifyLimit); Flagsmith ships no machine-readable error code.
var ErrQuotaExceeded = errors.New("resource limit reached on your organisation's current plan")

// Environment carries the curated fields plus the raw API item, so JSON output
// mirrors the server's full field set. Identified by APIKey, not id.
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
	return unmarshalRaw(b, (*alias)(e), &e.rawItem)
}

func (e Environment) MarshalJSON() ([]byte, error) {
	type alias Environment
	return e.marshalOr(alias(e))
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

func (c *Client) EnvironmentAPIKeys(ctx context.Context, envKey string) ([]EnvironmentAPIKey, error) {
	return getMany[EnvironmentAPIKey](ctx, c, "/api/v1/environments/"+envKey+"/api-keys/")
}

// CreateEnvironmentAPIKey mints a server-side key; the response includes the
// full key value (shown once).
func (c *Client) CreateEnvironmentAPIKey(ctx context.Context, envKey string, body map[string]any) (*EnvironmentAPIKey, error) {
	return send[EnvironmentAPIKey](ctx, c, http.MethodPost, "/api/v1/environments/"+envKey+"/api-keys/", body)
}

func (c *Client) DeleteEnvironmentAPIKey(ctx context.Context, envKey string, keyID int) error {
	path := fmt.Sprintf("/api/v1/environments/%s/api-keys/%d/", envKey, keyID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}

// IdentityFeatureState is a feature's override for one identity. ID is the
// feature-state id used to update/delete it; it is unset for edge reads.
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
// the trailing slash — replicated verbatim.
func edgeIdentityFeatureStatesPath(envKey string) string {
	return "/api/v1/environments/environments/" + envKey + "/edge-identities-featurestates"
}

// SetEdgeIdentityOverride creates-or-updates an edge identity override in one
// call (the identity is created if it does not exist). value is a native scalar.
func (c *Client) SetEdgeIdentityOverride(ctx context.Context, envKey, identifier string, featureID int, enabled bool, value any) error {
	body := map[string]any{"identifier": identifier, "feature": featureID, "enabled": enabled, "feature_state_value": value}
	return c.sendJSON(ctx, http.MethodPut, edgeIdentityFeatureStatesPath(envKey), body, nil)
}

func (c *Client) DeleteEdgeIdentityOverride(ctx context.Context, envKey, identifier string, featureID int) error {
	body := map[string]any{"identifier": identifier, "feature": featureID}
	return c.sendJSON(ctx, http.MethodDelete, edgeIdentityFeatureStatesPath(envKey), body, nil)
}

// SegmentCondition is one condition in a segment rule. On the wire `value` is a
// plain string (or null); IN arrays are carried as a JSON-array string.
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

// CreateSegment creates a segment.
func (c *Client) CreateSegment(ctx context.Context, projectID int, in Segment) (*Segment, error) {
	in.Project = projectID // the serializer requires project in the body, not just the URL
	return send[Segment](ctx, c, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/segments/", projectID), in)
}

// UpdateSegment replaces a segment's rule tree and fields.
func (c *Client) UpdateSegment(ctx context.Context, projectID, segmentID int, in Segment) (*Segment, error) {
	in.Project = projectID // the serializer requires project in the body, not just the URL
	return send[Segment](ctx, c, http.MethodPut, fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID), in)
}

func (c *Client) DeleteSegment(ctx context.Context, projectID, segmentID int) error {
	path := fmt.Sprintf("/api/v1/projects/%d/segments/%d/", projectID, segmentID)
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil)
}
