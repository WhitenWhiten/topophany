// Package engine derives presentation states from immutable observations. It
// never manufactures packet-level evidence or identifies a filtering authority.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func Evaluate(x model.Check, rows []model.Observation, now time.Time) model.CheckState {
	return EvaluateCarried(x, nil, rows, now)
}

// EvaluateCarried judges x from rows after those folded into carry, as
// Evaluate would from all of them; rows that carry holds are passed over.
func EvaluateCarried(x model.Check, carry *Carry, rows []model.Observation, now time.Time) model.CheckState {
	refs := make([]*model.Observation, len(rows))
	for i := range rows {
		refs[i] = &rows[i]
	}
	return evaluate(x, carry, refs, now)
}

// evaluate owns only the index slice. Sorting pointers avoids copying entire
// observations and never changes the input order or its diagnostic maps.
func evaluate(x model.Check, carry *Carry, rows []*model.Observation, now time.Time) model.CheckState {
	return currentState(x, evaluateEvidence(x, carry, rows), now)
}

func evaluateEvidence(x model.Check, carry *Carry, rows []*model.Observation) model.CheckState {
	s := model.CheckState{ID: x.ID, Observer: x.Observer, Health: "unknown", Freshness: "never", Evidence: "unknown", Reason: "not yet measured"}
	if x.Disabled {
		s.Freshness = "paused"
		s.Reason = "check paused"
		return s
	}
	if x.Maintenance {
		s.Freshness = "maintenance"
		s.Reason = "maintenance; excluded from alerts"
		return s
	}
	var k Carry
	if carry != nil {
		k = *carry
	}
	rows = slices.DeleteFunc(rows, func(v *model.Observation) bool { return k.Holds(*v) })
	if len(rows) == 0 {
		return s
	}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i]
		return earlier(a.SampledAt, a.Boot, a.Sequence, a.ID, *rows[j])
	})
	for _, v := range rows {
		k.Fold(*v, time.Duration(x.StaleSeconds)*time.Second)
	}
	last := *rows[len(rows)-1]
	s.Last = &last
	s.Evidence = last.Evidence
	s.Reason = last.Detail
	s.Failures, s.Successes, s.Since = k.Failures, k.Successes, k.Since
	down := k.down(x.FailureThreshold, x.RecoveryThreshold)
	switch last.Outcome {
	case "ok":
		s.Health = "up"
		if down {
			s.Health = "degraded"
			s.Reason = "recovering; waiting for consecutive successes"
		}
	case "fail":
		s.Health = "down"
		if !down {
			s.Health = "degraded"
			s.Reason = "failure observed; confirmation threshold not reached"
		}
	case "policy":
		s.Health = "degraded"
		s.Reason = "policy violation: " + last.Code
		// A policy violation is a state assertion, not a failed request, so
		// it never reads as down. Preserve immediate ordinary policy
		// incidents unless confirmation is explicitly configured. A pending
		// provider load independently adds its bounded settle window.
		settling := providerSettling(x, last)
		run, threshold := k.PolicyRun, policyThreshold(x, settling)
		s.PolicyConfirmed = run >= threshold
		if s.PolicyConfirmed && settling {
			if ref := x.Adapter.ProviderReference; ref != nil {
				settle := time.Duration(ref.SettleSecondsOrDefault()) * time.Second
				if settle > 0 && !s.Since.IsZero() && last.SampledAt.Sub(s.Since) < settle {
					s.PolicyConfirmed = false
				}
			}
		}
		if !s.PolicyConfirmed {
			if run < threshold {
				s.Reason = fmt.Sprintf("%s; confirmation threshold not reached (%d/%d)", s.Reason, run, threshold)
			} else {
				s.Reason = fmt.Sprintf("%s; settle window not reached", s.Reason)
			}
		}
	default:
		if last.Outcome == "error" {
			s.Reason = "monitor-side error: " + last.Code
		}
	}
	s.Freshness = "fresh"
	return s
}

