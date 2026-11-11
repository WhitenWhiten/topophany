# Komari external source adapter

`komari` reads CPU, memory and aggregate disk samples through the same
external-source pipeline as Beszel. It is explicitly registered in the
production binary; the runner, outbox, collector, store, resource selection
and monitor require no vendor-specific changes or new wire schema.

## Supported protocol

The supported server schema is **Komari 1.6.0**, commit
[`1b76b7ad5bc3de6529bb7f0577069b4ac241fec3`](https://github.com/komari-monitor/komari/tree/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3).
The adapter makes only this read-only request, once per bound node:

```text
GET <base>/api/recent/<client-uuid>
Accept: application/json
Authorization: Bearer <api-key>  # only when credential_env is configured
```

The response is `{"status":"success","message":"","data":[Report,...]}`.
Evidence for this mapping is in the pinned server's
[`Report` types](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/protocol/v2/jsonrpc.go),
[`recent` handler](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/rpc/jsonrpc/public.go),
[`router`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/router/router.go),
[`ingestReport`](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/api/client/ingest.go)
and [one-minute runtime cache](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/agent/connections.go).
The endpoint reads existing in-memory reports; it does not create tasks,
start a terminal, contact agents, authenticate by login, or change records.

The official [compatibility timeline](https://komari-document.pages.dev/dev/compatibility)
lists `/api/recent/:uuid` as a future compatibility-cleanup item without a
removal version. Support is pinned to the tested schema, not promised for
all future releases. A removed endpoint (HTTP 404), an error envelope or a
changed response shape fails collection explicitly. There is no fallback
to a different API, WebSocket, or an agent endpoint.

## Configure and run

Start with [`examples/komari.json`](../../../examples/komari.json).

1. Set `target` to the Komari server base URL, optionally with a simple
   reverse-proxy prefix, such as `https://monitor.example.com/komari`.
   Do not append `/api`, a node UUID, credentials, query or fragment.
2. Set `external.object` to the exact lowercase canonical client UUID
   (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`), not its name or Agent token.
3. For authenticated access, set the worker environment named by
   `credential_env` to a raw administrator API key. The example uses
   `TOPO_KOMARI_API_KEY`; the adapter adds the `Bearer ` prefix.
   Omit `credential_env` to read public nodes anonymously. A configured
   missing or malformed credential is a local error; the adapter never
   retries without it. Komari can treat an unrecognized key as a guest for
   public nodes on a public site, so that read cannot validate the key. Verify
   authenticated access with a hidden node. The API key has administrator
   authority upstream even though this
   adapter only reads the endpoint above; keep it in the worker environment.
4. Use `external.primary_disk: "aggregate"`. This is the aggregate disk
   capacity reported by the agent, not the root filesystem or a mount path.
5. Review the source cadence, freshness windows, budgets and assertions.
   The recent endpoint retains only approximately one minute of reports;
   long polling intervals can miss intermittent reporting bursts.

With the environment variable set, or omitted from the config for public nodes:

```sh
go build -trimpath -o ./bin/topo ./cmd/topo
./bin/topo validate --config examples/komari.json
./bin/topo once --config examples/komari.json --observer komari-reader
```

`validate` checks the topology schema. Runtime commands additionally validate
the registered adapter and perform the actual reads. Run `once` to check
the URL, access and UUID before recurring collection. For collector-free TUI,
status, show and monitor reads, [direct mode](../../../docs/DIRECT.md#configure-komari)
reuses this resource adapter. It supports API-key, environment and anonymous
authentication; it does not use Beszel's PocketBase login/refresh workflow.

Direct discovery uses the pinned server's read-only
`GET /api/admin/client/list` for API keys (raw client array) or `GET /api/nodes`
for anonymous reads (success envelope). The former is protected by the
router's administrator-role middleware and the
[`adminListClients` handler](https://github.com/komari-monitor/komari/blob/1b76b7ad5bc3de6529bb7f0577069b4ac241fec3/web/rpc/jsonrpc/admin.client.go)
only reads existing client records. It verifies a key even with zero nodes;
`/api/me` does not prove API-key identity in this version. No discovery call
creates records, tasks, terminals or agent connections. Only validated UUIDs
and sanitized names leave the adapter. Discovery rejects duplicate IDs,
malformed envelopes and lists over 1000 nodes; transport applies a 1 MiB body
limit. The collector resource adapter still requests only `/api/recent/<uuid>`.

Only `"report":"resource"` is supported. Komari's separate online-presence
state is exposed by WebSocket/RPC, not by this endpoint. A `status` binding
is refused before credentials or network access; neither a populated nor
an empty recent array is interpreted as `up` or `down`. Keep ordinary probes
for application availability. Network rates, swap and load are optional
resource fields from the same recent report.

## Sample identity and freshness

The adapter verifies each array element's UUID and timestamp, finds the
greatest `updated_at`, and maps that sample regardless of response ordering.
It does not list all nodes or read historical pages. The server assigns
the UUID and receive time during report ingestion; `observed_at` preserves
that timestamp in UTC with `time_basis: upstream_sample`. This is the
server's receive time, not an independently measured agent timestamp.

`sample_id` is `komari-` plus the SHA-256 digest of the configured UUID,
a newline and the normalized UTC RFC3339Nano timestamp. Equivalent timezone
or fractional spellings identify the same instant. Re-fetching a cached
sample keeps the same identity and data deadline. A new receive time is a
new sample even if its values did not change. Conflicting normalized reports
at the newest timestamp fail the collection rather than choosing an array
position; equal normalized duplicates are accepted. Unknown labels and
unmapped fields do not change the normalized sample.

An empty `data` array produces no report. It can mean no recent sample or
an unknown node; it cannot confirm health or an assignment. Hidden nodes
without authenticated access produce an upstream error in the pinned
version, not an empty resource report. Existing evidence ages normally.
Authentication, endpoint,
parse and transport failures are source-attempt diagnostics, never target
outages or new resource evidence.

## Resource mapping

| Komari field | Normalized field | Rule |
| --- | --- | --- |
| `cpu.usage` | `cpu.percent` | Finite 0..100 percent. In a present CPU object, omitted `usage` means zero because the pinned server uses `omitempty`. |
| `cpu.cores` | `cpu.logical_cores` | Optional integer from 1 through the shared report limit of 65536. |
| `ram.total`, `ram.used` | `memory.total_bytes`, `memory.used_bytes` | Exact nonnegative signed-int64 wire integers converted to uint64; total must be positive. |
| `ram.used / ram.total` | `memory.percent` | Derived only when both byte counters are valid. |
| `disk.total`, `disk.used` | `disk.aggregate.total_bytes`, `disk.aggregate.used_bytes` | Same exact-byte validation; no mount path is invented. |
| `disk.used / disk.total` | `disk.aggregate.percent` | Derived only from a valid pair. |
| `network.down`, `network.up` | `network.rx_bytes_per_second`, `network.tx_bytes_per_second` | Agent-reported bytes per second. |
| `swap.total`, `swap.used` | `swap.total_bytes`, `swap.used_bytes` | Exact byte counts. Zero capacity and zero usage are valid readings. |
| `load.load1`, `load.load5`, `load.load15` | `load.avg_1m`, `load.avg_5m`, `load.avg_15m` | Host load averages. |

Missing/null CPU objects and explicit `usage:null` remain missing, distinct
from the documented omitted-zero encoding `cpu:{}`. Missing/null numeric
fields stay nil. Negative values, negative zero, fractions in byte/cores
fields, overflow, invalid types and out-of-range values are marked invalid.
Used bytes above total invalidate usage and the derived percentage without
discarding the independent total. No clamping or missing-to-zero conversion
is applied to memory or disk. Available memory, disk free space and the CPU
measurement window are unsupported; subtraction is not proof of those
metrics' operating-system semantics.

The [official agent's disk collector](https://github.com/komari-monitor/komari-agent/blob/9e532e0429cd049571e35cf344654181879b33c7/monitoring/unit/disk.go)
sums selected filesystem capacities and usage. Its selection can be
configured upstream; Topophany preserves the received aggregate.

Integers above 2^53 remain exact **as received from the server**. The pinned
server's Agent v2 ingestion can round an original agent integer through an
intermediate floating-point representation before it reaches this endpoint.
The adapter cannot reconstruct lost upstream precision. The official-binary
harness records this difference explicitly; synthetic tests separately
verify exact preservation of odd wire integers above 2^53 through the
collector and monitor.

## Transport, security and budgets

Collector sources require HTTPS except for direct literal loopback HTTP,
without a proxy path or non-loopback resolution override. Direct profiles can
explicitly opt into non-loopback HTTP using `source.allow_insecure:true`; see
[direct HTTP setup](../../../docs/DIRECT.md#http-on-a-lan-or-tailscale-network).
The shared IO owns explicit TLS,
CA, server name, bind address, resolution and proxy routing. Redirects,
ambient proxies, compression and direct fallback are disabled. Credentials
travel only in the header, never a query. Upstream strings such as names,
messages, device labels and error bodies are not exposed in reports or
diagnostics. Sample IDs are generated from the configured object and parsed
time, not copied from credential-bearing text. The final serialized report
is also checked for the configured API key, including its JSON-escaped form.
A key shaped like a valid timestamp or byte count cannot be reflected through
an otherwise valid metric; a match fails the whole collection with a fixed
diagnostic. The URL, credential, response and budget safety helpers are shared
with Beszel in `sourceutil`; the Komari endpoint, Bearer protocol, resource-only
support, sample identity and metric mapping remain in this package.

One unique bound UUID consumes one page; duplicate bindings share the read.
All object, report-type, page and response settings are checked before credentials or
network access. At most 20 UUIDs fit the common source page limit. Use
separately budgeted sources for more nodes. A later failure returns no
successful prefix from a collection. Cancellation is honored before and
after reads, and partial batches are never published.

The per-response maximum is 8 MiB, with the configured lower limit applied
by IO. Structural bounds reject duplicate keys, invalid UTF-8, unpaired
Unicode escapes, depth over 32, more than 128 members per object, 4096 entries
per array, 16384 containers, strings over 4096 bytes, keys over 128 bytes and
more than 1024 recent records. Every record's identity/time is checked;
resource fields are decoded only for the newest candidates. Normalization
and total result limits remain enforced by the shared runtime.

## Verification

```sh
go test -count=1 ./internal/adapters/komari ./internal/probe ./internal/integration
go test -race -count=1 ./internal/adapters/komari ./internal/probe ./internal/integration
go test ./internal/adapters/komari -run='^$' \
  -fuzz='^FuzzKomariResourceDecode$' -fuzztime=25000x -parallel=2
go test ./internal/adapters/komari -run='^$' \
  -bench='BenchmarkKomariRuntimeCollect' -benchmem
python3 scripts/verify.py
```

Tests exercise independent numeric expectations, endpoint/authentication
contracts, unordered samples, duplicate/conflicting/late samples, invalid
fields, Unicode/JSON budgets, cancellation, atomic multi-node failure,
redaction and exact public wire integers. The integration suite uses real
loopback HTTP through the production runner/outbox/API/store/view/monitor,
including checkpoint recovery and resource expiry.

The opt-in official-binary acceptance harness is reproducible without a
user's monitoring data. Download the official
[`1.6.0` Linux amd64 binary](https://github.com/komari-monitor/komari/releases/download/1.6.0/komari-linux-amd64),
then run:

```sh
make build
python3 scripts/accept_komari.py --topo bin/topo \
  --binary /absolute/path/to/komari-linux-amd64 \
  --output .verify-build/komari-official.json
```

The harness verifies the official binary's pinned SHA-256, starts an isolated
loopback server, creates synthetic nodes and submits Agent v2 samples only
to that disposable instance. It checks the actual recent endpoint and the
compiled `topo once` pipeline, then removes temporary state and credentials.
It also exercises compiled direct inspect/save/read/status/show/remove commands,
administrator-key verification without guest fallback, anonymous public-node
discovery and aggregate disk resources without a collector.
This validates the released server protocol; it is distinct from deployment
testing against a user's production server and actual monitoring agents.
