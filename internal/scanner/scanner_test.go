package scanner

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/mrflobow/newsletteraway/internal/config"
	"github.com/mrflobow/newsletteraway/internal/imapconn"
	"github.com/mrflobow/newsletteraway/internal/report"
)

const (
	testUser = "user"
	testPass = "pass"
)

var fixtures = []struct {
	raw string
	age time.Duration
}{
	{"From: Acme <news@acme.example>\r\nSubject: Acme weekly 1\r\nDate: Mon, 21 Sep 2026 10:00:00 +0000\r\n" +
		"List-Unsubscribe: <https://acme.example/u?t=1>, <mailto:u@acme.example>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\nhi\r\n", 48 * time.Hour},
	{"From: Acme <news@acme.example>\r\nSubject: Acme weekly 2\r\nDate: Mon, 28 Sep 2026 10:00:00 +0000\r\n" +
		"List-Unsubscribe: <https://acme.example/u?t=2>\r\n\r\nhi\r\n", 24 * time.Hour},
	{"From: Friend <friend@example.com>\r\nSubject: Lunch\r\nDate: Tue, 29 Sep 2026 10:00:00 +0000\r\n\r\nLunch tomorrow?\r\n", time.Hour},
	{"From: Club <club@list.example>\r\nSubject: Club news\r\nDate: Tue, 29 Sep 2026 10:00:00 +0000\r\nList-Id: <club.list.example>\r\n" +
		"Content-Type: text/html\r\n\r\n<p><a href=\"https://list.example/leave\">Unsubscribe</a></p>\r\n", time.Hour},
	{"From: Shop <deals@shop.example>\r\nSubject: Deals\r\nDate: Tue, 29 Sep 2026 10:00:00 +0000\r\n" +
		"Content-Type: text/html\r\n\r\n<p>Deals!</p><p><a href=\"https://shop.example/optout\">Abmelden</a></p>\r\n", time.Hour},
	{"From: Old <old@acme.example>\r\nSubject: Ancient\r\nDate: Mon, 01 Jan 2024 10:00:00 +0000\r\n" +
		"List-Unsubscribe: <https://old.example/u>\r\n\r\nold\r\n", 400 * 24 * time.Hour},
}

func startServer(t *testing.T, caps imap.CapSet) (config.Account, *imapmemserver.User) {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testUser, testPass)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		r := bytes.NewReader([]byte(f.raw))
		if _, err := user.Append("INBOX", r, &imap.AppendOptions{Time: time.Now().Add(-f.age)}); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         caps,
		Logger:       nopLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return config.Account{Name: "test", Host: host, Port: p, Security: config.SecurityNone, Username: testUser}, user
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

func dial(t *testing.T, a config.Account) *imapconn.Conn {
	t.Helper()
	c, err := imapconn.Dial(a, imapconn.Credentials{Password: testPass}, imapconn.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func since30d() time.Time { return time.Now().AddDate(0, 0, -30) }

func TestScanHeadersOnly(t *testing.T) {
	a, _ := startServer(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}})
	rep := Scan(dial(t, a), a.Name, []string{"INBOX"}, Options{Since: since30d(), GroupBy: report.BySender})

	if rep.Error != "" {
		t.Fatal(rep.Error)
	}
	if rep.Scanned != 5 {
		t.Errorf("scanned = %d, want 5 (old message excluded by date)", rep.Scanned)
	}
	// Acme (2, header) + Club (1, list-id only). Shop has no header signal.
	if rep.Detected != 3 || len(rep.Groups) != 2 {
		t.Fatalf("detected=%d groups=%+v", rep.Detected, rep.Groups)
	}
	acme := rep.Groups[0]
	if acme.Key != "news@acme.example" || acme.Count != 2 || acme.Links[0] != "https://acme.example/u?t=2" || acme.OneClick {
		t.Errorf("acme = %+v", acme)
	}
	if club := rep.Groups[1]; club.Key != "club@list.example" || len(club.Links) != 0 {
		t.Errorf("club = %+v", club)
	}
}

