package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/preferences"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (a *App) views(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("use views list, save, open or remove")
	}
	command := args[0]
	f := a.flags("views " + command)
	p := addPreferenceFlags(f)
	path := f.String("views-file", "", "named-view registry (default beside preferences)")
	observer := f.String("observer", "", "saved observer perspective")
	preview := f.Bool("preview", false, "open this view on synthetic evidence")
	forwarded := []string{}
	if i := slices.Index(args, "--"); i >= 0 {
		forwarded = args[i+1:]
		args = args[:i]
	}
	pos, err := parseMixed(f, args[1:])
	if err != nil {
		return err
	}
	if *path == "" {
		base, err := p.pathName()
		if err != nil {
			return err
		}
		*path = filepath.Join(filepath.Dir(base), "views.json")
	}
	_, version, err := config.ReadFileVersion(*path)
	if err != nil {
		return err
	}
	saved, err := preferences.LoadViews(*path)
	if err != nil {
		return err
	}
	if command == "list" {
		if len(pos) != 0 {
			return errors.New("list takes no view name")
		}
		return printJSON(a.Out, saved)
	}
	if len(pos) != 1 || !config.ID(pos[0]) {
		return errors.New("choose one valid view name")
	}
	index := slices.IndexFunc(saved.Items, func(v preferences.NamedView) bool { return v.Name == pos[0] })
	switch command {
	case "open":
		if index < 0 {
			return errors.New("saved view not found")
		}
		item := saved.Items[index]
		flags := []string{"--page", item.Settings.Page, "--map-mode", item.Settings.MapMode, "--observer", item.Observer, "--filter", item.Search, "--related-incidents=" + strconv.FormatBool(item.Related), "--groups-collapsed=" + strconv.FormatBool(item.Settings.GroupsCollapsed), "--uniform-cards=" + strconv.FormatBool(item.Settings.UniformCards), "--service-cards=" + strconv.FormatBool(item.Settings.ServiceCards), "--full-map=" + strconv.FormatBool(item.Settings.FullMap), "--nerd-font-icons=" + strconv.FormatBool(item.Settings.NerdFontIcons)}
		flags = append(flags, forwarded...)
		if *preview {
			return a.preview(ctx, flags)
		}
		return a.view(ctx, flags)
	case "save":
		if len(forwarded) > 0 {
			return errors.New("forwarded connection flags are only accepted by views open")
		}
		resolved, err := p.resolve()
		if err != nil {
			return err
		}
		item := preferences.NamedView{Name: pos[0], Settings: resolved.Settings, Observer: *observer, Search: *p.search, Related: *p.related}
		if index < 0 {
			saved.Items = append(saved.Items, item)
		} else {
			saved.Items[index] = item
		}
	case "remove":
		if index < 0 {
			return errors.New("saved view not found")
		}
		saved.Items = slices.Delete(saved.Items, index, index+1)
	default:
		return errors.New("use views list, save, open or remove")
	}
	if err := saved.Validate(); err != nil {
		return err
	}
	bytes, err := secure.MarshalJSON(saved, "  ")
	if err != nil {
		return err
	}
	if _, err = config.CommitFile(*path, version, append(bytes, '\n'), 0600); err != nil {
		return err
	}
	return printJSON(a.Out, saved)
}
