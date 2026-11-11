"""Anonymous pair, Work/CV, and conservative label checks; no private I/O."""
import copy
import tempfile
from pathlib import Path
import unittest
from unittest import mock
import numpy as np
import v4_match as m
import v4_labels as labels
import v4_baselines as baselines
from core import atomic_json, digest
from v4_data import family_folds

PARAMS = {'strategy': 'sscd', 'strong_threshold': .75, 'candidate_threshold': .55}
V2 = {'work_threshold': .9, 'series_threshold': .72, 'title_weight': .86, 'image_weight': .04}


def book(i, title='A story', tags=None, pages=32):
    return {'id': i, 'title': title, 'title_jpn': '', 'pages': pages, 'tags': tags or []}


def signal(cover=.8, risks=None, route=False):
    return {'sscd': cover, 'risks': risks or [], 'fixed_v2_same_work': route}


def row(i, label='same_work'):
    return {'pair_id': str(i), 'book_ids': [i * 2, i * 2 + 1], 'family_ids': ['f' + str(i)], 'label': label, 'reason': '', 'collection_or_partial_overlap': False}


def manual(i, language, **properties):
    return {**book(i), 'languages': [language] if language else [], 'manual_properties': {d: {'value': v} for d, v in properties.items()}}


class MatchTest(unittest.TestCase):
    def test_cover_primary_no_title_or_language_gate(self):
        books = {1: book(1, '完全不同名字', [['language', 'chinese'], ['language', 'translated']]), 2: book(2, 'Unknown title', [['language', 'japanese'], ['other', 'full color'], ['other', 'uncensored']])}
        features = {'sscd': {i: np.eye(1, 512, dtype=np.float32)[0] for i in books}, 'phash': {}, 'minilm': {}}
        s = m.pair_signal(1, 2, books, m.structures(books), features, V2)
        self.assertEqual(m.decision(s, PARAMS)['tier'], 'strong')

    def test_conflict_demotes_does_not_discard(self):
        for risk in m.HARD_RISKS:
            result = m.decision(signal(.99, [risk]), PARAMS)
            self.assertEqual(result['tier'], 'supplementary'); self.assertIn(risk, result['risks'])

    def test_missing_and_negative_cover_distinct_and_v2_union(self):
        self.assertIsNone(m.decision(signal(None), PARAMS)['sscd'])
        self.assertEqual(m.decision(signal(-.2), PARAMS)['sscd'], -.2)
        params = {**PARAMS, 'strategy': 'sscd_union_fixed_v2'}
        self.assertEqual(m.decision(signal(None, route=True), params)['tier'], 'strong')
        self.assertEqual(m.decision(signal(None, route=True), PARAMS)['tier'], 'none')

    def test_nonpreferred_support_dedup_and_contaminated_member(self):
        work = {'id': 'w', 'edition_ids': [2, 3], 'preferred_edition_id': 2}
        result = m.aggregate_work(1, work, {2: signal(.2), 3: signal(.95)}, PARAMS)
        self.assertEqual(result['supporting_edition_id'], 3); self.assertEqual(result['tier'], 'strong')
        contaminated = m.aggregate_work(1, work, {2: signal(.2, ['different_explicit_volume_or_chapter']), 3: signal(.95)}, PARAMS)
        self.assertEqual(contaminated['tier'], 'supplementary'); self.assertEqual(contaminated['conflicting_member_ids'], [2])
        self.assertEqual(len(m.rank_works([result], 5)), 1)
        with self.assertRaisesRegex(ValueError, 'duplicate'): m.rank_works([result, result], 5)
        with self.assertRaisesRegex(ValueError, 'leave_query'): m.aggregate_work(2, work, {}, PARAMS)

    def test_total_topK_stable_not_route_union(self):
        candidates = [{'work_id': str(i), 'tier': 'supplementary', 'matching_score': .6} for i in range(30)]
        self.assertEqual(len(m.rank_works(candidates, 5)), 5)
        self.assertEqual(m.rank_works(candidates, 10), m.rank_works(list(reversed(candidates)), 10))

    def test_unknown_excluded_and_supplementary_fn_for_strong(self):
        rows = [row(1), row(2, 'unrelated'), row(3, 'uncertain')]
        decisions = {r['pair_id']: {'tier': 'supplementary'} for r in rows}
        self.assertEqual(m.metrics(rows, decisions)['tp'], 1); self.assertEqual(m.metrics(rows, decisions)['fp'], 1)
        self.assertEqual(m.metrics(rows, decisions)['excluded_selected'], 1)
        self.assertEqual(m.metrics(rows, decisions, True)['fn'], 1)
        self.assertEqual(m.metrics(rows, decisions, True)['excluded_selected'], 0)

    def test_minimum_counts_no_feasible_null_policy(self):
        rows = [row(i) for i in range(9)]; signals = {r['pair_id']: signal(.99) for r in rows}
        selected = m.select_parameters(rows, signals)
        self.assertFalse(selected['target_met']); self.assertIsNone(selected['parameters'])
        self.assertEqual(m.decision(signal(.99), None)['tier'], 'none')

    def test_finite_grid_and_cv_training_only(self):
        rows = [row(i, 'same_work' if i < 50 else 'unrelated') for i in range(80)]
        signals = {r['pair_id']: signal(.85 if r['label'] == 'same_work' else .2) for r in rows}
        assignments, _ = family_folds(rows)
        result = m.grouped_cross_validation(rows, signals, assignments)
        self.assertEqual(len(result['predictions']), 80); self.assertEqual(result['combined']['fp'], 0)
        self.assertEqual(result['combined']['recall'], 1)
        for fold in result['folds']:
            self.assertFalse(set(fold['training_pair_ids']) & set(fold['validation_pair_ids']))
            self.assertEqual(len(fold['selection']['trials']), 132)
        self.assertEqual(m.GRID, (.4, .45, .5, .55, .6, .65, .7, .75, .8, .85, .9))

    def test_component_split_refused(self):
        rows = [row(i) for i in range(5)]; assignments = [{'pair_id': str(i), 'fold': i, 'component_id': 'same'} for i in range(5)]
        with self.assertRaisesRegex(ValueError, 'leakage'): m.grouped_cross_validation(rows, {}, assignments)


