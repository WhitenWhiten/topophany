package cfservermonitor

import (
	"context"
	"net/http"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/source"
)

type fixtureIO struct {
	token       string
	credentials int
	pages       int
	read        func(string, map[string]string, int64) source.Response
}

func (f *fixtureIO) Credential(context.Context, model.Source) (string, error) {
	f.credentials++
	return f.token, nil
}

func (f *fixtureIO) Get(_ context.Context, _ model.Source, target string, headers map[string]string, limit int64) (source.Response, error) {
	f.pages++
	return f.read(target, headers, limit), nil
}

func (f *fixtureIO) Pages() int { return f.pages }
func (f *fixtureIO) Close()     {}

func fixtureSource() model.Source {
	return model.Source{
		ID: "cfsm", Adapter: "cf-server-monitor", Target: "https://cfsm.example.invalid/monitor/",
		MaxPages: 2, MaxObjects: 2, MaxResponseBytes: 4096,
	}
}

func resourceRequest(id string) source.Request {
	return source.Request{Object: id, Reports: []string{model.ReportTypeResource}}
}

func TestCFServerMonitorWorkerEndpointAndAuthentication(t *testing.T) {
	for _, token := range []string{"", "synthetic.cfsm.sig+nature/"} {
		name := "public"
		if token != "" {
			name = "bearer"
		}
		t.Run(name, func(t *testing.T) {
			s := fixtureSource()
			auth := ""
			credentialReads := 0
			if token != "" {
				s.CredentialEnv = "CFSM_TEST_JWT"
				auth = "Bearer " + token
				credentialReads = 1
			}
			io := &fixtureIO{token: token, read: func(target string, headers map[string]string, limit int64) source.Response {
				if target != "https://cfsm.example.invalid/monitor/api/server?id="+testObject || limit != s.MaxResponseBytes ||
					headers["Accept"] != "application/json" || headers["Authorization"] != auth {
					t.Errorf("unexpected target, headers or read budget")
				}
				return source.Response{Status: http.StatusOK, ContentType: "application/json; charset=utf-8",
					Body: detail(`"last_updated":1793653320123,"cpu":12.5`)}
			}}
			got, err := (Adapter{}).Collect(t.Context(), io, s, []source.Request{resourceRequest(testObject)})
			if err != nil || len(got) != 1 || got[0].Object != testObject || io.pages != 1 || io.credentials != credentialReads {
				t.Fatalf("collection = %+v, %v; reads = %d, credential resolutions = %d", got, err, io.pages, io.credentials)
			}
		})
	}
}

func TestCFServerMonitorRequestPreflight(t *testing.T) {
	second := "23456789-2345-4234-8234-123456789abc"
	cases := []struct {
		name     string
		requests []source.Request
		pages    int
		code     string
	}{
		{"invalid id", []source.Request{resourceRequest(testObject + "&id=" + second)}, 0, "invalid_object_request"},
		{"duplicate id", []source.Request{resourceRequest(testObject), resourceRequest(testObject)}, 0, "invalid_object_request"},
		{"status", []source.Request{{Object: testObject, Reports: []string{model.ReportTypeStatus}}}, 0, "unsupported_report_type"},
		{"empty", nil, 0, "object_budget"},
		{"page budget", []source.Request{resourceRequest(testObject), resourceRequest(second)}, 1, "page_budget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := fixtureSource()
			s.CredentialEnv = "CFSM_TEST_JWT"
			io := &fixtureIO{pages: c.pages}
			got, err := (Adapter{}).Collect(t.Context(), io, s, c.requests)
			if got != nil || source.Code(err) != c.code || io.credentials != 0 || io.pages != c.pages {
				t.Fatalf("collection = %+v, %v; reads = %d, credential resolutions = %d", got, err, io.pages, io.credentials)
			}
		})
	}
	s := fixtureSource()
	s.CredentialEnv = "CFSM_TEST_JWT"
	io := &fixtureIO{token: "agent-secret"}
	got, err := (Adapter{}).Collect(t.Context(), io, s, []source.Request{resourceRequest(testObject)})
	if got != nil || source.Code(err) != "cfsm_credential" || io.pages != 0 {
		t.Fatalf("agent secret used as a JWT: %+v, %v", got, err)
	}
	s.Target = "https://cfsm.example.invalid?token=private"
	if (Adapter{}).Validate(s) == nil {
		t.Fatal("accepted a credential in the base URL")
	}
}

func TestCFServerMonitorCollectionDiscardsPartialResults(t *testing.T) {
	second := "23456789-2345-4234-8234-123456789abc"
	s := fixtureSource()
	cases := []struct {
		name     string
		response source.Response
		code     string
	}{
		{"missing server", source.Response{Status: http.StatusNotFound}, "cfsm_server_not_found"},
		{"denied", source.Response{Status: http.StatusForbidden}, "cfsm_auth"},
		{"limited", source.Response{Status: http.StatusTooManyRequests}, "cfsm_rate_limited"},
		{"redirect", source.Response{Status: http.StatusFound}, "cfsm_http"},
		{"HTML", source.Response{Status: http.StatusOK, ContentType: "text/html"}, "cfsm_content_type"},
		{"over budget", source.Response{Status: http.StatusOK, Body: make([]byte, s.MaxResponseBytes+1)}, "response_budget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			io := &fixtureIO{}
			io.read = func(string, map[string]string, int64) source.Response {
				if io.pages == 1 {
					return source.Response{Status: http.StatusOK, Body: detail(`"last_updated":1793653320123,"cpu":12.5`)}
				}
				return c.response
			}
			got, err := (Adapter{}).Collect(t.Context(), io, s, []source.Request{resourceRequest(testObject), resourceRequest(second)})
			if got != nil || source.Code(err) != c.code || io.pages != 2 {
				t.Fatalf("collection = %+v, %v; reads = %d", got, err, io.pages)
			}
		})
	}
}
