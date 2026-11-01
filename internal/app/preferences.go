package app

import (
	"errors"
	"flag"
	"fmt"

	"github.com/WhitenWhiten/topophany/internal/preferences"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

type preferenceFlags struct {
	language *string
	path     *string
	search   *string
	related  *bool
	values   preferences.Settings
	flags    *flag.FlagSet
}

func addPreferenceFlags(f *flag.FlagSet) *preferenceFlags {
	p := &preferenceFlags{flags: f, values: preferences.Defaults()}
	p.language = f.String("lang", localeLanguage(), "interface language: en, zh or ja")
	p.search = f.String("filter", "", "initial text filter")
	p.related = f.Bool("related-incidents", false, "group the Incidents tab by declared target")
	p.path = f.String("preferences", "", "local startup preferences file (default OS config directory/topophany/preferences.json)")
	f.StringVar(&p.values.Page, "page", "overview", "initial page: overview, topology, servers, services, paths, checks, incidents, observers, about")
	f.BoolVar(&p.values.GroupsCollapsed, "groups-collapsed", false, "start with groups collapsed (default expanded)")
	f.BoolVar(&p.values.UniformCards, "uniform-cards", false, "use equal-sized Servers cards")
	f.BoolVar(&p.values.ServiceCards, "service-cards", false, "show the Services tab as cards rather than a list")
	f.BoolVar(&p.values.FullMap, "full-map", false, "show maps without surrounding panels on Overview/Topology")
	f.StringVar(&p.values.MapMode, "map-mode", "auto", "initial map mode: auto, virtual, geo, network")
	f.BoolVar(&p.values.NerdFontIcons, "nerd-font-icons", false, "draw providers' logos; needs a Nerd Font in the terminal")
	return p
}
func (p *preferenceFlags) pathName() (string, error) {
	if *p.path != "" {
		return *p.path, nil
	}
	return preferences.DefaultPath()
}
func (p *preferenceFlags) resolve() (tui.Startup, error) { return p.resolveWith(false) }

// startup resolves the TUI's startup settings. Saved preferences that
// cannot be read keep nobody from their monitor: the built-in defaults
// stand in for them and the TUI says why. Flags given on the command line
// must still be valid.
func (p *preferenceFlags) startup() (tui.Startup, error) { return p.resolveWith(true) }

func (p *preferenceFlags) resolveWith(lenient bool) (tui.Startup, error) {
	if len(*p.search) > 256 || secure.Text(*p.search) != *p.search {
		return tui.Startup{}, errors.New("filter must be at most 256 bytes of printable text")
	}
	if preview.Language(*p.language) != *p.language {
		return tui.Startup{}, errors.New("--lang must be en, zh or ja")
	}
	path, err := p.pathName()
	if err != nil && !lenient {
		return tui.Startup{}, err
	}
	notice := ""
	s, err := preferences.Load(path)
	if err != nil {
		if !lenient {
			return tui.Startup{}, fmt.Errorf("%s: %w", path, err)
		}
		s = preferences.Defaults()
		notice = "Saved preferences ignored, defaults used (" + err.Error() + "); topo preferences reset repairs them"
	}
	overrides := []string{}
	p.flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "page":
			s.Page = p.values.Page
		case "groups-collapsed":
			s.GroupsCollapsed = p.values.GroupsCollapsed
		case "uniform-cards":
			s.UniformCards = p.values.UniformCards
		case "service-cards":
			s.ServiceCards = p.values.ServiceCards
		case "full-map":
			s.FullMap = p.values.FullMap
		case "map-mode":
			s.MapMode = p.values.MapMode
		case "nerd-font-icons":
			s.NerdFontIcons = p.values.NerdFontIcons
		default:
			return
		}
		overrides = append(overrides, f.Name)
	})
	return tui.Startup{Language: *p.language, Search: *p.search, RelatedIncidents: *p.related, Settings: s, Path: path, Overrides: overrides, Notice: notice}, s.Validate()
}
func (a *App) preferences(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(a.Out, "Usage: topo preferences show|set|reset [--preferences PATH]\nSet flags: --page PAGE --groups-collapsed=BOOL --uniform-cards=BOOL --service-cards=BOOL --full-map=BOOL --map-mode MODE --nerd-font-icons=BOOL")
		return err
	}
	command := args[0]
	if command != "show" && command != "set" && command != "reset" {
		return fmt.Errorf("unknown preferences command %q", command)
	}
	f := a.flags("preferences " + command)
	p := addPreferenceFlags(f)
	if err := parsed(f, args[1:]); err != nil {
		return err
	}
	if command != "set" {
		invalid := false
		f.Visit(func(f *flag.Flag) {
			if f.Name != "preferences" {
				invalid = true
			}
		})
		if invalid {
			return errors.New("preference overrides require 'preferences set'")
		}
	}
	if command == "reset" {
		path, err := p.pathName()
		if err != nil {
			return err
		}
		if err = preferences.Save(path, preferences.Defaults()); err != nil {
			return err
		}
		fmt.Fprintln(a.Out, "Startup preferences reset:", path)
		return nil
	}
	resolved, err := p.resolve()
	if err != nil {
		return err
	}
	if command == "show" {
		return printJSON(a.Out, resolved.Settings)
	}
	if len(resolved.Overrides) == 0 {
		return errors.New("preferences set requires at least one preference flag")
	}
	if err = preferences.Save(resolved.Path, resolved.Settings); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Startup preferences saved:", resolved.Path)
	return nil
}
