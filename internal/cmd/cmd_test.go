package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/zalando/go-keyring"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
)

const (
	masterKey   = "AbCd1234.0123456789abcdefABCDEF01234567"
	bearerToken = "envBearerToken0000000000000000"
	oauthAccess = "cmd-access"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func isolateStorage(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("AppData", tmp)
}

// resetFlags clears package-level flag state that would otherwise leak
// between Execute calls on the shared rootCmd.
func resetFlags() {
	reset := func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	// Reset local flags on every command too — cobra's --help is a local
	// flag and would otherwise stay sticky across Execute calls.
	var resetAll func(c *cobra.Command)
	resetAll = func(c *cobra.Command) {
		c.Flags().VisitAll(reset)
		for _, sub := range c.Commands() {
			resetAll(sub)
		}
	}
	resetAll(rootCmd)
	noBrowser = false
	// StringArray flags do not reset cleanly via Set(DefValue) — pflag appends
	// the "[]" default as a literal element — so clear them explicitly.
	apiHeaderFlags = nil
	apiFieldFlags = nil
	apiRawFields = nil
}

func run(stdin string, args ...string) (string, error) {
	resetFlags()
	if args == nil {
		args = []string{} // nil would make cobra fall back to os.Args
	}
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// fakeInstance is a Flagsmith instance stub covering the endpoints the auth
// slice touches. Organisations answers to the master key, the env bearer
// token, and the OAuth access token; users/me only to bearer credentials.
type fakeInstance struct {
	srv *httptest.Server

	mu             sync.Mutex
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
	tokenPosts     int                         // count of POST /o/token/ (refresh) calls
	lastUpdate     map[string]any              // last update-flag-v2 request body
	lastDelete     map[string]any              // last delete-segment-override request body
	workflowGated  bool                        // when true, update endpoints return 403
	segmentMissing bool                        // when true, delete-segment-override returns 404

	useEdge           bool                       // GET /projects/{id}/ use_edge_identities
	coreIdentities    map[string]int             // identifier -> identity id
	coreOverrides     map[int]map[int]*fakeFS    // identity id -> feature id -> state
	edgeOverrides     map[string]map[int]*fakeFS // identifier -> feature id -> state
	nextFSID          int
	lastIdentityWrite map[string]any // last core identity FS create/update body
	lastEdgeWrite     map[string]any // last edge identifier PUT body
	lastEdgeDelete    map[string]any // last edge identifier DELETE body

	segments        map[int]map[string]any // segment id -> segment
	nextSegmentID   int
	lastSegmentBody map[string]any // last segment create/update body

	nextFeatureID   int
	lastFeatureBody map[string]any // last feature create/update body
	nextMVID        int
	lastMVBody      map[string]any // last mv-options create/update body
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
	f := &fakeInstance{
		orgs: []map[string]any{{"id": 3, "name": "Acme"}},
		projects: map[string][]map[string]any{
			"3": {{"id": 101, "name": "acme-api"}},
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
		nextSegmentID: 100,
		nextFeatureID: 900,
		nextMVID:      300,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
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
	authorized := func(r *http.Request) bool {
		a := r.Header.Get("Authorization")
		return a == "Api-Key "+masterKey || a == "Bearer "+oauthAccess || a == "Bearer "+bearerToken
	}
	mux.HandleFunc("GET /api/v1/organisations/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
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
		projects := f.projects[r.URL.Query().Get("organisation")]
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(projects), "results": projects})
	})
	mux.HandleFunc("POST /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Name         string `json:"name"`
			Organisation int    `json:"organisation"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.created = append(f.created, body.Name)
		f.mu.Unlock()
		f.mu.Lock()
		if f.envs["999"] == nil {
			f.envs["999"] = []map[string]any{} // created projects start empty
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 999, "name": body.Name})
	})
	mux.HandleFunc("GET /api/v1/environments/", func(w http.ResponseWriter, r *http.Request) {
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
		var body struct {
			Name    string `json:"name"`
			Project int    `json:"project"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.createdEnvs = append(f.createdEnvs, body.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": body.Name, "api_key": "createdEnvKey00000000"})
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/features/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.lastFeatEnv = r.URL.Query().Get("environment")
		f.lastFeatSeg = r.URL.Query().Get("segment")
		f.lastFeatArch = r.URL.Query().Get("is_archived")
		items := f.features[r.PathValue("project")]
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
		if f.workflowGated {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.lastUpdate = body
		f.applyFlagUpdate(body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/experiments/environments/{env}/delete-segment-override/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.segmentMissing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.lastDelete = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// Project retrieve — carries use_edge_identities.
	mux.HandleFunc("GET /api/v1/projects/{project}/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(r.PathValue("project"))
		f.mu.Lock()
		edge := f.useEdge
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "acme-api", "use_edge_identities": edge})
	})
	// Core identities: identifier lookup and create.
	mux.HandleFunc("GET /api/v1/environments/{env}/identities/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.Trim(r.URL.Query().Get("q"), `"`)
		f.mu.Lock()
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
		f.lastIdentityWrite = body
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
		f.lastIdentityWrite = body
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
		f.lastEdgeWrite = body
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
		f.lastEdgeDelete = body
		if fv, ok := body["feature"].(float64); ok {
			delete(f.edgeOverrides[ident], int(fv))
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// Feature retrieve (feature CRUD; the list route above is shared with flags).
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
		f.lastFeatureBody = body
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
		f.lastFeatureBody = body
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
		f.lastMVBody = body
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
		f.lastMVBody = body
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
	// Segments: list, retrieve, create, update, delete.
	mux.HandleFunc("GET /api/v1/projects/{project}/segments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		include := r.URL.Query().Get("include_feature_specific") == "true"
		f.mu.Lock()
		results := []map[string]any{}
		for _, s := range f.segments {
			if !include && s["feature"] != nil {
				continue
			}
			results = append(results, s)
		}
		f.mu.Unlock()
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
		f.lastSegmentBody = body
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
		f.lastSegmentBody = body
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
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
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
			for _, o := range overrides {
				ov, _ := o.(map[string]any)
				segEnabled, _ := ov["enabled"].(bool)
				segVal, _ := ov["value"].(map[string]any)
				item["segment_feature_state"] = map[string]any{
					"enabled": segEnabled, "feature_state_value": scalarFromWire(segVal),
				}
			}
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

// featuresEnv returns the ?environment= value the /features/ endpoint last saw.
func (f *fakeInstance) featuresEnv() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastFeatEnv
}

// featuresSeg returns the ?segment= value the /features/ endpoint last saw.
func (f *fakeInstance) featuresSeg() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastFeatSeg
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

func (f *fakeInstance) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenPosts
}

func TestFlagListResolvesEnvironmentName(t *testing.T) {
	t.Run("name resolved to its id for the features query", func(t *testing.T) {
		// Given a config naming the environment, with admin credentials
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "Development", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list")

		// Then — "Development" (id 1) drives the features environment filter
		if err != nil {
			t.Fatalf("flag list: %v\noutput: %s", err, out)
		}
		if got := f.featuresEnv(); got != "1" {
			t.Errorf("features environment = %q, want the resolved Development id (1)", got)
		}
	})

	t.Run("without credentials the command errors", func(t *testing.T) {
		// Given a name but no admin credentials — flag list is Admin-only now
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "Development", "apiUrl": "`+f.srv.URL+`"}`)

		// When / Then
		if _, err := run("", "flag", "list"); !errors.Is(err, auth.ErrNotLoggedIn) {
			t.Errorf("err = %v, want ErrNotLoggedIn", err)
		}
	})

	t.Run("ambiguous name exits 2 without a TTY", func(t *testing.T) {
		// Given two environments sharing a name
		isolateStorage(t)
		f := newFakeInstance(t)
		f.envs["101"] = []map[string]any{
			{"id": 1, "name": "Staging", "api_key": "stagingKeyA0000000000"},
			{"id": 2, "name": "Staging", "api_key": "stagingKeyB0000000000"},
		}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "Staging", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When / Then
		_, err := run("", "flag", "list")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("err = %v, want an ambiguity usage error (exit 2)", err)
		}
	})

	t.Run("a key reference resolves to its environment", func(t *testing.T) {
		// Given the reference is a client-side key present in the project
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "K2mVsGdXhZ8kQqZ9pJmNbJ", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		if _, err := run("", "flag", "list"); err != nil {
			t.Fatalf("flag list: %v", err)
		}

		// Then — the key mapped to Production (id 2) for the features query
		if got := f.featuresEnv(); got != "2" {
			t.Errorf("features environment = %q, want the Production id (2)", got)
		}
	})
}

// commandShapes are the two supported spellings of login/logout:
// top-level and under `auth`.
var commandShapes = []struct {
	name   string
	prefix []string
}{
	{"top-level", nil},
	{"auth alias", []string{"auth"}},
}

// shapeArgs prepends a shape prefix to a command line.
func shapeArgs(prefix []string, args ...string) []string {
	return append(append([]string{}, prefix...), args...)
}

func TestBrowserLoginFlow(t *testing.T) {
	for _, shape := range commandShapes {
		t.Run(shape.name, func(t *testing.T) {
			testBrowserLoginFlow(t, shape.prefix)
		})
	}
}

