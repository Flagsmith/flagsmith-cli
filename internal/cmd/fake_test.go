package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
)

// fakeInstance is a Flagsmith instance stub.
type fakeInstance struct {
	srv *httptest.Server

	mu             sync.Mutex
	reqLog         []string // every request served, in arrival order
	revoked        []url.Values
	orgs           []map[string]any
	projects       map[string][]map[string]any // orgID -> projects
	envs           map[string][]map[string]any // projectID -> environments
	created        []string
	createdEnvs    []string
	features       map[string][]map[string]any // projectID -> features list; nil → default
	lastFeatEnv    string                      // last ?environment= seen by /features/
	lastFeatSeg    string                      // last ?segment= seen by /features/
	lastFeatArch   string                      // last ?is_archived= seen by /features/
	lastFeatSearch string                      // last ?search= seen by /features/
	featListCalls  int                         // count of GET /features/ list calls
	fsListCalls    int                         // count of GET /features/feature-segments/ calls
	fsDelay        time.Duration               // artificial latency for feature-segments
	fsInFlight     int                         // feature-segments requests currently being served
	fsPeak         int                         // high-water mark of fsInFlight
	segListCalls   int                         // count of GET /segments/ list calls
	stListCalls    int                         // count of GET /features/featurestates/ calls
	idLookupCalls  int                         // count of GET /identities/ (identifier lookups)
	edgeLookups    int                         // count of GET /edge-identities/ (uuid lookups)
	envGetCalls    int                         // count of GET /environments/{key}/ (retrieve)
	envListCalls   int                         // count of GET /environments/ list calls
	projGetCalls   int                         // count of GET /projects/{id}/ (retrieve)
	orgListCalls   int                         // count of GET /organisations/ list calls
	tokenPosts     int                         // count of POST /o/token/ (refresh) calls
	workflowGated  bool                        // when true, update endpoints return 403
	segmentMissing bool                        // when true, delete-segment-override returns 404

	useEdge        bool                       // GET /projects/{id}/ use_edge_identities
	coreIdentities map[string]int             // identifier -> identity id
	coreOverrides  map[int]map[int]*fakeFS    // identity id -> feature id -> state
	edgeOverrides  map[string]map[int]*fakeFS // identifier -> feature id -> state
	nextFSID       int

	segments      map[int]map[string]any // segment id -> segment
	nextSegmentID int

	featureSegments map[string][]map[string]any // feature id -> feature-segment rows (priority order)
	featureStates   map[string][]map[string]any // feature id -> admin featurestates rows

	// The SDK API surface `flagsmith evaluate` reads. sdkEnvFlags is keyed by
	// environment key — an unrecognised key gets a 401, as the real SDK API
	// does; sdkIdentityFlags overrides it per identifier ("" is the anonymous
	// identity), so an identity evaluation can be told apart from the
	// environment defaults.
	sdkEnvFlags      map[string][]map[string]any
	sdkIdentityFlags map[string][]map[string]any
	lastIdentify     map[string]any // last POST /api/v1/identities/ body
	sdkUserAgents    []string       // User-Agent of every SDK API request
	sdkKeys          []string       // X-Environment-Key of every SDK API request
	sdkStatus        int            // when non-zero, the SDK endpoints answer with it
	sdkDelay         time.Duration  // artificial latency for the SDK endpoints

	nextFeatureID   int
	nextMVID        int
	nextOrgID       int
	serverKeys      map[string][]map[string]any // env api_key -> server-side keys
	nextServerKeyID int
}

// envByAPIKey finds a stored environment by its client-side key, returning its
// project key too (caller holds the lock).
func (f *fakeInstance) envByAPIKey(key string) (string, map[string]any) {
	for proj, list := range f.envs {
		for _, e := range list {
			if e["api_key"] == key {
				return proj, e
			}
		}
	}
	return "", nil
}

func (f *fakeInstance) orgByID(id int) map[string]any {
	for _, o := range f.orgs {
		if o["id"] == id {
			return o
		}
	}
	return nil
}

// projectByID finds a stored project across all orgs (caller holds the lock).
func (f *fakeInstance) projectByID(id int) map[string]any {
	for _, list := range f.projects {
		for _, p := range list {
			if p["id"] == id {
				return p
			}
		}
	}
	return nil
}

