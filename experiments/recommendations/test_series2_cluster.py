import unittest
from types import SimpleNamespace
from series2_cluster import agglomerate, assess, cross_evidence

POLICY = {'strategy':'raw','strong':.70,'supplementary':.45}


def corpus(values, subjects=None, risks=None):
    values = {tuple(sorted(k)):v for k,v in values.items()}
    ids = sorted({i for e in values for i in e})
    def signal(a,b):
        key=tuple(sorted((a,b)))
        return {'raw':values.get(key,0.),'eligible':True,'risks':(risks or {}).get(key,[])}
    return SimpleNamespace(signal=signal,profiles={i:{'subjects':set((subjects or {}).get(i,['same']))} for i in ids})


class ClusterTests(unittest.TestCase):
    def test_pair_chain_cannot_become_group_and_partition_has_no_duplicates(self):
        c=corpus({('a','b'):.9,('b','c'):.8,('a','c'):.1})
        r=agglomerate(c.profiles,[('a','b'),('b','c')],c,POLICY)
        self.assertEqual([g['work_ids'] for g in r['groups']],[['a','b'],['c']])

    def test_average_accepts_weak_cross_pair_but_complete_does_not(self):
        c=corpus({('a','b'):.9,('a','c'):.8,('b','c'):.42})
        edges=[('a','b'),('a','c')]
        a=agglomerate(c.profiles,edges,c,POLICY,'average')
        b=agglomerate(c.profiles,edges,c,POLICY,'complete')
        self.assertEqual(a['groups'][0]['work_ids'],['a','b','c'])
        self.assertEqual(len(b['groups']),2)
        self.assertEqual(assess(['a','b','c'],c,POLICY,'average')['tier'],'supplementary')

    def test_not_retrieved_cross_pair_is_still_checked(self):
        c=corpus({('a','b'):.9,('a','c'):.8,('b','c'):.39})
        r=agglomerate(c.profiles,[('a','b'),('a','c')],c,POLICY)
        self.assertEqual(len(r['groups']),2)

    def test_subject_and_explicit_conflict_block_merge(self):
        c=corpus({('a','b'):.95},{'a':['a'],'b':['b']})
        self.assertEqual(len(agglomerate(c.profiles,[('a','b')],c,POLICY)['groups']),2)
        c=corpus({('a','b'):.95},risks={('a','b'):['different_title_subject']})
        self.assertIsNone(cross_evidence(['a'],['b'],c,POLICY,'average'))

    def test_fixed_anchors_are_never_merged(self):
        c=corpus({('a','b'):.9,('a','c'):.8,('b','c'):.8})
        anchors=[{'id':'s1','work_ids':['a']},{'id':'s2','work_ids':['b']}]
        r=agglomerate(c.profiles,[('a','b'),('a','c'),('b','c')],c,POLICY,anchors=anchors)
        self.assertEqual(len([g for g in r['groups'] if g['anchor']]),2)
        self.assertFalse(any(set(('a','b'))<=set(g['work_ids']) for g in r['groups']))

    def test_tie_breaking_and_input_order_are_stable(self):
        c=corpus({('a','b'):.9,('a','c'):.9,('b','c'):.1})
        edges=[('a','b'),('a','c')]
        a=agglomerate(['a','b','c'],edges,c,POLICY)
        b=agglomerate(['c','b','a'],list(reversed(edges)),c,POLICY)
        self.assertEqual(a,b)

    def test_capacity_duplicate_anchor_and_unknown_edges_fail_closed(self):
        c=corpus({('a','b'):.9})
        self.assertIsNone(assess(['a','a'],c,POLICY,'average'))
        with self.assertRaises(ValueError):agglomerate(['a','b'],[('a','z')],c,POLICY)
        with self.assertRaises(ValueError):agglomerate(['a','b'],[],c,POLICY,anchors=[{'id':'s','work_ids':['a','a']}])


if __name__=='__main__': unittest.main()
