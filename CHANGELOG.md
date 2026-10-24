# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Optional native collector Web UI with all nine monitoring pages, interactive
  topology maps, server and service cards, observer views, incident evidence,
  sample history and a local topology draft editor with validation and export.
  Build with `-tags webui` and explicitly enable it with `topo serve --web`;
  `--web-listen` defaults to loopback. Ordinary builds do not embed the frontend
  or require Node.js. See the [Web UI guide](docs/WEBUI.md) for setup, the
  feature matrix and supported-mode limits.
- Viewer-authenticated Web projections and bounded event streaming, with
  automatic reconnection and token-revocation handling. Draft validation never
  applies configuration to a collector or worker; browser tokens remain in
  memory for the current connection.

### Changed

- Service and check views support grouping by environment while retaining the
  shared evidence and availability semantics.
- The homepage is drawn in ink on paper, as topophany-bar's new icon is: the
  icon's dithered globe turns in the hero, the wordmark is solid ink over a
  half-tone shadow, headings are set in a serif and labels in mono. The
  terminal replay, the live demo and the web app keep the TUI's palette, set
  on the page as plates. A new section introduces topophany-bar, Topophany's
  macOS menu bar companion.
- The favicon, the Apple touch icon and the social card are topophany-bar's
  globe. `scripts/icon.py` draws them from the same dither as its app icon,
  in Python alone.

## [1.5.0] - 2026-10-23

This release adds Komari resource collection and collector-free direct profiles,
and improves durable delivery, collector failover and evidence recovery. See the
[release notes](docs/releases/v1.5.0.md) for setup, capacity and rollback details.

### Added

- Komari external source adapter for the pinned 1.6.0 resource schema: read-only
  CPU, memory and aggregate disk samples, exact byte counters, stable upstream
  sample identity and bounded, atomic multi-node collection. API-key and
  anonymous access are supported; online presence and per-filesystem disks are
  outside this mapping. See the [adapter guide](internal/adapters/komari/README.md)
  and [`examples/komari.json`](examples/komari.json).
- Komari direct mode: collector-free resource TUI, status, show and monitor
  reads with API-key or anonymous node discovery, private profiles and aggregate
  disk selection. Profiles can mix Beszel and Komari sources. See
  [configuration and limits](docs/DIRECT.md#configure-komari).
- Opt-in official Komari binary acceptance harness using an isolated loopback
  server and synthetic Agent v2 samples.

### Changed

- Reserve worker outbox byte and record capacity per recipient, so an offline
  collector cannot exhaust healthy destinations' shares. Drain or archive legacy
  backlogs before upgrading if they exceed the new per-recipient shares.
- Preflight complete external replay-state reservations at collector startup,
  reload and capacity inspection. External state may use up to 128 MiB within
  the existing global budget; direct profiles allow 32 MiB total and 4 MiB per
  source with multiple sources, preserving full deduplication windows.
- Update only changed direct evidence and accounting, reuse unchanged prepared
  projections, and size store evaluation views from bounded evidence tails.
- Share bounded source transport, report transactions, check-option rules and
  evidence invalidation policy across the collection and display paths.
- Require an executed old-reader compatibility test in CI and release builds.

### Fixed

- Retain pending samples through HTTP 401/403 authorization outages and restart;
  isolate only explicitly rejected observations inside fan-out items while
  preserving valid neighbors and independent recipient delivery.
- Reserve read time for backup collectors and fail over scoped/global snapshot
  pairs together instead of combining incomplete reads.
- Retain worker-local external reports, deduplicate repeated samples and expire
  evidence at its original deadline. Preserve durable admission and publication
  ordering through external replay, migration and background checkpoints.
- Rebuild confirmation from retained samples when staleness rules change, and
  reconfirm legacy carries whose original rules are unknown. Preserve compatible
  confirmation across restart and supported threshold or presentation edits.
- Validate source execution before committing collector/worker reloads, and
  capture alert settling configuration before concurrent reloads can change it.
- Retry transient direct HTTP failures consistently, including 408 and all 5xx
  responses; keep source error backoff above its normal collection interval.
- Close collection and discovery transports on success, failure and cancellation,
  including unfinished TLS handshakes. Preserve stream shutdown and allow bounded
  Windows ACL cold startup without weakening permission checks.

### Security

- Reject credentials reflected through discovery identities and redact all
  session credentials before truncating discovery text. Share terminal-safe JSON
  encoding for source commands, and retain strict credential, Unicode, JSON and
  response-budget boundaries across Beszel and Komari.

## [1.4.2] - 2026-10-22

This release adds external observation sources with a read-only Beszel Hub
adapter and direct current-state reads without a collector, and advances the
wire schemas that carry external evidence. See the
[release notes](docs/releases/v1.4.2.md) for upgrade and rollback steps.

### Added

- External observation sources: typed top-level `sources` and `external` check
  bindings, normalized resource and status reports, persisted report cursors and
  source-contact state, bounded source-level collection with fan-out,
  reported-only snapshot projection, unified resource selection and optional
  stream metadata. Native host checks and their measurement contracts are
  unchanged. See
  [`docs/MULTI-PROBE-IMPLEMENTATION.md`](docs/MULTI-PROBE-IMPLEMENTATION.md) and
  [`docs/EXTERNAL-ADAPTERS.md`](docs/EXTERNAL-ADAPTERS.md).
- Beszel Hub adapter: read-only CPU, memory and filesystem samples with stable
  upstream sample identity, independent field availability and optional reported
  system status. It is documented in the
  [adapter guide](internal/adapters/beszel/README.md) and
  [`examples/beszel.json`](examples/beszel.json).
- Direct current-state reads: `topo sources list|inspect|save|remove` and
  `--source direct` read selected Beszel systems inside the `topo` process
  without a collector, daemon or resource history. See
  [`docs/DIRECT.md`](docs/DIRECT.md) and the
  [D1 contract](docs/contracts/direct-v1/contract.md).

### Changed

- Wire schemas: batches and snapshots are schema 3. A collector still accepts
  schema 2 native batches and a client still reads schema 2 native snapshots.
  External reports and their snapshot projection require schema 3 and fail
  explicitly on a schema 2 reader. The on-disk checkpoint moves to schema 2
  before the first external record is acknowledged; an older binary refuses it
  instead of ignoring state.
- External contract identity includes the binding and the source's measured
  configuration, including its resolved ordered route. Public contracts remain
  redacted, and native contract hashes are unchanged.
- Resource selection is shared by native host and external reports, with
  ordered fallback, an explicit primary disk that is never guessed, and
  independent deadlines for each resource.
- The monitor stream stays at protocol schema 1 and adds optional external
  metadata plus the `direct.schema=1` capability.

### Fixed

- Preserve external evidence through recovery and refresh, detach public
  snapshots from mutable report state, and apply report state changes only
  after a report is accepted. Reset confirmation across report epochs and
  evidence gaps, and preserve it when a configured threshold is unchanged.
- Keep external state consistent with background checkpoints: serialize format
  migration, preserve external state when a checkpoint is captured, charge it
  to observer quotas, and install the external context before journal recovery.
- Advance admission's future-time reference with monotonic elapsed time and
  share that clock between API and local writes, so a collector clock step-back
  does not reject valid samples.
- Keep a direct-mode nonrenewable token usable until it expires.
- Commit a stream replacement whose first read exceeds the five-second
  preparation budget as unavailable evidence instead of trusting a partial
  result. Keep the direct credential-storage test portable to Windows, where
  POSIX permission bits are not enforced.

## [1.4.1] - 2026-10-21

This patch reduces collector and worker CPU, memory, snapshot traffic and disk
synchronization overhead. See the [release notes](docs/releases/v1.4.1.md) for
upgrade considerations and the [operations guide](docs/OPERATIONS.md) for tuning.

### Changed

- Exclude unused probe option blocks from measurement contracts. This changes
  contract hashes: drain old worker queues before coordinating collector and
  worker upgrades. Stored observations retain their old contracts and do not
  count toward the new ones; see the release notes for the transition procedure.
- Keep admission's future-time reference advancing with monotonic elapsed time
  after collector clock rollback, including heartbeat delivery timestamps.

- Reuse folded check evidence across collector snapshots, incident reads and
  monitor panels, including heartbeat-only updates. Each read still evaluates
  freshness and observer liveness at its own time; one-shot and worker-local
  views avoid preparing an unused cache.
- Negotiate gzip snapshots on demand, retain useful encodings within the existing
  cache budget, and bound native clients' compressed and decoded input. Viewers
  cache a validated public topology per collector and request only dynamic state
  while its revision matches; older peers continue to exchange full snapshots.
  Snapshot encodings are validated once per capture, and availability history is
  loaded only for checks included in the requested view.
- Maintain store usage and byte counters incrementally. Automatic checkpoints
  adapt to the previous checkpoint's size between `--checkpoint-bytes` (default
  16 MiB) and `--max-journal-bytes` (default 128 MiB), then stream a detached
  capture in the background while ingestion continues in the live journal.
  Publication preserves the journal suffix and crash replay; explicit checkpoints
  and shutdown wait for durability, and persistence failures stop further writes.
- Keep worker outbox payloads on disk and load byte-bounded delivery batches from
  bounded recipient indexes. `--outbox-records` defaults to 20,000 and independently
  limits pending records; a queue already above the configured limit is refused
  at startup without deleting data.
- Batch durable outbox cleanup and journal partial recipient acknowledgements in
  `acks.jsonl`, reducing file rewrites and synchronization while preserving
  independent recipients and at-least-once delivery. Keep the ACK journal with
  the outbox during backups and moves.
