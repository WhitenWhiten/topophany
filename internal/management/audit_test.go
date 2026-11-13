//go:build linux || darwin

package management

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointDistinguishesCommitFromAuditFailure(t *testing.T) {
	for _, phase := range []string{"intent", "before-rename", "after-rename", "committed"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			old, next := []byte(`{"generation":1}`), []byte(`{"generation":2}`)
			if err := WriteLocal(path, old); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected storage fault")
			audit := func(_ string, e Audit) error {
				if e.Phase == phase {
					return fault
				}
				return nil
			}
			write := func(path string, raw []byte) error {
				if phase == "before-rename" {
					return fault
				}
				if err := WriteLocal(path, raw); err != nil {
					return err
				}
				if phase == "after-rename" {
					return fault
				}
				return nil
			}
			err := checkpoint(path, next, Audit{}, audit, write)
			if !errors.Is(err, fault) {
				t.Fatal(err)
			}
			actual, errRead := os.ReadFile(path)
			if errRead != nil {
				t.Fatal(errRead)
			}
			visible := string(actual) == string(next)
			if CheckpointCommitted(err) != visible {
				t.Fatal("commit outcome disagrees with disk", err)
			}
		})
	}
}
