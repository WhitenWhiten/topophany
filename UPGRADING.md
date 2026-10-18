# Upgrading

Version-to-version notes for people who already run Topophany. First-time
installs can ignore this file and start from the [README](README.md).

The [changelog](CHANGELOG.md) is the complete history. Each GitHub release also
ships notes under [`docs/releases/`](docs/releases/).

## Credentials on the command line (since 1.1.0)

The `--credential TOKEN` option has been removed to keep tokens out of process
arguments. Prefer saved credentials through `topo login`; for a temporary
override, use stdin or a private token file as below. `--token-stdin` and
`--credential-file` are mutually exclusive, and a multi-server profile requires
an explicit `--server` for either override.

Keep `token.txt` private (`0600` on POSIX; restrict its ACL on Windows):

```sh
topo --json --token-stdin < token.txt
topo --credential-file token.txt
```

## Managed log permissions (since 1.1.0)

On POSIX, managed logs and their existing numbered backups must have
permissions `0600` or stricter. If `serve` or `worker` refuses a file selected
by `--log-file`, correct those permissions before restarting.

## Snapshots during a configuration reload (since 1.1.0)

A snapshot request admitted before a configuration reload may return
`503 topology_reloaded` with `Retry-After: 1`. Send a new request; it will
authenticate against the current tokens.

## Later releases

- [v1.1.0](docs/releases/v1.1.0.md) — credential flag, log permissions, webhook
  response-header limit, release-packaging protections.
- [v1.1.1](docs/releases/v1.1.1.md) — monotonic probe durations across clock steps.
- [v1.2.0](docs/releases/v1.2.0.md) — optional `policy_threshold`, installer
  rollback rules, and viewer/collector upgrade order before new topology fields.
