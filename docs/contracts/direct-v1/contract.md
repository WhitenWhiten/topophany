# Topophany Direct v1 (D1)

Contract ID: `topophany-direct-v1`; fixture schema 1. D1 extends topology,
snapshot, monitor request/template/frame schema 1 without changing existing
client/preview fields. Unknown extension fields are ignored. This package is
owned by the Go repository; consumers copy and verify `manifest.json` bytes.

## Baseline and ownership

Baseline and initial HEAD: `7e7fdb11358fd8c49b07ac7e840a2cbe72826f0b`;
initial worktree clean; Go 1.26.10 darwin/arm64. Only topophany is writable.
Bar's Request/Frame/Values/TemplateCompiler/ProcessEngine/ReaderPolicy were
reviewed read-only. Its schema 1 decoder ignores extensions, uses UInt64 for
sequence and byte counters, and ages nested resource deadlines independently.
Direct requires new capability detection and a new sources.status renderer.
Old `--profiles` remains exclusively collector client profiles.

| Owner | Responsibility |
|---|---|
| app | command parsing, JSON envelope, output shape and exit codes |
| direct storage/auth | stable profile/source identities, config CAS, private credentials, controlled authentication |
| source.Runtime / probe / adapters | bounded transport, normalization and atomic source reads |
| direct.Session | per-process scheduling, admission, bounded external memory, confirmation, cancellation |
| report / engine.PrepareExternal | sample deduplication, thresholds, time-independent evidence cache |
| view | resource selection, source counts/state, origin and expiry |
| monitor / stream | bounded projection, request installation, publication and backpressure |

## Public fields and identities

| Field | Type | Missing / null semantics |
|---|---|---|
| profile | nonempty config.ID string | required for direct; stable configuration ID |
| source.id | config.ID, at most 64 bytes | required; clients generate bs- + lowercase UUID hex |
| source.name / adapter / url | strings | required. Detect support for beszel, komari and cf-server-monitor through capabilities |
| source.ca_file | string | omitted = empty |
| source.allow_insecure | boolean | omitted = false; true explicitly permits non-loopback HTTP for this source; null or a non-boolean is invalid |
| source.interval_seconds / timeout_seconds | integers | omitted = 60 / 8; zero is invalid |
| source.stale_seconds / max_age_seconds | integers | omitted = 180 / 180; zero is invalid |
| object_id | adapter-specific string | Beszel uses a 15-character lowercase ASCII alphanumeric systems.id. Komari and CF-Server-Monitor use canonical lowercase UUIDs |
| node.id | string | source.id + "." + object_id; object_id retained separately |
| profile_revision | opaque string | empty on request = latest; nonempty = exact CAS installation fence |
| dataset_id | opaque string | stable across restarts/renames/token renewals; changes on profile, Hub, subject or selected set changes |
| retry_nonce | string | empty = no retry; each distinct nonempty value once, per-source 5s debounce; Retry-After prevails |
| origin | optional object | adapter, source_id, name, object_id; selected resource's actual source |

Profile revisions, stream config revisions, token versions, request IDs and
dataset IDs are separate identities. Independent processes share only config
and credentials. Confirmation runs restart with each process. Equal inputs,
configuration and evaluation time give equal judgments; polling times may differ.

## Configuration API 1

`topo sources list --json` and `inspect|save|remove --json-stdin` support
`--direct-root ABS_PATH`. Default: os.UserConfigDir()/topophany/direct.
stdin is one UTF-8 JSON object through EOF, at most 256 KiB; stdout is exactly
one JSON object plus LF, at most 1 MiB. Diagnostics contain no upstream body or
secret. Unknown fields are ignored; known fields must have the declared type.

| Command | Request fields after schema:1, request_id:string | data on success |
|---|---|---|
| list | no input; request_id output is empty | profiles:[{profile,project,profile_revision,dataset_id,sources:[{source,auth_mode,selected_objects,nodes}]}] |
| inspect | profile, source, auth | source, systems:[{id,name}], truncated:boolean, limits:{max_selected:20}, warnings:[] |
| save | profile, base_revision, source, auth, selected_objects:[string] | profile, profile_revision, dataset_id, source_id, nodes:[{id,object_id,name}] |
| remove | profile, base_revision, source_id | profile, profile_revision, dataset_id, source_id |

