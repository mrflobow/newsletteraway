# newsletteraway

A CLI that scans IMAP mailboxes for newsletters and lists their unsubscribe links, grouped by sender.

- **Read-only by default:** it opens mailboxes with `EXAMINE` and reads with `BODY.PEEK`, so nothing is marked as read.
- **Detection:**
  - the `List-Unsubscribe` header (RFC 2369), with RFC 8058 one-click detection
  - the `List-Id` and `Precedence: bulk|list` headers
  - with `--body-scan`, unsubscribe links in the HTML or plain-text body (English, German, French, Spanish, Italian, Dutch wording)
- **Unsubscribing is opt-in:** a plain `scan` only lists the links. `scan --unsubscribe` sends RFC 8058 one-click requests (HTTPS POST) for senders that support it, after you pick them. Other senders (mailto, plain links) are never contacted; you click those yourself.
- **Terminal output:** on a terminal the table is fitted to the window width (links go on their own lines, never cut), important things are colored (1-click green, `again` yellow, errors red) and a progress bar shows scan progress on stderr. Pipes and files get plain, uncolored output; `NO_COLOR=1` turns colors off.
- **Failures:** the link is printed so you can unsubscribe in the browser, and the sender is marked `manual`. Temporary failures (timeout, network error, 5xx, 429) are tried again automatically after 1 day. Permanent ones (403, 404, bad redirect) are not retried; `scan --unsubscribe --retry-failed` forces another try for all of them.
- **Unsubscribe tracking:** every successful unsubscribe is recorded in `~/.config/newsletteraway/unsubscribed.json` (account, sender, time). Later scans hide that sender. If mail from it arrives more than 10 days after the unsubscribe (by the server's receive time), it shows up again marked `again`, e.g. after you signed up for a discount.
- **Optional move:** `--move-to <folder>` moves the detected newsletters (in Gmail this applies a label).

## Install

```sh
go install github.com/mrflobow/newsletteraway@latest
# or
go build -o newsletteraway .
```

## Quick start

```sh
# Ad-hoc, with a provider preset; the password comes from an env var (or a hidden prompt)
GMAIL_APP_PW=xxxx newsletteraway scan --provider gmail --user me@gmail.com --password-env GMAIL_APP_PW

# With a config file
newsletteraway config init               # writes ~/.config/newsletteraway/config.json (0600)
newsletteraway keychain set gmail        # store the app password in the macOS Keychain
newsletteraway oauth login outlook       # Outlook: one-time OAuth2 sign-in (see below)
newsletteraway scan                      # all accounts
newsletteraway scan -a gmail --days 30 -o json | jq .
newsletteraway scan -a gmail --group-by domain --body-scan
newsletteraway scan -a gmail --move-to Newsletters --create-folder --dry-run

# one-click unsubscribe: pick senders by number, or -y for all of them
newsletteraway scan -a gmail --unsubscribe
newsletteraway scan -a gmail --unsubscribe -y
```

Example output:

```
== gmail [INBOX]
   42 newsletter messages from 7 senders (812 messages scanned)

SENDER                    COUNT  LAST        SOURCE                 UNSUBSCRIBE
Acme <news@acme.example>  12     2026-09-28  header,list-id,1-click https://acme.example/u?t=…
                                                                    mailto:unsub@acme.example
```

## Providers

| preset    | host                    | port | security |
|-----------|-------------------------|------|----------|
| `gmail`   | imap.gmail.com          | 993  | tls      |
| `outlook` | outlook.office365.com   | 993  | tls (OAuth2) |
| `icloud`  | imap.mail.me.com        | 993  | tls      |
| `custom`  | set `host`/`port`/`security` yourself (`tls`, `starttls`, `none`) | | |

You log in with **app passwords**, not your normal account password:

- **Gmail:** turn on 2-Step Verification, then create an app password at <https://myaccount.google.com/apppasswords>. IMAP must be enabled in the Gmail settings.
- **iCloud:** go to <https://account.apple.com>, then Sign-In and Security, then App-Specific Passwords. The username is your full iCloud address.
- **Outlook.com / Microsoft 365:** app passwords no longer work for IMAP. Use OAuth2 instead (next section).

## Outlook OAuth2

Outlook logs in with an OAuth2 token over SASL XOAUTH2, obtained through the **device code flow**: the tool prints a URL and a code, and you sign in through your browser. Only the refresh token is stored, in the macOS Keychain (service `newsletteraway-oauth2`). Each run uses it to get a short-lived access token, which is kept in memory only, so you only sign in once.

The tool doesn't ship with a client ID of its own, so you need **your own app registration** (it's free):

