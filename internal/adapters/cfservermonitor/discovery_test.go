package cfservermonitor

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestCFServerMonitorAuthenticatedDiscovery(t *testing.T) {
	const token = "synthetic.cfsm.sig+nature/"
	s := fixtureSource()
	s.CredentialEnv = "TOPO_CFSM_TEST_JWT"
	for _, authorized := range []bool{false, true} {
		io := &fixtureIO{token: token}
		io.read = func(target string, headers map[string]string, _ int64) source.Response {
			if headers["Authorization"] != "Bearer "+token {
				t.Fatal("discovery omitted the JWT")
			}
			var body string
			switch target {
			case "https://cfsm.example.invalid/monitor/api/config":
				body = fmt.Sprintf(`{"authorization":%t}`, authorized)
			case "https://cfsm.example.invalid/monitor/api/servers":
				if !authorized {
					t.Fatal("rejected JWT led to anonymous discovery")
				}
				body = fmt.Sprintf(`{"servers":[{"id":%q,"name":%q,"address":"discard-this"}]}`, testObject, "prefix "+token)
			default:
				t.Fatalf("unexpected discovery URL %s", target)
			}
			return source.Response{Status: http.StatusOK, Body: []byte(body)}
		}
		nodes, err := Discover(t.Context(), io, s)
		if !authorized {
			if source.Code(err) != "cfsm_auth" || nodes != nil || io.pages != 1 {
				t.Fatalf("unauthorized discovery = %+v, %v; pages = %d", nodes, err, io.pages)
			}
			continue
		}
		if err != nil || len(nodes) != 1 || nodes[0].ID != testObject || nodes[0].Name != "Server "+testObject || io.pages != 2 {
			t.Fatalf("discovery = %+v, %v; pages = %d", nodes, err, io.pages)
		}
	}
}

func TestCFServerMonitorDiscoveryRepresentation(t *testing.T) {
	row := fmt.Sprintf(`{"id":%q,"name":"Worker server"}`, testObject)
	for _, c := range []struct{ body, code string }{
		{`{"servers":null}`, "cfsm_json"},
		{`{"servers":[{"id":"injected"}]}`, "cfsm_record_identity"},
		{`{"servers":[` + row + "," + row + `]}`, "cfsm_record_identity"},
		{`{"servers":[` + strings.Repeat(row+",", 1000) + row + `]}`, "cfsm_discovery_budget"},
	} {
		if nodes, err := decodeServers([]byte(c.body), ""); nodes != nil || source.Code(err) != c.code {
			t.Fatalf("discovery = %+v, %v; want %s", nodes, err, c.code)
		}
	}
	if nodes, err := decodeServers([]byte(`{"servers":[]}`), ""); err != nil || nodes == nil || len(nodes) != 0 {
		t.Fatalf("empty discovery = %+v, %v", nodes, err)
	}
}
