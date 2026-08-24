# SmakMail integration

`internal/smakmail` provides the mailbox API boundary needed by a controlled
DeepSeek account-enrollment workflow. It can order a mailbox, poll an order,
retrieve the mailbox credentials, and read the latest verification code.

Keep the SmakMail bearer token and issued mailbox passwords in a secret store.
Do not put them in `config.json`, logs, command-line arguments, or repository
files. The client intentionally accepts credentials at runtime and does not
persist them.

The integration does not bypass CAPTCHA, device verification, rate limits, or
other DeepSeek anti-abuse controls. An enrollment flow must stop for manual
completion whenever DeepSeek requires one of those checks.

The default API base URL is `https://api.smakmail.com/api/v1`. Order creation
requires a caller-generated idempotency key. Callers should set a deadline on
the context passed to `WaitOrderResult` and `LatestCode` polling.

## Controlled CLI workflow

The `smakmail` helper deliberately provisions one mailbox per invocation. It
checks the advertised product price against a required cap and writes mailbox
credentials to a newly created `0600` JSON file instead of stdout.

```bash
export SMAKMAIL_API_TOKEN='read-from-your-secret-store'
go run ./cmd/smakmail status
go run ./cmd/smakmail order --product Eternal --max-price-kopeks 100 --out /secure/path/mailbox.json
go run ./cmd/smakmail code --credentials /secure/path/mailbox.json --service deepseek --wait 5m
```

The provider's final order response remains the authoritative charged price.
Do not assume that the price returned by the product catalog is the final
charge; inspect the order summary and account balance.

## Banned-account cleanup

Managed DeepSeek accounts are removed only for explicit upstream ban codes
(`40012` or login business code `10`). On the first signal the account is
quarantined so it cannot receive new work. DS2API then performs one fresh login
as an independent confirmation. A second banned response permanently removes
the account and its persisted credentials and switches work to the next pool
member. An ordinary `401`, `403`, `502`, timeout, or inconclusive confirmation
restores the account instead of deleting it.
