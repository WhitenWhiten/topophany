package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// topo show products prints the board Products mode draws: --json is the
// view's board in every scenario the products example offers, and the text
// says what each line in trouble says.
func TestShowProductsIsTheBoard(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "products.json")
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 13, 30, 0, 0, time.UTC)
	ids := preview.New(c, now, "en").ScenarioIDs()
	for sc, id := range ids {
		sim := preview.New(c, now, "en")
		if sc != 0 {
			sim.SetScenario(sc, now.Add(-30*time.Minute))
		}
		snap, unreachable := sim.Snapshot("", now)
		args := []string{"preview", "--config", path, "--lang", "en", "--scenario", fmt.Sprint(sc), "show", "products"}
		out, e := at(now, append(args, "--json")...)
		if unreachable != nil {
			if exitCode(e) != ExitUnreachable {
				t.Fatalf("%s: unreachable collector exits %d: %v", id, exitCode(e), e)
			}
			continue
		}
		if e != nil {
			t.Fatalf("%s: %v", id, e)
		}
		var got view.ProductBoard
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s: %v\n%s", id, err, out)
		}
		board := view.View{Language: "en", Snapshot: snap, Now: now, Cached: view.Expired(snap, now)}.Products()
		raw, _ := json.Marshal(board)
		var want view.ProductBoard
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: show products --json is not the view's board:\n%s", id, out)
		}
		text, e := at(now, args...)
		if e != nil {
			t.Fatalf("%s: %v", id, e)
		}
		for _, p := range board.Products {
			if !strings.Contains(text, p.Name) {
				t.Errorf("%s: the board does not name %s:\n%s", id, p.Name, text)
			}
			for _, l := range p.Lines {
				if l.NeedsLook() && !strings.Contains(text, l.Name+": "+view.LookWord(l)) {
					t.Errorf("%s: %s asks for a look the text does not give:\n%s", id, l.Name, text)
				}
			}
		}
		if id == "app-origin-down" {
			for _, want := range []string{"PRODUCTS · 4 products · 1 incident", "NEEDS A LOOK", "Snapmeal API: ACCESS FAIL · for ", "HTTP 521 from Cloudflare LIS", "Hetzner Falkenstein (unknown)", "CERTIFICATE"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s: the text lacks %q:\n%s", id, want, text)
				}
			}
		}
	}
	if !slices.Contains(ids, "app-origin-down") {
		t.Fatal("the products example offers no app-origin-down scenario", ids)
	}
	if _, e := at(now, "preview", "--config", path, "show", "products", "snapmeal"); e == nil || !strings.Contains(e.Error(), "take no ID") {
		t.Fatalf("show products takes an ID: %v", e)
	}
}
