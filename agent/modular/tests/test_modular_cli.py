import subprocess, sys, tempfile, unittest
from pathlib import Path

SKILL_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = SKILL_ROOT / 'scripts/modular.py'

class ModularCliTest(unittest.TestCase):
    def run_cli(self, *args):
        with tempfile.TemporaryDirectory() as d:
            out = Path(d) / 'out'
            env = {'MODULAR_SCAFFOLD_TESTING':'1','MODULAR_SCAFFOLD_TEST_VERSION':'v0.4.2'}
            import os; env.update(os.environ)
            r = subprocess.run([sys.executable, str(SCRIPT), 'init', 'shop', '--modular-version', 'v0.4.2', '--out', str(out)], cwd=SKILL_ROOT, env=env, text=True, capture_output=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            p = out / 'shop'
            self.assertTrue((p/'cmd/shop/main.go').is_file())
            self.assertTrue((p/'cmd/shop/resources.go').is_file())
            self.assertTrue((p/'cmd/shop/modules.go').is_file())
            self.assertFalse((p/'cmd/shop/framework.gen.go').exists())
            self.assertTrue((p/'modules/.gitkeep').is_file())
            self.assertTrue((p/'config/shop/config.yaml').is_file())
            self.assertFalse((p/'internal').exists())

    def test_init_uses_current_layout(self):
        self.run_cli()


if __name__ == '__main__':
    unittest.main()
