package view

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// ServiceAssessment keeps aggregate check health separate from what the
// evidence establishes about an application, a path and its executor.
type ServiceAssessment struct {
	Availability string                  `json:"availability"`
	Reachability string                  `json:"path_reachability"`
	WorkerOnline string                  `json:"worker_online"`
	Evidence     []ScopedServiceEvidence `json:"evidence"`
	Hints        []string                `json:"configuration_hints"`
}
type ScopedServiceEvidence struct {
	Check        string `json:"check"`
	Observer     string `json:"observer"`
	ObserverName string `json:"observer_name"`
	Scope        string `json:"scope"`
	Health       string `json:"health"`
	Freshness    string `json:"freshness"`
	Worker       string `json:"worker"`
}

func (a ServiceAssessment) Label() string {
	if a.Reachability == "down" && a.Availability != "down" {
		return "ACCESS FAIL"
	}
	if a.Reachability == "degraded" && a.Availability != "down" {
		return "PATH WARN"
	}
	if a.Availability == "down" {
		return "LOCAL FAIL"
	}
	if a.Availability == "degraded" {
		return "HEALTH WARN"
	}
	if a.Availability == "up" {
		return "UP"
	}
	return "UNKNOWN"
}
func (a ServiceAssessment) Lines() []string {
	lines := []string{fmt.Sprintf("Service availability: %s; path reachability: %s; worker online: %s", a.Availability, a.Reachability, a.WorkerOnline)}
	for _, e := range a.Evidence {
		scope := "access from " + e.ObserverName
		if e.Scope == "local" {
			scope = e.ObserverName + " local health"
		}
		if e.Scope == "user_entry" {
			scope = "user entry from " + e.ObserverName
		}
		if e.Scope == "reported" {
			scope = "upstream report via " + e.ObserverName
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%s; check %s); worker: %s", scope, e.Health, e.Freshness, e.Check, e.Worker))
	}
	for _, hint := range a.Hints {
		lines = append(lines, "Configuration quality: "+hint)
	}
	if a.Reachability == "down" && a.Availability != "down" {
		lines = append(lines, "Remote access failure does not establish an application outage.")
	}
	return lines
}
func serviceAssessments(s model.Snapshot, cached bool) map[string]ServiceAssessment {
	states := map[string]model.CheckState{}
	for _, st := range s.Checks {
		states[st.ID] = st
	}
	observers := map[string]model.Observer{}
	for _, o := range s.Topology.Observers {
		observers[o.ID] = o
	}
	workers := map[string]string{}
	for _, o := range s.Observers {
		workers[o.ID] = o.Health
	}
	endpoints := map[string]model.Endpoint{}
	for _, ep := range s.Topology.Endpoints {
		endpoints[ep.ID] = ep
	}
	services := map[string]model.Service{}
	byService := map[string][]model.Check{}
	for _, sv := range s.Topology.Services {
		services[sv.ID] = sv
	}
	for _, x := range s.Topology.Checks {
		if x.Service != "" && !x.Disabled && !x.Maintenance {
			byService[x.Service] = append(byService[x.Service], x)
		}
	}
	out := map[string]ServiceAssessment{}
	for id, sv := range services {
		a := ServiceAssessment{Availability: "unknown", Reachability: "unknown", WorkerOnline: "unknown", Evidence: []ScopedServiceEvidence{}, Hints: []string{}}
		remote, local, entry := 0, 0, 0
		pathUp, pathDown, localDown, localWarn, available, workerUp, workerMissing := false, false, false, false, false, false, false
		for _, x := range byService[id] {
			if x.Kind == "host" {
				continue
			} // machine resources do not establish application health
			st, ok := states[x.ID]
			o := observers[x.Observer]
			scope := x.AvailabilityScope
			if scope == "" {
				scope = "remote"
				target := x.Target
				if target == "" {
					target = endpoints[x.Endpoint].Address
				}
				if x.Path == "" && o.Node != "" && o.Node == sv.Node && isLoopbackTarget(target) {
					scope = "local"
				}
			}
			if x.Kind == "external" || st.Evidence == "reported" || st.Last != nil && st.Last.Evidence == "reported" {
				scope = "reported"
			}
			switch scope {
			case "local":
				local++
			case "user_entry":
				entry++
			case "remote":
				remote++
			}
			if !ok {
				continue
			} // scoped view cannot borrow another observer's health
			health := st.Health
			worker := workers[x.Observer]
			if worker == "" {
				worker = "unknown"
			}
			if cached || st.Freshness != "fresh" {
				health = "unknown"
			}
			if cached {
				worker = "unknown"
			}
			a.Evidence = append(a.Evidence, ScopedServiceEvidence{Check: x.ID, Observer: x.Observer, ObserverName: named(o.Name, x.Observer), Scope: scope, Health: health, Freshness: st.Freshness, Worker: worker})
			workerUp = workerUp || worker == "up"
			workerMissing = workerMissing || worker != "up"
			if scope == "reported" {
				continue
			}
			if scope == "local" {
				localDown = localDown || x.Kind == "http" && health == "down"
				localWarn = localWarn || x.Kind == "http" && health == "degraded"
			} else {
				pathUp = pathUp || health == "up"
				pathDown = pathDown || health == "down" || health == "degraded"
			}
			// Only an application-level response establishes application availability;
			// a passing TCP/TLS handshake is path evidence, not a healthy application.
			available = available || x.Kind == "http" && health == "up"
		}
		if available {
			a.Availability = "up"
		}
		if localWarn {
			a.Availability = "degraded"
		}
		if localDown {
			a.Availability = "down"
			if available {
				a.Availability = "degraded"
			}
		}
		if pathUp {
			a.Reachability = "up"
		}
		if pathDown {
			a.Reachability = "down"
			if pathUp {
				a.Reachability = "degraded"
			}
		}
		if workerUp {
			a.WorkerOnline = "up"
			if workerMissing {
				a.WorkerOnline = "partial"
			}
		}
		if remote == 1 && local == 0 && entry == 0 {
			a.Hints = append(a.Hints, "only one remote check; add a local application health check or an explicit user_entry check (availability_scope)")
		}
		out[id] = a
	}
	return out
}
func isLoopbackTarget(target string) bool {
	host := target
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