- Pace idle worker heartbeats with `--heartbeat-interval` (default 15 seconds,
  capped at one third of the observer's freshness window). Queued delivery keeps
  its two-second polling cadence; successful backfill continues within bounded,
  paced bursts of up to four requests. Errors and throttling retain retry backoff.
- Refresh browser artifact cache versions for the updated source and toolchain.

### Fixed

- Refresh cached snapshot captures after a backward clock step so future samples
  and heartbeats are not kept active by a stale capture; reuse gzip buffers.
- Detach batches, diagnostics and mutable snapshot data with explicit deep copies
  so returned values cannot mutate cached evidence or another recipient's data.
- Make private-route probe assertions collision-safe and account for Windows'
  directory synchronization behavior in outbox regression tests.

### Security

- Require Go 1.26.10 for source builds and run `govulncheck` in the release
  workflow before building or publishing archives.

## [1.4.0] - 2026-10-20

This release adds deeper Cloudflare support across HTTP evidence, Tunnel
connectors, API reads, DNS and maps, and a Services card mode shared with monitor
streams. See the [Cloudflare configuration guide](docs/CLOUDFLARE.md) and
[release notes](docs/releases/v1.4.0.md) for setup and upgrade instructions.

### Added

- Services as cards: on the Services tab, `s` shows a card for each service in
  the server cards' uniform grid, under each application's heading. A card
  takes the hue of the server it runs on and says who sees the service from
  where: a row for each observer with a mark for each of its checks, a spark
  of their recent samples, the day's median latency and share up. Then
  tallies of its checks, endpoints and dependencies, the address it is known
  by with the edge in front and its colos, and its memo. `Enter` lays the card
  open with every check, endpoint, colo and cache share, the server it runs
  on, what it depends on and what depends on it, and its runbook. The choice
  is kept as the `service_cards` preference (`--service-cards`).
- `topo monitor stream` and monitor templates gain `service.card`, selected
  by `select.service`, and `services.list`: the service's card as the
  Services tab draws it, each check with its own deadline and up to 40 recent
  samples read from the collector's history once and extended from each
  snapshot, and the services in that tab's order to choose from.
- DNS checks can ask over DNS over TLS or DNS over HTTPS with
  `dns.transport`: empty or `udp` keeps the historical UDP probe with one
  TCP retry for a truncated answer, `tcp` uses TCP directly, `dot` makes a
  verified TLS handshake (usually port 853; `server_name` names the
  certificate, so an IP target such as 1.1.1.1 needs `one.one.one.one` or
  `cloudflare-dns.com`), and `doh` `POST`s the DNS wire question to an
  `https` URL with `Content-Type` and `Accept: application/dns-message`,
  reading at most 64 KiB. The transport changes only how the question
  travels: the same parser, transaction-ID check and `expected_ips`
  assertion decide a pass, and `dns_transport` records `udp`,
  `tcp_fallback`, `tcp`, `dot` or `doh`. DoT and DoH record `tls_ms` and
  `first_byte_ms`. The DoH `POST` is the probe's only non-GET request: it
  carries the same question as the RFC 8484 GET form in its body, with no
  query string and no credential. A wrong transaction ID or an oversized
  DoH body is a monitor error, never proof the resolver is down; a non-200
  status, wrong content type, truncated or negative answer remains a failed
  answer. DoH targets must be `https` URLs without userinfo, query or
  fragment; a `dns` check still cannot traverse a proxy chain. `transport`
  is inside `dns`, so it is part of the measurement contract: an unset field
  leaves every existing hash unchanged, and older collectors and viewers
  reject it, so upgrade them before enabling a transport.
- Cloudflare challenge pages are recorded as `edge_mitigation: challenge`
  from `cf-mitigated`, a closed word that survives a secret request. The
  check's evidence line reads, for example, *Cloudflare NRT · cache DYNAMIC ·
  challenge*, and an incident on that request says the origin was not asked.
  Incidents for `520`, `525`, `526` and `530` now name what the edge reports
  and keep the origin's own state unestablished.
- Cloudflare Access login redirects are recorded as `edge_access: login_redirect`
  from a `302`, `303` or `307` redirect whose `Location` host ends in
  `.cloudflareaccess.com`, a closed word that survives a secret request. The
  redirect's URL, key ID and target are not kept. When the response names
  Cloudflare, the check's evidence line reads, for example, *Cloudflare NRT ·
  cache DYNAMIC · Access login*, and an incident on that request says Access
  asked for a login and the origin was not asked.
- Cloudflare's `Server-Timing` `cfL4` entry is recorded as the edge's own
  reading of the connection (TCP or QUIC) that carried the response:
  `edge_rtt_ms` and `edge_min_rtt_ms`, read as microseconds from the live
  capture and recorded in whole milliseconds rounded to the nearest,
  `edge_retrans` and `edge_lost` counts, and the closed word `edge_proto`
  (`TCP` or `QUIC`). These are numbers and one closed word, so they survive a
  secret request; the entry's connection identifier and byte counters are
  never kept. When the entry carries an rtt, the check's evidence line reads,
  for example, *Cloudflare NRT · cache DYNAMIC · edge rtt 2 ms*, with a
  reading below a millisecond shown as `<1 ms`. The entry is optional; the
  spot checks behind the capture found no `cfL4` on several Cloudflare hosts,
  so it appears not to be on every zone.
- Cloudflare `/cdn-cgi/trace` bodies are read by HTTP checks with
  `exit_format: "trace"`: `trace_warp`, `trace_gateway`, `trace_http` and
  `trace_tls` only in their closed vocabularies, `trace_colo` and `trace_loc`
  only by their three- and two-letter shapes, and `exit_ip` from the `ip=`
  line even without `expected_exit`. A body is a trace only when both `fl` and
  `colo` are present, and a body over 4 KiB is not read as one, neither its
  facts nor that `exit_ip`. `expected_trace` requires `exit_format: "trace"`,
  must declare warp, gateway or colos, and reports `warp_mismatch`,
  `gateway_mismatch` or `colo_mismatch` as `policy` stage `trace_assert`; a
  line the trace did not report is not a violation, and a body that is not a
  trace says so in the passing detail. On a request that carries a secret the
  closed words survive while the colo, the client country and the address are
  withheld, and a declared colo list still compares. The Observers tab shows an
  observer's newest fresh trace sample as `Trace: WARP on · Gateway off
  (reported, not verified)`. `expected_trace` is part of the measurement
  contract: older collectors and viewers reject it, so upgrade them before
  enabling it.
- HTTP checks can send request headers whose values come from the worker's
  environment. `request_header_envs` maps a header name to a variable name,
  for example `{"CF-Access-Client-Id": "CF_ACCESS_ID",
  "CF-Access-Client-Secret": "CF_ACCESS_SECRET"}` for a Cloudflare Access
  service token, so no secret is written in the topology. Header names follow
  `request_headers`; `Authorization` and `Cookie` stay prohibited and a name
  may not appear in both maps. A nonempty map counts as a credential, so plain
  `http` is refused except direct literal loopback; a missing or empty variable
  is a monitor error before anything is sent, a resolved value with a control
  byte is a local `invalid_http_headers` error before anything is sent, and
  every such request is withheld as `peer_text: withheld_secret_request`.
  `Host` is allowed and is set only after its resolved value is checked as an
  outbound host or `host:port`. Only `http` checks accept the map; another kind
  that takes HTTP options refuses it. The editor has a "Secret request headers"
  list of comma-separated `Name=ENV_VAR` entries, and the map is part of the
  measurement contract (its variable names, not their values): older
  collectors and viewers reject the field, so upgrade them before adding it to
  a topology.
- Rollups keep what the edge said, not only outcomes and duration.
  `first_byte` splits passing samples' first-byte time into `cached` (`HIT`,
  `REVALIDATED`, `STALE`, `UPDATING`) and `origin` (`MISS`, `EXPIRED`,
  `BYPASS`, `DYNAMIC`) answers, each with `samples`, `p50_ms`, `p95_ms` and
  `max_ms`; a status outside both lists stays unclassified and is not
  reported. `pops` counts the colo every response named, at most sixteen named
  codes plus `other`, and `cache` counts the cache status of every edge answer
  from the closed vocabulary, absent or unknown as `other`; checks no edge
  answered (TCP, TLS, DNS, ICMP, host) carry no `cache`. Vercel's reported region is not a
  physical colo, so it contributes no `pops` count while its first-byte time
  and cache status still count. The TUI's check evidence adds the
  last day's `first byte cached p50 40 ms · p95 90 ms · origin p50 600 ms ·
  p95 700 ms · 24h` and `colos LAX 92% · HKG 8% · 24h`; the Servers cards order their
  *ANSWERED FROM* colos by the day's share (`LAX 92%`) and fall back to fresh
  response headers when no rollups are kept yet, with the card's JSON saying
  which through `pops_from_rollups`; the Observers rows add
  `Edge colos seen (24h): LAX 92% · HKG 8%`. The fields are additive schema 1
  rollup keys (`fb`, `p`, `c`): an old checkpoint reads as no first-byte time
  and no label counts, and an old binary ignores the keys it does not know, so
  no upgrade is needed to keep a collector running, though only an upgraded
  viewer shows them.
- HTTP checks can declare `expected_pops`, at most sixteen distinct colos of
  exactly three uppercase letters. After the ordinary assertions and
  `expected_exit` pass, and before `expected_trace`, a response whose edge
  named a colo outside the list is a `policy` sample (stage `edge_assert`,
  code `pop_mismatch`), never a failed request; a response with no colo,
  including one withheld on a secret request, is not a violation, and
  Vercel's region is not a physical colo. The editor takes the list as
  comma-separated codes. The list is part of the measurement contract, so
  changing it starts a new history. Older collectors and viewers reject the
  field, so upgrade them before using it.
- A new check kind, `cloudflared`, reads a Cloudflare Tunnel connector's own
  report from the local metrics listener that cloudflared runs beside the
  tunnel. It reads only `GET /ready` (4 KiB of JSON, `secure.JSONResponseBudget`)
  and `GET /metrics` (1 MiB of Prometheus text, seven allowlisted families) and
  sends no credential. `ready_connections`, `connector_id` (UUID shape only,
  lowercased), `ha_connections`, `connection_colos` (at most eight sorted
  lowercase tokens), `requests_total`, `request_errors`, `active_streams` and
  `connector_version` are reported facts, at most eight. `/ready` decides the
  outcome first. A 503, a body `status` other than 200 or zero ready
  connections is `fail` / `tunnel_not_ready` — the connector's own statement
  that it has no edge session. A refused or timed-out listener, a non-JSON or
  oversized `/ready`, and, only when `/ready` reported a session, an
  unreadable `/metrics` or one with no known family are monitor `error` /
  `metrics_unavailable`, never an outage. When `/ready` says there is no
  session, `/metrics` supplies best-effort facts and its failure is ignored:
  a full process stop closes the listener and is `metrics_unavailable`, while
  `tunnel_not_ready` is a live connector with no session. Otherwise the sample
  is `ok` with the detail `tunnel established; service behind it unproven`.
  `adapter.cloudflared.expected_connections` (1..8) and `expected_colos` (at
  most eight 3..8-character tokens, compared without case) are policy, with
  codes `connections_below_expected` and `colo_outside_expected`;
  `connection_colos` shows at most eight, but `expected_colos` is compared
  against every distinct colo the connector reports; zero ready
  sessions stays `fail`, one or more below the declaration is policy, and an
  absent family is not a violation. The target must be a literal loopback
  address (`127.0.0.0/8` or `::1`; commonly `http://127.0.0.1:PORT`,
  `http://[::1]:PORT`, or `https` with `ca_file`): `path`, `bind_ip`,
  `resolve_ip`, host names including `localhost`, proxies and credentials are
  refused, and no environment proxy applies. A public topology strips the
  `ca_file` path, so validation restores a non-secret placeholder on its owned
  copy for an https listener; the real path never reappears. Run the worker on
  the same host as the connector. Counters accept Prometheus decimal and
  exponent forms whose value is integral (`1.0`, `1e+06`) within the
  eighteen-digit bound. The connector version bound is 24 token characters
  because the observed `2026.10.0` is nine. Evidence is reported, path
  evidence is `local_connector_runtime_reported`, and a fresh local connector
  report decides the state of the link it is attached to after the failure
  threshold while the service behind it stays unproven, and the link then
  reads `reported` rather than `inferred`; a failed connector read leaves the
  link unknown. A `cloudflare` API check on the same link is shown beside the
  connector's report and does not vote on the link while a fresh connector
  report decides it. The nested options are part of the measurement
  contract; older collectors and viewers reject the new field, so upgrade them
  before adding it to a topology. Tested against cloudflared 2026.10.0 with a
  fake, unregistered tunnel; the 200-ready and connected-metrics shapes are
  documented, not observed, and their fixtures say so.
- A new check kind, `cloudflare`, reads three things only Cloudflare knows with
  a least-privilege API token from `adapter.token_env`: a Tunnel's status and
  connections (`tunnel_status`, `tunnel_connections`,
  `tunnel_pending_reconnect`, `tunnel_colos`, `connector_versions`,
  `oldest_connection_at`); whether a DNS record still exists, is still proxied
  and still matches the declared content (`record_count`, `record_proxied`,
  `record_ttl`, `record_content_matches`, with the record's content compared in
  memory and never stored; an absent/null result or a partial
  `result_info.total_count` page is a monitor error, while an explicit empty
  list is `record_missing` policy); and a load balancer pool's origin health from
  Cloudflare's own monitors (`pool_healthy`, `origins_healthy`,
  `origins_total`, `regions_reporting`, and `origin.<declared>` only for
  declared origins; an absent declared origin is policy but gets no invented
  verdict). A tunnel that reads `healthy` or `degraded` with an empty
  connections array, which Cloudflare has deprecated, is treated as not
  reported: the five connection facts are absent, neither connection policy
  can fire and the detail says so, while an empty array beside `down` or
  `inactive` is a count of zero. The DNS content comparison is canonical: `A`
  and `AAAA` content is compared as parsed addresses and `CNAME` content
  without case or a trailing dot. Each check makes exactly one allowlisted
  `GET` and never sends a request to the zone's own hostname; the token goes
  only to the API. The target defaults to
  `https://api.cloudflare.com/client/v4`; outside a literal loopback test
  address it must be exactly that host and base path, so the bearer token cannot
  go to a lookalike service, and `interval_seconds` must be at least 60,
  explicit path proxies are allowed, redirects are not followed and ambient
  proxies do not apply. `adapter.cloudflare` selects `read: tunnel`,
  `dns_record` or `pool` and carries the identifiers and declared expectations
  that read needs; a field belonging to another read is refused. Declared
  expectations are policy, not failure: `connections_below_expected`,
  `colo_outside_expected`, `record_missing`, `record_not_proxied`,
  `record_proxied_unexpectedly`, `record_content_mismatch` and
  `origins_below_expected`. A missing token is a local setup error
  (`cloudflare_token_missing`, nothing sent); a connect error or timeout, a
  401/403, a 429 or any other non-200 (code `http_<status>`), an oversized
  body and a malformed response are monitor `error`s at stage `adapter_api`,
  never target outages; HTTP 200 with `success: false` is also a monitor
  error. `tunnel_colos` is capped for the fact, but `colo_outside_expected`
  compares every distinct reported colo, and a value or list member equal to,
  or a run of at least eight bytes of, the bearer token is dropped even when
  its shape is one the adapter reports. Every response is credential-bearing, so only closed words, numbers,
  shape-validated tokens and declared comparisons leave the adapter, and
  `peer_text: withheld_secret_request` marks the sample. `topo edit` authors
  the kind: token variable, read, the read's identifiers and its declared
  expectations; switching reads clears the other read's fields. The token scopes are
  Account › Cloudflare Tunnel › Read, Zone › DNS › Read and Account › Load
  Balancing: Monitors and Pools › Read, nothing else. The fixtures are synthetic
  and document-based: no live account or token was used, so the acceptance
  record names what a real account must still confirm. The nested options are
  part of the measurement contract and the field is new, so upgrade collectors
  and all viewers before adding it to a topology.
