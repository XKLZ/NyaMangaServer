"""Anonymous tests for source export validation and beta preparation; no Docker."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'deploy' / filename)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


build = module('build_image', 'build-image.py')
prepare = module('prepare_beta', 'prepare-beta.py')


class BetaTools(unittest.TestCase):
    def test_build_proxy_bypass_preserves_existing_local_rules(self):
        self.assertEqual(build.proxy_bypass({'NO_PROXY': 'localhost,127.0.0.1', 'no_proxy': 'localhost'}, 'registry.npmjs.org'), 'localhost,127.0.0.1,registry.npmjs.org')
        for bad in ('*', 'https://registry.npmjs.org', 'host:80', 'one,,two'):
            with self.assertRaises(ValueError):
                build.proxy_bypass({}, bad)

    def test_verified_runtime_locks(self):
        self.assertTrue(build.runtime_image(ROOT).startswith('python:3.12.0-slim-bookworm@sha256:'))

    def test_preparation_is_new_private_and_anonymous(self):
        with tempfile.TemporaryDirectory(dir=ROOT / '.runtime/tests') as temporary:
            target = Path(temporary) / 'new'
            prepare.prepare(target, 'http://192.0.2.3:19400')
            self.assertEqual(target.stat().st_mode & 0o777, 0o700)
            self.assertEqual((target / 'setup-token').stat().st_mode & 0o777, 0o600)
            self.assertEqual(len(list((target / 'fixtures').glob('*.cbz'))), 7)
            self.assertFalse((target / 'data').exists())
            original = (target / 'setup-token').read_bytes()
            with self.assertRaises(ValueError):
                prepare.prepare(target, 'http://192.0.2.3:19400')
            self.assertEqual(original, (target / 'setup-token').read_bytes())

    def test_origin_rejects_secrets_and_path(self):
        for origin in ('file:///tmp/x', 'https://a:b@example.org', 'https://example.org/admin'):
            with self.assertRaises(ValueError):
                prepare.prepare(Path('/invalid/new'), origin)

    def test_source_archive_rejects_tamper_links_and_traversal(self):
        with tempfile.TemporaryDirectory(dir=ROOT / '.runtime/tests') as temporary:
            directory = Path(temporary)
            archive = directory / 'source.tar'
            receipt = directory / 'source.json'
            for filename, link in (('../escape', False), ('link', True), ('.runtime/secret', False), ('safe.txt', False)):
                with tarfile.open(archive, 'w', format=tarfile.PAX_FORMAT, pax_headers={'comment': 'a' * 40}) as bundle:
                    member = tarfile.TarInfo(filename)
                    if link:
                        member.type = tarfile.SYMTYPE
                        member.linkname = '/etc/passwd'
                    else:
                        member.size = 1
                    bundle.addfile(member, None if link else io.BytesIO(b'x'))
                receipt.write_text(json.dumps({'format': 1, 'commit': 'a' * 40, 'sha256': build.digest(archive)}))
                if filename != 'safe.txt':
                    with self.assertRaises(ValueError):
                        build.unpack_source(archive, receipt, directory / 'out')
                else:
                    self.assertEqual(build.unpack_source(archive, receipt, directory / 'out'), 'a' * 40)
                    archive.write_bytes(b'changed')
                    with self.assertRaises(ValueError):
                        build.unpack_source(archive, receipt, directory / 'tampered')

    def test_beta_resource_and_mount_contract(self):
        source = (ROOT / 'deploy/compose.beta.yaml').read_text()
        for expected in ('cpus: 2', 'mem_limit: 2g', 'memswap_limit: 2g', 'anonymous-library:/library/comics:ro', 'models:/models:ro', '19400}:9400', 'max-size: "10m"'):
            self.assertIn(expected, source)
        self.assertNotIn('build:', source)


if __name__ == '__main__':
    unittest.main()