func testBrowserLoginFlow(t *testing.T, prefix []string) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	resetFlags()
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(shapeArgs(prefix, "login", "--api", f.srv.URL, "--no-browser"))
	done := make(chan error, 1)
	go func() { done <- rootCmd.Execute() }()

	authURLPattern := regexp.MustCompile(`https?://\S+/oauth/authorize/\?\S+`)
	var q url.Values
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && q == nil {
		if m := authURLPattern.FindString(out.String()); m != "" {
			u, err := url.Parse(m)
			if err != nil {
				t.Fatal(err)
			}
			q = u.Query()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q == nil {
		t.Fatalf("login never printed an authorization URL; output: %q", out.String())
	}
	if _, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state"))); err != nil {
		t.Fatal(err)
	}

	// Then
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("login: %v\noutput: %s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not return")
	}
	if got := out.String(); !strings.Contains(got, "✓ Logged in to "+f.srv.URL+" as kim@example.com") ||
		!strings.Contains(got, "keychain") {
		t.Errorf("login output = %q", got)
	}

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"kim@example.com", "keychain", "Acme"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}

	// When
	tokenOut, err := run("", "auth", "token", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if strings.TrimSpace(tokenOut) != oauthAccess {
		t.Errorf("auth token output = %q, want the access token", tokenOut)
	}

	// When
	logoutOut, err := run("", shapeArgs(prefix, "logout", "--api", f.srv.URL)...)

	// Then
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(logoutOut, "Logged out of "+f.srv.URL) {
		t.Errorf("logout output = %q", logoutOut)
	}
	if f.revokeCount() != 1 {
		t.Errorf("revocations = %d, want 1", f.revokeCount())
	}

	// When / Then
	if _, err := run("", "auth", "status", "--api", f.srv.URL); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("auth status after logout = %v, want ErrNotLoggedIn", err)
	}

	// When / Then
	logoutOut, err = run("", shapeArgs(prefix, "logout", "--api", f.srv.URL)...)
	if err != nil || !strings.Contains(logoutOut, "Not logged in") {
		t.Errorf("second logout = (%q, %v), want a friendly no-op", logoutOut, err)
	}
}

func TestAPIFlagWorksInAnyPosition(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When / Then
	var outputs []string
	for _, args := range [][]string{
		{"auth", "status", "--api", f.srv.URL}, // after the subcommand (02 §8 shape)
		{"--api", f.srv.URL, "auth", "status"}, // before the subcommand
		{"auth", "--api", f.srv.URL, "status"}, // between nested subcommands
	} {
		out, err := run("", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		outputs = append(outputs, out)
	}
	if outputs[0] != outputs[1] || outputs[1] != outputs[2] {
		t.Errorf("flag position changed behaviour:\n%q\n%q\n%q", outputs[0], outputs[1], outputs[2])
	}
	if !strings.Contains(outputs[0], "Acme") {
		t.Errorf("output = %q, want the fake instance to have been hit", outputs[0])
	}
}

func TestAPIURLFlagWithHiddenAlias(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	canonical, err := run("", "auth", "status", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("--api-url: %v", err)
	}
	if !strings.Contains(canonical, "Acme") {
		t.Errorf("output = %q, want the fake instance to have been hit", canonical)
	}

	// When
	alias, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("--api alias: %v", err)
	}
	if alias != canonical {
		t.Errorf("alias output differs from canonical:\n%q\n%q", alias, canonical)
	}

	// When / Then — the alias stays out of help
	helpOut, err := run("", "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(helpOut, "--api-url") {
		t.Errorf("help = %q, want --api-url documented", helpOut)
	}
	if strings.Contains(helpOut, "--api ") {
		t.Errorf("help = %q, want the --api alias hidden", helpOut)
	}
}

// tempRepo creates a git repo dir, chdirs into it, and returns its path.
func tempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "flagsmith.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func configJSON(t *testing.T, args ...string) map[string]map[string]any {
	t.Helper()
	out, err := run("", append([]string{"config", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("config --json: %v\noutput: %s", err, out)
	}
	parsed := map[string]map[string]any{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	return parsed
}

func TestConfigCommand(t *testing.T) {
	t.Run("defaults with no config file", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When
		got := configJSON(t)

		// Then
		if v := got["apiUrl"]; v["value"] != "https://api.flagsmith.com" || v["source"] != "default" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://edge.api.flagsmith.com" || v["source"] != "default" {
			t.Errorf("sdkApiUrl = %v", v)
		}
		if v := got["configPath"]; v["value"] != nil {
			t.Errorf("configPath = %v, want null", v)
		}
		if v := got["project"]; v["value"] != nil {
			t.Errorf("project = %v, want null", v)
		}
	})

	t.Run("file, env, cli sources with cache names", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		path := writeConfig(t, root, `{
			"project": 12345,
			"organisation": 3,
			"environment": "K2mVsGdXhZ8kQqZ9pJmNbJ",
			"apiUrl": "https://acme.example"
		}`)
		if err := cache.Merge("https://acme.example", &cache.Names{
			Organisations: map[string]string{"3": "Acme"},
			Projects:      map[string]string{"12345": "my-app"},
			Environments:  map[string]string{"StagingKey123": "Staging"},
		}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FLAGSMITH_ENVIRONMENT", "StagingKey123")

		// When
		got := configJSON(t, "--sdk-api-url", "https://flags.example")

		// Then
		if v := got["configPath"]; v["value"] != path || v["source"] != "default" {
			t.Errorf("configPath = %v", v)
		}
		if v := got["project"]; v["value"] != float64(12345) || v["name"] != "my-app" || v["source"] != "config" {
			t.Errorf("project = %v", v)
		}
		if v := got["organisation"]; v["value"] != float64(3) || v["name"] != "Acme" || v["source"] != "config" {
			t.Errorf("organisation = %v", v)
		}
		if v := got["environment"]; v["value"] != "StagingKey123" || v["name"] != "Staging" || v["source"] != "env" {
			t.Errorf("environment = %v", v)
		}
		if v := got["apiUrl"]; v["value"] != "https://acme.example" || v["source"] != "config" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://flags.example" || v["source"] != "cli" {
			t.Errorf("sdkApiUrl = %v", v)
		}
	})

	t.Run("sdk api url follows a set api url", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When
		got := configJSON(t, "--api-url", "https://self.example")

		// Then
		if v := got["apiUrl"]; v["source"] != "cli" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://self.example" || v["source"] != "default" {
			t.Errorf("sdkApiUrl = %v, want it to follow apiUrl as default", v)
		}
	})

	t.Run("explicit config path via flag", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)
		elsewhere := t.TempDir()
		path := writeConfig(t, elsewhere, `{"project": 7}`)

		// When
		got := configJSON(t, "--config-path", path)

		// Then
		if v := got["configPath"]; v["value"] != path || v["source"] != "cli" {
			t.Errorf("configPath = %v", v)
		}
		if v := got["project"]; v["value"] != float64(7) {
			t.Errorf("project = %v", v)
		}
	})

	t.Run("human output shows values and sources", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 12345}`)

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"12345", "config", "https://api.flagsmith.com", "default", "flagsmith.json"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("unknown config fields warn on stderr", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 1, "enviroment": "typo"}`)

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "unknown field") {
			t.Errorf("output = %q, want an unknown-field warning", out)
		}
	})

	t.Run("non-numeric FLAGSMITH_PROJECT is taken as a name", func(t *testing.T) {
		// Given a non-digit value — a project name, not an error
		isolateStorage(t)
		tempRepo(t)
		t.Setenv("FLAGSMITH_PROJECT", "my-app")

		// When — config is offline and resolves nothing
		got := configJSON(t)

		// Then — recorded verbatim as the project reference, sourced from env
		if v := got["project"]; v["value"] != "my-app" || v["source"] != "env" {
			t.Errorf("project = %v, want the name carried through from the env var", v)
		}
	})

	t.Run("server-side key via -e is rejected", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When / Then
		if _, err := run("", "config", "-e", "ser.AbCd"); err == nil ||
			!strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
			t.Errorf("err = %v, want a pointer to FLAGSMITH_ENVIRONMENT_KEY", err)
		}
	})
}

// fakeTTY makes prompts believe stdin is a terminal for one test.
func fakeTTY(t *testing.T) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = orig })
}

func loadWritten(t *testing.T, dir string) *config.File {
	t.Helper()
	f, _, err := config.Load(filepath.Join(dir, "flagsmith.json"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// refID returns a config reference's ID, or 0 when unset — nil-safe for terse
// test assertions.
func refID(r *config.Ref) int {
	if r == nil {
		return 0
	}
	return r.ID
}

func TestInitNonInteractive(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	out, err := run("", "init", "--api-url", f.srv.URL,
		"--project", "12345", "--environment", "WqXhZk8sVY3dGgTqZ9pJmN", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"Verified access", "Wrote flagsmith.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	written := loadWritten(t, root)
	if refID(written.Project) != 12345 || written.Environment != "WqXhZk8sVY3dGgTqZ9pJmN" {
		t.Errorf("written = %+v", written)
	}
	if written.APIURL != f.srv.URL {
		t.Errorf("apiUrl = %q, want the non-SaaS instance recorded", written.APIURL)
	}
	if !strings.Contains(written.Schema, "schema/flagsmith.json") {
		t.Errorf("$schema = %q", written.Schema)
	}
	if got := cache.Load(f.srv.URL); got.Environments["WqXhZk8sVY3dGgTqZ9pJmN"] != "Development" {
		t.Errorf("cache = %+v, want environment names seeded", got)
	}
}

func TestInitNonInteractiveRequiresProject(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--yes")

	// Then
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usage error (exit 2)", err)
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Errorf("err = %v, want it to name --project", err)
	}
}

func TestInitNoCredentials(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345", "--yes")

	// Then
	if err == nil {
		t.Fatal("expected an error with no credentials")
	}
	for _, want := range []string{"FLAGSMITH_API_KEY", "flagsmith login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestInitRefusesOverwriteWithoutYes(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345")

	// Then — a promptable confirmation missing its flag is exit 2, naming it
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v, want a usage error (exit 2) naming --yes", err)
	}
}

func TestInitCreateProjectFlag(t *testing.T) {
	// Given — single org, non-interactive
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — create both a project and its environment purely via flags
	out, err := run("", "init", "--api-url", f.srv.URL,
		"--create-project", "acme-new", "--create-environment", "Development", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	f.mu.Lock()
	created, createdEnvs := append([]string{}, f.created...), append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(created) != 1 || created[0] != "acme-new" {
		t.Errorf("created projects = %v", created)
	}
	if len(createdEnvs) != 1 || createdEnvs[0] != "Development" {
		t.Errorf("created envs = %v", createdEnvs)
	}
	if w := loadWritten(t, root); refID(w.Project) != 999 || w.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", w)
	}
}

func TestInitCreateProjectRequiresOrgWhenMultiOrg(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	f.orgs = []map[string]any{{"id": 3, "name": "Acme"}, {"id": 7, "name": "Beta"}}
	tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — no --organisation, no TTY
	_, err := run("", "init", "--api-url", f.srv.URL, "--create-project", "x", "--yes")

	// Then — exit 2, naming the flag that resolves it
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "organisation") {
		t.Errorf("err = %v, want a usage error naming --organisation", err)
	}
}

func TestInitCreateProjectConflictsWithProject(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When / Then
	_, err := run("", "init", "--api-url", f.srv.URL, "--project", "101", "--create-project", "x", "--yes")
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v, want a mutual-exclusion usage error", err)
	}
}

