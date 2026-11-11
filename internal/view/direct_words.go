package view

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed direct.en.json
var directWordsJSON []byte
var directWords map[string]string
var directWordsOnce sync.Once

func DirectText(key string) string {
	directWordsOnce.Do(func() { _ = json.Unmarshal(directWordsJSON, &directWords) })
	return directWords[key]
}

// DirectConnection names the actual sources, including mixed-vendor profiles.
func DirectConnection(status SourcesStatus) string {
	adapter := ""
	for _, src := range status.Sources {
		if adapter != "" && adapter != src.Adapter {
			return DirectText("connection_mixed")
		}
		adapter = src.Adapter
	}
	switch adapter {
	case "beszel":
		return DirectText("connection")
	case "komari":
		return DirectText("connection_komari")
	case "cf-server-monitor":
		return DirectText("connection_cf_server_monitor")
	default:
		return DirectText("connection_mixed")
	}
}

func (v View) DirectConnection() string {
	status, _ := v.SourcesStatus()
	return DirectConnection(status)
}
func (v View) directVerdict() Verdict {
	status, _ := v.SourcesStatus()
	glyph := "○"
	if status.State == "ready" {
		glyph = "●"
	} else if status.State == "partial" {
		glyph = "▲"
	}
	text := DirectText(status.State)
	if status.Reason == "samples_stale" {
		text = DirectText(status.Reason)
	}
	return Verdict{State: status.State, Glyph: glyph, Text: text}
}
