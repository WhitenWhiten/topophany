#!/usr/bin/env python3
"""Build the browser preview and copy the matching Go WebAssembly runtime.

The ignored binaries are built locally and before deploying the static site.
--check verifies their source fingerprint and content without rebuilding;
ordinary runs rebuild when stale. A fresh checkout must run this builder first.
Use the exact Go version from go.mod so committed asset hashes match CI.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "site/public/data"
RUNTIME = ROOT / "site/src/js/wasm_exec.js"
LICENSE = ROOT / "site/src/js/wasm_exec.LICENSE"


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def toolchain_env() -> dict[str, str]:
    version = re.search(r"^go ([0-9.]+)$", (ROOT / "go.mod").read_text(), re.MULTILINE)[1]
    env = {**os.environ, "GOTOOLCHAIN": "go" + version}
    # An exported GOROOT may point at the caller's different Go installation.
    env.pop("GOROOT", None)
    return env


def inputs(env: dict[str, str]) -> tuple[dict, bytes, bytes]:
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], cwd=ROOT, env=env, text=True).strip())
    version = subprocess.check_output(["go", "env", "GOVERSION"], cwd=ROOT, env=env, text=True).strip()
    runtime = (goroot / "lib/wasm/wasm_exec.js").read_bytes()
    license_text = (goroot / "LICENSE").read_bytes()
    paths = [ROOT / "go.mod", ROOT / "scripts/build_demo.py"]
    paths += list((ROOT / "cmd/topo-demo").glob("*.go"))
    paths += [p for p in (ROOT / "internal").rglob("*") if p.is_file()
              and (p.suffix in (".go", ".json", ".bin")) and not p.name.endswith("_test.go")]
    hashes = {p.relative_to(ROOT).as_posix(): sha(p.read_bytes()) for p in sorted(paths)}
    return {"go": version, "sources": hashes, "runtime": sha(runtime)}, runtime, license_text


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    env = toolchain_env()
    source, runtime, license_text = inputs(env)
    wasm, viewer, manifest = OUT / "demo.wasm", OUT / "viewer.wasm", OUT / "demo.build.json"
    try:
        previous = json.loads(manifest.read_text())
        current = (previous["inputs"] == source and previous["wasm"] == sha(wasm.read_bytes())
                   and previous["viewer"] == sha(viewer.read_bytes())
                   and RUNTIME.read_bytes() == runtime and LICENSE.read_bytes() == license_text)
    except (OSError, KeyError, ValueError):
        current = False
    if current:
        return 0
    if args.check:
        print("browser demo is stale; run make site")
        return 1
    env.update(GOOS="js", GOARCH="wasm", CGO_ENABLED="0")
    with tempfile.TemporaryDirectory(prefix="topophany-wasm-") as temp:
        binary = Path(temp) / "demo.wasm"
        binaries = {}
        for mode in ("demo", "viewer"):
            command = ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w"]
            if mode == "viewer":
                command += ["-tags=collector"]
            subprocess.run(command + ["-o", str(binary), "./cmd/topo-demo"], cwd=ROOT, env=env, check=True)
            binaries[mode] = binary.read_bytes()
    OUT.mkdir(parents=True, exist_ok=True)
    wasm.write_bytes(binaries["demo"])
    viewer.write_bytes(binaries["viewer"])
    # Retire the old artifact so an existing checkout does not publish both.
    (OUT / "collector.wasm").unlink(missing_ok=True)
    RUNTIME.write_bytes(runtime)
    LICENSE.write_bytes(license_text)
    manifest.write_text(json.dumps({"inputs": source, "wasm": sha(binaries["demo"]),
                                    "viewer": sha(binaries["viewer"])}, indent=2) + "\n")
    for mode, data in binaries.items():
        print(f"built browser {mode} ({len(data):,} bytes, {source['go']})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
