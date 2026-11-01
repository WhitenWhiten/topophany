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

func TestSnapshotRetriesReplacedAndRemovedCredentials(t *testing.T) {
	for _, action := range []string{"replace", "clear", "remove"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			store, err := NewStore(filepath.Join(t.TempDir(), "local"))
			if err != nil {
				t.Fatal(err)
			}
			check := syntheticCheck("http://127.0.0.1:9090")
			original, err := store.Save(ctx, "", check, Auth{Mode: "token", Token: "old-token"})
			if err != nil {
				t.Fatal(err)
			}
			var updated Profile
			var secrets map[string]string
			reads := 0
			got, err := store.readSnapshot(ctx, func(p Profile) error {
				reads++
				if reads == 1 {
					// Force the writer between loading the old profile and reading
					// its credential, including deletion of the old file.
					switch action {
					case "replace":
						updated, err = store.Save(ctx, original.Revision, check, Auth{Mode: "token", Token: "new-token"})
					case "clear":
						updated, err = store.Save(ctx, original.Revision, check, Auth{Mode: "none"})
					case "remove":
						updated, err = store.Remove(ctx, original.Revision, check.ID)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				secrets, err = store.Secrets(p)
				return err
			})
			if err != nil || reads != 2 || got.Revision != updated.Revision {
				t.Fatalf("profile=%+v reads=%d err=%v", got, reads, err)
			}
			if action == "replace" && secrets[check.ID] != "new-token" || action != "replace" && len(secrets) != 0 {
				t.Fatalf("wrong secrets after %s: %v", action, secrets)
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
