//go:build linux || darwin

package management

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
)

func TestManagedGuardRefreshesConstraintsAndTopology(t *testing.T) {
	a, p, _, c, dir := reviewAgent(t, nil)
	limits, err := LoadConstraints(p.Config.ConstraintsFile, false)
	if err != nil {
		t.Fatal(err)
	}
	a.constraints.Store(&limits)
	guard := a.Guard(c, p.Observer)
	if err := guard(); err != nil {
		t.Fatal(err)
	}
	a.managed.Store(true)
	if err := guard(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.Config.ConstraintsFile)
	if err != nil {
		t.Fatal(err)
	}
	tightened := limits
	tightened.CIDRs = []string{"127.0.0.2/32"}
	replacement := put(t, dir, "replacement.json", tightened)
	if err = os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, p.Config.ConstraintsFile); err != nil {
		t.Fatal(err)
	}
	// Same length and timestamp; an atomic replacement must still revoke access.
	if err = guard(); !errors.Is(err, ErrNetworkPolicy) {
		t.Fatal("cached grant survived local network revocation", err)
	}
	if a.constraints.Load().allowsIP(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("dial policy did not see revocation")
	}
	put(t, dir, "constraints.json", limits)
	if err = guard(); err != nil {
		t.Fatal("valid local grant was not restored", err)
	}
	changed := config.Clone(c)
	changed.Endpoints[0].Address = "127.0.0.2:1234"
	if err := a.Guard(changed, p.Observer)(); err == nil {
		t.Fatal("another scheduler reused the old topology's authorization")
	}
	tightened = limits
	tightened.MaxConcurrency = 1
	p.Config.ConstraintsFile = put(t, dir, "other-constraints.json", tightened)
	put(t, dir, "policy.json", p)
	if err = guard(); err == nil {
		t.Fatal("local constraints path change was ignored")
	}
}

func BenchmarkManagedGuard(b *testing.B) {
	for _, count := range []int{1, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			c := topology()
			x := c.Checks[0]
			c.Checks = nil
			for i := 0; i < count; i++ {
				x.ID = fmt.Sprintf("probe-%d", i)
				c.Checks = append(c.Checks, x)
			}
			limits, err := ConstraintsFor(c, "a")
			if err != nil {
				b.Fatal(err)
			}
			dir := b.TempDir()
			p := Policy{Schema: 1, Installation: "worker-a", Config: ConfigPolicy{ConstraintsFile: filepath.Join(dir, "limits.json")}}
			a := &Agent{path: filepath.Join(dir, "policy.json"), identity: p, concurrency: 4}
			for path, value := range map[string]any{a.path: p, p.Config.ConstraintsFile: limits} {
				if err := os.WriteFile(path, Marshal(value), 0600); err != nil {
					b.Fatal(err)
				}
			}
			a.constraints.Store(&limits)
			a.managed.Store(true)
			guard := a.Guard(c, "a")
			if err := guard(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := guard(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
