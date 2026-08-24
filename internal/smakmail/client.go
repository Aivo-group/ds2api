package smakmail

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

const (
	DefaultBaseURL = "https://api.smakmail.com/api/v1"
	maxBodyBytes   = 1 << 20
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func New(baseURL, token string, options ...Option) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid SmakMail base URL: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("SmakMail API token is required")
	}
	c := &Client{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
	for _, option := range options {
		option(c)
	}
	return c, nil
}

type Balance struct {
	BalanceKopeks int64  `json:"balance_kopeks"`
	BalanceRub    string `json:"balance_rub"`
}

type Product struct {
	Product         string `json:"product"`
	Title           string `json:"title"`
	UnitPriceKopeks int64  `json:"unit_price_kopeks"`
	Available       bool   `json:"available"`
}

type Order struct {
	OrderID         string `json:"order_id"`
	Product         string `json:"product"`
	Quantity        int    `json:"qty"`
	UnitPriceKopeks int64  `json:"unit_price_kopeks"`
	TotalKopeks     int64  `json:"total_kopeks"`
	Status          string `json:"status"`
	LastError       any    `json:"last_error"`
}

type Mailbox struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type VerificationCode struct {
	Code      string          `json:"code"`
	Codes     []string        `json:"codes,omitempty"`
	Found     bool            `json:"found"`
	Status    string          `json:"status,omitempty"`
	MessageID string          `json:"message_id,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("SmakMail API returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("SmakMail API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func (c *Client) GetBalance(ctx context.Context) (Balance, error) {
	var response struct {
		OK bool `json:"ok"`
		Balance
	}
	if err := c.do(ctx, http.MethodGet, "/balance", nil, "", "", &response); err != nil {
		return Balance{}, err
	}
	return response.Balance, nil
}

func (c *Client) ListProducts(ctx context.Context) ([]Product, error) {
	var response struct {
		OK    bool      `json:"ok"`
		Items []Product `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/products", nil, "", "", &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) CreateOrder(ctx context.Context, product string, quantity int, idempotencyKey string) (Order, error) {
	if strings.TrimSpace(product) == "" {
		return Order{}, errors.New("product is required")
	}
	if quantity <= 0 {
		return Order{}, errors.New("quantity must be positive")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return Order{}, errors.New("idempotency key is required")
	}
	body := struct {
		Product  string `json:"product"`
		Quantity int    `json:"qty"`
	}{Product: strings.TrimSpace(product), Quantity: quantity}
	var response struct {
		OK      bool   `json:"ok"`
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
		Bridge  Order  `json:"bridge"`
	}
	if err := c.do(ctx, http.MethodPost, "/orders", body, idempotencyKey, "", &response); err != nil {
		return Order{}, err
	}
	response.Bridge.OrderID = response.OrderID
	if response.Bridge.Status == "" {
		response.Bridge.Status = response.Status
	}
	return response.Bridge, nil
}

func (c *Client) GetOrder(ctx context.Context, orderID string) (Order, error) {
	if strings.TrimSpace(orderID) == "" {
		return Order{}, errors.New("order ID is required")
	}
	var response struct {
		OK    bool  `json:"ok"`
		Order Order `json:"order"`
	}
	path := "/orders/" + url.PathEscape(strings.TrimSpace(orderID))
	if err := c.do(ctx, http.MethodGet, path, nil, "", "", &response); err != nil {
		return Order{}, err
	}
	return response.Order, nil
}

func (c *Client) GetOrderResult(ctx context.Context, orderID string) ([]Mailbox, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.New("order ID is required")
	}
	var response struct {
		OK    bool      `json:"ok"`
		Items []Mailbox `json:"items"`
	}
	path := "/orders/" + url.PathEscape(strings.TrimSpace(orderID)) + "/result"
	if err := c.do(ctx, http.MethodGet, path, nil, "", "", &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) LatestCode(ctx context.Context, email, service, mailboxPassword string) (VerificationCode, error) {
	if strings.TrimSpace(email) == "" {
		return VerificationCode{}, errors.New("email is required")
	}
	if strings.TrimSpace(mailboxPassword) == "" {
		return VerificationCode{}, errors.New("mailbox password is required")
	}
	query := url.Values{"email": {strings.TrimSpace(email)}}
	if strings.TrimSpace(service) != "" {
		query.Set("service", strings.TrimSpace(service))
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/mailbox/latest-code?"+query.Encode(), nil, "", mailboxPassword, &raw); err != nil {
		return VerificationCode{}, err
	}
	var response struct {
		Code      string   `json:"code"`
		Codes     []string `json:"codes"`
		Found     bool     `json:"found"`
		Status    string   `json:"status"`
		MessageID string   `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return VerificationCode{}, fmt.Errorf("decode SmakMail verification code: %w", err)
	}
	code := strings.TrimSpace(response.Code)
	if code == "" && len(response.Codes) > 0 {
		code = strings.TrimSpace(response.Codes[0])
	}
	return VerificationCode{
		Code:      code,
		Codes:     response.Codes,
		Found:     response.Found || code != "",
		Status:    response.Status,
		MessageID: response.MessageID,
		Raw:       raw,
	}, nil
}

func (c *Client) WaitLatestCode(ctx context.Context, email, service, mailboxPassword string, pollInterval time.Duration) (VerificationCode, error) {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		code, err := c.LatestCode(ctx, email, service, mailboxPassword)
		if err != nil {
			return VerificationCode{}, err
		}
		if code.Found {
			return code, nil
		}
		select {
		case <-ctx.Done():
			return VerificationCode{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) WaitOrderResult(ctx context.Context, orderID string, pollInterval time.Duration) ([]Mailbox, error) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		order, err := c.GetOrder(ctx, orderID)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(order.Status)) {
		case "done", "completed":
			return c.GetOrderResult(ctx, orderID)
		case "failed", "cancelled", "canceled", "expired":
			return nil, fmt.Errorf("SmakMail order %s ended with status %s", orderID, order.Status)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, idempotencyKey, mailboxPassword string, output any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode SmakMail request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create SmakMail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if mailboxPassword != "" {
		req.Header.Set("X-Mailbox-Password", mailboxPassword)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call SmakMail API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read SmakMail response: %w", err)
	}
	if len(data) > maxBodyBytes {
		return errors.New("SmakMail response exceeds size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &APIError{StatusCode: resp.StatusCode, Message: redactSecrets(apiErrorMessage(data), c.token, mailboxPassword)}
	}
	var envelope struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.OK != nil && !*envelope.OK {
		return &APIError{StatusCode: resp.StatusCode, Message: redactSecrets(apiErrorMessage(data), c.token, mailboxPassword)}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode SmakMail response: %w", err)
	}
	return nil
}

func redactSecrets(message string, secrets ...string) string {
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

func apiErrorMessage(data []byte) string {
	var response struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(data, &response) != nil {
		return "request failed"
	}
	for _, message := range []string{response.Error, response.Message, response.Detail} {
		if strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return "request failed"
}
