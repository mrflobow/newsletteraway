// Package imapconn is a thin wrapper around go-imap v2 with the operations
// newsletteraway needs. All reads use BODY.PEEK so the \Seen flag is untouched.
package imapconn

import (
	"crypto/tls"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/mrflobow/newsletteraway/internal/config"
)

// Conn is an authenticated IMAP connection.
type Conn struct {
	c *imapclient.Client
}

// Options tweak the connection; the zero value is fine.
type Options struct {
	TLSConfig   *tls.Config // nil uses system roots with ServerName = host
	DebugWriter io.Writer   // raw protocol trace (contains credentials!)
}

// Credentials hold either a password or an OAuth2 access token.
type Credentials struct {
	Password    string
	OAuth2Token string // when set, SASL XOAUTH2 is used instead of LOGIN
}

// Dial connects according to the account's security setting and logs in.
func Dial(a config.Account, cred Credentials, opts Options) (*Conn, error) {
	copts := &imapclient.Options{
		TLSConfig:   opts.TLSConfig,
		DebugWriter: opts.DebugWriter,
	}
	var (
		c   *imapclient.Client
		err error
	)
	switch a.Security {
	case config.SecurityTLS:
		c, err = imapclient.DialTLS(a.Addr(), copts)
	case config.SecuritySTARTTLS:
		c, err = imapclient.DialStartTLS(a.Addr(), copts)
	case config.SecurityNone:
		c, err = imapclient.DialInsecure(a.Addr(), copts)
	default:
		return nil, fmt.Errorf("unsupported security %q", a.Security)
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", a.Addr(), err)
	}
	if cred.OAuth2Token != "" {
		err = c.Authenticate(NewXOAuth2Client(a.Username, cred.OAuth2Token))
	} else {
		err = c.Login(a.Username, cred.Password).Wait()
	}
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("login as %s: %w", a.Username, err)
	}
	return &Conn{c: c}, nil
}

// Close logs out and closes the connection.
func (c *Conn) Close() error {
	_ = c.c.Logout().Wait()
	return c.c.Close()
}

// Open selects a mailbox. readOnly uses EXAMINE, which guarantees that no
// flags are changed.
func (c *Conn) Open(mailbox string, readOnly bool) (uint32, error) {
	data, err := c.c.Select(mailbox, &imap.SelectOptions{ReadOnly: readOnly}).Wait()
	if err != nil {
		return 0, fmt.Errorf("select %q: %w", mailbox, err)
	}
	return data.NumMessages, nil
}

// SearchSince returns the UIDs of messages received on or after since (all
// messages if since is zero), newest first, capped at limit when limit > 0.
func (c *Conn) SearchSince(since time.Time, limit int) ([]imap.UID, error) {
	criteria := &imap.SearchCriteria{Since: since}
	data, err := c.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	uids := data.AllUIDs()
	slices.SortFunc(uids, func(a, b imap.UID) int { return int(int64(b) - int64(a)) })
	if limit > 0 && len(uids) > limit {
		uids = uids[:limit]
	}
	return uids, nil
}

// FetchHeaders fetches the given header fields for uids in batches and calls
// fn for every message. At most maxBytes of each header are transferred
// (0 = unlimited), so a header of exactly maxBytes may be truncated.
func (c *Conn) FetchHeaders(uids []imap.UID, fields []string, batch int, maxBytes int64, fn func(uid imap.UID, header []byte) error) error {
	if batch <= 0 {
		batch = 200
	}
	section := &imap.FetchItemBodySection{
		Specifier:    imap.PartSpecifierHeader,
		HeaderFields: fields,
		Peek:         true,
	}
	if maxBytes > 0 {
		section.Partial = &imap.SectionPartial{Offset: 0, Size: maxBytes}
	}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}
	for start := 0; start < len(uids); start += batch {
		end := min(start+batch, len(uids))
		msgs, err := c.c.Fetch(imap.UIDSetNum(uids[start:end]...), opts).Collect()
		if err != nil {
			return fmt.Errorf("fetch headers: %w", err)
		}
		for _, m := range msgs {
			var raw []byte
			if len(m.BodySection) > 0 {
				raw = m.BodySection[0].Bytes
			}
			if err := fn(m.UID, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// FetchBody fetches up to maxBytes of the full raw message.
func (c *Conn) FetchBody(uid imap.UID, maxBytes int64) ([]byte, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	if maxBytes > 0 {
		section.Partial = &imap.SectionPartial{Offset: 0, Size: maxBytes}
	}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}
	msgs, err := c.c.Fetch(imap.UIDSetNum(uid), opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch body: %w", err)
	}
	if len(msgs) == 0 || len(msgs[0].BodySection) == 0 {
		return nil, fmt.Errorf("fetch body: message %d not returned", uid)
	}
	return msgs[0].BodySection[0].Bytes, nil
}

// MailboxExists reports whether a mailbox with that exact name exists.
func (c *Conn) MailboxExists(name string) (bool, error) {
	list, err := c.c.List("", name, nil).Collect()
	if err != nil {
		return false, fmt.Errorf("list %q: %w", name, err)
	}
	for _, m := range list {
		if m.Mailbox == name {
			return true, nil
		}
	}
	return false, nil
}

// CreateMailbox creates a mailbox.
func (c *Conn) CreateMailbox(name string) error {
	if err := c.c.Create(name, nil).Wait(); err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	return nil
}

// Move moves messages of the selected mailbox to dest. go-imap falls back to
// COPY + STORE \Deleted + EXPUNGE when the server lacks MOVE.
func (c *Conn) Move(uids []imap.UID, dest string) error {
	if len(uids) == 0 {
		return nil
	}
	if _, err := c.c.Move(imap.UIDSetNum(uids...), dest).Wait(); err != nil {
		return fmt.Errorf("move to %q: %w", dest, err)
	}
	return nil
}
