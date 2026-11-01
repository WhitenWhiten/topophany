package tui

import (
	"slices"
	"time"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

// Startup carries the resolved defaults and the location to save explicitly.
// Notice, when set, says why the saved defaults were not used.
type Startup struct {
	Search           string
	RelatedIncidents bool
	Settings         preferences.Settings
	Path             string
	Overrides        []string
	Notice           string
	Device           string // a device to lay the TUI out for and simulate (Devices), "" for none
}

func (m *Model) initializeStartup(options []Startup) {
	s := Startup{Settings: preferences.Defaults()}
	if len(options) > 0 {
		s = options[0]
	}
	if d, e := DeviceByID(s.Device); s.Device != "" && e == nil {
		m.Device = d
	}
	m.PreferencesPath = s.Path
	m.PreferenceOverrides = s.Overrides
	m.applyPreferences(s.Settings)
	m.Search = s.Search
	m.RelatedIncidents = s.RelatedIncidents
	for i, field := range preferenceForm(s.Settings).fields {
		m.preferenceInitial[i] = field.value
	}
	if s.Notice != "" {
		if m.Now.IsZero() {
			m.Now = time.Now()
		}
		m.say(s.Notice, true)
	}
}
func (m *Model) applyPreferences(s preferences.Settings) {
	m.setTab(slices.Index(preferences.Pages, s.Page))
	m.GroupsCollapsed = s.GroupsCollapsed
	m.Expanded = nil
	m.Uniform = s.UniformCards
	m.ServiceCards = s.ServiceCards
	m.FullMap = s.FullMap
	m.MapMode = s.MapMode
	m.NerdFont = s.NerdFontIcons
	if m.MapMode == "auto" {
		m.MapMode = ""
	}
}
func (m *Model) currentPreferences() preferences.Settings {
	mode := m.MapMode
	if mode == "" {
		mode = "auto"
	}
	return preferences.Settings{Page: preferences.Pages[m.Tab], GroupsCollapsed: m.GroupsCollapsed, UniformCards: m.Uniform, ServiceCards: m.ServiceCards, FullMap: m.FullMap, MapMode: mode, NerdFontIcons: m.NerdFont}
}
