package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testClient builds a Client pointed at a test server, using the server's own
// HTTP client so requests stay in-process.
func testClient(baseURL string, auth Auth, srv *httptest.Server) *Client {
	return NewClient(baseURL, auth, WithHTTPClient(srv.Client()))
}

func TestUsersMe(t *testing.T) {
	t.Run("happy path with bearer auth", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/auth/users/me/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Errorf("Authorization = %q, want Bearer access-1", got)
			}
			fmt.Fprint(w, `{"email":"kim@example.com","first_name":"Kim","last_name":"G","uuid":"u-1"}`)
		}))
		defer srv.Close()

		// When — base URL carries a trailing slash to prove NewClient trims it.
		user, err := testClient(srv.URL+"/", Bearer("access-1"), srv).UsersMe(context.Background())

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if user.Email != "kim@example.com" || user.UUID != "u-1" {
			t.Errorf("user = %+v", user)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		// When
		_, err := testClient(srv.URL, Bearer("expired"), srv).UsersMe(context.Background())

		// Then
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("err = %v, want a 401 error", err)
		}
	})
}

func TestProjects(t *testing.T) {
	t.Run("follows pagination across pages", func(t *testing.T) {
		// Given two pages: page 1's "next" points at a bogus host to prove
		// getList reuses the base URL and only carries over next's path + query.
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			if r.URL.Path != "/api/v1/projects/" || r.URL.Query().Get("organisation") != "3" {
				t.Errorf("request = %s %s", r.URL.Path, r.URL.RawQuery)
			}
			switch r.URL.Query().Get("page") {
			case "", "1":
				fmt.Fprint(w, `{"count":2,"next":"http://pagination.invalid/api/v1/projects/?organisation=3&page=2","results":[{"id":101,"name":"acme-api"}]}`)
			case "2":
				fmt.Fprint(w, `{"count":2,"next":null,"results":[{"id":202,"name":"beta"}]}`)
			default:
				t.Errorf("unexpected page = %q", r.URL.Query().Get("page"))
			}
		}))
		defer srv.Close()

		// When
		projects, err := testClient(srv.URL, Bearer("t"), srv).Projects(context.Background(), 3)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if hits != 2 {
			t.Errorf("server hits = %d, want 2 (both pages fetched)", hits)
		}
		if len(projects) != 2 {
			t.Fatalf("projects = %+v, want 2 items across both pages", projects)
		}
		if projects[0].ID != 101 || projects[0].Name != "acme-api" ||
			projects[1].ID != 202 || projects[1].Name != "beta" {
			t.Errorf("projects = %+v", projects)
		}
	})

	t.Run("lists ask for the largest page", func(t *testing.T) {
		// The backend's CustomPagination caps page_size at 999 and clamps
		// larger asks, so requesting it makes a list one round-trip instead
		// of a sequential walk at the server's default page size.
		var gotPageSize string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPageSize = r.URL.Query().Get("page_size")
			if r.URL.Query().Get("organisation") != "3" {
				t.Errorf("query = %q, want the original params kept", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
		}))
		defer srv.Close()

		// When
		if _, err := testClient(srv.URL, Bearer("t"), srv).Projects(context.Background(), 3); err != nil {
			t.Fatal(err)
		}

		// Then
		if gotPageSize != "999" {
			t.Errorf("page_size = %q, want 999", gotPageSize)
		}
	})

	t.Run("an empty paginated list is an empty slice, not nil", func(t *testing.T) {
		// Given — the Admin API paginates, so this is the live empty shape
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"count":0,"next":null,"results":[]}`)
		}))
		defer srv.Close()

		// When
		projects, err := testClient(srv.URL, Bearer("t"), srv).Projects(context.Background(), 3)

		// Then — the documented JSON contract is [] for an empty list, so the
		// slice must be non-nil (a nil slice renders null)
		if err != nil {
			t.Fatal(err)
		}
		if projects == nil {
			t.Error("projects = nil slice, want empty non-nil ([] not null)")
		}
		if len(projects) != 0 {
			t.Errorf("projects = %+v, want empty", projects)
		}
	})

	t.Run("bare array response", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `[{"id":101,"name":"acme-api"},{"id":202,"name":"beta"}]`)
		}))
		defer srv.Close()

		// When
		projects, err := testClient(srv.URL, Bearer("t"), srv).Projects(context.Background(), 3)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 2 || projects[1].Name != "beta" {
			t.Errorf("projects = %+v", projects)
		}
	})
}

func TestCreateProject(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Name         string `json:"name"`
			Organisation int    `json:"organisation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Name != "acme-web" || body.Organisation != 3 {
			t.Errorf("body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":999,"name":"acme-web"}`)
	}))
	defer srv.Close()

	// When
	p, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "acme-web", "organisation": 3})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 999 || p.Name != "acme-web" {
		t.Errorf("project = %+v", p)
	}
}

func TestApiMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"detail", `{"detail":"You do not have permission to perform this action."}`, "You do not have permission to perform this action."},
		{"field array", `{"project":["The project has reached the maximum allowed segments limit."]}`, "The project has reached the maximum allowed segments limit."},
		{"field string", `{"project":"The Project has reached the maximum allowed features limit."}`, "The Project has reached the maximum allowed features limit."},
		{"multiple fields sorted", `{"b":["two"],"a":["one"]}`, "one; two"},
		{"empty", ``, ""},
		{"non-object", `["nope"]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apiMessage([]byte(tt.body)); got != tt.want {
				t.Errorf("apiMessage = %q, want %q", got, tt.want)
			}
		})
	}
}

