# Windows CI failure history

Audit date: 2026-11-02. Dates below are UTC. Evidence comes from GitHub
Actions job logs and the corrective commits, rather than commit titles alone.

## Latest failure and repair

[CI #144](https://github.com/WhitenWhiten/topophany/actions/runs/41416580524)
at `af6d15f` failed in `internal/probe`:

```text
TestDNSUDPAndTCPFallback/TCP_fallback
  listen tcp 127.0.0.1:49834: bind: An attempt was made to access a socket in a way forbidden by its access permissions.
TestDNSLargeAnswerProducesDeliverableError
  listen tcp 127.0.0.1:49869: bind: An attempt was made to access a socket in a way forbidden by its access permissions.
```

The shared `dnsFixture` allocated a UDP ephemeral port first, then attempted
to bind TCP to the same number. A port selected for UDP is not guaranteed to
be available for TCP. Twenty retries did not help on this runner. A TCP
excluded/reserved range is consistent with these access-denied failures;
the runner's excluded-port inventory was not recorded, so that specific
reservation mechanism is an inference.

The fixture now binds TCP to port zero first and keeps that listener open
while binding UDP to its assigned port. A UDP collision closes the TCP
listener before another bounded attempt. UDP-only tests still request a
UDP ephemeral port directly. Both DNS assertions, including actual TCP
fallback and the oversized-answer error, remain enabled on Windows.

Local validation on macOS arm64 with Go 1.26.10:

- The two failing tests passed 100 iterations with `-race`.
- The complete `internal/probe` package passed with `-race`.
- `go test -count=1 ./...` and `go vet ./...` passed.
- Windows amd64 and arm64 probe test binaries cross-compiled successfully.

Cross-compilation is not Windows execution. This repair has not yet been
verified by a new Windows runner.

## Scope and evidence limits

The audit enumerated all 195 workflow-run records available through the
repository Actions API, including all 144 runs named `CI` (#1–#144).
It inspected jobs for every failed and cancelled CI run, plus #144, which
was still running with a completed Windows failure at inspection time.
Cancelled workflows were included because a Windows job can fail before
a newer push cancels the remaining jobs.

There are **17 confirmed CI runs with a failed Windows job**, including
the dedicated Windows diagnostic run #74. Not all were Windows-only
failures. The table explicitly identifies concurrent failures and unfinished
jobs.

GitHub returned no jobs or check runs for failed PR runs
[#129](https://github.com/WhitenWhiten/topophany/actions/runs/40045533876)
and [#130](https://github.com/WhitenWhiten/topophany/actions/runs/40175981176).
Five cancelled PR runs (#12, #14, #16, #28, #30) also returned no jobs.
Their Windows status and cause cannot be established from the available
records; they are not silently classified as Windows failures.

## Confirmed failures

| UTC date / run | Windows failure and cause | Corrective change | Other failed jobs in the same run |
|---|---|---|---|
| 10-14 [#21](https://github.com/WhitenWhiten/topophany/actions/runs/38522642345) | TUI failed to compile: duplicate `vLayout.route` and missing `routeBudget` during a partially integrated layout-budget change. | `3c5a417` completes the shared routing-budget implementation. | Linux, macOS, lint, acceptance. This was a common source error. |
| 10-14 [#22](https://github.com/WhitenWhiten/topophany/actions/runs/38523135232) | `TestVirtualWiresJoinOnlyAtAnEnd`: the crowded fixture lost 13 of 48 wires; the new shared search budget was too small. | `e931c00` raises the bounded work ceiling while retaining the layout limits. | Linux, macOS, lint, acceptance. This was not Windows-specific. |
| 10-14 [#38](https://github.com/WhitenWhiten/topophany/actions/runs/38555984180) | `TestLedgerSurvivesRestartWithinItsBounds` required POSIX mode `0600` on Windows, which reports different mode bits. | `4d2e09a` checks existence everywhere and POSIX privacy bits where supported. | None. |
| 10-16 [#57](https://github.com/WhitenWhiten/topophany/actions/runs/38848227804) | `TestNoAnswerSaysWhy` classified connection refusal as generic `unreachable`; Winsock error numbers differ from Go's POSIX-style constants. | `eeee524` recognizes Windows refusal, reset and unreachable codes. | None. |
| 10-17 [#72](https://github.com/WhitenWhiten/topophany/actions/runs/39021632282) | Bootstrap/rollback/ACL tests failed because the PowerShell ACL helper exited with status 1. These logs do not expose the underlying exception. | `a16533a` changes persistence to DACL-only to avoid audit privileges; continuing failures required `2e9bbe4` below. | None. |
| 10-17 [#73](https://github.com/WhitenWhiten/topophany/actions/runs/39023263579) | ACL tests still failed after the DACL-only repair; `powershell.exe` again exited with status 1. | `2e9bbe4`, following the diagnostic evidence in #74. | None. |
| 10-17 [#74](https://github.com/WhitenWhiten/topophany/actions/runs/39024474582) | Dedicated diagnostic run exposed `Get-Acl`: `Microsoft.PowerShell.Security` could not load. The inherited PowerShell module path could select an incompatible module. | `2e9bbe4` reads ACLs through .NET APIs instead of loading `Get-Acl`; DACL-only writes remain in place. | No matrix: this was a Windows-only diagnostic workflow. |
| 10-17 [#78](https://github.com/WhitenWhiten/topophany/actions/runs/39050866415) | `TestCommittedToursAreCurrent`: committed tours were stale on amd64. Arm64 could fuse color interpolation into a multiply-add, changing rounding by one channel value. | `dc9771d` forces the same rounding sequence; `3c180fa` regenerates the site. | Linux and acceptance. macOS arm64 passed. |
| 10-18 [#90](https://github.com/WhitenWhiten/topophany/actions/runs/39149018416) | All `TestMonitorStreamProtocol` subprocess cases failed to launch a fixture named `topo` without `.exe`. | `0175dce` adds the Windows executable suffix and handles native closed-pipe errors. | Acceptance also failed. |
| 10-19 [#101](https://github.com/WhitenWhiten/topophany/actions/runs/39244778486) | Bootstrap treated a regular-file outbox as an empty directory on Windows. Monitor's supposedly valid fixture used a drive-less `/tmp/metrics.json`, which is not an absolute Windows path. | `bbe3ef7` explicitly requires an outbox directory and uses the native temporary directory for monitor fixtures. | macOS also failed, including a separate protected-path alias issue repaired in the same commit. |
| 10-21 [#110](https://github.com/WhitenWhiten/topophany/actions/runs/39570565308) | Two outbox ACK tests expected a directory sync count and missing-directory sync error, although `SyncDir` is a no-op on Windows. | `e82412a` makes the sync expectations platform-aware while retaining deletion, index and restart assertions. | None recorded; acceptance was cancelled. The workflow's overall result was cancelled. |
| 10-22 [#117](https://github.com/WhitenWhiten/topophany/actions/runs/39763328246) | `TestPrivateCredentialPermissionsAndInvisibleSelection` expected `chmod 0644` to trigger the POSIX privacy rejection on Windows. | `a9221bd` gates that mode assertion and tests malformed credential content on every OS. | macOS also failed in a separate stream replacement timeout/cancellation path. |
| 10-22 [#120](https://github.com/WhitenWhiten/topophany/actions/runs/39874629016) | `TestServerBundleScopesKeysAndCertificates` failed at 15.03 seconds in the ACL helper. The corrective commit identifies cold PowerShell startup exhausting the generic 15-second command budget. | `4ad1c04` gives ACL operations a separate one-minute bound, respects shorter caller deadlines and preserves cancellation causes. | Acceptance also failed in stream shutdown handling. |
| 10-23 [#123](https://github.com/WhitenWhiten/topophany/actions/runs/39928029680) | `TestOldBinaryRefusesNewExternalData` hung until the 10-minute package timeout: tar EOF left `git archive` record padding unread, so the producer blocked on the Windows pipe while the consumer waited for it to exit. | `b74b334` drains stdout before `Wait`, tests trailing padding and adds `.exe` to the old-reader binary. | None. |
| 10-26 [#131](https://github.com/WhitenWhiten/topophany/actions/runs/40297257067) | `TestPrivateSessionLeaseAndLifecycle` again required POSIX `0600` mode bits on Windows. | `38e6ae4` limits that mode assertion to supported platforms. | Acceptance also failed; its separate browser Go toolchain problem was repaired in the same commit. |
| 11-01 [#141](https://github.com/WhitenWhiten/topophany/actions/runs/41333969351) | Local credentials again asserted POSIX modes. Concurrent saved-secret/snapshot reads and stream reconfiguration also returned `storage_error` while credentials were replaced or removed. | `bc53824` gates mode assertions and holds the same cross-process lock for profile/credential readers and writers; lock contention is distinguished from permanent errors. | No other failed job recorded; Linux, macOS and acceptance were cancelled. |
| 11-02 [#144](https://github.com/WhitenWhiten/topophany/actions/runs/41416580524) | Two DNS tests bound UDP first and then failed to bind the same TCP port with access denied. | This change binds TCP first and retains bounded retries for UDP collisions. | At inspection: macOS and lint passed; Linux and acceptance were still running. |

## Recurring causes and development rules

- **POSIX assumptions recur in new packages.** Ledger (#38), direct (#117),
  session (#131), and local credentials (#141) independently repeated mode-bit
  assumptions. Check that files exist on every OS; assert `0600`/`0700` only
  where supported. Windows ACL guarantees require ACL-specific verification.
- **Use each platform's native semantics.** This includes Winsock error codes,
  `.exe` suffixes, drive-qualified paths, explicit directory type checks and
  platform-specific directory-sync behavior. Building for Windows alone does
  not execute these paths.
- **Treat subprocesses and concurrent storage as protocols.** Drain output
  before waiting, preserve cancellation causes, bound cold startup separately,
  and keep profile plus credential reads under the writer's lock.
- **Let each transport select an eligible port.** When a DNS fixture needs
  one port for both protocols, keep the TCP listener bound while acquiring
  UDP, close failed attempts and retry within a fixed bound.
- **Reproducible artifacts must also be architecture-independent.** The
  amd64/arm64 color rounding failure affected Linux and Windows together.
  Regenerating assets alone would not have fixed the underlying arithmetic.

The record does not support the claim that every CI failure was Windows-only.
For example, [#105](https://github.com/WhitenWhiten/topophany/actions/runs/39410731774)
failed only in the macOS job while Windows passed, and
[#140](https://github.com/WhitenWhiten/topophany/actions/runs/41325102497)
failed only in acceptance while all three OS test jobs passed.
