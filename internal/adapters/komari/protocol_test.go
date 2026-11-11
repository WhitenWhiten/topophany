package komari

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
)

func TestKomariCPUZeroMissingInvalidAndCores(t *testing.T) {
	// The pinned official CPUReport uses omitempty only for usage and cores.
	// An existing cpu:{} means usage=0; an absent/null object is not evidence.
	for _, tc := range []struct {
		name, raw, state string
		want             *float64
	}{
		{"omitted-object", "", model.FieldMissing, nil},
		{"null-object", "null", model.FieldMissing, nil},
		{"omitted-usage", "{}", "", new(0.0)},
		{"null-usage", `{"usage":null}`, model.FieldMissing, nil},
		{"explicit-zero", `{"usage":0}`, "", new(0.0)},
		{"one-hundred", `{"usage":100}`, "", new(100.0)},
		{"fraction", `{"usage":12.5}`, "", new(12.5)},
		{"negative", `{"usage":-1}`, model.FieldInvalid, nil},
		{"negative-zero", `{"usage":-0}`, model.FieldInvalid, nil},
		{"negative-underflow", `{"usage":-1e-999}`, model.FieldInvalid, nil},
		{"over-100", `{"usage":100.0001}`, model.FieldInvalid, nil},
		{"overflow", `{"usage":1e999}`, model.FieldInvalid, nil},
		{"string", `{"usage":"42"}`, model.FieldInvalid, nil},
		{"boolean", `{"usage":true}`, model.FieldInvalid, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := `"ram":{"total":8,"used":3},"disk":{"total":100,"used":25}`
			if tc.raw != "" {
				fields += `,"cpu":` + tc.raw
			}
			r := requireResource(t, resourceBody(fields))
			var got *float64
			if r.Resource.CPU != nil {
				got = r.Resource.CPU.Percent
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("unavailable cpu invented %v", *got)
				}
				wantField(t, r.Fields, "cpu.percent", tc.state)
			} else {
				wantValue(t, "cpu", got, *tc.want)
			}
			wantValue(t, "independent memory", r.Resource.Memory.Percent, 37.5)
		})
	}
	for _, tc := range []struct {
		raw, state string
		want       int
	}{
		{"", model.FieldMissing, 0}, {"null", model.FieldMissing, 0}, {"1", "", 1}, {"65536", "", 65536},
		{"0", model.FieldInvalid, 0}, {"-0", model.FieldInvalid, 0}, {"-1", model.FieldInvalid, 0}, {"65537", model.FieldInvalid, 0}, {"1.5", model.FieldInvalid, 0}, {`"4"`, model.FieldInvalid, 0}, {"9223372036854775808", model.FieldInvalid, 0},
	} {
		t.Run("cores/"+tc.raw, func(t *testing.T) {
			cpu := `"cpu":{"usage":12.5`
			if tc.raw != "" {
				cpu += `,"cores":` + tc.raw
			}
			r := requireResource(t, resourceBody(cpu+`}`))
			if tc.want == 0 {
				if r.Resource.CPU.LogicalCores != nil {
					t.Fatal("invalid cores admitted")
				}
				wantField(t, r.Fields, "cpu.logical_cores", tc.state)
			} else {
				wantValue(t, "cores", r.Resource.CPU.LogicalCores, tc.want)
			}
		})
	}
}

