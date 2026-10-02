// Package detect decides whether a message is a newsletter and extracts its
// unsubscribe links from headers (RFC 2369 / RFC 8058) or, optionally, the body.
package detect

import (
	"net/url"
	"strings"
	"unicode"
)

// ParseListUnsubscribe parses an RFC 2369 List-Unsubscribe header value such as
//
//	<mailto:leave@example.com?subject=unsubscribe>, <https://example.com/u?id=1>
//
// and returns the http(s) and mailto URIs in header order, without duplicates.
// Folding whitespace inside the brackets is removed. Values without angle
// brackets (a common sender mistake) are accepted when they look like URIs.
func ParseListUnsubscribe(value string) []string {
	value = strings.NewReplacer("\r", "", "\n", "").Replace(value)

	var candidates []string
	rest := value
	for {
		start := strings.IndexByte(rest, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			// Unterminated bracket: take the remainder.
			candidates = append(candidates, rest[start+1:])
			rest = ""
			break
		}
		candidates = append(candidates, rest[start+1:start+end])
		rest = rest[start+end+1:]
	}
	if len(candidates) == 0 {
		// No brackets at all: fall back to comma separated values.
		candidates = strings.Split(value, ",")
	}

	var out []string
	seen := map[string]bool{}
	for _, c := range candidates {
		c = stripSpace(c)
		if c == "" || !IsUnsubscribeURI(c) || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// IsUnsubscribeURI reports whether s is a usable http(s) or mailto URI.
// URIs containing control or bidi characters are rejected: url.Parse accepts
// C1 controls, which could act as terminal escape sequences.
func IsUnsubscribeURI(s string) bool {
	if strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
	}) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != "" || u.Path != ""
	}
	return false
}

// IsOneClick reports whether a List-Unsubscribe-Post header value announces
// RFC 8058 one-click unsubscription.
func IsOneClick(value string) bool {
	return strings.EqualFold(stripSpace(value), "List-Unsubscribe=One-Click")
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s)
}
