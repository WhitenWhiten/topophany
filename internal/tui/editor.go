package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Editor holds a local draft of a reviewed topology file. Every change is
// validated as a whole configuration before it is accepted into the draft;
// nothing is written until an explicit save.
type Editor struct {
	Path    string
	Config  model.Config
	Dirty   bool
	undo    []model.Config
	base    config.FileVersion
	tracked bool
}

// NewEditor captures the exact file that supplied the draft. A concurrent
// change between loading and opening is rejected instead of becoming baseline.
func NewEditor(path string, c model.Config) (*Editor, error) {
	data, base, err := config.ReadFileVersion(path)
	if err != nil {
		return nil, err
	}
	if base.Exists {
		disk, err := config.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if config.Revision(disk) != config.Revision(c) {
			return nil, config.ErrFileChanged
		}
	}
	return &Editor{Path: path, Config: c, base: base, tracked: true}, nil
}

func clone(c model.Config) model.Config {
	b, _ := json.Marshal(c)
	var out model.Config
	_ = json.Unmarshal(b, &out)
	return out
}

// Apply mutates a copy, normalizes and validates it, then commits it.
func (e *Editor) Apply(f func(*model.Config) error) error {
	next := clone(e.Config)
	if err := f(&next); err != nil {
		return err
	}
	config.Defaults(&next)
	if err := config.Validate(next); err != nil {
		return err
	}
	e.undo = append(e.undo, e.Config)
	if len(e.undo) > 64 {
		e.undo = e.undo[1:]
	}
	e.Config = next
	e.Dirty = true
	return nil
}
func (e *Editor) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	e.Config = e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.Dirty = true
	return true
}

// Save atomically replaces the file, keeping its permission bits.
func (e *Editor) Save() (string, error) {
	if !e.tracked {
		_, base, err := config.ReadFileVersion(e.Path)
		if err != nil {
			return "", err
		}
		e.base, e.tracked = base, true
	}
	if err := config.Validate(e.Config); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(e.Config, "", "  ")
	if err != nil {
		return "", err
	}
	mode := os.FileMode(0600)
	if st, err := os.Stat(e.Path); err == nil {
		mode = st.Mode().Perm()
	}
	mode = config.WriteMode(e.Config, mode)
	if e.base, err = config.CommitFile(e.Path, e.base, append(b, '\n'), mode); err != nil {
		return "", err
	}
	e.Dirty = false
	return config.Revision(e.Config), nil
}

// rebuild derives a display snapshot from the draft; with no observations
// every state is unknown, which is exactly what an unmonitored draft is.
func (m *Model) rebuild() {
	if runtime.GOOS == "js" || m.Edit == nil {
		return
	}
	if m.Now.IsZero() {
		m.Now = time.Now()
	}
	m.Snapshot = engine.Build(m.Edit.Config, "local-draft", m.Observer, nil, nil, m.Now)
	m.Server = m.Edit.Path
	m.Error = ""
}

func (m *Model) save() {
	if runtime.GOOS == "js" {
		return
	}
	rev, err := m.Edit.Save()
	if err != nil {
		m.say("Save failed: "+err.Error(), true)
		return
	}
	m.say("Saved "+m.Edit.Path+" · revision "+rev[:12]+" · reload the collector and workers (SIGHUP) to apply", false)
}

// --- modals ------------------------------------------------------------------

type menuItem struct{ key, label, kind, hint string }
type modal struct {
	kind  string // menu, form, confirm, info
	title string
	lines []string
	items []menuItem
	sel   int
	form  *Form
	yes   func() string
	pick  func(int) string // menu action; nil opens the add form for the item kind
}

var addItems = []menuItem{{"b", "Application — business grouping", "application", ""}, {"n", "Node — host, VM, container or external party", "node", ""}, {"l", "Link — directed connection between nodes", "link", ""}, {"p", "Endpoint — an address on a node", "endpoint", ""}, {"s", "Service — something hosted on a node", "service", ""}, {"o", "Observer — a place checks run from", "observer", ""}, {"t", "Path — declared route an observer takes", "path", ""}, {"c", "Check — a probe that produces evidence", "check", ""}}

func (m *Model) confirm(title string, lines []string, yes func() string) {
	m.modal = &modal{kind: "confirm", title: title, lines: lines, yes: yes}
}
func (m *Model) info(title string, lines ...string) {
	m.modal = &modal{kind: "info", title: title, lines: lines}
}

func (m *Model) selectedRow() (Row, bool) {
	rows := m.Rows()
	if m.Selected >= 0 && m.Selected < len(rows) {
		return rows[m.Selected], true
	}
	return Row{}, false
}

func editable(kind string) bool {
	switch kind {
	case "node", "link", "endpoint", "service", "observer", "path", "check":
		return true
	}
	return false
}

func (m *Model) editKey(k string) {
	row, ok := m.selectedRow()
	if m.Preview != nil {
		m.info("Preview topology is read-only",
			"Evidence here is synthetic and the topology is built in.",
			"To change it, copy it out and open it in the editor:",
			"",
			"    topo preview --print-topology > preview.json",
			"    topo edit --config preview.json",
			"",
			"Then preview your edited file with synthetic evidence:",
			"",
			"    topo preview --config preview.json")
		return
	}
	if runtime.GOOS == "js" || m.Edit == nil {
		what := "this topology"
		if ok && editable(row.Kind) {
			what = row.Kind + " " + row.ID
		}
		m.info("Read-only collector view",
			"Collectors deliberately expose no remote configuration writes,",
			"so "+what+" cannot be changed from here.",
			"",
			"Edit the reviewed topology file on its host instead:",
			"",
			"    topo edit --config topology.json",
			"",
			"The editor has the same views, plus a / e / d to add, edit and",
			"delete, validation on every change, u to undo and ^S to save.",
			"Then reload the collector and workers with the same file",
			"(SIGHUP, or systemctl reload; a restart does the same).")
		return
	}
	switch k {
	case "a":
		def := map[int]string{tabTopology: "node", tabServices: "service", tabPaths: "path", tabChecks: "check", tabObservers: "observer"}[m.Tab]
		if ok && row.Kind == "node" {
			def = "endpoint"
		}
		if ok && (row.Kind == "endpoint" || row.Kind == "link") {
			def = row.Kind
		}
		sel := 0
		for i, it := range addItems {
			if it.kind == def {
				sel = i
			}
		}
		m.modal = &modal{kind: "menu", title: "Add to topology", items: addItems, sel: sel}
	case "e":
		if !ok || !editable(row.Kind) {
			m.say("Select a node, endpoint, link, service, path, check or observer to edit", true)
			return
		}
		m.openForm(row.Kind, row.ID)
	case "d", "delete":
		if !ok || !editable(row.Kind) {
			m.say("Nothing deletable is selected", true)
			return
		}
		kind, id := row.Kind, row.ID
		m.confirm("Delete "+kind+" “"+id+"”?", []string{"The whole topology is re-validated after the delete.", "If anything still references " + id + ", the delete is refused", "and the reference is named so you can fix it first.", "", "y delete   ·   Esc keep"}, func() string {
			err := m.Edit.Apply(func(c *model.Config) error { return remove(c, kind, id) })
			if err != nil {
				m.say("Cannot delete "+id+": "+err.Error(), true)
			} else {
				m.rebuild()
				m.say("Deleted "+kind+" "+id+" (u to undo, ^S to save)", false)
			}
			return ""
		})
	}
}