// featureByID finds a stored feature item by id (caller holds the lock).
func (f *fakeInstance) featureByID(project string, id int) map[string]any {
	for _, it := range f.features[project] {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

// fakeFS is a stored identity feature-state in the fake backend.
type fakeFS struct {
	id      int
	enabled bool
	value   any
}

func newFakeInstance(t *testing.T) *fakeInstance {
	t.Helper()
	f := newFake()
	t.Cleanup(f.srv.Close)
	return f
}

// newFake builds the fake instance without a *testing.T, so a testscript Setup
// hook — which only has an Env — can construct one too. The caller owns the
// server's lifetime.
func newFake() *fakeInstance {
	f := &fakeInstance{
		orgs: []map[string]any{{"id": 3, "name": "Acme"}},
		projects: map[string][]map[string]any{
			"3": {{"id": 101, "name": "acme-api", "organisation": 3}},
		},
		envs: map[string][]map[string]any{
			"101": {
				{"id": 1, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN"},
				{"id": 2, "name": "Production", "api_key": "K2mVsGdXhZ8kQqZ9pJmNbJ"},
			},
			"12345": {
				{"id": 3, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN"},
			},
		},
		features:       map[string][]map[string]any{"101": defaultFeatures()},
		coreIdentities: map[string]int{"user-1": 501},
		coreOverrides:  map[int]map[int]*fakeFS{},
		edgeOverrides:  map[string]map[int]*fakeFS{},
		nextFSID:       9000,
		segments: map[int]map[string]any{
			42: {
				"id": 42, "name": "us-adults", "description": "Users in the US aged 18+", "feature": nil,
				"rules": []any{map[string]any{"type": "ALL", "rules": []any{
					map[string]any{"type": "ANY", "conditions": []any{
						map[string]any{"property": "country", "operator": "IN", "value": `["US","CA"]`},
						map[string]any{"property": "age", "operator": "GREATER_THAN_INCLUSIVE", "value": "18"},
					}},
				}}},
			},
			57: {
				"id": 57, "name": "beta-optin", "description": "Opted into the beta", "feature": nil,
				"rules": []any{map[string]any{"type": "ALL", "conditions": []any{
					map[string]any{"property": "beta", "operator": "IS_SET", "value": nil},
				}}},
			},
			58: {
				"id": 58, "name": "beta-cohort", "description": "Beta cohort for checkout-v2", "feature": 2,
				"rules": []any{map[string]any{"type": "ALL", "conditions": []any{
					map[string]any{"property": "beta", "operator": "IS_SET", "value": nil},
				}}},
			},
		},
		sdkEnvFlags: map[string][]map[string]any{
			"WqXhZk8sVY3dGgTqZ9pJmN": sdkFlagsFrom(defaultFeatures()),
		},
		sdkIdentityFlags: map[string][]map[string]any{},
		nextSegmentID:    100,
		nextFeatureID:    900,
		nextMVID:         300,
		nextOrgID:        20,
		serverKeys:       map[string][]map[string]any{},
		nextServerKeyID:  500,
	}
	mux := http.NewServeMux()
	f.mountOAuth(mux)
	f.mountAccounts(mux)
	f.mountEnvironments(mux)
	f.mountFlags(mux)
	f.mountProjectItem(mux)
	f.mountIdentities(mux)
	f.mountOrganisationItem(mux)
	f.mountFeatures(mux)
	f.mountSegments(mux)
	f.mountSDK(mux)
	f.srv = httptest.NewServer(f.record(mux))
	return f
}

// record wraps the fake's mux so every request the CLI makes is logged. The
// log is what a transcript asserts on: it makes request bodies, call counts and
// query parameters visible without a bespoke recording field per endpoint.
func (f *fakeInstance) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		line := r.Method + " " + r.URL.RequestURI()
		// Which credential travelled, by kind not by value: enough to pin
		// scoping ("the master key never reaches the SDK surface") without
		// writing a secret into a golden file.
		if cred := credKind(r); cred != "" {
			line += " [" + cred + "]"
		}
		if len(body) > 0 {
			line += " " + string(body)
		}
		f.mu.Lock()
		f.reqLog = append(f.reqLog, line)
		f.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// credKind names the credential a request carried, without echoing it.
func credKind(r *http.Request) string {
	switch {
	case r.Header.Get("X-Environment-Key") == masterKey:
		return "master-key-as-environment-key" // a leak, and the transcript says so
	case r.Header.Get("X-Environment-Key") != "":
		return "environment-key"
	case r.Header.Get("Authorization") == "Api-Key "+masterKey:
		return "master-key"
	case strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
		return "bearer"
	case r.Header.Get("Authorization") != "":
		return "other-credential"
	}
	return ""
}

// requests returns the logged requests in the order they arrived.
func (f *fakeInstance) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqLog...)
}

// applyFlagUpdate mutates the stored features to reflect an update-flag-v2
// body, so a re-fetch after the mutation sees the new state. Called under lock.
func (f *fakeInstance) applyFlagUpdate(body map[string]any) {
	feature, _ := body["feature"].(map[string]any)
	name, _ := feature["name"].(string)
	def, _ := body["environment_default"].(map[string]any)
	enabled, _ := def["enabled"].(bool)
	val, _ := def["value"].(map[string]any)
	overrides, _ := body["segment_overrides"].([]any)
	for _, items := range f.features {
		for _, item := range items {
			if item["name"] != name {
				continue
			}
			state, _ := item["environment_feature_state"].(map[string]any)
			if state == nil {
				state = map[string]any{}
				item["environment_feature_state"] = state
			}
			state["enabled"] = enabled
			state["feature_state_value"] = scalarFromWire(val)
			featureKey := ""
			if id, ok := item["id"].(int); ok {
				featureKey = strconv.Itoa(id)
			}
			for _, o := range overrides {
				ov, _ := o.(map[string]any)
				segEnabled, _ := ov["enabled"].(bool)
				segVal, _ := ov["value"].(map[string]any)
				item["segment_feature_state"] = map[string]any{
					"enabled": segEnabled, "feature_state_value": scalarFromWire(segVal),
				}
				// A priority write moves the feature-segment row, so a re-fetch
				// sees the new order.
				if prio, ok := ov["priority"].(float64); ok {
					for _, row := range f.featureSegments[featureKey] {
						if seg, _ := row["segment"].(int); float64(seg) == ov["segment_id"] {
							row["priority"] = int(prio)
						}
					}
				}
			}
			sort.SliceStable(f.featureSegments[featureKey], func(a, b int) bool {
				pa, _ := f.featureSegments[featureKey][a]["priority"].(int)
				pb, _ := f.featureSegments[featureKey][b]["priority"].(int)
				return pa < pb
			})
		}
	}
}

// scalarFromWire turns an update-flag-v2 {type,value} into the bare scalar the
// features list would report.
func scalarFromWire(val map[string]any) any {
	t, _ := val["type"].(string)
	v, _ := val["value"].(string)
	switch t {
	case "integer":
		n, _ := strconv.Atoi(v)
		return n
	case "boolean":
		return v == "true"
	default:
		return v
	}
}

func (f *fakeInstance) revokeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.revoked)
}

