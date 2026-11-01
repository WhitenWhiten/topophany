package importer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImportCandidates(t *testing.T) {
	for _, tt := range []struct {
		format, source, host string
		nodes, checks        int
	}{
		{"ssh", "Host server\n HostName 192.0.2.1\n Port 2222\n IdentityFile PRIVATE_KEY\n User PRIVATE_USER\n", "", 1, 1},
		{"ssh", "Host server\n ProxyCommand SECRET_COMMAND\n", "", 1, 0},
		{"ssh", "Host server\nHost *\n ProxyJump gateway\n", "", 1, 0},
		{"compose", `{"services":{"web":{"environment":{"TOKEN":"SECRET"},"ports":["8080:80","5353:53/udp",{"published":"9000","target":80}]}}}`, "192.0.2.2", 1, 2},
		{"tailscale", `{"Peer":{"public-key":{"HostName":"peer","TailscaleIPs":["100.64.0.1"],"Online":true}}}`, "", 1, 0},
	} {
		r, err := Build(tt.format, []byte(tt.source), tt.host)
		if err != nil {
			t.Fatalf("%s: %v", tt.format, err)
		}
		if len(r.Config.Nodes) != tt.nodes || len(r.Config.Checks) != tt.checks {
			t.Fatalf("%s: %+v", tt.format, r)
		}
		for _, check := range r.Config.Checks {
			if !check.Disabled {
				t.Fatal("active imported probe")
			}
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "PRIVATE") {
			t.Fatal("secret source leaked")
		}
		again, err := Build(tt.format, []byte(tt.source), tt.host)
		if err != nil {
			t.Fatal(err)
		}
		other, _ := json.Marshal(again)
		if string(b) != string(other) {
			t.Fatal("nondeterministic candidate")
		}
	}
}
func TestImportRejectsUnsafeAndUnboundedInputs(t *testing.T) {
	for _, tt := range []struct{ format, source, host string }{
		{"compose", `services: {web: {}}`, "host"},
		{"compose", `{"services":{}}`, "$(command)"},
		{"tailscale", `{"Peer":{},"Peer":{}}`, ""},
		{"ssh", strings.Repeat("x", (4<<20)+1), ""},
	} {
		if _, err := Build(tt.format, []byte(tt.source), tt.host); err == nil {
			t.Fatal("invalid import accepted", tt.format)
		}
	}
}

func TestSSHRepeatedHostsMergeDeclarationsAndRouteRestrictions(t *testing.T) {
	for _, tt := range []struct{ name, source, target string }{
		{"later port", "Host server\n HostName 192.0.2.1\nHost server\n Port 2222\n", "192.0.2.1:2222"},
		{"first value wins", "Host server\n Port 2222\nHost server\n Port 3333\n HostName 192.0.2.1\n", "192.0.2.1:2222"},
		{"later proxy", "Host server\n HostName 192.0.2.1\nHost server\n ProxyJump gateway\n", ""},
		{"overlapping aliases", "Host server other\n HostName 192.0.2.1\nHost server\n ProxyCommand private-command\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Build("ssh", []byte(tt.source), "")
			if err != nil {
				t.Fatal(err)
			}
			var target string
			for _, check := range r.Config.Checks {
				if check.Name == "SSH banner server" {
					target = check.Target
				}
			}
			if target != tt.target {
				t.Fatalf("target = %q, want %q", target, tt.target)
			}
		})
	}
}
