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

	"github.com/google/uuid"

	"ds2api/internal/smakmail"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "smakmail:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: smakmail <status|order|code>")
	}
	client, err := clientFromEnv()
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		return status(client)
	case "order":
		return order(client, args[1:])
	case "code":
		return code(client, args[1:])
	default:
		return fmt.Errorf("unknown command %q; use status, order, or code", args[0])
	}
}

func clientFromEnv() (*smakmail.Client, error) {
	return smakmail.New(os.Getenv("SMAKMAIL_BASE_URL"), os.Getenv("SMAKMAIL_API_TOKEN"))
}

func status(client *smakmail.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	balance, err := client.GetBalance(ctx)
	if err != nil {
		return err
	}
	products, err := client.ListProducts(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("balance: %s RUB (%d kopeks)\n", balance.BalanceRub, balance.BalanceKopeks)
	for _, product := range products {
		if product.Available {
			fmt.Printf("product: %s, advertised unit price: %d kopeks\n", product.Product, product.UnitPriceKopeks)
		}
	}
	return nil
}

func order(client *smakmail.Client, args []string) error {
	flags := flag.NewFlagSet("order", flag.ContinueOnError)
	product := flags.String("product", "Eternal", "SmakMail product")
	maxPrice := flags.Int64("max-price-kopeks", 0, "maximum advertised unit price")
	out := flags.String("out", "", "new 0600 JSON credentials file")
	wait := flags.Duration("wait", 2*time.Minute, "maximum order wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *maxPrice <= 0 {
		return errors.New("--max-price-kopeks must be positive")
	}
	if strings.TrimSpace(*out) == "" {
		return errors.New("--out is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	products, err := client.ListProducts(ctx)
	if err != nil {
		return err
	}
	selected, err := findProduct(products, *product)
	if err != nil {
		return err
	}
	if !selected.Available {
		return fmt.Errorf("product %s is unavailable", selected.Product)
	}
	if selected.UnitPriceKopeks > *maxPrice {
		return fmt.Errorf("advertised price %d kopeks exceeds cap %d", selected.UnitPriceKopeks, *maxPrice)
	}
	idempotencyKey := "ds2api-" + uuid.NewString()
	created, err := client.CreateOrder(ctx, selected.Product, 1, idempotencyKey)
	if err != nil {
		return err
	}
	mailboxes, err := client.WaitOrderResult(ctx, created.OrderID, time.Second)
	if err != nil {
		return err
	}
	if len(mailboxes) != 1 {
		return fmt.Errorf("expected one mailbox, got %d", len(mailboxes))
	}
	if err := writeCredentials(*out, mailboxes[0]); err != nil {
		return err
	}
	finalOrder, err := client.GetOrder(ctx, created.OrderID)
	if err != nil {
		return err
	}
	fmt.Printf("mailbox provisioned; order=%s charged=%d kopeks credentials=%s\n", finalOrder.OrderID, finalOrder.TotalKopeks, *out)
	return nil
}

func code(client *smakmail.Client, args []string) error {
	flags := flag.NewFlagSet("code", flag.ContinueOnError)
	credentialsPath := flags.String("credentials", "", "0600 JSON credentials file")
	service := flags.String("service", "deepseek", "service filter")
	wait := flags.Duration("wait", 5*time.Minute, "maximum code wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*credentialsPath) == "" {
		return errors.New("--credentials is required")
	}
	mailbox, err := readCredentials(*credentialsPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *wait)
	defer cancel()
	verificationCode, err := client.WaitLatestCode(ctx, mailbox.Email, *service, mailbox.Password, 2*time.Second)
	if err != nil {
		return err
	}
	fmt.Println(verificationCode.Code)
	return nil
}

func findProduct(products []smakmail.Product, wanted string) (smakmail.Product, error) {
	for _, product := range products {
		if strings.EqualFold(strings.TrimSpace(product.Product), strings.TrimSpace(wanted)) {
			return product, nil
		}
	}
	return smakmail.Product{}, fmt.Errorf("product %q not found", wanted)
}

func writeCredentials(path string, mailbox smakmail.Mailbox) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create credentials file: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(mailbox)
	closeErr := file.Close()
	if encodeErr != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return fmt.Errorf("encode credentials: %v; remove incomplete file: %w", encodeErr, removeErr)
		}
		return fmt.Errorf("encode credentials: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close credentials file: %w", closeErr)
	}
	return nil
}

func readCredentials(path string) (smakmail.Mailbox, error) {
	info, err := os.Stat(path)
	if err != nil {
		return smakmail.Mailbox{}, fmt.Errorf("stat credentials file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return smakmail.Mailbox{}, errors.New("credentials file must not be accessible by group or others")
	}
	file, err := os.Open(path)
	if err != nil {
		return smakmail.Mailbox{}, fmt.Errorf("open credentials file: %w", err)
	}
	var mailbox smakmail.Mailbox
	decodeErr := json.NewDecoder(file).Decode(&mailbox)
	closeErr := file.Close()
	if decodeErr != nil {
		return smakmail.Mailbox{}, fmt.Errorf("decode credentials file: %w", decodeErr)
	}
	if closeErr != nil {
		return smakmail.Mailbox{}, fmt.Errorf("close credentials file: %w", closeErr)
	}
	if strings.TrimSpace(mailbox.Email) == "" || strings.TrimSpace(mailbox.Password) == "" {
		return smakmail.Mailbox{}, errors.New("credentials file is missing email or password")
	}
	return mailbox, nil
}
