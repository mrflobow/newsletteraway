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

// Entry is one recorded unsubscribe.
type Entry struct {
	At time.Time `json:"at"`
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
// as Resubscribed.
func (s *Store) Apply(account string, groups []report.Group) []report.Group {
	out := groups[:0:0]
	for _, g := range groups {
		if e, ok := s.Entries[key(account, g.Key)]; ok {
			if !g.Received.After(e.At.Add(Grace)) {
				continue
			}
			g.Resubscribed = true
		}
		out = append(out, g)
	}
	return out
}
