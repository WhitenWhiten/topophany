# Opt-in remote configuration and software updates

Topophany can deliver reviewed configuration and request an exact official
software version through a separate HTTPS control plane. **Configuration
acceptance and update acceptance are independent and default to `false`.**
Enrollment does not enable either capability. Existing observation worker tokens
and viewer tokens have no management authority.

This is the operator guide for the implementation in this source tree, following
1.6.0. The [design and acceptance plan](REMOTE-MANAGEMENT-PLAN.md) records the
security requirements and remaining release gates. Do not infer that an older
published binary accepts these commands.

## Supported installations

| Capability | Support |
| --- | --- |
| Configuration controller and node client | Linux and macOS, with checked POSIX ownership and local policy |
| Managed binary replacement | Linux/systemd, dedicated executable, unprivileged monitoring service, separately installed root helper |
| Windows management | Refused pending native ACL/enrollment acceptance; existing local monitoring continues to work |
| Containers, package-managed installations, shared executables | Use their external deployment system; unsupported by the update helper |
| macOS/Windows software replacement, interactive viewers, `topophany-bar` | Outside this executor's scope |

No management process, helper, credential or listener is installed implicitly.
Generated deployment bundles retain their existing defaults. New services need
an explicit `--management-policy` argument. A controller needs an explicit
`management serve --policy` command or `serve --control-plane-policy` argument.
The collector's observation listener never becomes a management listener.

## Authority and local files

Use a separate service identity for the controller and each monitoring
installation. For a Linux system installation, `system: true` requires policy,
constraints and trust to be administrator-owned, with trusted, non-writable
parent directories. Public policy/trust can be `0640 root:topophany` or `0644`;
the service must be able to read them. Its management bearer credential is a
regular `0600` file owned by the service account, in an administrator-controlled
directory. Signing keys and helper state remain private to their owner.

All paths in management files are absolute. Symlinks, unexpected owners,
group/world-writable policy, unknown fields, case aliases, duplicate JSON keys,
trailing data and oversized documents are refused. macOS's fixed root-owned
`/etc`, `/var` and `/tmp` aliases are the only path-alias exceptions. Filesystem
and root-administrator integrity remain assumptions of the trust model.

There are three independent authorities:

1. Local policy authorizes this installation's configuration and/or updates.
2. A pinned controller key signs scoped, expiring deployment commands.
3. A separately provisioned publisher root authenticates release bytes and the
   current security catalog. A controller key cannot sign a release.

Do not copy a controller private key or a publisher private key to workers.
Verify public trust fingerprints through an administrative channel before
installing them; downloading a replacement root from the controller is not
enrollment. The initial enrollment is a local operation with a separately
provisioned random agent credential; there is no public enrollment endpoint.

## Set up the controller

Create a private directory as the controller's service user and generate a
controller signing key. The destination key directory must not already exist:

```sh
topo management keys --dir /etc/topophany-control/keys --controller-id control-home
topo management credential --output /etc/topophany-control/config-publisher.json
topo management credential --output /etc/topophany-control/update-publisher.json
topo management credential --output /etc/topophany-control/worker-a-agent.json
```

`credential` writes the secret to the private file and prints only its SHA-256
digest and path. Deliver each agent's file privately to that installation, then
remove the delivery copy. Keep operator credentials on the operator's machine;
the server needs only their digests. Generate a different credential for each
collector agent and each operator role. The key generator's controller trust
expires after one year; arrange local trust renewal before expiry.

Create a separate `0600` management auth file. Replace the digest placeholders
with the output from `credential`; these are not usable example credentials:

```json
{
  "schema": 1,
  "tokens": [
    {"token_sha256": "<agent-token-sha256>", "role": "management-agent", "project": "home", "installation_id": "worker-a"},
    {"token_sha256": "<config-token-sha256>", "role": "config-publish", "project": "home", "installations": ["worker-a", "collector-a", "collector-b"]},
    {"token_sha256": "<update-token-sha256>", "role": "update-publish", "project": "home", "installations": ["worker-a", "collector-a", "collector-b"]}
  ]
}
```

Add a `management-agent` entry for each registered collector. Optional
`management-read` credentials can read only their explicit installation list.
Publishers also need an explicit installation list, even if they manage the
whole project. Do not put these roles in the observation `auth.json`.

