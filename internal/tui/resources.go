package tui

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// A host's local resources: on its card, a panel each for CPU, memory and
// disk that fills as far as it is used, its top the last few minutes; in
// its evidence, a chart of each on the samples' time axis with its limit
// drawn across. Readings that are not current are drawn faint and their
// values withheld.

// metric is one of the resources charted, with its limit.
type metric struct {
	label, name string
	of          func(usage) float64
	limit       float64
}

func metrics(h model.HostOptions) []metric {
	return []metric{
		{"CPU", "CPU", func(u usage) float64 { return u.cpu }, h.MaxCPUPercent},
		{"MEM", "MEMORY", func(u usage) float64 { return u.memory }, h.MaxMemoryPercent},
		{"DISK", "DISK", func(u usage) float64 { return u.disk }, h.MaxDiskPercent},
	}
}

// hostCheck is a check's declaration and its host options.
func (m *Model) hostCheck(id string) (model.Check, model.HostOptions) {
	for _, x := range m.Snapshot.Topology.Checks {
		if x.ID == id {
			return x, view.ResourceOptions(x)
		}
	}
	return model.Check{ID: id, IntervalSeconds: 60}, model.HostOptions{}
}

// capacities say how much there is of each resource: CPUs, then memory
// and disk as used of total.
func capacities(f map[string]string) [3]string {
	out := [3]string{}
	if n, e := strconv.Atoi(f["cpu_logical_count"]); e == nil && n > 0 {
		out[0] = plural(n, "CPU")
	}
	for i, k := range []string{"memory", "disk"} {
		used, e1 := strconv.ParseUint(f[k+"_used_bytes"], 10, 64)
		total, e2 := strconv.ParseUint(f[k+"_total_bytes"], 10, 64)
		if e1 == nil && e2 == nil && total > 0 && used <= total {
			out[i+1] = capacity(used, total)
		}
	}
	return out
}

// processes says how many processes run, and of the one named, how many
// and whether that is what its check asks: "caddy ×1", "180 processes".
func processes(f map[string]string, h model.HostOptions) (named []span, total string) {
	if n, e := strconv.Atoi(f["process_count"]); e == nil {
		total = itoa(n) + " processes"
		if n == 1 {
			total = "1 process"
		}
		if z, e := strconv.Atoi(f["process_zombie_count"]); e == nil && z > 0 {
			total += " · " + plural(z, "zombie")
		}
	}
	name := f["process_name"]
	n, e := strconv.Atoi(f["process_matching_count"])
	if name == "" || e != nil {
		return nil, total
	}
	g, c := "● ", cUp
	if n < h.MinProcesses || h.MaxProcesses > 0 && n > h.MaxProcesses {
		g, c = "▲ ", cWarn
	}
	named = []span{{g, fg(c)}, {name, fg(cText)}, {" ×" + itoa(n), fg(c)}}
	switch {
	case n < h.MinProcesses:
		named = append(named, span{" · want " + itoa(h.MinProcesses), fg(cWarn)})
	case c == cWarn:
		named = append(named, span{" · at most " + itoa(h.MaxProcesses), fg(cWarn)})
	}
	return named, total
}

// resourceRows are the rows of a card's local resources, w wide: under
// the observer's name when there are several, its resource panels, then
// the processes.
func (m *Model) resourceRows(rs []view.CardResource, w int) []cardRow {
	rows := []cardRow{}
	for _, r := range rs {
		_, h := m.hostCheck(r.Check)
		hit := "goto:check:" + r.Check
		if len(rs) > 1 {
			// Several checks read this host, say of different disks: each
			// under its observer, disk and how old its reading is.
			sub := cardRow{left: []span{{"◎ ", fg(healthColor(r.Health))}, {r.Observer.Name, fg(cMuted)}}, hit: hit}
			if h.DiskPath != "" {
				sub.left = append(sub.left, span{" · " + h.DiskPath, fg(cFaint)})
			}
			sub.right = m.resourceAside([]view.CardResource{r})
			rows = append(rows, sub)
		}
		panel := m.resourcePanel(r, w)
		if r.Facts == nil {
			// Nothing to say of how much there is or what runs.
			rows = append(rows, panel[:3]...)
			continue
		}
		rows = append(rows, panel...)
		if cells := readingCells(r.Facts, panelWidths(w), panelGap); cells != nil {
			rows = append(rows, cardRow{left: cells, hit: hit})
		}
		if !r.External {
			named, total := processes(r.Facts, h)
			if named == nil {
				named, total = []span{{total, fg(cMuted)}}, ""
			}
			rows = append(rows, cardRow{left: named, right: []span{{total, fg(cFaint)}}, hit: hit})
		}
	}
	return rows
}