func TestInitCreateEnvironmentFlag(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	out, err := run("", "init", "--api-url", f.srv.URL,
		"--project", "101", "--create-environment", "Staging", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	f.mu.Lock()
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(createdEnvs) != 1 || createdEnvs[0] != "Staging" {
		t.Errorf("created envs = %v", createdEnvs)
	}
	if w := loadWritten(t, root); w.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", w)
	}
}

func TestInitCreateEnvironmentConflictsWithEnvironment(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When / Then
	_, err := run("", "init", "--api-url", f.srv.URL,
		"--project", "101", "--environment", "WqXhZk8sVY3dGgTqZ9pJmN", "--create-environment", "x", "--yes")
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v, want a mutual-exclusion usage error", err)
	}
}

func TestPromptSelfGuardsWithoutTTY(t *testing.T) {
	// Given — prompting disallowed (no TTY)
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = orig })
	yesFlag = false
	initPrompts(rootCmd)

	// When / Then — a prompt refuses and names its flag, exit 2
	if _, err := selectPrompt(rootCmd, "project", "Project", []string{"a"}, 0); err == nil {
		t.Error("selectPrompt should refuse without a TTY")
	} else {
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--project") {
			t.Errorf("err = %v, want usage error naming --project", err)
		}
	}
}

func TestInitInteractiveMultiOrg(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	f.orgs = []map[string]any{{"id": 3, "name": "Acme"}, {"id": 7, "name": "Beta"}}
	f.projects["7"] = []map[string]any{{"id": 202, "name": "beta-app"}}
	f.envs["202"] = []map[string]any{
		{"id": 9, "name": "Development", "api_key": "BetaDevKey00000000000"},
	}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick org 2 (Beta), project 1 (beta-app), environment 1 (Development)
	out, err := run("2\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"Organisation", "Project", "environment", "Wrote flagsmith.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	written := loadWritten(t, root)
	if refID(written.Project) != 202 || written.Environment != "BetaDevKey00000000000" {
		t.Errorf("written = %+v", written)
	}
	if refID(written.Organisation) != 7 {
		t.Errorf("organisation = %d, want 7 recorded for a multi-org user", refID(written.Organisation))
	}
}

func TestInitInteractiveCreateProject(t *testing.T) {
	// Given — single org, one existing project; option 2 is "create new"
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — choose create (option 2), accept default project name, then
	// accept the default environment name (Development) for the empty project
	out, err := run("2\n\n\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Created project") {
		t.Errorf("output = %q, want a created-project line", out)
	}
	f.mu.Lock()
	created := append([]string{}, f.created...)
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(created) != 1 || created[0] != filepath.Base(root) {
		t.Errorf("created = %v, want the cwd name as the default project name", created)
	}
	if len(createdEnvs) != 1 || createdEnvs[0] != "Development" {
		t.Errorf("createdEnvs = %v, want a Development environment created", createdEnvs)
	}
	if written := loadWritten(t, root); refID(written.Project) != 999 || written.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", written)
	}
}

func TestInitEmptyProjectPromptsEnvironmentCreation(t *testing.T) {
	// Given — an existing accessible project with no environments
	isolateStorage(t)
	f := newFakeInstance(t)
	f.envs["101"] = []map[string]any{}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick the existing (empty) project, accept the env-name default
	out, err := run("1\n\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "environment") {
		t.Errorf("output = %q, want an environment-creation prompt", out)
	}
	f.mu.Lock()
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(createdEnvs) != 1 || createdEnvs[0] != "Development" {
		t.Errorf("createdEnvs = %v, want a Development environment created", createdEnvs)
	}
	if written := loadWritten(t, root); written.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", written)
	}
}

func TestInitEmptyProjectNonInteractiveSkipsEnvironment(t *testing.T) {
	// Given — non-interactive init of an empty project
	isolateStorage(t)
	f := newFakeInstance(t)
	f.envs["101"] = []map[string]any{}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — no TTY, no environment given: don't create anything silently
	out, err := run("", "init", "--api-url", f.srv.URL, "--project", "101", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	f.mu.Lock()
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(createdEnvs) != 0 {
		t.Errorf("createdEnvs = %v, want none created without a TTY", createdEnvs)
	}
	if written := loadWritten(t, root); written.Environment != "" {
		t.Errorf("written = %+v, want no environment", written)
	}
}

func TestInitPreservesExistingOrganisation(t *testing.T) {
	// Given — a single-org user re-initialising a file that records its org
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 12345, "organisation": 3, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — non-interactive re-init that doesn't touch the organisation
	out, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345", "--yes")

	// Then — the organisation must survive, not be dropped
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if written := loadWritten(t, root); refID(written.Organisation) != 3 {
		t.Errorf("organisation = %d, want 3 preserved", refID(written.Organisation))
	}
}

func TestInitReinitReoffersOrgPicker(t *testing.T) {
	// Given — a multi-org user re-initialising; current org is NOT first
	isolateStorage(t)
	f := newFakeInstance(t)
	f.orgs = []map[string]any{{"id": 7, "name": "Beta"}, {"id": 3, "name": "Acme"}}
	f.projects["7"] = []map[string]any{{"id": 202, "name": "beta-app"}}
	f.envs["202"] = []map[string]any{{"id": 9, "name": "Development", "api_key": "BetaDevKey00000000000"}}
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 101, "organisation": 3, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — accept the org picker default, then project 1, environment 1
	out, err := run("\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then — the picker was offered, and its default (the current org) held
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Organisation") {
		t.Errorf("output = %q, want the org picker re-offered on re-init", out)
	}
	if written := loadWritten(t, root); refID(written.Organisation) != 3 {
		t.Errorf("organisation = %d, want the pre-selected current org (3)", refID(written.Organisation))
	}
}

func TestInitInvalidChoiceReprompts(t *testing.T) {
	// Given — single org; project prompt has 2 options (acme-api, create new)
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — an invalid choice, then project 1, then environment 1
	out, err := run("99\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "between 1 and") {
		t.Errorf("output = %q, want a re-prompt instead of a crash", out)
	}
	if written := loadWritten(t, root); refID(written.Project) != 101 {
		t.Errorf("written = %+v", written)
	}
}

func TestInitReinitShowsDiffAndConfirms(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 12345, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick project 1 (acme-api, 101), env 2 (Production), confirm
	out, err := run("1\n2\ny\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{`- "project": 12345`, `+ "project": 101`, "Write changes?"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	if written := loadWritten(t, root); refID(written.Project) != 101 || written.Environment != "K2mVsGdXhZ8kQqZ9pJmNbJ" {
		t.Errorf("written = %+v", written)
	}
}

func TestBareInvocationNudgesInit(t *testing.T) {
	// Given
	isolateStorage(t)
	tempRepo(t)

	// When
	out, err := run("")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "flagsmith init") {
		t.Errorf("output = %q, want a nudge towards flagsmith init", out)
	}
}

// runSplit is run with stdout and stderr captured separately.
func runSplit(stdin string, args ...string) (string, string, error) {
	resetFlags()
	if args == nil {
		args = []string{}
	}
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestJSONIsGlobal(t *testing.T) {
	t.Run("auth status --json", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "auth", "status", "--api-url", f.srv.URL, "--json")

		// Then
		if err != nil {
			t.Fatalf("auth status --json: %v", err)
		}
		var status struct {
			APIURL        string `json:"apiUrl"`
			Kind          string `json:"kind"`
			Organisations []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"organisations"`
			Source string `json:"credentialSource"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if status.Kind != "master" || status.Source != "$FLAGSMITH_API_KEY" ||
			len(status.Organisations) != 1 || status.Organisations[0].Name != "Acme" {
			t.Errorf("status = %+v", status)
		}
	})

	t.Run("auth token --json", func(t *testing.T) {
		// Given
		isolateStorage(t)
		newFakeInstance(t)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "auth", "token", "--json")

		// Then
		if err != nil {
			t.Fatalf("auth token --json: %v", err)
		}
		var token struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(out), &token); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if token.Token != masterKey {
			t.Errorf("token = %q", token.Token)
		}
	})

	t.Run("FLAGSMITH_JSON_OUTPUT enables JSON", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)
		t.Setenv("FLAGSMITH_JSON_OUTPUT", "1")

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("output = %q, want JSON via env var", out)
		}
	})
}

