# CLAUDE.md

Go CLI (`github.com/mrflobow/newsletteraway`) that scans IMAP mailboxes for newsletters and lists their unsubscribe links. User docs live in README.md.

## Commands

```sh
go build -o newsletteraway .
go test ./...                       # unit tests plus end-to-end tests against an in-process IMAP server
go test ./cmd -run TestScanCommand -v   # prints the rendered table output
go vet ./... && gofmt -l .
```

Git: single branch `main`, no remote. Run `go vet`, `gofmt -l` and `go test ./...` before committing.

Tests never touch real mail servers. Do not run `scan` against real providers with placeholder addresses such as `me@icloud.com` from the example config, because those may be real people's accounts.

## Layout

- `cmd/`: Cobra commands. `scan.go` resolves the accounts and handles output plus the move/confirm step. `misc.go` holds `providers`, `config init` and `keychain set|delete`.
- `internal/config`: JSON config and `PermissionWarning`, provider presets (`providers.go`) and password resolution (`secrets.go`).
- `internal/imapconn`: thin wrapper over go-imap v2. This is the only package that talks IMAP. `xoauth2.go` is our own SASL XOAUTH2 client, because go-sasl only has OAUTHBEARER.
- `internal/oauth`: Microsoft device-code flow (`golang.org/x/oauth2`) and the token store (keychain service `newsletteraway-oauth2`, payload `{"refresh_token": ...}`). `cmd/oauth.go` has `oauth login|logout`.
- `internal/detect`: header classification (`classify.go`), the RFC 2369 parser (`listunsub.go`) and the body link scan (`bodyscan.go`, fixtures in `testdata/*.eml`).
- `internal/scanner`: the scan loop (`Scan`) and `MoveAll`. It lives outside `cmd/` so the tests can drive it.
- `internal/report`: grouping (`Build`), table/JSON rendering, and `Clean` (`sanitize.go`) for terminal-safe output.

## Invariants (don't break these)

- **Read-only by default.** Scanning opens mailboxes with `Open(mb, true)` (EXAMINE), and every fetch uses `Peek: true`. Only `MoveAll` selects a mailbox read-write. `TestScanBodyAndReadOnly` asserts that no `\Seen` flag gets set.
- **Unsubscribing only on request.** Plain `scan` never contacts senders. `scan --unsubscribe` (`unsubStep`, `internal/unsub`) sends only RFC 8058 one-click POSTs to the https link of groups with `OneClick`; it asks which senders unless `-y`, and refuses on a non-TTY stdin without `-y`. Never send mailto or POST to body-scan links, and never follow redirects. The URL comes from untrusted mail, so `unsub.Client` refuses loopback/private/link-local addresses (SSRF); tests swap `unsub.Client` for an httptest TLS client.
- **Unsubscribe tracking** (`internal/track`, `unsubscribed.json` beside the config, mode 0600) is keyed by account name + group key. `Apply` hides a group until its newest INTERNALDATE (`Group.Received`, not the forgeable Date header) is later than the unsubscribe time + `track.Grace` (10 days), then shows it with `Resubscribed`. It runs before the report and the move step, so hidden groups are not moved either.
- **Terminal styling** goes through `report.Style` (`StyleFor` returns the zero value for non-TTYs, so piped output and tests stay plain). Apply `Clean` first, then truncate (`Fit`), then color; never color or cut before cleaning. Links are never truncated. Progress is a transient stderr line (`cmd/ui.go`) shown only on a TTY and cleared before every log line.
- **Moving needs confirmation** unless `--yes` is passed. On a non-TTY stdin without `--yes`, refuse.
- **Precedence** is CLI flags > account entry > config `defaults` > provider preset. Connection flags without `--config`/`--account` create an ad-hoc account and ignore the default config file.
- **OAuth2:** accounts with `auth: oauth2` never go through `SecretResolver`. `oauth.AccessToken` refreshes the stored token and saves rotated refresh tokens. The device login is interactive only on a TTY; otherwise it fails with "run oauth login". Only presets with an `OAuth2` field (currently `outlook`) allow oauth2. Users bring their own client ID; never hardcode another app's client ID.
- **Store only the refresh token** in the keychain, never the access token. go-keyring refuses values over about 4 KB on macOS, and Microsoft access tokens are large. Access tokens are obtained fresh on each run and kept in memory. A failed save of a rotated refresh token is a warning, not an error.
- **Built-in providers are locked to their own server.** `Resolve` rejects host, port or security values that differ from the preset, because credentials are stored per account name rather than per server. Connecting elsewhere requires `provider: custom`.
- **Untrusted text on the terminal goes through `report.Clean`.** This covers sender names and addresses, subjects, links, mailbox names and server errors. It strips C0/C1/DEL and bidi controls. `IsUnsubscribeURI` also rejects such characters. JSON output is already safe.
- **Headers are fetched with a 64 KB cap** (`scanner.MaxHeaderBytes`, a partial fetch); larger headers are skipped.
- **Password order** is env var, then keychain, then plain-text config value (warns), then a hidden prompt (TTY only). Secrets are never logged.
- **Output streams:** stdout carries only the report (table or JSON). Progress and warnings go to stderr.
- **Errors are per account.** One failing account doesn't stop the others; the exit code is non-zero if any account failed.

## Gotchas

- go-imap is `v2.0.0-beta.8`, so its API can still change. `Client.Move` already falls back to COPY + STORE + EXPUNGE, so don't reimplement that.
- To parse a header fetched with HEADER.FIELDS, use `textproto.ReadHeader`, then wrap it as `mail.Header{Header: message.Header{Header: h}}` (see `scanner.parseHeader`).
- The charset import in `bodyscan.go` (`_ "github.com/emersion/go-message/charset"`) is required for non-UTF-8 bodies.
- A group's links come from the sender's **newest** message that has links, because unsubscribe URLs often carry tokens that work for only one message. Don't merge links across messages.
- In `cmd` tests, flags are package globals. Reset them with `resetFlags()` (`cmd/scan_test.go`), not by zeroing `sf`, because zeroing loses the defaults.
- The integration tests use `imapmemserver` with `SecurityNone` and `InsecureAuth`. Use caps `{IMAP4rev1}` only to exercise the fallback for servers without MOVE.
- In `internal/oauth` tests, swap `oauth.Endpoint` for an httptest fake. Device polling waits at least 1 s per poll, so keep the number of `authorization_pending` replies low. In `cmd`, `tokenStore` can be swapped the same way.
- The XOAUTH2 test wraps the memserver session to add `SessionSASL`. The wrapper hides the IMAP4rev2 methods, so that test advertises IMAP4rev1 only.
- Write control and bidi characters in Go sources as `\u` escapes, never as literal characters.
- For body-scan keywords, extend `keywordRe` and `clickRe` in `bodyscan.go` and add an `.eml` fixture to `testdata/` along with an entry in `TestFindBodyLinks`.

## Known open issues (from the security review; deliberately not fixed yet)

- **M2:** `security: none` sends LOGIN passwords and XOAUTH2 tokens in cleartext without a warning. It is meant only for local bridges and tests. A fix would allow it only for loopback hosts, and never with oauth2 or a built-in preset.
- **L3:** go-keyring items can be read without a prompt by any process running as the same user (via `/usr/bin/security`). Document this rather than work around it.
- **L4:** `MoveAll` reuses UIDs from the scan session without checking UIDVALIDITY.
- **Not built yet:** a `config add` command that writes an account into the JSON config. Users currently edit the file by hand (see the README, "After you have the client ID").
