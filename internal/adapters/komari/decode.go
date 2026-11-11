package komari

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
)

type object map[string]json.RawMessage

// Maps keep protocol names case-sensitive. encoding/json's struct decoder
// would let an unknown "UUID" or "CPU" alias overwrite a canonical member.
func decodeObject(raw []byte) (object, error) {
	var o object
	if json.Unmarshal(raw, &o) != nil || o == nil {
		return nil, failure("komari_json")
	}
	return o, nil
}

func jsonBudget(body []byte) error {
	if sourceutil.JSONBudget(body, maxJSONMembers, maxJSONArray, maxJSONContainers) != nil {
		return failure("komari_json")
	}
	return nil
}

// resource validates every record's identity and time, then maps only the
// latest sample. Recent reports are usually oldest-first; relying on array
// order would either lag by a minute or accept an arbitrary cached sample.
func resource(body []byte, id string) (*model.ExternalReport, error) {
	if !ValidObjectID(id) {
		return nil, failure("komari_record_identity")
	}
	if err := jsonBudget(body); err != nil {
		return nil, err
	}
	envelope, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	var state string
	if json.Unmarshal(envelope["status"], &state) != nil || state != "success" {
		return nil, failure("komari_response")
	}
	var records []json.RawMessage
	if json.Unmarshal(envelope["data"], &records) != nil || records == nil {
		return nil, failure("komari_json")
	}
	if len(records) > maxRecentRecords {
		return nil, failure("komari_record_budget")
	}
	var latest time.Time
	var candidates []object
	for _, raw := range records {
		record, err := decodeObject(raw)
		if err != nil {
			return nil, err
		}
		var uuid, updated string
		if json.Unmarshal(record["uuid"], &uuid) != nil || uuid != id {
			return nil, failure("komari_record_identity")
		}
		if json.Unmarshal(record["updated_at"], &updated) != nil {
			return nil, failure("komari_sample_time")
		}
		at, err := sampleTime(updated)
		if err != nil {
			return nil, err
		}
		if at.After(latest) {
			latest = at
			candidates = candidates[:0]
		}
		if at.Equal(latest) {
			candidates = append(candidates, record)
		}
	}
	if len(candidates) == 0 {
		// The one-minute cache can be empty for an idle, unknown or hidden
		// node. A successful fetch alone supplies no new resource evidence.
		return nil, nil
	}
	values, fields, err := decodeStats(candidates[0])
	if err != nil {
		return nil, err
	}
	// Identity contains no raw upstream label. Equal instants with different
	// RFC3339 offsets/fractional spellings retain the same sample identity.
	sum := sha256.Sum256([]byte(id + "\n" + latest.Format(time.RFC3339Nano)))
	r := &model.ExternalReport{
		ReportMeta: model.ReportMeta{
			Schema: model.ReportSchema, Object: id, SampleID: "komari-" + hex.EncodeToString(sum[:]),
			ObservedAt: latest, TimeBasis: model.TimeBasisUpstreamSample, Fields: fields,
		},
		Resource: values,
	}
	if len(candidates) > 1 {
		// Receive time identifies a sample but supplies no ordering between
		// same-time reports. Tolerate duplicated equivalent latest entries,
		// but never pick a winner between contradictory values.
		canonical, err := json.Marshal(r)
		if err != nil {
			return nil, failure("komari_report")
		}
		for _, candidate := range candidates[1:] {
			v, f, err := decodeStats(candidate)
			if err != nil {
				return nil, err
			}
			other := *r
			other.Resource, other.Fields = v, f
			encoded, err := json.Marshal(other)
			if err != nil {
				return nil, failure("komari_report")
			}
			if !bytes.Equal(canonical, encoded) {
				return nil, failure("komari_sample_conflict")
			}
		}
	}
	return r, nil
}

// Do not accept time.Parse's permissive single-digit hours, comma fractions,
// subnanosecond truncation or out-of-range timezone offsets as sample IDs.
var sampleTimeFormat = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func sampleTime(raw string) (time.Time, error) {
	if len(raw) > 35 || !sampleTimeFormat.MatchString(raw) {
		return time.Time{}, failure("komari_sample_time")
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		at = at.UTC()
		if !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999 {
			return at, nil
		}
	}
	return time.Time{}, failure("komari_sample_time")
}

func missing(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func optionalObject(raw json.RawMessage) (object, error) {
	if missing(raw) {
		return nil, nil
	}
	return decodeObject(raw)
}

func availability(fields map[string]string, key, state string) {
	if state != model.FieldKnown {
		fields[key] = state
	}
}

func percent(raw json.RawMessage) (*float64, string) {
	value, state := sourceutil.ResourceNumber(raw)
	if value != nil && *value > 100 {
		return nil, model.FieldInvalid
	}
	return value, state
}

func byteNumber(raw json.RawMessage, capacity bool) (*uint64, string) {
	if missing(raw) {
		return nil, model.FieldMissing
	}
	// The Komari wire fields are signed int64 bytes. Decode directly, never
	// through float64, and refuse negative zero as well as negative values.
	var value int64
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-")) || json.Unmarshal(raw, &value) != nil || value < 0 || capacity && value == 0 {
		return nil, model.FieldInvalid
	}
	unsigned := uint64(value)
	return &unsigned, model.FieldKnown
}

