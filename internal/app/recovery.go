package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/alert"
	"github.com/WhitenWhiten/topophany/internal/api"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/outbox"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
)

type recoveryReport struct {
	ProjectObserved bool   `json:"project_identity_observed"`
	AuthValidated   bool   `json:"auth_config_validated"`
	Schema          int    `json:"schema"`
	Version         string `json:"version"`
	Role            string `json:"role"`
	Recoverable     bool   `json:"recoverable"`
	Files           int    `json:"files"`
	Bytes           int64  `json:"bytes"`
	Digest          string `json:"data_digest"`
	Revision        string `json:"private_config_revision"`
	Pending         int    `json:"pending_records,omitempty"`
	Quarantined     int    `json:"new_quarantine_files,omitempty"`
	StateBytes      int64  `json:"recovered_state_bytes,omitempty"`
	Note            string `json:"note"`
}
type recoveryReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r recoveryReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

// copyRecoveryData copies only regular files into our private scratch directory.
// The caller holds the original writer lock; no original content is rewritten.
func copyRecoveryData(ctx context.Context, source, destination string, maxBytes int64) (int, int64, string, error) {
	files := 0
	entries := 0
	buffer := make([]byte, 32<<10)
	var bytes int64
	digest := sha256.New()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entries++
		if entries > 100000 {
			return errors.New("backup exceeds directory-entry budget")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("backup contains a symlink; refusing to follow it")
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if rel == "writer.lock" || rel == "local-runtime.json" {
			return nil
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() {
			return errors.New("backup contains a non-regular file")
		}
		files++
		if files > 100000 || stat.Size() > maxBytes-bytes {
			return errors.New("backup exceeds file or byte budget")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		opened, err := input.Stat()
		if err != nil || !os.SameFile(stat, opened) {
			return errors.New("backup changed while opening a file")
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		fileHash := sha256.New()
		n, copyErr := io.CopyBuffer(io.MultiWriter(output, fileHash), recoveryReader{ctx, io.LimitReader(input, stat.Size()+1)}, buffer)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(stat, current) || current.Size() != stat.Size() || !current.ModTime().Equal(stat.ModTime()) || n != stat.Size() {
			return errors.New("backup changed while copying; use a stopped-writer backup")
		}
		bytes += n
		fmt.Fprintf(digest, "%s\x00%d\x00%x\n", filepath.ToSlash(rel), n, fileHash.Sum(nil))
		return nil
	})
	return files, bytes, hex.EncodeToString(digest.Sum(nil)), err
}
func quarantineFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(filepath.Join(dir, "quarantine"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			count++
		}
		return nil
	})
	return count
}
func (a *App) recovery(ctx context.Context, args []string) error {
	f := a.flags("recovery")
	dir := f.String("data", "", "stopped backup data directory; original contents are never rewritten")
	path := f.String("config", "topology.json", "matching reviewed topology")
	auth := f.String("auth", "", "optional collector auth file to validate")
	role := f.String("role", "collector", "collector or worker")
	stateBytes := f.Int64("state-bytes", 128<<20, "proposed collector state budget")
	maxHistory := f.Int("max-history", 20000, "proposed collector raw-history limit")
	maxReceipts := f.Int("max-receipts", 500000, "proposed collector receipt limit")
	retention := f.Duration("retention", 24*time.Hour, "proposed collector ingestion window")
	maxRecords := f.Int("max-records", 20000, "proposed worker outbox record limit")
	maxBytes := f.Int64("max-bytes", 1<<30, "maximum bytes to copy to private scratch (1 MiB..8 GiB)")
	if err := parsed(f, args); err != nil {
		return err
	}
	if *dir == "" || *role != "collector" && *role != "worker" || *maxBytes < 1<<20 || *maxBytes > 8<<30 {
		return errors.New("choose --data, --role collector|worker and a 1 MiB..8 GiB copy budget")
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *auth != "" {
		if *role != "collector" {
			return errors.New("--auth applies to a collector backup")
		}
		if _, err := api.LoadAuth(*auth, c); err != nil {
			return err
		}
	}
	stat, err := os.Lstat(*dir)
	if err != nil || !stat.IsDir() {
		return errors.New("backup data must be a real directory")
	}
	marker, err := os.Lstat(filepath.Join(*dir, "writer.lock"))
	if err != nil || !marker.Mode().IsRegular() {
		return errors.New("backup must include its regular writer.lock marker")
	}
	unlock, err := secure.Lock(filepath.Join(*dir, "writer.lock"))
	if err != nil {
		return errors.New("backup has an active writer or its lock is unavailable; use a stopped copy")
	}
	defer unlock()
	if *role == "collector" {
		_, journal := os.Stat(filepath.Join(*dir, "journal.jsonl"))
		_, checkpoint := os.Stat(filepath.Join(*dir, "checkpoint.json"))
		if journal != nil && checkpoint != nil {
			return errors.New("collector backup contains neither journal nor checkpoint")
		}
	}
	scratch, err := os.MkdirTemp("", "topophany-recovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	files, bytes, digest, err := copyRecoveryData(ctx, *dir, scratch, *maxBytes)
	if err != nil {
		return err
	}
	report := recoveryReport{AuthValidated: *auth != "", Schema: 1, Version: model.Version, Role: *role, Files: files, Bytes: bytes, Digest: digest, Revision: config.Revision(c), Note: "Validated by this binary in a disposable private copy. No probe, delivery or notification was started. Original data was not rewritten. Review deployment budgets and matching credentials before restoring."}
	if *role == "collector" {
		recovered, err := store.Open(scratch, store.Options{Revision: config.Revision(c), Observers: observerIDs(c), ObserverWeights: config.StorageWeights(c), MaxStateBytes: *stateBytes, MaxHistory: *maxHistory, MaxReceipts: *maxReceipts, Retention: *retention}.WithTopology(c))
		if err != nil {
			return &commandProblem{Code: "recovery_failed", Message: "This binary could not recover the backup with the proposed storage budgets.", Cause: err}
		}
		if err = recovered.ValidateTopology(c); err != nil {
			_ = recovered.Close()
			return err
		}
		report.ProjectObserved, err = recovered.ValidateProject(c.Project)
		if err != nil {
			_ = recovered.Close()
			return err
		}
		if _, err = ledger.Open(filepath.Join(scratch, "incidents.json")); err != nil {
			_ = recovered.Close()
			return err
		}
		notifier := &alert.Notifier{Path: filepath.Join(scratch, "alert-queue.json")}
		if err = notifier.Load(); err != nil {
			_ = recovered.Close()
			return err
		}
		report.StateBytes = recovered.Usage().StateBytes
		if err = recovered.Close(); err != nil {
			return err
		}
	} else {
		before := quarantineFiles(scratch)
		queue, err := outbox.OpenWithOptions(scratch, c.Project, outbox.Options{MaxBytes: *maxBytes, MaxRecords: *maxRecords})
		if err != nil {
			return err
		}
		report.Pending, _ = queue.Stats()
		if err = queue.Close(); err != nil {
			return err
		}
		report.Quarantined = max(0, quarantineFiles(scratch)-before)
	}
	report.Recoverable = report.Quarantined == 0
	if err = printJSON(a.Out, report); err != nil {
		return err
	}
	if !report.Recoverable {
		return &ExitError{Code: 2}
	}
	return nil
}
