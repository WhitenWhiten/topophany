//go:build webui

package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/view"
	"github.com/WhitenWhiten/topophany/internal/webmap"
)

func NativeMap(s model.Snapshot, mode string, selected view.Row, cached bool) webmap.Map {
	const w, h = 180, 28
	out := webmap.Map{Width: w * 8, Height: h * 16, Mode: mode, Nodes: []webmap.MapNode{}, Edges: []webmap.MapEdge{}, Places: []webmap.Place{}}
	v := view.View{Snapshot: s, Cached: cached}
	nodes, edges := v.Graph()
	if !mapLayoutAllowed(w, h, len(nodes), len(edges)) {
		out.Notice = "Map complexity limit: topology remains available in lists and details."
		return out
	}
	m := Model{Snapshot: s, Observer: s.ViewObserver, Now: s.GeneratedAt, FullMap: true, Width: w, Height: h}
	if cached {
		m.Error = "cached"
	}
	if mode == "globe" {
		d := &webGlobeDots{points: map[[2]int]webmap.Dot{}, priorities: map[[2]int]int8{}}
		marks := m.paintGlobe(d, 160, 160, 1, float64(s.GeneratedAt.Unix()%360), 0)
		out.Width, out.Height = 640, 640
		out.Nodes = []webmap.MapNode{}
		for y := 0; y < 160; y++ {
			for x := 0; x < 160; x++ {
				if dot, ok := d.points[[2]int{x, y}]; ok {
					out.Globe = append(out.Globe, dot)
				}
			}
		}
		for _, mark := range marks {
			out.Places = append(out.Places, webmap.Place{Name: mark.name, X: mark.x*4 + 12, Y: mark.y * 4, W: 0, H: 0})
		}
		return out
	}
	f := m.focus(selected)
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, webmap.MapNode{MapNode: n, Signal: view.NodeSignal(n), Focus: f.hasNode(n.ID)})
	}
	routePoints := map[string][][2]float64{}
	switch mode {
	case "virtual":
		l := m.layoutVirtual(w, h, 0, nodes, edges, f)
		if l == nil {
			out.Notice = "No declared locations; showing the network graph."
			out.Mode = "network"
		} else {
			if !l.fits() {
				for columns := l.cols - 1; columns > 0; columns-- {
					candidate := m.layoutVirtual(w, h, columns, nodes, edges, f)
					if candidate != nil && candidate.fits() {
						l = candidate
						break
					}
				}
			}
			if !l.fits() {
				out.Notice = "Map complexity limit: switch to network or use the topology list."
				out.Nodes = []webmap.MapNode{}
				return out
			}
			paths := l.routeBudget(newLayoutWork())
			for i, cells := range paths {
				e := l.drawn[i].e
				key := e.ID + "/" + e.From + "/" + e.To
				for _, p := range corners(cells, w) {
					routePoints[key] = append(routePoints[key], [2]float64{float64(p[0] * 8), float64(p[1]*16 + 8)})
				}
			}
			for _, c := range l.cards {
				out.Places = append(out.Places, webmap.Place{Name: c.title(), X: c.x * 8, Y: c.y * 16, W: c.w * 8, H: c.h * 16})
			}
			for i := range out.Nodes {
				c := l.cards[l.cardOf[i]]
				out.Nodes[i].X = float64((c.x + 2) * 8)
				out.Nodes[i].Y = float64((c.y+l.rowOf[i])*16 + 8)
			}
		}
	case "geo":
		pts := [][2]float64{}
		for _, n := range nodes {
			if n.Located {
				pts = append(pts, [2]float64{n.Lat, n.Lon})
			}
		}
		g := fitGeo(pts, float64(w*2), float64(h*4), 1, 0, nil)
		// A Braille land layer from the existing public-domain land mask; nodes,
		// wires and labels remain independent SVG objects, never character frames.
		for cy := 0; cy < h; cy++ {
			line := make([]rune, w)
			for cx := 0; cx < w; cx++ {
				mask := 0
				bits := [4][2]int{{0, 3}, {1, 4}, {2, 5}, {6, 7}}
				for dy := 0; dy < 4; dy++ {
					for dx := 0; dx < 2; dx++ {
						lat, lon, ok := g.at(float64(cx*2+dx), float64(cy*4+dy))
						if ok && land(lat, lon) {
							mask |= 1 << bits[dy][dx]
						}
					}
				}
				line[cx] = rune(0x2800 + mask)
			}
			out.Land = append(out.Land, string(line))
		}
		unplaced := 0
		at := map[string]int{}
		for i, n := range nodes {
			if n.Located {
				x, y := g.project(n.Lat, n.Lon)
				key := fmt.Sprintf("%.2f/%.2f", n.Lat, n.Lon)
				off := at[key]
				at[key]++
				out.Nodes[i].X = x*4 + float64(off%3)*16
				out.Nodes[i].Y = y*4 + float64(off/3)*20
			} else {
				out.Nodes[i].X = float64(20 + unplaced%6*230)
				out.Nodes[i].Y = float64(out.Height - 45 + unplaced/6*18)
				unplaced++
			}
		}
	}
	if out.Mode == "network" {
		depth := layers(nodes, layoutEdges(nodes, edges))
		cols := map[int][]int{}
		maxD := 0
		for i, n := range nodes {
			d := depth[n.ID]
			cols[d] = append(cols[d], i)
			maxD = max(maxD, d)
		}
		for d, indices := range cols {
			for j, i := range indices {
				out.Nodes[i].X = 40 + float64(d)*float64(out.Width-260)/float64(max(1, maxD))
				out.Nodes[i].Y = 45 + (float64(j)+.5)*float64(out.Height-100)/float64(len(indices))
			}
		}
	}
	for _, e := range edges {
		if mode != "network" && e.Kind == edgePath && !f.hasEdge(e) && signalRank(e.Signal) < 3 && (m.Observer == "" || e.Observer != m.Observer) {
			continue
		}
		kind, id := "", ""
		switch e.Kind {
		case edgeLink:
			kind, id = "link", e.ID
		case edgePath:
			kind, id = "path", strings.TrimPrefix(e.ID, "path:")
		case edgeVia:
			kind, id = "endpoint", strings.TrimPrefix(e.ID, "via:")
		case edgeContain:
			kind, id = "node", strings.TrimPrefix(e.ID, "parent:")
		}
		out.Edges = append(out.Edges, webmap.MapEdge{MapEdge: e, KindName: view.EdgeKinds[e.Kind], TargetKind: kind, TargetID: id, Focus: f.hasEdge(e), Points: routePoints[e.ID+"/"+e.From+"/"+e.To]})
	}
	// Guard against a malformed optional layout leaking non-finite coordinates.
	for i := range out.Nodes {
		n := &out.Nodes[i]
		if math.IsNaN(n.X) || math.IsInf(n.X, 0) {
			n.X = 0
		}
		if math.IsNaN(n.Y) || math.IsInf(n.Y, 0) {
			n.Y = 0
		}
	}
	return out
}

// A plotter of independent SVG points, with no terminal cell representation.
type webGlobeDots struct {
	points     map[[2]int]webmap.Dot
	priorities map[[2]int]int8
}

func (d *webGlobeDots) Set(x, y int, c Color, priority int8) {
	if x < 0 || x >= 160 || y < 0 || y >= 160 {
		return
	}
	key := [2]int{x, y}
	if old, exists := d.priorities[key]; exists && old > priority {
		return
	}
	d.priorities[key] = priority
	d.points[key] = webmap.Dot{X: x * 4, Y: y * 4, Color: fmt.Sprintf("#%06x", uint32(c)&0xffffff)}
}
