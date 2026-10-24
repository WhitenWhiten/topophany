package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/alert"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/logging"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"github.com/WhitenWhiten/topophany/internal/rollup"
	"github.com/WhitenWhiten/topophany/internal/runner"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
)

func (a *App) serve(parent context.Context, args []string) (retErr error) {
	f := a.flags("serve")
	panelFlags := addMonitorFlags(f)
	webFlags := addWebFlags(f)
	logs := addLogFlags(f)
	memory := addMemoryFlags(f)
	path := f.String("config", "topology.json", "reviewed local topology JSON")
	authPath := f.String("auth", "auth.json", "0600 collector token-hash configuration")
	alertsPath := f.String("alerts", "", "0600 webhook configuration; no alerts when empty")
	dir := f.String("data", "./collector-data", "exclusive local journal directory")
	listen := f.String("listen", "127.0.0.1:7447", "collector listen address")
	webOrigin := f.String("web-origin", "", "exact origin allowed to read viewer APIs in a browser (disabled by default)")
	exposeIngress := f.Bool("expose-ingress-sources", false, "include raw TLS handshake source addresses in viewer snapshots (privacy-sensitive)")
	instance := f.String("instance", "collector", "visible collector instance ID")
	observer := f.String("observer", "", "optional local observer ID; otherwise collect only")
	workers := f.Int("concurrency", 4, "bounded local probe concurrency (1..32)")
	cert := f.String("tls-cert", "", "PEM server certificate")
	key := f.String("tls-key", "", "PEM server private key")
	insecure := f.Bool("allow-insecure", false, "explicitly allow plaintext non-loopback HTTP (unsafe without a secure tunnel)")
	retention := f.Duration("retention", 24*time.Hour, "raw history/receipt retention and maximum accepted sample age")
	stateBytes := f.Int64("state-bytes", 128<<20, "bounded state budget, at most 128 MiB")
	checkpointBytes := f.Int64("checkpoint-bytes", 16<<20, "minimum journal bytes before checkpoint (1 KiB..128 MiB)")
	maxJournalBytes := f.Int64("max-journal-bytes", 128<<20, "adaptive checkpoint ceiling (checkpoint-bytes..128 MiB)")
	maxReceipts := f.Int("max-receipts", 500000, "maximum deduplication receipts")
	maxHistory := f.Int("max-history", 20000, "maximum retained raw history rows; per-check state tails retained separately")
	rollupBytes := f.Int64("rollup-bytes", 32<<20, "hard cap on long-term rollups, in encoded bytes (64 KiB..64 MiB)")
	if e := parsed(f, args); e != nil {
		return e
	}
	if e := webFlags.validate(); e != nil {
		return e
	}
	stopMemory, e := memory.apply()
	if e != nil {
		return e
	}
	defer stopMemory()
	if e := api.ValidateWebOrigin(*webOrigin); e != nil {
		return e
	}
	finishLog, logErr := a.serviceLog(logs)
	if logErr != nil {
		return logErr
	}
	defer func() { finishLog(retErr) }()
	if !config.ID(*instance) {
		return errors.New("invalid instance ID")
	}
	if (*cert == "") != (*key == "") {
		return errors.New("both tls-cert and tls-key required")
	}
	tc, e := serverTLS(*cert, *key)
	if e != nil {
		return e
	}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil {
		return errors.New("listen must be host:port")
	}
	ip := net.ParseIP(host)
	if tc == nil && !*insecure && (ip == nil || !ip.IsLoopback()) {
		return errors.New("non-loopback collectors require TLS, or explicit --allow-insecure")
	}
	if *retention < time.Minute || *retention > 30*24*time.Hour {
		return errors.New("retention must be 1m..720h")
	}
	if *rollupBytes < rollup.MinLimit || *rollupBytes > rollup.MaxLimit {
		return errors.New("rollup-bytes must be 64 KiB..64 MiB")
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	panel, panelPath, e := panelFlags.load("collector", c.Project, "")
	if e != nil {
		return e
	}
	auth, e := api.LoadAuth(*authPath, c)
	if e != nil {
		return e
	}
	alerts, e := loadAlerts(*alertsPath, c)
	if e != nil {
		return e
	}
	storeOptions := store.Options{CheckpointBytes: *checkpointBytes, MaxJournalBytes: *maxJournalBytes, Observers: observerIDs(c), ObserverWeights: config.StorageWeights(c), MaxStateBytes: *stateBytes, MaxReceipts: *maxReceipts, Retention: *retention, MaxHistory: *maxHistory, RollupBytes: *rollupBytes}.WithTopology(c)
	st, e := store.Open(*dir, storeOptions)
	if e != nil {
		return e
	}
	defer st.Close()
	book, e := ledger.Open(filepath.Join(*dir, "incidents.json"))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	failures := make(chan error, 2)
	backend := api.New(c, *instance, st, auth)
	configureWeb(backend, *webFlags.enabled)
	backend.WebOrigin = *webOrigin
	backend.ExposeIngressSources = *exposeIngress
	backend.MaxSampleAge = *retention
	backend.Ledger = book
	// A failed handshake is where an observer's evidence stops arriving, and
	// net/http reports it only to the error log. Count it there so a snapshot
	// can say delivery is failing instead of leaving checks merely stale.
	// Attach the counter before any goroutine can read a snapshot: the monitor
	// fetches one as soon as attachMonitor starts it.
	ingress := api.NewIngressCounter(api.DefaultIngressWindow)
	backend.Ingress = ingress
	var samples *monitor.SampleBook
	if panel != nil {
		samples = monitor.NewSampleBook(panel.Template)
	}
	stopPanel, e := a.attachMonitor(ctx, panel, panelPath, func(ctx context.Context) (monitor.Input, error) {
		s := backend.MonitorSnapshot(time.Now())
		in := monitor.Input{IO: ioaccount.Read(), Snapshot: s}
		in.Samples = samples.Fill(ctx, s, s.Revision, -1, func(_ context.Context, id string) ([]model.Observation, error) {
			return backend.MonitorHistory(id, monitor.CardSamples)
		})
		return in, nil
	}, *dir, *path, *authPath, *alertsPath, *cert, *key)
	if e != nil {
		return e
	}
	defer stopPanel()

	var notifier *alert.Notifier
	if *alertsPath != "" {
		notifier = &alert.Notifier{Project: c.Project, Instance: *instance, Path: filepath.Join(*dir, "alert-queue.json"), Log: func(format string, args ...any) { fmt.Fprintf(a.Err, format+"\n", args...) }}
		if e = notifier.Load(); e != nil {
			return e
		}
		notifier.Configure(alerts)
		defer notifier.Close()
	}
	wg.Add(1)
	go func(settle time.Duration) {
		defer wg.Done()
		a.watch(ctx, backend, settle, notifier)
	}(ledger.Settle(c))
	wg.Go(func() { a.watchStorage(ctx, st, time.Minute) })
	// A handshake error is diagnostic. Do not make the connection goroutine
	// wait for a slow stderr, journald or rotating log file: forward through
	// a bounded queue and drop lines when the sink cannot keep up. The
	// counter has already recorded the failure before the line reaches here.
	handshakeLog := make(chan string, 128)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case line := <-handshakeLog:
				fmt.Fprintln(a.Err, line)
			}
		}
	}()
	srv := &http.Server{Addr: *listen, Handler: backend.Handler(), TLSConfig: tc, ConnContext: api.ConnectionContext, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384,
		ErrorLog: log.New(ingress.Writer(func(line string) {
			select {
			case handshakeLog <- line:
			default:
			}
		}), "", log.LstdFlags)}
	// Local checks, if any, under a topology; reloads replace them.
	local := func(c model.Config) (*runner.Scheduler, error) {
		if *observer == "" {
			return nil, nil
		}
		revision := config.Revision(c)
		batch := func(vs []model.Observation) error {
			return st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: revision, Observations: vs})
		}
		return prepareScheduler(c, *observer, *workers, batch)
	}
	scheduler, e := local(c)
	if e != nil {
		return e
	}
	ln, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	defer ln.Close()
	stopWeb, e := a.startWeb(webFlags, backend.Handler(), tc, failures)
	if e != nil {
		return e
	}
	defer stopWeb()
	wg.Add(1)
	go func() {
		defer wg.Done()
		var e error
		if tc != nil {
			e = srv.ServeTLS(api.LimitListener(ln, 128), "", "")
		} else {
			e = srv.Serve(api.LimitListener(ln, 128))
		}
		if e != nil && !errors.Is(e, http.ErrServerClosed) {
			select {
			case failures <- e:
			default:
			}
		}
	}()
	var running *probing
	if scheduler != nil {
		running = background(ctx, &wg, scheduler, failures)
	}
	hup, stopHup := a.hangups()
	defer stopHup()
	fmt.Fprintf(a.Err, "Topophany %s collector %s listening on %s; project=%s revision=%s\n", model.Version, *instance, ln.Addr(), c.Project, config.Revision(c)[:12])
	a.logMemory()
	if *insecure && *cert == "" {
		fmt.Fprintln(a.Err, "WARNING: plaintext HTTP explicitly enabled; use only inside a trusted encrypted tunnel")
	}
	// reload reads the topology, tokens and alerts again; on any error the
	// collector keeps what it runs.
	reload := func() error {
		next, e := reloaded(*path, c, *observer)
		if e != nil {
			return e
		}
		auth, e := api.LoadAuth(*authPath, next)
		if e != nil {
			return fmt.Errorf("%s: %w", *authPath, e)
		}
		alerts, e := loadAlerts(*alertsPath, next)
		if e != nil {
			return e
		}
		scheduler, e := local(next)
		if e != nil {
			return e
		}
		if e := st.ValidateTopology(next); e != nil {
			return e
		}
		running.stop()
		if e := backend.ReloadWith(next, auth, func() {
			if notifier != nil {
				notifier.Configure(alerts)
			}
		}); e != nil {
			return e
		}
		if scheduler != nil {
			running = background(ctx, &wg, scheduler, failures)
		}
		also := " and tokens"
		if notifier != nil {
			also = ", tokens and alerts"
		}
		fmt.Fprintf(a.Err, "reloaded topology revision %s → %s%s; %s\n", config.Revision(c)[:12], config.Revision(next)[:12], also, changes(c, next))
		c = next
		return nil
	}
	var result error
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case result = <-failures:
			break wait
		case <-hup:
			if e := reload(); e != nil {
				fmt.Fprintf(a.Err, "reload refused; still serving revision %s: %v\n", config.Revision(c)[:12], e)
			}
		}
	}
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if e = srv.Shutdown(shutdown); e != nil {
		_ = srv.Close()
	}
	return result
}

