package unsub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPost(t *testing.T) {
	var method, ctype, body, agent string
	status := http.StatusOK
	slow := 0 // number of requests to /slow that stall
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redir":
			http.Redirect(w, r, "/u", http.StatusFound)
			return
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
			return
		case "/plain":
			http.Redirect(w, r, "http://example.com/x", http.StatusFound)
			return
		case "/slow":
			if slow > 0 {
				slow--
				time.Sleep(400 * time.Millisecond)
			}
		}
		b, _ := io.ReadAll(r.Body)
		method, ctype, body, agent = r.Method, r.Header.Get("Content-Type"), string(b), r.Header.Get("User-Agent")
		w.WriteHeader(status)
	}))
	defer srv.Close()

	// The default client must refuse the loopback test server.
	if err := Post(context.Background(), srv.URL+"/u"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback not refused: %v", err)
	}

	old := Client
	Client = srv.Client()
	Client.CheckRedirect = old.CheckRedirect
	Client.Timeout = 200 * time.Millisecond
	defer func() { Client = old }()

	if err := Post(context.Background(), srv.URL+"/u"); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || ctype != "application/x-www-form-urlencoded" || body != "List-Unsubscribe=One-Click" {
		t.Errorf("got %s %q %q", method, ctype, body)
	}
	if agent != userAgent {
		t.Errorf("user agent %q", agent)
	}
	if err := Post(context.Background(), srv.URL+"/redir"); err != nil {
		t.Errorf("redirect to a 2xx page: %v", err)
	}
	if err := Post(context.Background(), srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("redirect loop: %v", err)
	}
	if err := Post(context.Background(), srv.URL+"/plain"); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Errorf("redirect to http: %v", err)
	}
	slow = 1 // first attempt times out, the retry succeeds
	if err := Post(context.Background(), srv.URL+"/slow"); err != nil {
		t.Errorf("timeout was not retried: %v", err)
	}
	slow = 2
	if err := Post(context.Background(), srv.URL+"/slow"); err == nil {
		t.Error("two timeouts accepted")
	}
	status = http.StatusInternalServerError
	if err := Post(context.Background(), srv.URL+"/u"); err == nil {
		t.Error("500 accepted")
	}
	for _, u := range []string{"http://example.com/u", "mailto:a@b.example", "https:///x", "::"} {
		if err := Post(context.Background(), u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestTemporary(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"503", &StatusError{503, "503 Service Unavailable"}, true},
		{"429", &StatusError{429, "429 Too Many Requests"}, true},
		{"403", &StatusError{403, "403 Forbidden"}, false},
		{"404", &StatusError{404, "404 Not Found"}, false},
		{"deadline", context.DeadlineExceeded, true},
		{"timeout", &url.Error{Op: "Post", URL: "https://x.example", Err: &net.OpError{Op: "dial", Err: timeoutErr{}}}, true},
		{"refused", &url.Error{Op: "Post", URL: "https://x.example", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		{"dns not found", &url.Error{Err: &net.DNSError{IsNotFound: true}}, false},
		{"redirect policy", &url.Error{Op: "Post", URL: "https://x.example", Err: errors.New("redirected to a non-https URL")}, false},
		{"non-public", &url.Error{Err: &net.OpError{Op: "dial", Err: fmt.Errorf("%w 10.0.0.1", ErrNonPublic)}}, false},
	} {
		if got := Temporary(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
