// Package track remembers which senders were unsubscribed and when, so that
// later mail from them can be flagged.
package track

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mrflobow/newsletteraway/internal/config"
	"github.com/mrflobow/newsletteraway/internal/report"
)

// Grace is how long after an unsubscribe mail may still arrive (senders have
// days to process a request) before it counts as "mailing again".
const Grace = 10 * 24 * time.Hour

// TempRetryAfter is how long after a temporary failure (timeout, network
// error, 5xx) the request is tried again automatically. Permanent failures
// (403, 404, ...) are never retried automatically.
const TempRetryAfter = 24 * time.Hour

// Entry is one recorded unsubscribe attempt. An empty Failure means success.
type Entry struct {
	At        time.Time `json:"at"`
	Failure   string    `json:"failure,omitempty"`
	Temporary bool      `json:"temporary,omitempty"` // failure may go away by itself
}

// Store maps account + group key to the unsubscribe record.
type Store struct {
	path    string
	Entries map[string]Entry `json:"entries"`
}

// DefaultPath is the store file next to the default config.
func DefaultPath() string {
	return filepath.Join(filepath.Dir(config.DefaultPath()), "unsubscribed.json")
}

func key(account, group string) string { return account + "\x00" + group }

// Load reads the store; a missing file is an empty store.
func Load(path string) (*Store, error) {
	s := &Store{path: path, Entries: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	return s, nil
}

// Add records an unsubscribe at the given time.
func (s *Store) Add(account, group string, at time.Time) {
	s.Entries[key(account, group)] = Entry{At: at.UTC()}
}

// AddFailure records a failed attempt with a short reason.
func (s *Store) AddFailure(account, group string, at time.Time, reason string, temporary bool) {
	s.Entries[key(account, group)] = Entry{At: at.UTC(), Failure: reason, Temporary: temporary}
}

// Save writes the store atomically with mode 0600.
func (s *Store) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Apply drops groups that were unsubscribed and have not mailed since, and
// marks groups that have (newest INTERNALDATE after the unsubscribe + Grace)
// as Resubscribed. Groups whose last attempt failed stay listed and are marked
// Manual (not tried automatically): permanent failures always, temporary ones
// for TempRetryAfter. retryFailed clears the mark so everything is tried again.
func (s *Store) Apply(account string, groups []report.Group, now time.Time, retryFailed bool) []report.Group {
	out := groups[:0:0]
	for _, g := range groups {
		if e, ok := s.Entries[key(account, g.Key)]; ok {
			if e.Failure != "" {
				g.Manual = !retryFailed && (!e.Temporary || now.Before(e.At.Add(TempRetryAfter)))
				out = append(out, g)
				continue
			}
			if !g.Received.After(e.At.Add(Grace)) {
				continue
			}
			g.Resubscribed = true
		}
		out = append(out, g)
	}
	return out
}
