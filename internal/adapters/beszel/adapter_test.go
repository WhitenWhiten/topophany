package beszel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	testSystem  = "validationsys01"
	testRecord  = "validationrec03"
	testToken   = "synthetic.beszel.readonly-token"
	testCreated = "2026-10-21 12:34:56.789Z"
	storageDisk = "fs-49a25f9feefaffecad0fcd30c50dc9331cff8b55ece53def6285c09e17e6f5d7"
)

type recordedGet struct {
	source   model.Source
	url      string
	headers  map[string]string
	maxBytes int64
}

// This double exercises the adapter's bounded source.IO contract. End-to-end
// transport and production registration are covered in internal/integration.
type fixtureIO struct {
	token         string
	credentialErr error
	getErr        error
	responses     []source.Response
	credentials   int
	pages         int
	gets          []recordedGet
	afterGet      func()
}

func (f *fixtureIO) Credential(context.Context, model.Source) (string, error) {
	f.credentials++
	return f.token, f.credentialErr
}

func (f *fixtureIO) Get(_ context.Context, s model.Source, u string, headers map[string]string, maxBytes int64) (source.Response, error) {
	h := make(map[string]string, len(headers))
	for k, v := range headers {
		h[k] = v
	}
	f.gets = append(f.gets, recordedGet{s, u, h, maxBytes})
	f.pages++
	if f.afterGet != nil {
		f.afterGet()
	}
	if f.getErr != nil {
		return source.Response{}, f.getErr
	}
	if len(f.gets) > len(f.responses) {
		return source.Response{}, errors.New("unexpected extra fixture request")
	}
	return f.responses[len(f.gets)-1], nil
}

func (f *fixtureIO) Pages() int { return f.pages }

func testSource() model.Source {
	return model.Source{ID: "beszel-test", Adapter: "beszel", Observer: "cloud", Target: "https://beszel.example.test/hub", CredentialEnv: "BESZEL_TEST_TOKEN", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, MaxResponseBytes: 1 << 20, MaxPages: 2, MaxObjects: 2, MaxReportBytes: model.MaxReportBytes}
}

func jsonResponse(body []byte) source.Response {
	return source.Response{Status: http.StatusOK, ContentType: "application/json; charset=utf-8", Body: body}
}

func readFixture(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/system_stats_v0.21.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func resourceBody(stats, created, id string) []byte {
	return []byte(fmt.Sprintf(`{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[{"id":%q,"system":%q,"type":"1m","created":%q,"stats":%s}]}`, id, testSystem, created, stats))
}

func collectResponse(t testing.TB, body []byte, kind string) ([]source.Reading, error) {
	t.Helper()
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
	return (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{kind}}})
}

func decodeFixture(t testing.TB, body string) (*model.ResourceReport, map[string]string) {
	t.Helper()
	r, fields, err := decodeStats(json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	assertContract(t, r, fields)
	return r, fields
}

func assertContract(t testing.TB, r *model.ResourceReport, fields map[string]string) {
	t.Helper()
	e := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: testSystem, SampleID: testRecord, ObservedAt: time.Date(2026, 10, 21, 12, 34, 56, 789000000, time.UTC), TimeBasis: model.TimeBasisUpstreamSample, Fields: fields}, Resource: r}
	if _, err := report.Normalize(e, model.MaxReportBytes); err != nil {
		t.Fatalf("decoded report violates the production contract: %v; report=%+v fields=%v", err, r, fields)
	}
}

