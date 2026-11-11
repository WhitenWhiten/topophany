package komari

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

const (
	testObject   = "d4c8d9a1-4ec5-4c1b-9b95-4c1c8f930b0d"
	otherObject  = "c2710eef-7211-433e-8956-333484d30cbc"
	testToken    = "synthetic.komari.api-key.123456789"
	testUpdated  = "2026-10-22T12:34:56.123456789Z"
	testSampleID = "komari-858635a967990049145e1a62f5e9e656294deaab6ebdda1232573970eb828b37"
)

type recordedRead struct {
	target   string
	headers  map[string]string
	maxBytes int64
}

// This double tests the small adapter contract. Real sockets, TLS, redirects
// and production registration are covered by the integration suite.
type fixtureIO struct {
	token         string
	credentialErr error
	getErr        error
	responses     []source.Response
	credentials   int
	pages         int
	gets          []recordedRead
	afterGet      func()
}

func (f *fixtureIO) Credential(context.Context, model.Source) (string, error) {
	f.credentials++
	return f.token, f.credentialErr
}

func (f *fixtureIO) Get(_ context.Context, _ model.Source, target string, headers map[string]string, maxBytes int64) (source.Response, error) {
	h := make(map[string]string, len(headers))
	for key, value := range headers {
		h[key] = value
	}
	f.gets = append(f.gets, recordedRead{target, h, maxBytes})
	f.pages++
	if f.afterGet != nil {
		f.afterGet()
	}
	if f.getErr != nil {
		return source.Response{}, f.getErr
	}
	if len(f.gets) > len(f.responses) {
		return source.Response{}, errors.New("unexpected extra fixture read")
	}
	return f.responses[len(f.gets)-1], nil
}

func (f *fixtureIO) Pages() int { return f.pages }

func testSource() model.Source {
	return model.Source{ID: "komari-test", Adapter: "komari", Observer: "cloud", Target: "https://komari.example.test/monitor", CredentialEnv: "KOMARI_TEST_TOKEN", IntervalSeconds: 30, TimeoutSeconds: 8, StaleSeconds: 90, MaxResponseBytes: 1 << 20, MaxPages: 2, MaxObjects: 2, MaxReportBytes: model.MaxReportBytes}
}

func requestsFor(objects ...string) []source.Request {
	requests := make([]source.Request, len(objects))
	for i, object := range objects {
		requests[i] = source.Request{Object: object, Reports: []string{model.ReportTypeResource}}
	}
	return requests
}

func jsonResponse(body []byte) source.Response {
	return source.Response{Status: http.StatusOK, ContentType: "application/json; charset=utf-8", Body: body}
}

func readFixture(t testing.TB) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/recent_v1.6.0.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func sample(object, at, fields string) string {
	return fmt.Sprintf(`{"uuid":%q,"updated_at":%q,%s}`, object, at, fields)
}

func recent(records ...string) []byte {
	return []byte(`{"status":"success","message":"","data":[` + strings.Join(records, ",") + `]}`)
}

func resourceBody(fields string) []byte { return recent(sample(testObject, testUpdated, fields)) }

func requireResource(t testing.TB, body []byte) *model.ExternalReport {
	t.Helper()
	r, err := resource(body, testObject)
	if err != nil || r == nil || r.Resource == nil || r.Status != nil {
		t.Fatalf("resource decode = %+v, %v", r, err)
	}
	if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
		t.Fatalf("decoded report violates production admission: %v", err)
	}
	return r
}

func wantValue[T comparable](t testing.TB, name string, got *T, want T) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func wantField(t testing.TB, fields map[string]string, key, want string) {
	t.Helper()
	if fields[key] != want {
		t.Fatalf("field %s = %q, want %q", key, fields[key], want)
	}
}

