"""Anonymous targeted tests for the sealed v2 cached analysis."""
import copy
import json
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch
import numpy as np
import analyze_v2 as a


def book(i, title, creators=None, **kwargs):
    return {"id": i, "title": title, "title_jpn": None, "pages": 30, "tags": creators or [], **kwargs}


def signal(title=1., series_title=1., **kwargs):
    return {"title": title, "series_title": series_title, "same_core": True, "same_stem": True,
            "explicit_difference": False, "explicit_same": False, "tentative_difference": False,
            "creator": {"shared": True, "conflict": False}, "pages": 1., "scores": {"phash": .9, "sscd": .9, "minilm": .9},
            "empty_title": False, "ordinal_ambiguity": False, **kwargs}


PARAMS = {"work_threshold": .86, "series_threshold": .78, "title_weight": .86, "image_weight": .04}


class TitleTests(unittest.TestCase):
    def test_unknown_brackets_intrinsic_digits_and_modifiers(self):
        for title, expected in [("Agent 007", "agent007"), ("3×3 Eyes", "33eyes"), ("[Secret Garden]", "secretgarden"), ("The (English Patient)", "theenglishpatient")]:
            got = a.structure(book(1, title), {})
            self.assertEqual(got['core'], expected)
        self.assertEqual(a.structure(book(1, '[English] Agent 007 [DL版]'), {})['core'], 'agent007')
        self.assertIsNone(a.structure(book(1, 'Agent 007'), {})['tentative_number'])

    def test_creator_prefix_requires_tag_support_and_repetition(self):
        books = {1: book(1, '[青 (人)] Quiet Garden [中国翻訳]', [['artist', 'anonymous']]),
                 2: book(2, '[青 (人)] Another Title', [['artist', 'anonymous']])}
        aliases = a.build_aliases(books)
        self.assertEqual(a.structure(books[1], aliases)['core'], 'quietgarden')
        self.assertNotEqual(a.structure(books[1], {})['core'], 'quietgarden')
        unsupported = book(3, '[青 (人)] Quiet Garden', [['artist', 'different']])
        self.assertNotEqual(a.structure(unsupported, aliases)['core'], 'quietgarden')
        self.assertFalse(a.build_aliases({1: books[1]}))

    def test_repeated_real_title_bracket_same_artist_is_not_an_alias(self):
        books = {1: book(1, '[Secret Garden] Dawn', [['artist', 'anonymous']]),
                 2: book(2, '[Secret Garden] Sunset', [['artist', 'anonymous']])}
        aliases = a.build_aliases(books)
        self.assertFalse(aliases)
        self.assertEqual(a.structure(books[1], aliases)['core'], 'secretgardendawn')

    def test_finite_roman_and_parenthesized_positions_remain_tentative(self):
        for title, expected in [('Quiet Garden II', '2'), ('Quiet Garden III', '3'), ('Quiet Garden(2)', '2')]:
            parsed = a.structure(book(1, title), {})
            self.assertEqual(parsed['tentative_number'], expected)
            self.assertEqual(parsed['ordinals'], [])
            self.assertTrue(parsed['core'].startswith('quietgarden'))

    def test_conflicting_anthology_creators_do_not_authorize_removal(self):
        books = {1: book(1, '[Anthology] Garden', [['artist', 'A']]), 2: book(2, '[Anthology] Forest', [['artist', 'B']])}
        self.assertFalse(a.build_aliases(books))
        self.assertEqual(a.structure(books[1], {})['core'], 'anthologygarden')

    def test_explicit_types_chinese_ordinals_sections_and_bracket_protection(self):
        for title, expected in [('Garden 第二話', [['chapter', '2']]), ('Garden Vol. 2', [['volume', '2']]), ('Garden 下編', [['section', '3']]), ('Garden 《前編》', [['section', '1']]), ('Garden 二十一巻', [['volume', '21']])]:
            self.assertEqual(a.structure(book(1, title), {})['ordinals'], expected)
        self.assertEqual(a.structure(book(1, 'Garden (Magazine Vol. 9)'), {})['ordinals'], [])
        self.assertEqual(a.structure(book(1, 'Garden vol2 chapter2'), {})['ordinals'], [['volume', '2'], ['chapter', '2']])

    def test_bare_suffix_is_ambiguous_and_preserved_in_work_title(self):
        got = a.structure(book(1, 'Quiet Garden 4.5 (Unknown)'), {})
        self.assertEqual(got['core'], 'quietgarden45unknown')
        self.assertEqual(got['tentative_number'], '4.5')
        self.assertEqual(got['ordinals'], [])


