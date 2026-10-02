package config

import "sort"

// Security describes how the IMAP connection is secured.
type Security string

const (
	SecurityTLS      Security = "tls"      // implicit TLS (usually port 993)
	SecuritySTARTTLS Security = "starttls" // plain connection upgraded via STARTTLS (usually port 143)
	SecurityNone     Security = "none"     // unencrypted; only for local bridges and tests
)

// Provider is a built-in IMAP preset.
type Provider struct {
	Name     string
	Host     string
	Port     int
	Security Security
	Note     string
	OAuth2   *OAuth2Preset // nil if the provider has no OAuth2 support here
}

// OAuth2Preset describes the OAuth2 settings of a provider. Endpoints are
// derived from the tenant (Microsoft identity platform).
type OAuth2Preset struct {
	Scopes        []string
	DefaultTenant string
}

// Providers holds the built-in presets, keyed by name.
var Providers = map[string]Provider{
	"gmail": {
		Name: "gmail", Host: "imap.gmail.com", Port: 993, Security: SecurityTLS,
		Note: "Requires 2FA and an app password (myaccount.google.com/apppasswords). --move-to applies a Gmail label.",
	},
	"outlook": {
		Name: "outlook", Host: "outlook.office365.com", Port: 993, Security: SecurityTLS,
		Note: "Basic auth / app passwords are disabled for Outlook.com and most Microsoft 365 tenants; use \"auth\": \"oauth2\" (see README).",
		OAuth2: &OAuth2Preset{
			Scopes:        []string{"https://outlook.office.com/IMAP.AccessAsUser.All", "offline_access"},
			DefaultTenant: "common",
		},
	},
	"icloud": {
		Name: "icloud", Host: "imap.mail.me.com", Port: 993, Security: SecurityTLS,
		Note: "Requires an app-specific password (account.apple.com > Sign-In and Security).",
	},
}

// OAuth2ProviderNames returns the presets that support OAuth2, sorted.
func OAuth2ProviderNames() []string {
	var names []string
	for _, n := range ProviderNames() {
		if Providers[n].OAuth2 != nil {
			names = append(names, n)
		}
	}
	return names
}

// ProviderNames returns the preset names in sorted order.
func ProviderNames() []string {
	names := make([]string, 0, len(Providers))
	for n := range Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