- Topology maps place the Cloudflare colo a check reached. A new generated
  table in `internal/view/colos.go` holds Cloudflare's own public location
  list (`https://speed.cloudflare.com/locations`; 309 locations, fetched
  2026-10-19, input SHA-256
  `6fd21425b338fc8ff37da26f1e1031bf528c537dc695db59e3e24dab64d898b2`), written
  by `scripts/gen_colos.py` from a saved capture (`--input`) and verified
  without writing (`--check`); `ColoAt` and `AllColos` read it and no request is
  made at runtime. The preview's Cloudflare nearest-colo choice now uses all
  309 real locations instead of seven stand-ins, keeping the mainland `LAX`
  override and Vercel's own lowercase region table. On every map, a fresh
  check whose endpoint reaches an edge and whose last sample says
  `edge_pop` adds one pseudo-node per (edge, colo) at the colo's coordinates,
  named by its code and placed by its city, with one hop from the observer
  node that saw it and one to the edge pill. The node and its hops claim what
  the check claims: carried when it passed, and only affected when it failed,
  because a failing request does not establish that the colo is down; a stale,
  paused, cached or withheld sample, a malformed code, a non-Cloudflare
  provider and Vercel's non-colo region place nothing. A well-formed code the
  list does not know floats unplaced (in the geo map's tray, as a pill on the
  virtual map). A map draws at most 32 colo pseudo-nodes, and they
  count against its existing node and edge budgets; the nodes are kind `colo`
  and the hops a new `colo` edge kind in `topo show map` and its JSON. A short
  virtual map now splits places into more columns, down to the narrowest card,
  instead of stacking cards past its height. A reported colo is drawn hollow,
  `◌`, in a muted green when it carried a response and in the unknown color
  when nothing backs it, never as a server's filled `●`, since it is where the
  edge said it answered from and not something a check measured; the legend
  adds `◌ reported colo` and `┈ reported colo` while one is drawn. A site or
  card is named by the place of its declared nodes, and a colo's city names it
  only when no declared node there names a place, so a plate no longer reads
  "Frankfurt · Frankfurt-am-Main". The preview's recorded tours change because
  its colos are now real places.
- `examples/cloudflare.json` is a worked Cloudflare zone written with
  documentation hostnames and address ranges. It declares a proxied origin
  with its own direct check (`resolve_ip` pins the dial address while Host and
  SNI keep the zone name), a Worker and a Pages site on the edge, a home
  dashboard reached both through a Tunnel and on the box, a `cloudflared`
  check attached to the Tunnel's link, a trace check per observer, a DoT check
  against `1.1.1.1` with `server_name: one.one.one.one`, an
  Access-protected hostname whose service token comes from
  `request_header_envs` (variable names only), and `expected_pops` on the
  edge check. The three Cloudflare API reads are named in the edge's `notes`
  with their `CF_API_TOKEN` variable rather than enabled, because the example
  holds no token or account; the tunnel read may carry the Tunnel's link, where
  it is shown beside the connector's report and never decides the link. Of its
  public hosts only the trace target
  (`https://www.cloudflare.com/cdn-cgi/trace`) and the `1.1.1.1` DoT resolver
  are real; the `example.com` hosts are placeholders to replace. `topo validate` accepts it, and `scripts/verify.py`
  validates it with the other shipped examples.
- The preview gains three Cloudflare failures, each implemented as a change
  in the simulated evidence and judged by the unchanged engine.
  `edge-challenge` answers the relay observer's requests for the fronted site
  with `403` and `edge_mitigation: challenge`, so the evidence line names the
  challenge and the incident says the origin was not asked.
  `edge-rerouted` carries the relay's answers from `LAX` while its `r-app-web`
  check declares colos near it, which each language's catalog names
  (`scenario.edge-rerouted.pops`: `SJC` and `SEA`, `KIX` and `NRT`, or `HKG`
  and `NRT`): the request succeeds, the check reads degraded, and a policy
  incident opens instead of an outage, saying which colo answered and which
  were declared. `tunnel-down` adds a Tunnel from
  the built-in home box with a `cloudflared` check on its link: the connector
  reports no ready edge session, the link reads down, the public hostname gets
  `530` from the edge and proves only the edge, and the loopback check still
  proves the dashboard. `topo preview scenarios` lists all three in every
  language. The preview topology also gains a `cf-trace` endpoint for
  `https://www.cloudflare.com/cdn-cgi/trace` and three trace checks against it,
  for the Windows, MacBook and relay observers, so the Observers tab shows
  `Trace: WARP on · Gateway off (reported, not verified)` for each of them.
- A Servers card's link row carries the link's state glyph when it is down
  (`✖`) or degraded (`▲`) and what the link's own fresh checks report:
  `→ Cloudflare   cloudflared · 4 sessions · hkg,sin`, or
  `✖ → Cloudflare   cloudflared · no ready session`, with
  `· API 4 connections healthy` appended when a `cloudflare` API read names the
  same link. Both readings are shown and neither is the link's reason; a row
  too narrow for all of it drops the transport word first. In
  `topo show servers --json` each link gains `health` and `reading`.
- `topo edit` authors `adapter.cloudflared`: the check form takes the expected
  connections (1..8) and expected colos of a `cloudflared` check and clears
  both when the kind is anything else.
