package monitor

import (
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// The products, as Products mode draws them: products.list says each
// application as its owner ships it, a line for each service nothing else
// in it depends on, with what the line rests on and how its lead check
// fared. Every judgment is the view's; the projection only bounds it and
// gives each reading its clock.

// ProductSamples is how many slots of samples a product line's lead check
// shows: fewer than a service card's, since a line is one row.
const ProductSamples = 24

// ProductLinesMax bounds the lines one products.list value lists across all
// its products; past it, a product counts its lines in more_lines.
const ProductLinesMax = 64

// productHistoryChecks bounds the checks whose history a products row asks
// its source for.
const productHistoryChecks = 96

type ProductsValue struct {
	Products  []ProductValue `json:"products"`
	Counts    view.Counts    `json:"counts"`
	Incidents int            `json:"incidents"`
	Ungrouped int            `json:"ungrouped"`
}

type ProductValue struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Kind        string             `json:"kind"`
	Environment string             `json:"environment"`
	Health      string             `json:"health"`
	Note        string             `json:"note"`
	Attention   int                `json:"attention"`
	Unmonitored int                `json:"unmonitored"`
	Incidents   int                `json:"incidents"`
	Since       *time.Time         `json:"since"`
	Lines       []ProductLineValue `json:"lines"`
	MoreLines   int                `json:"more_lines"`
	Certificate *CertificateValue  `json:"certificate"`
}

type ProductLineValue struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Runtime       string       `json:"runtime"`
	Health        string       `json:"health"`
	Note          string       `json:"note"`
	Reason        string       `json:"reason"`
	Assessment    string       `json:"assessment"`
	UnknownReason string       `json:"unknown_reason,omitempty"`
	Monitored     bool         `json:"monitored"`
	Elsewhere     bool         `json:"elsewhere"`
	Checks        view.Counts  `json:"checks"`
	Chain         []view.Layer `json:"chain"`
	MoreLayers    int          `json:"more_layers"`
	Lead          *LeadValue   `json:"lead"`
}

// LeadValue is a line's lead check as a service card's check says it, with
// whose it is, where it measures from, since when it fails, its budget and
// its share up over each window. It keeps its own deadline: past it, its
// readings, since and uptime are withheld as a card's check's are.
type LeadValue struct {
	SeenCheckValue
	Observer RefValue      `json:"observer"`
	Label    string        `json:"label"`
	Scope    string        `json:"scope"`
	Since    *time.Time    `json:"since"`
	BudgetMS *int          `json:"budget_ms"`
	Uptime   []view.Uptime `json:"uptime"`
}

// CertificateValue is the soonest-expiring certificate a product's lines
// read, with the deadline of the check that read it. Past it, the days and
// the expiry are withheld and the check and host remain.
type CertificateValue struct {
	Check      string     `json:"check"`
	Host       string     `json:"host"`
	Days       *int       `json:"days_remaining"`
	NotAfter   *time.Time `json:"not_after"`
	MinDays    *int       `json:"min_days"`
	ValidUntil *time.Time `json:"valid_until"`
}

