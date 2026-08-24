package signup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterUsesCurrentWebContract(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/register" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		payload, _ := body["payload"].(map[string]any)
		if payload["email"] != "new@example.com" || payload["email_verification_code"] != "123456" || payload["password"] != "password" {
			t.Fatalf("unexpected payload: %#v", body)
		}
		if body["os"] != "web" || body["device_id"] != "device-1" {
			t.Fatalf("unexpected metadata: %#v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"biz_code":0,"biz_data":{"user":{"id":"user-1"}}}}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Register(context.Background(), RegisterRequest{Email: "new@example.com", VerificationCode: "123456", Password: "password", DeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "user-1" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestRegisterReturnsTypedBusinessError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"biz_code":8,"biz_msg":"EMAIL_PASSCODE_FAILED"}}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Register(context.Background(), RegisterRequest{Email: "new@example.com", VerificationCode: "bad", Password: "password"})
	var businessErr *BusinessError
	if !errors.As(err, &businessErr) || businessErr.Code != 8 {
		t.Fatalf("unexpected error: %v", err)
	}
}