Create the controller policy; provision a real TLS certificate and private key
separately. The private key must be a regular private file, not a certificate
manager's symlink:

```json
{
  "schema": 1,
  "enabled": true,
  "id": "control-home",
  "authority_epoch": 1,
  "listen": "127.0.0.1:7448",
  "tls_cert_file": "/etc/topophany-control/server.crt",
  "tls_key_file": "/etc/topophany-control/server.key",
  "auth_file": "/etc/topophany-control/auth.json",
  "signing_key_file": "/etc/topophany-control/keys/signing-key.json",
  "data_dir": "/var/lib/topophany-control",
  "inventory_complete": false,
  "observation_seconds": 300,
  "installations": [
    {"installation_id": "worker-a", "project": "home", "role": "worker", "observer": "a"},
    {"installation_id": "collector-a", "project": "home", "role": "collector"},
    {"installation_id": "collector-b", "project": "home", "role": "collector"}
  ]
}
```

Set an explicitly reachable interface and firewall policy when serving other
hosts. There is no plaintext management mode, redirect following, environment
proxy, cookie authentication or browser CORS. An optional private CA file on
the node replaces the system CA pool for that connection.

Start with `topo management serve --policy /etc/topophany-control/policy.json`.
Alternatively, the observation collector can run the same isolated listener
using `--control-plane-policy /etc/topophany-control/policy.json`. A separate
controller service is preferable when updating a collector: it avoids taking
the coordinator down with its monitoring process. Only one writer can own the
controller data directory. Auth and controller settings reload on restart.

`inventory_complete` is an administrative assertion that **every worker and
receiving collector for the project is registered**, including standby nodes.
Set it to `true` only after checking this inventory. It is required for
measurement-contract changes and software deployments. Automatic discovery
cannot prove the absence of an offline or unmanaged worker.

## Enroll an installation without enabling it

The following example assumes an existing `topophany` user, an installed worker,
and administrator-provisioned files under `/etc/topophany/management/worker-a`.
Keep the normal observation credentials separate. If future topology versions
will change measurements, review them locally before generating constraints.

```sh
sudo topo management init --policy /etc/topophany/management/worker-a/policy.json
sudo topo management constraints \
  --config /etc/topophany/worker-a/topology.json --observer a \
  --output /etc/topophany/management/worker-a/constraints.json
sudo topo management enroll \
  --policy /etc/topophany/management/worker-a/policy.json --system \
  --project home --role worker --observer a --installation worker-a \
  --controller-id control-home --server https://control.example:7448 \
  --ca-file /etc/topophany/management/worker-a/control-ca.pem \
  --credential-file /etc/topophany/management/worker-a/credential.json \
  --trust-file /etc/topophany/management/worker-a/control-trust.json \
  --constraints-file /etc/topophany/management/worker-a/constraints.json
sudo chown root:topophany /etc/topophany/management/worker-a/policy.json
sudo chmod 0640 /etc/topophany/management/worker-a/policy.json
```

Install the public controller `trust.json` as `control-trust.json`. Make the
constraints, trust and CA readable by the service and keep their parents under
administrator control. The credential itself is `0600 topophany:topophany`.
The policy CLI preserves an existing file's owner, group and mode during
atomic replacement. Keep its adjacent `.lock` file; enable/disable uses it to
serialize with a node's final authorization boundary.

Add `--management-policy /etc/topophany/management/worker-a/policy.json` to the
worker's existing service invocation and restart it locally. Both acceptance
flags remain off; it makes no management network requests. A collector is
enrolled with `--role collector`; omit `--observer` if it only collects. Generate
its constraints from the full topology without `--observer`.

For updates, include `--update-policy-file` and `--publisher-trust-file` in the
enrollment command, after provisioning the files described below. A later local
administrator may add those paths to an enrolled, disabled policy; reusing
`enroll` refuses an existing installation identity. Do not delete management
state to reset identity or replay counters.

## Configure local grants

The constraints generator authorizes exact reviewed per-check contracts and
execution bindings, including secret **references**, targets, paths, TLS and
proxy settings. It also produces an IP allowlist, required-check set, minimum
interval, check-count and concurrency budgets. The default concurrency limit
is four, matching normal worker startup. Review the generated file before
enabling; use a smaller budget or explicitly authorize a larger local limit as
appropriate.

