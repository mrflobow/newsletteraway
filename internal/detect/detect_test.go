package detect

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
)

func TestParseListUnsubscribe(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single https", "<https://ex.com/u?id=1>", []string{"https://ex.com/u?id=1"}},
		{"mailto and https", "<mailto:leave@ex.com?subject=unsubscribe>, <https://ex.com/u>",
			[]string{"mailto:leave@ex.com?subject=unsubscribe", "https://ex.com/u"}},
		{"folded", "<https://ex.com/u?id=1&\r\n token=abc>,\r\n <mailto:u@ex.com>",
			[]string{"https://ex.com/u?id=1&token=abc", "mailto:u@ex.com"}},
		{"no brackets", "https://ex.com/u, mailto:u@ex.com", []string{"https://ex.com/u", "mailto:u@ex.com"}},
		{"junk schemes dropped", "<ftp://ex.com/u>, <javascript:alert(1)>, <https://ex.com/ok>", []string{"https://ex.com/ok"}},
		{"duplicates", "<https://ex.com/u>, <https://ex.com/u>", []string{"https://ex.com/u"}},
		{"unterminated", "<https://ex.com/u", []string{"https://ex.com/u"}},
		{"empty", "", nil},
		{"garbage", "please reply STOP", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseListUnsubscribe(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsOneClick(t *testing.T) {
	for in, want := range map[string]bool{
		"List-Unsubscribe=One-Click":   true,
		" list-unsubscribe=one-click ": true,
		"":                             false,
		"something=else":               false,
	} {
		if got := IsOneClick(in); got != want {
			t.Errorf("IsOneClick(%q) = %v, want %v", in, got, want)
		}
	}
}

func header(t *testing.T, raw string) mail.Header {
	t.Helper()
	h, err := textproto.ReadHeader(bufio.NewReader(strings.NewReader(raw + "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	return mail.Header{Header: message.Header{Header: h}}
}

func TestClassify(t *testing.T) {
	t.Run("list-unsubscribe with one-click", func(t *testing.T) {
		r := Classify(header(t, "From: \"Acme News\" <News@Acme.example>\r\n"+
			"Subject: =?UTF-8?Q?Gr=C3=BC=C3=9Fe?=\r\n"+
			"Date: Mon, 01 Sep 2026 10:00:00 +0000\r\n"+
			"List-Unsubscribe: <mailto:u@acme.example>, <https://acme.example/u/1>\r\n"+
			"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"))
		if !r.IsNewsletter() || !r.OneClick {
			t.Fatalf("want newsletter with one-click, got %+v", r)
		}
		if r.FromAddress != "news@acme.example" || r.FromName != "Acme News" {
			t.Errorf("from = %q %q", r.FromName, r.FromAddress)
		}
		if r.Subject != "Grüße" {
			t.Errorf("subject = %q", r.Subject)
		}
		if len(r.Links) != 2 || r.Date.IsZero() {
			t.Errorf("links=%v date=%v", r.Links, r.Date)
		}
	})
	t.Run("one-click needs https", func(t *testing.T) {
		r := Classify(header(t, "From: a@b.example\r\nList-Unsubscribe: <mailto:u@b.example>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"))
		if r.OneClick {
			t.Error("one-click without https link")
		}
	})
	t.Run("list-id only", func(t *testing.T) {
		r := Classify(header(t, "From: a@b.example\r\nList-Id: Weekly <weekly.b.example>\r\n"))
		if !r.IsNewsletter() || len(r.Links) != 0 || r.Sources[0] != SourceListID {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("precedence bulk", func(t *testing.T) {
		r := Classify(header(t, "From: a@b.example\r\nPrecedence: bulk\r\n"))
		if !r.IsNewsletter() || r.Sources[0] != SourcePrecedence {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("personal mail", func(t *testing.T) {
		r := Classify(header(t, "From: Friend <friend@example.com>\r\nSubject: hi\r\n"))
		if r.IsNewsletter() {
			t.Errorf("personal mail classified as newsletter: %+v", r)
		}
	})
}

func TestFindBodyLinks(t *testing.T) {
	tests := map[string][]string{
		"multipart_html.eml": {"https://shop.example/unsubscribe?u=42"},
		"german_qp.eml":      {"https://news.example.de/abmelden?id=7"},
		"click_here.eml":     {"https://list.example/x/9f8e"},
		"plain_text.eml":     {"https://plain.example/opt-out?u=1"},
		"no_unsubscribe.eml": nil,
	}
	for file, want := range tests {
		t.Run(file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			got, err := FindBodyLinks(f)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestUnsubscribeURIRejectsControlChars(t *testing.T) {
	for _, s := range []string{"https://ex.com/u\u009b31m", "https://ex.com/\u202eu", "https://ex.com/\x1b[2J"} {
		if IsUnsubscribeURI(s) {
			t.Errorf("accepted %q", s)
		}
	}
	if !IsUnsubscribeURI("https://ex.com/ünïcode?x=1") {
		t.Error("rejected a normal non-ASCII URL")
	}
}
