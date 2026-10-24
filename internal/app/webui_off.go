//go:build !webui

package app

import (
	"crypto/tls"
	"errors"
	"flag"
	"github.com/WhitenWhiten/topophany/internal/api"
	"net/http"
)

type webFlags struct {
	enabled *bool
	listen  *string
}

func addWebFlags(f *flag.FlagSet) webFlags {
	return webFlags{f.Bool("web", false, "enable optional native Web UI (requires -tags webui)"), f.String("web-listen", "127.0.0.1:8080", "optional Web UI listener")}
}
func (f webFlags) validate() error {
	if *f.enabled {
		return errors.New("Web UI is not compiled in; rebuild with go build -tags webui ./cmd/topo")
	}
	return nil
}
func (a *App) startWeb(f webFlags, handler http.Handler, tc *tls.Config, failures chan<- error) (func(), error) {
	return func() {}, nil
}

func configureWeb(backend *api.Server, enabled bool) {}