// capServer serves a project POST that 403s, plus the subscription-metadata and
// project-list lookups CreateProject uses to disambiguate a project cap.
func capServer(t *testing.T, maxProjects, existingProjects int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"You do not have permission to perform this action."}`)
	})
	mux.HandleFunc("GET /api/v1/organisations/7/get-subscription-metadata/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"max_projects":%d}`, maxProjects)
	})
	mux.HandleFunc("GET /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		list := make([]string, existingProjects)
		for i := range list {
			list[i] = fmt.Sprintf(`{"id":%d,"name":"p%d"}`, i+1, i+1)
		}
		fmt.Fprintf(w, "[%s]", strings.Join(list, ","))
	})
	return httptest.NewServer(mux)
}

func TestCreateProjectProjectCap(t *testing.T) {
	t.Run("at the cap surfaces ErrPlanGated with a clear reason", func(t *testing.T) {
		srv := capServer(t, 1, 1)
		defer srv.Close()

		_, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "x", "organisation": 7})
		if !errors.Is(err, ErrPlanGated) {
			t.Fatalf("err = %v, want ErrPlanGated", err)
		}
		if !strings.Contains(err.Error(), "allows 1 project") || !strings.Contains(err.Error(), "already has 1") {
			t.Errorf("err = %q, want the plan/count reason", err)
		}
	})

	t.Run("a 403 below the cap stays a permission error", func(t *testing.T) {
		srv := capServer(t, 5, 1) // room for more projects → the 403 is RBAC, not a cap
		defer srv.Close()

		_, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "x", "organisation": 7})
		if errors.Is(err, ErrPlanGated) {
			t.Fatalf("err = %v, must not be ErrPlanGated below the cap", err)
		}
		if statusOf(err) != http.StatusForbidden || !strings.Contains(err.Error(), "do not have permission") {
			t.Errorf("err = %q, want the original 403 permission error", err)
		}
	})
}

func TestClassifyLimit(t *testing.T) {
	// The exact backend detail strings, and which recovery each routes to (see
	// errors.go): quota caps are enterprise-negotiable (→ support), upgrade
	// limits are self-serve (→ pricing). If these stop matching, hints vanish.
	tests := []struct {
		msg  string
		want error // ErrQuotaExceeded, ErrPlanGated, or nil
	}{
		{"The Project has reached the maximum allowed features limit.", ErrQuotaExceeded},
		{"The project has reached the maximum allowed segments limit.", ErrQuotaExceeded},
		{"The environment has reached the maximum allowed segments overrides limit.", ErrQuotaExceeded},
		{"Please upgrade your plan to add additional seats/users", ErrPlanGated},
		{"Joining the organisation has failed due to a payment issue. Please contact your organisation's admin.", ErrPlanGated},
		{"Organisation has no subscription", ErrPlanGated},
		{"You do not have permission to perform this action.", nil},
		{"Not found.", nil},
		{"", nil},
	}
	for _, tt := range tests {
		got := classifyLimit(tt.msg)
		switch {
		case tt.want == nil && got != nil:
			t.Errorf("classifyLimit(%q) = %v, want nil", tt.msg, got)
		case tt.want != nil && !errors.Is(got, tt.want):
			t.Errorf("classifyLimit(%q) = %v, want match %v", tt.msg, got, tt.want)
		}
	}
}

