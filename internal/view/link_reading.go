package view

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// A link a Cloudflare Tunnel runs over has two readings, each reported by its
// own check and neither deciding the link's health here: the connector's own
// (how many edge sessions it holds, at which colos) and the platform's
// (how many connections the API lists, and its status word). They are said
// side by side on the card, so a reader sees both agree or not.

// maxReadingColos bounds the colo tokens a reading names, as the connector's
// report does.
const maxReadingColos = 8

// readingColo is the lowercase shape cloudflared labels a connection's edge
// location with: three to eight ASCII letters or digits.
var readingColo = regexp.MustCompile(`^[0-9a-z]{3,8}$`)

// readingStatus is the closed set of words the API gives a tunnel.
var readingStatus = map[string]bool{"healthy": true, "degraded": true, "down": true, "inactive": true}

// linkReadings reads what each link's newest fresh checks report of it into
// the fleet: the connector's reading, then the API's. A stale, paused or
// absent check says nothing, and a fact that is not a bounded number or a
// closed word is left out rather than shown.
func (f *Fleet) linkReadings(s model.Snapshot, t model.Config) {
	link, kind := map[string]string{}, map[string]string{}
	for _, x := range t.Checks {
		if x.Link != "" && (x.Kind == "cloudflared" || x.Kind == "cloudflare") {
			link[x.ID], kind[x.ID] = x.Link, x.Kind
		}
	}
	type pair struct{ connector, platformStatus, platformConnections *model.Observation }
	newest := map[string]*pair{}
	for _, cs := range s.Checks {
		id := link[cs.ID]
		if id == "" || cs.Freshness != "fresh" || cs.Last == nil {
			continue
		}
		p := newest[id]
		if p == nil {
			p = &pair{}
			newest[id] = p
		}
		slot := &p.connector
		if kind[cs.ID] == "cloudflare" {
			if cs.Last.Facts["tunnel_status"] != "" && (p.platformStatus == nil || cs.Last.SampledAt.After(p.platformStatus.SampledAt)) {
				p.platformStatus = cs.Last
			}
			if cs.Last.Facts["tunnel_connections"] != "" && (p.platformConnections == nil || cs.Last.SampledAt.After(p.platformConnections.SampledAt)) {
				p.platformConnections = cs.Last
			}
			continue
		}
		if *slot == nil || cs.Last.SampledAt.After((*slot).SampledAt) {
			*slot = cs.Last
		}
	}
	for id, p := range newest {
		reading := []string{}
		if p.connector != nil {
			reading = append(reading, connectorReading(p.connector.Facts)...)
		}
		platform := map[string]string{}
		if p.platformStatus != nil {
			platform["tunnel_status"] = p.platformStatus.Facts["tunnel_status"]
		}
		if p.platformConnections != nil {
			platform["tunnel_connections"] = p.platformConnections.Facts["tunnel_connections"]
		}
		reading = append(reading, platformReading(platform)...)

		if len(reading) > 0 {
			f.linkRead[id] = reading
		}
	}
}

// readingCount is a small non-negative count from a fact.
func readingCount(fact string) (int, bool) {
	n, err := strconv.Atoi(fact)
	return n, err == nil && n >= 0 && n <= 1<<16
}

// connectorReading says what a cloudflared sample reports: its ready edge
// sessions, or that it has none, and the colos they are held at.
func connectorReading(facts map[string]string) []string {
	out := []string{}
	if facts["metrics_status"] == "unavailable" {
		out = append(out, "metrics unavailable")
	}
	n, ok := readingCount(facts["ready_connections"])
	switch {
	case !ok:
	case n == 0:
		out = append(out, "no ready session")
	default:
		out = append(out, Plural(n, "session"))
	}
	colos := []string{}
	for _, c := range strings.Split(facts["connection_colos"], ",") {
		if readingColo.MatchString(c) && len(colos) < maxReadingColos {
			colos = append(colos, c)
		}
	}
	if len(colos) > 0 {
		out = append(out, strings.Join(colos, ","))
	}
	return out
}

// platformReading says what the Cloudflare API reports of a tunnel, led by
// "API" so it is not taken for the connector's own count: "API 4 connections
// healthy".
func platformReading(facts map[string]string) []string {
	words := []string{"API"}
	if n, ok := readingCount(facts["tunnel_connections"]); ok {
		words = append(words, Plural(n, "connection"))
	}
	if status := facts["tunnel_status"]; readingStatus[status] {
		words = append(words, status)
	}
	if len(words) == 1 {
		return nil
	}
	return []string{strings.Join(words, " ")}
}