class DecisionTests(unittest.TestCase):
    def test_missing_creator_is_not_conflict_and_namespaces_stay_separate(self):
        aa = book(1, 'A', [['artist', 'creator']]); missing = book(2, 'A')
        self.assertFalse(a.creator_relation(aa, missing)['conflict'])
        bb = book(2, 'A', [['group', 'creator']])
        self.assertFalse(a.creator_relation(aa, bb)['shared'])
        cc = book(3, 'A', [['artist', 'other']])
        self.assertTrue(a.creator_relation(aa, cc)['conflict'])

    def test_language_color_and_gallery_organization_are_not_features(self):
        aa = book(1, 'Garden', [['artist', 'A']], language_code='en')
        bb = book(2, 'Garden', [['artist', 'A']], language_code='ja', is_full_color=1, collection_id=200, gallery_id_sampling_only=7)
        structures = {i: a.structure(b, {}) for i, b in {1: aa, 2: bb}.items()}
        before = a.signals(1, 2, structures, {}, {1: aa, 2: bb})
        bb['language_code'] = 'fr'; bb['is_full_color'] = 0; bb['collection_id'] = 999; bb['gallery_id_sampling_only'] = 40
        self.assertEqual(before, a.signals(1, 2, structures, {}, {1: aa, 2: bb}))

    def test_mutually_exclusive_work_series_and_conflict_review(self):
        self.assertEqual(a.predict(signal(), PARAMS)['action'], 'same_work')
        different = signal(explicit_difference=True, same_core=False, same_stem=True)
        self.assertEqual(a.predict(different, PARAMS)['action'], 'same_series')
        conflict = signal(creator={'shared': False, 'conflict': True})
        self.assertEqual(a.predict(conflict, PARAMS)['action'], 'defer')
        no_title = signal(empty_title=True)
        self.assertEqual(a.predict(no_title, PARAMS)['action'], 'defer')
        self.assertEqual(a.predict(signal(ordinal_ambiguity=True), PARAMS)['action'], 'defer')

    def test_low_title_image_route_needs_semantic_creator_and_compatible_pages(self):
        sig = signal(title=.4, series_title=.4, same_core=False, same_stem=False,
                     scores={'phash': .9, 'sscd': .9, 'minilm': .95})
        self.assertEqual(a.predict(sig, PARAMS)['action'], 'same_work')
        for field, value in [('pages', .5), ('creator', {'shared': False, 'conflict': False}), ('scores', {'sscd': .9, 'phash': 1., 'minilm': .5})]:
            changed = {**sig, field: value}
            self.assertEqual(a.predict(changed, PARAMS)['action'], 'defer')

    def test_possible_partial_overlap_and_ordinal_kind_conflicts_defer(self):
        self.assertEqual(a.predict(signal(possible_partial_overlap=True), PARAMS)['action'], 'defer')
        self.assertEqual(a.predict(signal(ordinal_kind_ambiguity=True), PARAMS)['action'], 'defer')

    def test_image_alone_never_establishes_same_work(self):
        sig = signal(title=.1, series_title=.1, same_core=False, same_stem=False,
                     scores={'sscd': 1., 'phash': 1., 'minilm': .1})
        self.assertEqual(a.predict(sig, PARAMS)['action'], 'defer')

    def test_negative_sscd_without_phash_cannot_supply_negative_support(self):
        sig = signal(scores={'sscd': -.9})
        self.assertAlmostEqual(a.scored(sig, PARAMS)['version'], .9)

    def test_missing_features_are_zero_support_not_a_probability(self):
        sig = signal(title=.5, series_title=.5, scores={})
        self.assertEqual(a.predict(sig, PARAMS)['action'], 'defer')
        values = a.features_for_pair(1, 2, {'sscd': {1: np.ones(512)}, 'phash': {1: 0}})
        self.assertEqual(values, {'sscd': None, 'phash': None})


