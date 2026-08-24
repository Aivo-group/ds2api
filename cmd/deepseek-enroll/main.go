package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"ds2api/internal/config"
	"ds2api/internal/deepseek/signup"
	"ds2api/internal/enrollment"
	"ds2api/internal/smakmail"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "deepseek-enroll:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: deepseek-enroll <prepare|resume>")
	}
	switch args[0] {
	case "prepare":
		return prepare(args[1:])
	case "resume":
		return resume(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func prepare(args []string) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	out := flags.String("out", "", "new 0600 enrollment state file")
	product := flags.String("product", "Eternal", "SmakMail product")
	maxPrice := flags.Int64("max-price-kopeks", 0, "hard mailbox price cap")
	proxyID := flags.String("proxy-id", "", "DS2API proxy ID assigned after enrollment")
	wait := flags.Duration("wait", 2*time.Minute, "mailbox provisioning timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" || *maxPrice <= 0 {
		return errors.New("--out and a positive --max-price-kopeks are required")
	}
	mailbox, err := smakmail.New(os.Getenv("SMAKMAIL_BASE_URL"), os.Getenv("SMAKMAIL_API_TOKEN"))
	if err != nil {
		return err
	}
	registrar, err := signup.New(os.Getenv("DEEPSEEK_SIGNUP_BASE_URL"))
	if err != nil {
		return err
	}
	service, err := enrollment.New(mailbox, registrar, discardSink{}, enrollment.CamoufoxPlaceholder{})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	session, prepareErr := service.Prepare(ctx, *product, *maxPrice, *proxyID)
	var manual *enrollment.ManualActionRequiredError
	if prepareErr != nil && !errors.As(prepareErr, &manual) {
		return prepareErr
	}
	if err := writeState(*out, session); err != nil {
		return err
	}
	if manual != nil {
		fmt.Printf("state prepared; status=%s signup_url=%s state=%s\n", session.Status, manual.URL, *out)
		return nil
	}
	fmt.Printf("state prepared; status=%s state=%s\n", session.Status, *out)
	return nil
}

func resume(args []string) error {
	flags := flag.NewFlagSet("resume", flag.ContinueOnError)
	statePath := flags.String("state", "", "0600 enrollment state file")
	wait := flags.Duration("wait", 5*time.Minute, "verification-code wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*statePath) == "" {
		return errors.New("--state is required")
	}
	session, err := readState(*statePath)
	if err != nil {
		return err
	}
	mailbox, err := smakmail.New(os.Getenv("SMAKMAIL_BASE_URL"), os.Getenv("SMAKMAIL_API_TOKEN"))
	if err != nil {
		return err
	}
	registrar, err := signup.New(os.Getenv("DEEPSEEK_SIGNUP_BASE_URL"))
	if err != nil {
		return err
	}
	sink, err := enrollment.NewAdminSink(os.Getenv("DS2API_BASE_URL"), os.Getenv("DS2API_ADMIN_KEY"))
	if err != nil {
		return err
	}
	service, err := enrollment.New(mailbox, registrar, sink, enrollment.CamoufoxPlaceholder{})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	completed, err := service.Resume(ctx, session)
	if err != nil {
		return err
	}
	if err := replaceState(*statePath, completed); err != nil {
		return err
	}
	fmt.Printf("enrollment completed; status=%s state=%s\n", completed.Status, *statePath)
	return nil
}

type discardSink struct{}

func (discardSink) Add(context.Context, config.Account) error { return nil }

func writeState(path string, session enrollment.Session) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create enrollment state: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(session)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode enrollment state: %w", encodeErr)
	}
	return closeErr
}

func replaceState(path string, session enrollment.Session) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open enrollment state: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(session)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode enrollment state: %w", encodeErr)
	}
	return closeErr
}

func readState(path string) (enrollment.Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return enrollment.Session{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return enrollment.Session{}, errors.New("enrollment state must not be accessible by group or others")
	}
	file, err := os.Open(path)
	if err != nil {
		return enrollment.Session{}, err
	}
	var session enrollment.Session
	decodeErr := json.NewDecoder(file).Decode(&session)
	closeErr := file.Close()
	if decodeErr != nil {
		return enrollment.Session{}, decodeErr
	}
	return session, closeErr
}
