# Usability implementation work

User-authorized scope: implement the capability review in priority order, with
separate verified commits for each deliverable. Completion below means shipped
code and appropriate verification, not merely a proposal.

## P0: connect, understand, investigate

- [x] Website onboarding: generate a real HTTP check and matching local credentials with `init --url`; configuration review precedes execution.
- [x] Task-based onboarding entry point: direct resources, website/server, multi-location network.
- [x] First-sample progress and guided handling of failed enrollment.
- [x] Status scope, monitoring coverage and explicit unknown reasons across viewers.
- [x] CLI related-incident grouping with independent evidence and counter-evidence preserved (`topo problems`).
- [x] Related-incident navigation in native/browser TUI.
- [ ] Related-incident shortcut from the menu bar.
- [x] Unified collector/viewer diagnostics, next actions and redacted diagnostic export.
- [x] Direct-source diagnostics in the same doctor report.

## P1: daily use and maintenance

- [ ] Topology editing: external-modification conflict protection, batch edits, change preview.
- [ ] Configuration rollout: per-collector/worker revision and affected measurement contracts.
- [ ] Adapter templates, compatibility feedback and missing-evidence guidance.
- [ ] Direct-to-collector migration preserving selected server identity and display preferences.
- [ ] History: incident before/after and observer comparison with a shared time range.
- [ ] Alerts: notification test, delivery/queue/drop visibility, timed silence, maintenance windows, service routing and explicit dual-collector notification policy.
- [ ] Saved TUI views, contextual actions and issue-focused maps.
- [ ] Browser connection recovery and actionable configuration help.
- [ ] Menu bar: in-app demo, collector enrollment and signed distribution.
- [ ] Consistent structured CLI errors, field paths and change-impact output.
- [ ] Operational status, backup/restore validation and upgrade compatibility checks.
- [ ] Chinese status, error and troubleshooting copy; task-oriented documentation.

## P2 and ongoing verification

- [ ] Certificate-expiry warning, sustained-latency policy and JSON-field assertions.
- [ ] Reviewed import candidates from SSH, Compose and Tailscale, with provenance.
- [ ] Large-topology, long-running, sleep/wake and hidden-window resource measurements.

Signing/publication may need the owner's signing assets and release destination.
Live hardware acceptance requires actual hardware evidence; synthetic tests do
not complete it. Keep collector viewer APIs read-only and preserve original
sample timestamps throughout these changes.
