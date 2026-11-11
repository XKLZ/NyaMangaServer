import json
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import Mock
import numpy as np
from series import SeriesDiscovery, PARAMETERS
from worker import dispatch, validate_book


def request():
    books=[{'id':str(i),'library_id':'anonymous','title':'Memory 第'+str(i)+'卷','title_jpn':'',
            'tags':[['artist','anonymous']],'pages':20} for i in range(1,5)]
    vector=[1.]+[0.]*383
    return {'pool_key':'anonymous','books':books,'works':[{'id':'w'+b['id'],'edition_ids':[b['id']],'preferred_edition_id':b['id']} for b in books],
            'series':[],'features':{b['id']:vector for b in books}}


class SeriesTests(unittest.TestCase):
    def test_organization_refresh_reuses_corpus_but_changed_inputs_do_not(self):
        r=request();first=SeriesDiscovery(r,validate_book)
        first.corpus.signal('w1','w2')
        r['series']=[{'id':'s','name':'User confirmed','work_ids':['w1','w2']}]
        second=SeriesDiscovery(r,validate_book,first)
        self.assertIs(first.corpus,second.corpus)
        self.assertEqual(second.anchors[0]['id'],'s')
        r['books'][0]['title']='Changed title'
        third=SeriesDiscovery(r,validate_book,second)
        self.assertIsNot(third.corpus,second.corpus)
    def test_real_partition_and_immutable_existing_anchor(self):
        r=request();r['works'][0]['edition_ids'].append('2');r['works'].pop(1)
        r['series']=[{'id':'s','name':'Existing','work_ids':['w1','w3']}]
        d=SeriesDiscovery(r,validate_book)
        edges=[e for row in range(3) for e in d.row({'pool_key':d.key,'row':row})['edges']]
        result=d.cluster({'pool_key':d.key,'edges':edges})
        self.assertEqual(result['proposals'][0]['kind'],'join_series')
        self.assertEqual(result['proposals'][0]['new_work_ids'],['w4'])
        self.assertEqual(len(result['proposals'][0]['work_ids']),3)

    def test_raw_feature_only_operation_never_computes_image(self):
        runtime=Mock();runtime.feature.return_value={'feature':[1]+[0]*383}
        out=dispatch(runtime,{'protocol':'work-recommendation-worker-v1','operation':'series_features','book':request()['books'][0]})
        runtime.feature.assert_called_once_with('minilm','Memory 第1卷')
        self.assertEqual(set(out['features']),{'minilm'})

    def test_invalid_partition_keys_and_cross_library_are_rejected(self):
        r=request();r['works'][1]['edition_ids']=['1']
        with self.assertRaises(ValueError):SeriesDiscovery(r,validate_book)
        r=request();r['books'][0]['library_id']='foreign'
        with self.assertRaises(ValueError):SeriesDiscovery(r,validate_book)
        d=SeriesDiscovery(request(),validate_book)
        with self.assertRaises(ValueError):d.row({'pool_key':'changed','row':0})
        with self.assertRaises(ValueError):d.cluster({'pool_key':d.key,'edges':[['missing','w1']]})

    def test_missing_features_and_unavailable_anchor_never_become_fake_members(self):
        r=request();r['features']={};r['series']=[{'id':'s','name':'Partly unavailable','work_ids':['w1','missing']}]
        d=SeriesDiscovery(r,validate_book)
        edges=[e for row in range(4) for e in d.row({'pool_key':d.key,'row':row})['edges']]
        result=d.cluster({'pool_key':d.key,'edges':edges})
        self.assertEqual(result['unavailable_series'],['s'])
        self.assertTrue(all('w1' not in p['work_ids'] for p in result['proposals']))

    def test_snapshot_strategy_matches_frozen_offline_implementation(self):
        r=request();r['books'][2]['title']='Memory 第3巻 [未知内容]'
        r['books'][3]['title']='Different Story 第1卷'
        # Two isolated import graphs. Production never imports experiments.
        root=Path(__file__).resolve().parents[2]
        script='''import json,sys,numpy as np
from series_match import Corpus,predict
from series2_cluster import agglomerate
from series2_profiles import representative_subject,boundary_review,proposals
r=json.loads(sys.stdin.read());books={b['id']:b for b in r['books']};works={w['id']:w for w in r['works']};vectors={i:np.asarray(v,dtype=np.float32) for i,v in r['features'].items()}
c=Corpus(books,works,{'minilm':vectors},vectors);policy={'strategy':'raw','strong':.70,'supplementary':.45}
edges=sorted({tuple(sorted((i,j))) for i in c.ids for j in c.retrieve(i)['candidates'] if predict(c.signal(i,j),policy)!='none'})
partition=agglomerate(c.ids,edges,c,policy,'average',representative_subject)
out=boundary_review(partition['groups'],edges,c,policy,'average',representative_subject)
print(json.dumps({'plans':proposals(out['groups'],c,policy,'average',representative_subject),'pending':out['pending'],'edges':edges},ensure_ascii=False))'''
        result=subprocess.run([sys.executable,'-B','-c',script],input=json.dumps(r),text=True,capture_output=True,cwd=root/'experiments/recommendations')
        self.assertEqual(result.returncode,0,result.stderr)
        expected=json.loads(result.stdout)
        d=SeriesDiscovery(r,validate_book)
        edges=sorted({tuple(e) for row in range(len(r['works'])) for e in d.row({'pool_key':d.key,'row':row})['edges']})
        out=d.cluster({'pool_key':d.key,'edges':edges})
        self.assertEqual([list(e) for e in edges],expected['edges'])
        self.assertEqual(out['proposals'],expected['plans']);self.assertEqual(out['pending'],expected['pending'])


if __name__=='__main__':unittest.main()
