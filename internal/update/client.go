package update

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
)

func helperHTTP(p management.Policy) (*http.Client, error) {
	policy, e := LoadPolicy(p.Updates.PolicyFile, p.System)
	if e != nil {
		return nil, e
	}
	if e = management.ValidateDirectory(filepath.Dir(policy.Socket), true); e != nil {
		return nil, e
	}
	st, e := os.Lstat(policy.Socket)
	if e != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0007 != 0 {
		return nil, errors.New("untrusted_helper_socket")
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		connection, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", policy.Socket)
		if e != nil {
			return nil, e
		}
		if e = verifyHelperPeer(connection); e != nil {
			connection.Close()
			return nil, e
		}
		return connection, nil
	}}
	return &http.Client{Transport: transport, Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// CheckHelper is a local, read-only readiness check performed before enabling
// update acceptance. The authenticated root peer must name this installation.
func CheckHelper(ctx context.Context, p management.Policy) error {
	client, err := helperHTTP(p)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(ctx, "GET", "http://local-helper/capabilities", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("update_helper_unavailable")
	}
	defer response.Body.Close()
	raw, err := readBounded(response.Body, 16<<10)
	var capability struct {
		Schema       int    `json:"schema"`
		Installation string `json:"installation_id"`
		Protocol     int    `json:"protocol"`
	}
	if err != nil || response.StatusCode != 200 || management.Decode(raw, &capability, 16<<10) != nil || capability.Schema != 1 || capability.Protocol != 1 || capability.Installation != p.Installation {
		return errors.New("update_helper_registration_or_protocol_mismatch")
	}
	return nil
}

func HelperCall(ctx context.Context, p management.Policy, env management.Envelope, payload []byte) (management.Receipt, error) {
	client, e := helperHTTP(p)
	if e != nil {
		return management.Receipt{}, e
	}
	defer client.CloseIdleConnections()
	// Read an already completed result even if activation authority has since
	// expired. This read cannot begin or retry an installation.
	statusReq, err := http.NewRequestWithContext(ctx, "GET", "http://local-helper/status", nil)
	if err != nil {
		return management.Receipt{}, err
	}
	if response, err := client.Do(statusReq); err == nil {
		raw, readErr := readBounded(response.Body, 16<<10)
		response.Body.Close()
		var receipt management.Receipt
		if response.StatusCode == 200 && readErr == nil && management.Decode(raw, &receipt, 16<<10) == nil && receipt.CommandDigest == management.Digest(management.Marshal(env)) && receipt.State != "accepted" {
			return receipt, nil
		}
	}
	body := management.Marshal(HelperRequest{env, payload})
	req, e := http.NewRequestWithContext(ctx, "POST", "http://local-helper/update", bytes.NewReader(body))
	if e != nil {
		return management.Receipt{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	response, e := client.Do(req)
	if e != nil {
		return management.Receipt{}, errors.New("update_helper_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return management.Receipt{}, errors.New("update_helper_refused")
	}
	raw, e := readBounded(response.Body, 16<<10)
	if e != nil {
		return management.Receipt{}, e
	}
	var receipt management.Receipt
	e = management.Decode(raw, &receipt, 16<<10)
	return receipt, e
}
