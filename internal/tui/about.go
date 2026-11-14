package tui

import (
	"math"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// Topophany = τόπος (place) + -phany (φαίνειν, to show): the appearance of
// place. The About art is a lit, dotted globe on which the places declared
// in the topology are the only lights.

var font5x7 = map[rune][7]string{
	'T': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'A': {".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'N': {"#...#", "##..#", "#.#.#", "#..##", "#...#", "#...#", "#...#"},
	'Y': {"#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."},
}

type place struct {
	name     string
	lat, lon float64
	health   string
}

var defaultPlaces = []place{
	{"Shanghai", 31.2, 121.5, ""}, {"Tokyo", 35.7, 139.7, ""}, {"Singapore", 1.35, 103.8, ""},
	{"Frankfurt", 50.1, 8.7, ""}, {"London", 51.5, -0.1, ""}, {"Ashburn", 39.0, -77.5, ""},
	{"Los Angeles", 34.05, -118.25, ""}, {"São Paulo", -23.5, -46.6, ""}, {"Sydney", -33.9, 151.2, ""},
	{"Mumbai", 19.1, 72.9, ""},
}

// aboutPlaces uses the operator's declared node locations when present, so
// the art literally shows *their* places appearing. Nodes at one declared
// place become one light. Links are left to the maps: on the globe they
// would only tangle.
func (m *Model) aboutPlaces() []place {
	nodes, _, _ := m.graph()
	ps := []place{}
	byPlace := map[string]int{}
	for _, n := range nodes {
		if !n.Located || n.Inherited {
			continue
		}
		key := n.Place
		if key == "" {
			key = n.ID
		}
		if k, ok := byPlace[key]; ok {
			ps[k].health = worseSignal(ps[k].health, nodeSignal(n))
			continue
		}
		name := n.Place
		if name == "" {
			name = n.Name
		}
		byPlace[key] = len(ps)
		ps = append(ps, place{name, n.Lat, n.Lon, nodeSignal(n)})
	}
	if len(ps) >= 2 {
		return ps
	}
	return defaultPlaces
}

type vec [3]float64

func (a vec) dot(b vec) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a vec) norm() vec {
	l := math.Sqrt(a.dot(a))
	return vec{a[0] / l, a[1] / l, a[2] / l}
}
func sphere(lat, lon float64) vec {
	la, lo := lat*math.Pi/180, lon*math.Pi/180
	return vec{math.Cos(la) * math.Sin(lo), math.Sin(la), math.Cos(la) * math.Cos(lo)}
}

// globe transforms: world -> view rotates by spin around the polar axis and
// then tilts the north pole toward the viewer.
type globe struct{ spin, tilt float64 }

func (g globe) toView(v vec) vec {
	s, c := math.Sin(-g.spin), math.Cos(-g.spin)
	x, z := v[0]*c+v[2]*s, -v[0]*s+v[2]*c
	y := v[1]
	ts, tc := math.Sin(g.tilt), math.Cos(g.tilt)
	return vec{x, y*tc - z*ts, y*ts + z*tc}
}

var (
	cLandDay    = Hex(0xB4F5E8)
	cLandDusk   = Hex(0x2F9389)
	cLandDark   = Hex(0x1A4448)
	cSeaNight   = Hex(0x0C1922)
	cSeaDay     = Hex(0x1A4B69)
	cSeaGlint   = Hex(0x4C93B8)
	cGroundDark = Hex(0x0E2321)
	cGroundDay  = Hex(0x23584F)
	cGraticule  = Hex(0x3E86AD)
	cLimb       = Hex(0x235A77)
	cCity       = Hex(0xFCD34D)
)

func (g globe) toWorld(v vec) vec {
	ts, tc := math.Sin(-g.tilt), math.Cos(-g.tilt)
	y, z := v[1]*tc-v[2]*ts, v[1]*ts+v[2]*tc
	s, c := math.Sin(g.spin), math.Cos(g.spin)
	return vec{v[0]*c + z*s, y, -v[0]*s + z*c}
}

// cellAspect is how many times taller than wide a terminal cell is, as the
// terminal reports it; 2, at which a cell's 2x4 braille dots are square,
// when it does not say.
func (m *Model) cellAspect() float64 {
	if m.Cell < 1 || m.Cell > 4 {
		return 2
	}
	return m.Cell
}

// drawGlobe renders the globe centred in w×h cells and names the places
// facing the viewer. t is seconds since the About view opened; spin is extra
// rotation from the mouse wheel.
func (m *Model) drawGlobe(cv *Canvas, x, y, w, h int, t, spin float64) {
	d := NewDots(w, h)
	var p plotter = d
	if m.profile() == color256 {
		// The xterm cube turns a smooth shade into grey and teal blocks;
		// these terminals get the dotted ocean instead.
		p = dotsOnly{d}
	}
	marks := m.paintGlobe(p, w*2, h*4, m.cellAspect()/2, t, spin)
	d.Blit(cv, x, y, 0)
	// Names of the places facing the viewer, where they do not collide.
	used := map[[2]int]bool{}
	for _, mk := range marks {
		lx, ly := mk.x/2+2, mk.y/4
		n := width(mk.name)
		free := lx+n < w && ly >= 0 && ly < h
		for i := -1; i <= n && free; i++ {
			free = !used[[2]int{lx + i, ly}]
		}
		if !free {
			continue
		}
		for i := -1; i <= n; i++ {
			used[[2]int{lx + i, ly}] = true
		}
		cv.Text(x+lx, y+ly, mk.name, fg(Mix(mk.c, cText, .55)).bold(), -1)
	}
}

// globeMark is a place worth naming, in dots.
type globeMark struct {
	x, y int
	name string
	c    Color
}

// paintGlobe draws the globe centred on a W×H dot grid whose dots are ky
// times as tall as they are wide, so the ball is round where the dots land
// rather than in the grid. Lengths are in dot widths. Where the plotter
// can tint the ground beneath its dots (the terminal's cells, the banner's
// dots) the sphere is shaded: an ocean lit from the upper left with a
// specular glint and land a lighter ground, both falling off across a soft
// terminator and toward the limb, so it reads as a ball rather than a disc.
// Over that, land is an ordered dither of lit dots with a crisp coast, a
// graticule curves with the surface, the limb is a thin line brightest
// sunward and a dithered haze lies beyond it. It returns the places facing
// the viewer.
func (m *Model) paintGlobe(d plotter, W, H int, ky, t, spin float64) []globeMark {
	short := math.Min(float64(W), float64(H)*ky)
	R := short/2 - math.Max(3, short*.05)
	if R < 6 {
		return nil
	}
	cx, cy := float64(W)/2, float64(H)/2
	places := m.aboutPlaces()
	// Start with the first declared place facing the viewer, then drift east.
	g := globe{spin: places[0].lon*math.Pi/180 - .35 + t*.11 + spin, tilt: .36}
	light := vec{-.62, .42, .66}.norm()
	half := vec{light[0], light[1], light[2] + 1}.norm()
	sh, shading := d.(shader)
	normal := func(px, py int) vec {
		nx, ny := (float64(px)+.5-cx)/R, (float64(py)+.5-cy)*ky/R
		return vec{nx, -ny, math.Sqrt(math.Max(0, 1-nx*nx-ny*ny))}
	}
	// Once per frame, for every dot on the disc: land or sea, and the
	// graticule band it lies in. A dot whose right or lower neighbour lies
	// in another band is on a grid line, so lines are one dot wide and bend
	// with the surface. Meridians stop short of the poles, where they bunch.
	grid := make([]bool, W*H)
	band := make([]int16, W*H)
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			i := py*W + px
			band[i] = -1
			if math.Hypot(float64(px)+.5-cx, (float64(py)+.5-cy)*ky) > R+.5 {
				continue
			}
			wv := g.toWorld(normal(px, py))
			lat := math.Asin(clamp(wv[1], -1, 1)) * 180 / math.Pi
			lon := math.Atan2(wv[0], wv[2]) * 180 / math.Pi
			grid[i] = land(lat, lon)
			band[i] = int16((lat+90)/30) * 16
			if math.Abs(lat) < 70 {
				band[i] += int16((lon+180)/30) + 1
			}
		}
	}
	isLand := func(px, py int) bool { return px >= 0 && py >= 0 && px < W && py < H && grid[py*W+px] }
	onLine := func(px, py int) bool {
		i := py*W + px
		differs := func(j int) bool {
			a, b := band[i], band[j]
			if b < 0 {
				return false
			}
			// Parallels always; meridians only where both dots carry one.
			return a/16 != b/16 || a%16 != 0 && b%16 != 0 && a%16 != b%16
		}
		return px+1 < W && differs(i+1) || py+1 < H && differs(i+W)
	}
	// Shades are quantized: neighbouring cells share colors, which keeps
	// the escape stream small without visible banding under the dots.
	landColor := func(lit, z float64) Color {
		lit, z = math.Round(lit*10)/10, math.Round(z*5)/5
		c := Mix(cLandDark, cLandDusk, math.Min(1, lit*3))
		if lit > .33 {
			c = Mix(cLandDusk, cLandDay, math.Pow((lit-.33)/.67, .8))
		}
		return Mix(cInk, c, .5+.5*math.Sqrt(z))
	}
	thick := math.Max(3, R*.09)
	for py := 0; py < H; py++ {
		for px := 0; px < W; px++ {
			dx, dy := float64(px)+.5-cx, (float64(py)+.5-cy)*ky
			r := math.Hypot(dx, dy)
			// How squarely this edge of the disc faces the light.
			side := 0.0
			if r > 0 {
				side = math.Max(0, (dx*light[0]-dy*light[1])/r)
			}
			switch {
			case r > R+thick*2:
				// Sparse stars far from the globe.
				hs := hash32(string([]byte{byte(px), byte(px >> 8), byte(py), byte(py >> 8)}))
				if hs%241 == 0 {
					tw := .5 + .5*math.Sin(t*(.9+float64(hs%7)*.3)+float64(hs%97))
					if tw > .3 {
						d.Set(px, py, Mix(cInk, cMuted, .25+.45*tw), 0)
					}
				}
			case r > R+thick:
			case r > R+.5:
				// Atmosphere: an ordered-dither haze, brightest sunward.
				f := 1 - (r-R)/thick
				if dither(px, py, f*f*(.18+.82*side)) {
					d.Set(px, py, Mix(cInk, cAccent2, .25+.5*math.Round(f*4)/4), 1)
				}
			default:
				v := normal(px, py)
				nl := v.dot(light)
				lit := math.Max(0, nl)
				// Lambert light through a soft terminator, darkening
				// toward the limb.
				day := clamp((nl+.12)/.42, 0, 1)
				day = day * day * (3 - 2*day)
				level := math.Round((.1+.9*day*(.4+.6*lit))*math.Pow(v[2], .3)*16) / 16
				sea := Mix(cSeaNight, cSeaDay, level)
				onLand := grid[py*W+px]
				if shading {
					c := sea
					if onLand {
						c = Mix(cGroundDark, cGroundDay, level)
					} else if spec := math.Pow(math.Max(0, v.dot(half)), 24); spec > .04 {
						c = Mix(c, cSeaGlint, math.Round(spec*8)/8)
					}
					sh.Shade(px, py, c, clamp(R+.5-r, 0, 1))
				}
				// The limb is one dot deep: deeper toward the poles when
				// a row of dots is taller than a dot is wide, so it does
				// not break where the rows step over it.
				deep := 1.0
				if ky > 1 && r > 0 {
					deep += (ky - 1) * dy * dy / (r * r)
				}
				switch {
				case r > R-deep:
					// The limb: one thin line, so the sphere keeps its
					// shape on the night side too.
					d.Set(px, py, Mix(cLimb, cAccent2, math.Round(side*4)/4*.6), 1)
				case onLand:
					if !isLand(px-1, py) || !isLand(px+1, py) || !isLand(px, py-1) || !isLand(px, py+1) {
						d.Set(px, py, landColor(math.Max(lit, .2), v[2]), 2)
					} else if dither(px, py, .22+.6*lit) {
						d.Set(px, py, landColor(lit, v[2]), 2)
					}
				case onLine(px, py):
					d.Set(px, py, Mix(sea, cGraticule, .3+.4*level), 1)
				case !shading:
					// Without a ground to shade, the ocean is a sparse
					// halftone that follows the light.
					spec := math.Pow(math.Max(0, v.dot(half)), 26)
					if dither(px, py, .16*math.Pow(lit, 1.3)+.4*spec) {
						d.Set(px, py, Mix(cSeaDay, cSeaGlint, math.Round(math.Min(1, lit*.7+spec)*6)/6), 0)
					}
				}
			}
		}
	}
	project := func(v vec) (float64, float64, vec) {
		p := g.toView(v)
		return cx + p[0]*R, cy - p[1]*R/ky, p
	}
	// Places: a bright core and a slow ring; their health colors them when
	// the topology has evidence, otherwise they are warm lights.
	marks := []globeMark{}
	for i, p := range places {
		sx, sy, v := project(sphere(p.lat, p.lon))
		if v[2] < .08 {
			continue
		}
		c := cCity
		if p.health != "" && p.health != "unknown" {
			c = signalColor(p.health)
		}
		core := 1
		if R < 40 {
			core = 0
		}
		for dy := -core; dy <= 1; dy++ {
			for dx := -core; dx <= 0; dx++ {
				d.Set(int(sx)+dx, int(sy)+dy, c, 9)
			}
		}
		grow := math.Min(1, R/60)
		ring := 2 + 3*grow*math.Mod(t*.6+float64(i)*.23, 1)
		halo(d, sx, sy, ring, ky, Mix(c, cInk, .3+ring/8), 7)
		if v[2] > .3 && R >= 30 {
			marks = append(marks, globeMark{int(sx), int(sy), p.name, c})
		}
	}
	return marks
}

