"""Browser asset hashes must use the same Go toolchain locally and in CI."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("build_demo", Path(__file__).with_name("build_demo.py"))
build_demo = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_demo)


class BrowserToolchainTests(unittest.TestCase):
    def test_module_version_overrides_caller_toolchain(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/browser\n\ngo 1.26.10\n")
            caller = {"GOTOOLCHAIN": "local", "GOROOT": "/other/go", "PATH": "/bin"}
            with patch.object(build_demo, "ROOT", root), patch.dict(os.environ, caller, clear=True):
                env = build_demo.toolchain_env()
                self.assertEqual(env["GOTOOLCHAIN"], "go1.26.10")
                self.assertNotIn("GOROOT", env)
                self.assertEqual(env["PATH"], "/bin")
                self.assertEqual(os.environ["GOROOT"], "/other/go")
                (root / "go.mod").write_text("module example.test/browser\n\ngo 1.27.3\n")
                self.assertEqual(build_demo.toolchain_env()["GOTOOLCHAIN"], "go1.27.3")

    def test_selected_go_matches_module_version(self):
        env = build_demo.toolchain_env()
        version = subprocess.check_output(["go", "env", "GOVERSION"], env=env,
                                          cwd=build_demo.ROOT, text=True).strip()
        self.assertEqual(version, env["GOTOOLCHAIN"])


if __name__ == "__main__":
    unittest.main()