func TestScanBodyAndReadOnly(t *testing.T) {
	a, _ := startServer(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}})
	rep := Scan(dial(t, a), a.Name, []string{"INBOX"}, Options{BodyScan: true, GroupBy: report.ByDomain})

	keys := map[string]report.Group{}
	for _, g := range rep.Groups {
		keys[g.Key] = g
	}
	if g := keys["list.example"]; len(g.Links) != 1 || g.Links[0] != "https://list.example/leave" {
		t.Errorf("club via body = %+v", g)
	}
	if g := keys["shop.example"]; len(g.Links) != 1 {
		t.Errorf("shop via body = %+v", g)
	}
	if _, ok := keys["example.com"]; ok {
		t.Error("personal mail detected as newsletter")
	}
	if g := keys["acme.example"]; g.Count != 3 {
		t.Errorf("acme.example without date filter = %+v", g)
	}

	// Nothing may have been marked as read.
	c, err := imapclient.DialInsecure(a.Addr(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Login(testUser, testPass).Wait()
	c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	msgs, err := c.Fetch(imap.SeqSetNum(1, 2, 3, 4, 5, 6), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		for _, f := range m.Flags {
			if f == imap.FlagSeen {
				t.Errorf("message %d marked as seen", m.SeqNum)
			}
		}
	}
}

func TestMove(t *testing.T) {
	for name, caps := range map[string]imap.CapSet{
		"MOVE":          {imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
		"COPY fallback": {imap.CapIMAP4rev1: {}},
	} {
		t.Run(name, func(t *testing.T) {
			a, user := startServer(t, caps)
			c := dial(t, a)
			rep := Scan(c, a.Name, []string{"INBOX"}, Options{Since: since30d(), GroupBy: report.BySender})

			if _, err := MoveAll(c, rep.Groups, "Newsletters", false); err == nil || !strings.Contains(err.Error(), "does not exist") {
				t.Fatalf("expected missing-folder error, got %v", err)
			}
			moved, err := MoveAll(c, rep.Groups, "Newsletters", true)
			if err != nil || moved != 3 {
				t.Fatalf("moved=%d err=%v", moved, err)
			}
			inbox, _ := user.Status("INBOX", &imap.StatusOptions{NumMessages: true})
			nl, _ := user.Status("Newsletters", &imap.StatusOptions{NumMessages: true})
			if *inbox.NumMessages != 3 || *nl.NumMessages != 3 {
				t.Errorf("INBOX=%d Newsletters=%d, want 3/3", *inbox.NumMessages, *nl.NumMessages)
			}
		})
	}
}

func TestOversizedHeaderIsSkipped(t *testing.T) {
	a, user := startServer(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}})
	huge := "From: Spam <spam@huge.example>\r\nSubject: big\r\nList-Unsubscribe: <https://huge.example/u?pad=" +
		strings.Repeat("A", 2*MaxHeaderBytes) + ">\r\n\r\nbody\r\n"
	if _, err := user.Append("INBOX", bytes.NewReader([]byte(huge)), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	rep := Scan(dial(t, a), a.Name, []string{"INBOX"}, Options{Since: since30d(), GroupBy: report.BySender, Log: &log})
	if rep.Error != "" {
		t.Fatal(rep.Error)
	}
	for _, g := range rep.Groups {
		if g.Key == "spam@huge.example" {
			t.Errorf("oversized header was processed: %+v", g)
		}
	}
	if rep.Detected != 3 {
		t.Errorf("detected = %d, want the 3 normal newsletters", rep.Detected)
	}
	if !strings.Contains(log.String(), "headers larger than") {
		t.Errorf("no skip notice in log:\n%s", log.String())
	}
}

func TestLogIsSanitized(t *testing.T) {
	var log bytes.Buffer
	a, _ := startServer(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}})
	Scan(dial(t, a), a.Name, []string{"INBOX\x1b[2J"}, Options{Log: &log})
	if strings.Contains(log.String(), "\x1b") {
		t.Errorf("escape sequence reached the log: %q", log.String())
	}
}