// A host may also report how fast its network moves, how much swap it
// uses and its load averages. A card says each under the panel it belongs
// with, the load under the CPU, the swap under the memory and the network
// under the disk; an evidence panel or an open card says each in full, on
// a line of its own under the charts.

// shortBytes writes a byte count as a card has room for it: "512", "305K",
// "1.7M", "12G".
func shortBytes(v float64) string {
	units := []string{"", "K", "M", "G", "T", "P"}
	u := 0
	for v >= 1000 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if v < 10 && u > 0 {
		return strconv.FormatFloat(v, 'f', 1, 64) + units[u]
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64) + units[u]
}

// fullRate writes bytes a second with their unit: "305.4 KiB/s".
func fullRate(v float64) string {
	units := []string{"B/s", "KiB/s", "MiB/s", "GiB/s", "TiB/s", "PiB/s"}
	u := 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[u]
}

// swapShare is the share of swap in use, "" when either reading is
// missing and "none" when the host has none.
func swapShare(s model.SwapReport) string {
	switch {
	case s.TotalBytes != nil && *s.TotalBytes == 0:
		return "none"
	case s.TotalBytes == nil || s.UsedBytes == nil:
		return ""
	}
	return pct(float64(*s.UsedBytes)/float64(*s.TotalBytes)*100, 0)
}

// loads writes the averages it has, "—" for one missing.
func loads(l model.LoadReport) []string {
	out := []string{}
	for _, v := range []*float64{l.Avg1m, l.Avg5m, l.Avg15m} {
		if v == nil {
			out = append(out, "—")
			continue
		}
		out = append(out, strconv.FormatFloat(*v, 'f', 2, 64))
	}
	return out
}

// readingCells is a card's row for the other readings, the panels' widths
// in ns: the load under the CPU, the swap under the memory, the network
// under the disk, each cell blank where its host reports nothing. nil when
// it reports none of them.
func readingCells(facts map[string]string, ns [3]int, gap int) []span {
	r := model.ResourceMetricsFromFacts(facts)
	if r.Network == (model.NetworkReport{}) && r.Swap == (model.SwapReport{}) && r.Load == (model.LoadReport{}) {
		return nil
	}
	label, value := fg(cFaint), fg(Mix(cMuted, cText, .3))
	cell := func(n int, left, right []span) []span {
		pad := n - spansWidth(left) - spansWidth(right)
		if pad < 1 {
			return []span{{strings.Repeat(" ", n), Style{}}}
		}
		return append(append(left, span{strings.Repeat(" ", pad), Style{}}), right...)
	}
	out := []span{}
	if r.Load.Avg1m != nil {
		out = append(out, cell(ns[0], []span{{"load", label}}, []span{{loads(r.Load)[0], value}})...)
	} else {
		out = append(out, span{strings.Repeat(" ", ns[0]), Style{}})
	}
	out = append(out, span{strings.Repeat(" ", gap), Style{}})
	if share := swapShare(r.Swap); share != "" {
		out = append(out, cell(ns[1], []span{{"swap", label}}, []span{{share, value}})...)
	} else {
		out = append(out, span{strings.Repeat(" ", ns[1]), Style{}})
	}
	out = append(out, span{strings.Repeat(" ", gap), Style{}})
	if rx, tx := r.Network.RXBytesPerSecond, r.Network.TXBytesPerSecond; rx != nil || tx != nil {
		side := func(arrow string, v *float64) []span {
			if v == nil {
				return []span{{arrow, label}, {"—", label}}
			}
			return []span{{arrow, label}, {shortBytes(*v), value}}
		}
		out = append(out, cell(ns[2], side("↓", rx), side("↑", tx))...)
	}
	return out
}