func TestKomariExactByteCountersAndDerivedPercentages(t *testing.T) {
	// This integer cannot be represented exactly by float64. The expected
	// counters are literals, independently asserted before checking ratios.
	r := requireResource(t, resourceBody(`"cpu":{"usage":0},"ram":{"total":9007199254740993,"used":4503599627370496},"disk":{"total":9223372036854775807,"used":9007199254740993}`))
	wantValue(t, "memory >2^53", r.Resource.Memory.TotalBytes, uint64(9007199254740993))
	wantValue(t, "memory used", r.Resource.Memory.UsedBytes, uint64(4503599627370496))
	wantValue(t, "disk int64 limit", r.Resource.Disks[0].TotalBytes, uint64(9223372036854775807))
	wantValue(t, "disk >2^53", r.Resource.Disks[0].UsedBytes, uint64(9007199254740993))
	if r.Resource.Memory.Percent == nil || math.Abs(*r.Resource.Memory.Percent-50) > 1e-12 {
		t.Fatalf("memory ratio = %v", r.Resource.Memory.Percent)
	}
	encoded, err := json.Marshal(r)
	if err != nil || !bytes.Contains(encoded, []byte(`9007199254740993`)) || !bytes.Contains(encoded, []byte(`9223372036854775807`)) {
		t.Fatal("report JSON lost exact byte counters")
	}

	for _, metric := range []string{"ram", "disk"} {
		for _, tc := range []struct {
			name, total, used, totalState, usedState, percentState string
			totalValue, usedValue                                  uint64
			hasTotal, hasUsed                                      bool
		}{
			{"valid", "8", "3", "", "", "", 8, 3, true, true},
			{"used-zero", "8", "0", "", "", "", 8, 0, true, true},
			{"missing-total", "", "3", model.FieldMissing, "", model.FieldMissing, 0, 3, false, true},
			{"null-total", "null", "3", model.FieldMissing, "", model.FieldMissing, 0, 3, false, true},
			{"missing-used", "8", "", "", model.FieldMissing, model.FieldMissing, 8, 0, true, false},
			{"null-used", "8", "null", "", model.FieldMissing, model.FieldMissing, 8, 0, true, false},
			{"zero-capacity", "0", "0", model.FieldInvalid, "", model.FieldInvalid, 0, 0, false, true},
			{"used-over-capacity", "8", "9", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"negative-total", "-1", "3", model.FieldInvalid, "", model.FieldInvalid, 0, 3, false, true},
			{"negative-used", "8", "-1", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"negative-zero", "8", "-0", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"fractional-used", "8", "0.5", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"exponent-used", "8", "1e0", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"quoted-used", "8", `"3"`, "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"boolean-used", "8", "false", "", model.FieldInvalid, model.FieldInvalid, 8, 0, true, false},
			{"int64-overflow", "9223372036854775808", "3", model.FieldInvalid, "", model.FieldInvalid, 0, 3, false, true},
		} {
			t.Run(metric+"/"+tc.name, func(t *testing.T) {
				var members []string
				if tc.total != "" {
					members = append(members, `"total":`+tc.total)
				}
				if tc.used != "" {
					members = append(members, `"used":`+tc.used)
				}
				r := requireResource(t, resourceBody(`"cpu":{"usage":12.5},"`+metric+`":{`+strings.Join(members, ",")+`}`))
				prefix := "memory."
				var total, used *uint64
				var percent *float64
				if metric == "ram" {
					total, used, percent = r.Resource.Memory.TotalBytes, r.Resource.Memory.UsedBytes, r.Resource.Memory.Percent
				} else {
					if len(r.Resource.Disks) != 1 {
						t.Fatal("disk missing")
					}
					d := r.Resource.Disks[0]
					total, used, percent = d.TotalBytes, d.UsedBytes, d.Percent
					prefix = "disk.aggregate."
				}
				if tc.hasTotal {
					wantValue(t, "total", total, tc.totalValue)
				} else {
					if total != nil {
						t.Fatal("invalid total became known")
					}
					wantField(t, r.Fields, prefix+"total_bytes", tc.totalState)
				}
				if tc.hasUsed {
					wantValue(t, "used", used, tc.usedValue)
				} else {
					if used != nil {
						t.Fatal("invalid used became known")
					}
					wantField(t, r.Fields, prefix+"used_bytes", tc.usedState)
				}
				if tc.percentState == "" {
					wantValue(t, "ratio", percent, float64(tc.usedValue)/float64(tc.totalValue)*100)
				} else {
					if percent != nil {
						t.Fatal("missing/invalid capacity supplied a known ratio")
					}
					wantField(t, r.Fields, prefix+"percent", tc.percentState)
				}
				wantValue(t, "independent CPU", r.Resource.CPU.Percent, 12.5)
			})
		}
	}
}

func TestKomariLatestSampleIdentityAndConflictRules(t *testing.T) {
	old := sample(testObject, "2026-10-22T12:34:55Z", `"cpu":{"usage":99}`)
	latest := sample(testObject, testUpdated, `"cpu":{"usage":12.5}`)
	want := requireResource(t, recent(latest))
	for _, records := range [][]string{{old, latest}, {latest, old}, {latest, old, latest}, {latest, sample(testObject, "2026-10-22T20:34:56.123456789+08:00", `"cpu":{"usage":12.5,"name":"ignored"}`)}} {
		got := requireResource(t, recent(records...))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("array order or equivalent duplicate changed latest report: %+v", got)
		}
	}
	conflict := sample(testObject, testUpdated, `"cpu":{"usage":13}`)
	for _, records := range [][]string{{latest, conflict}, {conflict, latest}} {
		got, err := resource(recent(records...), testObject)
		if source.Code(err) != "komari_sample_conflict" || got != nil {
			t.Fatalf("same-time conflicting newest samples selected arbitrarily: %+v, %v", got, err)
		}
	}
	newer := requireResource(t, recent(sample(testObject, "2026-10-22T12:34:57.123456789Z", `"cpu":{"usage":12.5}`)))
	if newer.SampleID == want.SampleID || !newer.ObservedAt.After(want.ObservedAt) {
		t.Fatal("same-value newer sample lost its new identity")
	}
	changed := requireResource(t, recent(conflict))
	if changed.SampleID != want.SampleID {
		t.Fatal("same-time altered payload evaded downstream conflict detection with a new identity")
	}
	// Every record belongs to the configured object, including older rows.
	for _, records := range [][]string{{sample(otherObject, "2026-10-22T12:34:55Z", `"cpu":{"usage":0}`), latest}, {latest, sample(otherObject, testUpdated, `"cpu":{"usage":0}`)}} {
		if got, err := resource(recent(records...), testObject); err == nil || got != nil {
			t.Fatal("foreign object was silently admitted or ignored")
		}
	}
}

