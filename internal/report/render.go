package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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

// WriteTable writes a human readable table per account in plain style.
func WriteTable(w io.Writer, reports []AccountReport) error {
	return WriteTableStyled(w, reports, Style{})
}

// WriteTableStyled writes a human readable table per account. All text that
// can come from emails or servers is passed through Clean. With a terminal
// Style the rows are fitted to the width, links go on their own lines (never
// cut, so they stay copyable) and important things are colored.
func WriteTableStyled(w io.Writer, reports []AccountReport, st Style) error {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s [%s]\n", st.Cyan("== "+Clean(r.Account)), Clean(strings.Join(r.Mailboxes, ", ")))
		if r.Error != "" {
			fmt.Fprintf(w, "   %s %s\n", st.Red("error:"), Clean(r.Error))
			if len(r.Groups) == 0 {
				continue
			}
		}
		fmt.Fprintf(w, "   %s newsletter messages from %s senders (%d messages scanned)\n\n",
			st.Bold(strconv.Itoa(r.Detected)), st.Bold(strconv.Itoa(len(r.Groups))), r.Scanned)
		if len(r.Groups) == 0 {
			continue
		}
		if st.Width > 0 {
			writeFitted(w, r.Groups, st)
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

// writeFitted renders one row per sender within st.Width columns, with the
// links indented below it. The SOURCE column is dropped when it would leave
// less than minSender columns for the sender.
func writeFitted(w io.Writer, groups []Group, st Style) {
	const (
		minSender = 16
		countW    = 5
		dateW     = 10
	)
	srcW := 0
	for _, g := range groups {
		srcW = max(srcW, len([]rune(sourceLabel(g))))
	}
	srcW = min(srcW, 16)
	fixed := 1 + 2 + countW + 2 + dateW // lead, gaps, count, date
	senderW := st.Width - fixed - 2 - srcW
	if senderW < minSender {
		srcW = 0
		senderW = max(st.Width-fixed, 8)
	}
	row := func(sender, count, date, src string) string {
		line := " " + sender + "  " + count + "  " + date
		if srcW > 0 {
			line += "  " + src
		}
		return line
	}
	fmt.Fprintln(w, st.Bold(row(Fit("SENDER", senderW), fmt.Sprintf("%*s", countW, "COUNT"), Fit("LAST", dateW), "SOURCE")))
	for _, g := range groups {
		src := strings.TrimRight(Fit(sourceLabel(g), srcW), " ")
		switch {
		case g.Resubscribed:
			src = st.Yellow(src)
		case g.OneClick:
			src = st.Green(src)
		}
		fmt.Fprintln(w, row(Fit(Clean(senderLabel(g)), senderW), st.Bold(fmt.Sprintf("%*d", countW, g.Count)), g.LastDate.Format("2006-01-02"), src))
		if len(g.Links) == 0 {
			fmt.Fprintf(w, "    %s\n", st.Dim("(no unsubscribe link)"))
		}
		for _, l := range g.Links {
			fmt.Fprintf(w, "    %s\n", st.Dim(Clean(l)))
		}
	}
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
	if g.Resubscribed {
		parts = append(parts, "again")
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