// drawWordmark sets TOPOPHANY in a 5x7 pixel face, two pixels per cell with
// half blocks, filled with an ordered-dither gradient that a slow highlight
// sweeps across. It dithers into view from the left on opening. Returns its
// height in rows.
func (m *Model) drawWordmark(cv *Canvas, x, y int, t float64) int {
	const word = "TOPOPHANY"
	gw, gh := len(word)*6-1, 7
	on := func(gx, gy int) bool {
		if gx < 0 || gy < 0 || gy >= gh || gx >= gw || gx%6 == 5 {
			return false
		}
		return font5x7[rune(word[gx/6])][gy][gx%6] == '#'
	}
	ramp := []Color{cGlow, Hex(0xA7F3E4), cAccent, Hex(0x2DB3A3), Hex(0x1F7F78)}
	band := math.Mod(t*9, float64(gw)*2.2) - 12
	pixel := func(gx, gy int) Color {
		reveal := clamp(t*1.4-float64(gx)/float64(gw)*.9, 0, 1)
		if on(gx, gy) {
			if reveal < bayer8[gy&7][gx&7] {
				return 0
			}
			v := float64(gy)/float64(gh-1)*float64(len(ramp)-1) + bayer8[gy&7][gx&7] - .5
			c := ramp[int(clamp(math.Round(v), 0, float64(len(ramp)-1)))]
			if b := math.Exp(-math.Pow(float64(gx)-band-float64(gy)*.6, 2) / 18); b > .05 {
				c = Mix(c, cGlow, b*.8)
			}
			return c
		}
		if on(gx-1, gy-1) && reveal >= 1 && !m.NoColor {
			return Mix(cAccent, cInk, .8) // the shadow
		}
		return 0
	}
	rows := (gh + 2) / 2
	for r := 0; r < rows; r++ {
		for gx := 0; gx <= gw; gx++ {
			top, bottom := pixel(gx, r*2), pixel(gx, r*2+1)
			switch {
			case top != 0 && bottom != 0 && top == bottom:
				cv.Set(x+gx, y+r, '█', fg(top))
			case top != 0 && bottom != 0:
				cv.Set(x+gx, y+r, '▀', fg(top).Bg(bottom))
			case top != 0:
				cv.Set(x+gx, y+r, '▀', fg(top))
			case bottom != 0:
				cv.Set(x+gx, y+r, '▄', fg(bottom))
			}
		}
	}
	return rows
}

