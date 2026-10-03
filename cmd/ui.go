package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mrflobow/newsletteraway/internal/report"
)

// outStyle styles the report only when it goes to a real terminal.
func outStyle(w io.Writer) report.Style {
	if f, ok := w.(*os.File); ok {
		return report.StyleFor(f)
	}
	return report.Style{}
}

// progress draws one transient status line ("acct/INBOX  [####-----] 40%
// 400/1000") on a terminal; update(…, 0, 0) removes it.
type progress struct {
	w     io.Writer
	st    report.Style
	last  time.Time
	shown bool
}

const clearLine = "\r\x1b[K"

func (p *progress) update(label string, done, total int) {
	if total == 0 {
		p.clear()
		return
	}
	if done < total && time.Since(p.last) < 80*time.Millisecond {
		return
	}
	p.last = time.Now()
	const barW = 20
	filled := done * barW / total
	tail := fmt.Sprintf("[%s%s] %3d%% %d/%d", strings.Repeat("#", filled), strings.Repeat("-", barW-filled), done*100/total, done, total)
	// keep the line on one row: shorten the label, never the bar
	labelW := max(p.st.Width-len(tail)-3, 8)
	fmt.Fprint(p.w, clearLine, " ", p.st.Dim(report.Fit(report.Clean(label), min(len([]rune(label)), labelW))), " ", p.st.Cyan(tail))
	p.shown = true
}

func (p *progress) clear() {
	if p.shown {
		fmt.Fprint(p.w, clearLine)
		p.shown = false
	}
}

// clearingWriter removes the progress line before a log line is written.
type clearingWriter struct {
	p *progress
	w io.Writer
}

func (c clearingWriter) Write(b []byte) (int, error) {
	c.p.clear()
	return c.w.Write(b)
}

// transient shows a status line that the next output replaces (terminal only).
func transient(st report.Style, text string) {
	if st.Width > 0 {
		fmt.Fprint(os.Stderr, clearLine, st.Dim(report.Fit(report.Clean(text), min(len([]rune(text)), st.Width-1))))
	}
}
