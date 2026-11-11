// Package cfservermonitor reads resource samples from the CF-Server-Monitor
// Worker API using the caller's bounded source transport.
package cfservermonitor

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

// Adapter reads the latest persisted resource sample for each configured server.
type Adapter struct{}

var _ source.Adapter = Adapter{}

// Name is the configuration identifier of this adapter.
const Name = "cf-server-monitor"

var (
	serverID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// CF-Server-Monitor also issues tokens with unpadded standard Base64.
	bearerJWT = regexp.MustCompile(`^[A-Za-z0-9_+/-]+\.[A-Za-z0-9_+/-]+\.[A-Za-z0-9_+/-]+$`)
)

// ValidObjectID accepts the canonical lowercase server UUID.
func ValidObjectID(id string) bool { return serverID.MatchString(id) }

// ValidJWT checks the credential's wire shape. The Worker verifies it.
func ValidJWT(token string) bool {
	return sourceutil.ValidCredential(token, 1, 1024-len("Bearer ")) && bearerJWT.MatchString(token)
}

func (Adapter) Validate(s model.Source) error {
	return sourceutil.ValidateSource(s, sourceutil.SourcePolicy{
		Adapter: Name, BaseName: "Worker", CredentialOptional: true,
	})
}

func (a Adapter) Collect(ctx context.Context, io source.IO, s model.Source, requests []source.Request) ([]source.Reading, error) {
	if a.Validate(s) != nil {
		return nil, failure("cfsm_config")
	}
	if code := sourceutil.ObjectBudget(s, len(requests)); code != "" {
		return nil, failure(code)
	}
	seen := make(map[string]bool, len(requests))
	for _, request := range requests {
		if !ValidObjectID(request.Object) || seen[request.Object] {
			return nil, failure("invalid_object_request")
		}
		seen[request.Object] = true
		if len(request.Reports) != 1 || request.Reports[0] != model.ReportTypeResource {
			return nil, failure("unsupported_report_type")
		}
	}
	if code := sourceutil.ReadBudget(s, io.Pages(), len(requests)); code != "" {
		return nil, failure(code)
	}
	headers, token, err := requestHeaders(ctx, io, s)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(s.Target, "/") + "/api/server?id="
	readings := make([]source.Reading, 0, len(requests))
	for _, request := range requests {
		body, err := get(ctx, io, s, base+request.Object, headers)
		if err != nil {
			return nil, err
		}
		sample, err := resource(body, request.Object)
		if err != nil {
			return nil, err
		}
		if sample == nil {
			continue
		}
		if err := sourceutil.GuardReport(sample, token); err != nil {
			if errors.Is(err, sourceutil.ErrCredentialReflection) {
				return nil, failure("cfsm_credential_reflection")
			}
			return nil, failure("cfsm_report")
		}
		readings = append(readings, source.Reading{Object: request.Object, Report: *sample})
	}
	if ctx.Err() != nil {
		return nil, failure("source_canceled")
	}
	return readings, nil
}

func requestHeaders(ctx context.Context, io source.IO, s model.Source) (map[string]string, string, error) {
	if ctx.Err() != nil {
		return nil, "", failure("source_canceled")
	}
	headers := map[string]string{"Accept": "application/json"}
	if s.CredentialEnv == "" {
		return headers, "", nil
	}
	token, err := io.Credential(ctx, s)
	if err != nil {
		return nil, "", failure(sourceutil.IOFailureCode(ctx, err))
	}
	if !ValidJWT(token) {
		return nil, "", failure("cfsm_credential")
	}
	headers["Authorization"] = "Bearer " + token
	return headers, token, nil
}

func get(ctx context.Context, io source.IO, s model.Source, target string, headers map[string]string) ([]byte, error) {
	response, err := sourceutil.Get(ctx, io, s, target, headers)
	if err != nil {
		return nil, err
	}
	switch response.Status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, failure("cfsm_auth")
	case http.StatusNotFound:
		return nil, failure("cfsm_server_not_found")
	case http.StatusTooManyRequests:
		return nil, failure("cfsm_rate_limited")
	default:
		return nil, failure("cfsm_http")
	}
	if !sourceutil.JSONContentType(response.ContentType) {
		return nil, failure("cfsm_content_type")
	}
	return response.Body, nil
}

func failure(code string) error {
	return source.NewError(code, "CF-Server-Monitor source data could not be collected")
}
