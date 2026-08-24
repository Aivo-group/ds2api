package enrollment

import (
	"context"
	"errors"
	"testing"
	"time"

	"ds2api/internal/config"
	"ds2api/internal/deepseek/signup"
	"ds2api/internal/smakmail"
)

type mailboxStub struct{}

func (mailboxStub) ListProducts(context.Context) ([]smakmail.Product, error) {
	return []smakmail.Product{{Product: "Eternal", UnitPriceKopeks: 10, Available: true}}, nil
}
func (mailboxStub) CreateOrder(context.Context, string, int, string) (smakmail.Order, error) {
	return smakmail.Order{OrderID: "order-1"}, nil
}
func (mailboxStub) WaitOrderResult(context.Context, string, time.Duration) ([]smakmail.Mailbox, error) {
	return []smakmail.Mailbox{{Email: "new@example.com", Password: "mail-password"}}, nil
}
func (mailboxStub) GetOrder(context.Context, string) (smakmail.Order, error) {
	return smakmail.Order{TotalKopeks: 10}, nil
}
func (mailboxStub) WaitLatestCode(context.Context, string, string, string, time.Duration) (smakmail.VerificationCode, error) {
	return smakmail.VerificationCode{Found: true, Code: "123456"}, nil
}

type registrarStub struct{ request signup.RegisterRequest }

func (s *registrarStub) Register(_ context.Context, request signup.RegisterRequest) (signup.RegisterResult, error) {
	s.request = request
	return signup.RegisterResult{UserID: "user-1"}, nil
}

type sinkStub struct{ account config.Account }

func (s *sinkStub) Add(_ context.Context, account config.Account) error {
	s.account = account
	return nil
}

type browserStub struct{ err error }

func (s browserStub) RequestEmailCode(context.Context, Session) error { return s.err }

func TestPrepareStopsAtHumanVerification(t *testing.T) {
	t.Parallel()
	service, err := New(mailboxStub{}, &registrarStub{}, &sinkStub{}, CamoufoxPlaceholder{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Prepare(context.Background(), "Eternal", 20, "proxy-1")
	var manual *ManualActionRequiredError
	if !errors.As(err, &manual) {
		t.Fatalf("expected manual action error, got %v", err)
	}
	if session.Status != StatusManualActionRequired || session.Mailbox == "" || session.AccountPassword == "" || manual.URL != DeepSeekSignupURL {
		t.Fatalf("unexpected session: %#v, error=%#v", session, manual)
	}
}

func TestResumeRegistersAndImportsAccount(t *testing.T) {
	t.Parallel()
	registrar := &registrarStub{}
	sink := &sinkStub{}
	service, err := New(mailboxStub{}, registrar, sink, browserStub{})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{Status: StatusWaitingForCode, Mailbox: "new@example.com", MailboxPassword: "mail-password", AccountPassword: "account-password", DeviceID: "device-1", ProxyID: "proxy-1"}
	completed, err := service.Resume(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || registrar.request.VerificationCode != "123456" || sink.account.Email != session.Mailbox || sink.account.ProxyID != "proxy-1" {
		t.Fatalf("unexpected result: session=%#v request=%#v account=%#v", completed, registrar.request, sink.account)
	}
}
