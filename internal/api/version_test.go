package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerVersion(t *testing.T) {
	t.Run("reports the image tag", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/version/" {
				t.Errorf("path = %q", r.URL.Path)
			}
			fmt.Fprint(w, `{"ci_commit_sha":"67853f3","image_tag":"2.262.0","is_saas":true,"package_versions":{".":"2.262.0"}}`)
		}))
		defer srv.Close()

		// When
		got, err := ServerVersion(context.Background(), srv.Client(), srv.URL+"/")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != "2.262.0" {
			t.Errorf("ServerVersion = %q, want 2.262.0", got)
		}
	})

	t.Run("errors when the endpoint is absent", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()

		// When
		_, err := ServerVersion(context.Background(), srv.Client(), srv.URL)

		// Then
		if err == nil {
			t.Error("err = nil, want an error")
		}
	})

	t.Run("errors when the body is not the version document", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "<html>nope</html>")
		}))
		defer srv.Close()

		// When
		_, err := ServerVersion(context.Background(), srv.Client(), srv.URL)

		// Then
		if err == nil {
			t.Error("err = nil, want an error")
		}
	})

	t.Run("errors when the tag is missing", func(t *testing.T) {
		// Given
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ci_commit_sha":"67853f3"}`)
		}))
		defer srv.Close()

		// When
		_, err := ServerVersion(context.Background(), srv.Client(), srv.URL)

		// Then
		if err == nil {
			t.Error("err = nil, want an error")
		}
	})
}