func TestWarningsGoToStderr(t *testing.T) {
	// Given
	isolateStorage(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1, "enviroment": "typo"}`)

	// When
	stdout, stderr, err := runSplit("", "config")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "unknown field") {
		t.Errorf("stdout = %q — warnings belong on stderr", stdout)
	}
	if !strings.Contains(stderr, "unknown field") {
		t.Errorf("stderr = %q, want the unknown-field warning", stderr)
	}
}

func TestLogoutRevokeWarningGoesToStderr(t *testing.T) {
	// Given — a stored OAuth session whose instance is unreachable
	isolateStorage(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: "http://127.0.0.1:1",
		AccessToken: "x", RefreshToken: "y",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// When
	stdout, stderr, err := runSplit("", "logout", "--api-url", "http://127.0.0.1:1")

	// Then — logout is a mutation: confirmation and warning on stderr,
	// stdout empty (no data result).
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Logged out") {
		t.Errorf("stderr = %q, want the confirmation", stderr)
	}
	if !strings.Contains(stderr, "Warning") {
		t.Errorf("stderr = %q, want the revoke warning", stderr)
	}
}

func TestBrowserLoginWithoutTTYNeverOpensBrowser(t *testing.T) {
	// Given — no TTY and no --no-browser flag; opening a real browser here
	// would violate 02 (and hijack the test machine)
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	resetFlags()
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"login", "--api-url", f.srv.URL})
	done := make(chan error, 1)
	go func() { done <- rootCmd.Execute() }()

	authURLPattern := regexp.MustCompile(`https?://\S+/oauth/authorize/\?\S+`)
	var q url.Values
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && q == nil {
		if m := authURLPattern.FindString(out.String()); m != "" {
			u, err := url.Parse(m)
			if err != nil {
				t.Fatal(err)
			}
			q = u.Query()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q == nil {
		t.Fatalf("no authorization URL printed; output: %q", out.String())
	}
	if _, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state"))); err != nil {
		t.Fatal(err)
	}

	// Then
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("login: %v\noutput: %s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not return")
	}
}

func TestBrowserLoginRefusesNoInput(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When — --yes promises zero interaction; waiting on a browser is interaction
	_, err := run("", "login", "--api-url", f.srv.URL, "--yes")

	// Then
	if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_API_KEY") {
		t.Errorf("err = %v, want a refusal pointing at FLAGSMITH_API_KEY", err)
	}
}

func TestResolveCredentialRefreshesOnceUnderConcurrency(t *testing.T) {
	// Given an expired OAuth session and many concurrent callers
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		APIURL:       f.srv.URL,
		AccessToken:  "stale",
		RefreshToken: "cmd-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	apiURL = f.srv.URL
	resetCredentialCache()

	// When — N goroutines resolve the credential simultaneously
	const n = 20
	var wg sync.WaitGroup
	results := make([]*activeCredential, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = resolveCredential(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	// Then — the session refreshed exactly once, shared by every caller
	if got := f.refreshCount(); got != 1 {
		t.Errorf("refresh POSTs = %d, want exactly 1 (herd collapsed)", got)
	}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i].token != oauthAccess {
			t.Errorf("caller %d token = %q, want the refreshed access token", i, results[i].token)
		}
	}
}

func TestProjectNameResolvesForEnvironmentLookup(t *testing.T) {
	// Given a config naming both the project and the environment
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": "acme-api", "environment": "Development", "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — flag list needs the project ID to list environments by name
	out, err := run("", "flag", "list")

	// Then — "acme-api" resolved to project 101, "Development" to env id 1
	if err != nil {
		t.Fatalf("flag list: %v\noutput: %s", err, out)
	}
	if got := f.featuresEnv(); got != "1" {
		t.Errorf("features environment = %q, want env id resolved via the named project", got)
	}
}

func TestUnknownProjectNameErrors(t *testing.T) {
	// Given a project name that matches nothing in the organisation
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": "ghost", "environment": "Development", "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When / Then — the miss surfaces when a command needs the project
	_, err := run("", "flag", "list")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("err = %v, want a not-found error naming the project", err)
	}
}

func TestFlagsList(t *testing.T) {
	t.Run("human table with count", func(t *testing.T) {
		// Given — admin credentials, project and environment in config
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list")

		// Then — the richer Admin columns, values, and count
		if err != nil {
			t.Fatalf("flags list: %v\noutput: %s", err, out)
		}
		for _, want := range []string{
			"NAME", "TYPE", "STATE", "VALUE", "LIFECYCLE",
			"onboarding_banner", "standard", "on", "live",
			"max_items", "25", "2 flags",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("json is a curated array with state hoisted", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list", "--json")

		// Then — a bare array of the curated shape, no dashboard noise
		if err != nil {
			t.Fatal(err)
		}
		var flags []map[string]any
		if err := json.Unmarshal([]byte(out), &flags); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if len(flags) != 2 {
			t.Fatalf("flags = %+v", flags)
		}
		if flags[0]["feature"] != "onboarding_banner" || flags[0]["enabled"] != true {
			t.Errorf("item = %+v, want curated fields at top level", flags[0])
		}
		if _, ok := flags[0]["environment_feature_state"]; ok {
			t.Errorf("item = %+v, want the raw nested state dropped", flags[0])
		}
		if _, ok := flags[0]["lifecycle_stage"]; !ok {
			t.Errorf("item = %+v, want lifecycle_stage present", flags[0])
		}
	})

	t.Run("empty", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		f.features["101"] = []map[string]any{}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "No flags") {
			t.Errorf("output = %q, want a no-flags message", out)
		}
	})

	t.Run("environment from FLAGSMITH_ENVIRONMENT_KEY fallback", func(t *testing.T) {
		// Given — no config environment; a client-side key via the env var
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)
		t.Setenv("FLAGSMITH_ENVIRONMENT_KEY", "WqXhZk8sVY3dGgTqZ9pJmN")

		// When
		out, err := run("", "flag", "list")

		// Then
		if err != nil {
			t.Fatalf("flags list: %v\noutput: %s", err, out)
		}
		if !strings.Contains(out, "onboarding_banner") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("no environment errors", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		_, err := run("", "flag", "list")

		// Then
		if err == nil || !strings.Contains(err.Error(), "environment") {
			t.Errorf("err = %v, want a missing-environment error", err)
		}
	})

	t.Run("off state and a truncated long value", func(t *testing.T) {
		// Given a disabled flag with a very long value
		isolateStorage(t)
		f := newFakeInstance(t)
		long := strings.Repeat("x", 200)
		f.features["101"] = []map[string]any{{
			"id": 1, "name": "blob", "type": "MULTIVARIATE",
			"environment_feature_state": map[string]any{"enabled": false, "feature_state_value": long},
		}}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list")

		// Then — off, lower-case type, and no full 200-char value
		if err != nil {
			t.Fatalf("flags list: %v", err)
		}
		if !strings.Contains(out, "off") || !strings.Contains(out, "multivariate") || !strings.Contains(out, "…") {
			t.Errorf("output = %q, want off/multivariate/truncation", out)
		}
		if strings.Contains(out, long) {
			t.Errorf("output = %q, want the long value truncated", out)
		}
	})

	t.Run("multi-line JSON value stays on one row", func(t *testing.T) {
		// Given a value containing newlines (a JSON blob)
		isolateStorage(t)
		f := newFakeInstance(t)
		f.features["101"] = []map[string]any{{
			"id": 1, "name": "blob", "type": "STANDARD",
			"environment_feature_state": map[string]any{
				"enabled": true, "feature_state_value": "[\n  {\n    \"value\": \"EQUAL\"\n  }\n]",
			},
		}}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "list")

		// Then — the value row holds no embedded newline; only the table's own
		// line breaks remain (header, one data row, blank line, count).
		if err != nil {
			t.Fatalf("flags list: %v", err)
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		for _, l := range lines {
			// Any JSON fragment must sit on the blob row, not spill onto its own.
			if strings.Contains(l, `"value"`) && !strings.HasPrefix(l, "blob") {
				t.Errorf("value spilled onto its own row: %q", l)
			}
		}
		if !strings.Contains(out, `[ { "value": "EQUAL" } ]`) {
			t.Errorf("output = %q, want the value flattened to one line", out)
		}
	})
}

func TestFlagListSegment(t *testing.T) {
	t.Run("lists only the flags overridden for the segment", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withSegmentOverride(f, true) // max_items with a segment override

		out, err := run("", "flag", "list", "--segment", "12")
		if err != nil {
			t.Fatalf("flag list --segment: %v\noutput: %s", err, out)
		}
		if f.featuresSeg() != "12" {
			t.Errorf("features segment = %q, want 12", f.featuresSeg())
		}
		for _, want := range []string{"NAME", "TYPE", "STATE", "VALUE", "max_items", "special", "on"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
		if strings.Contains(out, "LIFECYCLE") {
			t.Errorf("output = %q, segment list should drop LIFECYCLE", out)
		}
	})

	t.Run("--json is the segment-override shape", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withSegmentOverride(f, true)

		out, err := run("", "flag", "list", "--segment", "12", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var arr []map[string]any
		if err := json.Unmarshal([]byte(out), &arr); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if len(arr) != 1 || arr[0]["feature"] != "max_items" ||
			arr[0]["segment"] != float64(12) || arr[0]["enabled"] != true {
			t.Errorf("items = %+v", arr)
		}
	})

	t.Run("no overrides for the segment", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withSegmentOverride(f, false) // max_items, no segment override

		out, err := run("", "flag", "list", "--segment", "99")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "No segment overrides") {
			t.Errorf("output = %q, want a no-overrides message", out)
		}
	})
}

