package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrflobow/newsletteraway/internal/detect"
)

func msg(mb string, uid uint32, from, name string, day int, links ...string) Message {
	return Message{Mailbox: mb, UID: uid, Result: &detect.Result{
		FromAddress: from, FromName: name, Subject: "s" + from,
		Date:    time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC),
		Links:   links,
		Sources: []detect.Source{detect.SourceHeader},
	}}
}

func TestBuildBySender(t *testing.T) {
	msgs := []Message{
		msg("INBOX", 1, "a@x.example", "A", 1, "https://x.example/old"),
		msg("INBOX", 5, "a@x.example", "A", 5, "mailto:u@x.example", "https://x.example/new"),
		msg("Promo", 2, "a@x.example", "A", 3),
		msg("INBOX", 3, "b@y.example", "", 9, "https://y.example/u"),
	}
	groups := Build(msgs, BySender)
	if len(groups) != 2 {
		t.Fatalf("groups = %d", len(groups))
	}
	a := groups[0]
	if a.Key != "a@x.example" || a.Count != 3 {
		t.Fatalf("first group = %+v", a)
	}
	// Links from the newest message with links, https first.
	if !reflect.DeepEqual(a.Links, []string{"https://x.example/new", "mailto:u@x.example"}) {
		t.Errorf("links = %v", a.Links)
	}
	if a.LastDate.Day() != 5 {
		t.Errorf("last date = %v", a.LastDate)
	}
	if !reflect.DeepEqual(a.UIDs, map[string][]uint32{"INBOX": {1, 5}, "Promo": {2}}) {
		t.Errorf("uids = %v", a.UIDs)
	}
}

func TestBuildByDomain(t *testing.T) {
	groups := Build([]Message{
		msg("INBOX", 1, "news@x.example", "", 1, "https://x.example/1"),
		msg("INBOX", 2, "promo@x.example", "", 2, "https://x.example/2"),
	}, ByDomain)
	if len(groups) != 1 || groups[0].Key != "x.example" || groups[0].Count != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if !reflect.DeepEqual(groups[0].Senders, []string{"news@x.example", "promo@x.example"}) {
		t.Errorf("senders = %v", groups[0].Senders)
	}
}

func TestRender(t *testing.T) {
	reps := []AccountReport{{
		Account: "acc", Mailboxes: []string{"INBOX"}, Scanned: 10, Detected: 1,
		Groups: Build([]Message{msg("INBOX", 1, "a@x.example", "A", 1, "https://x.example/u", "mailto:u@x.example")}, BySender),
	}}
	var tbl bytes.Buffer
	if err := WriteTable(&tbl, reps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"== acc [INBOX]", "A <a@x.example>", "https://x.example/u", "mailto:u@x.example"} {
		if !strings.Contains(tbl.String(), want) {
			t.Errorf("table missing %q:\n%s", want, tbl.String())
		}
	}

	var js bytes.Buffer
	if err := WriteJSON(&js, reps); err != nil {
		t.Fatal(err)
	}
	var back []AccountReport
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back[0].Groups[0].Links[0] != "https://x.example/u" {
		t.Errorf("json roundtrip = %+v", back)
	}
	if strings.Contains(js.String(), `\u0026`) {
		t.Error("JSON should not HTML-escape URLs")
	}
}

func TestCleanAndTableEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"plain <a@b.example>":   "plain <a@b.example>",
		"red\x1b[31mtext":       "red\ufffd[31mtext",
		"osc\x1b]52;c;aGk=\x07": "osc\ufffd]52;c;aGk=\ufffd",
		"c1\u009b31m":           "c1\ufffd31m",
		"bidi\u202egnp.exe":     "bidi\ufffdgnp.exe",
		"tab\tcol":              "tab\ufffdcol",
		"Grüße 👋":               "Grüße 👋",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}

	m := msg("INBOX", 1, "\x1b[2jevil@x.example", "\x1b]8;;https://evil.example\x07Bank", 1, "https://x.example/u")
	var buf bytes.Buffer
	WriteTable(&buf, []AccountReport{{Account: "a", Mailboxes: []string{"INBOX"}, Error: "server said \x1b[31mNO",
		Groups: Build([]Message{m}, BySender)}})
	if strings.ContainsAny(buf.String(), "\x1b\x07") {
		t.Errorf("control characters in table output: %q", buf.String())
	}
}