type aboutLine struct {
	parts  []span
	action string
	url    string // the last part is a link to url
	drop   int    // when rows are short, 2 goes first, then 1; 0 stays
}

// Where Topophany lives.
const (
	homepage = "https://topophany.com"
	repo     = "https://github.com/WhitenWhiten/topophany"
)

// aboutText is the right-hand (or lower) column of the About view; the
// caption about the globe is left out when there is no globe.
func (m *Model) aboutText(globe bool) []aboutLine {
	c := m.Snapshot.Topology
	sp := func(s string, st Style) span { return span{s, st} }
	greek, latin, gloss := fg(cAccent).bold(), fg(cMuted).italic(), fg(cText)
	link := func(label, url string) aboutLine {
		return aboutLine{parts: []span{sp("↗ ", fg(cAccent)), sp(fit(label, 10), latin), sp(strings.TrimPrefix(url, "https://"), fg(cAccent2).With(Underline))}, url: url}
	}
	lines := []aboutLine{
		{parts: []span{sp("τόπος", greek), sp("   tópos    ", latin), sp("place", gloss)}},
		{parts: []span{sp("φαίνω", greek), sp("   phaínō   ", latin), sp("to bring to light · to appear", gloss)}},
		{},
		{parts: []span{sp("topophany", fg(cGlow).bold()), sp("  n.  ", latin), sp("the appearance of a place —", gloss)}},
		{parts: []span{sp("the moment a network shows where it actually is.", gloss)}},
		{},
		{parts: []span{sp("From where, through which path, reaching what,", fg(cAccent))}},
		{parts: []span{sp("and on what evidence.", fg(cAccent))}},
		{},
		link("homepage", homepage),
		link("source", repo),
	}
	if m.Device == nil {
		// A touch screen has its settings in the sidebar and under ⋯.
		lines = append(lines, aboutLine{parts: []span{sp(", ", fg(cAccent).bold()), sp(fit("settings", 10), latin), sp("display & startup preferences", fg(cAccent2))}, action: "key:,"})
	}
	lines = append(lines, []aboutLine{
		{},
		{parts: []span{sp("Topophany "+model.Version, fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("MIT", fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("self-hosted", fg(cMuted)), sp("  ·  ", fg(cFaint)), sp("topo", fg(cText).bold())}},
	}...)
	if c.Project != "" {
		lines = append(lines, aboutLine{parts: []span{sp(c.Project, fg(cText)), sp("  ·  "+plural(len(c.Nodes), "node")+"  ·  "+plural(len(c.Links), "link")+"  ·  "+plural(len(c.Checks), "check"), fg(cMuted))}})
	}
	lines = append(lines, m.aboutKeys()...)
	if globe {
		lines = append(lines, aboutLine{drop: 2}, aboutLine{parts: []span{sp("Each lit point on the globe is a place you declared.", fg(cFaint).italic())}, drop: 2})
	}
	return lines
}

// aboutKeys is a reference to the keys of the mode the UI is in, as
// keycaps three to a row.
func (m *Model) aboutKeys() []aboutLine {
	if m.Device != nil {
		return nil // a touch screen says how it is used beside About (drawAboutKeys)
	}
	keys := [][2]string{{"1–9", "tabs"}, {"↑↓", "move"}, {"↵", "evidence"}, {"/", "filter"}, {"v", "vantage"}, {"m", "map"}, {"z", "zoom"}, {"f", "full map"}, {"p", "map labels"}, {"s", "uniform"}, {"P", "products"}}
	switch {
	case m.Edit != nil:
		keys = append(keys, [2]string{"a", "add"}, [2]string{"e", "edit"}, [2]string{"d", "delete"}, [2]string{"u", "undo"}, [2]string{"^S", "save"})
	case m.Preview != nil:
		keys = append(keys, [2]string{"r", "refresh"}, [2]string{"x", "scenario"}, [2]string{"D", "debug"})
	default:
		keys = append(keys, [2]string{"r", "refresh"})
	}
	keys = append(keys, [2]string{"?", "back"}, [2]string{"q", "quit"})
	lines := []aboutLine{{drop: 1}}
	for i := 0; i < len(keys); i += 3 {
		l := aboutLine{drop: 1}
		for _, k := range keys[i:min(i+3, len(keys))] {
			pad := 3 - width(k[0])
			cap := strings.Repeat(" ", pad/2+1) + k[0] + strings.Repeat(" ", pad-pad/2+1)
			if m.NoColor {
				cap = "[" + k[0] + "]" + strings.Repeat(" ", pad)
			}
			l.parts = append(l.parts, span{cap, fg(cAccent).Bg(cKeyBG).bold()}, span{" " + fit(k[1], 13), fg(cMuted)})
		}
		lines = append(lines, l)
	}
	return lines
}

// fitAbout fits the column into n rows: the globe's caption goes first,
// then the keys, then the blank rows between paragraphs, from the bottom up.
func fitAbout(lines []aboutLine, n int) []aboutLine {
	for _, drop := range []int{2, 1} {
		if len(lines) <= n {
			return lines
		}
		kept := []aboutLine{}
		for _, l := range lines {
			if l.drop != drop {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	for i := len(lines) - 1; i >= 0 && len(lines) > n; i-- {
		if len(lines[i].parts) == 0 {
			lines = append(lines[:i:i], lines[i+1:]...)
		}
	}
	return lines
}

const aboutTextW = 58

// drawAbout composes the globe and the text for the space available:
// side by side and centred when wide, the globe over the text when tall,
// the text alone when small. The text fits the rows it is given.
func (m *Model) drawAbout(cv *Canvas, x, y, w, h int) {
	t := m.Now.Sub(m.aboutAt).Seconds()
	if m.aboutAt.IsZero() || t < 0 {
		t = 0
	}
	spin := m.aboutSpin
	lines := m.aboutText(true)
	big := w >= aboutTextW+4
	head := 5 // the wordmark and a blank row
	if !big {
		head = 2
	}
	core := 0 // the rows that always stay
	for _, l := range lines {
		if l.drop == 0 {
			core++
		}
	}
	tw := min(aboutTextW, w-2)
	cell := m.cellAspect()
	var gx, gy, gw, gh, tx, ty int
	switch {
	case w >= aboutTextW+44 && h >= 14:
		// Side by side: the globe is as tall as the view allows (and no
		// wider than leaves room for the text), the pair centred.
		gh = h
		gw = min(int(float64(gh)*cell)+2, w-aboutTextW-6)
		gh = min(gh, int(float64(gw)/cell)+1)
		total := gw + 6 + tw
		gx = x + max(0, (w-total)/2)
		gy = y + (h-gh)/2
		tx = gx + gw + 6
		lines = fitAbout(lines, h-head)
		ty = y + max(0, (h-len(lines)-head)/2)
	case h >= core+head+12 && w >= 30:
		// Stacked: the globe above, the text below, both centred; the
		// globe takes its rows before the keys do.
		gh = min(h-core-head-1, int(float64(w)/cell))
		gw = min(w, int(float64(gh)*cell)+2)
		lines = fitAbout(lines, h-gh-1-head)
		gx = x + (w-gw)/2
		gy = y + max(0, (h-gh-len(lines)-head-1)/2)
		tx = x + max(1, (w-tw)/2)
		ty = gy + gh + 1
	default:
		lines = fitAbout(m.aboutText(false), h-head)
		tx = x + max(1, (w-tw)/2)
		ty = y + max(0, (h-len(lines)-head)/2)
	}
	if gw >= 16 && gh >= 7 {
		m.drawGlobe(cv, gx, gy, gw, gh, t, spin)
	} else if gw > 0 {
		lines = fitAbout(m.aboutText(false), y+h-ty-head)
	}
	if big && ty+5 < y+h {
		ty += m.drawWordmark(cv, tx, ty, t) + 1
	} else {
		cv.Text(tx, ty, "T O P O P H A N Y", fg(cAccent).bold(), tw)
		ty += 2
	}
	// Lines fade up one after another on opening.
	for i, l := range lines {
		if ty >= y+h {
			break
		}
		k := clamp(t*1.6-float64(i)*.16, 0, 1)
		cx := tx
		for j, s := range l.parts {
			st := s.st
			st.FG = Mix(cInk, st.FG, k)
			if st.BG != 0 {
				st.BG = Mix(cInk, st.BG, k)
			}
			n := cv.Text(cx, ty, s.s, st, tx+tw-cx)
			if l.url != "" && j == len(l.parts)-1 {
				cv.Link(cx, ty, n, l.url)
			}
			cx += n
		}
		if l.action != "" {
			m.hit(tx, ty, tw, l.action)
		}
		ty++
	}
}
