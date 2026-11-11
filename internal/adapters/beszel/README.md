# Beszel external source adapter

`beszel` reads CPU, memory and filesystem samples from a Beszel Hub through
the existing external-source pipeline. The production registry includes it;
no custom build tags, dynamic plugins or changes to the generic resource
model are required. The worker reads the Hub, and the existing runner,
outbox, collector, store, resource selection and monitor consume the reports.

The adapter performs read-only `GET` requests. It does not log in, refresh a
token, create records, contact agents or infer application reachability from
Hub telemetry.

## Supported protocol

The released schema is pinned to Beszel **v0.21.0**, commit
[`ec96c134660c998f7eb044e349e3715b65733fe1`](https://github.com/henrygd/beszel/tree/ec96c134660c998f7eb044e349e3715b65733fe1).
The four-integer `mem` representation is additionally covered from upstream
commit
[`b97abf7662a7c7a2dd7542ea2062405abd22d4dc`](https://github.com/henrygd/beszel/tree/b97abf7662a7c7a2dd7542ea2062405abd22d4dc).
This forward-schema support is distinct from validation against a released
Hub binary. Beszel's [REST API guide](https://beszel.dev/guide/rest-api)
explicitly allows API changes in minor releases, so a structurally different
future response is not automatically supported.

Protocol evidence is in the upstream
[`system.Stats` definitions](https://github.com/henrygd/beszel/blob/ec96c134660c998f7eb044e349e3715b65733fe1/internal/entities/system/system.go),
[`agent/system.go`](https://github.com/henrygd/beszel/blob/ec96c134660c998f7eb044e349e3715b65733fe1/agent/system.go),
[`agent/disk.go`](https://github.com/henrygd/beszel/blob/ec96c134660c998f7eb044e349e3715b65733fe1/agent/disk.go),
and the pinned Hub's collection migrations and record creation code. The
test fixtures use synthetic values and credentials; they contain no user's
monitoring data.

## Configure and run

Start with [`examples/beszel.json`](../../../examples/beszel.json).

1. Set the source's `target` to the Hub's base URL, such as
   `https://beszel.example.com` or `https://monitor.example.com/beszel`.
   Do not include `/api`, an individual system, a query or credentials.
2. Set `external.object` to the exact **15-character lowercase alphanumeric
   `systems.id`**, not the display name, hostname, agent key or URL.
3. Supply a PocketBase authentication token in the worker environment named
   by `credential_env`. The example uses `TOPO_BESZEL_TOKEN`.
4. Set `external.primary_disk` to `root` to select the agent's primary
   filesystem. The identifier does not imply that its mount point is `/`.
5. Review collection budgets, freshness windows and resource thresholds.

The worker sends the token as `Authorization: <token>`, without adding a
`Bearer` prefix. Use a dedicated ordinary Beszel user with read access to the
assigned systems. A superuser token is unnecessary. Obtain and rotate the
token through your existing Beszel/PocketBase authentication workflow;
automatic token refresh is outside this read-only adapter. The explicit
[direct mode](../../../docs/DIRECT.md) supplies a separate controlled
authentication module and a process-local session while reusing this GET-only
adapter. It does not require a collector or keep resource history. Do not paste a
token into topology JSON or commit it to the repository. See the official
[PocketBase authentication documentation](https://pocketbase.io/docs/authentication/).

With the environment variable already set:

```sh
go build -trimpath -o ./bin/topo ./cmd/topo
./bin/topo validate --config examples/beszel.json
./bin/topo once --config examples/beszel.json --observer beszel-reader
```

`validate` validates the topology schema and references. `once`, `worker` and
local collection in `serve` also validate the registered source adapter and
perform its runtime reads. Run `once` to verify the actual URL, token, system
ID and data mapping before enabling recurring collection.

For an optional status binding, add another check with the same source and
object and `"report": "status"`. It uses one additional request per unique
system. Resource and status bindings for the same object coexist; bindings
that request the same object and report type share a single read.

## Reads and sample identity

For a resource report the adapter requests:

```text
GET <base>/api/collections/system_stats/records
filter=system = "<systems.id>" && type = "1m"
sort=-created,-id
page=1
perPage=1
skipTotal=true
fields=id,system,type,created,stats
```

The query is URL-encoded by `net/url`. Only the latest raw `1m` record is
selected; a newer `10m` or other aggregate cannot replace it. Record ID is
the deterministic tie-breaker when creation timestamps match. The adapter
checks the returned pagination metadata, record ID, system ID, record type
and timestamp rather than trusting the filter alone. It does not walk
historical pages or list every system in the Hub. `skipTotal=true` avoids
counting the matching history on every poll. Both totals must be `-1`, as
specified by the [PocketBase Records API](https://pocketbase.io/docs/api-records/);
the bounded `items` list determines whether a sample is visible. This also
avoids depending on a count query that runs separately from the item query.

`sample_id` is the `system_stats` record ID. `observed_at` is its `created`
timestamp, normalized to UTC, with `time_basis: upstream_sample`. This is
the time the Hub saved the raw sample; Beszel does not supply an independent
precise agent measurement timestamp here. Polling the same record does not
create a new resource sample or extend its evidence deadline. Source contact
and resource freshness remain separate.

For status, the only extra endpoint is:

```text
GET <base>/api/collections/systems/records/<systems.id>?fields=id,status,updated
```

`up` and `down` are preserved; paused, pending and unrecognized statuses are
`unknown`. The report has `time_basis: unknown` and no invented sample time.
`systems.updated` can change when the system is renamed; it is not proof of
a new health sample. Consequently, reported status cannot establish fresh
end-to-end availability. Keep an application probe for that purpose.

## Resource mapping

| Beszel field | Normalized field | Rule |
| --- | --- | --- |
| `cpu` | `cpu.percent` | Finite value from 0 through 100; zero is a real value. |
| `m`, `mu` | `memory.total_bytes`, `memory.used_bytes` | Legacy GiB values multiplied by 2^30 and rounded to the nearest byte, with range checks. |
| `mp` | `memory.percent` | Preserve the independent upstream percentage. |
| `mem[0]`, `mem[1]` | `memory.total_bytes`, `memory.used_bytes` | Decode integers directly as `uint64`; values above 2^53 remain exact. |
| `d`, `du` | `disk.root.total_bytes`, `disk.root.used_bytes` | Legacy GiB conversion with range checks. |
| `dp` | `disk.root.percent` | Preserve the independent upstream percentage, including filesystem reserved-block semantics. |
| `efs[key].d`, `efs[key].du` | Additional disk capacity and usage | Convert GiB values; the percentage uses the original GiB ratio, matching Beszel's extra-filesystem presentation. |
| `b[1]`, `b[0]` | `network.rx_bytes_per_second`, `network.tx_bytes_per_second` | Receive and transmit bytes per second. Legacy `nr` and `ns` are MiB/s and are used when `b` is absent. |
| `s`, `su` or `swap[0]`, `swap[1]` | `swap.total_bytes`, `swap.used_bytes` | Legacy values are rounded GiB. The newer two-integer array takes precedence and preserves byte counts. |
| `la[0..2]` | `load.avg_1m`, `load.avg_5m`, `load.avg_15m` | Host load averages in upstream order. |

Legacy agent capacities are already rounded upstream. Converting them back
to bytes cannot recover the original byte counters. Newer Hubs can also
backfill a `mem` array from legacy agent values; the adapter preserves the
received integers without claiming the original agent had byte precision.

When `mem` is present, it takes precedence over legacy `m`/`mu`, including
when it is null or malformed. Its schema is exactly four unsigned integers
`[total, used, buffer/cache, ZFS ARC]`; the two trailing values are validated
but are not mapped to `available_bytes`. If valid total/used byte counters
exist and `mp` is **absent**, their ratio supplies the memory percentage.
An explicit `mp: null` remains missing. Legacy rounded capacities never
replace a missing `mp` or root `dp`.

Missing numbers remain nil with `field_status: missing`; malformed,
negative (including negative zero or underflow), overflowing or out-of-range
numbers are marked `invalid`. Used
capacity greater than total is invalid, including an overrun that byte
rounding would otherwise hide. The adapter neither clamps values nor
replaces missing values with zero. CPU cores/window, available memory and
free filesystem bytes are unsupported by this mapping.

Additional filesystem IDs are `fs-` plus the full SHA-256 hex digest of the
UTF-8 `efs` map key. Disks are sorted by that original key, so their order and
identity are deterministic. The map key is a device or user-assigned name,
not a reliable mount point. Its raw text is never exposed as a report ID,
path or diagnostic. To select an extra disk, use its emitted hashed ID as
`primary_disk`.

An omitted rate is unavailable, including when the Hub omits an all-zero
`b` array. Legacy `s` with omitted `su` reports zero swap usage. Explicit
null stays unavailable. The adapter reports rates for the Agent's selected
interfaces and leaves out cumulative interface counters.

## Failure, transport and resource boundaries

An empty list means the requested system has no visible raw sample. It
produces no resource report. PocketBase can return HTTP 200 with an empty
list when a user lacks permission, so empty results are not proof of either
a healthy system or a valid assignment. A missing or inaccessible system
record similarly yields no status report. See
[PocketBase API rules](https://pocketbase.io/docs/api-rules-and-filters/).

Authentication failures, rate limiting, transport errors, malformed JSON,
identity mismatches and exhausted budgets fail the source collection. No
successful prefix from that collection is returned if a later request fails.
The shared runtime records a collection-attempt diagnostic; it does not
turn that failure into a target outage or refresh old resource evidence.
Error messages contain fixed codes and explanations, not upstream response
bodies, tokens, filesystem labels or exception strings.

The final mapped resource and status reports also pass the shared `sourceutil`
credential guard. A full credential reflected through a valid timestamp,
integer or JSON-escaped string fails the whole collection with the fixed
`beszel_credential_reflection` code. The existing sample-ID credential-fragment
check remains in place. Numeric/timestamp credential regression fixtures test
the adapter's accepted header boundary; they do not claim PocketBase issues
or accepts such tokens instead of its normal authentication tokens.

Collector sources require HTTPS except for a direct literal loopback HTTP
address. An HTTP loopback source cannot use a proxy path or resolve to a
non-loopback address. Direct profiles can explicitly opt into non-loopback HTTP
using `source.allow_insecure:true`; see
[direct HTTP setup](../../../docs/DIRECT.md#http-on-a-lan-or-tailscale-network). The base URL excludes userinfo, query, fragment, escaped path
components and dot segments. Explicit TLS CA, server name, resolve address,
bind address and supported HTTPS proxy routing remain owned by `source.IO`.
Redirects, ambient proxies, unbounded reads and direct fallback are disabled
by the existing transport.

One unique object/report pair consumes one page. Set `max_pages` high enough
for every enabled pair, and `max_objects` high enough for the distinct
systems. The adapter checks the complete request set before obtaining the
credential or starting network reads. The common maximum is 20 pages per
source collection: at most 20 resource-only systems, or 10 systems requesting
both resource and status. Repeated bindings do not consume extra pages.
Use separately budgeted sources for a larger deployment.

The response limit is per HTTP response. The shared runtime also enforces
the configured normalized report limit and a total result limit. Structural
JSON bounds reject duplicate keys, invalid UTF-8 or unpaired Unicode escapes,
depth over 32, more than 128 members per object, more than 4096 entries per
array, more than 1024 containers, strings over 4096 bytes, and keys longer
than 128 UTF-8 bytes, including additional-filesystem keys. The array budget
accommodates large per-core CPU arrays even though only aggregate CPU is mapped.
Envelope and record fields use exact case-sensitive names; unknown aliases
cannot overwrite canonical data.
Overlong keys are rejected rather than truncated. Reports have at most
64 disks and 64 field-availability entries; heavily incomplete disk data
may reach the field budget before the disk budget. A budget failure is
explicit and never silently drops a subset of disks.

## Verification

```sh
go test -count=1 ./internal/adapters/beszel ./internal/probe ./internal/integration
go test -race -count=1 ./internal/adapters/beszel ./internal/probe ./internal/integration
go test ./internal/adapters/beszel -run='^$' \
  -fuzz='^FuzzBeszelResourceDecode$' -fuzztime=25000x -parallel=2
python3 scripts/verify.py
```

The unit suite checks independent numeric expectations, exact integers,
partial and invalid fields, sample identities and timestamps, strict URL
and request construction, complete collection budgets and secret reflection.
Multi-system tests verify separate bindings and atomic failure across systems.
The integration suite uses the production registry and real loopback HTTP
through the runner/outbox/API/store/view chain, including repeated samples,
source failures and resource freshness. Synthetic API fixtures are separate
from acceptance against an official Hub binary.

The opt-in official-Hub harness is kept in the repository so protocol checks
remain reproducible after the patch archive is discarded. Download the
[official v0.21.0 Linux amd64 archive](https://github.com/henrygd/beszel/releases/download/v0.21.0/beszel_linux_amd64.tar.gz)
and run:

```sh
make build
python3 scripts/accept_beszel.py --topo bin/topo \
  --archive /absolute/path/to/beszel_linux_amd64.tar.gz \
  --output .verify-build/beszel-official.json
```

The harness verifies the pinned archive SHA256
`c0cbab161f322426228419fbbeab4163dded6464a537cc1e2f92e090892f6368`,
starts a disposable loopback Hub, creates synthetic records and a regular
read-only user, then tests API filtering, sorting, `skipTotal`, permissions,
metadata timestamps and the production CLI's normalized reports. It stops
the Hub and removes its private database and credentials before publishing
sanitized results. This requires Linux amd64 and Python 3.11+; it does not
connect to a user's Hub or a real Agent and is not part of the offline suite.
