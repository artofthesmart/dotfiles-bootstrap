"""Exercise the full bootstrap with isolated command stubs, never installing tools."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
STUB = r'''
import json
import os
from pathlib import Path
import sys

name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["CALL_LOG"], "a") as log:
    log.write(json.dumps([name] + args) + "\n")
if name == "uname":
    print(os.environ["TEST_PLATFORM"])
elif name == "python3":
    if args[:2] != ["-m", "venv"]:
        sys.exit("Unexpected Python command")
    python = Path(args[2]) / "bin/python"
    python.parent.mkdir(parents=True, exist_ok=True)
    python.write_text(Path(__file__).read_text())
    python.chmod(0o755)
elif name == "uv":
    sys.exit(int(os.environ.get("UV_EXIT", "0")))
elif name == "python":
    if args[:2] == ["-m", "pip"]:
        sys.exit(int(os.environ.get("PIP_EXIT", "0")))
    sys.exit(int(os.environ.get("SETUP_EXIT", "0")))
elif name == "curl":
    sys.exit("Unexpected network request")
'''


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bootstrap test ")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.bin = self.base / "bin"
        self.bin.mkdir()
        for name in ("uname", "python3", "uv", "brew", "apt-get", "sudo", "zsh", "chsh", "curl"):
            command = self.bin / name
            command.write_text(f"#!{sys.executable}\n" + STUB)
            command.chmod(0o755)
        shutil.copy(ROOT / "bootstrap.sh", self.base)
        (self.base / "setup.py").write_text("# Fixture: do not run real setup.\n")
        self.log = self.base / "calls.jsonl"
        self.env = dict(os.environ, HOME=str(self.base), XDG_CACHE_HOME=str(self.base / "cache"),
                        PATH=f"{self.bin}:/usr/bin:/bin", SHELL=str(self.bin / "zsh"),
                        BOOTSTRAP_AUTO="1", CALL_LOG=str(self.log), TEST_PLATFORM="Darwin",
                        UV_EXIT="0", PIP_EXIT="0", SETUP_EXIT="0")

    def run_bootstrap(self, **overrides):
        result = subprocess.run(["bash", "./bootstrap.sh"], cwd=self.base,
                                env=dict(self.env, **overrides), text=True,
                                capture_output=True, timeout=10)
        self.calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return result

    def test_uv_success_uses_isolated_python_without_pip_or_uv_run(self):
        result = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        venv = self.base / "cache/dotfiles-bootstrap/venv"
        self.assertIn(["brew", "install", "git", "python"], self.calls)
        self.assertIn(["python3", "-m", "venv", str(venv)], self.calls)
        self.assertIn(["uv", "pip", "install", "--python", str(venv / "bin/python"),
                       "rich", "questionary"], self.calls)
        self.assertEqual([call for call in self.calls if call[0] == "python"],
                         [["python", "setup.py"]])
        self.assertNotIn("Retrying", result.stdout)

    def test_uv_connection_failure_falls_back_to_pip_then_launches_once(self):
        result = self.run_bootstrap(UV_EXIT="1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("WARNING: uv could not install", result.stdout)
        self.assertEqual([call for call in self.calls if call[0] == "python"], [
            ["python", "-m", "pip", "install", "rich", "questionary"],
            ["python", "setup.py"],
        ])

    def test_both_downloaders_failing_never_launches_wizard(self):
        result = self.run_bootstrap(UV_EXIT="1", PIP_EXIT="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(["python", "setup.py"], self.calls)
        self.assertNotIn("Launching modern hybrid setup", result.stdout)
        self.assertIn("Both uv and pip failed", result.stdout)
        self.assertIn("files.pythonhosted.org", result.stdout)

    def test_wizard_failure_is_not_retried_as_dependency_failure(self):
        result = self.run_bootstrap(SETUP_EXIT="7")
        self.assertEqual(result.returncode, 7)
        self.assertEqual([call for call in self.calls if call[0] == "python"],
                         [["python", "setup.py"]])
        self.assertNotIn("Retrying", result.stdout)

    def test_linux_keeps_apt_path_and_supports_dependency_recovery(self):
        result = self.run_bootstrap(TEST_PLATFORM="Linux", UV_EXIT="1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(["sudo", "apt-get", "update", "-y"], self.calls)
        self.assertFalse(any(call[0] == "brew" for call in self.calls))
        self.assertIn(["python", "-m", "pip", "install", "rich", "questionary"], self.calls)
        self.assertIn(["python", "setup.py"], self.calls)


if __name__ == "__main__":
    unittest.main()
