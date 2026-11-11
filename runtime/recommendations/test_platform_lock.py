"""Platform capability metadata only: no weights, sessions or inference."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import features


class PlatformLockTests(unittest.TestCase):
    def runtime(self, directory):
        return features.Runtime(Path(directory).resolve() / 'missing-models', Path(directory).resolve() / 'cache')

    def test_exact_platform_lock_selection_and_no_inference(self):
        for system, machine, expected in [('Darwin', 'arm64', 'macos-arm64'), ('Linux', 'x86_64', 'linux-amd64'), ('Linux', 'aarch64', 'linux-arm64')]:
            with self.subTest(platform=expected), tempfile.TemporaryDirectory() as directory:
                runtime = self.runtime(directory)
                pins = {}
                for line in Path(features.__file__).with_name('requirements-lock-' + expected + '.txt').read_text().splitlines():
                    if line and not line.startswith('#'):
                        name, version = line.split()[0].split('==')
                        pins[name] = version
                with patch('features.platform.system', return_value=system), patch('features.platform.machine', return_value=machine), patch('features.platform.python_version', return_value='3.12.0'), patch('features.importlib.metadata.version', side_effect=pins.__getitem__), patch.object(runtime, 'model_identity', return_value='anonymous-model-identity'), patch.object(runtime, 'session', side_effect=AssertionError('capabilities inferred')):
                    result = runtime.capabilities()
                    self.assertEqual(result['runtime_platform'], expected)
                    self.assertEqual(len(result['dependencies']), 23)
                    self.assertEqual(result['cpu_threads'], 1)
                    self.assertEqual(runtime.stats['model_loads'], 0)

    def test_unknown_platform_and_wrong_python_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            runtime = self.runtime(directory)
            with patch('features.platform.system', return_value='Unknown'), self.assertRaisesRegex(ValueError, 'runtime_platform_unsupported'):
                runtime.capabilities()
            with patch('features.platform.system', return_value='Linux'), patch('features.platform.machine', return_value='x86_64'), patch('features.platform.python_version', return_value='3.12.1'), self.assertRaisesRegex(ValueError, 'runtime_python_version_mismatch'):
                runtime.capabilities()


if __name__ == '__main__':
    unittest.main()
