#!/usr/bin/env python3
"""Fetch a pinned official release; verify GitHub's SHA-256 before extraction."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import platform
import re
import urllib.request
import zipfile


def fetch(version, output):
    if not re.fullmatch(r"v\d+\.\d+\.\d+", version):
        raise ValueError("an explicit stable version is required")
    system = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}[platform.system()]
    machine = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "AMD64": "amd64"}[platform.machine()]
    suffix = "-v1" if machine == "amd64" else ""
    ext = ".zip" if system == "windows" else ".gz"
    name = f"mihomo-{system}-{machine}{suffix}-{version}{ext}"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def read(url, limit):
        with opener.open(urllib.request.Request(url, headers={"User-Agent": "Topophany-acceptance"}), timeout=60) as r:
            data = r.read(limit + 1)
        if len(data) > limit:
            raise ValueError("download exceeds budget")
        return data
    release = json.loads(read(f"https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/{version}", 2 << 20))
    asset = next(a for a in release["assets"] if a["name"] == name)
    data = read(asset["browser_download_url"], 64 << 20)
    digest = "sha256:" + hashlib.sha256(data).hexdigest()
    if asset.get("digest") != digest:
        raise ValueError("release digest missing or mismatched")
    if ext == ".zip":
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            files = [n for n in archive.namelist() if n.endswith(".exe")]
            if len(files) != 1:
                raise ValueError("ambiguous executable")
            binary = archive.read(files[0])
    else:
        binary = gzip.decompress(data)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(binary)
    output.chmod(0o700)
    output.with_suffix(".provenance.json").write_text(json.dumps({"version": version, "asset": name, "archive_digest": digest,
        "binary_sha256": hashlib.sha256(binary).hexdigest()}, indent=2) + "\n")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--version", required=True)
    p.add_argument("--output", type=Path, required=True)
    a = p.parse_args()
    fetch(a.version, a.output)