class CacheTest(unittest.TestCase):
    def test_readonly_cache_preserves_float_bytes_missing_fails(self):
        books = {1: book(1, '[Chinese] A story')}
        parsed = m.structures(books); sha = 'model'
        fingerprint = digest([baselines.CLEAN_VERSION, baselines.titles.VERSION, sha, parsed[1]['clean_text'][:8192]])
        vector = np.eye(1, 384, dtype=np.float32)[0] * np.float32(.9999)
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp).resolve()
            with mock.patch.object(baselines, 'ROOT', directory):
                with self.assertRaisesRegex(ValueError, 'missing_no_inference'): baselines.clean_readonly(books, parsed, {}, directory, sha)
                atomic_json(directory / (fingerprint + '.json'), {'fingerprint': fingerprint, 'feature_digest': digest(vector.tolist()), 'feature': vector.tolist(), 'model_sha256': sha, 'parser_version': baselines.titles.VERSION, 'version': baselines.CLEAN_VERSION})
                values, summary = baselines.clean_readonly(books, parsed, {}, directory, sha)
                self.assertEqual(values[1].tobytes(), vector.tobytes()); self.assertEqual(summary['model_loads'], 0)


class LabelsTest(unittest.TestCase):
    def test_eight_stable_presets(self):
        self.assertEqual(list(labels.PRESETS), ['original', 'translated', 'monochrome', 'color', 'censored', 'uncensored', 'special', 'other'])

    def test_no_reverse_inference_defaults_or_japanese(self):
        b = {**book(1, 'Story [Japanese]'), 'language_code': 'japanese', 'is_translated': False, 'is_full_color': False, 'censorship': 'none'}
        inferred = labels.infer(b)
        self.assertTrue(all(v['value'] is None for v in inferred['properties'].values()))

    def test_explicit_translation_color_uncensored(self):
        result = labels.infer(book(1, 'Story [全彩] [無修正]', [['language', 'translated'], ['language', 'chinese']]))
        self.assertEqual([result['properties'][d]['value'] for d in ('translation', 'color', 'censorship')], ['translated', 'color', 'uncensored'])
        self.assertEqual(result['languages'], ['chinese'])
        self.assertIsNone(labels.infer(book(1, tags=[['artist', 'translated']]))['properties']['translation']['value'])

    def test_revision_prose_not_censorship_or_special(self):
        result = labels.infer(book(1, '特别的漫画 [修订版] [修正版]'))
        self.assertIsNone(result['properties']['censorship']['value']); self.assertIsNone(result['properties']['special']['value'])
        self.assertEqual(labels.infer(book(2, 'Story [特別版]'))['properties']['special']['value'], 'special')

    def test_automatic_conflict_and_manual_priority_unknown(self):
        b = book(1, 'Story [全彩]', [['other', 'monochrome']])
        self.assertEqual(labels.infer(b)['properties']['color']['state'], 'conflict')
        b['manual_properties'] = {'color': {'value': 'color'}}
        self.assertEqual(labels.infer(b)['properties']['color']['origin'], 'manual')
        b['manual_properties']['color']['value'] = None
        self.assertEqual(labels.infer(b)['properties']['color']['state'], 'explicit_unknown')

    def test_minimal_distinguishing_and_tie_order(self):
        editions = [manual(1, 'chinese', translation='translated', color='color'), manual(2, 'chinese', translation='original', color='monochrome')]
        result = labels.suggest_work(editions)
        self.assertEqual([r['suggested_presets'][0]['id'] for r in result['editions']], ['translated', 'original'])
        self.assertTrue(all(len(r['suggested_presets']) == 1 for r in result['editions']))

    def test_language_already_sufficient_and_singleton_zero(self):
        result = labels.suggest_work([manual(1, 'chinese', color='color'), manual(2, 'japanese', color='monochrome')])
        self.assertTrue(all(r['suggested_presets'] == [] for r in result['editions']))
        self.assertEqual(labels.suggest_work([manual(1, None, color='color')])['editions'][0]['suggested_presets'], [])

    def test_unknown_not_contrast_common_suppressed_other_fallback(self):
        result = labels.suggest_work([manual(1, 'chinese', color='color'), manual(2, 'chinese')])
        self.assertTrue(all(r['suggested_presets'] == [{'id': 'other', 'label': '其他'}] for r in result['editions']))
        self.assertTrue(all(r['needs_manual_review'] for r in result['editions']))
        common = labels.suggest_work([manual(1, 'chinese', color='color'), manual(2, 'chinese', color='color')])
        self.assertTrue(all(r['suggested_presets'][0]['id'] == 'other' for r in common['editions']))

    def test_custom_identical_preset_text_display_only_and_preserved(self):
        b = {**book(1), 'custom_labels': ['彩色', '<script>', '原版']}; original = copy.deepcopy(b)
        self.assertTrue(all(v['value'] is None for v in labels.infer(b)['properties'].values()))
        result = labels.suggest_work([b, book(2)])
        self.assertEqual(result['editions'][0]['existing_custom_labels'], b['custom_labels']); self.assertEqual(b, original)

    def test_special_needs_explicit_standard_counterpart(self):
        result = labels.suggest_work([manual(1, 'chinese', special='special'), manual(2, 'chinese', special='standard')])
        self.assertEqual(result['editions'][0]['suggested_presets'][0]['id'], 'special')
        self.assertEqual(result['editions'][1]['suggested_presets'][0]['id'], 'other')
        self.assertNotIn('standard', labels.PRESETS)

    def test_creator_namespace_markers_and_unknown_languages_ignored(self):
        b = {**book(1, tags=[['artist', 'color'], ['group', 'uncensored'], ['female', 'special edition']]), 'language_code': 'unknown'}
        result = labels.infer(b)
        self.assertEqual(result['languages'], [])
        self.assertTrue(all(v['value'] is None for v in result['properties'].values()))

    def test_partial_coverage_still_minimal(self):
        editions = [manual(1, 'chinese', translation='translated', color='color'), manual(2, 'chinese', translation='original', color='monochrome'), manual(3, 'chinese')]
        first = labels.suggest_work(editions)['editions'][0]
        self.assertEqual(first['suggested_presets'], [{'id': 'translated', 'label': '译本'}])
        self.assertEqual(first['unresolved_peer_ids'], [3])

    def test_new_member_recalculation_without_mutation(self):
        pair = [manual(1, 'chinese', color='color'), manual(2, 'japanese', color='color')]
        before = copy.deepcopy(pair)
        a = labels.suggest_work(pair)
        b = labels.suggest_work(pair + [manual(3, 'chinese', color='monochrome')])
        self.assertEqual(a['editions'][0]['suggested_presets'], [])
        self.assertEqual(b['editions'][0]['suggested_presets'][0]['id'], 'color'); self.assertEqual(pair, before)


if __name__ == '__main__': unittest.main()
