package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type ComparedHistory struct {
	Check    string         `json:"check"`
	Observer string         `json:"observer"`
	Window   string         `json:"window"`
	History  client.Rollups `json:"history"`
}

func (a *App) compare(ctx context.Context, args []string) error {
	f := a.flags("compare")
	connect := a.connection(f)
	checks := f.String("checks", "", "comma-separated check IDs, at most 8")
	service := f.String("service", "", "select a service's checks (at most 8)")
	step := f.String("step", "5m", "5m, 1h or 1d")
	span := f.String("span", "1h", "duration of each comparison window")
	until := f.String("until", "", "fixed RFC3339 window end; defaults to collector snapshot time")
	around := f.String("around", "", "RFC3339 incident time; compare equal windows before and after")
	incident := f.String("incident", "", "incident ID; compare around its start")
	asJSON := f.Bool("json", false, "structured comparison with original coverage and contract metadata")
	if err := parsed(f, args); err != nil {
		return err
	}
	width, length, err := layout(*step, *span)
	if err != nil {
		return err
	}
	if *checks != "" && *service != "" {
		return errors.New("choose --checks or --service")
	}
	if *around != "" && *incident != "" || *until != "" && (*around != "" || *incident != "") {
		return errors.New("choose --until, --around or --incident")
	}
	c, err := connect()
	if err != nil {
		return err
	}
	defer c.Close()
	snap, server, err := c.Snapshot(ctx, "")
	if err != nil {
		return err
	}
	if view.Expired(snap, a.now()) {
		return errors.New("comparison requires a current snapshot to bind the collector and contracts")
	}
	observers := map[string]string{}
	ids := []string{}
	for _, x := range snap.Topology.Checks {
		observers[x.ID] = x.Observer
		if *service != "" && x.Service == *service {
			ids = append(ids, x.ID)
		}
	}
	if *checks != "" {
		ids = strings.Split(*checks, ",")
	}
	if *incident != "" {
		found := false
		for _, i := range snap.Incidents {
			if i.ID == *incident {
				*around = i.Since.Format(time.RFC3339)
				if len(ids) == 0 {
					ids = i.Checks
				}
				found = true
				break
			}
		}
		if !found {
			for _, i := range snap.Ledger {
				if i.ID == *incident {
					*around = i.Since.Format(time.RFC3339)
					if len(ids) == 0 {
						ids = i.Checks
					}
					found = true
					break
				}
			}
		}
		if !found {
			return errors.New("incident not in the snapshot; specify --around and --checks for older evidence")
		}
	}
	if len(ids) < 1 || len(ids) > 8 {
		return errors.New("select 1..8 checks with --checks, --service or --incident")
	}
	seen := map[string]bool{}
	for i, id := range ids {
		id = strings.TrimSpace(id)
		if !config.ID(id) || observers[id] == "" || seen[id] {
			return errors.New("comparison contains an unknown or repeated check")
		}
		seen[id] = true
		ids[i] = id
	}
	end := snap.GeneratedAt.Truncate(time.Second)
	if *until != "" {
		end, err = time.Parse(time.RFC3339, *until)
		if err != nil {
			return errors.New("--until requires RFC3339")
		}
	}
	ends := []time.Time{end}
	names := []string{"selected"}
	if *around != "" {
		anchor, e := time.Parse(time.RFC3339, *around)
		if e != nil {
			return errors.New("--around requires RFC3339")
		}
		anchor = anchor.Truncate(width)
		ends = []time.Time{anchor, anchor.Add(length)}
		names = []string{"before", "after"}
	}
	for _, end := range ends {
		if end.After(snap.GeneratedAt) {
			return errors.New("comparison window has not elapsed; choose a shorter --span or an earlier time")
		}
	}
	result := struct {
		Schema int               `json:"schema"`
		Series []ComparedHistory `json:"series"`
		Note   string            `json:"note"`
	}{1, []ComparedHistory{}, "All series use one collector, snapshot contracts and fixed window ends. Buckets are aggregate estimates; unmeasured or unreconciled time is not uptime. Historical resolution follows retention. Before/after boundaries align to the selected step."}
	for i, end := range ends {
		for _, id := range ids {
			h, _, e := c.RollupsAt(ctx, client.SnapshotHistory(snap, server, id), *step, *span, end)
			if e != nil {
				return e
			}
			result.Series = append(result.Series, ComparedHistory{id, observers[id], names[i], h})
		}
	}
	if *asJSON {
		return printJSON(a.Out, result)
	}
	t := &text{w: a.Out}
	t.line(result.Note)
	for _, series := range result.Series {
		t.line()
		t.line(series.Window + " · " + series.Check + " · observer " + series.Observer)
		t.kv("Window", series.History.Total.Start.Format(time.RFC3339)+" → "+series.History.WindowEnd.Format(time.RFC3339))
		t.kv("Summary", view.RollupLine(series.History.Total))
		for _, slot := range series.History.Slots {
			t.line(fmt.Sprintf("%s  %s", slot.Start.Format(time.RFC3339), view.RollupLine(slot)))
		}
	}
	return t.err
}
