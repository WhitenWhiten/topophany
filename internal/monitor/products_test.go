package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func productsTemplate() Template {
	t := Template{Schema: 1, Title: "Products", Rows: []Row{
		{ID: "status", Metric: "summary.verdict", Visual: "status"},
		{ID: "products", Metric: "products.list", Visual: "list"},
	}}
	t.Bar.Row = "status"
	return t
}

// builderSim is the products example, in a scenario that began half an hour
// before now.
func builderSim(t *testing.T, scenario string, now time.Time) *preview.Sim {
	t.Helper()
	c, err := config.Load(filepath.Join("..", "..", "examples", "products.json"))
	if err != nil {
		t.Fatal(err)
	}
	sim := preview.New(c, now, "en")
	if scenario != "" {
		i := slices.Index(sim.ScenarioIDs(), scenario)
		if i < 0 {
			t.Fatalf("the products example offers no %s scenario: %v", scenario, sim.ScenarioIDs())
		}
		sim.SetScenario(i, now.Add(-30*time.Minute))
	}
	return sim
}

// productsPanel builds the products row from sim as a stream would, with
// every check's history read.
func productsPanel(t *testing.T, sim *preview.Sim, now time.Time) (Panel, ProductsValue) {
	t.Helper()
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Name: "stream", Source: "preview", Template: productsTemplate()}
	book := NewSampleBook(cfg.Template)
	samples := book.Fill(context.Background(), snap, "", -1, func(_ context.Context, id string) ([]model.Observation, error) {
		return sim.History(id, CardSamples, now)
	})
	p := Build(cfg, Input{Snapshot: snap, Samples: samples}, now)
	row := p.Rows[1]
	v, ok := row.Value.(ProductsValue)
	if !ok {
		t.Fatalf("products row has no products: %+v", row)
	}
	return p, v
}