func TestFlagGet(t *testing.T) {
	t.Run("detail view for a named feature", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "get", "max_items")

		// Then — the detail fields for that one feature
		if err != nil {
			t.Fatalf("flag get: %v\noutput: %s", err, out)
		}
		for _, want := range []string{"max_items", "Value", "25", "Segment overrides", "Identity overrides", "Code references", "3"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("json is the curated shape", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "get", "max_items", "--json")

		// Then — flat, scriptable, matching the human detail fields
		if err != nil {
			t.Fatalf("flag get --json: %v", err)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if v["feature"] != "max_items" || v["type"] != "standard" ||
			v["enabled"] != false || v["value"] != float64(25) ||
			v["segment_overrides"] != float64(1) || v["identity_overrides"] != float64(2) ||
			v["code_references"] != float64(3) {
			t.Errorf("curated view = %+v", v)
		}
		if _, ok := v["environment_feature_state"]; ok {
			t.Errorf("view = %+v, want no nested state", v)
		}
	})

	t.Run("case-insensitive exact match, not a contains match", func(t *testing.T) {
		// Given features whose names share a prefix
		isolateStorage(t)
		f := newFakeInstance(t)
		f.features["101"] = []map[string]any{
			{"id": 1, "name": "checkout", "type": "STANDARD",
				"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": "a"}},
			{"id": 2, "name": "checkout-v2", "type": "STANDARD",
				"environment_feature_state": map[string]any{"enabled": false, "feature_state_value": "b"}},
		}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "get", "CheckOut")

		// Then — resolves to "checkout" (value "a"), never "checkout-v2"
		if err != nil {
			t.Fatalf("flag get: %v", err)
		}
		if strings.Contains(out, "checkout-v2") {
			t.Errorf("output = %q, matched the contains sibling instead of the exact name", out)
		}
		if !strings.Contains(out, "a") {
			t.Errorf("output = %q, want checkout's value", out)
		}
	})

	t.Run("unknown feature errors", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When / Then
		_, err := run("", "flag", "get", "ghost")
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Errorf("err = %v, want a not-found error naming the feature", err)
		}
	})

	t.Run("a null identity-override count shows 0", func(t *testing.T) {
		// Given num_identity_overrides is null (Edge/Dynamo projects)
		isolateStorage(t)
		f := newFakeInstance(t)
		f.features["101"] = []map[string]any{{
			"id": 1, "name": "edgeflag", "type": "STANDARD",
			"num_segment_overrides": 0, "num_identity_overrides": nil,
			"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": "x"},
		}}
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "flag", "get", "edgeflag")

		// Then — shown as 0, not "-"
		if err != nil {
			t.Fatalf("flag get: %v", err)
		}
		if !strings.Contains(out, "Identity overrides") || !strings.Contains(out, "0") {
			t.Errorf("output = %q, want Identity overrides 0", out)
		}
	})
}

// flagUpdateEnv writes a config bound to project 101 / Development and returns
// the fake instance with admin credentials set.
func flagUpdateEnv(t *testing.T) *fakeInstance {
	t.Helper()
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	return f
}

func TestFlagUpdate(t *testing.T) {
	t.Run("--enable preserves the current value and reprints", func(t *testing.T) {
		// Given max_items is off with integer value 25
		f := flagUpdateEnv(t)

		// When
		out, err := run("", "flag", "update", "max_items", "--enable", "--yes")

		// Then — the full environment default carries enabled=true and value 25
		if err != nil {
			t.Fatalf("flag update: %v\noutput: %s", err, out)
		}
		def := f.lastUpdate["environment_default"].(map[string]any)
		val := def["value"].(map[string]any)
		if def["enabled"] != true || val["type"] != "integer" || val["value"] != "25" {
			t.Errorf("environment_default = %+v", def)
		}
		if !strings.Contains(out, "Enabled max_items") {
			t.Errorf("output = %q, want an Enabled confirmation", out)
		}
	})

	t.Run("--value infers integer", func(t *testing.T) {
		// Given
		f := flagUpdateEnv(t)

		// When
		out, err := run("", "flag", "update", "onboarding_banner", "--value", "42", "--yes")

		// Then
		if err != nil {
			t.Fatalf("flag update: %v", err)
		}
		val := f.lastUpdate["environment_default"].(map[string]any)["value"].(map[string]any)
		if val["type"] != "integer" || val["value"] != "42" {
			t.Errorf("value = %+v, want inferred integer 42", val)
		}
		if !strings.Contains(out, "Set onboarding_banner to 42") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("--type string keeps a numeric literal as a string, quoted in message", func(t *testing.T) {
		// Given
		f := flagUpdateEnv(t)

		// When
		out, err := run("", "flag", "update", "max_items", "--value", "25", "--type", "string", "--yes")

		// Then
		if err != nil {
			t.Fatalf("flag update: %v", err)
		}
		val := f.lastUpdate["environment_default"].(map[string]any)["value"].(map[string]any)
		if val["type"] != "string" || val["value"] != "25" {
			t.Errorf("value = %+v, want string \"25\"", val)
		}
		if !strings.Contains(out, `Set max_items to "25"`) {
			t.Errorf("output = %q, want the string value quoted", out)
		}
	})

	t.Run("--enable and --disable conflict", func(t *testing.T) {
		f := flagUpdateEnv(t)
		_, err := run("", "flag", "update", "max_items", "--enable", "--disable", "--yes")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want a usage error", err)
		}
		_ = f
	})

	t.Run("nothing to update errors", func(t *testing.T) {
		f := flagUpdateEnv(t)
		_, err := run("", "flag", "update", "max_items", "--yes")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "nothing to update") {
			t.Errorf("err = %v, want a usage error", err)
		}
		_ = f
	})

	t.Run("workflow-gated environment is reported clearly", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.workflowGated = true
		_, err := run("", "flag", "update", "max_items", "--enable", "--yes")
		if !errors.Is(err, api.ErrWorkflowGated) {
			t.Errorf("err = %v, want ErrWorkflowGated", err)
		}
	})

	t.Run("unknown feature errors before any write", func(t *testing.T) {
		f := flagUpdateEnv(t)
		_, err := run("", "flag", "update", "ghost", "--enable", "--yes")
		if err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Errorf("err = %v, want a not-found error", err)
		}
		if f.lastUpdate != nil {
			t.Errorf("lastUpdate = %+v, want no write attempted", f.lastUpdate)
		}
	})

	t.Run("without --yes and no TTY exits 2", func(t *testing.T) {
		f := flagUpdateEnv(t)
		_, err := run("", "flag", "update", "max_items", "--enable")
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("err = %v, want a usage error (confirmation needed)", err)
		}
		if f.lastUpdate != nil {
			t.Errorf("lastUpdate = %+v, want no write without confirmation", f.lastUpdate)
		}
	})
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

