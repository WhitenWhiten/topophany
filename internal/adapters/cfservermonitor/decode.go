package cfservermonitor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/adapters/sourceutil"
	"github.com/WhitenWhiten/topophany/internal/model"
)

const maxUnixMillis = 253402300799999 // Last millisecond in RFC 3339 year 9999.

func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	if sourceutil.JSONBudget(body, 128, 4096, 16384) != nil {
		return nil, failure("cfsm_json")
	}
	// Exact map keys keep unknown metadata and differently cased aliases
	// from overriding the response's documented fields.
	var record map[string]json.RawMessage
	if json.Unmarshal(body, &record) != nil || record == nil {
		return nil, failure("cfsm_json")
	}
	return record, nil
}

func resource(body []byte, id string) (*model.ExternalReport, error) {
	record, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	var server string
	if json.Unmarshal(record["id"], &server) != nil || server != id {
		return nil, failure("cfsm_record_identity")
	}
	var millis int64
	if raw := record["last_updated"]; len(raw) != 0 {
		if json.Unmarshal(raw, &millis) != nil || millis < 0 || millis > maxUnixMillis {
			return nil, failure("cfsm_sample_time")
		}
	}
	if millis == 0 {
		// The Worker can return a server before it has any metrics.
		// Its top-level timestamp belongs to the server record.
		return nil, nil
	}
	res, fields := metrics(record)
	if res == nil {
		for _, state := range fields {
			if state == model.FieldInvalid {
				return nil, failure("cfsm_metrics")
			}
		}
		return nil, nil
	}
	sum := sha256.Sum256([]byte(id + "\n" + strconv.FormatInt(millis, 10)))
	return &model.ExternalReport{
		ReportMeta: model.ReportMeta{
			Schema: model.ReportSchema, Object: id, SampleID: "cfsm-" + hex.EncodeToString(sum[:]),
			ObservedAt: time.UnixMilli(millis).UTC(), TimeBasis: model.TimeBasisUpstreamSample, Fields: fields,
		},
		Resource: res,
	}, nil
}

func metrics(record map[string]json.RawMessage) (*model.ResourceReport, map[string]string) {
	fields := map[string]string{
		"cpu.sample_window_ms":   model.FieldUnsupported,
		"memory.available_bytes": model.FieldUnsupported,
	}
	cpu, cpuState := nonnegativeNumber(record["cpu"])
	if cpu != nil && *cpu > 100 {
		cpu, cpuState = nil, model.FieldInvalid
	}
	cores, coresState := logicalCores(record["cpu_cores"])
	fields["cpu.percent"], fields["cpu.logical_cores"] = cpuState, coresState

	memory := model.MemoryReport{Basis: "cfsm_mib"}
	memory.TotalBytes, memory.UsedBytes, memory.Percent = capacity(record["ram_total"], record["ram_used"], "memory.", fields)
	disk := model.DiskReport{ID: "aggregate", Basis: "cfsm_aggregate_mib"}
	if record["disk_total"] != nil || record["disk_used"] != nil {
		disk.TotalBytes, disk.UsedBytes, disk.Percent = capacity(record["disk_total"], record["disk_used"], "disk.aggregate.", fields)
		fields["disk.aggregate.free_bytes"] = model.FieldUnsupported
	}
	extraMetrics := resourceMetrics(record, fields)
	if cpu == nil && cores == nil && memory.TotalBytes == nil && memory.UsedBytes == nil && disk.TotalBytes == nil && disk.UsedBytes == nil && extraMetrics == (model.ResourceMetrics{}) {
		return nil, fields
	}
	res := &model.ResourceReport{ResourceMetrics: extraMetrics}
	if cpu != nil || cores != nil {
		res.CPU = &model.CPUReport{Percent: cpu, LogicalCores: cores, Basis: "cfsm_agent_percent"}
	}
	if memory.TotalBytes != nil || memory.UsedBytes != nil {
		res.Memory = &memory
	}
	if record["disk_total"] != nil || record["disk_used"] != nil {
		res.Disks = []model.DiskReport{disk}
	}
	return res, fields
}