- An incident's details no longer print an "Other-observer response evidence"
  or a hypothesis section with nothing under it. A policy incident, which has
  no failed request to contrast with another observer's, is headed "Policy
  (not an outage)" instead of "Hypothesis (not a finding)" and shows each
  sample's detail with what it was held to: `(answered from LAX; declared SJC,
  SEA)` for `pop_mismatch` and `colo_mismatch`, `(WARP off; declared on)` for
  `warp_mismatch` and `gateway_mismatch`. Any other policy, `exit_mismatch`
  included, keeps the sample's sentence alone.

### Changed

- A configured `Host` request header, literal or read through
  `request_header_envs`, must now be a value Go will actually send: a reg-name,
  an IPv4 address or a bracketed IPv6 literal, with an optional 1..65535 port.
  Raw non-ASCII/IDN, quotes, angle brackets, unbalanced brackets, unbracketed
  IPv6 and empty or out-of-range ports are refused. A literal `Host` value that
  the transport would reject or drop is no longer accepted silently; dial and
  SNI behavior is unchanged.
- Environment variable names for `bearer_env` and `request_header_envs` now
  follow one rule: 1..128 bytes with no `=`, comma, whitespace, control or bidi
  character. A `bearer_env` spelling that older releases accepted and that
  violates this rule is now refused; check existing topologies before
  upgrading.
- A configured `User-Agent` request header, literal or environment-backed, now
  wins over Topophany's default instead of being silently overwritten.
- A `dns` check that receives a message whose transaction ID is not the
  question's now reports the monitor error `unexpected_transaction_id` (stage
  `dns_answer`, local) on the existing UDP path as on the new transports,
  where it used to report the failed check `invalid_or_negative_answer`.
- The rollup reader refuses, as corrupt, a checkpoint with a timestamp after
  the end of the year 9999, as it would otherwise make every snapshot that
  included it fail to encode.

### Fixed

- Service cards build without device-simulation support. TUI layout tests reuse
  immutable fixtures, and macOS race tests have enough time to finish on shared
  CI runners.
- Cross-observer response evidence is indexed once per snapshot and bounded,
  avoiding repeated scans when many checks share an endpoint.

### Security

- Release archives are built in fresh directories from an immutable checkout.
  Publication verifies the exact file and archive-member manifests, rejects
  unexpected or nonregular entries, and verifies that the tag still names the
  source that was built before uploading downloads.
- Credential-bearing probe and adapter responses withhold numeric values that
  reflect a secret, as well as free text. Rich monitor rows share an output
  budget enforced before repeated values are retained.

## [1.3.0] - 2026-10-19

### Added

- `topo monitor stream` feeds desktop widgets and menu-bar apps without
  configuration files or locks. It reads one NDJSON request per line on stdin
  and writes a bounded frame per line on stdout; a new request replaces the
  running one. `--capabilities` describes the protocol, sources and metric
  catalog, `--profiles` lists saved client profiles (names, projects and the
  active flag only, never tokens or URLs), and `source: "preview"` streams the
  synthetic scenarios without credentials. Programs embedding Topophany can
  drive the same stream through a cancellable function that takes request
  lines and a frame callback.
- Structured monitor metrics reuse the terminal's views: `summary.verdict`,
  `observers.verdicts`, `attention.list`, `applications.list`, `servers.list`
  and `server.card`. Templates add `status`, `list` and `card` visuals,
  attention limits and node selectors. `servers.list` returns up to 256 nodes
  in the Servers tab's order, ready to pick a `server.card` from.
  [`examples/monitor-stream-preview.json`](examples/monitor-stream-preview.json)
  requests all six.
- Memory budgets for `serve` and `worker`. `--memory-limit` sets the Go
  runtime's soft target, which now defaults to 128 MiB for a collector and
  64 MiB for a worker; an existing `GOMEMLIMIT` still takes precedence.
  `--memory-max` enforces a Linux cgroup v2 hard cap and fails startup rather
  than run without one. The supplied and generated systemd units set
  `MemoryMax` (256M for a collector, 128M for a worker) with swap disabled.
  Both roles log `memory_since_start` at startup and with their minute-level
  I/O reports. [Memory budgets](docs/MEMORY.md) explains the controls, the
  measurements behind the defaults and how to tell retained data from a leak.

### Changed

- Collectors use much less memory. Checkpoints are read and written as
  streams instead of whole-file buffers, fingerprints are stored compactly,
  repeated strings are shared, worker delivery copies are bounded and rollups
  release their arrays after eviction. Retention and the checkpoint format
  are unchanged.
- Views index topology, ownership and evidence once per frame and share the
  indexes across rows and tabs, so large fleets no longer rebuild the
  topology for every server card or list row.
- macOS host checks read CPU, memory and process counters through libSystem
  inside the process, sampling CPU over 200 ms. They no longer run system
  commands, and only configured process names have their RSS read.
- `topo install` installs only the bundle that was reviewed. `--dry-run`
  prints a `bundle_sha256` receipt; installing requires it back as
  `--bundle-sha256`, and any change to the payloads, checksums, service
  definition or options invalidates it. Privileged installations also refuse
  bundle paths that other accounts can write to.
- The mark is drawn matte instead of glossy: its land is a halftone of the
  light falling on it, its night side stays dark under a thin atmosphere,
  and the arc tapers like light leaving Shanghai for Honolulu, where it glows
  at dusk. The Apple touch icon shows the whole globe among a few stars; the
  favicon keeps coarse dots and a broad arc so it still reads at 16 pixels.

### Fixed

- Server cards keep their identity and other evidence when host readings
  expire. Resource numbers and nested observer verdicts have independent
  deadlines, readers keep the original sample times and apply those
  deadlines after decoding JSON too, and service judgments include
  transitive dependencies.
- Structured monitor rows keep each piece of evidence's own deadline and hide
  expired values until the collector judges again. Nested health and
  freshness changes publish at once instead of waiting for the display
  cadence, and application services keep topology order.
- Scoped file and attached panels keep the selected observer's verdict and
  global heartbeat liveness without inventing other observers' judgments.
  Summary and delivery rows use the documented health vocabulary, and panel
  readers apply the five-minute future-clock bound.
- A stream replaces its request only after the new one's first read
  succeeds, and keeps the old source running when the answer comes from a
  different project. Each snapshot scope is pinned to one collector
  generation. Stream callbacks own every frame they receive, and oversized
  error identifiers or messages can no longer end the stream.
- Incident evaluation and webhook hand-off are serialized with configuration
  reloads, so a reload cannot swap the topology or notification settings
  between an incident being recorded and its alert being queued. Alerts that
  have expired are dropped instead of sent after a restart or retry backoff.
- Location inheritance follows the full parent chain regardless of
  declaration order.
- Collectors reject rollup spans that overflow, and workers refuse an outbox
  path that is not a directory (Windows lists a regular file as empty) or a
  queue record that is not a regular file. Response encoding failures are
  reported instead of ignored.
- When macOS returns cached CPU ticks, host checks wait a second and sample
  a fresh 200 ms window, up to three times within the check deadline,
  instead of reporting zero CPU use.
- TUI fuzz tests no longer write the user's saved preferences: a random
  input could reach Settings' Save action and overwrite real startup defaults
  during local verification. Upgrade acceptance now checks that preferences
  survive installation and rollback unchanged.

### Security

- Bundle installation is bound to the reviewed receipt (see Changed), and
  a bundle whose observer its topology does not declare is rejected.
- Monitor output paths that reach a protected configuration or data
  directory through a symlinked parent are refused even before the file
  exists, as with macOS's `/var` → `/private/var`.
- A Windows service data directory outside the install directory gets the
  same private ACL, verified before the service starts; it previously
  inherited its parent's permissions.
- The site development bridge picks a random origin, needs an explicit
  opt-in for a fixed port, and bounds connections and header deadlines.

## [1.2.0] - 2026-10-18

### Added

- The geographic map cycles through Cards, Locations and Points with `p` in
  the terminal and browser viewer. Locations shows compact city labels, or
  coordinates for unnamed sites, with collision-aware placement, selection
  highlighting and clickable labels. The map's label and zoom controls are
  independent and remain inside narrow panels.
- Mihomo supports up to 4096 names across the combined proxy/provider graph,
  4096 members per group/provider and 8 MiB inventory responses. Built-in names
  and groups count toward the graph limit; over-capacity inventories return
  explicit monitor errors. Other JSON and history budgets remain bounded.
- Mihomo can compare rotating provider membership against a fresh, independent
  names-only JSON publication, with separate publisher credentials and TLS
  settings. Fixed expectations remain strict; unavailable references are
  monitor errors and publications changing during sampling are unknown.
- Optional Mihomo TUN expectations and ordered/set provider comparisons report
  bounded counts of missing, extra and reordered members.
- Mihomo `provider_reference.settle_seconds` (0..3600, default 120, 0 disables)
  allows a bounded loading window after publication. Samples retain
  `policy / provider_membership_mismatch` with
  `provider_reference_settling: true`; opening an incident requires both the
  elapsed window and its equivalent consecutive policy samples. Repeated
  publications cannot postpone confirmation forever. Separate publisher,
  controller-load and sample times remain within the 32-fact budget.
- Optional `policy_threshold` requires 1..20 consecutive ordinary policy
  violations. Omitted or zero retains the immediate 1.1.1 behavior;
  failure/recovery thresholds and provider-loading settlement are independent.
- WireGuard supports AllowedIPs and keepalive expectations through public-field
  commands, without reading private or preshared keys.
- Opt-in bounded Tailscale DISCO/TSMP diagnostics retain reported evidence and
  do not imply application reachability. Default collection stays passive;
  versioned synthetic fixtures document supported response formats.
- Collector snapshots and CLI/terminal/browser status show TLS handshake
  failures in a rolling 30-minute ingress window. Source tracking is bounded
  and identifies truncated counts. Sources are pseudonymous by default;
  `--expose-ingress-sources` opts into raw addresses. Failed delivery makes
  affected evidence stale rather than declaring the monitored target down.
- Opt-in real-core capacity and isolated native installation acceptance tools
  record program hashes, versions and scope, including upgrade, credential
  preservation, authenticated rollback and reinstallation.

### Changed

- Cloudflare, Vercel, CloudFront, Fastly and Netlify header parsing lives in
  separate platform adapter packages. Existing HTTP configuration, first-match
  precedence, private-response filtering and evidence attribution are preserved;
  intentional Vercel location corrections are listed below.
- Mihomo, Tailscale and WireGuard implementations, validators and tests live in
  dedicated adapter packages with contribution and evidence notes.
- Tailscale peer identity resolution is deterministic and reports ambiguous
  identities, backend states, missing online state and peer relay separately.
- Software details distinguish runtime reads, policy assertions and unmeasured
  application paths; stale reports do not become current evidence.

### Fixed

- Snapshot readers try healthy collector failover first, then wait one second
  and retry once if every endpoint fails and one explicitly reports
  `429 snapshot_busy`. This reduces offline/cached flicker when LAN and overlay
  addresses share a busy collector. Cancellation, persistent failures and
  authentication/response-validation errors retain their normal behavior.
- Vercel request IDs use the real colon-separated routing format. Receiving
  and ordered routing regions are bounded and displayed as regions, without
  inferring PoPs or arbitrary function execution locations. Historical labels
  remain role-unverified and private requests withhold every new location key.
  The frozen pre-split oracle is unchanged; intentional differences have
  separate regression coverage.
- Mihomo resolves subscription-only selected nodes from the existing read-only
  provider inventory when `/proxies` omits them. Ambiguous names and cycles
  remain unresolved rather than claiming a verified route.
- Mihomo controller load times use `provider_updated_at.<name>` independently
  of membership facts. Overlong keys are omitted so long provider names cannot
  refuse a batch or overwrite membership evidence.
- Reloading policy confirmation or provider settlement re-evaluates retained
  and queued evidence while preserving measurement contracts and history.
  Provider settlement applies only to membership mismatches, requires elapsed
  time as well as samples, and conservatively waits for missing load evidence
  within the current publication window.
- Requests answered through an edge no longer automatically blame the origin.
  The explanation retains the failed assertion and unlocated failure layer;
  status codes provide candidate explanations, not proof of root cause.
  HTTP 524 is described as the platform's reported read/write timeout after an
  origin connection, separately from an unreachable origin.
- The Checks tab's `UP 24H` shows measured coverage for partial windows
  (`98.9% · 3h`, or `<1m`), rather than presenting partial evidence as a full day.
- Snapshot-backed CLI and terminal/browser views pin history to their
  collector, public revision and check contract. Source/revision switches
  clear cached charts and discard superseded background history.
- `topo version` honors injected release versions and otherwise identifies
  a tagged build, `dev-<revision>[-dirty]` or `devel` from available build
  information. Release builds inject and verify their own tag instead of
  claiming the last release or exposing a toolchain pseudo-version.
- A Tailscale CLI diagnostic's exact timeout response for its fixed peer is
  `unknown / diagnostic_response: timed_out`; other nonzero exits remain
  monitor errors. Missing `LastSeen` is reported as unavailable rather than
  a fabricated year-zero timestamp.
- Ingress counts include failures across the actual rolling window, parse
  only the HTTP server's own handshake log lines, and keep log handling off
  the connection path. Collector heartbeats tolerate a backward clock step.
- Authenticated installer rollback restores executable backups larger than
  1 MiB within the existing 64 MiB per-file limit.
- The real process/PTY acceptance harness refuses non-Linux hosts before
  starting processes, opening listeners or saving credentials.

### Security

- Privileged Unix installation rejects intermediate symlinks before mutations,
  creates directories through verified handles and changes ownership through
  an open descriptor.
- Windows bundle/installation directories replace and verify their entire
  DACL, including existing children, before writing sensitive payloads.
  Unrelated grants, untrusted owners and reparse points are refused or removed.
- Manual rollback requires installer-created private backups under the same
  installation, trusted ownership and authenticated metadata/payload hashes.
  Unsigned legacy backups are refused before service or file changes.
- Source identities and ZIP/tar exports reject nonportable paths and aliases
  under case and Unicode normalization, preventing cross-platform extraction
  from replacing reviewed files.
- Alert queues enforce presentation, encoded-event and persistence budgets so
  saved queues remain bounded and readable on restart.
- Local browser-viewer bootstrap uses a short-lived, one-time launch capability
  and a per-process session token; saved collector tokens stay out of the
  browser. Ingress source pseudonyms use a per-collector secret.
- Prerelease tags are marked as prereleases and do not replace the latest
  stable download. Release publication verifies immutable build artifacts and
  that the tag still resolves to the exact source commit.

## [1.1.1] - 2026-10-16

### Fixed

- Probe durations retain Go's monotonic clock across system clock adjustments,
  while sample timestamps remain UTC. Previously, a backward clock step could
  produce negative durations and a forward step could exceed the check's
  timeout budget, causing valid observations to be rejected with
  `observation_budget_exceeded` and quarantined. Invalid samples could also
  delay delivery of valid observations in the same batch.

## [1.1.0] - 2026-10-16

### Security

- Managed logs reject existing POSIX log files and backups with group or
  other permissions before writing; newly created and rotated logs stay private.
- Temporary client tokens use `--token-stdin` or `--credential-file` instead
  of the removed `--credential TOKEN` argument. Multi-server overrides still
  require an explicit server.
- Alert webhooks limit response headers to 32 KiB, including HTTP/2 responses.
- Release documentation is staged from an explicit list of regular Git blobs
  at the built commit, without following checkout symlinks into the runner.
- Snapshots admitted before a configuration reload return a retryable 503
  if capture has not completed, keeping storage and incident metadata within
  the request's authorization generation.

### Added

- Persistent monitor panels through `topo monitor`: typed metric catalogs,
  imported templates, transactional configuration, previews and exports,
  authenticated client panels, and panels attached to collectors or workers.
  Panels publish bounded JSON snapshots under an exclusive writer lock,
  expire stale evidence and expose lifecycle state after shutdown or a crash.
  Includes desktop, worker and compact RunCat example templates.
- Managed service logs with bounded rotation and retention, repeated-error
  summaries and recovery messages. Delivery failures retain actionable
  contract-mismatch diagnostics without logging arbitrary upstream text.
- Process-local I/O counters for journal, checkpoint, queue and sync work,
  available to monitor panels without changing durable-write guarantees.
- A browser WebAssembly preview at `/demo/` and an authenticated collector
  viewer at `/app/`, in English, Chinese and Japanese. The collector's
  `--web-origin` enables an exact-origin, read-only CORS policy; viewer
  credentials stay in the tab's worker session. Requests and input queues
  have deadlines and budgets, and quitting cancels pending reads.
- `--device` on `topo` and `topo preview` lays the TUI out for the screen
  of a 6.5-inch iPhone, of iPhone Duo shut, open, part folded like a
  book, upright and part folded like a laptop, or of a 13-inch iPad, and
  simulates that screen in the middle of the terminal. A phone gets one
  column with the tabs in a sidebar rail and rows a finger tall; iPhone
  Duo opened puts what is looked at and what is touched on either side of
  the crease, the map above the hinge and the events below it when
  upright, with the tabs in the system's column or along the foot; an
  iPad keeps the terminal's layouts with tabs and a toolbar for fingers.
  A pinch magnifies the map and a drag moves around it, and `⋯` offers
  every action the footer would. `[` and `]` fold through the poses, `\`
  picks the next device, and the mouse stands in for a finger. See
  docs/DEVICES.md. Built with `-tags nodevice` (`make build TAGS=nodevice`),
  `topo` leaves all of it out and has no `--device`.
- `scripts/device_shots.py` pictures each device's screen in its frame,
  iPhone Duo's part-folded poses rendered in 3D with the iPhone Duo
  simulator kit.
- On a phone, the website's live demo and collector viewer take the phone
  layout at the page's own width instead of scrolling an 80-column
  terminal sideways: the tabs in a sidebar rail, controls a finger tall,
  and taps, swipes and two-finger pinches read as on a device. A page
  wider than a phone is the terminal again. `--device fit` lays out the
  terminal itself for touch the same way.
- topophany.com has a section on the iPhone Duo layouts: the laptop pose
  as `topo preview --device duo-laptop` draws it, every pose side by side,
  and the commands to try the simulator. There is no iPhone app; the
  section says so. Each README links the live demo in its language.

### Changed

- The header, the tab strip, the status line and the key bar draw into the
  span they are given rather than the whole terminal, the key bar's
  actions come from one list a touch screen shares, and a click target can
  be taller than a row. The terminal draws exactly as before.

## [1.0.0] - 2026-10-15

### Added

- `s` on the Servers tab lines the cards up in a grid at one size, each
  saying the same things in the same places: the server's state, a panel
  each for CPU, memory and disk, how many services, addresses and checks it
  has with a mark for each in its state, the address it is known by and the
  first two lines of its memo. In the resources' place, an edge or a
  platform, which has no machine of yours to read, says what it stands in
  front of and where it answered from; a host nothing reads says why (no
  worker runs there, or its worker runs no host check), and one whose
  reading has gone silent says when it was last read. Anything else a
  server lacks keeps its place, empty; `s` again brings back the wall of
  full cards.
- `Enter` on a server lays its card open over the others, which dim behind
  it, in either layout: the memo first, then its resources charted over the
  samples kept with their peaks and limits, everything that runs on it and
  reaches it with nothing cut short, and the checks its state rests on, in
  two columns where the terminal is wide. `↑↓` scroll it; `Enter`, `Esc` or
  a click beside it closes it, and the cards behind keep their place.
- Providers' logos where the terminal's font is a Nerd Font (v3.3 or
  later): `--nerd-font-icons`, or Nerd Font icons in Settings. Nodes run by
  Cloudflare, Vercel, Supabase, GCP, Azure, AWS, Oracle, Deno Deploy,
  Linode or DigitalOcean wear their provider's logo before their names, on
  their cards, on the maps and in the Services tab's HOST column, and so
  does an edge wherever it is named in front of a service or an address.
  Off by default, as a terminal cannot tell which font it draws with. The
  homepage and the guide's stills draw the logos from their outlines.
- Add persistent startup display preferences, CLI overrides and `preferences
  show/set/reset`, plus a comma-key TUI settings dialog; application groups
  now start expanded by default.
- Add application and environment organization, collapsible Services/Checks
  groups, persistent browsing scope, shared dependency navigation, and editor
  support without changing measurement contracts.
- Add `topo capacity`, observer storage weights, configurable bounded
  collector state/receipt limits, and authenticated quota visibility before
  migration.
- Expose worker-reported backlog, quarantine, sample ACK, contact and retry
  telemetry in Observers, with bounded heartbeat payloads and closed
  diagnostic codes. A collector that refuses the telemetry (one not yet
  upgraded, or a worker clock running ahead) no longer has the samples
  quarantined with it: the worker resends without telemetry and withholds it
  from that collector for an hour.
- Generate private server/worker/client installation bundles with role-scoped
  enrollment, TLS, native startup definitions, dry-run, read-only enrollment
  verification and rollback.
- Centralize literal addresses with reviewed references and `topo address`;
  preview affected measurements without resetting equivalent history or
  flattening editor source. Bundle creation and address editing remain local
  operations; live routes and proxy policies are untouched.
- Collect Windows host CPU, memory, caller-relative disk capacity and selected
  process working sets through native APIs; validate Windows drive paths on
  any collector OS.
- Before the first snapshot, the TUI shows the globe turning beside the
  wordmark and names the collector it is asking, rather than empty views
  counting zeros. When none answers, the scene goes still and says which
  collector did not answer and why, that it tries again every 3 seconds
  (`r` at once), and what to check.
- Where the topology declares applications, the Overview's services card
  gives each application a line, with a mark for each of its services and
  how many need attention, and lists the services by name under their
  application where it has room, those needing attention first. A click
  opens the application, or the service, in the Services tab.
- The README and the homepage say how to have a coding agent write the
  topology, with a prompt to start from, and how the topology then serves
  as the network's memo and `topo status`, `show` and `history` as its
  quick interface.

### Changed

- Where a card has no room for the edge a service or an address is reached
  through, the edge's logo or its mark (`CF`, `▲`, `DO`, …) stands in for
  its name, whole, rather than the name cut short and the address's state
  pushed off the card.
- Known providers are named in their own spelling, by any of their usual
  names: a node's `provider` of `oracle` or `oci` reads Oracle, `gcp` or
  `google` reads GCP, `digitalocean` reads DigitalOcean. Edges and
  platforms keep their brand colors; a machine you run keeps its group's.

- `Enter` on the Servers tab opens the server's card rather than the
  evidence panel; a node's evidence panel is still on the Topology tab.
- When no collector answers, the error names each one and why it gave no
  answer: connection refused, timed out, a host name not found, a TLS
  certificate from an authority not trusted here or for another name, an
  http:// collector, a busy or failing collector, or a snapshot that is not
  current. `topo status`, `show` and `history` give the same reasons.
- `topo` run without saved credentials points to `topo preview`, `topo init`
  and `topo login` rather than to the file it could not find.
- `topo help` lists the commands by task, getting started first.
- Lists leave out a column in which every row says the same word (every
  check `fresh`, every item an `incident`); it comes back as soon as a row
  differs. On terminals 120 columns or wider the list takes a little more
  than half the width, so names read whole.
- A long label in the evidence panel keeps a short value beside it rather
  than hanging it on the next line, and `true` and `false` read yes and no.
- The Check form shows only the options of the kind chosen; what the others
  hold is kept.
- The Settings dialog explains the focused setting and has buttons like the
  other dialogs: Apply now, Save as startup defaults, Restore defaults.
- Preferences that cannot be read no longer keep the TUI from opening: it
  starts with the built-in defaults and says why. `preferences show` still
  reports the error.
- CLI tables let a column grow to 48 characters before a cell runs on.
- The built-in preview declares six applications (Main site, Serverless,
  Proxy chain, Remote access, Personal cloud, Code & backups), so
  `topo preview` opens Services grouped by application, and its Chinese and
  Japanese maps name every place in their own language.
- A group is named by its application alone where no service or check
  names an environment, rather than "Shop / Unspecified" on every group,
  and the scope line says "by application".
- On a calm Overview the all-clear keeps to a few lines, and the services
  and observers take the room.
- The homepage's replay and the README's animations and still are recorded
  at 200×50 rather than 140×42, so the full maps have room for the world
  and their places' cards; on a wide screen the replay reaches past the
  text column, up to 1600 pixels. The guide's stills keep 140×42.

### Fixed

- Recognize Windows socket errors in collector diagnostics, so connection
  failures report the same actionable reasons as on Linux and macOS.
- Wait for the failed webhook attempt to be persisted before stopping the
  notifier in the restart test, avoiding a scheduling-dependent CI failure.
- The systemd units of server and worker bundles quoted `WorkingDirectory=`
  and `EnvironmentFile=`, which systemd refuses, so installing such a bundle
  on Linux failed and rolled back. A new test checks the unit with
  `systemd-analyze verify` where it is installed.
- `topo preview` said how to switch scenarios before its clock started, so
  the hint was gone before the first frame.
- Editor labels longer than the form's label column ran out of the dialog;
  the Browse menu drew an empty key column; an evidence title too long for
  its panel was cut without an ellipsis.
- A group of one said "1 members", and a group's evidence ran its checks
  and shared dependencies into the note under them.

### Security

- Withhold reflections of private HTTP `Host` overrides from observations
  visible to viewers.
- `topo install` installs the bytes whose checksums it verified, the service
  definition and the topology included, rather than reading the bundle a
  second time, so a bundle changed between the check and the copy cannot
  install anything unreviewed.
- Running as root, `topo install` refuses an installation or data directory
  that someone other than root or the administrator who ran it could
  rearrange; changes ownership only of the file itself, never through a
  symbolic link; refuses a data directory replaced by a link; and only looks
  at the service's writer lock, never creating or changing its owner, so a
  compromised service account cannot turn the next upgrade into root access.
- A bundle's private certificate authority can sign no intermediate
  authority, and the bundle's README says when the collector certificate
  expires and how to replace it.
- The preferences file is read as a bounded regular file, so a FIFO or a
  device in its place cannot hold up the TUI's start.

## [0.9.1] - 2026-10-15

### Fixed

- Retry collector connection admission when concurrent authentication or socket
  closure frees capacity after the initial quota check, even if no pending
  socket remains to evict. New connections no longer receive an intermittent
  EOF despite available capacity; source and global connection limits remain
  enforced.
- Avoid false secret-reflection test failures when a contract digest happens
  to contain a short hexadecimal fragment from the test credential.

## [0.9.0] - 2026-10-15

### Added

- Local host resource checks on Linux and macOS: CPU utilization, physical
  memory, filesystem capacity and inodes, visible process and zombie counts,
  and optional named-process count/RSS. Resource and process-count assertions
  use existing policy incidents; metrics travel through worker buffering,
  collector storage, check details and JSON history. Includes local editor
  fields and a runnable `examples/host-resources.json` topology.
- Host resource preview with deterministic changing counters and localized
  CPU, memory, disk and missing-process scenarios. Preview shares the real
  probe's accounting and threshold evaluation, including incidents and history,
  without reading host resources. The full built-in topology includes resources
  for ten hosts and its router, with per-node failures and worker liveness.
  Servers cards and CLI output show current resource summaries; stale and
  cached readings are withheld.

### Changed

- Servers cards chart CPU, memory and disk usage, recent trends and capacity.
  Host-check evidence and node details chart resource history against configured
  limits instead of probe elapsed time. The TUI reads each host check's recent
  history once and retains newer snapshot samples, up to 240 per check under
  its current contract. Readings that are not current stay faint with their
  values withheld. Preview CPU varies between samples while memory and disk
  drift slowly.
- Host checks briefly share CPU, memory and selected-process aggregates,
  retaining the original sample timestamp and independent disk/assertion results.
  Linux CPU collection reads only the aggregate line instead of the entire
  per-CPU and interrupt table.

### Fixed

- Bound outstanding host filesystem queries across timeouts and reloads so a
  stalled mount cannot block worker shutdown. Ignore Linux procfs `ESRCH` when
  a process exits during a read, and recognize macOS `ps` fallback names for
  processes whose command information is unreadable.
- Bound each late sample's rollup correction to shifting at most 1,024 retained
  boundaries during ingestion and journal recovery. Larger corrections still
  count the sample but mark its time totals incomplete with
  `unreconciled_samples`.
- Apply node, edge and viewport limits to the geographic map as well as the
  network and virtual maps. Geographic label placement shares a bounded work
  budget across all placement attempts; oversized or exhausted layouts show a
  complexity notice while lists and details remain available.

### Security

- Preserve collector authentication capacity when pending sockets fill the
  listener: new arrivals replace the oldest pending socket within the relevant
  quota, and unauthenticated sockets expire after five seconds. Authenticated
  connections leave the source quota and are protected from pending-socket
  eviction, with at most eight sockets per principal and 96 globally under the
  default 128-connection cap, leaving 32 slots for authentication. Existing
  authenticated connections remain usable during anonymous connection floods;
  continuous transport floods can still interrupt clients before authentication.

## [0.8.3] - 2026-10-15

### Security

- Anonymous and rejected HTTP/2 requests now close their connections after
  active streams finish, releasing listener slots as HTTP/1 requests do.
- Plain HTTP checks through authenticated proxies withhold peer-controlled
  text whenever a hop receives a username, password or SSH login, so a proxy
  cannot reflect its private login into observations visible to viewers.
- Custom HTTP request headers, including API keys, require HTTPS or direct
  literal loopback. Invalid header names and values are rejected before
  execution, and incomplete collector snapshots are refused by clients.
- A worker token revoked by a reload can no longer store a sample. A
  collector found a request's token among those in force when the
  request arrived, but judged its batch under the topology in force once
  the body was read, so a worker holding its body open across a reload
  that revoked its token had a sample of the new topology stored; a
  viewer admitted just before a reload could likewise read the topology
  loaded after it. Each request is now served under the tokens and the
  topology it was admitted with, and a batch whose sending straddles a
  reload is refused with `503 topology_reloaded`, for its worker to send
  again and be admitted, or not, under the new ones.
- A response through an edge or proxy no longer proves the nodes behind
  it. A passing HTTP response whose edge said it fetched it from behind,
  with a cache status such as `MISS` or `DYNAMIC`, made the origin read
  up, measured, and stood as evidence against a failure of the origin
  seen from another observer; but those headers are the response's own
  word, which the edge, or anything behind it, can send whatever
  happened, so an edge could hide a dead origin. Such an answer now
  proves the first hop alone, and a node reached only through it reads
  unknown and says why; give it a check that does not go through the
  edge to measure it.
- A query in an HTTP check's URL is kept as the credential it may be.
  The public topology hid it from viewers, yet it was sent over plain
  HTTP to any address, where a bearer token is refused, and a peer that
  was sent one could hand it back in an edge's labels, which every
  viewer reads. Such a check now needs HTTPS or a direct literal
  loopback address, as `bearer_env` does, and its observation keeps
  what the peer answered only in closed words, with
  `peer_text: withheld_secret_request`. A topology with a query in a
  plain `http://` URL to anywhere else is refused; use HTTPS, or move
  the token to `bearer_env`.