func TestKomariReadOnlyProtocolAndAuthoritativeMapping(t *testing.T) {
	s := testSource()
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t))}}
	got, err := (Adapter{}).Collect(context.Background(), f, s, requestsFor(testObject))
	if err != nil || len(got) != 1 {
		t.Fatalf("Collect = %+v, %v", got, err)
	}
	if f.credentials != 1 || len(f.gets) != 1 || f.pages != 1 {
		t.Fatalf("I/O credentials=%d reads=%d pages=%d", f.credentials, len(f.gets), f.pages)
	}
	request := f.gets[0]
	u, err := url.Parse(request.target)
	if err != nil || u.Scheme != "https" || u.Host != "komari.example.test" || u.Path != "/monitor/api/recent/"+testObject || u.RawQuery != "" || u.User != nil {
		t.Fatalf("read targeted unexpected endpoint: %s, %v", request.target, err)
	}
	if request.headers["Authorization"] != "Bearer "+testToken || request.headers["Accept"] != "application/json" || request.maxBytes != s.MaxResponseBytes {
		t.Fatal("authorization, media type or response budget changed (token omitted)")
	}
	r := got[0].Report
	if got[0].Object != testObject || r.Object != testObject || r.SampleID != testSampleID || r.TimeBasis != model.TimeBasisUpstreamSample || r.Epoch != "" || r.Sequence != nil {
		t.Fatalf("sample identity = %+v", r.ReportMeta)
	}
	wantAt := time.Date(2026, 10, 22, 12, 34, 56, 123456789, time.UTC)
	if !r.ObservedAt.Equal(wantAt) || r.ObservedAt.Location() != time.UTC {
		t.Fatalf("sample time = %v", r.ObservedAt)
	}
	if _, err := report.Normalize(r, model.MaxReportBytes); err != nil {
		t.Fatal(err)
	}
	if r.Resource == nil || r.Status != nil || r.Resource.CPU == nil || r.Resource.Memory == nil || len(r.Resource.Disks) != 1 {
		t.Fatalf("resource shape = %+v", r)
	}
	cpu, memory, disk := r.Resource.CPU, r.Resource.Memory, r.Resource.Disks[0]
	wantValue(t, "cpu", cpu.Percent, 12.5)
	wantValue(t, "cores", cpu.LogicalCores, 4)
	wantValue(t, "memory total", memory.TotalBytes, uint64(8589934592))
	wantValue(t, "memory used", memory.UsedBytes, uint64(3221225472))
	wantValue(t, "memory percent", memory.Percent, 37.5)
	wantValue(t, "disk total", disk.TotalBytes, uint64(107374182400))
	wantValue(t, "disk used", disk.UsedBytes, uint64(21474836480))
	wantValue(t, "disk percent", disk.Percent, 20.0)
	wantValue(t, "rx rate", r.Resource.Network.RXBytesPerSecond, 5678.0)
	wantValue(t, "tx rate", r.Resource.Network.TXBytesPerSecond, 1234.0)
	wantValue(t, "swap total", r.Resource.Swap.TotalBytes, uint64(2147483648))
	wantValue(t, "swap used", r.Resource.Swap.UsedBytes, uint64(0))
	wantValue(t, "load 1m", r.Resource.Load.Avg1m, 0.5)
	wantValue(t, "load 5m", r.Resource.Load.Avg5m, 0.75)
	wantValue(t, "load 15m", r.Resource.Load.Avg15m, 1.0)
	if disk.ID != "aggregate" || disk.Path != "" {
		t.Fatalf("aggregate disk was mislabeled as a mount: %+v", disk)
	}
	if cpu.SampleWindowMS != nil || memory.AvailableBytes != nil || disk.FreeBytes != nil {
		t.Fatal("unsupported sample window/available/free fields were invented")
	}
	for _, key := range []string{"cpu.sample_window_ms", "memory.available_bytes", "disk.aggregate.free_bytes"} {
		wantField(t, r.Fields, key, model.FieldUnsupported)
	}
}

