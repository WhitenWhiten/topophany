# Cloudflare real account acceptance 2026 11 02

**Original run: partial pass.** A real registered Tunnel, DNS, Access, Worker and
Pages deployment were exercised from macOS and the Linux `s2` server. The
core reads and fault/recovery behavior passed. Two colo-policy assertions
failed because both expose the same API colo-name compatibility gap. No
adapter fix was applied during this acceptance run.

**Follow-up: CF LIVE 001 resolved.** The normalization fix passed a separate
real-account recheck of matching and outside policies on both Tunnel reads
(four assertions). The [post-fix receipt](evidence/cloudflare-live-2026-11-02/colo-fix-recheck.json)
keeps that result separate from the original failure and source revision.

Source: clean commit `70c92b67c61d6e7956e6826e067eb4052d477a85`.
The macOS arm64 and Linux amd64 binaries were built with `-ldflags='-s -w'`;
their SHA-256 hashes are in the [machine-readable receipt](evidence/cloudflare-live-2026-11-02/report.json).
Provisioning began at **07:42:39 UTC**, final connected captures were taken at
**08:06:32 UTC**, and cleanup completed at **08:07:55 UTC** on 2026-11-02.
This is a bounded run with injected failures, not a continuous uptime benchmark.

## Environment and method

- macOS ran an isolated JSON application, cloudflared **2026.10.5**, collector
  and local worker. The application returned a unique JSON marker and
  `Cache-Control: no-store`; both application checks asserted its content.
- `s2` ran the Linux binary from the same source in a private temporary
  directory. Its existing **Topophany 1.5.1** binary and service installation
  were retained. The original binary hash was checked before and after.
- The two hosts reported different trace exit addresses; the receipt keeps
  hashes of those addresses. A local system path may traverse an existing
  proxy/TUN. This run does not establish direct physical routing.
- Worker delivery used a dedicated SSH reverse forward to the collector's
  literal loopback listener. The collector was not exposed publicly.
- Probe credentials were a four-hour account token with **Cloudflare Tunnel
  Read** for the account and **DNS Read** for the test zone. A second
  Tunnel-only token tested scope enforcement and revocation. Existing CLI
  OAuth authentication provisioned and removed the temporary resources.
- Ordinary checks ran every 10 seconds with failure/recovery thresholds of
  two. API checks ran every 60 seconds. The deployment established **two**
  ready sessions; connection policies declared two after observing the live
  deployment. Four was not assumed to be a platform requirement.

There were **35 online check definitions**. The receipt contains 71 passing
and two failing assertions, including setup, repeated scenarios and cleanup;
these are not 73 independent features. Expected failures, monitor errors and
policy violations pass acceptance only when their actual classification
matches the scenario.

## Observed results

| Scenario | Result |
|---|---|
| Public application from both hosts and origin loopback | Unique JSON body validated; public responses reported Cloudflare LAX. |
| Registered connector | `/ready` returned 200 with two sessions; metrics and API session counts agreed when settled. HTTP/2 and QUIC both carried real application requests. |
| API tunnel and dedicated connections | Status and nested `conns` counts read successfully. History contains `degraded`, `down` and `healthy`. Colo extraction has the gap below. |
| DNS API | One CNAME and two A records returned the expected cardinality, proxy status, TTL and content comparison. Wrong proxy/content and absent-record assertions produced their policy codes. |
| Token setup and permissions | Missing token → `cloudflare_token_missing`; invalid token and DNS with the Tunnel-only token → `authentication_failed`; the same scoped token could read the Tunnel. Revocation subsequently denied that read. |
| Access | Valid service token satisfied the application JSON assertions. Invalid and anonymous requests were denied; a missing header environment variable produced `http_secret_missing`. The observed anonymous denial was HTTP 403. |
| Workers and Pages | Separate real deployments returned the unique JSON marker to `s2`. |
| Managed challenge | A temporary rule restricted to the exact test hostname and `/challenge` produced HTTP 403 with `edge_mitigation: challenge`. |
| Trace and encrypted DNS | Both hosts supplied trace facts and successful DoT and DoH answers. A WARP expectation differing from the observed `off` value produced `warp_mismatch`. |
| Policy classification | Connector redundancy/colo, API redundancy, HTTP colo and DNS mismatches produced policy outcomes. |
| Application stop and recovery | Local and public application checks became down while the connector remained up. A **new API sample during the application failure** still reported healthy. Both observers confirmed application recovery. |
| Connector process stopped | Closed metrics listener produced monitor error `metrics_unavailable`; the origin loopback remained up. |
| Live connector with no edge sessions | An isolated connector bound outbound edge connections to loopback while retaining its metrics listener. It reported zero ready sessions, `tunnel_not_ready`, and the Tunnel link became down. Public responses included Cloudflare 530. |
| Connector restart | Link and application evidence recovered; connector identity changed and cumulative requests reset from 77 to 16 in the recorded pair. |
| Control-plane precedence | A fresh healthy local connector kept the link up despite failed/unknown API policy checks beside it. Deliberately failing connector-policy checks were detached from the link for this assertion. |
| Worker silence | Local evidence aged to stale/unknown while the `s2` public check continued to pass. Fresh samples restored it after restart. |
| Delivery interruption | Removing only the dedicated SSH forward expired `s2` evidence. Reconnection restored it and delivered delayed samples with original timestamps and unique IDs; maximum delay was **35.632 seconds**. |
| Consumer and credential handling | CLI check/server/service/map/incident/observer JSON views and five monitor stream card/status rows rendered. A scan of 59 snapshot/history/view/stream exports found none of the actual API, Access, Tunnel or Topophany credential values. Secret-bearing Access samples withheld status and colo labels. |