Use `--also-configs /absolute/next.json,/absolute/alternative.json` to authorize
additional reviewed measurement variants. `--allow-disable` explicitly permits
removal/disabling of checks; it defaults to false. With no such grant, the
original required checks must remain enabled. The controller cannot broaden
these local constraints. Presentation and scheduling changes still pass the
same validation and resource limits.

Literal destinations are granted as individual IP prefixes. For reviewed DNS
names, supply explicit networks, for example `--allow-cidrs 192.0.2.0/24` for
an appropriately configured test network. Avoid a universal CIDR when a narrower
one is possible. Direct connections validate each resolved address and dial
that exact address, including normalization of IPv4-mapped IPv6. Proxy paths
require authorized literal destinations/hops; remote proxy DNS is refused
because the executor cannot enforce the resulting address. This restriction
also applies when a proxy transport would otherwise support hostnames.

External-source credential bindings are checked independently of measurement
contracts. Secret values are never sent by the controller; required environment
references must already exist locally. A managed denial is a **monitor error**,
not a measured target outage. Restrictions are checked at preparation and at
execution. A tightened valid local grant takes effect without controller access.

Each scheduler caches validation of its fixed topology against an unchanged
trusted constraints file. Execution still checks local policy and file ownership;
replacement or modification of the constraints refreshes both authorization and
dial restrictions before the next probe or source collection.

Enable only the desired capability:

```sh
sudo topo management enable --policy /etc/topophany/management/worker-a/policy.json \
  --capability config --config /etc/topophany/worker-a/topology.json
topo management inspect --policy /etc/topophany/management/worker-a/policy.json
```

`--capability updates` is separate. `--capability status` permits status-only
reporting when both command capabilities are off. Enabling either command
capability already permits the minimum polling and receipts needed for it.
Each config/update enable or disable rotates that capability's acceptance
session, invalidating queued commands from the previous session. Use these CLI
operations instead of hand-editing acceptance booleans.

## Publish and activate configuration

Query `topo management nodes --server https://control.example:7448
--credential-file /private/config-publisher.json --ca-file /private/control-ca.pem`
to inspect fresh inventory, generations, prepared receipts and actual outcomes.
`received_at` older than one minute is stale for coordination. A receipt is an
executor report, not hardware attestation. `inspect` reports local policy;
`nodes` and `topo runtime` report observed process/deployment state.

Create a private deployment request containing the complete reviewed topology:

```json
{
  "deployment_id": "home-config-001",
  "installations": ["collector-a", "collector-b", "worker-a"],
  "base_generations": {"collector-a": 0, "collector-b": 0, "worker-a": 0},
  "topology": {"schema": 1, "project": "home", "nodes": [], "observers": [], "checks": []}
}
```

Replace `topology` with the real validated document and bases with the current
`generation` from inventory. Keep request files private (`0600`); they may
contain internal addresses and secret reference names. Use a new deployment ID
for a changed proposal. The local plan command performs no network request:

```sh
topo management config plan --request-file /private/config-deployment.json
topo management config publish --request-file /private/config-deployment.json \
  --server https://control.example:7448 --credential-file /private/config-publisher.json \
  --ca-file /private/control-ca.pem
```

The controller validates and projects each worker's own topology, preserving
its measurement contracts and necessary dependency closure. Collectors receive
the full topology. Agents independently verify signed commands, digest, local
grants, secrets, identity, capability and generation before recording `prepared`.
`config plan` is a local syntax/contract preview; `prepared` is the node's actual
authorization result. A successful publication HTTP response is not activation.

For a change preserving measurement contracts, wait for all selected nodes to
report `prepared`, then send an activation request:

```json
{"deployment_id": "home-config-001", "installations": ["collector-a", "collector-b", "worker-a"]}
```

```sh
topo management deployment activate --request-file /private/activate.json \
  --server https://control.example:7448 --credential-file /private/config-publisher.json \
  --ca-file /private/control-ca.pem
```

For a **measurement-contract change**, include the complete project cohort,
including every receiving collector, and use this sequence:

1. Publish and wait for every participant to be prepared.
2. Send `management deployment quiesce` with the entire cohort in its request.
3. Wait for every node to report `quiesced`. Scheduling pauses while existing
   outbox delivery continues; a quarantine event aborts the drain.
4. Activate collectors first. Wait for their `active` receipts.
5. Activate workers, then confirm all active contracts and generations.

