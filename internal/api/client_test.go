package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsersMe(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
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

		user, err := UsersMe(context.Background(), srv.URL+"/", "access-1")
		if err != nil {
			t.Fatal(err)
		}
		if user.Email != "kim@example.com" || user.UUID != "u-1" {
			t.Errorf("user = %+v", user)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := UsersMe(context.Background(), srv.URL, "expired")
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("err = %v, want a 401 error", err)
		}
	})
}