func currentState(x model.Check, s model.CheckState, now time.Time) model.CheckState {
	if s.Last == nil {
		return s
	}
	if s.Last.SampledAt.After(now.Add(model.SampleClockAhead)) {
		s.Health = "unknown"
		s.Freshness = "stale"
		s.Reason = "executor clock is ahead; sample not treated as current"
		s.Failures = 0
		s.Successes = 0
		s.PolicyConfirmed = false
	} else if now.Sub(s.Last.SampledAt) > time.Duration(x.StaleSeconds)*time.Second {
		s.Health = "unknown"
		s.Freshness = "stale"
		s.Reason = "measurement expired; last outcome is historical, not current"
		s.Failures = 0
		s.Successes = 0
		s.PolicyConfirmed = false
	}
	return s
}

// Build derives the snapshot of c at now from stored observations and
// heartbeats. A sample counts when it carries its check's contract in c
// (or, from a schema 1 executor, c's revision). The snapshot shows only
// public revisions and contracts.
func Build(c model.Config, instance, view string, rows []model.Observation, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	return BuildCarried(c, instance, view, rows, nil, beats, now)
}

// BuildCarried is Build where carries hold what samples no longer stored
// left behind: each check is judged from its carry under its contract in
// c, if it has one, and the samples after it.
func BuildCarried(c model.Config, instance, view string, rows []model.Observation, carries []Carry, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	return NewPlan(c).Build(instance, view, rows, carries, beats, now)
}

// Build evaluates one snapshot without allocating a reusable Prepared cache.
// Concurrent calls are safe; callers must not mutate inputs during the call.
func (p *Plan) Build(instance, view string, rows []model.Observation, carries []Carry, beats map[string]model.Heartbeat, now time.Time) model.Snapshot {
	lastSeen, reported := map[string]time.Time{}, map[string]string{}
	limit := now.Add(model.SampleClockAhead)
	seen := func(observer string, at time.Time) {
		if !at.After(limit) && at.After(lastSeen[observer]) {
			lastSeen[observer] = at
		}
	}
	for id, h := range beats {
		if h.Project == p.config.Project {
			seen(id, h.SampledAt)
			reported[id] = p.reportedContract(id, h)
		}
	}
	states := p.foldEvidence(rows, carries, seen)
	for _, x := range p.config.Checks {
		if x.Kind == "external" {
			states[x.ID], _ = p.currentExternal(x, nil, now)
			continue
		}
		states[x.ID] = currentState(x, states[x.ID], now)
	}
	return p.buildSnapshot(instance, view, now, states, lastSeen, reported, beats)
}

// Build derives current freshness, observer liveness and entity judgments from
// prepared evidence. It is safe for concurrent readers and returns owned data.
func (e *Prepared) Build(instance, view string, now time.Time) model.Snapshot {
	p := e.plan
	states := make(map[string]model.CheckState, len(p.checks))
	external := []model.ExternalBindingView{}
	for _, x := range p.config.Checks {
		if x.Kind == "external" {
			st, ext := p.currentExternal(x, e.external, now)
			states[x.ID] = st
			if view == "" || view == x.Observer {
				external = append(external, ext)
			}
			continue
		}
		st := currentState(x, e.checks[x.ID], now)
		if st.Last != nil {
			last := model.CloneObservation(*st.Last)
			st.Last = &last
		}
		states[x.ID] = st
	}
	snap := p.buildSnapshot(instance, view, now, states, e.lastSeen(now), e.reported, e.beats)
	snap.External = external
	return snap
}