The targeted Go suites for `internal/adapters/cloudflare`,
`internal/adapters/cloudflared`, `internal/probe`, `internal/engine`,
`internal/api`, `internal/config`, `internal/view` and `internal/tui` all passed.
These local suites are separate from the live results above.

## Finding CF LIVE 001

The real Tunnel APIs returned `colo_name` values such as **`lax12` and
`lax13`**, also seen in the local connector's `edge_location` labels. The
shared API connection collector calls `model.IsColo` at
[`internal/adapters/cloudflare/tunnel.go`](../../internal/adapters/cloudflare/tunnel.go#L99),
which accepts only three uppercase letters. Both `tunnel` and
`tunnel_connections` therefore omit `tunnel_colos` for these real responses.

The API configuration accepts only three-letter uppercase `expected_colos`.
Consequently, both the intended matching `LAX` assertion and outside `NRT`
assertion yielded **`unknown / connection_colo_unreported`**, rather than
`ok` and `policy / colo_outside_expected`, respectively. This is one
compatibility finding exposed by two failing assertions. Session counts and
status reads still worked; HTTP `CF-Ray` colo policies and the local
cloudflared token policies worked independently.

The sanitized [Tunnel response](evidence/cloudflare-live-2026-11-02/responses/tunnel.json)
and [dedicated connections response](evidence/cloudflare-live-2026-11-02/responses/connections.json)
preserve these actual names. The follow-up below closes this specific finding;
the original machine-readable receipt retains its two failed assertions.

## Post fix verification

Both API reads now normalize a location consisting of three ASCII letters and
an optional numeric suffix, at most eight bytes total, to the uppercase city
code. `lax08` and `lax13` therefore report `LAX`, while their two sessions stay
separate. Invalid suffixes and missing locations still leave the policy
unverified. Only three letters enter the fact, preserving the existing output
and credential-redaction bounds. Local connector location policies retain
their full-token semantics.

The captured responses were first replayed as failing regression tests, then
passed with the fix. Tests also cover malformed/oversized/non-ASCII locations,
partial evidence, outside-policy precedence, unchanged session counts and a
token matching the normalized fact. `go test ./...` passed; bounded
`FuzzParseTunnel` testing completed 151,457 executions without failure. Both
macOS arm64 and Linux amd64 binaries built successfully.

A fresh temporary registered Tunnel on cloudflared 2026.10.5 reported
`lax09` and `lax13`. The actual collector running the fixed binary reported
`tunnel_colos: "LAX"` and two sessions on both reads. `expected_colos: ["LAX"]`
returned `ok`; `expected_colos: ["NRT"]` returned
`policy / colo_outside_expected` on both reads. The
[four-assertion receipt](evidence/cloudflare-live-2026-11-02/colo-fix-recheck.json)
records source/binary hashes, sample times, facts and cleanup. The temporary
Tunnel and its read-only token were removed, and test processes stopped.

This focused recheck closes the colo compatibility issue; it does not rerun
or expand the original acceptance's other scopes.

## Evidence and remaining scope

- [Receipt and assertion outcomes](evidence/cloudflare-live-2026-11-02/report.json)
- [Sanitized stage observations](evidence/cloudflare-live-2026-11-02/stages.json)
- [Capture provenance and response hashes](evidence/cloudflare-live-2026-11-02/provenance.json)
- [Connected cloudflared readiness](evidence/cloudflare-live-2026-11-02/responses/cloudflared-ready.json)
  and [selected metrics](evidence/cloudflare-live-2026-11-02/responses/cloudflared-metrics.prom)

Real-account coverage remains open for Load Balancer pool health (no existing
pool and no paid feature enabled), upstream 429/5xx/cooldown behavior, large
inventory pagination/truncation, WARP/Gateway `on`, optional `cfL4` metrics,
cache HIT/MISS cases, multi-connector host failover and other cloudflared
versions. Native menu bar and browser viewer UI were not exercised; CLI views
and the live monitor protocol were. Those limits do not become passed merely
because local synthetic tests exist.

All **13 temporary Cloudflare resources** were deleted or revoked, including
the Tunnel, four DNS records, Access application/policy/service token, Worker,
Pages project, challenge ruleset and two account tokens. Test processes and
the `s2` temporary directory were removed. The three original Tunnels still
reported healthy after cleanup. Committed evidence removes account/zone IDs,
addresses, actual hostnames and credentials; documented replacements and
preserved fields are listed in the provenance file.