- A worker changing its boot ID without end can no longer slow every
  observer's ingestion. Each boot whose receipts expire leaves a floor,
  up to an eighth of its observer's share, 131,072 with one observer by
  default, and every write counted them all, twice, under the lock all
  ingestion waits on, sorting the observer's floors whenever one had to
  go. Each observer's floors are now kept oldest first, so a write
  counts them and sheds the oldest without visiting the rest; with a
  full share of floors, a write prunes them about as fast as with a
  thousand.
- A peer can no longer hold a probe for its whole timeout by streaming
  compressed nothing. HTTP and Mihomo probes asked for gzip and counted
  their body budgets after decoding, so a target or controller sending
  empty gzip members without end kept the check, and a scheduler slot,
  busy until the check timed out, up to five minutes. Probes, workers,
  viewers and webhooks now ask for no compression and decode none, so
  each budget counts what the peer sends and runs out at once. A target
  that compresses its body unasked is checked as sent: `contains` and
  `body_regex` see it compressed.
- `--json` output carries no C1 control. It escaped bidi controls, and
  encoding/json escapes C0 ones, but DEL and the C1 controls, which some
  terminals act on as the escape sequences they stand for, were written
  as a collector sent them, in a check's reason, a history entry or a
  rollup's outcomes. Every control is now written as a `\u` escape,
  which reads back the same, and a rollup whose outcomes are not named
  in plain text is refused.
