package app

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestDoctorEnrollmentAndRedaction(t *testing.T) {
	c := DemoConfig()
	st, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Observers: observerIDs(c)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token := strings.Repeat("v", 64)
	backend := api.New(c, "private-collector-name", st, api.Auth{Schema: 1, Tokens: []api.Principal{{TokenSHA256: secure.Hash(token), Role: "viewer"}}})
	srv := httptest.NewServer(backend.Handler())
	defer srv.Close()
	args := []string{"doctor", "--json", "--credentials", filepath.Join(t.TempDir(), "missing"), "--server", srv.URL, "--project", c.Project, "--credential-file", credentialFile(t, token)}
	out, _, err := invoke("", args...)
	if exitCode(err) != 3 {
		t.Fatal(err, out)
	}
	var r DoctorReport
	if json.Unmarshal([]byte(out), &r) != nil || r.Code != "waiting_for_samples" {
		t.Fatal(out)
	}
	now := time.Now().UTC().Add(2 * time.Second)
	backend.Now = func() time.Time { return now }
	if err := st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "api-health", 1, now.Add(-time.Second), "fail")}}); err != nil {
		t.Fatal(err)
	}
	out, _, err = invoke("", args...)
	if err != nil {
		t.Fatal(err, out)
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.State != "samples_received" || r.Current != 1 {
		t.Fatal(out)
	}
	for _, private := range []string{token, srv.URL, "private-collector-name", "api-health"} {
		if strings.Contains(out, private) {
			t.Fatal("private infrastructure escaped", private)
		}
	}
}
func TestDoctorMissingConfigurationIsActionable(t *testing.T) {
	out, _, err := invoke("", "doctor", "--json", "--credentials", filepath.Join(t.TempDir(), "missing"))
	if exitCode(err) != 1 || !strings.Contains(out, "configuration_unavailable") || !strings.Contains(out, "next_actions") {
		t.Fatal(out, err)
	}
	if _, _, err := invoke("", "doctor", "--wait", "10m"); err == nil {
		t.Fatal("unbounded wait")
	}
}
