// Package scanner runs the newsletter detection over an IMAP connection.
package scanner

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/mrflobow/newsletteraway/internal/detect"
	"github.com/mrflobow/newsletteraway/internal/imapconn"
	"github.com/mrflobow/newsletteraway/internal/report"
)

// DefaultBodyMaxBytes caps how much of a message the body scan downloads.
const DefaultBodyMaxBytes = 1 << 20

// MaxHeaderBytes caps the fetched classification headers per message. Real
// headers are a few KB; larger ones are skipped as malformed or hostile.
const MaxHeaderBytes = 64 << 10

// Options control a scan.
type Options struct {
	Since        time.Time // zero = no date filter
	Limit        int       // max messages per mailbox, newest first; 0 = unlimited
	BodyScan     bool      // look for unsubscribe links in bodies of messages without header links
	BodyMaxBytes int64
	GroupBy      report.GroupBy
	Log          io.Writer // progress output; nil = silent
	// Progress, if set, is called as messages are processed (label like
	// "INBOX" or "INBOX body"); done == total == 0 means "finished, clear".
	Progress func(label string, done, total int)
}

// Scan examines every mailbox read-only and returns the account report.
// Errors in one mailbox are recorded in the report and do not stop the others.
func Scan(c *imapconn.Conn, account string, mailboxes []string, opts Options) report.AccountReport {
	if opts.BodyMaxBytes == 0 {
		opts.BodyMaxBytes = DefaultBodyMaxBytes
	}
	logf := func(format string, args ...any) {
		if opts.Log != nil {
			// Errors may quote raw header data from the message.
			msg := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
			fmt.Fprintln(opts.Log, report.Clean(msg))
		}
	}

	rep := report.AccountReport{Account: account, Mailboxes: mailboxes}
	var msgs []report.Message
	var errs []string
	for _, mb := range mailboxes {
		found, scanned, err := scanMailbox(c, mb, opts, logf)
		rep.Scanned += scanned
		msgs = append(msgs, found...)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	rep.Detected = len(msgs)
	rep.Groups = report.Build(msgs, opts.GroupBy)
	if len(errs) > 0 {
		rep.Error = joinErrs(errs)
	}
	return rep
}

func scanMailbox(c *imapconn.Conn, mb string, opts Options, logf func(string, ...any)) ([]report.Message, int, error) {
	if _, err := c.Open(mb, true); err != nil {
		return nil, 0, err
	}
	uids, err := c.SearchSince(opts.Since, opts.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", mb, err)
	}
	logf("%s: scanning %d messages\n", mb, len(uids))

	var (
		found     []report.Message
		bodyQueue []report.Message // messages without header links
		done      int
	)
	progress := func(label string, n, total int) {
		if opts.Progress != nil {
			opts.Progress(label, n, total)
		}
	}
	defer progress(mb, 0, 0)
	err = c.FetchHeaders(uids, detect.HeaderFields, 200, MaxHeaderBytes, func(uid imap.UID, received time.Time, raw []byte) error {
		done++
		progress(mb, done, len(uids))
		if len(raw) >= MaxHeaderBytes {
			logf("%s: uid %d: headers larger than %d KB, skipped", mb, uid, MaxHeaderBytes>>10)
			return nil
		}
		h, err := parseHeader(raw)
		if err != nil {
			logf("%s: uid %d: unparsable header: %v\n", mb, uid, err)
			return nil
		}
		m := report.Message{Mailbox: mb, UID: uint32(uid), Received: received, Result: detect.Classify(h)}
		switch {
		case len(m.Links) > 0:
			found = append(found, m)
		case opts.BodyScan:
			bodyQueue = append(bodyQueue, m)
		case m.IsNewsletter():
			found = append(found, m) // list-id / precedence only, no links
		}
		return nil
	})
	if err != nil {
		return found, len(uids), fmt.Errorf("%s: %w", mb, err)
	}

	if len(bodyQueue) > 0 {
		logf("%s: body scan of %d messages without List-Unsubscribe\n", mb, len(bodyQueue))
	}
	for i, m := range bodyQueue {
		progress(mb+" body", i+1, len(bodyQueue))
		raw, err := c.FetchBody(imap.UID(m.UID), opts.BodyMaxBytes)
		if err != nil {
			return found, len(uids), fmt.Errorf("%s: %w", mb, err)
		}
		links, err := detect.FindBodyLinks(bytes.NewReader(raw))
		if err != nil {
			logf("%s: uid %d: body parse: %v\n", mb, m.UID, err)
		}
		m.AddBodyLinks(links)
		if m.IsNewsletter() {
			found = append(found, m)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].UID > found[j].UID })
	return found, len(uids), nil
}

func parseHeader(raw []byte) (mail.Header, error) {
	// HEADER.FIELDS responses end with an empty line; make sure there is one.
	if !bytes.HasSuffix(raw, []byte("\r\n\r\n")) && !bytes.HasSuffix(raw, []byte("\n\n")) {
		raw = append(raw, "\r\n"...)
	}
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return mail.Header{}, err
	}
	return mail.Header{Header: message.Header{Header: h}}, nil
}

// MoveAll moves the UIDs of all groups to dest, one mailbox at a time.
// It returns the number of moved messages.
func MoveAll(c *imapconn.Conn, groups []report.Group, dest string, create bool) (int, error) {
	exists, err := c.MailboxExists(dest)
	if err != nil {
		return 0, err
	}
	if !exists {
		if !create {
			return 0, fmt.Errorf("mailbox %q does not exist (use --create-folder)", dest)
		}
		if err := c.CreateMailbox(dest); err != nil {
			return 0, err
		}
	}

	byMailbox := map[string][]imap.UID{}
	for _, g := range groups {
		for mb, uids := range g.UIDs {
			for _, u := range uids {
				byMailbox[mb] = append(byMailbox[mb], imap.UID(u))
			}
		}
	}
	moved := 0
	for mb, uids := range byMailbox {
		if mb == dest {
			continue
		}
		if _, err := c.Open(mb, false); err != nil {
			return moved, err
		}
		if err := c.Move(uids, dest); err != nil {
			return moved, fmt.Errorf("%s: %w", mb, err)
		}
		moved += len(uids)
	}
	return moved, nil
}

func joinErrs(errs []string) string {
	var b bytes.Buffer
	for i, e := range errs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e)
	}
	return b.String()
}
