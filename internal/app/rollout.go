package app

import (
	"context"
	"fmt"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
)

type RolloutCollector struct {
	Address        int             `json:"address_index"`
	Instance       string          `json:"instance,omitempty"`
	Code           string          `json:"code,omitempty"`
	PublicRevision string          `json:"public_revision,omitempty"`
	Matches        bool            `json:"matches_reviewed_public_configuration"`
	Changes        *EditPlan       `json:"public_changes,omitempty"`
	Workers        []RolloutWorker `json:"workers"`
}
type RolloutWorker struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

func assessRollout(c model.Config, s model.Snapshot, now time.Time) RolloutCollector {
	out := RolloutCollector{Instance: s.Instance, PublicRevision: s.Revision, Workers: []RolloutWorker{}}
	if view.Expired(s, now) {
		out.Code = "snapshot_expired"
		return out
	}
	out.Matches = s.Revision == config.PublicRevision(c)
	changes := planEdit(s.Topology, config.Public(c))
	out.Changes = &changes
	for _, o := range s.Observers {
		state := "unverified"
		switch {
		case o.Health != "up":
			state = "silent"
		case o.Contracts == "outdated":
			state = "outdated"
		case o.Contracts == "current" && out.Matches:
			state = "matches_collector_contracts"
		case o.Contracts == "current":
			state = "matches_old_collector_contracts"
		}
		out.Workers = append(out.Workers, RolloutWorker{o.ID, state})
	}
	return out
}
func (a *App) rollout(ctx context.Context, args []string) error {
	f := a.flags("rollout")
	connect := a.connection(f)
	path := f.String("config", "topology.json", "reviewed desired topology")
	asJSON := f.Bool("json", false, "report collector and worker agreement as JSON")
	if err := parsed(f, args); err != nil {
		return err
	}
	desired, err := config.Load(*path)
	if err != nil {
		return err
	}
	c, err := connect()
	if err != nil {
		return err
	}
	defer c.Close()
	report := struct {
		Schema     int                `json:"schema"`
		Expected   string             `json:"expected_public_revision"`
		Collectors []RolloutCollector `json:"collectors"`
		Limit      string             `json:"verification_limit"`
	}{1, config.PublicRevision(desired), []RolloutCollector{}, "Viewer reads verify public configuration and reported measurement-contract agreement. They cannot verify private credentials or a worker's exact file revision. Address indices may be aliases of the same collector instance."}
	code := 0
	for i := 0; i < c.AddressCount(); i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		snap, err := c.SnapshotAt(attempt, i)
		cancel()
		row := RolloutCollector{Address: i + 1, Workers: []RolloutWorker{}}
		if err != nil {
			row.Code = client.FailureCode(err)
			code = 2
		} else if snap.Project != desired.Project {
			row.Code = "project_mismatch"
			code = 2
		} else {
			row = assessRollout(desired, snap, a.now())
			row.Address = i + 1
			if !row.Matches || row.Code != "" {
				code = 2
			}
			for _, worker := range row.Workers {
				if worker.State != "matches_collector_contracts" {
					code = 2
				}
			}
		}
		report.Collectors = append(report.Collectors, row)
	}
	if *asJSON {
		err = printJSON(a.Out, report)
	} else {
		fmt.Fprintln(a.Out, report.Limit)
		for _, c := range report.Collectors {
			fmt.Fprintf(a.Out, "Address %d · %s · public config matches=%t · %s\n", c.Address, c.Instance, c.Matches, c.Code)
			for _, w := range c.Workers {
				fmt.Fprintf(a.Out, "  %s: %s\n", w.ID, w.State)
			}
		}
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
