"""Dependency-free, isolated Bash wizard tests; never install real applications."""
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import subprocess
import tempfile
import threading
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/setup-local-ai.sh"


class OllamaFixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.send_json({"version": "0.40.6"})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.requests.append((self.path, body))
        if self.server.mode == "http-error":
            self.send_error(500, "fixture failure")
            return
        if self.path == "/api/show":
            result = {"capabilities": ["completion", "tools"]}
            if self.server.mode == "no-tools":
                result["capabilities"] = ["completion"]
            if self.server.mode == "remote":
                result["remote_host"] = "https://example.invalid"
        else:
            result = {"done": True, "message": {"tool_calls": [
                {"function": {"name": "local_ai_check", "arguments": {"value": "ready"}}}
            ]}}
            if self.server.mode == "no-call":
                result["message"] = {"content": "hello"}
            if self.server.mode == "wrong-call":
                result["message"]["tool_calls"][0]["function"]["arguments"]["value"] = "wrong"
            if self.server.mode == "malformed":
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"not json")
                return
        self.send_json(result)

    def send_json(self, value):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(value).encode())


STUB = r'''#!/usr/bin/python3
import json, os, pathlib, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["CALL_LOG"], "a") as log:
    log.write(json.dumps([name] + args) + "\n")
if name == "id":
    print("1000")
elif name == "code":
    if "--version" in args:
        print(os.environ.get("CODE_VERSION", "1.127.0") + "\nfixture\nx64")
    elif "--list-extensions" in args:
        print("GitHub.copilot-chat")
        if not os.environ.get("MISSING_EXTENSION"):
            print("Ollama.ollama")
elif name == "ollama":
    if os.environ.get("FAIL_OLLAMA") == args[0]:
        sys.exit(1)
    print("fixture Ollama output")
elif name == "nvidia-smi":
    if os.environ.get("FAIL_NVIDIA"):
        sys.exit(1)
    print("Fixture GPU, 12288 MiB, 580.0")
elif name == "df":
    print("Filesystem 1024-blocks Used Available Capacity Mounted")
    print("/dev/fixture 100000000 1 " + os.environ.get("FREE_KIB", "90000000") + " 1% /")
elif name == "curl":
    for i, arg in enumerate(args):
        if arg.startswith("http://127.0.0.1:11434/"):
            if os.environ.get("FAIL_CHAT") and arg.endswith("/chat"):
                sys.exit(28)
            args[i] = arg.replace("127.0.0.1:11434", os.environ["FIXTURE_HOST"])
            sys.exit(subprocess.call([os.environ["REAL_CURL"]] + args))
    print("Unexpected external download blocked by test", file=sys.stderr)
    sys.exit(90)
elif name == "uname":
    print(os.environ.get("MOCK_OS", "Darwin") if args == ["-s"] else os.environ.get("MOCK_ARCH", "x86_64"))
elif name == "sw_vers":
    print(os.environ.get("MAC_VERSION", "14.6"))
elif name == "sysctl":
    if "hw.optional.arm64" in args:
        print(os.environ.get("APPLE_SILICON", "0"))
    elif "hw.memsize" in args:
        print("17179869184")
    else:
        print("Fixture CPU")
'''


