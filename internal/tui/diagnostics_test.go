package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/session"
)

func blockSessionCache(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	key := "XDG_CACHE_HOME"
	switch runtime.GOOS {
	case "windows":
		key = "LocalAppData"
	case "darwin":
		key = "HOME"
	}
	t.Setenv(key, path)
}

func TestRemoteContinuesWithoutSessionCache(t *testing.T) {
	_, profile := remoteFixture(t)
	blockSessionCache(t)
	r, err := NewRemote(profile, "")
	if err != nil {
		t.Fatal("diagnostic cache prevented starting viewer", err)
	}
	defer r.Close()
	waitRemote(t, r, func() bool { return !r.m.Snapshot.GeneratedAt.IsZero() })
	if r.diagnostics != nil || r.m.Error != "" || r.m.Cached() || !r.m.noticeErr || r.m.notice != "Session diagnostics write failed" {
		t.Fatal("viewer lost live state or diagnostic failure notice", r.m.Diagnostics())
	}
}

func TestDiagnosticsRetainErrorsAfterScreenRecovers(t *testing.T) {
	now := time.Now()
	m := &Model{Now: now, Project: "lab", Server: "https://collector.test", Observer: "wsl", Search: "api", Application: "app", Environment: "prod", ServiceFilter: "svc", Snapshot: model.Snapshot{GeneratedAt: now, Instance: "s2"}}
	m.keepFetchError(errors.New("TLS handshake timed out"))
	m.KeepSamples("check", nil, errors.New("history unavailable"))
	m.say("temporary banner", true)
	d := m.Diagnostics()
	if d.Error != m.Error || d.Notice.Text != m.notice || d.History.Error != m.samples.err || !d.Cached || d.Filters.Search != "api" || d.Observer != "wsl" {
		t.Fatal(d)
	}
	m.Now = now.Add(time.Second)
	m.diagnosticEvent("retry", "snapshot_retry", "")
	m.keepFetchError(nil)
	m.KeepSamples("check", nil, nil)
	d = m.Diagnostics()
	if d.Error != "" || d.History.Error != "" || len(d.Events) != 6 {
		t.Fatal(d)
	}
	m.Now = now.Add(session.Retention + 2*time.Second)
	if len(m.Diagnostics().Events) != 0 {
		t.Fatal("expired history retained")
	}
}
