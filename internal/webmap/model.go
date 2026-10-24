//go:build webui

// Package webmap defines optional browser geometry independently of collector and renderer.
package webmap

import "github.com/WhitenWhiten/topophany/internal/view"

// Map is geometry, never a terminal frame. Native SVG consumes the same
// virtual placement, geographic projection and graph/focus semantics as the TUI.
type Map struct {
	Width  int       `json:"width"`
	Height int       `json:"height"`
	Mode   string    `json:"mode"`
	Nodes  []MapNode `json:"nodes"`
	Edges  []MapEdge `json:"edges"`
	Places []Place   `json:"places"`
	Land   []string  `json:"land,omitempty"`
	Globe  []Dot     `json:"globe,omitempty"`
	Notice string    `json:"notice,omitempty"`
}
type MapNode struct {
	view.MapNode
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Signal string  `json:"display_signal"`
	Focus  bool    `json:"focus"`
}
type MapEdge struct {
	view.MapEdge
	KindName   string       `json:"kind"`
	Points     [][2]float64 `json:"points,omitempty"`
	TargetKind string       `json:"target_kind,omitempty"`
	TargetID   string       `json:"target_id,omitempty"`
	Focus      bool         `json:"focus"`
}
type Place struct {
	Name string `json:"name"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

// Dot is a point of the About globe's existing stippled illustration.
type Dot struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Color string `json:"color"`
}
