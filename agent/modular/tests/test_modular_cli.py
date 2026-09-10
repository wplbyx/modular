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

    def init_v3_project(self, name: str = "demo") -> Path:
        out = self.root / (name + "-v3-out")
        self.run_cli(
            "init",
            name,
            "--out",
            str(out),
            version="v0.3.0",
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

    def test_generated_runtime_carries_managed_tomli(self) -> None:
        out = self.root / "vendored-out"
        self.run_cli(
            "init",
            "vendored",
            "--topology",
            "single",
            "--out",
            str(out),
        )
        project = out / "vendored"

        self.assertTrue((project / ".modular/tool/_vendor/tomli/__init__.py").is_file())
        self.assertTrue((project / ".modular/tool/_vendor/tomli/LICENSE").is_file())

        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
        for path in [
            ".modular/tool/_vendor/tomli/__init__.py",
            ".modular/tool/_vendor/tomli/LICENSE",
        ]:
            self.assertEqual(manifest["files"][path]["owner"], "managed")

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

    def test_generated_config_uses_named_pascal_case_application(self) -> None:
        project = self.init_project("namedconfig")
        self.project_cli(project, "service", "add", "user", "--transport", "http")

        generated = (project / "config/user/config.gen.go").read_text(encoding="utf-8")
        config_yaml = (project / "config/user/config.yaml").read_text(encoding="utf-8")
        process_yaml = (project / "config/namedconfig/config.yaml").read_text(encoding="utf-8")
        command = (project / "cmd/namedconfig/framework.gen.go").read_text(encoding="utf-8")

        self.assertIn(
            'Application configitem.Application `mapstructure:"Application"`',
            generated,
        )
        self.assertNotIn('mapstructure:"application,squash"', generated)
        self.assertIn("Application:\n", config_yaml)
        self.assertIn("HTTP:\n", config_yaml)
        self.assertIn("User:\n", process_yaml)
        self.assertIn("modularconfig.NewRootCommand", command)
        self.assertIn("cfg.Application.Name", command)
        self.assertIn("cfg.Application.Version", command)

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
        self.project_cli(project, "resource", "add", "eventbus", "--svc", "user")

        cmd = (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8")
        wiring = (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8")
        config = (project / "config/user/config.gen.go").read_text(encoding="utf-8")

        self.assertIn("bunresource.NewResource", cmd)
        self.assertIn("redisresource.NewResource", cmd)
        self.assertIn("storageresource.New", cmd)
        self.assertIn("telemetry.NewOpenTelemetry", cmd)
        self.assertIn("eventbus.New", cmd)
        self.assertIn("telemetry.WithLoggerManager(loggerManager)", cmd)
        self.assertIn("UserDB", wiring)
        self.assertIn("*bunresource.Resource", wiring)
        self.assertIn("UserRedis", wiring)
        self.assertIn("*redisresource.Resource", wiring)
        self.assertIn("UserStorage", wiring)
        self.assertIn("*storageresource.Resource", wiring)
        self.assertIn("UserEventBus", wiring)
        self.assertIn("*eventbus.Bus", wiring)
        self.assertIn("AddHTTP", wiring)
        self.assertIn("AddGRPC", wiring)
        self.assertIn("Database", config)
        self.assertIn("Redis", config)
        self.assertIn("Storage", config)
        self.assertIn("Telemetry", config)
        self.assertIn("EventBus", config)
        self.assertFalse((project / "internal/user/repository").exists())

        bootstrap = [
            cmd.index("newLoggerManager(ctx, &cfg.Logging)"),
            cmd.index("modularlog.SetDefault(loggerManager.Logger())"),
            cmd.index("newTransportPolicy(cfg.Application.Name, loggerManager.Logger())"),
            cmd.index("app.NewApplication(ctx, &cfg.Application, loggerManager.Logger(), options...)"),
        ]
        self.assertEqual(bootstrap, sorted(bootstrap))
        self.assertTrue((project / "cmd/demo/policy.go").is_file())

        import_body = cmd.split("import (\n", 1)[1].split("\n)\n", 1)[0]
        import_groups = import_body.split("\n\n")
        self.assertEqual(len(import_groups), 3)
        self.assertNotIn("github.com/", import_groups[0])
        self.assertTrue(all("github.com/wplbyx/modular" in line for line in import_groups[1].splitlines()))
        self.assertTrue(all('"demo/' in line for line in import_groups[2].splitlines()))

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

    def test_v3_init_and_headless_modules_share_process_runtime(self) -> None:
        project = self.init_v3_project()
        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
        architecture_path = project / ".modular/architecture.yaml"
        architecture = json.loads(architecture_path.read_text(encoding="utf-8"))

        self.assertEqual(manifest["project"]["model"], "module-process")
        self.assertNotIn("topology", manifest["project"])
        self.assertEqual(architecture["processes"]["demo"]["transports"], ["http"])
        self.assertEqual(architecture["modules"], {})
        self.assertIn("PYTHON ?= python3", (project / "Makefile").read_text(encoding="utf-8"))
        make_targets = (project / ".modular/make/modular.mk").read_text(encoding="utf-8")
        self.assertIn("scaffold-module:", make_targets)
        self.assertIn("scaffold-process:", make_targets)
        self.assertIn("scaffold-extract-check:", make_targets)
        profile = (project / ".modular/profile.toml").read_text(encoding="utf-8")
        self.assertIn('"internal/modules/*/internal/**/*.go"', profile)

        self.project_cli(project, "module", "add", "customer", version="v0.3.0")
        self.project_cli(
            project,
            "module",
            "add",
            "order",
            "--depends-on",
            "customer",
            version="v0.3.0",
        )
        self.project_cli(project, "transport", "add", "demo", "grpc", version="v0.3.0")
        self.project_cli(
            project,
            "resource",
            "add",
            "db",
            "--driver",
            "bun",
            version="v0.3.0",
        )

        architecture = json.loads(architecture_path.read_text(encoding="utf-8"))
        self.assertEqual(architecture["modules"]["order"]["dependencies"], ["customer"])
        self.assertEqual(architecture["processes"]["demo"]["modules"], ["customer", "order"])
        self.assertFalse((project / "internal/modules/customer/internal").exists())
        self.assertTrue((project / "config/modules/customer/config.go").is_file())

        framework = (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8")
        process_config = (project / "config/demo/config.gen.go").read_text(encoding="utf-8")
        wiring = (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8")
        self.assertEqual(framework.count("httpserver.NewServer("), 1)
        self.assertEqual(framework.count("rpcserver.NewServer("), 1)
        self.assertEqual(framework.count("bunresource.NewResource("), 1)
        self.assertRegex(process_config, r"Customer\s+customerconfig\.Config")
        self.assertRegex(process_config, r"Order\s+orderconfig\.Config")
        self.assertRegex(wiring, r"DB\s+\*bunresource\.Resource")
        self.assertIn("WithHealthManager(healthManager)", framework)
        self.assertIn("cfg.Application.InstanceID", framework)
        self.assertIn("cfg.Application.Metadata", framework)
        self.assertIn("app.WithRegistrar(contribution.Registrar)", framework)
        self.assertIn("Registrar registry.Registrar", wiring)

    def test_v3_module_dependency_cycle_is_rejected_without_writes(self) -> None:
        project = self.init_v3_project()
        self.project_cli(project, "module", "add", "customer", version="v0.3.0")
        self.project_cli(
            project,
            "module",
            "add",
            "order",
            "--depends-on",
            "customer",
            version="v0.3.0",
        )
        architecture_path = project / ".modular/architecture.yaml"
        before = architecture_path.read_bytes()

        completed = self.project_cli(
            project,
            "module",
            "depend",
            "add",
            "customer",
            "order",
            version="v0.3.0",
            expect_ok=False,
        )

        self.assertIn("module dependency cycle", completed.stderr)
        self.assertEqual(before, architecture_path.read_bytes())

    def test_v3_processes_keep_different_database_provider_types(self) -> None:
        project = self.init_v3_project()
        self.project_cli(project, "process", "add", "worker", version="v0.3.0")
        self.project_cli(
            project,
            "resource",
            "add",
            "db",
            "--process",
            "demo",
            "--driver",
            "bun",
            version="v0.3.0",
        )
        self.project_cli(
            project,
            "resource",
            "add",
            "db",
            "--process",
            "worker",
            "--driver",
            "gorm",
            "--dialect",
            "sqlite",
            version="v0.3.0",
        )

        wiring = (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8")
        demo = (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8")
        worker = (project / "cmd/worker/framework.gen.go").read_text(encoding="utf-8")
        self.assertIn("type DemoResources struct", wiring)
        self.assertIn("type WorkerResources struct", wiring)
        self.assertRegex(wiring, r"DB\s+\*bunresource\.Resource")
        self.assertRegex(wiring, r"DB\s+\*modulargorm\.Resource")
        self.assertIn("platform.Resources.Demo.DB = dbResource", demo)
        self.assertIn("platform.Resources.Worker.DB = dbResource", worker)

        legacy = self.project_cli(
            project,
            "service",
            "add",
            "legacy",
            "--transport",
            "http",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("service commands are v0.2-only", legacy.stderr)

    def test_v3_doctor_enforces_declared_contract_only_imports(self) -> None:
        project = self.init_v3_project()
        self.project_cli(project, "module", "add", "customer", version="v0.3.0")
        self.project_cli(project, "module", "add", "order", version="v0.3.0")
        app_dir = project / "internal/modules/order/internal/app"
        app_dir.mkdir(parents=True)
        source = app_dir / "use_customer.go"
        source.write_text(
            'package app\n\nimport _ "demo/common/customer"\n',
            encoding="utf-8",
        )

        undeclared = self.project_cli(
            project,
            "doctor",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("imports undeclared dependency order -> customer", undeclared.stderr)

        self.project_cli(
            project,
            "module",
            "depend",
            "add",
            "order",
            "customer",
            version="v0.3.0",
        )
        source.write_text(
            'package app\n\nimport _ "demo/internal/modules/customer/internal/app"\n',
            encoding="utf-8",
        )
        implementation_import = self.project_cli(
            project,
            "doctor",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("crosses into customer outside its contract", implementation_import.stderr)

    def test_v3_extraction_reports_blockers_and_required_adapters(self) -> None:
        project = self.init_v3_project()
        self.project_cli(project, "module", "add", "inventory", version="v0.3.0")
        self.project_cli(
            project,
            "module",
            "add",
            "order",
            "--depends-on",
            "inventory",
            version="v0.3.0",
        )
        self.project_cli(
            project,
            "module",
            "blocker",
            "add",
            "--module",
            "order",
            "--module",
            "inventory",
            "--kind",
            "shared-transaction",
            "--reason",
            "reservation commits with order",
            version="v0.3.0",
        )
        self.project_cli(project, "process", "add", "order_api", version="v0.3.0")

        blocked = self.project_cli(
            project,
            "module",
            "extract",
            "order",
            "--to-process",
            "order_api",
            "--check",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("shared-transaction: reservation commits with order", blocked.stderr)
        bypass = self.project_cli(
            project,
            "process",
            "attach",
            "order_api",
            "order",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("module cannot move across processes", bypass.stderr)

        architecture_path = project / ".modular/architecture.yaml"
        architecture = json.loads(architecture_path.read_text(encoding="utf-8"))
        architecture["extraction_blockers"] = []
        architecture_path.write_text(json.dumps(architecture, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        missing = self.project_cli(
            project,
            "module",
            "extract",
            "order",
            "--to-process",
            "order_api",
            "--check",
            version="v0.3.0",
            expect_ok=False,
        )
        self.assertIn("missing protobuf contract for module inventory", missing.stderr)
        self.assertIn("missing remote adapter for order -> inventory", missing.stderr)

        proto_dir = project / "proto/inventory"
        proto_dir.mkdir(parents=True)
        (proto_dir / "inventory.proto").write_text('syntax = "proto3";\n', encoding="utf-8")
        adapter_dir = project / "internal/modules/order/internal/adapters/remote/inventory"
        adapter_dir.mkdir(parents=True)
        (adapter_dir / "adapter.go").write_text("package inventory\n", encoding="utf-8")
        ready = self.project_cli(
            project,
            "module",
            "extract",
            "order",
            "--to-process",
            "order_api",
            "--check",
            version="v0.3.0",
        )
        self.assertIn("module order is extractable", ready.stdout)

        self.project_cli(
            project,
            "module",
            "extract",
            "order",
            "--to-process",
            "order_api",
            "--apply",
            version="v0.3.0",
        )
        architecture = json.loads(architecture_path.read_text(encoding="utf-8"))
        self.assertEqual(architecture["processes"]["order_api"]["modules"], ["order"])
        self.assertTrue((project / "cmd/order_api/framework.gen.go").is_file())

    def test_v02_project_migrates_to_v3_and_creates_architecture(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "customer", "--transport", "http")
        self.project_cli(project, "service", "add", "order", "--transport", "grpc")
        self.project_cli(project, "resource", "add", "db", "--svc", "customer", "--driver", "bun")
        go_mod = project / "go.mod"
        go_mod.write_text(go_mod.read_text(encoding="utf-8") + "\nrequire example.com/keep v1.2.3\n", encoding="utf-8")
        buf_gen = project / "buf.gen.yaml"
        buf_gen.write_text(buf_gen.read_text(encoding="utf-8") + "# custom buf option\n", encoding="utf-8")
        gitignore = project / ".gitignore"
        gitignore.write_text(gitignore.read_text(encoding="utf-8") + "custom-output/\n", encoding="utf-8")

        preview = self.project_cli(
            project,
            "migrate",
            "v0.2-to-v0.3",
            "--modular-version",
            "v0.3.0",
            version="v0.3.0",
        )
        self.assertIn("create .modular/architecture.yaml", preview.stdout)
        self.assertFalse((project / ".modular/architecture.yaml").exists())

        self.project_cli(
            project,
            "migrate",
            "v0.2-to-v0.3",
            "--modular-version",
            "v0.3.0",
            "--apply",
            version="v0.3.0",
        )
        manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
        architecture = json.loads((project / ".modular/architecture.yaml").read_text(encoding="utf-8"))
        self.assertEqual(manifest["project"]["model"], "module-process")
        self.assertNotIn("topology", manifest["project"])
        self.assertEqual(architecture["processes"]["demo"]["modules"], ["customer", "order"])
        self.assertEqual(architecture["processes"]["demo"]["transports"], ["grpc", "http"])
        self.assertEqual(architecture["processes"]["demo"]["resources"]["db"]["kind"], "db")
        self.assertIn("type Contribution struct", (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8"))
        migrated_go_mod = go_mod.read_text(encoding="utf-8")
        self.assertIn("github.com/wplbyx/modular v0.3.0", migrated_go_mod)
        self.assertIn("example.com/keep v1.2.3", migrated_go_mod)
        self.assertIn("local: protoc-gen-go-modular", buf_gen.read_text(encoding="utf-8"))
        self.assertIn("# custom buf option", buf_gen.read_text(encoding="utf-8"))
        self.assertIn("custom-output/", gitignore.read_text(encoding="utf-8"))
        self.assertFalse((project / "config/customer/config.go").exists())
        self.assertFalse((project / "config/order/config.go").exists())

    def test_v02_migration_stops_for_customized_module_config_extension(self) -> None:
        project = self.init_project()
        self.project_cli(project, "service", "add", "customer", "--transport", "http")
        extension = project / "config/customer/config.go"
        extension.write_text(extension.read_text(encoding="utf-8") + "\n// customer setting\n", encoding="utf-8")

        completed = self.project_cli(
            project,
            "migrate",
            "v0.2-to-v0.3",
            "--modular-version",
            "v0.3.0",
            "--apply",
            version="v0.3.0",
            expect_ok=False,
        )

        self.assertIn("move its module settings to config/modules/customer/config.go", completed.stderr)
        self.assertFalse((project / ".modular/architecture.yaml").exists())
        self.assertIn("// customer setting", extension.read_text(encoding="utf-8"))

    def test_empty_v02_project_migrates_to_headless_v3_process(self) -> None:
        project = self.init_project(topology="service")

        self.project_cli(
            project,
            "migrate",
            "v0.2-to-v0.3",
            "--modular-version",
            "v0.3.0",
            "--apply",
            version="v0.3.0",
        )

        architecture = json.loads((project / ".modular/architecture.yaml").read_text(encoding="utf-8"))
        process = architecture["processes"]["demo"]
        self.assertEqual(process["modules"], [])
        self.assertEqual(process["transports"], [])
        self.assertEqual(process["resources"], {})
        framework = (project / "cmd/demo/framework.gen.go").read_text(encoding="utf-8")
        self.assertIn("app.NewApplication", framework)
        self.assertIn("endpoints := make([]core.Endpoint, 0)", framework)
        self.assertIn("transports := make([]core.Transport, 0)", framework)
        self.assertNotIn("httpserver.NewServer", framework)
        self.assertNotIn("rpcserver.NewServer", framework)


class ModularSkillContentTest(unittest.TestCase):
    def test_cli_uses_one_vendored_toml_parser(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")

        self.assertIn("from _vendor import tomli", source)
        self.assertNotIn("import tomllib", source)

    def test_python_sources_avoid_apis_newer_than_python38(self) -> None:
        cli_source = SCRIPT.read_text(encoding="utf-8")
        eval_source = (SKILL_ROOT / "evals/run_scaffold_benchmark.py").read_text(encoding="utf-8")

        self.assertNotIn(".removeprefix(", cli_source)
        self.assertNotIn(".write_text(content, encoding=\"utf-8\", newline=", cli_source)
        self.assertNotIn("strict=True", eval_source)

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