func TestKomariSampleTimeStrictnessAndNormalization(t *testing.T) {
	want := time.Date(2026, 10, 22, 12, 34, 56, 123456789, time.UTC)
	for _, at := range []string{testUpdated, "2026-10-22T20:34:56.123456789+08:00", "2026-10-22T09:04:56.123456789-03:30"} {
		r := requireResource(t, recent(sample(testObject, at, `"cpu":{"usage":0}`)))
		if !r.ObservedAt.Equal(want) || r.ObservedAt.Location() != time.UTC || r.SampleID != testSampleID {
			t.Fatalf("equivalent offset/precision changed identity: %+v", r.ReportMeta)
		}
	}
	for _, at := range []string{"2026-10-22T12:34:56Z", "2026-10-22T12:34:56+00:00"} {
		r := requireResource(t, recent(sample(testObject, at, `"cpu":{"usage":0}`)))
		if !r.ObservedAt.Equal(want.Truncate(time.Second)) {
			t.Fatalf("whole-second timestamp changed: %v", r.ObservedAt)
		}
	}
	for _, at := range []string{"", "0001-01-01T00:00:00Z", "0000-01-01T00:00:00Z", "2026-10-22 12:34:56Z", "2026-10-22T2:34:56Z", "2026-10-22T12:34:56,123Z", "2026-10-22T12:34:56.1234567891Z", "2026-10-22T12:34:56+24:00", "2026-10-22T12:34:56+00:60", "2026-10-22T12:34:56", "2026-02-30T12:34:56Z", "0001-01-01T00:30:00+01:00", "9999-12-31T23:30:00-01:00"} {
		t.Run(at, func(t *testing.T) {
			if got, err := resource(recent(sample(testObject, at, `"cpu":{"usage":0}`)), testObject); err == nil || got != nil {
				t.Fatal("invalid upstream time became usable evidence")
			}
		})
	}
}