func TestFlagGetSegment(t *testing.T) {
	t.Run("shows the segment override", func(t *testing.T) {
		// Given
		f := flagUpdateEnv(t)
		withSegmentOverride(f, true)

		// When
		out, err := run("", "flag", "get", "max_items", "--segment", "12")

		// Then — the features query carried the segment, output shows its state
		if err != nil {
			t.Fatalf("flag get --segment: %v\noutput: %s", err, out)
		}
		if got := f.featuresSeg(); got != "12" {
			t.Errorf("features segment = %q, want 12", got)
		}
		for _, want := range []string{"Segment", "12", "special", "on"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("no override errors", func(t *testing.T) {
		// Given a feature with no segment override
		f := flagUpdateEnv(t)
		withSegmentOverride(f, false)

		// When / Then
		_, err := run("", "flag", "get", "max_items", "--segment", "99")
		if err == nil || !strings.Contains(err.Error(), "segment 99") {
			t.Errorf("err = %v, want a no-override error naming the segment", err)
		}
	})
}

func TestFlagUpdateSegment(t *testing.T) {
	t.Run("updates the override and carries the env default unchanged", func(t *testing.T) {
		// Given an existing segment override (enabled true, value "special")
		f := flagUpdateEnv(t)
		withSegmentOverride(f, true)

		// When — change only the segment value
		out, err := run("", "flag", "update", "max_items", "--segment", "12", "--value", "new", "--yes")

		// Then
		if err != nil {
			t.Fatalf("flag update --segment: %v\noutput: %s", err, out)
		}
		def := f.lastUpdate["environment_default"].(map[string]any)
		defVal := def["value"].(map[string]any)
		if def["enabled"] != false || defVal["type"] != "integer" || defVal["value"] != "25" {
			t.Errorf("environment_default = %+v, want the current default carried unchanged", def)
		}
		ovs := f.lastUpdate["segment_overrides"].([]any)
		ov := ovs[0].(map[string]any)
		ovVal := ov["value"].(map[string]any)
		if ov["segment_id"] != float64(12) || ov["enabled"] != true ||
			ovVal["type"] != "string" || ovVal["value"] != "new" {
			t.Errorf("segment override = %+v, want enabled preserved and value \"new\"", ov)
		}
		if !strings.Contains(out, `Set max_items to "new" in segment 12`) {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("a new override inherits the env default value", func(t *testing.T) {
		// Given no existing override; env default value is integer 25
		f := flagUpdateEnv(t)
		withSegmentOverride(f, false)

		// When — enable a fresh override with no explicit value
		_, err := run("", "flag", "update", "max_items", "--segment", "7", "--enable", "--yes")

		// Then — the override inherits the env default value (integer 25)
		if err != nil {
			t.Fatalf("flag update --segment: %v", err)
		}
		ov := f.lastUpdate["segment_overrides"].([]any)[0].(map[string]any)
		ovVal := ov["value"].(map[string]any)
		if ov["segment_id"] != float64(7) || ov["enabled"] != true ||
			ovVal["type"] != "integer" || ovVal["value"] != "25" {
			t.Errorf("segment override = %+v, want inherited integer 25", ov)
		}
	})
}

func TestFlagDelete(t *testing.T) {
	t.Run("deletes a segment override", func(t *testing.T) {
		// Given
		f := flagUpdateEnv(t)

		// When
		out, err := run("", "flag", "delete", "max_items", "--segment", "12", "--yes")

		// Then — the override is deleted; a delete prints no resource
		if err != nil {
			t.Fatalf("flag delete: %v\noutput: %s", err, out)
		}
		if f.lastDelete["feature"].(map[string]any)["name"] != "max_items" ||
			f.lastDelete["segment"].(map[string]any)["id"] != float64(12) {
			t.Errorf("delete body = %+v", f.lastDelete)
		}
		if !strings.Contains(out, "Deleted max_items override for segment 12") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("without --segment exits 2", func(t *testing.T) {
		f := flagUpdateEnv(t)
		_, err := run("", "flag", "delete", "max_items", "--yes")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--segment") {
			t.Errorf("err = %v, want a usage error naming --segment", err)
		}
		if f.lastDelete != nil {
			t.Errorf("lastDelete = %+v, want no call", f.lastDelete)
		}
	})

	t.Run("missing override reports not found", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.segmentMissing = true
		_, err := run("", "flag", "delete", "max_items", "--segment", "99", "--yes")
		if err == nil || !strings.Contains(err.Error(), "segment 99") {
			t.Errorf("err = %v, want a not-found error naming the segment", err)
		}
	})
}

func TestUsageIsSingleLine(t *testing.T) {
	// Given / When — root help
	out, err := run("", "--help")
	if err != nil {
		t.Fatal(err)
	}

	// Then — one context-appropriate line, not cobra's default two-line form
	if !strings.Contains(out, "flagsmith [command] [flags]") {
		t.Errorf("root usage = %q, want the single-line form", out)
	}
	if strings.Contains(out, "flagsmith [flags]\n") {
		t.Errorf("root usage still shows the two-line form:\n%s", out)
	}

	// Leaf commands render their own use line
	leaf, err := run("", "flag", "list", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(leaf, "flagsmith flag list [flags]") {
		t.Errorf("leaf usage = %q, want its own use line", leaf)
	}
}

func TestFlagIdentity(t *testing.T) {
	// max_items is feature id 2 in defaultFeatures; user-1 is core identity 501.

	t.Run("core: update creates an override via the core endpoint", func(t *testing.T) {
		f := flagUpdateEnv(t) // useEdge defaults to false

		out, err := run("", "flag", "update", "max_items", "--identifier", "user-1", "--value", "42", "--yes")
		if err != nil {
			t.Fatalf("flag update --identifier: %v\noutput: %s", err, out)
		}
		w := f.lastIdentityWrite
		if w["feature"] != float64(2) || w["enabled"] != false || w["feature_state_value"] != float64(42) {
			t.Errorf("core write = %+v", w)
		}
		if f.lastEdgeWrite != nil {
			t.Errorf("edge endpoint should not have been used: %+v", f.lastEdgeWrite)
		}
		if !strings.Contains(out, "Set max_items to 42 for identifier user-1") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("core: get shows the override", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.coreOverrides[501] = map[int]*fakeFS{2: {id: 9001, enabled: true, value: "custom"}}

		out, err := run("", "flag", "get", "max_items", "--identifier", "user-1")
		if err != nil {
			t.Fatalf("flag get --identifier: %v", err)
		}
		for _, want := range []string{"Identifier", "user-1", "custom", "on"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("core: delete removes the override", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.coreOverrides[501] = map[int]*fakeFS{2: {id: 9001, enabled: true, value: "x"}}

		out, err := run("", "flag", "delete", "max_items", "--identifier", "user-1", "--yes")
		if err != nil {
			t.Fatalf("flag delete --identifier: %v", err)
		}
		if f.coreOverrides[501][2] != nil {
			t.Errorf("override still present: %+v", f.coreOverrides[501][2])
		}
		if !strings.Contains(out, "Deleted max_items override for identifier user-1") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("edge: update via the identifier endpoint", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.useEdge = true

		out, err := run("", "flag", "update", "max_items", "--identifier", "edge-user", "--enable", "--value", "7", "--yes")
		if err != nil {
			t.Fatalf("flag update --identifier (edge): %v\noutput: %s", err, out)
		}
		w := f.lastEdgeWrite
		if w["identifier"] != "edge-user" || w["feature"] != float64(2) || w["enabled"] != true || w["feature_state_value"] != float64(7) {
			t.Errorf("edge write = %+v", w)
		}
		if f.lastIdentityWrite != nil {
			t.Errorf("core endpoint should not have been used: %+v", f.lastIdentityWrite)
		}
		if !strings.Contains(out, "Set max_items to 7 for identifier edge-user") ||
			!strings.Contains(out, "Enabled max_items for identifier edge-user") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("edge: get resolves uuid then reads", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.useEdge = true
		f.edgeOverrides["edge-user"] = map[int]*fakeFS{2: {enabled: false, value: "e"}}

		out, err := run("", "flag", "get", "max_items", "--identifier", "edge-user")
		if err != nil {
			t.Fatalf("flag get --identifier (edge): %v", err)
		}
		for _, want := range []string{"Identifier", "edge-user", "e", "off"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("edge: delete via the identifier endpoint", func(t *testing.T) {
		f := flagUpdateEnv(t)
		f.useEdge = true
		f.edgeOverrides["edge-user"] = map[int]*fakeFS{2: {enabled: true, value: "e"}}

		out, err := run("", "flag", "delete", "max_items", "--identifier", "edge-user", "--yes")
		if err != nil {
			t.Fatalf("flag delete --identifier (edge): %v", err)
		}
		if f.lastEdgeDelete["identifier"] != "edge-user" || f.lastEdgeDelete["feature"] != float64(2) {
			t.Errorf("edge delete body = %+v", f.lastEdgeDelete)
		}
		if f.edgeOverrides["edge-user"][2] != nil {
			t.Errorf("edge override still present")
		}
		if !strings.Contains(out, "Deleted max_items override for identifier edge-user") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("--segment and --identifier are mutually exclusive", func(t *testing.T) {
		flagUpdateEnv(t)
		_, err := run("", "flag", "update", "max_items", "--segment", "1", "--identifier", "user-1", "--yes")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want a usage error", err)
		}
	})

	t.Run("delete demands a target", func(t *testing.T) {
		flagUpdateEnv(t)
		_, err := run("", "flag", "delete", "max_items", "--yes")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--identifier") {
			t.Errorf("err = %v, want a usage error naming --segment/--identifier", err)
		}
	})
}

func TestSegmentList(t *testing.T) {
	t.Run("hides feature-specific segments by default", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "segment", "list")
		if err != nil {
			t.Fatalf("segment list: %v\noutput: %s", err, out)
		}
		for _, want := range []string{"NAME", "ID", "CONDITIONS", "DESCRIPTION", "us-adults", "beta-optin", "2 segments"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
		if strings.Contains(out, "beta-cohort") {
			t.Errorf("output = %q, should hide feature-specific beta-cohort", out)
		}
	})

	t.Run("--include-feature-specific shows them", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "segment", "list", "--include-feature-specific")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "beta-cohort") || !strings.Contains(out, "3 segments") {
			t.Errorf("output = %q, want beta-cohort and 3 segments", out)
		}
	})

	t.Run("--json is an array of curated segments", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "segment", "list", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var arr []map[string]any
		if err := json.Unmarshal([]byte(out), &arr); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if len(arr) != 2 {
			t.Errorf("segments = %+v, want 2", arr)
		}
	})
}

func TestSegmentGet(t *testing.T) {
	t.Run("renders the rule tree and a nudge", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "segment", "get", "us-adults") // by name
		if err != nil {
			t.Fatalf("segment get: %v\noutput: %s", err, out)
		}
		for _, want := range []string{
			"us-adults (42)", "All of the below:", "Any of the below:",
			"country", "IN", "US, CA", "age", "GREATER_THAN_INCLUSIVE",
			"flag list --segment 42",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("--json decodes IN to an array and stamps $schema", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "segment", "get", "42", "--json") // by id
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		rules := v["rules"].(map[string]any)
		if rules["$schema"] == nil {
			t.Errorf("rules = %+v, want a $schema pointer", rules)
		}
		sub := rules["rules"].([]any)[0].(map[string]any)
		cond := sub["conditions"].([]any)[0].(map[string]any)
		arr, ok := cond["value"].([]any)
		if !ok || len(arr) != 2 || arr[0] != "US" || arr[1] != "CA" {
			t.Errorf("IN value = %v, want [\"US\",\"CA\"]", cond["value"])
		}
	})
}

func TestSegmentCreate(t *testing.T) {
	t.Run("encodes IN and wraps the rule", func(t *testing.T) {
		f := flagUpdateEnv(t)
		rule := `{"type":"ALL","rules":[{"type":"ANY","conditions":[{"property":"country","operator":"IN","value":["US","CA"]}]}]}`
		out, err := run("", "segment", "create", "newseg", "--rules", rule)
		if err != nil {
			t.Fatalf("segment create: %v\noutput: %s", err, out)
		}
		body := f.lastSegmentBody
		if body["name"] != "newseg" {
			t.Errorf("name = %v", body["name"])
		}
		top := body["rules"].([]any)[0].(map[string]any)
		sub := top["rules"].([]any)[0].(map[string]any)
		cond := sub["conditions"].([]any)[0].(map[string]any)
		if cond["value"] != `["US","CA"]` {
			t.Errorf("IN value on the wire = %v, want the JSON-array string", cond["value"])
		}
		if !strings.Contains(out, "Created segment newseg") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("requires --rules", func(t *testing.T) {
		flagUpdateEnv(t)
		_, err := run("", "segment", "create", "x")
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--rules") {
			t.Errorf("err = %v, want a usage error naming --rules", err)
		}
	})

	t.Run("--feature resolves a name to an id", func(t *testing.T) {
		f := flagUpdateEnv(t)
		rule := `{"type":"ALL","conditions":[{"property":"beta","operator":"IS_SET"}]}`
		if _, err := run("", "segment", "create", "fs", "--rules", rule, "--feature", "max_items"); err != nil {
			t.Fatalf("segment create --feature: %v", err)
		}
		if f.lastSegmentBody["feature"] != float64(2) {
			t.Errorf("feature = %v, want 2 (max_items)", f.lastSegmentBody["feature"])
		}
	})

	t.Run("rejects a too-deep tree", func(t *testing.T) {
		flagUpdateEnv(t)
		deep := `{"type":"ALL","rules":[{"type":"ANY","rules":[{"type":"ALL","conditions":[]}]}]}`
		_, err := run("", "segment", "create", "deep", "--rules", deep)
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "two levels") {
			t.Errorf("err = %v, want a depth usage error", err)
		}
	})
}

func TestSegmentUpdate(t *testing.T) {
	t.Run("keeps rules when only description changes", func(t *testing.T) {
		f := flagUpdateEnv(t)
		if _, err := run("", "segment", "update", "us-adults", "--description", "new desc"); err != nil {
			t.Fatalf("segment update: %v", err)
		}
		if f.lastSegmentBody["description"] != "new desc" || f.lastSegmentBody["rules"] == nil {
			t.Errorf("body = %+v, want new description with rules preserved", f.lastSegmentBody)
		}
	})

	t.Run("replaces the rule tree", func(t *testing.T) {
		f := flagUpdateEnv(t)
		rule := `{"type":"ALL","conditions":[{"property":"x","operator":"EQUAL","value":"1"}]}`
		if _, err := run("", "segment", "update", "42", "--rules", rule); err != nil {
			t.Fatalf("segment update --rules: %v", err)
		}
		top := f.lastSegmentBody["rules"].([]any)[0].(map[string]any)
		cond := top["conditions"].([]any)[0].(map[string]any)
		if cond["property"] != "x" {
			t.Errorf("body rules = %+v, want the replacement", f.lastSegmentBody["rules"])
		}
	})
}

func TestSegmentDelete(t *testing.T) {
	f := flagUpdateEnv(t)
	out, err := run("", "segment", "delete", "us-adults", "--yes")
	if err != nil {
		t.Fatalf("segment delete: %v", err)
	}
	if f.segments[42] != nil {
		t.Errorf("segment 42 still present")
	}
	if !strings.Contains(out, "Deleted segment us-adults (42)") {
		t.Errorf("output = %q", out)
	}
}

// withFeatures loads project 101 with feature-CRUD-shaped features (one
// multivariate, one archived), replacing the flag-oriented defaults.
func withFeatures(f *fakeInstance) {
	f.features["101"] = []map[string]any{
		{"id": 88, "name": "checkout-v2", "type": "STANDARD", "description": "New checkout flow",
			"initial_value": "green", "default_enabled": true, "is_archived": false,
			"multivariate_options": []any{}},
		{"id": 91, "name": "banner-copy", "type": "MULTIVARIATE", "description": "A/B banner text",
			"initial_value": "hello", "default_enabled": false, "is_archived": false,
			"multivariate_options": []any{
				map[string]any{"id": 201, "type": "unicode", "string_value": "headline", "default_percentage_allocation": 30, "key": "hero"},
				map[string]any{"id": 202, "type": "unicode", "string_value": "subhead", "default_percentage_allocation": 70, "key": "sub"},
			}},
		{"id": 40, "name": "legacy-copy", "type": "STANDARD", "description": "Retired",
			"initial_value": "old", "default_enabled": false, "is_archived": true,
			"multivariate_options": []any{}},
	}
}

func TestFeatureList(t *testing.T) {
	t.Run("hides archived by default", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "list")
		if err != nil {
			t.Fatalf("feature list: %v\noutput: %s", err, out)
		}
		if f.lastFeatArch != "false" {
			t.Errorf("is_archived param = %q, want false", f.lastFeatArch)
		}
		for _, want := range []string{"NAME", "ID", "TYPE", "VALUE", "DESCRIPTION", "checkout-v2", "banner-copy", "multivariate", "green", "2 features"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
		if strings.Contains(out, "legacy-copy") {
			t.Errorf("output = %q, should hide archived", out)
		}
	})

	t.Run("--include-archived shows archived", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "list", "--include-archived")
		if err != nil {
			t.Fatal(err)
		}
		if f.lastFeatArch != "" {
			t.Errorf("is_archived param = %q, want unset", f.lastFeatArch)
		}
		if !strings.Contains(out, "legacy-copy") || !strings.Contains(out, "3 features") {
			t.Errorf("output = %q, want legacy-copy and 3 features", out)
		}
	})
}

func TestFeatureGet(t *testing.T) {
	t.Run("multivariate detail with variants", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "get", "banner-copy") // by name
		if err != nil {
			t.Fatalf("feature get: %v\noutput: %s", err, out)
		}
		for _, want := range []string{"banner-copy (91)", "multivariate", "hello", "Variants", "headline", "30", "hero", "subhead"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("--json curated shape", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "get", "91", "--json") // by id
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if v["type"] != "multivariate" || v["value"] != "hello" {
			t.Errorf("feature = %+v", v)
		}
		variants := v["variants"].([]any)
		v0 := variants[0].(map[string]any)
		if v0["value"] != "headline" || v0["weight"] != float64(30) || v0["key"] != "hero" {
			t.Errorf("variant = %+v", v0)
		}
	})
}

func TestFeatureCreate(t *testing.T) {
	t.Run("standard with a default value", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "create", "checkout-3", "--value", "blue", "--description", "d", "--enabled")
		if err != nil {
			t.Fatalf("feature create: %v\noutput: %s", err, out)
		}
		b := f.lastFeatureBody
		if b["name"] != "checkout-3" || b["initial_value"] != "blue" || b["description"] != "d" || b["default_enabled"] != true {
			t.Errorf("body = %+v", b)
		}
		if !strings.Contains(out, "Created feature checkout-3") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("multivariate with inline variants types by JSON value", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		variants := `[{"value":"a","weight":30},{"value":42,"weight":70}]`
		if _, err := run("", "feature", "create", "banner-2", "--value", "hello", "--variants", variants); err != nil {
			t.Fatalf("feature create --variants: %v", err)
		}
		opts := f.lastFeatureBody["multivariate_options"].([]any)
		o0 := opts[0].(map[string]any)
		o1 := opts[1].(map[string]any)
		if o0["type"] != "unicode" || o0["string_value"] != "a" || o0["default_percentage_allocation"] != float64(30) {
			t.Errorf("variant 0 = %+v", o0)
		}
		if o1["type"] != "int" || o1["integer_value"] != float64(42) || o1["default_percentage_allocation"] != float64(70) {
			t.Errorf("variant 1 = %+v", o1)
		}
	})
}

func TestFeatureUpdate(t *testing.T) {
	t.Run("description", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		if _, err := run("", "feature", "update", "checkout-v2", "--description", "redesign"); err != nil {
			t.Fatalf("feature update: %v", err)
		}
		if f.lastFeatureBody["description"] != "redesign" {
			t.Errorf("body = %+v", f.lastFeatureBody)
		}
	})

	t.Run("archive and unarchive", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		if _, err := run("", "feature", "update", "checkout-v2", "--archive"); err != nil {
			t.Fatalf("archive: %v", err)
		}
		if f.lastFeatureBody["is_archived"] != true {
			t.Errorf("archive body = %+v", f.lastFeatureBody)
		}
		if _, err := run("", "feature", "update", "legacy-copy", "--unarchive"); err != nil {
			t.Fatalf("unarchive: %v", err)
		}
		if f.lastFeatureBody["is_archived"] != false {
			t.Errorf("unarchive body = %+v", f.lastFeatureBody)
		}
	})

	t.Run("archive and unarchive conflict", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		_, err := run("", "feature", "update", "checkout-v2", "--archive", "--unarchive")
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("err = %v, want a usage error", err)
		}
	})
}

