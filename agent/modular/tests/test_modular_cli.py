from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
SCRIPT = REPO_ROOT / "agent" / "modular" / "scripts" / "modular.py"


class ModularCliTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def run_cli(self, *args: str, expect_ok: bool = True) -> subprocess.CompletedProcess[str]:
        completed = subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            cwd=REPO_ROOT,
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

    def init_project(self, topology: str = "single") -> Path:
        self.run_cli("init", "demo", topology, "--out", str(self.root), "--modular-path", str(REPO_ROOT).replace("\\", "/"))
        return self.root / "demo"

    def test_init_and_doctor(self) -> None:
        project = self.init_project()

        self.assertTrue((project / "go.mod").exists())
        self.assertTrue((project / "cmd" / "demo" / "main.go").exists())
        self.assertFalse((project / "config" / "config.go").exists())
        self.assertIn("replace github.com/wplbyx/modular", (project / "go.mod").read_text(encoding="utf-8"))
        self.run_cli("doctor", "--project-dir", str(project))

    def test_single_service_repository_and_resources(self) -> None:
        project = self.init_project()

        self.run_cli("service", "user", "--surface", "public", "--methods", "CreateUser", "--gen", "skip", "--project-dir", str(project))
        self.run_cli(
            "repository",
            "app",
            "user",
            "public",
            "--aggregate",
            "User",
            "--query",
            "FindUser",
            "--command",
            "SaveUser",
            "--force",
            "--project-dir",
            str(project),
        )
        self.run_cli(
            "repository",
            "domain",
            "user",
            "--aggregate",
            "User",
            "--query",
            "FindUser",
            "--command",
            "SaveUser",
            "--force",
            "--project-dir",
            str(project),
        )
        self.run_cli("resource", "db", "--driver", "bun", "--project-dir", str(project))
        self.run_cli("resource", "redis", "--project-dir", str(project))
        self.run_cli("resource", "storage", "--project-dir", str(project))
        self.run_cli("resource", "telemetry", "--project-dir", str(project))
        self.run_cli("doctor", "--project-dir", str(project))

        proto = (project / "proto" / "user" / "user.proto").read_text(encoding="utf-8")
        main = (project / "cmd" / "demo" / "main.go").read_text(encoding="utf-8")
        app_adapter = (project / "internal" / "user" / "app" / "public" / "adapter.go").read_text(encoding="utf-8")
        app_repo = (project / "internal" / "user" / "repository" / "app" / "public_user.go").read_text(encoding="utf-8")
        domain_adapter = (project / "internal" / "user" / "domain" / "adapter.go").read_text(encoding="utf-8")
        domain_repo = (project / "internal" / "user" / "repository" / "domain" / "user.go").read_text(encoding="utf-8")

        self.assertTrue((project / "config" / "user" / "config.go").exists())
        self.assertIn("rpc CreateUser(CreateUserRequest) returns (CreateUserResponse);", proto)
        self.assertIn("httpserver.NewServer", main)
        self.assertIn("rpcserver.NewServer", main)
        self.assertIn("grpc.ServiceRegistrar", main)
        self.assertIn("app.WithEndpoint(endpoint)", main)
        self.assertIn("resources = append(resources, userDBResource)", main)
        self.assertIn("resources = append(resources, userRedisResource)", main)
        self.assertIn("resources = append(resources, userStorageResource)", main)
        self.assertIn("resources = append(resources, userTelemetryResource)", main)
        self.assertIn("FindUser(ctx context.Context, id string) (UserDTO, error)", app_adapter)
        self.assertIn("surfaceapp.UserDTO", app_repo)
        self.assertIn("FindUser(ctx context.Context, id string) (*entity.User, error)", domain_adapter)
        self.assertIn("_ = id", domain_repo)
        self.assertTrue((project / "internal" / "user" / "repository" / "storage_resource.go").exists())

    def test_repository_recommend_outputs_scaffold_commands(self) -> None:
        project = self.init_project()
        self.run_cli("service", "user", "--surface", "public", "--methods", "CreateUser", "--gen", "skip", "--project-dir", str(project))

        app = self.run_cli(
            "repository",
            "recommend",
            "user",
            "public",
            "--aggregate",
            "User",
            "--feature",
            "CRUD admin page for user search",
            "--query",
            "FindUser",
            "--command",
            "SaveUser",
            "--project-dir",
            str(project),
        )
        domain = self.run_cli(
            "repository",
            "recommend",
            "user",
            "--aggregate",
            "Order",
            "--feature",
            "order aggregate invariants and transaction consistency",
            "--command",
            "CancelOrder",
            "--project-dir",
            str(project),
        )

        self.assertIn("placement: app", app.stdout)
        self.assertIn("internal/user/app/public/adapter.go", app.stdout)
        self.assertIn("repository app user public", app.stdout)
        self.assertIn("FindUser(ctx context.Context, id string) (UserDTO, error)", app.stdout)
        self.assertIn("SaveUser(ctx context.Context, item UserDTO) error", app.stdout)
        self.assertIn("placement: domain", domain.stdout)
        self.assertIn("internal/user/domain/adapter.go", domain.stdout)
        self.assertIn("repository domain user", domain.stdout)
        self.assertIn("CancelOrder(ctx context.Context, item *entity.Order) error", domain.stdout)

    def test_service_topology_surface_appends_to_svc_main(self) -> None:
        project = self.init_project("service")

        self.run_cli("service", "billing", "--surface", "admin", "--methods", "CreateInvoice", "--gen", "skip", "--project-dir", str(project))
        self.run_cli("surface", "billing", "platform", "--method", "SyncInvoice", "--gen", "skip", "--project-dir", str(project))

        main = (project / "cmd" / "billing" / "main.go").read_text(encoding="utf-8")
        platform_proto = (project / "proto" / "billing" / "platform.proto").read_text(encoding="utf-8")

        self.assertIn("billingAdminServer", main)
        self.assertIn("billingPlatformServer", main)
        self.assertEqual(main.count("billingAppRepo := billingAppRepository.NewRepository()"), 1)
        self.assertIn("rpc SyncInvoice(SyncInvoiceRequest) returns (SyncInvoiceResponse);", platform_proto)

    def test_doctor_rejects_stale_old_layout(self) -> None:
        project = self.init_project()
        self.run_cli("service", "user", "--gen", "skip", "--project-dir", str(project))
        stale = project / "internal" / "user" / "domain" / "repository.go"
        stale.write_text("package domain\n", encoding="utf-8")

        completed = self.run_cli("doctor", "--project-dir", str(project), expect_ok=False)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("domain/repository.go", completed.stderr)


if __name__ == "__main__":
    unittest.main()
