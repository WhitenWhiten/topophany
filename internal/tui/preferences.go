package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

var preferenceNames = []string{"page", "mode", "groups-collapsed", "uniform-cards", "service-cards", "full-map", "map-mode", "nerd-font-icons"}

// prefFields is how many preferences the dialog lists; its three buttons
// follow them.
const prefFields = 8

func preferenceForm(s preferences.Settings) *Form {
	f := &Form{}
	f.add("Startup page", s.Page, fChoice, "", preferences.Pages...)
	mode := s.Mode
	if mode == "" {
		mode = "network"
	}
	f.add("Overview", mode, fChoice, "", preferences.Modes...)
	groups := "expanded"
	if s.GroupsCollapsed {
		groups = "collapsed"
	}
	f.add("Initial groups", groups, fChoice, "", "expanded", "collapsed")
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	f.add("Uniform cards", yes(s.UniformCards), fBool, "")
	f.add("Service cards", yes(s.ServiceCards), fBool, "")
	f.add("Full map", yes(s.FullMap), fBool, "")
	f.add("Map mode", s.MapMode, fChoice, "", preferences.MapModes...)
	f.add("Nerd Font icons", yes(s.NerdFontIcons), fBool, "")
	return f
}
func formPreferences(f *Form) preferences.Settings {
	return preferences.Settings{Page: f.val("Startup page"), Mode: f.val("Overview"), GroupsCollapsed: f.val("Initial groups") == "collapsed", UniformCards: f.yes("Uniform cards"), ServiceCards: f.yes("Service cards"), FullMap: f.yes("Full map"), MapMode: f.val("Map mode"), NerdFontIcons: f.yes("Nerd Font icons")}
}
func (m *Model) openPreferences() {
	f := preferenceForm(m.currentPreferences())
	m.modal = &modal{kind: "preferences", title: "Display & startup preferences", form: f}
	for i, field := range f.fields {
		if slices.Contains(m.PreferenceOverrides, preferenceNames[i]) && field.value == m.preferenceInitial[i] {
			m.modal.lines = append(m.modal.lines, preferenceNames[i])
		}
	}
}
func (m *Model) preferencesKey(k string) string {
	f := m.modal.form
	switch k {
	case "esc":
		m.modal = nil
		return ""
	case "ctrl-c":
		return "quit"
	case "tab", "down", "wheel-down":
		f.focus = (f.focus + 1) % (prefFields + 3)
		return ""
	case "shift-tab", "up", "wheel-up":
		f.focus = (f.focus + prefFields + 2) % (prefFields + 3)
		return ""
	case "enter":
		if f.focus < prefFields {
			f.focus++
			return ""
		}
		k = []string{"pref:apply", "pref:save", "pref:reset"}[f.focus-prefFields]
	}
	if strings.HasPrefix(k, "pref:field:") {
		i, err := strconv.Atoi(strings.TrimPrefix(k, "pref:field:"))
		if err == nil && i >= 0 && i < prefFields {
			f.focus = i
			f.key("right")
			m.modal.lines = slices.DeleteFunc(m.modal.lines, func(name string) bool { return name == preferenceNames[i] })
		}
		return ""
	}
	switch k {
	case "pref:reset":
		m.modal.lines = nil
		m.modal.form = preferenceForm(preferences.Defaults())
		m.modal.form.note = "Built-in defaults in form; apply or save to confirm."
		return ""
	case "pref:apply", "pref:save":
		s := formPreferences(f)
		if err := s.Validate(); err != nil {
			f.err = err.Error()
			return ""
		}
		if k == "pref:save" {
			path := m.PreferencesPath
			if path == "" {
				var err error
				path, err = preferences.DefaultPath()
				if err != nil {
					f.err = err.Error()
					return ""
				}
			}
			if err := preferences.Save(path, s); err != nil {
				f.err = err.Error()
				return ""
			}
			m.PreferencesPath = path
			// Saving changes future launches only; applying is a separate action.
			m.modal = nil
			m.say("Startup defaults saved; current session unchanged", false)
		} else {
			m.applyPreferences(s)
			m.PreferenceOverrides = nil
			m.modal = nil
			m.say("Display preferences applied to this session only", false)
		}
		return ""
	}
	if f.focus < prefFields {
		before := f.fields[f.focus].value
		f.key(k)
		if f.fields[f.focus].value != before {
			f.note = ""
			m.modal.lines = slices.DeleteFunc(m.modal.lines, func(name string) bool { return name == preferenceNames[f.focus] })
		}
	}
	return ""
}

