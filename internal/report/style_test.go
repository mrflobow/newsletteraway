package report

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

func TestWriteTableStyledFitsWidth(t *testing.T) {
	long := "https://very-long-tracking-host.example/unsubscribe?token=" + strings.Repeat("abcdef", 30)
	reports := []AccountReport{{
		Account: "outlook", Mailboxes: []string{"INBOX"}, Scanned: 500, Detected: 3,
		Groups: []Group{
			{Key: "newsletter-with-a-really-long-address@subdomain.example.com", Name: "A Very Long Sender Name Inc.", Count: 12, LastDate: time.Now(), OneClick: true, Links: []string{long}},
			{Key: "b@x.example", Count: 2, LastDate: time.Now(), Resubscribed: true, Links: []string{"mailto:b@x.example"}},
			{Key: "evil\x1b[2J@x.example", Count: 1, LastDate: time.Now()},
		},
	}}
	for _, width := range []int{MinWidth, 60, 80, 120} {
		var plain, colored bytes.Buffer
		if err := WriteTableStyled(&plain, reports, Style{Width: width}); err != nil {
			t.Fatal(err)
		}
		if err := WriteTableStyled(&colored, reports, Style{Width: width, Color: true}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plain.String(), "\x1b") {
			t.Errorf("width %d: escape codes without Color:\n%q", width, plain.String())
		}
		if !strings.Contains(colored.String(), "\x1b[") {
			t.Errorf("width %d: no color with Color", width)
		}
		if strings.Contains(ansi.ReplaceAllString(colored.String(), ""), "\x1b") {
			t.Errorf("width %d: untrusted escape survived", width)
		}
		if ansi.ReplaceAllString(colored.String(), "") != plain.String() {
			t.Errorf("width %d: color changes the text", width)
		}
		if !strings.Contains(plain.String(), long) {
			t.Errorf("width %d: link was cut", width)
		}
		for _, line := range strings.Split(plain.String(), "\n") {
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "   ") && strings.Contains(line, "newsletter messages") {
				continue // links and the summary line may wrap
			}
			if n := len([]rune(line)); n > width {
				t.Errorf("width %d: line is %d wide: %q", width, n, line)
			}
		}
		if width == 60 {
			t.Log("\n" + plain.String())
		}
	}
}
