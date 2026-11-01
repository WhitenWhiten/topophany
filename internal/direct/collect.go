package direct

import (
	"context"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/httpfailure"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
	"github.com/WhitenWhiten/topophany/internal/source"
	"net/url"
	"time"
)

type attemptIO struct {
	now func() time.Time
	source.IO
	failure error
	retry   time.Time
}

func (a *attemptIO) Get(ctx context.Context, s model.Source, u string, headers map[string]string, max int64) (source.Response, error) {
	r, e := a.IO.Get(ctx, s, u, headers, max)
	if e != nil {
		code := "upstream_incompatible"
		if source.Code(e) == "transport_error" || ctx.Err() != nil {
			code = "connection_failed"
		}
		var transport *source.Error
		if source.Code(e) == "source_tls" || errors.As(e, &transport) && transport.Transport == "tls_error" {
			code = "tls_error"
		}
		a.failure = fail(code)
		return r, e
	}
	switch httpfailure.Classify(r.Status) {
	case httpfailure.Authentication:
		a.failure = fail("auth_required")
	case httpfailure.Permission:
		a.failure = fail("forbidden")
	case httpfailure.RateLimited:
		a.failure = fail("rate_limited")
		a.retry = retryAfter(r.RetryAfter, a.now())
	case httpfailure.Transient:
		a.failure = fail("connection_failed")
	}
	return r, nil
}
func (s *Session) collect(ctx context.Context, c model.Config, src model.Source, requests []source.Request) ([]source.Reading, time.Time, error) {
	// Local transport files can disappear between reads. Report their failure
	// through this source's loop, without preventing healthy sources starting.
	if src.CAFile != "" {
		u, err := url.Parse(src.Target)
		if err != nil {
			return nil, time.Time{}, fail("invalid_request")
		}
		if _, err := probe.TLSConfig(u.Hostname(), src.CAFile); err != nil {
			return nil, time.Time{}, fail("tls_error")
		}
	}
	s.mu.Lock()
	entry, ok := findEntry(s.profile, src.ID)
	profile := s.profile.Profile
	s.mu.Unlock()
	if !ok || entry.Source.URL != src.Target {
		return nil, time.Time{}, fail("source_not_found")
	}
	token, e := s.store.Token(ctx, profile, entry, s.opts.Now())
	if e != nil {
		return nil, AsError(e).RetryAt, e
	}
	var io *attemptIO
	rt := probe.NewSourceRuntime(probe.ProductionSourceAdapters())
	rt.NewIO = func(c model.Config, src model.Source) (source.IO, error) {
		base, e := probe.SourceIO(c, src, func(context.Context, model.Source) (string, error) { return token, nil })
		if e != nil {
			return nil, e
		}
		io = &attemptIO{IO: base, now: s.opts.Now}
		return io, nil
	}
	readings, e := rt.Collect(ctx, c, src, requests)
	if io != nil && io.failure != nil {
		if AsError(io.failure).Code == "auth_required" {
			s.mu.Lock()
			l := s.loops[src.ID]
			canRefresh := l != nil && !l.renewed401
			if canRefresh {
				l.renewed401 = true
			}
			s.mu.Unlock()
			if canRefresh {
				if _, err := s.store.Refresh(ctx, profile, entry, token); err != nil {
					return nil, AsError(err).RetryAt, err
				}
				return nil, time.Time{}, &Error{Code: "credential_renewed", Message: "Credential renewed; retrying the read.", Retryable: true}
			}
		}
		return nil, io.retry, io.failure
	}
	if e != nil {
		if source.Code(e) == "source_tls" {
			return nil, time.Time{}, fail("tls_error")
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || source.Code(e) == "transport_error" {
			return nil, time.Time{}, fail("connection_failed")
		}
		return nil, time.Time{}, fail("upstream_incompatible")
	}
	return readings, time.Time{}, nil
}
