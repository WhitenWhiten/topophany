package api

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/model"
	"net/url"
	"testing"
	"time"
)

func TestRollupFixedWindowAndFutureRejection(t *testing.T) {
	srv, h, _ := fixture(t)
	end := srv.Now().Add(-time.Hour).Truncate(time.Second)
	response := request(h, "GET", "/v1/rollups?check=home-http&step=5m&span=1h&until="+url.QueryEscape(end.Format(time.RFC3339)), viewerToken, "", nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result struct {
		WindowEnd time.Time    `json:"window_end"`
		Total     model.Rollup `json:"total"`
	}
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.WindowEnd.Equal(end) || result.Total.Start.After(end) {
		t.Fatal(response.Body.String())
	}
	for _, bad := range []string{"invalid", srv.Now().Add(time.Hour).Format(time.RFC3339), srv.Now().Add(-801 * 24 * time.Hour).Format(time.RFC3339)} {
		r := request(h, "GET", "/v1/rollups?check=home-http&until="+url.QueryEscape(bad), viewerToken, "", nil)
		if r.Code != 400 {
			t.Fatal("invalid window accepted", r.Code)
		}
	}
}
