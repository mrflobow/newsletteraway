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
	got := s.Apply("gmail", groups)
	if len(got) != 2 || !got[0].Resubscribed || got[1].Resubscribed || got[1].Key != "other@b.example" {
		t.Errorf("got %+v", got)
	}
	if n := len(s.Apply("other-account", groups)); n != 3 {
		t.Errorf("other account saw %d groups, want 3", n)
	}
}
