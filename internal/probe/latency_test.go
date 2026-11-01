package probe

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLatencyBudgetKeepsFailureAndContractIdentity(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer server.Close()
	x := httpCheck(server.URL)
	c := testkit.Config()
	legacy := config.Contract(c, x)
	x.MaxDurationMS = 100
	if config.Contract(c, x) == legacy {
		t.Fatal("policy missing from contract")
	}
	runner := Runner{since: func(time.Time) time.Duration { return 101 * time.Millisecond }}
	o := runner.Run(context.Background(), c, x)
	if o.Code != "latency_exceeded" || o.Outcome != "policy" || !o.Connectivity {
		t.Fatal(o)
	}
	status = 503
	o = runner.Run(context.Background(), c, x)
	if o.Code != "unexpected_status" || o.Outcome != "fail" {
		t.Fatal(o)
	}
	x.MaxDurationMS = 0
	if config.Contract(c, x) != legacy {
		t.Fatal("disabled policy changed contract")
	}
	x.MaxDurationMS = 100
	x.Kind = "host"
	if config.ValidateProbePolicies(c, x) == nil {
		t.Fatal("reported adapter accepted latency policy")
	}
}
