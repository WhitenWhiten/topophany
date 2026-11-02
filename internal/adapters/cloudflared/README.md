# cloudflared

Reads a Cloudflare Tunnel connector's own report from the metrics listener
cloudflared runs beside the tunnel. The check kind is `cloudflared`; the
listener is loopback-only, sends no credential and is read with `GET` only.

Tested against cloudflared **2026.10.0** on macOS arm64 (2026-10-19), with a
fake locally-managed tunnel and no live connection
([fixture notes](testdata/2026.10.0/README.md)). The parser reads only the
families below and skips every other line, so an exporter that adds families
keeps working; a release that renames one of these loses that fact rather
than the check.

## Reads

Both reads happen under the check's one timeout, in this order, each with its
own byte budget:

| Read | Budget | Kept |
|---|---|---|
| `GET /ready` | 4 KiB, `secure.JSONResponseBudget` | `ready_connections`, `connector_id` |
| `GET /metrics` | 1 MiB Prometheus text | `ha_connections`, `connection_colos`, `requests_total`, `request_errors`, `active_streams`, `connector_version` |

`/ready` is the connector's readiness statement, the only source that decides
whether it holds an edge session. `/metrics` is a Prometheus registry
snapshot; both are read-only, and neither changes tunnel state, routing or
configuration. `/healthcheck` and every other route are not requested.

The body's `status` field duplicates the HTTP status, and the live 503
carried `Content-Type: text/plain` even though the body is JSON, so the
adapter parses the body and never requires `application/json`. The connector
UUID is kept only when it has a UUID shape, lowercased; any other value is
omitted, never echoed.

## Facts and outcomes

`/ready` decides the outcome; `/metrics` supplies facts, in this order:

1. If `/ready` cannot be read, is non-JSON or is oversized, the sample is a
   monitor `error` (`metrics_unavailable`) and `/metrics` is not consulted.
2. If `/ready` is 503, its body `status` is not 200, or `readyConnections` is
   0, the sample is `fail` / `tunnel_not_ready`. `/metrics` is read only as a
   best-effort fact source then, and its failure is ignored.
3. If `/ready` reports at least one session but `/metrics` is unreadable,
   non-200 or has no allowlisted family, the sample is a monitor `error`
   (`metrics_unavailable`).
4. Otherwise the sample is `ok` with the detail
   `tunnel established; service behind it unproven`.

A stopped connector closes the listener and is `metrics_unavailable`;
`tunnel_not_ready` is a live connector that says it holds no session. A
failure reaches the link only after the check's failure threshold, as with any
check.

| Situation | Outcome / stage / code | Detail |
|---|---|---|
| refused or timed-out listener, non-JSON or oversized `/ready`, or a `/ready` status other than 200/503 | `error` / `adapter` / `metrics_unavailable` (local) | a monitor error, never an outage |
| `/ready` 503, body status other than 200, or `readyConnections == 0` | `fail` / `adapter` / `tunnel_not_ready` | the connector's own statement that it has no edge session |
| ready ≥ 1 but `/metrics` is unreadable, non-200 or has no allowlisted family | `error` / `adapter` / `metrics_unavailable` (local) | the ready statement succeeded, the report did not |
| ready ≥ 1, `expected_connections` set and ready below it | `policy` / `adapter` / `connections_below_expected` | redundancy below what the deployment declared |
| ready ≥ 1, a reported colo outside `expected_colos` | `policy` / `adapter` / `colo_outside_expected` | the connector holds a session elsewhere |
| ready ≥ 1 otherwise | `ok` / `adapter` | `tunnel established; service behind it unproven` |

Facts are `ready_connections` (decimal count, at most 1024 in the documented
shape; a larger value is a malformed-input guard, not a deployment limit),
`connector_id` (lowercase UUID only), `ha_connections`, `requests_total`,
`request_errors`, `active_streams` (decimal counts, absent when the family is
absent), `connection_colos` (sorted lowercase tokens joined with commas, at
most eight; when more are reported the first eight sorted are kept, while the
colo policy compares every distinct reported colo) and `connector_version` (at most 24 token
characters). That is at most eight facts; the probe drops its generic
`connection_semantics` fact for this kind. Counters and gauges are
non-negative integers within eighteen decimal digits; Prometheus decimal and
exponent forms whose value is integral (`1.0`, `1e+06`) are accepted, while
fractional values, signed mantissas, NaN, Inf and overflow are not.
`cloudflared_tunnel_timer_retries` is parsed but not reported as a fact: the
eight-fact budget is spent on the defined readings. Counters are cumulative
since the connector started; a decrease or a changed `connector_id` means a
restart, which the raw totals show rather than hide. The evidence is
`reported` and the path evidence is `local_connector_runtime_reported`.

