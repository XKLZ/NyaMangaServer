"""Anonymous readonly report UI checks, without predicting or private inputs."""
import copy
from pathlib import Path
import tempfile
import unittest
from test_study import synthetic_books
import review_analysis_v3 as review


def row(label='same_work', action='same_series', tier='strong'):
    return {'pair_id': 'anonymous', 'book_ids': [1, 2], 'label': label, 'reason': '</script><img onerror=bad>',
            'collection_or_partial_overlap': False, 'prediction': {'action': action, 'relation': action,
            'tier': tier, 'reasons': ['ordinal_conflict'], 'risks': [], 'scores': {'sscd': .8}, 'algorithm_version': 'anonymous-v3'}}


class ReviewTests(unittest.TestCase):
    def test_strong_review_and_excluded_categories(self):
        self.assertEqual(review.classify(row()), 'wrong')
        self.assertEqual(review.classify(row(action='same_work')), 'correct')
        self.assertEqual(review.classify(row(tier='review')), 'review')
        self.assertEqual(review.classify(row(action='defer')), 'review')
        self.assertEqual(review.classify(row(label='uncertain')), 'excluded')
        overlap = row();overlap['collection_or_partial_overlap'] = True
        self.assertEqual(review.classify(overlap), 'excluded')
        with self.assertRaises(ValueError): review.classify(row(action='invalid'))

    def test_readonly_html_escapes_private_text_and_renders_all_review_rows(self):
        rows = [row(), row(action='same_work', tier='review'), row(label='uncertain'), row(action='same_work')]
        for index, value in enumerate(rows): value['pair_id'] = str(index)
        reports = {'heldout': {'predictions': rows}};original = copy.deepcopy(reports)
        books = synthetic_books();books[1]['title'] = '<script>bad()</script>'
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve();output = root / 'review.html'
            summary = review.render_reports(reports, books, root, output, 'anonymous')
            text = output.read_text()
        self.assertEqual(reports, original)
        self.assertEqual(summary['review_pairs'], 3)
        self.assertEqual(summary['by_category'], {'wrong': 1, 'review': 1, 'excluded': 1})
        self.assertNotIn(books[1]['title'], text)
        self.assertNotIn(rows[0]['reason'], text)
        self.assertIn('不是概率', text)
        self.assertIn("connect-src 'none'", text)
        self.assertNotIn('fetch(', text)


if __name__ == '__main__': unittest.main()
