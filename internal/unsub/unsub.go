// Package unsub sends RFC 8058 one-click unsubscribe requests.
package unsub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Client sends the requests. It never follows redirects, and its dialer
// refuses loopback, private and link-local addresses, because the URL comes
// from an untrusted email. Tests replace it.
var Client = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Control: publicOnly}).DialContext,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func publicOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	a := ap.Addr().Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast() {
		return fmt.Errorf("refusing to connect to non-public address %s", a)
	}
	return nil
}

// Post sends the one-click POST to rawURL, which must be https. Any 2xx
// response counts as success.
func Post(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("one-click needs an https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("server answered %s", resp.Status)
	}
	return nil
}
