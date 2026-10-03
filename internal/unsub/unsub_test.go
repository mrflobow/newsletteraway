package unsub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPost(t *testing.T) {
	var method, ctype, body string
	status := http.StatusOK
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		method, ctype, body = r.Method, r.Header.Get("Content-Type"), string(b)
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
	defer func() { Client = old }()

	if err := Post(context.Background(), srv.URL+"/u"); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || ctype != "application/x-www-form-urlencoded" || body != "List-Unsubscribe=One-Click" {
		t.Errorf("got %s %q %q", method, ctype, body)
	}
	if err := Post(context.Background(), srv.URL+"/redir"); err == nil {
		t.Error("redirect was followed or accepted")
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
