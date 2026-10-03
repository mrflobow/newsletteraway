package track

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrflobow/newsletteraway/internal/report"
)

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "unsubscribed.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s.Add("gmail", "news@a.example", at)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	s, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}

	groups := []report.Group{
		{Key: "news@a.example", Received: at.Add(Grace)},             // within grace: hidden
		{Key: "news@a.example", Received: at.Add(Grace + time.Hour)}, // mailed again: shown
		{Key: "other@b.example", Received: at.Add(-time.Hour)},       // never unsubscribed
	}
	got := s.Apply("gmail", groups, at.Add(Grace), false)
	if len(got) != 2 || !got[0].Resubscribed || got[1].Resubscribed || got[1].Key != "other@b.example" {
		t.Errorf("got %+v", got)
	}
	// Failed attempts keep the group listed. Permanent: Manual forever.
	// Temporary: Manual for TempRetryAfter. --retry-failed clears both.
	s.AddFailure("gmail", "perm@c.example", at, "server answered 403 Forbidden", false)
	s.AddFailure("gmail", "temp@c.example", at, "timeout", true)
	fg := []report.Group{{Key: "perm@c.example", Received: at}, {Key: "temp@c.example", Received: at}}
	for _, tc := range []struct {
		now        time.Time
		retry      bool
		perm, temp bool
	}{
		{at.Add(time.Hour), false, true, true},
		{at.Add(TempRetryAfter + time.Hour), false, true, false},
		{at.Add(365 * 24 * time.Hour), false, true, false},
		{at.Add(time.Hour), true, false, false},
	} {
		g := s.Apply("gmail", fg, tc.now, tc.retry)
		if len(g) != 2 || g[0].Manual != tc.perm || g[1].Manual != tc.temp {
			t.Errorf("now=%v retry=%v: %+v", tc.now.Sub(at), tc.retry, g)
		}
	}
	if n := len(s.Apply("other-account", groups, at, false)); n != 3 {
		t.Errorf("other account saw %d groups, want 3", n)
	}
}
