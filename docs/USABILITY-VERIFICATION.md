# Usability work verification — 2026-11-01

Implementation is tracked in [USABILITY-WORK.md](USABILITY-WORK.md). Each feature
has a separate commit. Tests use synthetic inventories, temporary directories,
local loopback services and isolated application settings. No production topology
or credentials were changed, and no public release was pushed.

## Functional and compatibility checks

- Full `go test -race -count=1 ./...`: every package passed except the preview
  stream goldens, which still lacked the new coverage counts. Regenerated all
  20 affected frames and mechanically verified that removing only the additive
  `summary.verdict.value.coverage` restored exact old JSON equality. The complete
  affected stream package then passed with `-race -count=1`.
- `go vet ./...`, formatting checks and all 90 Python source/site/release-boundary
  tests passed. Website generation and all 12 dedicated site tests passed.
- Native macOS arm64, Linux amd64 and Windows amd64 builds passed. The regenerated
  website also compiled the WebAssembly viewer. Cross-builds are compilation
  evidence, not native Linux/Windows runtime acceptance. The Linux-only process
  harness explicitly skipped on macOS; it is not counted as a pass.
- Companion `swift test`: 383 tests across six packages passed. Xcode application
  tests: 20 tests across six suites passed. Canonical direct contract fixtures
  are synchronized between repositories and pass both Go and Swift decoding.
- HTTP policy tests cover verified TLS expiration, trust-failure precedence,
  measured duration contracts, ordinary failure precedence, bounded JSON Pointer
  traversal, missing versus null, duplicate keys, precise large integers and
  private assertion projection/cloning. Import tests cover deterministic paused
  candidates, unsupported routes, secret omission, budgets and overwrite refusal.
- Browser connection recovery was exercised manually before the final
  localization pass. The later browser task was no longer available; final
  localized browser output was validated by generated tours/build tests rather
  than claimed as a second live-browser session.

## Scale and real application interaction

Machine: Apple M6, macOS arm64; Debug application, Chinese UI, synthetic data.
These are observations on this machine, not release performance guarantees or
before/after speedup claims. Some verification workloads ran concurrently.

| Scenario | Samples | Maximum main-thread gap |
| --- | ---: | ---: |
| Native theme wheel scrolling | 10 | 24.32 ms |
| Terminal theme wheel scrolling | 10 | 36.72 ms |
| Native theme map interaction | 10 | 39.45 ms |
| Terminal theme map interaction | 10 | 24.50 ms |

All scroll runs moved the viewport and retained valid hosting/window bounds.
Native map drawing peaked at 22.46 ms per recorded draw. Terminal wheel drawing
peaked at 9.26 ms. These observations do not establish a 60 fps guarantee.

Existing Go benchmarks exercised 255, 1020 and 4095 definition entities, plus
128, 512 and 1200 fleet cards with services. At 4095 entities, services took
2.55 ms / 3.77 MB per operation, overview 2.07 ms / 3.78 MB, paths 1.14 ms /
1.81 MB and observers 0.81 ms / 1.25 MB. Building 1200 fleet cards took 0.94 ms /
0.59 MB. No speculative cache or new background loop was added for these numbers.

## Lifecycle boundaries

The application test advances its clock by eight hours, expires old evidence,
then invokes the same retry handler as the workspace wake notification twenty
times and opens/closes the panel twenty times. It retains one engine, coalesces
retry requests and never turns expired evidence into current health. This is a
controlled lifecycle test, not actual hardware sleep/wake acceptance.

A local signed/notarized universal companion DMG is recorded in the companion
repository's `docs/releases/0.2.0-local.md`. Public publication is separate.

A three-minute native collector run used a temporary loopback HTTP server, one
request per second, history capped at 100 and a 1 KiB checkpoint threshold.
RSS grew from 18.1 MiB at 10 seconds to 20.8 MiB at 180 seconds; accumulated CPU
time was 0.31 seconds. The runtime lease was current and matched the private
configuration. After graceful shutdown, isolated recovery validated the project,
authentication configuration and 158380 bytes of persisted files. Recovery did
not rewrite the original or start probes. This is a bounded checkpoint/recovery
soak, not a claim of multi-day stability.

One initial hidden-window sample taken during concurrent UI tests recorded
unexpected drawing; it lacked explicit window-state capture and is not used as
acceptance evidence. An isolated repeat captured the window as hidden both before
and after 60 seconds: no drawing/layout/scene counters, approximately 0.1% CPU,
and RSS 95.4 to 95.2 MiB. A further rapid-open/close repeat is recorded in the
companion verification report.

The isolated rapid-cycle repeat also passed: twenty open/close cycles followed by
60 seconds with the panel verified hidden before and after, zero rendering/layout
counters, RSS 87.1 to 86.9 MiB and 0.13 seconds accumulated CPU. The app reopened
successfully afterwards. All temporary app and collector processes were stopped.
No rendering code was changed based on the non-reproducible concurrent sample.
Machine-readable interaction and hidden-lifecycle results are committed in
`topophany-bar/docs/verification/2026-11-01/`.
