"""Anonymous parity tests; no models, private inputs or remote access."""
import copy
import unittest
import json
import subprocess
import sys
import tempfile
from pathlib import Path
from test_series import request
from series import SeriesDiscovery
from worker import validate_book,dispatch


def run(r,blocks=()):
    d=SeriesDiscovery(r,validate_book)
    for block in blocks:d.restore({'pool_key':d.key,**block})
    saved=[];edges=[]
    def flush():
        while True:
            b=d.checkpoint({'pool_key':d.key});saved.append(b)
            if not b['more']:break
    flush()
    for row in range(len(d.corpus.ids)):
        edges.extend(d.row({'pool_key':d.key,'row':row})['edges']);flush()
    out=d.cluster({'pool_key':d.key,'edges':edges});flush()
    return d,out,saved

class IncrementalSeriesTests(unittest.TestCase):
    def test_nullable_checkpoint_and_real_worker_wire(self):
        r=request()
        for change in ({}, {'signals':None,'routes':None}, {'signals':[],'routes':{}}):
            d=SeriesDiscovery(r,validate_book)
            d.restore({'pool_key':d.key,**change})
            self.assertEqual(d.row({'pool_key':d.key,'row':0})['next_row'],1)
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();(root/'models').mkdir()
            messages=[{'protocol':'work-recommendation-worker-v1','operation':'series_prepare',**r},
                {'protocol':'work-recommendation-worker-v1','operation':'series_restore','pool_key':r['pool_key'],'signals':[],'routes':None},
                {'protocol':'work-recommendation-worker-v1','operation':'series_restore','pool_key':r['pool_key'],'routes':{'invalid':'not-a-route'}},
                [], # unexpected protocol shape must not terminate the process or leak exception text
                {'protocol':'work-recommendation-worker-v1','operation':'series_row','pool_key':r['pool_key'],'row':0}]
            result=subprocess.run([sys.executable,'-B',str(Path(__file__).with_name('worker.py')),'--models',str(root/'models'),'--cache',str(root/'cache')],input=''.join(json.dumps(v)+'\n' for v in messages),capture_output=True,text=True,timeout=20)
            self.assertEqual(result.returncode,0,result.stderr)
            replies=[json.loads(v) for v in result.stdout.splitlines()]
            self.assertEqual(len(replies),5)
            self.assertIn('result',replies[1]);self.assertEqual(replies[2]['error'],'invalid_series_checkpoint')
            self.assertEqual(replies[3]['error'],'recommendation_worker_failed')
            self.assertEqual(replies[4]['result']['next_row'],1)
            self.assertTrue(all(v['resources']['model_loads']==0 for v in replies))

    def test_identical_repeat_has_no_new_scoring_and_full_parity(self):
        r=request();first,a,blocks=run(r);second,b,_=run(r,blocks)
        self.assertEqual(a,b)
        self.assertGreater(len(first.corpus.computed_signal_keys),0)
        self.assertEqual(len(second.corpus.computed_signal_keys),0)
        self.assertGreater(len(second.corpus.reused_signal_keys),0)
    def test_new_changed_deleted_inputs_rebuild_retrieval_and_match_full(self):
        r=request();_,_,blocks=run(r)
        for kind in ('add','change','delete'):
            changed=copy.deepcopy(r)
            if kind=='change':changed['books'][0]['title']='Different Series 第1卷'
            elif kind=='delete':
                changed['books'].pop();changed['works'].pop();changed['features'].pop('4')
            else:
                b=copy.deepcopy(changed['books'][0]);b.update(id='5',title='Memory 第5卷');changed['books'].append(b)
                changed['works'].append({'id':'w5','edition_ids':['5'],'preferred_edition_id':'5'});changed['features']['5']=changed['features']['1']
            inc,a,_=run(changed,blocks);full,b,_=run(changed)
            self.assertEqual(a,b,kind)
            self.assertEqual(inc.corpus.routes,full.corpus.routes,kind)
            if kind!='delete':self.assertGreater(len(inc.corpus.computed_signal_keys),0)
    def test_negative_and_conflicting_signals_are_reused_too(self):
        r=request();r['books'][3]['title']='Entirely Different Subject';r['books'][3]['tags']=[['artist','different']]
        first,a,blocks=run(r);second,b,_=run(r,blocks)
        self.assertEqual(a,b);self.assertEqual(len(second.corpus.computed_signal_keys),0)
        signals=[v['signal'] for block in blocks for v in block['signals']]
        self.assertTrue(any(not v['eligible'] or 'explicit_creator_conflict' in v.get('risks',[]) for v in signals))

    def test_wrong_profile_key_cannot_restore_signal(self):
        r=request();_,_,blocks=run(r)
        damaged=copy.deepcopy(blocks)
        for block in damaged:
            for signal in block['signals']:signal['key']='invalid'
        inc,a,_=run(r,damaged);_,b,_=run(r)
        self.assertEqual(a,b);self.assertGreater(len(inc.corpus.computed_signal_keys),0)
    def test_global_alias_preprocessing_changes_profile_identity(self):
        r=request();r['books'][0]['title']='[Group (Artist)] Memory 第1卷';r['books'][1]['title']='[Group (Artist)] Memory 第2卷'
        before=SeriesDiscovery(r,validate_book)
        changed=copy.deepcopy(r);changed['books'][1]['tags']=[['artist','different']]
        after=SeriesDiscovery(changed,validate_book)
        self.assertNotEqual(before.profiles['w1'],after.profiles['w1'])
    def test_work_preprocessing_is_stable_and_tracks_actual_aliases(self):
        r=request();runtime=object();q={'protocol':'work-recommendation-worker-v1','operation':'discovery_preprocessing','books':r['books']}
        first=dispatch(runtime,q);second=dispatch(runtime,q)
        self.assertEqual(first,second)
        q['books'][0]['title']='Changed actual title'
        self.assertNotEqual(first['fingerprints']['1'],dispatch(runtime,q)['fingerprints']['1'])

if __name__=='__main__':unittest.main()
