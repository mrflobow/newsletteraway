package config

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

// KeyringService is the service name used for OS keychain entries.
const KeyringService = "newsletteraway"

// SecretResolver finds the password for an account. The function fields allow
// tests to replace the environment, keychain and terminal.
type SecretResolver struct {
	LookupEnv  func(string) (string, bool)
	KeyringGet func(service, user string) (string, error)
	Prompt     func(label string) (string, error) // nil disables prompting
	Warn       io.Writer
}

// DefaultSecretResolver uses the real environment, the OS keychain and an
// interactive prompt when stdin is a terminal.
func DefaultSecretResolver() *SecretResolver {
	r := &SecretResolver{
		LookupEnv:  os.LookupEnv,
		KeyringGet: keyring.Get,
		Warn:       os.Stderr,
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		r.Prompt = PromptPassword
	}
	return r
}

// Password resolves in this order: env var, keychain, plain config value, prompt.
func (r *SecretResolver) Password(a Account) (string, error) {
	if a.PasswordEnv != "" {
		if v, ok := r.LookupEnv(a.PasswordEnv); ok && v != "" {
			return v, nil
		}
		fmt.Fprintf(r.Warn, "warning: account %q: env var %s is not set\n", a.Name, a.PasswordEnv)
	}
	if a.UseKeychain() {
		v, err := r.KeyringGet(KeyringService, a.Name)
		switch {
		case err == nil && v != "":
			return v, nil
		case errors.Is(err, keyring.ErrNotFound):
			fmt.Fprintf(r.Warn, "warning: account %q: no keychain entry (run: newsletteraway keychain set %s)\n", a.Name, a.Name)
		case err != nil:
			fmt.Fprintf(r.Warn, "warning: account %q: keychain: %v\n", a.Name, err)
		}
	}
	if a.Password != "" {
		fmt.Fprintf(r.Warn, "warning: account %q: using plain-text password from config; prefer password_env or the keychain\n", a.Name)
		return a.Password, nil
	}
	if r.Prompt != nil {
		return r.Prompt(fmt.Sprintf("Password for %s (%s): ", a.Name, a.Username))
	}
	return "", fmt.Errorf("account %q: no password available (set password_env, use the keychain, or run interactively)", a.Name)
}

// PromptPassword reads a password from the terminal without echo.
func PromptPassword(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