// prefHelp says what each preference and each button does, in the order
// the dialog lists them.
var prefHelp = []string{
	"The tab the TUI opens on.",
	"What the Overview shows first: the network's map and what needs attention, or your products, a row for each line (P).",
	"Whether Services and Checks open with their groups expanded or folded.",
	"Servers as a grid of cards of one size rather than a wall of full cards (s).",
	"Services as a card each, under their groups, rather than a list (s).",
	"Overview and Topology open with the map alone, without the panels (f).",
	"The map shown first; auto picks the virtual geo map when places are declared (m).",
	"Providers' logos before their names. Needs a Nerd Font in the terminal.",
	"Use these settings now, in this session only.",
	"Open with these settings from the next launch on; this session stays as it is.",
	"Put the built-in defaults in the form; apply or save them to use them.",
}

func (m *Model) drawPreferences(cv *Canvas) {
	f := m.modal.form
	x, y, w, h := m.modalBox(cv, 74, prefFields+9, m.modal.title)
	bg := Style{BG: cPanelBG}
	message := "Apply changes this session; Save keeps them for every launch."
	if len(m.modal.lines) > 0 {
		message = "* CLI override, for this launch only"
	}
	if f.note != "" {
		message = f.note
	}
	cv.Text(x, y, ellipsis(message, w), bg.Fg(cMuted), w)
	labelW := 0
	for _, fd := range f.fields {
		labelW = max(labelW, width(fd.label)+2)
	}
	vx := x + labelW + 2
	for i := 0; i < prefFields; i++ {
		ry := y + 2 + i
		if ry >= y+h-4 {
			break
		}
		fd := f.fields[i]
		focused := i == f.focus
		ls, vs := bg.Fg(cMuted), bg.Fg(cText)
		if focused {
			cv.Fill(x-1, ry, w+2, 1, Style{BG: cSelBG})
			cv.Set(x-1, ry, '▌', fg(cAccent).Bg(cSelBG))
			ls, vs = Style{FG: cAccent, BG: cSelBG}.bold(), Style{FG: cGlow, BG: cSelBG}.bold()
		}
		label := fd.label
		if slices.Contains(m.modal.lines, preferenceNames[i]) {
			label += " *"
		}
		cv.Text(x+1, ry, ellipsis(label, labelW), ls, labelW)
		value := "‹ " + fd.value + " ›"
		if fd.kind == fBool {
			value = map[bool]string{true: "● yes", false: "○ no"}[fd.value == "yes"]
		}
		cv.Text(vx, ry, ellipsis(value, x+w-vx), vs, x+w-vx)
		m.hit(x, ry, w, "pref:field:"+strconv.Itoa(i))
	}
	// What the focused item does, or what went wrong.
	hy := y + h - 3
	switch {
	case f.err != "":
		cv.Text(x, hy, ellipsis("✖ "+f.err, w), bg.Fg(cDown).bold(), w)
	case f.focus < len(prefHelp):
		cv.Text(x, hy, ellipsis(prefHelp[f.focus], w), bg.Fg(cAccent2), w)
	}
	bx := x
	for i, b := range []struct{ label, action string }{{"Apply now", "pref:apply"}, {"Save as startup defaults", "pref:save"}, {"Restore defaults", "pref:reset"}} {
		bx += m.button(cv, bx, y+h-1, b.label, b.action, f.focus == prefFields+i) + 2
	}
}
