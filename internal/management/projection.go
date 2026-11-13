package management

import (
	"errors"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

// Project retains the reference closure of one executor's checks. Private
// options are restored only for its own execution paths and endpoints.
func Project(c model.Config, observer, role string) (model.Config, error) {
	if role == "collector" {
		return config.Clone(c), nil
	}
	full, e := config.ResolveAddresses(c)
	if e != nil {
		return model.Config{}, e
	}
	public := config.Public(full)
	out := model.Config{Schema: c.Schema, Project: c.Project}
	checks, sources, paths, endpoints, nodes, services, links, observers, apps := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{observer: true}, map[string]bool{}
	privateEndpoints := map[string]bool{}
	for _, x := range full.Checks {
		if x.Observer != observer {
			continue
		}
		out.Checks = append(out.Checks, x)
		checks[x.ID] = true
		paths[x.Path] = true
		endpoints[x.Endpoint] = true
		privateEndpoints[x.Endpoint] = true
		services[x.Service] = true
		links[x.Link] = true
		apps[x.Application] = true
		if x.External != nil {
			sources[x.External.Source] = true
			nodes[x.External.Node] = true
		}
	}
	for _, s := range full.Sources {
		if sources[s.ID] {
			out.Sources = append(out.Sources, s)
			paths[s.Path] = true
		}
	}
	for _, path := range full.Paths {
		if paths[path.ID] {
			// Dependency/candidate paths are collector evaluation relationships, not
			// additional routes executed by a worker. The complete topology retains them.
			path.DependsOn = nil
			path.Candidates = nil
			out.Paths = append(out.Paths, path)
			observers[path.Observer] = true
			for _, id := range path.Nodes {
				nodes[id] = true
			}
			for _, id := range path.Links {
				links[id] = true
			}
			for _, proxy := range path.Proxies {
				nodes[proxy.Node] = true
			}
		}
	}
	// Services and node parents form bounded reference graphs. Iterate to a fixed
	// point instead of depending on topology order.
	for changed := true; changed; {
		changed = false
		add := func(set map[string]bool, id string) {
			if id != "" && !set[id] {
				set[id] = true
				changed = true
			}
		}
		for _, s := range full.Services {
			if services[s.ID] {
				add(nodes, s.Node)
				add(apps, s.Application)
				for _, id := range s.DependsOn {
					add(services, id)
				}
				for _, id := range s.Endpoints {
					add(endpoints, id)
				}
			}
		}
		for _, ep := range full.Endpoints {
			if endpoints[ep.ID] {
				add(nodes, ep.Node)
				add(observers, ep.Observer)
				for _, id := range ep.Via {
					add(nodes, id)
				}
			}
		}
		for _, l := range full.Links {
			if links[l.ID] {
				add(nodes, l.From)
				add(nodes, l.To)
			}
		}
		for _, o := range full.Observers {
			if observers[o.ID] {
				add(nodes, o.Node)
			}
		}
		for _, n := range full.Nodes {
			if nodes[n.ID] {
				add(nodes, n.Parent)
			}
		}
	}
	for _, n := range public.Nodes {
		if nodes[n.ID] {
			keep := []string{}
			for _, id := range n.ResourceChecks {
				if checks[id] {
					keep = append(keep, id)
				}
			}
			n.ResourceChecks = keep
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, s := range public.Services {
		if services[s.ID] {
			out.Services = append(out.Services, s)
		}
	}
	for _, l := range public.Links {
		if links[l.ID] {
			out.Links = append(out.Links, l)
		}
	}
	for _, o := range public.Observers {
		if observers[o.ID] {
			out.Observers = append(out.Observers, o)
		}
	}
	for _, a := range public.Applications {
		if apps[a.ID] {
			out.Applications = append(out.Applications, a)
		}
	}
	for i, ep := range public.Endpoints {
		if endpoints[ep.ID] {
			if privateEndpoints[ep.ID] {
				ep = full.Endpoints[i]
			}
			out.Endpoints = append(out.Endpoints, ep)
		}
	}
	if e = config.Validate(out); e != nil {
		return out, errors.New("configuration_projection_invalid")
	}
	for _, x := range out.Checks {
		if config.Contract(out, x) != config.Contract(full, x) {
			return out, errors.New("configuration_projection_contract_mismatch")
		}
	}
	return out, nil
}
