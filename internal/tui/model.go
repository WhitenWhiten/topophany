// Package tui is a terminal UI. In viewer mode it is remote-only, with no probe
// or persistence responsibilities; losing this client does not stop monitoring.
// In editor mode (topo edit) it edits a local reviewed topology file and never
// talks to a collector.
package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/session"
	"github.com/WhitenWhiten/topophany/internal/view"
)

// tabs are the view's tabs, then About.
var tabs = append(append([]string{}, view.Tabs...), "About")

const (
	tabOverview  = view.Overview
	tabTopology  = view.Topology
	tabServers   = view.Servers
	tabServices  = view.Services
	tabPaths     = view.Paths
	tabChecks    = view.Checks
	tabIncidents = view.Incidents
	tabObservers = view.Observers
	tabAbout     = view.Observers + 1
)

// Row is one selectable entry of a list (see view.Row).
type Row = view.Row
type Model struct {
	diagnosticEvents                        []session.Event
	Direct                                  bool   // process-local resource source, with no collector or history
	ReconnectHint                           string // optional browser connection guidance, supplied by its language catalog
	Snapshot                                model.Snapshot
	Server                                  string
	Error                                   string
	Loading                                 string   // a request in flight while nothing current is shown
	Fetching                                bool     // a request for a snapshot is in flight
	Collectors                              []string // the collectors the viewer asks, for the splash to name
	Project                                 string   // the project the viewer expects
	Observer                                string
	Tab, Selected, Offset                   int
	Width, Height                           int
	Application, Environment, ServiceFilter string
	GroupBy                                 map[int]string
	Expanded                                map[string]bool
	GroupsCollapsed                         bool
	PreferencesPath                         string
	PreferenceOverrides                     []string
	preferenceInitial                       [7]string
	AttentionOnly                           bool
	Search                                  string
	Searching                               bool
	Detail                                  bool
	NoColor                                 bool
	Colors                                  int // colorTrue or color256
	Now                                     time.Time
	MapMode                                 string // "", "virtual", "geo" or "network"; "" picks virtual when locations exist
	Zoom                                    int
	FullMap                                 bool      // the map alone fills the body where a tab has one (f)
	GeoLabels                               geoLabels // geo map: cards, locations, points (p)
	Uniform                                 bool      // Servers: a grid of uniform cards rather than the wall (s)
	ServiceCards                            bool      // Services: a card for each service rather than the list (s)
	NerdFont                                bool      // the terminal's font has the Nerd Fonts' logos, so providers wear them
	Edit                                    *Editor   // non-nil only for topo edit
	Preview                                 Preview   // non-nil only for topo preview
	Debug                                   bool      // performance/geometry overlay (D)
	Transparent                             bool      // keep the terminal background (TOPO_BACKGROUND=terminal)
	Hyperlinks                              bool      // write links as OSC 8 hyperlinks
	Cell                                    float64   // a cell's height over its width, as the terminal reports it; 0 when unknown
	perf                                    perf
	modal                                   *modal
	notice                                  string
	noticeErr                               bool
	noticeAt                                time.Time
	aboutAt                                 time.Time
	splashAt                                time.Time         // when the splash came up
	aboutSpin                               float64           // extra globe rotation from the mouse wheel
	cardsTop                                int               // the Servers cards' scroll, kept while a card lies open
	vcache                                  vCache            // the virtual map's wires, kept while its layout holds
	pcache                                  plateCache        // the geographic map's labels, kept while what they label holds
	samples                                 samples           // the selected check's recent samples, as last read
	trends                                  map[string]*trend // each host check's recent samples, for its charts
	absorbed                                time.Time         // when the snapshot last taken into trends was generated
	hits                                    []hit
	drawingModal                            bool
	frameRows                               *[]Row  // shared only while drawing one frame
	Device                                  *Device // non-nil when the TUI is laid out for a device's screen, simulated in the terminal (device.go)
	stage                                   *rect   // where the TUI draws on the canvas, modals centred on it; nil for all of it
	touch                                   touch   // gestures in flight and what the touch layouts keep between frames (touch.go)
}

// A hit is where a click or a tap does something: w cells from x on h rows
// from y. A row of a terminal list is one row tall; a touch target is as
// tall as a finger needs.
type hit struct {
	x, y, w, h int
	key        string
	modal      bool
}