Auth is an exclusive union: password={mode,identity,password}, token={mode,token},
saved={mode}, env={mode,name}, anonymous={mode}. Beszel accepts password auth.
Komari and CF-Server-Monitor accept anonymous auth for public data. Komari
token/env values are raw administrator API keys. CF-Server-Monitor token/env
values are pre-issued Worker JWTs. Password is command-local. Saved requires
an existing profile/source with the same adapter and exact URL. Env persists
the reference. Komari keys and CF-Server-Monitor JWTs are nonrenewable and
bound by a local fingerprint. Save with explicit env auth after changing the
credential value.
Save reauthenticates, discovers trusted names and upserts only one source.
Creation uses base_revision=""; modifications require the current revision.
A removed final source leaves an empty profile. List never contacts a Hub and
returns an empty array only for absent storage, never corrupt/unreadable storage.

Success: `{schema:1,request_id,ok:true,data:{...}}`.
Failure: `{schema:1,request_id,ok:false,error:{code,message,retryable}}`.
Exit 0 success, 1 business/network/storage failure, 2 command/JSON/shape failure.
Stable codes: invalid_request, profile_not_found, source_not_found,
profile_conflict, profile_unavailable, auth_required, forbidden, mfa_required,
auth_method_unsupported, connection_failed, tls_error, rate_limited,
upstream_incompatible, selection_invalid, selection_limit, discovery_limit,
storage_error, credential_storage_error, state_budget,
history_unsupported_in_direct_mode. Unknown codes require generic presentation.

## Stream and status

Capabilities retain protocol:1 and add sources=[client,preview,direct] and
`direct:{schema:1,config_api:1,adapters:[beszel,komari,cf-server-monitor],allow_insecure:true,retry:true,limits:{sources_per_profile:64,objects_per_source:20,objects_per_profile:256,template_rows:128}}`.
Discovery does no I/O to config, credentials or network. Metrics advertise exact
supported sources. Direct supports `sources.status`, `servers.list`,
`server.card` and the node-scoped `resource.*` metrics. Server cards may
include `network`, `swap` and `load` in their resources. List entries may
include `resource_metrics` with those readings and their own sample time and
deadline. Absent readings omit their optional fields.

Direct requests require profile; credentials/scenario/lang cannot carry meaningful
client/preview values. Each request reloads public config and optionally checks
profile_revision. Errors are request-correlated and retain the previous request.
A valid request immediately emits running + loading, then collects asynchronously.
No lifecycle=loading. Network failures remain source-scoped running data.
Running direct frames add profile, profile_revision, dataset_id. Projection-only
changes reuse a session; Close of the old projection cannot close a shared session.
Fetch/Prepare never wait for an eight-second Hub collection. One installed session
and one bounded candidate maximum; invalid candidates leave the old one alive.

`sources.status` is object/status with no selector:

| Field | Type / semantics |
|---|---|
| state | loading, ready, partial, no_data, unavailable |
| reason | optional refinement; `samples_stale` means all sources connected, no current samples, and at least one expired sample; omitted otherwise |
| counts | sources, selected, current, stale, missing, attention integers |
| sources | array of id,name,adapter,state,code,last_attempt_at,last_contact_at,contact_valid_until,next_retry_at,counts |
| source.state | loading,connected,retrying,auth_required,forbidden,error |
| code | stable string or null |
| all source timestamps | RFC3339 UTC string or null; never empty/Unix-zero substitutes |
| per-source counts | selected,current,stale,missing; (source_id,object_id) deduplicated |

State precedence: selected=0 => no_data; all sources unattempted => loading;
all connected and current=selected => ready; current>0 => partial; all connected
and missing=selected => no_data; otherwise unavailable. Empty sources never ready.
current/stale/missing partition selected; attention=stale+missing. Stale means a
previous usable sample has expired. Missing means none has been usable. Source
errors never imply a machine is down. Ready health=up, partial=degraded, others
unknown. These describe data acquisition, not network availability.
`reason` is additive: older readers retain the same state and counts. A
`samples_stale` reason does not change sample deadlines or imply failed contact.

## Four clocks and expiry

| Clock | Owner | May advance when |
|---|---|---|
| sample / data deadline | Beszel record.created, Komari updated_at or CF-Server-Monitor last_updated / report.MergeBinding | a distinct accepted report. Deadline = sample + max_age |
| receive / contact deadline | Session admission / report.MergeSource | real receipt / successful source read; deadline = contact + stale_seconds |
| publication | monitor.Build | projecting at now; never extends source evidence |
| handoff write | Bar | writing its snapshot; never substitutes any evidence clock |

