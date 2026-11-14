package view_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// productsView is the products example half an hour into scenario.
func productsView(t *testing.T, scenario string) view.View {
	t.Helper()
	c, err := config.Load(filepath.Join("..", "..", "examples", "products.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 13, 30, 0, 0, time.UTC)
	sim := preview.New(c, now, "en")
	if scenario != "" {
		i := slices.Index(sim.ScenarioIDs(), scenario)
		if i < 0 {
			t.Fatalf("no %s scenario: %v", scenario, sim.ScenarioIDs())
		}
		sim.SetScenario(i, now.Add(-30*time.Minute))
	}
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	return view.View{Language: "en", Snapshot: snap, Now: now}
}

func findLine(t *testing.T, b view.ProductBoard, id string) (view.Product, view.ProductLine) {
	t.Helper()
	for _, p := range b.Products {
		for _, l := range p.Lines {
			if l.ID == id {
				return p, l
			}
		}
	}
	t.Fatalf("no line %s", id)
	return view.Product{}, view.ProductLine{}
}

// A product's lines are its services nothing else in it depends on: a
// dependency in another product, or of itself, hides no line, and a
// service in no application is in no product.
func TestProductRootsAreWhatNothingElseInTheProductRestsOn(t *testing.T) {
	c := model.Config{Services: []model.Service{
		{ID: "api", Application: "a", DependsOn: []string{"db", "api"}},
		{ID: "db", Application: "a"},
		{ID: "site", Application: "a"},
		{ID: "worker", Application: "b", DependsOn: []string{"db", "queue"}},
		{ID: "queue", Application: "b", Environment: "staging"},
		{ID: "loose"},
	}}
	if got, want := view.ProductRoots(c), []string{"api", "site", "worker", "queue"}; !slices.Equal(got, want) {
		t.Fatalf("roots %v, want %v", got, want)
	}
}

func TestProductLineNameDropsOnlyTheProductsWord(t *testing.T) {
	for _, c := range [][3]string{
		{"Habitly", "Habitly API", "API"},
		{"Habitly", "habitly · web", "web"},
		{"Habitly", "Habitly", "Habitly"},
		{"Habitly", "Habitly ·", "Habitly ·"},
		{"Habitly", "Landing page", "Landing page"},
		{"Habit", "Habitly API", "Habitly API"},
		{"", "API", "API"},
		{"東京", "東京 API", "API"},
	} {
		if got := view.ProductLineName(c[0], c[1]); got != c[2] {
			t.Errorf("ProductLineName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// A share up is over measured time, never rounded up, and says how much
// of the window was measured when it was not all of it.
func TestUptimeIsNeverRoundedUp(t *testing.T) {
	day := int64(86400)
	list := view.Uptimes([]model.Availability{
		{Window: "24h", Rollup: model.Rollup{Seconds: day, Up: day - 1, Down: 1}},
		{Window: "7d", Rollup: model.Rollup{Seconds: 7 * day, Up: 3 * day, Unmeasured: 4 * day}},
		{Window: "30d", Rollup: model.Rollup{Seconds: 30 * day, Up: day, Down: day, Unreconciled: 1}},
	})
	if len(list) != 3 {
		t.Fatalf("%+v", list)
	}
	if got := view.UptimeShare(list[0]); got != "99.99%" {
		t.Errorf("a second down in a day reads %q", got)
	}
	if got := view.UptimeShare(list[1]); got != "100% · 3d" {
		t.Errorf("three measured days of seven read %q", got)
	}
	if list[2].Up != nil || view.UptimeShare(list[2]) != "" {
		t.Errorf("unreconciled totals give a share: %+v", list[2])
	}
	if u := view.LongestUptime(list); u == nil || u.Window != "7d" {
		t.Errorf("the longest window with a share is not 7d: %+v", u)
	}
}

// The products example as Products mode reads it: one product per
// application, a line per part a user reaches, each chain in request order.
func TestProductsReadTheExample(t *testing.T) {
	b := productsView(t, "").Products()
	ids := []string{}
	for _, p := range b.Products {
		ids = append(ids, p.ID+":"+p.Kind)
	}
	if want := []string{"habitly:ios", "snapmeal:ios", "driftwood:web", "notebook:web"}; !slices.Equal(ids, want) {
		t.Fatalf("products %v, want %v", ids, want)
	}
	if b.Counts.Up != 4 || b.Incidents != 0 || b.Ungrouped != 0 {
		t.Fatalf("a calm example counts %+v, %d incidents, %d ungrouped", b.Counts, b.Incidents, b.Ungrouped)
	}
	_, api := findLine(t, b, "snapmeal-api")
	roles := []string{}
	for _, l := range api.Chain {
		roles = append(roles, l.Role+":"+l.ID)
	}
	if want := []string{"edge:cf", "host:app", "dependency:snapmeal-db"}; !slices.Equal(roles, want) {
		t.Fatalf("Snapmeal API rests on %v, want %v", roles, want)
	}
	if api.Lead == nil || api.Lead.Scope != "user_entry" || view.LineState(api) != "" || api.NeedsLook() {
		t.Fatalf("a calm line leads with its user entry check and asks for nothing: %+v", api.Lead)
	}
	_, worker := findLine(t, b, "habitly-api")
	if worker.Chain[0].Role != "platform" || view.LayerLabel(worker.Chain[0]) != "Cloudflare workers" {
		t.Fatalf("the Worker does not run on Cloudflare's platform: %+v", worker.Chain)
	}
}

// The origin down behind Cloudflare: the line fails and leads with the
// failing check, the edge in its chain stays up, the server it runs on is
// unknown by its own state, never affected, and the incident on the server
// both Snapmeal and Notebook rest on counts once.
func TestProductsOriginDownKeepsEachLayersOwnState(t *testing.T) {
	v := productsView(t, "app-origin-down")
	b := v.Products()
	p, api := findLine(t, b, "snapmeal-api")
	if api.Lead == nil || api.Lead.Health != "down" || api.Lead.Since.IsZero() {
		t.Fatalf("Snapmeal API does not lead with its failing check: %+v", api.Lead)
	}
	if view.LineState(api) != "ACCESS FAIL" || !api.NeedsLook() {
		t.Fatalf("Snapmeal API says %q", view.LineState(api))
	}
	health := map[string]string{}
	for _, l := range api.Chain {
		health[l.ID] = l.Health
	}
	if health["cf"] != "up" || health["app"] != "unknown" {
		t.Fatalf("the chain judges its layers: %v", health)
	}
	// The server is affected, which the chain leaves out: a server several
	// products share is affected by any of them.
	for _, n := range v.Snapshot.Nodes {
		if n.ID == "app" && (!n.Affected || n.Health != health["app"]) {
			t.Fatalf("the server's own state is %s, affected %v; its layer says %s", n.Health, n.Affected, health["app"])
		}
	}
	k := view.ProductLook(v.Now, p, api)
	if k.Since == "" || k.Says != "HTTP 521 via Cloudflare LIS" || k.Seen != "seen from Lisbon" || k.Incidents != "⚑ 1 incident" {
		t.Fatalf("the look says %+v", k)
	}
	sum := 0
	for _, p := range b.Products {
		sum += p.Incidents
	}
	if b.Incidents != 1 || sum != 2 {
		t.Fatalf("one incident on a shared server counts %d on the board, %d by product", b.Incidents, sum)
	}
}

// A lead says the status that answered only where the status was the
// failure, and that it came via the edge, not from it.
func TestLeadSaysTheStatusOnlyWhereItFailed(t *testing.T) {
	lead := func(code string, facts map[string]string) view.LeadCheck {
		return view.LeadCheck{SeenCheck: view.SeenCheck{Facts: facts}, Outcome: "fail", Stage: "http_assert", Code: code}
	}
	edge := map[string]string{"http_status": "521", "edge_provider": "cloudflare", "edge_pop": "LIS"}
	if got := view.LeadSays(lead("unexpected_status", edge)); got != "HTTP 521 via Cloudflare LIS" {
		t.Errorf("an unexpected status says %q", got)
	}
	if got := view.LeadSays(lead("unexpected_status", map[string]string{"http_status": "503"})); got != "HTTP 503" {
		t.Errorf("a status with no edge in front says %q", got)
	}
	if got := view.LeadSays(lead("body_mismatch", map[string]string{"http_status": "200", "edge_provider": "cloudflare"})); got != "http assert: body mismatch" {
		t.Errorf("a body that did not match says %q", got)
	}
}

// A certificate is inside its window as the probe judges it: expiring
// within min_days, so with fewer whole days left.
func TestCertificateWindowIsTheProbes(t *testing.T) {
	for _, c := range []struct {
		days, min int
		inside    bool
	}{{13, 14, true}, {14, 14, false}, {-1, 14, true}, {3, 0, false}} {
		if got := (view.Certificate{Days: c.days, MinDays: c.min}).InsideWindow(); got != c.inside {
			t.Errorf("%d days left, window %d: inside %v", c.days, c.min, got)
		}
	}
}

func TestProductsCertificateIsTheSoonest(t *testing.T) {
	b := productsView(t, "certificate-expiring").Products()
	found := false
	for _, p := range b.Products {
		c := p.Certificate
		if c == nil {
			t.Errorf("%s reads no certificate", p.ID)
			continue
		}
		if c.Days == 9 {
			found = true
			if c.MinDays != 14 || c.NotAfter.IsZero() || c.Host == "" || !c.InsideWindow() {
				t.Errorf("%s: %+v", p.ID, c)
			}
		}
	}
	if !found {
		t.Fatal("no product reads the certificate nine days from expiry")
	}
}

// A cached view knows nothing current: every product is unknown, its
// certificate and incidents unread.
func TestProductsInACachedView(t *testing.T) {
	v := productsView(t, "app-origin-down")
	v.Cached = true
	b := v.Products()
	if b.Counts.Unknown != len(b.Products) || b.Incidents != 0 {
		t.Fatalf("a cached board counts %+v, %d incidents", b.Counts, b.Incidents)
	}
	for _, p := range b.Products {
		if p.Health != "unknown" || p.Note != "cached" || p.Certificate != nil || p.Incidents != 0 {
			t.Errorf("%s in a cached view: %+v", p.ID, p)
		}
	}
}
