package monitor

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestCompactAndHistoryRowsDoNotShareProjection(t *testing.T) {
	now := time.Now().UTC()
	sim := previewSim(t, "", now)
	snap, err := sim.Snapshot("", now)
	if err != nil {
		t.Fatal(err)
	}
	noHistory := false
	tpl := serviceTemplate("blog", "blog")
	tpl.Rows[1].History = &noHistory
	book := NewSampleBook(tpl)
	samples := map[string][]model.Observation{}
	for _, id := range book.Unread(snap, "preview") {
		samples[id], err = sim.History(id, CardSamples, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	p := Build(Config{Source: "preview", Template: tpl}, Input{Snapshot: snap, Samples: samples}, now)
	for i := 1; i <= 2; i++ {
		c := p.Rows[i].Value.(ServiceCard)
		for _, o := range c.SeenFrom {
			for _, check := range o.Checks {
				if (len(check.Samples.Slots) > 0) != (i == 2) {
					t.Fatal("history projection crossed row settings", i, check.ID)
				}
			}
		}
	}
	cloned := p.Clone()
	*cloned.Rows[1].History = true
	if *p.Rows[1].History {
		t.Fatal("history selection aliases clone")
	}
	tpl.Rows[2].History = &noHistory
	if NewSampleBook(tpl) != nil {
		t.Fatal("compact template retained history book")
	}
	tpl.Rows[0].History = &noHistory
	if tpl.Validate("client") == nil {
		t.Fatal("list accepted history option")
	}
}