The quiesce lease is 180 seconds. Expiry, loss of local acceptance or restart
resumes the previously committed topology and refuses that activation. The
lease timer runs independently of network backoff. Do not treat expiry as
permission to skip a participant. Create a fresh coordinated proposal after
diagnosing a failed barrier. Contract transitions are not fleet-atomic: a
controller loss after some collectors activate can leave mixed generations.
Their ordinary contract checks remain in force; incompatible evidence is not
silently relabeled, backdated or deleted. Review every participant before retry.

`management deployment cancel` cancels an unactivated prepare/pause. Already
activated changes require a new forward deployment, or explicit local recovery.
Config and software deployments share a per-installation desired-command slot;
finish or cancel one before starting the other. Commands have 15-minute issued
lifetimes and bind activation to the exact durable preparation receipt.

Completed receipts are retransmitted without reexecuting the command or fetching
its payload, even after expiry or local session rotation, while management status
reporting is permitted. On restart, an accepted but uncommitted configuration
command is marked `failed` with `configuration_interrupted`; the durable active
topology and replay floors remain authoritative. Review that outcome and issue a
fresh deployment. After disabling and reenabling acceptance, cancellation of a
staged deployment uses the current session; activation still requires the
original preparation's session.

## Disable and return to local configuration

```sh
sudo topo management disable --policy /etc/topophany/management/worker-a/policy.json --capability config
sudo topo management disable --policy /etc/topophany/management/worker-a/policy.json --capability updates
sudo topo management disable --policy /etc/topophany/management/worker-a/policy.json --capability status
```

Disabling prevents new authorized mutations; it does not undo an already
committed configuration or interrupt a binary transaction's bounded recovery.
With all three off, management traffic stops on the next local policy check.
A prepared command cannot activate after a disable/reenable session change.

A missing/invalid policy or unavailable trust blocks new commands. Previously
committed managed configuration and its saved grants are recovered for
monitoring; removing the startup flag does not silently revert to a stale
local file. A node started in this recovery mode needs a local restart after
policy repair. Corrupt committed configuration/state is an explicit startup
error, not a fallback to unverified bytes.

While managed configuration is active, local SIGHUP topology reload is refused.
To return to a reviewed local topology, disable config acceptance, stop the
monitoring service, run the following as its data owner, then restart with the
reviewed `--config` path:

```sh
topo management revert-local --policy /etc/topophany/management/worker-a/policy.json \
  --data /var/lib/topophany/worker-a --config /etc/topophany/worker-a/topology.json
```

This retains replay/security floors and update state. Never delete state to
make an older command or version acceptable. Disabling a capability and
removing its management auth digest on the controller are separate operations;
restart the controller after changing its auth file.

## Install the Linux update helper

Managed updates need a signed, immutable release as the **initial installed
baseline**, its matching `topophany-update.json`, a locally verified public
publisher root, a fresh security catalog on GitHub and an explicit compatible
recovery pair. An unsigned historical release or a locally built development
binary is not an eligible baseline. The original 1.6.0 release cannot be made
eligible by supplying an unsigned checksum. Bootstrap a suitable signed release
locally once the maintainer has completed the publisher setup below.

The monitor remains unprivileged. Install a separate copy of `topo` as
`/opt/topophany-updater/topo`, root-owned and not writable by monitoring users.
It runs `management helper`; it is not replaced by the worker's own update.
Only a local administrator installs or upgrades this helper and its service.

Create an administrator-owned local update policy:

```json
{
  "schema": 1,
  "minimum_version": "v1.7.0",
  "max_major": 1,
  "any_time": false,
  "timezone": "UTC",
  "window_start": "02:00",
  "window_end": "04:00",
  "helper_socket": "/run/topophany-updater/worker-a/helper.sock",
  "health_seconds": 60
}
```

Versions here are illustrative, not claims that those releases exist.
`any_time: true` explicitly permits activation outside a maintenance window.
Windows crossing midnight are supported; ambiguous repeated DST minutes are
refused. Major-version upgrades and downgrades cannot be authorized by a
controller command. The highest attempted activation version remains a floor
even after automatic recovery.

Register the exact local installation in a root-owned `0600` helper file:

