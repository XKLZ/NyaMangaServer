"""Anonymous source-only boundary checks, including a checkout without web assets."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class BoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ('scripts/check', 'cmd/server', 'internal/webassets', 'internal/model'):
            (self.root / name).mkdir(parents=True)
        shutil.copyfile(ROOT / 'scripts/check/boundaries.py', self.root / 'scripts/check/boundaries.py')
        (self.root / 'go.mod').write_text('module anonymous.local/server\n\ngo 1.27.0\n')
        (self.root / 'cmd/server/main.go').write_text('package main\nimport _ "anonymous.local/server/internal/webassets"\nfunc main() {}\n')
        (self.root / 'internal/webassets/assets.go').write_text('package webassets\nimport _ "embed"\n//go:embed all:dist\nvar content string\n')
        self.model = self.root / 'internal/model/model.go'
        self.model.write_text('package model\n')

    def invoke(self, source_only=True):
        return subprocess.run(
            ['python3', str(self.root / 'scripts/check/boundaries.py'), *(['--source-only'] if source_only else [])],
            env={**os.environ, 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off'},
            capture_output=True, text=True,
        )

    def test_source_only_accepts_missing_generated_assets(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('PASS', result.stdout)

    def test_default_still_requires_generated_assets(self):
        self.assertNotEqual(self.invoke(source_only=False).returncode, 0)

    def test_missing_assets_do_not_hide_forbidden_imports(self):
        self.model.write_text('package model\nimport _ "database/sql"\n')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('imports forbidden database/sql', result.stderr)

    def test_source_only_rejects_other_package_errors(self):
        self.model.write_text('package model\nimport _ "anonymous.local/server/missing"\n')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('missing', result.stderr)


if __name__ == '__main__':
    unittest.main()
