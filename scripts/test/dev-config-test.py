"""Anonymous initialization boundary tests; never read the daily instance."""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class DevConfigTests(unittest.TestCase):
    def setUp(self):
        tmp = ROOT / '.runtime/tests/tmp'
        tmp.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=tmp)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts/dev').mkdir(parents=True)
        for name in ('dev-config.mjs', 'runtime-paths.mjs'):
            shutil.copyfile(ROOT / 'scripts/dev' / name, self.root / 'scripts/dev' / name)
        self.dev = self.root / '.runtime/dev'

    def invoke(self, expected=0):
        r = subprocess.run(['node', str(self.root / 'scripts/dev/dev-config.mjs')], capture_output=True, text=True)
        self.assertEqual(r.returncode, expected, r.stderr)
        return r

    def test_new_instance(self):
        r = self.invoke()
        token = (self.dev / 'secrets/setup-token').read_text()
        self.assertNotIn(token, r.stdout + r.stderr)
        for name, port in [('config.json', 8080), ('vite-config.json', 5173)]:
            p = self.dev / name
            self.assertEqual(json.loads(p.read_text())['public_url'], f'http://127.0.0.1:{port}')
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)
        self.assertFalse((self.dev / 'data/app.sqlite').exists())

    def test_existing_configuration_and_missing_token_preserved(self):
        self.dev.mkdir(parents=True)
        for name in ['config.json', 'vite-config.json']:
            (self.dev / name).write_text('{"provider_key_file":"","provider_credentials_file":""}')
        before = {p.name: p.read_bytes() for p in self.dev.iterdir()}
        self.invoke()
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.dev.iterdir()})
        self.assertFalse((self.dev / 'secrets').exists())

    def test_incomplete_or_invalid_existing_instance(self):
        self.dev.mkdir(parents=True)
        self.invoke(1)
        (self.dev / 'config.json').write_text('{}')
        self.invoke(1)
        self.assertFalse((self.dev / 'vite-config.json').exists())
        (self.dev / 'vite-config.json').write_text('invalid')
        self.invoke(1)
        self.assertFalse((self.dev / 'secrets').exists())

    def test_migration_marker_blocks_empty_replacement(self):
        p = self.root / '.runtime/maintenance/promote-pilot/state.json'
        p.parent.mkdir(parents=True)
        for phase in ['prepared', 'archived', 'moved']:
            p.write_text(json.dumps({'phase': phase}))
            self.invoke(1)
            self.assertFalse(self.dev.exists())


if __name__ == '__main__': unittest.main()
