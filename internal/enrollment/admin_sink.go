package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ds2api/internal/config"
)

type AdminSink struct {
	baseURL    string
	adminKey   string
	httpClient *http.Client
}

func NewAdminSink(baseURL, adminKey string) (*AdminSink, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil || baseURL == "" {
		return nil, errors.New("valid DS2API base URL is required")
	}
	if strings.TrimSpace(adminKey) == "" {
		return nil, errors.New("DS2API admin key is required")
	}
	return &AdminSink{baseURL: baseURL, adminKey: adminKey, httpClient: &http.Client{Timeout: 20 * time.Second}}, nil
}

func (s *AdminSink) Add(ctx context.Context, account config.Account) error {
	token, err := s.login(ctx)
	if err != nil {
		return err
	}
	return s.post(ctx, "/admin/accounts", account, token, nil)
}

func (s *AdminSink) login(ctx context.Context) (string, error) {
	var response struct {
		Token string `json:"token"`
	}
	if err := s.post(ctx, "/admin/login", map[string]any{"admin_key": s.adminKey, "expire_hours": 1}, "", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Token) == "" {
		return "", errors.New("DS2API admin login returned no token")
	}
	return response.Token, nil
}

func (s *AdminSink) post(ctx context.Context, path string, body any, bearer string, output any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode DS2API admin request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create DS2API admin request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send DS2API admin request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read DS2API admin response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DS2API admin request returned HTTP %d", resp.StatusCode)
	}
	if output != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, output); err != nil {
			return fmt.Errorf("decode DS2API admin response: %w", err)
		}
	}
	return nil
}
