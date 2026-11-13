//go:build linux || darwin

package management

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateCanaryUsesControllerObservationAndStopsAfterFailure(t *testing.T) {
	for _, kind := range []string{"fresh-receipt-with-old-clock", "stale-peer", "failed-peer", "observed-healthy"} {
		t.Run(kind, func(t *testing.T) {
			_, trust, key, base := fixture(t)
			now := time.Now().UTC()
			dir := t.TempDir()
			c := &Controller{Policy: ControllerPolicy{ID: trust.ID, Epoch: 1, Data: dir, InventoryComplete: true, ObservationSeconds: 30}, key: SigningKey{ID: "one"}, private: key, state: controllerState{Schema: 1, ID: trust.ID, Epoch: 1, Nodes: map[string]NodeRecord{}, Blobs: map[string]int64{}}}
			c.VerifyUpdate = func(context.Context, []byte, NodeStatus, []NodeRecord) error { return nil }
			payload := []byte(`{"version":"v1.7.1"}`)
			for _, id := range []string{"canary", "next"} {
				cmd := base
				cmd.Installation, cmd.Deployment, cmd.ID, cmd.Kind = id, "rollout", "rollout.prepare", "update.prepare"
				cmd.Digest, cmd.Length = Digest(payload), int64(len(payload))
				state := "prepared"
				if id == "canary" {
					cmd.Kind, cmd.ID, state = "update.activate", "rollout.activate", "healthy"
				}
				env := signed(t, key, cmd)
				receipt := &Receipt{CommandID: cmd.ID, Deployment: cmd.Deployment, CommandDigest: Digest(Marshal(env)), Sequence: cmd.Sequence, Generation: cmd.Generation, PayloadDigest: cmd.Digest, State: state, At: now.Add(-time.Hour)}
				node := NodeRecord{Registration: Registration{ID: id, Project: "home", Role: "worker", Observer: "a"}, Sequence: 1, TargetVersion: "v1.7.1", Desired: &env, Status: &NodeStatus{Installation: id, UpdateAccept: true, UpdateSession: cmd.Session, Receipt: receipt}, Received: now}
				if id == "canary" {
					node.HealthySince = now
					switch kind {
					case "stale-peer":
						node.HealthySince = now.Add(-time.Hour)
						node.Received = now.Add(-2 * time.Minute)
					case "failed-peer":
						node.Status.Receipt.State = "rolled_back"
					case "observed-healthy":
						node.HealthySince = now.Add(-time.Minute)
					}
				}
				c.state.Nodes[id] = node
			}
			if err := WriteLocal(filepath.Join(dir, "blob-"+Digest(payload)+".json"), payload); err != nil {
				t.Fatal(err)
			}
			c.state.Blobs[Digest(payload)] = int64(len(payload))
			request := httptest.NewRequest("POST", "/management/v1/update/activate", bytes.NewReader(Marshal(Advance{"rollout", []string{"next"}})))
			response := httptest.NewRecorder()
			c.advanceUpdate(response, request, ManagementPrincipal{Role: "update-publish", Project: "home", Installations: []string{"canary", "next"}}, now)
			want := 409
			if kind == "observed-healthy" {
				want = 200
			}
			if response.Code != want {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}