func (f *fakeInstance) featuresCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.featListCalls
}

// featureSegmentsCalls returns how many times feature-segments was hit.
func (f *fakeInstance) featureSegmentsCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fsListCalls
}

// featureSegmentsPeak returns the most feature-segments requests that were
// ever in flight at once.
func (f *fakeInstance) featureSegmentsPeak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fsPeak
}

func (f *fakeInstance) organisationLists() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orgListCalls
}

// defaultFeatures is the stock project features list (with per-environment
// state embedded) returned by the fake /features/ endpoint.
func defaultFeatures() []map[string]any {
	return []map[string]any{
		{
			"id": 1, "name": "onboarding_banner", "type": "STANDARD",
			"description": "Welcome banner", "lifecycle_stage": "live",
			"num_segment_overrides": 0, "num_identity_overrides": 0,
			"code_references_counts":    []any{},
			"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": nil},
		},
		{
			"id": 2, "name": "max_items", "type": "STANDARD",
			"num_segment_overrides": 1, "num_identity_overrides": 2,
			"code_references_counts":    []any{map[string]any{"count": 3}},
			"environment_feature_state": map[string]any{"enabled": false, "feature_state_value": 25},
		},
	}
}

// sdkFlagsFrom renders admin feature fixtures as the SDK API's flags payload —
// the shape `flagsmith evaluate` reads.
func sdkFlagsFrom(features []map[string]any) []map[string]any {
	flags := make([]map[string]any, 0, len(features))
	for _, item := range features {
		state, _ := item["environment_feature_state"].(map[string]any)
		flags = append(flags, map[string]any{
			"enabled":             state["enabled"],
			"feature_state_value": state["feature_state_value"],
			"feature":             map[string]any{"id": item["id"], "name": item["name"]},
		})
	}
	return flags
}

func (f *fakeInstance) sdkAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sdkUserAgents...)
}

func (f *fakeInstance) sdkSentKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sdkKeys...)
}

func (f *fakeInstance) environmentLists() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.envListCalls
}

func (f *fakeInstance) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenPosts
}

// commandShapes are the two supported spellings of login/logout:
// top-level and under `auth`.

func flagUpdateEnv(t *testing.T) *fakeInstance {
	t.Helper()
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
	setMasterKey(t, f.srv.URL)
	return f
}

func withFeatureSegments(f *fakeInstance, featureID int, rows ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.featureSegments == nil {
		f.featureSegments = map[string][]map[string]any{}
	}
	f.featureSegments[strconv.Itoa(featureID)] = rows
}

// withFeatureStates registers the admin featurestates rows the fake returns
// for one feature.
func withFeatureStates(f *fakeInstance, featureID int, rows ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.featureStates == nil {
		f.featureStates = map[string][]map[string]any{}
	}
	f.featureStates[strconv.Itoa(featureID)] = rows
}

// The fake serves requests concurrently (see fsPeak), so every field a handler
// reads is set through a locked setter rather than assigned directly.

// withWorkflowGating makes the update endpoints answer 403.
func withWorkflowGating(f *fakeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflowGated = true
}

// withMissingSegmentOverride makes delete-segment-override answer 404.
func withMissingSegmentOverride(f *fakeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.segmentMissing = true
}

// withEdgeIdentities makes the project report use_edge_identities.
func withEdgeIdentities(f *fakeInstance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.useEdge = true
}

// withFeatureSegmentDelay adds latency to feature-segments, so overlapping
// requests are observable in fsPeak.
func withFeatureSegmentDelay(f *fakeInstance, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fsDelay = d
}