- Viewers no longer see an SSH hop's login. The topology a collector
  gives viewers withheld a proxy's username, password, identity and
  known-hosts files, but kept `ssh_user`, so `/v1/config` and every
  snapshot named the account the worker logs into the bastion as. It is
  now withheld with the rest, and changing it changes no public
  revision or contract.
- A collector can no longer stop `topo` or the TUI with a sample no
  worker could have sent. A history holding one passing sample that took
  some time and one that took less than none drew its latency bar below
  the lowest height, and the TUI's evidence panel or `topo show` for that
  check crashed. History entries and a snapshot's last samples are now
  held to what a collector holds a worker's samples to, durations and
  timings from zero to the longest a check may take, a known outcome and
  plain-text diagnostics, and refused otherwise, which the evidence says;
  latency bars are drawn whatever durations they are given.

### Added

- Each README shows the TUI in motion, in its own language. It opens
  with an outage arriving on the Overview, the routes it breaks and the
  evidence for one incident, and its terminal UI section shows the
  network from two vantage points and then the three maps. They are
  chapters of the homepage's tour, made into GIFs by `make captures`,
  which now needs ffmpeg as well as a Chromium. The stills it makes keep
  their pixels in a quarter fewer bytes.

### Changed

- Snapshot evaluation reuses an immutable plan per topology generation,
  indexes entity/check relationships and sorts sample references instead
  of copying observations. Configuration cloning no longer serializes JSON.
- CLI commands, virtual-map rendering/layout/routing and rollup
  persistence/query/histogram code are separated into focused files.
- Late samples correct retained time intervals, including after restart.
  Correction boundaries share the rollup budget; missing or ambiguous
  boundaries produce `unreconciled_samples` and an incomplete uptime label.
  Existing checkpoints remain readable.
- The homepage's "The edge answered" panel draws the exchange it is
  about, as a sequence diagram played once as it comes into view: a
  check from Frankfurt asks Cloudflare for the site, the edge cannot
  reach the origin in New York and answers HTTP 521 itself, and below
  each, what that proves of it: the edge up, the origin unproven. "What
  it measures" lists the native probes by layer, each with the evidence
  it leaves and what a pass of it proves; draws a proxy chain as the
  route a check takes through its hops; and says what each adapter reads
  and what each edge is credited with.

### Fixed

- Active unknown or stale checks can no longer produce `ALL CLEAR` or
  `topo status` exit 0. Paused and maintenance checks remain excluded.
- Workers keep sending heartbeats during collector storage backpressure,
  while observations remain queued for retry without false acknowledgements.
- Rollup budgets include active-series metadata and sample boundaries,
  even when there are no historical buckets left to evict.
- A new version of the homepage shows without a forced refresh. Its
  styles, scripts and data were kept for an hour and then served stale
  for a day while they were checked, so a returning visitor got the new
  page with the old styles and scripts. They are now published under
  names that carry a digest of their content, and the data with its
  digest as a query, so a browser keeps them for good and a new page
  names new ones, while the pages themselves are checked on every visit.
  `scripts/build_site.py` writes them from `site/src/css` and
  `site/src/js`.

## [0.8.1] - 2026-10-14

### Changed

- The README and the homepage show the preview where 0.8.0 put it, at
  home in Seattle, Osaka or Chengdu by language. Their stills, the
  homepage's recording and the words beside them still showed the
  network the preview had in Changsha, and the recording's two vantage
  points were observers the preview no longer has, so made again it
  would have shown nothing measured from either. They are captured
  again, and the tour now fails to record, in the tests too, when it
  views an observer the preview lacks. The homepage's vantage panel is
  drawn where each language's preview puts its home, the transit host
  and New York, with its map fitted to them and its arcs and labels laid
  out so that none overlap.
- Topophany has a new mark, the globe of the README's banner in little:
  a dot-matrix Earth lit from the upper left, with an arc of light from
  Shanghai to a place it brings to light across the Pacific. It is the
  homepage's favicon, the mark beside its name and its Apple touch icon,
  and `scripts/icon.py` (`make icon`) draws it from the land mask.

### Fixed

- TLS and HTTPS checks, and https-connect hops, no longer report a
  healthy service as `tls: timeout` on a path that drops large
  ClientHellos. Built with Go 1.26, a probe offered Go's default key
  exchanges, led by the hybrid post-quantum X25519MLKEM768, whose key
  share alone is over a kilobyte; where a path never delivered that
  ClientHello, TCP connected and the handshake went unanswered until the
  check timed out, while curl and OpenSSL reached the same server. Probes
  now offer only X25519, P-256, P-384 and P-521, as those clients do.
  Workers still offer the hybrid exchange to their collectors.
- A worker with several collectors no longer probes on while it drops
  every sample. Once each collector's share of the outbox was full, as
  when all of them were down long enough, a new sample was kept for none
  but reported as a degraded delivery, which a worker takes to mean
  another collector still has it; it logged the loss and went on. With
  room at no collector a worker now stops with the outbox's capacity
  error, as one with a single collector always did, and keeps what it
  queued.
- An incident ledger that could not be written is written at the next
  judgement, two seconds on, whether or not anything changed by then. A
  failed write was tried again only once something else changed, yet the
  collector logged "incident ledger written again" at the next judgement
  that changed nothing, and a crash before the next change lost the
  incidents that write held. The log now follows a write that succeeded.
- A collector starting on a new data directory makes it durably. The
  directory, those made above it and its new journal were never synced
  into their parents, so on a POSIX filesystem a power loss could leave
  the samples it had acknowledged, each written and synced, with no name
  to find them by. Each is now synced into its parent as it is made,
  before the collector opens, and so is any directory made to write a
  file into, such as a credentials file's.
- A worker starting on a new outbox makes it durably. Its directories
  were never synced into their parents, and each record was synced into
  `pending` but `pending` into nothing, so a power loss could lose the
  samples a worker had queued before any collector took them. The
  outbox's directories are now synced as they are made, before the
  worker opens it.

### Security

- A worker can no longer crowd other observers' alerts out of a webhook
  they share. A webhook's queue held its 64 alerts whoever they were
  from, so while it was down or slow a worker could fill it with
  incidents of its own observer, which it can make at will, and every
  alert from another observer was dropped until it drained. A full queue
  now gives an alert from an observer with fewer waiting the place of the
  newest from the observer with the most, so each keeps its share; a
  drop is logged with the observer it was from.
- A collector's TLS private key is read as its other credentials are.
  `topo serve` handed `--tls-key` to the TLS server to open as it was,
  so a key other users could read, a link or anything but a file was
  served from; now the key must be a regular file, not a link, of mode
  `0600` or stricter on POSIX, or the collector does not start, and it
  says why. Copy a key out of a certificate tool's directory of links,
  such as Let's Encrypt's `live/`, rather than pointing at it.
- The editor no longer writes a terminal control or bidi rune from a
  topology to the terminal. A field's text was shown without them, but
  its cursor, in color, redrew the rune under it as it was, so moving
  through a field of a crafted topology could send its controls one at a
  time. Any such rune now shows as a space, wherever a single rune is
  drawn, and the cursor stands where the text before it is shown rather
  than a column further per control.
- A worker can no longer fill its observer's share of a collector for
  good by changing its boot. Each boot whose receipts expired left a
  floor that was kept forever and counted against the observer's share,
  and a worker picks its boot IDs, so one that sent each sample under a
  new boot made its observer's samples refused with 429 from then on,
  through a token rotation and a restart. An observer's floors now take
  at most an eighth of its share; past that, those whose receipts were
  taken longest ago go. The IDs they bound were received more than
  `--retention` and an hour ago, too long ago for their samples to be
  accepted again, and a boot still delivering keeps its floor.
- A topology's values reach no terminal raw. Errors that refused an
  endpoint, link or path ID, a path's node or dependency, a check's kind
  or a repeated header name wrote the value as it was, so a crafted
  topology could clear, retitle or reorder the terminal of whoever
  validated, served or edited it. Those values are now quoted with their
  controls escaped, and everything `topo` writes to stderr, its errors and
  logs, leaves out terminal controls and bidi runes but for newlines and
  tabs.

## [0.8.0] - 2026-10-14

Everything since 0.3.0. No release was published between them.

### Added

- On the geo map, `p` hides every site plate and draws the arcs dashed,
  leaving the markers and the lines. `p` again brings the plates back.
  The key does nothing on the virtual geo or the network graph.
- Long-term history. Collectors roll every sample they accept up into
  per-check rollups that outlive the raw journal: each sample's outcome
  stands until the next sample or until it goes stale, and the time it
  stood is summed as up, degraded, down or inconclusive (the monitor could
  not tell), with the samples by outcome and a histogram of what the
  passing ones took. Time no current sample stood for is unmeasured,
  never up or down, so availability reads as "95.7% up over 96%
  measured". Rollups are kept five minutes wide for two days, hourly for
  32 days and daily after that, written with each checkpoint and rebuilt
  from the journal, so a crash counts no sample twice. `topo serve
  --rollup-bytes` caps them (32 MiB by default); at the cap a collector
  sheds the rollups of contracts it no longer counts, then the oldest
  days of the observer whose rollups take the most room, and never stops
  ingesting for them.
