# Dynamic configuration and managed software updates

Status: proposed implementation plan; no remote management capability is shipped
by this document. Baseline: `main` at `a49a0e8`, following Topophany 1.6.0.
Prepared on November 12, 2026.

Topophany will let an operator publish reviewed configuration and deploy a
specific official release to explicitly enrolled installations. Workers must
default to rejecting both remote configuration and software update commands.
These are independent, local opt-ins. A collector, an upgrade, an enrollment
token, or a downloaded topology must never enable either permission implicitly.

This plan specifies the authorization boundary, proposed configuration and
protocol, failure behavior, implementation sequence, and release acceptance
criteria. Names of new commands, fields, endpoints, and packages below are
proposals, not commands or configuration accepted by the current binary.
Existing commands are identified explicitly. “Must” states an implementation
requirement; a phase is complete only when its acceptance evidence exists.

Reading paths:

- Operator controls: [local defaults](#4-local-configuration-and-default-behavior),
  [enrollment](#5-enrollment-and-authorization), [update policy](#9-local-software-update-policy),
  and [fleet workflow](#14-fleet-rollout-and-user-experience).
- Implementation contracts: [command protocol](#7-command-protocol-and-durable-state),
  [configuration activation](#8-configuration-publication-and-activation),
  [release verification](#10-resolve-and-authenticate-an-official-release),
  [publisher trust](#11-publisher-trust-and-signing), [updater](#12-local-update-executor),
  and [rollback](#13-compatibility-and-rollback).
- Review and delivery: [threat model](#16-threat-model-and-residual-risks),
  [implementation phases](#17-implementation-sequence-and-code-ownership), and
  [acceptance matrix](#18-required-acceptance-matrix).

## 1. Scope and fixed decisions

The first delivery supports a single management authority, outbound HTTPS
polling, dynamic topology configuration, and managed binary updates on Linux
with systemd. Configuration management must use a portable internal reload
entry point so that Windows does not depend on Unix signals. macOS and Windows
installation support follow separate native acceptance gates.

The update target is `topo`, including worker and collector installations.
Interactive clients can enroll explicitly in a later phase. The separate
`topophany-bar` application, third-party probe tools, operating system packages,
and container orchestrators are outside the first update executor's scope.

The following decisions are mandatory:

1. Configuration acceptance and update acceptance both default to `false`,
   including fresh installs, generated bundles, upgrades, and missing fields.
2. Only a local administrator can enable acceptance, change the controller
   identity, or broaden a node's policy. Remote topology never contains these
   permissions. Existing viewer and worker credentials gain no management powers.
3. A controller requests a desired state. A node independently authorizes it,
   and an update helper independently verifies executable provenance.
4. Only published, stable releases from the pinned official GitHub repository
   are eligible for remote installation. An operator selects an exact version.
5. Download URLs, executable paths, service definitions, shell commands,
   installation scripts, and arbitrary environment variables are not command
   parameters.
6. Configuration and software changes use durable, idempotent transactions.
   A network acknowledgment is not proof of activation or health.
7. Existing observation identities, sample times, contracts, storage durability,
   viewer redaction, and probe resource budgets remain authoritative.
8. Loss of the controller does not stop otherwise healthy monitoring. Failed
   verification blocks a new management action without weakening verification.
9. Installation failure must not require the failed `topo` process or a reachable
   controller to restore a permitted previous executable.
10. The first delivery does not claim fleet-wide atomic activation, lossless
    transitions for arbitrary offline workers, or protection against a compromised
    local administrator or trusted software publisher.

## 2. Existing foundations and gaps

| Area | Existing implementation | Required change |
| --- | --- | --- |
| Local configuration | Strict topology decoding, validation, defaults, private/public revisions in [config](../internal/config/config.go) | Separate management policy and signed deployment manifest; retain strict decoding |
| Reload | [worker](../internal/app/worker.go) and [collector](../internal/app/collector.go) prepare a scheduler before switching; [reload helpers](../internal/app/reload.go) preserve project/observer identity | Extract a transactional apply entry point shared by local reload and management; cover post-prepare failures |
| Measurements | [contracts](../internal/config/contract.go) bind each observation to what was measured | Preserve contracts across scoped configuration projection and coordinate contract changes |
| Authorization | [API](../internal/api/server.go) supports viewer and observer-scoped worker roles and explicitly rejects management roles | New management credentials, scopes, routes, and admission budgets |
| Public configuration | `/v1/config` serves a redacted viewer representation | Dedicated authorized node configuration download; never execute viewer topology |
| Progress inspection | [rollout](../internal/app/rollout.go) checks public revisions/contracts; [runtime](../internal/app/runtime_status.go) records local version/private revision with a lease | Private management status with desired/prepared/active state and fresh receipts |
| Installation | [bootstrap installer](../internal/bootstrap/install.go) locks installations, validates bundles, and authenticates backups | Restricted binary-only update transaction; do not feed remotely supplied bundles to the privileged installer |
| Existing rollback | [backup authentication](../internal/bootstrap/rollback_backup.go) uses a private per-installation HMAC key | Reuse safe primitives; add executable provenance and data compatibility checks |
| Recovery | [recovery command](../internal/app/recovery.go) validates stopped data copies without probes or delivery | Integrate candidate/previous-reader checks into update preflight |
| Release pipeline | [release workflow](../.github/workflows/release.yml) pins actions, builds six targets, validates archive membership and SHA-256, and checks the tag commit | Immutable releases, signed update metadata, build provenance, and a trusted publication policy |

The existing installer rejects a worker with pending outbox entries. Its backup
of installation files is not a backup of the entire collector data directory.
Neither behavior may be bypassed by simply calling `topo install` from a worker.

The current Linux service uses `NoNewPrivileges=true`, `ProtectSystem=strict`,
and a writable data directory. Keep that separation; do not make the main
monitoring process root or grant it general write access to its installation.

The current [security model](SECURITY.md) trusts reviewed local topology and
provides no remote configuration or restart authority. The implementation must
document the new opt-in boundary without changing the default boundary.

## 3. Components and ownership

| Component | Responsibilities | Must not control |
| --- | --- | --- |
| Management controller | Validate proposals, pin target installations, record desired state, issue signed commands, collect receipts, coordinate batches | Node acceptance flags, node policy, publisher trust roots |
| Node management client | Poll, authenticate, verify commands, enforce local configuration policy, stage configuration, report status | Arbitrary privileged writes, service definitions, publisher approval |
| Local update helper | Verify update authority and release metadata, stage artifacts in protected storage, replace registered binaries, supervise health and recovery | Arbitrary commands, unregistered services, remote filesystem paths |
| Release publisher | Authenticate official release bytes and compatibility/security metadata | Permission to deploy to a particular user's installation |
| Local administrator | Enroll installation, set independent acceptance flags and policy, register helper, recover trust state | Implicit authority delegated through a viewer or observation token |

The controller may initially be an optional subsystem of `topo serve`, but its
listener, authorization, queues, journal, and resource reservations are separate
from observation ingestion. No database daemon or broker is required for the first release.

A node has a stable, random `installation_id`, distinct from `observer`, boot ID,
hostname, and collector ID. One host can run multiple installations; an observer
can be represented by more than one process. An update targets an installation,
not whatever process happens to report a particular observer name. The helper
registers which binary and services belong to that installation. A shared binary
requires a local registration listing every affected service and a joint update
transaction; it cannot be changed under an unrelated service.

Two observation collectors are not two management leaders. Nodes pin one
`controller_id`, its HTTPS endpoint and its command trust root. Delivery failover
must not change management authority. The first release has no automatic management
failover. Authority replacement requires local enrollment or an explicitly
trusted authority-transition procedure, not a larger generation received from
another collector. A later HA design needs leader fencing and a durable authority
epoch; observation deduplication alone provides neither.

## 4. Local configuration and default behavior

### 4.1 Independent local opt-ins

Propose an optional `--management-policy PATH` startup setting for collector and
worker. Without it, both receiving capabilities are disabled and no node management
poller, enrollment, or helper installation is started implicitly. Hosting the
controller requires the separate local opt-in in section 4.4.
Generated policy files contain the following defaults:

```json
{
  "schema": 1,
  "config": {
    "accept": false
  },
  "updates": {
    "accept": false
  },
  "report_management_status": false
}
```

An omitted acceptance field is `false`; a missing policy file grants nothing.
An unknown field, invalid type, duplicate key, unsupported schema, insecure file,
or invalid trust reference disables new management actions and produces a local
diagnostic. It must not silently retain a previously cached `true` permission.
Existing monitoring continues under its last committed configuration when that
configuration and its persisted installation binding can be validated. A node
without such state uses its reviewed local `--config` as it does today.

| Configuration acceptance | Update acceptance | Result |
| --- | --- | --- |
| `false` | `false` | No configuration or update commands; no management network traffic unless status reporting is separately opted in |
| `true` | `false` | Only configuration commands within the local policy |
| `false` | `true` | Only updates within the local update policy; current topology remains locally selected or previously committed |
| `true` | `true` | Both capabilities, independently authorized and serialized where they affect the same installation |

Enabling either capability permits the minimal polling and status receipts
necessary for that capability. `report_management_status` separately permits
status-only reporting when both are disabled; it never grants execution. The
controller represents a node that stops reporting as unknown/stale, not as
proof that its local flags are enabled or disabled.

An enrolled policy could have the following shape. Placeholder identities and
paths illustrate the proposed format; this is not a deployable current config:

```json
{
  "schema": 1,
  "project": "homelab",
  "installation_id": "worker-tokyo-example",
  "role": "worker",
  "observer": "tokyo",
  "controller": {
    "id": "control-main",
    "url": "https://control.example.invalid:7448",
    "ca_file": "/etc/topophany/management-ca.pem",
    "credential_file": "/etc/topophany/management-credential.json",
    "command_trust_file": "/etc/topophany/management-trust.json"
  },
  "config": {
    "accept": false,
    "constraints_file": "/etc/topophany/config-policy.json"
  },
  "updates": {
    "accept": false,
    "policy_file": "/etc/topophany/update-policy.json",
    "publisher_trust_file": "/etc/topophany/publisher-trust.json"
  },
  "report_management_status": false
}
```

Missing policy/trust files are not required while their capability is disabled,
but all must pass local preflight before that capability can become effective.
The effective permission is the intersection of local acceptance, valid local
trust, scoped command authorization, and local constraints. Controller preferences
can narrow a rollout but cannot broaden that intersection.

### 4.2 Policy ownership and changes

For system services, policy, trust roots, credentials, and helper registration
must be in administrator-controlled directory chains. The service identity may
read its needed credentials but cannot modify authorization policy. POSIX files
must reject symlinks and special files, enforce appropriate owners and private
modes, and verify trusted parents. Define separate file classes: public trust
material may be readable, policy must be administrator-writable only, and secret
files must be readable only by the required identities. Do not blindly reuse a
private-file reader whose ownership/mode assumptions differ from the service
access requirements. Windows must enforce equivalent owner/DACL and
reparse-point restrictions. A user installation trusts its own user; it does not
claim isolation from other processes with that same user's privileges.

There is no permissive environment-variable override and no remote policy-edit
endpoint. If startup flags are added to temporarily disable either capability,
they may only narrow policy. A generated bundle or later software upgrade must
preserve explicit local settings and leave newly introduced capabilities off.

Propose local commands `topo management inspect`, `enable`, `disable`, and
`unenroll`. Enabling displays the controller identity, policy, authorized probe
destinations or update repository, and effective grants; only an explicit local
operation can write the new permission. Installation/enrollment is separate from
enabling either capability. Noninteractive enablement must require explicit
named capabilities, never an implied “yes to everything.”

Local policy changes increment a local policy revision. The client checks policy
on every polling tick, before accepting a command, and before activation. The
helper rereads it independently before any privileged mutation. A local disable
command must synchronize with the running client/helper and return whether any
transaction has already crossed its commit boundary. Merely writing a file is
not evidence that a running transaction has stopped.

### 4.3 Disabling and disconnecting

| Situation | Required behavior |
| --- | --- |
| Disable configuration acceptance | Cancel staged/uncommitted configuration, reject further activation, retain the last committed config |
| Disable update acceptance before replacement | Cancel the transaction and downloads; do not stop the service or install cached candidates |
| Disable during the replacement critical section | Complete local recovery to a permitted healthy executable; start no further transaction; report the actual result |
| Reenable either capability | Start a new acceptance session; require freshly issued commands rather than resume canceled tasks |
| Controller unavailable | Keep monitoring and delivery; retry management with bounded backoff; execute no uncommitted stale activation |
| Policy becomes unreadable or invalid | Block new remote actions and cancel prepared ones; keep valid committed monitoring state; make failure visible locally |
| Unenroll | Disable both grants, revoke/remove credentials, clear pending work; retain replay/trust history unless a separate local identity reset is performed |

Disabling management does not silently revert topology to `--config`, because
that may reenable obsolete probes. A separate local revert operation validates
the selected local topology and explicitly switches authority. While managed
configuration is active, a plain `SIGHUP` must not accidentally select a different
file: it reloads according to the documented active authority, and reports a
conflict if local and managed configuration changes compete.

Policy expiry and command expiry prevent new activations; they do not expire
already committed monitoring configuration. Removing a destination from local
configuration policy must also stop future managed probes using that destination
and report `policy_blocked`. Existing unmanaged local configuration retains the
current local-administrator trust model. Deleting a file is not a supported way
to broaden permissions or to transfer configuration authority.

### 4.4 Controller settings

The management controller has its own proposed `--control-plane-policy PATH`.
It is independent of a collector's permission to receive configuration or software
updates. Hosting a controller must not enable management of that same installation.
The controller policy defaults to `enabled=false` and requires explicit TLS,
credential/signing-key, storage and listener configuration before it can be enabled.

```json
{
  "schema": 1,
  "enabled": false,
  "listen": "127.0.0.1:7448",
  "tls_cert_file": "/etc/topophany/control-server.pem",
  "tls_key_file": "/etc/topophany/control-server-key.pem",
  "auth_file": "/etc/topophany/control-auth.json",
  "signing_key_file": "/etc/topophany/control-signing-key.json",
  "data_dir": "/var/lib/topophany/control",
  "rollout": {
    "canary_count": 1,
    "max_concurrent": 1,
    "observation_seconds": 300,
    "pause_on_first_failure": true,
    "allow_partial_config_activation": false
  }
}
```

This example is also proposed and uses placeholder files. Binding outside
loopback requires an explicit local choice; TLS remains mandatory even on a
loopback TCP management listener. The administrator chooses node enrollment
scopes, config/update publisher scopes, rollout defaults and audit retention.
An administrative listener is disabled unless separately configured; a private
local administrative socket is the initial interface. Per-deployment overrides
must be shown in the review and remain subject to every node's independent policy.
Controller signing keys are private service credentials, not topology contents
and not official publisher signing keys.

## 5. Enrollment and authorization

### 5.1 Separate identities and scopes

Keep existing `viewer` and `worker` roles unchanged. Use a separate management
credential store, with opaque random credentials hashed at rest on the controller
and private credential files on clients. The first release uses server-authenticated TLS
and scoped bearer credentials; mTLS may later supply device identity without
replacing operation-level authorization.

| Principal or scope | Allowed actions |
| --- | --- |
| Existing viewer | Existing redacted reads only |
| Existing worker | Existing observer-scoped enrollment check and observation ingestion only |
| `management-agent` | Read only this installation's desired state/blobs; write only its status receipts |
| `management-read` | Inspect authorized deployment metadata and redacted outcomes |
| `config-publish` | Prepare/activate configuration for an explicitly authorized project and installation set |
| `update-publish` | Prepare/activate an eligible exact version for an explicitly authorized installation set |
| Local administrator | Enrollment, acceptance flags, trust and constraints, service registration, exceptional recovery |

Configuration and update publication scopes are separate. Project scope alone
does not imply every installation in that project. Worker observation tokens
cannot self-enroll management agents. A management agent cannot mint credentials,
publish commands, fetch another observer's execution config, or return arbitrary
paths for a helper to use.

The controller is disabled by default. When explicitly enabled, its node-facing
HTTPS listener uses separate admission budgets. Administration initially uses a
private local socket or an explicitly configured TLS management listener with
administrative credentials. No plaintext exception, certificate bypass, ambient
proxy, credential in URL/argv, cookie session, or CORS access is introduced for
management. Browser viewers remain read-only; Origin checks are not authentication.

### 5.2 Bootstrap and credential lifecycle

Enrollment requires a reviewed local registration containing project, role,
observer where applicable, installation ID, controller identity/CA, command trust
root, and local constraints. If a one-time enrollment secret is used, it is
short-lived, single-use, scoped to that registration, and supplied through stdin
or a private file. It supplies no acceptance grant.

The first management-capable version and updater must be installed locally by a
trusted mechanism. An old observation-only worker cannot be remotely bootstrapped
using its existing token. Official publisher trust is established at this local
step, not downloaded unsigned from the controller on first use.

Rotate management credentials independently of observation credentials. Routine
rotation can be a bounded enrollment protocol proving possession of the current
credential and retaining the same installation/scope. Compromise recovery requires
local re-enrollment; it cannot trust the stolen credential as its only authority.
Revocation takes effect at the controller for future reads and receipts, while
local disablement stops execution on the node. Document the finite lifetime of
already signed commands; transport credential revocation alone cannot recall a
command already obtained by a disconnected node.

## 6. Local configuration constraints

Remote configuration grants access to networks and secrets reachable by the
executor. Removing literal secrets from topology does not eliminate that risk:
an attacker could reference an existing environment secret and change its HTTP
destination. Policy must authorize the full use of a capability, not just its name.

| Policy dimension | Requirement |
| --- | --- |
| Check kinds | Explicit allowlist; unknown/new kinds are denied after software upgrades |
| Destinations | Allowed protocols, hostnames and/or CIDRs, ports, and relevant HTTP paths/methods |
| Address resolution | Enforce IP constraints at connection time, including IPv6, mapped IPv4, `resolve_ip`, rebinding and explicit DNS resolver choices |
| Proxy paths | Explicitly authorized hop types and endpoints; no environment proxy or silent direct fallback |
| Secrets | Bind each allowed environment reference to specific destination/TLS identity, method/path, header, and route; deny arbitrary environment reads |
| TLS and local files | Explicit local CA/identity references; remote values cannot name arbitrary filesystem paths or disable verification |
| Host and local adapters | Explicit permissions for process/resource queries, loopback APIs, SSH forwarding, WireGuard and other helper executables |
| Probe budgets | Local maximum checks, concurrency, response sizes, timeouts and frequency; remote settings can only remain within these ceilings |
| Maintenance and disablement | Explicit authorization to disable checks or suppress alerts; audit such changes even when they do not create new network access |
| Privileges | A new check requiring unavailable privileges reports `local_permission_required`; it does not change capabilities, users, service units or sudo rules |

An empty allowlist denies that capability. No wildcard destination or unrestricted
secret policy is generated automatically. The enrollment UI/CLI may generate a
narrow policy from a reviewed topology, show its grants, and require a local
choice before enabling acceptance. Read-only HTTP methods can still have side
effects at a server; policy authors must review the actual health-check endpoints.

For proxy-side DNS, the worker may not know the destination IP chosen by the
proxy. If a policy requires an IP guarantee that cannot be enforced through the
selected route, refuse that combination. Do not claim that checking the proxy's
IP constrains its eventual destination. Homelab/private-network probes remain
possible through explicit policy; a blanket ban on all private IPs is unsuitable.

The first release excludes literal secrets and secret-bearing URL values from
remote payloads. Existing local topologies may still support them under today's rules.
Remote payloads use permitted local secret references; secret creation/rotation,
collector auth files, alert webhook credentials, service environment files and
management policy require separate local workflows in this phase.

Constraints are checked during preparation and at the execution/transport
boundary. Otherwise DNS changes, environment changes, and policy revocation
between validation and execution could bypass the decision. Existing adapter
endpoint restrictions and redaction stay in force in addition to these checks.

## 7. Command protocol and durable state

### 7.1 Transport and APIs

Use outbound short polling with a proposed 15-second interval, 20 percent jitter,
and exponential backoff capped at 5 minutes. Permit local interval tuning within
documented bounds. Reset backoff after a successful authenticated exchange; obey
bounded `Retry-After`. No polling loop blocks observation delivery or the scheduler.

Suggested routes, under a separate management listener:

| Route | Principal | Meaning |
| --- | --- | --- |
| `GET /management/v1/capabilities` | Management agent/operator | Control protocol and supported operation versions |
| `GET /management/v1/desired` | Management agent | Only this authenticated installation's pending commands; supports conditional retrieval |
| `GET /management/v1/configs/{digest}` | Management agent | Only a blob assigned to this installation; possession of a digest is not permission |
| `POST /management/v1/status` | Management agent | Idempotent, bounded receipts for this installation |
| `POST /management/v1/config-deployments` | Config publisher | Validate and create a proposal using an expected base generation |
| `POST /management/v1/update-deployments` | Update publisher | Resolve exact release and create a fixed target plan |
| `POST /management/v1/deployments/{id}/activate` | Appropriate publisher | Activate prepared targets after required conditions/approvals |
| `POST /management/v1/deployments/{id}/cancel` | Appropriate publisher | Cancel remaining uncommitted actions |

Administrative create/activate operations use idempotency keys and compare-and-
swap preconditions. Node GETs never activate work or grant authority. Do not add
management fields to observation batch acknowledgments without capability
negotiation: existing strict decoders must keep working. Long polling/SSE is
deferred; current client and server timeouts are not suitable for unbounded waits.

### 7.2 Signed command envelope

Closed command types are `config.prepare`, `config.quiesce`, `config.activate`, `config.cancel`,
`update.prepare`, `update.activate`, and `update.cancel`. There is no `exec`,
generic file write, raw restart, or remotely extensible operation type.

Every command authenticates these fields:

| Field | Purpose |
| --- | --- |
| `schema`, `kind` | Strict protocol and operation discrimination |
| `controller_id`, `authority_epoch`, `project` | Pinned authority and tenant binding |
| `installation_id`, `role`, optional `observer` | Exact recipient, independent of caller-selected request fields |
| `acceptance_session` | Current locally created capability session; changes after disable/reenable or re-enrollment |
| `command_id`, `deployment_id`, `sequence` | Idempotency, audit identity and replay ordering |
| `desired_generation`, `base_generation` | Desired configuration/update generation and active-state precondition |
| `payload_sha256`, `payload_length` | Bound configuration or update manifest bytes |
| `issued_at`, `not_before`, `expires_at` | Bounded authorization window |
| `required_capabilities` | Explicit protocol/feature prerequisites |
| Operation payload | Config digest/contracts, or exact release/asset identity, rollout conditions, and permitted recovery pair |

Keep `sequence` separate from desired generation: prepare and activate have
different sequences for the same generation. Acceptance sessions are independent
for configuration and updates; toggling one capability cannot grant or reset the
other. A config rollback publishes old
content under a new generation. The activation command binds the exact prepared
payload and recipient's preparation receipt, not a mutable deployment alias.

Use a versioned envelope with base64url-encoded payload bytes and detached Ed25519
signatures. Sign the exact decoded payload bytes with the domain separator
`topophany.control.v1` followed by a NUL byte. Do not decode, reserialize JSON, and
verify different bytes. Verify against locally pinned keys, then strictly parse
the authenticated payload; reject duplicate keys, unknown fields, trailing data,
non-integer counters, invalid timestamps and excessive nesting. Key IDs are only
selectors into local trust and never fetch locations. Signature counts use
distinct trusted key IDs, not repeated signatures from one key.

Persist the highest accepted sequence per authority epoch, installation and
capability channel before acknowledging acceptance. The same command ID/sequence
and digest returns its recorded status without repeating effects; the same ID or
sequence with different bytes is a conflict. Older sequences and mismatched base
generations fail. Counter overflow fails closed. Rejected unauthenticated commands
cannot advance counters. Compaction preserves replay floors and terminal results
needed for retries. Restoring an old management backup does not authorize resetting
sequence floors; use local recovery and a fresh authority/session binding.

Proposed activation-command lifetime is 15 minutes, locally capped at 24 hours;
prepared data may remain cached for at most 24 hours without granting activation.
Expired work requires a fresh command, preserving the original pinned digest and
revalidating policy. Refuse new activation when the clock is clearly invalid or
has moved behind the persisted trusted-time floor; continue monitoring and report
the fault. Wall-clock expiry complements persistent sequencing, not replaces it.

### 7.3 Journals and concurrency

Controller proposals, signatures, approvals, target snapshots and receipts are
durable before success is returned. Node acceptance, prepared state, active state,
replay floors, policy revision and recovery records are crash-consistent. A full
disk means no new management mutation, not an unlogged successful operation.

Use independent bounded management storage; reuse proven locking, atomic replace
and sync primitives, not the observation journal's schema. Protect directories
against untrusted replacement. System-helper trust and replay records must remain
administrator-owned even though probe data is service-owned. Checksums/HMACs with
keys writable by the same process do not protect against that process being
compromised; document the ownership boundary explicitly.

Only one activation/configuration switch or binary update may affect an
installation at once. Preparing an update cannot authorize a later config change;
a changed active config/policy/installation invalidates its preflight and requires
revalidation. Conflicting local reload, installer, management and helper operations
must share an explicit lock/coordination protocol, not race on files.

## 8. Configuration publication and activation

### 8.1 Configuration identity and projection

Keep these identities distinct:

- Deployment generation orders authorized desired states.
- SHA-256 of exact downloaded bytes authenticates the payload.
- Existing private configuration revision identifies decoded effective config.
- Per-check contracts identify measurement semantics; observer digests summarize
  those contracts.
- Public revision and public contracts remain the only identities exposed to viewers.

The controller retains the reviewed full topology and produces an observer-scoped
execution topology for each worker. Projection includes the dependency closure
of checks, sources, endpoints, nodes, paths and referenced relationships required
by validation. Do not simply filter the checks array. All selected native and
external check contracts must equal those calculated from the full collector
topology. The signed manifest binds both payload identity and expected contracts.
Test defaults, source fan-out, route changes, references, disabled checks, and
credential references explicitly. A collector receives its appropriate full
topology; any local probing still passes its own executor policy.

Avoid arbitrary post-download local overrides that change measurement semantics.
Local policy may accept, deny, or constrain execution; if an override changes a
contract, it must become an explicitly planned topology change that the collector
also understands. Local secret values are resolved at execution and are never
included in receipts or public hashes. Downloaded topology is sensitive even
without secrets because it describes private infrastructure.

### 8.2 State machine

Use `proposed → prepared → activating → active`, with terminal outcomes
`rejected`, `canceled`, `failed`, or `rolled_back`. A later revision can supersede
uncommitted work, but must not erase its audit record.

1. **Plan.** Validate topology, generate private and public diffs, classify
   contract changes, compute projections, pin recipients and capability needs.
   Require the publisher's expected base generation to match current desired state.
2. **Prepare.** Node checks local acceptance, envelope, project/observer, constraints,
   secrets availability without disclosure, version capabilities, size and disk
   budget. Decode/validate, build the complete execution plan, verify contracts,
   and persist the candidate in a private versioned data directory. Continue the
   old scheduler and report prepared only after durable storage succeeds.
3. **Authorize activation.** Controller checks required prepared receipts, their
   age, rollout policy and approvals, then issues a fresh bound activation command.
   A downloaded/prepared config has no independent right to run.
4. **Apply locally.** Recheck policy and preconditions; persist activation intent;
   stop/join the old scheduler at the defined boundary; atomically select the
   candidate and publish its matching runtime generation. On a late failure,
   restore the old scheduler and active pointer. No intermediate file is treated
   as committed merely because it exists.
5. **Confirm.** Report generation, private revision, contracts and runtime boot
   identity only after the new scheduler/backend is active and the transaction
   result is durable. Control health checks detect failed initialization; a probe
   target being down is not itself evidence of a bad deployment.

Extract the existing reload code rather than duplicating scheduler setup. The
current collector path stops local probing before its backend reload completes;
the refactor must explicitly restore probing if a subsequent operation fails.
Disk state, backend generation, notification policy and scheduling must agree
after crash recovery, with no false “active” receipt from a previous process.

Do not overwrite the administrator's original `topology.json`. Store immutable
candidate files and an active selection under the managed data area; retain a
bounded number of known-good versions. Recovery only selects a recorded committed
version or completes/undoes an existing recorded transaction. It never scans a
directory and executes whichever file has the largest number.

### 8.3 Changes to measurement contracts

Classify changes with existing contract functions, not a hand-maintained list of
field names. Names, notes and scheduling can preserve contracts; target/route and
some assertion changes do not. The unchanged topology schema number does not
imply unchanged contract or parser support.

For unchanged contracts, collectors and workers may activate in bounded batches.
For changed contracts, the first release uses a strict coordinated transition:

1. Require all collectors receiving affected observations and all affected workers
   to be prepared and reachable. Default to postponing if any required participant
   is missing; do not pretend partial reachability is an atomic rollout.
2. Pause affected scheduling, retain delivery, and drain relevant old-contract
   outbox records to every required recipient. Initially pausing the whole selected
   worker is acceptable if the reported monitoring gap is explicit.
3. Apply collector configurations, verify their active generations, then activate
   the workers and resume checks. Preserve sample timestamps and original contracts.
4. On drain timeout before collector changes, abort and resume old scheduling.
   After any participant switches, report partial activation and reconcile or
   issue a higher-generation rollback. Do not report global success or automatically
   clear queues to force progress.

A signed `config.quiesce` command binds the prepared generation and an explicit
pause lease. Propose a 180-second lease with a local 10-minute maximum, measured
with a monotonic clock once accepted; duplicates do not extend it. Only this
configuration transition may use the pause, and a drained receipt identifies the
required recipients and remaining old-contract records. It is not a general
remote process-stop facility.

Pause leases must be bounded so a dead coordinator cannot pause a worker forever.
If a lease expires during a partial rollout, the node resumes its locally
committed configuration and reports the inconsistency; some recipients may then
refuse its old contracts. Preserve those records under existing bounded outbox/
quarantine rules and report the consequence. Global losslessness is not promised.
Known transition errors may be made retryable only for explicitly recorded,
bounded deployment windows; arbitrary contract mismatches remain errors.

An explicit operator option may permit a partial/offline rollout after showing
which checks and queued evidence can become incompatible. It must never be the
default or silently weaken node policy. Offline nodes receive the newest authorized
desired state when they reconnect; stale intermediate activation commands expire.

Supporting delayed old-contract evidence without rejection requires a separate
design for versioned admission, history, deduplication, external cursors, quotas,
health and alerts. Do not accept multiple contracts by merely bypassing today's
`contract_mismatch` check. Rollback also needs to preserve state whose contract
became obsolete; reverting a file does not recreate pruned evidence.

## 9. Local software update policy

`updates.accept=true` permits deployment only within a locally reviewed policy.
The controller cannot lower the security floor, authorize downgrade, change
publisher keys, or change the registered installation type.

| Setting | Proposed default or rule |
| --- | --- |
| Repository | Exactly `WhitenWhiten/topophany`, pinned with its numeric repository and owner identities in publisher trust |
| Release channel | Stable only; no setting to enable prerelease/nightly through this executor |
| Target version | Exact `vMAJOR.MINOR.PATCH`; no aliases, ranges, branch names, commit-only downloads, or build metadata in the first release |
| Immutable release | Required for remotely managed versions |
| Project release signature | Required; unknown keys and missing metadata fail closed |
| Security floor | Maximum of local minimum, current authenticated publisher floor, and replay/downgrade constraints |
| Major version changes | Denied until explicitly allowed locally; same-major updates still require compatibility checks |
| Remote downgrade | Denied; separate bounded transaction recovery is described below |
| Installation type | Locally detected/registered; only `managed_binary` supported by the first helper |
| Maintenance window | Must be selected locally before enabling updates; an explicit `any_time` choice is supported, never inferred |
| Parallel transactions | One per registered binary/service group |
| Local health timeout | Proposed 120 seconds, configurable from 30 to 600 seconds |
| Automatic recovery | Enabled for an explicitly authorized previous version with proven compatible state |
| Candidate/backup retention | At least active and permitted previous version; bounded by local disk budget; do not remove transaction recovery material |
| GitHub access failure | Defer update, retain current version; never use an unverified mirror or controller-supplied executable |

The maintenance window has an explicit IANA time zone and a documented daylight
saving rule: ambiguous repeated periods use the narrower permitted interval and
missing periods do not create a new window. Staging may occur outside the window,
but service interruption may not. Recovery of an already interrupted installation
continues outside the window. New tasks never silently inherit a permission from
an older task's scheduled time.

Parse version components numerically with bounded integers, not lexicographic
string comparison. Record the highest successfully activated software version
without lowering it during automatic recovery. Repeatedly requesting the current
version is an idempotent no-op only when the installed digest matches; otherwise
report a local integrity problem rather than silently replacing bytes.

Unmanaged binaries, development builds, package-manager installations and
containers report `unsupported_installation` or a specific manual-action state.
Do not overwrite a package manager's files, mount a container host socket, install
Docker/system packages, or turn an image update into an in-container self-update.

## 10. Resolve and authenticate an official release

### 10.1 Exact release resolution

Both the controller's planning step and the node/helper's verification step enforce
the same rules. Controller verification is useful feedback, never a substitute
for verification at the installation boundary.

1. Start from the locally pinned repository identity and constructed GitHub API
   endpoints. Confirm numeric identity as well as owner/name. Refuse repository
   transfers/recreation/renames until trusted policy explicitly handles the change.
2. Fetch the exact tag's Release object. Require `draft=false`,
   `prerelease=false`, a valid `published_at`, `immutable=true`, and exact stable
   tag syntax. A Git tag without a Release is not eligible.
3. Pin release ID, resolved source commit, asset ID, exact name, platform, length
   and SHA-256. Peel annotated tags with a depth limit. `target_commitish` alone
   may be a branch and is not sufficient proof of the source commit.
4. Construct the expected asset name from the version and locally detected target:
   `topophany_<version>_<os>_<arch>.tar.gz` on Linux/macOS or `.zip` on Windows.
   Require one matching uploaded regular artifact. Do not use source archives,
   Actions artifacts, a URL supplied by the operator, or `latest` resolution.
5. Fetch signed release metadata and current security metadata from the pinned
   publisher locations. Validate trust, signatures, expiry, replay floors,
   compatibility and revocations before executing any candidate code.
6. Stream-download the exact GitHub asset to bounded private staging; verify size,
   archive digest, allowed contents, and extracted executable digest. Reject
   missing digests rather than silently reducing assurance.
7. Before activation, obtain a fresh authenticated Release state and security
   catalog, proposed maximum age 5 minutes for the Release lookup. Require the
   same pinned identity and assets; a changed pre-release designation or deleted
   release stops a new install. Do not depend on mutable release notes for policy.

The release signature manifest can bind a release ID by creating the draft first;
the final published immutable release must contain those exact artifacts and
metadata. The manifest must not claim to contain its own digest, avoiding a
self-reference. It describes executable archives and their extracted binaries.

GitHub allows published immutable releases to have their title/notes and
pre-release/latest designation changed. Immutability protects the associated tag
and assets, and does not replace checking stable eligibility at activation.
See [immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases)
and the [Release API](https://docs.github.com/en/rest/releases/releases#get-a-release-by-tag-name).

### 10.2 Dedicated download transport

The normal collector transport refuses redirects and carries collector credentials.
Keep it unchanged. Release downloads use a separate client with no observation or
management tokens, no cookies, no ambient environment proxy and no TLS bypass.

GitHub's asset API can return either bytes with `200` or a `302` download redirect.
Allow only HTTPS redirects originating from validated GitHub release asset
responses and terminating at an explicitly maintained set of exact GitHub asset
delivery hosts. Bound redirect count, headers, total time and total bytes; reject
userinfo, nonstandard ports unless locally specified, IP-literal redirects, and
unexpected hosts. Validate public destination IPs during each connection to avoid
SSRF through DNS rebinding. Do not use a broad `*.githubusercontent.com` allowlist.
The initial host set and observed redirect chains need acceptance evidence before
release; a future CDN change fails closed until trusted software/policy updates it.
See [release asset downloads](https://docs.github.com/en/rest/releases/assets#get-a-release-asset).

Public releases should not require node-held GitHub credentials. If rate limits
require optional credentials, use a separate read-only GitHub credential, attach
it only to its exact API origin, and strip it before every cross-origin redirect.
Use conditional metadata requests, bounded backoff and jitter. Do not poll GitHub
for every heartbeat; resolve only when preparing or activating an update and when
refreshing an opted-in update policy catalog. Respect API retry responses.

Nodes in the first release download executable artifacts directly from GitHub.
The controller does not proxy executable bytes. Nodes unable to reach GitHub remain pending or
require a separate reviewed local installation; no mirror fallback is implied.

### 10.3 Archive handling

Mirror the release workflow's closed archive member list at the receiver. Require
exact expected root and binary names and allow only documented metadata files.
Reject absolute paths, `..`, duplicate/overlapping members, symlinks, hard links,
devices, executable scripts, unexpected directories, Windows drive/UNC/alternate
stream paths, case-folding collisions and reparse points. Ignore archive ownership
and privileged mode bits; set locally defined file modes.

Enforce compressed and expanded byte limits, member count, per-file limit and a
streaming decompression budget. Extract into a new protected directory with
descriptor-relative/no-follow operations. Do not execute an archive entry to ask
its version, and do not trust its extension or content type as format validation.
The helper rehashes the exact file descriptor/bytes it will install after all
verification and before replacement, preventing a staging swap after validation.

## 11. Publisher trust and signing

### 11.1 Runtime trust choice

The first release keeps the current no-external-Go-module runtime model by using standard
library Ed25519 to verify project release manifests and a small explicitly
versioned security catalog. This is a constrained project-specific protocol, not
an implementation of TUF. Its parser, key lifecycle, expiry and recovery behavior
require security review before unattended updates ship.

If that review cannot establish the required guarantees within a maintainable
design, adopt a maintained TUF implementation and document the dependency change.
Do not drop signature, freshness or revocation checks merely to preserve a zero
dependency count. TUF's separation of root/targets/snapshot/timestamp roles and
rollback/freeze defenses are a useful review reference:
[TUF security](https://theupdateframework.io/docs/security/) and
[specification](https://theupdateframework.github.io/specification/latest/).

The minimum runtime verification chain is:

`local publisher root → authorized release key → signed manifest → exact archive and binary hashes`

In parallel, require:

`local controller trust → authorized update command → exact installation, release and transaction`

Neither chain supplies the authority of the other. A valid publisher signature
does not grant deployment permission; a valid controller signature does not make
an executable official.

### 11.2 Manifest and security catalog

A signed release manifest binds schema, official repository identity, release ID,
stable version, resolved commit, artifact names/IDs, platforms, compressed sizes,
archive SHA-256 and binary SHA-256. It also describes supported management
protocols/features, supported configuration capabilities, state read/write
compatibility, minimum compatible peer versions/capabilities and whether a
specific upgrade path allows automatic recovery. Schema numbers alone are not a
sufficient compatibility declaration.

A separate signed security catalog binds a monotonically increasing catalog
version, repository identity, issued/expiry times, revoked artifact digests/keys,
minimum allowed versions and supported trust metadata versions. The proposed
publisher paths are `update-metadata/security.json` and
`update-metadata/roots/<version>.json` on protected `main` in the pinned official
repository, read through the GitHub contents API with an explicit ref. These paths
are part of installed publisher trust, not deployment-command parameters. Mutable
signed metadata may be read there; executable bytes must still come only from
exact eligible Releases. A controller cannot substitute a different metadata URL.

Historical release signatures authenticate their bytes without requiring old
releases to be republished. Authorization to newly install them requires a
current, unexpired catalog and local policy. Proposed catalog freshness limit is
24 hours. An expired/missing catalog blocks new installs, not the running binary.
Persist the highest accepted catalog/root versions; never erase those floors
during binary rollback. A network attacker may deny updates, but cannot turn
stale metadata into indefinite authorization for new installs. Invalid clock
state also blocks new installs.

Use distinct signing domains for control commands, release manifests, security
catalogs and trust-root transitions. Apply exact-byte signature verification,
strict parsing and distinct-key threshold counting as in section 7. Never fetch
an unknown public key based on a field in an untrusted envelope.

### 11.3 Key custody and recovery

Keep publisher keys outside user deployment controllers. Use a protected signer
or explicit offline signing approval, with separation from untrusted build jobs.
The online controller may hold a scoped deployment-signing key; compromise of
that controller therefore remains able to issue commands within node policy.
Signing with a controller-held key is not protection against controller compromise.

Publisher trust roots support distinct key roles and a threshold. Recommend an
offline 2-of-3 root with independently held recovery keys; release and catalog
signers receive narrower delegated roles. Root rotation must be signed by the
old root threshold and new root threshold, increment its version, preserve the
chain for nodes that missed rotations, and enforce bounded retrieval. Root
metadata includes role thresholds, a version and an expiry. An expired root may
only be used to verify its sequentially authenticated replacements, not to
authorize a new install; the final root and catalog must be current. A revoked
key cannot authorize its own replacement. Loss of the necessary trusted keys
requires explicit local trust repair; do not offer an unsigned recovery URL.

Controller deployment-key rotation follows a locally trusted, versioned transition
without changing acceptance flags or broadening scopes. Publisher-root replacement
or controller-authority replacement cannot be smuggled inside a topology or update
command. Emergency local repair records its operator, fingerprints and reason.

### 11.4 Release pipeline changes

Retain current action SHA pinning, isolated test/build/publish jobs, archive
validation and source-tag verification. Add these gates:

1. Require stable release commits and workflow definitions to satisfy protected
   branch/tag rules and required reviews, including workflow/CODEOWNERS changes.
   Verify actual repository environment protections; a YAML `environment: release`
   declaration alone does not prove approval is enforced.
2. Build tested artifacts with read-only source access and a pinned toolchain;
   generate artifact provenance for the exact archives. Restrict OIDC and
   attestation-write permissions to the job that needs them.
3. Verify provenance against the official repository identity, approved signer
   workflow, expected source ref/commit, GitHub OIDC issuer and artifact digest.
   A claim's arbitrary predicate fields do not substitute for authenticated
   certificate identity. Reusable workflows require checking their signer identity.
4. Create a draft release, upload the verified archives, and resolve their assigned
   asset IDs. Produce/sign the release manifest using reviewed artifact metadata
   and those allocated release/asset identities. The protected signing step must
   never execute the downloaded candidate.
5. Upload checksums, signed manifests and provenance, verify all archive and
   metadata membership/digests, then publish with immutability enabled. Refuse
   incomplete releases. A replacement build needs a new version, not overwritten artifacts.
6. Publish/update the signed security catalog and keep historical trust transitions
   available. A release without required verification metadata remains ineligible
   for remote installation even if GitHub marks it stable.

Operate a catalog refresh process before its 24-hour expiry. Its narrowly
delegated online signer can refresh permitted security metadata but cannot sign
new executable manifests or replace the offline root. Failure to refresh defers
new installations; clients must never extend publisher expiry themselves. This
operational obligation is part of shipping unattended updates, not a node setting
that the controller may disable.

Build provenance is a required publication gate. In the first release, the node
verifies the project signature and GitHub release identity; it does not claim native Sigstore
verification until a suitable verifier is implemented and tested. An optional
administrator-supplied verifier may add checks but cannot replace mandatory
project verification or run arbitrary remotely selected commands. See
[GitHub artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations)
and [attestation verification policy](https://cli.github.com/manual/gh_attestation_verify).

The researched v1.6.0 Release was stable but `immutable=false`, and the reviewed
workflow contains no project release signatures. These are baseline facts, not
permission to weaken the new executor for legacy releases. Start automatic
management at a new signed, immutable release and use a trusted local upgrade to
reach that baseline. See the [v1.6.0 Release API](https://api.github.com/repos/WhitenWhiten/topophany/releases/tags/v1.6.0).

## 12. Local update executor

### 12.1 Privilege separation

The helper may be a restricted subcommand of the same project, installed as a
separate registered executable/process or fixed one-shot system service. It must
outlive the worker/collector it replaces, including service-manager kill groups.
It must not depend on the new candidate to perform failed-update recovery.

Its local registration fixes the installation ID, allowed binary destination,
service IDs, service identity, data paths, supported installation type and trust
policy. IPC uses a private Unix socket with peer-credential checks or a Windows
named pipe with explicit ACLs. Requests carry a bounded signed transaction, not
shell strings, arbitrary paths, service names or installer bundle manifests.

The helper independently verifies local acceptance, current acceptance session,
command sequence, issuer, target identity, release signatures, catalog policy and
exact artifact digest. A compromised worker cannot gain root by setting
`verified=true`, swapping a staging pathname or generating a fake rollback record.
The helper's trust/replay journal and staged bytes are administrator-owned.

Use absolute registered service-manager executable paths with fixed arguments,
a minimal environment and no shell. A one-shot helper must copy verified inputs
to protected storage before privileged work; a persistent helper must additionally
limit IPC requests, idle resources and concurrent sessions. Never install a
generic passwordless sudo rule for `topo`, `systemctl`, or a shell.

Ordinary remote updates replace only the registered application executable.
Service unit changes, capabilities, users, environment files, firewall rules,
trust roots and helper policy need separate local installation steps. Updating
the helper itself is deferred to local administrator action in the first release, with a
minimum helper protocol version advertised to the controller. Application updates
requiring a newer helper return `helper_upgrade_required`.

### 12.2 Transaction and health checks

Use `planned → downloading → verified → staged → activating → checking → healthy`,
with `deferred`, `rejected`, `canceled`, `failed`, `rolled_back`, and
`manual_recovery_required` as explicit outcomes.

1. Acquire the installation transaction lock. Record old/new digests and versions,
   command identity, policy/catalog versions, exact service registration,
   configuration identity and allowed recovery target.
2. Download/verify while the service still runs. Check disk space for staging,
   old binary, recovery data and journal; reserve a safety margin. Do not evict
   observation data to make room. Run candidate validation only after authenticity
   verification, under the unprivileged service identity with bounded execution.
3. Confirm local maintenance window, current permission, fresh official release/
   catalog state, peer compatibility and unchanged configuration. If any preflight
   input changed, defer/revalidate rather than relying on the earlier receipt.
4. Drain or safely persist pending work according to the compatibility plan. Stop
   services gracefully, ensure writer locks are released, create a consistent
   data snapshot where required, and rehearse candidate recovery on a disposable
   copy. A live directory copy is not a valid consistent backup.
5. Persist replacement intent and the protected previous executable. Stage on the
   destination filesystem and use platform-appropriate atomic replacement/durable
   directory handling. Do not assume Linux rename/fsync guarantees on Windows.
6. Start the registered service as its existing unprivileged identity. The helper
   supervises locally using a monotonic timeout and transaction nonce. Verify
   running executable identity/version, fresh process/boot identity, config,
   writable/recovered state and a functioning scheduler/API. `/healthz` alone and
   a stale `local-runtime.json` are insufficient.
7. Persist local health success before reporting it. Keep local success separate
   from controller receipt and observed delivery health. An unreachable controller
   alone does not force rollback of a locally healthy binary; record
   `healthy_pending_report`. A real ingestion incompatibility pauses the fleet
   rollout and follows its tested recovery policy.
8. On failure after replacement, independently restore the authorized previous
   executable only if state compatibility permits it. Otherwise stop safely,
   preserve data, report `manual_recovery_required`, and retain useful local
   diagnostics. Do not loop endlessly between two versions.

Crash recovery examines the durable transaction stage and actual registered-file
digest. Unknown bytes, ambiguous state or missing recovery material cause an
explicit local recovery state, never execution of an arbitrary file found in a
cache. Cache garbage collection must preserve all active transaction references.

### 12.3 Platform requirements

| Platform | Required integration and evidence |
| --- | --- |
| Linux | Separate helper/systemd unit, fixed service identity, protected directories, tested stop/restart/recovery; retain hardening of worker/collector |
| macOS | Independent launchd job/process, native replacement/permission behavior, installation-specific signing/quarantine behavior; actual native tests |
| Windows | Independent helper handling occupied executable files, restricted named pipe/DACLs, task stop/start matching the current scheduled-task installer, native interrupted-update tests |
| User installation | No privilege escalation; writable paths and same-user threat assumptions explicitly disclosed; unsupported until its lifecycle is tested |
| Containers/packages | Report external ownership and desired version; no automatic in-place mutation by this helper |

Cross-compilation proves compilation, not service-manager, ACL, crash-recovery or
power-loss behavior. Native acceptance is required before advertising that
platform's `managed-update` capability.

## 13. Compatibility and rollback

### 13.1 Software compatibility

The controller plans with a matrix of control protocol, topology features,
observation/snapshot formats, external report fields, persisted state readers/
writers and helper versions. The node checks its own prerequisites again.
Version strings alone and topology schema 1 alone are insufficient. For example,
1.6.0 external reports add fields that 1.5.4 collectors reject despite unchanged
batch schema numbering; see [the release compatibility notes](releases/v1.6.0.md).

Default rollout order is receiving collectors first, compatible viewers as needed,
then worker canaries and remaining workers. Only then publish configuration that
uses new features. All recipients of a dual-collector worker must be compatible
before that worker changes its report format. A controller upgrading itself must
persist its work and use an independent helper; its absence does not transfer
management leadership or invalidate already recorded local recovery authority.

Existing pending outbox records must survive a binary-only update when the
candidate can read and deliver them. Do not reuse the current installer's
empty-outbox requirement by deleting or blindly archiving queued evidence. If
compatibility cannot be proved, postpone and ask the operator to use the existing
explicit drain/archive workflow. Outbox identity, sequence and sampled time must
remain unchanged. Collector receipts and retention floors must also survive.

### 13.2 Recovery versus downgrade

An automatic recovery authorization binds exactly one transaction's previous and
candidate artifact digests, data/config identities, failure deadline and permitted
recovery conditions. It does not permit installation of any lower GitHub version.
The previous binary must already be authenticated and permitted by the local
security floor. Reject preparation if no acceptable recovery target exists and
the chosen update mode requires automatic recovery.

Keep the highest successful software version, command sequences, catalog versions
and trust-root versions outside rollback snapshots. A recovery must not reset
anti-replay protection. An older application unable to preserve these records is
not a supported automatic recovery target. Expired activation authority does not
prevent completing the bounded local recovery already authorized for a started
transaction; it cannot authorize a fresh install later.

### 13.3 Data safety

Automatic recovery is allowed only for tested paths where the previous binary
can read every state the candidate may have durably written, or where an isolated
trial cannot have acknowledged new data and can safely restore its snapshot.
The initial executor supports proven backward-compatible paths; irreversible
migrations require an explicit local maintenance workflow.

Restoring a pre-upgrade data snapshot after the candidate acknowledged new
observations would lose acknowledged data and can violate deduplication. Therefore
it is not an automatic fallback. Preserve the failed candidate's data for repair
or forward recovery. A future isolated trial mode must block external ingestion,
delivery and irreversible writes until promotion; a sleep-based “health window”
does not create that guarantee.

Rehearse backups with the existing `topo recovery` command against candidate and,
where relevant, previous binaries on stopped private copies. Validate project,
configuration, credentials where applicable, quotas, pending count and quarantine
changes. One successful pre-upgrade recovery does not prove that the previous
binary can read data subsequently produced by the candidate; version-pair tests
must establish that property. Failure to obtain a consistent backup or sufficient
disk budget stops before service replacement.

## 14. Fleet rollout and user experience

An update/configuration proposal pins a snapshot of installation IDs. Labels can
help select targets during planning, but later label changes or newly enrolled
nodes cannot expand an approved batch. Expanding scope creates a new reviewed
plan. A single node must be useful without creating a fictional canary group.

Proposed rollout defaults are one worker canary, one concurrent update, a
five-minute observation period, and pause on the first failed activation or
recovery. Larger batches and success criteria are operator-configurable within
local node limits. The controller shows disabled, unsupported, offline and stale
nodes separately from prepared/active nodes; exclusions require an explicit
operator choice and remain in the report.

Before activation, show the target version/config generation, exact pinned
artifact or config digest, affected installations, new network/secret grants
requested, contract changes, expected interruption, incompatible peers, required
helper versions, current local-policy refusals, and available recovery path.
Insufficient information means pending or rejected, never “probably compatible.”

Proposed operator commands are a separate management command group, for example
`topo management config plan|publish`, `update plan|publish`,
`deployment status|cancel`, and `nodes`. All are future interfaces. Preserve
existing `topo rollout` viewer-only semantics; richer private deployment reporting
must require management authorization rather than quietly elevating that command.

Required node status fields include installation/role/observer, supported
capabilities, software and helper version, actual acceptance flags when recently
reported, local policy revision, desired/prepared/active generation, transaction
phase, boot identity, last successful contact, receipt timestamp/expiry and a
bounded error code. Exact private config hashes, paths, secret references and
release policy details stay on appropriately scoped management reads.

Use specific errors such as `remote_config_disabled`, `remote_updates_disabled`,
`policy_denied`, `secret_binding_denied`, `stale_command`, `generation_conflict`,
`unsupported_capability`, `release_not_stable`, `release_not_immutable`,
`publisher_signature_invalid`, `security_metadata_expired`,
`downgrade_denied`, `helper_upgrade_required`, `insufficient_disk`,
`incompatible_state`, and `manual_recovery_required`. A disabled node is an
expected policy outcome, not a monitor failure or target outage. CLI output must
explain the local action needed without including credentials or raw peer errors.

Cancellation prevents new work. It cannot recall already executed probes or undo
an in-progress replacement without completing local recovery. Update and config
tasks serialize at the installation boundary; bulk scheduling does not grant
permission to bypass it. Retried status delivery does not repeat the operation.

An optional two-person activation policy can require independent operator
approvals over the fixed plan digest. For protection against controller compromise,
those approvals need independently held signing credentials and node/helper
verification. Two approval rows in a database controlled by the same compromised
controller provide an operational workflow, not an independent security boundary.

## 15. Resource limits and audit records

Initial limits below are design targets to measure, not current performance
claims. Smaller local limits win; remote commands cannot raise them.

| Resource | Initial limit |
| --- | --- |
| Signed command payload / envelope | 32 KiB / 64 KiB, at most 4 distinct signatures |
| Configuration blob | 4 MiB, plus existing JSON nesting and model budgets |
| Release manifest / trust-root document | 128 KiB / 64 KiB |
| Security catalog / GitHub metadata response | 256 KiB / 1 MiB |
| Status receipt | 16 KiB; closed fields and bounded text |
| Prepared configs per installation | 2 candidates plus active and previous retained config |
| Concurrent candidate downloads | 1 per installation; controller rollout throttles fleet concurrency |
| Archive compressed / total expanded / one member | 128 MiB / 256 MiB / 128 MiB; at most 16 members |
| Download total deadline | 10 minutes with bounded idle and TLS/header timeouts |
| Per-node management journal | 32 MiB; bounded receipt retention plus permanent enrollment replay floors |
| Controller management journal | 256 MiB initial budget; explicit backpressure and safe compaction |
| Recovery copy | Existing 1 GiB default and explicit bounded override, with disk reservation |

Do not load an entire archive in memory. Candidate config allocations, hashing,
signature checks and journals count toward memory/CPU budgets. Measure the worker
under its existing 64 MiB memory target and collector under its existing 128 MiB
target; decline a plan or document an explicit local budget increase if necessary.
The helper has its own bounded lifetime and memory limit. Large downloads and
management floods must not consume ingestion's reserved connections/worker slots.

If audit persistence cannot succeed, reject the new mutating action. Retention
must not erase active transactions, trusted-version floors or the evidence needed
for local recovery. When an old terminal receipt is compacted, a retry returns a
consumed/expired-result status, not a chance to execute the same command again.

Audit publication, approval, preparation, activation, cancellation, recovery,
local permission changes, trust rotation and policy refusals. Record principal
identity, project/installation, command/deployment IDs, old/new versions or
generations, authenticated digests, policy versions, timestamps and outcome.
Never record token values, environment secrets, raw credential-bearing configs,
signed download URL query strings, or unbounded server errors. Preserve current
terminal-control and Unicode filtering in all user-visible output.

Controller and local helper logs are complementary; a controller cannot provide
independent proof of its own honesty. Local administrator compromise can alter
local logs. An optional external append-only audit destination may provide stronger
evidence, but is not required or silently configured by this feature.

## 16. Threat model and residual risks

| Threat | Required defense | Remaining limit |
| --- | --- | --- |
| MITM or wrong controller | Verified HTTPS, pinned authority identity, signed commands, no redirect/token forwarding | An attacker may deny connectivity |
| Stolen viewer/worker token | Separate management credentials/scopes/listener | Observation spoofing remains possible within today's worker scope |
| Stolen management-agent token | Exact installation scope; no publication; helper independently verifies authority | Attacker can read that installation's assigned config and forge agent status; receipts are not hardware attestation |
| Compromised controller | Independent local gates and constraints; publisher trust; exact release selection; local version floor | May disrupt authorized probes, disable allowed checks or deploy eligible official releases within policy |
| Remote secret exfiltration | Secret-to-destination binding, execution-time network policy, existing redaction | Authorized endpoints receiving secrets remain trusted; timing/availability channels are not eliminated |
| Malicious archive or staging swap | Signature/digest, bounded extraction, trusted paths and exact installed-byte verification | A publisher-approved malicious binary is still trusted software |
| Replayed command or old backup | Durable sequences, session/epoch binding, expected base state, trusted-time/catalog floors | Local administrator can replace trusted state; recovery must be explicit |
| Downgrade/freeze | Exact versions, highest-successful floor, fresh signed catalog, bounded recovery pair | Network denial can prevent discovering or installing updates; running old software is not stopped automatically |
| Release or repository tampering | Numeric repository identity, immutable assets, independent project signature, protected publication gates | Compromise of sufficient publisher/root keys can authorize malicious releases |
| Compromised worker process | Unprivileged service, protected helper IPC/state, independent verification, fixed file/service scope | Worker may forge its own measurements; same-user unmanaged installations have weaker isolation |
| Power failure or disk full | Durable stage journal, atomic local operations, preserved old executable, independent recovery | Filesystem/platform durability must be tested; no universal power-loss guarantee |
| Incompatible migration | Version-pair state tests, consistent backup, refusal of unsupported auto-recovery paths | Manual/forward recovery may be required; acknowledged data cannot be silently discarded |

Do not market stable GitHub publication, signatures, or provenance as proof that
software contains no vulnerability. The controls authenticate origin, authorize
deployment, limit privileges and make failures recoverable within stated bounds.
They do not make a malicious trusted publisher or local root harmless.

## 17. Implementation sequence and code ownership

Every phase is pending implementation. This document does not authorize changing
release settings or enrolling any real installation as part of documentation work.

| Phase | Deliverables | Exit gate |
| --- | --- | --- |
| 0. Contracts and fixtures | Freeze local policy schema, closed command types, signing domains, metadata paths, compatibility semantics and error catalog; design security review | Golden protocol/policy fixtures and reviewed ownership/trust boundaries |
| 1. Local gates and enrollment | Independent default-off flags, scoped identities, explicit local enrollment/disable, private state and status-only mode | Default/off/reenable/revocation tests; existing viewer and worker privilege regressions pass |
| 2. Dynamic configuration | Controller journal, polling, signed prepare/activate, observer projection, local policy, transactional reload, contract-aware rollout | Configuration safety matrix and crash tests; old clients still work |
| 3. Publisher trust | Protected signing/custody, project manifests, security catalog, root rotation, provenance gate and immutable release workflow | Tamper/replay/rotation vectors plus a complete eligible test release; no unsigned fallback |
| 4. Linux local updater | Protected helper, registered systemd lifecycle, bounded download/extraction, binary transaction, data compatibility and recovery | Native failure injection and resource measurements; helper attack tests |
| 5. Fleet updates | Version/capability inventory, exact-target planning, canary/batches, pause/cancel, independent controller self-update | Dual-collector mixed-version and coordinator-failure acceptance |
| 6. Other platforms | macOS launchd and Windows scheduled-task/ACL integration, platform documentation | Separate native acceptance, including interrupted replacement and recovery |

Suggested code boundaries, to confirm in phase 0:

- `internal/management`: typed protocol, authorization, controller state, node
  polling, receipts and deployment orchestration; no direct privileged installation.
- `internal/management/policy`: strict local permission/constraint model and
  execution-time capability decisions; no remote writes to policy.
- `internal/update`: exact release resolution, metadata verification, compatibility,
  bounded staging and the transaction model.
- `internal/update/helper`: local IPC, installation registry, privilege boundary,
  service-manager adapters and protected recovery journal.
- `internal/app`: explicit command/startup wiring, shared transactional reload,
  local policy control and management CLI. Reuse `prepareScheduler` and runtime
  reporting instead of implementing parallel probe execution.
- `internal/config`, `runner`, `probe`: projection/contract validation and mandatory
  execution-time constraints without altering existing default local semantics.
- `internal/bootstrap`, `secure`: reviewed installation/path/locking primitives;
  do not expose the general installer as a remote privileged API.
- `.github/workflows/release.yml` and release scripts: build provenance, signature
  gates, draft-to-immutable publication and signed compatibility metadata.

Update `SECURITY.md`, `OPERATIONS.md`, `REFERENCE.md`, deployment templates,
localized CLI help, README descriptions and upgrade notes when behavior actually
ships. Update `CONTRIBUTING.md` to describe the new isolated management subsystem
while retaining its rule against elevating viewer/worker roles. Keep default
local-only operation and the zero-dependency claim accurate throughout the work.

## 18. Required acceptance matrix

Use temporary files, loopback test servers, synthetic credentials and test signing
keys. No test changes real service registration, accesses production credentials,
or publishes to the official repository without a separate explicit acceptance
environment. Public GitHub integration checks are read-only. Native destructive
failure tests use disposable VMs/installations and report their platform scope.

| ID | Scenario | Required result |
| --- | --- | --- |
| G01 | Fresh install, missing policy, omitted flags, older bundle upgrade | Both acceptance flags false; no unsolicited enrollment or management traffic |
| G02 | All four flag combinations, including update-only | Only explicitly enabled capability runs; no implicit config acceptance for update-only |
| G03 | Remote topology/command tries to enable flags or replace trust | Strict rejection; local files unchanged |
| G04 | Invalid policy, unknown fields, duplicate keys, bad ownership/symlink | Fail closed for new actions; valid committed monitoring continues with diagnostic |
| G05 | Disable before prepare, after prepare and immediately before commit | No new activation; canceled candidate does not resume after restart |
| G06 | Disable during binary replacement | Helper finishes bounded recovery; no later task starts; actual outcome reported |
| G07 | Reenable after disable; resend previous session command | Old session rejected; fresh authorization required |
| G08 | Status-only opt-in and subsequent disable | Only bounded status exchange; no command execution; controller eventually marks stale |
| A01 | Viewer/worker token against every management route | Denied, including blob download, publication and status writes |
| A02 | Agent crosses project/installation/observer or requests unassigned digest | Denied without private-data leak |
| A03 | Enrollment token reuse/expiry, credential rotation/revocation | Scoped one-time enrollment; no grants; no broadened identity |
| A04 | Browser Origin/preflight, missing Origin, plaintext and invalid TLS | Authorization always required; no management CORS or transport bypass |
| P01 | Bad signature, wrong domain, key ID, audience, role or controller | Rejected before any privileged or probe action |
| P02 | Duplicate, out-of-order, conflicting or oversized command | Idempotent known result or explicit rejection; sequence floors survive restart/compaction |
| P03 | Expired/future command, backward/invalid clock, restored stale journal | New activation blocked; no counter/trust downgrade |
| P04 | Second collector offers newer command or authority epoch | No management failover based on delivery reachability |
| P05 | Crash/disk full at each intent, stage, pointer and receipt write | Recoverable old/new state; no false success or half-applied active generation |
| C01 | Invalid config, removed self observer, project change or missing capability | Old scheduler remains/restarts; refusal names the reason |
| C02 | Observer projection with native/external checks and dependency graphs | Strictly valid slice; exact per-check contract equivalence; no unrelated secret references |
| C03 | Local secret rebound to attacker host/path/proxy or different TLS identity | Denied at prepare and execution boundaries |
| C04 | DNS rebinding, mapped IPv6, `resolve_ip`, proxy-side DNS and CA path tricks | No connection or local read outside policy; unenforceable guarantees refused |
| C05 | Changed policy, absent secrets, new privileged check, upgraded unknown adapter | No broadened authority; explicit error; no service privilege changes |
| C06 | Contract-preserving changes | Existing measurements/history remain countable under existing semantics |
| C07 | Contract-changing rollout with pending records and dual collectors | Drain/prepared barrier; original times/contracts retained; no implicit queue deletion |
| C08 | Offline participant, partial activation, coordinator loss and pause expiry | Bounded pause; truthful partial/outdated status; documented evidence consequences |
| C09 | Local reload races with remote activation or software update | Single serialized authority; no split disk/runtime state |
| C10 | Config activation or backend reload fails after old scheduler stops | Old scheduler restored or explicit safe recovery; no stale active receipt |
| U01 | Draft/prerelease/tag-only/branch/latest/foreign repository/source archive | Ineligible; no candidate execution |
| U02 | Stable label changes, release deleted, tag/asset/commit identity differs | Refuse new activation of the pinned plan |
| U03 | Missing/bad signature, wrong platform/size/hash, revoked key/artifact | Reject without fallback to SHA-only verification |
| U04 | Expired/replayed catalog/root, unknown key, valid/invalid root rotation | Enforce local chain/threshold and durable floors; offline blocks new installs |
| U05 | Unsigned old official release or locally built binary | Not eligible for remote update/recovery baseline |
| U06 | Redirect/token leak, lookalike CDN, DNS rebinding, API rate limit | Constrained HTTPS download, no leaked credentials; defer on unavailable source |
| U07 | Archive traversal, link/device/duplicate/member bomb, Windows path collision | Reject within byte/time budgets; no writes outside protected staging |
| U08 | Staging path swap, forged IPC/receipt/rollback record, malicious service name | Helper independently refuses; no arbitrary privileged execution/write |
| U09 | Exact current version retry and remote downgrade request | Idempotent matching digest or integrity error; downgrade denied |
| U10 | Download interrupted, disk full, restart/kill/power failure at each stage | Existing service preserved or bounded helper recovery; journal describes outcome |
| U11 | Candidate won't start, stale runtime record, fake health response | No healthy result without current transaction/process binding |
| U12 | Controller unreachable after locally successful update | Local success retained; pending receipt; no repeated update/rollback loop |
| U13 | Nonempty outbox, changed state fields, candidate-written new journal data | No discarded acknowledged evidence; incompatible auto-recovery refused |
| U14 | Recovered older binary resets replay/catalog floors | Refused or unsupported recovery pair; floors remain authoritative |
| U15 | Package/container install, old helper, helper in killed service cgroup | Unsupported/deferred as appropriate; recovery executor survives service stop |
| F01 | Dual collectors and mixed 1.5.4/1.6.0-style report compatibility | Workers wait for all required receivers; schema equality alone does not pass |
| F02 | Canary failure, changed labels, stale receipts, canceled batch | Pause future activation; fixed target scope; no false fleet success |
| F03 | Controller self-update and restart during a rollout | Durable orchestration resumes; no second leader or lost recovery authority |
| R01 | Large configs, slow GitHub, management flood, exhausted audit budget | Bounded CPU/memory/disk; observation reservations preserved; new mutations backpressured |
| R02 | Tokens/env/URLs/control characters in errors and audit inputs | No secret output or terminal injection; private revisions stay off viewer APIs |

Fuzz strict envelope/policy/manifest/archive parsing and projection invariants.
Race-test cancellation, policy changes, local reload, receipts and helper locking.
Run relevant package tests during each phase and the repository's complete
release checks before shipping, including compatibility readers, cross-builds and
native acceptance for every advertised platform. Reuse release/archive regression
tests and add version-pair recovery cases rather than tests that merely mirror
an implementation's field assignments.

For the first public management release, publish evidence for default-off behavior,
credential isolation, a verified official release upgrade, forged-release refusal,
an interrupted installation with recovery, and a contract-changing dual-collector
configuration rollout. Mark unsupported/untested paths explicitly. No release
may advertise unattended updates while mandatory trust, replay or recovery gates
remain disabled or unimplemented.

## 19. Reference material

Existing behavior is defined by [security boundaries](SECURITY.md),
[operations](OPERATIONS.md), [configuration and API reference](REFERENCE.md),
[implementation dependencies](DEPENDENCIES.md), and the source files linked above.
The proposed behavior in this plan does not replace those current contracts until
implementation and acceptance are complete.

Official external references used for the design:

- [GitHub Release API](https://docs.github.com/en/rest/releases/releases#get-a-release-by-tag-name): exact release lookup and published/draft/prerelease metadata.
- [GitHub release assets](https://docs.github.com/en/rest/releases/assets#get-a-release-asset): asset identity, digest and binary-download responses.
- [GitHub immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases): tag/asset protection, mutable designation limits and draft publication order.
- [Verifying release integrity](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/verify-release-integrity): release and local-asset attestation verification; generated source archives are excluded.
- [Artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations) and [CLI verification](https://cli.github.com/manual/gh_attestation_verify): provenance identity and verification-policy requirements.
- [TUF security](https://theupdateframework.io/docs/security/) and [specification](https://theupdateframework.github.io/specification/latest/): update metadata trust separation, expiry, rollback/freeze threats and key rotation reference.
