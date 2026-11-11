# Software and platform adapters

Each supported software integration owns a directory here, including its
configuration validation, runtime parsing, tests and compatibility notes.
Edge response-header interpreters also own one directory per platform, with
their own parsing, tests and contribution notes.
Adapters depend on `model`, `secure` and the small shared contract in this
package. They must not import `probe`, `config`, the collector or the UI.

`probe` supplies explicit HTTP transports and bounded command execution. It
owns scheduling, cancellation, elapsed time, the observation envelope and
error classification. Adapters fill software-specific facts and policy
outcomes. Keep runtime reports distinct from measured application responses.

Cloudflare, Vercel, CloudFront, Fastly and Netlify interpret headers from
existing HTTP checks, and Cloudflare also owns the `cloudflare` API check kind
below. Their `HeaderFacts` functions make no network calls
and do not need separate check kinds or configuration validators. Shared
reported-label bounds live in `edgeheaders`; `probe.EdgeFacts` retains the
explicit first-match order: Cloudflare, Vercel, CloudFront, Fastly, Netlify.
The HTTP probe still owns secret-response filtering. Provider branding remains
in `view`, and generic `via` evidence attribution remains in `engine`.

`cloudflare` reads three documented Cloudflare API resources, one allowlisted
`GET` per check: a Tunnel's status, a DNS record's presence/proxied/content
comparison, and a load balancer pool's origin health. The bearer token is named
by `adapter.token_env` and read from the worker's environment; every response
is credential-bearing, so only closed words, numbers, shaped tokens and
declared comparisons leave the adapter. Outside a literal loopback test
address the target must be exactly `https://api.cloudflare.com/client/v4`, and
the kind may use explicit path proxies. See [cloudflare](cloudflare/README.md).

`cloudflared` reads the local Cloudflare Tunnel connector's metrics listener.
Its target must be a literal loopback URL, it sends no credential, and it is
not in the proxy-traversal list; the listener's `/ready` and `/metrics` are
read-only. The connector's own statement is reported evidence about the tunnel
session and never proves that the service behind it answers. See
[cloudflared](cloudflared/README.md).

Supabase currently uses the shared topology model and ordinary HTTP checks.
Add its adapter directory when introducing actual platform-specific behavior.
Future cloud API collection belongs in the relevant platform directory with
explicit typed configuration and separate evidence boundaries.

To add an adapter:

1. Add its package, pure configuration validator and synthetic regression
   fixtures. Keep all software-specific parsing and endpoint restrictions here.
2. Add typed options in `model`; connect them to the versioned configuration,
   cloning, redaction and measurement contract as needed.
3. Wire execution in `probe/adapters.go` and `probe.Run`, configuration validation
   in `config`, and reported-evidence admission in `api`.
4. Add the check kind to the editor and synthetic preview when supported.
5. Document supported versions, evidence limits and a local test command in
   the package README. Map semantic regressions in `docs/SCENARIOS.md`.

Do not broaden the common contract for a capability used by only one adapter.
Do not add implicit registration, dynamic loading or a direct transport fallback.
HTTP GET is not sufficient proof that an API endpoint has no side effects.

`probe.TestAdapterCompatibility` compares serialized observations and measurement
contracts against fixtures captured before the package split, plus one row per
adapter added later (`cloudflared-ok`, `cloudflare-tunnel`, `cloudflare-dns`,
`cloudflare-pool`), each frozen once from its first output and never
regenerated. It ignores only
the ephemeral listener address, sampling timestamp and elapsed time.
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare header facts with an independent,
frozen copy of `599c890:internal/probe/edge.go` in `edge_legacy_test.go`.
Its original source hash is recorded there. The post-split `edge_mitigation` fact (exempted only when `Cf-Mitigated` carries the closed challenge word) and Vercel 1.2 location corrections are explicitly exempted in the comparison harness and covered separately; provider/cache facts and other platforms still match. Keep that oracle independent of
shared production helpers; do not regenerate it to make a refactor pass.

Run `go test ./internal/adapters/... ./internal/probe ./internal/config` for
focused checks. Follow `CONTRIBUTING.md` for the full verification requirements.

## External source adapters

The shared multi-probe infrastructure is described in
[`docs/EXTERNAL-ADAPTERS.md`](../../docs/EXTERNAL-ADAPTERS.md). Each
external source adapter belongs in its own directory here and implements the
small production interface from `internal/source.Adapter`: `Validate` and
`Collect`. It receives a bounded `source.IO` that owns transport, TLS, explicit
route and credential lookup; the adapter owns versions, pagination, object IDs,
time basis and field mapping.

External resource adapters compose the helpers in `sourceutil` for strict base URLs,
explicit credential references, header credential bounds, complete object/page
preflight, JSON representation limits, controlled reads and closed IO errors.
The same report guard rejects raw or JSON-escaped credential reflection after
mapping, including through valid timestamps or integers. It receives an
already resolved credential; anonymous reads never resolve one. Beszel also
retains its stricter record-ID credential-fragment check. These helpers do
not open transports, read environment variables, register adapters or own
vendor HTTP status meanings. Object IDs, endpoints, raw-token versus Bearer
authentication, sample time/identity, supported report types and metric
semantics remain local to each adapter. Protocol-specific JSON limits remain
explicit arguments, and runtime normalization keeps its separate result budget.

`beszel` reads the latest raw one-minute resource record for each configured
Beszel system and, when requested, its reported status. It preserves record
identity, upstream time, independent percentages and exact integer memory
fields; unavailable values remain unavailable. The Hub's status is reported
evidence with unknown sample time. See [beszel](beszel/README.md) for supported
schemas, authentication, field mapping and budgets.

`komari` reads each configured node's latest recent CPU, memory and aggregate
disk report from Komari 1.6.0. It preserves the server's receive time, handles
omitted zero CPU values and exact wire byte counters, and supports optional
Bearer API keys or anonymous public reads. The GET endpoint supplies no
online status; a status binding is explicitly refused. See
[komari](komari/README.md) for the pinned schema and compatibility limits.

`cf-server-monitor` reads the latest persisted CPU, memory and aggregate disk
sample for each configured UUID from the Worker API. It preserves the upstream
timestamp and supports anonymous reads or an explicit Bearer JWT.
[CF-Server-Monitor guide](cfservermonitor/README.md)

Register a real adapter only in `probe.ProductionSourceAdapters`, explicitly.
Synthetic adapters live in `internal/testkit` and must never be added to that
map. An adapter should not need changes to persistence deduplication,
external confirmation, generic resource selection or the bar's numeric card
rendering.
