package view

import (
	"maps"
	"math"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// CardResource is one resource report selected for a node. Native host
// checks fill Facts exactly as before; external reports fill the same keys
// from their normalized, unspliced payload. The extra fields identify the
// source and the deadlines a reader must apply before showing numbers.
type CardResource struct {
	Origin    *model.Origin     `json:"origin,omitempty"`
	Check     string            `json:"check"`
	Observer  Ref               `json:"observer"`
	Health    string            `json:"health"`
	Freshness string            `json:"freshness"`
	SampledAt time.Time         `json:"sampled_at,omitzero"`
	Facts     map[string]string `json:"facts,omitempty"`

	Native          bool                  `json:"native,omitempty"`
	External        bool                  `json:"external,omitempty"`
	Selected        bool                  `json:"selected,omitempty"`
	Source          string                `json:"source,omitempty"`
	Object          string                `json:"object,omitempty"`
	TimeBasis       string                `json:"time_basis,omitempty"`
	DiskPath        string                `json:"disk_path,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	Current         bool                  `json:"current,omitempty"`
	DataDeadline    time.Time             `json:"data_deadline,omitzero"`
	ContactDeadline time.Time             `json:"contact_deadline,omitzero"`
	ValidUntil      time.Time             `json:"valid_until,omitzero"`
	Report          *model.ExternalReport `json:"report,omitempty"`
}

// NodeResources is the unified selection for one node. Selected is always
// the first candidate to display. All preserves the native-only multi-check
// behavior; explicit source selection and external resources show only the
// selected report so a reader cannot accidentally stitch sources.
type NodeResources struct {
	Node     string         `json:"node"`
	Selected CardResource   `json:"selected"`
	All      []CardResource `json:"all,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}

// ResourceOptions derives display limits from either a native host check or
// an external binding's declared assertions. It never turns missing values
// into tests.
func ResourceOptions(x model.Check) model.HostOptions {
	h := model.HostOptions{}
	if x.Host != nil {
		h = *x.Host
	}
	if x.External != nil && x.External.Assertions != nil {
		a := x.External.Assertions
		if a.CPUPercentMax != nil {
			h.MaxCPUPercent = *a.CPUPercentMax
		}
		if a.MemoryPercentMax != nil {
			h.MaxMemoryPercent = *a.MemoryPercentMax
		}
		if a.DiskPercentMax != nil {
			h.MaxDiskPercent = *a.DiskPercentMax
		}
	}
	return h
}

type resourceCandidate struct {
	card   CardResource
	usable bool
}

// SelectResources is the one place TUI, CLI and monitor choose a node's
// resources. It never averages or splices sources.
func (v View) SelectResources() map[string]NodeResources {
	byNode := map[string][]resourceCandidate{}
	byCheck := map[string]resourceCandidate{}
	checks := map[string]model.Check{}
	for _, x := range v.Snapshot.Topology.Checks {
		checks[x.ID] = x
	}
	observers := map[string]model.Observer{}
	for _, o := range v.Snapshot.Topology.Observers {
		observers[o.ID] = o
	}
	states := map[string]model.CheckState{}
	for _, st := range v.Snapshot.Checks {
		states[st.ID] = st
	}
	for _, x := range v.Snapshot.Topology.Checks {
		if x.Kind != "host" {
			continue
		}
		o := observers[x.Observer]
		if o.Node == "" {
			continue
		}
		st := states[x.ID]
		r := v.hostResource(st, x, o)
		r.Native, r.Source, r.DiskPath = true, "native", nativeDiskPath(x)
		r.Reason = "native host reading"
		if !r.SampledAt.IsZero() {
			r.ValidUntil = resourceDeadline(r.SampledAt, time.Duration(x.StaleSeconds)*time.Second, v.Snapshot.GeneratedAt)
			r.DataDeadline = r.SampledAt.Add(time.Duration(x.StaleSeconds) * time.Second)
		}
		r.Current = r.Facts != nil && r.Freshness == "fresh" && !v.Cached && !Expired(v.Snapshot, v.Now) && (r.ValidUntil.IsZero() || v.Now.Before(r.ValidUntil))
		c := resourceCandidate{card: r, usable: r.Current && r.Facts != nil}
		byNode[o.Node] = append(byNode[o.Node], c)
		byCheck[x.ID] = c
	}
	for _, e := range v.Snapshot.External {
		if e.ReportType != model.ReportTypeResource {
			continue
		}
		x, ok := checks[e.Check]
		if !ok || x.External == nil || x.Kind != "external" {
			continue
		}
		o := observers[x.Observer]
		r := v.externalResource(e, x, o)
		c := resourceCandidate{card: r, usable: externalUsable(v, e)}
		byNode[e.Node] = append(byNode[e.Node], c)
		byCheck[e.Check] = c
	}
	out := map[string]NodeResources{}
	for _, n := range v.Snapshot.Topology.Nodes {
		cands := byNode[n.ID]
		if len(cands) == 0 {
			continue
		}
		var selected resourceCandidate
		var all []CardResource
		reason := ""
		if len(n.ResourceChecks) > 0 {
			ordered := []resourceCandidate{}
			for _, id := range n.ResourceChecks {
				if c, ok := byCheck[id]; ok {
					ordered = append(ordered, c)
				}
			}
			if len(ordered) > 0 {
				selected, reason = choose(ordered)
				selected.card.Selected = true
				all = []CardResource{selected.card}
			}
		} else if hasExternal(cands) {
			// Config validation refuses this ambiguity. Keep a deterministic
			// first-candidate fallback for snapshots loaded from elsewhere.
			selected = cands[0]
			for _, c := range cands {
				if c.usable {
					selected = c
					break
				}
			}
			selected.card.Selected = true
			all = []CardResource{selected.card}
			reason = "first resource source in topology order"
			if len(cands) > 1 {
				reason = "ambiguous resource sources; " + reason
			}
		} else {
			// Preserve the native-only behavior: show every host check on the
			// node, with the first current one as the primary display. The
			// selected report is first so a consumer that reads resources[0]
			// sees the same choice as SelectResources.
			selected = cands[0]
			for _, c := range cands {
				if c.usable {
					selected = c
					break
				}
			}
			reason = "native host default"
			all = make([]CardResource, 0, len(cands))
			selected.card.Selected = true
			all = append(all, selected.card)
			for _, c := range cands {
				if c.card.Check == selected.card.Check {
					continue
				}
				all = append(all, c.card)
			}
		}
		if selected.card.Check != "" {
			out[n.ID] = NodeResources{Node: n.ID, Selected: selected.card, All: all, Reason: reason}
		}
	}
	return out
}

func hasExternal(cands []resourceCandidate) bool {
	for _, c := range cands {
		if c.card.External {
			return true
		}
	}
	return false
}

func choose(ordered []resourceCandidate) (resourceCandidate, string) {
	for i, c := range ordered {
		if !c.usable {
			continue
		}
		if i == 0 {
			return c, "primary resource check " + c.card.Check
		}
		return c, "fallback resource check " + c.card.Check + " after " + ordered[0].card.Check + " unavailable or expired"
	}
	primary := ordered[0]
	if primary.card.Freshness == "" {
		primary.card.Freshness = "never"
	}
	primary.card.Current = false
	primary.card.Health = "unknown"
	primary.card.Facts = nil
	if primary.card.Reason == "" {
		primary.card.Reason = "no current report from ordered resource checks"
	}
	return primary, "no current report from ordered resource checks; showing " + primary.card.Check + " as unknown"
}

func (v View) externalResource(e model.ExternalBindingView, x model.Check, o model.Observer) CardResource {
	name := o.Name
	if name == "" {
		name = o.ID
	}
	r := CardResource{Origin: v.ResourceOrigin(e.Source, e.Object), Check: e.Check, Observer: Ref{ID: o.ID, Name: name}, Health: e.Health, Freshness: e.Freshness, SampledAt: e.SampledAt, External: true, Source: e.Source, Object: e.Object, TimeBasis: e.TimeBasis, Reason: e.Reason, DataDeadline: e.DataDeadline, ContactDeadline: e.ContactDeadline, ValidUntil: e.ValidUntil, Report: e.Report}
	if r.ValidUntil.IsZero() {
		r.ValidUntil = minTime(deadlineOrZero(r.DataDeadline), deadlineOrZero(r.ContactDeadline), v.Snapshot.GeneratedAt.Add(model.SnapshotLifetime))
	}
	if e.Report != nil && e.Report.Resource != nil {
		r.Facts, r.DiskPath = externalFacts(x, e.Report)
	}
	current := externalUsable(v, e)
	r.Current = current
	if !current {
		r.Health = "unknown"
		r.Facts = nil
		if r.Freshness == "fresh" {
			r.Freshness = "stale"
		}
		r.Reason = "external resource report is not current"
	}
	if r.DiskPath == "" && x.External != nil && x.External.PrimaryDisk != "" {
		if !diskPresent(e.Report, x.External.PrimaryDisk) {
			r.Reason = appendReason(r.Reason, "primary disk "+x.External.PrimaryDisk+" absent; disk unknowns stay unknown")
		}
	}
	return r
}

func appendReason(reason, add string) string {
	if reason == "" {
		return add
	}
	return reason + "; " + add
}

func externalUsable(v View, e model.ExternalBindingView) bool {
	if v.Cached || v.Draft || Expired(v.Snapshot, v.Now) || e.Report == nil {
		return false
	}
	if e.Freshness != "fresh" {
		return false
	}
	return e.ValidUntil.IsZero() || v.Now.Before(e.ValidUntil)
}

func diskPresent(r *model.ExternalReport, id string) bool {
	if r == nil || r.Resource == nil {
		return false
	}
	for _, d := range r.Resource.Disks {
		if d.ID == id {
			return true
		}
	}
	return false
}

// externalFacts converts one selected external payload into the existing
// compact fact vocabulary. Missing fields are omitted; no cross-source value
// fills them.
func externalFacts(x model.Check, r *model.ExternalReport) (map[string]string, string) {
	facts := map[string]string{"resource_scope": "external_reported"}
	res := r.Resource
	res.ResourceMetrics.SetFacts(facts)
	if res.CPU != nil {
		if res.CPU.Percent != nil {
			facts["cpu_used_percent"] = strconv.FormatFloat(*res.CPU.Percent, 'f', 2, 64)
		}
		if res.CPU.LogicalCores != nil {
			facts["cpu_logical_count"] = strconv.Itoa(*res.CPU.LogicalCores)
		}
		if res.CPU.SampleWindowMS != nil {
			facts["cpu_sample_ms"] = strconv.FormatInt(*res.CPU.SampleWindowMS, 10)
		}
		if res.CPU.Basis != "" {
			facts["cpu_basis"] = res.CPU.Basis
		}
	}
	if res.Memory != nil {
		if res.Memory.TotalBytes != nil {
			facts["memory_total_bytes"] = strconv.FormatUint(*res.Memory.TotalBytes, 10)
		}
		if res.Memory.UsedBytes != nil {
			facts["memory_used_bytes"] = strconv.FormatUint(*res.Memory.UsedBytes, 10)
		}
		if res.Memory.AvailableBytes != nil {
			facts["memory_available_bytes"] = strconv.FormatUint(*res.Memory.AvailableBytes, 10)
		}
		if res.Memory.Percent != nil {
			facts["memory_used_percent"] = strconv.FormatFloat(*res.Memory.Percent, 'f', 2, 64)
		}
		if res.Memory.Basis != "" {
			facts["memory_basis"] = res.Memory.Basis
		}
	}
	diskPath := ""
	if x.External != nil && x.External.PrimaryDisk != "" {
		for _, d := range res.Disks {
			if d.ID != x.External.PrimaryDisk {
				continue
			}
			diskPath = d.Path
			if diskPath == "" {
				diskPath = d.ID
			}
			facts["disk_path"] = diskPath
			if d.TotalBytes != nil {
				facts["disk_total_bytes"] = strconv.FormatUint(*d.TotalBytes, 10)
			}
			if d.UsedBytes != nil {
				facts["disk_used_bytes"] = strconv.FormatUint(*d.UsedBytes, 10)
			}
			if d.FreeBytes != nil {
				facts["disk_available_bytes"] = strconv.FormatUint(*d.FreeBytes, 10)
			}
			if d.Percent != nil {
				facts["disk_used_percent"] = strconv.FormatFloat(*d.Percent, 'f', 2, 64)
			}
			if d.Basis != "" {
				facts["disk_basis"] = d.Basis
			}
			break
		}
	}
	if len(facts) == 1 {
		return nil, diskPath
	}
	return facts, diskPath
}

func nativeDiskPath(x model.Check) string {
	if x.Host != nil && x.Host.DiskPath != "" {
		return x.Host.DiskPath
	}
	return "/"
}

func resourceDeadline(sampled time.Time, stale time.Duration, generated time.Time) time.Time {
	a, b := sampled.Add(stale), generated.Add(model.SnapshotLifetime)
	if b.Before(a) {
		return b
	}
	return a
}

func deadlineOrZero(t time.Time) time.Time { return t }
func minTime(a, b, c time.Time) time.Time {
	out := time.Time{}
	for _, t := range []time.Time{a, b, c} {
		if t.IsZero() {
			continue
		}
		if out.IsZero() || t.Before(out) {
			out = t
		}
	}
	return out
}

// ResourceCards flattens the unified selection into what existing cards and
// CLI details display. It is the only caller-side shape: no TUI or monitor
// filters checks by kind or observer node again.
func (v View) ResourceCards() map[string][]CardResource { return resourceCards(v.SelectResources()) }

func resourceCards(selection map[string]NodeResources) map[string][]CardResource {
	out := map[string][]CardResource{}
	for node, n := range selection {
		if len(n.All) > 0 {
			out[node] = n.All
		} else {
			out[node] = []CardResource{n.Selected}
		}
	}
	return out
}

// HostResources is the pre-external native projection, retained for callers
// that explicitly ask for native host telemetry. New generic display uses
// SelectResources or ResourceCards.
func (v View) HostResources() map[string][]CardResource {
	out := map[string][]CardResource{}
	observers := map[string]model.Observer{}
	checks := map[string]model.Check{}
	for _, o := range v.Snapshot.Topology.Observers {
		observers[o.ID] = o
	}
	for _, x := range v.Snapshot.Topology.Checks {
		if x.Kind == "host" {
			checks[x.ID] = x
		}
	}
	for _, cs := range v.Snapshot.Checks {
		x, ok := checks[cs.ID]
		o := observers[x.Observer]
		if !ok || o.Node == "" {
			continue
		}
		out[o.Node] = append(out[o.Node], v.hostResource(cs, x, o))
	}
	return out
}

// HostResource is a host check's local telemetry, whether or not its
// observer is on a node. An external resource check returns its selected
// report; other kinds return false.
func (v View) HostResource(id string) (CardResource, bool) {
	for _, x := range v.Snapshot.Topology.Checks {
		if x.ID != id {
			continue
		}
		if x.Kind == "external" {
			for _, e := range v.Snapshot.External {
				if e.Check == id && e.ReportType == model.ReportTypeResource {
					for _, o := range v.Snapshot.Topology.Observers {
						if o.ID == x.Observer {
							return v.externalResource(e, x, o), true
						}
					}
				}
			}
			return CardResource{}, false
		}
		if x.Kind != "host" {
			continue
		}
		for _, cs := range v.Snapshot.Checks {
			if cs.ID == id {
				for _, o := range v.Snapshot.Topology.Observers {
					if o.ID == x.Observer {
						return v.hostResource(cs, x, o), true
					}
				}
				return v.hostResource(cs, x, model.Observer{ID: x.Observer}), true
			}
		}
	}
	return CardResource{}, false
}

func (v View) hostResource(cs model.CheckState, x model.Check, o model.Observer) CardResource {
	name := o.Name
	if name == "" {
		name = o.ID
	}
	r := CardResource{Check: cs.ID, Observer: Ref{o.ID, name}, Health: cs.Health, Freshness: cs.Freshness, DiskPath: nativeDiskPath(x), Native: true, Source: "native"}
	if cs.Last != nil {
		r.SampledAt = cs.Last.SampledAt
	}
	current := !v.Cached && !v.Draft && !Expired(v.Snapshot, v.Now) && cs.Freshness == "fresh" && cs.Last != nil && !cs.Last.SampledAt.After(v.Now.Add(model.SampleClockAhead)) && v.Now.Sub(cs.Last.SampledAt) <= time.Duration(x.StaleSeconds)*time.Second
	if current && (cs.Last.Outcome == "ok" || cs.Last.Outcome == "policy") && cs.Last.Evidence == "reported" {
		r.Facts = maps.Clone(cs.Last.Facts)
	} else {
		r.Health = "unknown"
	}
	r.Current = r.Facts != nil && r.Health != "unknown"
	if v.Cached || Expired(v.Snapshot, v.Now) {
		r.Freshness = "cached"
	}
	return r
}

// ResourceValues are the same compact summaries on a card and in CLI output.
func ResourceValues(r CardResource) [][2]string {
	f := r.Facts
	percent := func(key string) string {
		n, err := strconv.ParseFloat(f[key], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return "unknown"
		}
		return strconv.FormatFloat(n, 'f', 1, 64) + "%"
	}
	capacity := func(prefix string) string {
		if f == nil {
			return "unknown"
		}
		p := percent(prefix + "_used_percent")
		used, e1 := strconv.ParseUint(f[prefix+"_used_bytes"], 10, 64)
		total, e2 := strconv.ParseUint(f[prefix+"_total_bytes"], 10, 64)
		if p == "unknown" || e1 != nil || e2 != nil || total == 0 || used > total {
			return "unknown"
		}
		return p + " · " + resourceBytes(used) + "/" + resourceBytes(total)
	}
	values := [][2]string{{"CPU", percent("cpu_used_percent")}, {"Memory", capacity("memory")}, {"Disk", capacity("disk")}}
	if !r.External {
		processes := "unknown"
		if n, err := strconv.ParseUint(f["process_count"], 10, 64); err == nil {
			processes = strconv.FormatUint(n, 10) + " total"
			if n, err := strconv.ParseUint(f["process_matching_count"], 10, 64); err == nil && f["process_name"] != "" {
				processes += " · " + f["process_name"] + " " + strconv.FormatUint(n, 10)
			}
		}
		values = append(values, [2]string{"Processes", processes})
	}
	return append(values, ResourceMetricValues(f)...)
}

// ResourceMetricValues formats only the optional groups present in a sample.
func ResourceMetricValues(facts map[string]string) [][2]string {
	m := model.ResourceMetricsFromFacts(facts)
	var values [][2]string
	if m.Network != (model.NetworkReport{}) {
		values = append(values, [2]string{"Network", "↓ " + resourceRate(m.Network.RXBytesPerSecond) + " · ↑ " + resourceRate(m.Network.TXBytesPerSecond)})
	}
	if m.Swap != (model.SwapReport{}) {
		used, total := "—", "—"
		if m.Swap.UsedBytes != nil {
			used = resourceBytes(*m.Swap.UsedBytes)
		}
		if m.Swap.TotalBytes != nil {
			total = resourceBytes(*m.Swap.TotalBytes)
		}
		values = append(values, [2]string{"Swap", used + " / " + total})
	}
	if m.Load != (model.LoadReport{}) {
		load := func(value *float64) string {
			if value == nil {
				return "—"
			}
			return strconv.FormatFloat(*value, 'f', 2, 64)
		}
		values = append(values, [2]string{"Load", "1/5/15m  " + load(m.Load.Avg1m) + " / " + load(m.Load.Avg5m) + " / " + load(m.Load.Avg15m)})
	}
	return values
}

func resourceRate(rate *float64) string {
	if rate == nil {
		return "—"
	}
	n := *rate
	units := [...]string{"B/s", "KiB/s", "MiB/s", "GiB/s", "TiB/s", "PiB/s", "EiB/s"}
	unit := 0
	for n >= 1024 && unit < len(units)-1 {
		n /= 1024
		unit++
	}
	return strconv.FormatFloat(n, 'f', 1, 64) + units[unit]
}

func resourceBytes(n uint64) string {
	if n >= 1<<30 {
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + "GiB"
	}
	if n >= 1<<20 {
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + "MiB"
	}
	return strconv.FormatUint(n, 10) + "B"
}