func (m *Model) hit(x, y, w int, key string) {
	m.hitArea(x, y, w, 1, key)
}

// hitArea makes h rows from y a target, w cells from x.
func (m *Model) hitArea(x, y, w, h int, key string) {
	m.hits = append(m.hits, hit{x, y, w, h, key, m.drawingModal})
}
func (m *Model) st(s Style) Style { return s }

func (m *Model) Cached() bool {
	if m.Edit != nil {
		return false
	}
	return m.Error != "" || view.Expired(m.Snapshot, m.Now)
}
func (m *Model) say(msg string, isErr bool) {
	at := m.Now
	if at.IsZero() {
		// Said before the first frame, it shows from the first frame.
		at = time.Now()
	}
	m.notice, m.noticeErr, m.noticeAt = secure.Text(msg), isErr, at
	m.diagnosticEvent("notice", "", m.notice)
}

// view is what the model shows, as the view package derives it for the
// TUI and the CLI alike.
func (m *Model) view() view.View {
	return view.View{Snapshot: m.Snapshot, Now: m.Now, Cached: m.Cached(), Draft: m.Edit != nil}
}

// Rows lists the current tab, filtered by the search and marked when the
// view is cached or a local draft.
func (m *Model) Rows() []Row {
	if m.frameRows != nil && *m.frameRows != nil {
		return *m.frameRows
	}
	var rows []Row
	switch {
	case m.serviceCards():
		// The cards lie under their groups' headings, never folded away.
		for _, r := range m.groupedRows(true) {
			if r.Kind != "group" {
				rows = append(rows, r)
			}
		}
	case m.businessTab():
		rows = m.groupedRows(false)
	default:
		rows = m.view().List(m.Tab, m.Search)
	}
	if m.frameRows != nil {
		*m.frameRows = rows
	}
	return rows
}

// groupedRows are the business tab's rows by group, as the list folds
// them, or with open every group unfolded.
func (m *Model) groupedRows(open bool) []Row {
	o := view.GroupOptions{Application: m.Application, Environment: m.Environment, Service: m.ServiceFilter, By: m.groupMode(), Search: m.Search, Attention: m.AttentionOnly, Expanded: m.Expanded, Collapsed: m.GroupsCollapsed}
	if open {
		o.Expanded, o.Collapsed = nil, false
	}
	return m.view().GroupedList(m.Tab, o)
}

// tabRows builds the unfiltered rows of a tab.
func (m *Model) tabRows(tab int) []Row { return m.view().Rows(tab) }

// tabColumns heads the list columns of the current tab: the title column
// first, then one heading per Row.Cols entry.
func (m *Model) tabColumns() []string { return view.Columns(m.Tab) }

func age(now, t time.Time) string { return view.Age(now, t) }

func (m *Model) setTab(t int) {
	if t != m.Tab && t == tabAbout {
		m.aboutAt = m.Now
	}
	m.Tab = t
	m.Selected = 0
	m.Offset = 0
	m.cardsTop = 0
	m.Detail = false
}

// Animating reports how often the run loop should repaint without input:
// as often as what is on screen moves, and no more.
func (m *Model) Animating() time.Duration {
	switch {
	case m.Tab == tabAbout && m.Now.Sub(m.aboutAt) < 3*time.Second:
		return 70 * time.Millisecond // the wordmark and the lines come in
	case m.Tab == tabAbout:
		return 100 * time.Millisecond // then only the globe turns
	case m.waiting() && m.Loading != "":
		return 70 * time.Millisecond // the splash's globe turns and its wordmark comes in
	case m.waiting() && !m.Fetching:
		return 500 * time.Millisecond // nobody answered: the splash holds still
	case m.Loading != "" || m.waiting():
		return 90 * time.Millisecond // the spinner turns, retrying included
	case m.notice != "" && m.Now.Sub(m.noticeAt) >= noticeFor-noticeFade && m.Now.Sub(m.noticeAt) < noticeFor:
		return 100 * time.Millisecond // the notice fades out
	case m.mapShown():
		return 140 * time.Millisecond // glints run along the map's wires
	}
	return 500 * time.Millisecond
}

// mapShown reports whether the body shows a map: Topology always, the
// Overview when it has the room or the map fills the body.
func (m *Model) mapShown() bool {
	switch {
	case m.Device != nil:
		// A device's layout decides; the last frame says what it did.
		return !m.touch.mapRect.empty()
	case m.Detail:
		return false
	case m.Tab == tabTopology:
		return true
	case m.Tab == tabOverview:
		return m.FullMap || m.Width >= 90 && m.Height >= 25
	}
	return false
}