```json
{
  "schema": 1,
  "install_type": "managed_binary",
  "installation_id": "worker-a",
  "project": "home",
  "role": "worker",
  "observer": "a",
  "management_policy": "/etc/topophany/management/worker-a/policy.json",
  "binary": "/opt/topophany/worker-a/topo",
  "service": "topophany-worker-a.service",
  "user": "topophany",
  "data_dir": "/var/lib/topophany/worker-a",
  "config_file": "/etc/topophany/worker-a/topology.json",
  "updater_state_dir": "/var/lib/topophany-updater/worker-a",
  "bootstrap_manifest": "/etc/topophany/management/worker-a/bootstrap-manifest.json"
}
```

For a collector, set `role: "collector"`, its actual optional observer, and
`auth_file` to its observation auth file. Every installation must have a
dedicated executable and service; do not register a binary used by another
service. Data, protected updater state and authority files must not overlap.
Data belongs to the registered unprivileged user; its parent belongs to root.
The helper refuses container markers, known system-package paths and binaries
owned by `dpkg`/`rpm`. It never runs an install script or accepts a remote path,
command line, service definition, URL or environment assignment.

The supplied [systemd template](../deploy/systemd/topophany-updater@.service)
uses the paths above. Create the installation/updater directories with reviewed
ownership first; adjust the template's local paths if your installation differs.
Copy the registration to
`/etc/topophany/management/worker-a/helper.json`, install the unit locally and
start `topophany-updater@worker-a.service` **while updates are disabled**.
Initial helper state authenticates the current executable against the bootstrap
manifest. Registration is then bound by a persistent digest; retargeting it
requires explicit local recovery, not editing a live registration in place.

After verifying the helper is running, explicitly enable (the CLI also verifies
the root peer, installation identity and supported helper protocol before saving):

```sh
sudo topo management enable --policy /etc/topophany/management/worker-a/policy.json --capability updates
```

This does not enable configuration acceptance. The private Unix socket checks
both ends: the helper admits only root/the registered UID and the client requires
a root peer. Missing or incompatible helpers defer the update. Socket identity
alone is insufficient: the helper independently verifies the signed controller
command, acceptance session, local update policy, current configuration digest
and publisher metadata.

## Plan and deploy an exact version

Add `publisher_trust_file` to the controller policy and restart it. Resolve a
version independently for each target architecture:

```sh
topo management release verify --trust-file /private/publisher-root.json \
  --version v1.7.1 --os linux --arch arm64
```

Use the returned `target` object verbatim in a private update proposal:

```json
{
  "deployment_id": "home-update-001",
  "installations": ["collector-a"],
  "base_generations": {"collector-a": 0},
  "targets": {
    "collector-a": {
      "version": "v1.7.1",
      "release_id": 123,
      "asset_id": 456,
      "archive_sha256": "<resolved-archive-sha256>",
      "manifest_sha256": "<resolved-manifest-sha256>"
    }
  }
}
```

Use inventory's `update_generation` for the base. The controller and helper
both resolve and revalidate the pinned target; values in this example are
placeholders and will not pass verification.

```sh
topo management update publish --request-file /private/update-deployment.json \
  --server https://control.example:7448 --credential-file /private/update-publisher.json \
  --ca-file /private/control-ca.pem
topo management update activate --request-file /private/update-activate.json \
  --server https://control.example:7448 --credential-file /private/update-publisher.json \
  --ca-file /private/control-ca.pem
```

The activation file has `deployment_id` and `installations`, as for configuration,
but **exactly one installation** may activate at a time. Preparation downloads
and verifies without stopping the service. Activate after `prepared`; wait for
`healthy`, the target running version and the controller's observation interval
(default 300 seconds) before advancing another node in that deployment. The
observation clock starts when the controller receives health, not at a timestamp
supplied by the agent. A failure pauses that deployment. Diagnose it and make
a fresh reviewed deployment to resume; there is no force/bypass flag.

Update every receiving collector before its workers. The target collector's
signed `compatible_workers` list must include all current inbound worker
versions, and inventory must be fresh. A worker can update only when all its
registered receivers already run the target version. Stage collectors first,
then workers after collector health is confirmed; a combined proposal containing
not-yet-eligible workers is refused. `update cancel` cancels pending preparations,
not an executing replacement. No automatic fleet-wide rollback is attempted.

The local helper stops only its registered service, locks its writer, rehearses
the candidate against a bounded stopped-data copy under the service UID, saves
the verified old binary, replaces the fixed executable atomically and starts
the service. Health checks use systemd's main PID, `/proc/PID/exe` digest and a
fresh runtime lease with the correct version/project/role/observer. The exact
runtime configuration must match preparation. A repeated exact current-version
request verifies integrity and completes without restarting the service.