func TestKomariPublicSourceDoesNotResolveOrSendCredentials(t *testing.T) {
	s := testSource()
	s.CredentialEnv = ""
	f := &fixtureIO{token: testToken, credentialErr: errors.New("must not resolve a public credential"), responses: []source.Response{jsonResponse(readFixture(t))}}
	got, err := (Adapter{}).Collect(context.Background(), f, s, requestsFor(testObject))
	if err != nil || len(got) != 1 || f.credentials != 0 || len(f.gets) != 1 {
		t.Fatalf("public collection touched credential resolver: %v; credentials=%d", err, f.credentials)
	}
	if _, found := f.gets[0].headers["Authorization"]; found {
		t.Fatal("public source sent an Authorization header")
	}
}

func TestKomariSourceValidation(t *testing.T) {
	for _, target := range []string{"https://komari.example.test", "https://komari.example.test:8443/prefix/", "http://127.0.0.1:8080", "http://[::1]/monitor", "http://[::ffff:127.0.0.1]"} {
		t.Run("valid/"+target, func(t *testing.T) {
			s := testSource()
			s.Target = target
			if err := (Adapter{}).Validate(s); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, target := range []string{
		"", "komari.example.test", "ftp://komari.example.test", "http://komari.example.test", "http://localhost", "http://127.1", "http://2130706433", "http://0x7f000001", "http://127.000.000.001", "http://127.0.0.1.evil.test", "http://[::]", "http://[::1%25lo0]", "http://[::ffff:192.0.2.1]",
		"https://user:secret@komari.example.test", "https://komari.example.test?secret=value", "https://komari.example.test?", "https://komari.example.test#secret", "https://komari.example.test#", "https://komari.example.test/api", "https://komari.example.test/hub/api/", "https://komari.example.test/a/../b", "https://komari.example.test/a/./b", "https://komari.example.test/a//b", "https://komari.example.test/a%2fb", "https://komari.example.test/escaped%61", "https://komari.example.test:0", "https://komari.example.test:65536", "https://komari.example.test:",
	} {
		t.Run("invalid/"+target, func(t *testing.T) {
			s := testSource()
			s.Target = target
			err := (Adapter{}).Validate(s)
			if err == nil {
				t.Fatal("unsafe or ambiguous target admitted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("untrusted target reflected in validation error")
			}
		})
	}
	for _, change := range []func(*model.Source){
		func(s *model.Source) { s.CredentialEnv = "1INVALID" },
		func(s *model.Source) { s.CredentialEnv = "INVALID-NAME" },
		func(s *model.Source) { s.CredentialEnv = strings.Repeat("A", 129) },
		func(s *model.Source) { s.Target = "http://127.0.0.1"; s.Path = "proxy-path" },
		func(s *model.Source) { s.Target = "http://127.0.0.1"; s.ResolveIP = "192.0.2.1" },
	} {
		s := testSource()
		change(&s)
		if err := (Adapter{}).Validate(s); err == nil {
			t.Fatal("invalid credential reference or plaintext route admitted")
		}
	}
}

func TestKomariCompleteRequestBudgetCheckedBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*model.Source, *fixtureIO)
		requests []source.Request
	}{
		{"no-objects", nil, nil},
		{"object-budget", func(s *model.Source, _ *fixtureIO) { s.MaxObjects = 1 }, requestsFor(testObject, otherObject)},
		{"zero-object-budget", func(s *model.Source, _ *fixtureIO) { s.MaxObjects = 0 }, requestsFor(testObject)},
		{"huge-object-budget", func(s *model.Source, _ *fixtureIO) { s.MaxObjects = model.MaxSourceObjects + 1 }, requestsFor(testObject)},
		{"duplicate-object", nil, requestsFor(testObject, testObject)},
		{"object-path-injection", nil, requestsFor("../" + testObject)},
		{"uppercase-object", nil, requestsFor(strings.ToUpper(testObject))},
		{"object-without-dashes", nil, requestsFor(strings.ReplaceAll(testObject, "-", ""))},
		{"status-is-unsupported", nil, []source.Request{{Object: testObject, Reports: []string{model.ReportTypeStatus}}}},
		{"mixed-status-is-unsupported", nil, []source.Request{{Object: testObject, Reports: []string{model.ReportTypeResource, model.ReportTypeStatus}}}},
		{"duplicate-report", nil, []source.Request{{Object: testObject, Reports: []string{model.ReportTypeResource, model.ReportTypeResource}}}},
		{"missing-report", nil, []source.Request{{Object: testObject}}},
		{"page-budget", func(s *model.Source, _ *fixtureIO) { s.MaxPages = 1 }, requestsFor(testObject, otherObject)},
		{"already-used-page", func(s *model.Source, f *fixtureIO) { s.MaxPages = 1; f.pages = 1 }, requestsFor(testObject)},
		{"negative-used-pages", func(_ *model.Source, f *fixtureIO) { f.pages = -1 }, requestsFor(testObject)},
		{"zero-page-budget", func(s *model.Source, _ *fixtureIO) { s.MaxPages = 0 }, requestsFor(testObject)},
		{"huge-page-budget", func(s *model.Source, _ *fixtureIO) { s.MaxPages = model.MaxSourcePages + 1 }, requestsFor(testObject)},
		{"zero-response-budget", func(s *model.Source, _ *fixtureIO) { s.MaxResponseBytes = 0 }, requestsFor(testObject)},
		{"huge-response-budget", func(s *model.Source, _ *fixtureIO) { s.MaxResponseBytes = (8 << 20) + 1 }, requestsFor(testObject)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSource()
			f := &fixtureIO{token: testToken}
			if tc.change != nil {
				tc.change(&s, f)
			}
			got, err := (Adapter{}).Collect(context.Background(), f, s, tc.requests)
			if err == nil || len(got) != 0 || f.credentials != 0 || len(f.gets) != 0 {
				t.Fatalf("invalid request started collection: error=%v reports=%d credentials=%d reads=%d", err, len(got), f.credentials, len(f.gets))
			}
		})
	}
}

