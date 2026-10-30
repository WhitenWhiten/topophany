# Configuration and API Reference (Schema 1)

The complete schema is defined in Go in `internal/model/model.go`. Input configuration is strictly parsed JSON; unknown fields, multiple top-level values, and files exceeding 4 MiB are rejected. Identifiers must use stable ASCII strings; human-readable names and notes may contain UTF-8 text. Never use raw IP addresses as entity IDs.

## Entities

| Collection | Key Fields | Constraints and Semantics |
|---|---|---|
| `nodes` | `id`, `name`, `kind`, `parent`, `group`, `provider`, `notes`, `location` | Host machines and virtual environments can share IP addresses while maintaining distinct identities; `parent` hierarchy must be acyclic; `kind` is free-form, and `edge` (an anycast network in front of origins, such as Cloudflare or Vercel) and `platform` (managed hosting, such as Supabase) are drawn as places you deploy to rather than machines you run; `provider` (≤64 bytes) names who runs it; `location` is an optional object: `{"label": "Tokyo", "lat": 35.68, "lon": 139.69}` used solely for map layout (lat −90..90, lon −180..180, label ≤64 bytes); inherits parent location when omitted; location is never guessed from IP addresses |
| `endpoints` | `id`, `node`, `address`, `network`, `observer`, `via` | `network` values: `public`, `lan`, `tailnet`, `loopback`, `external`; loopback endpoints must specify a bound `observer`; `via` lists up to 8 distinct edge or proxy nodes a client reaches before `node`, client side first (see [Edges and Platforms](#edges-and-platforms)) |
| `services` | `id`, `name`, `node`, `runtime`, `endpoints`, `depends_on`, `runbook`, `notes` | Deployment host may differ from access endpoint (for example, a forwarded port on a cloud relay accessing a service inside a local network); `runtime` (≤32 bytes) says how it runs there, such as `workers`, `pages`, `functions` or `container`; `runbook` is informational only |
| `observers` | `id`, `name`, `node`, `context`, `route_expectation`, `stale_seconds` | Represents an explicit OS and network execution namespace; `route_expectation` documents intended routing rather than cryptographically verified routing proof |
| `links` | `id`, `from`, `to`, `transport`, `notes` | `from`/`to` are node IDs indicating connection initiation direction, not packet drop direction |
| `paths` | `id`, `name`, `observer`, `nodes`, `links`, `proxies`, `candidates`, `depends_on`, `opaque` | Sequential links must connect; `proxies` defines outer connection chain order; `candidates` specifies alternative failover paths rather than intermediate hops |
| `checks` | `id`, `observer`, `endpoint`, `service`, `path`, `link`, `kind`, `target` | Executed from the specified observer; defaults to `endpoint.address` when `target` is empty; each check has a measurement contract (see below) |

Hard service dependencies, parent-child hierarchies, and path dependencies must be strictly acyclic. Distinct vantage points must not share a single path definition; maintain separate path objects for `local->transit-gw-01` and `cloud-relay->transit-gw-01`. Do not directly probe an abstract `candidates` routing group; probe each candidate path individually or configure a dedicated path pointing to the active production listener.

The destination endpoint in a check template must correspond to the actual service being evaluated. Do not associate an external public egress check target with a transit gateway host endpoint and treat responses from that target as the gateway's own availability. Overriding `target` is an explicit administrative configuration; the software does not automatically discover or attest to physical asset identity.

## Edges and Platforms

A site behind Cloudflare, or an app on Vercel or Supabase, is modeled with the same entities. The edge is a node of kind `edge` (no `location`: it answers from many places); a managed platform is a node of kind `platform` that may carry the location of its region. A service deployed on one names it as `node`, with `runtime` saying how it runs; an address served through one lists it in `via`:

```json
{"id": "cf", "name": "Cloudflare", "kind": "edge", "provider": "cloudflare"}
{"id": "blog-web", "node": "origin", "address": "https://www.example.invalid/", "network": "public", "via": ["cf"]}
{"id": "worker", "name": "Short links", "node": "cf", "runtime": "workers", "endpoints": ["worker-url"]}
```

The edge answers first, so evidence is attributed along `via`. Any response (an error page included) proves that the first hop answered, and nothing behind it: the nodes behind it, the origin among them, count only checks that reach them without going through it. What the edge says of where a response came from, its `edge_cache` status among them, is recorded as reported labels and proves nothing, since the edge, or anything behind it, can send it whatever happened; a node reached only through the edge reads unknown and says so, and an answer through the edge is no evidence against a failure of the origin's own check. Give the origin a check of its own, not through the edge, to measure it. A Cloudflare `521` therefore leaves the edge up and the origin unproven; an incident on that address says the request did not satisfy its assertions and the failing layer is not located, because the status is a word the edge or the origin can send whatever happened. Where the status class supports a candidate the incident names it: a response carrying `cf-mitigated: challenge` is the edge's own statement that it served a challenge page and the origin was not asked for that request; a `302`, `303` or `307` redirect to a `Location` host ending in `.cloudflareaccess.com` is recorded as Cloudflare Access asking for a login, and the origin was not asked for that request (Cloudflare documents the `302` form; the adapter also accepts `303` and `307`); `403` and `429` without either may be edge policy or a challenge page rather than the origin's answer; `520` reports an empty, unknown or malformed response from the origin; `521`/`522`/`523` are the platform's own account of an origin it could not reach; `524` reports a read or write timeout after a successful origin connection; `525`/`526` report TLS to the origin failing (a handshake, or a certificate the edge does not accept); `530` reports an origin name it could not resolve or reach, a Tunnel with no live connector included; and any other `5xx` may come from either side. The origin is never declared at fault from the response alone. A confirmed failure without any response leaves every hop on the chain affected.

HTTP probes record which edge answered from the headers edges add to their responses: `edge_provider` (`cloudflare`, `vercel`, `cloudfront`, `fastly`, `netlify`), `edge_pop` (the point of presence, e.g. `NRT` from `CF-Ray`), `vercel_edge_region` (the receiving Vercel edge region), `vercel_route_regions` (up to eight reported Vercel routing regions, colon separated; no inferred function role), `edge_mitigation` (`challenge` when Cloudflare marks a challenge page; other Cloudflare products' values, such as `guardrails`, are not kept), `edge_access` (`login_redirect` when a `302`, `303` or `307` redirect's `Location` host ends in `.cloudflareaccess.com`; the URL's key ID and redirect target are not kept, and an evidence line names it only when the response names the edge's provider), `edge_cache` (one of the statuses edges report, such as `HIT`, `MISS` or `DYNAMIC`; any other is not kept), and, when Cloudflare adds its `cfL4` `Server-Timing` entry, `edge_rtt_ms` and `edge_min_rtt_ms` (the colo's own round-trip and minimum round-trip readings for the connection (TCP or QUIC) that carried the response, read as microseconds from the live capture and recorded as whole milliseconds rounded to the nearest), `edge_retrans` and `edge_lost` (decimal counts), and `edge_proto` (`TCP` or `QUIC`; any other word is dropped). The entry's connection identifier, byte counters, window, delivery rate and timestamp are not kept; no official field-layout or unit specification for the entry was found, and spot checks of several Cloudflare hosts sent no `cfL4`. A trace check whose `exit_format` is `trace` adds the edge's account of the request from its `/cdn-cgi/trace` body: `trace_colo` (the colo it reached, exactly three uppercase letters), `trace_warp` (`off`, `on` or `plus`), `trace_gateway` (`on` or `off`), `trace_http` (`http1.1`, `http2` or `http3`, mapped from the body's slash form), `trace_tls` (`TLSv1.2` or `TLSv1.3`) and `trace_loc` (the client country the edge saw, two uppercase letters). The four words `trace_warp`, `trace_gateway`, `trace_http` and `trace_tls` are kept only in their closed vocabularies; `trace_colo` and `trace_loc` are free tokens checked only for their letter shape and length, and a value outside either form is dropped. `exit_ip` is read from the `ip=` line, with or without `expected_exit`; a body over 4 KiB is not read as a trace, so neither the trace facts nor that unconditional `exit_ip` are recorded, while an `expected_exit` assertion keeps its own exit parser. The `uag`, `h`, `sni`, `ts`, `fl`, `visit_scheme`, `sliver`, `kex` and `rbi` lines are not kept, and a body without both `fl` and `colo` is not a trace. These are what the response says, not verified routing: an origin can send the same headers. Values are reduced to short label characters.

A peer that is sent a secret could hand it back in what it answers, and observations reach every viewer of a collector, who is never given the secret. So when a request carries one (`bearer_env`, any `request_headers` entry, any `request_header_envs` entry, or a query in its URL, which the public topology hides, or a proxy `password_env` for a plain `http://` target, as such a hop can write the answer), its observation keeps what the peer said only as closed words, numbers and comparisons with the topology: an HTTP check keeps `edge_provider`, `edge_cache`, the closed `edge_mitigation` and `edge_access` words, Cloudflare's numeric `edge_rtt_ms`, `edge_min_rtt_ms`, `edge_retrans` and `edge_lost`, the closed `edge_proto` word, and the trace check's closed `trace_warp`, `trace_gateway`, `trace_http` and `trace_tls` words, but not `edge_pop`, legacy `edge_region`, `vercel_edge_region` or `vercel_route_regions`; on such a request every trace check withholds `trace_colo`, `trace_loc` and `exit_ip`, and a check that asserts `expected_exit` keeps no `exit_ip`; a `mihomo` check sent a `token_env` keeps its `mode` as `rule`, `global`, `direct`, `script` or `other`, names in `reported_chain`, `reported_selection` and `provider.*` only the proxies its `root_proxy` and `expected_providers` declare (any other reads `undeclared`), and leaves out `test_url`. Such observations carry `peer_text: withheld_secret_request`. The values of `request_header_envs` are variable names, not secrets: each is 1..128 bytes with no whitespace, `=`, comma, control or bidi characters, the same rule `bearer_env` now follows. Use an `https://` exit endpoint to keep `exit_ip` through an authenticated proxy. A `cloudflare` API check sends a token on every read, so its observations keep only closed words, numbers, shaped tokens (three uppercase letters for a colo, at most 24 token characters for a connector version) and comparisons with the declared topology; `origin_ip`, connection and client identifiers, the DNS record's content, undeclared origin names and every `errors[].message` are never kept, and `peer_text: withheld_secret_request` marks the sample.

## Probe Parameters

Defaults: `interval_seconds=60`, `timeout_seconds=8`, `stale_seconds=3*interval`, `failure_threshold=3`, `recovery_threshold=2`. The first detected failure transitions state to `degraded`; reaching `failure_threshold` consecutive failures transitions state to `down`. An established outage requires `recovery_threshold` consecutive successes to return to healthy. Stale samples, monitor errors, and unknown states reset consecutive confirmation counters; observation gaps do not accumulate across lapses.

A `policy` outcome is a state assertion, not a failed request, so it never reads as `down`. An ordinary policy violation opens an incident on its first current sample, preserving the 1.1.1 default. Set `policy_threshold` to 1..20 to explicitly require consecutive violating samples; omitted or 0 means immediate confirmation. Any other outcome or a stale gap breaks the run. This setting is independent of `failure_threshold` and does not change measurement contracts or discard history. Reloading it re-evaluates retained evidence. A pending Mihomo provider load separately adds its configured settle window.

Older collectors/viewers reject the new `policy_threshold` field. Upgrade collectors and all viewers to 1.2.0 or newer before enabling it in a topology; default configurations omit the field and remain readable by older clients.

`disabled` pauses execution and `maintenance` flags planned work; neither state executes active probes or emits actionable incident alerts. Supported ranges: `interval_seconds` in 1..86400, `timeout_seconds` in 1..300. (The 1-second interval used in test suites is not recommended for production). Probes run with random jitter, non-overlapping executions per target, and a default global worker concurrency limit of 4. When more checks are due than workers are free, the one overdue longest goes first, so an observer with too much to do runs every check late rather than some never.

`bind_ip` specifies local source IP binding. An IPv6 address may carry a zone (`fe80::1%eth0`), which must name an interface the observer has; a check bound to an address or interface it cannot use reports a monitor error and never measures from another. `resolve_ip` pins dial destination IP while preserving the original HTTP `Host` header and TLS `ServerName`. These options do not guarantee bypassing host-level virtual network adapters. DNS probe target resolvers may also be pinned, while remote domain resolution over proxies depends on SOCKS/CONNECT target hostnames. ICMP requires explicit IPv4 literals, binds only an IPv4 `bind_ip` and rejects `resolve_ip`. Tailscale and WireGuard adapters read local status and reject dial/bind/target overrides.

| Kind | Target / Options | Operational Meaning of Success |
|---|---|---|
| `http` | HTTP(S) URL; `http` object | Target request satisfies configured assertions; does not prove application session state, streaming protocols, or unrelated API endpoints |
| `tcp` | `host:port` | Observer established a TCP connection; does not prove application protocol health or end-to-end proxy path function |
| `tls` | `host:port`; `server_name`, `ca_file` | Complete TLS handshake with verified identity and trust chain; does not attest to custom proxy transport authentication |
| `ssh-banner` | `host:port` | Received a valid `SSH-2.0` protocol banner; does not prove host key authentication |
| `dns` | Resolver `host:port` for `dns.transport` empty, `udp`, `tcp` or `dot`, or an `https` URL for `doh`; `dns.name`, `type=A/AAAA`, `expected_ips`; `server_name`, `ca_file` for DoT/DoH | Resolver returned matching resource records over the declared transport within constraints; DoT and DoH change the transport, not the answer; no DNSSEC validation guarantee |
| `udp-echo` | `host:port` | Target reflected the randomly generated per-probe nonce; requires an active UDP echo service |
| `icmp` | IPv4 literal | Matching ICMP echo reply received with expected identifier and sequence; requires raw socket privileges |
| `mihomo` | Controller API base URL; `adapter` object | Successfully queried runtime metadata; indicates reported state rather than end-to-end traffic success |
| `tailscale` | `adapter.peer` matching peer DNS, IP, or ID | Local Tailscale client reported peer status; idle or relay indicators do not directly prove application service status |
| `wireguard` | `adapter.interface`, `adapter.peer` (public key) | Local WireGuard status queried; older handshakes do not imply broken connectivity; private keys and PSKs are stripped |
| `cloudflared` | Connector metrics listener at a literal loopback `http://127.0.0.1:PORT` or `http://[::1]:PORT`; `adapter` object | Read the connector's own `/ready` and `/metrics`; reports an edge session and its counters, not the reachability of the service behind it |
| `cloudflare` | Cloudflare API base URL, default `https://api.cloudflare.com/client/v4`; `adapter.token_env` and one `adapter.cloudflare` read | Read one of Cloudflare's own records (tunnel, DNS record, pool health) with one GET; reports the platform's state, never a request the edge answered or the origin's reachability |
| `host` | Optional `host` object; Linux, macOS and Windows | Local CPU, memory, filesystem and process counters collected; configured resource assertions pass |

### DNS Probe Transports

`dns.transport` selects how the question travels: empty (the historical UDP probe) or one of `udp`, `tcp`, `dot` and `doh`. The transport is part of the measurement contract, but a DoT or DoH check asks the same question and applies the same parser, transaction-ID check and `expected_ips` assertion as UDP: the transport changes, not the meaning of a pass. A pass says the configured resolver returned addresses satisfying the assertions over that transport; DNSSEC is still not validated.

| `dns.transport` | Target | Exchange |
|---|---|---|
| empty or `udp` | resolver `host:port` | One UDP question. A truncated answer (TC) is retried once over TCP; `dns_transport` is `udp`, or `tcp_fallback` after the retry. |
| `tcp` | resolver `host:port` | One length-prefixed question over TCP, without trying UDP first; `dns_transport: tcp`. |
| `dot` | resolver `host:port`; 853 by convention, but any valid port is accepted | TCP, then a verified TLS handshake, then one length-prefixed question. The certificate name is `server_name`, or the target host when it is empty; `ca_file` adds a private CA. A certificate is normally issued for a name, so an IP target needs `server_name` — for 1.1.1.1 use `one.one.one.one` or `cloudflare-dns.com`. `dns_transport: dot`. |
| `doh` | an `https` URL with a host and optional path, without userinfo, query or fragment | One `POST` whose body is the DNS wire query; `Content-Type` and `Accept` are both `application/dns-message`. The answer must be a `200` `application/dns-message` body of at most 64 KiB, parsed by the same DNS parser. The URL's host is the TLS name unless `server_name` overrides it; `resolve_ip` pins the dial address and `bind_ip` the source. `dns_transport: doh`. |

The DoH `POST` is the only non-GET request the probe makes. RFC 8484 defines it as the same question as the GET form, carried in the request body: it has no query string, no credential and no side effect beyond asking the resolver to answer, so it stays inside the read-only boundary. HTTP checks still accept only `GET` and `HEAD`.

DoT and DoH record `tls_ms`, the verified TLS handshake, and `first_byte_ms`. For `dot`, `first_byte_ms` runs from just before the TCP connect through the handshake to the first byte of the length-prefixed answer, so it includes the handshake; for `doh` it runs from sending the request to the first response byte, which likewise includes the connect and handshake. `tls_ms` isolates the handshake in both cases. Neither timing includes resolver name lookup on the worker.

An answer the monitor cannot attribute or read within its budget is a monitor error, never evidence that the resolver is down: a message whose transaction ID is not the question's (`dns_answer` / `unexpected_transaction_id`) and a DoH body over 64 KiB (`dns_transport` / `response_too_large`). A DoH endpoint that answers with a non-200 status (`http_<status>`), a different content type (`content_type`), a truncated or malformed DNS message, a negative response or no address remains a failed answer (`fail`) for the configured question, not proof of a broader outage.

A `dns` check cannot use a proxy chain: validation refuses a path with proxies for this kind, so the probe dials directly and DoH does not traverse the path's hops. `dns.transport` is inside `dns`, so it is part of the measurement contract; leaving it unset writes nothing and leaves every contract hash unchanged. Older collectors and viewers reject the field, so upgrade collectors and all viewers to 1.4.0 or newer before enabling a transport in a topology.

### Host Resources

`kind: "host"` reads the worker's OS view. It needs no endpoint or network target. It also works with `topo once` and `serve --observer`. Linux reads bounded `/proc` files; macOS reads libSystem counters in process, without running commands. Filesystem capacity comes from `statfs`. No third-party dependencies or root privileges are required on a normally configured host. An unsupported OS, denied access, malformed counters or exhausted read budget yields a monitor `error`, never a target failure.

| `host` option | Meaning |
|---|---|
| `disk_path` | Clean absolute path on a **local** filesystem; defaults to `/`. One filesystem per check; add checks for other mounts. It is resolved in the worker's mount namespace, including systemd sandbox mounts. |
| `max_cpu_percent` | Maximum aggregate CPU use, 0..100; 0 disables the assertion. Linux samples a 200 ms interval, excluding idle and I/O wait and avoiding double-counted guest time; macOS samples `HOST_CPU_LOAD_INFO` tick deltas over 200 ms. Both report `cpu_sample_ms: 200`. |
| `max_memory_percent` | Maximum physical memory use, 0..100; 0 disables the assertion. Linux uses `MemTotal - MemAvailable`. macOS estimates available memory as free + inactive + speculative pages; this is not Activity Monitor memory pressure. |
| `max_disk_percent` | Maximum filesystem capacity use, 0..100; 0 disables the assertion. Used bytes = total − free; percent = used / (used + available to unprivileged callers), as in `df`, accounting for reserved blocks. This measures capacity, not disk I/O throughput. |
| `process_name` | Optional exact process name, at most 15 bytes. Linux matches `/proc/PID/stat`'s `comm`; macOS matches the kernel process name (`p_comm`) from `kern.proc.all`. No substring, argument or shell matching. Processes can change their names. |
| `min_processes`, `max_processes` | Bounds on matching non-zombie processes; each defaults to 0 (disabled). Require `process_name`; set `min_processes: 1` to detect a missing process. |

Host checks require `timeout_seconds >= 3` (default 8), and reject `endpoint`, `target`, `path`, `link` and dial/TLS options. A `service`, if supplied, must belong to the observer's node. A failed resource assertion emits `policy`, which the engine displays as degraded and records as a policy incident immediately unless `policy_threshold` is explicitly configured; equality with a maximum passes. Collection without enabled assertions succeeds when all counters are readable. Changing resource options changes the measurement contract.

Checks on the same worker share CPU, memory and process aggregates for up to 500 ms after collection finishes, preserving the original `sampled_at`. Only configured process names are retained as aggregates. Each check still queries its own filesystem and evaluates its own assertions. Reloading the topology starts a new runtime cache. Linux reads only the aggregate CPU line and tolerates processes exiting during both open and read. macOS reads memory capacity with `hw.memsize`, available pages with `HOST_VM_INFO64`, and visible process counts and names with `kern.proc.all`; it requests RSS with `PROC_PIDTASKINFO` only for configured names. The process table stays within an 8 MiB read budget. If macOS returns unchanged cached CPU ticks, collection retries a fresh 200 ms window after a one-second cooldown, at most three attempts within the check deadline; it never turns a missing delta into zero utilization.

Filesystem queries respect the check's deadline without delaying shutdown or reload. A blocked `statfs` syscall itself cannot be interrupted: at most four filesystem calls may remain outstanding across the entire process, including reloads. Requests for the same blocked path share its call; new paths receive monitor error `host_disk_busy` if all slots are occupied. Slots recover when the underlying calls return. A clean absolute path alone does not guarantee a responsive local filesystem.

The observation's `facts` contain `cpu_used_percent`, `cpu_logical_count` (CPUs available to the worker process), `cpu_sample_ms`; `memory_total_bytes`, `memory_available_bytes`, `memory_used_bytes`, `memory_used_percent`, `memory_basis`; `disk_path`, `disk_total_bytes`, `disk_used_bytes`, `disk_available_bytes`, `disk_used_percent`, `disk_inodes_total`, `disk_inodes_free`; `process_count`, `process_zombie_count`, and, when selected, `process_name`, `process_matching_count`, `process_matching_rss_bytes`. `violations` lists any failed resource assertions. Matching RSS sums resident memory and may count shared pages more than once.

Evidence is `reported`, with `path_evidence: local_host_runtime_reported`; collecting resources never proves a remote response or path connectivity. Process counts describe a non-atomic snapshot of **visible** processes: namespaces and OS permissions may restrict visibility. CPU and memory describe the OS view, not cgroup quotas; deploy the worker on the host for host measurements. No command arguments, environments, user names or complete process list are retained or uploaded.

See [`examples/host-resources.json`](../examples/host-resources.json). Inspect current metrics with `topo show checks host-resources`, or raw samples with `topo history host-resources --json`. Existing long-term rollups summarize check outcomes and probe duration; they do not aggregate CPU/RAM/disk values into metric time series. The TUI's charts read up to 120 raw samples of each host check's history once, then add each newer sample a snapshot carries; it keeps at most 240 per check in memory, only under the check's current contract, and stores nothing.

`topo preview` includes resource checks for all ten host machines and the router in the full built-in topology, with a local observer for each. The Servers tab (`3`) gives each card a panel for CPU, memory and disk: the reading now and the capacity, over a two-row chart of the last samples that fills as far as the resource is used, colored by headroom against the check's limit; then the named process and the visible process count. Selecting a resource row opens its check. A host check's evidence (`6`, then `Enter`) and its node's details chart each resource on the samples' time axis, four rows on a fixed 0–100% scale with the configured limit dotted across, in place of the probe's elapsed time; the raw facts follow. Edges, managed platforms and external parties have no local host counters. The two containers remain on their parent host cards; container quota metrics are not simulated. The preview remains synthetic on every OS; native host collection supports Linux, macOS and Windows.

Press `x` for CPU saturation on the application host, memory pressure on the relay, disk capacity exhaustion on exit A, or a missing process on the transit host. Other hosts retain independent healthy resource samples. A node's downtime stops its worker samples and heartbeat; maintenance excludes its resource checks. Returning to normal restores samples. Stale, cached or unreadable resource values are withheld on server cards and in evidence: the heading says when the last reading was, and charts keep earlier samples faint. Resource telemetry does not change the node's remote-response health.

Custom topologies such as `topo preview --config examples/host-resources.json` also simulate host checks. Outcomes use the same counter accounting and configured assertions as real host probes, and appear in Overview, Incidents and preview history. No machine resources or configured filesystem paths are read. Observations carry `simulation: host_resources`, stable per-host simulated OS/CPU/capacity metadata and `memory_basis: synthetic_available`. `--lang zh` or `--lang ja` localizes scenario names and descriptions.

### External source bindings

External probes are disabled by default because no production adapter is
implemented yet. The configuration and storage formats are versioned now; see
[EXTERNAL-ADAPTERS.md](EXTERNAL-ADAPTERS.md) for the authoring and upgrade
guide. Synthetic adapters used by tests are never production adapters.

A topology may declare bounded `sources` and external checks. A source owns one
observer, target, transport, credential reference, schedule and collection
budgets. A binding names one stable upstream object and the topology subject it
is evidence about; a response cannot create a node, service, check or binding.

| `sources` field | Meaning |
|---|---|
| `id`, `name`, `notes` | Stable source identity and bounded presentation text. |
| `adapter` | Lowercase static adapter type; must be registered explicitly in the build. No dynamic loading. |
| `observer` | Sole executor allowed to collect and submit this source. Every bound check must use the same observer. |
| `target` | Adapter address. Credentials, query and fragment are redacted from public topology. HTTPS is required when credentials or a query are present; literal loopback HTTP is the local-test exception. |
| `credential_env` | Environment variable name read at collection time, never the value. |
| `path`, `bind_ip`, `resolve_ip`, `server_name`, `ca_file` | Optional proxy route and explicit dial/TLS policy. Ambient proxies, redirects and compression are not used. |
| `interval_seconds` | Source cadence; defaults to 60. One read is fanned out to every binding. |
| `timeout_seconds` | Whole-source collection deadline; defaults to 8. |
| `stale_seconds` | Successful-contact freshness window; defaults to three intervals. A failed read never extends it. |
| `max_response_bytes` | Per-page encoded response budget, 1024..8388608; defaults to 1 MiB. |
| `max_pages` | 1..20 pages per read; defaults to 1. |
| `max_objects` | 1..10000 parser objects; defaults to 10000. |
| `max_report_bytes` | 1024..65536 encoded bytes per normalized report; defaults to 64 KiB. |

An `external` check carries no HTTP, DNS, host, adapter, target, path or dial
options. Its schedule and timeout are inherited from the source; an explicit
different value is refused, as are a missing source, a different observer and
multiple resource providers for one node without an explicit order.

| `external` field | Meaning |
|---|---|
| `source` | Source ID that collects the report. |
| `object` | Stable upstream object ID (1..256 safe characters). |
| `report` | `resource` or `status`. |
| `node` | Topology node this evidence is about. Required. |
| `max_age_seconds` | 1..604800; expiry of the upstream data. |
| `primary_disk` | Stable upstream disk ID for resource selection; optional, but no other disk is guessed if absent. Required by `disk_percent_max`. |
| `assertions` | Optional `cpu_percent_max`, `memory_percent_max`, `disk_percent_max` in 0..100. A missing asserted field is unknown, not a pass. |

A status binding has no disk or assertions and may name a `service` on its
node. It remains `reported` evidence: it is exposed in `snapshot.external` and
the external check state, but does not enter measured service, node, path, link
or incident aggregation in this phase. A successful source read is not proof
that the target is online, and a source auth/transport/parse failure is never
turned into target downtime.

Resource reports use bytes, milliseconds and 0..100 percent. Every numeric
field may be missing; missing is not zero. CPU reports percent, logical cores,
sample window and basis; memory reports total/used/available bytes, percent and
basis; disks carry a stable ID, optional display path, total/used/free bytes,
percent and basis. `primary_disk` selects one disk; there is no cross-source
stitching, averaging or automatic root-disk fallback.

Report identity prefers `sample_id`, then `epoch` + `sequence`, then a known
`observed_at`. `time_basis` is `upstream_sample`, `live_read` or `unknown`.
`unknown` may be retained diagnostically but cannot refresh a deadline, count a
status sample, confirm a failure/recovery or enter latency statistics. Duplicate
identity and content is ignored; same identity and different content is a
conflict; older identity is late; a different sample at the same upstream time
is a conflict unless a sequence distinguishes it. Late reports are not
backfilled into history or rollups. A new identity with unchanged values is a
new sample. `data_deadline` is observed time + `max_age`; `contact_deadline` is
the last successful read + source `stale_seconds`; `valid_until` is the minimum
of those and the snapshot deadline.

Resource selection order is explicit `node.resource_checks`, primary first.
A fallback is used only when the primary report is absent or expired, not when
one field is missing. Root-disk sorting never overrides it. Native-only
topologies keep their previous all-host-check presentation and facts. Monitor
`resource.*` metrics select by node through this same order; `host.*` remains
scoped to a native host check.


### HTTP Probe Specifications

`http.method` supports `GET` and `HEAD` (defaults to `GET`). `status` accepts an array of expected HTTP status codes; defaults to 2xx when omitted. `contains`, `body_regex`, and `headers` define response body and header assertions. `request_headers` defines static non-sensitive headers, each named once whatever its case; `Host` (a host or `host:port`) sets the Host the request carries, for a virtual host the URL does not name, while TLS still names the URL's host unless `server_name` says otherwise; `Authorization` and `Cookie` headers are prohibited; bearer tokens must be referenced via `bearer_env`. A query in an `http` check's URL is taken for a credential, as a service's token often travels in one: it is sent only over HTTPS or to a direct literal loopback address, like `bearer_env`, and what the peer answers is kept as for a secret (above). Use `bearer_env` where the service takes one.

`request_header_envs` is an `http` option that sends request headers whose values are read from the worker's environment: a map of header name to environment variable name, for example `{"CF-Access-Client-Id": "CF_ACCESS_ID", "CF-Access-Client-Secret": "CF_ACCESS_SECRET"}` for a Cloudflare Access service token; `topo edit` takes the same map as comma-separated `Name=ENV_VAR` entries. Header names follow `request_headers`: each is an HTTP token, `Authorization` and `Cookie` are refused in favor of `bearer_env`, and a name may not appear in both maps, whatever its case. A variable name is 1..128 bytes with no whitespace, `=`, comma, control or bidi character, the same rule `bearer_env` now follows; the variable's value is never written in the topology. Upgrade note: this rule also applies to `bearer_env`, so a spelling older releases accepted that contains one of these characters is now refused. A nonempty map counts as a credential, so a plain `http` target is refused except direct literal loopback, and a missing or empty variable is a monitor error before the request is sent. `Host` is allowed and sets the request Host after the resolved value is checked as an outbound host: a reg-name, an IPv4 address or a bracketed IPv6 literal, with an optional 1..65535 port; raw non-ASCII/IDN, quotes, angle brackets, unbalanced brackets, unbracketed IPv6 and empty or out-of-range ports are refused. TLS still names the URL's host unless `server_name` says otherwise. Only `http` checks accept the map; another kind that takes HTTP options refuses it. The map is inside `http`, so its header and variable names are part of the measurement contract, never their values: setting or changing it changes that check's contract and history, while an unset field leaves every contract unchanged. Older collectors/viewers reject the new field. Upgrade collectors and all viewers to 1.4.0 or newer before enabling it in a topology; default configurations omit the field and remain readable by older clients.

`expected_exit` accepts an array of expected IP addresses or CIDR blocks. `exit_format` supports `plain`, `trace`, and `json` (with `exit_json_key`, default `ip`). When the observed egress IP violates constraints, `outcome` is set to `policy` while connectivity facts are preserved. Timestamps and configuration revisions are bound to measurements, preventing metric mixing across target changes.

`expected_pops` declares the colos a response from an edge may name, as at most 16 distinct codes of exactly three ASCII uppercase letters. It is evaluated after the ordinary assertions and `expected_exit` have passed, and before `expected_trace`: when the response named a colo (`edge_pop`) and the edge is not Vercel, a colo outside the list is `policy` with stage `edge_assert` and code `pop_mismatch`, while connectivity facts are preserved because the request did succeed. A response with no `edge_pop` is not a violation: the target may be direct, or a secret request may have had its free labels withheld. Vercel's reported edge region is not a physical colo and never satisfies or violates the list. `expected_pops` is inside `http`, so it is part of the measurement contract: setting or changing it changes that check's contract and history, while an unset field leaves every contract unchanged. Older collectors and viewers reject the new field; upgrade them to 1.4.0 or newer before enabling it in a topology, and default configurations omit it and remain readable by older clients. The preview's `edge-rerouted` scenario exercises the policy — the relay observer's answers arrive from `LAX` while its check declares the colos its language's preview names (`SJC` and `SEA`, `KIX` and `NRT`, or `HKG` and `NRT`) — and [`examples/cloudflare.json`](../examples/cloudflare.json) declares `HKG`, `NRT` and `SIN` on its proxied origin.

`expected_trace` declares what a `/cdn-cgi/trace` body must report and requires `exit_format: "trace"`, because only that format parses a trace body. It must declare at least one of `warp`, `gateway` or `colos`; an empty expectation is refused, so an editor that drops it cannot change a contract the user did not edit. `warp` takes `off`, `on` or `plus`; `gateway` takes `on` or `off`; `colos` takes at most 16 distinct three-letter codes (uppercase ASCII). A reported value outside a declared one is `policy` with stage `trace_assert` and code `warp_mismatch`, `gateway_mismatch` or `colo_mismatch`, and its detail names neither the observed colo nor any free text; a line the trace did not report is not a violation, and the passing detail names it, or says the body was not a trace. `expected_trace` is inside `http`, so it is part of the measurement contract: setting or changing it changes that check's contract and history. Older collectors/viewers reject the new field. Upgrade collectors and all viewers to 1.4.0 or newer before enabling it in a topology; default configurations omit the field and remain readable by older clients.

Default HTTP maximum response body size is 65,536 bytes (configurable up to 1 MiB). Exceeding this budget reports a monitor configuration error rather than target downtime. Probes, like every Topophany client, ask for no compression and decode none, so the budget, and a Mihomo controller's 2 MiB, count the bytes the peer sends: a body the server compresses anyway is checked as sent, and `contains` and `body_regex` see it compressed. Throughput stress-testing modes are not provided; heavy payload validations should be performed via authorized external diagnostics.

### Proxy Chains and Adapters

`proxies` defines outer connection hops in execution order: `http-connect`, `https-connect`, `socks5`, or an initial `ssh` jump hop. HTTP and SOCKS credentials reference `username` and `password_env`. `https-connect` verifies outer TLS using `server_name` and `ca_file`. SSH hops specify `ssh_user`, `ssh_identity_file`, and `ssh_known_hosts`. Give the two files as absolute paths: a relative one is resolved against the worker's working directory, which under systemd is not the directory you wrote the topology in. A leading `~` and `${...}` are refused rather than expanded.

A TCP check over an SSH jump hop alone returns `unknown / ssh_forward_unverified`: spawning an SSH subprocess cannot verify remote forward completion without application payload. Use TLS, HTTP, or SSH banner checks to obtain definitive protocol evidence. SSH file paths are normalized to absolute paths, rejecting quotes, control characters, a leading `~`, and `${...}` expansions. Ambient SSH agent forwarding is disabled.

Three-hop sequence: `Local Observer -> CONNECT Ingress -> SOCKS5 Transit -> CONNECT Egress -> Target`. Complex layered proxy protocols do not need to be decomposed into native primitives: direct probes toward a dedicated local SOCKS listener, and document internal hops in `path.nodes` and `adapter.root_proxy`.

`mihomo` adapter supports `token_env`, `root_proxy`, `expected_mode`, and `expected_providers` (mapping provider names to ordered node name arrays). Mismatched runtime members or ordering emit `policy` outcomes; identical names with differing internal parameters cannot be distinguished. Unresolved routing groups, load-balancing groups without single selections, and dependency loops report unknown status. TUN status and modes reflect controller reports rather than kernel routing table inspections.

The full controller proxy inventory is bounded at 4096 entries (including
built-ins and groups), with up to 4096 members per group/provider and 8 MiB per
inventory response. `/configs` retains its 2 MiB limit; unrelated objects,
history arrays, depth and duplicate keys remain strictly bounded. Inventory and
member overflows report `proxy_inventory_capacity_exceeded` and
`proxy_members_capacity_exceeded` as monitor errors. See the
[adapter capacity notes](../internal/adapters/mihomo/README.md#inventory-capacity).

For rotating providers, `adapter.provider_reference` can select fresh ordered
member lists from an independent HTTPS JSON publisher. Keep fixed groups in
`expected_providers`; unavailable or stale reference data is a monitor error,
and a publication changing during sampling is unknown. `settle_seconds`
(0..3600, default 120, 0 disables, and shorter than `max_age_seconds` when
set) is how long after `published_at` a difference is treated as not yet
confirmed: the controller loads a new provider over a minute or two. A
difference inside that window remains a policy sample, with
`provider_reference_settling: true`, code `provider_membership_mismatch`, and
`provider_comparison` saying `=settling`; the engine opens the incident only
after the window's equivalent consecutive policy samples (and at least
the effective `policy_threshold`), so repeated publications cannot defer it forever.
Matching membership is never deferred. Facts record
`provider_reference_published_at` (publisher), `provider_updated_at.<name>`
(the controller's own load time for that provider, reference providers first,
while the observation stays within the collector's 32 facts and each complete
key fits 128 bytes) and the sample's own time. Optional load-time keys that do
not fit are omitted; provider membership is preserved. Credential-bearing
controller responses omit raw load timestamps and numeric comparison counts;
closed match/mismatch/settling labels and independent load-pending evidence
remain available.
`provider_reference_load_pending` records load evidence independently of the
worker's wait. The collector applies its current window at the sample's time,
including after a reload and for queued samples. Legacy samples lacking this
evidence conservatively wait within the current publication window, except
for explicitly known mode/TUN violations.
An explicit `settle_seconds` is a new topology field: upgrade every collector
and worker before adding it, because older binaries reject unknown topology
fields. Omitting it uses the default and needs no topology change. See the
[Mihomo adapter guide](../internal/adapters/mihomo/README.md) for the document
format, transport and freshness limits.

A single check supports up to 16 static and dynamic providers combined, with provider names capped at 119 bytes without control characters to respect API record budgets. Long strings are truncated at UTF-8 byte boundaries; DNS response evidence exceeding 2,048 bytes returns an explicit check error.

## Collector Authentication

```json
{
  "schema": 1,
  "tokens": [
    {"token_sha256": "REPLACE_WITH_SHA256_FROM_TOPO_TOKEN", "role": "viewer"},
    {"token_sha256": "REPLACE_WITH_SHA256_FOR_WORKER", "role": "worker", "observer": "observer-local-01"}
  ]
}
```

The sample above illustrates schema structure and is not an active credential. `topo token` outputs a `{token, token_sha256}` pair generated using high-entropy random bytes. Never copy test tokens into production. Assign a separate worker token to each observer; viewer tokens have no write permissions. Changes to `auth.json` apply when the collector reloads (`SIGHUP`) or restarts; a reload that fails validation keeps the tokens in force. A request is served under the tokens and topology it was admitted with: a batch still being sent when a reload lands is refused with `503 topology_reloaded` and sent again under the new ones, so a revoked token stores nothing once the reload is done. A snapshot whose state has not yet been captured also returns `503 topology_reloaded` with `Retry-After: 1` after a reload, rather than mixing old authorization with new storage or incident metadata. Send a new request to authenticate against the current tokens.

Client and worker credential files use the following format, with separate files referencing distinct token environment variables:

```json
{
  "schema": 1,
  "active": "primary",
  "profiles": {
    "primary": {
      "project": "example-network",
      "servers": [
        {"url": "https://monitor-a.example.invalid:7447", "token_env": "TOPH_VIEW_TOKEN"},
        {"url": "https://monitor-b.example.invalid:7447", "token_env": "TOPH_VIEW_TOKEN_B"}
      ]
    }
  }
}
```

Specify either `token` or `token_env`, with up to 8 server addresses. Worker credential files must reference tokens authorized for the worker role on each server. Client read-only failover engages on connection errors, 5xx server errors, rate limits (429), or stale snapshots. Failover is refused on 401/403 authorization failures, project/observer mismatches, and malformed responses.

If no collector answers and a collector explicitly returned `429 snapshot_busy`,
snapshot reads wait one second and retry the configured collectors once. The
wait is cancellable, does not extend with the server's retry header, and does
not delay a successful failover. A viewer keeps drawing its existing evidence
during the read; its usual expiry rule still applies. Other rate limits and
authentication or validation failures receive no extra retry.

## Alerts

A collector started with `--alerts alerts.json` tells webhooks what it concludes. Webhook URLs are secrets, so the file is private to the collector like `auth.json` (a regular file, mode `0600` or stricter, unknown fields refused), never part of the topology that workers and viewers receive:

```json
{
  "schema": 1,
  "webhooks": [
    {"id": "phone", "format": "ntfy", "url": "https://ntfy.sh/REPLACE_WITH_A_PRIVATE_TOPIC"},
    {"id": "team", "format": "slack", "url": "https://hooks.slack.com/services/REPLACE", "events": ["incidents"]},
    {"id": "ops", "format": "json", "url": "https://ops.example.invalid/topophany", "bearer": "REPLACE", "observers": ["observer-local-01"]}
  ]
}
```

| Field | Meaning |
|---|---|
| `id` | Names the webhook in logs, which never show its URL |
| `format` | `json` (the event as data), `ntfy` (text, with a title, tags and high priority for an opened incident or a silent observer), `slack` (incoming webhook, markup escaped) or `discord` (mentioning no one) |
| `url` | HTTPS; plain HTTP only to a literal loopback address or with `"allow_insecure": true`. A query may carry the service's own token; credentials in the authority are refused |
| `bearer` | Optional; sent as `Authorization: Bearer …` |
| `events` | `incidents`, `observers` or both (the default) |
| `observers` | Optional; only what these observers saw |

There are four events: `incident.opened` and `incident.closed` when the incident ledger opens or closes a record (a recovery, an unconfirmed ending, a pause or a change, with how long it lasted), and `observer.silent` and `observer.back`. An observer is called silent once the collector has settled after starting (as long as its stalest check takes to lapse), once per silence, and called back only after it was called silent. A `json` webhook receives `{"schema": 1, "project", "collector", "event", "at", "title", "text"}` and either `incident` (the ledger record) or `observer` (its state).

Each webhook has its own queue of 64 alerts, delivered in order, at least once. When it is full, an alert from an observer with fewer waiting takes the place of the newest from the observer with the most, so a worker cannot crowd other observers' alerts out of a webhook they share however many incidents it makes for its own; any other new alert is dropped. Either drop is logged with the observer it was from. An alert is tried again after a failure to connect or a 408, 429 or 5xx status, with pauses doubling from 2 seconds to 5 minutes, until it has waited a day, when it is dropped and logged; any other status, including a redirect, which is never followed, drops it at once. Environment proxies are not used. Response headers are limited to 32 KiB over HTTP/1 and HTTP/2; response bodies are read only up to 4 KiB, with a ten-second request timeout. What waits is kept in `alert-queue.json` in the collector's data directory (mode `0600`; it holds alerts, never a webhook's URL or bearer), rewritten whenever an alert is queued, sent or dropped, so a collector that stops sends what waited when it starts, and logs how many wait for each webhook and how many it has sent and dropped. A reload keeps what waits for a webhook by its `id`, sent as the webhook is now configured; what it no longer takes, by event or observer, is dropped, as is what waits for a webhook removed. A queue file the collector cannot read stops it from starting rather than being replaced; move it aside to start a new one. Each collector with an alerts file sends its own, so give the file to one collector of a pair unless duplicates are wanted. The alerts file applies when the collector reloads (`SIGHUP`) or restarts; one that fails validation is refused with the rest of the reload.

Alert titles and text are clipped before queue admission to the same bounds
used for delivery (250 and 1500 bytes, plus a truncation marker). Each encoded
event has a share of the 16 MiB restart read budget, reserving space for all
16 webhooks, 64 entries each and queue metadata. An oversized event is dropped
and logged before it can evict another observer's alert. A final byte check
preserves the last readable queue file if persistence would exceed that budget.

## HTTP API Specifications

`topo serve --web-origin https://topophany.com` enables browser reads for one
exact origin. The default is empty (no CORS opt-in); wildcard origins, paths,
credentials and non-loopback HTTP origins are rejected. CORS preflight only
allows `GET` with `Authorization` on `/v1/snapshot`, `/v1/config`,
`/v1/history` and `/v1/rollups`. Actual reads still require a viewer token;
preflights pass through the existing anonymous admission/rate budgets and
never promote a socket to authenticated. Cookies and worker ingestion are
not enabled by this option. The origin is fixed at startup, independently of
configuration/auth reloads.

| Method / Path | Role | Description |
|---|---|---|
| `GET /healthz` | Unauthenticated | Indicates collector HTTP daemon is running; does not indicate observation freshness |
| `GET /v1/snapshot?observer=observer-local-01` | `viewer` | Returns current topology, evidence states (checks with their public contracts, nodes, endpoints, services, paths, links), observer liveness with whether each measures the current topology, and incidents; `observer` query param is optional; current snapshots are schema 3 and external bindings appear in `external` without entering measured aggregates |
| `GET /v1/config` | `viewer` | Returns deep-copied sanitized topology and revision hash: endpoint addresses and check targets lose their user information and fragment and have any query replaced by `redacted`, and secret references, request headers, CA files and proxy credentials are removed, an SSH hop's `ssh_user`, identity and known-hosts files among them |
| `GET /v1/history?check=ID&limit=100` | `viewer` | Returns the check's raw observation samples under its current contract, newest first (limit: 1..500), with the public revision and contract; `topo history` reads it |
| `GET /v1/rollups?check=ID&step=1h&span=7d` | `viewer` | Returns the check's long-term rollups under its current contract in `slots`, oldest first, with their `total`, `since` (the earliest moment the collector holds) and the public revision and contract; see [rollups](#rollups); `topo history --step` reads it |
| `POST /v1/observations` | Scoped `worker` | Ingests observation batches and optional heartbeats (up to 100 observations / 1 MiB per batch) |

Authentication requires the `Authorization: Bearer <token>` header. Batch writes require `Content-Type: application/json` (optional parameters such as `charset=utf-8` are permitted). Observation IDs follow `<observer>.<boot>.<sequence>`, where `sequence` is non-zero and `boot` is a valid identifier without dots to prevent cross-scope ID collision. `SampledAt` specifies local sample timestamp; `ReceivedAt` must be omitted or zero, and is populated by the collector.

Heartbeat-only durable updates are limited to one per second per observer,
including across token changes and collector restarts. An advancing heartbeat
inside that interval receives `429 observer_heartbeat_rate` with
`Retry-After: 1`; it is not acknowledged or persisted. Unchanged heartbeats
remain idempotent. Heartbeats attached to new observations share their durable
write; duplicate observations do not bypass the heartbeat limit. The latest
heartbeat's collector-owned `ReceivedAt` sets the interval; sender timestamps,
boots and topology revisions cannot reset it. A collector clock stepped back
restarts the interval at the new time instead of refusing heartbeats until the
clock catches up. Observation quotas still reserve
one liveness slot per observer.

Current batches are schema 3 (`"schema": 3`) and may carry normalized external reports. A collector still accepts a schema 2 batch as native-only; a schema 2 observation carrying external evidence is refused, not silently stripped. The batch's `revision` is the SHA-256 of the sender's normalized topology and only describes the sender; the acknowledgement echoes it. Each observation carries the `contract` of its check, which must equal the collector's contract for that check, or the batch is refused with `409 contract_mismatch`. A check's contract is the SHA-256 of what it measures: project, check ID, observer, kind, target (the endpoint's address when `target` is empty), timeout, `bind_ip`, `resolve_ip`, `server_name`, `ca_file`, the `http`, `dns` and `adapter` options (except `provider_reference.settle_seconds`, which only defers confirmation), and the proxies of its path in wire order. Nothing else enters it: names, notes, locations, intervals, staleness and confirmation thresholds, `disabled` and `maintenance`, what the check is attributed to (endpoint, service, path, link) and every other check can change without disturbing its samples, its history or the ID of an incident it is part of. `topo validate` prints every contract. Viewers see public contracts, computed over the redacted topology like the public revision, so they permit no guess at hidden fields.

Health confirmation summaries record the staleness rule used to fold their
samples, separately from measurement contracts. Changing `stale_seconds`
recomputes confirmation from retained observations; a reclaimed prefix cannot
be reconstructed and contributes no confirmation under the new rule. Worker
local panels retain only their latest sample and reconfirm from that evidence.
The same rule applies on checkpoint restart, including older checkpoints whose
summaries have no rule marker. Names and confirmation threshold edits preserve
compatible summaries; raw observation history remains compatible throughout.

Native health confirmation advances in observation order up to a bounded
summary watermark. A sample arriving at or before that watermark is retained
in raw history but does not rewind the folded failure/recovery confirmation.
It never replaces the latest observation. Thus the same historical samples can
produce different current confirmation states when delivery order crosses that
watermark. Rollup corrections follow their separate bounded reorder window;
they do not imply current-health correction. A later staleness-rule change
reconfirms from available evidence, including any retained late samples.

A snapshot from a collector that saw TLS handshake failures also carries
`ingress`: `window_seconds`, `failures`, `source_count`, `last`, and up to
eight busiest `sources` (`address`, `count`, `last`) inside a rolling
30-minute window. The window is counted in sixty equal buckets, so a source
that keeps failing does not accumulate a lifetime total; a bucket at the old
edge may be omitted, so `failures` has one bucket's (30-second) resolution. `source_count` is
the number of distinct addresses the counter tracked; `truncated` says more
addresses were seen than it can track, making that count a lower bound.
`failures` counts every failure in the window, including addresses omitted
from `sources`. It counts failures reported by the HTTP server's own error
log, before any request carried an observer identity. Read it as evidence
about delivery into the collector, not about a target: a failed handshake
means that observer's evidence did not arrive, so the checks behind it read
stale rather than failed, and a source stays an address because an observer
names itself only after the handshake succeeds. Viewer snapshots replace each
source address with a short keyed digest by default (stable for the
collector's lifetime, so it cannot be reversed by enumerating IPv4 addresses
offline); start the collector with `--expose-ingress-sources` to include the
raw addresses, an explicitly privacy-sensitive choice. `topo status` prints the same summary as
`Collector ingress` when the window holds any failure.

A collector also accepts a schema 1 batch (no `schema`, observations with `revision` and no `contract`) from a worker not yet upgraded, as long as its `revision` is the collector's own; such samples count until the topology changes in any way. Upgrade collectors before workers: a 0.3 collector refuses schema 2 batches, and the worker quarantines them. Current snapshots are schema 3 and carry `external` views when external state exists. A current client reads schema 2 native snapshots as well; a schema 2 reader refuses a schema 3 external snapshot rather than silently omitting evidence. External sources and report state require the upgrade guide in [EXTERNAL-ADAPTERS.md](EXTERNAL-ADAPTERS.md). Besides the contracts, a schema 3 snapshot carries `endpoints`, the state of each endpoint from the checks that target it, and `external`, the bounded reported views for external bindings, and for each observer `contracts`: `current` when its executor last reported measuring what the collector counts, `outdated` when it measures an older topology (its samples of changed checks are refused until it reloads), or absent when it has not said. Workers report this in their heartbeats as a digest of their observer's contracts. A snapshot also carries `ledger`, what the collector's incident ledger remembers of the incidents in its view: the records open (`state` `open` or `recovering`), then at most 50 closed in the last day (`state` `closed`, with `closed` and `ended`: `recovered`, `unconfirmed`, `paused` or `changed`), newest first. Each record has the incident's `id`, `scope`, `summary` and failed `checks`, `since` (the earliest failure the ledger saw), `opened` (when the collector first counted it) and `previous`, the earlier records of the same incident it holds. Clients read a record as what the collector concluded, not as evidence. A schema 2 client ignores fields it does not know, so later releases can add some without breaking it; it passes over a collector serving another schema, and says so if no collector serves its own. 0.8 clients therefore read only 0.8 collectors.

HTTP `202 Accepted` indicates successful storage and returns confirmed batch count. HTTP `409 Conflict` indicates project, contract (or, for schema 1, revision), or content mismatch. HTTP `403 Forbidden` indicates unauthorized observer scope or role. HTTP `422 Unprocessable Entity` indicates sample timestamps falling outside the ingestion window. HTTP `429` and `503` are retryable. Validation errors reject the entire batch without partial commits. External custom workers must adhere to `model.Batch` and `model.Observation` field schemas and budgets; `topo once` terminal output does not imply ingestion into a collector.

Remote configuration mutation, shell execution, and administrative actions are not supported. The API serves as a clean boundary for future web or TUI frontends rather than exposing database and log files directly.

### Rollups

A collector rolls every sample it accepts up into its check's rollups (see [operations](OPERATIONS.md#storage-and-disaster-recovery)). A rollup covers `seconds` from `start`. Each sample's outcome stands from when it was taken until the next sample or until it goes stale under the topology the collector counted it with, and the rollup says how long each stood: `up_seconds` (ok), `degraded_seconds` (policy), `down_seconds` (fail) and `unknown_seconds` (error or unknown: the monitor could not tell). `unmeasured_seconds` is the rest, when no current sample stood for it; it is never up or down. Availability is `up_seconds` over the measured time, `up_seconds + degraded_seconds + down_seconds`, read together with the share of `seconds` that was measured. `unreconciled_samples`, when nonzero, counts samples in intersecting buckets whose interval boundaries were unavailable after retention, budget eviction, the per-sample correction work limit or upgrade from an older checkpoint, or conflicted at an identical timestamp; this count can exceed `samples` when a partial bucket contributes time but its sample counts are omitted; time totals are incomplete, so clients do not present them as an uptime percentage. `samples` counts the samples taken within the rollup and `outcomes` the samples by outcome; `latency` estimates what the passing ones took (`samples`, `p50_ms`, `p95_ms` and `max_ms`) from histogram bins a quarter of a doubling wide.

`first_byte` splits what the passing samples' first byte took by the cache class of the same sample, with the same `samples`, `p50_ms`, `p95_ms` and `max_ms` fields and the same bins: `cached` for `HIT`, `REVALIDATED`, `STALE` and `UPDATING`, `origin` for `MISS`, `EXPIRED`, `BYPASS` and `DYNAMIC`. A cache status outside both lists, including none, is unclassified: it is kept in the store so the classes stay separate but is not reported, so the two reported classes never include an answer the edge did not classify. First-byte time is kept for passing samples only, as `latency` is. `pops` counts every sample whose response named a colo, by exactly-three-uppercase-letter code; a malformed or unknown code counts under `other`, and a response with no colo is not counted. Vercel's reported region is not a physical colo, so a Vercel response contributes no `pops` count; its first-byte time and cache status are counted like any other response's. `cache` counts the cache status of every sample an edge answered, by the closed vocabulary the edge headers keep, with an absent or unknown status under `other`; a sample no edge answered (a TCP, TLS, DNS, ICMP or host check, or an HTTP check with no edge in front) is not counted and carries no `cache`, so `other` always means an edge answered without a status. A failing edge answer is counted in `pops` and `cache` even though its first-byte time is not.

A bucket keeps at most sixteen named colos plus `other`. A new code enters only when its first sample ties the smallest named count, displacing that count into `other`; otherwise its sample counts as `other`. A code already named grows without rechecking, so a code rare when it appeared can become the largest, and ties evict the lexicographically largest held code. On a merge the smallest counts move into `other`. A window merges its buckets' maps uncapped and applies the same sixteen-code cap once, so a code that is small in every bucket but large over the day stays named. Views lower-bound each share, never rounding up, so independently listed shares cannot add to more than 100%.

On disk these are the optional `fb`, `p` and `c` keys in the same schema 1 bucket. `fb` is absent or exactly three histogram lists in class order, each in the same shape as `l`, decoded by the same validator, so the two fields cannot drift; the writer emits all three once any class has a sample. Counts in `p` and `c` are positive, as the writer never emits a zero. A bucket of samples no edge answered writes no `c`. The reader refuses a file whose timestamps cannot be a time, as corrupt rather than clamped: a series' `f`, `u` and `c`, the `at` and `stale` of its open sample `o` and of each `r` entry, and a bucket's `s` must lie between 1970 and the end of the year 9999, and a bucket's `w` must be 300, 3600 or 86400. A checkpoint written before these keys reads as no first-byte time and no label counts, and a checkpoint that carries them is still read by an older binary, which ignores JSON keys it does not know. No upgrade is needed to keep a collector running, but an older viewer shows none of the new fields; upgrade viewers to see them.

`/v1/rollups` lays them out in slots of `step`: `5m`, `1h` (the default) or `1d`, aligned to UTC, over `span` before now, written as minutes, hours or days such as `90m`, `24h` or `30d` (by default a day of `5m`, a week of `1h` or 90 days of `1d`; at most 800 days and 999 steps). A collector keeps rollups five minutes wide for 48 hours, an hour wide for 32 days and a day wide after that; where they are wider than `step`, a slot is as wide as what is kept, so slots may grow wider into the past. The first slot holds the start of the span and the last ends now. `step_seconds` and `generated_at` say how it was laid out. A step or span outside these bounds is refused with `400 invalid_step` or `400 invalid_span`.

Each check in a snapshot may carry `availability`: its rollups over each `window` ending when the snapshot was generated, `24h`, `7d` and `30d`, in the fields above, with `since` when the rollups begin inside the window. A check the collector holds no rollups of has none, and a snapshot over its budget leaves them out before it leaves out last observations.

## Environment Variables

The terminal interface (`topo`, `topo edit` and `topo preview`) reads these.

| Variable | Effect |
|---|---|
| `NO_COLOR` | Any non-empty value turns color off, as `--no-color` does: no escape for color is written and selection is shown with plain glyphs such as `›` and `[a]` |
| `COLORTERM` | `truecolor` or `24bit` turns on 24-bit color. Windows Terminal, iTerm2, WezTerm, VS Code, Ghostty, Hyper, Tabby and kitty are recognized without it; other terminals get xterm-256 |
| `TOPO_BACKGROUND` | `terminal` (or `none`, `transparent`) keeps the terminal's own background instead of the UI's dark ink |
| `TOPO_HYPERLINKS` | `1` (`yes`, `on`, `true`) or `0` (`no`, `off`, `false`) decides whether links are written as OSC 8 hyperlinks; by default they are, except on the Linux console, a dumb terminal and GNU screen outside tmux |
| `TOPO_CELL_ASPECT` | A character cell's height over its width, such as `2.6`, for drawing the About globe and the geographic map in their true shape. When unset, the shape comes from the pixel size the terminal reports, else from its answer to `CSI 16 t`, else is taken as `2`. Windows Terminal answers with a fixed 10×20 cell whatever its font and line height, so it needs this where its cells are not 2:1. Values outside 1 to 4 count as `2` |
| `LC_ALL`, `LC_MESSAGES`, `LANG` | The first that is set chooses the language of `topo preview`'s built-in topology and scenarios; `--lang` overrides it |

### Storage allocation

Observers accept `storage_weight` (omitted/zero means 1; otherwise 1–1000). This trusted administrative setting changes quota allocation, not check identity or authorization scope. `/v1/snapshot` adds optional `storage`: global `receipts`, `max_receipts`, `state_bytes`, `max_state_bytes`, `receipt_hours`, and `observers` containing `observer`, `receipts`, `receipt_limit`, `bytes`, `byte_limit`, `last_hour_receipts`, `settled_receipts`, `fills` and `full_in_ns`. An observer-filtered view includes only that observer's allocation rows. Legacy snapshots may omit it. `topo capacity --help` describes offline preflight flags.

### Central address definitions

An optional top-level `addresses` object maps up to 256 stable IDs to literal IPv4, IPv6 or DNS hostnames. For example, `"addresses":{"dmit-public":"154.17.5.197"}` and an endpoint `"address":"${dmit-public}:22"` share one declared address. IPv6 references in host:port targets need brackets: `[${entry-v6}]:443`.

References resolve in endpoint addresses; check `target`, `bind_ip`, `resolve_ip`, `server_name`, DNS names/expected IPs, HTTP expected exits and adapter peers; and proxy addresses/server names. They never read environment variables or interpolate credentials, filesystem paths, commands or notes. Definitions cannot be recursive, URLs or secrets. Missing/malformed references fail validation; expanded fields are bounded to 8192 bytes. See `examples/address-registry.json` (paused, documentation addresses only).

`topo address --config topology.json` lists definitions. `--set NAME=IP_OR_HOSTNAME` validates and atomically updates the local file; `--delete NAME` refuses definitions still referenced. `--dry-run` previews the change. Output lists `changed_checks`: precisely the checks whose resolved measurement contract changes. Distribute/reload the reviewed topology to apply it; the command performs no remote writes.

Local config, the editor and deployment bundles preserve references. Public topology and server cards show resolved addresses; aliases themselves are omitted from public topology. Replacing a literal with an equivalent reference, renaming a definition or changing an unused definition preserves measurement history. Changing an address starts new series only for dependent checks. Keep stable names and purposes in labels/notes and let the address rows show current endpoints, avoiding a second copy of IPs in prose.


### Native Windows host resources

Windows host checks call Win32 APIs directly, without PowerShell, WMI or an external agent: `GetSystemTimes` for CPU deltas, `GlobalMemoryStatusEx` for physical memory, `GetDiskFreeSpaceExW` for capacity, and Toolhelp/`K32GetProcessMemoryInfo` for visible process counts and selected working sets. The sampler queries every processor group on private threads when needed; a thread whose affinity changed is retired instead of returned to the scheduler. CPU sampling and process enumeration respect cancellation; counters and arithmetic are bounded. The API definitions are documented by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getsystemtimes).

Use a clean local drive path such as `C:/` or `D:\Data`; drive-relative paths, UNC paths and traversal are refused. The default `/` means the worker's current drive root. A Linux/macOS collector can validate a Windows drive path without interpreting it as its own filesystem.

`memory_basis` is `windows_AvailPhys`. Windows capacity is caller-relative under quotas: `disk_basis=windows_caller_quota`, total and available are the caller's counters, and used is their difference; the separately reported `disk_volume_free_bytes` is volume-wide. Inode counters are unavailable (`disk_inodes_basis=unavailable_on_windows`), and POSIX zombie semantics do not apply. See [GetDiskFreeSpaceExW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getdiskfreespaceexw).

Process matching is case-insensitive against the executable basename or its stem without `.exe`; the existing 15-byte configured-name bound still applies. Only selected names' counts/RSS are retained, never the inventory, arguments, environment or user names. RSS uses query-limited process handles; protected/unreadable selected processes produce a monitor error, and processes that exit during enumeration are skipped. Shared pages can be counted in more than one working set. Native acceptance must run on Windows; successful cross-compilation alone does not test these APIs.

## Persistent monitor panels

For RunCat Neo, use the [five-row custom metrics example](../examples/runcat/README.md).
It publishes RunCat's JSON format with host-online and application-health bars.

`topo monitor` manages named, local display panels. Templates use Topophany metric
names and contain no executable expressions. File imports and stdin imports save
the template content; the original file is not needed afterwards.

```sh
topo monitor configure desktop --template examples/monitor-desktop.json --source client --profile default
topo monitor run desktop
topo --monitor desktop

topo monitor configure desktop --output ./topophany-metrics.json --interval 10s
topo monitor show desktop
topo monitor path desktop
topo monitor preview desktop
topo monitor export desktop
topo monitor metrics
topo monitor configure desktop --enabled=false
```

`--template -` reads one bounded JSON value from stdin, including a quoted heredoc.
`export` prints the saved template. `metrics` lists metric names, types, units,
selectors and supported sources. `path` prints only the output path. `preview`
reads the last output and applies current freshness deadlines without writing or
contacting a source. `show` reports saved configuration, observed publisher state,
and the running configuration revision when its publisher lease is current.

A first configuration requires a template. Later calls patch only explicitly
provided settings. Invalid changes leave the previous configuration intact.
**Changes take effect after restarting the panel's process.** In particular,
`--enabled=false` prevents subsequent attachment/start; it does not stop a running
Collector, Worker, TUI or standalone panel. A configuration command never installs
or starts a background service.

Configuration schema 1 embeds the template in one `config.json`, so configuration
and template commit in a single durable replacement. Its default location is
`<user-config-dir>/topophany/monitor/<name>/config.json`. The rebuildable output
is `<user-cache-dir>/topophany/monitor/<name>/metrics.json`. `--monitor-dir DIR`
selects an explicit configuration root on every monitor command and on `topo`,
`serve` and `worker`. Relative output/credential paths become absolute during
configuration. Output paths must end in `.json`. Existing unrelated files,
symlink output targets, protected configuration names and attached-process data
directories are refused. Parent directory aliases are resolved before locking.

The configuration stores the named credential profile and absolute credential
file path, never the bearer token. The active profile is resolved once at first
configuration; changing the credential file's active profile later does not
retarget the panel. An existing profile pins its project. A changed project
requires explicit reconfiguration. Offline entities are allowed; unknown metric
names, unsupported source capabilities, invalid row references and incompatible
formats fail validation.

### Streaming panels

`topo monitor stream` reads one JSON request per line on stdin and writes one
JSON frame per line on stdout. Client/preview create no panel configuration,
output file or lock. Direct may lock/read/atomically renew credentials under
its private direct root; it writes no metrics journal or history database.
See [Direct v1](contracts/direct-v1/contract.md) for the configuration API,
capabilities, sources.status, origin, dataset identity and expiry rules. `topo monitor stream --capabilities` prints one line with protocol 1, the
binary version, sources, visuals and the metric catalog, then exits. The catalog
includes `servers.list`, `service.card` and `services.list`; `profiles: true`
advertises saved-profile discovery. These additions retain protocol 1. Consumers
of older binaries treat an absent `profiles` field as false and check the catalog
before requesting `servers.list`, `service.card` or `services.list`.

`topo monitor stream --profiles` lists saved client profiles in one JSON line,
then exits 0:

```json
{"profiles":[{"name":"home","project":"homelab","active":true},{"name":"work","project":"","active":false}]}
```

The active profile comes first, followed by other names in lexical order, at most
64 entries. `active` identifies the profile an empty request `profile` selects.
Only names, projects and the active flag are exposed; tokens, collector URLs and
file paths are omitted. An unset project is `""`. Listing does not validate a
profile's connection or contact a collector; normal client connections still
require valid credentials and a project.

`--profiles --credentials PATH` reads that file instead of the normal credential
path, retaining the private-file permission and 1 MiB size checks. No credential
file, or no saved profiles, produces `{"profiles":[]}`. An unreadable, unsafe or
invalid file also produces an empty list and exit 0, with one fixed diagnostic on
stderr. The command reads no stdin and writes no files. `--capabilities` and
`--profiles` are mutually exclusive; the command-line `--credentials` option is
for `--profiles`, while a running stream takes credentials in each request.

A request has `schema: 1`, an optional `request_id` (at most 64 bytes), `source`
(`client` by default), `interval` (5s by default, 1s–1h), and a schema-1
`template`. Client requests accept an absolute `credentials` path, a saved
`profile`, a `project` guard and an `observer` view. Empty credentials/profile
use normal credential discovery and the active profile. Credentials are read
only for the client source. Preview requests accept `scenario` (0 by default)
and `lang` (`en`, `ja` or `zh`, default `en`); they read synthetic evidence
from the same simulator as `topo preview`, without credentials or host probes.

For example, [monitor-stream-preview.json](../examples/monitor-stream-preview.json)
requests all six structured evidence metrics, including the server chooser and
one selected server card:

```sh
topo monitor stream < examples/monitor-stream-preview.json
```

The example is one NDJSON request line. After its first frame, file EOF produces
a final stopped frame; an interactive consumer keeps stdin open for updates.

No frame precedes the first request. A valid request replaces the current one
and publishes immediately, with a new `config_revision` and the echoed
`request_id`. A client starts with unavailable rows while its first collector
read is pending. A replacement client source checks its first read within a
five-second budget while the previous request continues publishing; only then
is the replacement committed. If that read does not finish within the budget,
the replacement is committed with unavailable rows, never with a partial
result. A foreign-project response rejects the candidate.
An invalid request publishes an `error` frame with no rows and
keeps the previous request running. Error codes are `invalid_request`,
`unsupported_protocol`, `credentials_unavailable`, `profile_not_found` and
`project_mismatch`; messages contain no raw upstream errors and are bounded to
1024 UTF-8 bytes. An invalid request ID longer than 64 bytes is omitted from
the error's identity (the field is empty). A collector that
cannot be read produces running frames with null values and
`reason: "source_unavailable"`.

Requests are bounded to 256 KiB per line; an oversized line is drained and
rejected without ending the stream. Frames are bounded to 1 MiB, each written
once and flushed. Values too large for a frame are withheld with
`reason: "frame_budget_exceeded"`. EOF, SIGINT or SIGTERM publishes one final
`stopped` frame with an empty `rows` array. A closed stdout ends quietly.
Runtime failures exit 0; only bad command-line flags exit 2.

The stream uses the same source refresh, one-second evidence aging, display
cadence, immediate safety transitions and publisher lease as file panels below.
Structured rows keep the original deadlines of the evidence behind their
collector judgments. When a deadline passes, the row's value is withheld until
a new snapshot supplies a current judgment; aging a check never extends an old
healthy service or node. Server card row deadlines cover node and displayed service judgments, including
transitive service and path dependencies. Resource readings and nested observer
verdicts carry independent deadlines: expired readings lose their numbers, and
expired observer verdicts become cached, without discarding the card. The source
envelope and publisher lease still bound the entire card.
Its function API in `internal/monitor/stream` accepts request lines and a frame
callback, honors context cancellation, and allows another source adapter for
embedding. Every callback receives an independently writable frame, including
the first frame and nested metric values.

### Sources and process ownership

| Source | Process | Scope |
|---|---|---|
| `client` / `preview` | `topo monitor stream` | Requests on stdin; saved connection or synthetic evidence |
| `client` | `topo monitor run NAME` | Saved profile; optional `--observer` configured view |
| `client` | `topo --monitor NAME` | The TUI's actual fetch results, including view switches and failures |
| `collector` | `topo serve … --monitor NAME` | Current collector's complete project view, read without persistence side effects |
| `worker` | `topo worker … --monitor NAME` | This worker's local observations, threshold state and separate delivery reports |

The source must match the process role. Optional configured project/observer
bindings must match at startup. An attached TUI also checks the saved profile's
project and collector URLs against its actual connection; a mismatch fails
explicitly. Its panel shares the TUI fetches, without opening another polling
connection. Worker panels cannot use `summary.status`; `local.status` describes
local checks and does not assert successful upload or global project health.

For example:

```sh
topo monitor configure collector-panel --source collector --template examples/monitor-desktop.json
topo serve --config topology.json --auth auth.json --data collector-data --monitor collector-panel

topo monitor configure worker-panel --source worker --template examples/monitor-worker.json
topo worker --config topology.json --credentials worker-credentials.json --observer local --data worker-data --monitor worker-panel
```

### Template and metric contract

Templates have `schema: 1`, `title`, optional symbolic `icon`, `bar.row` and an
ordered `rows` array (1–128 rows, at most 256 KiB for the saved configuration).
Each row has a unique `id`, optional `label`, `metric`, optional `select`, optional
`format`, `visual` and optional `limit`. `bar.row` references a row for a
system-bar consumer. Icons are symbolic names; consumer-specific
icon rendering is not implemented here.

The metric catalog includes:

- `summary.verdict` (`status`): the Overview verdict, glyph and text, check
  counts, configured total, incident count and live/total observers. Paused and
  maintenance counts are subsets of unknown and never add to the total.
- `observers.verdicts` (`list`): each observer's own verdict and counts, liveness
  and last evidence, ordered down, degraded, unknown, up, then by name; at most
  64 entries. A scoped stream also reads the complete snapshot for these
  verdicts. Scoped file and attached TUI panels use the evidence they already
  receive: the selected observer retains its verdict; other observers retain
  heartbeat liveness but have cached verdicts and unknown check counts.
- `attention.list` (`list`): the Overview's Needs attention list in its order,
  with a total, titles, routes, observers and incident start times. Its row
  `limit` defaults to 5 and accepts 1–20. Incident states are lowercase
  `view.Pill`; other states are lowercase `view.State`, or `up` when empty.
- `applications.list` (`list`): the Services tab's application/environment
  groups, evidence counts and services in topology order, with attention and
  unmonitored counts; at most 64 groups and 64 services per group.
- `server.card` (`card`): a required `select: {"node":"ID"}` names the server.
  Its card carries the node state, place, local resources (primary disk first),
  up to 32 services in trouble-first order, and observers with their verdicts.
  Stale, cached and unreadable resource numbers are null; original sample
  timestamps remain. An unknown node yields `entity_missing`.
- `servers.list` (`list`): up to 256 nodes with cards in the Servers tab, in that
  tab's topology order. External nodes and containers belonging to a declared
  parent are omitted; edges, platforms and unowned containers remain eligible.
  Each entry has `id`, `name` (ID when unnamed), `kind`, `provider`, `group`,
  `where`, `health` and `note`, matching its `server.card`. Use its ID as that
  card's `select.node`. Cached entries have unknown health. The row health is
  the worst listed health, and its text is, for example, `14 servers`. Its
  deadline is the earliest source-envelope or listed-node judgment deadline;
  unrelated service, resource and observer evidence does not expire the list.
  Per-node health and note changes publish immediately, even if the row's worst
  health stays the same. A readable empty topology produces `[]`; an unavailable
  source produces null with `source_unavailable`, as for other metrics.
- `service.card` (`card`): a required `select: {"service":"ID"}` names the
  service. Its card is the one the Services tab draws: the service's state,
  `evidence` and `reason`, application, environment and runtime; whether any
  check measures it (`monitored`) and, in a view scoped to one observer,
  whether another observer's checks do (`elsewhere`), so a card can say it is
  not seen from here rather than unmonitored; its `host`
  with that node's place (`where`) and state; `seen_from`, its checks by
  observer, the observer with the worst check first (at most 8, with at most 4
  checks each), each labelled by its place, or by its name less the place where
  two observers share one; each check's state, its own `valid_until`, the
  median and 95th-percentile latency and share up over its 24-hour rollup, its
  `first_byte` line, the `colo` its newest response named and its recent
  `samples`: at most 40 slots, oldest first, each an `outcome` (`none` where no
  sample was taken) with `elapsed_ms` when it passed, as the TUI's strip lays
  them out; `endpoints` with what stands in front of each (`via`, with each
  node's kind and provider); `answered_from`, the colos and cache statuses over
  the day; `depends_on` and `used_by` (at most 16 each); `runbook` and `memo`.
  Samples are read from the collector's history the first time a card shows a
  check, at most four checks a fetch, then extended from each snapshot; `slots`
  is null while none are held. At a check's own deadline its health becomes
  unknown, its freshness stale, and its latency, share up, colo and slots are
  null; its identity and clocks remain, and one check's deadline never expires
  the card. The row's deadline is the earliest of the source envelope, the
  service's, its host's and its dependencies' judgments. An unknown service
  yields `entity_missing`.
- `services.list` (`list`): up to 256 services in the Services tab's order,
  each with `id`, `name`, `application` (its name), `health`, `note`,
  `monitored`, `elsewhere` and `host` (`id`, `name`, `kind`, `provider`), matching its
  `service.card`. Use its ID as that card's `select.service`. Cached entries
  have unknown health; the row health is the worst listed, its text, for
  example, `23 services`, and its deadline the earliest of the source envelope
  and the listed services' judgments.
- `summary.status`: complete source-view verdict, independent of which other
  rows the template displays; available to client/collector sources.
- `local.status`: the worker's local view verdict.
- `check.status`: one check's current health.
- `host.cpu.used_percent`, `host.memory.used_percent`, `host.disk.used_percent`:
  numeric percentages in **0–100**, suitable for `format: "percent"` and
  `visual: "bar"`. CPU is whole-host normalized utilization over the sampler's
  window. Output includes `sample_window_ms` when available, and `basis` for
  CPU/memory/disk accounting; memory and disk retain the native sampler's
  platform-specific definitions rather than silently normalizing unlike metrics.
- `delivery.status`, `delivery.pending_records`, `delivery.pending_bytes`:
  worker transport reports, selected by optional collector identity digest.
  `ready` means no currently reported transport error, not proof all samples
  have reached the collector; read the pending counts alongside it.
- `storage.journal.write_bytes`, `storage.checkpoint.write_bytes` for collectors;
  `storage.queue.operations`, `storage.queue.write_bytes` for workers;
  `storage.sync.count`, `storage.sync.duration_ms` for both local service roles.
  These are counters since process start, not remote client metrics.

The eight structured metrics above support client and collector sources; preview
uses the client capabilities. Each requires its listed visual. Existing metrics
keep `text` and percent metrics also accept `bar`. `limit` belongs only to
`attention.list`; `select.node` belongs only to `server.card` and
`select.service` only to `service.card`. Validation errors identify the row.

Check metrics accept `select: {"check":"ID"}`. Delivery metrics accept
`select: {"collector":"16-character-lowercase-hex-digest"}`. A missing selector
is permitted only when runtime matching yields exactly one entity. Zero matches
produce `entity_missing`/`never_reported`; multiple matches produce
`selector_ambiguous`. There is no implicit aggregation. With multiple delivery
destinations, set the collector digest explicitly in each delivery row.

### Output schema and freshness

Panel output has its own `schema: 1`: `name`, `config_revision`, `instance`,
`sequence`, `source`, `project`, optional `observer`, `lifecycle`, `generated_at`,
`publisher_expires_at`, `title`, `icon`, `bar_row`, and ordered `rows`. Sequence
numbers advance within a publisher instance and can skip for slow consumers.
Each row includes its template fields, scalar or structured `value`, `unit`, formatted `text`,
`health`, `freshness`, optional `reason`, `sampled_at`, `valid_until`, and resolved
check/observer/collector identity where applicable. Unknown numbers are `null`,
never zero. Health and data freshness are separate dimensions.

Rows are projected at one evaluation time; their underlying samples can have
different timestamps. Observer verdicts in a scoped stream use an accompanying
complete snapshot from the same source. Actual sample timestamps
survive refresh and replay. Summary rows use the newest underlying check sample
when one exists and expire at the earliest relevant fresh-evidence boundary.
Source snapshots also expire after 15 seconds, independently of check TTLs.

`--interval` defaults to 5 seconds and accepts 1 second through 1 hour. It does
not reschedule checks. Source refresh is at most 5 seconds apart; evidence is
aged every second. Changed evidence is written on the display cadence; health,
freshness and lifecycle transitions can write sooner. Unchanged content skips
writes, except publisher renewal at most 10 seconds apart, with a 30-second
publisher lease. Thus a large interval does not suppress safety transitions or
publisher renewal. A first configuration creates a `not_started` placeholder;
reconfiguration never resets existing output. Startup publishes immediately;
orderly shutdown publishes `stopped` and removes current values.

**Readers must evaluate both `publisher_expires_at` and each row's `valid_until`
against their current clock.** A killed process cannot write `stopped`; its file
can still contain `running` after the lease has expired. Do not use mtime or
`generated_at` as a substitute for the original sample time, or extend evidence
validity because the publisher renewed its lease. Implausibly future timestamps
are treated as unavailable. `monitor preview` applies this reader policy.

File snapshots and stream frames share a 1 MiB serialized limit. Rich row
values have a 64 KiB individual limit and share a 512 KiB aggregate budget,
leaving room for template metadata and scalar rows. Repeated structured
projections are computed once per panel. Oversized values become unavailable
with `reason: "frame_budget_exceeded"` before retention or file serialization.
Files use compact JSON; excessive envelope/template metadata is rejected
without replacing the previous complete file.

Each output has one cooperating writer, protected by a process-lifetime lock in
a separate stable file. Locks release after process termination. Writes create
a complete temporary file beside the destination and replace the destination;
Windows sharing violations receive bounded retries without deleting the old
file. Readers should reopen by pathname and permit delete sharing on Windows.
Only local filesystems are supported for lock/replacement guarantees. Output
failures are reported and retried; the output path can become unavailable.
Always retain freshness checks even when a previous complete file remains.

The display cache does not force file/directory synchronization on each update.
Configuration, journals, checkpoints and outboxes retain durable writes. File
output runs separately from ingestion/sampling and coalesces only display
frames. `monitor.Hub` exposes latest-snapshot retrieval and bounded subscriptions
with the current frame included atomically; slow consumers skip intermediate
frames. The core can run with no file sink. Native menu-bar/tray UI, IPC, charts,
expression evaluation and history buffers remain future work.

### Additional software adapter policies

Mihomo `adapter.expected_tun` accepts an explicit boolean; missing controller
state stays unknown. `provider_match_modes` maps expected provider names to
`ordered` (the default) or `set`. Set mode ignores ordering, while duplicate,
missing and extra members still violate policy. Bounded `provider_comparison`
counts explain mismatches without exposing undeclared names for public
controllers. Credential-bearing responses retain closed comparison labels
without raw counts or controller timestamps.

WireGuard `adapter.expected_allowed_ips` optionally asserts a nonempty set of
1..128 CIDRs. `expected_keepalive_seconds` asserts 0..65535 seconds (0 is off).
These inspect public runtime fields, not OS route correctness or business
reachability. Use an application check inside the tunnel for that evidence.

Tailscale `adapter.tailscale_diagnostic` optionally accepts `disco` or `tsmp`
and requires `timeout_seconds >= 4`. Omission keeps collection passive. Enabled
checks issue one bounded ping to the uniquely selected peer's reported IP.
DISCO records direct/DERP/peer-relay response hints and may negotiate transport;
TSMP records a response without identifying the actual transport. Both retain
reported evidence and leave application reachability unverified. No application
or host health is inferred from a diagnostic timeout.

The CLI can report an internal timeout on stdout and still exit non-zero
(`ping "<ip>" timed out`). The adapter recognizes exactly that text for the
fixed target and reports `unknown` with `diagnostic_response: timed_out`; every
other non-zero exit stays a monitor-side error, so a stopped daemon, a missing
peer or a permission problem is never read as the peer not answering. Facts
carry `last_seen` only when the control plane reports it; a missing time reads
`not reported`, not the year 1 and not the sample's own time.

Cloudflare Tunnel connectors are read by `kind: "cloudflared"` from
cloudflared's local metrics listener. The target is the listener itself: a
literal loopback address (`127.0.0.0/8` or `::1`), commonly
`http://127.0.0.1:PORT` or `http://[::1]:PORT`, or `https` with `ca_file`. A
host name, including `localhost`, is refused because a hosts file or resolver
could point it elsewhere, and the URL carries no user information, query,
fragment or path other than a bare `/`. `path`, `bind_ip`, `resolve_ip` and
proxy chains are refused; this kind is not in the proxy-traversal list, so no
environment proxy applies and there is no fallback transport. No credential is
sent. Run the worker on the same host as the connector: another host's
loopback listener is unreachable by design.

Two fixed reads happen under the check's timeout. `GET /ready` is limited to
4 KiB of JSON and yields `ready_connections`, plus `connector_id` only when the
body's identifier has a UUID shape (lowercased); any other value is omitted. A
reported count above 1024 is not this listener's shape: that is a
malformed-input bound, not a deployment limit. `GET /metrics` is limited to
1 MiB of Prometheus text. Only `cloudflared_tunnel_server_locations`,
`cloudflared_tunnel_ha_connections`, `cloudflared_tunnel_total_requests`,
`cloudflared_tunnel_request_errors`, `cloudflared_tunnel_active_streams`,
`cloudflared_tunnel_timer_retries` and `build_info` are parsed; every other
line, including histogram members and `cloudflared_tunnel_response_by_code`, is
skipped as soon as its name is read. Labels are parsed with escaped quotes and
backslashes, and counters and gauges must be non-negative integers within
eighteen decimal digits; Prometheus decimal and exponent forms whose value is
integral (`1.0`, `1e+06`) are accepted, while fractional values, signed
mantissas, NaN, Inf and overflow are not. An absent family stays absent and a
non-integer is not reported as zero. `connection_colos` carries at most eight
distinct lowercase colo tokens from `server_locations` samples whose value is
1, sorted; when more are reported, the first eight sorted are kept, and the
colo policy compares every distinct reported colo. `edge_location` is
lowercased deliberately, because Cloudflare writes a colo uppercase in `CF-Ray`
and its documentation while this label is lowercase. The unready live capture
did not include `cloudflared_tunnel_server_locations`, so its labels and values
are the documented shape, not an observation. `connector_version` comes from
`build_info{version=...}` and is a token of at most 24 characters: the observed
`2026.10.0` is nine, and a bound that could not hold the real version would
hide it. `cloudflared_tunnel_timer_retries` is parsed but not reported as a
fact: the eight-fact budget is spent on the defined readings.

`/ready` decides the outcome; `/metrics` supplies facts. If `/ready` cannot be
read, is non-JSON or is oversized, the sample is a monitor `error` with code
`metrics_unavailable` and `/metrics` is not consulted. If `/ready` is a 503,
its body `status` is not 200, or `readyConnections` is 0, the sample is `fail`
with code `tunnel_not_ready`, stage `adapter`: the connector's own statement
that it has no edge session; `/metrics` is then read only as a best-effort fact
source and its failure is ignored. If `/ready` reports at least one session but
`/metrics` is unreadable, non-200 or has no allowlisted family, the sample is
`error` with code `metrics_unavailable`. That is how a stopped connector
differs from a live one with no session: a full process stop closes the
listener and is `metrics_unavailable`, while `tunnel_not_ready` is a live
connector that says it holds no session. Otherwise the sample is `ok` with the
detail `tunnel established; service behind it unproven`. A failed sample
reaches the link only after the check's failure threshold, as with any check.

`adapter.cloudflared.expected_connections` (1..8) turns ready sessions below the
declared number into `policy` with code `connections_below_expected`, never a
failure; `readyConnections: 0` is still `fail`, and one or more ready sessions
below the declaration is `policy`. `expected_colos` (at most eight
3..8-character tokens) turns a reported session outside the declared list into
`policy` with code `colo_outside_expected`. A colo the metrics did not report
is not a violation, and an entry may use either case. `topo edit` authors both:
the check form takes the expected connections (1..8) and the expected colos of
a `cloudflared` check and clears them when the kind is anything else. These
are policy assertions, not request failures. `cloudflared_tunnel_ha_connections` is not a
ready count: the live unready capture reported `ha_connections 1` with zero
ready connections, so readiness is decided only by `/ready`.

The facts are `ready_connections`, `connector_id`, `ha_connections`,
`connection_colos`, `requests_total`, `request_errors`, `active_streams` and
`connector_version`, at most eight. Evidence is `reported` and path evidence is
`local_connector_runtime_reported`; counters are cumulative since the connector
started, and a decrease or a changed `connector_id` means a restart. A fresh
local connector report decides the state of a `link` it is attached to: a
`tunnel_not_ready` sample takes the link down after the failure threshold, an
established session shows it up, and the reason says the service behind it is
unproven. The link then reports its evidence as `reported` rather than
`inferred`. A `cloudflare` API check on the same link is shown beside the
connector's and does not vote on the link while a fresh connector report
decides it. A connector read that failed leaves the link unknown with its own
reason. The public check through the edge still proves only the edge, and a
colo is the connector's metadata, not routing proof. `/ready` proves a
connector session, never that the hostname routes to this connector or that the
service answers; add a separate application check for that. The preview's
`tunnel-down` scenario shows that shape: the connector reports
`tunnel_not_ready`, its link reads down, the public hostname gets `530` from
the edge and the loopback check still proves the service.

`adapter.cloudflared` is new. Older binaries refuse a topology that contains
it, so upgrade collectors and all viewers to 1.4.0 or newer before enabling it;
default configurations omit the field and remain readable by older clients.
The nested options are part of the measurement contract, so setting or changing
them starts a new contract and history for that check.

### Cloudflare API adapter

`kind: "cloudflare"` reads one Cloudflare API resource per check with one
allowlisted `GET`, using a token from `adapter.token_env` in the worker's
environment. The public target is `https://api.cloudflare.com/client/v4`; any
target must be exactly `https://api.cloudflare.com/client/v4` (a trailing
slash and an explicit `:443` are accepted) or a literal loopback test address,
which may use `http`; another host, another path, or a port outside 1..65535 is
refused, and the base carries no userinfo, query, fragment or dot segments.
`path`, `bind_ip`, `resolve_ip`, `server_name` and `ca_file` work as they do for
the Mihomo controller read, and this kind may traverse its path's proxies
because the API is legitimately reachable through them; there is no environment
proxy, no redirect following and no direct fallback. An endpoint is refused:
the API base is `target`. `interval_seconds` must be at least 60, well inside
Cloudflare's documented limit of about 1,200 requests per five minutes per user
(the limit is shared by that user's tokens).

`adapter.cloudflare.read` selects exactly one of `tunnel`, `dns_record` or
`pool`. `account_id`, `zone_id` and `pool_id` are 32 ASCII hex characters.
`tunnel_id` accepts a UUID in `8-4-4-4-12` ASCII hex form, with uppercase or
lowercase letters; legacy 32-hex inputs remain accepted. Its spelling is
preserved in the configuration and request path, without normalization, so
existing configurations keep their measurement contracts. `record_name` is a
DNS name and `record_type` is `A`, `AAAA` or
`CNAME`. `expected_proxied` and `expected_content` belong to the DNS read,
`expected_connections` (1..64) and `expected_colos` (at most eight three-letter
uppercase codes) to the tunnel read, and `expected_origins` (at most sixteen IP
addresses or host names) to the pool read. A field belonging to another read is
refused rather than ignored.

| Read | Request | Facts | Policy |
|---|---|---|---|
| `tunnel` | `GET /accounts/{account_id}/cfd_tunnel/{tunnel_id}` | `tunnel_status` (`healthy`, `degraded`, `down` or `inactive`), `tunnel_connections`, `tunnel_pending_reconnect`, `tunnel_colos` (sorted, at most eight), `connector_versions` (sorted, at most four), `oldest_connection_at` (RFC 3339); the five connection facts are absent when the connection list is empty beside a `healthy` or `degraded` tunnel | `connections_below_expected`, `colo_outside_expected` |
| `dns_record` | `GET /zones/{zone_id}/dns_records?name={record_name}&type={record_type}` | `record_count`, `record_proxied` (`true`/`false` when every record agrees), `record_ttl` (same), `record_content_matches` (`true`, `false` or `not_declared`) | `record_missing`, `record_not_proxied`, `record_proxied_unexpectedly`, `record_content_mismatch` |
| `pool` | `GET /accounts/{account_id}/load_balancers/pools/{pool_id}/health` | `pool_healthy` (when a region reports it), `origins_healthy`, `origins_total`, `regions_reporting`, `origin.<declared>` (`healthy`/`unhealthy`) | `origins_below_expected` |

The DNS query is the only query and carries exactly one `name` and one closed
`type`. A path outside the three exact forms, a dot segment and an extra query
parameter are refused with `api_path_not_allowed` before a request exists. The
DNS record's `content` is compared with `expected_content` in memory and never
stored; the observation keeps only whether it matched, so a record that still
points at the old origin is reported as a mismatch without republishing the
address. The comparison is canonical: `A` and `AAAA` content is compared as
parsed addresses (an address that does not parse on either side is a
mismatch) and `CNAME` content without case or a trailing dot. The record id,
zone name and comment are never kept. The `result`
must be present: absent or null is a monitor error, while an explicit `[]` is
`record_missing` policy; when `result_info.total_count` is present and differs
from the page length the response is a partial page and is refused rather than
counted. More than one record for one name and type leaves `record_proxied`
and `record_ttl` absent when they disagree, with `record_count` saying how many
were returned.

The tunnel read counts the documented `result.connections` array (pending
reconnections included; the current reference marks `is_pending_reconnect`
deprecated and says it is always false) and records the API's closed `status`. `origin_ip`,
connection and client identifiers and the tunnel name never become facts.
Cloudflare documents that the connections array is deprecated in favour of the
dedicated connections endpoint and will start returning empty. An empty
connections array beside `healthy` or `degraded` is treated as not reported:
the five connection facts are absent, neither connection policy can fire, the
outcome stays `ok` and the detail reads "Cloudflare API state reported; the
connections list was not reported". Beside `down` or `inactive` it is a count
of zero and the policies apply. `tunnel_status` carries the API's own word in
every case. A `down` or `degraded` status is a reported fact and does not by
itself fail the check: it is the platform's statement, and
`expected_connections` and `expected_colos` are the declared assertions. The pool reference renders `pop_health` both as one
`{healthy, origins}` object and as a list of regions, and the generated model
collapses the region map; the adapter accepts both. `regions_reporting` counts
parsed regions, the two origin counts cover distinct origins, and an origin
reported in several regions is healthy only when every report says so. Only
declared origins appear as `origin.<declared>` keys; an undeclared origin
contributes to the counts only, and a declared one that is missing or unhealthy
is `origins_below_expected` policy.

Every read is credential-bearing, so every failure is a local monitor `error`,
never a target failure. A missing token is `cloudflare_token_missing` at stage
`setup`, with nothing sent; every other failure is stage `adapter_api`: a
connect error or timeout (`api_unavailable`), `401`/`403`
(`authentication_failed`), any other non-200 including a refused redirect
(`http_<status>`, such as `http_429`), an oversized body
(`response_too_large`) and an invalid, over-complex or unexpected body
(`unsupported_response_shape`). HTTP 200 with `success: false`
is `api_unavailable`, and `errors[].message` is never read. A passing sample is
`ok`, evidence `reported`, path evidence `cloudflare_api_reported`, with
`peer_text: withheld_secret_request`. Only closed words, numbers,
shape-validated tokens and comparisons with the topology leave the adapter;
the reflection tests echo the bearer token into every string field and check
that nothing of the token reaches the observation; in `tunnel_colos`,
`connector_versions` and `oldest_connection_at`, a value or list member that
is the token, or any run of at least eight bytes of it, is dropped even when it
has a reportable shape (a connector version, a colo). The platform's statement says nothing about the origin or the public
hostname answering, so keep a separate application check and the edge check.

`adapter.cloudflare` is new. Older binaries refuse a topology that contains it,
so upgrade collectors and all viewers to 1.4.0 or newer before enabling it;
default configurations omit the field and remain readable by older clients.
The nested options are part of the measurement contract, so setting or changing
them starts a new contract and history for that check. The fixtures under
`internal/adapters/cloudflare/testdata/api-v4/2026-10-19/` are synthetic and
document-based; no live account or token was used.

[`examples/cloudflare.json`](../examples/cloudflare.json) is the worked topology
to copy from: a proxied origin with its own direct check, a Worker and a Pages
site, a Tunnel with a `cloudflared` check on its link and a loopback check of
the service behind it, trace and DoT checks, an Access-protected hostname using
`request_header_envs`, and `expected_pops` on the edge check. The three reads
above are named in the edge's `notes` with `CF_API_TOKEN` rather than enabled,
because the example holds no token or account; the tunnel read may carry the
Tunnel's link, where it is shown beside the connector's report and never
decides the link. The checks that are active use documentation hostnames and
address ranges: of the public hosts only the trace target
(`https://www.cloudflare.com/cdn-cgi/trace`) and the `1.1.1.1` DoT resolver
are real, and the `example.com` hosts are placeholders to replace.

Worker outboxes also accept `--outbox-records` (default 20,000, range
1–1,000,000). This independently caps the pending-record index. Reaching either
this count or the pending byte budget stops execution explicitly; an existing
queue above the configured count is refused at startup without deleting data.
Use a larger explicit count to drain such a queue.