// withSegmentOverride sets project 101's features to a single max_items
// feature carrying an environment default and, optionally, a segment override.
func withSegmentOverride(f *fakeInstance, withOverride bool) {
	item := map[string]any{
		"id": 2, "name": "max_items", "type": "STANDARD",
		"num_segment_overrides": 1, "num_identity_overrides": 0,
		"environment_feature_state": map[string]any{"enabled": false, "feature_state_value": 25},
	}
	if withOverride {
		item["segment_feature_state"] = map[string]any{"enabled": true, "feature_state_value": "special"}
	}
	f.features["101"] = []map[string]any{item}
}

func withFeatureOverridesRows(f *fakeInstance) {
	withFeatureSegments(f, 2,
		map[string]any{"id": 1200, "segment": 57, "segment_name": "beta-optin", "priority": 0, "environment": 1},
		map[string]any{"id": 1201, "segment": 42, "segment_name": "us-adults", "priority": 1, "environment": 1},
	)
	str := func(s string) map[string]any { return map[string]any{"type": "unicode", "string_value": s} }
	withFeatureStates(f, 2,
		map[string]any{"id": 9000, "feature_segment": nil, "enabled": false, "feature_state_value": str("default")},
		map[string]any{"id": 9001, "feature_segment": 1200, "enabled": true, "feature_state_value": str("blue")},
		map[string]any{"id": 9002, "feature_segment": 1201, "enabled": false, "feature_state_value": map[string]any{"type": "int", "integer_value": 25}},
	)
}

// mountOAuth mounts the OAuth endpoints login and refresh go through, and the identity behind a bearer token.
func (f *fakeInstance) mountOAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/oauth/authorize/",
			"token_endpoint":         f.srv.URL + "/o/token/",
			"revocation_endpoint":    f.srv.URL + "/o/revoke_token/",
		})
	})
	mux.HandleFunc("POST /o/token/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.tokenPosts++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  oauthAccess,
			"refresh_token": "cmd-refresh",
			"expires_in":    900,
			"scope":         auth.Scope,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("POST /o/revoke_token/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm)
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /api/v1/auth/users/me/", func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if a != "Bearer "+oauthAccess && a != "Bearer "+bearerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"email": "kim@example.com", "uuid": "u-1"})
	})
}

// authorized reports whether a request carries a credential the fake accepts:
// the master key, or either of the bearer tokens.
func authorized(r *http.Request) bool {
	a := r.Header.Get("Authorization")
	return a == "Api-Key "+masterKey || a == "Bearer "+oauthAccess || a == "Bearer "+bearerToken
}

// mountAccounts mounts organisations and projects: what a credential can see, and project CRUD.
func (f *fakeInstance) mountAccounts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/organisations/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.orgListCalls++
		orgs := f.orgs
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(orgs), "results": orgs})
	})
	mux.HandleFunc("GET /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		var projects []map[string]any
		if org := r.URL.Query().Get("organisation"); org != "" {
			projects = f.projects[org]
		} else {
			for _, list := range f.projects {
				projects = append(projects, list...)
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(projects), "results": projects})
	})
	mux.HandleFunc("POST /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		name, _ := body["name"].(string)
		f.mu.Lock()
		f.created = append(f.created, name)
		proj := map[string]any{"id": 999, "name": name}
		if org, ok := body["organisation"].(float64); ok {
			proj["organisation"] = int(org)
			f.projects[strconv.Itoa(int(org))] = append(f.projects[strconv.Itoa(int(org))], proj)
		}
		if f.envs["999"] == nil {
			f.envs["999"] = []map[string]any{} // created projects start empty
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(proj)
	})
}