// Both entry points supply owned check states. Entity derivation and public
// projection stay identical without copying diagnostics a second time.
func (p *Plan) buildSnapshot(instance, view string, now time.Time, allStates map[string]model.CheckState, lastSeen map[string]time.Time, reported map[string]string, beats map[string]model.Heartbeat) model.Snapshot {
	c, checks := p.config, p.checks
	out := model.Snapshot{Schema: model.SnapshotSchema, Version: model.Version, Project: c.Project, Instance: instance, Revision: p.publicRevision, GeneratedAt: now.UTC(), ViewObserver: view, Topology: config.Clone(p.public), Checks: []model.CheckState{}, Nodes: []model.EntityState{}, Endpoints: []model.EntityState{}, Services: []model.EntityState{}, Paths: []model.EntityState{}, Links: []model.EntityState{}, Observers: []model.ObserverState{}, Incidents: []model.Incident{}}
	for _, x := range c.Checks {
		if view == "" || view == x.Observer {
			out.Checks = append(out.Checks, allStates[x.ID])
		}
	}
	for _, o := range c.Observers {
		s := model.ObserverState{ID: o.ID, Health: "unknown", LastSeen: lastSeen[o.ID], Reason: "no recent executor evidence", Contracts: reported[o.ID]}
		if !s.LastSeen.IsZero() && now.Sub(s.LastSeen) <= time.Duration(o.StaleSeconds)*time.Second {
			s.Health = "up"
			s.Reason = "recent executor evidence; target health evaluated separately"
		}
		if h, ok := beats[o.ID]; ok && h.Project == c.Project {
			s.Delivery = model.CloneHeartbeat(h).Delivery
		}
		if s.Contracts == "outdated" {
			s.Reason += "; it measures an outdated topology, so samples of checks that changed are refused until it reloads"
		}
		out.Observers = append(out.Observers, s)
	}
	relevant := func(x model.Check) bool { return view == "" || x.Observer == view }
	group := func(ids []string) []model.CheckState {
		r := make([]model.CheckState, 0, len(ids))
		for _, id := range ids {
			if relevant(checks[id]) {
				r = append(r, allStates[id])
			}
		}
		return r
	}
	endpoints := p.endpoints
	for _, n := range c.Nodes {
		// A node's evidence comes from checks on its own endpoints and on
		// endpoints reached through it as an edge or proxy.
		states := group(p.nodeChecks[n.ID])
		s := model.EntityState{ID: n.ID, Health: "unknown", Evidence: "unknown", Reason: "no fresh remote-response evidence"}
		failures := 0
		proof := []string{}
		through := false
		for _, cs := range states {
			s.Checks = append(s.Checks, cs.ID)
			if cs.Freshness != "fresh" || cs.Last == nil {
				continue
			}
			x := checks[cs.ID]
			ep := endpoints[x.Endpoint]
			if answered(ep, n.ID, cs.Last) {
				proof = append(proof, cs.ID)
			}
			through = through || behind(ep, n.ID, cs.Last)
			if cs.Health == "down" && implicated(ep, n.ID, cs.Last) {
				failures++
			}
		}
		if len(proof) > 0 {
			s.Health = "up"
			s.Evidence = "measured"
			s.Reason = "remote-response evidence: " + strings.Join(proof, ", ")
			s.Affected = failures > 0
		} else if failures > 0 {
			s.Reason = "configured response paths unavailable; physical power state unknown"
			s.Evidence = "inferred"
			s.Affected = true
		} else if through {
			s.Reason = "answered only through the edge or proxy in front, which proves that hop alone; a check that does not go through it proves this node"
		}
		if len(states) == 0 {
			s.Reason = "registered asset; no checks in this view"
		}
		out.Nodes = append(out.Nodes, s)
	}
	// An endpoint is judged from the checks that target it.
	for _, e := range c.Endpoints {
		out.Endpoints = append(out.Endpoints, Aggregate(e.ID, group(p.endpointChecks[e.ID]), "no check targets this endpoint in this view"))
	}
	pathStates := map[string]model.EntityState{}
	pathDefs := p.paths
	var evalPath func(string) model.EntityState
	evalPath = func(id string) model.EntityState {
		if s, ok := pathStates[id]; ok {
			return s
		}
		path := pathDefs[id]
		s := aggregatePath(id, group(p.pathChecks[id]), "no endpoint-to-endpoint check; segment health unknown")
		if len(path.Candidates) > 0 {
			up, down, unknown := 0, 0, 0
			for _, q := range path.Candidates {
				cs := evalPath(q)
				s.Checks = boundedChecks(s.Checks, cs.Checks)
				switch cs.Health {
				case "up":
					up++
				case "down":
					down++
				default:
					unknown++
				}
			}
			s.Evidence = "inferred"
			switch {
			case up == len(path.Candidates):
				s.Health = "up"
				s.Reason = "all candidates available; active request selection not inferred"
			case up > 0:
				s.Health = "degraded"
				s.Reason = "usable candidate exists, redundancy reduced; actual failover not verified"
			case down == len(path.Candidates):
				s.Health = "down"
				s.Reason = "all configured candidates failed"
			default:
				s.Health = "unknown"
				s.Reason = fmt.Sprintf("no confirmed available candidate (%d unknown/degraded)", unknown)
			}
		}
		for _, dep := range path.DependsOn {
			v := evalPath(dep)
			if v.Health == "down" || v.Affected {
				s.Affected = true
				s.Reason += "; dependency affected: " + dep
			}
		}
		pathStates[id] = s
		return s
	}
	for _, p := range c.Paths {
		if view == "" || p.Observer == view {
			out.Paths = append(out.Paths, evalPath(p.ID))
		}
	}
	for _, l := range c.Links {
		s := aggregateLink(l.ID, group(p.linkChecks[l.ID]), "segment not independently measured")
		for _, id := range p.linkPaths[l.ID] {
			if relevant(model.Check{Observer: p.paths[id].Observer}) && evalPath(id).Health == "down" {
				s.Affected = true
			}
		}
		if s.Affected && len(s.Checks) == 0 {
			s.Reason = "on a failing end-to-end path; individual segment remains unknown"
			s.Evidence = "inferred"
		}
		out.Links = append(out.Links, s)
	}
	serviceDefs := p.services
	serviceStates := map[string]model.EntityState{}
	var evalService func(string) model.EntityState
	evalService = func(id string) model.EntityState {
		if s, ok := serviceStates[id]; ok {
			return s
		}
		def := serviceDefs[id]
		s := Aggregate(id, group(p.serviceChecks[id]), "registered, not monitored")
		for _, dep := range def.DependsOn {
			v := evalService(dep)
			if v.Health == "down" || v.Affected {
				s.Affected = true
				s.Reason += "; dependency affected: " + dep
			}
		}
		for _, check := range p.serviceChecks[id] {
			x := checks[check]
			if x.Service == id && x.Path != "" && relevant(x) && evalPath(x.Path).Health == "down" {
				if !strings.HasSuffix(s.Reason, unavailable) {
					s.Reason += unavailable
				}
				s.Affected = true
			}
		}
		serviceStates[id] = s
		return s
	}
	for _, s := range c.Services {
		out.Services = append(out.Services, evalService(s.ID))
	}
	// Incidents aggregate only confirmed failures in the same observer/endpoint
	// scope. They do not diagnose an IP-wide ban, GFW, or untested UDP behavior.
	proofs := p.responseProofs(allStates)
	incidents := map[string]*model.Incident{}
	// An incident can hold several checks on one node. Remember every status,
	// mitigation word and Access word each of them reported through the edge
	// so one check's answer cannot stand for another's.
	edgeStatuses := map[string][]edgeStatus{}
	edgeChecks, incidentChecks := map[string]int{}, map[string]int{}
	for _, x := range c.Checks {
		if !relevant(x) || x.Kind == "external" {
			continue
		}
		s := allStates[x.ID]
		if s.Freshness != "fresh" || s.Last == nil {
			continue
		}
		policy := s.Last.Outcome == "policy"
		// A policy violation opens an incident only once ConfirmationSamples
		// says it has stood long enough; see evaluate. Until then it is a warning.
		if s.Health != "down" && !(policy && s.PolicyConfirmed) {
			continue
		}
		base := p.scope(x)
		scope := base
		if policy {
			scope += "/policy"
		}
		inc := incidents[scope]
		if inc == nil {
			inc = &model.Incident{ID: p.incidents[base] + "/" + scope, Scope: scope, Summary: "confirmed failure of the listed checks from this observer", Checks: []string{}, CounterEvidence: []string{}, Since: s.Since}
			if policy {
				inc.Summary = "observed policy constraint violation"
			}
			incidents[scope] = inc
		}
		inc.Checks = append(inc.Checks, x.ID)
		incidentChecks[scope]++
		if s.Since.Before(inc.Since) {
			inc.Since = s.Since
		}
		if !policy && x.Endpoint != "" {
			ep := endpoints[x.Endpoint]
			if len(inc.CounterEvidence) < counterEvidenceLimit {
				inc.CounterEvidence = boundedChecks(inc.CounterEvidence, proofs.excluding(ep.Node, x.Observer))
			}
			if len(ep.Via) > 0 && s.Last.Connectivity {
				edgeChecks[scope]++
				edgeStatuses[scope] = append(edgeStatuses[scope], edgeStatus{
					status:     s.Last.Facts["http_status"],
					mitigation: s.Last.Facts["edge_mitigation"],
					access:     s.Last.Facts["edge_access"],
				})
			}
		}
	}
	for scope, inc := range incidents {
		switch {
		case incidentChecks[scope] > 0 && edgeChecks[scope] == incidentChecks[scope]:
			inc.Hypothesis = edgeHypothesisStatuses(edgeStatuses[scope])
		case len(inc.CounterEvidence) > 0:
			inc.Hypothesis = "source/path-specific interference, filtering or routing fault is possible; blocking authority and untested protocols are not established"
		}
		sort.Strings(inc.Checks)
		sort.Strings(inc.CounterEvidence)
		inc.CounterEvidence = dedup(inc.CounterEvidence)
		out.Incidents = append(out.Incidents, *inc)
	}
	// Incidents read in the order of their scopes: by observer, then what failed.
	sort.Slice(out.Incidents, func(i, j int) bool { return out.Incidents[i].Scope < out.Incidents[j].Scope })
	return out
}

