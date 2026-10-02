package detect

import (
	"bytes"
	"io"
	"mime"
	"regexp"
	"strings"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 bodies
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

// keywordRe matches unsubscribe wording in several languages.
var keywordRe = regexp.MustCompile(`(?i)unsubscribe|unsub|opt[\s-]?out|abmelden|abbestellen|austragen|désabonner|desabonner|désinscri|darse de baja|cancelar suscripci|disiscriviti|uitschrijven|afmelden`)

// clickRe matches generic link texts like "click here" that often follow
// "To unsubscribe, ...".
var clickRe = regexp.MustCompile(`(?i)^\s*(click\s+)?here\s*\.?$|^\s*hier\s*\.?$|^\s*ici\s*\.?$|^\s*aquí\s*\.?$|^\s*klicken\s*\.?$`)

var urlRe = regexp.MustCompile(`(?i)(https?://[^\s<>"')\]]+|mailto:[^\s<>"')\]]+)`)

// FindBodyLinks parses a full RFC 5322 message and returns unsubscribe links
// found in its text/html and text/plain parts.
func FindBodyLinks(r io.Reader) ([]string, error) {
	mr, err := mail.CreateReader(r)
	if err != nil && mr == nil {
		return nil, err
	}
	defer mr.Close()

	var htmlLinks, textLinks []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Partial results are better than none for malformed MIME.
			break
		}
		ih, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := mime.ParseMediaType(ih.Get("Content-Type"))
		if ct == "" {
			ct = "text/plain"
		}
		body, err := io.ReadAll(io.LimitReader(p.Body, 4<<20))
		if err != nil {
			continue
		}
		switch ct {
		case "text/html":
			htmlLinks = append(htmlLinks, FindHTMLLinks(body)...)
		case "text/plain":
			textLinks = append(textLinks, FindTextLinks(body)...)
		}
	}
	// HTML links are more reliable; plain-text links are the fallback.
	if len(htmlLinks) > 0 {
		return dedupe(htmlLinks), nil
	}
	return dedupe(textLinks), nil
}

// FindHTMLLinks returns hrefs of <a> elements whose text or URL contains
// unsubscribe wording, or whose generic text ("click here") directly follows
// such wording.
func FindHTMLLinks(body []byte) []string {
	z := html.NewTokenizer(bytes.NewReader(body))
	var (
		out       []string
		inAnchor  bool
		href      string
		anchorTxt strings.Builder
		before    string // text seen right before the current anchor
		recent    strings.Builder
	)
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "a" {
				continue
			}
			inAnchor, href = true, ""
			anchorTxt.Reset()
			before = recent.String()
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) == "href" {
					href = strings.TrimSpace(string(v))
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) != "a" || !inAnchor {
				continue
			}
			inAnchor = false
			if !IsUnsubscribeURI(href) {
				continue
			}
			txt := anchorTxt.String()
			if keywordRe.MatchString(txt) || keywordRe.MatchString(href) ||
				(clickRe.MatchString(txt) && keywordRe.MatchString(tail(before, 120))) {
				out = append(out, href)
			}
		case html.TextToken:
			t := string(z.Text())
			if inAnchor {
				anchorTxt.WriteString(t)
			}
			recent.WriteString(t)
			if recent.Len() > 1024 {
				s := tail(recent.String(), 256)
				recent.Reset()
				recent.WriteString(s)
			}
		}
	}
}

// FindTextLinks returns URLs from plain text that contain unsubscribe wording
// themselves or sit on a line (or right after a line) mentioning it.
func FindTextLinks(body []byte) []string {
	var out []string
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		ctx := line
		if i > 0 {
			ctx = lines[i-1] + " " + line
		}
		for _, u := range urlRe.FindAllString(line, -1) {
			u = strings.TrimRight(u, ".,;:")
			if IsUnsubscribeURI(u) && (keywordRe.MatchString(u) || keywordRe.MatchString(ctx)) {
				out = append(out, u)
			}
		}
	}
	return out
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
