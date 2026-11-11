"""Anonymous coordinator boundaries; never open real labels or feature indexes."""
import json
from pathlib import Path
import stat
import tempfile
import unittest
from core import atomic_json, file_digest
import coordinate_v3 as c


def pair_material(count=120):
    pack = {'pack_id': 'anonymous', 'pairs': [{'pair_id': str(i), 'book_ids': [1, 2], 'family_ids': ['anonymous-family']} for i in range(count)]}
    labels = {'schema': 'recommendation-labels-v2', 'pack_id': 'anonymous', 'annotations': [
        {'pair_id': str(i), 'label': 'unrelated', 'reason': '', 'collection_or_partial_overlap': False} for i in range(count)]}
    return pack, labels


class CoordinatorTests(unittest.TestCase):
    def test_complete_rows_drop_sampling_fields_but_preserve_annotations(self):
        pack, labels = pair_material(1);pack['pairs'][0]['sampling_score'] = .99
        labels['annotations'][0].update(label='uncertain', collection_or_partial_overlap=True)
        row = c.labeled_rows(pack, labels)[0]
        self.assertEqual(set(row), {'pair_id', 'book_ids', 'family_ids', 'label', 'reason', 'collection_or_partial_overlap'})
        self.assertEqual(row['label'], 'uncertain')
        self.assertTrue(row['collection_or_partial_overlap'])
        labels['annotations'][0]['label'] = None
        with self.assertRaisesRegex(ValueError, 'incomplete'): c.labeled_rows(pack, labels)

    def test_seal_idempotence_permissions_and_changed_answers_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();pack, labels = pair_material()
            atomic_json(root / 'candidates.json', pack);atomic_json(root / 'labels.user.v3.json', labels)
            raw = (root / 'labels.user.v3.json').read_bytes()
            c.seal(root);c.seal(root)
            self.assertEqual(raw, (root / 'labels.user.v3.json').read_bytes())
            self.assertEqual(stat.S_IMODE((root / 'labels.user.v3.json').stat().st_mode), 0o600)
            sealed = (root / 'coordinator/heldout-seal.json').read_bytes()
            labels['annotations'][0]['label'] = 'same_work'
            atomic_json(root / 'labels.user.v3.json', labels)
            with self.assertRaisesRegex(ValueError, 'immutable'): c.seal(root)
            self.assertEqual(sealed, (root / 'coordinator/heldout-seal.json').read_bytes())

    def test_development_feature_subset_and_digest_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();source = root / 'source';output = root / 'development'
            for name in ('phash', 'sscd', 'minilm'):
                atomic_json(source / ('features-' + name + '.json'), {'items': {'1': {'feature': 'a'}, '3': {'feature': 'not-authorized'}}})
            pack, labels = pair_material(1)
            c.write_inputs(output, [{'id': 1}, {'id': 2}], pack, labels, source, {'parameters': {}}, False)
            for name in ('phash', 'sscd', 'minilm'):
                self.assertEqual(set(json.loads((output / ('features-' + name + '.json')).read_text())['items']), {'1'})
            manifest = json.loads((output / 'input-manifest.json').read_text())
            self.assertEqual(set(manifest['files']), set(c.INPUT_FILES))
            self.assertTrue(all(file_digest(output / name) == sha for name, sha in manifest['files'].items()))

    def test_release_requires_explicit_digest_and_refuses_after_started(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();directory = root / 'analysis'
            atomic_json(directory / 'frozen-policy-v3.json', {'parameters': {}})
            with self.assertRaisesRegex(ValueError, 'explicit_approved'):
                c.release(None, root)
            with self.assertRaisesRegex(ValueError, 'explicit_approved'):
                c.release('0' * 64, root)
            (directory / 'heldout-evaluation.started').write_text('immutable')
            with self.assertRaisesRegex(ValueError, 'already_started'):
                c.release(file_digest(directory / 'frozen-policy-v3.json'), root)
            self.assertFalse((directory / 'heldout.json').exists())


if __name__ == '__main__': unittest.main()