func resourceMetrics(record map[string]json.RawMessage, fields map[string]string) model.ResourceMetrics {
	var m model.ResourceMetrics
	m.Network.RXBytesPerSecond, fields["network.rx_bytes_per_second"] = sourceutil.ResourceNumber(metricJSONNumber(record["net_in_speed"]))
	m.Network.TXBytesPerSecond, fields["network.tx_bytes_per_second"] = sourceutil.ResourceNumber(metricJSONNumber(record["net_out_speed"]))
	totalRaw, usedRaw := metricJSONNumber(record["swap_total"]), metricJSONNumber(record["swap_used"])
	m.Swap.TotalBytes, fields["swap.total_bytes"] = mibBytes(totalRaw)
	m.Swap.UsedBytes, fields["swap.used_bytes"] = mibBytes(usedRaw)
	// Compare before rounding too, so sub-byte overruns cannot become valid.
	total, _ := nonnegativeNumber(totalRaw)
	used, _ := nonnegativeNumber(usedRaw)
	if total != nil && used != nil && *used > *total {
		m.Swap.UsedBytes, fields["swap.used_bytes"] = nil, model.FieldInvalid
	}
	loadState := model.FieldMissing
	raw := bytes.TrimSpace(record["load_avg"])
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		loadState = model.FieldInvalid
		var load string
		if json.Unmarshal(raw, &load) == nil {
			m.Load = model.ParseLoadAverages(strings.Fields(load))
		}
	}
	for _, field := range []struct {
		key   string
		value *float64
	}{
		{"load.avg_1m", m.Load.Avg1m}, {"load.avg_5m", m.Load.Avg5m}, {"load.avg_15m", m.Load.Avg15m},
	} {
		state := loadState
		if field.value != nil {
			state = model.FieldKnown
		}
		fields[field.key] = state
	}
	return m
}

// Some Worker deployments return the decimal strings sent by the Agent.
func metricJSONNumber(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			number := json.RawMessage(value)
			if _, state := sourceutil.ResourceNumber(number); state == model.FieldKnown {
				return number
			}
		}
	}
	return raw
}

func logicalCores(raw json.RawMessage) (*int, string) {
	var cores int
	if len(raw) == 0 {
		return nil, model.FieldMissing
	}
	if json.Unmarshal(raw, &cores) != nil || cores < 0 || cores > 65536 {
		return nil, model.FieldInvalid
	}
	if cores == 0 {
		return nil, model.FieldMissing
	}
	return &cores, model.FieldKnown
}

func nonnegativeNumber(raw json.RawMessage) (*float64, string) {
	return sourceutil.ResourceNumber(raw)
}

func mibBytes(raw json.RawMessage) (*uint64, string) {
	value, state := nonnegativeNumber(raw)
	if value == nil {
		return nil, state
	}
	// The Agent divides bytes by 1024² before reporting the fields labelled
	// MB in the API. The Worker stores them as JSON numbers.
	scaled := math.Round(*value * (1 << 20))
	if scaled >= 0x1p64 {
		return nil, model.FieldInvalid
	}
	bytes := uint64(scaled)
	return &bytes, model.FieldKnown
}

func capacity(totalRaw, usedRaw json.RawMessage, prefix string, fields map[string]string) (total, used *uint64, percent *float64) {
	total, totalState := mibBytes(totalRaw)
	used, usedState := mibBytes(usedRaw)
	// The Worker defaults absent counters to zero. An empty capacity has
	// no usable denominator and cannot establish a zero usage reading.
	if total != nil && *total == 0 {
		total, totalState = nil, model.FieldMissing
	}
	if used != nil {
		switch {
		case total == nil && *used == 0:
			used, usedState = nil, model.FieldMissing
		case total != nil && *used > *total:
			used, usedState = nil, model.FieldInvalid
		}
	}
	percentState := model.FieldMissing
	switch {
	case total != nil && used != nil:
		value := float64(*used) / float64(*total) * 100
		percent, percentState = &value, model.FieldKnown
	case totalState == model.FieldInvalid || usedState == model.FieldInvalid:
		percentState = model.FieldInvalid
	}
	fields[prefix+"total_bytes"] = totalState
	fields[prefix+"used_bytes"] = usedState
	fields[prefix+"percent"] = percentState
	return total, used, percent
}
