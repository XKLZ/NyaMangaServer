"""Validate deployment material only; never runs Docker, smoke or network IO."""
import ast
import json
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]

class DeploymentMaterial(unittest.TestCase):
    def test_single_port_and_health_targets(self):
        compose = (ROOT / 'deploy/compose.yaml').read_text()
        ports = re.search(r'    ports:\n(.*?)    volumes:', compose, re.S).group(1)
        self.assertEqual(ports.strip(), '- "${NYAMANGA_PORT:-9400}:9400"')
        self.assertIn('http://127.0.0.1:9400/api/v1/health/ready', compose)
        docker = (ROOT / 'deploy/Dockerfile').read_text()
        self.assertEqual(re.findall(r'^EXPOSE (.*)$', docker, re.M), ['9400'])
        self.assertIn('http://127.0.0.1:9400/api/v1/health/ready', docker)
        config = json.loads((ROOT / 'deploy/config.example.json').read_text())
        self.assertEqual(config['listen_addr'], '0.0.0.0:9400')
        self.assertEqual(config['public_url'], 'http://localhost:9400')
        self.assertIn('http://192.168.1.3:19400', (ROOT / 'deploy/README.md').read_text())

    def test_smoke_defaults_without_executing_them(self):
        for filename in ['smoke.py', 'ingestion-smoke.py']:
            tree = ast.parse((ROOT / 'deploy/tests' / filename).read_text())
            defaults = []
            for call in ast.walk(tree):
                if isinstance(call, ast.Call) and isinstance(call.func, ast.Attribute) and call.func.attr == 'add_argument' and call.args and isinstance(call.args[0], ast.Constant) and call.args[0].value == '--url':
                    defaults.extend(k.value.value for k in call.keywords if k.arg == 'default')
            self.assertEqual(defaults, ['http://localhost:9400'])

if __name__ == '__main__':
    unittest.main()
