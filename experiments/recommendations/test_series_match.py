import unittest
import numpy as np
from series_match import Corpus, POLICY, select_policy, predict, metrics, grouped_cv
from series_proposals import cliques, join_plan, order_plan
from series_data import experimental_works
from test_series_data import book, row


class SeriesMatchTests(unittest.TestCase):
    def corpus(self):
        books = {i: book(i, 'Memory 第'+str(i)+'卷') for i in range(1, 5)}
        books[4]['title'] = 'Different Story 第1卷'; books[4]['tags'] = [['artist', 'different']]
        works, mapping, _ = experimental_works(books, [])
        vectors = {i: np.asarray([1.,0.], dtype=np.float32) for i in books}
        return Corpus(books, works, {'minilm': vectors}, vectors), mapping

    def test_no_chain_and_no_lost_local_cliques(self):
        self.assertEqual(cliques(['a','b','c'], [('a','b'),('b','c')]), [['a','b'],['b','c']])

    def test_multi_edition_nonpreferred_support_and_self_exclusion(self):
        c, _ = self.corpus()
        b = {1: book(1, 'Unhelpful'), 2: book(2, 'Memory 第1卷'), 3: book(3, 'Memory 第2卷')}
        w, m, _ = experimental_works(b, [row(1, 2, 'same_work')])
        vs = {i: np.asarray([1.,0.], dtype=np.float32) for i in b}
        c = Corpus(b, w, {'minilm': vs}, vs)
        self.assertEqual(c.signal(m[1], m[3])['title_support'][0], 2)
        self.assertNotIn(m[1], c.retrieve(m[1])['candidates'])

    def test_creator_conflict_downgrades_missing_does_not(self):
        s = {'eligible':True,'raw':.9,'dual':.9,'risks':[]}
        p = {'strategy':'raw','strong':.8,'supplementary':.6}
        self.assertEqual(predict(s, p), 'strong')
        self.assertEqual(predict({**s,'risks':['explicit_creator_conflict']},p), 'supplementary')
        self.assertEqual(predict({**s,'risks':['creator_support_missing']},p), 'strong')
        self.assertEqual(predict(s, p, False), 'none')

    def test_finite_policy_and_infeasible_not_fallback(self):
        selection = select_policy([], {}, {})
        self.assertEqual(len(selection['trials']), 132)
        self.assertIsNone(selection['parameters'])
        self.assertEqual(predict({'eligible':True}, None), 'none')

    def test_series_order_and_existing_relative_order(self):
        c, m = self.corpus()
        ids = [m[3],m[1],m[2]]
        self.assertEqual(order_plan(ids,c.profiles)['suggested_order'], [m[1],m[2],m[3]])
        self.assertIsNone(order_plan(ids,c.profiles,[m[2],m[1]])['suggested_order'])

    def test_join_all_member_conflict_and_other_series(self):
        c, m = self.corpus(); p = {'strategy':'raw','strong':.8,'supplementary':.4}
        s = {'id':'virtual','name':'Memory','work_ids':[m[1],m[2]]}
        plan = join_plan(c,m[3],s,p,{m[3]:'elsewhere'})
        self.assertTrue(plan['blocked'])
        self.assertIn('already_in_other_series',plan['risks'])
        self.assertEqual(plan['order']['suggested_order'],[m[1],m[2],m[3]])

    def test_unknown_and_overlap_not_negative_and_defer_positive_is_fn(self):
        rows = [row(1,2,'same_series'),row(2,3,'uncertain'),row(3,4,'unrelated')]
        self.assertEqual(metrics(rows,{})['fn'],1)
        self.assertEqual(metrics(rows,{})['samples'],2)
        rows[0]['collection_or_partial_overlap'] = True
        self.assertEqual(metrics(rows,{})['samples'],1)

    def test_grouped_validation_rejects_shared_edition_leak(self):
        rr=[row(1,2,'same_series'),row(2,3,'unrelated')]
        with self.assertRaises(ValueError):
            grouped_cv(rr,{}, {},[{'pair_id':rr[0]['pair_id'],'fold':0},{'pair_id':rr[1]['pair_id'],'fold':1}])


if __name__ == '__main__': unittest.main()
