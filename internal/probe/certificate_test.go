package probe

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"strings"
	"testing"
)

func TestCertificateExpiryIsPolicyNotConnectivityFailure(t *testing.T) {
	server, ca := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	x := httpCheck(server.URL)
	x.CAFile = ca
	x.TLSMinDays = 14
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "policy" || o.Code != "certificate_expiring" || !o.Connectivity || o.Facts["tls_not_after"] == "" {
		t.Fatal(o)
	}
	x.Kind = "tls"
	x.Target = strings.TrimPrefix(server.URL, "https://")
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Code != "certificate_expiring" || o.Outcome != "policy" {
		t.Fatal(o)
	}
	x.CAFile = ""
	o = (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "fail" || o.Stage != "tls" {
		t.Fatal("expiry policy bypassed trust", o)
	}
}
