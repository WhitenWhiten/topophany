# External source adapters and upgrades

This guide describes the external-source contract and how to add an adapter.
The production registry ships three host resource adapters.

- `beszel` reads Beszel Hub resources and optional reported system status.
  [Guide](../internal/adapters/beszel/README.md) · [Example](../examples/beszel.json)
- `komari` reads recent CPU, memory and aggregate disk samples from Komari
  Monitor. [Guide](../internal/adapters/komari/README.md) · [Example](../examples/komari.json)
- `cf-server-monitor` reads CPU, memory and aggregate disk from a
  CF-Server-Monitor Worker. [Guide](../internal/adapters/cfservermonitor/README.md) · [Example](../examples/cf-server-monitor.json)

Unknown adapter names are rejected before collection. Synthetic adapters
remain test-only.

Read [MULTI-PROBE-IMPLEMENTATION.md](MULTI-PROBE-IMPLEMENTATION.md) for the
frozen contract and delivery record. The scenario-level behavior is mapped in
[SCENARIOS.md](SCENARIOS.md). This guide is the short version an adapter author
needs.

## 1. Layers and ownership

```text
reviewed topology
  Source      one external instance, one observer, transport/TLS/credentials,
              interval, timeout, budgets
    Binding   an `external` check: stable upstream object, target node/service,
              report type, max age, primary disk, resource assertions
      Report  normalized resource or status evidence with its own identity,
              upstream time, time basis and field availability
```

The identities are deliberately separate:

- the executing observer / worker;
- the API target (the collector is not the upstream);
- the resource subject (the node or service in the topology);
- the stable upstream object ID (`external.object`).

An upstream response cannot create topology entities or choose a binding. The
runner only asks for the enabled bindings' object IDs, and the runtime discards
objects that are not wanted.

## 2. Authoring an adapter

An adapter is a package under `internal/adapters/<name>`. It implements the
small static interface in `internal/source`:

```go
type Adapter interface {
    Validate(model.Source) error
    Collect(context.Context, source.IO, model.Source, []source.Request) ([]source.Reading, error)
}
```

`Collect` receives:

- a bounded `source.IO` that can read only the configured origin over the
  source's explicit route/TLS policy, with no ambient proxy, redirects,
  compression or unbounded bodies;
- the reviewed source;
- the bounded stable object IDs and report types the runner needs, grouped
  into one `source.Request` per object.

The adapter owns everything vendor-specific:

1. endpoint path construction and version negotiation;
2. authentication header construction from `IO.Credential` (the credential
   value is read from the named environment variable at collection time);
3. pagination within `source.MaxPages` and the byte budget;
4. versioned response decoding;
5. stable object-ID mapping;
6. sample identity (`sample_id`, or epoch/sequence), upstream time and time
   basis;
7. field mapping into `ResourceReport` / `StatusReport`, setting
   `FieldMissing`, `FieldUnsupported` or `FieldInvalid` instead of inventing a
   zero.

Do not import `config`, `probe`, `runner`, `api`, `store`, `engine`, `view` or
the UI from an adapter package. Tests must use synthetic endpoints and
credentials.

### Static wiring (the only production registration)

Append the adapter to the explicit map in
`internal/probe/source.go:ProductionSourceAdapters`:

```go
func ProductionSourceAdapters() map[string]source.Adapter {
    return map[string]source.Adapter{
        "your-adapter": yourpackage.Adapter{},
    }
}
```

There is no `init` registration, dynamic loading, URL-based adapter selection or
fallback. A source's `adapter` string must match an entry exactly.

Add focused tests:

- pure config/endpoint validation;
- parsing fixtures for each supported upstream version;
- pagination, page/object/response/report budget failures;
- first sample, second sample, counter reset and series isolation if the
  adapter derives rates;
- time-basis fixtures, including a cached dashboard value that is `unknown`;
- partial resource fields and a missing primary disk;
- no secret in errors, observations, contracts or public topology;
- a local listener end-to-end run through the real runner/outbox/API chain.

Do not regenerate existing frozen fixtures. Add a new named row/case for the
new adapter and map semantic regressions in `docs/SCENARIOS.md`.

## 3. Config shape

A complete new-style topology is in
[`examples/multi-probe.template.json`](../examples/multi-probe.template.json).
It is schema-valid but intentionally names the placeholder `example-api`,
which is not in the production registry. The production examples above use
registered adapters. Validate the generic template's schema with:

```sh
topo validate --config examples/multi-probe.template.json
```

Minimal source and bindings:

```json
{
  "schema": 1,
  "project": "multi-probe-example",
  "nodes": [
    {"id": "collector", "kind": "host"},
    {"id": "vm-1", "kind": "host", "resource_checks": ["vm-1-resources"]}
  ],
  "endpoints": [],
  "services": [{"id": "app", "name": "Application", "node": "vm-1"}],
  "observers": [
    {"id": "cloud", "name": "Cloud observer", "node": "collector"}
  ],
  "paths": [],
  "sources": [{
    "id": "probe-api",
    "adapter": "example-api",
    "observer": "cloud",
    "target": "https://probe.example.com/api/objects",
    "credential_env": "TOPO_PROBE_TOKEN",
    "interval_seconds": 30,
    "timeout_seconds": 8,
    "stale_seconds": 90,
    "max_response_bytes": 1048576,
    "max_pages": 2,
    "max_objects": 1000,
    "max_report_bytes": 65536
  }],
  "checks": [
    {
      "id": "vm-1-resources",
      "observer": "cloud",
      "kind": "external",
      "external": {
        "source": "probe-api",
        "object": "vm/vm-1",
        "report": "resource",
        "node": "vm-1",
        "max_age_seconds": 120,
        "primary_disk": "root",
        "assertions": {"cpu_percent_max": 90, "memory_percent_max": 90, "disk_percent_max": 85}
      }
    },
    {
      "id": "vm-1-app-status",
      "observer": "cloud",
      "kind": "external",
      "service": "app",
      "external": {
        "source": "probe-api",
        "object": "service/app",
        "report": "status",
        "node": "vm-1",
        "max_age_seconds": 60
      }
    }
  ]
}
```

Rules enforced by config validation:

- the source observer equals every bound check's observer;
- `interval_seconds`, `timeout_seconds` and `stale_seconds` resolve from the
  source; an explicit conflicting check value is an error;
- external checks cannot carry endpoint/target/path/link/dial/TLS/HTTP/DNS/host
  or adapter option blocks;
- a status binding cannot declare `primary_disk` or assertions;
- a resource binding's service, if present, belongs to its target node;
- multiple resource providers for a node need an ordered
  `node.resource_checks` list, primary first;
- a disk assertion requires `primary_disk`.

The old host config remains valid and keeps its contract:

```json
{
  "schema": 1,
  "project": "host-monitoring",
  "nodes": [{"id": "local-host", "kind": "host"}],
  "endpoints": [],
  "services": [],
  "observers": [{"id": "local", "node": "local-host"}],
  "paths": [],
  "checks": [{
    "id": "host-resources",
    "observer": "local",
    "kind": "host",
    "interval_seconds": 60,
    "timeout_seconds": 8,
    "host": {"disk_path": "/", "max_cpu_percent": 90}
  }]
}
```

No `sources` field is required. Existing host facts, thresholds, contracts and
card behavior are unchanged.

## 4. Time and sample identity

Report identity preference is:

1. `sample_id`;
2. `epoch` + `sequence`;
3. `observed_at` with a known time basis.

The adapter must set a time basis honestly:

- `upstream_sample`: the upstream's own sample time;
- `live_read`: the adapter's versioned contract confirms the read itself is a
  live point, so `observed_at` is the read time;
- `unknown`: no reliable time. The report is retained as a diagnostic only;
  it cannot refresh a deadline, count a status sample, confirm a failure or
  recovery, or populate latency statistics.

Never relabel a dashboard fetch time as an upstream sample time. If the value
is cached and the upstream does not provide its sample time, use `unknown`.

The collector's rules:

- same identity + same normalized content: duplicate;
- same identity + different content: conflict;
- older identity: late;
- different sample at exactly the same upstream timestamp: conflict unless a
  sequence distinguishes it;
- newer identity: new, even when every value is unchanged.

Duplicates and late/conflict reports do not advance deadlines, confirmation
runs, report samples or latency samples. They may update source contact and
diagnostic counters. Late historical values are not backfilled into rollups in
this phase.

## 5. Evidence boundaries

- A successful read is source contact, not proof that the service or node is
  healthy.
- A status report is `reported` evidence. It does not enter measured endpoint,
  path, link, node, service or incident aggregation in this phase; it appears in
  `snapshot.external` and the external check state.
- A resource report never creates service availability. External observations
  are excluded from native rollups, carries and probe-latency histograms.
- Source authentication, transport, pagination, parsing and normalization
  failures become collection-attempt diagnostics. They never become target
  failures or recoveries.
- The collector's `data_deadline` is upstream sample time + `max_age`; its
  `contact_deadline` is the newest successful read + source `stale_seconds`;
  `snapshot.external[].valid_until` is the minimum of those two and the
  snapshot deadline.

