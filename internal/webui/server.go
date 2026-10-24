//go:build webui

// Package webui hosts optional embedded browser assets and a restricted viewer
// surface. The collector remains the authority for authentication and evidence.
package webui

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/api"
)

//go:embed assets/*
var assets embed.FS

func Handler(backend http.Handler, host, scheme string) http.Handler {
	files, _ := fs.Sub(assets, "assets")
	static := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; connect-src 'self'; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'; object-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Exact port and fixed host reject DNS rebinding. localhost is a convenience
		// alias only for a listener actually bound to a literal loopback address.
		allowed := r.Host == host
		h, p, _ := net.SplitHostPort(host)
		ip := net.ParseIP(h)
		if ip != nil && ip.IsLoopback() {
			allowed = allowed || r.Host == net.JoinHostPort("localhost", p)
		}
		origin := r.Header.Get("Origin")
		if !allowed || origin != "" && origin != scheme+"://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "origin or host rejected", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			permitted := false
			switch r.URL.Path {
			case "/v1/snapshot", "/v1/config", "/v1/history", "/v1/rollups", "/v1/web/view", "/v1/web/events", "/v1/web/history":
				permitted = r.Method == http.MethodGet
			case "/v1/web/validate":
				permitted = r.Method == http.MethodPost
			}
			if !permitted {
				http.NotFound(w, r)
				return
			}
			// The browser boundary above has already checked the actual Web origin.
			// The collector's independent CORS setting is for its own API listener.
			clone := r.Clone(r.Context())
			clone.Header = r.Header.Clone()
			clone.Header.Del("Origin")
			backend.ServeHTTP(w, clone)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/core.js", "/map.js", "/style.css":
			static.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// Start binds synchronously, so a bad Web listener fails collector startup.
// Cancellation closes streaming readers before waiting for graceful shutdown.
func Start(address string, backend http.Handler, tc *tls.Config, failures chan<- error) (func(), string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, "", errors.New("web-listen must be host:port")
	}
	if _, e := strconv.ParseUint(port, 10, 16); e != nil {
		return nil, "", errors.New("invalid web-listen port")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || ip.IsUnspecified()) {
		return nil, "", errors.New("web-listen host must be a concrete literal IP or localhost")
	}
	if tc == nil && host != "localhost" && !ip.IsLoopback() {
		return nil, "", errors.New("non-loopback Web UI requires tls-cert and tls-key")
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, "", err
	}
	scheme := "http"
	if tc != nil {
		scheme = "https"
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{Handler: Handler(backend, ln.Addr().String(), scheme), TLSConfig: tc, ConnContext: api.ConnectionContext, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var err error
		limited := api.LimitListener(ln, 128)
		if tc != nil {
			err = srv.ServeTLS(limited, "", "")
		} else {
			err = srv.Serve(limited)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case failures <- fmt.Errorf("Web UI: %w", err):
			case <-ctx.Done():
			}
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if srv.Shutdown(deadline) != nil {
				_ = srv.Close()
			}
			<-done
		})
	}
	return stop, ln.Addr().String(), nil
}
