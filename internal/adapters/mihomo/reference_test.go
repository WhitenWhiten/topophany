package mihomo_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

var testControllerUpdated = time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)

func referenceDocument(at time.Time, names []string) string {
	raw, _ := json.Marshal(map[string]any{"schema": 1, "published_at": at.UTC(), "providers": map[string][]string{"edge-local": names}})
	return string(raw)
}
func referenceHarness(t *testing.T, reference http.HandlerFunc, loaded func() []string) model.Check {
	t.Helper()
	t.Setenv("TOPH_CONTROLLER_TEST", "controller-private")
	t.Setenv("TOPH_PUBLISHER_TEST", "publisher-private")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reference" {
			if r.Header.Get("Authorization") != "Bearer publisher-private" {
				t.Error("publisher did not receive its own credential")
			}
			reference(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer controller-private" {
			t.Error("controller credential crossed publisher boundary")
		}
		switch r.URL.Path {
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule","tun":{"enable":true}}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"root":{"type":"Selector","now":"fixed-node"},"fixed-node":{"type":"VLESS"}}}`)
		case "/providers/proxies":
			proxies := []map[string]string{}
			for _, name := range loaded() {
				proxies = append(proxies, map[string]string{"name": name})
			}
			json.NewEncoder(w).Encode(map[string]any{"providers": map[string]any{"fixed": map[string]any{"proxies": []map[string]string{{"name": "fixed-node"}}, "updatedAt": testControllerUpdated}, "edge-local": map[string]any{"proxies": proxies, "updatedAt": testControllerUpdated}}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	x := httpCheck(srv.URL)
	x.Kind = "mihomo"
	x.Adapter = model.AdapterOptions{TokenEnv: "TOPH_CONTROLLER_TEST", RootProxy: "root", ExpectedMode: "rule", ExpectedProviders: map[string][]string{"fixed": {"fixed-node"}}, ProviderReference: &model.MihomoProviderReference{URL: srv.URL + "/reference", Providers: []string{"edge-local"}, MaxAgeSeconds: 3600, SettleSeconds: new(int), TokenEnv: "TOPH_PUBLISHER_TEST"}}
	return x
}
func runReference(x model.Check) model.Observation {
	return (probe.Runner{}).Run(context.Background(), testkit.Config(), x)
}

func TestMihomoDynamicProviderRotation(t *testing.T) {
	var generation atomic.Int32
	generation.Store(1)
	published := time.Now().UTC().Truncate(time.Second)
	members := func() []string { return []string{fmt.Sprintf("edge-%d", generation.Load())} }
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, referenceDocument(published.Add(time.Duration(generation.Load()-1)*time.Second), members()))
	}, members)
	contract := config.Contract(testkit.Config(), x)
	first := runReference(x)
	generation.Store(2)
	second := runReference(x)
	for _, o := range []model.Observation{first, second} {
		if o.Outcome != "ok" || o.Evidence != "reported" || o.Connectivity || o.Facts["provider.edge-local"] != "undeclared" || o.Contract != contract {
			t.Fatal(o)
		}
		if code := config.ObservationBounds(o, time.Second); code != "" {
			t.Fatal(code, o)
		}
	}
	if first.Facts["provider_reference_published_at"] == second.Facts["provider_reference_published_at"] {
		t.Fatal("publication rotation lost provenance")
	}
	if config.Contract(testkit.Config(), x) != contract {
		t.Fatal("dynamic contents changed the configured contract")
	}
	if got := second.Facts["provider_updated_at.edge-local"]; got != "" {
		t.Fatalf("credential-bearing controller exported load time: %q", got)
	}
}