func TestKomariRejectsMalformedEnvelopeAndForeignSchemas(t *testing.T) {
	for name, body := range map[string][]byte{
		"null-envelope": []byte(`null`), "array-envelope": []byte(`[]`), "missing-status": []byte(`{"data":[]}`), "error-envelope": []byte(`{"status":"error","message":"upstream-secret","data":[]}`), "missing-data": []byte(`{"status":"success"}`), "null-data": []byte(`{"status":"success","data":null}`), "object-data": []byte(`{"status":"success","data":{}}`), "null-record": recent(`null`), "missing-uuid": recent(fmt.Sprintf(`{"updated_at":%q,"cpu":{}}`, testUpdated)), "foreign-uuid": recent(sample(otherObject, testUpdated, `"cpu":{}`)), "missing-time": recent(fmt.Sprintf(`{"uuid":%q,"cpu":{}}`, testObject)), "foreign-metrics": resourceBody(`"processor":12.5,"memory":42`), "array-cpu": resourceBody(`"cpu":[],"ram":{"total":8,"used":3}`), "scalar-ram": resourceBody(`"cpu":{},"ram":42`), "array-disk": resourceBody(`"cpu":{},"disk":[]`),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resource(body, testObject)
			if err == nil || got != nil {
				t.Fatalf("malformed data became a report: %+v, %v", got, err)
			}
			if strings.Contains(err.Error(), "upstream-secret") {
				t.Fatal("upstream message escaped error")
			}
		})
	}
	if got, err := resource(recent(), testObject); err != nil || got != nil {
		t.Fatalf("empty official array is not absence: %+v, %v", got, err)
	}
}

