import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from common import atomic_json, digest
from features import VERSIONS
from import_cache import transfer_item, snapshot_record_matches

class CacheTransferTests(unittest.TestCase):
    def test_nullable_original_title_is_the_only_typed_view_normalization(self):
        original = {'id': 1, 'title': 'anonymous', 'title_jpn': None, 'tags': []}
        typed = {**original, 'title_jpn': ''}
        self.assertTrue(snapshot_record_matches(typed, original))
        self.assertTrue(snapshot_record_matches(typed, typed))
        self.assertFalse(snapshot_record_matches({**typed, 'title': 'changed'}, original))
        self.assertFalse(snapshot_record_matches({**typed, 'tags': None}, original))
        self.assertFalse(snapshot_record_matches({**typed, 'title_jpn': 'other'}, original))
        self.assertFalse(snapshot_record_matches({**typed, 'language_code': ''}, {**original, 'language_code': None}))
        self.assertFalse(snapshot_record_matches(typed, {k:v for k,v in original.items() if k != 'title_jpn'}))
        self.assertIsNone(original['title_jpn'])

    def test_exact_input_only_and_no_overwrite(self):
        with tempfile.TemporaryDirectory(dir=Path(tempfile.gettempdir()).resolve()) as root:
            root = Path(root)
            raw = b'anonymous bytes'
            book = {'id': 1, 'title': 'anonymous', 'title_jpn': ''}
            sha = hashlib.sha256(raw).hexdigest()
            fp = digest([VERSIONS['phash'], 'none', sha])
            feature = '0000000000000001'
            item = {'fingerprint': fp, 'input_sha256': sha, 'algorithm_version': VERSIONS['phash'], 'model_sha256': 'none', 'feature': feature, 'feature_digest': digest(feature)}
            atomic_json(root / 'old/phash' / (fp + '.json'), item)
            index = {'version': VERSIONS['phash'], 'model_sha256': 'none', 'items': {'1': {'status': 'ok', 'fingerprint': fp, 'feature': feature}}}
            self.assertEqual(transfer_item('phash', index, book, raw, [root/'old'], root/'new', 'none'), 'reused')
            self.assertEqual(transfer_item('phash', index, book, raw, [root/'old'], root/'new', 'none'), 'already_present')
            self.assertEqual(transfer_item('phash', index, book, b'changed', [root/'old'], root/'new', 'none'), 'input_or_version_mismatch')
            self.assertEqual(transfer_item('phash', index, book, None, [root/'old'], root/'new', 'none'), 'missing_cover')
            item['feature_digest'] = 'invalid'
            atomic_json(root / 'old/phash' / (fp + '.json'), item)
            self.assertEqual(transfer_item('phash', index, book, raw, [root/'old'], root/'new2', 'none'), 'unverified_cache')

if __name__ == '__main__': unittest.main()
