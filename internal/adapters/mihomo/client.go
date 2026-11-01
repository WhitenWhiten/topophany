package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters"
)

// Only these Mihomo API reads are permitted: collection uses the first three,
// and only Inspect reads /version. In particular GET delay endpoints are
// excluded: some of them clear a policy group's fixed selection.
var mihomoReads = map[string]bool{"/configs": true, "/proxies": true, "/providers/proxies": true, "/version": true}

type coreProxy struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Now         string   `json:"now"`
	All         []string `json:"all"`
	DialerProxy string   `json:"dialer-proxy"`
	TestURL     string   `json:"testUrl"`
	Alive       *bool    `json:"alive"`
	History     []struct {
		Time  time.Time `json:"time"`
		Delay int       `json:"delay"`
	} `json:"history"`
}

func readCore(ctx context.Context, c *http.Client, base, path, token string, v any) error {
	if !mihomoReads[path] {
		return adapters.Local("adapter", "api_path_not_allowed")
	}
	u, e := url.Parse(base)
	if e != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return adapters.Local("setup", "invalid_adapter_url")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.Do(req)
	if e != nil {
		return adapters.Fail("adapter_api", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return adapters.Local("adapter_api", "authentication_failed")
	}
	if resp.StatusCode != 200 {
		return &adapters.Failure{Stage: "adapter_api", Code: fmt.Sprintf("http_%d", resp.StatusCode)}
	}
	limit := int64(2 << 20)
	if path == "/proxies" || path == "/providers/proxies" {
		limit = 8 << 20
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return adapters.Fail("adapter_api", e)
	}
	if int64(len(b)) > limit {
		return adapters.Local("adapter_api", "response_limit_exceeded")
	}
	// Bound proxy/provider maps, member/history arrays and aggregate containers
	// before allocating typed response objects, including ignored fields.
	if code := coreResponseBudget(b, path); code != "" {
		return adapters.Local("adapter_api", code)
	}
	if e = json.Unmarshal(b, v); e != nil {
		return adapters.Local("adapter_api", "unsupported_response_shape")
	}
	return nil
}
