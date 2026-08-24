package signup

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
)

const DefaultBaseURL = "https://chat.deepseek.com/api/v0/users"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func New(baseURL string, options ...Option) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid DeepSeek signup base URL: %w", err)
	}
	client := &Client{baseURL: baseURL, httpClient: &http.Client{Timeout: 30 * time.Second}}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

type RegisterRequest struct {
	Email            string
	VerificationCode string
	Password         string
	Locale           string
	Region           string
	DeviceID         string
}

type RegisterResult struct {
	UserID string
}

type BusinessError struct {
	Code    int
	Message string
}

func (e *BusinessError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("DeepSeek registration failed with biz_code=%d", e.Code)
	}
	return fmt.Sprintf("DeepSeek registration failed with biz_code=%d: %s", e.Code, e.Message)
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (RegisterResult, error) {
	req.Email = strings.TrimSpace(req.Email)
	req.VerificationCode = strings.TrimSpace(req.VerificationCode)
	if req.Email == "" || req.VerificationCode == "" || req.Password == "" {
		return RegisterResult{}, errors.New("email, verification code, and password are required")
	}
	if strings.TrimSpace(req.Locale) == "" {
		req.Locale = "en_US"
	}
	body := map[string]any{
		"locale": req.Locale,
		"region": strings.TrimSpace(req.Region),
		"payload": map[string]string{
			"email":                   req.Email,
			"email_verification_code": req.VerificationCode,
			"password":                req.Password,
		},
		"device_id": strings.TrimSpace(req.DeviceID),
		"os":        "web",
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("encode DeepSeek registration: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/register", bytes.NewReader(encoded))
	if err != nil {
		return RegisterResult{}, fmt.Errorf("create DeepSeek registration request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("send DeepSeek registration request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RegisterResult{}, fmt.Errorf("read DeepSeek registration response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RegisterResult{}, fmt.Errorf("DeepSeek registration returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			BusinessCode    int    `json:"biz_code"`
			BusinessMessage string `json:"biz_msg"`
			BusinessData    struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return RegisterResult{}, fmt.Errorf("decode DeepSeek registration response: %w", err)
	}
	if payload.Data.BusinessCode != 0 {
		return RegisterResult{}, &BusinessError{Code: payload.Data.BusinessCode, Message: payload.Data.BusinessMessage}
	}
	return RegisterResult{UserID: strings.TrimSpace(payload.Data.BusinessData.User.ID)}, nil
}
