package enrollment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ds2api/internal/config"
)

func TestAdminSinkLogsInAndAddsAccount(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/login":
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		case "/admin/accounts":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				t.Fatalf("missing admin bearer token")
			}
			var account config.Account
			if err := json.NewDecoder(r.Body).Decode(&account); err != nil {
				t.Fatal(err)
			}
			if account.Email != "new@example.com" || account.Password != "secret" {
				t.Fatalf("unexpected account: %#v", account)
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	sink, err := NewAdminSink(server.URL, "admin-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Add(context.Background(), config.Account{Email: "new@example.com", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
}
