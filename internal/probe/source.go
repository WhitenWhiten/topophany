package probe

import (
	"context"
	"errors"
	stdio "io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/beszel"
	"github.com/WhitenWhiten/topophany/internal/adapters/cfservermonitor"
	"github.com/WhitenWhiten/topophany/internal/adapters/komari"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// NewSourceRuntime wires the static adapter map. Production passes the
// adapters this binary ships; tests pass synthetic adapters explicitly.
// There is no dynamic registration or environment-selected adapter.
func NewSourceRuntime(adapters map[string]source.Adapter) source.Runtime {
	return source.Runtime{Adapters: adapters, NewIO: newSourceIO}
}

// sourceIO is the only network access a source adapter gets. It reads the
// configured origin over the source's explicit route and TLS policy, with no
// ambient proxy, redirect following, compression or unbounded body.
type sourceIO struct {
	client   *http.Client
	source   model.Source
	origin   *url.URL
	pages    int
	maxPages int
}

func newSourceIO(c model.Config, s model.Source) (source.IO, error) {
	u, err := url.Parse(s.Target)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, source.NewError("invalid_source_target", "source target must be an HTTP(S) origin without userinfo or fragment")
	}
	name := u.Hostname()
	if s.ServerName != "" {
		name = s.ServerName
	}
	tc, err := TLSConfig(name, s.CAFile)
	if err != nil {
		return nil, source.NewError("source_tls", "source TLS configuration is invalid")
	}
	proxies := config.Path(c, s.Path).Proxies
	d := Dialer{Proxies: proxies, BindIP: s.BindIP}
	tr := restrictedTransport(tc, 0)
	tr.TLSHandshakeTimeout = time.Duration(max(1, s.TimeoutSeconds)) * time.Second
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, time.Duration(max(1, s.TimeoutSeconds))*time.Second)
		defer cancel()
		return d.DialContext(dialCtx, network, pinned(address, s.ResolveIP))
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &sourceIO{client: client, source: s, origin: u, maxPages: s.MaxPages}, nil
}

func (io *sourceIO) Credential(ctx context.Context, s model.Source) (string, error) {
	if s.ID != io.source.ID {
		return "", source.NewError("wrong_source", "credential requested for another source")
	}
	if s.CredentialEnv == "" {
		return "", nil
	}
	value, err := secure.SecretEnv(s.CredentialEnv)
	if err != nil {
		return "", source.NewError("credential_unavailable", "the source credential is missing")
	}
	return value, nil
}

func (io *sourceIO) Pages() int { return io.pages }
func (io *sourceIO) Close()     { io.client.CloseIdleConnections() }

func (io *sourceIO) Get(ctx context.Context, s model.Source, page string, headers map[string]string, maxBytes int64) (source.Response, error) {
	if s.ID != io.source.ID {
		return source.Response{}, source.NewError("wrong_source", "request for another source")
	}
	if io.pages >= io.maxPages {
		return source.Response{}, source.NewError("page_budget", "source page budget exhausted")
	}
	if maxBytes < 1 || maxBytes > s.MaxResponseBytes {
		return source.Response{}, source.NewError("response_budget", "requested response budget is outside the source budget")
	}
	u, err := io.sameOrigin(page)
	if err != nil {
		return source.Response{}, err
	}
	if err := validSourceHeaders(headers); err != nil {
		return source.Response{}, err
	}
	io.pages++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return source.Response{}, source.NewError("invalid_source_request", "could not build the source request")
	}
	req.Header.Set("Accept-Encoding", "identity")
	for name, value := range headers {
		if strings.EqualFold(name, "Accept-Encoding") {
			continue
		}
		req.Header.Set(name, value)
	}
	resp, err := io.client.Do(req)
	if err != nil {
		if strings.HasPrefix(secure.ErrorCode(err), "tls_") {
			return source.Response{}, &source.Error{Code: "transport_error", Detail: "source request failed", Transport: "tls_error"}
		}
		return source.Response{}, source.NewError("transport_error", "source request failed")
	}
	defer resp.Body.Close()
	body, err := readAtMost(resp.Body, maxBytes)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, stdio.ErrUnexpectedEOF) {
			return source.Response{}, source.NewError("transport_error", "source response read failed")
		}
		return source.Response{}, source.NewError("response_budget", "source response exceeded its byte budget")
	}
	return source.Response{RetryAfter: resp.Header.Get("Retry-After"), Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}, nil
}

func (io *sourceIO) sameOrigin(page string) (*url.URL, error) {
	if page == "" {
		page = io.source.Target
	}
	u, err := url.Parse(page)
	if err != nil {
		return nil, source.NewError("invalid_source_url", "page URL is invalid")
	}
	u = io.origin.ResolveReference(u)
	if u.Scheme != io.origin.Scheme || !strings.EqualFold(u.Host, io.origin.Host) || u.User != nil || u.Fragment != "" {
		return nil, source.NewError("cross_origin", "source pages must stay on the configured origin")
	}
	return u, nil
}

func validSourceHeaders(headers map[string]string) error {
	if len(headers) > 16 {
		return source.NewError("header_budget", "too many request headers")
	}
	for name, value := range headers {
		if len(name) == 0 || len(name) > 64 || len(value) > 1024 || !validHeaderName(name) || hasHeaderBreak(value) {
			return source.NewError("invalid_header", "source request header is unsafe")
		}
		switch strings.ToLower(name) {
		case "host", "content-length", "transfer-encoding", "connection", "upgrade", "proxy-authorization", "proxy-authenticate":
			return source.NewError("invalid_header", "source request header is transport-owned")
		}
	}
	return nil
}

func validHeaderName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func hasHeaderBreak(s string) bool {
	for _, r := range s {
		if r == '\r' || r == '\n' || r == 0 || r == 0x7f {
			return true
		}
	}
	return false
}

// readAtMost bounds a body with a hard read cap, including a peer that
// ignores Content-Length.
func readAtMost(r stdio.Reader, limit int64) ([]byte, error) {
	if limit < 1 {
		return nil, errors.New("invalid read limit")
	}
	b, err := stdio.ReadAll(stdio.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("read limit exceeded")
	}
	return b, nil
}

// ProductionSourceAdapters is the static adapter registry this binary ships.
// Real adapters are wired here explicitly; test-only synthetic adapters are
// never registered.
func ProductionSourceAdapters() map[string]source.Adapter {
	return map[string]source.Adapter{
		"beszel":             beszel.Adapter{},
		cfservermonitor.Name: cfservermonitor.Adapter{},
		"komari":             komari.Adapter{},
	}
}

// SourceIO supplies the same controlled GET transport with an explicit resolver.
// The resolver's references stay outside public topology and snapshots.
func SourceIO(c model.Config, s model.Source, resolve func(context.Context, model.Source) (string, error)) (source.IO, error) {
	io, err := newSourceIO(c, s)
	if err != nil {
		return nil, err
	}
	return &resolvedSourceIO{IO: io, resolve: resolve}, nil
}

type resolvedSourceIO struct {
	source.IO
	resolve func(context.Context, model.Source) (string, error)
}

func (io *resolvedSourceIO) Credential(ctx context.Context, s model.Source) (string, error) {
	return io.resolve(ctx, s)
}
