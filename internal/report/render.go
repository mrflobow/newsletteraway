package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/mrflobow/newsletteraway/internal/detect"
)

// WriteJSON writes all account reports as a JSON array.
func WriteJSON(w io.Writer, reports []AccountReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(reports)
}

// WriteTable writes a human readable table per account. All text that can
// come from emails or servers is passed through Clean.
func WriteTable(w io.Writer, reports []AccountReport) error {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "== %s [%s]\n", Clean(r.Account), Clean(strings.Join(r.Mailboxes, ", ")))
		if r.Error != "" {
			fmt.Fprintf(w, "   error: %s\n", Clean(r.Error))
			if len(r.Groups) == 0 {
				continue
			}
		}
		fmt.Fprintf(w, "   %d newsletter messages from %d senders (%d messages scanned)\n\n", r.Detected, len(r.Groups), r.Scanned)
		if len(r.Groups) == 0 {
			continue
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SENDER\tCOUNT\tLAST\tSOURCE\tUNSUBSCRIBE")
		for _, g := range r.Groups {
			links := g.Links
			first := "-"
			if len(links) > 0 {
				first = Clean(links[0])
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n",
				truncate(Clean(senderLabel(g)), 48), g.Count, g.LastDate.Format("2006-01-02"), sourceLabel(g), first)
			for _, l := range links[min(1, len(links)):] {
				fmt.Fprintf(tw, "\t\t\t\t%s\n", Clean(l))
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func senderLabel(g Group) string {
	if g.Name != "" && !strings.EqualFold(g.Name, g.Key) {
		return fmt.Sprintf("%s <%s>", g.Name, g.Key)
	}
	return g.Key
}

func sourceLabel(g Group) string {
	parts := make([]string, 0, len(g.Sources)+1)
	for _, s := range g.Sources {
		parts = append(parts, string(s))
	}
	if g.OneClick {
		parts = append(parts, "1-click")
	}
	if len(parts) == 0 {
		return string(detect.SourceHeader)
	}
	return strings.Join(parts, ",")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
