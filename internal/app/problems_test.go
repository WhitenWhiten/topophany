package app

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/view"
	"testing"
)

func TestProblemCommandsReadSyntheticEvidence(t *testing.T) {
	out, _, err := invoke("", "preview", "--scenario", "1", "problems", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Problems []view.Problem }
	if json.Unmarshal([]byte(out), &result) != nil || len(result.Problems) == 0 {
		t.Fatal(out)
	}
	for _, p := range result.Problems {
		out, _, err = invoke("", "preview", "--scenario", "1", "problems", p.ID)
		if err != nil || out == "" {
			t.Fatal(out, err)
		}
	}
	if _, _, err = invoke("", "preview", "problems", "missing"); err == nil {
		t.Fatal("missing group accepted")
	}
}
