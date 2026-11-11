"""Anonymous V3 preparation tests: no private source, model inference or answers."""
import collections
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from core import atomic_json
from features import FEATURE_VERSIONS
import review_v3 as review_v2
import sampling_v2 as v2
import sampling_v3 as v3
from test_study import synthetic_books


class V3PreparationTests(unittest.TestCase):
    def test_reviewed_families_and_identical_cover_bytes_excluded(self):
        books = synthetic_books()
        hashes = {i: str(i).zfill(64) for i in books}
        # Different file references, but the exact same bytes in another family.
        books[9]['cover_path'] = 'different-file.jpg'
        hashes[9] = hashes[1]
        old = [{'pairs': [{'book_ids': [1, 2]}]}, {'pairs': [{'book_ids': [5, 6]}]}]
        eligible, family, audit = v3.eligible_books(books, old, hashes)
        self.assertFalse(set(range(1, 13)) & eligible.keys())
        self.assertEqual(audit['additional_exact_cover_books_outside_reviewed_families'], 1)
        self.assertEqual(audit['excluded_known_families'], 3)
        with self.assertRaisesRegex(ValueError, 'isolation'):
            v3.audit_selection([{'book_ids': [9, 13]}], books, old, hashes, family)

    def test_fixed_quotas_unique_batches_diversity_and_determinism(self):
        books = synthetic_books();hashes = {i: str(i).zfill(64) for i in books}
        old = [{'pairs': [{'book_ids': [1, 2]}]}, {'pairs': [{'book_ids': [5, 6]}]}]
        eligible, family, _ = v3.eligible_books(books, old, hashes)
        ids = sorted(eligible);routes = {name: {} for name in FEATURE_VERSIONS}
        for index, a in enumerate(ids):
            for delta in (1, 4, 8, 12, 21):
                b = ids[(index + delta) % len(ids)]
                for n, name in enumerate(routes):
                    routes[name][tuple(sorted((a, b)))] = .9 - n * .1 - delta * .001
        pools = v2.build_pools(eligible, routes)
        pairs, audit = v3.select(eligible, family, pools)
        self.assertEqual(len({tuple(p['book_ids']) for p in pairs}), 120)
        self.assertEqual(collections.Counter(p['sampling_pool'] for p in pairs), dict(v3.QUOTAS))
        self.assertEqual(collections.Counter(p['batch'] for p in pairs), {i: 30 for i in range(1, 5)})
        self.assertEqual({p['split'] for p in pairs}, {'heldout'})
        self.assertEqual(pairs, v3.select(eligible, family, pools)[0])
        self.assertLessEqual(audit['max_family_pair_appearances'], 12)
        self.assertEqual(v3.audit_selection(pairs, books, old, hashes, family)['previous_book_overlap'], 0)

    def test_shortage_refuses_to_relax_isolation_or_silently_change_quotas(self):
        with self.assertRaisesRegex(ValueError, 'insufficient_v3_pool'):
            v3.select({}, {}, {name: {} for name, _ in v3.QUOTAS})

    def test_nearest_neighbors_rank_only_eligible_inputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            for name, version in FEATURE_VERSIONS.items():
                items = {str(i): {'status': 'ok', 'feature': '0000000000000000' if name == 'phash' else [1., 0.]} for i in range(1, 4)}
                atomic_json(root / ('features-' + name + '.json'), {'pack_id': 'anonymous', 'version': version, 'items': items})
            routes = v3.neighbors(root, {'pack_id': 'anonymous'}, {2: {}, 3: {}}, k=1)
            for pairs in routes.values(): self.assertEqual(set(pairs), {(2, 3)})

    def test_completed_booklet_refuses_overwrite_before_reading_inputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();path = root / 'candidates.json';path.write_text('immutable')
            with patch.object(v3, 'verify_v2_freeze', side_effect=AssertionError('must not read source')):
                with self.assertRaisesRegex(ValueError, 'no_overwrite'): v3.prepare(root)
            self.assertEqual(path.read_text(), 'immutable')

    def test_120_booklet_dynamic_count_blind_payload_and_filename(self):
        pack = {'pack_id': 'anonymous-v3', 'pairs': [
            {'pair_id': 'pair-' + str(i), 'book_ids': [1, 2], 'batch': i // 30 + 1,
             'sampling_pool': 'PRIVATE_POOL', 'sampling_score': .987654, 'split': 'heldout'} for i in range(120)]}
        books = synthetic_books();books[1]['title'] = '__PAIR_COUNT__ </script>'
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();target = root / 'review.html'
            summary = review_v2.render(pack, books, root, target, labels_filename='labels.user.v3.json')
            text = target.read_text()
            self.assertIn('120对 · 4批', text)
            self.assertIn('导出完整120对JSON', text)
            self.assertIn('link.download="labels.user.v3.json"', text)
            self.assertNotIn('PRIVATE_POOL', text)
            self.assertNotIn('sampling_score', text)
            self.assertNotIn('300对', text)
            self.assertNotIn('__LABELS_FILENAME__', text)
            self.assertIn('__PAIR_COUNT__ \\u003c/script>', text)
            self.assertIn("connect-src 'none'", text)
            self.assertEqual(summary['missing_cover_references'], 2)
            with self.assertRaises(ValueError):
                review_v2.render(pack, books, root, target, labels_filename='../bad.json')
        import subprocess
        result = subprocess.run(['node', str(Path(__file__).with_name('test_review_v3.cjs'))], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        doc = {'schema': v2.LABEL_SCHEMA, 'pack_id': pack['pack_id'], 'annotations': [
            {'pair_id': p['pair_id'], 'label': v2.LABELS[i % 5], 'reason': '', 'collection_or_partial_overlap': False}
            for i, p in enumerate(pack['pairs'])]}
        self.assertEqual(len(v2.validate_labels(pack, json.loads(json.dumps(doc)))['annotations']), 120)


if __name__ == '__main__': unittest.main()
