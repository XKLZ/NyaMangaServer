#!/usr/bin/env python3
"""Anonymous deletion-engine tests: no real runtime paths or services."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('cleanup', Path(__file__).parents[1] / 'dev/prepare-restore-cleanup.py')
cleanup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cleanup)


class CleanupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.record = self.root / cleanup.RECORD
        self.record.mkdir(parents=True)
        self.target = self.root / '.runtime/dev'
        self.target.mkdir()
        (self.target / 'old').write_text('anonymous')
        self.keep = self.root / '.runtime/bin/server'
        self.keep.parent.mkdir()
        self.keep.write_bytes(b'fixed executable')
        self.journal = self.record / 'deletion.json'

    def run_plan(self, plan, **kwargs):
        cleanup.apply(self.root, plan, self.journal, lambda: None, **kwargs)

    def test_delete_preserves_program_and_record_and_repeat(self):
        plan = cleanup.preview(self.root)
        self.run_plan(plan)
        self.run_plan(plan)
        self.assertFalse(self.target.exists())
        self.assertEqual(self.keep.read_bytes(), b'fixed executable')
        self.assertTrue(self.journal.exists())

    def test_recreated_target_rejected(self):
        plan = cleanup.preview(self.root)
        self.run_plan(plan)
        self.target.mkdir()
        (self.target / 'new').write_text('new instance')
        with self.assertRaises(RuntimeError): self.run_plan(plan)
        self.assertEqual((self.target / 'new').read_text(), 'new instance')

    def test_resume_after_intent_and_partial_removal(self):
        for i in range(260): (self.target / str(i)).write_text('data')
        plan = cleanup.preview(self.root)
        self.run_plan(plan, max_batches=1)
        self.assertTrue(self.target.exists())
        self.run_plan(plan)
        self.assertFalse(self.target.exists())

    def test_empty_recreated_target_rejected(self):
        plan = cleanup.preview(self.root)
        self.run_plan(plan)
        self.target.mkdir()
        with self.assertRaises(RuntimeError): self.run_plan(plan)
        self.assertTrue(self.target.is_dir())

    def test_new_file_aborts_before_deletion(self):
        plan = cleanup.preview(self.root)
        (self.target / 'new').write_text('new')
        with self.assertRaises(RuntimeError): self.run_plan(plan)
        self.assertTrue((self.target / 'old').exists())

    def test_changed_file_rejected(self):
        plan = cleanup.preview(self.root)
        (self.target / 'old').write_text('changed')
        with self.assertRaises(RuntimeError): self.run_plan(plan)

    def test_unexplained_missing_file_rejected(self):
        plan = cleanup.preview(self.root)
        (self.target / 'old').unlink()
        with self.assertRaises(RuntimeError): self.run_plan(plan)

    def test_symlink_in_tree_or_ancestor_rejected(self):
        (self.target / 'link').symlink_to(self.keep)
        with self.assertRaises(RuntimeError): cleanup.preview(self.root)
        (self.target / 'link').unlink()
        plan = cleanup.preview(self.root)
        (self.target / 'old').unlink()
        self.target.rmdir()
        self.target.symlink_to(self.keep.parent)
        with self.assertRaises(RuntimeError): self.run_plan(plan)
        self.assertTrue(self.keep.exists())

    def test_forbidden_root_and_traversal(self):
        for p in ('.runtime', '.git', '../outside', '.runtime/dev/../bin', '.runtime/bin/server', cleanup.RECORD):
            self.assertFalse(cleanup.allowed(p))
        plan = cleanup.preview(self.root)
        plan['paths'] = ['.runtime/bin']
        with self.assertRaises(RuntimeError): self.run_plan(plan)

    def test_running_guard_stops_before_deletion(self):
        def busy(): raise RuntimeError('process running')
        with self.assertRaises(RuntimeError):
            cleanup.apply(self.root, cleanup.preview(self.root), self.journal, busy)
        self.assertTrue((self.target / 'old').exists())

    def test_journal_mismatch_rejected(self):
        plan = cleanup.preview(self.root)
        cleanup.private_write(self.journal, {'manifest_sha256': 'wrong'})
        with self.assertRaises(RuntimeError): self.run_plan(plan)


if __name__ == '__main__':
    unittest.main()