class MetricsTests(unittest.TestCase):
    def rows(self):
        return [{'pair_id': str(i), 'label': label, 'collection_or_partial_overlap': flag} for i, (label, flag) in enumerate([
            ('same_work', False), ('same_work', False), ('same_series', False), ('unrelated', False), ('uncertain', False), ('same_work', True)])]

    def test_defer_positives_count_as_fn_and_exclusions_are_explicit(self):
        rows = self.rows(); decisions = {'0': 'same_work', '1': 'defer', '2': 'same_work', '3': 'unrelated', '4': 'same_work', '5': 'same_work'}
        got = a.metrics(rows, decisions, 'version')
        self.assertEqual((got['samples'], got['tp'], got['fp'], got['fn'], got['deferred']), (4, 1, 1, 1, 1))
        self.assertEqual((got['precision'], got['recall']), (.5, .5))
        conditional = a.metrics(rows, decisions, 'series', True)
        self.assertEqual(conditional['samples'], 2)

    def test_wilson_zero_denominators_and_empty_metrics(self):
        self.assertIsNone(a.wilson(0, 0))
        self.assertAlmostEqual(a.wilson(10, 10)[0], .7224672, places=6)
        got = a.metrics([], {}, 'version')
        self.assertIsNone(got['precision']); self.assertIsNone(got['recall']); self.assertIsNone(got['defer_fraction'])

    def test_non_degenerate_grid_falls_back_when_fewer_than_ten(self):
        rows = [{'pair_id': 'x', 'label': 'same_work', 'collection_or_partial_overlap': False}]
        params, trials = a.grid_search(rows, {'x': signal()})
        self.assertEqual(params, {'work_threshold': .90, 'series_threshold': .84, 'title_weight': .86, 'image_weight': .04})
        self.assertEqual(len(trials), 36)

    def test_rows_validate_four_labels_and_duplicate_pair_rejection(self):
        row = {'pair_id': 'x', 'book_ids': [1, 2], 'family_ids': ['f'], 'label': 'same_work', 'reason': '', 'collection_or_partial_overlap': False}
        material = {'schema': 'recommendation-heldout-v2', 'rows': [row]}
        self.assertEqual(a.validate_rows(material, {1: {}, 2: {}}, material['schema']), [row])
        bad = copy.deepcopy(material); bad['rows'].append({**row, 'pair_id': 'y', 'book_ids': [2, 1]})
        with self.assertRaises(ValueError): a.validate_rows(bad, {1: {}, 2: {}}, bad['schema'])
        for label in ('same', None):
            bad = copy.deepcopy(material); bad['rows'][0]['label'] = label
            with self.assertRaises(ValueError): a.validate_rows(bad, {1: {}, 2: {}}, bad['schema'])

    def test_top_k_retrieves_from_full_pool_and_feature_missing_is_counted(self):
        books = {i: book(i, title) for i, title in [(1, 'Anonymous Garden'), (2, 'Anonymous Garden'), (3, 'Distinct Forest')]}
        structures = {i: a.structure(b, {}) for i, b in books.items()}
        rows = [{'pair_id': 'x', 'book_ids': [1, 2], 'label': 'same_work', 'collection_or_partial_overlap': False}]
        got = a.retrieval(rows, books, structures, {'phash': {}, 'sscd': {}, 'minilm': {}}, PARAMS)
        self.assertEqual(got['version:improved:top5']['found'], 1)
        self.assertEqual(got['version:phash:top5']['missing_pair_features'], 1)
        self.assertEqual(got['version:rule:top20']['pool_books'], 3)
        self.assertEqual(got['version:union:top5']['maximum_emitted_candidates_per_query'], 25)
        self.assertEqual(got['version:union:top5']['union_semantics'], 'union_of_each_route_top_K_not_a_total_top_K')

    def test_triangular_contradictions_only_flag_no_label_mutation(self):
        rows = [{'pair_id': str(i), 'book_ids': pair, 'label': label, 'collection_or_partial_overlap': False} for i, (pair, label) in enumerate([([1, 2], 'same_work'), ([2, 3], 'same_work'), ([1, 3], 'same_series')])]
        before = copy.deepcopy(rows)
        self.assertEqual(len(a.label_consistency(rows)), 1)
        self.assertEqual(rows, before)

    def test_missing_heldout_or_wrong_release_does_not_consume_evaluation(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve()
            frozen = {'code_sha256': {}, 'input_sha256': {}, 'python': a.platform.python_version(), 'numpy': np.__version__}
            (path/'frozen-policy.json').write_text(json.dumps(frozen))
            cal = {'pack_id': 'anonymous', 'feature_input_id': 'anonymous-features', 'rows': []}
            mocked = ({}, cal, {'heldout_pairs': 0}, {}, {})
            with patch.object(a, 'code_hashes', return_value={}), patch.object(a, 'input_hashes', return_value={}), patch.object(a, 'load_inputs', return_value=mocked):
                with self.assertRaises(FileNotFoundError): a.run('evaluate', path)
                self.assertFalse((path/'heldout-evaluation.started').exists())
                (path/'heldout.json').write_text(json.dumps({'schema': 'recommendation-heldout-v2', **cal, 'frozen_policy_sha256': 'wrong'}))
                with self.assertRaises(ValueError): a.run('evaluate', path)
                self.assertFalse((path/'heldout-evaluation.started').exists())

    def test_frozen_code_tamper_rejected_before_reading_heldout(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve(); (path/'frozen-policy.json').write_text(json.dumps({'code_sha256': {}, 'input_sha256': {}}))
            with patch.object(a, 'code_hashes', return_value={'test': 'changed'}) as hashed, patch.object(a, 'read_json', wraps=a.read_json) as read:
                with self.assertRaisesRegex(ValueError, 'frozen_code_or_input_changed'):
                    a.run('evaluate', path)
                hashed.assert_called_once()
                self.assertEqual([call.args[0].name for call in read.call_args_list], ['frozen-policy.json'])
            self.assertFalse((path/'heldout-evaluation.started').exists())


if __name__ == '__main__':
    unittest.main()