// readingLines are the other readings in full, each a label in the style
// of the charts' names (style) padded to n, then its values.
func readingLines(facts map[string]string, n int, style Style) []vline {
	r := model.ResourceMetricsFromFacts(facts)
	value, quiet := fg(cText), fg(cMuted)
	out := []vline{}
	if rx, tx := r.Network.RXBytesPerSecond, r.Network.TXBytesPerSecond; rx != nil || tx != nil {
		rate := func(v *float64) string {
			if v == nil {
				return "—"
			}
			return fullRate(*v)
		}
		out = append(out, vline{{fit("NETWORK", n), style}, {"↓ ", quiet}, {rate(rx), value}, {"   ↑ ", quiet}, {rate(tx), value}})
	}
	if r.Swap != (model.SwapReport{}) {
		line := vline{{fit("SWAP", n), style}}
		if share := swapShare(r.Swap); share != "" && share != "none" {
			line = append(line, span{share, value.bold()}, span{"  ", Style{}})
		}
		switch {
		case r.Swap.TotalBytes != nil && *r.Swap.TotalBytes == 0:
			line = append(line, span{"none configured", quiet})
		case r.Swap.TotalBytes != nil && r.Swap.UsedBytes != nil:
			line = append(line, span{capacity(*r.Swap.UsedBytes, *r.Swap.TotalBytes), quiet})
		case r.Swap.TotalBytes != nil:
			line = append(line, span{"— of " + capacity(*r.Swap.TotalBytes, *r.Swap.TotalBytes), quiet})
		}
		out = append(out, line)
	}
	if r.Load != (model.LoadReport{}) {
		l := loads(r.Load)
		out = append(out, vline{{fit("LOAD", n), style}, {l[0], value.bold()}, {"  " + l[1] + "  " + l[2], value}, {"   1 · 5 · 15 min", fg(cFaint)}})
	}
	return out
}

func resourceTitle(rs []view.CardResource) string {
	if len(rs) == 1 && rs[0].External {
		return "REPORTED RESOURCES"
	}
	return "LOCAL RESOURCES"
}

// resourcePanel is four rows across w with a panel each for CPU, memory
// and disk: the reading now over a chart of the last minutes that fills
// as far as the resource is used, and how much there is under it. Without
// a current reading, when the last one was read stands under them.
func (m *Model) resourcePanel(r view.CardResource, w int) []cardRow {
	const gap = panelGap
	ns := panelWidths(w)
	pw, extra := ns[0], ns[2]-ns[0]
	well := Mix(cInk, cLine, .35)
	x, h := m.hostCheck(r.Check)
	_, use := usageSlots(x, m.trendSamples(r.Check), m.Now, pw+extra)
	current := r.Facts != nil
	caps := capacities(r.Facts)
	now := usageOf(model.Observation{Evidence: "reported", Outcome: "ok", Facts: r.Facts})
	panel := make([]cardRow, 4)
	for i, mt := range metrics(h) {
		n := pw
		if i == 2 {
			n += extra // the last panel takes what does not divide
		}
		if i > 0 {
			for k := range panel {
				panel[k].left = append(panel[k].left, span{strings.Repeat(" ", gap), Style{}})
			}
		}
		v := mt.of(now)
		value, vst := "—", fg(cFaint)
		if current && !math.IsNaN(v) {
			value, vst = pct(v, 0), fg(loadColor(v, mt.limit))
			if mt.limit > 0 && v > mt.limit {
				vst = vst.bold()
			}
		}
		panel[0].left = append(panel[0].left, span{fit(mt.label, n-width(value)), fg(cMuted)}, span{value, vst})
		vals := make([]float64, len(use))
		for k, u := range use {
			vals[k] = mt.of(u)
		}
		for k, line := range tank(padNaN(vals, n), 2, mt.limit, current, well) {
			panel[1+k].left = append(panel[1+k].left, line...)
		}
		panel[3].left = append(panel[3].left, span{fit(caps[i], n), fg(cFaint)})
	}
	if !current {
		panel[3].left = []span{{fit(m.lastRead(r), w), fg(cWarn)}}
	}
	for k := range panel {
		panel[k].hit = "goto:check:" + r.Check
	}
	return panel
}

