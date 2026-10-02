package detect

import (
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

// Source names the signal that marked a message as a newsletter.
type Source string

const (
	SourceHeader     Source = "header"     // List-Unsubscribe
	SourceListID     Source = "list-id"    // List-Id
	SourcePrecedence Source = "precedence" // Precedence: bulk / list
	SourceBody       Source = "body"       // unsubscribe link found in the body
)

// HeaderFields are the headers needed for classification; fetch only these.
var HeaderFields = []string{
	"From", "Subject", "Date",
	"List-Unsubscribe", "List-Unsubscribe-Post", "List-Id", "Precedence",
}

// Result is the classification of a single message.
type Result struct {
	FromName    string
	FromAddress string
	Subject     string
	Date        time.Time
	ListID      string
	Links       []string
	OneClick    bool
	Sources     []Source
}

// IsNewsletter reports whether any newsletter signal was found.
func (r *Result) IsNewsletter() bool { return len(r.Sources) > 0 }

// AddBodyLinks merges links found by the body scan.
func (r *Result) AddBodyLinks(links []string) {
	if len(links) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, l := range r.Links {
		seen[l] = true
	}
	for _, l := range links {
		if !seen[l] {
			seen[l] = true
			r.Links = append(r.Links, l)
		}
	}
	r.Sources = append(r.Sources, SourceBody)
}

// Classify inspects the message header.
func Classify(h mail.Header) *Result {
	r := &Result{}

	if addrs, err := h.AddressList("From"); err == nil && len(addrs) > 0 {
		r.FromName = addrs[0].Name
		r.FromAddress = strings.ToLower(addrs[0].Address)
	} else {
		r.FromAddress = strings.ToLower(strings.TrimSpace(h.Get("From")))
	}
	if s, err := h.Subject(); err == nil {
		r.Subject = s
	} else {
		r.Subject = h.Get("Subject")
	}
	if d, err := h.Date(); err == nil {
		r.Date = d
	}

	if v := text(h, "List-Unsubscribe"); v != "" {
		r.Links = ParseListUnsubscribe(v)
		r.Sources = append(r.Sources, SourceHeader)
		r.OneClick = IsOneClick(h.Get("List-Unsubscribe-Post")) && hasHTTPS(r.Links)
	}
	if v := text(h, "List-Id"); v != "" {
		r.ListID = v
		r.Sources = append(r.Sources, SourceListID)
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "list":
		r.Sources = append(r.Sources, SourcePrecedence)
	}
	return r
}

func text(h mail.Header, key string) string {
	v, err := h.Text(key)
	if err != nil {
		v = h.Get(key)
	}
	return strings.TrimSpace(v)
}

func hasHTTPS(links []string) bool {
	for _, l := range links {
		if strings.HasPrefix(strings.ToLower(l), "https://") {
			return true
		}
	}
	return false
}
