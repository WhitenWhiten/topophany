package management

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"
)

type Credential struct {
	Token string `json:"token"`
}

func HTTP(p Policy) (*http.Client, string, error) {
	b, e := ReadLocal(p.Controller.CredentialFile, 4096, false, true)
	if e != nil {
		return nil, "", e
	}
	var cred Credential
	if e = Decode(b, &cred, 4096); e != nil || !HashOK(cred.Token) {
		return nil, "", errors.New("invalid_management_credential")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.Controller.CAFile != "" {
		b, e := ReadLocal(p.Controller.CAFile, 1<<20, p.System, false)
		if e != nil {
			return nil, "", e
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(b) {
			return nil, "", errors.New("invalid_management_ca")
		}
		tc.RootCAs = roots
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tc, DisableCompression: true, MaxResponseHeaderBytes: 16384, MaxIdleConnsPerHost: 1, TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 6 * time.Second, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	return &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, cred.Token, nil
}
func Exchange(ctx context.Context, h *http.Client, token, method, url string, body []byte, limit int64) ([]byte, int, error) {
	req, e := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, e := h.Do(req)
	if e != nil {
		return nil, 0, errors.New("management_transport_failed")
	}
	defer r.Body.Close()
	b, e := readLimit(r.Body, limit)
	if e != nil {
		return nil, r.StatusCode, e
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, r.StatusCode, errors.New("management_request_refused")
	}
	return b, r.StatusCode, nil
}
