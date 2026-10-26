#!/usr/bin/env python3
"""Reproducible, offline Go + loopback Linux acceptance; preserves actual failures.

No package downloads, production addresses, SSH keys, or third-party Python modules.
A successful cross-build is deliberately not reported as a native execution test.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time

from source_identity import source_identity, require_unchanged

ROOT = Path(__file__).resolve().parents[1]


def run_command(command: list[str], *, timeout: float, **kwargs) -> subprocess.CompletedProcess:
    """Run a logged command; cancel its POSIX process group on timeout or interruption."""
    with subprocess.Popen(command, start_new_session=os.name == "posix", **kwargs) as proc:
        try:
            return subprocess.CompletedProcess(command, proc.wait(timeout=timeout))
        except BaseException:
            # Go launches separate test binaries. Killing only the Go driver
            # leaves those children running into the next verification stage.
            if os.name == "posix":
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            else:
                # Preserve the existing direct-child cleanup on non-POSIX hosts.
                proc.kill()
            proc.wait()
            raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / ".verify-build" / "verification")
    fuzz_budget = parser.add_mutually_exclusive_group()
    fuzz_budget.add_argument("--fuzz-seconds", type=int)
    fuzz_budget.add_argument("--fuzz-executions", type=int, default=25000)
    args = parser.parse_args()
    if args.fuzz_seconds is not None and not 1 <= args.fuzz_seconds <= 60:
        parser.error("fuzz-seconds must be in [1,60]")
    if args.fuzz_executions < 1:
        parser.error("fuzz-executions must be positive")
    # An execution budget avoids racing Go's short fuzz deadline on busy CI
    # runners. Real failures still fail the run; the subprocess timeout bounds hangs.
    fuzz_time = f"{args.fuzz_seconds}s" if args.fuzz_seconds is not None else f"{args.fuzz_executions}x"
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if not shutil.which("go"):
        parser.error("Go must be installed and on PATH")
    env = os.environ.copy()
    env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="4")
    source_before = source_identity(ROOT)
    results: list[dict] = []
    started = dt.datetime.now(dt.timezone.utc).isoformat()
    start_clock = time.monotonic()
    build_dir = Path(tempfile.mkdtemp(prefix="topophany-verify-"))

    required_go = re.search(r"^go ([0-9.]+)$", (ROOT / "go.mod").read_text(), re.MULTILINE)[1]
    toolchain_bin = next(Path.home().glob(f"go/pkg/mod/golang.org/toolchain@v0.0.1-go{required_go}.*/bin"), None)
    if toolchain_bin and toolchain_bin.is_dir():
        env["PATH"] = str(toolchain_bin) + os.pathsep + env.get("PATH", "")
        # A version-manager shim may have exported another toolchain's GOROOT.
        # Keep the selected compiler and standard-library sources together.
        env["GOROOT"] = str(toolchain_bin.parent)

    def sanitize_str(s: str) -> str:
        s = s.replace(str(ROOT) + "/", "").replace(str(ROOT), ".")
        return s.replace(str(Path.home()), "~")

    def run(name: str, command: list[str], timeout: int = 180,
            overrides: dict | None = None) -> dict:
        log_path = output / (name + ".log")
        begin = time.monotonic()
        sanitized_command = [sanitize_str(arg) for arg in command]
        record = {"name": name, "command": sanitized_command, "log": log_path.name}
        print("RUN " + name + ": " + shlex.join(sanitized_command), flush=True)
        procenv = dict(env)
        procenv.update(overrides or {})
        try:
            with log_path.open("w", encoding="utf-8") as log:
                proc = run_command(command, cwd=ROOT, env=procenv, stdout=log,
                                   stderr=subprocess.STDOUT, timeout=timeout)
            content = log_path.read_text(encoding="utf-8", errors="replace")
            sanitized_content = sanitize_str(content)
            if sanitized_content != content:
                log_path.write_text(sanitized_content, encoding="utf-8")
            record["exit_code"] = proc.returncode
        except subprocess.TimeoutExpired:
            record.update(exit_code=124, error="verification command timed out")
            with log_path.open("a", encoding="utf-8") as log:
                log.write("\nVERIFICATION TIMEOUT\n")
        except OSError as exc:
            record.update(exit_code=127, error=str(exc))
            log_path.write_text(str(exc), encoding="utf-8")
        record["duration_seconds"] = round(time.monotonic() - begin, 3)
        record["status"] = "pass" if record["exit_code"] == 0 else "fail"
        results.append(record)
        print(f"{record['status'].upper()} {name} ({record['duration_seconds']}s)", flush=True)
        (output / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
        return record

    try:
        run("go-version", ["go", "version"])
        run("go-environment", ["go", "env", "GOOS", "GOARCH", "CGO_ENABLED", "GOVERSION"])
        formatting = run("gofmt", ["gofmt", "-l", "cmd", "internal"])
        if (output / "gofmt.log").read_text().strip():
            formatting.update(status="fail", exit_code=1, error="gofmt emitted unformatted filenames")
        run("source-and-release-boundaries", [sys.executable, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_*.py"])
        run("module-integrity", ["go", "mod", "verify"])
        run("vet", ["go", "vet", "./..."])
        cov_path = str((output / "coverage.out").relative_to(ROOT)) if (output / "coverage.out").is_relative_to(ROOT) else str(output / "coverage.out")
        # Coverage-instrumented rendering also needs the bounded package
        # deadline and collection allowance used by the repeated test pass.
        run("tests-json", ["go", "test", "-json", "-count=1", "-timeout=10m",
                           "-coverprofile=" + cov_path, "./..."], timeout=900)
        # Race-instrumented TUI rendering can outgrow the former 600 s limit
        # on shared CI runners. Let Go report stacks for a hung package before
        # the outer deadline, which also allows time for compilation.
        run("race", ["go", "test", "-race", "-count=1", "-timeout=15m", "./..."], timeout=1200)
        # Three full passes can exceed five minutes on shared CI runners.
        # Keep a bounded package deadline and leave the wrapper time to collect
        # Go's timeout stacks and finish other packages before terminating it.
        run("repeat", ["go", "test", "-count=3", "-timeout=10m", "./..."], timeout=900)
        run("coverage-summary", ["go", "tool", "cover", "-func=" + cov_path])
        fuzz = [
            ("adapters/beszel", "FuzzBeszelResourceDecode"),
            ("adapters/komari", "FuzzKomariResourceDecode"),
            ("tui", "FuzzDecoderAndRendering"),
            ("api", "FuzzValidateBatch"),
            ("client", "FuzzSnapshotStructure"),
            ("engine", "FuzzStateNeverPromotesExpiredSample"),
            ("config", "FuzzDecodeTopology"),
            ("report", "FuzzNormalize"),
            ("store", "FuzzEnvelope"),
            ("probe", "FuzzSOCKSAddress"),
            ("probe", "FuzzEdgeFactsCompatibility"),
            ("adapters/tailscale", "FuzzTailscaleStatus"),
            ("adapters/tailscale", "FuzzTailscaleDiagnostic"),
            ("adapters/wireguard", "FuzzWireGuardRows"),
            ("adapters/cloudflared", "FuzzReady"),
            ("adapters/cloudflared", "FuzzMetrics"),
            ("adapters/cloudflare", "FuzzParseTunnel"),
            ("adapters/cloudflare", "FuzzParseDNSRecord"),
            ("adapters/cloudflare", "FuzzParsePoolHealth"),
            ("probe", "FuzzDNSPacket"),
            ("probe", "FuzzICMPChecksum"),
            ("rollup", "FuzzLateSampleOrder"),
        ]
        for package, target in fuzz:
            run("fuzz-" + target, ["go", "test", "./internal/" + package, "-run=^$",
                "-fuzz=^" + target + "$", "-fuzztime=" + fuzz_time, "-parallel=2"], timeout=120)
        binary = build_dir / ("topo.exe" if os.name == "nt" else "topo")
        run("build-native", ["go", "build", "-trimpath", "-o", str(binary), "./cmd/topo"],
            overrides={"CGO_ENABLED": "0"}, timeout=300)
        targets = [("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64"),
                   ("darwin", "amd64"), ("darwin", "arm64"), ("windows", "arm64")]
        for goos, goarch in targets:
            target = build_dir / ("topo-" + goos + "-" + goarch + (".exe" if goos == "windows" else ""))
            run("build-" + goos + "-" + goarch,
                ["go", "build", "-trimpath", "-o", str(target), "./cmd/topo"],
                overrides={"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}, timeout=300)
        for example in ("minimal", "homelab", "host-resources", "cloudflare", "beszel", "komari"):
            run("example-" + example, [str(binary), "validate", "--config", "examples/" + example + ".json"])
        for panel, source in (("desktop", "collector"), ("worker", "worker")):
            run("monitor-example-" + panel, [str(binary), "monitor", "configure", panel,
                "--monitor-dir", str(build_dir / "panels"), "--source", source,
                "--template", "examples/monitor-" + panel + ".json",
                "--output", str(build_dir / (panel + "-metrics.json"))])
            run("monitor-preview-" + panel, [str(binary), "monitor", "preview", panel,
                "--monitor-dir", str(build_dir / "panels")])
        help_sections = []
        for cmd in ("", "serve", "worker", "login", "init", "validate", "export", "once", "token", "monitor"):
            name = "help-" + (cmd or "topo")
            command = [str(binary)] + ([cmd] if cmd else []) + ["--help"]
            run(name, command)
            help_sections.append("## topo " + cmd + " --help\n\n```text\n" + (output / (name + ".log")).read_text() + "```\n")
        (output / "CLI-HELP.md").write_text("# Actual compiled CLI help\n\n" + sanitize_str("\n".join(help_sections)), encoding="utf-8")
        smoke_out = str((output / "process-smoke.json").relative_to(ROOT)) if (output / "process-smoke.json").is_relative_to(ROOT) else str(output / "process-smoke.json")
        run("process-smoke", [sys.executable, "scripts/smoke.py", "--binary", str(binary),
                              "--output", smoke_out], timeout=120)
    finally:
        shutil.rmtree(build_dir, ignore_errors=True)

    events = []
    packages = []
    tests_log = output / "tests-json.log"
    if tests_log.exists():
        for line in tests_log.read_text(encoding="utf-8", errors="replace").splitlines():
            try:
                value = json.loads(line)
            except (ValueError, TypeError):
                continue
            if value.get("Action") not in ("pass", "fail", "skip"):
                continue
            item = {key: value[key] for key in ("Action", "Package", "Test", "Elapsed") if key in value}
            (events if "Test" in value else packages).append(item)
    coverage_log = (output / "coverage-summary.log").read_text(errors="replace") if (output / "coverage-summary.log").exists() else ""
    match = re.search(r"total:\s+\(statements\)\s+([\d.]+)%", coverage_log)
    smoke_path = output / "process-smoke.json"
    smoke = json.loads(smoke_path.read_text()) if smoke_path.exists() else {"status": "not_run"}
    top_tests = [event for event in events if "/" not in event["Test"] and event["Test"].startswith("Test")]
    fuzz_roots = [event for event in events if "/" not in event["Test"] and event["Test"].startswith("Fuzz")]
    sources = list((ROOT / "cmd").rglob("*.go")) + list((ROOT / "internal").rglob("*.go"))
    source_after = source_identity(ROOT)
    try:
        require_unchanged(source_before, source_after)
    except RuntimeError as exc:
        results.append({"name": "source-identity", "status": "fail", "exit_code": 1,
                        "error": str(exc), "duration_seconds": 0, "log": "results.json"})
    failures = [r["name"] for r in results if r["status"] != "pass"]
    summary = {
        "schema": 1, "project": "Topophany", "version": "1.5.1",
        "source_before": source_before, "source_after": source_after,
        "started_utc": started, "finished_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "duration_seconds": round(time.monotonic() - start_clock, 3),
        "host_platform": platform.platform(),
        "go_version": (output / "go-version.log").read_text().strip(),
        "network_policy": "Go dependencies offline; all protocol acceptance requests loopback; no production credentials",
        "go_files": len(sources), "go_test_files": sum(p.name.endswith("_test.go") for p in sources),
        "go_lines": sum(len(p.read_text().splitlines()) for p in sources),
        "command_count": len(results), "quality_gate_failures": failures,
        "status": "pass" if not failures else "fail",
        "coverage_statement_percent": float(match.group(1)) if match else None,
        "test_events": {action: sum(v["Action"] == action for v in events) for action in ("pass", "fail", "skip")},
        "top_level_test_functions": {action: sum(v["Action"] == action for v in top_tests) for action in ("pass", "fail", "skip")},
        "fuzz_root_seed_suites": {action: sum(v["Action"] == action for v in fuzz_roots) for action in ("pass", "fail", "skip")},
        "skipped_tests": [event for event in events if event["Action"] == "skip"],
        "package_results": packages, "bounded_fuzz_targets": len(fuzz),
        "fuzz_seconds_per_target": args.fuzz_seconds,
        "fuzz_executions_per_target": args.fuzz_executions if args.fuzz_seconds is None else None,
        "process_acceptance": smoke,
        "cross_builds_are_execution_tests": False,
        "production_acceptance": "NOT EXECUTED; see docs/ACCEPTANCE.md",
    }
    (output / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    (output / "results.json").write_text(json.dumps(results, indent=2) + "\n", encoding="utf-8")
    (output / "test-events.json").write_text(json.dumps(events, indent=2) + "\n", encoding="utf-8")
    event_rows = ["| " + event["Action"] + " | `" + event["Package"] + "` | `" + event["Test"] + "` | " + str(event.get("Elapsed", "")) + " |" for event in events]
    (output / "TEST-EVENTS.md").write_text(
        "# Actual Go test terminal events\n\nParent tests, subtests and fuzz seed cases are separate events; do not add them up as independent feature scenarios.\n\n"
        "| Result | Package | Test or seed | Elapsed seconds |\n|---|---|---|---|\n" + "\n".join(event_rows) + "\n", encoding="utf-8")
    rows = [f"| {r['name']} | {r['status']} | {r['exit_code']} | {r['duration_seconds']} | [{r['log']}]({r['log']}) |" for r in results]
    report = f"""# Topophany 1.5.1 — Automated Acceptance Report

