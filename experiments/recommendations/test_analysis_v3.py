"""Anonymous parser/risk/cache/ranking/freeze/report checks only."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import numpy as np
import analyze_v3 as a
import title_v3 as t
import semantic_v3 as semantic
from core import atomic_json, file_digest, digest


def book(i,title,pages=32):
    return {'id':i,'title':title,'title_jpn':'','pages':pages,'tags':[['artist','creator']]}


def inputs(books):
    ss={i:t.structure(b,t.build_aliases(books)) for i,b in books.items()}
    features={'sscd':{},'minilm':{},'phash':{},'clean_minilm':{}}
    for i in books:
        features['sscd'][i]=np.eye(1,512,0,dtype=np.float32)[0]
        for name in ('minilm','clean_minilm'): features[name][i]=np.eye(1,384,0,dtype=np.float32)[0]
        features['phash'][i]=1
    return ss,features


class V3Test(unittest.TestCase):
    params={'work_threshold':.75,'series_threshold':.82,'series_view':'dual_semantic'}

    def test_unknown_numbers_and_subtitle_preserved(self):
        s=t.structure(book(1,'Agent 007 - New beginning [unknown]'),{})
        self.assertIn('007',s['clean_text']); self.assertIn('[unknown]',s['clean_text']); self.assertEqual(s['subtitle_text'],'new beginning [unknown]')
        self.assertEqual(s['parse_version'],t.VERSION)

    def test_typed_ordinal_conflict_blocks_work_not_series(self):
        books={1:book(1,'Long Story 第1巻'),2:book(2,'Long Story 第2巻')}; structures,features=inputs(books)
        sig=a.signal(1,2,books,structures,features); self.assertTrue(sig['explicit_ordinal_conflict'])
        decision=a.predict(sig,self.params); self.assertNotEqual(decision['action'],'same_work')
        self.assertEqual(decision['action'],'same_series')

    def test_cover_primary_no_title_hard_gate(self):
        books={1:book(1,'完全に違う題名'),2:book(2,'A translated name')}; structures,features=inputs(books)
        sig=a.signal(1,2,books,structures,features); sig['clean_semantic']=0.; sig['scores']['minilm']=0.
        self.assertEqual(a.predict(sig,self.params)['action'],'same_work')

    def test_conflicting_creator_vs_missing_creator(self):
        books={1:book(1,'Same title'),2:book(2,'Same title')}; structures,features=inputs(books)
        books[2]['tags']=[['artist','other']]
        self.assertIn('explicit_creator_conflict',a.predict(a.signal(1,2,books,structures,features),self.params)['risks'])
        books[2]['tags']=[]
        self.assertEqual(a.predict(a.signal(1,2,books,structures,features),self.params)['action'],'same_work')

    def test_large_pages_compilation_and_ambiguity_review(self):
        books={1:book(1,'Story',20),2:book(2,'Story 総集編',200)}; structures,features=inputs(books)
        decision=a.predict(a.signal(1,2,books,structures,features),self.params)
        self.assertEqual(decision['tier'],'review'); self.assertIn('large_page_count_difference',decision['risks']); self.assertIn('compilation_vs_single_work',decision['risks'])

    def test_labels_notes_and_overlap_not_prediction_inputs(self):
        books={1:book(1,'Story'),2:book(2,'Story')}; structures,features=inputs(books)
        decision=a.predict(a.signal(1,2,books,structures,features),self.params)
        polluted=copy.deepcopy(books); polluted[1].update(label='uncertain',reason='secret',collection_or_partial_overlap=True)
        self.assertEqual(decision,a.predict(a.signal(1,2,polluted,structures,features),self.params))

    def test_review_positives_count_as_fn(self):
        row={'pair_id':'p','book_ids':[1,2],'label':'same_work','collection_or_partial_overlap':False}
        result=a.measured([row],{'p':{'action':'defer'}})['version']
        self.assertEqual((result['tp'],result['fn']),(0,1))

    def test_cache_reuse_and_corruption(self):
        books={1:book(1,'[Chinese] Changed title')}; structures,_=inputs(books)
        v=np.eye(1,384,0,dtype=np.float32)[0]; calls=[]
        def factory(name):
            calls.append(name); return lambda text:v.tolist(),'model'
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve()
            values,summary,hashes=semantic.clean_features(books,structures,{},path,'model',factory)
            self.assertEqual(summary['computed'],1)
            values,summary,_=semantic.clean_features(books,structures,{},path,'model',factory)
            self.assertEqual(summary['cache_hits'],1); self.assertEqual(len(calls),1)
            file=path/next(iter(hashes)); data=json.loads(file.read_text()); data['feature'][0]=0; atomic_json(file,data)
            with self.assertRaisesRegex(ValueError,'clean_cache_corrupt'): semantic.clean_features(books,structures,{},path,'model',factory)

    def test_random_cold_warm_vectors_exactly_equal(self):
        # This seed produces norm rounding, so an unnecessary second normalization
        # would fail the bit-for-bit check rather than accidentally pass.
        rng=np.random.default_rng(0)
        raw=rng.normal(size=384).astype(np.float32)
        normalized=raw/np.linalg.norm(raw)
        self.assertFalse(np.array_equal(normalized,normalized/np.linalg.norm(normalized)))
        books={1:book(1,'unused')}; structures={1:{'clean_text':'fresh view','clean_changed':True}}
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve()
            cold,first,hashes=semantic.clean_features(books,structures,{},path,'m',lambda _:(lambda text:raw.tolist(),'m'))
            warm,second,new_hashes=semantic.clean_features(books,structures,{},path,'m',lambda _:self.fail('warm worker loaded'))
            self.assertTrue(np.array_equal(cold[1],warm[1])); self.assertEqual(hashes,new_hashes)
            self.assertEqual(first['computed'],1); self.assertEqual(second['computed'],0)
            self.assertEqual(second['cache_hits'],1)

    def test_unchanged_raw_vector_reused_without_worker(self):
        books={1:book(1,'already clean')}; structures,_=inputs(books); vector=np.eye(1,384,0,dtype=np.float32)[0]
        with tempfile.TemporaryDirectory() as tmp:
            _,summary,_=semantic.clean_features(books,structures,{1:vector},Path(tmp).resolve(),'m',lambda _:self.fail('worker loaded'))
            self.assertEqual(summary['unchanged_raw_reused'],1)

    def test_final_top_k_is_total_not_union(self):
        books={i:book(i,'Story '+str(i)) for i in range(1,32)}; structures,features=inputs(books)
        row={'pair_id':'p','book_ids':[1,2],'label':'same_work','collection_or_partial_overlap':False}
        result=a.retrieval([row],books,structures,features,self.params)
        self.assertTrue(result['not_each_route_union_topK']); self.assertEqual(result['coverage']['version:final_total_top5']['maximum_emitted_candidates_per_query'],5)
        self.assertLessEqual(result['deduplicated_candidates_max'],30)

    def test_unknown_input_fields_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve(); material={'schema':'recommendation-books-v3','books':[{**book(1,'Story'),'gallery_id':123}]}
            for name in a.FILES: atomic_json(path/name,material if name=='books.json' else {})
            manifest={'schema':'recommendation-development-manifest-v3','pairs':0,'books':1,'files':{name:file_digest(path/name) for name in a.FILES}}
            atomic_json(path/'input-manifest.json',manifest)
            with self.assertRaisesRegex(ValueError,'unapproved_book_fields'): a.load_inputs(path)

    def test_gate_requires_approval_and_matching_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve(); (path/'development').mkdir(); (path/'evaluation').mkdir()
            for name in a.FILES+['input-manifest.json']: atomic_json(path/'development'/name,{})
            atomic_json(path/'evaluation/input-manifest.json',{})
            atomic_json(path/'development-policy.json',{})
            atomic_json(path/'development-report.json',{})
            frozen={'policy_sha256':file_digest(path/'development-policy.json'),'development_report_sha256':file_digest(path/'development-report.json'),'code_sha256':a.code_hashes(),'input_sha256':a.input_hashes(path/'development'),'python':a.platform.python_version(),'numpy':np.__version__,'clean_cache_sha256':{}}
            atomic_json(path/'frozen-policy-v3.json',frozen); sha=file_digest(path/'frozen-policy-v3.json')
            atomic_json(path/'approval.json',{'approved':False,'frozen_policy_sha256':sha}); atomic_json(path/'heldout.json',{'frozen_policy_sha256':sha,'input_manifest_sha256':file_digest(path/'evaluation/input-manifest.json'),'expected_pairs':120})
            with self.assertRaisesRegex(ValueError,'root_approval_required'): a.verify_gate(path,frozen)
            atomic_json(path/'approval.json',{'approved':True,'frozen_policy_sha256':sha}); a.verify_gate(path,frozen)
            atomic_json(path/'development-policy.json',{'changed':True})
            with self.assertRaisesRegex(ValueError,'frozen_policy_or_development_report_changed'): a.verify_gate(path,frozen)
            atomic_json(path/'development-policy.json',{})
            atomic_json(path/'development-report.json',{'changed':True})
            with self.assertRaisesRegex(ValueError,'frozen_policy_or_development_report_changed'): a.verify_gate(path,frozen)
            atomic_json(path/'development-report.json',{})
            frozen['code_sha256']={}
            with self.assertRaisesRegex(ValueError,'frozen_code_or_development_input_changed'): a.verify_gate(path,frozen)

    def test_evaluation_wrong_model_or_baseline_rejected(self):
        metadata={route:{'model_sha256':'model','version':version,'algorithm':route,'pack_id':'p','input_fingerprint':'i'} for route,version in a.FEATURE_VERSIONS.items()}
        baseline={'parameters':self.params,'approved_policy_sha256':'approved'}
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve(); (path/'development').mkdir(); atomic_json(path/'development/v2-baseline.json',baseline)
            frozen={'feature_metadata':copy.deepcopy(metadata)}
            a.validate_evaluation_identity(metadata,baseline,frozen,path)
            for field in ('model_sha256','version','algorithm','pack_id','input_fingerprint'):
                broken=copy.deepcopy(metadata); broken['sscd'][field]='wrong'
                with self.assertRaisesRegex(ValueError,'heldout_feature_metadata_changed'): a.validate_evaluation_identity(broken,baseline,frozen,path)
            for field,value in [('parameters',{}),('approved_policy_sha256','wrong')]:
                broken={**baseline,field:value}
                with self.assertRaisesRegex(ValueError,'heldout_v2_baseline_changed'): a.validate_evaluation_identity(metadata,broken,frozen,path)

    def test_all_excluded_primary_tiers_are_null(self):
        books={1:book(1,'Story'),2:book(2,'Story')}; structures,features=inputs(books)
        for label,overlap in [('uncertain',False),('same_work',True)]:
            row={'pair_id':'p','book_ids':[1,2],'family_ids':['f'],'label':label,'reason':'','collection_or_partial_overlap':overlap}
            pairs={'p':a.signal(1,2,books,structures,features)}; predictions={'p':a.predict(pairs['p'],self.params)}
            result=a.report([row],pairs,predictions,self.params,books,structures,features,{'work_threshold':.9,'series_threshold':.72,'title_weight':.86,'image_weight':.04})
            self.assertEqual(result['primary_samples'],0)
            self.assertIsNone(result['tiers']['strong']['fraction']); self.assertIsNone(result['tiers']['review']['fraction'])

    def test_truncated_actual_text_is_cache_identity(self):
        books={1:book(1,'unused')}; structures={1:{'clean_text':'a'*8192+'tailA','clean_changed':True}}
        vector=np.eye(1,384,0,dtype=np.float32)[0]; seen=[]
        def factory(name): return lambda text:(seen.append(text) or vector.tolist()),'m'
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve()
            _,summary,hashes=semantic.clean_features(books,structures,{},path,'m',factory)
            self.assertEqual(len(seen[0]),8192); self.assertEqual(summary['computed'],1)
            structures[1]['clean_text']='a'*8192+'tailB'
            _,summary,new_hashes=semantic.clean_features(books,structures,{},path,'m',factory)
            self.assertEqual(summary['cache_hits'],1); self.assertEqual(hashes,new_hashes)

    def test_cancel_after_inference_does_not_publish_cache(self):
        books={1:book(1,'unused')}; structures={1:{'clean_text':'changed','clean_changed':True}}
        vector=np.eye(1,384,0,dtype=np.float32)[0]
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve()
            with mock.patch.object(semantic,'checkpoint',side_effect=[None,InterruptedError('cancelled')]):
                with self.assertRaises(InterruptedError): semantic.clean_features(books,structures,{},path,'m',lambda _:(lambda text:vector.tolist(),'m'))
            self.assertEqual(list((path/'feature-cache-v3/minilm').glob('*.json')),[])

    def test_cancel_retrieval_checkpoints(self):
        books={1:book(1,'Story'),2:book(2,'Story')}; structures,features=inputs(books)
        row={'pair_id':'p','book_ids':[1,2],'label':'same_work','collection_or_partial_overlap':False}
        with mock.patch.object(a,'checkpoint',side_effect=InterruptedError('cancelled')):
            with self.assertRaises(InterruptedError): a.retrieval([row],books,structures,features,self.params)

    def test_preexisting_marker_refuses_before_answers(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp).resolve(); (path/'heldout-evaluation.started').touch()
            with self.assertRaisesRegex(ValueError,'heldout_already_started'): a.run('evaluate',path)

    def test_grid_fixed_non_degenerate_target(self):
        self.assertEqual(len(list(__import__('itertools').product(*a.GRID.values()))),75)
        self.assertEqual(a.POLICY['minimum_strong_predictions_per_relation'],10)
        self.assertIn('both relations',a.POLICY['selection'])


if __name__=='__main__': unittest.main()