func wantValue[T comparable](t testing.TB, name string, got *T, want T) {
	t.Helper()
	if got == nil || *got != want {
		if got == nil {
			t.Fatalf("%s = nil, want %v", name, want)
		}
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func wantField(t testing.TB, fields map[string]string, key, want string) {
	t.Helper()
	if fields[key] != want {
		t.Fatalf("field availability %q = %q, want %q", key, fields[key], want)
	}
}

func TestBeszelOptionalResourceMetrics(t *testing.T) {
	r, _, err := decodeStats([]byte(`{"cpu":12,"b":[2048,4096],"la":[0,0.25,1.5],"s":2,"su":0.5}`))
	if err != nil {
		t.Fatal(err)
	}
	wantValue(t, "rx rate", r.Network.RXBytesPerSecond, 4096.0)
	wantValue(t, "tx rate", r.Network.TXBytesPerSecond, 2048.0)
	wantValue(t, "swap total", r.Swap.TotalBytes, uint64(2<<30))
	wantValue(t, "swap used", r.Swap.UsedBytes, uint64(1<<29))
	wantValue(t, "load 1m", r.Load.Avg1m, 0.0)
	wantValue(t, "load 5m", r.Load.Avg5m, 0.25)
	wantValue(t, "load 15m", r.Load.Avg15m, 1.5)

	r, _, err = decodeStats([]byte(`{"cpu":12,"nr":0.5,"ns":1.5,"s":2}`))
	if err != nil {
		t.Fatal(err)
	}
	wantValue(t, "legacy rx rate", r.Network.RXBytesPerSecond, float64(1<<19))
	wantValue(t, "legacy tx rate", r.Network.TXBytesPerSecond, 1.5*(1<<20))
	wantValue(t, "omitted zero swap usage", r.Swap.UsedBytes, uint64(0))

	r, _, err = decodeStats([]byte(`{"cpu":12,"b":[0,-1],"nr":3,"la":[1,-1,null],"s":10,"swap":[9007199254740993,1]}`))
	if err != nil {
		t.Fatal(err)
	}
	wantValue(t, "idle tx rate", r.Network.TXBytesPerSecond, 0.0)
	wantValue(t, "exact swap total", r.Swap.TotalBytes, uint64(9007199254740993))
	if r.Network.RXBytesPerSecond != nil || r.Load.Avg5m != nil || r.Load.Avg15m != nil {
		t.Fatal("invalid or missing fields acquired values", r.ResourceMetrics)
	}
}

func TestResourceProtocolQueryAndAuthoritativeValues(t *testing.T) {
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t))}}
	s := testSource()
	got, err := (Adapter{}).Collect(context.Background(), f, s, []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Collect = %+v, %v", got, err)
	}
	if f.credentials != 1 || len(f.gets) != 1 {
		t.Fatalf("IO calls: credential=%d GET=%d", f.credentials, len(f.gets))
	}
	request := f.gets[0]
	u, err := url.Parse(request.url)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/hub/api/collections/system_stats/records" || u.Host != "beszel.example.test" || u.Scheme != "https" {
		t.Fatalf("resource endpoint = %s", request.url)
	}
	wantQuery := url.Values{"page": {"1"}, "perPage": {"1"}, "sort": {"-created,-id"}, "skipTotal": {"true"}, "filter": {`system = "validationsys01" && type = "1m"`}, "fields": {"id,system,type,created,stats"}}
	if !reflect.DeepEqual(u.Query(), wantQuery) {
		t.Fatalf("query = %v, want %v", u.Query(), wantQuery)
	}
	if request.headers["Authorization"] != testToken || request.headers["Accept"] != "application/json" || request.maxBytes != s.MaxResponseBytes || request.source.ID != s.ID {
		t.Fatalf("request authorization/media/budget contract is wrong (token omitted)")
	}
	r := got[0].Report
	if got[0].Object != testSystem || r.Object != testSystem || r.SampleID != testRecord || r.TimeBasis != model.TimeBasisUpstreamSample || r.Epoch != "" || r.Sequence != nil {
		t.Fatalf("upstream identity = %+v", r.ReportMeta)
	}
	wantTime := time.Date(2026, 10, 21, 12, 34, 56, 789000000, time.UTC)
	if !r.ObservedAt.Equal(wantTime) || r.ObservedAt.Location() != time.UTC {
		t.Fatalf("upstream created time = %v", r.ObservedAt)
	}
	if _, err := report.Normalize(r, model.MaxReportBytes); err != nil {
		t.Fatal(err)
	}
	if r.Resource == nil || r.Status != nil || r.Resource.CPU == nil || r.Resource.Memory == nil || len(r.Resource.Disks) != 2 {
		t.Fatalf("report shape = %+v", r)
	}
	res := r.Resource
	wantValue(t, "cpu percent", res.CPU.Percent, 12.5)
	wantValue(t, "memory total", res.Memory.TotalBytes, uint64(8589934592))
	wantValue(t, "memory used", res.Memory.UsedBytes, uint64(3221225472))
	wantValue(t, "memory percent", res.Memory.Percent, 37.5)
	root, extra := res.Disks[0], res.Disks[1]
	if root.ID != "root" || extra.ID != storageDisk || root.Path != "" || extra.Path != "" {
		t.Fatalf("disk identities or invented mount paths: %+v", res.Disks)
	}
	wantValue(t, "root total", root.TotalBytes, uint64(107374182400))
	wantValue(t, "root used", root.UsedBytes, uint64(21474836480))
	wantValue(t, "reported dp", root.Percent, 23.25)
	wantValue(t, "efs percent", extra.Percent, 10.0)
	if res.CPU.LogicalCores != nil || res.CPU.SampleWindowMS != nil || res.Memory.AvailableBytes != nil || root.FreeBytes != nil || extra.FreeBytes != nil {
		t.Fatal("upstream-absent fields were fabricated")
	}
}

