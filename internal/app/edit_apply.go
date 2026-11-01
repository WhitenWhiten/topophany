package app

import (
	"errors"
	"sort"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

type EditPlan struct {
	Before        string   `json:"before_revision"`
	After         string   `json:"after_revision"`
	Changed       bool     `json:"configuration_changed"`
	ChangedChecks []string `json:"changed_measurements"`
	AddedChecks   []string `json:"added_measurements"`
	RemovedChecks []string `json:"removed_measurements"`
	Applied       bool     `json:"applied"`
}

func planEdit(before, after model.Config) EditPlan {
	p := EditPlan{Before: config.Revision(before), After: config.Revision(after), ChangedChecks: []string{}, AddedChecks: []string{}, RemovedChecks: []string{}}
	p.Changed = p.Before != p.After
	old, next := config.Contracts(before), config.Contracts(after)
	for id, value := range old {
		if newer, ok := next[id]; !ok {
			p.RemovedChecks = append(p.RemovedChecks, id)
		} else if newer != value {
			p.ChangedChecks = append(p.ChangedChecks, id)
		}
	}
	for id := range next {
		if _, ok := old[id]; !ok {
			p.AddedChecks = append(p.AddedChecks, id)
		}
	}
	sort.Strings(p.ChangedChecks)
	sort.Strings(p.AddedChecks)
	sort.Strings(p.RemovedChecks)
	return p
}
func (a *App) applyEdit(path string, current model.Config, candidate, expected, expectedCandidate string, dry bool) error {
	editor, err := tui.NewEditor(path, current)
	if err != nil {
		return err
	}
	next, err := config.Load(candidate)
	if err != nil {
		return err
	}
	if next.Project != current.Project {
		return errors.New("batch edit cannot change project identity")
	}
	plan := planEdit(current, next)
	if expected != "" && expected != plan.Before {
		return config.ErrFileChanged
	}
	if !dry {
		if expectedCandidate != plan.After {
			return errors.New("apply requires --expect-candidate-revision matching the reviewed replacement")
		}
		if expected == "" {
			return errors.New("apply requires --expect-revision from a reviewed --dry-run")
		}
		if err := editor.Apply(func(c *model.Config) error { *c = next; return nil }); err != nil {
			return err
		}
		if _, err := editor.Save(); err != nil {
			return err
		}
		plan.Applied = true
	}
	return printJSON(a.Out, plan)
}
