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
- [x] Related-incident shortcut from the menu bar (capability-gated).
- [x] Unified collector/viewer diagnostics, next actions and redacted diagnostic export.
- [x] Direct-source diagnostics in the same doctor report.

## P1: daily use and maintenance

- [x] Topology editing: external-modification conflict protection, batch edits, change preview.
- [x] Configuration rollout: independently inspect collector public revisions, worker contract agreement, and affected public measurements (`topo rollout`).
- [x] Exact private worker revision verification through leased local runtime records (not viewer APIs).
- [x] Bundled adapter templates, pinned compatibility notes and missing-evidence guidance.
- [x] Direct-to-collector migration preserving selected server identity and display preferences.
- [x] CLI history: incident before/after and observer comparison with pinned source and time range.
- [x] Service history comparison from the menu bar service picker.
- [x] Alerts: explicit notification test and delivery/queue/drop visibility.
- [x] Alerts: timed silence, maintenance windows, service routing and explicit dual-collector notification policy.
- [x] Saved TUI views with perspective/filter/map choices; existing selection-focused map highlighting retained.
- [x] Investigation/history shortcuts in TUI/browser incident sheets and the menu bar.
- [x] Browser connection recovery and actionable configuration help.
- [x] Menu bar: in-app demo, collector enrollment and explicit migration of direct pins.
- [x] Signed and notarized universal 0.2.0 local distribution artifact; provenance in the companion repository.
- [x] Consistent structured CLI errors, field paths and change-impact output.
- [x] Operational status, isolated backup recovery validation and candidate-binary compatibility checks.
- [x] Chinese critical status, controls, coverage/connection-error explanations and task-oriented guide; Japanese counterparts retained.

## P2 and ongoing verification

- [x] Certificate-expiry warning, sustained-latency policy and private bounded JSON-field assertions.
- [x] Reviewed offline import candidates from SSH, rendered Compose JSON and Tailscale peer inventory, with provenance and paused checks.
- [x] Large-topology benchmarks, bounded collector/checkpoint soak and recovery, controlled eight-hour wake-gap tests, and real hidden-window resource measurements. See [verification evidence and limits](USABILITY-VERIFICATION.md).

The signed/notarized local distribution artifact is complete. Public publication
was not requested and has not been performed.
Live hardware acceptance requires actual hardware evidence; synthetic tests do
not complete it. Keep collector viewer APIs read-only and preserve original
sample timestamps throughout these changes.