1. Open <https://entra.microsoft.com>, then **App registrations**, then **New registration**.
   - Name: anything, for example `newsletteraway`.
   - Supported account types: **Accounts in any organizational directory and personal Microsoft accounts**. For personal-only use, pick "Personal Microsoft accounts only". Then, in **your newsletteraway config** (not in the portal), add `"oauth2_tenant": "consumers"` to the account, or pass `--oauth2-tenant consumers`.
   - Redirect URI: leave the URL empty. The platform dropdown (e.g. "Web") doesn't matter, because nothing is registered without a URL and the device code flow doesn't use redirects.
2. Under **Authentication**, then **Advanced settings**, set **Allow public client flows** to **Yes**. The device code flow needs this.
3. Under **API permissions**, choose **Add a permission**, then **Microsoft Graph**, then **Delegated permissions**. Use the search box to tick:
   - `offline_access`, in the **OpenId permissions** group. It lets the tool get a refresh token, so you only sign in once.
   - `IMAP.AccessAsUser.All`, in the **IMAP** group.

   For personal accounts this step is optional, because the tool requests both permissions at sign-in and Microsoft asks you to consent then. Work and school tenants may need an admin to grant consent in advance.
4. Copy the **Application (client) ID** from the Overview page.
5. Make sure IMAP is enabled for the mailbox. On Outlook.com this is under Settings, then Mail, then Forwarding and IMAP.

### After you have the client ID

**Option A: no config file (quickest).** Pass everything as flags:

```sh
newsletteraway scan --provider outlook --user you@outlook.com \
  --auth oauth2 --oauth2-client-id <client-id> --oauth2-tenant consumers
```

The first run prints a URL and a code: open the URL, enter the code and sign in. The token is saved in the Keychain, so later runs of the same command don't ask again. Leave out `--oauth2-tenant consumers` if the app allows organizational *and* personal accounts.

**Option B: config file.** Afterwards you only type `scan -a outlook`.

1. Run `newsletteraway config init`. This creates `~/.config/newsletteraway/config.json` with **example** accounts.
2. Edit the file and replace the `accounts` list with your own account. Delete the example gmail, icloud and work entries, or a plain `scan` will try them too.
   ```json
   {
     "defaults": { "mailboxes": ["INBOX"], "days": 90 },
     "accounts": [
       {
         "name": "outlook",
         "provider": "outlook",
         "username": "you@outlook.com",
         "auth": "oauth2",
         "oauth2_client_id": "<client-id>",
         "oauth2_tenant": "consumers"
       }
     ]
   }
   ```
3. Sign in once with `newsletteraway oauth login outlook` (it shows a URL and a code).
4. Scan with `newsletteraway scan -a outlook`.

Run `newsletteraway oauth logout outlook` to remove the stored token.

If the stored token can't be refreshed, an interactive run starts the sign-in again. A non-interactive run (cron, a pipe) fails and tells you to run `oauth login`. Registering an app may require a Microsoft Entra tenant: personal accounts without one can get one through a free Azure account.

## Configuration

Default path: `~/.config/newsletteraway/config.json`. Override it with `--config`.

