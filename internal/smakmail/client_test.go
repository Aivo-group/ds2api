package smakmail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateOrderSendsCredentialsAndIdempotencyKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/orders" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "order-1" {
			t.Fatalf("unexpected idempotency key: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["product"] != "Eternal" || body["qty"] != float64(1) {
			t.Fatalf("unexpected body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"order_id":"order-id","status":"awaiting_backend","bridge":{"status":"running","total_kopeks":100}}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	order, err := client.CreateOrder(context.Background(), "Eternal", 1, "order-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderID != "order-id" || order.Status != "running" || order.TotalKopeks != 100 {
		t.Fatalf("unexpected order: %#v", order)
	}
}

func TestLatestCodeUsesMailboxPassword(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("email") != "user@example.com" || r.URL.Query().Get("service") != "deepseek" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Mailbox-Password"); got != "mail-secret" {
			t.Fatalf("unexpected mailbox password header: %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true,"code":"123456","message_id":"message-1"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	code, err := client.LatestCode(context.Background(), "user@example.com", "deepseek", "mail-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !code.Found || code.Code != "123456" || code.MessageID != "message-1" {
		t.Fatalf("unexpected code: %#v", code)
	}
}

func TestWaitOrderResult(t *testing.T) {
	t.Parallel()
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orders/order-id":
			status := "running"
			if polls.Add(1) >= 2 {
				status = "done"
			}
			_, _ = w.Write([]byte(`{"ok":true,"order":{"order_id":"order-id","status":"` + status + `"}}`))
		case "/orders/order-id/result":
			_, _ = w.Write([]byte(`{"ok":true,"items":[{"email":"user@example.com","password":"mail-secret"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	mailboxes, err := client.WaitOrderResult(context.Background(), "order-id", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(mailboxes) != 1 || mailboxes[0].Email != "user@example.com" {
		t.Fatalf("unexpected mailboxes: %#v", mailboxes)
	}
}

func TestAPIErrorDoesNotExposeCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"super-secret-token and mail-secret are not authorized"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := New(server.URL, "super-secret-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.LatestCode(context.Background(), "user@example.com", "deepseek", "mail-secret")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "super-secret-token") || strings.Contains(err.Error(), "mail-secret") {
		t.Fatalf("error contains a credential: %v", err)
	}
}

func TestOKFalseReturnsAPIError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"quota exceeded"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetBalance(context.Background())
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLatestCodeAcceptsCodesArray(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"status":"found","codes":["654321"]}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	code, err := client.LatestCode(context.Background(), "user@example.com", "deepseek", "mail-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !code.Found || code.Code != "654321" || code.Status != "found" {
		t.Fatalf("unexpected code: %#v", code)
	}
}
