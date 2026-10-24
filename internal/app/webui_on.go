//go:build webui

package app

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"

	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/tui"
	"github.com/WhitenWhiten/topophany/internal/webui"
)

type webFlags struct {
	enabled *bool
	listen  *string
}

func addWebFlags(f *flag.FlagSet) webFlags {
	return webFlags{f.Bool("web", false, "enable the native read-only Web UI"), f.String("web-listen", "127.0.0.1:8080", "Web UI listener (non-loopback requires TLS)")}
}
func (f webFlags) validate() error { return nil }
func (a *App) startWeb(f webFlags, handler http.Handler, tc *tls.Config, failures chan<- error) (func(), error) {
	if !*f.enabled {
		return func() {}, nil
	}
	stop, addr, err := webui.Start(*f.listen, handler, tc, failures)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if tc != nil {
		scheme = "https"
	}
	fmt.Fprintf(a.Err, "Native Web UI listening on %s://%s\n", scheme, addr)
	return stop, nil
}

func configureWeb(backend *api.Server, enabled bool) {
	backend.WebEnabled = enabled
	if enabled {
		backend.WebMap = tui.NativeMap
	}
}
