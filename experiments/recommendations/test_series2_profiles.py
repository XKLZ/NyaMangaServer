import unittest
from unittest.mock import patch
from types import SimpleNamespace
import series2_cluster as cluster
import series2_profiles as profiles
import test_series2_cluster as fixtures

POLICY=fixtures.POLICY


class ProfileTests(unittest.TestCase):
    def make(self,values,subjects=None):
        c=fixtures.corpus(values,subjects)
        signal=c.signal
        c.signal=lambda a,b:{**signal(a,b),'title':signal(a,b)['raw'],
                              'title_support':[1,2],'positions':[[],[]]}
        for p in c.profiles.values():p['views']=[]
        return c

    def test_new_subject_allows_nonidentical_names_with_direct_support(self):
        c=self.make({('a','b'):.8},{'a':['alpha'],'b':['alphaalternative']})
        self.assertFalse(cluster.legacy_subject(['a','b'],c)['valid'])
        self.assertTrue(profiles.representative_subject(['a','b'],c)['valid'])

    def test_representative_is_medoid_not_alias_chain(self):
        c=self.make({('a','b'):.9,('b','c'):.8,('a','c'):.1})
        self.assertEqual(profiles.representative_subject(['a','b','c'],c)['representative'],'b')
        r=cluster.agglomerate(c.profiles,[('a','b'),('b','c')],c,POLICY,
                              subject_check=profiles.representative_subject)
        self.assertEqual(len(r['groups']),2)

    def test_equal_target_moves_boundary_out_of_primary_and_revalidates(self):
        c=self.make({('a','b'):.8,('b','c'):.8,('a','c'):.1})
        edges=[('a','b'),('b','c')]
        original=cluster.agglomerate(c.profiles,edges,c,POLICY)
        result=profiles.boundary_review(original['groups'],edges,c,POLICY,'average',cluster.legacy_subject)
        self.assertEqual([p['work_id'] for p in result['pending']],['b'])
        self.assertEqual(len(result['pending'][0]['options']),2)
        self.assertEqual(profiles.proposals(result['groups'],c,POLICY,'average',cluster.legacy_subject),[])

    def test_clear_target_does_not_become_ambiguous(self):
        c=self.make({('a','b'):.9,('b','c'):.5,('a','c'):.1})
        edges=[('a','b'),('b','c')]
        original=cluster.agglomerate(c.profiles,edges,c,POLICY)
        result=profiles.boundary_review(original['groups'],edges,c,POLICY,'average',cluster.legacy_subject)
        self.assertEqual(result['pending'],[])

    def test_fixed_members_never_removed_and_multiple_targets_are_bounded(self):
        values={('q',str(i)):.8 for i in range(5)}
        values.update({(str(i),str(j)):.1 for i in range(5) for j in range(i+1,5)})
        c=self.make(values)
        anchors=[{'id':'s'+str(i),'name':'Anonymous','work_ids':[str(i)]} for i in range(5)]
        r=cluster.agglomerate(c.profiles,list(values),c,POLICY,anchors=anchors)
        b=profiles.boundary_review(r['groups'],list(values),c,POLICY,'average',cluster.legacy_subject)
        self.assertEqual([p['work_id'] for p in b['pending']],['q'])
        self.assertEqual(len(b['pending'][0]['options']),3)
        self.assertTrue(all(len(g['work_ids'])==1 for g in b['groups']))

    def test_nonpreferred_edition_title_aliases_are_preserved(self):
        c=self.make({('a','b'):.8})
        c.profiles['a']['views']=[{'edition_id':1,'raw_text':'原题','stem_text':'原题','series_main':'原题'},
                                  {'edition_id':2,'raw_text':'Alias','stem_text':'Alias','series_main':'alias'}]
        self.assertEqual(len(profiles.representative_subject(['a','b'],c)['explicit_edition_aliases']),2)

    def test_missing_title_cannot_be_replaced_by_shared_creator_or_semantics(self):
        c=self.make({('a','b'):.8}); old=c.signal
        c.signal=lambda a,b:{**old(a,b),'title':0.}
        self.assertFalse(profiles.representative_subject(['a','b'],c)['valid'])

    def test_capacity_and_existing_order_are_protected(self):
        c=self.make({('a','b'):.8,('a','c'):.8,('b','c'):.8})
        with patch.object(cluster,'CAPACITY',2):
            r=cluster.agglomerate(c.profiles,[('a','b'),('a','c')],c,POLICY)
            self.assertTrue(all(len(g['work_ids'])<=2 for g in r['groups']))
        p=profiles.proposals([{'work_ids':['a','b','c'],'anchor':{'id':'s','name':'Existing','work_ids':['b','a']}}],
                             c,POLICY,'average',cluster.legacy_subject)[0]
        self.assertEqual(p['existing_work_ids'],['b','a'])
        self.assertIsNone(p['order']['suggested_order'])


if __name__=='__main__':unittest.main()
