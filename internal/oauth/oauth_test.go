package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mrflobow/newsletteraway/internal/config"
)

type memStore map[string]string

func (m memStore) Load(a string) (string, error) {
	if t, ok := m[a]; ok {
		return t, nil
	}
	return "", ErrNoToken
}
func (m memStore) Save(a, rt string) error { m[a] = rt; return nil }
func (m memStore) Delete(a string) error   { delete(m, a); return nil }

// failingStore loads like memStore but cannot save.
type failingStore struct{ memStore }

func (failingStore) Save(string, string) error { return errors.New("keychain full") }

// fakeMicrosoft emulates the device code and token endpoints.
type fakeMicrosoft struct {
	pending      atomic.Int32 // number of authorization_pending replies before success
	refreshFails bool
	lastTenant   string
	lastScope    string
}

func (f *fakeMicrosoft) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		f.lastTenant = strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		switch {
		case strings.HasSuffix(r.URL.Path, "/devicecode"):
			f.lastScope = r.Form.Get("scope")
			json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-123", "user_code": "ABCD-EFGH",
				"verification_uri": "https://microsoft.com/devicelogin", "expires_in": 60, "interval": 1,
			})
		case strings.HasSuffix(r.URL.Path, "/token"):
			switch r.Form.Get("grant_type") {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if f.pending.Add(-1) >= 0 {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600,
				})
			case "refresh_token":
				if f.refreshFails || r.Form.Get("refresh_token") != "refresh-1" {
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"access_token": "access-2", "refresh_token": "refresh-2", "token_type": "Bearer", "expires_in": 3600,
				})
			}
		}
	}))
	t.Cleanup(srv.Close)
	orig := Endpoint
	Endpoint = func(tenant string) oauth2.Endpoint {
		return oauth2.Endpoint{
			TokenURL:      srv.URL + "/" + tenant + "/token",
			DeviceAuthURL: srv.URL + "/" + tenant + "/devicecode",
			AuthStyle:     oauth2.AuthStyleInParams,
		}
	}
	t.Cleanup(func() { Endpoint = orig })
}

var account = config.Account{
	Name: "outlook", Provider: "outlook", Username: "me@outlook.com",
	Auth: config.AuthOAuth2, OAuth2ClientID: "client-1",
}

func TestDeviceLoginStoresToken(t *testing.T) {
	f := &fakeMicrosoft{}
	f.pending.Store(1)
	f.start(t)
	store := memStore{}
	var out bytes.Buffer

	tok, err := AccessToken(context.Background(), account, store, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "access-1" || store["outlook"] != "refresh-1" {
		t.Errorf("token=%q stored=%+v", tok, store["outlook"])
	}
	if !strings.Contains(out.String(), "ABCD-EFGH") || !strings.Contains(out.String(), "https://microsoft.com/devicelogin") {
		t.Errorf("instructions not printed:\n%s", out.String())
	}
	if f.lastTenant != "common" || !strings.Contains(f.lastScope, "IMAP.AccessAsUser.All") || !strings.Contains(f.lastScope, "offline_access") {
		t.Errorf("tenant=%q scope=%q", f.lastTenant, f.lastScope)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	(&fakeMicrosoft{}).start(t)
	store := memStore{"outlook": "refresh-1"}

	tok, err := AccessToken(context.Background(), account, store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "access-2" || store["outlook"] != "refresh-2" {
		t.Errorf("token=%q stored=%q", tok, store["outlook"])
	}
}

func TestSaveFailureIsOnlyAWarning(t *testing.T) {
	(&fakeMicrosoft{}).start(t)
	var warn bytes.Buffer
	tok, err := AccessToken(context.Background(), account, failingStore{memStore{"outlook": "refresh-1"}}, nil, &warn)
	if err != nil || tok != "access-2" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if !strings.Contains(warn.String(), "could not save") {
		t.Errorf("warning missing: %q", warn.String())
	}
}

func TestKeyringStoreKeepsOnlyRefreshToken(t *testing.T) {
	keyring.MockInit()
	var ks KeyringStore
	if _, err := ks.Load("a"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("empty load: %v", err)
	}
	if err := ks.Save("a", "rt-1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := keyring.Get(KeyringService, "a")
	if raw != `{"refresh_token":"rt-1"}` {
		t.Errorf("stored payload = %s", raw)
	}
	if rt, err := ks.Load("a"); err != nil || rt != "rt-1" {
		t.Errorf("load = %q, %v", rt, err)
	}
	if err := ks.Save("a", ""); err == nil {
		t.Error("saving an empty refresh token should fail")
	}
	keyring.Set(KeyringService, "bad", "not json")
	if _, err := ks.Load("bad"); err == nil || strings.Contains(err.Error(), "not json") {
		t.Errorf("corrupt entry: %v", err)
	}
	if err := ks.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := ks.Delete("a"); !errors.Is(err, ErrNoToken) {
		t.Errorf("second delete: %v", err)
	}
}

func TestNonInteractiveFailures(t *testing.T) {
	f := &fakeMicrosoft{refreshFails: true}
	f.start(t)

	_, err := AccessToken(context.Background(), account, memStore{}, nil, nil)
	if !errors.Is(err, ErrNoToken) || !strings.Contains(err.Error(), "oauth login outlook") {
		t.Errorf("missing token: %v", err)
	}

	expired := memStore{"outlook": "revoked"}
	if _, err := AccessToken(context.Background(), account, expired, nil, nil); err == nil || !strings.Contains(err.Error(), "oauth login outlook") {
		t.Errorf("failed refresh: %v", err)
	}
}

func TestRefreshFailureFallsBackToLogin(t *testing.T) {
	f := &fakeMicrosoft{refreshFails: true}
	f.start(t)
	store := memStore{"outlook": "revoked"}
	var out bytes.Buffer
	tok, err := AccessToken(context.Background(), account, store, &out, nil)
	if err != nil || tok != "access-1" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if !strings.Contains(out.String(), "no longer valid") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestConfigTenant(t *testing.T) {
	a := account
	a.OAuth2Tenant = "consumers"
	cfg, err := Config(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Endpoint.DeviceAuthURL, "/consumers/") || cfg.ClientID != "client-1" {
		t.Errorf("cfg = %+v", cfg)
	}
	a.Provider = "icloud"
	if _, err := Config(a); err == nil {
		t.Error("expected error for provider without OAuth2")
	}
}