func (m *Model) phase() float64 { return float64(m.Now.UnixMilli()%3_600_000) / 1000 }

// NextObserver cycles the observer view: all observers, then each declared one.
func (m *Model) NextObserver() {
	views := []string{""}
	for _, o := range m.Snapshot.Topology.Observers {
		views = append(views, o.ID)
	}
	next := ""
	for i, v := range views {
		if v == m.Observer {
			next = views[(i+1)%len(views)]
			break
		}
	}
	m.Observer = next
	m.Selected = 0
	m.Offset = 0
	if m.Edit != nil {
		m.rebuild()
	}
}

// useDevice lays the TUI out for the device with id, or for the terminal
// with "". The same device again changes nothing, so the map's
// magnification and the list's scroll survive a page that repeats itself.
func (m *Model) useDevice(id string) {
	var d *Device
	if id != "" {
		var e error
		if d, e = DeviceByID(id); e != nil {
			return
		}
	}
	if d == nil && m.Device == nil || d != nil && m.Device != nil && d.ID == m.Device.ID {
		return
	}
	m.Device, m.touch = d, touch{}
}

// deviceID is the device the TUI is laid out for, "" for the terminal.
func (m *Model) deviceID() string {
	if m.Device == nil {
		return ""
	}
	return m.Device.ID
}

