import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SKILL_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SKILL_ROOT.parents[1]
SCRIPT = SKILL_ROOT / "scripts/modular.py"
FIXTURE = SKILL_ROOT / "tests/testdata/two_level_assembly"


class ModularCliTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.env = {
            **os.environ,
            "MODULAR_SCAFFOLD_TESTING": "1",
            "MODULAR_SCAFFOLD_TEST_VERSION": "v0.4.2",
            "GOWORK": "off",
        }
        self.run_cli("init", "shop", "--modular-version", "v0.4.2",
                     "--out", str(self.root))
        self.project = self.root / "shop"

    def run_cli(self, *args, success=True):
        result = subprocess.run(
            [sys.executable, str(SCRIPT), *args], cwd=SKILL_ROOT,
            env=self.env, text=True, encoding="utf-8", capture_output=True,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def project_cli(self, *args, success=True):
        return self.run_cli(*args, "--project-dir", str(self.project), success=success)

    def run_go(self, *args):
        if shutil.which("go") is None:
            self.skipTest("Go toolchain is required for assembly compilation")
        result = subprocess.run(
            ["go", *args], cwd=self.project, env=self.env,
            text=True, encoding="utf-8", capture_output=True, timeout=180,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def use_local_library(self):
        # Exercise current repository APIs without changing repository go.mod/sum.
        self.run_go("mod", "edit", "-replace",
                    f"github.com/wplbyx/modular={REPO_ROOT.as_posix()}")

    def test_init_uses_current_layout(self):
        p = self.project
        self.assertTrue((p / "cmd/shop/main.go").is_file())
        self.assertTrue((p / "cmd/shop/resources.go").is_file())
        self.assertTrue((p / "cmd/shop/modules.go").is_file())
        main = (p / "cmd/shop/main.go").read_text(encoding="utf-8")
        modules = (p / "cmd/shop/modules.go").read_text(encoding="utf-8")
        self.assertIn("modularlog.NewLoggerManager(&cfg.Logging)", main)
        self.assertNotIn("func newLoggerManager(", main + modules)
        self.assertFalse((p / "cmd/shop/framework.gen.go").exists())
        self.assertTrue((p / "modules/.gitkeep").is_file())
        self.assertTrue((p / "config/shop/config.yaml").is_file())
        self.assertFalse((p / "internal").exists())

    def test_new_module_assembly_compiles(self):
        self.project_cli("module", "add", "customer")
        self.project_cli("module", "add", "order", "--depends-on", "customer")
        test = self.project / "modules/order/bootstrap_test.go"
        test.write_text("""package order_test

import (
    "testing"
    "shop/modules/order"
)

func TestEmptyAssembly(t *testing.T) {
    var constructor func(order.Config, order.Dependencies) (*order.Module, error) = order.New
    module, err := constructor(order.Config{}, order.Dependencies{})
    if err != nil || module == nil {
        t.Fatalf("empty assembly: module=%v err=%v", module, err)
    }
}
""", encoding="utf-8")
        self.use_local_library()
        self.run_go("build", "-mod=mod", "./modules/...")
        self.run_go("test", "-mod=mod", "./modules/...")

    def test_module_layout_stays_minimal_after_sync(self):
        self.project_cli("module", "add", "order")
        module = self.project / "modules/order"

        def assert_layout():
            for name in ("contract", "internal/app", "internal/domain",
                         "infrastructure/http", "infrastructure/gorm",
                         "infrastructure/eventbus"):
                self.assertTrue((module / name / "doc.go").is_file(), name)
            for name in ("api", "service"):
                self.assertFalse((module / "internal" / name).exists(), name)

        assert_layout()
        for _ in range(2):
            self.project_cli("sync")
            assert_layout()

        # Existing downstream adapter/contract implementations remain user-owned.
        legacy = {}
        for name in ("api", "service"):
            path = module / "internal" / name / "custom.go"
            path.parent.mkdir()
            path.write_text(f"package {name}\n", encoding="utf-8")
            legacy[path] = path.read_bytes()
        self.project_cli("sync")
        self.assertEqual(legacy, {path: path.read_bytes() for path in legacy})

    def test_sync_preserves_user_and_legacy_assembly(self):
        self.project_cli("module", "add", "customer")
        paths = [self.project / f"modules/customer/{name}" for name in
                 ("bootstrap.go", "config.go")]
        # Old signatures are also user-owned; syncing must not migrate them.
        paths[0].write_text("package customer\nfunc New() any { return nil }\n",
                            encoding="utf-8")
        with paths[1].open("a", encoding="utf-8") as stream:
            stream.write("\n// Application-specific configuration.\n")
        before = {path: path.read_bytes() for path in paths}
        for _ in range(2):
            self.project_cli("sync")
            self.assertEqual(before, {path: path.read_bytes() for path in paths})

    def test_two_level_assembly_behavior_and_boundaries(self):
        self.project_cli("module", "add", "customer")
        self.project_cli("module", "add", "order", "--depends-on", "customer")
        shutil.copytree(FIXTURE, self.project, dirs_exist_ok=True)
        self.use_local_library()
        self.project_cli("doctor", "--strict", "--phase", "framework")
        self.run_go("build", "-mod=mod", "./modules/...", "./cmd/assemblycheck")
        self.run_go("test", "-mod=mod", "./modules/...", "./cmd/assemblycheck")

        probe = self.project / "modules/order/invalid.go"
        probe.write_text('package order\nimport _ "shop/modules/customer"\n',
                         encoding="utf-8")
        result = self.project_cli("doctor", "--strict", success=False)
        self.assertIn("outside its contract", result.stderr)
        probe.unlink()
        result = self.project_cli("module", "depend", "add", "customer", "order",
                                  success=False)
        self.assertIn("cycle", result.stderr)
        self.project_cli("doctor", "--strict")


if __name__ == "__main__":
    unittest.main()
