// Package oauth obtains OAuth2 access tokens for IMAP (currently Microsoft /
// Outlook) using the device code flow, and persists refresh tokens in the OS
// keychain.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"

	"github.com/mrflobow/newsletteraway/internal/config"
)

// KeyringService is the keychain service under which tokens are stored.
const KeyringService = "newsletteraway-oauth2"

// Endpoint returns the OAuth2 endpoint for a tenant. Tests replace it.
var Endpoint = microsoft.AzureADEndpoint

// ErrNoToken means no usable token is stored and no interactive login was possible.
var ErrNoToken = errors.New("no OAuth2 token stored")

// Config builds the oauth2 config for a resolved account.
func Config(a config.Account) (*oauth2.Config, error) {
	p, ok := config.Providers[a.Provider]
	if !ok || p.OAuth2 == nil {
		return nil, fmt.Errorf("account %q: provider %q has no OAuth2 support", a.Name, a.Provider)
	}
	if a.OAuth2ClientID == "" {
		return nil, fmt.Errorf("account %q: oauth2_client_id is not set", a.Name)
	}
	tenant := a.OAuth2Tenant
	if tenant == "" {
		tenant = p.OAuth2.DefaultTenant
	}
	return &oauth2.Config{
		ClientID: a.OAuth2ClientID, // public client: no secret
		Endpoint: Endpoint(tenant),
		Scopes:   p.OAuth2.Scopes,
	}, nil
}

// Store persists refresh tokens per account name. Access tokens are short
// lived (about an hour) and large, so they are only kept in memory.
type Store interface {
	Load(account string) (refreshToken string, err error) // ErrNoToken if absent
	Save(account, refreshToken string) error
	Delete(account string) error
}

// KeyringStore stores refresh tokens in the OS keychain.
type KeyringStore struct{}

// storedToken is the keychain payload. It is JSON so fields can be added later.
type storedToken struct {
	RefreshToken string `json:"refresh_token"`
}

func (KeyringStore) Load(account string) (string, error) {
	s, err := keyring.Get(KeyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", fmt.Errorf("keychain: %w", err)
	}
	var t storedToken
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return "", fmt.Errorf("keychain: corrupt token for %q (run: newsletteraway oauth login %s)", account, account)
	}
	if t.RefreshToken == "" {
		return "", ErrNoToken
	}
	return t.RefreshToken, nil
}

func (KeyringStore) Save(account, refreshToken string) error {
	if refreshToken == "" {
		return errors.New("no refresh token to store (is offline_access granted?)")
	}
	b, err := json.Marshal(storedToken{RefreshToken: refreshToken})
	if err != nil {
		return err
	}
	err = keyring.Set(KeyringService, account, string(b))
	if errors.Is(err, keyring.ErrSetDataTooBig) {
		return fmt.Errorf("keychain: refresh token too large to store (%d bytes)", len(refreshToken))
	}
	return err
}

func (KeyringStore) Delete(account string) error {
	err := keyring.Delete(KeyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNoToken
	}
	return err
}

// DeviceLogin runs the device code flow: it prints the verification URL and
// code to out and blocks until the user has signed in (or the code expires).
func DeviceLogin(ctx context.Context, cfg *oauth2.Config, out io.Writer) (*oauth2.Token, error) {
	da, err := cfg.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("device authorization: %w", err)
	}
	fmt.Fprintf(out, "To sign in, open %s and enter the code %s\n", da.VerificationURI, da.UserCode)
	if da.VerificationURIComplete != "" {
		fmt.Fprintf(out, "(or open %s)\n", da.VerificationURIComplete)
	}
	fmt.Fprintln(out, "Waiting for sign-in...")
	tok, err := cfg.DeviceAccessToken(ctx, da)
	if err != nil {
		return nil, fmt.Errorf("device sign-in: %w", err)
	}
	if tok.RefreshToken == "" {
		fmt.Fprintln(out, "warning: no refresh token returned (is offline_access granted?); you will need to sign in again later")
	}
	return tok, nil
}

// AccessToken returns a fresh access token for the account, obtained from
// the stored refresh token. Rotated refresh tokens are saved; a failed save
// is reported on warn but does not fail the run, since the access token is
// still valid. Without a usable refresh token an interactive device login is
// started when interactive is non-nil.
func AccessToken(ctx context.Context, a config.Account, store Store, interactive, warn io.Writer) (string, error) {
	cfg, err := Config(a)
	if err != nil {
		return "", err
	}
	if warn == nil {
		warn = io.Discard
	}

	refresh, err := store.Load(a.Name)
	if err != nil && !errors.Is(err, ErrNoToken) {
		return "", err
	}
	if refresh != "" {
		tok, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refresh}).Token()
		if err == nil {
			if tok.RefreshToken != "" && tok.RefreshToken != refresh {
				if err := store.Save(a.Name, tok.RefreshToken); err != nil {
					fmt.Fprintf(warn, "warning: %s: could not save the renewed OAuth2 token: %v\n", a.Name, err)
				}
			}
			return tok.AccessToken, nil
		}
		if interactive == nil {
			return "", fmt.Errorf("account %q: refreshing the OAuth2 token failed (%v); run: newsletteraway oauth login %s", a.Name, err, a.Name)
		}
		fmt.Fprintf(interactive, "%s: stored OAuth2 token is no longer valid, signing in again\n", a.Name)
	}

	if interactive == nil {
		return "", fmt.Errorf("account %q: %w; run: newsletteraway oauth login %s", a.Name, ErrNoToken, a.Name)
	}
	fmt.Fprintf(interactive, "%s: OAuth2 sign-in required for %s\n", a.Name, a.Username)
	tok, err := DeviceLogin(ctx, cfg, interactive)
	if err != nil {
		return "", err
	}
	if err := store.Save(a.Name, tok.RefreshToken); err != nil {
		fmt.Fprintf(warn, "warning: %s: could not save the OAuth2 token, you will have to sign in again next time: %v\n", a.Name, err)
	}
	return tok.AccessToken, nil
}
