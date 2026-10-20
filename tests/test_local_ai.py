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
            result = {"capabilities": ["completion", "tools"], "details": {"context_length": 131072}}
            if self.server.mode == "short-context":
                result["details"]["context_length"] = 32768
            if self.server.mode == "info-context":
                result.pop("details")
                result["model_info"] = {"general.architecture": "qwen2", "qwen2.context_length": 32768}
            if self.server.mode == "unknown-context":
                result.pop("details")
            if self.server.mode == "no-tools":
                result["capabilities"] = ["completion"]
            if self.server.mode == "remote":
                result["remote_host"] = "https://example.invalid"
        else:
            result = {"done": True, "message": {"tool_calls": [
                {"function": {"name": "get_temperature", "arguments": {"city": "New York"}}}
            ]}}
            if self.server.mode == "no-call":
                result["message"] = {"content": "hello"}
            if self.server.mode == "wrong-call":
                result["message"]["tool_calls"][0]["function"]["arguments"]["city"] = "wrong"
            if self.server.mode == "json-prose":
                result["message"] = {"content": '{"name":"get_temperature","arguments":{"city":"New York"}}'}
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
import json, os, pathlib, struct, subprocess, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["CALL_LOG"], "a") as log:
    log.write(json.dumps([name] + args) + "\n")
if name == "id":
    print("1000")
elif name == "code":
    profile = pathlib.Path(os.environ["PROFILE_STATE"])
    extension = pathlib.Path(os.environ["EXTENSION_STATE"])
    if "--version" in args:
        print(os.environ.get("CODE_VERSION", "1.127.0") + "\nfixture\nx64")
    elif "--new-window" in args and "--profile" in args:
        if not os.environ.get("FAIL_PROFILE_CREATE"):
            profile.touch()
    elif os.environ.get("FAIL_CODE") == "install" and "--install-extension" in args:
        print("fixture extension install failure", file=sys.stderr)
        sys.exit(9)
    elif "--install-extension" in args and "GitHub.copilot-chat" in args:
        print("built-in extension cannot be installed", file=sys.stderr)
        sys.exit(1)
    elif "--install-extension" in args:
        if not profile.exists():
            print("Profile 'Local AI' not found.", file=sys.stderr)
            sys.exit(1)
        extension.touch()
    elif "--list-extensions" in args:
        if not profile.exists():
            print("Profile 'Local AI' not found.", file=sys.stderr)
            sys.exit(1)
        if os.environ.get("FAIL_CODE") == "list" and extension.exists():
            print("fixture extension list failure", file=sys.stderr)
            sys.exit(9)
        if extension.exists() and not os.environ.get("MISSING_EXTENSION"):
            print("Ollama.ollama")
elif name == "ollama":
    if os.environ.get("FAIL_OLLAMA") == args[0]:
        sys.exit(1)
    if args and args[0] == "list":
        print("NAME ID SIZE MODIFIED")
        if os.environ.get("INSTALLED_MODEL"):
            print(os.environ["INSTALLED_MODEL"] + " abc123 4 GB now")
    else:
        print("fixture Ollama output")
elif name == "nvidia-smi":
    if os.environ.get("FAIL_NVIDIA"):
        sys.exit(1)
    print("Fixture GPU, 12288 MiB, 580.0")
elif name == "df":
    print("Filesystem 1024-blocks Used Available Capacity Mounted")
    print("/dev/fixture 100000000 1 " + os.environ.get("FREE_KIB", "90000000") + " 1% /")