const unavailable = "; access path unavailable, intrinsic service state not established"

// answered reports whether v is remote-response evidence for node: the
// endpoint's own node, or the first edge or proxy in its via chain. The
// first hop answers whatever happens behind it, even with an error page,
// so any response proves it and nothing behind it. Where an edge says a
// response came from, its cache status among them, is the response's own
// word, which the edge or anything behind it can send whatever happened,
// so the nodes behind the first hop count only a check that reaches them
// without going through it.
func answered(ep model.Endpoint, node string, v *model.Observation) bool {
	return responded(v) && (len(ep.Via) == 0 || ep.Via[0] == node)
}

// behind reports whether v is an answer through the first hop of ep's via
// chain that proves that hop and not node, behind it.
func behind(ep model.Endpoint, node string, v *model.Observation) bool {
	return responded(v) && len(ep.Via) > 0 && ep.Via[0] != node
}

// responded reports whether v is a measured response from the remote end.
func responded(v *model.Observation) bool {
	return v.Evidence == "measured" && v.Connectivity && v.Facts["response_proof"] != "" && v.Facts["response_proof"] != "none"
}

// implicated reports whether a confirmed failure of a check on ep may lie at
// node: anywhere along the chain, except at a first hop that answered.
func implicated(ep model.Endpoint, node string, v *model.Observation) bool {
	return len(ep.Via) == 0 || ep.Via[0] != node || !v.Connectivity
}