## 6. Resource reports

Units are bytes, milliseconds and 0..100 percent. All numeric members are
nullable pointers; a missing value is not zero.

- CPU: percent, logical cores, sample window ms, basis.
- Memory: total/used/available bytes, percent, basis.
- Disks: bounded list of stable disk IDs, optional display path,
  total/used/free bytes, percent, basis.

The primary disk is the binding's `primary_disk`. If it is absent from the
report, disk values are unknown; another disk is never selected automatically.
A `disk_percent_max` assertion requires that disk.

Resource assertions:

- a known value above a declared maximum is a policy violation;
- if there is no violation but a required assertion field is missing, the
  result is unknown;
- missing never passes.

Field availability belongs to the report (`field_status`), and whole-report
staleness is separate. A partial but current report can have current CPU and
unknown memory/disk; it does not trigger a fallback source.

## 7. Resource selection and cards

`view.SelectResources` is the single selector for TUI, CLI and monitor. It
returns the selected report, source, object, valid-until/deadlines and reason.

- `node.resource_checks` is ordered primary then fallbacks.
- A fallback is used only when the primary is absent or expired, not because a
  field is missing.
- A source's primary disk is explicit; no cross-source stitching or averaging.
- With no explicit list and only native host checks, the old behavior is
  preserved and all host readings can be shown.
- With multiple sources and no explicit order, config is refused.
- `server.card.resources[0]` is the selected report. Existing CPU, memory and
  disk JSON names and `valid_until` are unchanged; source/object/time-basis/
  selection metadata is additive and ignored by older bar decoders.
- `resource.cpu.used_percent`, `resource.memory.used_percent` and
  `resource.disk.used_percent` select by node through the same choice.
  `host.*` remains native host-check scoped.

## 8. Budgets

The defaults and ceilings are described in the implementation contract. The
runtime enforces:

- one in-flight source read per source, at most four source workers;
- source timeout, interval jitter and bounded exponential error backoff;
- per-page response bytes, pages and objects;
- encoded report size, disk count and field-status count;
- 100-observation / 512 KiB fan-out batches;
- a 4 MiB persisted external-state budget and the store's overall state budget.

Exceeding a budget is an explicit attempt error, never a truncated success.

## 9. Upgrade order

The wire and persistence changes are deliberately explicit:

1. Back up the collector data directory and every worker outbox directory.
   Keep the topology file under version control.
2. Upgrade collectors first. A current collector accepts schema 2 native
   batches and schema 3 external batches; it rejects schema 4 and later.
3. Upgrade workers next. Old schema 2 workers continue to deliver native
   samples. Once a worker can emit external reports it uses schema 3.
4. Upgrade TUI/bar clients. A current client reads schema 2 native snapshots and
   schema 3 snapshots. An old client refuses schema 3 explicitly; it does not
   silently show a partial external snapshot.
5. Deploy a binary that includes the configured adapter and its reviewed config.
   The production registry includes `beszel`; unknown names fail at startup
   with an unwired-adapter error.
6. Publish/monitor only after delivery shows accepted reports and source contact.

Rollback:

- Before any external report state exists, checkpoints remain schema 1 and an
  older binary can read the store.
- Once external state exists, the checkpoint is schema 2 with an `external`
  member. An older binary refuses it. Do not downgrade in place.
- Roll back by stopping writers, restoring the pre-upgrade collector data
  directory and worker outboxes from backup, restoring the matching topology,
  and starting the older binary. Keep worker outboxes: they may contain valid
  native samples and their receipts are part of delivery.
- The schema 2 checkpoint contains source contact and report cursors. Restoring
  only the journal replays valid transactions but loses the checkpoint's
  bounded state; use the paired backup for a clean rollback.

## 10. Run entry points

Collector/worker commands remain available:

```sh
topo serve --config topology.json --data ./collector-data ...
topo worker --config topology.json --observer cloud --data ./worker-data ...
topo once --config topology.json --observer cloud
topo monitor stream  # bounded NDJSON requests on stdin
```

In collector mode the source read is performed by the worker or collector.
The explicit `direct` read mode runs the same resource adapters inside each
viewer/monitor process, using bounded memory and private credentials. It
needs no collector, does not mix collector data, and has no history database.
See [Direct setup](DIRECT.md) and the [D1 contract](contracts/direct-v1/contract.md).

The integration suite exercises production and test-only adapters through
the real runner, outbox, HTTP API, store, engine, view and monitor stream:

```sh
go test ./internal/integration -count=1
```
