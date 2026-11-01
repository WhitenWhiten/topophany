package probe

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONAssertionsBoundaries(t *testing.T) {
	for _, tt := range []struct{ body, pointer, want, code string }{
		{`{"a/b":{"~x":[null,9007199254740993]}}`, "/a~1b/~0x/1", `9007199254740993`, ""},
		{`{"a":null}`, "/a", `null`, ""},
		{`{}`, "/a", `null`, "json_assertion_mismatch"},
		{`{"a":1,"a":2}`, "/a", `2`, "invalid_json"},
		{`[1]`, "/01", `1`, "json_assertion_mismatch"},
		{`9007199254740993`, "", `9007199254740992`, "json_assertion_mismatch"},
		{`1e999999999`, "", `1e999999999`, ""},
		{`1.0`, "", `1`, "json_assertion_mismatch"},
	} {
		if got := jsonAssertions([]byte(tt.body), map[string]json.RawMessage{tt.pointer: json.RawMessage(tt.want)}); got != tt.code {
			t.Fatalf("%s: %s", tt.pointer, got)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":"private-actual"}`)) }))
	defer server.Close()
	x := httpCheck(server.URL)
	x.HTTP.JSONEquals = map[string]json.RawMessage{"/token": json.RawMessage(`"private-expected"`)}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	b, _ := json.Marshal(o)
	if o.Code != "json_assertion_mismatch" || strings.Contains(string(b), "private-") {
		t.Fatal(string(b))
	}
	x.HTTP.JSONEquals["/~2"] = json.RawMessage(`true`)
	if config.ValidateProbePolicies(testkit.Config(), x) == nil {
		t.Fatal("invalid pointer accepted")
	}
}
