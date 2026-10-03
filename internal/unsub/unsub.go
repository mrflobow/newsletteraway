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

// maxRedirects bounds the redirect chain. Many senders answer the POST with a
// redirect to a "you are unsubscribed" page.
const maxRedirects = 3

// userAgent identifies us honestly; some filters block the default Go agent.
const userAgent = "newsletteraway (one-click unsubscribe, RFC 8058)"

// Client sends the requests. Its dialer refuses loopback, private and
// link-local addresses on every hop, because the URL comes from an untrusted
// email, and redirects must stay on https. Tests replace it.
var Client = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Control: publicOnly}).DialContext,
	},
	CheckRedirect: checkRedirect,
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.URL.Scheme != "https" {
		return errors.New("redirected to a non-https URL")
	}
	return nil
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

// Post sends the one-click POST to rawURL, which must be https, and follows
// up to maxRedirects https redirects. A final 2xx response counts as success.
// A timeout is retried once.
func Post(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("one-click needs an https URL")
	}
	err = post(ctx, u)
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() && ctx.Err() == nil {
		err = post(ctx, u)
	}
	return err
}

func post(ctx context.Context, u *url.URL) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
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
