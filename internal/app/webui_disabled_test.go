//go:build !webui

package app

import (
	"strings"
	"testing"
)

func TestWebRequiresBuildTag(t *testing.T) {
	_, _, err := invoke("", "serve", "--web")
	if err == nil || !strings.Contains(err.Error(), "-tags webui") {
		t.Fatal(err)
	}
}