- Snapshots carry each check's `availability` over the last 24 hours, 7
  days and 30 days. A check's evidence shows them, such as `Last 24h:
  99.2% up over 96% measured · down 11m`, and the Checks tab its last
  day's share up as UP 24H.
- `GET /v1/rollups?check=ID&step=1h&span=7d` lays a check's rollups out
  in slots of five minutes, an hour or a day, and `topo history CHECK
  --step 1h [--span 7d]` prints them, slot by slot with a total, or as
  JSON. The preview keeps synthetic rollups as a collector watching all
  along would, so scenarios show in them too.
- A check's evidence opens with its recent samples, read from the
  collector's history: a strip of one slot per sample colored by outcome,
  with a grey dot for each interval in which none was taken, a bar for
  what each passing sample took, and their median and 95th-percentile
  times with the median DNS, connect, TLS and first-byte times.
  `topo show checks ID` prints the same strip, and its JSON carries it
  slot by slot as `samples`.
- Collectors keep an incident ledger, `incidents.json` in their data
  directory, judged every two seconds from the evidence of all observers
  whether anyone is looking: an incident opens when it is confirmed,
  recovers while its failed checks pass again short of their recovery
  threshold (a failure then is a relapse, not a new incident), and closes
  as recovered, unconfirmed, paused or changed. Each change is logged.
  Snapshots carry the open records and those closed in the last day as
  `ledger`; the Incidents tab and `topo show incidents` list them after
  the open incidents, one row per occurrence, and an incident's evidence
  says when it opened, how often it happened before and how long it
  lasted. A recovering incident still needs attention.
- A collector logs what its store holds when it starts: each observer's
  receipts and bytes against its share, and where its receipts settle at
  the last hour's rate, since a receipt lasts the ingestion window and an
  hour. It warns once when an observer is set to fill its share, and
  when, so its sampling or `--retention` can change before its samples
  are refused.
- Webhook alerts. `topo serve --alerts alerts.json` posts incidents that
  open and close, and observers that fall silent or report again, as
  JSON or in the formats of ntfy, Slack and Discord, filtered by event
  and observer. The file names secrets, so it is private to the
  collector like `auth.json` and reloads with it on `SIGHUP`. Each
  webhook has a bounded queue delivered in order, at least once, with
  retries for failures, 408, 429 and 5xx until an alert has waited a
  day. What waits is kept in the data directory as `alert-queue.json`,
  so a collector that stops or reloads loses none of it, and each
  webhook's sent and dropped alerts are counted there and logged at
  start; logs name webhooks by ID, never by URL.
- Everything the TUI shows can be read from the command line. `topo status`
  prints the Overview (verdict, check and tab counts, what needs attention,
  services and observers) and says it in its exit status: 0 all clear,
  2 something needs attention, 3 nothing current is known, 4 no collector
  answered. `topo show TAB [ID]` prints any tab, or the evidence behind
  one entry as the evidence panel has it, a server with its card;
  `topo show map` lists the map's nodes and edges with what each may
  claim. `topo history CHECK` reads a check's recent samples. Each takes
  `--json`, `--observer` and the connection flags of `topo`.
- Collectors and workers reload on `SIGHUP` (`systemctl reload` with the
  shipped units): a collector rereads its topology and `auth.json`, a
  worker its topology. A file that fails validation, names another
  project or drops the process's own observer is refused and logged, and
  the running topology kept; a reload logs which checks measure something
  new and so start afresh. Windows has no `SIGHUP`; restart there.
- Snapshots say, for each observer, whether its executor measures what the
  collector counts. After a collector reload, an observer whose worker
  still runs the old topology reads `outdated`, needs attention and is
  counted on the Observers tab until that worker reloads too. Workers
  report this in their heartbeats.
- Snapshots carry each endpoint's state from the checks that target it,
  and the Topology tab's endpoints show the same evidence lines as other
  entities: the state's reason and the checks behind it.
- `topo preview [--scenario N] status` (or `show`, `history`) reads the
  synthetic evidence, and `topo preview scenarios` lists the scenarios, so
  scripts and agents can be rehearsed on a simulated outage.
- The preview speaks English, Japanese and Chinese. Names, notes and
  scenarios come from one catalog per language in
  `internal/preview/locales`, through references such as `@node.win`; a
  language is supported when its catalog is there. Each language also has
  its own placed topology, so the home is Seattle, Osaka or Chengdu
  rather than one private network shown in translation. The node IDs,
  the scenarios and the evidence stay the same, and a test holds every
  language to that. `topo preview --lang` picks one, and by default the
  first of `LC_ALL`, `LC_MESSAGES` and `LANG` that is set decides,
  falling back to English. `--print-topology` writes the topology in
  that language, and `topo preview scenarios --json` gives each scenario
  an `id` that is the same in every language.
- The homepage is in English at `/`, Japanese at `/ja/` and Chinese at
  `/zh/`, each linking the others, with a language menu and a hint that
  offers a visitor arriving from elsewhere the page in their browser's
  language. The pages are written by `scripts/build_site.py` from one
  template per page in `site/src` and one catalog per language, and each
  language replays a TUI recording of its own preview. It says what
  Topophany is, and can be found as that: a sitemap naming every
  language's page, `robots.txt`, and schema.org data for the site and
  the software. Headings are lit dot by dot, the docs sit beside the
  install, and the semantics are drawn as the TUI draws them. The page
  is a line or two where it was the manual, and what was there for
  effect is gone.
- The README is translated into Japanese (`README.ja.md`) and Chinese
  (`README.zh.md`); a test keeps their commands, links and sections those
  of the original, and each shows the TUI in its own language.
- `make captures` captures the TUI stills in `docs/assets` from the
  preview, painted by the homepage's terminal renderer: the README's in
  every language, the guide's in English. They now show the current TUI.
- `f` shows the map alone, on the Overview or Topology (from another tab,
  the Topology's), and `f` or `Esc` brings the panels back. The selection
  still moves with the arrow keys and is named with its state along the
  map's foot. With the room, the virtual geo says what runs on each node,
  services with a problem first and in their color, on a line under it
  when its cards have the height, and geo plates name them after each
  node.

### Changed

- The geo map labels a site with a plate, its place over a row for each
  node, set down where it covers the least land and the fewest arcs:
  beside its marker when there is room, otherwise joined to it by a thin
  leader, so a crowded region spreads its labels into the sea around it
  rather than stacking them over each other. A short map shows fewer rows
  and counts the rest, then folds a plate to one line. Labels stay put
  while the selection moves.
- Fitting every site, the geo map folds a stretch of longitude wider than
  60° with no site in it, such as the Pacific between China and
  California, to a narrow seam marked with how many degrees it holds, so
  the coasts on either side get the width instead of an empty ocean. The
  focus and world views are never folded. Its natural height is the
  latitudes its sites span with room for their labels, rather than the
  whole world, and on the Overview it keeps the rest when nothing needs
  attention.
- Labels take the room there is rather than a fixed width: virtual geo
  cards widen to their full names where the gaps can spare it, a network
  chip cut short by its column runs on into free cells to its right, a
  service on the Overview with no cell beside it runs on to the edge, and
  a server card's row keeps what it names whole before what it says on
  the right.
- Server cards name the server once, its state in the pill, and take an
  amber or red header when something on them is affected or down. Their
  sections open with a quiet label and a hairline rather than a bright
  heading, and the frame is the card's edge, with no paper outside it.

- Accepting a batch no longer visits every receipt a collector holds.
  Receipts expire from a queue in the order they were taken, and what
  each observer holds is counted as it changes rather than recounted on
  every write, under the store's lock, twice: with 150,000 receipts held
  a write takes 5.7ms rather than 26ms.
- Deduplication receipts expire an hour after the ingestion window
  (`--retention`), when no sample they could match can be accepted any
  more, and release their capacity. Each worker boot keeps the highest
  expired sequence as a floor, so an expired observation ID stays bound
  and is refused with `409 observation_id_conflict`, across restarts.
- An incident starts as early as the ledger saw it fail, rather than at
  the oldest sample a collector still holds for its checks, and in a
  cached view its evidence reads CACHED rather than OPEN.
- **Wire format (schema 2).** Each observation carries the measurement
  contract of its check, a digest of what the check measures (kind,
  observer, target, timeout, bind and resolve settings, assertions,
  adapter options, the proxies of its path), instead of the revision of
  the whole topology. Editing a note, a name, a place, a threshold, an
  interval or another check no longer resets a check's history, refuses
  in-flight samples or renames its incident; changing what it measures
  still starts it afresh. `topo validate` prints every contract, snapshots
  and history show public contracts, and incidents are ordered by scope.
  Collectors accept the schema 1 batches of 0.3 workers while the
  topology is unchanged; upgrade collectors first, then workers, then
  viewers. 0.8 viewers read only 0.8 collectors. Schema 2 clients ignore
  snapshot fields they do not know, so later additions will not break
  them, and pass over a collector of another release rather than refuse.
- What the TUI shows is derived in one place, `internal/view`, which the
  TUI draws and the CLI prints; a test holds the two to the same rows,
  evidence, verdict and counts across every preview scenario.
- `topo preview` follows the locale, so where none of its languages is
  asked for it now shows the built-in topology in English rather than
  Chinese; `--lang zh` shows it as before.
- The reference lists every setting the TUI reads from the environment:
  `NO_COLOR`, `COLORTERM`, `TOPO_BACKGROUND`, `TOPO_HYPERLINKS`,
  `TOPO_CELL_ASPECT` and the locale variables the preview follows.
- The acceptance checklist names what to confirm on live hardware for
  the changes since 0.3.0, among them an overloaded worker, a check
  flapping past its history, storage near capacity, one collector of a
  pair answering slowly, anonymous load on a public collector, and a
  webhook outage across a restart. They are procedures, not results.

### Fixed

- Anonymous requests can no longer lock workers and viewers out of a
  collector. Every request, authenticated or not, was charged to one
  admission of 16 simultaneous requests and 300 a second before its token
  was read, so anyone who could reach a collector could have its workers'
  samples and its viewers refused with 429. Tokens are now checked first;
  a request that authenticates counts only against its own principal's
  30 a second, on every route, and the shared admission bounds the rest.
- A collector slow to answer no longer delays a worker's delivery to its
  other collector. Each round went to both at once but waited for both,
  so one that took eight seconds to time out, or answered slowly without
  failing and so was never backed off, held every round to the other as
  long. Each collector now has a delivery loop of its own, and a backoff
  counts from when a failed attempt ended rather than when it began. A
  worker with a long backlog picks each round's samples without sorting
  all of it: on 45,000 queued samples a round takes 1.7ms and 170KB
  rather than 24ms and 34MB.
- A confirmed failure stays confirmed until its check recovers, however
  long ago the samples that confirmed it were taken. A check was judged
  from the samples a collector still held, so once those that confirmed
  a failure left the history and the latest 40 samples, a check that had
  flapped ever since, never passing twice in a row, read up, and the
  incident ledger closed its incident as unconfirmed and told webhooks
  so. One healthy backend of two behind round-robin does just that. The
  samples a series lets go now leave what they established, kept for
  every pair of thresholds and written with each checkpoint, so a check
  is judged as though they were all still held, across restarts and
  threshold edits.
- The origin behind an edge is credited only with a passing response the
  edge says it fetched from it (`MISS`, `DYNAMIC`, `BYPASS`, `EXPIRED`,
  `REVALIDATED`, `REFRESHHIT` or `PASS`). Any status but a cache hit used
  to count, as did none, so the origin read up on the strength of
  answers the edge made itself: a Cloudflare Worker that sends no
  subrequest reports no cache status, and CloudFront reports
  `LambdaGeneratedResponse`. Behind a hop that reports no cache status,
  such as a plain reverse proxy, the origin is now unknown until a check
  of its own measures it.
- A worker with more checks due than it has workers runs each in turn,
  the longest overdue first. Every pass gave free workers to the checks
  listed first, so under sustained load, such as an outage with many
  checks waiting out their timeouts, those listed last never ran: with
  eight checks on one worker, five never started. A worker that comes
  free also takes the next check at once rather than at the next tick.
- A `Host` in an HTTP check's `request_headers` is sent. It was accepted
  and then dropped, since Go's client sends the URL's host whatever a
  Host header says, so a check meant for a virtual host measured the
  URL's site. A Host value that is not a host or `host:port`, and a
  header named twice in different cases, are refused.
- A `bind_ip` with an IPv6 zone (`fe80::1%eth0`) binds to it. The zone
  was validated and then lost, leaving the kernel to choose the source,
  so such a check measured from whatever address it picked. A zone that
  names no interface on the observer is now a monitor error,
  `local_bind_interface_unavailable`, and an ICMP check refuses an IPv6
  `bind_ip`, since it sends IPv4 only.
- Collectors no longer stop accepting an observer's samples once it has
  delivered its share of receipts. Receipts were kept until
  administrative archival, so of two observers, one sampling once a
  second in all was refused after about a day and a half, and the
  busiest of the preview topology's five after about eight hours. A collector paused this
  way resumes after upgrading, once its old receipts expire.
- The About globe and the geographic map keep their shape on terminals
  whose character cells are not twice as tall as they are wide; on
  Windows Terminal with a tall line height the globe was an upright oval
  and the continents stood 30% too tall. The TUI reads a cell's shape
  from the pixel size the terminal gives with its rows and columns, or
  asks for it with `CSI 16 t`, and `TOPO_CELL_ASPECT` (a cell's height
  over its width, such as `2.6`) sets it where the terminal says neither.
  Windows Terminal answers the query with a fixed 10×20 cell whatever its
  font, so there it takes the variable.
- The homepage no longer scrolls sideways on phones. On narrow screens the
  panels of its Semantics section were as wide as their widest line, up to
  65 pixels wider than the screen at 320; they now fit, and the edge
  diagram's names wrap.
- A worker no longer backs off after quarantining a sample a collector
  refused, and returns to batching with the first sample accepted; it
  used to slow to one sample per tick, with growing pauses, until its
  queue was empty.
- Endpoints are judged by the same rule as services and nodes. One whose
  checks are degraded reads degraded rather than unknown on the Topology
  tab and its server's card, and one with a confirmed failure while its
  other checks have no current evidence reads unknown rather than degraded.

### Security

- A worker token can no longer make a collector write and fsync its
  journal at will. A heartbeat was written before the store compared it
  with the one it held, so one sent again, or an older one, cost a
  journal write and an fsync under the lock every observer's ingestion
  waits on, 30 times a second per token, while changing nothing. A
  heartbeat that would change nothing is now acknowledged without being
  written.
- A viewer token can no longer make a collector redo work over the whole
  topology with each request. `/v1/config` made the public topology
  twice, and `/v1/history` and `/v1/rollups` the public topology and the
  contract of every check to answer for one, as HEAD requests did too,
  and every worker batch hashed the whole topology. What requests need of
  a topology is now worked out once when it is loaded or reloaded.
- A monitored service or Mihomo controller that is sent a secret can no
  longer hand it to viewers. Labels it answered with were kept in the
  observation every viewer reads: the edge headers an HTTP check reads
  its point of presence, region and cache status from, the exit address,
  and a controller's mode, proxy names and test URL. When a request
  carries a secret (a bearer, a private request header, a Mihomo token,
  or a proxy password for a plain HTTP target), the observation now keeps
  closed words, numbers and comparisons with the topology only: no
  `edge_pop`, `edge_region` or `exit_ip`, a `mode` of `rule`, `global`,
  `direct`, `script` or `other`, proxy names the topology declares and
  `undeclared` for the rest, no `test_url`, and `peer_text:
  withheld_secret_request`. Every check's `edge_cache` is now one of the
  statuses edges report, or absent.
- One worker token can no longer shorten another observer's long-term
  history. Rollups past the cap were shed oldest bucket first across
  every check, so an observer sending all it may could push out the
  oldest rollups of observers that held far less. A collector now sheds
  the oldest buckets of the observer whose rollups take the most room;
  each series records its observer, and keeps it through checkpoints.
- Viewers no longer see the fragment of an endpoint address or check
  target (`https://host/path#secret`), nor the user information or query
  of an address that is not a URL (`user:pass@host:22`). The public
  topology removed user information and queries only from URLs with a
  scheme, and kept every fragment. Adapter test URLs and the topology
  now share one sanitizer.