func TestResponseErrorQuota(t *testing.T) {
	// A quota-cap 400 surfaces as ErrQuotaExceeded carrying the API's own reason.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"project":["The project has reached the maximum allowed segments limit."]}`)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "x", "organisation": 1})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
	if errors.Is(err, ErrPlanGated) {
		t.Errorf("a quota cap must not also match ErrPlanGated: %v", err)
	}
	if !strings.Contains(err.Error(), "maximum allowed segments limit") {
		t.Errorf("err = %q, want the API reason", err)
	}
}

func TestResponseErrorPlanGated(t *testing.T) {
	// A self-serve upgrade limit surfaces as ErrPlanGated.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"detail":"Please upgrade your plan to add additional seats/users"}`)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "x", "organisation": 1})
	if !errors.Is(err, ErrPlanGated) {
		t.Fatalf("err = %v, want ErrPlanGated", err)
	}
	if errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("a seat limit must not also match ErrQuotaExceeded: %v", err)
	}
}

func TestResponseErrorSurfacesDetail(t *testing.T) {
	// A non-plan error still surfaces the API's message rather than a bare status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"detail":"Invalid organisation."}`)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, Bearer("t"), srv).CreateProject(context.Background(), map[string]any{"name": "x"})
	if err == nil || errors.Is(err, ErrPlanGated) {
		t.Fatalf("err = %v, want a plain error", err)
	}
	if !strings.Contains(err.Error(), "Invalid organisation.") {
		t.Errorf("err = %q, want the API detail", err)
	}
}