// mountEnvironments mounts environments, their server-side keys, documents and clones.
func (f *fakeInstance) mountEnvironments(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/environments/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.envListCalls++
		f.mu.Unlock()
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		envs, known := f.envs[r.URL.Query().Get("project")]
		f.mu.Unlock()
		if !known {
			w.WriteHeader(http.StatusForbidden) // no access to this project
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(envs), "results": envs})
	})
	mux.HandleFunc("POST /api/v1/environments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		name, _ := body["name"].(string)
		f.mu.Lock()
		f.createdEnvs = append(f.createdEnvs, name)
		env := map[string]any{"id": 42, "name": name, "api_key": "createdEnvKey00000000"}
		for k, v := range body {
			env[k] = v
		}
		env["api_key"] = "createdEnvKey00000000"
		if proj, ok := body["project"].(float64); ok {
			key := strconv.Itoa(int(proj))
			f.envs[key] = append(f.envs[key], env)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(env)
	})
	mux.HandleFunc("GET /api/v1/environments/{api_key}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.envGetCalls++
		_, env := f.envByAPIKey(r.PathValue("api_key"))
		f.mu.Unlock()
		if env == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(env)
	})
	mux.HandleFunc("PATCH /api/v1/environments/{api_key}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		_, env := f.envByAPIKey(r.PathValue("api_key"))
		if env != nil {
			for k, v := range body {
				env[k] = v
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(env)
	})
	mux.HandleFunc("DELETE /api/v1/environments/{api_key}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.PathValue("api_key")
		f.mu.Lock()
		for proj, list := range f.envs {
			kept := list[:0:0]
			for _, e := range list {
				if e["api_key"] != key {
					kept = append(kept, e)
				}
			}
			f.envs[proj] = kept
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/environments/{api_key}/document/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"api_key": r.PathValue("api_key"),
			"feature_states": []any{
				map[string]any{"feature": map[string]any{"name": "onboarding"}},
				map[string]any{"feature": map[string]any{"name": "checkout"}},
			},
		})
	})
	// Server-side SDK keys sub-resource.
	mux.HandleFunc("GET /api/v1/environments/{api_key}/api-keys/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		keys := f.serverKeys[r.PathValue("api_key")]
		f.mu.Unlock()
		json.NewEncoder(w).Encode(keys) // bare array (pagination_class = None)
	})
	mux.HandleFunc("POST /api/v1/environments/{api_key}/api-keys/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		env := r.PathValue("api_key")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.nextServerKeyID++
		key := map[string]any{
			"id": f.nextServerKeyID, "name": body["name"], "active": true,
			"key": "ser.mintedKey000000000", "created_at": "2026-07-16T00:00:00Z",
		}
		f.serverKeys[env] = append(f.serverKeys[env], key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(key)
	})
	mux.HandleFunc("DELETE /api/v1/environments/{api_key}/api-keys/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		env := r.PathValue("api_key")
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		kept := []map[string]any{}
		for _, k := range f.serverKeys[env] {
			if k["id"] != id {
				kept = append(kept, k)
			}
		}
		f.serverKeys[env] = kept
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/environments/{api_key}/clone/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		proj, src := f.envByAPIKey(r.PathValue("api_key"))
		name, _ := body["name"].(string)
		clone := map[string]any{"id": 77, "name": name, "api_key": "clonedEnvKey000000000"}
		if src != nil {
			clone["project"] = src["project"]
			f.envs[proj] = append(f.envs[proj], clone)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(clone)
	})
}