func (m *Model) modalKey(k string) string {
	md := m.modal
	switch md.kind {
	case "preferences":
		return m.preferencesKey(k)
	case "info":
		if k == "ctrl-c" {
			return "quit"
		}
		if k == "esc" || k == "enter" || k == "q" || k == "btn:ok" || k == "e" || k == "a" || k == "d" {
			m.modal = nil
		}
	case "confirm":
		switch k {
		case "y", "Y", "btn:yes":
			m.modal = nil
			return md.yes()
		case "n", "N", "esc", "btn:no", "q":
			m.modal = nil
		case "ctrl-c":
			return "quit"
		}
	case "menu":
		switch k {
		case "up", "k", "wheel-up":
			md.sel = (md.sel + len(md.items) - 1) % len(md.items)
		case "down", "j", "wheel-down", "tab":
			md.sel = (md.sel + 1) % len(md.items)
		case "esc", "q":
			m.modal = nil
		case "ctrl-c":
			return "quit"
		case "enter":
			return m.choose(md, md.sel)
		default:
			if strings.HasPrefix(k, "menu:") {
				if n, e := strconv.Atoi(k[5:]); e == nil && n >= 0 && n < len(md.items) {
					return m.choose(md, n)
				}
				return ""
			}
			for i, it := range md.items {
				if it.key == k {
					return m.choose(md, i)
				}
			}
		}
	case "form":
		if runtime.GOOS == "js" {
			return ""
		}
		f := md.form
		switch r := f.key(k); r {
		case "cancel":
			m.modal = nil
		case "quit":
			return "quit"
		case "submit":
			err := m.Edit.Apply(f.commit)
			if err != nil {
				f.err = err.Error()
				return ""
			}
			m.modal = nil
			m.rebuild()
			verb := "Added "
			if f.orig != "" {
				verb = "Updated "
			}
			m.say(verb+f.kind+" "+f.val("ID")+" to the draft (u undo · ^S save)", false)
			m.selectEntity(f.kind, f.val("ID"))
		}
	}
	return ""
}

func (m *Model) choose(md *modal, i int) string {
	m.modal = nil
	if md.pick != nil {
		return md.pick(i)
	}
	m.openForm(md.items[i].kind, "")
	return ""
}

func (m *Model) selectEntity(kind, id string) {
	if kind == "application" {
		m.setTab(tabServices)
		m.Application = id
		m.Environment, m.ServiceFilter, m.Search = "", "", ""
		m.AttentionOnly = false
		return
	}
	tab := map[string]int{"node": tabTopology, "endpoint": tabTopology, "link": tabTopology, "service": tabServices, "path": tabPaths, "check": tabChecks, "observer": tabObservers}[kind]
	if m.Tab != tab {
		m.setTab(tab)
	}
	m.Search = ""
	m.revealBusinessEntity(kind, id)
	for i, r := range m.Rows() {
		if r.Kind == kind && r.ID == id {
			m.Selected = i
		}
	}
}

// --- forms -------------------------------------------------------------------

const (
	fText = iota
	fInt
	fFloat
	fBool
	fChoice
	fList
)

type field struct {
	label, value, hint string
	kind               int
	choices            []string
	readonly           bool
	cursor             int
	blur               func(*Form)
	when               func(*Form) bool // shown, and focused, only while it holds; nil for always
}
type Form struct {
	kind, orig, title string
	fields            []*field
	focus             int // len(fields) = Save, len(fields)+1 = Cancel
	scroll            int
	err               string
	note              string
	commit            func(*model.Config) error
}