// panelGap is the room between a card's resource panels.
const panelGap = 2

// panelWidths are the widths of a card's three resource panels across w,
// the last taking what does not divide.
func panelWidths(w int) [3]int {
	pw := (w - 2*panelGap) / 3
	return [3]int{pw, pw, w - 2*panelGap - 2*pw}
}

// resourceAside is what a card's resources heading says on the right: how
// old the reading is, or when the last one was read.
func (m *Model) resourceAside(rs []view.CardResource) []span {
	if len(rs) != 1 {
		return nil
	}
	r := rs[0]
	if r.Facts != nil {
		return []span{{age(m.Now, r.SampledAt), fg(cFaint)}}
	}
	return []span{{m.lastRead(r), fg(cWarn)}}
}

// lastRead says why a host check has no current reading and when it last
// had one, from its kept samples where the snapshot holds none.
func (m *Model) lastRead(r view.CardResource) string {
	at := r.SampledAt
	if s := m.trendSamples(r.Check); len(s) > 0 && s[len(s)-1].SampledAt.After(at) {
		at = s[len(s)-1].SampledAt
	}
	why := r.Freshness
	if r.External && r.Reason != "" {
		why = r.Reason
	} else {
		switch {
		case why == "fresh":
			why = "no reading"
		case why == "never" && !at.IsZero():
			why = "stale"
		}
	}
	if at.IsZero() {
		return why
	}
	return why + " · read " + age(m.Now, at)
}

// chartRows is how tall each resource's chart is in an evidence panel.
const chartRows = 4

// resourceCharts draw a host check's CPU, memory and disk over use, one
// slot a column, each under its reading now, with a scale on the right
// and the time axis below; facts are its newest sample's when current.
func (m *Model) resourceCharts(h model.HostOptions, use []usage, facts map[string]string, spanned time.Duration) []vline {
	current := facts != nil
	now := usageOf(model.Observation{Evidence: "reported", Outcome: "ok", Facts: facts})
	caps := capacities(facts)
	n := len(use)
	out := []vline{}
	for i, mt := range metrics(h) {
		vals := make([]float64, n)
		for k, u := range use {
			vals[k] = mt.of(u)
		}
		v := mt.of(now)
		head := vline{{fit(mt.name, 8), fg(Mix(cAccent, cMuted, .35)).bold()}}
		if current && !math.IsNaN(v) {
			head = append(head, span{pct(v, 1), fg(loadColor(v, mt.limit)).bold()})
			if caps[i] != "" {
				head = append(head, span{"  " + caps[i], fg(cMuted)})
			}
		} else {
			head = append(head, span{"no current reading", fg(cFaint).italic()})
		}
		if peak := maxOf(vals); !math.IsNaN(peak) {
			head = append(head, span{"  ·  peak " + pct(peak, 1), fg(cFaint)})
		}
		out = append(out, head)
		lim := -1
		if mt.limit > 0 && mt.limit < 100 {
			lim = int(math.Round(mt.limit / 100 * float64(4*chartRows)))
		}
		for row, line := range areaChart(vals, chartRows, mt.limit, current) {
			foot := (chartRows - 1 - row) * 4
			scale := ""
			st := fg(cFaint)
			switch {
			case lim > foot && lim <= foot+4:
				scale, st = "─ "+pct(mt.limit, 0)+" limit", fg(Mix(cWarn, cInk, .3))
			case row == 0:
				scale = "  100%"
			case row == chartRows-1:
				scale = "  0%"
			}
			out = append(out, append(line, span{" " + scale, st}))
		}
		out = append(out, vline{})
	}
	// The time axis: when the oldest slot was, and now.
	if n >= 16 {
		ago := view.Duration(spanned) + " ago"
		out = append(out[:len(out)-1], vline{{ago + strings.Repeat(" ", max(1, n-width(ago)-3)) + "now", fg(cFaint)}}, vline{})
	}
	return out
}