// Key returns quit/refresh/view/observe actions; it never performs network calls itself.
func (m *Model) Key(k string) string {
	if c, ok := strings.CutPrefix(k, "cell:"); ok {
		// The terminal's answer to CSI 16 t: a cell's height and width in
		// pixels.
		h, w, _ := strings.Cut(c, ":")
		ch, e1 := strconv.Atoi(h)
		cw, e2 := strconv.Atoi(w)
		if e1 == nil && e2 == nil && ch > 0 && cw > 0 {
			m.Cell = float64(ch) / float64(cw)
		}
		return ""
	}
	if id, ok := strings.CutPrefix(k, "device:"); ok {
		// A page that knows its screen, the website's viewer on a phone,
		// lays the TUI out for it ("device:fit"); "device:" goes back to
		// the terminal's own layouts.
		m.useDevice(id)
		return ""
	}
	if strings.HasPrefix(k, "mouse:") {
		if m.Device != nil {
			k = m.deviceMouse(k)
		} else {
			k = m.mouse(k)
		}
		if k == "" {
			return ""
		}
	}
	if m.Device != nil && !m.Searching {
		if r, ok := m.simKey(k); ok {
			return r
		}
	}
	if t, ok := strings.CutPrefix(k, "touch:"); ok {
		if m.modal != nil {
			return ""
		}
		return m.touchKey(t)
	}
	if m.modal != nil {
		return m.modalKey(k)
	}
	if m.Searching {
		switch k {
		case "esc", "enter":
			m.Searching = false
		case "backspace":
			r := []rune(m.Search)
			if len(r) > 0 {
				m.Search = string(r[:len(r)-1])
			}
		case "ctrl-c":
			return "quit"
		default:
			if strings.HasPrefix(k, "paste:") {
				m.Search += secure.Text(strings.TrimPrefix(k, "paste:"))
			} else if !strings.Contains(k, "\x1b") && len([]rune(k)) == 1 {
				m.Search += k
			}
		}
		if len([]rune(m.Search)) > 120 {
			m.Search = string([]rune(m.Search)[:120])
		}
		m.Selected = 0
		m.Offset = 0
		return ""
	}
	switch {
	case strings.HasPrefix(k, "tab:"):
		if n, e := strconv.Atoi(k[4:]); e == nil && n >= 0 && n < len(tabs) {
			m.setTab(n)
		}
		return ""
	case strings.HasPrefix(k, "row:"):
		if n, e := strconv.Atoi(k[4:]); e == nil {
			if n == m.Selected && !m.Detail {
				if !m.toggleGroup() {
					m.Detail = true
				}
				m.Offset = 0
			} else {
				m.Selected = n
			}
		}
		k = ""
	case strings.HasPrefix(k, "select:"), strings.HasPrefix(k, "goto:"):
		// select: picks a row in this tab when it has one; goto: (and a
		// select: this tab cannot satisfy) opens the entity's own tab.
		parts := strings.SplitN(k, ":", 3)
		found := false
		for i, r := range m.Rows() {
			if len(parts) == 3 && parts[0] == "select" && r.Kind == parts[1] && r.ID == parts[2] {
				m.Selected = i
				found = true
			}
		}
		if !found && len(parts) == 3 && parts[1] == "server" {
			// A node's card on the Servers tab, rather than its row on the
			// Topology's.
			m.setTab(tabServers)
			m.Search = ""
			if i := slices.IndexFunc(m.Rows(), func(r Row) bool { return r.ID == parts[2] }); i >= 0 {
				m.Selected = i
			}
		} else if !found && len(parts) == 3 {
			m.selectEntity(parts[1], parts[2])
		}
		k = ""
	case strings.HasPrefix(k, "key:"):
		return m.Key(k[4:])
	}
	if m.businessTab() && !m.Detail {
		switch k {
		case "i":
			m.Detail = true
			m.Offset = 0
			return ""
		case "b":
			m.businessMenu()
			return ""
		case " ":
			if m.toggleGroup() {
				return ""
			}
		case "enter":
			if m.toggleGroup() {
				return ""
			}
		case "c":
			m.serviceChecks()
			return ""
		}
	}
	switch k {
	case ",":
		m.openPreferences()
		return ""
	case "q", "ctrl-c":
		if m.Edit != nil && m.Edit.Dirty && k == "q" {
			m.confirm("Discard unsaved changes?", []string{"The draft has changes that are not written to", m.Edit.Path + ".", "", "Press y to quit without saving, or Esc to go back and ^S to save."}, func() string { return "quit" })
			return ""
		}
		return "quit"
	case "r":
		if m.Edit == nil {
			return "refresh"
		}
	case "v":
		if m.Edit != nil {
			m.NextObserver()
			return ""
		}
		return "view"
	case "/":
		m.Searching = true
	case "?":
		if m.Tab == tabAbout {
			m.setTab(tabOverview)
		} else {
			m.setTab(tabAbout)
		}
	case "m":
		for i, mode := range mapModes {
			if mode == m.mapMode() {
				m.MapMode = mapModes[(i+1)%len(mapModes)]
				break
			}
		}
	case "z":
		m.Zoom = (m.Zoom + 1) % 3
	case "f":
		// The map alone, or the panels back; from a tab without a map,
		// the Topology's map alone.
		if m.Tab != tabTopology && m.Tab != tabOverview {
			m.setTab(tabTopology)
			m.FullMap = true
		} else {
			m.FullMap = !m.FullMap
			m.Detail = false
		}
	case "p":
		// Cycle detail only while the geographic map is on screen.
		if m.mapShown() && m.mapMode() == "geo" {
			m.GeoLabels = m.GeoLabels.next()
		}
	case "s":
		// The Servers cards as a grid of one size, or the wall back; the
		// services as cards, or the list back, the same one selected.
		switch m.Tab {
		case tabServers:
			m.Uniform = !m.Uniform
		case tabServices:
			sel, _ := m.selectedRow()
			m.ServiceCards = !m.ServiceCards
			m.Detail, m.Offset, m.cardsTop = false, 0, 0
			m.Selected = max(0, slices.IndexFunc(m.Rows(), func(r Row) bool { return r.Kind == sel.Kind && r.ID == sel.ID }))
		}
	case "x":
		if m.Preview != nil {
			m.scenarioMenu()
		}
	case "D":
		m.Debug = !m.Debug
	case "a", "e", "d", "delete":
		m.editKey(k)
	case "ctrl-s", "w":
		if m.Edit != nil {
			m.save()
		}
	case "u":
		if m.Edit != nil {
			if m.Edit.Undo() {
				m.rebuild()
				m.say("Undid last change", false)
			} else {
				m.say("Nothing to undo", false)
			}
		}
	case "esc":
		if m.Search == "" && !m.Detail {
			m.FullMap = false
		}
		m.Search = ""
		m.Detail = false
	case "enter":
		m.Detail = !m.Detail
		m.Offset = 0
	case "tab", "right", "l":
		m.setTab((m.Tab + 1) % len(tabs))
	case "shift-tab", "left", "h":
		m.setTab((m.Tab + len(tabs) - 1) % len(tabs))
	case "down", "j":
		if m.Detail {
			m.Offset++
		} else {
			m.Selected++
		}
	case "up", "k":
		if m.Detail {
			m.Offset--
		} else {
			m.Selected--
		}
	case "wheel-down", "wheel-up":
		if m.Tab == tabAbout {
			// The wheel turns the globe.
			if k == "wheel-down" {
				m.aboutSpin += .3
			} else {
				m.aboutSpin -= .3
			}
			return ""
		}
		if k == "wheel-up" {
			if m.Detail {
				m.Offset -= 3
			} else {
				m.Selected -= 3
			}
			break
		}
		if m.Detail {
			m.Offset += 3
		} else {
			m.Selected += 3
		}
	case "pgdown":
		if m.Detail {
			m.Offset += 10
		} else {
			m.Selected += 10
		}
	case "pgup":
		if m.Detail {
			m.Offset -= 10
		} else {
			m.Selected -= 10
		}
	case "home", "g":
		m.Selected = 0
		m.Offset = 0
	case "end", "G":
		m.Selected = len(m.Rows()) - 1
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] < byte('1'+len(tabs)) {
			m.setTab(int(k[0] - '1'))
		}
	}
	rows := m.Rows()
	if m.Selected >= len(rows) {
		m.Selected = len(rows) - 1
	}
	if m.Selected < 0 {
		m.Selected = 0
	}
	if m.Offset < 0 {
		m.Offset = 0
	}
	return ""
}

