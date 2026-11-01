# Deployment, High Availability, and Operations

## Principle of Least Privilege

Run the loopback demonstration in `README.md` first. For production installations, place configurations in dedicated directories, create an isolated unprivileged system user, and install the compiled `topo` binary. The systemd unit templates in `deploy/topophany-collector.service` and `deploy/topophany-worker.service` are provided for administrator adaptation and are never installed automatically during tests. Administrators must supply TLS certificates, keys, hashed `auth.json` files, an optional `alerts.json` (add `--alerts /etc/topophany/alerts.json` to the collector's command line), and worker credentials with strict directory ownership matching the service user.

Always run `topo validate --config config/topology.json` before deployment to verify sources, endpoints, paths, and response assertions. Example templates start fully paused to prevent inadvertent immediate probing upon file placement. Observers must never be reused across different execution contexts: native host, virtual containers, desktop TUN interfaces, cloud relays, and transit gateways represent distinct observation vantage points. Never run identical checks under the same observer ID across two concurrent processes.

Install auxiliary utilities (such as Tailscale, WireGuard, or OpenSSH) on the host only when those specific check adapters are required. Standard HTTP, TCP, TLS, DNS, and UDP checks do not require external binaries. Grant raw ICMP socket capabilities to workers only when ICMP is required and acceptable under local policy; never elevate privileges for collector or TUI viewer processes for this purpose.

Collectors default to loopback HTTP; configure certificate and key flags for direct HTTPS on a network listener. If fronted by an existing reverse proxy, bind the collector backend strictly to loopback, terminate TLS at the reverse proxy, forward the `Authorization` header, disable request body buffering and verbose access logging, and enforce a 1 MiB upload limit. Note that a shared reverse proxy remains a single point of failure; configuring multiple server endpoints in the client credential profile provides cleaner independent access.

## Dual Collector Deployment

Deploy each collector instance with its own data directory and unique instance identifier, sharing the same project ID and reviewed topology. Worker credential profiles list both collector endpoints; each collector recognizes that observer's token. Tokens may differ per instance and are configured on a per-server basis. Client viewer credentials can also list both collector endpoints for read-only failover.

The worker persists observations locally before attempting dual dispatch. Each collector is delivered to from a loop of its own, with its own retry backoff, so an outage, a connection timeout or a slow answer from one does not delay or block delivery to the other. Delivery acknowledgments (ACKs) are recorded independently, allowing pending items for a single collector to resume across worker restarts. This mechanism provides at-least-once delivery with idempotent deduplication; network exactly-once delivery is not claimed. Synchronous dual-write confirmation is not required, so network partitions may cause temporary dashboard divergence; inspect instance IDs, observation origins, and timestamps.

When all configured collectors are unreachable, observations remain queued in the bounded outbox with their original timestamps preserved. A record is queued once it and its directory are synced, and an outbox made afresh syncs the directories it makes into their parents before the worker opens it, so what it queued survives a power loss from the first sample. The pending byte and record budgets are divided equally among configured recipients, charging each pending recipient one record and the full serialized record size even when the physical record is shared. In multi-collector mode a full recipient enters degraded delivery: its new samples are dropped with an explicit stderr report while other recipients continue receiving durable samples. Previously queued evidence remains intact. A worker stops with an explicit capacity error when no recipient has room for a new sample, whether it has one collector or several, rather than probe on and drop every sample. Drain or archive pre-upgrade outboxes before enabling these reservations; legacy backlogs may already exceed a recipient share. Upon reconnection, backlogged records within the retention window are retransmitted: `ReceivedAt` reflects ingestion time while `SampledAt` retains the original sample time, preventing delayed evidence from being masked as recent passes. The ingestion window defaults to 24 hours (configurable between 1 minute and 30 days). Records older than the ingestion window, mismatched authorization tokens, or samples whose check now has a different measurement contract are rejected to quarantine to prevent infinite retries; quarantine evidence has the bounded retention described below. Historical raw records have equal per-observer shares of `max-history` (rounded down) and observer byte reservations, so a busy observer cannot evict another observer’s history.

Persisted heartbeats carry the enclosing batch’s project and revision; a heartbeat from another project or a legacy unscoped one never establishes liveness, while one from a worker running another revision of the topology does, since whether its samples count is decided sample by sample. Observer heartbeats demonstrate worker liveness but do not extend or refresh stale check results. If a branch/residential worker disconnects, even if both collectors remain online, they cannot synthesize or take over that specific vantage point.

## Storage and Disaster Recovery

The collector maintains state using `journal.jsonl`, `checkpoint.json`, `incidents.json` (the incident ledger, below), `alert-queue.json` (alerts waiting for webhooks, with `--alerts`) and `writer.lock`. Incoming batches are acknowledged only after SHA-256 record checksums are written and fsynced. A data directory made afresh, the directories made above it and the journal created in it are synced into their parents before the collector opens, so the first samples it acknowledges survive a power loss too. Checkpoints are written via atomic file replacement, followed by journal rotation. Crash recovery replays only incomplete records at the journal tail; corrupt lines halt recovery immediately and are not silently skipped. Every accepted sample is also added to its check's long-term rollups (below), which are written with each checkpoint and rebuilt from the journal after it, so neither a crash nor a checkpoint the journal outlived counts a sample twice. A check's state is judged from its latest 40 samples and what the samples before them left: how many passed or failed in a row and whether the check stood confirmed down, kept for every pair of thresholds it can have and written with each checkpoint. A failure stays confirmed until the check recovers, however long ago and however far out of the history the samples that confirmed it went, and an edited threshold applies as though they were all still held. Operators should back up the data directory and inspect disk health rather than deleting damaged records to force a green status.

Storage enforces limits on both record count and a conservative estimate of serialized state bytes (default 128 MiB), divided equally among observers. A receipt lasts the ingestion window and an hour, so at a steady rate an observer holds what it delivers in that long, and with the defaults its share of bytes (512 bytes a receipt) runs out before its share of receipts. The collector logs what it holds at start, each observer's receipts and bytes against its share and where its receipts settle at the last hour's rate, and warns once when an observer is set to fill its share, saying when; sample less often or shorten `--retention` before then. When state budgets are reached, new observation transactions return capacity errors before journal write; existing checkpoints remain readable. Workers back off sample delivery but send standalone heartbeats while storage is full, so storage backpressure does not itself mark an executor silent; these heartbeats never acknowledge queued samples. Process resident memory (RSS) will exceed state size due to runtime object overhead, so 128 MiB is not an RSS ceiling. `serve` and `worker` also accept `--memory-limit` (a Go soft target) and `--memory-max` (a Linux cgroup v2 hard cap, failing startup when unavailable). See [Memory budgets](MEMORY.md) for small-machine service defaults, recovery limits and leak diagnostics. Checkpoints enforce a 256 MiB maximum read limit, and single journal lines are capped at 2 MiB during read.

Raw history lasts as long as `--retention` and `--max-history` allow; **rollups** last much longer. For each check under its contract, a collector sums what its samples said. Each sample's outcome stands from when it was taken until the next sample or until it goes stale (`stale_seconds`), whichever comes first, as freshness does in a snapshot: up while it passed, degraded while it found a policy violated, down while it failed, and inconclusive while the monitor could not tell (an `error` or `unknown` outcome). Time no current sample stood for, such as a silent worker, a paused check, a check in maintenance or the time before the rollups begin, is unmeasured and never counted as up or down; availability is the share of the measured time that was up, given with the share of the window that was measured. Late samples correct the time intervals around them using sample boundaries retained for the ingestion window (plus clock-skew allowance), including across restarts. These boundaries share the rollup byte budget. Each late sample may shift at most 1,024 retained boundaries; larger corrections are left unreconciled, bounding insertion work during ingestion and journal recovery independently of the byte cap. Older checkpoints lack those boundaries, and budget pressure may retire them; a late sample that cannot be reconciled still counts, but the affected rollup carries `unreconciled_samples` and readers label its time totals incomplete instead of presenting an uptime percentage. Sums are kept in buckets five minutes wide for 48 hours, an hour wide for 32 days and a day wide after that, aligned to UTC, with the samples counted by outcome and a histogram of what the passing ones took, so percentiles of any span come from its buckets. `--rollup-bytes` caps their encoded size (default 32 MiB, from 64 KiB to 64 MiB). Retained sample boundaries add up to 64 bytes per sample to the budget, about 90 KiB per check at a one-minute interval and the default one-day ingestion window. Longer ingestion windows or faster sampling use more of the same budget, leaving less for historical buckets. At the cap a collector drops the rollups of contracts it no longer counts first, those least recently added to first, then the oldest buckets of the observer whose rollups take the most room: an observer loses history only while no other holds more, so a busy or compromised worker shortens its own history rather than another observer's, and of observers with as many checks each keeps as far back. When even active series metadata will not fit, the oldest series of the observer holding the most is evicted too. The history kept grows shorter, and ingestion never stops for it. A check whose contract changes starts a new series, as its raw history does. Snapshots carry each check's last 24 hours, 7 days and 30 days; `/v1/rollups` and `topo history CHECK --step` lay a check's rollups out in slots of five minutes, an hour or a day. Collectors keep their rollups independently.

When an aggregated worker dispatch is permanently rejected by a collector, the worker isolates entries and retries each original outbox item individually. This prevents a single expired or malformed record from forcing adjacent valid records into quarantine. A refused item is quarantined at the next tick without a backoff, since the collector answered and the queue moved on, and batching resumes with the first item accepted; only unreachable or failing collectors cause backoff. A reload that leaves samples of an old contract queued therefore costs one tick per such sample.

To back up an instance: stop the process, copy the local data directory alongside the associated topology and auth configuration, then restart the original process. In dual-collector deployments, backups can be performed via rolling restarts. Never copy partial write-ahead logs or checkpoints from a running instance without stopping, and never open a single data directory with multiple concurrent writer processes. Restorations must use matching software schemas; verify history and freshness in an isolated test environment before returning to service.

Worker state consists of `pending/`, `quarantine/`, and `writer.lock`. Quarantined items use a separate rotating forensic budget: globally the larger of the pending budget and 16 MiB, and per recipient the larger of one eighth of the pending budget and 2 MiB. There are also limits of 1024 records per recipient and 8192 records globally to bound directory work. Oldest quarantine records are deleted first, including on restart; pending observations are never deleted by this retention policy. Reserve disk space for both budgets plus an atomic-write record. If quarantined items accumulate, stop the worker, archive the directory, examine whether errors stem from mismatched measurement contracts, clock drift, or authorization failures, and resolve manually. **Never modify `SampledAt` timestamps to make historical records appear fresh.** Changing collector endpoints modifies target recipient IDs while retaining existing queued deliveries; flush or archive queues prior to endpoint migrations, as unacknowledged records are not remapped automatically.

Samples are bound to their check's measurement contract, not to the revision of the whole file. Renaming an entity, editing a note or moving a threshold changes the revision but no contract, so workers still running the previous file keep delivering countable samples; only checks whose contracts changed are refused until those workers run the new file. To change what checks measure: distribute the new reviewed configuration across all collectors and workers and restart them, comparing the contracts that `topo validate` prints. To upgrade from 0.3: upgrade collectors first (they accept the schema 1 batches of 0.3 workers while the topology is unchanged), then workers, then viewers. Read-only TUI clients require no adjustments. Collectors and workers reload on `SIGHUP` (the shipped systemd units map `systemctl reload` to it): the collector rereads its topology, `auth.json` and its alerts file, the worker its topology; a refused reload is logged with the revision still running. Worker credentials, TLS certificates and command-line flags still need a restart. Distributing the file to every host, and schema migrations, are left to the operator.

Six topology fields are new in 1.4.0 and older binaries refuse a file that contains them: `http.expected_trace`, `http.expected_pops`, `http.request_header_envs`, `dns.transport`, `adapter.cloudflared` and `adapter.cloudflare` (the last two come with the check kinds `cloudflared` and `cloudflare`, which older binaries refuse as well). Upgrade every collector and every viewer to 1.4.0 or newer before adding any of them, and every worker before it is given the new file; a worker still running the previous file is refused only for the checks whose contract changed. The rollup keys `fb`, `p` and `c` need no upgrade order: an older binary ignores them.

## Resource Limits and Monitoring

Start with a default check interval of 60 seconds; critical paths can be configured to 30 seconds. Do not probe entire external proxy provider lists by default, and do not use heavy bulk file downloads as health checks. Timeouts, concurrency, response bodies, history retention, deduplication receipts, and outbox sizes are strictly budgeted; resource exhaustion errors are reported independently from network reachability failures.

Active incidents displayed in the dashboard represent current aggregations derived from the latest snapshot; historical raw samples can be queried via `/v1/history`. Each collector also keeps an incident ledger, `incidents.json` in its data directory: every two seconds, whether or not anyone is looking, it judges the evidence of all observers as a snapshot of them would, records an incident when it opens, marks it recovering while its failed checks pass again short of their recovery threshold (a failure then is a relapse of the same incident), and closes it as `recovered`, `unconfirmed` (its evidence lapsed or no longer confirms it), `paused` or `changed` (what its checks measure changed, or they were removed). Each change is logged, such as `incident opened: home/path/direct (3f2a9c1b04de/home/path/direct); checks: home-http`. For as long as its stalest check takes to lapse (10 seconds to 15 minutes) after it starts, a collector keeps open an incident whose evidence has lapsed, so its workers can deliver what they queued. The ledger holds at most 1,000 records and drops closed ones after 90 days; it is written atomically on every change, and one the collector cannot read stops it from starting rather than being replaced, so move it aside to start a new one. Collectors keep their ledgers independently. With `--alerts`, a collector posts incidents that open and close, and observers that fall silent or report again, to webhooks in JSON or in the formats of ntfy, Slack and Discord; the [reference](REFERENCE.md#alerts) describes the file and the delivery. Topophany keeps long-term availability as rollups (above), not as a time-series metrics database, and it does not send email or SMS alerts. Worker and storage errors are logged to standard error for collection by systemd journal or host operations tooling; background collection continues uninterrupted when the TUI is closed.

## Proxy and Transport Verification

Establish dedicated loopback listeners for candidate routes prior to defining paths. Never alter production proxy routing groups or toggle active TUN interfaces for monitoring purposes. Verifying Mihomo or external proxy adapters requires two distinct checks: one inspecting the reported selection state via adapter APIs, and an end-to-end network request traversing the candidate path. Errors are isolated to the layer evaluated; passing a front CDN HTTP probe must not be interpreted as end-to-end transport authentication success, nor can identical egress IPs prove that identical ingress hops were taken.

Configuring expected mode rules catches unauthorized global fallbacks; validating provider member counts and ordering detects stale unloaded subscription pools, though it cannot verify internal cryptographic parameters for nodes sharing identical names. If cryptographic configuration verification is needed, rely on external host configuration management tools; Topophany does not request or export private subscription credentials.

## Resource isolation and summary limits

Collector observation capacity is divided equally among configured observers, independent of how many tokens or boot IDs each observer uses. Receipt and retained-byte accounting survives restart; an exhausted observer receives retryable HTTP 429. Every observer has a reserved heartbeat allocation. Deduplication receipts use collector-owned `ReceivedAt` and bind an observation ID to its content for as long as a sample with that ID could still be accepted: the ingestion window (`--retention`) and an hour. Then the receipt goes and releases its capacity. For each worker boot whose receipts have expired, the collector keeps the highest sequence among them as the boot's floor, so an ID at or below it stays bound and is refused (`409 observation_id_conflict`, which the worker quarantines) rather than accepted again, across restarts; each floor costs its observer 128 bytes of quota. A worker picks its boot IDs, so an observer's floors may take an eighth of its share and no more: past that, the floors whose receipts were taken longest ago go, so a worker that changes its boot without end cannot fill its observer's share with floors for good. Each observer's floors are kept oldest first, so a write counts them, and sheds the oldest, without visiting each floor the collector holds: a share full of floors slows no observer's ingestion. A boot still delivering keeps raising its floor and keeps it, and the IDs a shed floor bound were received more than the window and an hour ago, too old for their samples to be accepted again. An observer at its receipt quota receives retryable HTTP 429 until older receipts expire, so a quota only bounds what one observer may deliver within the window. History expiry alone releases no receipt. A store paused at its quota by an earlier release, which kept receipts until administrative archival, resumes ingestion once its old receipts expire after the upgrade.

A listener cap of 128 connections applies before TLS and HTTP parsing. Pending connections are limited to 16 per socket IPv4 address or IPv6 /64, ignoring forwarded headers. When a pending source quota or the global cap is full, a new connection replaces the oldest pending socket in that source or globally, respectively. Pending sockets have a hard five-second lifetime covering TLS and headers. After bearer authentication, a socket leaves the source quota and is protected from pending-socket eviction. At most eight sockets are charged to each viewer token or worker observer, and at most 96 authenticated sockets are retained globally, leaving 32 slots for authentication. Authenticated sockets have an absolute two-minute lifetime from acceptance. The HTTP server installs `api.ConnectionContext` to associate requests and HTTP/2 streams with their limited sockets. A request's bearer token is checked before HTTP request budgets are charged. One that authenticates counts against its principal alone: 30 requests per second for each viewer token and for each observer's workers, whatever it asks for. Everything else, health checks, unknown routes and authentication failures among it, shares an anonymous admission of 16 simultaneous requests and 300 requests per second; excess HTTP work is rejected without consuming authenticated request budgets. A full pending source bucket allows new clients to attempt authentication; already authenticated sockets remain available under an anonymous connection flood. Continuous transport floods can still interrupt sockets before they authenticate and require upstream network admission controls. Header and idle deadlines release stalled connection slots. Each observer has at most two ingestion requests in flight (including across different tokens). Viewer requests have separate slots, health checks do not consume ingestion slots, and ingestion bodies have a three-second deadline. Snapshot state is captured at most once per second globally; up to eight observer views share that state and cache at most 32 MiB of output. Each viewer token can trigger at most two distinct cache misses per second, with eight builds globally; cache hits remain available. Cached entries may be evicted to make room within the byte budget. Further distinct views within the same interval receive retryable HTTP 429. Snapshot reads copy evaluation fields from retained history and per-check tails, then detach only the latest diagnostics outside the writer lock; they never serialize the full stored history or scan receipts.

Topology is limited to 4096 total entities, a 1 MiB serialized representation, 4096 bytes per string, 8192 graph references and 32 levels of dependency depth. Exit-IP JSON is limited to 16 keys per object, 16 entries per array and 32 containers. Mihomo JSON permits 128 keys per object (including proxy/provider maps), 256 entries per array (including members/history), and 4096 containers. Duplicate keys are rejected before typed decoding. Remote snapshots also undergo bounded structural parsing and reference validation before rendering. All three map modes (network, virtual and geographic) admit at most 256 nodes, 512 edges and 32,768 cells within a 512-by-256-cell viewport. Geographic label placement shares a two-million-operation budget across summed-area table construction, candidate positions, leader checks and all alternate orders. An oversized map or exhausted layout shows a complexity notice; the complete topology remains available in lists and details. Snapshot output is limited to 8 MiB; `details_omitted` identifies summary responses. Diagnostic text is shortened, fact maps have a summary budget, and last-observation details may be omitted when the project budget requires it. Entity and incident reference lists carry at most 32 entries each and share an 8192-reference summary budget. Check state and topology remain available; `/v1/history?check=ID&limit=N` returns the full retained diagnostic values, with at most 1 MiB of observations per response (possibly fewer than N).

## Release and source export

Release tests and builds run in separate read-only jobs against the triggering commit; the publish job downloads the build artifact by its immutable ID and verifies checksums without executing repository code. Immediately before publication, the workflow recursively resolves lightweight or annotated tags and requires the resulting commit to equal the immutable triggering SHA; release notes include that SHA. All Actions are pinned to commit SHAs. Protect `v*` tags against updates and deletion using repository rulesets: this hosted setting is necessary to close the race between the final tag check and publication and cannot be established by this source change. Configure required reviewers and allowed release tags on the GitHub `release` environment to enforce hosted approval policy; the workflow file cannot establish those hosted protections itself.

Run `scripts/package.py` from a clean committed checkout after verification. The exporter rejects ignored or untracked inputs under source directories, rejects private-key/environment filenames even when tracked, and reads archive content exclusively from the pinned Git revision. Verification captures the commit and a deterministic per-file SHA-256 source manifest before and after testing, failing if either changes. Packaging requires matching clean verification identities and exported blob digests; evidence from another revision is rejected. Its manifest records that revision. Local changes made after validation cannot alter archive bytes.

Source identities and archive manifests use the same portable pathname policy.
Backslashes, drive/stream syntax, controls, Windows reserved names and trailing
dots or spaces are refused. Case and Unicode aliases, including directory
prefixes, are rejected before export; ZIP and tar manifests must match the
reviewed file set exactly.

## Capacity planning and collector migration

Before adding observers or moving a collector, save an authenticated `topo --json > snapshot.json`, then run `topo capacity --config proposed-topology.json --snapshot snapshot.json`. This reads only the reviewed topology and saved snapshot; it never opens or changes collector journals. Exit 2 means an existing allocation or the scheduled receipts need attention. Older collectors without `storage` cannot supply migration usage; omit `--snapshot` for schedule-only planning.

Each observer has an optional `storage_weight` (1 by default, 1–1000 explicitly). Receipt, state-byte and raw-history allocations are proportional to trusted weights, rounded down, with heartbeat slots reserved separately. A busy observer can receive more capacity without borrowing another observer's reservation. Weight changes do not change measurement contracts; reload applies the new allocation. An allocation smaller than already held state causes backpressure, not removal of unexpired deduplication receipts. Plan that change before restarting.

`serve` and `capacity` accept `--state-bytes` (up to the existing hard limit of 128 MiB), `--max-receipts`, `--max-history` and `--retention`. Zero uses store defaults. The plan's receipt-byte projection is a **lower bound**, including the ingestion window's extra hour of deduplication plus startup samples. It excludes retained observations, tails, floors, rollups and process overhead; leave headroom. Suggested weights describe relative scheduled sample rates and are never applied automatically. Disabled and maintenance checks are excluded. Saved snapshots describe their recorded time, not a live migration lock: still stop the old writer before copying its data.

Authenticated snapshots expose `storage` with per-observer use, limits, recent receipts and projected exhaustion. The Observers view shows this allocation alongside liveness. A cached snapshot remains historical evidence. This reporting does not bypass ingestion limits.

## Delivery telemetry

New workers attach bounded, worker-reported `delivery` telemetry to heartbeats. In Observers (`topo show observers`), inspect each collector digest's pending records/bytes, last **sample** ACK, last successful contact, retry time and a closed reason code. A heartbeat ACK updates contact only; it never advances the sample ACK or clears storage backpressure. Quarantine bytes cover the whole worker outbox; quarantined sample counts are since the worker started. Reports are timestamped and are not an independent collector receipt or target-health measurement. When a worker cannot reach any collector its latest status cannot be uploaded: the view becomes stale as normal. Old workers omit the telemetry.

Heartbeats allow at most eight collector reports, no URL/token/raw errors, and 6 KiB encoded payload. Each configured observer reserves 8 KiB for its persisted heartbeat independently of sample quotas. Workers report only their own observer's telemetry; this changes neither sample contracts nor ingestion scope.

A collector refuses a whole batch whose heartbeat it cannot accept: one from before delivery telemetry knows no such field (`invalid_batch_json`), and any refuses telemetry stamped more than 30 seconds in its future, as a worker whose clock runs ahead sends (`invalid_delivery_status`). So when a request carrying telemetry is refused, the worker sends it again at once without it, and only if that is refused too are its samples at fault, to be isolated and quarantined as before. If the bare request is accepted, that collector's heartbeats go without telemetry for an hour, then offer it again, so an upgraded collector receives it within the hour. Upgrading collectors before workers therefore loses no sample; until a collector is upgraded, its worker logs `…; resending without delivery telemetry` about once an hour.

## Generate and install a deployment bundle

`topo init` still creates the loopback demo. `topo init --role server|worker|client` instead generates a **new private directory** containing a reviewed manifest, checksummed native binary, configuration and credentials. `--interactive` prompts for missing non-secret values; secrets use `--token-stdin` or a private enrollment credential file. No service or network configuration changes during generation.

For a new collector, run `topo init --role server --config topology.json --server https://monitor.example:7447 --dir server-bundle`. Add `--observer local-id` only if the server executes that reviewed observer itself. `--tls-hosts` adds LAN/tailnet certificate SANs. The HTTPS URL needs an explicit port from 1024 to 65535; the listener uses that port. The bundle creates a private CA, one viewer credential and separate worker tokens under `enrollment/OBSERVER/`. The CA signing key remains under `authority/` and is never installed into the service runtime. A new server bundle creates a new trust identity; distribute its CA and enrollment credentials to the intended workers.

On a worker, use `topo init --role worker --config topology.json --observer cloud --credentials enrollment/cloud/credentials.json --ca-file enrollment/cloud/ca.pem --dir worker-bundle`. On a client, use `topo init --role client --credentials server-bundle/payload/credentials.json --ca-file server-bundle/payload/ca.pem --dir client-bundle`; its project is taken from the credential file, or set `--project` when using `--token-stdin`. Reviewed configurations may require auxiliary programs and probe secrets; Linux units read an optional private `worker.env` in the installation directory. Supply any required runtime environment before enabling those checks.

Review `manifest.json`, `payload/topology.json` and `service`. `topo install --bundle worker-bundle --dry-run` verifies payload hashes and prints paths plus a `bundle_sha256` review receipt without writing. Retain that receipt outside the bundle after reviewing its contents. `--verify` additionally checks HTTPS trust, project and worker/viewer authorization against the collector without emitting measurements or heartbeats. Worker verification uses authenticated `GET /v1/worker`, which returns only the token's project and observer. It grants no topology read or remote execution rights.

`topo install --bundle DIR --bundle-sha256 REVIEW_RECEIPT` installs the exact reviewed bundle locally. Any change to payloads, checksums, service definition or installation options invalidates the receipt. Privileged installations also reject bundle paths writable by other accounts. Linux and macOS service installation needs an administrator and runs the actual service under an unprivileged user. Linux defaults to `/etc/topophany`, with systemd and `/var/lib/topophany-ROLE` data; macOS defaults to the current user's config directory and a system LaunchDaemon; Windows uses a limited current-user logon task with restart settings. A client starts no background process; its default installation directory is the OS config directory `/topophany`, so existing `topo` clients immediately use the saved credentials. The bundled binary is under `INSTALL_DIR/bin`; add that directory to PATH if installing the CLI for the first time.

Use `--platform`, `--binary`, `--install-dir`, `--data-dir` and `--user` to generate for another host. A cross-platform bundle requires a matching binary and explicit target paths. Installation checks the native platform, payload permissions and regenerated service definition. Unix service users cannot be root. Windows directories are restricted to the current user, SYSTEM and administrators.

Installations save replaced files privately under `INSTALL_DIR/.backups`, restore them when installation/startup fails, and print the backup path. `topo install --rollback BACKUP` restores files and the prior service state, including a prior current-user interactive Windows task. It never copies or rewrites collector journals. Workers with pending outbox files are refused until their queue is drained or archived. Existing installations from another project and data directories with an active writer are refused. A fresh bundle alone does not migrate history: follow the stopped-writer backup procedure above. Review and retain signing material, backups and enrollment directories privately.

Use the original printed `.backups/install-*` path and keep the private
`.backups/rollback.key` with its installation. Rollback verifies the record and
all saved-file hashes before stopping a service. Moved, modified and unsigned
legacy backups are refused; recover legacy installations through a reviewed new
bundle. On POSIX, use root-owned installation and parent directories for
privileged installs and rollback; a directory owned by the sudo caller is not a
trusted root backup location.

## Monitor panels and managed logs

See [persistent monitor panels](REFERENCE.md#persistent-monitor-panels) for the
CLI and output protocol. Run standalone client panels under an existing process
supervisor with `topo monitor run NAME`; no monitor command installs a service.
An attached panel shares its host process lifecycle. Configuration changes,
including disable and output path changes, require restarting that process.
A panel startup configuration/lock error fails explicitly. A runtime output
error does not interrupt collector acknowledgements or worker durable enqueue.

Service accounts have different user configuration/cache directories from the
interactive user. Configure a service panel with an explicit root, for example
`--monitor-dir /etc/topophany/monitor`, an output in a service-writable cache
location, and pass the same root on `serve`/`worker`. Keep output outside the
journal/outbox directory. On sandboxed systemd services, grant that separate
cache directory with `ReadWritePaths`; make its ownership and permissions match
the service account. Review service definitions before restarting them. The
monitor output and lock are local-machine artifacts, not network-share storage.

`serve` and `worker` accept `--log-file PATH.log`, `--log-max-bytes BYTES` (default
5 MiB, 64 KiB–1 GiB) and `--log-backups N` (default 3, 1–10). The process owns
rotation and reopens the active log, preserving bounded numbered backups.
On POSIX, existing active logs and managed backups must have mode `0600` or
stricter; otherwise startup refuses them before writing. Correct their modes
with `chmod 600` before restarting. New logs and rotated backups remain private.
There is one lock-protected writer per managed log. Without `--log-file`, logs
continue to stderr. New macOS deployment bundles pass a managed
`DATA_DIR/topophany.log` and direct launchd's raw stdout/stderr to `/dev/null`;
normal service diagnostics go through the managed writer. Errors opening that
log cannot be recorded there: inspect the service status and run the service
command interactively when diagnosing startup permissions. Existing installed
services are not rewritten automatically; regenerate/review/reinstall their
service definition to opt in. Historical `stdout.log`/`stderr.log` files are not
deleted by this change; archive or remove them after stopping the old writer.

Delivery and ledger persistence errors retain a first report, summaries once a
minute while repeated reports continue, and a recovery record. Groups include
component, entity and error code, with report counts and first/last timestamps;
there are at most 128 groups. Delivery backoff with an outstanding error does
not count as recovery. Counts represent reports, not attempted connections.
Monitor output errors likewise report the first failure, periodic failure
counts and recovery. Existing incident records and state-change logs remain.

Collector and Worker logs emit `storage_io_since_start` once a minute. The
bounded, process-local counters include journal write calls/bytes/errors/time,
checkpoint replacement calls/bytes/errors/time, logical queue file operations
and written bytes, and separate file/directory synchronization calls/errors/time
(in nanoseconds). Queue operations count reads, directory listings, metadata
reads, removals and durable replacements; they do not count each nested syscall
inside a replacement separately. Expected missing-file probes also contribute
to operation error counts. Byte counts measure application writes, including
temporary-file bytes written before a failed replacement. They do not measure
filesystem/device write amplification or physical SSD wear. Counters reset on
restart and are not persisted. The `storage.*` monitor metrics expose the most
useful totals to local panels.

To measure a deployment, record two counter snapshots over a fixed period,
subtract them, and compare with the operating system's process/device I/O
measurements for the same load. Include cold start, steady healthy sampling,
delivery outage/recovery, checkpoint and log rotation separately. Display-only
writes are rebuildable and do not force sync; they are not evidence deduplication.
New observations with the same result still persist and count as monitoring
evidence. Outbox ACK cleanup batches directory synchronization, and checkpoint
cadence adapts within the replay budget described below. Collector acknowledgement
remains after durable storage and reliable worker enqueue is unchanged.

### Idle worker heartbeats

`worker --heartbeat-interval` defaults to `15s` (allowed `1s`–`1m`). The worker caps it at one third of its observer's `stale_seconds`, recalculating on topology reload. Delivery continues every two seconds for queued samples; sample uploads always carry current liveness when their schema and revision permit it. Empty rounds send only when the idle deadline is due. A new boot, contracts digest, revision, or backward clock step sends liveness on the next eligible round. Retry backoff remains in force after transport failures. Storage backpressure still permits independent heartbeats, at the same idle cadence. Heartbeats never extend the validity of check measurements. Delivery telemetry may therefore update less often while idle.

Snapshot responses negotiate gzip for payloads of at least 1 KiB when compression saves bytes. The collector generates gzip only when requested, including after an identity response was cached, and retains both representations within the existing 32 MiB budget. Unhelpful compression results are remembered for that capture. Native clients bound encoded input as well as decompressed JSON; deadlines, validation and decoded response limits still apply. Clients that do not negotiate gzip keep receiving JSON without paying for compression.

New viewers retain one validated public topology per collector and send its revision as `topology_revision` on subsequent snapshot reads. A matching collector omits the static topology and marks `topology_omitted`; a different revision sends it in full. State timestamps, measurement expiry and dynamic validation are unchanged. The cache owns its data independently of returned snapshots, is scoped to the configured collector, and is replaced on reload. Older clients and collectors keep using complete snapshots without negotiation.

Automatic checkpointing uses the last successfully persisted checkpoint's encoded size as its journal threshold, bounded by `serve --checkpoint-bytes` (default 16 MiB) and `--max-journal-bytes` (default 128 MiB). Both must be at most 128 MiB, and the ceiling must be at least the floor. Equal values restore a fixed threshold. The threshold survives restart through the checkpoint's file size; no on-disk format changes. This reduces repeated large rewrites at the cost of more journal disk space and restart replay. One bounded transaction can cross the threshold, still below the 256 MiB journal recovery limit. Explicit checkpoints retain their immediate durability behavior.

The collector reuses folded check evidence across HTTP views, its incident watcher and attached monitor panels until the store evidence version or topology generation changes. Each read still recalculates measurement freshness, observer liveness and entity state using its own time; future samples and clock rollback are handled without a new write. The one-second HTTP cache continues to bind observer projections to one capture. Published snapshots and prepared evidence own separate mutable data.

One-shot engine users, including worker-local panels and preview generation,
use a direct build path without allocating a reusable evidence cache. Both paths
share evidence folding and entity derivation. Compare cache hits, freshly prepared
evidence and direct builds separately when measuring performance.

The outbox stores partial recipient acknowledgements in `acks.jsonl`, separately
from immutable observation files. Each batch needs one file sync. The journal
has a separate 1 MiB ceiling and is compacted into pending records before it
grows past that ceiling. Recovery discards an incomplete final append, rejects
corrupt complete entries, and preserves at-least-once delivery. Keep this file
with the rest of the outbox when moving or backing up a worker.

After a successful delivery, a worker with more pending records immediately
continues backfill within a bounded burst: at most four requests (each at most
1 MiB), 100 ms between requests, and a one-second continuation window. Idle
polling retains its two-second cadence. A rejection, 429, invalid acknowledgement
or local persistence error ends the burst and preserves the normal retry policy.
Each collector runs independently.

Outbox payloads stay on disk after startup validation. The worker retains only
record identity, first-sample timestamp, pending recipients and byte accounting;
each send loads a batch bounded by count and encoded bytes. A missing or corrupt
payload produces `local_read_failed`, retains the queue entry and sends no part
of that batch. The independent `--outbox-records` limit defaults to 20,000.

Automatic collector checkpoints capture state and a WAL byte boundary, then
stream and sync the checkpoint in one background task. Ingestion keeps syncing
the live WAL. After publication, a bounded lock-held operation atomically
replaces the journal with the suffix appended after that boundary. Recovery
accepts either the old journal (deduplicated against the checkpoint) or this
suffix. At the journal ceiling, writers wait for the active checkpoint; reads
remain available during the main checkpoint write. Explicit checkpoints and
shutdown wait for publication, and persistence failure stops further writes.

### External replay-state capacity

Collector startup, topology reload and `topo capacity` conservatively reserve each
external binding's declared maximum report, 256 sample IDs and cursor metadata.
The estimate includes JSON escaping of IDs and must fit both `--state-bytes` and
the observer's weighted share. An impossible configuration is refused before
replacing running state. Leave additional room for receipts and raw history.
External state can now use up to 128 MiB within that existing global budget; each
source is limited to its declared bindings' reservation. Runtime capacity errors
retain the storage-backpressure classification, so workers can continue standalone
heartbeats. Accepted replay identities are never evicted merely to admit a write.
Earlier binaries enforce a 4 MiB external-state recovery limit and cannot reopen a
larger checkpoint; retain a matching backup before downgrading.

Worker HTTP 401/403 responses pause delivery with bounded backoff while keeping
original pending samples. Repair credentials or collector authorization and
restart the worker when its saved credentials changed. Authentication failures
do not quarantine samples; explicit data rejections still do.

## Check rollout agreement

`topo rollout --config topology.json --json` reads every saved collector address
independently, including aliases, with a five-second deadline per address.
One unavailable collector does not hide the others behind ordinary viewer
failover. Each row reports instance identity, public configuration agreement,
public measurement changes, and worker contract/liveness states. Exit 2 means
at least one address or worker is not verified as matching; exit 0 means all
reported public configurations and worker contract states agree.

The viewer cannot inspect private execution settings or exact worker file
revisions. A worker matching an old collector is labelled
`matches_old_collector_contracts`, never as deployed to the desired configuration.
Inspect private revisions locally with `topo validate`. Multiple address rows
may refer to the same instance. This command only reads; it neither distributes
configuration nor reloads a service.

## Inspect the exact local running configuration

`topo runtime --data DATA_DIR --config topology.json --json` compares a local
collector/worker's loaded private revision against a reviewed file. It detects
edits that have not been reloaded, even when they leave measurement contracts
unchanged. It exits 0 for a current matching lease, 2 for a mismatch and 3 for an
expired lease. Without `--config`, it reports the current local record only.

Each process writes private `local-runtime.json` using its existing tick, at most
once per five seconds and immediately after reload. Records expire after 15
seconds and are removed on orderly shutdown. A crash or write failure cannot
leave a permanent live claim. These are time-stamped observations, not an
instantaneous process query. No extra scheduler or network endpoint is added.
The private digest stays local and must not be included in public diagnostic
exports; viewers continue to receive only public configuration identities.

## Rehearse recovery and upgrade compatibility

```sh
topo recovery --data stopped-backup --config topology.json --auth auth.json
topo recovery --role worker --data worker-backup --config topology.json
```

Run the candidate new binary against a stopped backup before upgrading. The
command acquires the existing writer lock non-blockingly, refuses an active
writer, symlinks and special files, then copies regular data into a private
temporary directory. It runs the actual collector checkpoint/journal/ledger/alert
queue recovery or worker outbox recovery there. No probe, worker delivery or
notification starts. Original data is never rewritten; scratch data is removed
when verification finishes. Keep the original `writer.lock` marker in backups.

The JSON receipt includes byte/file counts, a content digest, binary version,
configuration revision, recovered state or pending count, and whether explicit
project identity survived in the retained evidence. Compacted hashes alone
cannot establish project identity. `auth_config_validated` is true only when
`--auth` was supplied and validated; remote token authorization is not tested.
Newly quarantined pending records produce `recoverable:false` and exit 2.
Unsupported schemas, malformed durable files and exhausted budgets fail.

The default copy cap is 1 GiB, configurable with `--max-bytes` up to 8 GiB; the
traversal is capped at 100,000 entries. Supply proposed `--state-bytes`,
`--max-history`, `--max-receipts`, `--retention` or worker `--max-records` when
they differ from defaults. A successful rehearsal proves readability with those
budgets, not live deployment correctness or future capacity. Retain matching
private topology/authentication and signing material alongside the stopped data.
