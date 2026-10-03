package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mrflobow/newsletteraway/internal/report"
)

func TestProgress(t *testing.T) {
	var buf bytes.Buffer
	p := &progress{w: &buf, st: report.Style{Width: 60}}
	p.update("outlook/INBOX", 50, 100)
	out := buf.String()
	if !strings.Contains(out, "50%") || !strings.Contains(out, "50/100") || strings.Contains(out, "\n") {
		t.Errorf("progress line: %q", out)
	}
	buf.Reset()
	(clearingWriter{p: p, w: &buf}).Write([]byte("log line\n"))
	if !strings.HasPrefix(buf.String(), clearLine) || p.shown {
		t.Errorf("log line did not clear progress: %q", buf.String())
	}
	buf.Reset()
	p.update("x", 0, 0) // nothing shown: nothing to clear
	if buf.Len() != 0 {
		t.Errorf("unexpected output %q", buf.String())
	}
	// a very long label must not push the line past the width
	p.update(strings.Repeat("a", 200), 100, 100)
	if n := len([]rune(strings.TrimPrefix(buf.String(), clearLine))); n > 60 {
		t.Errorf("line is %d wide", n)
	}
}
