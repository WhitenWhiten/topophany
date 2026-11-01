package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/monitor/stream"
)

func (a *App) monitorStream(ctx context.Context, args []string) error {
	f := a.flags("monitor stream")
	directRoot := f.String("direct-root", "", "absolute private direct configuration root")
	capabilities := f.Bool("capabilities", false, "print protocol, version, sources, visuals and metrics; then exit")
	profiles := f.Bool("profiles", false, "list saved client profile names, projects and active selection; then exit")
	credentials := f.String("credentials", "", "credential file for --profiles (default normal discovery)")
	if err := parsed(f, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &ExitError{Code: 2, Err: err}
	}
	if *directRoot != "" && !filepath.IsAbs(*directRoot) {
		return &ExitError{Code: 2, Err: errors.New("--direct-root must be absolute")}
	}
	if *capabilities && *profiles {
		return &ExitError{Code: 2, Err: errors.New("--capabilities and --profiles are mutually exclusive")}
	}
	if *credentials != "" && !*profiles {
		return &ExitError{Code: 2, Err: errors.New("--credentials requires --profiles; stream requests carry their own credentials")}
	}
	// stdout is a pipe contract: a closed reader is an ordinary orderly exit.
	signal.Ignore(syscall.SIGPIPE)
	write := func(p monitor.Panel) error { return stream.WriteFrame(a.Out, p) }
	var err error
	if *capabilities {
		err = writeCapabilities(a.Out)
	} else if *profiles {
		err = a.writeProfiles(*credentials)
	} else {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		inputDone := make(chan struct{})
		lines := make(chan stream.Line)
		go func() { defer close(inputDone); stream.ReadLines(runCtx, a.In, lines) }()
		go func() {
			select {
			case <-runCtx.Done():
				return
			case <-inputDone:
			}
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-runCtx.Done():
			case <-timer.C:
				cancel()
			}
		}()
		var closeSink func()
		write, closeSink = frameSink(runCtx, a.Out)
		defer closeSink()
		manager := stream.NewDirectManager(*directRoot, a.now)
		defer manager.Close()
		err = stream.RunClock(runCtx, lines, manager.Open, write, a.now)
		cancel()
		if closer, ok := a.In.(io.ReadCloser); ok {
			// An inherited blocking stdin descriptor may also block Close until
			// its writer closes. Shutdown must not wait for that writer.
			go closer.Close()
		}
		// A Reader without Close may be owned by the embedding caller and remain
		// blocked. Its sender still observes cancellation before delivering a line.
	}
	if err != nil && !errors.Is(err, context.Canceled) && !isStreamClosedPipe(err) {
		fmt.Fprintln(a.Err, "monitor stream output unavailable")
	}
	return nil
}

func isStreamClosedPipe(err error) bool {
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Windows pipe writes return Win32 errors instead of the POSIX EPIPE.
	const (
		errorBrokenPipe syscall.Errno = 109
		errorNoData     syscall.Errno = 232
	)
	return runtime.GOOS == "windows" && (errors.Is(err, errorBrokenPipe) || errors.Is(err, errorNoData))
}

func writeCapabilities(w io.Writer) error {
	return writeJSONLine(w, struct {
		Investigation bool             `json:"investigation"`
		Protocol      int              `json:"protocol"`
		Version       string           `json:"version"`
		Sources       []string         `json:"sources"`
		Visuals       []string         `json:"visuals"`
		Metrics       []monitor.Metric `json:"metrics"`
		Profiles      bool             `json:"profiles"`
		CollectorRead bool             `json:"collector_read"`
		ClientRetry   bool             `json:"client_retry"`
		CardHistory   bool             `json:"card_history"`
		Direct        any              `json:"direct"`
	}{true, 1, model.Version, []string{"client", "preview", "direct"}, []string{"text", "bar", "status", "list", "card"}, monitor.Catalog(), true, true, true, true, map[string]any{"schema": 1, "config_api": 1, "adapters": []string{"beszel", "komari"}, "allow_insecure": true, "retry": true, "limits": map[string]int{"sources_per_profile": 64, "objects_per_source": 20, "objects_per_profile": 256, "template_rows": 128}}})
}

func (a *App) writeProfiles(path string) error {
	var err error
	if path == "" {
		path, err = client.DefaultPath()
	}
	profiles := []client.ProfileSummary{}
	if err == nil {
		profiles, err = client.ListProfiles(path)
	}
	if err != nil {
		profiles = []client.ProfileSummary{}
		if !errors.Is(err, os.ErrNotExist) {
			// File paths, parser errors and endpoint validation errors can all
			// contain private input. The diagnostic is deliberately fixed.
			fmt.Fprintln(a.Err, "monitor stream profiles unavailable")
		}
	}
	return writeJSONLine(a.Out, struct {
		Profiles []client.ProfileSummary `json:"profiles"`
	}{profiles})
}

// Keep discovery on the same one-write, one-flush NDJSON sink contract.
func writeJSONLine(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	b = append(b, '\n')
	n, e := w.Write(b)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
