package app

import (
	"encoding/json"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/config"
	"os"
	"sort"
	"strings"
)

// address edits only a reviewed local registry, reporting the precise checks
// that begin a new series. It never reloads remote collectors or workers.
func (a *App) address(args []string) error {
	f := a.flags("address")
	path := f.String("config", "topology.json", "reviewed local topology")
	set := f.String("set", "", "address definition NAME=IP_OR_HOSTNAME")
	remove := f.String("delete", "", "delete an unused address definition")
	dry := f.Bool("dry-run", false, "validate the change and list affected measurements without writing")
	if e := parsed(f, args); e != nil {
		return e
	}
	if *set != "" && *remove != "" {
		return errors.New("choose set or delete")
	}
	_, base, e := config.ReadFileVersion(*path)
	if e != nil {
		return e
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	before := config.Contracts(c)
	if *set != "" {
		name, value, ok := strings.Cut(*set, "=")
		if !ok || !config.ID(name) {
			return errors.New("set needs NAME=IP_OR_HOSTNAME")
		}
		if c.Addresses == nil {
			c.Addresses = map[string]string{}
		}
		c.Addresses[name] = value
	}
	if *remove != "" {
		if _, ok := c.Addresses[*remove]; !ok {
			return errors.New("unknown address definition")
		}
		delete(c.Addresses, *remove)
	}
	if e = config.Validate(c); e != nil {
		return e
	}
	changed := []string{}
	for id, contract := range config.Contracts(c) {
		if before[id] != contract {
			changed = append(changed, id)
		}
	}
	sort.Strings(changed)
	if (*set != "" || *remove != "") && !*dry {
		info, e := os.Stat(*path)
		if e != nil {
			return e
		}
		b, e := json.MarshalIndent(c, "", "  ")
		if e != nil {
			return e
		}
		if _, e = config.CommitFile(*path, base, append(b, '\n'), config.WriteMode(c, info.Mode())); e != nil {
			return e
		}
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Addresses map[string]string `json:"addresses"`
		Changed   []string          `json:"changed_checks"`
		Revision  string            `json:"revision"`
		Dry       bool              `json:"dry_run"`
	}{c.Addresses, changed, config.Revision(c), *dry})
}