// mountFlags mounts the flag surface: the features list an environment resolves, and the two experiment writes behind flag update and delete.
func (f *fakeInstance) mountFlags(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{project}/features/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.lastFeatEnv = r.URL.Query().Get("environment")
		f.lastFeatSeg = r.URL.Query().Get("segment")
		f.lastFeatArch = r.URL.Query().Get("is_archived")
		f.lastFeatSearch = r.URL.Query().Get("search")
		f.featListCalls++
		items := f.features[r.PathValue("project")]
		// Like the backend, search is a case-insensitive contains match on the
		// name — deliberately broader than the exact match the CLI wants, so
		// tests exercise the client-side narrowing.
		if search := r.URL.Query().Get("search"); search != "" {
			filtered := []map[string]any{}
			for _, it := range items {
				name, _ := it["name"].(string)
				if strings.Contains(strings.ToLower(name), strings.ToLower(search)) {
					filtered = append(filtered, it)
				}
			}
			items = filtered
		}
		if arch := r.URL.Query().Get("is_archived"); arch != "" {
			want := arch == "true"
			filtered := []map[string]any{}
			for _, it := range items {
				a, _ := it["is_archived"].(bool)
				if a == want {
					filtered = append(filtered, it)
				}
			}
			items = filtered
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"count": len(items), "next": nil, "previous": nil, "results": items,
		})
	})
	mux.HandleFunc("POST /api/experiments/environments/{env}/update-flag-v2/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		gated := f.workflowGated
		f.mu.Unlock()
		if gated {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.applyFlagUpdate(body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/experiments/environments/{env}/delete-segment-override/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		missing := f.segmentMissing
		f.mu.Unlock()
		if missing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// mountProjectItem mounts a single project: the retrieve that carries use_edge_identities, and its updates.
func (f *fakeInstance) mountProjectItem(mux *http.ServeMux) {
	// Project retrieve — carries use_edge_identities.
	mux.HandleFunc("GET /api/v1/projects/{project}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("project"))
		f.mu.Lock()
		f.projGetCalls++
		resp := map[string]any{"id": id, "name": "acme-api", "organisation": 3}
		if p := f.projectByID(id); p != nil {
			resp = map[string]any{}
			for k, v := range p {
				resp[k] = v
			}
		}
		resp["use_edge_identities"] = f.useEdge
		f.mu.Unlock()
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("project"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		p := f.projectByID(id)
		if p != nil {
			for k, v := range body {
				p[k] = v
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("project"))
		f.mu.Lock()
		for org, list := range f.projects {
			kept := list[:0:0]
			for _, p := range list {
				if p["id"] != id {
					kept = append(kept, p)
				}
			}
			f.projects[org] = kept
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
}

// mountIdentities mounts identities and their overrides, core and edge.
func (f *fakeInstance) mountIdentities(mux *http.ServeMux) {
	// Core identities: identifier lookup and create.
	mux.HandleFunc("GET /api/v1/environments/{env}/identities/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.Trim(r.URL.Query().Get("q"), `"`)
		f.mu.Lock()
		f.idLookupCalls++
		var results []map[string]any
		if id, ok := f.coreIdentities[q]; ok {
			results = append(results, map[string]any{"id": id, "identifier": q})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	mux.HandleFunc("POST /api/v1/environments/{env}/identities/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ident, _ := body["identifier"].(string)
		f.mu.Lock()
		id := 700 + len(f.coreIdentities)
		f.coreIdentities[ident] = id
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": id, "identifier": ident})
	})
	// Core identity feature-states: list, create, update, delete.
	mux.HandleFunc("GET /api/v1/environments/{env}/identities/{id}/featurestates/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		idID, _ := strconv.Atoi(r.PathValue("id"))
		fid, _ := strconv.Atoi(r.URL.Query().Get("feature"))
		f.mu.Lock()
		var results []map[string]any
		if fs := f.coreOverrides[idID][fid]; fs != nil {
			results = append(results, map[string]any{"id": fs.id, "enabled": fs.enabled, "feature_state_value": fs.value, "feature": fid})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	mux.HandleFunc("POST /api/v1/environments/{env}/identities/{id}/featurestates/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		idID, _ := strconv.Atoi(r.PathValue("id"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		fid := int(body["feature"].(float64))
		en, _ := body["enabled"].(bool)
		f.mu.Lock()
		if f.coreOverrides[idID] == nil {
			f.coreOverrides[idID] = map[int]*fakeFS{}
		}
		f.nextFSID++
		f.coreOverrides[idID][fid] = &fakeFS{id: f.nextFSID, enabled: en, value: body["feature_state_value"]}
		id := f.nextFSID
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	mux.HandleFunc("PUT /api/v1/environments/{env}/identities/{id}/featurestates/{fsid}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		idID, _ := strconv.Atoi(r.PathValue("id"))
		fsID, _ := strconv.Atoi(r.PathValue("fsid"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		en, _ := body["enabled"].(bool)
		f.mu.Lock()
		for _, fs := range f.coreOverrides[idID] {
			if fs.id == fsID {
				fs.enabled = en
				fs.value = body["feature_state_value"]
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": fsID})
	})
	mux.HandleFunc("DELETE /api/v1/environments/{env}/identities/{id}/featurestates/{fsid}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		idID, _ := strconv.Atoi(r.PathValue("id"))
		fsID, _ := strconv.Atoi(r.PathValue("fsid"))
		f.mu.Lock()
		for fid, fs := range f.coreOverrides[idID] {
			if fs.id == fsID {
				delete(f.coreOverrides[idID], fid)
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// Edge identities: uuid lookup and per-uuid feature-states (read).
	mux.HandleFunc("GET /api/v1/environments/{env}/edge-identities/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.Trim(r.URL.Query().Get("q"), `"`)
		f.mu.Lock()
		f.edgeLookups++
		var results []map[string]any
		if _, ok := f.edgeOverrides[q]; ok {
			results = append(results, map[string]any{"identity_uuid": "uuid-" + q, "identifier": q})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	mux.HandleFunc("GET /api/v1/environments/{env}/edge-identities/{uuid}/edge-featurestates/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		identifier := strings.TrimPrefix(r.PathValue("uuid"), "uuid-")
		fid, _ := strconv.Atoi(r.URL.Query().Get("feature"))
		f.mu.Lock()
		var results []map[string]any
		if fs := f.edgeOverrides[identifier][fid]; fs != nil {
			results = append(results, map[string]any{"enabled": fs.enabled, "feature_state_value": fs.value, "feature": fid, "featurestate_uuid": "fsu"})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	// Edge identifier-based feature-states (note the double environments, no slash).
	mux.HandleFunc("PUT /api/v1/environments/environments/{env}/edge-identities-featurestates", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ident, _ := body["identifier"].(string)
		fid := int(body["feature"].(float64))
		en, _ := body["enabled"].(bool)
		f.mu.Lock()
		if f.edgeOverrides[ident] == nil {
			f.edgeOverrides[ident] = map[int]*fakeFS{}
		}
		f.edgeOverrides[ident][fid] = &fakeFS{enabled: en, value: body["feature_state_value"]}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"feature": fid, "enabled": en, "feature_state_value": body["feature_state_value"]})
	})
	mux.HandleFunc("DELETE /api/v1/environments/environments/{env}/edge-identities-featurestates", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ident, _ := body["identifier"].(string)
		f.mu.Lock()
		if fv, ok := body["feature"].(float64); ok {
			delete(f.edgeOverrides[ident], int(fv))
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
}

// mountOrganisationItem mounts a single organisation, and organisation CRUD.
func (f *fakeInstance) mountOrganisationItem(mux *http.ServeMux) {
	// Organisation CRUD (the list route handles GET /organisations/).
	mux.HandleFunc("GET /api/v1/organisations/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		o := f.orgByID(id)
		f.mu.Unlock()
		if o == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(o)
	})
	mux.HandleFunc("POST /api/v1/organisations/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.nextOrgID++
		body["id"] = f.nextOrgID
		f.orgs = append(f.orgs, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PATCH /api/v1/organisations/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		o := f.orgByID(id)
		if o != nil {
			for k, v := range body {
				o[k] = v
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(o)
	})
	mux.HandleFunc("DELETE /api/v1/organisations/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		kept := f.orgs[:0:0]
		for _, o := range f.orgs {
			if o["id"] != id {
				kept = append(kept, o)
			}
		}
		f.orgs = kept
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
}

// mountFeatures mounts features and their multivariate options.
func (f *fakeInstance) mountFeatures(mux *http.ServeMux) {
	// Feature retrieve (feature CRUD; the list route is shared with flags).
	mux.HandleFunc("GET /api/v1/projects/{project}/features/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		var found map[string]any
		for _, it := range f.features[r.PathValue("project")] {
			if it["id"] == id {
				found = it
				break
			}
		}
		f.mu.Unlock()
		if found == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(found)
	})
	// Feature create/update/delete.
	mux.HandleFunc("POST /api/v1/projects/{project}/features/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		project := r.PathValue("project")
		f.mu.Lock()
		f.nextFeatureID++
		body["id"] = f.nextFeatureID
		f.features[project] = append(f.features[project], body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/features/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		var found map[string]any
		for _, it := range f.features[r.PathValue("project")] {
			if it["id"] == id {
				for k, v := range body {
					it[k] = v
				}
				found = it
			}
		}
		f.mu.Unlock()
		if found == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(found)
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/features/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		project := r.PathValue("project")
		f.mu.Lock()
		kept := f.features[project][:0:0]
		for _, it := range f.features[project] {
			if it["id"] != id {
				kept = append(kept, it)
			}
		}
		f.features[project] = kept
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// Multivariate options sub-resource.
	mux.HandleFunc("POST /api/v1/projects/{project}/features/{feature}/mv-options/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fid, _ := strconv.Atoi(r.PathValue("feature"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.nextMVID++
		body["id"] = f.nextMVID
		if feat := f.featureByID(r.PathValue("project"), fid); feat != nil {
			opts, _ := feat["multivariate_options"].([]any)
			feat["multivariate_options"] = append(opts, body)
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PATCH /api/v1/projects/{project}/features/{feature}/mv-options/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fid, _ := strconv.Atoi(r.PathValue("feature"))
		oid, _ := strconv.Atoi(r.PathValue("id"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		var found map[string]any
		if feat := f.featureByID(r.PathValue("project"), fid); feat != nil {
			for _, o := range feat["multivariate_options"].([]any) {
				om := o.(map[string]any)
				if om["id"] == oid {
					for k, v := range body {
						om[k] = v
					}
					found = om
				}
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(found)
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/features/{feature}/mv-options/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fid, _ := strconv.Atoi(r.PathValue("feature"))
		oid, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		if feat := f.featureByID(r.PathValue("project"), fid); feat != nil {
			opts, _ := feat["multivariate_options"].([]any)
			kept := []any{}
			for _, o := range opts {
				if o.(map[string]any)["id"] != oid {
					kept = append(kept, o)
				}
			}
			feat["multivariate_options"] = kept
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
}

// mountSegments mounts segments, and the override metadata behind the priority views.
func (f *fakeInstance) mountSegments(mux *http.ServeMux) {
	// Segments: list, retrieve, create, update, delete.
	mux.HandleFunc("GET /api/v1/projects/{project}/segments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		include := r.URL.Query().Get("include_feature_specific") == "true"
		f.mu.Lock()
		f.segListCalls++
		results := []map[string]any{}
		for _, s := range f.segments {
			if !include && s["feature"] != nil {
				continue
			}
			results = append(results, s)
		}
		f.mu.Unlock()
		// By id, as a paginated API would: the segments are held in a map, and
		// its iteration order would otherwise vary between runs.
		sort.Slice(results, func(i, j int) bool {
			return results[i]["id"].(int) < results[j]["id"].(int)
		})
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/segments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.nextSegmentID++
		id := f.nextSegmentID
		body["id"] = id
		f.segments[id] = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/segments/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		s := f.segments[id]
		f.mu.Unlock()
		if s == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(s)
	})
	mux.HandleFunc("PUT /api/v1/projects/{project}/segments/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		body["id"] = id
		f.segments[id] = body
		f.mu.Unlock()
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("DELETE /api/v1/projects/{project}/segments/{id}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("id"))
		f.mu.Lock()
		delete(f.segments, id)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// feature-segments lists a feature's segment overrides (priority + segment
	// name metadata), ordered by priority, for one environment.
	mux.HandleFunc("GET /api/v1/features/feature-segments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("environment") == "" || r.URL.Query().Get("feature") == "" {
			w.WriteHeader(http.StatusBadRequest) // both are required upstream
			return
		}
		f.mu.Lock()
		f.fsListCalls++
		f.fsInFlight++
		if f.fsInFlight > f.fsPeak {
			f.fsPeak = f.fsInFlight
		}
		delay := f.fsDelay
		rows := f.featureSegments[r.URL.Query().Get("feature")]
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		f.mu.Lock()
		f.fsInFlight--
		f.mu.Unlock()
		if rows == nil {
			rows = []map[string]any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(rows), "results": rows})
	})
	// featurestates lists a feature's states in one environment (the default
	// plus one row per segment override), with the typed value wire form.
	mux.HandleFunc("GET /api/v1/features/featurestates/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("environment") == "" {
			w.WriteHeader(http.StatusBadRequest) // required upstream
			return
		}
		f.mu.Lock()
		f.stListCalls++
		rows := f.featureStates[r.URL.Query().Get("feature")]
		f.mu.Unlock()
		if rows == nil {
			rows = []map[string]any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(rows), "results": rows})
	})
	// The environment featurestates list with ?anyIdentity= is how core
	// identity overrides for one feature are enumerated.
	mux.HandleFunc("GET /api/v1/environments/{env}/featurestates/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("anyIdentity") == "" {
			w.WriteHeader(http.StatusBadRequest) // the CLI only uses the identity mode
			return
		}
		featureID, _ := strconv.Atoi(r.URL.Query().Get("feature"))
		f.mu.Lock()
		identifiers := make([]string, 0, len(f.coreIdentities))
		for identifier := range f.coreIdentities {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		results := []map[string]any{}
		for _, identifier := range identifiers {
			id := f.coreIdentities[identifier]
			if fs := f.coreOverrides[id][featureID]; fs != nil {
				results = append(results, map[string]any{
					"id": fs.id, "enabled": fs.enabled, "feature_state_value": fs.value,
					"identity": map[string]any{"id": id, "identifier": identifier},
					"feature":  featureID,
				})
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})
	// Edge identity overrides: no trailing slash, no pagination.
	mux.HandleFunc("GET /api/v1/environments/{env}/edge-identity-overrides", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		featureID, _ := strconv.Atoi(r.URL.Query().Get("feature"))
		f.mu.Lock()
		identifiers := make([]string, 0, len(f.edgeOverrides))
		for identifier := range f.edgeOverrides {
			identifiers = append(identifiers, identifier)
		}
		sort.Strings(identifiers)
		results := []map[string]any{}
		for _, identifier := range identifiers {
			if fs := f.edgeOverrides[identifier][featureID]; fs != nil {
				results = append(results, map[string]any{
					"identifier": identifier,
					"feature_state": map[string]any{
						"enabled": fs.enabled, "feature_state_value": fs.value, "feature": featureID,
					},
				})
			}
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
}

// mountSDK mounts the SDK API: the two endpoints the Flagsmith SDK evaluates flags over, and the echo used to exercise `flagsmith api`.
func (f *fakeInstance) mountSDK(mux *http.ServeMux) {
	// The SDK API: the two endpoints the Flagsmith SDK evaluates flags over.
	mux.HandleFunc("GET /api/v1/flags/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.sdkUserAgents = append(f.sdkUserAgents, r.Header.Get("User-Agent"))
		f.sdkKeys = append(f.sdkKeys, r.Header.Get("X-Environment-Key"))
		status, flags, delay := f.sdkStatus, f.sdkEnvFlags[r.Header.Get("X-Environment-Key")], f.sdkDelay
		f.mu.Unlock()
		time.Sleep(delay)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if flags == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(flags)
	})
	mux.HandleFunc("POST /api/v1/identities/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		identifier, _ := body["identifier"].(string)
		f.mu.Lock()
		f.sdkUserAgents = append(f.sdkUserAgents, r.Header.Get("User-Agent"))
		f.sdkKeys = append(f.sdkKeys, r.Header.Get("X-Environment-Key"))
		f.lastIdentify = body
		status, flags, delay := f.sdkStatus, f.sdkEnvFlags[r.Header.Get("X-Environment-Key")], f.sdkDelay
		if override, ok := f.sdkIdentityFlags[identifier]; ok {
			flags = override
		}
		f.mu.Unlock()
		time.Sleep(delay)
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if flags == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"identifier": identifier, "traits": body["traits"], "flags": flags,
		})
	})
	// echo reflects the request back, for exercising `flagsmith api`.
	mux.HandleFunc("/api/v1/echo/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) && r.Header.Get("X-Environment-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(map[string]any{
			"method":        r.Method,
			"path":          r.URL.Path,
			"query":         r.URL.RawQuery,
			"authorization": r.Header.Get("Authorization"),
			"envkey":        r.Header.Get("X-Environment-Key"),
			"content_type":  r.Header.Get("Content-Type"),
			"custom":        r.Header.Get("X-Custom"),
			"body":          string(body),
		})
	})
}