func product(t *testing.T, v ProductsValue, id string) ProductValue {
	t.Helper()
	for _, p := range v.Products {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no product %s", id)
	return ProductValue{}
}

func line(t *testing.T, p ProductValue, id string) ProductLineValue {
	t.Helper()
	for _, l := range p.Lines {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("%s has no line %s", p.ID, id)
	return ProductLineValue{}
}

func roles(l ProductLineValue) []string {
	out := []string{}
	for _, c := range l.Chain {
		out = append(out, c.Role+":"+c.ID)
	}
	return out
}

// Each application is a product, its lines the services nothing else in it
// depends on, each resting on what a request meets on its way.
func TestProductsReadTheBuilderExample(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p, v := productsPanel(t, builderSim(t, "", now), now)
	ids := []string{}
	for _, pr := range v.Products {
		ids = append(ids, pr.ID+"/"+pr.Kind)
	}
	if !slices.Equal(ids, []string{"habitly/ios", "snapmeal/ios", "driftwood/web", "notebook/web"}) || v.Ungrouped != 0 {
		t.Fatal(ids, v.Ungrouped)
	}
	if p.Rows[1].Text != "4 products" || p.Rows[1].ValidUntil.IsZero() || !now.Before(p.Rows[1].ValidUntil) {
		t.Fatalf("%+v", p.Rows[1])
	}
	habitly := product(t, v, "habitly")
	if len(habitly.Lines) != 2 || habitly.Lines[0].ID != "habitly-api" || habitly.Lines[1].ID != "habitly-site" {
		t.Fatalf("a dependency is not a line of its own: %+v", habitly.Lines)
	}
	api := line(t, habitly, "habitly-api")
	if got := roles(api); !slices.Equal(got, []string{"platform:cf", "dependency:habitly-db"}) || api.Chain[0].Runtime != "workers" || api.Chain[1].Provider != "Supabase" {
		t.Fatalf("a Worker rests on its edge and its database: %v %+v", got, api.Chain)
	}
	snapmeal := line(t, product(t, v, "snapmeal"), "snapmeal-api")
	if got := roles(snapmeal); !slices.Equal(got, []string{"edge:cf", "host:app", "dependency:snapmeal-db"}) {
		t.Fatalf("a proxied API rests on the edge, its machine and its database: %v", got)
	}
	// While all is well, the lead is what users meet, and it holds samples,
	// a budget, and its share up over every window.
	if api.Lead == nil || api.Lead.ID != "habitly-api-home" || api.Lead.Scope != "user_entry" || api.Lead.Health != "up" || api.Lead.Since != nil {
		t.Fatalf("%+v", api.Lead)
	}
	if api.Lead.BudgetMS == nil || *api.Lead.BudgetMS != 800 || api.Lead.Latency == nil || len(api.Lead.Samples.Slots) == 0 || len(api.Lead.Samples.Slots) > ProductSamples {
		t.Fatalf("%+v", api.Lead)
	}
	windows := []string{}
	for _, u := range api.Lead.Uptime {
		windows = append(windows, u.Window)
		if u.Up == nil || *u.Up < 0 || *u.Up > 100 || u.Measured > u.Seconds {
			t.Fatalf("%+v", u)
		}
	}
	if !slices.Equal(windows, view.UptimeWindows) {
		t.Fatal(windows)
	}
	// Its soonest certificate, read on its checks' current samples.
	c := habitly.Certificate
	if c == nil || c.Days == nil || *c.Days < 30 || c.MinDays == nil || *c.MinDays != 14 || c.Host == "" || c.ValidUntil == nil {
		t.Fatalf("%+v", c)
	}
	// The database with no line of its own has no certificate to read.
	if product(t, v, "notebook").Certificate == nil {
		t.Fatal("the blog's public check reads its certificate")
	}
	if size, _ := json.Marshal(v); len(size) > rowValueBudget("products.list") {
		t.Fatal("products exceed their budget", len(size))
	}
}

// The origin behind Cloudflare stops answering: the edge still answers, so
// it is up, the machine behind it is not proven, and the line follows the
// failing check from when it began. The products that never touch the
// machine stay as they were.
func TestProductsOriginDownBehindTheEdge(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	_, v := productsPanel(t, builderSim(t, "app-origin-down", now), now)
	snapmeal := product(t, v, "snapmeal")
	api := line(t, snapmeal, "snapmeal-api")
	if api.Health == "up" || api.Lead == nil || api.Lead.ID != "snapmeal-api-home" || api.Lead.Health != "down" || api.Lead.Since == nil || api.Lead.Since.After(now) {
		t.Fatalf("%+v %+v", api, api.Lead)
	}
	// It says what answered, for a consumer to word as the TUI does:
	// "HTTP 521 via Cloudflare LIS".
	if d := api.Lead.Diagnostic; d == nil || d.Facts["http_status"] != "521" || d.Facts["edge_provider"] != "cloudflare" || d.Facts["edge_pop"] != "LIS" {
		t.Fatalf("the lead's diagnostic does not say what answered: %+v", d)
	}
	healths := map[string]string{}
	for _, c := range api.Chain {
		healths[c.ID] = c.Health
	}
	if healths["cf"] != "up" || healths["app"] != "unknown" {
		t.Fatal("the edge answered and the machine behind it is not proven", healths)
	}
	if snapmeal.Health == "up" || snapmeal.Attention == 0 {
		t.Fatalf("%+v", snapmeal)
	}
	// The Worker does not fail with the origin: its check from home still
	// passes and leads its line. The server's observer went down with the
	// machine, so its own check has nothing current, and the service says so.
	worker := line(t, product(t, v, "habitly"), "habitly-api")
	if worker.Lead.ID != "habitly-api-home" || worker.Lead.Health != "up" || worker.Health != "degraded" || !strings.Contains(worker.Reason, "lack current evidence") {
		t.Fatalf("%+v %+v", worker, worker.Lead)
	}
	for _, c := range worker.Chain {
		if c.Role == "platform" && c.Health != "up" {
			t.Fatal("the edge it runs on is up", c)
		}
	}
}

// A certificate inside its check's warning window warns the line that
// reads it, while its requests still succeed.
func TestProductsCertificateInsideItsWindow(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	_, v := productsPanel(t, builderSim(t, "certificate-expiring", now), now)
	habitly := product(t, v, "habitly")
	c := habitly.Certificate
	if c == nil || c.Days == nil || *c.Days != 9 || c.Host != "api.habitly.example" {
		t.Fatalf("%+v", c)
	}
	api := line(t, habitly, "habitly-api")
	if api.Health != "degraded" || api.Lead.ID != "habitly-api-home" || api.Lead.Health != "degraded" || api.Lead.Diagnostic == nil || api.Lead.Diagnostic.Code != "certificate_expiring" {
		t.Fatalf("%+v %+v", api, api.Lead)
	}
}

// Each lead check keeps its own deadline, which a renewed lease never moves:
// past it, its readings, since and uptime go and its identity stays. The
// certificate goes with its own check.
func TestProductsAgeEachLeadAtItsOwnDeadline(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p, v := productsPanel(t, builderSim(t, "app-origin-down", now), now)
	api := line(t, product(t, v, "snapmeal"), "snapmeal-api").Lead
	if api.ValidUntil == nil {
		t.Fatal("a lead has its own deadline")
	}
	later := api.ValidUntil.Add(time.Second)
	p.Rows[1].ValidUntil = later.Add(time.Minute) // as if a renewal had kept the row
	p.PublisherExpiresAt = later.Add(time.Minute)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var file Panel
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	got := Current(file, later).Rows[1].Value.(ProductsValue)
	aged := line(t, product(t, got, "snapmeal"), "snapmeal-api").Lead
	if aged.ID != api.ID || aged.Health != "unknown" || aged.Freshness != "stale" || aged.Latency != nil || aged.Samples.Slots != nil || aged.Since != nil || aged.Uptime != nil {
		t.Fatalf("%+v", aged)
	}
	for _, pr := range got.Products {
		if c := pr.Certificate; c != nil && c.ValidUntil != nil && !later.Before(*c.ValidUntil) && (c.Days != nil || c.NotAfter != nil) {
			t.Fatalf("an expired certificate keeps its days: %+v", c)
		}
	}
}

// A row that asks for no history carries none, and the book reads the
// history of the checks product lines can lead with, never a machine's.
func TestProductsHistory(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	sim := builderSim(t, "", now)
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := productsTemplate()
	book := NewSampleBook(tmpl)
	unread := book.Unread(snap, "")
	if len(unread) == 0 || slices.Contains(unread, "app-host") || slices.Contains(unread, "habitly-db-home") {
		t.Fatal("history is read for line checks only", unread)
	}
	off := false
	tmpl.Rows[1].History = &off
	if err := tmpl.Validate("preview"); err != nil {
		t.Fatal(err)
	}
	if NewSampleBook(tmpl) != nil {
		t.Fatal("a row without history asks for none")
	}
	p := Build(Config{Source: "preview", Template: tmpl}, Input{Snapshot: snap, Samples: book.Fill(context.Background(), snap, "", -1, func(_ context.Context, id string) ([]model.Observation, error) {
		return sim.History(id, CardSamples, now)
	})}, now)
	for _, pr := range p.Rows[1].Value.(ProductsValue).Products {
		for _, l := range pr.Lines {
			if l.Lead != nil && l.Lead.Samples.Slots != nil {
				t.Fatal("history was not asked for", l.ID)
			}
		}
	}
}

// In every scenario of the built-in network the products are its
// application groups, fit their budget and stay readable.
func TestProductsInEveryPreviewScenario(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, name := range previewSim(t, "", now).ScenarioIDs() {
		t.Run(name, func(t *testing.T) {
			sim := previewSim(t, name, now)
			snap, err := sim.Snapshot("", now)
			if err != nil {
				return // Collector outage has no snapshot.
			}
			p := Build(Config{Source: "preview", Template: productsTemplate()}, Input{Snapshot: snap}, now)
			v, ok := p.Rows[1].Value.(ProductsValue)
			if !ok {
				t.Fatalf("%+v", p.Rows[1])
			}
			groups := view.View{Snapshot: snap, Now: now}.ApplicationGroups()
			if len(v.Products) != len(groups) {
				t.Fatal(len(v.Products), len(groups))
			}
			for i, pr := range v.Products {
				if pr.ID != groups[i].ID || pr.Health != groups[i].Health {
					t.Fatalf("a product says what its group says: %+v / %+v", pr, groups[i])
				}
			}
			if _, err := MarshalPanel(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TOPO_PRODUCT_FRAMES=DIR writes a stream frame of the products example in
// each scenario a consumer draws, for its fixtures.
func TestWriteProductFrames(t *testing.T) {
	dir := os.Getenv("TOPO_PRODUCT_FRAMES")
	if dir == "" {
		t.Skip("set TOPO_PRODUCT_FRAMES to a directory to write the frames")
	}
	now := time.Date(2026, 10, 18, 13, 30, 0, 0, time.UTC)
	for _, scenario := range []string{"normal", "app-origin-down", "edge-blocked", "certificate-expiring", "host-disk-full"} {
		p, _ := productsPanel(t, builderSim(t, scenario, now), now)
		p.Name, p.RequestID = "stream", "products-"+scenario
		p.PublisherExpiresAt = now.Add(30 * time.Second)
		raw, err := MarshalPanel(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "products-"+strings.ReplaceAll(scenario, "-", "_")+".ndjson"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
