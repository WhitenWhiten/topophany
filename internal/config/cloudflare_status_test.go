package config

import (
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestCloudflareStatusChangesContract(t *testing.T) {
	c := model.Config{Project: "test"}
	x := model.Check{Kind: "cloudflare", Adapter: model.AdapterOptions{Cloudflare: &model.CloudflareOptions{Read: "tunnel"}}}
	before := Contract(c, x)
	x.Adapter.Cloudflare.ExpectedStatus = "healthy"
	if Contract(c, x) == before {
		t.Fatal("status assertion did not change contract")
	}
	copy := x.Adapter.CloudflareOptions()
	copy.ExpectedStatus = "down"
	if x.Adapter.Cloudflare.ExpectedStatus != "healthy" {
		t.Fatal("options alias")
	}
}