Immediately before committing activation intent, the helper reloads management
policy, update policy and publisher trust, reauthenticates the command against
current controller trust and checks the current maintenance window. A policy or
publisher-root change during verification requires a fresh attempt; expired or
revoked command authority cannot stop the service.

Failure restores only the authenticated, permitted previous executable. It
**does not restore an old data snapshot over acknowledged evidence**. Recovery
requires the publisher's explicit `rollback_from` compatibility declaration;
maintainers must test that pair. The copy used for preflight is disposable,
not a retained disaster-recovery backup. Maintain your normal stopped-data
backups. Recovery outlives the client/monitor process and can resume from the
helper's protected journal after helper restart. Disk or health failures can
require local intervention; do not delete journals or manually lower floors.

Restoring the old service has a separate two-minute recovery budget, including
when activation or the candidate rehearsal has exhausted its own deadline.
Rehearsal failure also verifies that the previous service becomes healthy after
restart before reporting the candidate's failure.

## Publisher setup and release procedure

This repository contains the verification/signing tools and draft-promotion
workflow, but contains **no production private signing keys or trust root**.
An administrator must establish publisher custody before any release becomes
eligible for unattended updates. These are maintainer operations, not worker
setup commands.

1. On a trusted offline machine, `topo management release keygen --dir
   /private/new-publisher` creates three root keys (threshold two), a release key,
   a separate catalog key and `publisher-root.json`. Keep the root keys offline
   and separately controlled. The public root expires after two years; plan
   renewal before then. Review its repository identity and fingerprints.
2. Install only that reviewed public root on nodes/controllers, and commit its
   public bytes as `.github/topophany-publisher.json` through a separately
   reviewed protected-main change. Do not learn initial trust from a release.
3. Enable GitHub **immutable releases**, protected branches/tags and the `release`
   environment's approval rules. Set the repository variable
   `TOPOPHANY_MANAGED_RELEASES=true`. Stable builds now produce a draft; unset
   retains the existing manual release behavior. Nodes always reject mutable,
   draft, prerelease and unsigned releases regardless of this variable.
4. Run the normal release verification/compatibility suite for the exact commit.
   Push the reviewed stable tag through the normal release process. The pinned
   release workflow builds all six archives and creates GitHub provenance
   attestations. No candidate binary executes with publication credentials.
