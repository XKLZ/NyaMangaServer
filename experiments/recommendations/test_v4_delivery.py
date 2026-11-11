"""Anonymous offline publication, forward ranking and review safety checks."""
import contextlib
from html.parser import HTMLParser
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import numpy as np
import core
import v4_data as data
import v4_match as match
import v4_run as run
import v4_review as review
from core import atomic_json, file_digest
from test_v4_rules import book, row, PARAMS, V2


class Parser(HTMLParser):
    def __init__(self): super().__init__(); self.tags = []; self.attributes = []
    def handle_starttag(self, tag, attrs): self.tags.append(tag); self.attributes.extend(attrs)


class DeliveryTest(unittest.TestCase):
    def test_final_directory_not_activated_on_cancel_or_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); output = root / 'final'
            with mock.patch.object(data, 'OUTPUT', root):
                with self.assertRaises(InterruptedError):
                    with run.atomic_directory(output) as pending:
                        (pending / 'artifact').write_text('partial')
                        raise InterruptedError('cancelled')
                self.assertFalse(output.exists()); self.assertEqual(list(root.iterdir()), [])
                with self.assertRaises(InterruptedError):
                    with run.atomic_directory(output) as pending:
                        (pending / 'artifact').write_text('partial')
                        with mock.patch.object(core, 'CANCELLED', True): core.checkpoint()
                self.assertFalse(output.exists()); self.assertEqual(list(root.iterdir()), [])
                with run.atomic_directory(output) as pending: atomic_json(pending / 'result.json', {'complete': True})
                self.assertTrue(output.is_dir()); self.assertEqual((output / 'result.json').stat().st_mode & 0o777, 0o600)
                with self.assertRaisesRegex(ValueError, 'exists'):
                    with run.atomic_directory(output): pass

    def test_worker_exclusion(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp).resolve()
            with run.worker_lock(directory):
                with self.assertRaisesRegex(ValueError, 'another'):
                    with run.worker_lock(directory): pass

    def test_forward_reverse_direct_annotation_known_not_unknown(self):
        books = {i: book(i) for i in (1, 2, 3)}
        rows = [row(1), row(2)]
        rows[0]['book_ids'] = [1, 2]; rows[1]['book_ids'] = [2, 3]
        features = {'sscd': {i: np.eye(1, 512, dtype=np.float32)[0] for i in books}, 'minilm': {}, 'phash': {}}
        cv = {'folds': [{'fold': 0, 'selection': {'parameters': PARAMS}}]}
        folds = [{'pair_id': r['pair_id'], 'fold': 0} for r in rows]
        with contextlib.redirect_stdout(io.StringIO()):
            result = run.forward_ranking(books, rows, match.structures(books), features, V2, cv, folds)
        query2 = next(q for q in result['queries'] if q['query_edition_id'] == 2)
        self.assertEqual(query2['candidate_counts']['known_positive_selected'], 2)
        self.assertEqual(query2['candidate_counts'].get('unlabeled_selected', 0), 0)
        neighbor1 = next(c for c in query2['final_top20'] if c['supporting_edition_id'] == 1)
        self.assertEqual(neighbor1['annotated_relation'], 'same_work')
        self.assertTrue(all(c['supporting_edition_id'] != q['query_edition_id'] for q in result['queries'] for c in q['final_top20']))

    def test_complete_triangles_only_no_chain(self):
        books = {i: book(i) for i in (1, 2, 3)}
        rows = [row(i) for i in range(3)]
        for r, pair in zip(rows, ([1, 2], [2, 3], [1, 3])): r['book_ids'] = pair
        features = {'sscd': {i: np.eye(1, 512, dtype=np.float32)[0] for i in books}, 'minilm': {}, 'phash': {}}
        cv = {'folds': [{'fold': 0, 'selection': {'parameters': PARAMS}}]}; folds = [{'pair_id': r['pair_id'], 'fold': 0} for r in rows]
        self.assertEqual(run.explicit_triangles(books, rows[:2], match.structures(books), features, V2, cv, folds), [])
        examples = run.explicit_triangles(books, rows, match.structures(books), features, V2, cv, folds)
        self.assertEqual(len(examples), 1); self.assertEqual(examples[0]['existing_work']['edition_ids'], [2, 3])
        self.assertEqual(examples[0]['result']['query_edition_id'], 1)

    def test_layer_excluded_counts_and_supplementary_precision(self):
        rows = [row(1), row(2, 'unrelated'), row(3, 'uncertain'), row(4, 'uncertain')]
        predictions = {str(1): {'tier': 'supplementary'}, str(2): {'tier': 'supplementary'}, str(3): {'tier': 'strong'}, str(4): {'tier': 'supplementary'}}
        metrics = run.tier_metrics(rows, predictions)
        self.assertEqual(metrics['strong']['excluded_selected'], 1)
        self.assertEqual(metrics['supplementary_only']['excluded_selected'], 1)
        self.assertEqual(metrics['combined']['excluded_selected'], 2)
        self.assertEqual(metrics['supplementary_only']['precision'], .5)

    def test_json_script_breakout_escaped_and_no_network_api(self):
        value = {'title': '</script><img src="https://invalid/" onerror="bad()">&\u2028\u2029'}
        escaped = review.safe_json(value)
        self.assertNotIn('<', escaped); self.assertNotIn('&', escaped); self.assertEqual(json.loads(escaped), value)
        parser = Parser(); parser.feed(review.PAGE.replace('__PAYLOAD__', escaped))
        self.assertEqual(parser.tags.count('script'), 2)
        self.assertNotIn('img', parser.tags)
        self.assertFalse(any(k in ('src', 'href') and str(v).startswith(('http:', 'https:', '//')) for k, v in parser.attributes))
        for forbidden in ('fetch(', 'XMLHttpRequest', 'WebSocket(', '.innerHTML', 'eval('): self.assertNotIn(forbidden, review.PAGE)
        self.assertIn("connect-src 'none'", review.PAGE)

    def test_cover_mapping_and_display_status_not_empty_truth(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); source = root / 'source'; output = root / 'output'; output.mkdir()
            covers = source / 'Covers/aa'; covers.mkdir(parents=True)
            cover = covers / ('a' * 40 + '.jpg'); cover.write_bytes(b'jpeg-placeholder')
            books = {1: book(1, '<private>'), 2: book(2)}
            exported = [{'cover_path': 'Covers/aa/' + 'a' * 40 + '.jpg', **books[1]}, {**books[2], 'cover_path': None}]
            export = root / 'books.json'; atomic_json(export, {'books': exported})
            report = {'diagnostics': {'rows': [{'book_ids': [1, 2]}]}, 'forward_retrieval': {'queries': []}}
            with mock.patch.object(review, 'ROOT', root):
                display, summary = review.display_books(report, books, output, export, source)
            self.assertEqual(summary['unique_cover_assets'], 1); self.assertEqual(summary['missing_or_rejected_books'], 1)
            self.assertEqual(file_digest(output / display['1']['image']), file_digest(cover))
            self.assertIn('未核对', display['1']['custom_labels_status'])
            self.assertIn(str(cover.relative_to(root)), summary['private_input_sha256'])

    def test_verify_rejects_manifest_input_escape_dev_and_artifact_escape(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); v4 = root / '.runtime/tests/runs/recommendations/run-v4'; final = v4 / 'final'; final.mkdir(parents=True)
            with mock.patch.object(data, 'ROOT', root), mock.patch.object(data, 'OUTPUT', v4), mock.patch.object(run, 'code_hashes', return_value={}):
                for bad in ('/etc/passwd', '../escape', '.runtime/dev/data/app.sqlite', 'README.md', '.runtime/tests/runs/recommendations/run-v4/coordinator/private'):
                    atomic_json(final / 'manifest.json', {'code_sha256': {}, 'input_sha256': {bad: 'sha'}, 'files': {}})
                    with self.assertRaises(ValueError): run.verify(final)
                atomic_json(final / 'manifest.json', {'code_sha256': {}, 'input_sha256': {}, 'files': {'../outside': 'sha'}})
                with self.assertRaises(ValueError): run.verify(final)
                with self.assertRaises(ValueError): run.verify(v4 / 'coordinator')
                with self.assertRaises(ValueError): run.verify(root / 'outside')

    def test_forged_policy_baseline_paths_rejected_before_read(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); v4 = root / '.runtime/tests/runs/recommendations/run-v4'; v4.mkdir(parents=True)
            for field in ('historical_input_sha256', 'clean_cache'):
                baseline = {'same_frozen_decisions': True, 'fixed_v2_new120_historical_aggregate_match': True, 'historical_input_sha256': {}, 'clean_cache': {}}
                if field == 'historical_input_sha256': baseline[field] = {'.runtime/dev/data/app.sqlite': 'sha'}
                else: baseline[field] = {'dev': {'cache_sha256': {'../private': 'sha'}}}
                def read(path):
                    name = path.name
                    if name == 'books.json': return {'books': []}
                    if name == 'rows.json': return {'rows': []}
                    if name == 'folds.json': return {'assignments': []}
                    if name == 'baseline-audit-reviewed.json': return baseline
                    return {}
                with mock.patch.object(data, 'ROOT', root), mock.patch.object(data, 'OUTPUT', v4), mock.patch.object(data, 'prepare', return_value={}), mock.patch.object(data, 'read_json', side_effect=read), mock.patch.object(data, 'load_features', return_value=({}, {}, {})), mock.patch.object(run, 'file_digest', side_effect=AssertionError('must_not_read_forged_path')):
                    with self.assertRaises(ValueError): run.load_context(v4 / 'stage2')

    def test_cancel_before_final_rename(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve(); output = root / 'final'
            with mock.patch.object(data, 'OUTPUT', root):
                with self.assertRaises(InterruptedError):
                    with run.atomic_directory(output) as pending:
                        atomic_json(pending / 'ready.json', {})
                        core.CANCELLED = True
                core.CANCELLED = False
                self.assertFalse(output.exists())


if __name__ == '__main__': unittest.main()
