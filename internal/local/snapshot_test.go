package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotLocksOutCredentialChanges(t *testing.T) {
	for _, action := range []string{"replace", "clear", "remove"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			root := filepath.Join(t.TempDir(), "local")
			store, err := NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewStore(root)
			if err != nil {
				t.Fatal(err)
			}
			check := syntheticCheck("http://127.0.0.1:9090")
			original, err := store.Save(ctx, "", check, Auth{Mode: "token", Token: "old-token"})
			if err != nil {
				t.Fatal(err)
			}
			write := func(ctx context.Context) (Profile, error) {
				switch action {
				case "replace":
					return writer.Save(ctx, original.Revision, check, Auth{Mode: "token", Token: "new-token"})
				case "clear":
					return writer.Save(ctx, original.Revision, check, Auth{Mode: "none"})
				default:
					return writer.Remove(ctx, original.Revision, check.ID)
				}
			}
			var secrets map[string]string
			got, err := store.readSnapshot(ctx, func(p Profile) error {
				// Another Store must not replace the profile or delete the old
				// credential while this snapshot is reading it.
				writeCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				if _, err := write(writeCtx); err == nil || AsError(err).Code != "storage_error" {
					t.Fatalf("%s did not wait for the snapshot: %v", action, err)
				}
				secrets, err = store.Secrets(p)
				return err
			})
			if err != nil || got.Revision != original.Revision || secrets[check.ID] != "old-token" {
				t.Fatalf("original profile=%+v secrets=%v err=%v", got, secrets, err)
			}
			updated, err := write(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got, secrets, err = store.Snapshot(ctx, "")
			if err != nil || got.Revision != updated.Revision {
				t.Fatalf("updated profile=%+v err=%v", got, err)
			}
			if action == "replace" && secrets[check.ID] != "new-token" || action != "replace" && len(secrets) != 0 {
				t.Fatalf("wrong secrets after %s: %v", action, secrets)
			}
			if _, err := os.Stat(filepath.Join(root, "credential-"+original.Checks[0].Credential+".json")); !os.IsNotExist(err) {
				t.Fatalf("old credential remains after %s: %v", action, err)
			}
			if _, _, err := store.Snapshot(ctx, original.Revision); AsError(err).Code != "profile_conflict" {
				t.Fatalf("stale revision after %s: %v", action, err)
			}
			token, err := store.SavedSecret(ctx, check)
			if action == "remove" {
				if AsError(err).Code != "saved_auth_mismatch" {
					t.Fatalf("removed check: %v", err)
				}
			} else if err != nil || token != secrets[check.ID] {
				t.Fatalf("saved secret after %s: %q %v", action, token, err)
			}
		})
	}
}

func TestSnapshotReportsRealCredentialErrorsAndDoesNotCreateEmptyStore(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "local")
	store, _ := NewStore(root)
	p, secrets, err := store.Snapshot(ctx, "")
	if err != nil || p.Revision != "" || len(p.Checks) != 0 || len(secrets) != 0 {
		t.Fatal(p, secrets, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("reading an empty store created its root", err)
	}
	check := syntheticCheck("http://127.0.0.1:9090")
	p, err = store.Save(ctx, "", check, Auth{Mode: "token", Token: "old-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "credential-"+p.Checks[0].Credential+".json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Snapshot(ctx, ""); AsError(err).Code != "credential_storage_error" {
		t.Fatal("missing credential not reported", err)
	}
	if _, err := store.SavedSecret(ctx, check); AsError(err).Code != "credential_storage_error" {
		t.Fatal("missing saved credential not reported", err)
	}
	if _, _, err := store.Snapshot(ctx, "stale"); AsError(err).Code != "profile_conflict" {
		t.Fatal("stale revision should be checked before credentials", err)
	}
}

func TestLoadReportsUnusableLockWithoutWaitingForCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.Load(ctx); err == nil || AsError(err).Code != "storage_error" || ctx.Err() != nil {
		t.Fatalf("unusable lock: err=%v context=%v", err, ctx.Err())
	}
}

func TestSnapshotAndSavedSecretDuringConcurrentWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, _ := NewStore(filepath.Join(t.TempDir(), "local"))
	check := syntheticCheck("http://127.0.0.1:9090")
	check.Name = "token-0"
	initial, err := store.Save(ctx, "", check, Auth{Mode: "token", Token: check.Name})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		p := initial
		var err error
		for i := 1; i <= 90 && ctx.Err() == nil; i++ {
			if i%3 == 2 {
				p, err = store.Remove(ctx, p.Revision, check.ID)
			} else {
				next := check
				next.Name = fmt.Sprintf("token-%d", i)
				p, err = store.Save(ctx, p.Revision, next, Auth{Mode: "token", Token: next.Name})
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		done <- err
	}()
	defer func() {
		cancel()
		<-finished
	}()
	for {
		p, secrets, err := store.Snapshot(ctx, "")
		if err != nil {
			t.Fatal("snapshot during write:", err)
		}
		for _, entry := range p.Checks {
			if secrets[entry.Check.ID] != entry.Check.Name {
				t.Fatalf("profile and token from different revisions: %+v %v", p, secrets)
			}
		}
		token, err := store.SavedSecret(ctx, check)
		if err != nil && AsError(err).Code != "saved_auth_mismatch" {
			t.Fatal("saved secret during write:", err)
		}
		if err == nil && !strings.HasPrefix(token, "token-") {
			t.Fatalf("unexpected saved token %q", token)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
	}
}
