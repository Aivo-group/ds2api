package enrollment

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"ds2api/internal/config"
	"ds2api/internal/deepseek/signup"
	"ds2api/internal/smakmail"
)

const DeepSeekSignupURL = "https://chat.deepseek.com/sign_up"

type Status string

const (
	StatusManualActionRequired Status = "manual_action_required"
	StatusWaitingForCode       Status = "waiting_for_code"
	StatusCompleted            Status = "completed"
)

type Session struct {
	ID              string    `json:"id"`
	Status          Status    `json:"status"`
	Mailbox         string    `json:"mailbox"`
	MailboxPassword string    `json:"mailbox_password"`
	AccountPassword string    `json:"account_password"`
	DeviceID        string    `json:"device_id,omitempty"`
	ProxyID         string    `json:"proxy_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ManualActionRequiredError struct {
	URL     string
	Message string
}

func (e *ManualActionRequiredError) Error() string {
	return "manual action required: " + e.Message
}

type MailboxProvider interface {
	ListProducts(context.Context) ([]smakmail.Product, error)
	CreateOrder(context.Context, string, int, string) (smakmail.Order, error)
	WaitOrderResult(context.Context, string, time.Duration) ([]smakmail.Mailbox, error)
	GetOrder(context.Context, string) (smakmail.Order, error)
	WaitLatestCode(context.Context, string, string, string, time.Duration) (smakmail.VerificationCode, error)
}

type Registrar interface {
	Register(context.Context, signup.RegisterRequest) (signup.RegisterResult, error)
}

type AccountSink interface {
	Add(context.Context, config.Account) error
}

type BrowserHandoff interface {
	RequestEmailCode(context.Context, Session) error
}

type Service struct {
	mailbox   MailboxProvider
	registrar Registrar
	sink      AccountSink
	browser   BrowserHandoff
}

func New(mailbox MailboxProvider, registrar Registrar, sink AccountSink, browser BrowserHandoff) (*Service, error) {
	if mailbox == nil || registrar == nil || sink == nil || browser == nil {
		return nil, errors.New("mailbox, registrar, account sink, and browser handoff are required")
	}
	return &Service{mailbox: mailbox, registrar: registrar, sink: sink, browser: browser}, nil
}

func (s *Service) Prepare(ctx context.Context, product string, maxPriceKopeks int64, proxyID string) (Session, error) {
	if maxPriceKopeks <= 0 {
		return Session{}, errors.New("maximum mailbox price must be positive")
	}
	products, err := s.mailbox.ListProducts(ctx)
	if err != nil {
		return Session{}, err
	}
	var selected smakmail.Product
	for _, candidate := range products {
		if strings.EqualFold(candidate.Product, product) {
			selected = candidate
			break
		}
	}
	if selected.Product == "" || !selected.Available {
		return Session{}, fmt.Errorf("mailbox product %q is unavailable", product)
	}
	if selected.UnitPriceKopeks > maxPriceKopeks {
		return Session{}, fmt.Errorf("advertised mailbox price %d exceeds cap %d", selected.UnitPriceKopeks, maxPriceKopeks)
	}
	order, err := s.mailbox.CreateOrder(ctx, selected.Product, 1, "deepseek-enrollment-"+uuid.NewString())
	if err != nil {
		return Session{}, err
	}
	mailboxes, err := s.mailbox.WaitOrderResult(ctx, order.OrderID, time.Second)
	if err != nil {
		return Session{}, err
	}
	if len(mailboxes) != 1 {
		return Session{}, fmt.Errorf("expected one mailbox, got %d", len(mailboxes))
	}
	finalOrder, err := s.mailbox.GetOrder(ctx, order.OrderID)
	if err != nil {
		return Session{}, err
	}
	if finalOrder.TotalKopeks > maxPriceKopeks {
		return Session{}, fmt.Errorf("charged mailbox price %d exceeds cap %d", finalOrder.TotalKopeks, maxPriceKopeks)
	}
	password, err := generatePassword()
	if err != nil {
		return Session{}, err
	}
	session := Session{ID: uuid.NewString(), Status: StatusManualActionRequired, Mailbox: mailboxes[0].Email, MailboxPassword: mailboxes[0].Password, AccountPassword: password, ProxyID: strings.TrimSpace(proxyID), CreatedAt: time.Now().UTC()}
	if err := s.browser.RequestEmailCode(ctx, session); err != nil {
		var manual *ManualActionRequiredError
		if errors.As(err, &manual) {
			return session, err
		}
		return Session{}, err
	}
	session.Status = StatusWaitingForCode
	return session, nil
}

func (s *Service) Resume(ctx context.Context, session Session) (Session, error) {
	code, err := s.mailbox.WaitLatestCode(ctx, session.Mailbox, "deepseek", session.MailboxPassword, 2*time.Second)
	if err != nil {
		return session, err
	}
	_, err = s.registrar.Register(ctx, signup.RegisterRequest{Email: session.Mailbox, VerificationCode: code.Code, Password: session.AccountPassword, DeviceID: session.DeviceID})
	if err != nil {
		return session, err
	}
	if err := s.sink.Add(ctx, config.Account{Email: session.Mailbox, Password: session.AccountPassword, ProxyID: session.ProxyID, Remark: "deepseek enrollment"}); err != nil {
		return session, err
	}
	session.Status = StatusCompleted
	return session, nil
}

func generatePassword() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate account password: %w", err)
	}
	return "Ds!" + base64.RawURLEncoding.EncodeToString(raw), nil
}
