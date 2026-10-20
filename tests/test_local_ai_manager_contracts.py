import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs" / "local-ai-manager"
SCHEMAS = ROOT / "schemas" / "local-ai-manager"

SCHEMA_FILES = {
    "transaction": SCHEMAS / "transaction-state-v1.schema.json",
    "stream": SCHEMAS / "event-stream-v1.schema.json",
    "config": SCHEMAS / "config-v1.schema.json",
    "catalog": SCHEMAS / "model-catalog-v1.schema.json",
}


def load_schema(name):
    return json.loads(SCHEMA_FILES[name].read_text(encoding="utf-8"))


def local_refs(value):
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "$ref" and isinstance(child, str) and child.startswith("#/"):
                yield child
            yield from local_refs(child)
    elif isinstance(value, list):
        for child in value:
            yield from local_refs(child)


def resolve_pointer(document, pointer):
    value = document
    for token in pointer[2:].split("/"):
        token = token.replace("~1", "/").replace("~0", "~")
        value = value[token]
    return value


class LocalAIManagerContractTests(unittest.TestCase):
    def test_schema_json_and_local_refs_are_valid(self):
        for name in SCHEMA_FILES:
            with self.subTest(schema=name):
                schema = load_schema(name)
                self.assertEqual(
                    schema["$schema"],
                    "https://json-schema.org/draft/2020-12/schema",
                )
                self.assertFalse(schema.get("additionalProperties", False))
                for pointer in local_refs(schema):
                    self.assertIsNotNone(resolve_pointer(schema, pointer))

    def test_version_discriminators_are_explicit(self):
        transaction = load_schema("transaction")
        config = load_schema("config")
        catalog = load_schema("catalog")
        stream = load_schema("stream")

        self.assertEqual(
            transaction["properties"]["schema_version"]["const"],
            "local-ai.transaction-state/v1",
        )
        self.assertEqual(
            config["properties"]["schema_version"]["const"],
            "local-ai.config/v1",
        )
        self.assertEqual(
            catalog["properties"]["schema_version"]["const"],
            "local-ai.model-catalog/v1",
        )
        stream_versions = {
            branch["$ref"]
            for branch in stream["oneOf"]
        }
        self.assertEqual(
            stream_versions,
            {"#/$defs/event", "#/$defs/result"},
        )
        for record in ("event", "result"):
            self.assertEqual(
                stream["$defs"][record]["properties"]["schema_version"]["const"],
                "local-ai.event-stream/v1",
            )

    def test_transaction_receipts_ownership_and_privacy_contract(self):
        schema = load_schema("transaction")
        required = set(schema["required"])
        self.assertTrue(
            {
                "steps",
                "managed_assets",
                "issues",
                "manual_actions",
                "rollback",
            }.issubset(required)
        )

        receipt = schema["$defs"]["stepReceipt"]
        self.assertFalse(receipt["additionalProperties"])
        self.assertEqual(
            set(receipt["properties"]["phases"]["required"]),
            {"check", "plan", "apply", "verify", "undo"},
        )
        self.assertIn("mutation_asset_ids", receipt["required"])

        asset = schema["$defs"]["managedAsset"]
        self.assertFalse(asset["additionalProperties"])
        ownership = set(asset["properties"]["ownership"]["enum"])
        self.assertEqual(
            ownership,
            {"manager_owned", "user_owned", "external_detected"},
        )
        policies = set(
            schema["$defs"]["uninstallRule"]["properties"]["policy"]["enum"]
        )
        self.assertEqual(
            policies,
            {
                "remove_with_manager",
                "retain",
                "explicit_flag",
                "guided_ui_only",
            },
        )

        description = schema["description"].lower()
        for excluded in (
            "secrets",
            "repository",
            "prompts",
            "model responses",
            "usernames",
            "hostnames",
            "ip/mac",
            "raw command output",
        ):
            self.assertIn(excluded, description)

    def test_event_result_issue_and_exit_contract(self):
        schema = load_schema("stream")
        result = schema["$defs"]["result"]
        self.assertFalse(result["additionalProperties"])
        self.assertEqual(
            set(result["properties"]["outcome"]["enum"]),
            {"success", "cancelled", "manual_required", "failed"},
        )
        expected_exits = {
            0,
            2,
            3,
            10,
            11,
            12,
            20,
            21,
            22,
            30,
            31,
            32,
            40,
            50,
            60,
            70,
        }
        self.assertEqual(
            set(result["properties"]["exit_code"]["enum"]),
            expected_exits,
        )
        issue = schema["$defs"]["issue"]
        self.assertIn("remediations", issue["required"])
        self.assertFalse(issue["additionalProperties"])

        contract = (DOCS / "issue-and-exit-codes.md").read_text(encoding="utf-8")
        documented = {
            int(value)
            for value in re.findall(r"^\| (\d+) \| `[^`]+` \|", contract, re.MULTILINE)
        }
        self.assertEqual(documented, expected_exits)
        issue_codes = re.findall(
            r"^\| `(LAI-[A-Z]+-\d{3})` \|",
            contract,
            re.MULTILINE,
        )
        self.assertEqual(len(issue_codes), len(set(issue_codes)))
        self.assertGreaterEqual(len(issue_codes), 40)
        self.assertEqual(
            set(issue_codes),
            set(schema["$defs"]["issueCode"]["enum"]),
        )
        transaction = load_schema("transaction")
        self.assertEqual(
            set(issue_codes),
            set(transaction["$defs"]["issueCode"]["enum"]),
        )

    def test_config_is_closed_and_cannot_encode_approval_bypasses(self):
        schema = load_schema("config")
        self.assertFalse(schema["additionalProperties"])
        for section in schema["properties"]:
            if section == "schema_version":
                continue
            target = resolve_pointer(schema, schema["properties"][section]["$ref"])
            self.assertFalse(target["additionalProperties"])

        serialized = json.dumps(schema).lower()
        self.assertNotIn('"token"', serialized)
        self.assertNotIn('"password"', serialized)
        self.assertNotIn('"workspace_trust"', serialized)
        self.assertNotIn('"auto_approve_tools"', serialized)
        self.assertEqual(
            schema["$defs"]["model"]["properties"]["context_tokens"]["minimum"],
            65536,
        )
        self.assertEqual(
            schema["$defs"]["vscode"]["properties"][
                "install_official_ollama_extension"
            ]["const"],
            True,
        )

    def test_catalog_requires_evidence_license_and_memory_components(self):
        schema = load_schema("catalog")
        entry_required = set(schema["$defs"]["entry"]["required"])
        self.assertTrue(
            {
                "model",
                "source",
                "download",
                "context",
                "tools",
                "license",
                "host_tiers",
                "memory_estimate",
                "evidence_date",
                "references",
            }.issubset(entry_required)
        )
        context = schema["$defs"]["context"]
        self.assertEqual(
            context["properties"]["advertised_native_tokens"]["minimum"],
            65536,
        )
        self.assertEqual(context["properties"]["required_tokens"]["const"], 65536)
        memory = set(schema["$defs"]["memoryEstimate"]["required"])
        self.assertTrue(
            {
                "model_weights_bytes",
                "kv_cache_bytes_per_token",
                "runtime_overhead_bytes",
                "minimum_os_editor_headroom_bytes",
            }.issubset(memory)
        )
        self.assertIn(
            "commercial_use",
            schema["$defs"]["license"]["required"],
        )
        self.assertIn(
            "large_tool_set",
            schema["$defs"]["tools"]["required"],
        )

    def test_contract_markdown_links_resolve(self):
        link_pattern = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")
        failures = []
        for document in sorted(DOCS.glob("*.md")):
            text = document.read_text(encoding="utf-8")
            for target in link_pattern.findall(text):
                target = target.strip().split("#", 1)[0]
                if not target or "://" in target or target.startswith("mailto:"):
                    continue
                resolved = (document.parent / target).resolve()
                if not resolved.exists():
                    failures.append(f"{document.relative_to(ROOT)} -> {target}")
        self.assertEqual(failures, [])

    def test_parity_inventory_has_no_unresolved_status(self):
        text = (DOCS / "parity-inventory.md").read_text(encoding="utf-8")
        self.assertNotRegex(text, r"\b(?:TBD|TODO|Unknown)\b")
        self.assertIn("Stable", text)
        self.assertIn("Manual", text)
        self.assertIn("Out", text)
        for owner in (
            "`hardware`",
            "`catalog`",
            "`ollama`",
            "`vscode`",
            "`state`",
            "`update`",
        ):
            self.assertIn(owner, text)


if __name__ == "__main__":
    unittest.main()