func TestMihomoReferenceSettlesAfterPublication(t *testing.T) {
	names := []string{"edge-a", "edge-b"}
	doc := referenceDocument(time.Now().UTC().Add(-30*time.Second), names)
	drifted := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string { return []string{"old"} })
	// Unset uses the default wait: a publication 30s old may still be loading.
	drifted.Adapter.ProviderReference.SettleSeconds = nil
	o := runReference(drifted)
	if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" || o.Facts["provider_reference_settling"] != "true" {
		t.Fatal(o)
	}
	if !strings.Contains(o.Facts["provider_comparison"], "=settling") {
		t.Fatalf("comparison does not say why it is unknown yet: %q", o.Facts["provider_comparison"])
	}
	if !strings.Contains(o.Detail, "not yet confirmed") {
		t.Fatalf("settling sample is not marked unconfirmed: %q", o.Detail)
	}
	one, zero := 1, 0
	drifted.Adapter.ProviderReference.SettleSeconds = &one
	if o = runReference(drifted); o.Outcome != "policy" || o.Code != "provider_membership_mismatch" || o.Facts["provider_reference_settling"] != "" {
		t.Fatal("a window shorter than the publication age hid a violation", o)
	}
	drifted.Adapter.ProviderReference.SettleSeconds = &zero
	if o = runReference(drifted); o.Outcome != "policy" || o.Facts["provider_reference_settling"] != "" {
		t.Fatal("zero did not disable the wait", o)
	}

	// A matching publication is never held back by the wait.
	fresh := referenceDocument(time.Now().UTC(), names)
	matched := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, fresh) }, func() []string { return names })
	matched.Adapter.ProviderReference.SettleSeconds = nil
	if o = runReference(matched); o.Outcome != "ok" || o.Facts["provider_reference_settling"] != "" {
		t.Fatal("matching membership was reported as settling", o)
	}
}

func TestMihomoKnownModePolicyIsNotSettledByAProvider(t *testing.T) {
	names := []string{"edge-a", "edge-b"}
	doc := referenceDocument(time.Now().UTC().Add(-30*time.Second), names)
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string { return []string{"old"} })
	x.Adapter.ExpectedMode = "global" // the controller reports rule
	x.Adapter.ProviderReference.SettleSeconds = nil
	o := runReference(x)
	if o.Outcome != "policy" || o.Facts["provider_reference_settling"] != "" || o.Facts["provider_reference_load_pending"] != "false" {
		t.Fatalf("a known mode policy was marked as a settling publication: %+v", o)
	}
}

func TestMihomoReferenceFailuresAreMonitorErrors(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	valid := referenceDocument(at, []string{"edge-a", "edge-b"})
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{name: "stale", body: referenceDocument(at.Add(-2*time.Hour), []string{"edge-a"}), code: "reference_not_fresh"},
		{name: "future", body: referenceDocument(at.Add(time.Hour), []string{"edge-a"}), code: "reference_not_fresh"},
		{name: "missing", body: `{"schema":1,"published_at":"` + at.Format(time.RFC3339) + `","providers":{}}`, code: "reference_provider_missing"},
		{name: "empty", body: referenceDocument(at, []string{}), code: "reference_provider_missing"},
		{name: "duplicate names", body: referenceDocument(at, []string{"edge-a", "edge-a"}), code: "invalid_reference_members"},
		{name: "control text", body: referenceDocument(at, []string{"edge-\x1b"}), code: "invalid_reference_members"},
		{name: "shape", body: `not-json`, code: "reference_complexity_exceeded"},
		{name: "schema", body: strings.Replace(valid, `"schema":1`, `"schema":2`, 1), code: "invalid_reference_document"},
		{name: "unknown field", body: strings.Replace(valid, `"schema":1`, `"schema":1,"secret":"private"`, 1), code: "invalid_reference_document"},
		{name: "duplicate key", body: strings.Replace(valid, `"schema":1`, `"schema":1,"schema":1`, 1), code: "reference_complexity_exceeded"},
		{name: "size", body: strings.Repeat("x", (1<<20)+1), code: "reference_response_limit_exceeded"},
		{name: "auth", status: 401, code: "reference_http_401"},
		{name: "unavailable", status: 503, code: "reference_http_503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}, func() []string { return []string{"edge-a", "edge-b"} })
			o := runReference(x)
			if o.Outcome != "error" || o.Code != tc.code || o.Stage != "provider_reference" {
				t.Fatal(o)
			}
		})
	}
}

func TestMihomoReferenceStillDetectsMembershipAndOrderDrift(t *testing.T) {
	doc := referenceDocument(time.Now().UTC(), []string{"edge-a", "edge-b"})
	for _, names := range [][]string{{"old"}, {"edge-b", "edge-a"}, {"edge-a"}} {
		x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string { return names })
		o := runReference(x)
		if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" {
			t.Fatal(o)
		}
	}
}

