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

## DeepSeek enrollment handoff

`deepseek-enroll` connects mailbox provisioning, verification-code polling,
the current DeepSeek registration contract, and the DS2API Admin API. Sensitive
session state is written only to a newly-created `0600` file.

The prepare command stops with `manual_action_required`. Open the official
signup page, use the mailbox and generated account password from the protected
state file, and request the email code after completing any human verification.
Then run resume; it waits for the code, completes registration, and imports the
account into the live DS2API pool.

```bash
export SMAKMAIL_API_TOKEN='read-from-your-secret-store'
go run ./cmd/deepseek-enroll prepare \
  --max-price-kopeks 100 \
  --proxy-id proxy-id-from-ds2api \
  --out /secure/path/deepseek-enrollment.json

export DS2API_BASE_URL='http://127.0.0.1:6011'
export DS2API_ADMIN_KEY='read-from-your-secret-store'
go run ./cmd/deepseek-enroll resume \
  --state /secure/path/deepseek-enrollment.json \
  --wait 5m
```

`CamoufoxPlaceholder` reserves a browser-adapter boundary but is intentionally
disabled: it does not install or launch Camoufox, rotate fingerprints, or solve
Turnstile. Its only result is the explicit operator handoff above. This keeps
the rest of enrollment independently testable without embedding an anti-detect
browser into the production service.

## Banned-account cleanup

Managed DeepSeek accounts are removed only for explicit upstream ban codes
(`40012` or login business code `10`). On the first signal the account is
quarantined so it cannot receive new work. DS2API then performs one fresh login
as an independent confirmation. A second banned response permanently removes
the account and its persisted credentials and switches work to the next pool
member. An ordinary `401`, `403`, `502`, timeout, or inconclusive confirmation
restores the account instead of deleting it.
