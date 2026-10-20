"""Exercise the real PowerShell download loop against a local HTTP fixture."""
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest


PWSH = os.environ.get("PWSH") or shutil.which("pwsh")
SCRIPT = Path(__file__).resolve().parents[1] / "scripts/setup-local-ai.ps1"
PAYLOAD = b"installer fixture " * 65536


class DownloadFixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path == "/error":
            self.send_error(503, "Fixture server unavailable")
            return
        self.send_response(200)
        if self.path != "/unknown":
            self.send_header("Content-Length", str(len(PAYLOAD)))
        self.end_headers()
        self.wfile.write(PAYLOAD[:10] if self.path == "/truncated" else PAYLOAD)


@unittest.skipUnless(PWSH, "PowerShell required: set PWSH or install pwsh")
class WindowsDownloadTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), DownloadFixture)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="local-ai-download-test-")
        self.base = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def download(self, route="/known", uri="https://fixture.invalid/installer"):
        test = self.base / "download.ps1"
        test.write_text(r"""
. $env:SETUP_SCRIPT
$script:Updates = [Collections.Generic.List[object]]::new()
function Write-Progress {
    param($Id, $Activity, $Status, $PercentComplete, [switch]$Completed)
    $script:Updates.Add(@{
        Activity = $Activity; Status = $Status
        Percent = $PercentComplete; Completed = $Completed.IsPresent
    })
}
# Override only request creation; production file IO, transfer and UI logic run.
function New-DownloadRequest([uri]$Uri) {
    $request = [Net.WebRequest]::Create($env:FIXTURE_URL)
    $request.Proxy = $null
    $request.Timeout = 3000
    $request.ReadWriteTimeout = 3000
    return $request
}
# The local fixture is HTTP; redirect security is unit-tested separately.
function Assert-HttpsResponse($Response) {}
$exitCode = 0
try {
    Save-Download $env:DOWNLOAD_URI $env:DESTINATION 'Ollama'
} catch {
    Write-Host "FAILED: $($_.Exception.Message)"
    $exitCode = 1
} finally {
    ConvertTo-Json -Depth 5 -InputObject @($script:Updates.ToArray()) |
        Set-Content -LiteralPath $env:PROGRESS_LOG
}
exit $exitCode
""")
        self.destination = self.base / "Ollama installer.exe"
        self.progress = self.base / "progress.json"
        env = dict(os.environ, SETUP_SCRIPT=str(SCRIPT), DOWNLOAD_URI=uri,
                   FIXTURE_URL=f"http://127.0.0.1:{self.server.server_port}{route}",
                   DESTINATION=str(self.destination), PROGRESS_LOG=str(self.progress))
        result = subprocess.run([PWSH, "-NoProfile", "-File", str(test)],
                                env=env, text=True, capture_output=True, timeout=15)
        self.updates = json.loads(self.progress.read_text(encoding="utf-8-sig"))
        return result

    def test_known_size_readable_progress_and_exact_file(self):
        result = self.download()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertIn("Download size: 1.1 MB", result.stdout)
        self.assertIn("Downloaded Ollama", result.stdout)
        self.assertTrue(any("MB of 1.1 MB" in (u["Status"] or "") for u in self.updates))
        self.assertTrue(any("/s" in (u["Status"] or "") for u in self.updates))
        self.assertEqual(self.updates[-1]["Activity"], "Downloading Ollama")
        self.assertTrue(self.updates[-1]["Completed"])
        self.assertNotIn("Writing request stream", result.stdout)

    def test_unknown_size_shows_downloaded_amount_without_fake_percentage(self):
        result = self.download("/unknown")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertIn("did not report a total size", result.stdout)
        updates = [u for u in self.updates if "/s" in (u["Status"] or "")]
        self.assertTrue(updates)
        self.assertTrue(all(u["Percent"] == -1 for u in updates))
        self.assertTrue(all("downloaded" in u["Status"] for u in updates))

    def test_truncated_transfer_removes_partial_file_and_never_reports_success(self):
        result = self.download("/truncated")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())
        self.assertNotIn("Downloaded Ollama", result.stdout)
        self.assertTrue(self.updates[-1]["Completed"])

    def test_http_failure_clears_progress_without_success(self):
        result = self.download("/error")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())
        self.assertIn("503", result.stdout)
        self.assertNotIn("Downloaded Ollama", result.stdout)
        self.assertTrue(self.updates[-1]["Completed"])

    def test_non_https_installer_source_rejected(self):
        result = self.download(uri="http://fixture.invalid/installer")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must use HTTPS", result.stdout)
        self.assertFalse(self.destination.exists())


if __name__ == "__main__":
    unittest.main()