func TestMihomoReferenceChangingDuringSamplingIsUnknown(t *testing.T) {
	var calls atomic.Int32
	at := time.Now().UTC()
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, referenceDocument(at, []string{fmt.Sprintf("edge-%d", calls.Add(1))}))
	}, func() []string { return []string{"old"} })
	o := runReference(x)
	if o.Outcome != "unknown" || o.Code != "provider_reference_changed" {
		t.Fatal(o)
	}
	// A fixed mode violation remains valid even when the dynamic source changes.
	x.Adapter.ExpectedMode = "global"
	o = runReference(x)
	if o.Outcome != "policy" || o.Code != "mode_mismatch" {
		t.Fatal(o)
	}
	x.Adapter.ExpectedMode = "rule"
	x.Adapter.ExpectedProviders["fixed"] = []string{"different-fixed-node"}
	o = runReference(x)
	if o.Outcome != "policy" || o.Code != "provider_membership_mismatch" {
		t.Fatal("reference churn hid a fixed provider violation", o)
	}
}

func TestMihomoReferenceCannotPublishSecrets(t *testing.T) {
	names := []string{"controller-private", "publisher-private"}
	doc := referenceDocument(time.Now().UTC(), names)
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string { return names })
	o := runReference(x)
	raw, _ := json.Marshal(o)
	if o.Outcome != "ok" || strings.Contains(string(raw), "controller-private") || strings.Contains(string(raw), "publisher-private") {
		t.Fatal(o)
	}
	x.Adapter.ProviderReference.TokenEnv = "MISSING_REFERENCE_SECRET"
	t.Setenv("MISSING_REFERENCE_SECRET", "")
	if o = runReference(x); o.Outcome != "error" || o.Code != "reference_secret_missing" {
		t.Fatal(o)
	}
}

func TestMihomoReferenceRejectsRedirectAndHonorsDeadline(t *testing.T) {
	for _, slow := range []bool{false, true} {
		var followed atomic.Int32
		trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
		t.Cleanup(trap.Close)
		x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
			if slow {
				<-r.Context().Done()
				return
			}
			http.Redirect(w, r, trap.URL, http.StatusFound)
		}, func() []string { return []string{"edge-a"} })
		began := time.Now()
		o := runReference(x)
		want := "reference_http_302"
		if slow {
			want = "reference_unavailable"
		}
		if o.Outcome != "error" || o.Code != want || followed.Load() != 0 || time.Since(began) > 3*time.Second {
			t.Fatal(o)
		}
	}
}

func TestMihomoReferenceHasIndependentTLSAndResolveSettings(t *testing.T) {
	doc := referenceDocument(time.Now().UTC(), []string{"edge-a"})
	publisher := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }))
	defer publisher.Close()
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { t.Error("wrong publisher") }, func() []string { return []string{"edge-a"} })
	x.Adapter.ProviderReference.URL = publisher.URL
	// A controller resolve override must not pin the publisher to that controller.
	x.ResolveIP = "127.0.0.1"
	x.ServerName = "controller.invalid"
	if o := runReference(x); o.Outcome != "error" || o.Code != "reference_unavailable" {
		t.Fatal(o)
	}
	ca := filepath.Join(t.TempDir(), "publisher-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: publisher.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	x.Adapter.ProviderReference.CAFile = ca
	if o := runReference(x); o.Outcome != "ok" {
		t.Fatal(o)
	}
}

func TestMihomoReferenceFitsObservationBudget(t *testing.T) {
	doc := referenceDocument(time.Now().UTC(), []string{"edge-a"})
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, doc)
	}, func() []string { return []string{"edge-a"} })
	// Fifteen fixed providers plus one dynamic provider is the maximum. Missing
	// fixed providers are a legitimate policy result, not a rejected observation.
	for i := 0; i < 14; i++ {
		x.Adapter.ExpectedProviders[fmt.Sprintf("fixed-%d", i)] = []string{"fixed-node"}
	}
	x.BindIP = "127.0.0.1"
	o := runReference(x)
	if o.Outcome != "policy" || config.ObservationBounds(o, time.Second) != "" {
		t.Fatal("maximum provider evidence exceeds collector admission budget", o)
	}
}