Reading the same record advances contact only. Status row valid_until is bounded
by the earliest relevant future data/contact deadline and a finite projection
lease. Loading/no_data without evidence also have bounded leases. Resource.origin,
card/list origin retain identity after expiry. Beszel is an adapter, never provider.
Widget snapshots must label their as-of time and expire at the earliest relevant
row/resource deadline, without the old 30m heartbeat + 15m grace extension.

## Safety, budgets and compatibility

| Budget | Hard maximum / default |
|---|---|
| sources / selected per source / per profile | 64 / 20 / 256; no truncation |
| template rows / request / frame | 128 / 256 KiB / 1 MiB |
| discovery | 1000 systems, 10 pages, 100/page, 30s overall; bounded partial set sets truncated |
| collection | 60s interval, 8s source deadline; display 5s, independent |
| data / contact windows | 180s / 180s |
| page generation | explicitly count unique object/report pairs, at most 20 |
| report / source normalized results | existing report 64 KiB / result 16 MiB limits |
| memory | 32 MiB accepted state, 4 MiB/source with multiple sources, 2 MiB batch; 4 concurrent source reads; 256 Seen IDs + 8 retired epochs/binding |
| retry nonce history | 1024 distinct nonempty values/session; further new values are rejected with state_budget, old values remain idempotent |
| history | unsupported in direct; no metrics journal/database/listening socket/daemon |

Generated config uses a stable local executor observer without a remote node,
resource bindings only, primary_disk=root for Beszel or aggregate for Komari
and CF-Server-Monitor,
no active probes or default alarms.
Non-loopback HTTP requires source.allow_insecure=true on inspect/save. This
per-source consent is persisted and returned by inspect/list, and covers
authentication, discovery and resource collection. It applies to IPs (including
LAN and Tailscale addresses) and hostnames. HTTPS certificate verification is
unchanged. Loopback HTTP remains allowed without the option. Consumers should
check direct.allow_insecure in capabilities before offering this opt-in. The
runtime permission is excluded from topology JSON; collector sources retain
their existing transport policy.

Root means adapter primary filesystem, not mount point /. Existing normalized
status handling remains valid. Private directories 0700, files 0600; public
configuration contains references only. Credential renewals use locking/version
fences and atomic replacement without changing profile_revision. Tokens never
enter snapshots, argv, logs or App Group. Auth POSTs are restricted to explicitly
owned PocketBase endpoints; adapter collection remains GET-only; no redirects,
ambient proxy or cross-origin access. Transient failures use an independent
5,10,20,40,60s capped/jittered policy; permanent auth failures do not spin and
Retry-After is honored. Existing collector/worker policies remain unchanged.

Direct is an explicit exception to old client/preview-only read documentation
and the stream no-write rule: it may lock/read/atomically renew credentials under
its private root. File monitor output is still owned only by file monitor mode.
Current-state CLI/TUI/status/show/monitor use the same Session and view projection.
Once retains observation semantics. History always refuses direct explicitly.
One-shot reads wait at most 15s by default (--wait overrides), then close.
Status exits: 0 ready, 2 current resources need attention, 3 no current resources,
4 all selected sources failed contact; config/argument errors 1.

## Reproducible evidence

Synthetic fixture clock: 2026-10-21T12:00:00Z. No real accounts, servers or tokens.
Supported resource schema: Beszel v0.21.0 and the four-integer mem schema described
in internal/adapters/beszel/README.md; synthetic authentication tests exercise
PocketBase users auth-with-password/auth-refresh and authenticated user record
lookup. No claim of arbitrary Beszel version compatibility.
Komari supports the pinned 1.6.0 schema. Authenticated discovery reads only
GET /api/admin/client/list (administrator-protected raw array); anonymous
discovery reads GET /api/nodes (success envelope). Only canonical UUIDs and
sanitized names leave discovery. Listings over 1000 nodes or 1 MiB fail explicitly.
There is no authenticated-to-anonymous fallback. API keys are never interpreted
as PocketBase JWTs or refreshed. Current resource reads use GET /api/recent/:uuid.
The recent window is about one minute; an explicit 30-second interval is useful.
Online presence and per-filesystem disks are outside this mapping.

The canonical manifest lists fixture paths and lowercase SHA-256 hashes. Production
serialization tests check fixture bytes. HANDOFF.md records actual completed
verification and remaining limitations. scripts/direct-contract.py owns a literal
loopback synthetic Hub and uses isolated temporary roots; no real Hub login.
