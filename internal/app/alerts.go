package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/alert"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func (a *App) alerts(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("use alerts status, test, quiet or resume")
	}
	command := args[0]
	f := a.flags("alerts " + command)
	data := f.String("data", "", "collector data directory (status)")
	path := f.String("alerts", "", "private alerts configuration (test)")
	topology := f.String("config", "topology.json", "reviewed topology for validating alert routes")
	id := f.String("webhook", "", "one webhook ID to test")
	duration := f.Duration("for", 0, "manual notification quiet duration, up to 24h")
	dry := f.Bool("dry-run", false, "preview a quiet/resume change without writing")
	asJSON := f.Bool("json", false, "structured status without webhook URLs or credentials")
	if err := parsed(f, args[1:]); err != nil {
		return err
	}
	switch command {
	case "status":
		if *data == "" {
			return errors.New("status requires --data COLLECTOR_DIRECTORY")
		}
		report, err := alert.ReadStatus(filepath.Join(*data, "alert-queue.json"))
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(a.Out, report)
		}
		fmt.Fprintf(a.Out, "Alert delivery snapshot as of %s (not a live lease); queue durable=%t\n", report.AsOf.Format(time.RFC3339), report.QueueDurable)
		for _, h := range report.Webhooks {
			fmt.Fprintf(a.Out, "%s: %d pending, %d sent, %d dropped; oldest attempts=%d; HTTP=%d code=%s; last success=%s\n", secure.Text(h.ID), h.Pending, h.Sent, h.Dropped, h.Tries, h.Delivery.HTTPStatus, secure.Text(h.Delivery.Code), h.Delivery.LastSuccess.Format(time.RFC3339))
			fmt.Fprintf(a.Out, "  mode=%s primary=%s resume_at=%s\n", secure.Text(h.Mode), secure.Text(h.PrimaryInstance), h.ResumeAt.Format(time.RFC3339))
		}
		return nil
	case "quiet", "resume":
		if *path == "" || *id == "" {
			return errors.New("quiet/resume requires --alerts FILE and --webhook ID")
		}
		if command == "quiet" && (*duration <= 0 || *duration > 24*time.Hour) || command == "resume" && *duration != 0 {
			return errors.New("quiet requires --for between 0 and 24h; resume takes no duration")
		}
		c, err := config.Load(*topology)
		if err != nil {
			return err
		}
		_, base, err := config.ReadFileVersion(*path)
		if err != nil {
			return err
		}
		routes, err := alert.Load(*path, c)
		if err != nil {
			return err
		}
		found := false
		until := time.Time{}
		if command == "quiet" {
			until = a.now().UTC().Add(*duration)
		}
		for i := range routes.Webhooks {
			if routes.Webhooks[i].ID == *id {
				routes.Webhooks[i].QuietUntil = until
				found = true
				break
			}
		}
		if !found {
			return errors.New("webhook ID is not configured")
		}
		if err = alert.Validate(routes, c); err != nil {
			return err
		}
		if !*dry {
			data, err := json.MarshalIndent(routes, "", "  ")
			if err != nil {
				return err
			}
			if _, err = config.CommitFile(*path, base, append(data, '\n'), 0600); err != nil {
				return err
			}
		}
		return printJSON(a.Out, struct {
			Webhook string    `json:"webhook"`
			Until   time.Time `json:"quiet_until,omitzero"`
			Applied bool      `json:"applied"`
			Reload  bool      `json:"requires_collector_reload"`
		}{*id, until, !*dry, !*dry})
	case "test":
		if *path == "" || *id == "" {
			return errors.New("test requires --alerts FILE and one explicit --webhook ID")
		}
		c, err := config.Load(*topology)
		if err != nil {
			return err
		}
		routes, err := alert.Load(*path, c)
		if err != nil {
			return err
		}
		for _, w := range routes.Webhooks {
			if w.ID == *id {
				attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				result, err := alert.TestDelivery(attempt, w, c.Project)
				if writeErr := printJSON(a.Out, result); writeErr != nil {
					return writeErr
				}
				if err != nil {
					return &ExitError{Code: 2}
				}
				return nil
			}
		}
		return errors.New("webhook ID is not configured")
	default:
		return errors.New("use alerts status, test, quiet or resume")
	}
}
