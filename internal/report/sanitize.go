package report

import (
	"strings"
	"unicode"
)

// Clean makes untrusted text (sender names, addresses, subjects, links,
// server messages) safe to print on a terminal. Control characters (C0, DEL,
// C1, which includes ESC-based ANSI/OSC sequences) and Unicode bidi controls
// (which can visually reorder text) are replaced with U+FFFD.
func Clean(s string) string {
	if strings.IndexFunc(s, unsafeRune) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return '\uFFFD'
		}
		return r
	}, s)
}

func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}
