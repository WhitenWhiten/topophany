package app

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/session"
)

func TestDiagnoseReadsExactViewerStateAndRetainedHistory(t *testing.T) {
	root := t.TempDir()
	w, err := session.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	now := time.Now()
	state := session.State{Project: "lab", Server: "https://s2.test", Observer: "wsl", Filters: session.Filters{Search: "api"}, Cached: true, Error: "TLS timeout", Notice: session.Notice{Text: "retrying"}, History: session.History{Check: "api", Error: "answer cut off"}, Events: []session.Event{{At: now, Kind: "connection_error", Code: "tls_timeout"}}}
	if err = w.Publish(state, now); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	a := &App{Out: &out, Err: &out, Now: func() time.Time { return now }}
	if err = a.Run(context.Background(), []string{"diagnose", "--sessions-dir", root, "--session", w.ID(), "--json"}); err != nil {
		t.Fatal(err)
	}
	var got session.State
	if err = json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want, err := session.Read(root, w.ID(), now)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
	state.Error = ""
	state.Cached = false
	if err = w.Publish(state, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = a.Run(context.Background(), []string{"sessions", "--sessions-dir", root, "--json"}); err != nil {
		t.Fatal(err)
	}
	var all []session.State
	if json.Unmarshal(out.Bytes(), &all) != nil || len(all) != 1 || all[0].Error != "" || len(all[0].Events) != 1 {
		t.Fatal(out.String())
	}
}