// mouse translates an SGR mouse report into a key using the hit regions of
// the last frame. Only left presses and the wheel are interpreted.
func (m *Model) mouse(k string) string {
	p := strings.Split(k, ":")
	if len(p) != 5 {
		return ""
	}
	b, e1 := strconv.Atoi(p[1])
	x, e2 := strconv.Atoi(p[2])
	y, e3 := strconv.Atoi(p[3])
	if e1 != nil || e2 != nil || e3 != nil || p[4] != "M" {
		return ""
	}
	x, y = x-1, y-1
	switch b &^ (4 | 8 | 16) {
	case 64:
		return "wheel-up"
	case 65:
		return "wheel-down"
	case 0:
		return m.hitAt(x, y)
	}
	return ""
}

// hitAt is what the last frame does at cell (x, y): the target drawn last
// there, only a modal's while one is open; "" for nothing.
func (m *Model) hitAt(x, y int) string {
	for i := len(m.hits) - 1; i >= 0; i-- {
		h := m.hits[i]
		if m.modal != nil && !h.modal {
			continue
		}
		if y >= h.y && y < h.y+h.h && x >= h.x && x < h.x+h.w {
			return h.key
		}
	}
	return ""
}

func runeWidth(r rune) int { return view.RuneWidth(r) }
func width(s string) int   { return view.Width(s) }
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = secure.Text(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		k := runeWidth(r)
		if n+k > w {
			break
		}
		b.WriteRune(r)
		n += k
	}
	if n < w {
		b.WriteString(strings.Repeat(" ", w-n))
	}
	return b.String()
}

// plural counts things: "1 check", "2 checks".
func plural(n int, word string) string { return view.Plural(n, word) }

// ellipsis shortens s to at most w columns, marking the cut with "…".
func ellipsis(s string, w int) string {
	if width(s) <= w {
		return s
	}
	if w <= 1 {
		return strings.TrimRight(fit(s, w), " ")
	}
	return strings.TrimRight(fit(s, w-1), " ") + "…"
}

// wrap breaks at spaces when possible and hard-wraps long words.
func wrap(s string, w int) []string {
	if w < 1 {
		return nil
	}
	s = secure.Text(s)
	out := []string{}
	line := []rune{}
	n := 0
	lastSpace := -1
	for _, r := range s {
		k := runeWidth(r)
		if n+k > w {
			if lastSpace > 0 && r != ' ' {
				rest := append([]rune{}, line[lastSpace+1:]...)
				out = append(out, string(line[:lastSpace]))
				line, n = rest, width(string(rest))
			} else {
				out = append(out, string(line))
				line, n = nil, 0
			}
			lastSpace = -1
			if r == ' ' {
				continue
			}
		}
		if r == ' ' {
			lastSpace = len(line)
		}
		line = append(line, r)
		n += k
	}
	out = append(out, string(line))
	return out
}
