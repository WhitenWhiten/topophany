// Package alert tells people what a collector concludes: an incident that
// opens or closes, an observer that falls silent or reports again. It posts
// to the webhooks named in the collector's private alerts file, outbound
// only, as generic JSON or in the formats of ntfy, Slack and Discord. An
// alert is what the collector concluded, never more evidence than its
// snapshot holds. Alerts wait in a bounded queue per webhook, kept in the
// collector's data directory, so one that stops or reloads sends them
// still, at least once.
package alert

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Config is a collector's alerts file. It names webhook URLs, which are
// secrets, so like auth.json it is private to the collector: never part
// of the reviewed topology that workers and viewers receive.
type Config struct {
	Schema   int       `json:"schema"`
	Webhooks []Webhook `json:"webhooks"`
}

// Webhook is where alerts go. Events are incidents, observers or both
// (when empty); Observers, when set, limit them to what those observers
// saw. Bearer, when set, is sent as an Authorization header. Plain HTTP
// is refused except to a literal loopback address or with AllowInsecure.
type Webhook struct {
	PrimaryInstance string        `json:"primary_instance,omitempty"`
	QuietUntil      time.Time     `json:"quiet_until,omitzero"`
	Windows         []QuietWindow `json:"maintenance_windows,omitempty"`
	Services        []string      `json:"services,omitempty"`
	serviceChecks   []string
	ID              string   `json:"id"`
	Format          string   `json:"format"` // json, ntfy, slack, discord
	URL             string   `json:"url"`
	Bearer          string   `json:"bearer,omitempty"`
	Events          []string `json:"events,omitempty"`
	Observers       []string `json:"observers,omitempty"`
	AllowInsecure   bool     `json:"allow_insecure,omitempty"`
}

// Formats and event groups a webhook may name.
var (
	Formats = []string{"json", "ntfy", "slack", "discord"}
	Groups  = []string{"incidents", "observers"}
)

const maxWebhooks = 16

// Load reads and checks the alerts file at path against topology c. Like
// auth.json it must be a private regular file.
func Load(path string, c model.Config) (Config, error) {
	var a Config
	b, e := secure.PrivateRead(path)
	if e != nil {
		return a, e
	}
	if e = secure.Decode(strings.NewReader(string(b)), &a, 1<<20); e != nil {
		return a, e
	}
	if err := Validate(a, c); err != nil {
		return a, err
	}
	return BindServices(a, c), nil
}

// Validate checks an alerts file against topology c.
func Validate(a Config, c model.Config) error {
	if a.Schema != 1 || len(a.Webhooks) == 0 || len(a.Webhooks) > maxWebhooks {
		return errors.New("alerts require schema 1 and 1..16 webhooks")
	}
	observers := map[string]bool{}
	for _, o := range c.Observers {
		observers[o.ID] = true
	}
	seen := map[string]bool{}
	for _, w := range a.Webhooks {
		if err := validatePolicy(w, c); err != nil {
			return err
		}
		if !config.ID(w.ID) || seen[w.ID] {
			return errors.New("webhook IDs must be valid and unique")
		}
		seen[w.ID] = true
		if !slices.Contains(Formats, w.Format) {
			return fmt.Errorf("webhook %s: format must be one of %s", w.ID, strings.Join(Formats, ", "))
		}
		if e := checkURL(w.URL, w.AllowInsecure); e != nil {
			return fmt.Errorf("webhook %s: %w", w.ID, e)
		}
		if len(w.Bearer) > 4096 || strings.ContainsFunc(w.Bearer, func(r rune) bool { return r <= ' ' || r > '~' }) {
			return fmt.Errorf("webhook %s: bearer must be printable ASCII without spaces", w.ID)
		}
		for i, g := range w.Events {
			if !slices.Contains(Groups, g) || slices.Contains(w.Events[:i], g) {
				return fmt.Errorf("webhook %s: events are incidents and observers, each at most once", w.ID)
			}
		}
		for i, o := range w.Observers {
			if !observers[o] || slices.Contains(w.Observers[:i], o) {
				return fmt.Errorf("webhook %s: unknown or repeated observer %q", w.ID, secure.Text(o))
			}
		}
	}
	return nil
}

// checkURL allows HTTPS, and plain HTTP only to a literal loopback address
// or when insecure. A query may carry a service's own token; credentials
// in the authority are refused, as they belong in Bearer.
func checkURL(raw string, insecure bool) error {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid webhook URL")
	}
	switch ip := net.ParseIP(u.Hostname()); {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (insecure || ip != nil && ip.IsLoopback()):
		return nil
	}
	return errors.New("HTTPS required except to a literal loopback address; plain HTTP requires allow_insecure")
}

// wants reports whether w takes an event of group seen by observer.
func (w Webhook) wants(group, observer string) bool {
	return (len(w.Events) == 0 || slices.Contains(w.Events, group)) && (len(w.Observers) == 0 || slices.Contains(w.Observers, observer))
}