// edgeStatus is one answer through an edge: its status word, the closed
// mitigation word the edge put beside it, and the closed Access word.
type edgeStatus struct {
	status, mitigation, access string
}

const (
	edgeUnlocated     = "the request did not satisfy its assertions and the failing layer is not located"
	edgeOriginUnknown = "the origin's own state is not established"
	edgeSomeChallenge = "the edge served a challenge page for at least one request; the origin was not asked by that request; "
	edgeSomeAccess    = "Cloudflare Access asked for a login for at least one request; the origin was not asked by that request; "
)

// edgeHypothesisStatuses explains a failed request whose response came back
// through an edge in front. Any response proves that hop and nothing behind
// it, and an error status is a word the edge or the origin can send whatever
// happened, so the hypothesis says only what is established and names a
// candidate cause only where the status class supports one. An HTTP 403 is
// not proof of a WAF, and 521 is the platform's own account of an origin it
// could not reach, not an independent observation of that origin. A
// challenge word is the edge's own statement that the origin was not asked;
// so is an Access login redirect, which is named above the status doubt it
// explains. Either word explains the whole incident only when every
// aggregated answer carries it, and is otherwise scoped to the request that
// saw it.
//
// An incident may aggregate checks that saw different statuses. One check's
// code must not be presented as another's, so mixed statuses get a sentence
// without a single code; a challenge or an Access redirect among them is
// named first.
func edgeHypothesisStatuses(statuses []edgeStatus) string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(statuses))
	challenged, accessed, missing := false, false, false
	allChallenged, allAccessed := len(statuses) > 0, len(statuses) > 0
	for _, s := range statuses {
		if s.mitigation == "challenge" {
			challenged = true
		} else {
			allChallenged = false
		}
		if s.access == "login_redirect" {
			accessed = true
		} else {
			allAccessed = false
		}
		if s.status == "" {
			missing = true
			continue
		}
		if seen[s.status] {
			continue
		}
		seen[s.status] = true
		unique = append(unique, s.status)
	}
	if len(unique) > 1 {
		slices.Sort(unique)
		mixed := "the edge in front answered with different HTTP statuses (" + strings.Join(unique, ", ") + "); the requests did not satisfy their assertions and the failing layer is not located; " + edgeOriginUnknown
		switch {
		case challenged:
			return edgeSomeChallenge + mixed
		case accessed:
			return edgeSomeAccess + mixed
		}
		return mixed
	}
	single := len(unique) == 1 && !missing
	switch {
	case allChallenged && single:
		return edgeHypothesisStatus(edgeStatus{status: unique[0], mitigation: "challenge"})
	case allChallenged:
		return edgeHypothesisStatus(edgeStatus{mitigation: "challenge"})
	case challenged:
		head := "the edge in front answered"
		if single {
			head += " with HTTP " + unique[0]
		}
		return head + "; " + edgeSomeChallenge + "another request did not satisfy its assertions and the failing layer is not located; " + edgeOriginUnknown
	case allAccessed && single:
		return edgeHypothesisStatus(edgeStatus{status: unique[0], access: "login_redirect"})
	case allAccessed:
		return edgeHypothesisStatus(edgeStatus{access: "login_redirect"})
	case accessed:
		head := "the edge in front answered"
		if single {
			head += " with HTTP " + unique[0]
		}
		return head + "; " + edgeSomeAccess + "another request did not satisfy its assertions and the failing layer is not located; " + edgeOriginUnknown
	case single:
		return edgeHypothesisStatus(edgeStatus{status: unique[0]})
	}
	// One check without a status, or several checks that agree but do not
	// all carry one: no single code can explain the whole incident.
	return edgeHypothesisStatus(edgeStatus{})
}

