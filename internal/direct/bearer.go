package direct

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/cfservermonitor"
	"github.com/WhitenWhiten/topophany/internal/adapters/komari"
	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/source"
)

type bearerConnection struct{ source SourceConfig }

func (*bearerConnection) Close() {}

func anonymousSubject(adapter string) string { return adapter + ":anonymous" }

// These credentials are nonrenewable. The fingerprint binds saved profiles
// to the credential accepted during discovery, including environment values.
func bearerSubject(adapter, token string) string {
	switch adapter {
	case "komari":
		if sourceutil.ValidCredential(token, 12, 1024-len("Bearer ")) {
			return "komari:key:" + secure.Hash(token)
		}
	case cfservermonitor.Name:
		if cfservermonitor.ValidJWT(token) {
			return cfservermonitor.Name + ":jwt:" + secure.Hash(token)
		}
	}
	return ""
}

func bearerValue(c Credential) (string, error) {
	switch c.Mode {
	case "token":
		return c.Token, nil
	case "env":
		token, err := secure.SecretEnv(c.Env)
		if err != nil {
			return "", fail("auth_required")
		}
		return token, nil
	default:
		return "", fail("auth_method_unsupported")
	}
}

func bearerToken(adapter string, c Credential, subject string) (string, error) {
	if c.Mode == "anonymous" && c.Subject == anonymousSubject(adapter) && subject == c.Subject {
		return "", nil
	}
	token, err := bearerValue(c)
	if err != nil {
		return "", err
	}
	if actual := bearerSubject(adapter, token); actual == "" || c.Subject != actual || subject != c.Subject {
		return "", fail("auth_required")
	}
	return token, nil
}

func (s *Store) authenticateBearer(r Request) (Credential, Connection, error) {
	if err := r.Source.Validate(); err != nil {
		return Credential{}, nil, err
	}
	c := Credential{Mode: r.Auth.Mode, Token: r.Auth.Token, Env: r.Auth.Name}
	if c.Mode == "saved" {
		p, err := s.Load(r.Profile)
		if err != nil {
			return c, nil, err
		}
		entry, ok := findEntry(p, r.Source.ID)
		if !ok {
			return c, nil, fail("source_not_found")
		}
		if entry.Source.URL != r.Source.URL || entry.Source.Adapter != r.Source.Adapter {
			return c, nil, fail("invalid_request")
		}
		c, err = s.credential(entry.Credential)
		if err != nil {
			return c, nil, err
		}
		if _, err := bearerToken(r.Source.Adapter, c, entry.Subject); err != nil {
			return c, nil, err
		}
	} else {
		if c.Mode == "anonymous" {
			c.Subject = anonymousSubject(r.Source.Adapter)
		} else {
			token, err := bearerValue(c)
			if err != nil {
				return c, nil, err
			}
			c.Subject = bearerSubject(r.Source.Adapter, token)
			if c.Subject == "" {
				return c, nil, fail("auth_required")
			}
		}
	}
	return c, &bearerConnection{source: r.Source}, nil
}

func (h *bearerConnection) Discover(ctx context.Context, c Credential) ([]System, bool, error) {
	token, err := bearerToken(h.source.Adapter, c, c.Subject)
	if err != nil {
		return nil, false, err
	}
	src := h.source.model(1)
	if c.Mode == "anonymous" {
		src.CredentialEnv = ""
	}
	discover := komari.Discover
	if src.Adapter == cfservermonitor.Name {
		discover = cfservermonitor.Discover
		src.MaxPages = 2
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.source.Timeout)*time.Second)
	defer cancel()
	base, err := probe.SourceIO(model.Config{}, src, func(context.Context, model.Source) (string, error) { return token, nil })
	if err != nil {
		return nil, false, discoveryError(err)
	}
	defer base.Close()
	io := &attemptIO{IO: base, now: time.Now}
	nodes, err := discover(ctx, io, src)
	if err != nil && source.Code(err) == "response_budget" && src.Adapter == cfservermonitor.Name {
		return nil, false, discoveryError(err)
	}
	if io.failure != nil {
		AsError(io.failure).RetryAt = io.retry
		return nil, false, io.failure
	}
	if err != nil {
		return nil, false, discoveryError(err)
	}
	systems := make([]System, 0, len(nodes))
	for _, node := range nodes {
		systems = append(systems, System{ID: node.ID, Name: node.Name})
	}
	return systems, false, nil
}

func discoveryError(err error) error {
	switch source.Code(err) {
	case "source_tls":
		return fail("tls_error")
	case "transport_error", "source_canceled":
		return fail("connection_failed")
	case "komari_discovery_budget", "cfsm_discovery_budget", "response_budget":
		return fail("discovery_limit")
	case "cfsm_auth":
		return fail("auth_required")
	default:
		return fail("upstream_incompatible")
	}
}

// An expired CF-Server-Monitor token can otherwise silently read public nodes
// as a guest. Only the Worker can verify the signature. Komari keys stay opaque.
func cfsmJWTExpired(token string, now time.Time) bool {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	return err == nil && json.Unmarshal(payload, &claims) == nil && claims.Expires > 0 &&
		!now.Before(time.Unix(claims.Expires, 0))
}
