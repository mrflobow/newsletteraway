package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/spf13/pflag"

	"github.com/mrflobow/newsletteraway/internal/report"
)

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

func startServer(t *testing.T) string {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("u", "p")
	user.Create("INBOX", nil)
	raw := "From: Acme <news@acme.example>\r\nSubject: Weekly\r\nDate: Mon, 28 Sep 2026 10:00:00 +0000\r\n" +
		"List-Unsubscribe: <https://acme.example/u?a=1&b=2>\r\n\r\nhi\r\n"
	user.Append("INBOX", bytes.NewReader([]byte(raw)), &imap.AppendOptions{Time: time.Now()})
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
		Logger:       nopLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags() // flags are package globals; reset between runs
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func resetFlags() {
	reset := func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			sv.Replace(nil)
		} else {
			f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	scanCmd.Flags().VisitAll(reset)
	rootCmd.PersistentFlags().VisitAll(reset)
}

func TestScanCommand(t *testing.T) {
	host, port, _ := net.SplitHostPort(startServer(t))
	t.Setenv("NA_TEST_PW", "p")
	home := t.TempDir() // no default config
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

	common := []string{"scan", "-q", "--host", host, "--port", port, "--security", "none", "--user", "u", "--password-env", "NA_TEST_PW"}

	out, err := run(t, append(common, "--days", "90")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Acme <news@acme.example>") || !strings.Contains(out, "https://acme.example/u?a=1&b=2") {
		t.Errorf("table output:\n%s", out)
	}
	t.Log("\n" + out)

	out, err = run(t, append(common, "-o", "json", "--days", "90", "--group-by", "sender")...)
	if err != nil {
		t.Fatal(err)
	}
	var reps []report.AccountReport
	if err := json.Unmarshal([]byte(out), &reps); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(reps) != 1 || reps[0].Account != "u" || reps[0].Detected != 1 {
		t.Errorf("json = %+v", reps)
	}

	// Config file with an account entry and a dry-run move.
	cfg := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(cfg, []byte(`{"accounts":[{"name":"local","provider":"custom","host":"`+host+`","port":`+port+
		`,"security":"none","username":"u","password_env":"NA_TEST_PW"}]}`), 0o600)
	out, err = run(t, "scan", "-q", "--config", cfg, "--account", "local", "--move-to", "Newsletters", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "== local [INBOX]") {
		t.Errorf("config output:\n%s", out)
	}

	if _, err := run(t, "scan", "--config", cfg, "--account", "nope"); err == nil {
		t.Error("expected error for unknown account")
	}
	if _, err := run(t, append(common, "--user", "u", "--password-env", "UNSET_VAR_X")...); err == nil {
		t.Error("expected error when no password source is available")
	}
}