func TestKomariCredentialAndFailureBoundaries(t *testing.T) {
	for _, token := range []string{"", "short", strings.Repeat("a", 1018), "Bearer " + testToken, testToken + "\n", testToken + "\r", testToken + "\x00", testToken + "\x7f", testToken + "é"} {
		f := &fixtureIO{token: token}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
		if err == nil || len(got) != 0 || len(f.gets) != 0 {
			t.Fatal("invalid credential reached network")
		}
		if token != "" && strings.Contains(err.Error(), token) {
			t.Fatal("credential reflected through failure")
		}
	}
	for _, n := range []int{12, 1017} {
		f := &fixtureIO{token: strings.Repeat("a", n), responses: []source.Response{jsonResponse(readFixture(t))}}
		if got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject)); err != nil || len(got) != 1 {
			t.Fatalf("valid %d-byte credential refused: %v", n, err)
		}
	}
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "komari_auth"}, {403, "komari_auth"}, {404, "komari_api_unavailable"}, {429, "komari_rate_limited"}, {500, "komari_http"}, {302, "komari_http"}, {204, "komari_http"}} {
		f := &fixtureIO{token: testToken, responses: []source.Response{{Status: tc.status, Body: []byte(testToken)}}}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
		if err == nil || source.Code(err) != tc.code || len(got) != 0 || strings.Contains(err.Error(), testToken) {
			t.Fatalf("HTTP %d failure mapping = %v, reports=%d", tc.status, err, len(got))
		}
	}
	for _, media := range []string{"text/html", "application/json; bad", "application/problem+json"} {
		response := jsonResponse(readFixture(t))
		response.ContentType = media
		f := &fixtureIO{token: testToken, responses: []source.Response{response}}
		if got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject)); err == nil || len(got) != 0 {
			t.Fatal("unexpected content type admitted")
		}
	}
	for _, inject := range []func(*fixtureIO){
		func(f *fixtureIO) { f.credentialErr = errors.New(testToken) },
		func(f *fixtureIO) { f.getErr = errors.New(testToken) },
		func(f *fixtureIO) { f.getErr = source.NewError("unexpected_vendor_secret", testToken) },
	} {
		f := &fixtureIO{token: testToken}
		inject(f)
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
		if err == nil || len(got) != 0 || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "unexpected_vendor_secret") {
			t.Fatal("upstream I/O error escaped the closed diagnostic vocabulary")
		}
	}
	for _, response := range []source.Response{jsonResponse(recent())} {
		f := &fixtureIO{token: testToken, responses: []source.Response{response}}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
		if err != nil || len(got) != 0 {
			t.Fatalf("absent resource fabricated a report or outage: %+v, %v", got, err)
		}
	}
}

