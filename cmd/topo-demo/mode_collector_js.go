//go:build js && wasm && collector

package main

import (
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/tui"
)

const previewMode = false

func newBrowserSession(input browserInput, _ time.Time) (browserSession, error) {
	if input.Mode != "live" {
		return nil, errors.New("this viewer requires a collector connection")
	}
	return tui.NewRemote(client.Profile{Project: input.Project,
		Servers: []client.Endpoint{{URL: input.Server, Token: input.Token}}}, input.ReconnectHint, input.Language)
}