func edgeHypothesisStatus(s edgeStatus) string {
	head := "the edge in front answered"
	if s.status != "" {
		head += " with HTTP " + s.status
	}
	head += "; "
	switch {
	case s.mitigation == "challenge":
		return head + "the edge served a challenge page; the origin was not asked; the request did not satisfy its assertions, and " + edgeOriginUnknown
	case s.access == "login_redirect":
		return head + "Cloudflare Access asked for a login; the origin was not asked; " + edgeOriginUnknown
	case s.status == "403" || s.status == "429":
		return head + edgeUnlocated + "; the response may be edge policy or a challenge page rather than the origin's answer, and " + edgeOriginUnknown
	case s.status == "520":
		return head + "the request did not satisfy its assertions; the edge reports an empty, unknown or malformed response from the origin, which is the platform's own account, and " + edgeOriginUnknown
	case s.status == "521" || s.status == "522" || s.status == "523":
		return head + "the request did not satisfy its assertions; the edge reports that it could not reach the origin, which is the platform's own account, and " + edgeOriginUnknown
	case s.status == "524":
		return head + "the request did not satisfy its assertions; the edge reports a read or write timeout after connecting to the origin, which is the platform's own account, and " + edgeOriginUnknown
	case s.status == "525" || s.status == "526":
		return head + "the request did not satisfy its assertions; the edge reports that TLS to the origin failed (handshake, or a certificate it does not accept), which is the platform's own account; " + edgeOriginUnknown
	case s.status == "530":
		return head + "the request did not satisfy its assertions; the edge reports that it could not resolve or reach the origin, including a Tunnel with no live connector; " + edgeOriginUnknown
	case strings.HasPrefix(s.status, "5"):
		return head + edgeUnlocated + "; the response may come from the edge itself or from the origin, whose own state is not established"
	}
	return head + edgeUnlocated + "; " + edgeOriginUnknown
}

// digest is the SHA-256 of a set of strings, in hex.
func digest(set []string) string {
	sorted := slices.Sorted(slices.Values(set))
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:])
}

