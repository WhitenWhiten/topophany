package tui

import (
	"strings"
	"testing"
	"time"
)

func TestChineseDemoUsesLocalizedControlsAndVerdict(t *testing.T) {
	d, err := NewDemo("zh", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d.m.Width, d.m.Height = 140, 40
	snap, err := d.sim.Snapshot(d.m.Observer, d.m.Now)
	if err != nil {
		t.Fatal(err)
	}
	d.m.Snapshot = snap
	rendered := strings.Join(d.m.Render(), "\n")
	for _, text := range []string{"概览", "筛选", "服务可用性"} {
		if !strings.Contains(rendered, text) {
			t.Fatal("missing localized UI", text)
		}
	}
	if strings.Contains(rendered, "ALL CLEAR") {
		t.Fatal("unscoped English verdict in Chinese demo")
	}
}