// maxOf is the largest reading, NaN with none.
func maxOf(vals []float64) float64 {
	peak := math.NaN()
	for _, v := range vals {
		if !math.IsNaN(v) && (math.IsNaN(peak) || v > peak) {
			peak = v
		}
	}
	return peak
}

// checkCharts are a host check's charts in its evidence, on the strip's
// time axis so each column sits under its slot.
func (m *Model) checkCharts(x model.Check, s view.Strip, entries []model.Observation) []vline {
	h := model.HostOptions{}
	if x.Host != nil {
		h = *x.Host
	}
	_, use := usageSlots(x, entries, m.Now, len(s.Slots))
	r, _ := m.view().HostResource(x.ID)
	// The axis starts where the strip does, at its oldest sample.
	spanned := time.Duration(len(s.Slots)*s.Interval) * time.Second
	if i := slices.IndexFunc(s.Slots, func(sl view.Slot) bool { return !sl.SampledAt.IsZero() }); i >= 0 {
		spanned = m.Now.Sub(s.Slots[i].SampledAt)
	}
	out := m.resourceCharts(h, use, r.Facts, spanned)
	return append(out, processLine(r.Facts, h), vline{})
}

// nodeResources are a node's local resources in its evidence, w wide: the
// observer and when it read them, a chart of each over the samples kept,
// then the processes.
func (m *Model) nodeResources(r view.CardResource, w int) []vline {
	x, h := m.hostCheck(r.Check)
	title := "LOCAL RESOURCES"
	if r.External {
		title = "REPORTED RESOURCES"
	}
	head := vline{{title, fg(Mix(cAccent, cMuted, .35)).bold()}, {"  " + r.Observer.Name + " · " + r.Check, fg(cFaint)}}
	out := []vline{head}
	if r.Facts == nil {
		out = append(out, vline{{m.lastRead(r), fg(cWarn)}})
	} else {
		out = append(out, vline{{"read " + age(m.Now, r.SampledAt), fg(cMuted)}})
	}
	out = append(out, vline{})
	// The charts keep their width while samples arrive, newest at the right.
	n := max(16, w-13)
	_, use := usageSlots(x, m.trendSamples(r.Check), m.Now, n)
	pad := make([]usage, n-len(use))
	for i := range pad {
		pad[i] = noUsage
	}
	use = append(pad, use...)
	out = append(out, m.resourceCharts(h, use, r.Facts, time.Duration(n*max(1, x.IntervalSeconds))*time.Second)...)
	if more := readingLines(r.Facts, 8, fg(Mix(cAccent, cMuted, .35)).bold()); len(more) > 0 {
		out = append(append(out, more...), vline{})
	}
	if r.External {
		return out
	}
	return append(out, processLine(r.Facts, h))
}

// processLine says in an evidence panel what runs, and of the process
// named, whether as many run as its check asks.
func processLine(facts map[string]string, h model.HostOptions) vline {
	named, total := processes(facts, h)
	line := vline{{fit("PROCESSES", 10), fg(Mix(cAccent, cMuted, .35)).bold()}}
	switch {
	case facts == nil:
		return append(line, span{"no current reading", fg(cFaint).italic()})
	case named != nil:
		return append(append(line, named...), span{"  ·  " + total, fg(cMuted)})
	}
	return append(line, span{total, fg(cText)})
}