5. Save the draft's GitHub release JSON, download its archives and verify the
   tag's exact commit. Run `management release manifest --trust-file ROOT
   --release-json RELEASE --artifacts-dir DIR --commit COMMIT --rollback-from
   vX.Y.Z --compatible-workers vX.Y.Z,vX.Y.N --output MANIFEST`, using absolute
   local paths and only compatibility pairs actually tested.
6. Sign the reviewed manifest with `management release sign --kind manifest
   --input MANIFEST --key-files RELEASE_KEY --output SIGNED`. Upload the result
   as `topophany-update.json` to that draft. The manifest binds repository ID,
   release ID, tag, commit, platform asset IDs, archive and binary sizes/digests,
   control protocol and tested compatibility. A SHA-256 checksum alone is
   insufficient authorization.
7. Dispatch `.github/workflows/managed-release.yml` on protected `main` with
   the exact stable tag. It verifies GitHub provenance against repository,
   workflow, source ref and commit, re-inspects all archives and validates the
   publisher signature. The publication job receives only a verification receipt,
   rechecks tag/asset identity, publishes the draft and confirms immutability.
   It never checks out release source or executes a downloaded binary.
8. Publish a signed security catalog at `update-metadata/security.json` on the
   official repository's `main`. `management release catalog` creates the
   unsigned document (`--trust-file`, increasing `--catalog-version`,
   `--minimum-version`, optional `--revoke-artifacts`/`--revoke-keys`, `--output`);
   sign it with `release sign --kind catalog`. The catalog expires after 24
   hours. Arrange a separately controlled scheduled refresh using
   `release refresh-catalog --trust-file ROOT --previous SIGNED_CATALOG
   --key-file CATALOG_KEY --output NEW_SIGNED_CATALOG`. Refresh preserves the
   minimum and revocations and increments the version. This change does not
   install a scheduled signer or store its secret in GitHub.

Root rotation uses an epoch exactly one higher, signed by the old and new root
thresholds. Verify with `management release rotate-root --trust-file OLD
--transition SIGNED_TRANSITION --output NEW`, then install the verified public
root locally. The signing command accepts multiple comma-separated key files.
Controllers cannot rotate a worker's publisher root. Root/catalog counters
survive recovery; expired metadata blocks new installs without stopping
monitoring. Offline operation never falls back to unsigned or cached-expired
release authorization.

The release resolver pins `WhitenWhiten/topophany`, repository ID `1417739003`
and owner ID `31219380` through the installed root. It accepts exact `vX.Y.Z`
stable tags and only the named platform assets. Downloads use fixed GitHub/API
origins and a small HTTPS CDN allowlist, without ambient proxies, forwarded
credentials or cookies. Redirect count, resolved IPs, body sizes, timeouts,
archive members and decompression are bounded. Generated source archives,
Actions artifacts, mirrors, arbitrary URLs, archive links and traversal paths
are ineligible. GitHub unavailability/rate limits defer new operations.

## Diagnostics, limits and acceptance evidence

Private state lives under the node's `data/management`, the controller data
directory and the helper's protected state directory. Audit JSONL contains
identities, digests, phases and closed receipts, not raw topology or secrets.
Agent audit storage is separated from administrative controller audit. Atomic
state writes record intent and confirmation; I/O failure latches new mutations
off until local recovery. Do not edit or restore stale state while a process
is running. Logs and state still contain internal identifiers and need private
storage and retention handling.

Default limits include 256 controller registrations/principals, 512 total management connections (including idle authenticated connections),
32 unauthenticated connections per source IP, 16 concurrent management requests, 60 requests/minute/principal, 64 config targets or 16
update targets per proposal, 4 MiB topology, 8 MiB aggregate controller blobs,
128 MiB compressed artifact and 256 MiB expanded archive. The helper's stopped
copy is limited to 1 GiB and 100,000 directory entries. Large installations must use an
external reviewed upgrade workflow. Typical polls are 15 seconds with jitter;
failures back off to five minutes. No claim of management HA is made.

Automated tests cover default-off combinations, credential boundaries, strict
JSON, signatures, duplicate-key threshold attacks, replay/restart, projections,
secret/destination binding, actual address denial, dual-collector drain/order,
archive and release rejection, root/catalog rotation, update health recovery,
configuration drift and post-commit audit failures. A disposable Linux arm64
Ubuntu 24.04/systemd 255 VM also executed actual service stop/start, candidate
recovery preflight, executable digest checks, successful replacement and failed
health recovery using synthetic signed releases. This was not an official
GitHub publication, real power-loss simulation or native macOS/Windows updater
test. Production publisher custody, immutable-release settings and a complete
eligible release remain explicit maintainer acceptance gates.

To repeat native lifecycle acceptance, use a disposable VM only. Cross-compile
`internal/update`'s tests, build two fixture `topo` binaries with model versions
`1.7.0` and `1.7.1` named `topo-old`/`topo-new`, create
`/run/topophany-disposable-acceptance`, and run the test binary as root with
`TOPO_UPDATE_SYSTEMD_ACCEPTANCE=1 TOPO_UPDATE_FIXTURES=/absolute/fixture-dir`.
The test creates temporary `/opt` installations and `/run/systemd/system`
units and removes them afterwards. Normal `go test` skips this opt-in test.

See [SECURITY.md](SECURITY.md) for residual trust assumptions and
[SCENARIOS.md](SCENARIOS.md) for named regressions. Management receipts cannot
prove that a compromised enrolled executor is honest. A compromised publisher
or local root can defeat these boundaries; signatures authenticate authority,
not the absence of software vulnerabilities.

Management polling reuses TLS connections while credential and CA material remain
unchanged. Authentication failures close the connection. Authenticated connections
leave the per-source unauthenticated budget, allowing enrolled nodes behind NAT
to share an address. Native probes and external collections share the configured
concurrency budget. Administrative audit events record the state digest; individual
node outcomes remain in their separate agent audit streams.

Release preflight runs outside the controller state mutex and refuses commit if
fleet state changes during verification. Immutable release metadata is cached for
at most 30 seconds and 32 entries, keyed by publisher trust, version and platform;
the security catalog is fetched and checked on every reuse. A stale-state refusal
requires retrying the proposal with current node state.