// Every provider loaded reports a load time, one optional fact each. The
// maximum providers, the reference facts and every fixed fact still have to
// fit the collector's budget, or the whole batch is refused.
func TestMihomoLoadedProviderTimesFitObservationBudget(t *testing.T) {
	doc := referenceDocument(time.Now().UTC(), []string{"edge-a"})
	loaded := map[string]any{"edge-local": map[string]any{"proxies": []map[string]string{{"name": "edge-a"}}, "updatedAt": testControllerUpdated}}
	fixed := map[string][]string{}
	for i := 0; i < 15; i++ {
		name := fmt.Sprintf("fixed-%02d", i)
		fixed[name] = []string{"fixed-node"}
		loaded[name] = map[string]any{"proxies": []map[string]string{{"name": "fixed-node"}}, "updatedAt": testControllerUpdated}
	}
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string {
		t.Error("read the harness controller instead of the loaded one")
		return nil
	})
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configs":
			fmt.Fprint(w, `{"mode":"rule","tun":{"enable":true}}`)
		case "/proxies":
			fmt.Fprint(w, `{"proxies":{"root":{"type":"Selector","now":"fixed-node","history":[{"time":"2026-10-17T06:00:00Z","delay":5}]},"fixed-node":{"type":"VLESS"}}}`)
		case "/providers/proxies":
			json.NewEncoder(w).Encode(map[string]any{"providers": loaded})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer controller.Close()
	x.Target = controller.URL
	x.BindIP = "127.0.0.1"
	x.Adapter.ExpectedProviders = fixed
	o := runReference(x)
	if o.Outcome != "ok" {
		t.Fatal(o)
	}
	if code := config.ObservationBounds(o, time.Second); code != "" {
		t.Fatalf("%s with %d facts: a full set of loaded providers would refuse the batch", code, len(o.Facts))
	}
	if o.Facts["provider_updated_at.edge-local"] != "" {
		t.Fatal("credential-bearing controller exported load time")
	}
}

func TestMihomoSettleReloadReevaluatesWorkerEvidence(t *testing.T) {
	for _, tc := range []struct {
		name              string
		worker, collector int
		age               time.Duration
		fixedPolicy       bool
		confirmed         bool
	}{
		{"enable window", 0, 120, 20 * time.Second, false, false},
		{"extend window", 10, 120, 20 * time.Second, false, false},
		{"shorten window", 120, 10, 20 * time.Second, false, true},
		{"disable window", 120, 0, 20 * time.Second, false, true},
		{"fixed violation", 0, 120, 20 * time.Second, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := referenceDocument(time.Now().UTC().Add(-tc.age), []string{"new"})
			x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, doc) }, func() []string { return []string{"old"} })
			x.IntervalSeconds, x.StaleSeconds, x.FailureThreshold = 10, 90, 2
			x.Adapter.ProviderReference.SettleSeconds = &tc.worker
			if tc.fixedPolicy {
				x.Adapter.ExpectedProviders["fixed"] = []string{"changed"}
			}
			o := runReference(x)
			wantPending := "true"
			if tc.fixedPolicy {
				wantPending = "false"
			}
			if o.Outcome != "policy" || o.Facts["provider_reference_load_pending"] != wantPending {
				t.Fatal("worker lost window-independent load evidence", o)
			}
			rows := []model.Observation{o, o}
			rows[0].SampledAt = o.SampledAt.Add(-10 * time.Second)
			before := config.Contract(testkit.Config(), x)
			x.Adapter.ProviderReference.SettleSeconds = &tc.collector
			if config.Contract(testkit.Config(), x) != before {
				t.Fatal("threshold reload invalidated worker history")
			}
			if got := engine.Evaluate(x, rows, o.SampledAt); got.PolicyConfirmed != tc.confirmed {
				t.Fatalf("collector used worker's window: %+v", got)
			}
		})
	}
}

func TestMihomoPublicReferenceReceivesNoControllerToken(t *testing.T) {
	doc := referenceDocument(time.Now().UTC(), []string{"edge-a"})
	var reads atomic.Int32
	publisher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("controller credential was forwarded to a public publisher")
		}
		fmt.Fprint(w, doc)
	}))
	defer publisher.Close()
	x := referenceHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("wrong publisher")
	}, func() []string { return []string{"edge-a"} })
	x.Adapter.ProviderReference.URL = publisher.URL
	x.Adapter.ProviderReference.TokenEnv = ""
	if o := runReference(x); o.Outcome != "ok" || reads.Load() != 2 {
		t.Fatal(o, reads.Load())
	}
}
