package probe

import (
	"crypto/tls"
	"strconv"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func certificatePolicy(state tls.ConnectionState, minDays int, o *model.Observation, now time.Time) {
	if minDays <= 0 || len(state.PeerCertificates) == 0 {
		return
	}
	expires := state.PeerCertificates[0].NotAfter
	if len(o.Facts) < model.MaxObservationFacts {
		o.Facts["tls_not_after"] = expires.UTC().Format(time.RFC3339)
	}
	if len(o.Facts) < model.MaxObservationFacts {
		o.Facts["tls_days_remaining"] = strconv.FormatInt(int64(expires.Sub(now)/(24*time.Hour)), 10)
	}
	if o.Outcome == "ok" && expires.Before(now.Add(time.Duration(minDays)*24*time.Hour)) {
		o.Outcome, o.Stage, o.Code = "policy", "tls_policy", "certificate_expiring"
		o.Detail = "verified TLS peer certificate expires within the configured warning window"
	}
}
