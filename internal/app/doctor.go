package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/session"
)

// DoctorReport deliberately excludes URLs, tokens, topology text and raw errors.
// It can be shared without exporting the monitored infrastructure.
type DoctorReport struct {
	Findings  []DiagnosticFinding    `json:"findings,omitempty"`
	Schema    int                    `json:"schema"`
	Version   string                 `json:"version"`
	State     string                 `json:"state"`
	Code      string                 `json:"code,omitempty"`
	Checks    int                    `json:"checks"`
	Current   int                    `json:"current"`
	Pending   int                    `json:"pending"`
	Transport []model.TransportEvent `json:"transport,omitempty"`
	Next      []string               `json:"next_actions"`
}

func diagnosticActions(code string) []string {
	switch code {
	case "tls_failure", "tls_timeout":
		return []string{"Check the collector certificate hostname, validity and trusted CA file; keep certificate verification enabled."}
	case "dns_failure", "dns_timeout":
		return []string{"Check the collector hostname and this machine's DNS resolution."}
	case "authorization_rejected", "authorization_failed", "unauthorized", "forbidden":
		return []string{"Verify a viewer token for this project with topo login; do not use a worker token."}
	case "contract_mismatch", "configuration_mismatch", "project_mismatch":
		return []string{"Compare topo validate contracts and project IDs on collector and workers; reload the reviewed configuration."}
	case "rate_limited":
		return []string{"The collector is limiting requests. Wait for its retry interval before reconnecting."}
	case "configuration_unavailable":
		return []string{"Run topo setup or topo login to configure a connection; check credential file permissions and the selected profile."}
	case "session_unavailable":
		return []string{"Run topo sessions to select a live viewer session; session files expire after the viewer stops."}
	case "waiting_for_samples":
		return []string{"Start the configured worker or collector observer; check its observer ID, credentials and logs.", "Compare topo validate contracts on the collector and worker; check system clock and delivery backlog."}
	default:
		return []string{"Check that the collector is running and reachable from this machine.", "Inspect worker delivery and collector logs; use topo sessions and topo diagnose for a running viewer."}
	}
}

func (a *App) doctor(ctx context.Context, args []string) error {
	f := a.flags("doctor")
	connect := a.connection(f)
	sessionID := f.String("session", "", "inspect a local running viewer instead of making a new connection")
	sessionRoot := f.String("sessions-dir", "", "private local session directory")
	wait := f.Duration("wait", 0, "wait up to 5m for the first current sample (0 takes one snapshot)")
	jsonOut := f.Bool("json", false, "write a shareable diagnostic report without URLs or credentials")
	if err := parsed(f, args); err != nil {
		return err
	}
	if *wait < 0 || *wait > 5*time.Minute {
		return errors.New("--wait must be between 0 and 5m")
	}
	report := DoctorReport{Schema: 1, Version: model.Version, State: "unavailable", Next: []string{}}
	finish := func(code int) error {
		if report.Code != "" {
			report.Next = diagnosticActions(report.Code)
		}
		var err error
		if *jsonOut {
			err = printJSON(a.Out, report)
		} else {
			_, err = fmt.Fprintf(a.Out, "%s: %d/%d checks with current samples (%d pending)\n", report.State, report.Current, report.Checks, report.Pending)
			for _, finding := range report.Findings {
				fmt.Fprintf(a.Out, "%s: %d — %s\n", finding.Code, finding.Count, finding.Action)
			}
			for _, next := range report.Next {
				fmt.Fprintln(a.Out, "Next:", next)
			}
		}
		if err != nil {
			return err
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	}
	if *sessionID != "" {
		if *wait != 0 {
			return errors.New("--wait applies to collector enrollment, not session inspection")
		}
		root := *sessionRoot
		if root == "" {
			var err error
			root, err = session.DefaultRoot()
			if err != nil {
				return err
			}
		}
		state, err := session.Read(root, *sessionID, a.now())
		if err != nil {
			report.Code = "session_unavailable"
			return finish(1)
		}
		report.Transport = state.Requests
		report.State = "viewer_current"
		if !state.Live || state.Cached {
			report.State = "viewer_not_current"
			report.Code = "waiting_for_samples"
			return finish(3)
		}
		report.Next = []string{"Use topo diagnose --session ID locally for detailed viewer filters, selection and notices; those details are omitted from this shareable report."}
		return finish(0)
	}
	c, err := connect()
	if err != nil {
		report.Code = "configuration_unavailable"
		return finish(1)
	}
	defer c.Close()
	budget := *wait
	if budget == 0 {
		budget = 15 * time.Second
	}
	readCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	for {
		snap, _, err := c.Snapshot(readCtx, "")
		report.Transport = c.Diagnostics()
		if err != nil {
			report.State = "unavailable"
			report.Code = client.FailureCode(err)
			// Snapshot can wrap exhausted aliases. Its bounded transport record keeps
			// the actual failure without exposing an endpoint or peer text.
			for i := len(report.Transport) - 1; i >= 0; i-- {
				if report.Transport[i].Code != "" {
					report.Code = report.Transport[i].Code
					break
				}
			}
		} else {
			report.Findings = operationalFindings(snap, a.now())
			report.Checks, report.Current = 0, 0
			fresh := !snap.GeneratedAt.IsZero() && time.Since(snap.GeneratedAt) <= model.SnapshotLifetime
			for _, check := range snap.Checks {
				if check.Freshness == "paused" || check.Freshness == "maintenance" {
					continue
				}
				report.Checks++
				if fresh && check.Freshness == "fresh" && check.Last != nil {
					report.Current++
				}
			}
			report.Pending = report.Checks - report.Current
			report.State = "waiting"
			report.Code = "waiting_for_samples"
			if report.Current > 0 {
				report.State = "samples_received"
				report.Code = ""
				report.Next = []string{"Open topo to inspect health outcomes; receiving a sample alone does not mean the target is healthy."}
				if len(report.Findings) > 0 {
					report.State = "monitoring_attention"
					return finish(2)
				}
				return finish(0)
			}
		}
		if report.Code == "authorization_rejected" || report.Code == "configuration_mismatch" {
			return finish(1)
		}
		if *wait == 0 {
			if err != nil {
				return finish(4)
			}
			return finish(3)
		}
		fmt.Fprintf(a.Err, "Waiting for first sample: %d/%d current (%s)\n", report.Current, report.Checks, report.State)
		delay := time.Second
		if len(report.Transport) > 0 {
			delay = max(delay, time.Duration(report.Transport[len(report.Transport)-1].RetryAfterSeconds)*time.Second)
		}
		timer := time.NewTimer(delay)
		select {
		case <-readCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if report.State == "unavailable" {
				return finish(4)
			}
			return finish(3)
		case <-timer.C:
		}
	}
}
