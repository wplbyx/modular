from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SKILL_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = SKILL_ROOT / "scripts" / "modular.py"


class ModularCliTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.cwd = self.root / "cwd"
        self.cwd.mkdir()

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def run_cli(
        self,
        *args: str,
        script: Path = SCRIPT,
        version: str = "v0.2.0",
        expect_ok: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        env.update(
            {
                "MODULAR_SCAFFOLD_TESTING": "1",
                "MODULAR_SCAFFOLD_TEST_VERSION": version,
            }
        )
        completed = subprocess.run(
            [sys.executable, str(script), *args],
            cwd=self.cwd,
            env=env,
            text=True,
            capture_output=True,
        )
        if expect_ok and completed.returncode != 0:
            self.fail(
                "command failed: "
                + " ".join(args)
                + "\nstdout:\n"
                + completed.stdout
                + "\nstderr:\n"
                + completed.stderr
            )
        return completed

    def init_project(
        self,
        name: str = "demo",
        topology: str = "single",
        *,
        script: Path = SCRIPT,
        version: str = "v0.2.0",
    ) -> Path:
        out = self.root / (name + "-out")
        self.run_cli(
            "init",
            name,
            "--topology",
            topology,
            "--out",
            str(out),
            script=script,
            version=version,
        )
        return out / name

    def project_cli(
        self,
        project: Path,
        *args: str,
        version: str = "v0.2.0",
        expect_ok: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        return self.run_cli(
            *args,
            "--project-dir",
            str(project),
            script=project / ".modular" / "tool" / "modular.py",
            version=version,
            expect_ok=expect_ok,
        )

    def test_skill_is_relocatable_and_local_runtime_self_checks(self) -> None:
        installed = self.root / "arbitrary" / "skills" / "modular"
        shutil.copytree(
            SKILL_ROOT,
            installed,
            ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
        )
        installed_script = installed / "scripts" / "modular.py"

        self.run_cli("self-check", script=installed_script)
        project = self.init_project("relocated", script=installed_script)
        local_script = project / ".modular" / "tool" / "modular.py"
        self.run_cli("self-check", script=local_script)

        self.assertTrue((project / ".modular/tool/assets/templates.json").is_file())
        self.assertTrue((project / ".modular/tool/references/commands.md").is_file())

    def test_init_uses_concrete_remote_version_and_no_local_replace(self) -> None:
        project = self.init_project(version="v0.2.7")
        go_mod = (project / "go.mod").read_text(encoding="utf-8")
        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))

        self.assertIn("github.com/wplbyx/modular v0.2.7", go_mod)
        self.assertNotIn("replace github.com/wplbyx/modular", go_mod)
        self.assertEqual(manifest["project"]["modular_version"], "v0.2.7")
        self.assertEqual(manifest["files"]["go.mod"]["owner"], "scaffold-once")

        rejected_out = self.root / "old-version"
        completed = self.run_cli(
            "init",
            "legacy",
            "--topology",
            "single",
            "--out",
            str(rejected_out),
            version="v0.1.0",
            expect_ok=False,
        )
        self.assertIn("require v0.2.0 or newer", completed.stderr)
        self.assertFalse((rejected_out / "legacy").exists())

    def test_service_requires_explicit_transport_and_creates_no_business_shells(self) -> None:
        project = self.init_project()
        completed = self.project_cli(project, "service", "add", "user", expect_ok=False)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("--transport", completed.stderr)
        self.assertFalse((project / "config/user").exists())
        self.assertFalse(any((project / "proto").rglob("*.proto")))
        self.assertFalse((project / "internal/user").exists())
        self.assertFalse((project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8").find("httpserver") >= 0)

    def test_project_local_sync_is_idempotent_and_preserves_scaffold_once_files(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http")
        extension = project / "config/user/config.go"
        extension.write_text(extension.read_text(encoding="utf-8") + "\n// user extension\n", encoding="utf-8")

        first = self.project_cli(project, "sync")
        second = self.project_cli(project, "sync")

        self.assertIn("no changes", first.stdout)
        self.assertIn("no changes", second.stdout)
        self.assertIn("// user extension", extension.read_text(encoding="utf-8"))
        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["files"]["config/user/config.go"]["owner"], "scaffold-once")
        self.assertEqual(manifest["files"]["config/user/config.gen.go"]["owner"], "managed")

    def test_dry_run_and_diff_never_write(self) -> None:
        project = self.init_project()
        manifest_path = project / ".modular/manifest.json"
        before = manifest_path.read_bytes()

        dry_run = self.project_cli(
            project,
            "service",
            "add",
            "user",
            "--transport",
            "http",
            "--dry-run",
        )
        diff = self.project_cli(
            project,
            "service",
            "add",
            "user",
            "--transport",
            "http",
            "--diff",
        )

        self.assertIn("create config/user/config.gen.go", dry_run.stdout)
        self.assertIn("+++ b/config/user/config.gen.go", diff.stdout)
        self.assertEqual(before, manifest_path.read_bytes())
        self.assertFalse((project / "config/user").exists())

    def test_managed_conflicts_stop_before_any_write(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http")
        generated = project / "config/user/config.gen.go"
        generated.write_text(generated.read_text(encoding="utf-8") + "\n// local edit\n", encoding="utf-8")
        manifest_path = project / ".modular/manifest.json"
        before_manifest = manifest_path.read_bytes()
        before_cmd = (project / "cmd/demo/framework.gen.go").read_bytes()

        completed = self.project_cli(
            project,
            "transport",
            "add",
            "user",
            "grpc",
            expect_ok=False,
        )

        self.assertIn("managed file was modified", completed.stderr)
        self.assertEqual(before_manifest, manifest_path.read_bytes())
        self.assertEqual(before_cmd, (project / "cmd/demo/framework.gen.go").read_bytes())
        self.assertNotIn("GRPC", generated.read_text(encoding="utf-8"))

    def test_post_write_verification_failure_rolls_back_transaction(self) -> None:
        project = self.init_project()
        business = project / "internal/platform/wiring/business.go"
        business.write_text(business.read_text(encoding="utf-8") + "\n// ExampleDTO\n", encoding="utf-8")
        manifest_path = project / ".modular/manifest.json"
        before_manifest = manifest_path.read_bytes()

        completed = self.project_cli(
            project,
            "service",
            "add",
            "user",
            "--transport",
            "http",
            expect_ok=False,
        )

        self.assertIn("placeholder check failed", completed.stderr)
        self.assertEqual(before_manifest, manifest_path.read_bytes())
        self.assertFalse((project / "config/user/config.gen.go").exists())
        self.assertNotIn("User", (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8"))

    def test_resource_wiring_uses_selected_library_resources_only(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http", "--transport", "grpc")
        self.project_cli(project, "resource", "add", "db", "--svc", "user", "--driver", "bun")
        self.project_cli(project, "resource", "add", "redis", "--svc", "user")
        self.project_cli(project, "resource", "add", "storage", "--svc", "user")
        self.project_cli(project, "resource", "add", "telemetry", "--svc", "user")

        cmd = (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8")
        wiring = (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8")
        config = (project / "config/user/config.gen.go").read_text(encoding="utf-8")

        self.assertIn("bunresource.NewResource", cmd)
        self.assertIn("redisresource.NewResource", cmd)
        self.assertIn("storageresource.New", cmd)
        self.assertIn("telemetry.NewOpenTelemetry", cmd)
        self.assertIn("UserDB", wiring)
        self.assertIn("*bunresource.Resource", wiring)
        self.assertIn("UserRedis", wiring)
        self.assertIn("*redisresource.Resource", wiring)
        self.assertIn("UserStorage", wiring)
        self.assertIn("*storageresource.Resource", wiring)
        self.assertIn("AddHTTP", wiring)
        self.assertIn("AddGRPC", wiring)
        self.assertIn("Database", config)
        self.assertIn("Redis", config)
        self.assertIn("Storage", config)
        self.assertIn("Telemetry", config)
        self.assertFalse((project / "internal/user/repository").exists())

    def test_phase_gates_allow_only_the_expected_markers_and_require_tests(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http")
        self.project_cli(project, "verify", "--phase", "framework")

        contract_blocked = self.project_cli(project, "verify", "--phase", "contract", expect_ok=False)
        self.assertIn("unwired business marker", contract_blocked.stderr)

        business = project / "internal/platform/wiring/business.go"
        business.write_text(
            business.read_text(encoding="utf-8").replace(
                "\t// modular:business-unwired - remove this marker after contracts are registered.\n",
                "",
            ),
            encoding="utf-8",
        )
        package = project / "internal/user/app/public"
        package.mkdir(parents=True)
        usecase = package / "create_user.go"
        usecase.write_text(
            "package public\n\n// modular:contract-unimplemented\nfunc CreateUser() {}\n",
            encoding="utf-8",
        )
        test_file = package / "create_user_test.go"
        test_file.write_text(
            "package public\n\nimport \"testing\"\n\nfunc TestCreateUser(t *testing.T) { CreateUser() }\n",
            encoding="utf-8",
        )

        self.project_cli(project, "verify", "--phase", "contract")
        complete_blocked = self.project_cli(project, "verify", "--phase", "complete", expect_ok=False)
        self.assertIn("contract Unimplemented marker", complete_blocked.stderr)

        usecase.write_text("package public\n\nfunc CreateUser() {}\n", encoding="utf-8")
        test_file.unlink()
        no_tests = self.project_cli(project, "verify", "--phase", "complete", expect_ok=False)
        self.assertIn("business package has no tests", no_tests.stderr)

        test_file.write_text(
            "package public\n\nimport \"testing\"\n\nfunc TestCreateUser(t *testing.T) { CreateUser() }\n",
            encoding="utf-8",
        )
        self.project_cli(project, "verify", "--phase", "complete")

    def test_remove_and_prune_require_apply_and_protect_user_files(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http")
        extension = project / "config/user/config.go"
        extension.write_text(extension.read_text(encoding="utf-8") + "\n// keep me\n", encoding="utf-8")

        preview = self.project_cli(project, "service", "remove", "user")
        self.assertIn("delete config/user/config.gen.go", preview.stdout)
        self.assertTrue((project / "config/user/config.gen.go").is_file())

        self.project_cli(project, "service", "remove", "user", "--apply")
        self.assertFalse((project / "config/user/config.gen.go").exists())
        self.assertTrue(extension.is_file())
        self.assertIn("// keep me", extension.read_text(encoding="utf-8"))

        stale = project / ".modular/stale.gen"
        stale.write_text("stale\n", encoding="utf-8")
        manifest_path = project / ".modular/manifest.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["files"][".modular/stale.gen"] = {
            "owner": "managed",
            "sha256": hashlib.sha256(b"stale\n").hexdigest(),
            "template": "test/stale",
            "template_version": "1.0.0",
            "provenance": {},
        }
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")

        self.project_cli(project, "prune")
        self.assertTrue(stale.is_file())
        self.project_cli(project, "prune", "--apply")
        self.assertFalse(stale.exists())

    def test_topology_migration_changes_only_managed_process_files(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "user", "--transport", "http")
        business = project / "internal/platform/wiring/business.go"
        business.write_text(business.read_text(encoding="utf-8") + "\n// custom business wiring\n", encoding="utf-8")
        before = business.read_bytes()

        preview = self.project_cli(project, "migrate", "topology", "--to", "service")
        self.assertIn("delete cmd/demo/main.go", preview.stdout)
        self.assertTrue((project / "cmd/demo/main.go").is_file())

        self.project_cli(project, "migrate", "topology", "--to", "service", "--apply")
        self.assertFalse((project / "cmd/demo/main.go").exists())
        self.assertFalse((project / "cmd/demo/framework.gen.go").exists())
        self.assertTrue((project / "cmd/user/framework.gen.go").is_file())
        self.assertEqual(before, business.read_bytes())
        self.assertFalse(any((project / "proto").rglob("*.proto")))

    def test_project_upgrade_updates_only_the_concrete_dependency(self) -> None:
        project = self.init_project()
        go_mod = project / "go.mod"
        go_mod.write_text(go_mod.read_text(encoding="utf-8") + "\nrequire example.com/keep v1.2.3\n", encoding="utf-8")

        preview = self.project_cli(project, "project", "upgrade", "--modular-version", "v0.3.1", version="v0.3.1")
        self.assertIn("update go.mod", preview.stdout)
        self.assertIn("github.com/wplbyx/modular v0.2.0", go_mod.read_text(encoding="utf-8"))

        self.project_cli(
            project,
            "project",
            "upgrade",
            "--modular-version",
            "v0.3.1",
            "--apply",
            version="v0.3.1",
        )
        updated = go_mod.read_text(encoding="utf-8")
        self.assertIn("github.com/wplbyx/modular v0.3.1", updated)
        self.assertIn("example.com/keep v1.2.3", updated)
        self.assertNotIn("replace github.com/wplbyx/modular", updated)
        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["project"]["modular_version"], "v0.3.1")


class ModularSkillContentTest(unittest.TestCase):
    def test_skill_router_references_exist_and_legacy_cli_is_not_documented(self) -> None:
        skill = SKILL_ROOT / "SKILL.md"
        text = skill.read_text(encoding="utf-8")
        references = re.findall(r"\((references/[^)]+\.md)\)", text)

        self.assertGreaterEqual(len(references), 6)
        for reference in references:
            self.assertTrue((SKILL_ROOT / reference).is_file(), reference)
        commands = (SKILL_ROOT / "references/commands.md").read_text(encoding="utf-8")
        self.assertNotIn("`surface`", commands)
        self.assertNotIn("`repository recommend`", commands)

    def test_eval_schema_contains_objective_expectations(self) -> None:
        payload = json.loads((SKILL_ROOT / "evals/evals.json").read_text(encoding="utf-8"))

        self.assertEqual(payload["skill_name"], "modular")
        self.assertGreaterEqual(len(payload["evals"]), 3)
        for item in payload["evals"]:
            self.assertTrue(item["prompt"])
            self.assertTrue(item["expected_output"])
            self.assertGreaterEqual(len(item.get("expectations", [])), 5)


if __name__ == "__main__":
    unittest.main()
