"""Anonymous checks for fresh selection and complete four-way annotations."""
import collections
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
import contextlib
import io
import features
import sampling_v2 as v2
import review_v2
from test_study import synthetic_books


class FourRelationTests(unittest.TestCase):
    def document(self):
        pack = {'pack_id': 'anonymous-v2', 'pairs': [{'pair_id': f'pair-{i}'} for i in range(300)]}
        doc = {'schema': v2.LABEL_SCHEMA, 'pack_id': pack['pack_id'], 'annotations': [
            {'pair_id': p['pair_id'], 'label': None, 'reason': '', 'collection_or_partial_overlap': False} for p in pack['pairs']]}
        return pack, doc

    def test_full_round_trip_and_all_relations(self):
        pack, doc = self.document()
        for index, label in enumerate(v2.LABELS):
            doc['annotations'][index]['label'] = label
        doc['annotations'][2]['collection_or_partial_overlap'] = True
        self.assertEqual(v2.validate_labels(pack, json.loads(json.dumps(doc))), doc)

    def test_partial_rows_legacy_labels_duplicate_and_bad_flags_rejected(self):
        for kind in ('partial', 'legacy_schema', 'legacy_label', 'duplicate', 'bad_flag', 'unknown', 'reason'):
            pack, doc = self.document()
            if kind == 'partial': doc['annotations'].pop()
            if kind == 'legacy_schema': doc['schema'] = 'recommendation-labels-v1'
            if kind == 'legacy_label': doc['annotations'][0]['label'] = 'same'
            if kind == 'duplicate': doc['annotations'][1] = doc['annotations'][0].copy()
            if kind == 'bad_flag': doc['annotations'][0]['collection_or_partial_overlap'] = 'yes'
            if kind == 'unknown': doc['annotations'][0]['pair_id'] = 'unknown'
            if kind == 'reason': doc['annotations'][0]['reason'] = 'x' * 4001
            with self.subTest(kind=kind), self.assertRaises(ValueError): v2.validate_labels(pack, doc)

    def test_browser_state_batches_and_complete_export(self):
        root = Path(__file__).resolve().parent
        result = subprocess.run(['node', str(root / 'test_review_v2.cjs')], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)


class SelectionTests(unittest.TestCase):
    def test_completed_pack_refuses_source_read_or_overwrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();path = root / 'candidates.json';path.write_text('immutable')
            with patch.object(v2, 'read_source', side_effect=AssertionError('must not read source')):
                with self.assertRaises(ValueError): v2.initialize(root)
            self.assertEqual(path.read_text(), 'immutable')

    def test_reuse_verified_cache_without_mutating_previous_run(self):
        from PIL import Image
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();source=root/'source';old=root/'old';new=root/'new'
            old.mkdir();new.mkdir();path=source/'Covers/ab'/('ab'+'0'*38+'.jpg');path.parent.mkdir(parents=True)
            Image.new('RGB',(8,8),'blue').save(path)
            books={1:{'cover_path':'/old/Covers/ab/'+path.name}}
            material=({'pack_id':'anonymous','source':{'input_fingerprint':'anonymous'}},books,[1])
            args=SimpleNamespace(algorithm='phash',limit=0);calls=[]
            def infer(image):calls.append(1);return '0000000000000000'
            with patch.object(features,'INPUT',source),patch.object(features,'model_worker',return_value=(infer,'fixed-model')),contextlib.redirect_stdout(io.StringIO()):
                features.run(old,args,material=material,checkpoint_every=50)
                snapshot={p.relative_to(old):p.read_bytes() for p in old.rglob('*') if p.is_file()}
                features.run(new,args,material=material,reuse_from=(old,),checkpoint_every=50)
            self.assertEqual(len(calls),1)
            self.assertEqual(json.loads((new/'resources-phash.json').read_text())['reused_previous_cache'],1)
            self.assertEqual(snapshot,{p.relative_to(old):p.read_bytes() for p in old.rglob('*') if p.is_file()})

    def test_fresh_300_unique_70_30_old_family_excluded_and_batches(self):
        books = synthetic_books()
        ids = sorted(books)
        routes = {name: {} for name in ('phash', 'sscd', 'minilm')}
        for a in ids:
            for delta in (1, 4, 8, 12, 21):
                b = ids[(a + delta) % len(ids)]
                if a != b:
                    for n, name in enumerate(routes): routes[name][tuple(sorted((a, b)))] = .9 - n * .1 - delta * .001
        old = {'pairs': [{'book_ids': [i, i + 1]} for i in range(1, 40, 4)]}
        pools = v2.build_pools(books, routes)
        pairs, isolation = v2.select(books, old, pools)
        self.assertEqual(len({tuple(p['book_ids']) for p in pairs}), 300)
        self.assertEqual(collections.Counter(p['split'] for p in pairs), {'calibration':210, 'test':90})
        self.assertEqual(collections.Counter(p['batch'] for p in pairs), {i:30 for i in range(1,11)})
        self.assertEqual(collections.Counter(p['sampling_pool'] for p in pairs), {'work_existing':50,'series_existing':50,'work_routes':50,'series_routes':50,'hard':80,'ordinary':20})
        self.assertEqual(isolation['known_family_overlap'], 0)
        self.assertEqual(isolation['reviewed_family_test_overlap'], 0)
        self.assertLessEqual(isolation['max_family_pair_appearances'], 12)
        self.assertEqual(pairs, v2.select(books, old, pools)[0])
        for p in pairs:
            if p['sampling_pool'] == 'series_existing':
                a,b = [books[i] for i in p['book_ids']]
                self.assertNotEqual(a['version_group_id'],b['version_group_id'])

    def test_review_display_is_whitelist_and_script_safe(self):
        books = synthetic_books();books[1]['title'] = '</script><img src=x onerror=alert(1)>'
        pack = {'pack_id': 'anonymous', 'pairs': [{'pair_id':'anonymous-pair','book_ids':[1,2],'batch':1,
                                                 'sampling_pool':'PRIVATE_POOL','sampling_score':.923,'split':'PRIVATE_SPLIT','family_ids':['PRIVATE_FAMILY']}]}
        with tempfile.TemporaryDirectory() as tmp:
            tmp = str(Path(tmp).resolve())
            with patch('features.safe_image', side_effect=AssertionError('no real input')):
                display, summary = review_v2.display_data(pack,books,Path(tmp))
                output=Path(tmp)/'review.html';review_v2.render(pack,books,Path(tmp),output)
            text=output.read_text()
        self.assertEqual(set(display['pairs'][0]), {'pair_id','book_ids','batch'})
        for token in ('PRIVATE_POOL','PRIVATE_SPLIT','PRIVATE_FAMILY',books[1]['title'],'collection_id','version_group_id','sampling_score'):
            self.assertNotIn(token,text)
        self.assertIn("connect-src 'none'",text)
        self.assertEqual(summary['missing_cover_references'],2)


if __name__ == '__main__': unittest.main()