```json
{
  "defaults": { "mailboxes": ["INBOX"], "days": 90, "limit": 0, "body_scan": false },
  "accounts": [
    { "name": "gmail",  "provider": "gmail",  "username": "me@gmail.com",  "password_source": "keychain" },
    { "name": "icloud", "provider": "icloud", "username": "me@icloud.com", "password_env": "ICLOUD_APP_PASSWORD" },
    { "name": "outlook", "provider": "outlook", "username": "me@outlook.com", "auth": "oauth2", "oauth2_client_id": "<application-id>" },
    { "name": "work", "provider": "custom", "host": "mail.example.com", "port": 993, "security": "tls",
      "username": "me", "password": "plain-text-is-discouraged", "mailboxes": ["INBOX", "Archive"] }
  ]
}
```

**Precedence:** CLI flags > account entry > `defaults` > provider preset. If you pass connection flags (`--provider`, `--host`, `--user`) without `--config`/`--account`, the tool scans a single ad-hoc account and ignores the default config file.

**`auth`** is `password` (the default) or `oauth2` (Outlook only). OAuth2 accounts don't use the password fields at all; their token comes from the keychain (see above). `oauth2_tenant` defaults to `common`.

**Built-in providers always use their own server.** You can't change `host`, `port` or `security` on a `gmail`, `outlook` or `icloud` account, either in the config or with flags, so a stored password or token is never sent to another server. Use `"provider": "custom"` to connect anywhere else. If a config file with a plain-text `password` is readable by other users, the tool warns you and suggests `chmod 600`.

**Password lookup order:**
1. `password_env` / `--password-env`
2. The OS keychain, when `"password_source": "keychain"` or `--keychain` is set. It uses service `newsletteraway` and the account name. Store a password with `newsletteraway keychain set <account>`.
3. `password` in the config. This prints a warning.
4. A hidden interactive prompt, when stdin is a terminal.

## Flags (`scan`)

| flag | meaning |
|------|---------|
| `-a, --account` | config account(s) to scan (default: all) |
| `-p, --provider`, `--host`, `--port`, `--security`, `-u, --user` | connection settings |
| `--password-env`, `--keychain` | password source |
| `--auth oauth2`, `--oauth2-client-id`, `--oauth2-tenant` | OAuth2 login (Outlook) |
| `-m, --mailbox` | mailbox(es) to scan, repeatable (default `INBOX`) |
| `-d, --days` / `--since YYYY-MM-DD` | date filter (default 90 days, `0` = all) |
| `-n, --limit` | max messages per mailbox, newest first |
| `--body-scan` | also search the bodies of messages that have no header links (downloads up to 1 MB each) |
| `--group-by sender\|domain` | how results are grouped |
| `-o, --output table\|json` | output format; progress messages go to stderr (`-q` silences them) |
| `--move-to`, `--create-folder`, `--dry-run`, `-y, --yes` | move the detected newsletters; asks for confirmation unless `--yes` is passed |
| `--retry-failed` | with `--unsubscribe`: also retry senders marked `manual` after an earlier failure |
| `--unsubscribe` | list the one-click senders and ask which to unsubscribe from (`1,3`, `1-3`, `all`); with `-y` it unsubscribes from all of them without asking, so wanted senders and receipts go too; `--dry-run` only lists |

The exit code is non-zero if any account failed. The other accounts are still scanned and reported.

## Windows

The tool builds and runs on Windows (`GOOS=windows go build -o newsletteraway.exe .`). Differences from macOS:

- **Config path:** `%USERPROFILE%\.config\newsletteraway\config.json`, unless you pass `--config`.
- **Keychain:** "keychain" means the **Windows Credential Manager**. `keychain set`, `password_source: "keychain"` and the OAuth2 token store all use it.
- **Password prompt:** works in Windows Terminal, PowerShell and cmd. Git Bash/mintty is not seen as a terminal, so use `password_env`, the keychain, or run under `winpty`.
- **Permissions:** the `chmod 600` warning is Unix-only. On Windows, the config file is protected by your user profile's ACLs.

## Development

```sh
go test ./...   # includes end-to-end tests against an in-process IMAP server
```