elif name == "curl":
    url = next((arg for arg in args if arg.startswith("https://registry.ollama.ai/")), "")
    if url:
        if os.environ.get("FAIL_REGISTRY"):
            sys.exit(22)
        output = pathlib.Path(args[args.index("-o") + 1])
        if "/manifests/" in url:
            size = int(os.environ.get("REGISTRY_BYTES", str(4 * 1024**3)))
            output.write_text(json.dumps({"schemaVersion": 2, "layers": [
                {"mediaType": "application/vnd.ollama.image.model",
                 "digest": "sha256:" + "a" * 64, "size": size}
            ]}))
        else:
            context = int(os.environ.get("REGISTRY_CONTEXT", "131072"))
            output.write_bytes(b"GGUFfixture.qwen2.context_length" + struct.pack("<I", 4) + struct.pack("<I", context))
        sys.exit(0)
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
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), OllamaFixture)
        self.server.mode = "ok"
        self.server.requests = []
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.env = dict(os.environ, HOME=str(self.home), PATH=f"{self.bin}:" + os.environ["PATH"],
                        CALL_LOG=str(self.log), FIXTURE_HOST=f"127.0.0.1:{self.server.server_port}",
                        REAL_CURL=shutil.which("curl"), DISPLAY=":fixture", OLLAMA_HOST="https://not-used.invalid")
        self.env["PROFILE_STATE"] = str(self.base / "profile-created")
        self.env["EXTENSION_STATE"] = str(self.base / "ollama-extension-installed")
        self.source = "source " + shlex.quote(str(SCRIPT)) + "; "
        self.state = self.home / ".local/share/local-ai-setup"
        self.state.mkdir(parents=True)
        self.prompt = self.home / "Documents/Local-AI-Gemini-Prompt.txt"
        self.prefix = self.source + (
            f'OS=Linux; ARCH=amd64; OS_VERSION=fixture; STATE={shlex.quote(str(self.state))}; '
            f'WORK={shlex.quote(str(self.base / "work"))}; mkdir -p "$WORK"; '
            f'MODEL_VOLUME="$HOME"; CODE={shlex.quote(str(self.bin / "code"))}; '
            f'OLLAMA={shlex.quote(str(self.bin / "ollama"))}; JQ={shlex.quote(shutil.which("jq"))}; '
            'VERIFIED_CANDIDATES_FILE="$WORK/verified-candidates.tsv"; '
            'printf "model:latest\\t%s\\t%s\\nmodel:q4\\t%s\\t%s\\n" '
            '"${REGISTRY_BYTES:-4294967296}" "${REGISTRY_CONTEXT:-131072}" '
            '"${REGISTRY_BYTES:-4294967296}" "${REGISTRY_CONTEXT:-131072}" > "$VERIFIED_CANDIDATES_FILE"; '
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
        result = self.run_shell("choose_model; check_model", "model:q4\n4\n65536\ny\n")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(["ollama", "pull", "model:q4"], self.calls("ollama"))
        chat = next(body for path, body in self.server.requests if path == "/api/chat")
        self.assertEqual(chat["model"], "local-ai-model-q4")
        self.assertEqual(chat["options"]["num_ctx"], 65536)
        self.assertIn("tool-call check passed", result.stdout)
        self.assertIn("Detected an Ollama Library model name", result.stdout)
        self.assertIn("Verified review", result.stdout)
        self.assertIn("GGUF context limit: 131072", result.stdout)
        self.assertNotIn("Model source:", result.stdout)
        self.assertIn("PARAMETER num_ctx 65536", (self.base / "work/Modelfile").read_text())
        positions = [result.stdout.index(label + ":") for label in ("MODEL", "DOWNLOAD_GIB", "CONTEXT_TOKENS")]
        self.assertEqual(positions, sorted(positions))

    def test_gguf_import_with_spaces(self):
        gguf = self.base / "weights with spaces.gguf"
        gguf.write_bytes(b"GGUFfixture")
        result = self.run_shell("choose_model", f"https://huggingface.co/example/tool-model\n{gguf}\nimported\n4\n\ny\n")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.calls("ollama")[0][1:3], ["create", "imported"])
        self.assertNotIn("pull", [row[1] for row in self.calls("ollama")])

    def test_invalid_gguf_rejected(self):
        gguf = self.base / "bad.gguf"
        gguf.write_text("not a GGUF")
        result = self.run_shell("choose_model", f"https://huggingface.co/example/tool-model\n{gguf}\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a GGUF", result.stderr)
        self.assertEqual(self.calls("ollama"), [])
        result = self.run_shell("choose_model", "https://example.invalid/model.gguf\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Only huggingface.co URLs", result.stderr)

    def test_low_disk_rejected_before_download(self):
        self.env["FREE_KIB"] = "1048576"
        result = self.run_shell("choose_model", "model\n4\n\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Not enough", result.stderr)
        self.assertFalse(any(row[1] == "pull" for row in self.calls("ollama")))

    def test_cancel_and_eof_do_not_download(self):
        for answers in ["q\n", ""]:
            result = self.run_shell("choose_model", answers)
            self.assertEqual(result.returncode, 0 if answers else 1)
        self.assertEqual(self.calls("ollama"), [])

    def test_context_and_size_validation(self):
        for answers in ["model\nbad\n", "model\n1\n999999999999999999999\n", "model\n1\n0\n"]:
            result = self.run_shell("choose_model", answers)
            self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls("ollama"), [])
        result = self.run_shell("choose_model; check_model", "model\n1\n0065536\ny\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        chat = next(body for path, body in self.server.requests if path == "/api/chat")
        self.assertEqual(chat["options"]["num_ctx"], 65536)

    def test_context_minimum_and_model_supported_limit(self):
        for context in ("8192", "32768", "65535"):
            result = self.run_shell("choose_model", f"model\n1\n{context}\n")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("65536", result.stderr)
        self.assertEqual(self.calls("ollama"), [])
        for mode in ("short-context", "info-context"):
            self.env["REGISTRY_CONTEXT"] = "32768"
            result = self.run_shell("choose_model", "model\n1\n\ny\n")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("32768", result.stderr)
            self.assertFalse(any(row[1] == "pull" for row in self.calls("ollama")))
            self.assertFalse(any(row[1] == "create" for row in self.calls("ollama")))
            self.env.pop("REGISTRY_CONTEXT")
            self.server.mode = mode
        self.server.mode = "unknown-context"
        result = self.run_shell("choose_model", "model\n1\n\ny\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("did not report a supported context limit", result.stdout)

    def test_no_suitable_recommendation_stops_before_download(self):
        result = self.run_shell("choose_model", "NONE\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no suitable model", result.stderr)
        self.assertEqual(self.calls("ollama"), [])

    def test_registry_failure_stops_before_pull(self):
        self.env["FAIL_REGISTRY"] = "1"
        result = self.run_shell("MODEL=model; remote_model_preflight")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("No model was downloaded", result.stderr)
        self.assertFalse(any(row[1] == "pull" for row in self.calls("ollama")))

    def test_out_of_catalog_gemini_recommendation_stops_before_pull(self):
        result = self.run_shell("choose_model", "qwen2.5-coder:14b\n9\n\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not in the wizard-verified candidate list", result.stderr)
        self.assertFalse(any(row[1] == "pull" for row in self.calls("ollama")))

    def test_installed_exact_model_is_reused_without_pull(self):
        self.env["INSTALLED_MODEL"] = "model:q4"
        result = self.run_shell("choose_model", "model:q4\n4\n\ny\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("already-installed model", result.stdout)
        self.assertFalse(any(row[1] == "pull" for row in self.calls("ollama")))
        self.assertTrue(any(row[1] == "create" and row[2] == "local-ai-model-q4" for row in self.calls("ollama")))

    def test_remote_and_non_tool_models_rejected(self):
        for mode, message in [("remote", "cloud inference"), ("no-tools", "advertise tools")]:
            with self.subTest(mode=mode):
                self.server.mode = mode
                result = self.run_shell("choose_model", "model\n1\n\ny\n")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)

    def test_failure_responses_never_claim_tool_success(self):
        for mode in ["no-call", "wrong-call", "json-prose", "malformed", "http-error"]:
            with self.subTest(mode=mode):
                self.server.mode = mode
                result = self.run_shell('LOCAL_MODEL=model; CONTEXT=65536; check_model')
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("check passed", result.stdout)
        self.env["FAIL_CHAT"] = "1"
        result = self.run_shell('LOCAL_MODEL=model; CONTEXT=65536; check_model')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timed out", result.stderr)

    def test_pull_failure_stops_before_profile_creation(self):
        self.env["FAIL_OLLAMA"] = "pull"
        result = self.run_shell("choose_model; connect_code", "model\n1\n\ny\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Download failed", result.stderr)
        self.assertEqual(self.calls("code"), [])

    def test_hardware_prompt_allowlist_and_unknown_memory(self):
        self.env["FAIL_NVIDIA"] = "1"
        self.env["USER"] = "DO-NOT-SHARE-USERNAME"
        result = self.run_shell("hardware_prompt", "\nn\n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        prompt = self.prompt.read_text()
        for forbidden in [str(self.home), self.env["USER"], "not-used.invalid"]:
            self.assertNotIn(forbidden, prompt)
        self.assertIn("GPU memory: unknown", prompt)
        self.assertIn("TOOL CALLING", prompt)
        self.assertRegex(prompt, r"(?m)^MODEL: .+\nDOWNLOAD_GIB: .+\nCONTEXT_TOKENS: ")
        self.assertIn("before\nany greeting", prompt)
        self.assertIn("structured Ollama API tool_calls", prompt)
        self.assertIn("MANDATORY FOR THIS SETUP", prompt)
        self.assertIn("Do not lower context to make it fit", prompt)
        self.assertIn("ONLY allowed Ollama Library recommendations", prompt)
        self.assertIn("llama3.1:8b", prompt)
        self.assertNotIn("qwen2.5-coder", prompt)
        self.assertIn("CONTEXT_TOKENS: 65536 exactly", prompt)
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
        result = self.run_shell("detect_platform; hardware_prompt", "n\n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("CPU-only", result.stdout)
        self.assertEqual(self.run_shell('OS=Darwin; choose_model', 'q\n').returncode, 0)
        desktop = self.run_shell('OS=Darwin; choose_model', 'q\n').stdout
        self.assertIn("checks the exact tag", desktop)
        self.assertIn("reused without pulling", desktop)
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
        for mode, message in [
            ("install", "could not install"),
            ("list", "could not list extensions"),
        ]:
            with self.subTest(mode=mode):
                self.env.pop("MISSING_EXTENSION", None)
                Path(self.env["EXTENSION_STATE"]).unlink(missing_ok=True)
                self.env["FAIL_CODE"] = mode
                result = self.run_shell("connect_code", "y\n")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.env.pop("FAIL_CODE")
        self.env["FAIL_PROFILE_CREATE"] = "1"
        Path(self.env["PROFILE_STATE"]).unlink(missing_ok=True)
        result = self.run_shell("sleep() { :; }; connect_code", "y\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("did not make the Local AI profile available", result.stderr)

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

    def test_https_downloads_restrict_redirect_protocols(self):
        source = SCRIPT.read_text()
        self.assertGreaterEqual(source.count("--proto-redir '=https'"), 2)

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

    def test_profile_creation_and_rerun_preserve_settings(self):
        settings = self.home / ".config/Code/User/settings.json"
        settings.parent.mkdir(parents=True)
        settings.write_text('{"keep": "unchanged"}')
        for _ in range(2):
            result = self.run_shell("LOCAL_MODEL=model; connect_code", "y\n")
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(settings.read_text(), '{"keep": "unchanged"}')
        for row in self.calls("code"):
            self.assertEqual(row[row.index("--profile") + 1], "Local AI")
        self.assertFalse(any("GitHub.copilot-chat" in row for row in self.calls("code")))
        self.assertTrue(any(row == ["code", "--new-window", "--profile", "Local AI"] for row in self.calls("code")))
        launches = [i for i, row in enumerate(self.calls("code")) if "--new-window" in row]
        installs = [i for i, row in enumerate(self.calls("code")) if "--install-extension" in row]
        self.assertTrue(launches and installs and launches[0] < installs[0])

    def test_full_interactive_flow_with_fake_api(self):
        answers = "\nn\n\nllama3.1:8b\n5\n65536\ny\ny\n"
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
