# Cloudflare API adapter — docs-based acceptance, 2026-10-19

Implementation: commit 3eb7ae8.

## What kind of acceptance this is

**No Cloudflare account and no API token were used.** A live capture is
blocked: it needs a real account, a real Tunnel, a real zone and a token
holding only the least-privilege scopes. Asking for one was out of scope for
this change. What was verified instead:

- The three request paths, the response field names, the closed value
  vocabularies and the permission names were read from Cloudflare's own API
  reference on 2026-10-19 (HTTPS, browser user agent).
- Sanitized synthetic fixtures were written from those pages and are stored in
  [`internal/adapters/cloudflare/testdata/api-v4/2026-10-19/`](../../internal/adapters/cloudflare/testdata/api-v4/2026-10-19/README.md).
  They contain no real account, token, zone, pool, record content, origin
  address or undeclared origin name; the addresses are RFC 5737 and RFC 3849
  documentation placeholders.
- The adapter was exercised only against loopback servers. Nothing in the test
  suite contacts `api.cloudflare.com`, and no test needs a credential.

This is a **shape acceptance**, not a live acceptance. It is enough to freeze
the field mapping and the closed vocabularies for a first release; it is not
evidence that a real token and a real account behave as documented.

## Sources checked

| Source | Result on 2026-10-19 |
|---|---|
| [Get a Cloudflare Tunnel](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/methods/get/) | `result.status` is `inactive`, `degraded`, `healthy` or `down`; `result.connections[]` carries `client_version`, `colo_name`, `is_pending_reconnect`, `opened_at`, `origin_ip`, `id`, `client_id`, `uuid`. The reference marks the `connections` array deprecated in favour of `/accounts/{account_id}/{tunnel_type}/{tunnel_id}/connections` and says it will start returning an empty array. |
| [List DNS Records](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/) | Records carry `name`, `type`, `content`, `proxied`, `ttl`, `id`, `zone_id`, `zone_name`, `comment`; the list response carries `result` and `result_info`; the endpoint accepts `name` and `type` query filters. |
| [Pool Health Details](https://developers.cloudflare.com/api/resources/load_balancers/subresources/pools/subresources/health/) | `result.pool_id`; `result.pop_health` is described as "List of regions and associated health status" but the generated model renders it as one object `{healthy, origins}`. `origins` is an array of objects keyed by origin address, each with `healthy`, `failure_reason`, `response_code`, `rtt`. The two renderings conflict, so the adapter accepts both and the fixtures carry both. |
| [Verify Token](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/verify/) | `result` carries `id`, `status` (`active`, `disabled`, `expired`), `expires_on` and `not_before`. Consulted but **not called**: one GET per check is the rule, the selected read's own 401/403 proves the same thing, and this endpoint would need an extra permission. |
| [API token permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/) | The exact permission names are `Cloudflare Tunnel Read`, `DNS Read` and `Load Balancing: Monitors and Pools Read`. |
| [Create API token](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/) | Token creation and scope model. |
| `https://developers.cloudflare.com/fundamentals/api/reference/tokens/` (an older link for token scopes) | **404 Page Not Found** on 2026-10-19. The permissions page and the create-token guide above are the current pages; no content was taken from the missing URL. |

No undocumented field was invented. Where the reference is ambiguous, the
adapter omits the fact or accepts the documented alternatives, as noted below.

## The three reads

One allowlisted GET per check, bearer token from `adapter.token_env`, exact
paths, no redirects, no ambient proxy, bounded body and JSON complexity. A
non-loopback target must be exactly `https://api.cloudflare.com/client/v4` (a
trailing slash and `:443` accepted; port 1..65535); a different host, a
different path or an invalid port is refused, so the token cannot be sent to a
lookalike service. Literal loopback addresses remain accepted for tests over
http or https. The client clears the URL's `RawPath` and trims trailing slashes
before composing one allowlisted path.

| Read | Request | Facts | Policy |
|---|---|---|---|
| `tunnel` | `GET /accounts/{account_id}/cfd_tunnel/{tunnel_id}` | `tunnel_status`, `tunnel_connections`, `tunnel_pending_reconnect` (deprecated flag), `tunnel_colos` (fact capped at 8 sorted), `oldest_connection_at`; the four connection facts are absent when the connection list is empty beside a `healthy` or `degraded` tunnel | `connections_below_expected`, `colo_outside_expected` (compares every distinct reported colo, not only the capped fact) |
| `dns_record` | `GET /zones/{zone_id}/dns_records?name={record_name}&type={record_type}` | `record_count` (present `result` only), `record_proxied`, `record_ttl`, `record_content_matches` | `record_missing` only for an explicit `[]`; absent/null result and a partial `total_count` page are monitor errors |
| `pool` | `GET /accounts/{account_id}/load_balancers/pools/{pool_id}/health` | `pool_healthy`, `origins_healthy`, `origins_total`, `regions_reporting`, `origin.<declared>` (a present verdict only) | `origins_below_expected` (an absent declared origin is policy but gets no per-origin fact) |

Not stored: the tunnel's `origin_ip`, its connection/client UUIDs and name, the
record's content, id, zone name and comment, undeclared origin names, and every
`errors[].message`. The DNS content is compared with `expected_content` in
memory and only the comparison is kept. The pool adapter reports only declared
origins as `origin.<declared>` keys; undeclared origins contribute to the two
counts only. Every observation is `reported`, stage `adapter_api`, path
evidence `cloudflare_api_reported`, with `peer_text: withheld_secret_request`.

## Error mapping

All of these are monitor errors: `error`, local, never a target failure or an
outage. The missing-token and invalid-configuration rows are stage `setup`, with
nothing sent; every other row is stage `adapter_api`.

| Situation | Code |
|---|---|
| `token_env` unset, variable missing or empty | `cloudflare_token_missing` (stage `setup`, nothing sent) |
| invalid options or target in a hand-built check | `invalid_cloudflare_options` / `invalid_adapter_url` (stage `setup`) |
| connect error, timeout, TLS failure, cancellation | `api_unavailable` |
| HTTP 401 or 403 | `authentication_failed` |
| HTTP 429, any 5xx, or any other non-200 including a refused redirect | `http_<status>` (for example `http_429`) |
| body over the 64 KiB (tunnel, pool) or 128 KiB (DNS) budget | `response_too_large` |
| invalid JSON, JSON complexity over budget, missing `success`, absent/null `result`, a partial DNS page (`result_info.total_count` ≠ page length), missing documented result shape | `unsupported_response_shape` |
| HTTP 200 with `success: false` | `api_unavailable` |

## What a real account still has to confirm

Each of these is open because no live capture was possible; the adapter's
choice for each is documented in the package README.

1. **Token scopes are sufficient and enforced as expected.** Only the three
   Read permissions have been documented, not exercised. A real token holding
   exactly them should make all three reads pass; a token missing one should
   produce `authentication_failed` on that read only.
2. **The DNS list cardinality and pagination.** Whether `name` + `type` can
   return more than one record, and whether the response always carries
   `result_info.total_count`. One record yields `record_proxied` and
   `record_ttl`; several yield them only when every record agrees; a total
   greater than the returned page is refused. A response without `result_info`
   is accepted from its page alone.
3. **The real `pop_health` shape.** Region-keyed or the single documented
   object; whether region keys are colo codes or region names. The adapter
   accepts both documented renderings, counts parsed regions and keeps no
   region name.
4. **Whether pool origin keys are addresses or names.** The reference example
   keys them by address; `expected_origins` accepts either an IP or a host name
   and compares canonically. A real report whose keys are names while the
   declaration uses addresses would read every declared origin as missing.
5. **The deprecated tunnel `connections` array.** A live account may already
   return it empty. In that case, beside `healthy` or `degraded`, the
   connection list is treated as not reported and no connection fact is
   emitted; the adapter does not switch to the dedicated connections endpoint,
   because that would be a second GET.
6. **Error bodies.** Only the success flag is read; the real `errors[]` code
   and message texts are never stored, so a 200/`success:false` body needs no
   fixture.
7. **Rate limiting.** The documented limit is about 1,200 requests per five
   minutes per user (shared by that user's tokens); validation requires
   `interval_seconds >= 60`, but a real 429 response was not produced.
8. **Tunnel status changes.** Whether `degraded` means what the reference says
   in a real deployment, and how quickly the connections array catches up with
   a connector restart.
9. **A tunnel check beside a `cloudflared` check.** The goal of this adapter
   is a tunnel check beside the connector's local check showing the two
   readings on the same link; that requires a live account and was not run
   against one. The link rule for the pair is covered by engine tests on
   synthetic reports, as recorded below.

## Follow-ups this release does not close

- **The link view.** An API tunnel check may now name the same link as the
  `cloudflared` check, and the link's row on the Servers card shows both
  readings, the connector's sessions and colos (`ready_connections`,
  `connection_colos`) and the API's connections and status
  (`tunnel_connections`, `tunnel_status`): `→ Cloudflare   cloudflared ·
  4 sessions · hkg,sin · API 4 connections healthy`. `topo show servers --json`
  carries the same words as the link's `reading`. The API sample does not vote: while a fresh connector report decides the link, the
  API check is listed beside it and neither outvotes nor dilutes it, whether it
  passes, violates a policy, fails to read or has gone stale, and the link
  reads `reported` rather than `inferred`. Without a fresh connector report the
  API sample stays one of the link's checks and a link it alone describes is
  unknown. A `cloudflared` connector report remains the local runtime
  statement, and a Cloudflare API read is remote platform metadata. This was
  exercised on synthetic reports only
  (`engine.TestRemotePlatformReportDoesNotVoteBesideConnector`,
  `view.TestCardLinkCarriesTheTunnelReading` and
  `tui.TestServerCardLinkRowSaysTheTunnel`).
- **Preview.** `internal/preview` has no `cloudflare` branch, so a custom
  preview topology containing this kind falls through the generic synthetic
  path. The shipped preview topologies contain no such check, and a
  reported branch for it is not part of this release.
- **The region-keyed `pop_health` fixture** is a plausible reading of the
  reference, not a captured shape; the single-object fixture matches the
  generated model. Both are labelled synthetic in the fixture README.
- **`is_pending_reconnect`** is deprecated in the current reference and
  documented as always false; the adapter still counts the flag it is sent,
  and `tunnel_pending_reconnect` may therefore read 0 on every live response.
- **`token_env` in the public topology.** `config.Public` strips it and
  `ValidatePublic` restores a non-secret placeholder on its owned copy, as for
  `cloudflared`'s `ca_file`; no token name or value is published.

## Automated verification

The TUI check form now authors this kind: token variable, read, the
read's identifiers, and expected connections/colos/proxied/content/origins,
with the other read's fields cleared on save; `TestEditorCloudflareCreateFromForm`
creates both a tunnel and a DNS check from the form and validates the topology.

The implementation leaves `headers.go` byte-identical. `gofmt -l .` is empty,
`go vet ./...` is clean and one `go test -count=1 ./...` run passed. The
adapter tests are loopback-only and cover the allowlist, traversal, exact
budgets and cancellation, status mapping, `success:false`, each read's happy
path and composed server path, DNS result presence and partial pages, every
policy code (including a ninth colo beyond the fact cap and an absent pool
origin with no invented verdict), the reflection tests that echo the bearer
token into every string field (including a 24-character token that is itself a
valid version token), config validation, contract/clone/public behavior,
admission as reported evidence, TUI authoring from the form, and the engine
invariant "API 200 while the business check fails still fails; API 401 while
the business check passes creates no downtime". See
[`docs/SCENARIOS.md`](../SCENARIOS.md) for the row-to-test map.

Because no live account was used, this acceptance does not claim that the
adapter works against a real Cloudflare account. It claims that it compiles,
behaves as specified on synthetic documented shapes, and withholds a
credential-bearing response completely. The real-account acceptance remains
blocked on a scoped token and a test zone/tunnel/pool.