@unittest.skipUnless(shutil.which("jq") and shutil.which("curl"), "jq and curl required for tests")
class WizardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="local-ai-tests-")
        self.base = Path(self.temp.name)
        self.home = self.base / "home"
        self.home.mkdir()
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.log = self.base / "calls.jsonl"
        self.log.touch()
        for name in ("id", "code", "ollama", "curl", "df", "nvidia-smi"):
            self.stub(name)
        self.repo = self.base / "repo with spaces"
        self.repo.mkdir()
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), OllamaFixture)
        self.server.mode = "ok"
        self.server.requests = []
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.env = dict(os.environ, HOME=str(self.home), PATH=f"{self.bin}:" + os.environ["PATH"],
                        CALL_LOG=str(self.log), FIXTURE_HOST=f"127.0.0.1:{self.server.server_port}",
                        REAL_CURL=shutil.which("curl"), DISPLAY=":fixture", OLLAMA_HOST="https://not-used.invalid")
        self.source = "source " + shlex.quote(str(SCRIPT)) + "; "
        self.state = self.home / ".local/share/local-ai-setup"
        self.state.mkdir(parents=True)
        self.prompt = self.home / "Documents/Local-AI-Gemini-Prompt.txt"
        self.prefix = self.source + (
            f'OS=Linux; ARCH=amd64; OS_VERSION=fixture; STATE={shlex.quote(str(self.state))}; '
            f'WORK={shlex.quote(str(self.base / "work"))}; mkdir -p "$WORK"; '
            f'MODEL_VOLUME="$HOME"; CODE={shlex.quote(str(self.bin / "code"))}; '
            f'OLLAMA={shlex.quote(str(self.bin / "ollama"))}; JQ={shlex.quote(shutil.which("jq"))}; '
        )

    def stub(self, name):
        path = self.bin / name
        path.write_text(STUB)
        path.chmod(0o700)

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def run_shell(self, code, answers="", prefix=True):
        return subprocess.run(["bash", "-c", (self.prefix if prefix else self.source) + code],
                              input=answers, text=True, capture_output=True, env=self.env, timeout=15)

    def calls(self, name):
        return [row for row in map(json.loads, self.log.read_text().splitlines()) if row[0] == name]

    def test_model_names_reject_commands_urls_namespaces_and_cloud(self):
        for model in ["model;touch bad", "$(id)", "--help", "a/b", "https://a", "x:cloud", "CLOUD-model", ""]:
            with self.subTest(model=model):
                result = self.run_shell("valid_model " + shlex.quote(model))
                self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.run_shell("valid_model model:Q4_K_M").returncode, 0)

    def test_noninteractive_launch_refused(self):
        result = subprocess.run(["bash", str(SCRIPT)], input="", text=True, capture_output=True, env=self.env)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("interactive terminal", result.stderr)
        self.assertEqual(self.calls("ollama"), [])

    def test_model_acquisition_and_actual_tool_call(self):
        result = self.run_shell("choose_model; check_model", "1\nmodel:q4\n4\n8192\ny\n")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(["ollama", "pull", "model:q4"], self.calls("ollama"))
        chat = next(body for path, body in self.server.requests if path == "/api/chat")
        self.assertEqual(chat["model"], "local-ai-model-q4")
        self.assertEqual(chat["options"]["num_ctx"], 8192)
        self.assertIn("tool-call check passed", result.stdout)
        self.assertIn("https://ollama.com/library", result.stdout)
        self.assertIn("You do not need to download it in the Ollama app", result.stdout)
        self.assertIn("this wizard downloads it after you confirm", result.stdout)
        self.assertIn("PARAMETER num_ctx 8192", (self.base / "work/Modelfile").read_text())

    def test_gguf_import_with_spaces(self):
        gguf = self.base / "weights with spaces.gguf"
        gguf.write_bytes(b"GGUFfixture")
        result = self.run_shell("choose_model", f"2\n{gguf}\nimported\n4\n\ny\n")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.calls("ollama")[0][1:3], ["create", "imported"])
        self.assertNotIn("pull", [row[1] for row in self.calls("ollama")])

    def test_invalid_gguf_rejected(self):
        gguf = self.base / "bad.gguf"
        gguf.write_text("not a GGUF")
        result = self.run_shell("choose_model", f"2\n{gguf}\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a GGUF", result.stderr)
        self.assertEqual(self.calls("ollama"), [])

    def test_low_disk_rejected_before_download(self):
        self.env["FREE_KIB"] = "1048576"
        result = self.run_shell("choose_model", "1\nmodel\n4\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Not enough", result.stderr)
        self.assertEqual(self.calls("ollama"), [])

    def test_cancel_and_eof_do_not_download(self):
        for answers in ["q\n", ""]:
            result = self.run_shell("choose_model", answers)
            self.assertEqual(result.returncode, 0 if answers else 1)
        self.assertEqual(self.calls("ollama"), [])

    def test_context_and_size_validation(self):
        for answers in ["1\nmodel\nbad\n", "1\nmodel\n1\n999999999999999999999\n", "1\nmodel\n1\n0\n"]:
            result = self.run_shell("choose_model", answers)
            self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls("ollama"), [])
        result = self.run_shell("choose_model; check_model", "1\nmodel\n1\n008192\ny\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        chat = next(body for path, body in self.server.requests if path == "/api/chat")
        self.assertEqual(chat["options"]["num_ctx"], 8192)

    def test_remote_and_non_tool_models_rejected(self):
        for mode, message in [("remote", "cloud inference"), ("no-tools", "advertise tools")]:
            with self.subTest(mode=mode):
                self.server.mode = mode
                result = self.run_shell("choose_model", "1\nmodel\n1\n\ny\n")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)

    def test_failure_responses_never_claim_tool_success(self):
        for mode in ["no-call", "wrong-call", "malformed", "http-error"]:
            with self.subTest(mode=mode):
                self.server.mode = mode
                result = self.run_shell('LOCAL_MODEL=model; CONTEXT=8192; check_model')
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("check passed", result.stdout)
        self.env["FAIL_CHAT"] = "1"
        result = self.run_shell('LOCAL_MODEL=model; CONTEXT=8192; check_model')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timed out", result.stderr)

    def test_pull_failure_stops_before_profile_creation(self):
        self.env["FAIL_OLLAMA"] = "pull"
        result = self.run_shell("choose_model; connect_code", "1\nmodel\n1\n\ny\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Download failed", result.stderr)
        self.assertEqual(self.calls("code"), [])

    def test_hardware_prompt_allowlist_and_unknown_memory(self):
        self.env["FAIL_NVIDIA"] = "1"
        result = self.run_shell("hardware_prompt", "\nn\n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        prompt = self.prompt.read_text()
        for forbidden in [str(self.home), str(self.repo), os.environ.get("USER", "root"), "not-used.invalid"]:
            self.assertNotIn(forbidden, prompt)
        self.assertIn("GPU memory: unknown", prompt)
        self.assertIn("TOOL CALLING", prompt)
        self.assertIn("nvidia-smi failed", result.stdout)

    def test_documents_location_respects_redirect_and_disabled_xdg(self):
        redirected = self.home / "My Documents"
        result = self.run_shell(
            'xdg-user-dir() { printf "%s" "$HOME/My Documents"; }; prompt_path')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, str(redirected / "Local-AI-Gemini-Prompt.txt"))
        self.assertTrue(redirected.is_dir())
        result = self.run_shell('xdg-user-dir() { printf "%s" "$HOME"; }; prompt_path')
        self.assertEqual(result.stdout, str(self.prompt))
    def test_apple_translation_and_intel_cpu_warning(self):
        for name in ["uname", "sw_vers", "sysctl"]:
            self.stub(name)
        self.env["APPLE_SILICON"] = "1"
        result = self.run_shell('detect_platform; printf "%s" "$ARCH"')
        self.assertEqual(result.stdout, "arm64")
        self.env["APPLE_SILICON"] = "0"
        result = self.run_shell("detect_platform; hardware_prompt", "\nn\n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("CPU-only", result.stdout)
        self.assertIn("CPU only", self.prompt.read_text())

    def test_unsupported_os_arch_and_macos_version(self):
        for name in ["uname", "sw_vers", "sysctl"]:
            self.stub(name)
        for values in [{"MOCK_OS": "FreeBSD"}, {"MOCK_ARCH": "i686"}, {"MAC_VERSION": "13.7"}]:
            with self.subTest(values=values):
                env_before = self.env.copy()
                self.env.update(values)
                self.assertNotEqual(self.run_shell("detect_platform").returncode, 0)
                self.env = env_before

    def test_old_code_version_and_missing_extensions(self):
        self.env["CODE_VERSION"] = "1.126.9"
        result = self.run_shell("ensure_code")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("too old", result.stderr)
        self.env["MISSING_EXTENSION"] = "1"
        result = self.run_shell("connect_code", "y\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not installed", result.stderr)

    def test_existing_apps_outside_path_and_private_jq(self):
        self.env["PATH"] = "/usr/bin:/bin"
        for app, resource in [
            ("Visual Studio Code", "Resources/app/bin/code"),
            ("Ollama", "Resources/ollama"),
        ]:
            path = self.home / f"Applications/{app}.app/Contents/{resource}"
            path.parent.mkdir(parents=True)
            path.write_text("#!/bin/sh\nexit 0\n")
            path.chmod(0o700)
        result = self.run_shell('OS=Darwin; find_code; find_ollama; printf "%s\\n%s" "$CODE" "$OLLAMA"')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Visual Studio Code.app", result.stdout)
        self.assertIn("Ollama.app", result.stdout)

    def test_cancel_install_and_failed_elevation(self):
        result = self.run_shell('find_code() { CODE=; }; ensure_code', "n\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("VS Code is required", result.stderr)
        result = self.run_shell('find_code() { CODE=; }; download() { touch "$2"; }; sudo() { return 1; }; ensure_code', "y\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls("code"), [])

    def test_missing_json_helper_checksum_and_download_failure(self):
        # Force only the dependency lookup to fail; do not hide host utilities.
        lookup = 'command() { if [ "$*" = "-v jq" ]; then return 1; else builtin command "$@"; fi; }; '
        result = self.run_shell(lookup + 'ensure_jq', "n\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("JSON reader is required", result.stderr)
        result = self.run_shell(lookup + 'download() { printf bad > "$2"; }; ensure_jq', "y\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)
        self.assertFalse((self.state / "bin/jq").exists())
        result = self.run_shell('find_code() { CODE=; }; download() { return 22; }; ensure_code', "y\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls("code"), [])

    def test_mac_download_targets_and_minimum_version_boundary(self):
        for arch, target in [("amd64", "/darwin/stable"), ("arm64", "/darwin-arm64/stable")]:
            command = (
                f'OS=Darwin; ARCH={arch}; '
                'find_code() { CODE="${FOUND_CODE:-}"; }; '
                'download() { printf "%s" "$1" > "$WORK/url"; }; '
                'open() { :; }; '
                f'ask() {{ ANSWER=y; FOUND_CODE={shlex.quote(str(self.bin / "code"))}; }}; '
                'ensure_code'
            )
            result = self.run_shell(command)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue((self.base / "work/url").read_text().endswith(target))
        self.env["CODE_VERSION"] = "1.127.0"
        self.assertEqual(self.run_shell("ensure_code").returncode, 0)

    def test_profile_scope_repo_spaces_and_rerun_preserves_settings(self):
        settings = self.home / ".config/Code/User/settings.json"
        settings.parent.mkdir(parents=True)
        settings.write_text('{"keep": "unchanged"}')
        for _ in range(2):
            result = self.run_shell("LOCAL_MODEL=model; connect_code", f"y\n{self.repo}\n")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(settings.read_text(), '{"keep": "unchanged"}')
        for row in self.calls("code"):
            self.assertEqual(row[row.index("--profile") + 1], "Local AI")
        self.assertIn(str(self.repo), self.calls("code")[-1])

    def test_full_interactive_flow_with_fake_api(self):
        answers = f"\n\nn\n\n1\nmodel:q4\n4\n8192\ny\ny\n{self.repo}\n"
        master, slave = pty.openpty()
        # Platform detection has its own tests; no host-specific install paths.
        command = self.source + 'detect_platform() { OS=Linux; ARCH=amd64; OS_VERSION=fixture; }; main'
        process = subprocess.Popen(["bash", "-c", command], stdin=slave, stdout=slave, stderr=slave,
                                   env=self.env, close_fds=True)
        os.close(slave)
        os.write(master, answers.encode())
        output = b""
        deadline = time.monotonic() + 15
        try:
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        output += os.read(master, 65536)
                    except OSError:
                        break
                if process.poll() is not None:
                    break
            process.wait(timeout=2)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)
        self.assertEqual(process.returncode, 0, output.decode(errors="replace"))
        self.assertIn(b"VS Code Agent behavior still needs your confirmation", output)
        for row in self.calls("code"):
            if "--install-extension" in row:
                self.assertIn("Local AI", row)
        self.assertTrue(self.prompt.exists())


if __name__ == "__main__":
    unittest.main()