func TestEnvironments(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/environments/" || r.URL.Query().Get("project") != "101" {
			t.Errorf("request = %s %s", r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"count":2,"results":[
			{"id":1,"name":"Development","api_key":"WqXhZk8sVY3dGgTqZ9pJmN"},
			{"id":2,"name":"Production","api_key":"K2mVsGdXhZ8kQqZ9pJmNbJ"}]}`)
	}))
	defer srv.Close()

	// When
	envs, err := testClient(srv.URL, APIKey("k.s"), srv).Environments(context.Background(), 101)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].APIKey != "WqXhZk8sVY3dGgTqZ9pJmN" || envs[1].Name != "Production" {
		t.Errorf("environments = %+v", envs)
	}
}

func TestFeatures(t *testing.T) {
	t.Run("happy path with embedded environment state", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/projects/101/features/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			if got := r.URL.Query().Get("environment"); got != "1" {
				t.Errorf("environment = %q, want 1", got)
			}
			fmt.Fprint(w, `{"count":2,"next":null,"previous":null,"results":[
				{"id":1,"name":"onboarding_banner","type":"STANDARD","lifecycle_stage":"live",
				 "num_segment_overrides":0,"num_identity_overrides":0,"code_references_counts":[],
				 "environment_feature_state":{"enabled":true,"feature_state_value":null}},
				{"id":2,"name":"max_items","type":"STANDARD",
				 "num_segment_overrides":1,"num_identity_overrides":2,
				 "code_references_counts":[{"count":3}],
				 "environment_feature_state":{"enabled":false,"feature_state_value":25}}
			]}`)
		}))
		defer srv.Close()

		// When
		features, err := testClient(srv.URL, APIKey("k.s"), srv).Features(context.Background(), 101, 1, 0, "")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(features) != 2 {
			t.Fatalf("features = %+v", features)
		}
		if features[0].Name != "onboarding_banner" || !features[0].EnvironmentState.Enabled ||
			features[0].EnvironmentState.Value != nil || features[0].LifecycleStage != "live" {
			t.Errorf("features[0] = %+v", features[0])
		}
		if features[1].EnvironmentState.Value != float64(25) || features[1].EnvironmentState.Enabled ||
			features[1].NumSegmentOverrides != 1 || *features[1].NumIdentityOverrides != 2 ||
			features[1].CodeReferences() != 3 {
			t.Errorf("features[1] = %+v", features[1])
		}
	})

	t.Run("bad request", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		// When
		_, err := testClient(srv.URL, APIKey("k.s"), srv).Features(context.Background(), 101, 1, 0, "")

		// Then
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("err = %v, want 403", err)
		}
	})

	t.Run("segment param is sent when non-zero", func(t *testing.T) {
		// Given
		var gotSegment string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotSegment = r.URL.Query().Get("segment")
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		}))
		defer srv.Close()

		// When
		if _, err := testClient(srv.URL, APIKey("k.s"), srv).Features(context.Background(), 101, 1, 12, ""); err != nil {
			t.Fatal(err)
		}

		// Then
		if gotSegment != "12" {
			t.Errorf("segment = %q, want 12", gotSegment)
		}
	})

	t.Run("search param is sent when non-empty, url-escaped", func(t *testing.T) {
		// Given
		var gotSearch string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotSearch = r.URL.Query().Get("search")
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		}))
		defer srv.Close()

		// When
		if _, err := testClient(srv.URL, APIKey("k.s"), srv).Features(context.Background(), 101, 1, 0, "my flag"); err != nil {
			t.Fatal(err)
		}

		// Then
		if gotSearch != "my flag" {
			t.Errorf("search = %q, want my flag", gotSearch)
		}
	})
}

func TestDeleteSegmentOverride(t *testing.T) {
	t.Run("posts the feature and segment, accepts 204", func(t *testing.T) {
		// Given
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.URL.Path != "/api/experiments/environments/envkey/delete-segment-override/" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		// When
		err := testClient(srv.URL, APIKey("k.s"), srv).DeleteSegmentOverride(context.Background(), "envkey", FeatureRef{Name: "max_items"}, 12)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if body["feature"].(map[string]any)["name"] != "max_items" ||
			body["segment"].(map[string]any)["id"] != float64(12) {
			t.Errorf("body = %+v", body)
		}
	})

	t.Run("404 becomes a no-override error", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		// When
		err := testClient(srv.URL, APIKey("k.s"), srv).DeleteSegmentOverride(context.Background(), "envkey", FeatureRef{Name: "max_items"}, 12)

		// Then
		if err == nil || !strings.Contains(err.Error(), "segment 12") {
			t.Errorf("err = %v, want a no-override error", err)
		}
	})
}

func TestCreateEnvironment(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/environments/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Name    string `json:"name"`
			Project int    `json:"project"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Name != "Development" || body.Project != 101 {
			t.Errorf("body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":5,"name":"Development","api_key":"FreshDevKey0000000000"}`)
	}))
	defer srv.Close()

	// When
	env, err := testClient(srv.URL, Bearer("t"), srv).CreateEnvironment(context.Background(), map[string]any{"name": "Development", "project": 101})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if env.APIKey != "FreshDevKey0000000000" || env.Name != "Development" {
		t.Errorf("env = %+v", env)
	}
}

func TestOrganisations(t *testing.T) {
	t.Run("master API key auth uses the Api-Key scheme", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/organisations/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Api-Key AbCd1234.secret" {
				t.Errorf("Authorization = %q, want Api-Key AbCd1234.secret", got)
			}
			fmt.Fprint(w, `{"count":1,"results":[{"id":3,"name":"Acme"}]}`)
		}))
		defer srv.Close()

		// When
		orgs, err := testClient(srv.URL, APIKey("AbCd1234.secret"), srv).Organisations(context.Background())

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(orgs) != 1 || orgs[0].ID != 3 || orgs[0].Name != "Acme" {
			t.Errorf("orgs = %+v", orgs)
		}
	})

	t.Run("bearer auth lists the user's organisations", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Errorf("Authorization = %q, want Bearer access-1", got)
			}
			fmt.Fprint(w, `{"count":2,"results":[{"id":3,"name":"Acme"},{"id":7,"name":"Beta"}]}`)
		}))
		defer srv.Close()

		// When
		orgs, err := testClient(srv.URL, Bearer("access-1"), srv).Organisations(context.Background())

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(orgs) != 2 || orgs[1].Name != "Beta" {
			t.Errorf("orgs = %+v", orgs)
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		// When
		_, err := testClient(srv.URL, APIKey("bad.key"), srv).Organisations(context.Background())

		// Then
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("err = %v, want a 403 error", err)
		}
	})
}

// The Admin API's segment serializer requires `project` in the request body
// (the URL's project_pk is not enough), so create/update must send it or the
// API returns 400 {"project":["This field is required."]}.
func TestCreateSegment(t *testing.T) {
	// Given
	var body Segment
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/101/segments/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":42,"name":"us-adults","project":101}`)
	}))
	defer srv.Close()

	// When
	seg, err := testClient(srv.URL, Bearer("t"), srv).CreateSegment(context.Background(), 101, Segment{
		Name:  "us-adults",
		Rules: []SegmentRule{{Type: "ALL"}},
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if body.Project != 101 {
		t.Errorf("body.project = %d, want 101 (must be sent in the body)", body.Project)
	}
	if seg.ID != 42 {
		t.Errorf("segment = %+v", seg)
	}
}

func TestUpdateSegment(t *testing.T) {
	// Given
	var body Segment
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/projects/101/segments/42/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"id":42,"name":"us-adults","project":101}`)
	}))
	defer srv.Close()

	// When
	_, err := testClient(srv.URL, Bearer("t"), srv).UpdateSegment(context.Background(), 101, 42, Segment{
		Name:  "us-adults",
		Rules: []SegmentRule{{Type: "ALL"}},
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if body.Project != 101 {
		t.Errorf("body.project = %d, want 101 (must be sent in the body)", body.Project)
	}
}

// The Admin API's mv-option serializer reads attrs["feature"] in validate()
// even on a PATCH, so a partial update that omits `feature` triggers an
// unhandled KeyError → 500. UpdateMVOption must always send the feature id.
func TestUpdateMVOption(t *testing.T) {
	// Given
	var body MultivariateOption
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/projects/101/features/5/mv-options/9/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"id":9,"feature":5,"default_percentage_allocation":40}`)
	}))
	defer srv.Close()

	// When
	w := 40.0
	_, err := testClient(srv.URL, Bearer("t"), srv).UpdateMVOption(context.Background(), 101, 5, 9,
		MultivariateOption{DefaultPercentageAllocation: &w})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if body.Feature != 5 {
		t.Errorf("body.feature = %d, want 5 (must be sent to avoid the backend KeyError)", body.Feature)
	}
}

