//go:build linux || darwin

package management

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestManagedDialEnforcesActualAddress(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		c, e := listener.Accept()
		if e == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()
	agent := &Agent{}
	agent.managed.Store(true)
	limits := Constraints{CIDRs: []string{"127.0.0.1/32"}}
	agent.constraints.Store(&limits)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, e := agent.Dial(ctx, &net.Dialer{}, "tcp", listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	<-accepted
	denied := Constraints{CIDRs: []string{"192.0.2.0/24"}}
	agent.constraints.Store(&denied)
	if _, e = agent.Dial(ctx, &net.Dialer{}, "tcp", listener.Addr().String()); !errors.Is(e, ErrNetworkPolicy) {
		t.Fatal("destination outside policy was dialed", e)
	}
	if limits.allowsIP(netip.MustParseAddr("::ffff:127.0.0.2")) {
		t.Fatal("IPv4-mapped policy bypass")
	}
}
func TestExternalCredentialReferenceIsAuthorizationNotMeasurement(t *testing.T) {
	c := model.Config{Schema: 1, Project: "home", Nodes: []model.Node{{ID: "server", Name: "Server", ResourceChecks: []string{"resources"}}}, Observers: []model.Observer{{ID: "a", Context: "test"}}, Sources: []model.Source{{ID: "source", Observer: "a", Adapter: "komari", Target: "https://monitor.example.invalid", CredentialEnv: "FIRST_TOKEN"}}, Checks: []model.Check{{ID: "resources", Name: "Resources", Kind: "external", Observer: "a", External: &model.ExternalBinding{Source: "source", Object: "server", Report: "resource", Node: "server", MaxAgeSeconds: 180}}}}
	config.Defaults(&c)
	if e := config.Validate(c); e != nil {
		t.Fatal(e)
	}
	limits, e := ConstraintsFor(c, "a", Constraints{CIDRs: []string{"0.0.0.0/0", "::/0"}})
	if e != nil {
		t.Fatal(e)
	}
	changed := config.Clone(c)
	changed.Sources[0].CredentialEnv = "OTHER_TOKEN"
	if config.Contract(c, c.Checks[0]) != config.Contract(changed, changed.Checks[0]) {
		t.Fatal("fixture should preserve measurement identity")
	}
	if e = limits.Check(changed, "a", 4); e == nil || e.Error() != "secret_binding_denied" {
		t.Fatal("credential reference authorized by measurement contract", e)
	}
}
