# Cloudflare

Pure interpretation of Cloudflare response headers, redirects and the
`/cdn-cgi/trace` body: `Cf-Ray`, `Cf-Cache-Status`, `Cf-Mitigated`,
`Server-Timing`, Access login redirects and trace lines; plus a read-only
Cloudflare API adapter (`kind: "cloudflare"`) for three documented resources.
A nonempty ray header selects the provider, even when its labels cannot be
retained. The POP is the suffix after the last hyphen. `Cf-Mitigated` is kept
only as the closed value `challenge`, which Cloudflare sets on challenge
pages; other Cloudflare products set other values, such as `guardrails`, and
this adapter drops them.
`AccessFacts` reads the status and `Location` of a response. Cloudflare
documents a browser request without an Access session as a `302` to
`<team>.cloudflareaccess.com/cdn-cgi/access/login/<host>?kid=…&redirect_url=…`;
under strict service-token authentication, a failed service-token
authentication returns `401` or `403` instead, with no redirect to recognize.
Strict authentication is on by default for Zero Trust organizations created on
or after 2026-10-05 and optional for older organizations
([Cloudflare's service-token documentation](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/)).
The adapter accepts `302`, `303` or `307` and keeps
only `edge_access: login_redirect` when the location is an absolute URL
without user information whose host, lowercased, ends in
`.cloudflareaccess.com` with at least one character before the suffix. The
scheme is not restricted and `Hostname` strips the port, even an out-of-range
one, so `.team.cloudflareaccess.com` and `..cloudflareaccess.com` pass: this
follows the documented host suffix, not the team-name syntax. Malformed,
opaque, relative, lookalike and control-bearing locations yield nothing.
Nothing else of the location is kept, because it carries a key ID and the
redirect target. Tests use synthetic redirects; no live Access-protected host
is used.
The views append `· Access login` when the response also names the edge. Both
the mitigation and Access words cross the secret boundary while the POP does
not.

`ServerTimingFacts` reads Cloudflare's `cfL4` entry from `Server-Timing`,
which some responses carry: a query string in its `desc` parameter with the
colo's own view of the connection (TCP or QUIC) that carried the response.
The first entry named exactly `cfL4` across all header values is read, quoted
or unquoted, with `url.ParseQuery`; the combined values are bounded at 2 KiB
of parsed bytes and a larger set yields nothing. Cloudflare's Rules changelog
([2026-04-01](https://developers.cloudflare.com/rules/changelog/)) references
`Server-Timing: cfL4`, and the rules field reference for
[`cf.timings.client_tcp_rtt_msec`](https://developers.cloudflare.com/ruleset-engine/rules-language/fields/reference/cf.timings.client_tcp_rtt_msec/)
documents that field, not the header's fields, in milliseconds. No official
field layout or unit specification for the header itself was found. The
header's `rtt` and `min_rtt` are read as microseconds from the live capture in
[`testdata/2026-10-19/`](testdata/2026-10-19/README.md): a fresh check read
`time_connect=0.0039 s` while the header read `rtt=1008`; as milliseconds that
would be a thousand times the completed handshake, as microseconds it is
1.008 ms. They are recorded as `edge_rtt_ms` and `edge_min_rtt_ms` in whole
milliseconds rounded to the nearest. `retrans` and `lost` become counts, and
`proto` only `TCP` or `QUIC`. Integers are one to twelve ASCII digits parsed
strictly; a malformed field never discards the others, and repeated keys keep
the first value. `cid`, `ts`, `sent_bytes`, `recv_bytes`, `cwnd`,
`delivery_rate`, `x` and everything else are dropped. These numbers and the
protocol word cross the secret boundary; POPs and other free labels do not.
The probe reads the entry only after `edge_provider` is `cloudflare`, and the
views append `· edge rtt N ms` last, showing a reading that rounded to 0 as
`<1 ms`. The entry is optional: `speed.cloudflare.com` is a Workers endpoint,
not a customer proxied zone, and spot checks of `www.cloudflare.com`,
`one.one.one.one` and `developers.cloudflare.com` found no `cfL4`. Presence on
an ordinary proxied zone is unverified; the spot checks suggest the entry is
not on every zone, but that is inferred rather than documented.

`TraceFacts` reads the body of `/cdn-cgi/trace`, which Cloudflare documents as
the way to identify the data center serving a request
([CDN-CGI endpoint](https://developers.cloudflare.com/fundamentals/reference/cdn-cgi-endpoint/));
that page does not specify the line vocabularies. The live capture is in
[`testdata/2026-10-19/`](testdata/2026-10-19/README.md): over HTTP/1.1 it read
`colo=LAX`, `http=http/1.1`, `tls=TLSv1.3`, `warp=off`, `gateway=off` and
`loc=US` beside `fl`, `h`, `ip`, `ts`, `visit_scheme`, `uag`, `sliver`, `sni`,
`rbi` and `kex`. Forced HTTP/2 and TLS 1.2 requests the same day read
`http=http/2` and `tls=TLSv1.2`; `http/3`, `warp=on`, `warp=plus` and
`gateway=on` were not observed live, and the parser accepts them as the closed
vocabulary rather than reporting any other value.

A body is recognized only when both the `fl` and `colo` keys are present, the
first occurrence of a repeated key is read, and a body over 4 KiB is not read
as a trace: neither the facts nor the unconditional `exit_ip` come from it,
while an `expected_exit` assertion keeps its own exit parser. Kept:
`trace_warp` (`off`, `on`, `plus`), `trace_gateway` (`on`, `off`),
`trace_http` (`http/1.1`, `http/2` or `http/3` mapped to `http1.1`, `http2`,
`http3`) and `trace_tls` (`TLSv1.2`, `TLSv1.3`) as closed words, and
`trace_colo` (exactly three uppercase letters) and `trace_loc` (two uppercase
letters) as free tokens checked only for their shape. The probe calls it only
when the check's `exit_format` is `trace`, reads the `ip=` line as `exit_ip`,
and merges the map under the same secret classification as the other labels;
`uag`, `h`, `sni`, `ts`, `fl`, `visit_scheme`, `sliver`, `kex`, `rbi` and
every unknown line are never kept. The colo and country are free labels and are
withheld from a secret observation while the four closed words survive; a
declared colo list is compared from the raw parse without storing the colo. No
line or body text is ever returned.

`HeaderFacts` returns a newly owned fact map and `true` when recognized, or
`nil, false` otherwise; `AccessFacts` returns a one-word map or `nil`,
`ServerTimingFacts` returns nil when there is no exact `cfL4` entry or no
usable field, and `TraceFacts` returns a newly owned map and true when both
keys are present, or nil and false otherwise, never any line text. None mutates
its input. `probe.EdgeFacts` owns the cross-provider first-match order;
adapters do not call one another. Shared short-label/cache-vocabulary rules
live in `edgeheaders`.

These labels are peer-reported metadata. They do not authenticate the provider,
verify routing, or establish the health of an origin or application. The HTTP
probe owns transport, response evidence and withholding labels after a private
request. The header, redirect, `Server-Timing` and trace functions make no API
calls and need no new configuration; the separate API adapter below does.

Run `go test ./internal/adapters/cloudflare`. Tests use synthetic headers and
trace bodies;
`probe.TestEdgeFactsCompatibilityBeforePackageSplit` and
`probe.FuzzEdgeFactsCompatibility` compare with the frozen parser from commit
`599c890`. This baseline identifies the Topophany behavior being preserved,
not execution against a particular vendor release. Add new response shapes
and their evidence limits here when extending this platform.

## Cloudflare API adapter (`kind: "cloudflare"`)

Reads three things only Cloudflare knows, with a least-privilege token. Each
check performs **one** allowlisted `GET`; nothing else is requested. The token
is named by `adapter.token_env` and read from the worker's environment; it is
never written in the topology, never sent to the zone, and cleared from the
public topology by `config.Public`. Documented scopes are exactly Account ›
Cloudflare Tunnel › Read, Zone › DNS › Read and Account › Load Balancing:
Monitors and Pools › Read; a read that needs another permission is out of scope.

| Read | Request (exact path) | Facts | Policy codes |
|---|---|---|---|
| `tunnel` | `GET /accounts/{account_id}/cfd_tunnel/{tunnel_id}` | `tunnel_status` (closed `healthy`/`degraded`/`down`/`inactive`), `tunnel_connections`, `tunnel_pending_reconnect`, `tunnel_colos` (≤ 8 sorted tokens), `connector_versions` (≤ 4 sorted tokens), `oldest_connection_at` (RFC 3339); the five connection facts are absent when the API's connection list is empty beside a `healthy` or `degraded` tunnel | `connections_below_expected`, `colo_outside_expected` (compares every distinct colo, not only the eight the fact shows) |
| `dns_record` | `GET /zones/{zone_id}/dns_records?name={record_name}&type={record_type}` | `record_count`, `record_proxied` (`true`/`false` when every record agrees), `record_ttl` (when every record agrees), `record_content_matches` (`true`/`false`/`not_declared`) | `record_missing`, `record_not_proxied`, `record_proxied_unexpectedly`, `record_content_mismatch` |
| `pool` | `GET /accounts/{account_id}/load_balancers/pools/{pool_id}/health` | `pool_healthy` (when a region reports it), `origins_healthy`, `origins_total`, `regions_reporting`, `origin.<declared>` (`healthy`/`unhealthy`, declared origins only) | `origins_below_expected` |

Side-effect review: all three are documented `GET` reads — Get a Cloudflare
Tunnel, List DNS Records and Pool Health Details — and none creates, updates
or deletes a tunnel, record, pool, monitor or load balancer. Paths added later
need their own review, because a `GET` alone is not proof of no side effects.

The DNS query is the only query this adapter builds, and it carries exactly one
`name` and one `type` (`A`, `AAAA` or `CNAME`). A path outside the three forms,
an extra query parameter, a dot segment and a lookalike suffix are refused in
the client before a request exists, with code `api_path_not_allowed`. The
bearer token is attached only after the allowlist check.

### Options

`adapter.cloudflare` is nested inside `adapter`: `read` is required and names
one of the three reads. `account_id`, `zone_id` and `pool_id` are 32 ASCII hex
characters. `tunnel_id` accepts a UUID in `8-4-4-4-12` ASCII hex form, with
uppercase or lowercase letters; legacy 32-hex inputs remain accepted. Its
spelling is preserved in the configuration and request path, without
normalization, so existing configurations keep their measurement contracts.
`record_name` is a DNS name (a whole `*` label is allowed for
a wildcard record); `record_type` is `A`, `AAAA` or `CNAME`. `expected_proxied`
and `expected_content` belong to the DNS read; `expected_connections` (1..64)
and `expected_colos` (≤ 8 three-uppercase-letter codes) to the tunnel read;
`expected_origins` (≤ 16 IP addresses or host names) to the pool read. A field
belonging to another read is refused rather than ignored, so a check cannot
silently change the contract of a different read. `expected_content` is
compared in memory and never stored; the facts keep only
`record_content_matches`. The options are part of the measurement contract:
setting or changing them starts a new contract and history for that check, and
an unset field leaves every existing contract unchanged.

`token_env` is required for this kind and is a credential: `HTTPCredentials`
therefore requires an `https` target, or a direct literal loopback target with
no proxy and no remote `resolve_ip`. The target defaults to
`https://api.cloudflare.com/client/v4`. Outside a literal loopback test
address, the target must be exactly that host and `/client/v4` base path (a
trailing slash and `:443` are accepted), because the token is sent to it; a
different host, a different path or an invalid port is refused. The base
carries no userinfo, query, fragment or dot segments. Plain `http` is accepted
only for a literal loopback test address. `path`,
`bind_ip`, `resolve_ip`, `server_name` and `ca_file` are honoured as for the
other API adapter: the API is legitimately reachable through the check's
proxies, so `cloudflare` is in the proxy-traversal kind list. The client uses
the path's explicit proxies, never an environment proxy, disables keep-alive
and compression, bounds response headers at 32 KiB and follows no redirect: a
3xx is a monitor error, so the bearer token cannot be forwarded to another
location. There is no direct-transport fallback. An endpoint is refused; the
API base is `target`.

`interval_seconds` must be at least 60 for this kind. Cloudflare documents a
limit of about 1,200 requests per five minutes per user (the limit is shared by
the user's tokens); one request per
minute is well inside it, and the validation refuses a topology that would
poll faster.

### Errors and outcomes

Every API response is credential-bearing, so every failure is a **monitor**
error (`error`, local), never a target failure. The missing-token and invalid-
configuration rows are stage `setup`, with nothing sent; every other row is
stage `adapter_api`:

| Situation | Code |
|---|---|
| `token_env` unset or the variable missing/empty | `cloudflare_token_missing` (stage `setup`, nothing sent) |
| a path outside the three exact forms, a dot segment or an extra DNS filter | `api_path_not_allowed` |
| invalid options or target in a hand-built check | `invalid_cloudflare_options` / `invalid_adapter_url` (stage `setup`) |
| connect error, timeout, TLS failure, context deadline | `api_unavailable` |
| `401` or `403` | `authentication_failed` |
| `429`, `5xx`, or any other non-200 (including a refused redirect) | `http_<status>` (for example `http_429`) |
| body over its budget (64 KiB tunnel/pool, 128 KiB DNS) | `response_too_large` |
| invalid JSON, exceeded JSON complexity, missing `success`, or a response missing the documented result shape | `unsupported_response_shape` |
| HTTP 200 with `success: false` | `api_unavailable`; `errors[].message` is never read |

A passing read is `ok`, stage `adapter_api`, evidence `reported`, path evidence
`cloudflare_api_reported`, with `peer_text: withheld_secret_request`. It proves
that Cloudflare's API returned the reported state; it makes no request to the
zone and says nothing about an origin or a public hostname answering. Policy
assertions are state comparisons, not request failures: the tunnel check reads
`ok` with `tunnel_status: down` unless a declared count or colo list makes it
`policy`, and a missing DNS record is `record_missing` policy.

When the array is reported, `tunnel_connections` is its length,
including any entry marked `is_pending_reconnect`; `tunnel_pending_reconnect`
is the subset that carries that flag. The current reference marks
`is_pending_reconnect` deprecated and says it is always false, so the pending
fact may read 0 on a live response; the array is still counted as returned.
A `down` or `degraded` `tunnel_status` is a reported fact and does not by
itself fail the check: it
is the platform's word, and `expected_connections` and `expected_colos` are the
declared state assertions. Use an application check and the link modelling
from `cloudflared` for reachability. A `cloudflare` check attached to the same
link as a `cloudflared` check stays in the link's check list beside the
connector's, but while a fresh connector report decides the link the API read
does not vote on it, so it neither outvotes the connector nor degrades the
link; with no connector report, an API read alone leaves the link unknown.

The tunnel read counts the `result.connections` array. Cloudflare documents
that array as deprecated and says it will start returning an empty array in
favour of the dedicated connections endpoint. A `healthy` or `degraded`
`tunnel_status` means at least one live connection by definition, so an empty
array beside either is the field having moved, not a tunnel without a
connector: the connection list is then **not reported**. The five connection
facts (`tunnel_connections`, `tunnel_pending_reconnect`, `tunnel_colos`,
`connector_versions`, `oldest_connection_at`) are absent, neither
`connections_below_expected` nor `colo_outside_expected` can fire, the outcome
stays `ok`, and the detail reads "Cloudflare API state reported; the
connections list was not reported". An empty array beside `down` or `inactive`
is a count of zero: `tunnel_connections` is `0` and the declared policies apply
as for any count. A populated array is counted as returned whatever the status
says. `tunnel_status` carries the API's own word in every case. The read does
not call the dedicated endpoint: one GET per check is the rule, and that
endpoint is outside the three allowlisted paths. The connection's `origin_ip`, its `id`, `client_id`
and `uuid`, and the tunnel's name and account tag are never read into a fact.
`connector_versions` keeps only tokens of at most 24 characters; `tunnel_colos`
keeps only three uppercase letters, sorts them and keeps the first eight. The
colo policy compares every distinct reported colo, not only the eight the fact
shows: a ninth colo is still a connection outside a declared list.

The DNS `result` must be present: an absent or null result is a monitor error,
not an empty record list, while an explicit `[]` yields `record_missing`
policy. When the response carries `result_info.total_count` and it differs
from the page length, the page is partial and the read is a monitor error
rather than a count or policy based on a subset. A DNS list can return more than
one record for one name and type. The adapter reports `record_proxied` and
`record_ttl` only when every record agrees; a mixed list leaves those facts
absent and `record_count` says how many were returned. `record_missing` is policy with `record_content_matches: false` when
a content expectation was declared and the list is empty. Content is compared
with `expected_content` in memory, in the form Cloudflare stores it rather than
byte for byte: for an `A` or `AAAA` record both sides are parsed as addresses
and compared as addresses (`2001:DB8::1` matches `2001:db8::1`), and an
address that does not parse on either side is a mismatch; for a `CNAME` both
sides are lowercased and lose one trailing dot (`Example.COM.` matches
`example.com`). The record's content, id, zone name and comment are never
stored.

The pool reference renders `pop_health` both as a single object
`{healthy, origins}` and as "a list of regions and associated health status",
collapsing the region map in its generated model; the adapter accepts both
renderings. The region-keyed form is the speculative reading; the single-object
form matches the generated model. `regions_reporting` counts the region objects it parsed,
`origins_total` and `origins_healthy` count distinct origins, and an origin
reported in several regions is healthy only when every report calls it
healthy. A region that omits `healthy` for an origin does not establish it
healthy, and `pool_healthy` is omitted when no region reports a verdict rather
than invented. Origin entries are keyed by origin address in the documented
example; only addresses declared in `expected_origins` appear as fact keys, and
an undeclared origin contributes to the counts only. A declared origin that is
missing or unhealthy is `origins_below_expected` policy; a missing one gets no
`origin.<name>` fact, because no region gave a verdict.

### Boundary and provenance

The bearer token is echoed into every string field of every read by the
reflection tests (`TestBearerTokenCannotEchoIntoObservation`,
`TestShapedTokenCannotEchoAsVersion`, `TestTokenFragmentsCannotEchoAsVersions`):
nothing of the token reaches the observation, its fact keys or values, its detail or its error. In
`tunnel_colos`, `connector_versions` and `oldest_connection_at`, a value (or a
comma-separated member of a list) that is the token, or any run of at least
eight bytes of it, is dropped even when it has a reportable shape (a
24-character version token, a three-letter colo), so a token split across two
version strings does not survive either; numbers are never examined, and
shape-validated tokens from the platform are otherwise indistinguishable from
evidence. A run shorter than eight bytes is not treated as an echo, because it
cannot be told from a colo.
Only closed words, numbers, shape-validated tokens (three uppercase letters for
a colo, at most 24 token characters for a connector version) and comparisons
with the topology leave the adapter. Numeric counts are an explicit exception,
as for every credential-bearing exchange.

No live account or token was used. The fixtures under
[`testdata/api-v4/2026-10-19/`](testdata/api-v4/2026-10-19/README.md) are
synthetic, written from Cloudflare's API reference on 2026-10-19, and contain
no real account, token, record content, origin address or undeclared origin
name. They pin field names and vocabularies only; the acceptance record
([`docs/acceptance/CLOUDFLARE_API_2026-10-19.md`](../../../docs/acceptance/CLOUDFLARE_API_2026-10-19.md))
lists what a real account still has to confirm.

Run `go test ./internal/adapters/cloudflare` from the repository root. The
three response parsers have bounded fuzz targets; loopback servers cover the
allowlist, budgets, status mapping, every policy code and the reflection test.
