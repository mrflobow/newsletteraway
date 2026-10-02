// Package config loads the JSON configuration, applies provider presets and
// merges command line overrides into a list of fully resolved accounts.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultDays    = 90
	DefaultMailbox = "INBOX"
)

// File is the on-disk JSON configuration.
type File struct {
	Defaults Defaults  `json:"defaults"`
	Accounts []Account `json:"accounts"`
}

// Defaults apply to every account unless the account overrides them.
type Defaults struct {
	Mailboxes []string `json:"mailboxes,omitempty"`
	Days      int      `json:"days,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	BodyScan  bool     `json:"body_scan,omitempty"`
}

// Account is a single mailbox account. After Resolve, Host/Port/Security are
// always populated.
type Account struct {
	Name           string   `json:"name"`
	Provider       string   `json:"provider,omitempty"`
	Host           string   `json:"host,omitempty"`
	Port           int      `json:"port,omitempty"`
	Security       Security `json:"security,omitempty"`
	Username       string   `json:"username"`
	Password       string   `json:"password,omitempty"`
	PasswordEnv    string   `json:"password_env,omitempty"`
	PasswordSource string   `json:"password_source,omitempty"`  // "keychain" or empty
	Auth           Auth     `json:"auth,omitempty"`             // "password" (default) or "oauth2"
	OAuth2ClientID string   `json:"oauth2_client_id,omitempty"` // your own app registration
	OAuth2Tenant   string   `json:"oauth2_tenant,omitempty"`    // default "common"
	Mailboxes      []string `json:"mailboxes,omitempty"`
}

// UseKeychain reports whether the password should be read from the OS keychain.
func (a Account) UseKeychain() bool { return a.PasswordSource == "keychain" }

// Auth is the login method.
type Auth string

const (
	AuthPassword Auth = "password" // LOGIN with an (app) password
	AuthOAuth2   Auth = "oauth2"   // SASL XOAUTH2 with an OAuth2 access token
)

// Addr returns host:port.
func (a Account) Addr() string { return fmt.Sprintf("%s:%d", a.Host, a.Port) }

// Overrides are values given on the command line. Zero values mean "not set".
type Overrides struct {
	Provider    string
	Host        string
	Port        int
	Security    Security
	Username    string
	PasswordEnv string
	Keychain    bool
	Mailboxes   []string

	Auth           Auth
	OAuth2ClientID string
	OAuth2Tenant   string
}

// HasConnection reports whether the overrides describe a connection on their
// own (used to build an ad-hoc account without a config file).
func (o Overrides) HasConnection() bool {
	return o.Provider != "" || o.Host != "" || o.Username != ""
}

// DefaultPath returns ~/.config/newsletteraway/config.json.
func DefaultPath() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(dir, ".config", "newsletteraway", "config.json")
}

// Load reads and parses a config file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// PermissionWarning returns a warning when the config file contains a
// plain-text password but is readable or writable by group or others.
// It returns "" when everything is fine or on Windows (no Unix modes).
func PermissionWarning(path string, f *File) string {
	if runtime.GOOS == "windows" || f == nil {
		return ""
	}
	var names []string
	for _, a := range f.Accounts {
		if a.Password != "" {
			names = append(names, a.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("warning: %s contains plain-text passwords (%s) but has mode %04o; other users may read it. Fix with: chmod 600 %s",
		path, strings.Join(names, ", "), fi.Mode().Perm(), path)
}

// Select returns the accounts to scan: the named ones, or all if names is empty.
// When the config is nil or has no accounts and overrides describe a
// connection, a single ad-hoc account is returned.
func Select(f *File, names []string, o Overrides) ([]Account, error) {
	if f == nil || len(f.Accounts) == 0 {
		if !o.HasConnection() {
			return nil, errors.New("no accounts configured: pass --config or connection flags (--provider/--host, --user)")
		}
		if len(names) > 0 {
			return nil, fmt.Errorf("--account given but no config file loaded")
		}
		return []Account{{Name: "cli"}}, nil
	}
	if len(names) == 0 {
		return append([]Account(nil), f.Accounts...), nil
	}
	var out []Account
	for _, n := range names {
		found := false
		for _, a := range f.Accounts {
			if a.Name == n {
				out = append(out, a)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("account %q not found in config", n)
		}
	}
	return out, nil
}

// Resolve applies (in increasing priority) the provider preset, the account
// values, the file defaults and the CLI overrides, and validates the result.
func Resolve(a Account, d Defaults, o Overrides) (Account, error) {
	if o.Provider != "" {
		a.Provider = o.Provider
	}
	if a.Provider != "" && a.Provider != "custom" {
		p, ok := Providers[a.Provider]
		if !ok {
			return a, fmt.Errorf("account %q: unknown provider %q (known: %v, or \"custom\")", a.Name, a.Provider, ProviderNames())
		}
		if a.Host == "" {
			a.Host = p.Host
		}
		if a.Port == 0 {
			a.Port = p.Port
		}
		if a.Security == "" {
			a.Security = p.Security
		}
	}

	if o.Host != "" {
		a.Host = o.Host
	}
	if o.Port != 0 {
		a.Port = o.Port
	}
	if o.Security != "" {
		a.Security = o.Security
	}
	if o.Username != "" {
		a.Username = o.Username
	}
	if o.PasswordEnv != "" {
		a.PasswordEnv = o.PasswordEnv
	}
	// Credentials are stored per account name, not per server. A built-in
	// provider therefore always connects to its own server, so that --host
	// can't send a stored Outlook token or keychain password elsewhere.
	if p, ok := Providers[a.Provider]; ok && (a.Host != p.Host || a.Port != p.Port || a.Security != p.Security) {
		return a, fmt.Errorf("account %q: provider %q always uses %s:%d (%s); to connect to %s:%d use \"provider\": \"custom\"",
			a.Name, a.Provider, p.Host, p.Port, p.Security, a.Host, a.Port)
	}
	if o.Auth != "" {
		a.Auth = o.Auth
	}
	if o.OAuth2ClientID != "" {
		a.OAuth2ClientID = o.OAuth2ClientID
	}
	if o.OAuth2Tenant != "" {
		a.OAuth2Tenant = o.OAuth2Tenant
	}
	if o.Keychain {
		a.PasswordSource = "keychain"
	}
	if len(o.Mailboxes) > 0 {
		a.Mailboxes = o.Mailboxes
	}

	if a.Security == "" {
		a.Security = SecurityTLS
	}
	if a.Port == 0 {
		if a.Security == SecurityTLS {
			a.Port = 993
		} else {
			a.Port = 143
		}
	}
	if len(a.Mailboxes) == 0 {
		a.Mailboxes = d.Mailboxes
	}
	if len(a.Mailboxes) == 0 {
		a.Mailboxes = []string{DefaultMailbox}
	}
	if a.Name == "" || a.Name == "cli" {
		if a.Username != "" {
			a.Name = a.Username
		}
	}

	switch a.Security {
	case SecurityTLS, SecuritySTARTTLS, SecurityNone:
	default:
		return a, fmt.Errorf("account %q: invalid security %q (tls, starttls, none)", a.Name, a.Security)
	}
	if a.Host == "" {
		return a, fmt.Errorf("account %q: no host (set provider or host)", a.Name)
	}
	if a.Username == "" {
		return a, fmt.Errorf("account %q: no username", a.Name)
	}
	if a.Auth == "" {
		a.Auth = AuthPassword
	}
	switch a.Auth {
	case AuthPassword:
	case AuthOAuth2:
		p, ok := Providers[a.Provider]
		if !ok || p.OAuth2 == nil {
			return a, fmt.Errorf("account %q: auth oauth2 is only supported for providers: %v", a.Name, OAuth2ProviderNames())
		}
		if a.OAuth2ClientID == "" {
			return a, fmt.Errorf("account %q: auth oauth2 needs oauth2_client_id (or --oauth2-client-id); see README \"Outlook OAuth2\"", a.Name)
		}
		if a.OAuth2Tenant == "" {
			a.OAuth2Tenant = p.OAuth2.DefaultTenant
		}
	default:
		return a, fmt.Errorf("account %q: invalid auth %q (password, oauth2)", a.Name, a.Auth)
	}
	if a.PasswordSource != "" && a.PasswordSource != "keychain" {
		return a, fmt.Errorf("account %q: invalid password_source %q (only \"keychain\")", a.Name, a.PasswordSource)
	}
	return a, nil
}

// SinceDate computes the search start date from --since or --days.
func SinceDate(since string, days int, now time.Time) (time.Time, error) {
	if since != "" {
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --since %q, want YYYY-MM-DD", since)
		}
		return t, nil
	}
	if days < 0 {
		return time.Time{}, fmt.Errorf("invalid --days %d", days)
	}
	if days == 0 {
		return time.Time{}, nil // no date filter
	}
	return now.AddDate(0, 0, -days), nil
}

// Example is the config written by "config init".
const Example = `{
  "defaults": {
    "mailboxes": ["INBOX"],
    "days": 90,
    "body_scan": false
  },
  "accounts": [
    {
      "name": "gmail",
      "provider": "gmail",
      "username": "me@gmail.com",
      "password_source": "keychain"
    },
    {
      "name": "icloud",
      "provider": "icloud",
      "username": "me@icloud.com",
      "password_env": "ICLOUD_APP_PASSWORD"
    },
    {
      "name": "outlook",
      "provider": "outlook",
      "username": "me@outlook.com",
      "auth": "oauth2",
      "oauth2_client_id": "00000000-0000-0000-0000-000000000000"
    },
    {
      "name": "work",
      "provider": "custom",
      "host": "mail.example.com",
      "port": 993,
      "security": "tls",
      "username": "me",
      "password": "plain-text-is-discouraged",
      "mailboxes": ["INBOX", "Archive"]
    }
  ]
}
`