// serverTLS loads a collector's certificate and key, if it has them. The
// key is read as a credential, like auth.json: a regular file, not a
// link, that no other user can read on POSIX.
func serverTLS(cert, key string) (*tls.Config, error) {
	if cert == "" {
		return nil, nil
	}
	c, e := secure.ReadBounded(cert, 1<<20)
	if e != nil {
		return nil, fmt.Errorf("tls-cert %s: %w", cert, e)
	}
	k, e := secure.PrivateRead(key)
	if e != nil {
		return nil, fmt.Errorf("tls-key %s: %w", key, e)
	}
	pair, e := tls.X509KeyPair(c, k)
	if e != nil {
		return nil, fmt.Errorf("tls-cert and tls-key: %w", e)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

// loadAlerts reads a collector's alerts file, if it has one.
func loadAlerts(path string, c model.Config) (alert.Config, error) {
	if path == "" {
		return alert.Config{}, nil
	}
	a, e := alert.Load(path, c)
	if e != nil {
		return a, fmt.Errorf("%s: %w", path, e)
	}
	return a, nil
}

// watchStorage logs what a collector's store holds when it starts and then
// says, once each time it happens, when an observer's receipts are set to
// fill its share at the last hour's rate, and when they no longer are.
func (a *App) watchStorage(ctx context.Context, st *store.Store, every time.Duration) {
	fmt.Fprintln(a.Err, st.Usage())
	warned := map[string]bool{}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		a.logIO()
		for _, o := range st.Usage().Observers {
			name := o.Observer
			if name == "" {
				name = "all"
			}
			switch {
			case o.Fills && !warned[name]:
				fmt.Fprintf(a.Err, "storage: observer %s fills its share in about %s at the last hour's %d receipts, and its samples are refused from then; sample less often or shorten --retention\n", name, o.FullIn.Round(time.Minute), o.LastHour)
			case !o.Fills && warned[name]:
				fmt.Fprintf(a.Err, "storage: observer %s settles within its share again\n", name)
			}
			warned[name] = o.Fills
		}
	}
}

// watch keeps a collector's incident ledger until ctx ends, judging the
// evidence every two seconds, logging what opens and closes and, with a
// notifier, alerting on it. For settle after it starts, a record whose
// evidence lapsed stays open and no observer is called silent.
func (a *App) watch(ctx context.Context, backend *api.Server, settle time.Duration, notifier *alert.Notifier) {
	started := time.Now()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	repeats := &logging.Repeater{Out: a.Err, Every: time.Minute}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			settled := now.Sub(started) >= settle
			_, _, e := backend.ObserveWith(now.UTC(), settled, func(snap model.Snapshot, changes []ledger.Change) {
				for _, ch := range changes {
					fmt.Fprintln(a.Err, ch.Line())
				}
				if notifier != nil {
					notifier.Observe(snap, changes, settled)
				}
			})
			code := ""
			if e != nil {
				code = "persist_failed"
			}
			repeats.Update("incident_ledger", backend.Instance, code, now)
		}
	}
}

func observerIDs(c model.Config) []string {
	ids := make([]string, 0, len(c.Observers))
	for _, o := range c.Observers {
		ids = append(ids, o.ID)
	}
	return ids
}