func (f *Form) add(label, value string, kind int, hint string, choices ...string) *field {
	fd := &field{label: label, value: value, kind: kind, hint: hint, choices: choices, cursor: len([]rune(value))}
	f.fields = append(f.fields, fd)
	return fd
}
func (f *Form) get(label string) *field {
	for _, fd := range f.fields {
		if fd.label == label {
			return fd
		}
	}
	return &field{}
}
func (f *Form) val(label string) string { return strings.TrimSpace(f.get(label).value) }
func (f *Form) list(label string) []string {
	out := []string{}
	for _, v := range strings.Split(f.val(label), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func (f *Form) num(label string) (int, error) {
	v := f.val(label)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}
	return n, nil
}
func (f *Form) yes(label string) bool { return f.val(label) == "yes" }

// shown reports whether field i is on the form: a field that only means
// something for another kind of check stays out of the way.
func (f *Form) shown(i int) bool {
	return i >= len(f.fields) || f.fields[i].when == nil || f.fields[i].when(f)
}

// move leaves the focused field and steps d places through the fields on
// show and the two buttons after them.
func (f *Form) move(d int) {
	if f.focus < len(f.fields) {
		if b := f.fields[f.focus].blur; b != nil {
			b(f)
		}
	}
	n := len(f.fields) + 2
	for ; d != 0; d -= sign(d) {
		next := (f.focus + sign(d) + n) % n
		for next != f.focus && !f.shown(next) {
			next = (next + sign(d) + n) % n
		}
		f.focus = next
	}
}

func sign(d int) int {
	if d < 0 {
		return -1
	}
	return 1
}
func (f *Form) key(k string) string {
	switch {
	case k == "ctrl-c":
		return "quit"
	case k == "esc" || k == "btn:cancel":
		return "cancel"
	case k == "ctrl-s" || k == "btn:save":
		f.move(0)
		return "submit"
	case strings.HasPrefix(k, "field:"):
		if n, e := strconv.Atoi(k[6:]); e == nil && n >= 0 && n < len(f.fields) && f.shown(n) {
			f.move(0)
			f.focus = n
		}
		return ""
	case k == "tab" || k == "down" || k == "wheel-down":
		f.move(1)
		return ""
	case k == "shift-tab" || k == "up" || k == "wheel-up":
		f.move(-1)
		return ""
	case k == "enter":
		switch f.focus {
		case len(f.fields):
			return "submit"
		case len(f.fields) + 1:
			return "cancel"
		}
		f.move(1)
		return ""
	}
	if f.focus >= len(f.fields) {
		if k == "left" || k == "right" {
			f.focus = len(f.fields) + (f.focus-len(f.fields)+1)%2
		}
		return ""
	}
	fd := f.fields[f.focus]
	if fd.readonly {
		return ""
	}
	switch fd.kind {
	case fBool:
		if k == " " || k == "left" || k == "right" || k == "y" || k == "n" {
			if fd.value == "yes" && k != "y" || k == "n" {
				fd.value = "no"
			} else {
				fd.value = "yes"
			}
		}
		return ""
	case fChoice:
		i := 0
		for j, c := range fd.choices {
			if c == fd.value {
				i = j
			}
		}
		switch {
		case k == "right" || k == " ":
			fd.value = fd.choices[(i+1)%len(fd.choices)]
		case k == "left":
			fd.value = fd.choices[(i+len(fd.choices)-1)%len(fd.choices)]
		case k == "backspace" || k == "delete":
			fd.value = fd.choices[0]
		case len([]rune(k)) == 1:
			// Jump to the next choice starting with the typed character.
			for d := 1; d <= len(fd.choices); d++ {
				c := fd.choices[(i+d)%len(fd.choices)]
				if strings.HasPrefix(strings.ToLower(c), strings.ToLower(k)) {
					fd.value = c
					break
				}
			}
		}
		return ""
	}
	r := []rune(fd.value)
	fd.cursor = min(max(fd.cursor, 0), len(r))
	switch {
	case k == "left":
		fd.cursor = max(0, fd.cursor-1)
	case k == "right":
		fd.cursor = min(len(r), fd.cursor+1)
	case k == "home":
		fd.cursor = 0
	case k == "end":
		fd.cursor = len(r)
	case k == "backspace":
		if fd.cursor > 0 {
			fd.value = string(append(r[:fd.cursor-1:fd.cursor-1], r[fd.cursor:]...))
			fd.cursor--
		}
	case k == "delete":
		if fd.cursor < len(r) {
			fd.value = string(append(r[:fd.cursor:fd.cursor], r[fd.cursor+1:]...))
		}
	default:
		ins := ""
		if strings.HasPrefix(k, "paste:") {
			ins = strings.ReplaceAll(secure.Text(strings.TrimPrefix(k, "paste:")), "\n", " ")
		} else if len([]rune(k)) == 1 && !strings.Contains(k, "\x1b") {
			ins = k
		}
		if ins != "" && len(r)+len([]rune(ins)) <= 512 {
			add := []rune(ins)
			fd.value = string(r[:fd.cursor]) + ins + string(r[fd.cursor:])
			fd.cursor += len(add)
		}
	}
	return ""
}

func ids[T any](list []T, id func(T) string) []string {
	out := []string{}
	for _, v := range list {
		out = append(out, id(v))
	}
	return out
}
func optional(xs []string) []string { return append([]string{""}, xs...) }
func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
func numStr(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// upsert replaces the element whose ID is orig, or appends a new one.
func upsert[T any](list []T, orig string, v T, id func(T) string) ([]T, error) {
	if orig == "" {
		for _, x := range list {
			if id(x) == id(v) {
				return list, fmt.Errorf("ID %q is already used", id(v))
			}
		}
		return append(list, v), nil
	}
	for i, x := range list {
		if id(x) == orig {
			list[i] = v
			return list, nil
		}
	}
	return list, errors.New("entity disappeared from the draft")
}
func without[T any](list []T, id string, key func(T) string) []T {
	out := list[:0]
	for _, x := range list {
		if key(x) != id {
			out = append(out, x)
		}
	}
	return out
}
func remove(c *model.Config, kind, id string) error {
	switch kind {
	case "application":
		c.Applications = without(c.Applications, id, func(a model.Application) string { return a.ID })
	case "node":
		c.Nodes = without(c.Nodes, id, func(n model.Node) string { return n.ID })
	case "link":
		c.Links = without(c.Links, id, func(n model.Link) string { return n.ID })
	case "endpoint":
		c.Endpoints = without(c.Endpoints, id, func(n model.Endpoint) string { return n.ID })
	case "service":
		c.Services = without(c.Services, id, func(n model.Service) string { return n.ID })
	case "observer":
		c.Observers = without(c.Observers, id, func(n model.Observer) string { return n.ID })
	case "path":
		c.Paths = without(c.Paths, id, func(n model.Path) string { return n.ID })
	case "check":
		c.Checks = without(c.Checks, id, func(n model.Check) string { return n.ID })
	default:
		return errors.New("not editable")
	}
	return nil
}

var checkKinds = config.EditableCheckKinds()

func (m *Model) openForm(kind, id string) {
	// Browser viewers never author topology; omit forms and their builders.
	if runtime.GOOS == "js" {
		return
	}
	c := m.Edit.Config
	nodes := ids(c.Nodes, func(n model.Node) string { return n.ID })
	observers := ids(c.Observers, func(n model.Observer) string { return n.ID })
	f := &Form{kind: kind, orig: id}
	verb := "New "
	if id != "" {
		verb = "Edit "
	}
	f.title = verb + kind
	idField := func(v string) {
		fd := f.add("ID", v, fText, "Stable identity: letters, digits, . _ - (not an IP address)")
		fd.readonly = id != ""
	}
	selected := ""
	if row, ok := m.selectedRow(); ok {
		if row.Kind == "node" {
			selected = row.ID
		}
		if row.Kind == "endpoint" {
			for _, e := range c.Endpoints {
				if e.ID == row.ID {
					selected = e.Node
				}
			}
		}
	}
	first := func(xs []string) string {
		if len(xs) > 0 {
			return xs[0]
		}
		return ""
	}
	if selected == "" {
		selected = first(nodes)
	}
	switch kind {
	case "node":
		n := model.Node{Kind: "host"}
		for _, x := range c.Nodes {
			if x.ID == id {
				n = x
			}
		}
		idField(n.ID)
		f.add("Name", n.Name, fText, "Display name")
		f.add("Kind", n.Kind, fChoice, "←/→ to choose; edge: an anycast network in front of origins (Cloudflare, Vercel); platform: managed hosting (Supabase)", "", "host", "vm", "container", "router", "edge", "platform", "external")
		f.add("Group", n.Group, fText, "Free-form grouping, e.g. Home, Domestic, Overseas")
		others := []string{}
		for _, x := range nodes {
			if x != n.ID {
				others = append(others, x)
			}
		}
		f.add("Parent", n.Parent, fChoice, "Runs inside this node (VM/container host)", optional(others)...)
		place, lat, lon := "", "", ""
		if n.Location != nil {
			place, lat, lon = n.Location.Label, strconv.FormatFloat(n.Location.Lat, 'f', -1, 64), strconv.FormatFloat(n.Location.Lon, 'f', -1, 64)
		}
		pf := f.add("Place", place, fText, "City or region; known names fill lat/lon (Tokyo, Frankfurt, 上海 …)")
		pf.blur = func(f *Form) {
			if p, ok := lookupPlace(f.val("Place")); ok && f.val("Lat") == "" && f.val("Lon") == "" {
				f.get("Lat").value = strconv.FormatFloat(p.lat, 'f', -1, 64)
				f.get("Lon").value = strconv.FormatFloat(p.lon, 'f', -1, 64)
				f.note = "Filled coordinates for " + p.name
			}
		}
		f.add("Lat", lat, fFloat, "Latitude −90…90 (declared placement for the geo map)")
		f.add("Lon", lon, fFloat, "Longitude −180…180")
		f.add("Provider", n.Provider, fText, "Who runs it, e.g. cloudflare, vercel, supabase, or a VPS vendor")
		f.add("Notes", n.Notes, fText, "")
		f.commit = func(c *model.Config) error {
			pf.blur(f)
			n.ID, n.Name, n.Kind, n.Group, n.Provider, n.Parent, n.Notes = f.val("ID"), f.val("Name"), f.val("Kind"), f.val("Group"), f.val("Provider"), f.val("Parent"), f.val("Notes")
			n.Location = nil
			if f.val("Lat") != "" || f.val("Lon") != "" {
				la, e1 := strconv.ParseFloat(f.val("Lat"), 64)
				lo, e2 := strconv.ParseFloat(f.val("Lon"), 64)
				if e1 != nil || e2 != nil {
					return errors.New("lat and lon must both be numbers")
				}
				n.Location = &model.Location{Label: f.val("Place"), Lat: la, Lon: lo}
			} else if f.val("Place") != "" {
				return errors.New("unknown place name; enter lat/lon, or clear Place")
			}
			var err error
			c.Nodes, err = upsert(c.Nodes, id, n, func(n model.Node) string { return n.ID })
			return err
		}
	case "link":
		l := model.Link{From: selected}
		for _, x := range c.Links {
			if x.ID == id {
				l = x
			}
		}
		idField(l.ID)
		f.add("From", l.From, fChoice, "Initiating node", nodes...)
		f.add("To", l.To, fChoice, "Receiving node", nodes...)
		f.add("Transport", l.Transport, fText, "e.g. public TCP, LAN, tailnet, reverse SSH forward")
		f.add("Notes", l.Notes, fText, "")
		f.commit = func(c *model.Config) error {
			l.ID, l.From, l.To, l.Transport, l.Notes = f.val("ID"), f.val("From"), f.val("To"), f.val("Transport"), f.val("Notes")
			var err error
			c.Links, err = upsert(c.Links, id, l, func(n model.Link) string { return n.ID })
			return err
		}
	case "endpoint":
		ep := model.Endpoint{Node: selected, Network: "public"}
		for _, x := range c.Endpoints {
			if x.ID == id {
				ep = x
			}
		}
		idField(ep.ID)
		f.add("Node", ep.Node, fChoice, "Node that owns this address", nodes...)
		f.add("Address", ep.Address, fText, "host:port or http(s):// URL")
		f.add("Network", ep.Network, fChoice, "loopback needs an observer scope", "public", "lan", "tailnet", "loopback", "external")
		f.add("Observer", ep.Observer, fChoice, "Only this observer may probe it", optional(observers)...)
		f.add("Via", strings.Join(ep.Via, ", "), fList, "Edge or proxy node IDs that answer first, client side first, e.g. a CDN in front")
		f.add("Notes", ep.Notes, fText, "")
		f.commit = func(c *model.Config) error {
			ep.ID, ep.Node, ep.Address, ep.Network, ep.Observer, ep.Via, ep.Notes = f.val("ID"), f.val("Node"), f.val("Address"), f.val("Network"), f.val("Observer"), f.list("Via"), f.val("Notes")
			var err error
			c.Endpoints, err = upsert(c.Endpoints, id, ep, func(n model.Endpoint) string { return n.ID })
			return err
		}
	case "application":
		a := model.Application{}
		for _, existing := range c.Applications {
			if existing.ID == id {
				a = existing
			}
		}
		idField(a.ID)
		f.add("Name", a.Name, fText, "Business application name")
		f.commit = func(c *model.Config) error {
			a.ID, a.Name = f.val("ID"), f.val("Name")
			var err error
			c.Applications, err = upsert(c.Applications, id, a, func(a model.Application) string { return a.ID })
			return err
		}

	case "service":
		s := model.Service{Node: selected}
		for _, x := range c.Services {
			if x.ID == id {
				s = x
			}
		}
		idField(s.ID)
		f.add("Name", s.Name, fText, "")
		f.add("Application", s.Application, fChoice, "Business application", optional(ids(c.Applications, func(a model.Application) string { return a.ID }))...)
		f.add("Environment", s.Environment, fText, "For example production, staging or development")
		f.add("Node", s.Node, fChoice, "Hosting node", nodes...)
		f.add("Runtime", s.Runtime, fText, "How it runs there, e.g. workers, pages, functions, container")
		f.add("Endpoints", strings.Join(s.Endpoints, ", "), fList, "Comma-separated endpoint IDs")
		f.add("Depends on", strings.Join(s.DependsOn, ", "), fList, "Comma-separated service IDs")
		f.add("Runbook", s.Runbook, fText, "Shown to operators; never executed")
		f.add("Notes", s.Notes, fText, "")
		f.commit = func(c *model.Config) error {
			s.Application, s.Environment = f.val("Application"), f.val("Environment")
			s.ID, s.Name, s.Node, s.Runtime, s.Endpoints, s.DependsOn, s.Runbook, s.Notes = f.val("ID"), f.val("Name"), f.val("Node"), f.val("Runtime"), f.list("Endpoints"), f.list("Depends on"), f.val("Runbook"), f.val("Notes")
			var err error
			c.Services, err = upsert(c.Services, id, s, func(n model.Service) string { return n.ID })
			return err
		}
	case "observer":
		o := model.Observer{Node: selected, StaleSeconds: 90}
		for _, x := range c.Observers {
			if x.ID == id {
				o = x
			}
		}
		idField(o.ID)
		f.add("Name", o.Name, fText, "")
		f.add("Node", o.Node, fChoice, "Where the executor runs", optional(nodes)...)
		f.add("Context", o.Context, fText, "OS / network namespace description (required)")
		f.add("Route expectation", o.RouteExpectation, fText, "Expected, not verified")
		f.add("Stale seconds", numStr(o.StaleSeconds), fInt, "5…86400")
		f.commit = func(c *model.Config) error {
			n, err := f.num("Stale seconds")
			if err != nil {
				return err
			}
			o.ID, o.Name, o.Node, o.Context, o.RouteExpectation, o.StaleSeconds = f.val("ID"), f.val("Name"), f.val("Node"), f.val("Context"), f.val("Route expectation"), n
			c.Observers, err = upsert(c.Observers, id, o, func(n model.Observer) string { return n.ID })
			return err
		}
	case "path":
		p := model.Path{Observer: first(observers)}
		for _, x := range c.Paths {
			if x.ID == id {
				p = x
			}
		}
		idField(p.ID)
		f.add("Name", p.Name, fText, "")
		f.add("Observer", p.Observer, fChoice, "Path is declared from this observer's view", observers...)
		f.add("Nodes", strings.Join(p.Nodes, ", "), fList, "Comma-separated node IDs in order")
		f.add("Links", strings.Join(p.Links, ", "), fList, "Contiguous link IDs (optional)")
		f.add("Candidates", strings.Join(p.Candidates, ", "), fList, "Alternative path IDs — not serial hops")
		f.add("Depends on", strings.Join(p.DependsOn, ", "), fList, "Path IDs")
		f.add("Opaque", yesno(p.Opaque), fBool, "Contains a black-box provider segment")
		f.add("Notes", p.Notes, fText, "")
		if len(p.Proxies) > 0 {
			f.note = itoa(len(p.Proxies)) + " proxy hop(s) are kept as-is; edit proxies in the JSON file"
		}
		f.commit = func(c *model.Config) error {
			p.ID, p.Name, p.Observer, p.Nodes, p.Links, p.Candidates, p.DependsOn, p.Opaque, p.Notes = f.val("ID"), f.val("Name"), f.val("Observer"), f.list("Nodes"), f.list("Links"), f.list("Candidates"), f.list("Depends on"), f.yes("Opaque"), f.val("Notes")
			var err error
			c.Paths, err = upsert(c.Paths, id, p, func(n model.Path) string { return n.ID })
			return err
		}
	case "check":
		x := model.Check{Observer: first(observers), Kind: "http", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2}
		for _, y := range c.Checks {
			if y.ID == id {
				x = y
			}
		}
		idField(x.ID)
		f.add("Name", x.Name, fText, "")
		f.add("Observer", x.Observer, fChoice, "Executor that runs this probe", observers...)
		f.add("Kind", x.Kind, fChoice, "Probe type", checkKinds...)
		f.add("Endpoint", x.Endpoint, fChoice, "Target endpoint (or set Target)", optional(ids(c.Endpoints, func(n model.Endpoint) string { return n.ID }))...)
		f.add("Target", x.Target, fText, "Explicit override; otherwise the endpoint address")
		f.add("Application", x.Application, fChoice, "Independent checks only; service checks inherit scope", optional(ids(c.Applications, func(a model.Application) string { return a.ID }))...)
		f.add("Environment", x.Environment, fText, "Independent checks only; leave blank when Service is set")
		f.add("Service", x.Service, fChoice, "Evidence counts toward this service", optional(ids(c.Services, func(n model.Service) string { return n.ID }))...)
		f.add("Path", x.Path, fChoice, "Declared path (same observer)", optional(ids(c.Paths, func(n model.Path) string { return n.ID }))...)
		f.add("Interval s", numStr(x.IntervalSeconds), fInt, "1…86400")
		f.add("Timeout s", numStr(x.TimeoutSeconds), fInt, "1…300")
		f.add("Stale s", numStr(x.StaleSeconds), fInt, "≥ interval")
		f.add("Fail after", numStr(x.FailureThreshold), fInt, "Consecutive failures before down (1…20)")
		f.add("Recover after", numStr(x.RecoveryThreshold), fInt, "Consecutive successes before up (1…20)")
		f.add("Policy after", numStr(x.PolicyThreshold), fInt, "Consecutive policy samples (0 = immediate, 1…20 explicit)")
		f.add("Disabled", yesno(x.Disabled), fBool, "Paused checks are never counted as uptime")
		f.add("Maintenance", yesno(x.Maintenance), fBool, "")
		status := []string{}
		for _, s := range x.HTTP.Status {
			status = append(status, strconv.Itoa(s))
		}
		// Options of one kind of probe show only while it is chosen; what
		// they hold is kept, and saved as before, whatever is chosen.
		kindIs := func(k string) func(*Form) bool { return func(f *Form) bool { return f.val("Kind") == k } }
		f.add("HTTP status", strings.Join(status, ", "), fList, "http: accepted status codes, e.g. 200, 204").when = kindIs("http")
		f.add("HTTP contains", x.HTTP.Contains, fText, "http: required body substring").when = kindIs("http")
		f.add("Exit format", x.HTTP.ExitFormat, fChoice, "http: how to read the exit address", "", "plain", "trace", "json").when = kindIs("http")
		f.add("Exit JSON key", x.HTTP.ExitJSONKey, fText, "json: key holding the address, default ip").when = kindIs("http")
		trace := model.TraceExpectations{}
		if x.HTTP.ExpectedTrace != nil {
			trace = *x.HTTP.ExpectedTrace
		}
		f.add("Trace warp", trace.Warp, fChoice, "trace: expected WARP state", "", "off", "on", "plus").when = kindIs("http")
		f.add("Trace gateway", trace.Gateway, fChoice, "trace: expected Gateway state", "", "on", "off").when = kindIs("http")
		f.add("Trace colos", strings.Join(trace.Colos, ", "), fList, "trace: expected colos, e.g. HKG, NRT").when = kindIs("http")
		f.add("Expected pops", strings.Join(x.HTTP.ExpectedPops, ", "), fList, "http: colos the edge may name, e.g. HKG, NRT").when = kindIs("http")
		envNames := make([]string, 0, len(x.HTTP.RequestHeaderEnvs))
		for name := range x.HTTP.RequestHeaderEnvs {
			envNames = append(envNames, name)
		}
		slices.Sort(envNames)
		envEntries := make([]string, 0, len(envNames))
		for _, name := range envNames {
			envEntries = append(envEntries, name+"="+x.HTTP.RequestHeaderEnvs[name])
		}
		f.add("Secret request headers", strings.Join(envEntries, ", "), fList, "http: Name=ENV_VAR, comma separated").when = kindIs("http")
		f.add("DNS name", x.DNS.Name, fText, "dns: name to resolve").when = kindIs("dns")
		f.add("DNS type", x.DNS.Type, fChoice, "dns: record type", "", "A", "AAAA").when = kindIs("dns")
		// Empty is the historical UDP probe; the other choices change only
		// how the same question travels.
		f.add("DNS transport", x.DNS.Transport, fChoice, "dns: udp (blank), tcp, dot or doh", "", "udp", "tcp", "dot", "doh").when = kindIs("dns")
		f.add("Server name", x.ServerName, fText, "dns dot/doh: certificate name; tls: SNI and certificate name").when = func(f *Form) bool {
			return f.val("Kind") == "dns" || f.val("Kind") == "tls"
		}
		cloudflareFields(f, x, kindIs("cloudflare"))
		cloudflaredFields(f, x, kindIs("cloudflared"))
		addHostFields(f, x.Host, kindIs("host"))
		f.add("Notes", x.Notes, fText, "")
		f.commit = func(c *model.Config) error {
			x.Application, x.Environment = f.val("Application"), f.val("Environment")
			x.ID, x.Name, x.Observer, x.Kind, x.Endpoint, x.Target, x.Service, x.Path, x.Notes = f.val("ID"), f.val("Name"), f.val("Observer"), f.val("Kind"), f.val("Endpoint"), f.val("Target"), f.val("Service"), f.val("Path"), f.val("Notes")
			nums := []*int{&x.IntervalSeconds, &x.TimeoutSeconds, &x.StaleSeconds, &x.FailureThreshold, &x.RecoveryThreshold, &x.PolicyThreshold}
			for i, label := range []string{"Interval s", "Timeout s", "Stale s", "Fail after", "Recover after", "Policy after"} {
				n, err := f.num(label)
				if err != nil {
					return err
				}
				*nums[i] = n
			}
			x.Disabled, x.Maintenance = f.yes("Disabled"), f.yes("Maintenance")
			x.HTTP.Status = nil
			for _, s := range f.list("HTTP status") {
				n, err := strconv.Atoi(s)
				if err != nil {
					return errors.New("HTTP status must be numbers")
				}
				x.HTTP.Status = append(x.HTTP.Status, n)
			}
			x.HTTP.Contains, x.DNS.Name, x.DNS.Type = f.val("HTTP contains"), f.val("DNS name"), f.val("DNS type")
			x.DNS.Transport = f.val("DNS transport")
			x.ServerName = f.val("Server name")
			x.HTTP.ExitFormat = f.val("Exit format")
			x.HTTP.ExitJSONKey = f.val("Exit JSON key")
			warp, gateway, colos := f.val("Trace warp"), f.val("Trace gateway"), f.list("Trace colos")
			if warp != "" || gateway != "" || len(colos) > 0 {
				x.HTTP.ExpectedTrace = &model.TraceExpectations{Warp: warp, Gateway: gateway, Colos: colos}
			} else {
				x.HTTP.ExpectedTrace = nil
			}
			pops := f.list("Expected pops")
			if len(pops) > 16 {
				return errors.New("at most 16 expected pops")
			}
			seenPops := map[string]bool{}
			for _, pop := range pops {
				if !model.IsColo(pop) || seenPops[pop] {
					return errors.New("expected pops must be three uppercase letters, without duplicates")
				}
				seenPops[pop] = true
			}
			if len(pops) == 0 {
				x.HTTP.ExpectedPops = nil
			} else {
				x.HTTP.ExpectedPops = pops
			}
			// A map value is a variable name, never a value: the topology
			// stays free of the secret it names.
			envs := map[string]string{}
			named := map[string]bool{}
			for _, entry := range f.list("Secret request headers") {
				name, env, ok := strings.Cut(entry, "=")
				name, env = strings.TrimSpace(name), strings.TrimSpace(env)
				if !ok || name == "" || env == "" {
					return errors.New("Secret request headers must be Name=ENV_VAR entries")
				}
				if named[strings.ToLower(name)] {
					return errors.New("duplicate secret request header name")
				}
				named[strings.ToLower(name)] = true
				envs[name] = env
			}
			if len(envs) == 0 {
				x.HTTP.RequestHeaderEnvs = nil
			} else {
				x.HTTP.RequestHeaderEnvs = envs
			}
			if x.Kind == "cloudflare" {
				if err := cloudflareFieldsCommit(f, &x); err != nil {
					return err
				}
			}
			if err := cloudflaredFieldsCommit(f, &x); err != nil {
				return err
			}
			host, err := hostFields(f, x.Kind)
			if err != nil {
				return err
			}
			x.Host = host
			c.Checks, err = upsert(c.Checks, id, x, func(n model.Check) string { return n.ID })
			return err
		}
	default:
		return
	}
	if id != "" {
		f.focus = 1
	}
	m.modal = &modal{kind: "form", title: f.title, form: f}
}

// A small gazetteer of common data-centre and home locations. It only fills
// coordinates the operator could type; nothing is inferred from addresses.
type city struct {
	name     string
	lat, lon float64
	zh       string
}

var gazetteer = []city{
	{"Shanghai", 31.23, 121.47, "上海"}, {"Beijing", 39.9, 116.4, "北京"}, {"Hangzhou", 30.27, 120.15, "杭州"},
	{"Shenzhen", 22.54, 114.06, "深圳"}, {"Guangzhou", 23.13, 113.26, "广州"}, {"Chengdu", 30.66, 104.07, "成都"},
	{"Nanjing", 32.06, 118.8, "南京"}, {"Wuhan", 30.59, 114.3, "武汉"}, {"Qingdao", 36.07, 120.38, "青岛"},
	{"Zhangjiakou", 40.77, 114.88, "张家口"}, {"Ulanqab", 41.03, 113.13, "乌兰察布"}, {"Hong Kong", 22.32, 114.17, "香港"},
	{"Taipei", 25.03, 121.57, "台北"}, {"Tokyo", 35.68, 139.69, "东京"}, {"Osaka", 34.69, 135.5, "大阪"},
	{"Seoul", 37.57, 126.98, "首尔"}, {"Singapore", 1.35, 103.82, "新加坡"}, {"Kuala Lumpur", 3.14, 101.69, "吉隆坡"},
	{"Jakarta", -6.2, 106.85, "雅加达"}, {"Bangkok", 13.76, 100.5, "曼谷"}, {"Manila", 14.6, 120.98, "马尼拉"},
	{"Mumbai", 19.08, 72.88, "孟买"}, {"Sydney", -33.87, 151.21, "悉尼"}, {"Melbourne", -37.81, 144.96, "墨尔本"},
	{"Auckland", -36.85, 174.76, "奥克兰"}, {"Dubai", 25.2, 55.27, "迪拜"}, {"Istanbul", 41.01, 28.98, "伊斯坦布尔"},
	{"Moscow", 55.76, 37.62, "莫斯科"}, {"Frankfurt", 50.11, 8.68, "法兰克福"}, {"Amsterdam", 52.37, 4.9, "阿姆斯特丹"},
	{"London", 51.51, -0.13, "伦敦"}, {"Paris", 48.86, 2.35, "巴黎"}, {"Dublin", 53.35, -6.26, "都柏林"},
	{"Stockholm", 59.33, 18.07, "斯德哥尔摩"}, {"Helsinki", 60.17, 24.94, "赫尔辛基"}, {"Warsaw", 52.23, 21.01, "华沙"},
	{"Zurich", 47.38, 8.54, "苏黎世"}, {"Milan", 45.46, 9.19, "米兰"}, {"Madrid", 40.42, -3.7, "马德里"},
	{"Johannesburg", -26.2, 28.05, "约翰内斯堡"}, {"Lagos", 6.52, 3.38, "拉各斯"}, {"Cairo", 30.04, 31.24, "开罗"},
	{"Ashburn", 39.04, -77.49, "阿什本"}, {"Virginia", 38.95, -77.45, "弗吉尼亚"}, {"New York", 40.71, -74.01, "纽约"},
	{"Chicago", 41.88, -87.63, "芝加哥"}, {"Dallas", 32.78, -96.8, "达拉斯"}, {"Atlanta", 33.75, -84.39, "亚特兰大"},
	{"Miami", 25.76, -80.19, "迈阿密"}, {"Toronto", 43.65, -79.38, "多伦多"}, {"Montreal", 45.5, -73.57, "蒙特利尔"},
	{"Los Angeles", 34.05, -118.24, "洛杉矶"}, {"San Jose", 37.34, -121.89, "圣何塞"}, {"San Francisco", 37.77, -122.42, "旧金山"},
	{"Fremont", 37.55, -121.99, "弗里蒙特"}, {"Seattle", 47.61, -122.33, "西雅图"}, {"Portland", 45.52, -122.68, "波特兰"},
	{"Phoenix", 33.45, -112.07, "凤凰城"}, {"Mexico City", 19.43, -99.13, "墨西哥城"}, {"São Paulo", -23.55, -46.63, "圣保罗"},
	{"Santiago", -33.45, -70.67, "圣地亚哥"}, {"Buenos Aires", -34.6, -58.38, "布宜诺斯艾利斯"},
}

func lookupPlace(s string) (city, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return city{}, false
	}
	for _, p := range gazetteer {
		if strings.ToLower(p.name) == s || p.zh == s {
			return p, true
		}
	}
	for _, p := range gazetteer {
		if strings.HasPrefix(s, strings.ToLower(p.name)) || strings.Contains(s, p.zh) {
			return p, true
		}
	}
	return city{}, false
}

// cloudflareFields adds the API adapter's authoring fields. Every field is
// kept on the form whatever the read is; commit writes only the fields the
// chosen read owns, so switching reads cannot leave another read's identifiers
// in the contract and the save-time validation sees a clean declaration.
func cloudflareFields(f *Form, x model.Check, when func(*Form) bool) {
	cf := model.CloudflareOptions{}
	if x.Adapter.Cloudflare != nil {
		cf = *x.Adapter.Cloudflare
	}
	connections := ""
	if cf.ExpectedConnections != nil {
		connections = strconv.Itoa(*cf.ExpectedConnections)
	}
	proxied := ""
	if cf.ExpectedProxied != nil {
		proxied = strconv.FormatBool(*cf.ExpectedProxied)
	}
	f.add("Cloudflare token env", x.Adapter.TokenEnv, fText, "cloudflare: worker variable holding the API token").when = when
	f.add("Cloudflare read", cf.Read, fChoice, "cloudflare: one read per check", "", "tunnel", "dns_record", "pool").when = when
	f.add("Cloudflare account id", cf.AccountID, fText, "cloudflare tunnel/pool: 32 hex characters").when = when
	f.add("Cloudflare zone id", cf.ZoneID, fText, "cloudflare dns_record: 32 hex characters").when = when
	f.add("Cloudflare tunnel id", cf.TunnelID, fText, "cloudflare tunnel: 32 hex characters").when = when
	f.add("Cloudflare pool id", cf.PoolID, fText, "cloudflare pool: 32 hex characters").when = when
	f.add("Cloudflare record name", cf.RecordName, fText, "cloudflare dns_record: DNS record name").when = when
	f.add("Cloudflare record type", cf.RecordType, fChoice, "cloudflare dns_record: record type", "", "A", "AAAA", "CNAME").when = when
	f.add("Cloudflare expected connections", connections, fText, "cloudflare tunnel: 1…64").when = when
	f.add("Cloudflare expected colos", strings.Join(cf.ExpectedColos, ", "), fList, "cloudflare tunnel: e.g. HKG, NRT").when = when
	f.add("Cloudflare expected proxied", proxied, fChoice, "cloudflare dns_record: true or false", "", "true", "false").when = when
	f.add("Cloudflare expected content", cf.ExpectedContent, fText, "cloudflare dns_record: compared in memory only").when = when
	f.add("Cloudflare expected origins", strings.Join(cf.ExpectedOrigins, ", "), fList, "cloudflare pool: declared origin addresses").when = when
}

// cloudflareFieldsCommit writes the selected read's options from the form.
// Fields of other reads are cleared rather than carried, because validation
// refuses a field that belongs to a different read. No API token itself is
// ever on the form, only the name of the variable that holds it.
func cloudflareFieldsCommit(f *Form, x *model.Check) error {
	read := f.val("Cloudflare read")
	if read == "" {
		return errors.New("Cloudflare read must be tunnel, dns_record or pool")
	}
	x.Adapter.TokenEnv = f.val("Cloudflare token env")
	cf := &model.CloudflareOptions{Read: read}
	switch read {
	case "tunnel":
		cf.AccountID, cf.TunnelID = f.val("Cloudflare account id"), f.val("Cloudflare tunnel id")
		if value := f.val("Cloudflare expected connections"); value != "" {
			n, err := f.num("Cloudflare expected connections")
			if err != nil {
				return err
			}
			cf.ExpectedConnections = &n
		}
		cf.ExpectedColos = f.list("Cloudflare expected colos")
	case "dns_record":
		cf.ZoneID, cf.RecordName, cf.RecordType = f.val("Cloudflare zone id"), f.val("Cloudflare record name"), f.val("Cloudflare record type")
		switch f.val("Cloudflare expected proxied") {
		case "true":
			value := true
			cf.ExpectedProxied = &value
		case "false":
			value := false
			cf.ExpectedProxied = &value
		}
		cf.ExpectedContent = f.val("Cloudflare expected content")
	case "pool":
		cf.AccountID, cf.PoolID = f.val("Cloudflare account id"), f.val("Cloudflare pool id")
		cf.ExpectedOrigins = f.list("Cloudflare expected origins")
	default:
		return errors.New("Cloudflare read must be tunnel, dns_record or pool")
	}
	x.Adapter.Cloudflare = cf
	return nil
}

// cloudflaredFields adds the connector adapter's authoring fields: the edge
// sessions the deployment declares and the colos they may be held at. The
// connector's listener needs no credential, so the target is all else it takes.
func cloudflaredFields(f *Form, x model.Check, when func(*Form) bool) {
	cf := x.Adapter.CloudflaredOptions()
	connections := ""
	if cf.ExpectedConnections != nil {
		connections = strconv.Itoa(*cf.ExpectedConnections)
	}
	f.add("Cloudflared expected connections", connections, fText, "cloudflared: edge sessions the deployment declares, 1…8").when = when
	f.add("Cloudflared expected colos", strings.Join(cf.ExpectedColos, ", "), fList, "cloudflared: colos its sessions may be held at, e.g. hkg, sin").when = when
}

// cloudflaredFieldsCommit writes the connector's options from the form when
// the check is a connector check, and clears them on any other kind, so a
// check that stops being one does not carry a declaration nothing reads.
// Whether a count or a colo is acceptable is for the save-time validation to
// say, so the form shows the reason it gives.
func cloudflaredFieldsCommit(f *Form, x *model.Check) error {
	if x.Kind != "cloudflared" {
		x.Adapter.Cloudflared = nil
		return nil
	}
	cf := &model.CloudflaredOptions{}
	if value := f.val("Cloudflared expected connections"); value != "" {
		n, err := f.num("Cloudflared expected connections")
		if err != nil {
			return err
		}
		cf.ExpectedConnections = &n
	}
	cf.ExpectedColos = f.list("Cloudflared expected colos")
	if cf.ExpectedConnections == nil && len(cf.ExpectedColos) == 0 {
		cf = nil
	}
	x.Adapter.Cloudflared = cf
	return nil
}