func TestFeatureDelete(t *testing.T) {
	f := flagUpdateEnv(t)
	withFeatures(f)
	out, err := run("", "feature", "delete", "checkout-v2", "--yes")
	if err != nil {
		t.Fatalf("feature delete: %v", err)
	}
	for _, it := range f.features["101"] {
		if it["id"] == 88 {
			t.Errorf("feature 88 still present")
		}
	}
	if !strings.Contains(out, "Deleted feature checkout-v2 (88)") {
		t.Errorf("output = %q", out)
	}
}

func TestFeatureVariant(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "variant", "list", "banner-copy")
		if err != nil {
			t.Fatalf("variant list: %v\noutput: %s", err, out)
		}
		for _, want := range []string{"VALUE", "WEIGHT", "KEY", "ID", "headline", "30", "hero", "201", "subhead"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
		}
	})

	t.Run("add types the value and posts to mv-options", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "variant", "add", "banner-copy", "--value", "cta", "--weight", "20", "--key", "button")
		if err != nil {
			t.Fatalf("variant add: %v\noutput: %s", err, out)
		}
		b := f.lastMVBody
		if b["type"] != "unicode" || b["string_value"] != "cta" || b["default_percentage_allocation"] != float64(20) ||
			b["key"] != "button" || b["feature"] != float64(91) {
			t.Errorf("mv body = %+v", b)
		}
		if !strings.Contains(out, "Added variant cta") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("add infers an integer value", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		if _, err := run("", "feature", "variant", "add", "banner-copy", "--value", "42", "--weight", "10"); err != nil {
			t.Fatalf("variant add: %v", err)
		}
		if f.lastMVBody["type"] != "int" || f.lastMVBody["integer_value"] != float64(42) {
			t.Errorf("mv body = %+v", f.lastMVBody)
		}
	})

	t.Run("update a variant by key", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		if _, err := run("", "feature", "variant", "update", "banner-copy", "hero", "--weight", "40"); err != nil {
			t.Fatalf("variant update: %v", err)
		}
		if f.lastMVBody["default_percentage_allocation"] != float64(40) {
			t.Errorf("mv body = %+v", f.lastMVBody)
		}
		// value untouched (only weight sent)
		if _, ok := f.lastMVBody["string_value"]; ok {
			t.Errorf("mv body = %+v, want only the weight sent", f.lastMVBody)
		}
	})

	t.Run("delete a variant by id", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		out, err := run("", "feature", "variant", "delete", "banner-copy", "201", "--yes")
		if err != nil {
			t.Fatalf("variant delete: %v", err)
		}
		feat := f.featureByID("101", 91)
		if len(feat["multivariate_options"].([]any)) != 1 {
			t.Errorf("options = %+v, want variant 201 removed", feat["multivariate_options"])
		}
		if !strings.Contains(out, "Deleted variant") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("unknown variant errors", func(t *testing.T) {
		f := flagUpdateEnv(t)
		withFeatures(f)
		_, err := run("", "feature", "variant", "delete", "banner-copy", "nope", "--yes")
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Errorf("err = %v, want a not-found error", err)
		}
	})
}

