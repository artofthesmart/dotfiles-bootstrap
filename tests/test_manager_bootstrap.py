import hashlib
import http.server
import io
import json
import os
import shutil
import ssl
import subprocess
import tarfile
import tempfile
import threading
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SANDBOXES = ROOT / "build" / "test-sandboxes"


def make_tar(name="release/local-ai", body=None):
    if body is None:
        body = b"""#!/bin/sh
case "${1:-}" in
  version) echo "local-ai 1.0.0";;
  setup) echo "setup complete";;
  *) exit 2;;
esac
"""
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        entry = tarfile.TarInfo(name)
        entry.mode = 0o755
        entry.size = len(body)
        archive.addfile(entry, io.BytesIO(body))
    return output.getvalue()


class FixtureHandler(http.server.BaseHTTPRequestHandler):
    files = {}

    def do_GET(self):
        data = self.files.get(self.path)
        if data is None:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_args):
        pass


class ManagerBootstrapTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not shutil.which("openssl") or not shutil.which("curl"):
            raise unittest.SkipTest("openssl and curl are required")
        SANDBOXES.mkdir(parents=True, exist_ok=True)
        cls.temp = tempfile.TemporaryDirectory(dir=SANDBOXES)
        cls.root = Path(cls.temp.name)
        cls.cert = cls.root / "cert.pem"
        cls.key = cls.root / "key.pem"
        subprocess.run(
            [
                "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                "-keyout", str(cls.key), "-out", str(cls.cert), "-days", "1",
                "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        cls.server = http.server.ThreadingHTTPServer(("localhost", 0), FixtureHandler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cls.cert, cls.key)
        cls.server.socket = context.wrap_socket(cls.server.socket, server_side=True)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()
        cls.server.server_close()
        cls.temp.cleanup()

    def run_case(self, archive, checksum=None):
        asset = "local-ai_1.0.0_linux_amd64.tar.gz"
        if checksum is None:
            checksum = hashlib.sha256(archive).hexdigest()
        base = f"https://localhost:{self.server.server_port}"
        release = {
            "tag_name": "v1.0.0",
            "draft": False,
            "prerelease": False,
            "assets": [
                {
                    "name": asset,
                    "size": len(archive),
                    "browser_download_url": f"{base}/asset",
                },
                {
                    "name": "SHA256SUMS",
                    "size": 80,
                    "browser_download_url": f"{base}/sums",
                },
            ],
        }
        FixtureHandler.files = {
            "/repos/owner/repo/releases/latest": json.dumps(release).encode(),
            "/asset": archive,
            "/sums": f"{checksum}  {asset}\n".encode(),
        }
        case = Path(tempfile.mkdtemp(dir=self.root))
        self.addCleanup(shutil.rmtree, case, True)
        home = case / "home"
        cache = case / "cache"
        home.mkdir()
        cache.mkdir()
        environment = os.environ.copy()
        environment.update(
            {
                "HOME": str(home),
                "XDG_CACHE_HOME": str(cache),
                "LOCAL_AI_REPOSITORY": "owner/repo",
                "LOCAL_AI_GITHUB_API_BASE": base,
                "CURL_CA_BUNDLE": str(self.cert),
            }
        )
        result = subprocess.run(
            ["sh", str(ROOT / "install.sh")],
            cwd=ROOT,
            env=environment,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        return result, home / ".local" / "bin" / "local-ai"

    def test_valid_fixture_installs_and_runs_setup(self):
        result, target = self.run_case(make_tar())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue(target.is_file())
        self.assertIn("setup complete", result.stdout)

    def test_corrupt_checksum_is_rejected(self):
        result, target = self.run_case(make_tar(), checksum="0" * 64)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(target.exists())
        self.assertIn("SHA-256 verification failed", result.stderr)

    def test_truncated_archive_is_rejected(self):
        archive = make_tar()
        result, target = self.run_case(archive[: len(archive) // 2])
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(target.exists())

    def test_path_traversal_archive_is_rejected(self):
        result, target = self.run_case(make_tar("../local-ai"))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(target.exists())
        self.assertIn("unsafe archive path", result.stderr.lower())

    def test_bootstrap_contracts_cover_both_shells(self):
        shell = (ROOT / "install.sh").read_text(encoding="utf-8")
        powershell = (ROOT / "install.ps1").read_text(encoding="utf-8")
        for text in (shell, powershell):
            self.assertIn("SHA256SUMS", text)
            self.assertIn("https://", text)
            self.assertIn("attestation verify", text)
            self.assertIn("local-ai managed PATH", text)
            self.assertIn("setup", text)
        self.assertNotIn("spctl --master-disable", shell)
        self.assertNotIn("Set-MpPreference", powershell)
        self.assertIn("SmartScreen", powershell)


if __name__ == "__main__":
    unittest.main()
