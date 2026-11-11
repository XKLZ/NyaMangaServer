"""Anonymous installation and version checks; no Docker or live instance access."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import tarfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


configure = load('configure', 'deploy/configure.py')
version = load('release_version', 'scripts/check/release-version.py')
package = load('package_release', 'deploy/package-release.py')


class ReleaseTools(unittest.TestCase):
    def test_install_bundle_contains_no_host_identity_or_runtime(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            release = version.check()
            package.installation_bundle(root, release)
            with tarfile.open(root / f'nyamangaserver-{release}-install.tar.gz') as archive:
                for entry in archive.getmembers():
                    self.assertTrue(entry.isfile())
                    self.assertFalse(Path(entry.name).is_absolute())
                    self.assertNotIn('..', Path(entry.name).parts)
                    self.assertNotIn('.runtime', Path(entry.name).parts)
                    self.assertEqual((entry.uid, entry.gid, entry.uname, entry.gname), (0, 0, '', ''))
                text = archive.extractfile('deploy/README.md').read().decode()
                self.assertNotIn('](../', text)
                self.assertIn('/blob/v' + release + '/', text)

    def test_shared_version(self):
        value = version.check()
        self.assertIn(':' + value, (ROOT / 'deploy/compose.release.yaml').read_text())

    def test_private_repeat_no_overwrite_and_spaces(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            library = root / 'manga directory'
            library.mkdir()
            target = root / 'my instance'
            args = (target, 'http://192.0.2.3:19400', 19400, library, (os.getuid(), os.getgid()))
            configure.prepare(*args)
            original = {p.name: p.read_bytes() for p in target.iterdir()}
            self.assertEqual(target.stat().st_mode & 0o777, 0o700)
            self.assertEqual((target / 'setup-token').stat().st_mode & 0o777, 0o400)
            config = json.loads((target / 'server.json').read_text())
            self.assertEqual(config['library_roots'][0]['path'], '/library/comics')
            self.assertIn('preserved', configure.prepare(*args))
            self.assertEqual(original, {p.name: p.read_bytes() for p in target.iterdir()})
            with self.assertRaises(ValueError):
                configure.prepare(target, 'http://other.example', owner=(os.getuid(), os.getgid()))
            (target / 'server.json').chmod(0o600)
            (target / 'server.json').write_text('{}')
            with self.assertRaises(ValueError):
                configure.prepare(*args)
            self.assertFalse((target / 'data').exists())

    def test_partial_symlink_and_invalid_input_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            existing = root / 'partial'
            existing.mkdir()
            with self.assertRaises(ValueError):
                configure.prepare(existing, 'http://localhost')
            link = root / 'link'
            link.symlink_to(existing)
            with self.assertRaises(ValueError):
                configure.prepare(link, 'http://localhost')
            for origin in ('http://user:secret@host', 'https://example.org/admin', 'file:///tmp', 'http://host:99999', 'http://host/$TOKEN'):
                with self.assertRaises(ValueError):
                    configure.prepare(root / 'unused', origin)
                self.assertFalse((root / 'unused').exists())

    def test_changed_secret_permissions_and_incomplete_receipt_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp).resolve() / 'instance'
            args = (target, 'http://localhost:9400', 9400, None, (os.getuid(), os.getgid()))
            configure.prepare(*args)
            (target / 'setup-token').chmod(0o644)
            with self.assertRaises(ValueError):
                configure.prepare(*args)
            (target / 'setup-token').chmod(0o400)
            path = target / 'installation.json'
            receipt = json.loads(path.read_text())
            receipt['sha256'] = {}
            path.write_text(json.dumps(receipt))
            with self.assertRaises(ValueError):
                configure.prepare(*args)


if __name__ == '__main__':
    unittest.main()
