package monitor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

const MaxConfigBytes = 256 << 10

type Config struct {
	DirectRoot  string   `json:"direct_root,omitempty"`
	Schema      int      `json:"schema"`
	Revision    string   `json:"revision"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Source      string   `json:"source"`
	Credentials string   `json:"credentials,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Project     string   `json:"project,omitempty"`
	Observer    string   `json:"observer,omitempty"`
	Output      string   `json:"output"`
	Interval    string   `json:"interval"`
	Template    Template `json:"template"`
}

func ConfigPath(root, name string) (string, error) {
	if !config.ID(name) {
		return "", errors.New("invalid monitor name")
	}
	if root == "" {
		d, e := os.UserConfigDir()
		if e != nil {
			return "", e
		}
		root = filepath.Join(d, "topophany", "monitor")
	}
	return filepath.Abs(filepath.Join(root, name, "config.json"))
}
func OutputPath(name string) (string, error) {
	if !config.ID(name) {
		return "", errors.New("invalid monitor name")
	}
	d, e := os.UserCacheDir()
	if e != nil {
		return "", e
	}
	return filepath.Abs(filepath.Join(d, "topophany", "monitor", name, "metrics.json"))
}
func (c Config) Every() time.Duration { d, _ := time.ParseDuration(c.Interval); return d }
func (c Config) Validate() error {
	if c.Schema != 1 || !config.ID(c.Name) {
		return errors.New("invalid monitor config schema/name")
	}
	if c.Every() < time.Second || c.Every() > time.Hour {
		return errors.New("monitor interval must be 1s..1h")
	}
	if !filepath.IsAbs(c.Output) || filepath.Ext(c.Output) != ".json" {
		return errors.New("monitor output must be an absolute .json path")
	}
	if c.Source == "client" && (!filepath.IsAbs(c.Credentials) || !config.ID(c.Profile)) {
		return errors.New("client monitor requires absolute credentials path and a named profile")
	}
	if c.DirectRoot != "" && !filepath.IsAbs(c.DirectRoot) {
		return errors.New("direct_root must be absolute")
	}
	if c.Source == "direct" && (!config.ID(c.Profile) || c.Credentials != "") {
		return errors.New("direct monitor requires a named profile and no client credentials")
	}
	if c.Project != "" && !config.ID(c.Project) || c.Observer != "" && !config.ID(c.Observer) {
		return errors.New("invalid monitor project/observer")
	}
	if c.Source == "local" {
		return errors.New("local checks are read by topo monitor stream only")
	}
	if c.Source == "collector" && c.Observer != "" {
		return errors.New("collector monitor uses the complete local collector view")
	}
	return c.Template.Validate(c.Source)
}
func Load(path string) (Config, error) {
	var c Config
	b, e := secure.ReadBounded(path, MaxConfigBytes)
	if e != nil {
		return c, e
	}
	e = secure.Decode(strings.NewReader(string(b)), &c, MaxConfigBytes)
	if e != nil {
		return c, e
	}
	return c, c.Validate()
}
func Save(path string, c Config) (Config, error) {
	if e := c.Validate(); e != nil {
		return c, e
	}
	c.Revision = ""
	b, e := json.Marshal(c)
	if e != nil {
		return c, e
	}
	c.Revision = secure.Hash(string(b))
	b, e = json.MarshalIndent(c, "", "  ")
	if e != nil {
		return c, e
	}
	if len(b) > MaxConfigBytes {
		return c, errors.New("monitor config exceeds size limit")
	}
	return c, secure.AtomicWrite(path, append(b, '\n'), 0600)
}
