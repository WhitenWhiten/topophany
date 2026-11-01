package preferences

import (
	"bytes"
	"errors"
	"os"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type NamedView struct {
	Name     string   `json:"name"`
	Settings Settings `json:"settings"`
	Observer string   `json:"observer"`
	Search   string   `json:"search"`
	Related  bool     `json:"related_incidents"`
}
type Views struct {
	Schema int         `json:"schema"`
	Items  []NamedView `json:"views"`
}

func (v Views) Validate() error {
	if v.Schema != 1 || len(v.Items) > 64 {
		return errors.New("views require schema 1 and at most 64 entries")
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if !config.ID(item.Name) || seen[item.Name] || item.Observer != "" && !config.ID(item.Observer) || len(item.Search) > 256 || secure.Text(item.Search) != item.Search {
			return errors.New("invalid saved view name, observer or filter")
		}
		if err := item.Settings.Validate(); err != nil {
			return err
		}
		seen[item.Name] = true
	}
	return nil
}
func LoadViews(path string) (Views, error) {
	out := Views{Schema: 1, Items: []NamedView{}}
	data, err := secure.ReadBounded(path, 128<<10)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = secure.Decode(bytes.NewReader(data), &out, 128<<10); err != nil {
		return out, err
	}
	return out, out.Validate()
}
