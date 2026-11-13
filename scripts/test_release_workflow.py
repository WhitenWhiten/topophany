"""Exercise the actual publication argument selection without publishing."""
import hashlib
import io
import json
import os
import shutil
import tarfile
import tempfile
import zipfile
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64", "windows_arm64")


def archive_fixtures(dist, version="1.2.3", poison=None):
    dist.mkdir(exist_ok=True)
    for target in TARGETS:
        name = f"topophany_{version}_{target}"
        windows = target.startswith("windows")
        members = {file: b"fixture" for file in ("LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md", "topo.exe" if windows else "topo")}
        if poison == "extra-member":
            members["unexpected.exe"] = b"poison"
        if poison == "traversal":
            members["../escape"] = b"poison"
        if windows:
            with zipfile.ZipFile(dist / (name + ".zip"), "w") as archive:
                for file, body in members.items():
                    info = zipfile.ZipInfo(name + "/" + file)
                    info.external_attr = (0o120777 if poison == "symlink" else 0o100644) << 16
                    archive.writestr(info, body)
        else:
            with tarfile.open(dist / (name + ".tar.gz"), "w:gz") as archive:
                for file, body in members.items():
                    info = tarfile.TarInfo(name + "/" + file)
                    info.size = len(body)
                    if poison == "symlink":
                        info.type, info.linkname, info.size = tarfile.SYMTYPE, "/outside", 0
                    archive.addfile(info, io.BytesIO(body))
    (dist / "CHANGELOG.md").write_text("fixture changelog\n")
    (dist / "RELEASE_NOTES.md").write_text("fixture notes\n\nSource commit: expected\n")
    if poison == "extra-file":
        (dist / "surprise.zip").write_bytes(b"poison")
    if poison == "missing-file":
        (dist / "topophany_1.2.3_linux_amd64.tar.gz").unlink()
    (dist / "SHA256SUMS.txt").write_text("".join(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n" for p in sorted(dist.iterdir())))


def workflow_script(job):
    source = (ROOT / ".github/workflows/release.yml").read_text()
    if job == "publish":
        source = source.split("\n  publish:\n")[1]
    return "\n".join(line[10:] for line in source.split("        run: |\n")[1].split("\n      - uses:")[0].splitlines())


class ReleaseArtifactIntegrity(unittest.TestCase):
    def test_publish_rejects_unexpected_files_and_members(self):
        script = workflow_script("publish").split('          object=')[0]
        # Stop immediately before the first GitHub API call.
        script = script.split('object=$(gh api')[0]
        for poison in (None, "extra-file", "missing-file", "extra-member", "traversal", "symlink", "manifest-path", "manifest-duplicate"):
            with self.subTest(poison=poison), tempfile.TemporaryDirectory() as tmp:
                dist = Path(tmp) / "dist"
                archive_fixtures(dist, poison=poison)
                if poison == "manifest-path":
                    with (dist / "SHA256SUMS.txt").open("a") as out:
                        out.write("0" * 64 + "  ../outside\n")
                if poison == "manifest-duplicate":
                    manifest = dist / "SHA256SUMS.txt"
                    manifest.write_text(manifest.read_text() + manifest.read_text().splitlines()[0] + "\n")
                result = subprocess.run(["bash", "-c", script], cwd=tmp, env={**os.environ, "VERSION": "v1.2.3"}, capture_output=True)
                self.assertEqual(result.returncode == 0, poison is None, result.stderr.decode())

    def test_build_uses_fresh_outputs_and_rejects_tracked_dist(self):
        for tracked in (False, True):
            with self.subTest(tracked=tracked), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                checkout = root / "checkout"
                checkout.mkdir()
                subprocess.run(["git", "init", "-q", str(checkout)], check=True)
                for name in ("LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md"):
                    (checkout / name).write_text("fixture\n")
                (checkout / "docs/releases").mkdir(parents=True)
                (checkout / "docs/releases/v1.2.3.md").write_text("release\n")
                (checkout / "scripts").mkdir()
                shutil.copyfile(ROOT / "scripts/release_metadata.py", checkout / "scripts/release_metadata.py")
                (checkout / ".gitignore").write_text("dist/\n")
                archive_fixtures(checkout / "dist", poison="extra-member")
                if tracked:
                    subprocess.run(["git", "add", "-f", "dist"], cwd=checkout, check=True)
                subprocess.run(["git", "add", "."], cwd=checkout, check=True)
                subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], cwd=checkout, check=True)
                revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip()
                runner = root / "runner"
                runner.mkdir()
                tools = root / "tools"
                tools.mkdir()
                # Only compilation is stubbed; Git, staging, zip, tar and checksum
                # construction execute the actual workflow against synthetic data.
                go = tools / "go"
                go.write_text('''#!/bin/bash
set -eu
while [ "$1" != -o ]; do shift; done
printf '#!/bin/bash\\necho "Topophany 1.2.3"\\n' > "$2"
chmod +x "$2"
''')
                go.chmod(0o755)
                output = root / "outputs"
                env = {**os.environ, "COPYFILE_DISABLE": "1", "PATH": str(tools) + os.pathsep + os.environ["PATH"], "VERSION": "v1.2.3", "SOURCE_SHA": revision, "RUNNER_TEMP": str(runner), "GITHUB_WORKSPACE": str(checkout), "GITHUB_OUTPUT": str(output)}
                result = subprocess.run(["bash", "-c", workflow_script("build")], cwd=checkout, env=env, capture_output=True)
                self.assertEqual(result.returncode == 0, not tracked, result.stderr.decode())
                if tracked:
                    self.assertFalse(output.exists())
                    continue
                dist = Path(output.read_text().strip().removeprefix("directory="))
                self.assertTrue(dist.is_relative_to(runner))
                self.assertFalse(dist.is_relative_to(checkout))
                with zipfile.ZipFile(dist / "topophany_1.2.3_windows_amd64.zip") as archive:
                    self.assertFalse(any("unexpected" in name for name in archive.namelist()))
                validate = workflow_script("publish").split('object=$(gh api')[0]
                result = subprocess.run(["bash", "-c", validate.replace("cd dist", 'cd "$ARTIFACT_DIR"')], cwd=root, env={**env, "ARTIFACT_DIR": str(dist)}, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr.decode())


