// Package report aggregates detected newsletters and renders them.
package report

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mrflobow/newsletteraway/internal/detect"
)

// GroupBy selects the aggregation key.
type GroupBy string

const (
	BySender GroupBy = "sender"
	ByDomain GroupBy = "domain"
)

// Message is a detected newsletter message.
type Message struct {
	Mailbox string
	UID     uint32
	*detect.Result
}

// Group aggregates the newsletters of one sender (or domain).
type Group struct {
	Key         string              `json:"key"`
	Name        string              `json:"name,omitempty"`
	Senders     []string            `json:"senders,omitempty"` // only when grouped by domain
	ListID      string              `json:"list_id,omitempty"`
	Count       int                 `json:"count"`
	LastDate    time.Time           `json:"last_date"`
	LastSubject string              `json:"last_subject"`
	Links       []string            `json:"unsubscribe_links"`
	OneClick    bool                `json:"one_click"`
	Sources     []detect.Source     `json:"sources"`
	UIDs        map[string][]uint32 `json:"uids"` // mailbox -> UIDs
}

// AccountReport is the result for one account.
type AccountReport struct {
	Account   string   `json:"account"`
	Mailboxes []string `json:"mailboxes"`
	Scanned   int      `json:"scanned"`
	Detected  int      `json:"newsletters"`
	Groups    []Group  `json:"groups"`
	Error     string   `json:"error,omitempty"`
}

// Build groups messages, sorted by message count (descending), then by
// latest date. Links are taken from the newest message that has any, because
// unsubscribe URLs often carry per-message tokens.
func Build(msgs []Message, by GroupBy) []Group {
	idx := map[string]*Group{}
	newestWithLinks := map[string]time.Time{}
	senders := map[string]map[string]bool{}

	// Process newest first so "last" fields come from the newest message.
	sorted := slices.Clone(msgs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.After(sorted[j].Date) })

	var order []string
	for _, m := range sorted {
		key := m.FromAddress
		if by == ByDomain {
			key = domainOf(m.FromAddress)
		}
		if key == "" {
			key = "(unknown sender)"
		}
		g, ok := idx[key]
		if !ok {
			g = &Group{Key: key, Name: m.FromName, ListID: m.ListID, LastDate: m.Date, LastSubject: m.Subject, UIDs: map[string][]uint32{}}
			idx[key] = g
			senders[key] = map[string]bool{}
			order = append(order, key)
		}
		g.Count++
		g.UIDs[m.Mailbox] = append(g.UIDs[m.Mailbox], m.UID)
		senders[key][m.FromAddress] = true
		for _, s := range m.Sources {
			if !slices.Contains(g.Sources, s) {
				g.Sources = append(g.Sources, s)
			}
		}
		if g.Name == "" {
			g.Name = m.FromName
		}
		if g.ListID == "" {
			g.ListID = m.ListID
		}
		if len(m.Links) > 0 {
			if _, done := newestWithLinks[key]; !done {
				newestWithLinks[key] = m.Date
				g.Links = SortLinks(m.Links)
				g.OneClick = m.OneClick
			}
		}
	}

	out := make([]Group, 0, len(order))
	for _, k := range order {
		g := idx[k]
		if by == ByDomain {
			for s := range senders[k] {
				g.Senders = append(g.Senders, s)
			}
			sort.Strings(g.Senders)
		}
		for _, uids := range g.UIDs {
			slices.Sort(uids)
		}
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].LastDate.After(out[j].LastDate)
	})
	return out
}

// SortLinks orders links https first, then http, then mailto (stable).
func SortLinks(links []string) []string {
	rank := func(l string) int {
		l = strings.ToLower(l)
		switch {
		case strings.HasPrefix(l, "https:"):
			return 0
		case strings.HasPrefix(l, "http:"):
			return 1
		}
		return 2
	}
	out := slices.Clone(links)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}