func TestEdgeIdentityUUID(t *testing.T) {
	// Edge (DynamoDB) identities paginate differently from page-number
	// endpoints: no "count", and "next" carries a base64 last_evaluated_key
	// cursor rather than a page number. getList must follow it the same way.
	// The wanted identity sits on page two — the exact case that used to
	// report "not found".
	t.Run("follows last_evaluated_key pagination to a match on page two", func(t *testing.T) {
		// Given
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			if r.URL.Path != "/api/v1/environments/env.key/edge-identities/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			if got := r.URL.Query().Get("q"); got != `"user@acme.io"` {
				t.Errorf("q = %q, want the quoted exact-match query", got)
			}
			switch r.URL.Query().Get("last_evaluated_key") {
			case "":
				// First page: no count, cursor points at a bogus host to
				// prove getList reuses the base URL and only carries path + query.
				fmt.Fprint(w, `{"next":"http://edge.invalid/api/v1/environments/env.key/edge-identities/?q=%22user%40acme.io%22&last_evaluated_key=eyJpZCI6MX0=","previous":null,"results":[{"identity_uuid":"uuid-1","identifier":"someone-else"}]}`)
			case "eyJpZCI6MX0=":
				fmt.Fprint(w, `{"next":null,"previous":null,"results":[{"identity_uuid":"uuid-2","identifier":"user@acme.io"}]}`)
			default:
				t.Errorf("unexpected last_evaluated_key = %q", r.URL.Query().Get("last_evaluated_key"))
			}
		}))
		defer srv.Close()

		// When
		uuid, found, err := testClient(srv.URL, APIKey("k.s"), srv).EdgeIdentityUUID(context.Background(), "env.key", "user@acme.io")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if hits != 2 {
			t.Errorf("server hits = %d, want 2 (cursor followed to page two)", hits)
		}
		if !found || uuid != "uuid-2" {
			t.Errorf("uuid = %q, found = %v, want uuid-2, true", uuid, found)
		}
	})
}
