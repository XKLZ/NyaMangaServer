import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from core import atomic_json
import series2_run as run
from series2_metrics import audit
import test_series_data as fixtures


def proposal(ids):
    return {'id':','.join(ids),'work_ids':ids,'tier':'strong','order':{'suggested_order':None}}


class RunTests(unittest.TestCase):
    def test_cancelled_publication_is_private_and_never_activated(self):
        with tempfile.TemporaryDirectory() as directory,patch.object(run,'OUTPUT',Path(directory).resolve()):
            d=Path(directory).resolve()
            p=Path(d)/'final'
            with self.assertRaises(InterruptedError):
                with run.publication(p) as tmp:
                    atomic_json(tmp/'report.json',{'anonymous':True})
                    self.assertEqual((tmp.stat().st_mode&0o777),0o700)
                    self.assertEqual(((tmp/'report.json').stat().st_mode&0o777),0o600)
                    raise InterruptedError()
            self.assertFalse(p.exists());self.assertEqual(list(Path(d).iterdir()),[])

    def test_manifest_tamper_unexpected_files_and_overwrite_rejected(self):
        with tempfile.TemporaryDirectory() as directory,patch.object(run,'OUTPUT',Path(directory).resolve()):
            d=Path(directory).resolve()
            with run.publication(Path(d)/'final') as p:
                atomic_json(p/'report.json',{});run.manifest(p)
            p=Path(d)/'final'
            self.assertEqual(len(run.verify_dir(p)['files']),1)
            with self.assertRaises(ValueError):
                with run.publication(p):pass
            (p/'extra').write_text('')
            with self.assertRaises(ValueError):run.verify_dir(p)
            (p/'extra').unlink();(p/'report.json').write_text('{"changed":true}')
            with self.assertRaises(ValueError):run.verify_dir(p)

    def test_output_cannot_escape_or_touch_reserved_history(self):
        for p in (run.OUTPUT.parent/'series-v1',run.OUTPUT/'coordinator/a',run.OUTPUT/'../other'):
            with self.assertRaises(ValueError):run.path_in_output(p)
        with tempfile.TemporaryDirectory() as directory,patch.object(run,'OUTPUT',Path(directory).resolve()):
            d=Path(directory).resolve()
            (Path(d)/'link').symlink_to('/tmp')
            with self.assertRaises(ValueError):run.path_in_output(Path(d)/'link/file')

    def test_metrics_never_turn_unknown_into_negative_or_self_into_prediction(self):
        rows=[{**fixtures.row(1,2,'same_series'),'work_ids':['a','b']},
              {**fixtures.row(2,3,'unrelated'),'work_ids':['b','c']},
              {**fixtures.row(4,5,'same_work'),'work_ids':['d','d']},
              {**fixtures.row(6,7,'uncertain'),'work_ids':['a','z']}]
        r=audit([proposal(['a','b','z']),proposal(['d','f'])],[],rows,['a','b','c','d','f','z'])
        self.assertEqual(r['pair_metrics']['combined']['tp'],1)
        self.assertEqual(r['pair_metrics']['combined']['fp'],0)
        self.assertEqual(r['groups_with_unknown_relations'],2)
        self.assertFalse(r['group_accuracy_claimed'])
        self.assertEqual(r['duplicate_memberships'],0)

    def test_duplicate_membership_cannot_pass_report(self):
        with self.assertRaises(ValueError):audit([proposal(['a','b']),proposal(['b','c'])],[],[],['a','b','c'])


if __name__=='__main__':unittest.main()