func TestValidateTargetPolicy(t *testing.T) {
	for _, target := range []string{"https://beszel.example.test", "https://beszel.example.test:8443/prefix", "https://beszel.example.test/prefix/", "http://127.0.0.1:8090", "http://[::1]:8090/prefix"} {
		t.Run("valid/"+target, func(t *testing.T) {
			s := testSource()
			s.Target = target
			if err := (Adapter{}).Validate(s); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, target := range []string{"", "beszel.example.test", "ftp://beszel.example.test", "http://beszel.example.test", "http://localhost:8090", "http://127.0.0.1.evil.test", "https://user:secret@beszel.example.test", "https://beszel.example.test?token=secret", "https://beszel.example.test#secret", "https://beszel.example.test/api", "https://beszel.example.test/hub/api/", "https://beszel.example.test/a/../b", "https://beszel.example.test/a/./b", "https://beszel.example.test/a%2fb", "https://beszel.example.test:0", "https://beszel.example.test:65536"} {
		t.Run("invalid/"+target, func(t *testing.T) {
			s := testSource()
			s.Target = target
			if err := (Adapter{}).Validate(s); err == nil {
				t.Fatal("unsafe or ambiguous target accepted")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("target credential reflected in error")
			}
		})
	}
	for _, mutate := range []func(*model.Source){func(s *model.Source) { s.CredentialEnv = "" }, func(s *model.Source) { s.Target = "http://127.0.0.1"; s.Path = "proxy-route" }, func(s *model.Source) { s.Target = "http://127.0.0.1"; s.ResolveIP = "192.0.2.1" }} {
		s := testSource()
		mutate(&s)
		if err := (Adapter{}).Validate(s); err == nil {
			t.Fatal("missing credential or unsafe cleartext routing accepted")
		}
	}
}

func TestPercentMissingZeroInvalidAndIndependent(t *testing.T) {
	for _, tc := range []struct {
		name, value, state string
		want               *float64
	}{
		{"missing", "", model.FieldMissing, nil},
		{"null", "null", model.FieldMissing, nil},
		{"zero", "0", "", floatPointer(0)},
		{"hundred", "100", "", floatPointer(100)},
		{"fraction", "23.25", "", floatPointer(23.25)},
		{"negative", "-0.01", model.FieldInvalid, nil},
		{"over-range", "100.0001", model.FieldInvalid, nil},
		{"overflow", "1e999", model.FieldInvalid, nil},
		{"quoted", `"42"`, model.FieldInvalid, nil},
		{"boolean", "true", model.FieldInvalid, nil},
		{"array", "[]", model.FieldInvalid, nil},
		{"object", "{}", model.FieldInvalid, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"m":8,"mu":3,"d":100,"du":20`
			if tc.value != "" {
				body += `,"cpu":` + tc.value + `,"mp":` + tc.value + `,"dp":` + tc.value
			}
			body += "}"
			r, fields := decodeFixture(t, body)
			for _, field := range []struct {
				name  string
				value *float64
			}{{"cpu.percent", r.CPU.Percent}, {"memory.percent", r.Memory.Percent}, {"disk.root.percent", r.Disks[0].Percent}} {
				if tc.want != nil {
					wantValue(t, field.name, field.value, *tc.want)
				} else if field.value != nil {
					t.Fatalf("%s fabricated value %v", field.name, *field.value)
				}
				if tc.state != "" {
					wantField(t, fields, field.name, tc.state)
				}
			}
		})
	}
	r, fields := decodeFixture(t, `{"cpu":0,"m":0,"mu":-1,"mp":77.7,"d":0,"du":1e999,"dp":33.3}`)
	wantValue(t, "independent mp", r.Memory.Percent, 77.7)
	wantValue(t, "independent dp", r.Disks[0].Percent, 33.3)
	wantField(t, fields, "memory.total_bytes", model.FieldInvalid)
	wantField(t, fields, "memory.used_bytes", model.FieldInvalid)
	wantField(t, fields, "disk.root.total_bytes", model.FieldInvalid)
	wantField(t, fields, "disk.root.used_bytes", model.FieldInvalid)
}

func floatPointer(v float64) *float64 { return &v }

func TestLegacyGiBRoundingAndBounds(t *testing.T) {
	r, _ := decodeFixture(t, `{"cpu":0,"m":7.99,"mu":3.21,"mp":40.12,"d":0.1,"du":0.01,"dp":9.99}`)
	wantValue(t, "rounded memory total", r.Memory.TotalBytes, uint64(8579197174))
	wantValue(t, "rounded memory used", r.Memory.UsedBytes, uint64(3446711255))
	wantValue(t, "rounded disk total", r.Disks[0].TotalBytes, uint64(107374182))
	wantValue(t, "rounded disk used", r.Disks[0].UsedBytes, uint64(10737418))
	wantValue(t, "reported mp", r.Memory.Percent, 40.12)
	wantValue(t, "reported dp", r.Disks[0].Percent, 9.99)
	for _, tc := range []struct {
		name, value, state string
		want               *uint64
	}{
		{"missing", "", model.FieldMissing, nil}, {"null", "null", model.FieldMissing, nil},
		{"zero", "0", "", uintPointer(0)}, {"negative", "-1", model.FieldInvalid, nil},
		{"overflow", "17179869184", model.FieldInvalid, nil}, {"float-overflow", "1e999", model.FieldInvalid, nil},
		{"quoted", `"1"`, model.FieldInvalid, nil}, {"fraction-rounds-up", "0.0000000005", "", uintPointer(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"m":8,"mp":12,"d":8,"dp":12`
			if tc.value != "" {
				body += `,"mu":` + tc.value + `,"du":` + tc.value
			}
			body += "}"
			r, fields := decodeFixture(t, body)
			for _, field := range []struct {
				name  string
				value *uint64
			}{{"memory.used_bytes", r.Memory.UsedBytes}, {"disk.root.used_bytes", r.Disks[0].UsedBytes}} {
				if tc.want != nil {
					wantValue(t, field.name, field.value, *tc.want)
				} else if field.value != nil {
					t.Fatalf("%s = %d", field.name, *field.value)
				}
				if tc.state != "" {
					wantField(t, fields, field.name, tc.state)
				}
			}
		})
	}
}

func uintPointer(v uint64) *uint64 { return &v }

func TestCapacityMustBePositiveAndZeroUsageRemainsKnown(t *testing.T) {
	for _, tc := range []struct {
		name, total, state string
	}{
		{"zero", "0", model.FieldInvalid},
		{"rounds-to-zero", "0.00000000001", model.FieldInvalid},
		{"missing", "", model.FieldMissing},
		{"null", "null", model.FieldMissing},
		{"overflow", "17179869184", model.FieldInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"cpu":0,"mu":0,"mp":0,"du":0,"dp":0`
			if tc.total != "" {
				body += `,"m":` + tc.total + `,"d":` + tc.total
			}
			body += "}"
			r, fields := decodeFixture(t, body)
			if r.Memory.TotalBytes != nil || r.Disks[0].TotalBytes != nil {
				t.Fatal("invalid or missing capacity became a value")
			}
			wantField(t, fields, "memory.total_bytes", tc.state)
			wantField(t, fields, "disk.root.total_bytes", tc.state)
			wantValue(t, "zero memory usage", r.Memory.UsedBytes, uint64(0))
			wantValue(t, "zero disk usage", r.Disks[0].UsedBytes, uint64(0))
		})
	}
	r, fields := decodeFixture(t, `{"mem":[0,0,0,0],"mp":0}`)
	if r.Memory.TotalBytes != nil {
		t.Fatal("zero byte-array capacity accepted")
	}
	wantField(t, fields, "memory.total_bytes", model.FieldInvalid)
	wantValue(t, "exact zero used bytes", r.Memory.UsedBytes, uint64(0))
	r, fields = decodeFixture(t, `{"mem":[null,null,0,0]}`)
	if r.Memory.TotalBytes != nil || r.Memory.UsedBytes != nil || r.Memory.Percent != nil {
		t.Fatal("null byte-array fields were filled with zero")
	}
	wantField(t, fields, "memory.total_bytes", model.FieldMissing)
	wantField(t, fields, "memory.used_bytes", model.FieldMissing)
}

func TestUsedOverTotalIsInvalidBeforeByteRounding(t *testing.T) {
	for _, pair := range [][2]string{{"1", "2"}, {"1.00000000001", "1.00000000002"}} {
		body := fmt.Sprintf(`{"m":%s,"mu":%s,"mp":99,"d":%s,"du":%s,"dp":98,"efs":{"storage":{"d":%s,"du":%s}}}`, pair[0], pair[1], pair[0], pair[1], pair[0], pair[1])
		r, fields := decodeFixture(t, body)
		if r.Memory.UsedBytes != nil || r.Disks[0].UsedBytes != nil || r.Disks[1].UsedBytes != nil || r.Disks[1].Percent != nil {
			t.Fatalf("used > total survived validation: memory=%+v disks=%+v", r.Memory, r.Disks)
		}
		for _, key := range []string{"memory.used_bytes", "disk.root.used_bytes", "disk." + storageDisk + ".used_bytes", "disk." + storageDisk + ".percent"} {
			wantField(t, fields, key, model.FieldInvalid)
		}
		wantValue(t, "independent mp", r.Memory.Percent, 99.0)
		wantValue(t, "independent dp", r.Disks[0].Percent, 98.0)
	}
}

func TestPreciseMemoryArrayKeepsUint64AndPrecedence(t *testing.T) {
	r, _ := decodeFixture(t, `{"mem":[9007199254740993,9007199254740991,100,50],"m":1,"mu":0,"mp":27.125}`)
	wantValue(t, "exact total > 2^53", r.Memory.TotalBytes, uint64(9007199254740993))
	wantValue(t, "exact used > 2^53", r.Memory.UsedBytes, uint64(9007199254740991))
	wantValue(t, "authoritative mp", r.Memory.Percent, 27.125)
	if r.Memory.AvailableBytes != nil || r.Memory.Basis != "beszel_bytes" {
		t.Fatalf("precise memory semantics = %+v", r.Memory)
	}
	r, _ = decodeFixture(t, `{"mem":[18446744073709551615,18446744073709551614,0,0],"mp":100}`)
	wantValue(t, "uint64 maximum total", r.Memory.TotalBytes, uint64(math.MaxUint64))
	wantValue(t, "uint64 near maximum used", r.Memory.UsedBytes, uint64(math.MaxUint64-1))
	r, _ = decodeFixture(t, `{"mem":[16,4,8,2]}`)
	wantValue(t, "used already excludes ARC", r.Memory.UsedBytes, uint64(4))
	wantValue(t, "missing mp derived", r.Memory.Percent, 25.0)
	r, fields := decodeFixture(t, `{"mem":[16,4,8,2],"mp":null}`)
	if r.Memory.Percent != nil {
		t.Fatal("explicit null mp was replaced")
	}
	wantField(t, fields, "memory.percent", model.FieldMissing)
	for _, mem := range []string{"null", "[]", "[16,4]", "[16,4,0,0,0]", "{}", `"[16,4,0,0]"`, "[16,4,-1,0]", "[16,4,0,18446744073709551616]"} {
		t.Run("invalid-or-null/"+mem, func(t *testing.T) {
			r, fields := decodeFixture(t, `{"mem":`+mem+`,"m":8,"mu":3,"mp":37.5}`)
			if r.Memory.TotalBytes != nil || r.Memory.UsedBytes != nil {
				t.Fatalf("present mem fell back to legacy: %+v", r.Memory)
			}
			state := model.FieldInvalid
			if mem == "null" {
				state = model.FieldMissing
			}
			wantField(t, fields, "memory.total_bytes", state)
			wantField(t, fields, "memory.used_bytes", state)
			wantValue(t, "independent mp", r.Memory.Percent, 37.5)
		})
	}
	for _, mem := range []string{"[16,17,0,0]", "[16,18446744073709551616,0,0]", "[16,4.5,0,0]", "[16,4e0,0,0]", `[16,"4",0,0]`} {
		r, fields := decodeFixture(t, `{"mem":`+mem+`}`)
		if r.Memory.UsedBytes != nil {
			t.Fatalf("invalid exact uint64 accepted from %s", mem)
		}
		wantField(t, fields, "memory.used_bytes", model.FieldInvalid)
	}
}

func TestFilesystemIdentityIsStableAndNeverReflectsNames(t *testing.T) {
	const secret = "secret-token-should-never-escape<script>"
	a := `{"cpu":1,"efs":{"zeta":{"d":1,"du":0},"` + secret + `":{"d":4,"du":1},"alpha":{"d":2,"du":1}}}`
	b := `{"efs":{"alpha":{"du":1,"d":2},"` + secret + `":{"du":1,"d":4},"zeta":{"du":0,"d":1}},"cpu":1}`
	x, xf := decodeFixture(t, a)
	y, yf := decodeFixture(t, b)
	if !reflect.DeepEqual(x, y) || !reflect.DeepEqual(xf, yf) {
		t.Fatal("JSON member order changed disk identity/order")
	}
	if len(x.Disks) != 3 {
		t.Fatalf("efs-only record fabricated a root disk: %+v", x.Disks)
	}
	want := []string{"fs-8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8", "fs-2f0d146717c2cdffca46498def453691abb7590de170b00aab52839f7b290d1f", "fs-5cc10d9143b2cff082cf5fb373073b13d02d12c9a4d24a97d822d701404fb421"}
	for i, d := range x.Disks {
		if d.ID != want[i] || d.Path != "" {
			t.Fatalf("disk %d = %+v", i, d)
		}
	}
	raw, err := json.Marshal(struct {
		Resource *model.ResourceReport
		Fields   map[string]string
	}{x, xf})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "<script>", "alpha", "zeta"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("raw filesystem name reflected: %q", forbidden)
		}
	}
	// A missing, null or empty extra-filesystem object supplies no disks.
	for _, body := range []string{`{"cpu":0}`, `{"cpu":0,"efs":null}`, `{"cpu":0,"efs":{}}`} {
		r, _ := decodeFixture(t, body)
		if len(r.Disks) != 0 {
			t.Fatalf("absent disk became zero-valued root: %+v", r.Disks)
		}
	}
}

func TestMalformedStatsAndCollectionBoundsFailClosed(t *testing.T) {
	for _, body := range []string{"", "null", "[]", "true", `{}`, `{"unrecognized":1}`, `{"cpu":0,"efs":[]}`, `{"cpu":0,"efs":{"":{}}}`, `{"cpu":0,"efs":{"storage":null}}`, `{"cpu":0,"efs":{"storage":[]}}`, `{"cpu":1,"cpu":2}`, `{"mem":[16,4,0,0],"mem":[16,8,0,0]}`} {
		t.Run(body, func(t *testing.T) {
			if _, _, err := decodeStats(json.RawMessage(body)); err == nil {
				t.Fatal("malformed or ambiguous stats accepted")
			}
		})
	}
	for _, count := range []int{model.MaxReportDisks + 1, model.MaxReportFields} {
		efs := make(map[string]any, count)
		for i := 0; i < count; i++ {
			efs[fmt.Sprintf("disk-%03d", i)] = map[string]any{"d": 1, "du": 0}
		}
		body, err := json.Marshal(map[string]any{"cpu": 1, "efs": efs})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := decodeStats(body); err == nil {
			t.Fatalf("%d filesystems bypassed disk/field budgets", count)
		}
	}
	oversizedName, _ := json.Marshal(map[string]any{"cpu": 1, "efs": map[string]any{strings.Repeat("x", 1025): map[string]any{"d": 1, "du": 0}}})
	if _, _, err := decodeStats(oversizedName); err == nil {
		t.Fatal("oversized filesystem key accepted")
	}
}

func TestResourcePageIdentityMetadataAndTimeAreStrict(t *testing.T) {
	for _, created := range []string{testCreated, "2026-10-21T12:34:56.789Z", "2026-10-21T14:34:56.789+02:00"} {
		readings, err := collectResponse(t, resourceBody(`{"cpu":0}`, created, testRecord), model.ReportTypeResource)
		if err != nil || len(readings) != 1 {
			t.Fatalf("valid created %s: %v", created, err)
		}
		want := time.Date(2026, 10, 21, 12, 34, 56, 789000000, time.UTC)
		if !readings[0].Report.ObservedAt.Equal(want) {
			t.Fatalf("created %s became %v", created, readings[0].Report.ObservedAt)
		}
	}
	base := resourceBody(`{"cpu":0}`, testCreated, testRecord)
	for _, tc := range []struct{ name, old, replacement string }{
		{"wrong-system", testSystem, "othersystem0001"}, {"aggregate-not-raw", `"type":"1m"`, `"type":"10m"`},
		{"missing-type", `"type":"1m",`, ""}, {"invalid-record-id", testRecord, "RECORD_INVALID!"},
		{"missing-record-id", `"id":"validationrec03",`, ""}, {"wrong-page", `"page":1`, `"page":2`},
		{"missing-page", `"page":1,`, ""}, {"wrong-page-size", `"perPage":1`, `"perPage":2`},
		{"invalid-total", `"totalItems":-1`, `"totalItems":-2`}, {"unexpected-counted-pages", `"totalPages":-1`, `"totalPages":1`},
		{"missing-total", `"totalItems":-1,`, ""}, {"fractional-total", `"totalItems":-1`, `"totalItems":-1.5`},
		{"timezone-missing", testCreated, "2026-10-21 12:34:56.789"}, {"invalid-date", testCreated, "2026-02-30T12:34:56Z"},
		{"missing-created", `"created":"2026-10-21 12:34:56.789Z",`, ""}, {"null-stats", `"stats":{"cpu":0}`, `"stats":null`},
		{"duplicate-system", `"system":"validationsys01"`, `"system":"validationsys01","system":"othersystem0001"`},
		{"trailing-json", "}]}", "}]} {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(strings.Replace(string(base), tc.old, tc.replacement, 1))
			if bytes.Equal(body, base) {
				t.Fatal("test mutation did not modify fixture")
			}
			got, err := collectResponse(t, body, model.ReportTypeResource)
			if err == nil || len(got) != 0 {
				t.Fatalf("invalid page admitted: readings=%+v err=%v", got, err)
			}
		})
	}
	for _, body := range []string{`{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":[]}`, `{"page":1,"perPage":1,"totalItems":-1,"totalPages":-1,"items":null}`} {
		got, err := collectResponse(t, []byte(body), model.ReportTypeResource)
		if strings.HasSuffix(body, `"items":[]}`) {
			if err != nil || len(got) != 0 {
				t.Fatalf("empty/invisible list = %+v, %v", got, err)
			}
		} else if err == nil {
			t.Fatal("null list accepted as a valid empty page")
		}
	}
	var page map[string]any
	if err := json.Unmarshal(base, &page); err != nil {
		t.Fatal(err)
	}
	items := page["items"].([]any)
	page["items"] = append(items, items[0])
	tooMany, _ := json.Marshal(page)
	if got, err := collectResponse(t, tooMany, model.ReportTypeResource); err == nil || len(got) != 0 {
		t.Fatal("multiple items in perPage=1 response accepted")
	}
}

func TestSystemsStatusNeverBorrowsMetadataTimeOrLatency(t *testing.T) {
	for _, tc := range []struct{ upstream, want string }{{"up", "up"}, {"down", "down"}, {"paused", "unknown"}, {"pending", "unknown"}, {"unrecognized", "unknown"}} {
		t.Run(tc.upstream, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"id":%q,"status":%q,"updated":"2026-10-21 12:34:56.789Z"}`, testSystem, tc.upstream))
			f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(body)}}
			got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeStatus}}})
			if err != nil || len(got) != 1 {
				t.Fatalf("status collect = %+v, %v", got, err)
			}
			r := got[0].Report
			if r.Status == nil || r.Status.Status != tc.want || r.Resource != nil || r.TimeBasis != model.TimeBasisUnknown || !r.ObservedAt.IsZero() || r.SampleID != "" || r.Status.UpstreamLatencyMS != nil {
				t.Fatalf("metadata masqueraded as a timed availability measurement: %+v", r)
			}
			if _, err := report.Normalize(r, model.MaxReportBytes); err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(f.gets[0].url)
			if u.Path != "/hub/api/collections/systems/records/"+testSystem || u.Query().Get("fields") != "id,status,updated" {
				t.Fatalf("status endpoint = %s", f.gets[0].url)
			}
		})
	}
	for _, body := range []string{`{"id":"othersystem0001","status":"up"}`, `{"status":"up"}`, `{"id":"validationsys01","status":true}`, `{"id":"validationsys01","status":"up","status":"down"}`} {
		if got, err := collectResponse(t, []byte(body), model.ReportTypeStatus); err == nil || len(got) != 0 {
			t.Fatalf("invalid status record accepted: %s", body)
		}
	}
}

func TestHTTPFailuresAndErrorsCannotReflectSecrets(t *testing.T) {
	const secret = "private-token-should-never-escape"
	for _, statusCode := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(statusCode), func(t *testing.T) {
			f := &fixtureIO{token: testToken, responses: []source.Response{{Status: statusCode, ContentType: "application/json", Body: []byte(`{"message":"` + secret + `"}`)}}}
			got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
			if err == nil || len(got) != 0 {
				t.Fatal("failed HTTP response produced a report")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("upstream body was reflected")
			}
		})
	}
	for _, kind := range []string{model.ReportTypeResource, model.ReportTypeStatus} {
		f := &fixtureIO{token: testToken, responses: []source.Response{{Status: 404, ContentType: "application/json", Body: []byte(`{"message":"` + secret + `"}`)}}}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{kind}}})
		if kind == model.ReportTypeStatus {
			if err != nil || len(got) != 0 {
				t.Fatalf("systems 404 should be absent: %+v %v", got, err)
			}
		} else if err == nil || len(got) != 0 {
			t.Fatal("missing stats collection treated as an empty authorized list")
		}
	}
	for _, f := range []*fixtureIO{{token: testToken, credentialErr: errors.New(secret)}, {token: testToken, getErr: errors.New(secret)}} {
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
		if err == nil || len(got) != 0 || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe IO error propagation (details omitted)")
		}
	}
	// Even a short credential shaped like a valid PocketBase ID cannot be
	// reflected into a sample identifier by an authenticated upstream.
	f := &fixtureIO{token: testRecord, responses: []source.Response{jsonResponse(readFixture(t))}}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
	if err == nil || len(got) != 0 || strings.Contains(err.Error(), testRecord) {
		t.Fatal("credential-shaped sample ID was reflected")
	}
}

func TestContentTypeTokenAndResponseBounds(t *testing.T) {
	for _, ct := range []string{"text/html", "text/plain", "application/xml", "application/json; charset=", "application/json\r\nX-Secret: reflected"} {
		f := &fixtureIO{token: testToken, responses: []source.Response{{Status: 200, ContentType: ct, Body: readFixture(t)}}}
		if got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}}); err == nil || len(got) != 0 {
			t.Fatalf("unsafe content type %q accepted", ct)
		}
	}
	for _, token := range []string{"", "Bearer already-prefixed", "contains space", "line\r\nbreak", strings.Repeat("t", 1025)} {
		f := &fixtureIO{token: token, responses: []source.Response{jsonResponse(readFixture(t))}}
		got, err := (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}})
		if err == nil || len(got) != 0 || len(f.gets) != 0 {
			t.Fatal("unsafe authorization token reached IO")
		}
		if token != "" && strings.Contains(err.Error(), token) {
			t.Fatal("token reflected in refusal")
		}
	}
	s := testSource()
	s.MaxResponseBytes = 128
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t))}}
	if got, err := (Adapter{}).Collect(context.Background(), f, s, []source.Request{{Object: testSystem, Reports: []string{model.ReportTypeResource}}}); err == nil || len(got) != 0 {
		t.Fatal("response exceeding declared budget accepted")
	}
	if len(f.gets) != 0 && f.gets[0].maxBytes != s.MaxResponseBytes {
		t.Fatal("adapter expanded IO response budget")
	}
}

func TestRequestAndPageBudgetsArePreflighted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requests []source.Request
		mutate   func(*model.Source)
		pages    int
	}{
		{"bad-object", []source.Request{{Object: `x" || true`, Reports: []string{"resource"}}}, nil, 0},
		{"unknown-report", []source.Request{{Object: testSystem, Reports: []string{"network"}}}, nil, 0},
		{"empty-report", []source.Request{{Object: testSystem}}, nil, 0},
		{"duplicate-report", []source.Request{{Object: testSystem, Reports: []string{"resource", "resource"}}}, nil, 0},
		{"duplicate-object", []source.Request{{Object: testSystem, Reports: []string{"resource"}}, {Object: testSystem, Reports: []string{"status"}}}, nil, 0},
		{"object-budget", []source.Request{{Object: testSystem, Reports: []string{"resource"}}, {Object: "othersystem0001", Reports: []string{"resource"}}}, func(s *model.Source) { s.MaxObjects = 1 }, 0},
		{"page-budget", []source.Request{{Object: testSystem, Reports: []string{"resource", "status"}}}, func(s *model.Source) { s.MaxPages = 1 }, 0},
		{"consumed-page-budget", []source.Request{{Object: testSystem, Reports: []string{"resource"}}}, nil, 2},
		{"extreme-consumed-pages", []source.Request{{Object: testSystem, Reports: []string{"resource"}}}, nil, int(^uint(0) >> 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSource()
			if tc.mutate != nil {
				tc.mutate(&s)
			}
			f := &fixtureIO{token: testToken, pages: tc.pages}
			got, err := (Adapter{}).Collect(context.Background(), f, s, tc.requests)
			if err == nil || len(got) != 0 || f.credentials != 0 || len(f.gets) != 0 {
				t.Fatalf("preflight did IO or accepted invalid request: credential=%d GET=%d err=%v", f.credentials, len(f.gets), err)
			}
		})
	}
	f := &fixtureIO{token: testToken}
	got, err := (Adapter{}).Collect(context.Background(), f, testSource(), nil)
	if err == nil || len(got) != 0 || f.credentials != 0 || len(f.gets) != 0 {
		t.Fatalf("empty request was admitted or performed IO: %+v %v", got, err)
	}
}

func TestCancellationAndMultiReportFailureAreAtomic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t))}}
	got, err := (Adapter{}).Collect(ctx, f, testSource(), []source.Request{{Object: testSystem, Reports: []string{"resource"}}})
	if err == nil || len(got) != 0 || f.credentials != 0 || len(f.gets) != 0 {
		t.Fatalf("canceled work performed IO: credentials=%d GET=%d err=%v", f.credentials, len(f.gets), err)
	}
	f = &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t)), {Status: 401, ContentType: "application/json", Body: []byte(`{"message":"private"}`)}}}
	got, err = (Adapter{}).Collect(context.Background(), f, testSource(), []source.Request{{Object: testSystem, Reports: []string{"resource", "status"}}})
	if err == nil || len(got) != 0 || len(f.gets) != 2 {
		t.Fatalf("partial source success leaked after later failure: len=%d GET=%d err=%v", len(got), len(f.gets), err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	f = &fixtureIO{token: testToken, responses: []source.Response{jsonResponse(readFixture(t))}, afterGet: cancel}
	got, err = (Adapter{}).Collect(ctx, f, testSource(), []source.Request{{Object: testSystem, Reports: []string{"resource", "status"}}})
	if err == nil || len(got) != 0 || len(f.gets) != 1 {
		t.Fatalf("cancellation between reads was not atomic: len=%d GET=%d err=%v", len(got), len(f.gets), err)
	}
}

func TestJSONComplexityUTF8AndFilesystemKeyBoundaries(t *testing.T) {
	for _, key := range []string{strings.Repeat("x", 128), strings.Repeat("界", 42) + "xx"} {
		body, err := json.Marshal(map[string]any{"cpu": 0, "efs": map[string]any{key: map[string]any{"d": 1, "du": 0}}})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := decodeFixture(t, string(body))
		if len(r.Disks) != 1 || len(r.Disks[0].ID) != 67 {
			t.Fatalf("valid 128-byte key lost stable identity: %+v", r.Disks)
		}
	}
	tooLong, _ := json.Marshal(map[string]any{"cpu": 0, "efs": map[string]any{strings.Repeat("x", 129): map[string]any{"d": 1, "du": 0}}})
	tooMany := map[string]any{"cpu": 0}
	for i := 0; i < 128; i++ {
		tooMany[fmt.Sprintf("ignored-%03d", i)] = 0
	}
	tooManyMembers, _ := json.Marshal(tooMany)
	tooManyItems, _ := json.Marshal(map[string]any{"cpu": 0, "ignored": make([]int, 4097)})
	containers := make([]any, 256)
	for i := range containers {
		containers[i] = []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}
	}
	tooManyContainers, _ := json.Marshal(map[string]any{"cpu": 0, "ignored": containers})
	deep := `{"cpu":0,"ignored":` + strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34) + "}"
	badUTF8 := append([]byte(`{"cpu":0,"efs":{"`), 0xff)
	badUTF8 = append(badUTF8, []byte(`":{"d":1,"du":0}}}`)...)
	for name, body := range map[string][]byte{"129-byte-key": tooLong, "members": tooManyMembers, "array": tooManyItems, "containers": tooManyContainers, "depth": []byte(deep), "invalid-utf8": badUTF8, "oversized-ignored-string": []byte(`{"cpu":0,"ignored":"` + strings.Repeat("s", 4097) + `"}`), "duplicate-escaped-key": []byte(`{"cpu":1,"\u0063pu":2}`)} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeStats(body); err == nil {
				t.Fatal("structural/identity budget bypass accepted")
			}
		})
	}
}

