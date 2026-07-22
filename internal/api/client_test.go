package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

		// When
		user, err := UsersMe(context.Background(), srv.URL+"/", Bearer("access-1"))

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
		_, err := UsersMe(context.Background(), srv.URL, Bearer("expired"))

		// Then
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("err = %v, want a 401 error", err)
		}
	})
}

func TestProjects(t *testing.T) {
	t.Run("paginated response", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/projects/" || r.URL.Query().Get("organisation") != "3" {
				t.Errorf("request = %s %s", r.URL.Path, r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"count":1,"results":[{"id":101,"name":"acme-api"}]}`)
		}))
		defer srv.Close()

		// When
		projects, err := Projects(context.Background(), srv.URL, Bearer("t"), 3)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(projects) != 1 || projects[0].ID != 101 || projects[0].Name != "acme-api" {
			t.Errorf("projects = %+v", projects)
		}
	})

	t.Run("bare array response", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `[{"id":101,"name":"acme-api"},{"id":202,"name":"beta"}]`)
		}))
		defer srv.Close()

		// When
		projects, err := Projects(context.Background(), srv.URL, Bearer("t"), 3)

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
	p, err := CreateProject(context.Background(), srv.URL, Bearer("t"), map[string]any{"name": "acme-web", "organisation": 3})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 999 || p.Name != "acme-web" {
		t.Errorf("project = %+v", p)
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
	envs, err := Environments(context.Background(), srv.URL, APIKey("k.s"), 101)

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
		features, err := Features(context.Background(), srv.URL, APIKey("k.s"), 101, 1, 0)

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
		_, err := Features(context.Background(), srv.URL, APIKey("k.s"), 101, 1, 0)

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
		if _, err := Features(context.Background(), srv.URL, APIKey("k.s"), 101, 1, 12); err != nil {
			t.Fatal(err)
		}

		// Then
		if gotSegment != "12" {
			t.Errorf("segment = %q, want 12", gotSegment)
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
		err := DeleteSegmentOverride(context.Background(), srv.URL, APIKey("k.s"), "envkey", "max_items", 12)

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
		err := DeleteSegmentOverride(context.Background(), srv.URL, APIKey("k.s"), "envkey", "max_items", 12)

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
	env, err := CreateEnvironment(context.Background(), srv.URL, Bearer("t"), "Development", 101)

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
		orgs, err := Organisations(context.Background(), srv.URL, APIKey("AbCd1234.secret"))

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
		orgs, err := Organisations(context.Background(), srv.URL, Bearer("access-1"))

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
		_, err := Organisations(context.Background(), srv.URL, APIKey("bad.key"))

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
	seg, err := CreateSegment(context.Background(), srv.URL, Bearer("t"), 101, Segment{
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
	_, err := UpdateSegment(context.Background(), srv.URL, Bearer("t"), 101, 42, Segment{
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
	_, err := UpdateMVOption(context.Background(), srv.URL, Bearer("t"), 101, 5, 9,
		MultivariateOption{DefaultPercentageAllocation: &w})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if body.Feature != 5 {
		t.Errorf("body.feature = %d, want 5 (must be sent to avoid the backend KeyError)", body.Feature)
	}
}
