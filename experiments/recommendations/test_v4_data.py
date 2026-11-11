"""Anonymous V4 audit tests; never reads private experiment inputs."""
import copy
import tempfile
from pathlib import Path
import unittest
from unittest import mock
import numpy as np
import v4_data as d
from core import atomic_json


def row(name, a, b, family=None, label='same_work'):
    return {'pair_id': name, 'book_ids': [a, b], 'family_ids': family or ['f' + str(a), 'f' + str(b)], 'label': label, 'reason': '', 'collection_or_partial_overlap': False}


class DataTest(unittest.TestCase):
    def test_family_shared_book_and_pair_endpoints_isolate(self):
        rows = [row(str(i), i * 2, i * 2 + 1) for i in range(8)]
        rows += [row('shared', 0, 40), row('family', 50, 51, ['f0'])]
        a, summary = d.family_folds(rows)
        folds = {r['pair_id']: r['fold'] for r in a}
        self.assertEqual(folds['0'], folds['shared']); self.assertEqual(folds['0'], folds['family'])
        self.assertEqual(sum(f['pairs'] for f in summary['folds']), len(rows))
        changed = copy.deepcopy(rows)
        for r in changed: r['label'] = 'unrelated'
        self.assertEqual(a, d.family_folds(changed)[0])
        self.assertEqual(a, d.family_folds(list(reversed(rows)))[0])

    def test_same_cover_fingerprint_isolation(self):
        rows = [row(str(i), i * 2, i * 2 + 1) for i in range(8)]
        assignments, _ = d.family_folds(rows, cover_fingerprints={0: 'image', 2: 'image'})
        folds = {r['pair_id']: r['fold'] for r in assignments}
        self.assertEqual(folds['0'], folds['1'])

    def test_output_canonical_boundary_and_coordinator_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve() / 'run-v4'; root.mkdir()
            with mock.patch.object(d, 'OUTPUT', root):
                with self.assertRaisesRegex(ValueError, 'outside'): d.private_directory(root / 'data/../../run-v3')
                with self.assertRaisesRegex(ValueError, 'coordinator'): d.private_directory(root / 'coordinator/x')
                (root / 'linked').symlink_to(root, target_is_directory=True)
                with self.assertRaisesRegex(ValueError, 'symlink'): d.private_directory(root / 'linked/new')
                self.assertEqual(d.private_directory(root / 'safe'), root / 'safe')

    def test_diagnostic_missing_and_overlapping_causes(self):
        rows = [row('missing', 1, 2), row('low', 3, 4)]
        predictions = [{'pair_id': name, 'prediction': {'action': 'defer', 'scores': {'sscd': score}, 'risks': ['risk'], 'algorithm_version': 'old'}} for name, score in [('missing', None), ('low', .5)]]
        source = Path('/anonymous')
        with mock.patch.object(d, 'read_json', return_value={'predictions': predictions, 'parameters': {'work_threshold': .7}}), mock.patch.object(d, 'file_digest', return_value='sha'):
            result = d.historical_diagnostics(rows, source)
        self.assertEqual(result['misses'][0]['causes'], ['missing_cover_feature', 'risk_gate'])
        self.assertEqual(result['misses'][1]['causes'], ['below_score_threshold', 'risk_gate'])
        self.assertEqual(result['cause_counts']['risk_gate'], 2)

    def test_insufficient_groups_refused(self):
        with self.assertRaisesRegex(ValueError, 'insufficient'):
            d.family_folds([row('one', 1, 2)])

    def test_duplicate_or_reversed_pair_rejected(self):
        a = {'schema': 'test', 'rows': [row('a', 1, 2)]}
        b = {'schema': 'test', 'rows': [row('b', 2, 1, label='unrelated')]}
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            d.merge_rows([(a, 'test'), (b, 'test')], {1: {}, 2: {}})

    def test_path_contradiction_flagged_not_repaired(self):
        rows = [row('a', 1, 2), row('b', 2, 3), row('c', 1, 3, label='unrelated')]
        before = copy.deepcopy(rows); result = d.consistency(rows)
        self.assertEqual(result['same_work_path_issues'][0]['pair_id'], 'c')
        self.assertFalse(result['automatic_transitive_work_expansion']); self.assertEqual(rows, before)

    def test_readonly_feature_float_bytes_and_negative_scores(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp).resolve()
            expected = np.eye(1, 512, dtype=np.float32)[0] * np.float32(.9999)
            for name, version in d.FEATURE_VERSIONS.items():
                feature = '0000000000000000' if name == 'phash' else (expected.tolist() if name == 'sscd' else np.eye(1, 384, dtype=np.float32)[0].tolist())
                atomic_json(directory / ('features-' + name + '.json'), {'algorithm': name, 'version': version, 'pack_id': 'pack', 'input_fingerprint': 'input', 'items': {'1': {'status': 'ok', 'feature': feature}}})
            values, _, _ = d.load_features(directory, {1: {}})
            self.assertEqual(values['sscd'][1].tobytes(), expected.tobytes())
            self.assertLess(float(np.dot(values['sscd'][1], -expected)), 0)

    def test_invalid_norm_and_feature_context_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp).resolve()
            for name, version in d.FEATURE_VERSIONS.items():
                atomic_json(p / ('features-' + name + '.json'), {'algorithm': name, 'version': version, 'pack_id': 'pack', 'input_fingerprint': 'input', 'items': {'1': {'status': 'ok', 'feature': '0' * 16 if name == 'phash' else [0] * (512 if name == 'sscd' else 384)}}})
            with self.assertRaisesRegex(ValueError, 'bad_embedding'): d.load_features(p, {1: {}})

    def test_immutable_output_preserves_original(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp).resolve() / 'result.json'; d.immutable_json(p, {'n': 1}); d.immutable_json(p, {'n': 1})
            with self.assertRaisesRegex(ValueError, 'changed'): d.immutable_json(p, {'n': 2})
            self.assertEqual(d.read_json(p), {'n': 1})


if __name__ == '__main__': unittest.main()