class ReleaseFlags(unittest.TestCase):
    def test_actual_workflow_flags(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/release.yml").read_text()
        start = workflow.index("          release_flags=(--verify-tag)")
        end = workflow.index('          gh release create "$VERSION"', start)
        snippet = "\n".join(line[10:] for line in workflow[start:end].splitlines())
        # Print arguments exactly as gh would receive them, including the existing tag verification
        # for stable versions. No credentials or network command are available.
        script = 'set -euo pipefail\n' + snippet + '\nprintf "%s\\n" "${release_flags[@]}"'
        for tag, expected in [("v1.2.0", ["--verify-tag"]), ("v1.2.0-rc.1", ["--verify-tag", "--prerelease", "--latest=false"]),
                              ("v1.2.0-beta.2", ["--verify-tag", "--prerelease", "--latest=false"])]:
            with self.subTest(tag=tag):
                result = subprocess.run(["bash", "-c", script], env={**os.environ, "VERSION": tag},
                                        text=True, capture_output=True, check=True)
                self.assertEqual([x for x in result.stdout.splitlines() if x], expected)
        result = subprocess.run(["bash", "-c", script], env={**os.environ, "VERSION": "v1.2.0", "MANAGED_RELEASES": "true"}, text=True, capture_output=True, check=True)
        self.assertEqual(result.stdout.splitlines(), ["--verify-tag", "--draft"])
        self.assertIn('--notes-file ./RELEASE_NOTES.md "${release_flags[@]}"', workflow)
        self.assertIn('[[ "$kind" == commit && "$sha" == "$SOURCE_SHA" ]]', workflow)


class ManagedPromotion(unittest.TestCase):
    def test_publication_rechecks_receipt_without_executing_release_code(self):
        source = (ROOT / ".github/workflows/managed-release.yml").read_text()
        verify, publish = source.split("\n  publish:\n")
        self.assertIn("--signer-workflow", verify)
        self.assertIn("--source-digest", verify)
        self.assertIn("--deny-self-hosted-runners", verify)
        self.assertNotIn("contents: write", verify)
        for forbidden in ("actions/checkout", "go build", "go test", "./topo", "bash receipt"):
            self.assertNotIn(forbidden, publish)
        self.assertIn("environment: release", publish)
        script = "\n".join(line[10:] for line in publish.split('          python3 - "$VERSION" <<\'PY\'\n')[1].split("          PY\n")[0].splitlines())
        asset = {"id": 8, "name": "topophany_1.7.1_linux_arm64.tar.gz", "size": 42, "digest": "sha256:"+"a"*64, "state": "uploaded"}
        for poison in (None, "tag", "id", "digest", "draft", "prerelease", "extra"):
            with self.subTest(poison=poison), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / "receipt").mkdir()
                receipt = {"schema": 1, "version": "v1.7.1", "release_id": 11, "assets": [asset]}
                current = {"tag_name": "v1.7.1", "id": 11, "assets": [dict(asset)], "draft": True, "prerelease": False}
                if poison == "tag": current["tag_name"] = "v1.7.2"
                if poison == "id": current["id"] += 1
                if poison == "digest": current["assets"][0]["digest"] = "sha256:"+"b"*64
                if poison == "draft": current["draft"] = False
                if poison == "prerelease": current["prerelease"] = True
                if poison == "extra": current["assets"].append({**asset, "id": 9, "name": "install.sh"})
                (root / "receipt/managed-release-receipt.json").write_text(json.dumps(receipt))
                (root / "current.json").write_text(json.dumps(current))
                result = subprocess.run(["python3", "-c", script, "v1.7.1"], cwd=root, capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, poison is None, result.stderr)


if __name__ == "__main__":
    unittest.main()
