package monitor

import (
	"encoding/json"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Current is the reader-side freshness policy. A producer cannot update its
// file after a crash, so every file consumer must apply deadlines locally.
func Current(p Panel, now time.Time) Panel {
	p = clone(p)
	if p.Schema != 1 {
		p.Lifecycle = "unavailable"
	}
	if p.Lifecycle == "running" && (!now.Before(p.PublisherExpiresAt) || p.GeneratedAt.After(now.Add(5*time.Minute)) || p.PublisherExpiresAt.After(now.Add(5*time.Minute))) {
		p.Lifecycle = "publisher_expired"
	}
	for i := range p.Rows {
		r := p.Rows[i]
		if p.Lifecycle != "running" {
			p.Rows[i] = missing(r, p.Lifecycle)
		} else if r.SampledAt.After(now.Add(5*time.Minute)) || r.ValidUntil.After(now.Add(5*time.Minute)) {
			p.Rows[i] = missing(r, "clock_skew")
		} else if r.Value != nil && !now.Before(r.ValidUntil) {
			r.Freshness = "stale"
			p.Rows[i] = missing(r, "sample_expired")
		} else if r.Metric == "server.card" && r.Value != nil {
			card, ok := r.Value.(ServerCard)
			if !ok {
				// File readers decode interface values as JSON maps. Decode an
				// owned typed card before applying the same nested policy.
				raw, err := json.Marshal(r.Value)
				if err != nil || json.Unmarshal(raw, &card) != nil {
					p.Rows[i] = missing(r, "invalid_value")
					continue
				}
			}
			ageCard(&card, now)
			p.Rows[i].Value = card
		} else if r.Metric == "servers.list" && r.Value != nil {
			servers, ok := r.Value.([]ServerListEntry)
			if !ok {
				raw, err := json.Marshal(r.Value)
				if err != nil || json.Unmarshal(raw, &servers) != nil {
					p.Rows[i] = missing(r, "invalid_value")
					continue
				}
			}
			for i := range servers {
				if m := servers[i].Metrics; m != nil && (!now.Before(m.ValidUntil) || m.ValidUntil.After(now.Add(5*time.Minute)) || m.SampledAt.After(now.Add(5*time.Minute))) {
					servers[i].Metrics = nil
				}
			}
			p.Rows[i].Value = servers
		} else if r.Metric == "link.card" && r.Value != nil {
			card, ok := r.Value.(LinkCard)
			if !ok {
				raw, err := json.Marshal(r.Value)
				if err != nil || json.Unmarshal(raw, &card) != nil {
					p.Rows[i] = missing(r, "invalid_value")
					continue
				}
			}
			for i := range card.Checks {
				ageSeenCheck(&card.Checks[i].SeenCheckValue, now)
			}
			p.Rows[i].Value = card
		} else if r.Metric == "check.card" && r.Value != nil {
			card, ok := r.Value.(CheckCard)
			if !ok {
				raw, err := json.Marshal(r.Value)
				if err != nil || json.Unmarshal(raw, &card) != nil {
					p.Rows[i] = missing(r, "invalid_value")
					continue
				}
			}
			ageSeenCheck(&card.SeenCheckValue, now)
			p.Rows[i].Value = card
		} else if r.Metric == "service.card" && r.Value != nil {
			card, ok := r.Value.(ServiceCard)
			if !ok {
				raw, err := json.Marshal(r.Value)
				if err != nil || json.Unmarshal(raw, &card) != nil {
					p.Rows[i] = missing(r, "invalid_value")
					continue
				}
			}
			ageServiceCard(&card, now)
			p.Rows[i].Value = card
		}
	}
	return p
}

// A service card's checks expire one by one, each at its own deadline,
// which a renewed lease never moves; the card stays. Its assessment rests
// on all of its evidence, so it is withheld whole at its earliest deadline.
func ageServiceCard(card *ServiceCard, now time.Time) {
	for i := range card.SeenFrom {
		for k := range card.SeenFrom[i].Checks {
			x := &card.SeenFrom[i].Checks[k]
			ageSeenCheck(x, now)
		}
	}
	ageAssessment(card.Assessment, now)
}

// An expired assessment keeps its identities and hints, which the topology
// decides, and loses every judgment: its state, its availability, path and
// worker words, and each piece of evidence's health and worker.
func ageAssessment(a *AssessmentValue, now time.Time) {
	if a == nil || a.ValidUntil == nil {
		return
	}
	skew := a.ValidUntil.After(now.Add(5 * time.Minute))
	if now.Before(*a.ValidUntil) && !skew {
		return
	}
	a.State, a.Availability, a.Reachability, a.WorkerOnline = "unknown", "unknown", "unknown", "unknown"
	freshness := "stale"
	if skew {
		freshness = "unavailable"
	}
	for i := range a.Evidence {
		e := &a.Evidence[i]
		e.Health, e.Freshness, e.Worker = "unknown", freshness, "unknown"
	}
}

// Resource readings and nested observer verdicts expire independently of the
// node and service judgments. Their original clocks never move on lease renewal.
func ageCard(card *ServerCard, now time.Time) {
	for i := range card.Resources {
		r := &card.Resources[i]
		skew := r.SampledAt != nil && r.SampledAt.After(now.Add(5*time.Minute)) || r.ValidUntil != nil && r.ValidUntil.After(now.Add(5*time.Minute))
		if r.Freshness == "fresh" && r.ValidUntil != nil && now.Before(*r.ValidUntil) && !skew {
			continue
		}
		r.CPU.Percent, r.CPU.Limit, r.CPU.Logical = nil, nil, nil
		r.Memory.Percent, r.Memory.Limit, r.Memory.Used, r.Memory.Total = nil, nil, nil, nil
		r.Disk.Percent, r.Disk.Limit, r.Disk.Used, r.Disk.Total = nil, nil, nil, nil
		r.ResourceMetrics = model.ResourceMetrics{}
		if r.Process != nil {
			r.Process.Matching = nil
		}
		r.Health = "unknown"
		if skew {
			r.Freshness = "unavailable"
		} else if r.Freshness == "fresh" {
			r.Freshness = "stale"
		}
	}
	for i := range card.Observes {
		o := &card.Observes[i]
		if o.ValidUntil == nil {
			// Older frames used the row's deadline.
			continue
		}
		if now.Before(*o.ValidUntil) && !o.ValidUntil.After(now.Add(5*time.Minute)) && (o.LastSeen == nil || !o.LastSeen.After(now.Add(5*time.Minute))) {
			continue
		}
		o.Health, o.State, o.Live = "unknown", "cached", false
		o.Counts.Unknown = o.Counts.Total()
		o.Counts.Up, o.Counts.Warn, o.Counts.Down = 0, 0, 0
	}
}

func ageSeenCheck(x *SeenCheckValue, now time.Time) {
	skew := x.SampledAt != nil && x.SampledAt.After(now.Add(5*time.Minute)) || x.ValidUntil != nil && x.ValidUntil.After(now.Add(5*time.Minute))
	if x.ValidUntil != nil && now.Before(*x.ValidUntil) && !skew {
		return
	}
	x.Health, x.Latency, x.Up24h, x.Colo, x.Samples.Slots = "unknown", nil, nil, nil, nil
	if skew {
		x.Freshness = "unavailable"
	} else {
		x.Freshness = "stale"
	}
	x.Diagnostic = nil
}