func dedup(s []string) []string {
	out := s[:0]
	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

// Aggregate is the state of an entity from the checks that measure it:
// up when all pass, down when all confirm failure, degraded when some
// pass or any is degraded, otherwise unknown. Checks without current
// evidence count as unknown; empty is the reason when none measures it.
func Aggregate(id string, states []model.CheckState, empty string) model.EntityState {
	s := model.EntityState{ID: id, Health: "unknown", Evidence: "unknown", Reason: empty}
	if len(states) == 0 {
		return s
	}
	up, down, degraded, unknown, active := 0, 0, 0, 0, 0
	for _, v := range states {
		s.Checks = append(s.Checks, v.ID)
		if v.Freshness == "paused" || v.Freshness == "maintenance" {
			continue
		}
		active++
		if v.Freshness != "fresh" {
			unknown++
			continue
		}
		switch v.Health {
		case "up":
			up++
		case "down":
			down++
		case "degraded":
			degraded++
		default:
			unknown++
		}
	}
	if active == 0 {
		s.Reason = "checks paused or in maintenance; no active evidence"
		return s
	}
	s.Evidence = "inferred"
	switch {
	case up == active:
		s.Health = "up"
		s.Reason = "all checks in this view pass"
	case up > 0:
		s.Health = "degraded"
		s.Reason = "some access checks pass; others fail, degrade or lack current evidence"
	case down == active:
		s.Health = "down"
		s.Reason = "all access checks in this view have confirmed failures"
	case degraded > 0:
		s.Health = "degraded"
		s.Reason = "confirmation, recovery or policy issue; inspect check evidence"
	default:
		s.Health = "unknown"
		s.Reason = fmt.Sprintf("no current successful check (%d unknown; %d failed)", unknown, down)
	}
	return s
}

// aggregatePath is the state of a path from its checks. When every active
// check is a fresh runtime report, the path is unknown for a reason worth
// saying: the software answered, and nothing measured the path itself.
func aggregatePath(id string, states []model.CheckState, empty string) model.EntityState {
	s := Aggregate(id, pathEvidenceStates(states), empty)
	active, reported := 0, 0
	for _, check := range states {
		if check.Freshness == "paused" || check.Freshness == "maintenance" {
			continue
		}
		active++
		if check.Freshness == "fresh" && check.Last != nil && check.Last.Evidence == "reported" {
			reported++
		}
	}
	if active > 0 && active == reported && s.Health == "unknown" {
		s.Reason = "runtime reports only; inspect software policy checks; end-to-end reachability is unmeasured"
	}
	return s
}

// aggregateLink is the state of a link from its checks. A measured check
// decides it as usual. Remote-reported telemetry alone stays unknown, as on a
// path, but a fresh local connector report decides the link's own state: the
// connector is one end of the link and says whether it holds an edge session.
// That is still reported evidence about the session, never proof that the
// service behind it answers; the reason says so, and the link says its state is
// reported rather than inferred when the connector alone decided it. A
// connector read that failed is not a report about the link at all, so it
// neither counts as one nor inherits the reported-only reason. The platform's
// own account of the tunnel, read beside the connector's, stays in the check
// list but does not vote while a connector report decides the link.
func aggregateLink(id string, states []model.CheckState, empty string) model.EntityState {
	voters := linkVoters(states)
	s := Aggregate(id, linkEvidenceStates(voters), empty)
	if len(voters) != len(states) {
		s.Checks = s.Checks[:0]
		for _, check := range states {
			s.Checks = append(s.Checks, check.ID)
		}
	}
	active, reported, connector, failed := 0, 0, 0, 0
	for _, check := range voters {
		if check.Freshness == "paused" || check.Freshness == "maintenance" {
			continue
		}
		active++
		if check.Freshness != "fresh" || check.Last == nil || check.Last.Evidence != "reported" {
			continue
		}
		if check.Last.Outcome == "error" {
			if check.Last.PathEvidence == model.LocalConnectorPathEvidence {
				failed++
			}
			continue
		}
		reported++
		if check.Last.PathEvidence == model.LocalConnectorPathEvidence {
			connector++
		}
	}
	switch {
	case active > 0 && active == connector && s.Health == "up":
		s.Evidence = "reported"
		s.Reason = "local connector reports an established tunnel; the service behind it is unproven"
	case active > 0 && active == connector && s.Health == "down":
		s.Evidence = "reported"
		s.Reason = "local connector reports no established tunnel"
	case failed > 0 && s.Health == "unknown":
		s.Reason = "local connector read failed; link state unknown"
	case active > 0 && active == reported && s.Health == "unknown":
		s.Reason = "runtime reports only; inspect software policy checks; end-to-end reachability is unmeasured"
	}
	return s
}

// linkVoters is the checks that decide a link. While a fresh local connector
// report that is not a failed read decides the link, the remote platform's
// reported samples are left out: they are its account of the same tunnel,
// shown beside the connector's, and neither outvote nor dilute it, whether
// they pass, violate a policy, fail to read or have gone stale. Without a
// connector report they stay, and a link they alone describe is unknown.
func linkVoters(states []model.CheckState) []model.CheckState {
	remote := func(s model.CheckState) bool {
		return s.Last != nil && s.Last.Evidence == "reported" && s.Last.PathEvidence == model.RemotePlatformPathEvidence
	}
	decided, anyRemote := false, false
	for _, s := range states {
		if remote(s) {
			anyRemote = true
		}
		if s.Freshness == "fresh" && s.Last != nil && s.Last.Evidence == "reported" &&
			s.Last.PathEvidence == model.LocalConnectorPathEvidence && s.Last.Outcome != "error" {
			decided = true
		}
	}
	if !decided || !anyRemote {
		return states
	}
	voters := make([]model.CheckState, 0, len(states))
	for _, s := range states {
		if !remote(s) {
			voters = append(voters, s)
		}
	}
	return voters
}

// LinkJudgmentChecks names the evidence that bounds an existing link judgment.
// Display projections use the same voter selection without deriving health a
// second time. Pass the original snapshot states, before locally aging them.
func LinkJudgmentChecks(states []model.CheckState) []string {
	out := []string{}
	for _, s := range linkVoters(states) {
		out = append(out, s.ID)
	}
	return out
}

// linkEvidenceStates keeps a fresh local connector's own verdict and leaves
// every other non-measured sample unknown, as a path does.
func linkEvidenceStates(states []model.CheckState) []model.CheckState {
	out := make([]model.CheckState, len(states))
	copy(out, states)
	for i := range out {
		s := &out[i]
		connector := s.Last != nil && s.Last.Evidence != "measured" && s.Last.PathEvidence == model.LocalConnectorPathEvidence
		if s.Last != nil && s.Last.Evidence != "measured" && !connector {
			s.Health = "unknown"
			s.Reason = "runtime telemetry is not an end-to-end probe"
		} else if s.Health == "up" && !connector && (s.Last == nil || s.Last.Facts["response_proof"] == "" || s.Last.Facts["response_proof"] == "none") {
			s.Health = "unknown"
			s.Reason = "no remote protocol response proof"
		}
	}
	return out
}

// Runtime selection/handshake metadata and a TUN-visible TCP accept are not
// enough to mark an end-to-end access path healthy.
func pathEvidenceStates(states []model.CheckState) []model.CheckState {
	out := make([]model.CheckState, len(states))
	copy(out, states)
	for i := range out {
		s := &out[i]
		if s.Last != nil && s.Last.Evidence != "measured" {
			s.Health = "unknown"
			s.Reason = "runtime telemetry is not an end-to-end probe"
		} else if s.Health == "up" && (s.Last == nil || s.Last.Facts["response_proof"] == "" || s.Last.Facts["response_proof"] == "none") {
			s.Health = "unknown"
			s.Reason = "no remote protocol response proof"
		}
	}
	return out
}

// A shared candidate DAG must not expand the same descendant list repeatedly.
// Summaries carry a bounded sample; the topology and check list retain all IDs.
func boundedChecks(dst, src []string) []string {
	if len(dst) >= counterEvidenceLimit {
		return dst
	}
	seen := map[string]bool{}
	for _, id := range dst {
		seen[id] = true
	}
	for _, id := range src {
		if len(dst) >= counterEvidenceLimit {
			break
		}
		if !seen[id] {
			dst = append(dst, id)
			seen[id] = true
		}
	}
	return dst
}
