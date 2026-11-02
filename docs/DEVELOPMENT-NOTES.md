# Development and Reconstruction Notes

The following records boundary issues identified and resolved during implementation and testing. They represent software verification and do not constitute live production network diagnostics. The final acceptance report reflects a complete re-run after fixes were applied, rather than inheriting earlier test metrics.

- Unavailable local bind addresses generated host socket errors whose classification was initially overly broad. These were updated to monitor-side errors, and an automated test verifying actual local bind failures was added.
- The deep copy of outbox `Pending` batches previously reused slices and nested maps. This was changed to decode into fully detached data structures to prevent callers from altering queued dispatch state. Tests verify memory separation and reopened state on disk.
- When an oversized bracketed paste was truncated, remaining trailing bytes previously fell back into the normal keystroke input stream. The decoder now discards bytes until the bracketed paste termination sequence, and regression tests were added for chunked and oversized paste command-injection boundaries.
- The TUI must verify that both stdin and stdout are genuine terminals to prevent emitting ANSI escape sequences into regular files or pipes. Linux PTY acceptance tests verify cursor restoration and password entry without local echo.
- HTTP and TLS traces retain the specific failure stage upon error. New HTTP client streams do not claim the ability to disable internal connection reuse inside third-party proxy cores.
- The SSH jump-host bridge properly binds local addresses and handles spaces and percent signs in `known_hosts` file paths. Controlled subprocess tests verify command invocation without running live cryptographic handshakes.
- Configuration validation prevents proxy adapters from silently ignoring incompatible dial parameters. Strict byte budgets were added for CA certificates and checkpoint files, and the outbox item encoding budget is guaranteed to remain smaller than the collector API message limit.
- `go vet` identified unkeyed struct literals in cross-package test fixtures; these were converted to keyed fields and re-verified across all suites.
- The invariant for minimal encoded length in SOCKS address fuzzing was initially overly strict, as single-character domain names are valid. This was adjusted to match standard protocol specifications, adding an `a:1` fuzz seed rather than rejecting valid inputs to appease a test invariant.
- When process and PTY acceptance tests shut down child processes, the test harness must continuously drain the PTY master buffer to prevent test scripts from imposing kernel backpressure. The test runner was adjusted accordingly rather than suppressing child exit timeouts.

External security audits are not claimed. Areas requiring manual hardware verification (such as Windows ACLs, production TUN interfaces, and live tunnels) are documented in [ACCEPTANCE.md](ACCEPTANCE.md).

Historical Windows CI failures, their corrective commits, and recurring portability
mistakes are recorded in [WINDOWS-CI-HISTORY.md](WINDOWS-CI-HISTORY.md).
