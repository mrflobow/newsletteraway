package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestResolvePrecedence(t *testing.T) {
	a := Account{Name: "g", Provider: "gmail", Username: "me@gmail.com"}
	got, err := Resolve(a, Defaults{Mailboxes: []string{"INBOX", "Promo"}}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	// Preset fills host/port/security, defaults fill mailboxes.
	if got.Host != "imap.gmail.com" || got.Port != 993 || got.Security != SecurityTLS {
		t.Errorf("got %+v", got)
	}
	if strings.Join(got.Mailboxes, ",") != "INBOX,Promo" {
		t.Errorf("mailboxes = %v", got.Mailboxes)
	}

	// CLI overrides win over the account entry.
	c := Account{Name: "c", Provider: "custom", Host: "mail.example.com", Port: 993, Username: "me"}
	got, err = Resolve(c, Defaults{}, Overrides{Host: "localhost", Port: 143, Security: SecurityNone, Username: "x", Mailboxes: []string{"A"}, Keychain: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "localhost" || got.Port != 143 || got.Security != SecurityNone || got.Username != "x" || got.Mailboxes[0] != "A" || !got.UseKeychain() {
		t.Errorf("got %+v", got)
	}
}

func TestPresetServerIsLocked(t *testing.T) {
	outlook := Account{Name: "o", Provider: "outlook", Username: "u", Auth: AuthOAuth2, OAuth2ClientID: "id"}
	for name, tc := range map[string]struct {
		a Account
		o Overrides
	}{
		"--host on outlook":    {outlook, Overrides{Host: "evil.example"}},
		"--port on outlook":    {outlook, Overrides{Port: 143}},
		"--security on gmail":  {Account{Name: "g", Provider: "gmail", Username: "u"}, Overrides{Security: SecuritySTARTTLS}},
		"host in config entry": {Account{Name: "i", Provider: "icloud", Username: "u", Host: "other.example"}, Overrides{}},
		"--provider + --host":  {Account{Name: "cli"}, Overrides{Provider: "gmail", Host: "localhost", Username: "u"}},
	} {
		_, err := Resolve(tc.a, Defaults{}, tc.o)
		if err == nil || !strings.Contains(err.Error(), "custom") {
			t.Errorf("%s: expected locked-server error, got %v", name, err)
		}
	}
	// Restating the preset's own values is fine.
	if _, err := Resolve(outlook, Defaults{}, Overrides{Host: "outlook.office365.com", Port: 993, Security: SecurityTLS}); err != nil {
		t.Errorf("same values: %v", err)
	}
}

func TestResolveDefaultsAndErrors(t *testing.T) {
	got, err := Resolve(Account{Name: "cli"}, Defaults{}, Overrides{Host: "h", Username: "u@x", Security: SecuritySTARTTLS})
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 143 || got.Mailboxes[0] != DefaultMailbox || got.Name != "u@x" {
		t.Errorf("got %+v", got)
	}

	for name, a := range map[string]Account{
		"unknown provider": {Name: "a", Provider: "aol", Username: "u"},
		"no host":          {Name: "a", Username: "u"},
		"no user":          {Name: "a", Provider: "icloud"},
		"bad security":     {Name: "a", Host: "h", Username: "u", Security: "ssl"},
		"bad source":       {Name: "a", Host: "h", Username: "u", PasswordSource: "vault"},
	} {
		if _, err := Resolve(a, Defaults{}, Overrides{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadAndSelect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(Example), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Accounts) != 4 || f.Defaults.Days != 90 {
		t.Fatalf("parsed %+v", f)
	}
	all, _ := Select(f, nil, Overrides{})
	if len(all) != 4 {
		t.Errorf("all = %d", len(all))
	}
	one, err := Select(f, []string{"icloud"}, Overrides{})
	if err != nil || len(one) != 1 || one[0].Provider != "icloud" {
		t.Errorf("select icloud = %+v, %v", one, err)
	}
	if _, err := Select(f, []string{"nope"}, Overrides{}); err == nil {
		t.Error("expected error for unknown account")
	}
	if _, err := Select(nil, nil, Overrides{}); err == nil {
		t.Error("expected error without config and flags")
	}
	if adhoc, err := Select(nil, nil, Overrides{Provider: "gmail"}); err != nil || len(adhoc) != 1 {
		t.Errorf("adhoc = %+v, %v", adhoc, err)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"acounts": []}`), 0o600)
	if _, err := Load(bad); err == nil {
		t.Error("expected error for unknown field")
	}
}

func TestSinceDate(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if d, _ := SinceDate("", 90, now); !d.Equal(now.AddDate(0, 0, -90)) {
		t.Errorf("days: %v", d)
	}
	if d, _ := SinceDate("2026-01-15", 90, now); d.Format("2006-01-02") != "2026-01-15" {
		t.Errorf("since: %v", d)
	}
	if d, _ := SinceDate("", 0, now); !d.IsZero() {
		t.Errorf("days=0 should be zero: %v", d)
	}
	if _, err := SinceDate("15.01.2026", 0, now); err == nil {
		t.Error("expected error")
	}
}

func TestPasswordResolution(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(KeyringService, "kc", "from-keychain"); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PW": "from-env"}
	var warn bytes.Buffer
	r := &SecretResolver{
		LookupEnv:  func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		KeyringGet: keyring.Get,
		Prompt:     func(string) (string, error) { return "from-prompt", nil },
		Warn:       &warn,
	}

	tests := []struct {
		name string
		a    Account
		want string
	}{
		{"env first", Account{Name: "kc", PasswordEnv: "PW", PasswordSource: "keychain", Password: "plain"}, "from-env"},
		{"keychain", Account{Name: "kc", PasswordEnv: "MISSING", PasswordSource: "keychain", Password: "plain"}, "from-keychain"},
		{"plain", Account{Name: "other", PasswordSource: "keychain", Password: "plain"}, "plain"},
		{"prompt", Account{Name: "other"}, "from-prompt"},
	}
	for _, tt := range tests {
		got, err := r.Password(tt.a)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	if !strings.Contains(warn.String(), "plain-text password") {
		t.Errorf("missing plain-text warning: %s", warn.String())
	}

	r.Prompt = nil
	if _, err := r.Password(Account{Name: "other"}); err == nil {
		t.Error("expected error without any source")
	}

	r.KeyringGet = func(string, string) (string, error) { return "", errors.New("locked") }
	r.Prompt = func(string) (string, error) { return "p", nil }
	if got, _ := r.Password(Account{Name: "kc", PasswordSource: "keychain"}); got != "p" {
		t.Errorf("keychain error should fall through to prompt, got %q", got)
	}
}

func TestResolveOAuth2(t *testing.T) {
	a := Account{Name: "o", Provider: "outlook", Username: "me@outlook.com", Auth: AuthOAuth2, OAuth2ClientID: "id"}
	got, err := Resolve(a, Defaults{}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if got.OAuth2Tenant != "common" {
		t.Errorf("tenant = %q", got.OAuth2Tenant)
	}
	got, err = Resolve(Account{Name: "o", Username: "u"}, Defaults{}, Overrides{Provider: "outlook", Auth: AuthOAuth2, OAuth2ClientID: "cli", OAuth2Tenant: "consumers"})
	if err != nil || got.OAuth2ClientID != "cli" || got.OAuth2Tenant != "consumers" {
		t.Errorf("overrides: %+v, %v", got, err)
	}
	if got, _ := Resolve(Account{Name: "p", Provider: "gmail", Username: "u"}, Defaults{}, Overrides{}); got.Auth != AuthPassword {
		t.Errorf("default auth = %q", got.Auth)
	}

	for name, a := range map[string]Account{
		"no client id":       {Name: "o", Provider: "outlook", Username: "u", Auth: AuthOAuth2},
		"provider w/o oauth": {Name: "o", Provider: "icloud", Username: "u", Auth: AuthOAuth2, OAuth2ClientID: "id"},
		"custom host":        {Name: "o", Host: "h", Username: "u", Auth: AuthOAuth2, OAuth2ClientID: "id"},
		"bad auth":           {Name: "o", Provider: "outlook", Username: "u", Auth: "kerberos"},
	} {
		if _, err := Resolve(a, Defaults{}, Overrides{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestPermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes don't apply on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	os.WriteFile(path, []byte("{}"), 0o644)
	withPw := &File{Accounts: []Account{{Name: "work", Password: "x"}, {Name: "gmail"}}}

	w := PermissionWarning(path, withPw)
	if !strings.Contains(w, "work") || strings.Contains(w, "gmail") || !strings.Contains(w, "chmod 600") {
		t.Errorf("warning = %q", w)
	}
	if w := PermissionWarning(path, &File{Accounts: []Account{{Name: "gmail", PasswordEnv: "X"}}}); w != "" {
		t.Errorf("no plain-text password, got %q", w)
	}
	os.Chmod(path, 0o600)
	if w := PermissionWarning(path, withPw); w != "" {
		t.Errorf("0600 file, got %q", w)
	}
}