- An SSH hop's `ssh_identity_file` or `ssh_known_hosts` beginning with
  `~` is refused, as the reference already said, rather than read as a
  directory named `~`. The reference now says that a relative path is
  resolved against the worker's working directory, the security notes
  that `tailscale`, `wg` and `ssh` are found on `PATH` rather than at
  fixed paths, `SECURITY.md` that 0.8.x is the supported line, and
  `--outbox-bytes` that quarantine has a budget of its own.

## [0.3.0] - 2026-10-14

### Added

- About links to the homepage, topophany.com, and to the source on GitHub,
  written as OSC 8 hyperlinks where the terminal takes them
  (`TOPO_HYPERLINKS=0` or `1` decides outright). On short screens the
  blank rows between its paragraphs close up so the links stay.
- Where there is room, About lists the keys of the mode the UI is in
  (viewer, preview or editor) as keycaps, so `?` doubles as help.
- Tabs count what in them needs a look (failing services, paths and
  checks, open incidents, silent observers), red when anything is down and
  amber otherwise; on narrow terminals the tab's number takes the color.
- The terminal window is named after the verdict, such as
  `✖ 2 incidents · homelab · Topophany` (`✎ topology.json ●` in the
  editor), and its own title is put back on exit.

### Changed

- The TUI writes only the cells that changed since the last frame, not
  every line with a change, so animation on the maps sends a few hundred
  bytes a step instead of tens of kilobytes.
- While connecting or loading a view, the header, the status row, the
  Overview's verdict and the empty map show a turning spinner rather than
  a still `◌`, so a slow collector reads as busy, not stuck.
- Menus, forms and confirmations cast a soft shadow onto the dimmed view.
- The Overview's big number is lit from above, a shade of its color for
  each of its five pixel rows, as the About wordmark is.
- On small terminals such as 80x24 the Overview keeps the verdict's big
  number, puts the observers beside it, and when nothing needs attention
  gives the room under a short all-clear to the service matrix.
- The glints on the Overview's map run as smoothly as on the Topology
  tab, and About's globe, once its introduction has played, turns at ten
  frames a second rather than fourteen.
- Times in the evidence read alike: an incident's start, a check's last
  sample and when the collector received it are local time to the second
  (`2026-10-13 10:58:07 PDT`), and an incident's start says how long ago
  it was, rather than mixing in RFC 3339.

### Fixed

- PTY acceptance reconstructs the terminal screen before checking state, so
  incremental redraws correctly verify collector failover. CI fuzz suites use
  an execution budget instead of a short wall-clock deadline.
- Large topology diagrams have bounded layout work and an explicit fallback;
  ordinary dense diagrams retain their routing and ordering quality.
- Notices fade out, and the evidence footer identifies Enter as the back action.
- On narrow terminals the header's project and view no longer run into
  the live indicator: they end in an ellipsis before it. Before the first
  snapshot the header no longer says it was updated "never".

### Security

- Enforce per-source collector connection quotas and absolute connection lifetimes;
  close unauthenticated HTTP/1 connections while preserving authenticated reuse.
- Give viewers a public topology revision that excludes private execution settings,
  including revisions embedded in snapshots and observation history. Workers keep
  the private execution revision contract.

## [0.2.0] - 2026-10-13

### Added

- A virtual geo map, now the default when any node has a location: places
  become cards in columns from west to east and north to south, keeping
  the world's order but not its distances, so crowded sites stay legible.
  Each node has its own row, links are wires between rows (and brackets
  inside a card for links within one place), and unplaced nodes such as
  edges and the internet float as pills beside what they connect to. `m`
  cycles virtual geo, geo and network; cards fold on short maps, and links
  that find no room are counted in the legend.
- A Servers tab (`3`): a card for each server with its state, kind,
  provider and place, the services it runs and the edges in front of them,
  its addresses by network, what it hosts, who observes from it, its links,
  and its notes as a memo. Edge and platform cards list what runs there,
  what they stand in front of and where they answered from. Later tabs move
  up one: Services is `4` and About is `9`.
- Edges and platforms: nodes of kind `edge` (Cloudflare, Vercel) and
  `platform` (Supabase) with a `provider`, services with a `runtime`
  (`workers`, `pages`, `functions`, …), and endpoints that name the edges in
  front of them in `via`. Evidence is attributed along `via`: an edge's
  answer, even a 521, proves the edge; the origin counts only a passing
  response not served from the edge's cache.
- HTTP probes record which edge answered, from where and whether from its
  cache (`edge_provider`, `edge_pop`, `edge_region`, `edge_cache`).
- The preview topology puts Cloudflare in front of the main site, with a
  Worker, a Pages site and a Vercel app on Supabase, and gains two
  scenarios: the origin down behind Cloudflare, and the edge unreachable
  from the mainland.

### Changed

- The About globe reads as a sphere instead of a flat disc: a shaded ocean
  and land lit from the upper left, falling off across a soft terminator and
  toward the limb, with a graticule that curves with the surface. Truecolor
  terminals shade under the dots; 256-colour terminals keep a dotted ocean.
  The README banner is redrawn from the same art.
- The homelab example models its CDN as an edge in front of the DMIT
  origin.

### Fixed

- Avoid a data race when reading outbox delivery retry state.
- A service reached over several failing paths no longer repeats "access
  path unavailable" once per path in its reason.

### Security

- Bound Exit-IP and Mihomo JSON structure before decoding; escape bidirectional controls in CLI JSON without changing parsed values.
- Reserve outbox capacity per destination, history capacity per observer, and snapshot computation per viewer; bound connections and pre-authentication work.
- Retain durable observation-ID bindings until administrative archival and scope persisted heartbeats to their project and configuration revision.
- Bind source verification to the tested commit and file manifest; resolve release tags back to the built commit immediately before publication.
- Add regression tests for all eleven scan-bundle findings, including restart recovery, invalid acknowledgements, and moved release tags.

## [0.1.0] - 2026-10-13

Initial release.

### Changed

- The terminal UI is redesigned to be read at a glance: an Overview dashboard
  (verdict, failure count, map, what needs attention, services, observers);
  aligned tables whose state column lights up only problems; a geo map that
  clusters sites by place, colors each arc by its evidence and keeps healthy
  links quiet; a network graph drawn as a left-to-right schematic; and a
  responsive About with a one-bit dithered globe. The UI paints its own
  background (`TOPO_BACKGROUND=terminal` keeps the terminal's) and writes only
  the colors that change.
- Loading has its own calm state instead of the offline warning, the editor's
  status row lists what the draft declares, and the footer keeps the essential
  keys when the terminal is narrow.
- The README opens with a banner drawn from the About art (`make banner`) and
  shows the interface in screenshots.
- Go module path is now `github.com/WhitenWhiten/topophany`.
- Contribution guidelines moved to the repository root; added security policy,
  code of conduct, issue/PR templates, Dependabot and release automation.
- Generated verification records are no longer tracked in `docs/verification/`;
  `scripts/verify.py` writes to `.verify-build/verification/` and CI uploads them
  as an artifact. `scripts/package.py` takes `--verification` to locate them.

### Fixed

- Flaky DNS TCP-fallback test fixture when the paired TCP port was already in use.

### Security

- Isolated API resource budgets, bounded JSON and snapshot decoding, and hardened
  configuration, probe, storage and worker outbox boundaries.
- Release archives are built from an immutable checkout and verified before
  publication; executable GitHub Actions are pinned to commit SHAs.

### Added

- Single `topo` binary: remote TUI client, ingestion collector (`serve`) and
  scoped observation worker (`worker`).
- Native HTTP/TLS, DNS, TCP, UDP and ICMP probes; CONNECT/SOCKS5 proxy chains;
  adapters for local proxy and tunnel tooling.
- Single-writer append journal with checkpoints, crash recovery and
  deduplication; durable worker outbox.
- Evidence- and freshness-based state engine with dependency derivation.
- Terminal UI with geographic and network topology maps, offline preview
  scenarios and a local topology editor.

[Unreleased]: https://github.com/WhitenWhiten/topophany/compare/v1.5.0...HEAD
[1.5.0]: https://github.com/WhitenWhiten/topophany/compare/v1.4.2...v1.5.0
[1.4.2]: https://github.com/WhitenWhiten/topophany/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/WhitenWhiten/topophany/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/WhitenWhiten/topophany/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/WhitenWhiten/topophany/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/WhitenWhiten/topophany/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/WhitenWhiten/topophany/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/WhitenWhiten/topophany/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/WhitenWhiten/topophany/compare/v0.9.1...v1.0.0
[0.9.1]: https://github.com/WhitenWhiten/topophany/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/WhitenWhiten/topophany/compare/v0.8.3...v0.9.0
[0.8.3]: https://github.com/WhitenWhiten/topophany/compare/v0.8.1...v0.8.3
[0.8.1]: https://github.com/WhitenWhiten/topophany/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/WhitenWhiten/topophany/compare/v0.3.0...v0.8.0
[0.3.0]: https://github.com/WhitenWhiten/topophany/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/WhitenWhiten/topophany/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/WhitenWhiten/topophany/releases/tag/v0.1.0