// capacity maps the same exact-byte total/used schema in ram and disk. A
// percentage is derived only from a valid pair, never from missing counters.
func capacity(o object, fields map[string]string, prefix string) (*uint64, *uint64, *float64) {
	total, totalState := byteNumber(o["total"], true)
	used, usedState := byteNumber(o["used"], false)
	if total != nil && used != nil && *used > *total {
		used, usedState = nil, model.FieldInvalid
	}
	availability(fields, prefix+"total_bytes", totalState)
	availability(fields, prefix+"used_bytes", usedState)
	var pct *float64
	state := model.FieldMissing
	if totalState == model.FieldInvalid || usedState == model.FieldInvalid {
		state = model.FieldInvalid
	} else if total != nil && used != nil {
		v := float64(*used) / float64(*total) * 100
		pct, state = &v, model.FieldKnown
	}
	availability(fields, prefix+"percent", state)
	return total, used, pct
}

func decodeStats(record object) (*model.ResourceReport, map[string]string, error) {
	cpu, err := optionalObject(record["cpu"])
	if err != nil {
		return nil, nil, err
	}
	ram, err := optionalObject(record["ram"])
	if err != nil {
		return nil, nil, err
	}
	disk, err := optionalObject(record["disk"])
	if err != nil {
		return nil, nil, err
	}
	fields := map[string]string{
		"cpu.sample_window_ms":   model.FieldUnsupported,
		"memory.available_bytes": model.FieldUnsupported,
	}
	extraMetrics, err := resourceMetrics(record, fields)
	if err != nil {
		return nil, nil, err
	}
	if cpu == nil && ram == nil && disk == nil && extraMetrics == (model.ResourceMetrics{}) {
		return nil, nil, failure("komari_empty_stats")
	}
	usage, state := percent(cpu["usage"])
	if _, present := cpu["usage"]; cpu != nil && !present {
		// Komari 1.6.0 serializes CPU.Usage with omitempty. Only a present
		// CPU object licenses this default; an absent/null CPU or explicit
		// usage:null is unavailable rather than an invented idle sample.
		zero := float64(0)
		usage, state = &zero, model.FieldKnown
	}
	availability(fields, "cpu.percent", state)
	c := &model.CPUReport{Percent: usage, Basis: "komari_agent_percent"}
	state = model.FieldMissing
	if raw := cpu["cores"]; !missing(raw) {
		var cores int
		if json.Unmarshal(raw, &cores) != nil || cores < 1 || cores > 65536 {
			state = model.FieldInvalid
		} else {
			c.LogicalCores, state = &cores, model.FieldKnown
		}
	}
	availability(fields, "cpu.logical_cores", state)
	m := &model.MemoryReport{Basis: "komari_bytes"}
	m.TotalBytes, m.UsedBytes, m.Percent = capacity(ram, fields, "memory.")
	res := &model.ResourceReport{CPU: c, Memory: m, ResourceMetrics: extraMetrics}
	if disk != nil {
		d := model.DiskReport{ID: "aggregate", Basis: "komari_aggregate_bytes"}
		d.TotalBytes, d.UsedBytes, d.Percent = capacity(disk, fields, "disk.aggregate.")
		fields["disk.aggregate.free_bytes"] = model.FieldUnsupported
		res.Disks = []model.DiskReport{d}
	}
	return res, fields, nil
}

func resourceMetrics(record object, fields map[string]string) (model.ResourceMetrics, error) {
	var m model.ResourceMetrics
	network, err := optionalObject(record["network"])
	if err != nil {
		return m, err
	}
	load, err := optionalObject(record["load"])
	if err != nil {
		return m, err
	}
	swap, err := optionalObject(record["swap"])
	if err != nil {
		return m, err
	}
	// The upstream rates are signed int64 integers, like its byte counters.
	rate := func(raw json.RawMessage, key string) *float64 {
		value, state := byteNumber(raw, false)
		availability(fields, key, state)
		if value == nil {
			return nil
		}
		return new(float64(*value))
	}
	m.Network.RXBytesPerSecond = rate(network["down"], "network.rx_bytes_per_second")
	m.Network.TXBytesPerSecond = rate(network["up"], "network.tx_bytes_per_second")
	var state string
	m.Load.Avg1m, state = sourceutil.ResourceNumber(load["load1"])
	availability(fields, "load.avg_1m", state)
	m.Load.Avg5m, state = sourceutil.ResourceNumber(load["load5"])
	availability(fields, "load.avg_5m", state)
	m.Load.Avg15m, state = sourceutil.ResourceNumber(load["load15"])
	availability(fields, "load.avg_15m", state)
	m.Swap.TotalBytes, state = byteNumber(swap["total"], false)
	availability(fields, "swap.total_bytes", state)
	m.Swap.UsedBytes, state = byteNumber(swap["used"], false)
	if m.Swap.TotalBytes != nil && m.Swap.UsedBytes != nil && *m.Swap.UsedBytes > *m.Swap.TotalBytes {
		m.Swap.UsedBytes, state = nil, model.FieldInvalid
	}
	availability(fields, "swap.used_bytes", state)
	return m, nil
}
