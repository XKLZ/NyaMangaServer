import unittest
from unittest.mock import patch
from pathlib import Path
import tempfile
import analyze_v2 as v2
from series_data import experimental_works, output_path, OUTPUT
from series_data import immutable, private_dir
from series_titles import title_views, compare, position


def book(i, title, artist='anonymous'):
    return {'id': i, 'title': title, 'title_jpn': '', 'tags': [['artist', artist]], 'pages': 20}


def row(a, b, label):
    return {'pair_id': str(a)+':'+str(b), 'book_ids': [a, b], 'label': label,
            'collection_or_partial_overlap': False}


class SeriesDataTests(unittest.TestCase):
    def test_preparation_is_idempotent_for_integer_identity_maps(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            with patch('series_data.OUTPUT', root):
                p = private_dir(root/'data')/'works.json'
                immutable(p, {'mapping': {10:'work:10',2:'work:2'}})
                before = p.read_bytes()
                immutable(p, {'mapping': {10:'work:10',2:'work:2'}})
                self.assertEqual(p.read_bytes(), before)
    def test_work_membership_is_not_cv_family_or_series_label(self):
        books = {i: book(i, 'Anonymous') for i in range(1, 5)}
        works, mapping, conflicts = experimental_works(books, [row(1, 2, 'same_work'), row(2, 3, 'same_series')])
        self.assertEqual(mapping[1], mapping[2]); self.assertNotEqual(mapping[2], mapping[3])
        self.assertEqual(len(works), 3); self.assertEqual(conflicts, [])

    def test_contradictory_work_edges_do_not_merge(self):
        books = {i: book(i, 'Anonymous') for i in range(1, 4)}
        works, mapping, conflicts = experimental_works(books, [row(1, 2, 'same_work'), row(2, 3, 'same_work'), row(1, 3, 'unrelated')])
        self.assertEqual(len(works), 3); self.assertEqual(len(conflicts), 3)

    def test_output_rejects_application_and_reserved_paths(self):
        for p in (OUTPUT.parent / 'run-v3', OUTPUT / 'coordinator' / 'x', OUTPUT / '..' / 'run-v4' / 'x'):
            with self.assertRaises(ValueError): output_path(p)

    def test_explicit_positions_subject_and_unknown_brackets(self):
        a = title_views(book(1, 'Memory 第1卷 [未知内容]'), {})[0]
        b = title_views(book(2, 'Memory 第10卷 [未知内容]'), {})[0]
        self.assertEqual(compare(a, b)['title'], 1)
        self.assertTrue(compare(a, b)['distinct'])
        self.assertIn('未知内容', a['stem_text'])
        self.assertLess(position([a])[1], position([b])[1])

    def test_intrinsic_numbers_and_section_families(self):
        intrinsic = title_views(book(1, 'Agent 007'), {})[0]
        self.assertIn('007', intrinsic['series_stem']); self.assertIsNone(position([intrinsic]))
        before = title_views(book(2, 'Memory 前篇'), {})[0]
        after = title_views(book(3, 'Memory 後篇'), {})[0]
        upper = title_views(book(4, 'Memory 上篇'), {})[0]
        self.assertLess(position([before])[1], position([after])[1])
        self.assertIn('ordinal_type_ambiguity', compare(before, upper)['risks'])

    def test_alternative_language_title_is_retained(self):
        b = {**book(1, '回忆 第2卷'), 'title_jpn': '思い出 第2巻'}
        self.assertEqual(len(title_views(b, {})), 2)


if __name__ == '__main__': unittest.main()