func FuzzBeszelResourceDecode(f *testing.F) {
	f.Add([]byte(`{"b":[0,4096],"swap":[9007199254740993,0],"la":[0,1,2]}`))
	f.Add([]byte(`{"cpu":0,"b":[0,-1],"swap":[0,1],"la":[0,-1,null]}`))
	for _, seed := range []string{`{"cpu":12.5,"m":8,"mu":3,"mp":37.5,"d":100,"du":20,"dp":23.25}`, `{"cpu":0,"mem":[9007199254740993,9007199254740991,0,0]}`, `{"m":1.00000000001,"mu":1.00000000002,"mp":100}`, `{"efs":{"storage":{"d":1,"du":0}}}`, `{"cpu":null,"mem":null,"dp":null}`, `{"cpu":1,"cpu":2}`, `null`, `[]`, `{}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		r, fields, err := decodeStats(raw)
		if err != nil {
			return
		}
		if r == nil {
			t.Fatal("successful decoder returned nil resource")
		}
		assertContract(t, r, fields)
		if len(r.Disks) > model.MaxReportDisks || len(fields) > model.MaxReportFields {
			t.Fatal("successful decoder exceeded model budgets")
		}
		// Re-decoding the same bytes must not depend on mutable decoder state.
		again, other, err := decodeStats(raw)
		if err != nil || !reflect.DeepEqual(r, again) || !reflect.DeepEqual(fields, other) {
			t.Fatal("decoder is not deterministic")
		}
	})
}

func (*fixtureIO) Close() {}