`cloudflared_tunnel_ha_connections` is not a ready-connection count: the live
503 capture reported `ha_connections 1` with zero ready connections. The
adapter therefore never decides readiness from it.

## Options

| Option | Meaning |
|---|---|
| `expected_connections` | 1..8 edge sessions the deployment declares. Zero ready sessions is still `fail`; one or more below the declaration is policy, not failure. Unset asserts nothing. |
| `expected_colos` | Up to eight colo tokens the connector's sessions may name. Unset asserts nothing; an absent `server_locations` family is not a violation. |

Cloudflare writes a colo uppercase in `CF-Ray` and its documentation, while
the connector's `edge_location` label is documented lowercase. `expected_colos`
therefore accepts either case and compares without it; `connection_colos`
stores the lowercase report. A reported colo is the connector's metadata about
which edge it holds a session with, not proof of which edge carried a service
request.

## Target and transport

The target is the listener itself: a literal loopback address (`127.0.0.0/8`
or `::1`), commonly `http://127.0.0.1:PORT` or `http://[::1]:PORT`, optionally
`https` with `ca_file`. A host name is
refused, including `localhost`, because a hosts file or resolver could point
it elsewhere. The URL carries no user information, query, fragment or path
other than a bare `/`; `path`, `bind_ip`, `resolve_ip` and proxy chains are
refused, and this kind is not in the proxy-traversal list. The probe's client
sets `Proxy: nil`, so no environment proxy applies, disables keep-alives and
compression, bounds response headers, and its dialer accepts only a literal
loopback address. There is no fallback transport. A public topology strips the
`ca_file` path; validation restores a non-secret placeholder on its owned copy
so a viewer can validate an https listener without ever seeing the real path.

## What this proves, and what it does not

`/ready` proves a connector session with the edge. It does not prove that the
public hostname routes to this connector, that the tunnel's ingress rules
reach the intended service, or that the service answers. A separate
application check does that: one on loopback for the service itself, and the
public check through the edge for what the edge serves.

It differs from the public edge check in what is measured. An HTTP check
through the edge measures a request the edge answers, and proves the edge hop
only. This adapter measures no request at all: it reads the connector's
runtime statement, which is evidence of the tunnel session's existence and
count. A reported colo is metadata, never routing proof, and the evidence
stays `reported`.

Attach the check to a `link` from the origin to the edge node (`transport:
"cloudflared"`). A fresh local connector report decides that link's state: a
`tunnel_not_ready` sample turns the link down after the failure threshold,
while a passing sample shows the session and leaves the service behind it
unproven. The link then says its state is `reported`, the connector's own
statement, rather than `inferred` from measured checks. A connector read that
failed leaves the link unknown with its own reason (`local connector read
failed`), never the reported-only reason. A `cloudflare` API check attached to
the same link is shown beside the connector's and does not vote on the link
while a fresh connector report decides it: it can neither outvote the connector
nor degrade the link, whether it passes, violates a policy or fails to read.

## Develop

Run `go test ./internal/adapters/cloudflared`. Parsers have bounded fuzz
targets; loopback servers cover the read paths. Add a new
`testdata/<version>/` directory with provenance notes for another observed
response shape. Do not commit a real connector UUID, tunnel secret, account
tag or full log.

### Partial metrics reads

When `/ready` reports a session but `/metrics` fails, the sample remains a
monitor error. It now preserves `ready_connections` and the shaped connector
ID, with `connector_ready: true` and `metrics_status: unavailable`. The view
shows the session reading alongside the metrics error; no missing metric is
filled with zero. Link health remains conservative and the partial read does
not bypass failure/recovery thresholds. A stopped listener still contributes
no readiness fact. Normal complete samples retain their existing fact set.