Generated: {summary['finished_utc']}. This report was generated by `scripts/verify.py` from actual command exit codes and Go JSON test events; pass counts are not manually entered.

## Results

Overall status: **{summary['status'].upper()}**. Command checks: {len(results)}; failed: {', '.join(failures) or 'None'}.

Go source: {summary['go_files']} files ({summary['go_test_files']} test files), totaling {summary['go_lines']} lines of Go.
Top-level Test functions: pass {summary['top_level_test_functions']['pass']}, fail {summary['top_level_test_functions']['fail']}, skip {summary['top_level_test_functions']['skip']}.
Terminal events including parent tests, subtests, and fuzz seeds: pass {summary['test_events']['pass']}, fail {summary['test_events']['fail']}, skip {summary['test_events']['skip']}. These events **must not** be conflated with independent features or network scenarios.
Full module statement coverage: **{summary['coverage_statement_percent']}%**; per-package/function details in [coverage-summary.log](coverage-summary.log). Statement coverage does not prove all scenarios.

Skipped items: {", ".join(event["Test"] for event in summary["skipped_tests"]) or "None"}. `TestICMPRealLoopback` runs only when raw socket permissions are available; explicitly skipped when unprivileged/unavailable. ICMP correlation, checksums, permission classification, and packet budgets remain verified by deterministic unit tests. Skipped items are never counted as real-network passes.

