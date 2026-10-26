#!/usr/bin/env python3
"""Export source ZIP/tar.gz, read every ZIP member, rebuild the actual ZIP copy.

Requires a successful scripts/verify.py run. Never packages files outside this
source root, binaries, runtime demo directories, or environment credentials.
Outputs truthful machine-readable checks and hashes; fails closed on any error.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

from source_identity import accepted, require_verified
from portable_paths import validate_paths

ROOT = Path(__file__).resolve().parents[1]


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def git(*args: str) -> bytes:
    return subprocess.check_output(["git", *args], cwd=ROOT)




def files_to_export(revision: str = "HEAD") -> dict[str, bytes]:
    """Read immutable Git blobs, never bytes from the physical worktree."""
    revision = git("rev-parse", "--verify", revision + "^{commit}").decode().strip()
    if git("status", "--porcelain", "--untracked-files=no").strip():
        raise RuntimeError("source export requires a clean tracked worktree and index")
    for flags in (("--exclude-standard",), ("--ignored", "--exclude-standard")):
        for name in git("ls-files", "--others", "-z", *flags).split(b"\0"):
            if name and accepted(Path(os.fsdecode(name))):
                raise RuntimeError("untracked or ignored source input: " + os.fsdecode(name))
    result = {}
    entries = [entry for entry in git("ls-tree", "-rz", "--full-tree", revision).split(b"\0") if entry]
    validate_paths(os.fsdecode(entry.split(b"\t", 1)[1]) for entry in entries)
    for entry in entries:
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        mode, kind, oid = meta.split()
        rel = Path(os.fsdecode(name))
        if not accepted(rel):
            continue
        if kind != b"blob" or mode not in (b"100644", b"100755"):
            raise RuntimeError("source contains a symlink or nonregular Git entry: " + str(rel))
        if rel.suffix.lower() in {".exe", ".pyc", ".zip", ".gz", ".env", ".pem", ".key", ".p12", ".pfx"} or rel.name.lower() in {".env", "topo", "auth.json", "credentials.json", "worker-credentials.json", "network-memo.md", "id_rsa", "id_ed25519"} or rel.name.lower().startswith(".env."):
            raise RuntimeError("runtime/private artifact in source revision: " + str(rel))
        result[rel.as_posix()] = git("cat-file", "blob", oid.decode())
    return result


def validate_archive_names(names: list[str], source_names) -> None:
    validate_paths(names)
    if set(names) != {"topophany/" + name for name in source_names}:
        raise RuntimeError("archive member manifest differs")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--name", default="Topophany-1.5.1-source")
    parser.add_argument("--verification", type=Path, default=ROOT / ".verify-build" / "verification",
                        help="output directory of a passing scripts/verify.py run")
    args = parser.parse_args()
    if Path(args.name).name != args.name or not args.name.startswith("Topophany-"):
        parser.error("name must be a Topophany- prefixed basename")
    output = args.output.resolve()
    if output == ROOT or ROOT in output.parents:
        parser.error("output must be outside the source tree")
    output.mkdir(parents=True, exist_ok=True)
    summary = json.loads((args.verification / "summary.json").read_text())
    if summary.get("status") != "pass" or summary.get("quality_gate_failures"):
        raise RuntimeError("refusing release: verification is missing or failing")
    revision = git("rev-parse", "--verify", "HEAD^{commit}").decode().strip()
    source_files = files_to_export(revision)
    validate_paths(source_files)
    source_sha = {name: hashlib.sha256(body).hexdigest() for name, body in source_files.items()}
    require_verified(summary, revision, source_sha)
    zip_path = output / (args.name + ".zip")
    tar_path = output / (args.name + ".tar.gz")
    with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for name, body in source_files.items():
            zf.writestr("topophany/" + name, body)
    with tarfile.open(tar_path, "w:gz", compresslevel=9) as tf:
        for name, body in source_files.items():
            info = tarfile.TarInfo("topophany/" + name)
            info.size = len(body)
            info.mode = 0o644
            tf.addfile(info, io.BytesIO(body))
    env = os.environ.copy()
    env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="4")
    rebuild_log = output / "Topophany-archive-rebuild.log"
    commands = []
    with tempfile.TemporaryDirectory(prefix="topophany-zip-validation-") as tmp:
        dest = Path(tmp)
        with zipfile.ZipFile(zip_path, "r") as zf:
            bad_member = zf.testzip()
            if bad_member:
                raise RuntimeError("ZIP CRC/read failure: " + bad_member)
            validate_archive_names(zf.namelist(), source_sha)
            zf.extractall(dest)
        extracted = dest / "topophany"
        for rel, expected in source_sha.items():
            if digest(extracted / rel) != expected:
                raise RuntimeError("extracted file differs: " + rel)
        with tarfile.open(tar_path, "r:gz") as tf:
            names = tf.getnames()
            validate_archive_names(names, source_sha)
            for member in tf:
                body = tf.extractfile(member)
                if not member.isfile() or body is None:
                    raise RuntimeError("nonregular tar member")
                with body:
                    if hashlib.sha256(body.read()).hexdigest() != source_sha[member.name[len("topophany/"):]]:
                        raise RuntimeError("tar content differs")
        with rebuild_log.open("w", encoding="utf-8") as log:
            for command in (["go", "test", "-count=1", "./..."],
                            ["go", "build", "-trimpath", "-o", str(dest / "topo"), "./cmd/topo"]):
                log.write("$ " + " ".join(command) + "\n")
                log.flush()
                result = subprocess.run(command, cwd=extracted, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=180)
                commands.append({"command": command, "exit_code": result.returncode})
                if result.returncode:
                    raise RuntimeError("actual ZIP source rebuild failed; see " + str(rebuild_log))
            log.write("ZIP CRC, all source SHA-256 matches, tar member/content validation and extracted-source tests/build: PASS\n")
    report_path = output / "Topophany-test-report.md"
    shutil.copyfile(args.verification / "FINAL-TEST-REPORT.md", report_path)
    with report_path.open("a", encoding="utf-8") as report:
        report.write("\n## Archive Rebuild Verification\n\nThe exported ZIP was verified per-member with CRC checks; all extracted file SHA-256 digests matched the source tree. The tar.gz archive content matched per-file as well. **Rebuilding from the extracted ZIP directory via `go test -count=1 ./...` and `go build` both returned 0.** For details, see Topophany-archive-rebuild.log and the delivery manifest included in this release.\n")
    manifest = {
        "schema": 1, "project": "Topophany", "version": "1.5.1",
        "exported_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "original_source_recovered": False,
        "verification": summary,
        "source_revision": revision, "source_files": len(source_files), "go_files": summary["go_files"], "go_test_files": summary["go_test_files"],
        "archive_member_integrity": "pass", "extracted_source_matches": True,
        "archive_rebuild_exit_code": 0, "archive_rebuild_commands": commands,
        "archives": [{"file": p.name, "bytes": p.stat().st_size, "sha256": digest(p)} for p in (zip_path, tar_path)],
        "source_sha256": source_sha,
        "excluded": ["user network memo", "user image", "production credentials", "runtime data", "compiled executables"],
    }
    manifest_path = output / "Topophany-delivery-manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    hash_paths = [zip_path, tar_path, report_path, rebuild_log, manifest_path]
    (output / "Topophany-SHA256SUMS.txt").write_text("".join(digest(p) + "  " + p.name + "\n" for p in hash_paths), encoding="utf-8")
    print(json.dumps({"status": "pass", "archives": manifest["archives"], "source_files": len(source_files), "archive_rebuild_exit_code": 0}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