// products is the products.list value with the row's clocks: the newest
// sample and the earliest deadline of every judgment its products rest on,
// their services', their dependencies' and the nodes their lines rest on.
// Each lead check's and certificate's readings keep their own.
func (p *projection) products() (ProductsValue, time.Time, time.Time) {
	fleet := p.serverFleet()
	board := p.v.ProductsWith(fleet)
	out := ProductsValue{Products: []ProductValue{}, Counts: board.Counts, Incidents: board.Incidents, Ungrouped: board.Ungrouped}
	ids, seen := map[string]bool{}, map[string]bool{}
	lines := 0
	for _, pr := range board.Products {
		v := ProductValue{ID: pr.ID, Name: pr.Name, Kind: pr.Kind, Environment: pr.Environment, Health: pr.Health, Note: pr.Note, Attention: pr.Attention, Unmonitored: pr.Unmonitored,
			Incidents: pr.Incidents, Since: stamp(pr.Since), Lines: []ProductLineValue{}, MoreLines: pr.MoreLines}
		for _, c := range pr.Checks {
			ids[c] = true
		}
		for _, l := range pr.Lines {
			p.serviceEvidence(l.ID, ids, seen)
			for _, layer := range l.Chain {
				if layer.Role != "dependency" {
					for _, c := range p.nodeChecks[layer.ID] {
						ids[c] = true
					}
				}
			}
			if lines == ProductLinesMax {
				v.MoreLines++
				continue
			}
			lines++
			v.Lines = append(v.Lines, p.productLine(l))
		}
		if c := pr.Certificate; c != nil {
			days, minDays := c.Days, c.MinDays
			v.Certificate = &CertificateValue{Check: c.Check, Host: c.Host, Days: &days, NotAfter: stamp(c.NotAfter), MinDays: &minDays}
			if clock, ok := p.evidence().checks[c.Check]; ok {
				v.Certificate.ValidUntil = stamp(clock.until)
			}
			if minDays <= 0 {
				v.Certificate.MinDays = nil
			}
		}
		out.Products = append(out.Products, v)
	}
	ageProducts(&out, p.v.Now)
	sampled, until := p.evidence().times(ids, false)
	return out, sampled, until
}

func (p *projection) productLine(l view.ProductLine) ProductLineValue {
	out := ProductLineValue{ID: l.ID, Name: l.Name, Runtime: l.Runtime, Health: l.Health, Note: l.Note, Reason: l.Reason, Assessment: l.Assessment, UnknownReason: p.unknownReason(l.ID),
		Monitored: l.Monitored, Elsewhere: l.Elsewhere, Checks: l.Checks, Chain: l.Chain, MoreLayers: l.MoreLayers}
	if x := l.Lead; x != nil {
		lead := &LeadValue{SeenCheckValue: p.seenCheck(x.SeenCheck, p.samples[x.ID]), Observer: RefValue(x.Observer), Label: x.Label, Scope: x.Scope, Since: stamp(x.Since), Uptime: x.Uptime}
		if x.BudgetMS > 0 {
			budget := x.BudgetMS
			lead.BudgetMS = &budget
		}
		if slots := lead.Samples.Slots; len(slots) > ProductSamples {
			lead.Samples.Slots = slots[len(slots)-ProductSamples:]
		}
		out.Lead = lead
	}
	return out
}

// productChecks are the checks whose history a products row shows: those of
// every product line's service, bar the machine resource checks, which
// never lead a line.
func productChecks(s model.Snapshot) []string {
	roots := map[string]bool{}
	for _, id := range view.ProductRoots(s.Topology) {
		roots[id] = true
	}
	out := []string{}
	for _, x := range s.Topology.Checks {
		if roots[x.Service] && x.Kind != "host" {
			out = append(out, x.ID)
			if len(out) == productHistoryChecks {
				break
			}
		}
	}
	return out
}

// ageProducts withholds each lead check's readings at its own deadline,
// and each certificate's days at its check's. The products, their lines and
// their marks stay: they are the row's, bounded by its own deadline.
func ageProducts(v *ProductsValue, now time.Time) {
	for i := range v.Products {
		pr := &v.Products[i]
		for k := range pr.Lines {
			if lead := pr.Lines[k].Lead; lead != nil {
				ageLead(lead, now)
			}
		}
		if c := pr.Certificate; c != nil {
			skew := c.ValidUntil != nil && c.ValidUntil.After(now.Add(5*time.Minute))
			if c.ValidUntil == nil || !now.Before(*c.ValidUntil) || skew {
				c.Days, c.NotAfter, c.MinDays = nil, nil, nil
			}
		}
	}
}

func ageLead(x *LeadValue, now time.Time) {
	skew := x.SampledAt != nil && x.SampledAt.After(now.Add(5*time.Minute)) || x.ValidUntil != nil && x.ValidUntil.After(now.Add(5*time.Minute))
	if x.ValidUntil != nil && now.Before(*x.ValidUntil) && !skew {
		return
	}
	ageSeenCheck(&x.SeenCheckValue, now)
	x.Since, x.Uptime = nil, nil
}