func TestAPI(t *testing.T) {
	// echoJSON runs `flagsmith api api/v1/echo/ <args>` and returns the
	// decoded reflection of the request the fake saw.
	echoJSON := func(t *testing.T, stdin string, args ...string) map[string]any {
		t.Helper()
		out, err := run(stdin, append([]string{"api", "api/v1/echo/"}, args...)...)
		if err != nil {
			t.Fatalf("api: %v\noutput: %s", err, out)
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(out), &e); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		return e
	}

	t.Run("GET applies the admin credential automatically", func(t *testing.T) {
		flagUpdateEnv(t)
		e := echoJSON(t, "")
		if e["method"] != "GET" || e["authorization"] != "Api-Key "+masterKey {
			t.Errorf("echo = %+v", e)
		}
	})

	t.Run("--jq filters the response", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "api", "api/v1/organisations/", "--jq", ".results[].name")
		if err != nil {
			t.Fatalf("api: %v", err)
		}
		if strings.TrimSpace(out) != "Acme" {
			t.Errorf("out = %q, want Acme", out)
		}
	})

	t.Run("a field implies POST with a typed JSON body", func(t *testing.T) {
		flagUpdateEnv(t)
		e := echoJSON(t, "", "-F", "n=3", "-f", "s=3")
		if e["method"] != "POST" || e["content_type"] != "application/json" {
			t.Errorf("echo = %+v", e)
		}
		body, _ := e["body"].(string)
		if !strings.Contains(body, `"n":3`) || !strings.Contains(body, `"s":"3"`) {
			t.Errorf("body = %q, want typed n and raw s", body)
		}
	})

	t.Run("fields on an explicit GET become query params", func(t *testing.T) {
		flagUpdateEnv(t)
		e := echoJSON(t, "", "-X", "GET", "-F", "a=1")
		if e["method"] != "GET" || e["query"] != "a=1" {
			t.Errorf("echo = %+v", e)
		}
	})

	t.Run("raw body from stdin", func(t *testing.T) {
		flagUpdateEnv(t)
		e := echoJSON(t, `{"x":1}`, "-X", "POST", "--input", "-")
		if e["body"] != `{"x":1}` {
			t.Errorf("body = %q", e["body"])
		}
	})

	t.Run("custom header", func(t *testing.T) {
		flagUpdateEnv(t)
		e := echoJSON(t, "", "-H", "X-Custom: hi")
		if e["custom"] != "hi" {
			t.Errorf("custom = %q", e["custom"])
		}
	})

	t.Run("--include shows the status line", func(t *testing.T) {
		flagUpdateEnv(t)
		out, err := run("", "api", "api/v1/echo/", "-i")
		if err != nil {
			t.Fatalf("api: %v", err)
		}
		if !strings.Contains(out, "HTTP/1.1 200") {
			t.Errorf("out = %q, want a status line", out)
		}
	})

	t.Run("non-2xx exits non-zero", func(t *testing.T) {
		flagUpdateEnv(t)
		_, err := run("", "api", "api/v1/nope/")
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Errorf("err = %v, want a 404 error", err)
		}
	})

	t.Run("--sdk uses the environment key, not the admin credential", func(t *testing.T) {
		flagUpdateEnv(t)
		t.Setenv("FLAGSMITH_ENVIRONMENT_KEY", "someClientKey")
		e := echoJSON(t, "", "--sdk")
		if e["envkey"] != "someClientKey" || e["authorization"] != "" {
			t.Errorf("echo = %+v, want the SDK key and no admin auth", e)
		}
	})

	t.Run("--input with fields is a usage error", func(t *testing.T) {
		flagUpdateEnv(t)
		_, err := run("", "api", "api/v1/echo/", "--input", "-", "-F", "a=1")
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("err = %v, want a usage error", err)
		}
	})
}

func TestFlagCreateIsNudge(t *testing.T) {
	// Given / When
	f := flagUpdateEnv(t)
	_, err := run("", "flag", "create", "brand-new")

	// Then — a usage error pointing at feature create
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "feature create brand-new") {
		t.Errorf("err = %v, want a nudge toward `feature create`", err)
	}
	_ = f
}

func TestAuthStatusHonoursConfigAPIURL(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1, "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — no --api-url flag anywhere
	out, err := run("", "auth", "status")

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(out, "Acme") {
		t.Errorf("output = %q, want the config-file apiUrl to have been used", out)
	}
}

func TestAuthStatusSeedsNameCache(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	if _, err := run("", "auth", "status", "--api-url", f.srv.URL); err != nil {
		t.Fatal(err)
	}

	// Then
	if got := cache.Load(f.srv.URL); got.Organisations["3"] != "Acme" {
		t.Errorf("cache = %+v, want the organisation name remembered", got)
	}
}

func TestEnvMasterKey(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"Master API key", "Acme", "$FLAGSMITH_API_KEY"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}

	// When
	tokenOut, err := run("", "auth", "token", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if strings.TrimSpace(tokenOut) != masterKey {
		t.Errorf("auth token output = %q, want the master key", tokenOut)
	}
}

func TestEnvBearerToken(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", bearerToken)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"kim@example.com", "$FLAGSMITH_API_KEY"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}
}

func TestEnvServerKeyRejected(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", "ser.AbCdEf1234")

	// When
	_, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
		t.Errorf("err = %v, want mention of FLAGSMITH_ENVIRONMENT_KEY", err)
	}
}

func TestEnvBeatsKeychain(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: f.srv.URL,
		AccessToken: oauthAccess, RefreshToken: "cmd-refresh",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(statusOut, "$FLAGSMITH_API_KEY") {
		t.Errorf("auth status output = %q, want the env source to win over the keychain", statusOut)
	}
}

func TestLoginFailsClosedWithoutKeychain(t *testing.T) {
	// Given — no keychain
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	f := newFakeInstance(t)

	// When — login probes the keychain before starting the flow
	out, err := run("", "login", "--api-url", f.srv.URL, "--no-browser")

	// Then — it fails closed toward FLAGSMITH_API_KEY, starting no OAuth flow
	if err == nil {
		t.Fatalf("expected fail-closed error, got success: %q", out)
	}
	if !strings.Contains(err.Error(), "FLAGSMITH_API_KEY") {
		t.Errorf("err = %v, want it to point at FLAGSMITH_API_KEY", err)
	}
	if strings.Contains(out, "oauth/authorize") {
		t.Errorf("output = %q — the OAuth flow started despite no keychain", out)
	}
}

func TestRefreshPersistsToKeychain(t *testing.T) {
	// Given a stale OAuth session in the keychain
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: f.srv.URL,
		AccessToken: "stale-access", RefreshToken: "cmd-refresh",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// When
	if _, err := run("", "auth", "status", "--api-url", f.srv.URL); err != nil {
		t.Fatalf("auth status: %v", err)
	}

	// Then — the rotated token is written back to the keychain
	creds, err := auth.Load(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != oauthAccess {
		t.Errorf("AccessToken = %q, want the refreshed token persisted", creds.AccessToken)
	}
}
