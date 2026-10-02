package imapconn

import (
	"bytes"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"

	"github.com/mrflobow/newsletteraway/internal/config"
)

// oauthSession adds XOAUTH2 to an in-memory server session. The token
// "good-token" logs in as user/pass.
type oauthSession struct {
	imapserver.Session
}

func (s oauthSession) AuthenticateMechanisms() []string { return []string{XOAuth2} }

func (s oauthSession) Authenticate(mech string) (sasl.Server, error) {
	if mech != XOAuth2 {
		return nil, errors.New("unsupported mechanism")
	}
	return &xoauth2Server{login: s.Session.Login}, nil
}

type xoauth2Server struct {
	login   func(user, pass string) error
	sentErr bool
}

func (s *xoauth2Server) Next(resp []byte) ([]byte, bool, error) {
	if s.sentErr { // client acknowledged the error challenge
		return nil, true, errors.New("invalid token")
	}
	parts := strings.Split(string(resp), "\x01")
	user := strings.TrimPrefix(parts[0], "user=")
	if len(parts) >= 2 && parts[1] == "auth=Bearer good-token" {
		return nil, true, s.login(user, "pass")
	}
	s.sentErr = true
	return []byte(`{"status":"401","schemes":"bearer","scope":"https://outlook.office.com/IMAP.AccessAsUser.All"}`), false, nil
}

func startServer(t *testing.T) config.Account {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("me@outlook.com", "pass")
	u.Create("INBOX", nil)
	u.Append("INBOX", bytes.NewReader([]byte("Subject: hi\r\n\r\nhi\r\n")), &imap.AppendOptions{})
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return oauthSession{mem.NewSession()}, nil, nil
		},
		InsecureAuth: true,
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}}, // the wrapper hides the IMAP4rev2 session methods
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
	return config.Account{Host: host, Port: p, Security: config.SecurityNone, Username: "me@outlook.com"}
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

func TestXOAuth2Login(t *testing.T) {
	a := startServer(t)

	c, err := Dial(a, Credentials{OAuth2Token: "good-token"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n, err := c.Open("INBOX", true); err != nil || n != 1 {
		t.Fatalf("open: n=%d err=%v", n, err)
	}

	_, err = Dial(a, Credentials{OAuth2Token: "bad-token"}, Options{})
	if err == nil || !strings.Contains(err.Error(), "login as me@outlook.com") {
		t.Errorf("bad token: %v", err)
	}
}

func TestXOAuth2InitialResponse(t *testing.T) {
	mech, ir, err := NewXOAuth2Client("me@outlook.com", "tok").Start()
	if err != nil || mech != "XOAUTH2" || string(ir) != "user=me@outlook.com\x01auth=Bearer tok\x01\x01" {
		t.Errorf("mech=%q ir=%q err=%v", mech, ir, err)
	}
}