Environment: `{summary['go_version']}`; `{summary['host_platform']}`. Go module network disabled, zero external Go modules.
Race detector, 3x repeat tests, {len(fuzz)} fuzz suites (budget {fuzz_time} each), native build, and {len(targets)} cross-compilation targets are detailed in the table below. Bounded fuzzing is budgeted exploration, not proof of zero defects.

## Real Process and Terminal Acceptance

Independent collector x 2, worker, default credential-backed `topo`, Linux PTY, primary outage / backup read failover, worker expiry after stop, and restart journal recovery: **{smoke.get('status', 'not_run')}**, {len(smoke.get('checks', []))} process acceptance records.
Details in [process-smoke.json](process-smoke.json); all targets are local loopback. SSH subprocess checks use controlled argv/stdio fixtures, **not** live OpenSSH cryptographic handshakes.

## Verification Boundaries

Production Windows TUN, live proxy core configs, external proxy endpoints, live Tailscale/WireGuard tunnels, and remote user servers **NOT EXECUTED**. Cross-platform builds verify compilation only and do not constitute native OS runtime acceptance on Windows/macOS. For manual hardware verification steps, see `docs/ACCEPTANCE.md`. No production network was accessed or modified, and no credentials from memos were used. GitHub Actions workflows are provided as runnable templates but remote CI execution is not claimed.

Scenario-to-test mapping is documented in `docs/SCENARIOS.md`; itemized events are in [TEST-EVENTS.md](TEST-EVENTS.md). Actual verification commands and exit codes are recorded in [results.json](results.json).

## Command Log

| Check | Result | Exit Code | Seconds | Log |
|---|---|---|---|---|
""" + "\n".join(rows) + "\n"
    (output / "FINAL-TEST-REPORT.md").write_text(report, encoding="utf-8")
    print(json.dumps({"status": summary["status"], "failures": failures,
                      "coverage": summary["coverage_statement_percent"], "tests": summary["test_events"]}, ensure_ascii=False), flush=True)
    return 0 if not failures else 1


if __name__ == "__main__":
    raise SystemExit(main())