func TestKomariResponseBudgetCancellationAndAtomicMultiObjectFailure(t *testing.T) {
	body := readFixture(t)
	for _, difference := range []int64{0, -1} {
		s := testSource()
		s.MaxResponseBytes = int64(len(body)) + difference
		f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
		got, err := (Adapter{}).Collect(context.Background(), f, s, requestsFor(testObject))
		if difference == 0 && (err != nil || len(got) != 1) {
			t.Fatalf("exact response boundary rejected: %v", err)
		}
		if difference < 0 && (source.Code(err) != "response_budget" || len(got) != 0) {
			t.Fatalf("oversized mock response admitted: %v", err)
		}
	}
	for _, during := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
		if during {
			f.afterGet = cancel
		} else {
			cancel()
		}
		got, err := (Adapter{}).Collect(ctx, f, testSource(), requestsFor(testObject))
		cancel()
		if source.Code(err) != "source_canceled" || len(got) != 0 {
			t.Fatalf("cancellation emitted success: %v", err)
		}
		if !during && (f.credentials != 0 || len(f.gets) != 0) {
			t.Fatal("already-canceled request performed I/O")
		}
	}
	second := recent(sample(otherObject, testUpdated, `"cpu":{"usage":75}`))
	for _, failSecond := range []bool{false, true} {
		responses := []source.Response{jsonResponse(body), jsonResponse(second)}
		if failSecond {
			responses[1] = source.Response{Status: 403}
		}
		f := &fixtureIO{token: testToken, responses: responses}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject, otherObject))
		if len(f.gets) != 2 || f.credentials != 1 {
			t.Fatal("multi-object reads were not bounded to one per object")
		}
		if failSecond {
			if err == nil || len(got) != 0 {
				t.Fatal("a later object failure leaked a successful prefix")
			}
			continue
		}
		if err != nil || len(got) != 2 || got[0].Object != testObject || got[1].Object != otherObject || got[0].Report.SampleID == got[1].Report.SampleID {
			t.Fatalf("multi-object identity crossed: %+v, %v", got, err)
		}
		wantValue(t, "second cpu", got[1].Report.Resource.CPU.Percent, 75.0)
	}
}

func TestKomariCredentialsAndUnmappedTextNeverLeaveAdapter(t *testing.T) {
	body := resourceBody(`"cpu":{"usage":12.5,"name":"` + testToken + `","arch":"` + testToken + `"},"ram":{"total":8,"used":3},"disk":{"total":100,"used":20},"message":"` + testToken + `","network":{"label":"` + testToken + `"}`)
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), requestsFor(testObject))
	if err != nil || len(got) != 1 {
		t.Fatalf("unmapped text prevented valid metrics: %v", err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), testToken) || strings.Contains(string(encoded), "network") || strings.Contains(string(encoded), "message") || strings.Contains(string(encoded), "name") {
		t.Fatalf("unmapped authenticated text escaped: %v", err)
	}
	// Changing an unconsumed field does not alter sample identity/content.
	clean := requireResource(t, resourceBody(`"cpu":{"usage":12.5},"ram":{"total":8,"used":3},"disk":{"total":100,"used":20}`))
	if !reflect.DeepEqual(&got[0].Report, clean) {
		t.Fatal("ignored metadata changed the normalized resource")
	}
}

func (*fixtureIO) Close() {}