func TestKomariCaseAliasesCannotOverrideCanonicalProtocolFields(t *testing.T) {
	base := resourceBody(`"cpu":{"usage":12.5},"ram":{"total":8,"used":3},"disk":{"total":100,"used":20}`)
	want := requireResource(t, base)
	for _, tc := range []struct{ old, replacement string }{
		{`"status":"success"`, `"status":"success","STATUS":"error"`},
		{`"uuid":"` + testObject + `"`, `"uuid":"` + testObject + `","UUID":"` + otherObject + `"`},
		{`"updated_at":"` + testUpdated + `"`, `"updated_at":"` + testUpdated + `","UPDATED_AT":"2027-01-01T00:00:00Z"`},
		{`"cpu":{"usage":12.5}`, `"cpu":{"usage":12.5,"USAGE":99},"CPU":{"usage":99}`},
		{`"total":8`, `"total":8,"TOTAL":8192`},
	} {
		body := []byte(strings.Replace(string(base), tc.old, tc.replacement, 1))
		if bytes.Equal(body, base) {
			t.Fatal("test did not mutate response")
		}
		got, err := resource(body, testObject)
		if err != nil {
			t.Fatalf("unknown case alias rejected an otherwise valid canonical report: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("case alias overwrote canonical metrics or sample identity")
		}
	}
	for _, body := range []string{
		strings.Replace(string(base), `"uuid":"`+testObject+`"`, `"UUID":"`+testObject+`"`, 1),
		strings.Replace(string(base), `"uuid":"`+testObject+`"`, `"uuid":"`+otherObject+`","UUID":"`+testObject+`"`, 1),
		strings.Replace(string(base), `"status":"success"`, `"STATUS":"success"`, 1),
		strings.Replace(string(base), `"data":`, `"DATA":`, 1),
	} {
		if got, err := resource([]byte(body), testObject); err == nil || got != nil {
			t.Fatal("noncanonical metadata repaired by case alias")
		}
	}
}

func TestKomariJSONRepresentationAndRecentWindowBudgets(t *testing.T) {
	base := resourceBody(`"cpu":{"usage":12.5}`)
	for name, body := range map[string][]byte{
		"duplicate-key":         resourceBody(`"cpu":{"usage":12.5,"usage":99}`),
		"duplicate-escaped-key": resourceBody(`"cpu":{"usage":12.5,"\u0075sage":99}`),
		"trailing-value":        append(append([]byte{}, base...), []byte(` {}`)...),
		"invalid-utf8":          resourceBody("\"cpu\":{},\"message\":\"" + string([]byte{0xff}) + "\""),
		"deep-container":        resourceBody(`"cpu":{},"extension":` + strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34)),
		"long-string":           resourceBody(`"cpu":{},"message":"` + strings.Repeat("x", 4097) + `"`),
		"long-key":              resourceBody(`"cpu":{},"` + strings.Repeat("x", 129) + `":0`),
		"large-array":           resourceBody(`"cpu":{},"extension":[` + strings.Repeat("0,", 4096) + `0]`),
		"many-containers":       resourceBody(`"cpu":{},"extension":[` + strings.Repeat(`[`+strings.Repeat(`{},`, 127)+`{}],`, 128) + `[]]`),
		"many-members": resourceBody(`"cpu":{},"extension":{` + func() string {
			var a []string
			for i := 0; i < 129; i++ {
				a = append(a, fmt.Sprintf(`"k%d":0`, i))
			}
			return strings.Join(a, ",")
		}() + `}`),
		"too-many-reports": recent(strings.Split(strings.Repeat(sample(testObject, testUpdated, `"cpu":{}`)+"\n", 1025), "\n")[:1025]...),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := resource(body, testObject); err == nil || got != nil {
				t.Fatal("malformed or over-budget representation was admitted")
			}
		})
	}
	for _, escape := range []string{`\ud800`, `\udc00`, `\ud800x`, `\ud800\u0041`, `\udc00\ud800`} {
		if got, err := resource(resourceBody(`"cpu":{},"message":"`+escape+`"`), testObject); err == nil || got != nil {
			t.Fatal("unpaired UTF-16 surrogate was repaired instead of refused")
		}
	}
	for _, text := range []string{`\ud83d\ude00`, `😀`, `\\ud800`, `\ufffd`, `�`} {
		requireResource(t, resourceBody(`"cpu":{},"message":"`+text+`"`))
	}
	// A populated valid recent window must not be mistaken for a fast early
	// parser rejection by benchmark or fuzz coverage.
	records := make([]string, 1024)
	for i := range records {
		records[i] = sample(testObject, testUpdated, `"cpu":{"usage":12.5}`)
	}
	requireResource(t, recent(records...))
}

func FuzzKomariResourceDecode(f *testing.F) {
	f.Add(resourceBody(`"network":{"up":0,"down":4096},"swap":{"total":9007199254740993,"used":0},"load":{"load1":0,"load5":1,"load15":2}`))
	f.Add(resourceBody(`"cpu":{},"network":{"down":-1},"swap":{"total":0,"used":1},"load":{"load1":null}`))
	f.Add(readFixture(f))
	f.Add(resourceBody(`"cpu":{},"ram":{"total":9007199254740993,"used":0},"disk":{"total":100,"used":20}`))
	f.Add(resourceBody(`"cpu":{"usage":null},"ram":{"total":0,"used":-0}`))
	f.Add(recent())
	f.Add([]byte(`{"status":"success","data":null}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 8<<20 {
			t.Skip()
		}
		r, err := resource(body, testObject)
		if err != nil {
			if r != nil {
				t.Fatal("failure returned partial report")
			}
			return
		}
		if r == nil {
			return
		}
		if r.Object != testObject || r.TimeBasis != model.TimeBasisUpstreamSample || r.ObservedAt.IsZero() || r.SampleID == "" || r.Status != nil {
			t.Fatalf("accepted report lost identity/time boundary: %+v", r.ReportMeta)
		}
		if _, err := report.Normalize(*r, model.MaxReportBytes); err != nil {
			t.Fatalf("decoder accepted inadmissible report: %v", err)
		}
		again, err := resource(body, testObject)
		if err != nil || !reflect.DeepEqual(r, again) {
			t.Fatal("deterministic upstream payload changed across decodes")
		}
	})
}
