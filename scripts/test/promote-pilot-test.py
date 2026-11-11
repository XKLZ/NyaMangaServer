import sys
sys.dont_write_bytecode = True
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import fcntl

spec = importlib.util.spec_from_file_location('promotion', Path(__file__).parents[1] / 'dev/promote-pilot.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class PromotionTests(unittest.TestCase):
    def setUp(self):
        tmp = Path(__file__).resolve().parents[2] / '.runtime/tests/tmp'
        tmp.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=tmp)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for rel in (m.SOURCE, m.TARGET):
            p = self.root / rel
            (p / 'data').mkdir(parents=True)
            (p / 'secrets').mkdir()
            (p / 'secrets/setup-token').write_text('anonymous-secret')
            (p / 'data/material').write_bytes(b'anonymous-material')
            config = {'data_dir': 'data', 'setup_token_file': 'secrets/setup-token',
                      'provider_key_file': 'secrets/key', 'provider_credentials_file': 'secrets/vault',
                      'work_recommendations': {'python': '../../../../../shared/python', 'worker': 'worker.py', 'models_dir': '../../../../../shared/models'},
                      'library_roots': [{'path': 'source-unavailable', 'root_key': 'anonymous'}],
                      'listen_addr': '127.0.0.1:8081', 'public_url': 'http://127.0.0.1:5174'}
            (p / 'config.json').write_text(json.dumps(config))
            db = sqlite3.connect(p / 'data/app.sqlite')
            db.executescript("CREATE TABLE jobs(state); CREATE TABLE schema_migrations(version); INSERT INTO schema_migrations VALUES(11); CREATE TABLE accounts(id, secret); INSERT INTO accounts VALUES('anonymous', x'00ff');")
            db.close()

    def run_migration(self, **kw):
        return m.run(self.root, check_ports=False, **kw)

    def test_complete_repeat_and_paths(self):
        original = json.loads((self.root / m.SOURCE / 'config.json').read_text())
        result = self.run_migration()
        self.assertEqual(result['before'], result['after'])
        self.assertEqual(result, self.run_migration())
        p = self.root / m.TARGET
        config = json.loads((p / 'vite-config.json').read_text())
        self.assertEqual(config['public_url'], 'http://127.0.0.1:5173')
        self.assertEqual(Path(config['data_dir']), p / 'data')
        self.assertEqual(Path(config['library_roots'][0]['path']), p / 'source-unavailable')
        self.assertEqual(Path(config['work_recommendations']['python']), m.resolve(self.root / m.SOURCE, original['work_recommendations']['python']))
        self.assertEqual(Path(config['provider_key_file']), p / 'secrets/key')
        self.assertEqual((p / 'config.json').stat().st_mode & 0o777, 0o600)

    def test_archive_conflict(self):
        (self.root / m.ARCHIVE).mkdir(parents=True)
        with self.assertRaises(RuntimeError): self.run_migration()
        self.assertTrue((self.root / m.SOURCE).exists())

    def test_resume_each_interruption(self):
        for point in ('archived', 'moved'):
            with self.subTest(point=point):
                # Recreate fixture per interruption.
                if point == 'moved': self.setUp()
                with self.assertRaises(RuntimeError): self.run_migration(failpoint=point)
                self.assertEqual(self.run_migration()['phase'], 'complete')

    def test_conflict_after_interruption(self):
        with self.assertRaises(RuntimeError): self.run_migration(failpoint='archived')
        (self.root / m.TARGET).mkdir()
        with self.assertRaises(RuntimeError): self.run_migration()
        self.assertTrue((self.root / m.SOURCE).exists())

    def test_rollback(self):
        original = (self.root / m.SOURCE / 'config.json').read_bytes()
        self.run_migration()
        self.assertEqual(self.run_migration(rollback=True)['phase'], 'rolled_back')
        self.assertEqual((self.root / m.SOURCE / 'config.json').read_bytes(), original)
        self.assertFalse((self.root / m.SOURCE / 'vite-config.json').exists())
        self.assertTrue((self.root / m.TARGET / 'data/app.sqlite').exists())

    def test_runtime_logs_survive_rollback(self):
        self.run_migration()
        (self.root / m.TARGET / 'api.log').write_text('anonymous runtime log')
        self.run_migration(rollback=True)
        self.assertEqual((self.root / m.SOURCE / 'api.log').read_text(), 'anonymous runtime log')

    def test_modified_configuration_blocks_rollback(self):
        self.run_migration()
        p = self.root / m.TARGET / 'config.json'
        c = json.loads(p.read_text())
        c['session_cookie_name'] = 'changed'
        p.write_text(json.dumps(c))
        with self.assertRaises(RuntimeError): self.run_migration(rollback=True)

    def test_rollback_preserves_newer_data(self):
        self.run_migration()
        with sqlite3.connect(self.root / m.TARGET / 'data/app.sqlite') as db:
            db.execute("INSERT INTO accounts VALUES('new', 'new')")
        with self.assertRaises(RuntimeError): self.run_migration(rollback=True)
        self.assertTrue((self.root / m.TARGET).exists())

    def test_tasks_and_lock_block(self):
        dbpath = self.root / m.SOURCE / 'data/app.sqlite'
        for state in ('queued', 'running', 'paused'):
            with sqlite3.connect(dbpath) as db:
                db.execute('DELETE FROM jobs')
                db.execute('INSERT INTO jobs VALUES(?)', (state,))
            with self.assertRaises(RuntimeError): self.run_migration()
        with sqlite3.connect(dbpath) as db: db.execute('DELETE FROM jobs')
        with open(dbpath.parent / 'app.lock', 'a+') as f:
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(BlockingIOError): self.run_migration()

    def test_source_change_after_preparation(self):
        with self.assertRaises(RuntimeError): self.run_migration(failpoint='archived')
        with sqlite3.connect(self.root / m.SOURCE / 'data/app.sqlite') as db:
            db.execute("INSERT INTO accounts VALUES('changed', 'keep')")
        with self.assertRaises(RuntimeError): self.run_migration()

    def test_symlink_refused(self):
        (self.root / m.ARCHIVE).parent.mkdir(parents=True)
        (self.root / m.ARCHIVE).symlink_to(self.root / m.SOURCE, target_is_directory=True)
        with self.assertRaises(RuntimeError): self.run_migration()


if __name__ == '__main__': unittest.main()
