"""Offline series V2: prepare -> run -> render -> verify. Never overwrites V1."""
from __future__ import annotations
import argparse
import contextlib
import itertools
import json
import os
from pathlib import Path
import shutil
import tempfile
import time
from core import atomic_json, checkpoint, digest, file_digest, install_signals, reject_symlinks
from features import check_resources, rss_bytes, worker_lock
import series_data as data
import series_run as prior
import series_match as match
import series2_cluster as cluster
import series2_profiles as profiles
from series2_metrics import audit

ROOT = data.ROOT
OUTPUT = ROOT/'.runtime/tests/runs/recommendations/series-v2'
CODE = ('series2_cluster.py','series2_profiles.py','series2_metrics.py','series2_run.py')
VARIANTS = ('complete_legacy','average_legacy','complete_profile','average_profile')
CONFIG = {'algorithm':'series-partition-v2','variants':VARIANTS,'default':'average_profile',
          'floor':cluster.FLOOR,'ambiguity_gap':profiles.AMBIGUITY_GAP,
          'title_support':profiles.TITLE_SUPPORT,'max_options':profiles.MAX_ALTERNATIVES,
          'max_members':cluster.CAPACITY,'parameter_search':False,'all420_seen':True,
          'cv':'frozen_V1_fold_parameters_transductive_unlabeled_pool_no_new_fit'}


def path_in_output(path):
    p=Path(path).absolute();reject_symlinks(p)
    if '..' in p.parts or not p.is_relative_to(OUTPUT) or p==OUTPUT or p.is_relative_to(OUTPUT/'coordinator'):
        raise ValueError('output_outside_series_v2_or_reserved')
    return p


@contextlib.contextmanager
def publication(target):
    target=path_in_output(target)
    reject_symlinks(OUTPUT);OUTPUT.mkdir(mode=0o700,parents=True,exist_ok=True);os.chmod(OUTPUT,0o700)
    if target.parent!=OUTPUT:raise ValueError('only_top_level_publication_allowed')
    if target.exists():raise ValueError('completed_output_exists_use_verify')
    pending=Path(tempfile.mkdtemp(prefix='.pending-v2-',dir=OUTPUT));os.chmod(pending,0o700)
    try:
        yield pending
        checkpoint()
        if target.exists():raise ValueError('output_publication_conflict')
        os.rename(pending,target)
        fd=os.open(OUTPUT,os.O_RDONLY)
        try:os.fsync(fd)
        finally:os.close(fd)
    finally:
        if pending.exists():shutil.rmtree(pending)


def read(path):
    reject_symlinks(path)
    return json.loads(Path(path).read_text())


def source_hashes():
    return {n:file_digest(Path(__file__).parent/n) for n in CODE}


def manifest(directory, extra=None):
    files={str(p.relative_to(directory)):file_digest(p) for p in sorted(directory.rglob('*')) if p.is_file() and p.name!='manifest.json'}
    atomic_json(directory/'manifest.json',{'schema':'series-v2-manifest','files':files,**(extra or {})})


def verify_dir(directory):
    directory=path_in_output(directory)
    m=read(directory/'manifest.json')
    for name,sha in m['files'].items():
        relative=Path(name)
        if relative.is_absolute() or '..' in relative.parts:raise ValueError('invalid_manifest_path')
        p=directory/relative;reject_symlinks(p)
        if file_digest(p)!=sha:raise ValueError('output_digest_changed')
    actual={str(p.relative_to(directory)) for p in directory.rglob('*') if p.is_file() and p.name!='manifest.json'}
    if actual!=set(m['files']):raise ValueError('unexpected_output_file')
    return m


def resource(start,cpu):
    return {'wall_seconds':time.monotonic()-start,'cpu_seconds':time.process_time()-cpu,
            'peak_rss_bytes':rss_bytes(),'models_loaded':0,'inferences':0,'cpu_threads':1,'nas_verified':False}


def prepare():
    started,cpu=time.monotonic(),time.process_time()
    prior_policy=prior.verify_policy(); prior.verify(data.OUTPUT/'final')
    values,corpus,cache,hashes=prior.inputs()
    _,rows,_,_,works,mapping,conflicts,assignments,folds,_=values
    cv=read(data.OUTPUT/'calibration/grouped-cv.json')
    policies=[prior_policy['parameters'],*[f['selection']['parameters'] for f in cv['folds']]]
    prepared=read(data.OUTPUT/'calibration/pair-inputs.json')['rows']
    if sorted(r['pair_id'] for r in rows)!=sorted(r['pair_id'] for r in prepared):raise ValueError('changed_rows')
    seen,edges=set(),[]
    for n,wid in enumerate(corpus.ids):
        checkpoint()
        for other in corpus.retrieve(wid)['candidates']:
            key=tuple(sorted((wid,other)))
            if key in seen:continue
            seen.add(key);s=corpus.signal(*key)
            if any(match.predict(s,p)!='none' for p in policies):
                edges.append({'work_ids':key,'signal':s})
        if n%200==0:print(json.dumps({'phase':'prepare_evidence','works_done':n,'works':len(works)}),flush=True)
        check_resources()
    hashes={str((data.old.SOURCE/k).relative_to(ROOT)) if '/' not in k else k:v for k,v in hashes.items()}
    inherited={str((Path(prior.__file__).parent/n).relative_to(ROOT)):v for n,v in prior.code_hashes().items()}
    hashes.update(inherited)
    for p in (data.OUTPUT/'calibration/policy.json',data.OUTPUT/'calibration/grouped-cv.json',
              data.OUTPUT/'calibration/pair-inputs.json',data.OUTPUT/'final/manifest.json',data.OUTPUT/'final/report.json'):
        hashes[str(p.relative_to(ROOT))]=file_digest(p)
    with publication(OUTPUT/'prepared') as out:
        atomic_json(out/'freeze.json',{'config':CONFIG,'source_sha256':source_hashes(),'input_sha256':hashes,
                    'fullfit_parameters':prior_policy['parameters'],'fold_parameters':[f['selection']['parameters'] for f in cv['folds']],
                    'folds':folds,'all_data_seen':True})
        atomic_json(out/'evidence.json',{'rows':prepared,'edges':edges,'cv':cv,'works':works,
                                        'input_fingerprint':digest(hashes)})
        atomic_json(out/'resources.json',resource(started,cpu));manifest(out)
    return {'works':len(works),'candidate_pairs_computed':len(seen),'retained_seed_edges':len(edges),
            'resources':resource(started,cpu),'recalibrated':False}


def verify_prepared():
    verify_dir(OUTPUT/'prepared')
    f=read(OUTPUT/'prepared/freeze.json')
    if digest(f['config'])!=digest(CONFIG) or f['source_sha256']!=source_hashes():raise ValueError('frozen_v2_algorithm_changed')
    for name,sha in f['input_sha256'].items():
        path=ROOT/name;reject_symlinks(path)
        allowed=(Path(__file__).parent,data.OUTPUT,data.old.SOURCE,ROOT/'.runtime/tests/runs/recommendations/run-v2')
        if '..' in Path(name).parts or not any(path.is_relative_to(p) for p in allowed):raise ValueError('unapproved_frozen_input')
        if file_digest(path)!=sha:raise ValueError('frozen_input_changed')
    return f


def run_variant(corpus,edges,policy,name,rows,anchors=()):
    method,subject=name.split('_')
    guard=cluster.legacy_subject if subject=='legacy' else profiles.representative_subject
    seeds=[tuple(e['work_ids']) for e in edges if match.predict(e['signal'],policy)!='none']
    partition=cluster.agglomerate(corpus.ids,seeds,corpus,policy,method,guard,anchors)
    before=profiles.proposals(partition['groups'],corpus,policy,method,guard)
    revised=profiles.boundary_review(partition['groups'],seeds,corpus,policy,method,guard)
    proposals=profiles.proposals(revised['groups'],corpus,policy,method,guard)
    return {'variant':name,'policy':policy,'proposals':proposals,'pending':revised['pending'],
            'ungrouped_work_ids':[i for g in revised['groups'] if len(g['work_ids'])==1 and not g.get('anchor') for i in g['work_ids']],
            'before_boundary_audit':audit(before,[],rows,corpus.ids),
            'audit':audit(proposals,revised['pending'],rows,corpus.ids),
            'candidate_merges_checked':partition['candidate_merges_checked'],'merges':partition['merge_count']}


def virtual_joins(corpus, prior_report, evidence, policy):
    results=[]
    for scenario in prior_report['virtual_series_scenarios']:
        series=scenario['series'];q=scenario['query_work_id'];ids=[*series['work_ids'],q]
        seeds=[tuple(sorted((a,b))) for a,b in itertools.combinations(ids,2) if match.predict(corpus.signal(a,b),policy)!='none']
        partition=cluster.agglomerate(ids,seeds,corpus,policy,'average',profiles.representative_subject,[series])
        revised=profiles.boundary_review(partition['groups'],seeds,corpus,policy,'average',profiles.representative_subject)
        results.append({'series':series,'query_work_id':q,
                        'proposals':profiles.proposals(revised['groups'],corpus,policy,'average',profiles.representative_subject),
                        'pending':revised['pending'],'scope':'three_direct_positive_pairs_virtual_not_real_series_accuracy'})
    return results


def run():
    freeze=verify_prepared();started,cpu=time.monotonic(),time.process_time()
    evidence=read(OUTPUT/'prepared/evidence.json')
    _,corpus,_,_=prior.inputs();rows=evidence['rows'];old=read(data.OUTPUT/'final/report.json')
    variants={};oof={}
    for name in VARIANTS:
        print(json.dumps({'phase':'cluster_full','variant':name}),flush=True)
        variants[name]=run_variant(corpus,evidence['edges'],freeze['fullfit_parameters'],name,rows)
        predictions,before_predictions,fold_results={},{},[]
        for fold in evidence['cv']['folds']:
            checkpoint();policy=fold['selection']['parameters'];ids=set(fold['validation_ids'])
            # Unlabeled full-pool features are transductive context, never labels.
            if policy==freeze['fullfit_parameters']:
                result=variants[name]
            else:
                result=run_variant(corpus,evidence['edges'],policy,name,rows)
            predictions.update({i:p for i,p in result['audit']['predictions'].items() if i in ids})
            before_predictions.update({i:p for i,p in result['before_boundary_audit']['predictions'].items() if i in ids})
            fold_results.append({'fold':fold['fold'],'policy_from_other_four_folds':policy,
                                 'validation_ids':sorted(ids),'groups':len(result['proposals'])})
            print(json.dumps({'phase':'cluster_fold','variant':name,'fold':fold['fold']}),flush=True)
        oof[name]={'folds':fold_results,'predictions':predictions,
                   'before_boundary_metrics':{k:match.metrics(rows,before_predictions,k) for k in ('strong','supplementary','combined')},
                   'metrics':{k:match.metrics(rows,predictions,k) for k in ('strong','supplementary','combined')},
                   'transductive_unlabeled_pool':True,'new_parameter_fit':False}
    old_edges={tuple(sorted(edge)) for p in old['proposals'] for edge in itertools.combinations(p['work_ids'],2)}
    old_choices={r['pair_id']:'strong' if r['work_ids'][0]!=r['work_ids'][1] and tuple(sorted(r['work_ids'])) in old_edges else 'none' for r in rows}
    report={'schema':'series-report-v2','config':CONFIG,'input_fingerprint':evidence['input_fingerprint'],
            'annotation_scope':'420_seen_development_not_independent_holdout','works':len(corpus.ids),
            'prior_v1':{'raw_proposals':len(old['proposals']),'fullfit_group_pair_metrics':match.metrics(rows,old_choices),
                        'pair_oof':old['oof'],'not_recalculated':True},'fullfit':variants,'oof':oof,
            'virtual_series':virtual_joins(corpus,old,evidence,freeze['fullfit_parameters']),
            'resources':resource(started,cpu),'integration_recommended':False,
            'limitations':['sparse_pair_labels_not_complete_series_truth','no_new_holdout',
                          'primary_partition_does_not_guarantee_correct_series','no_application_or_NAS_validation']}
    with publication(OUTPUT/'analysis') as out:
        atomic_json(out/'report.json',report)
        for name,value in variants.items():atomic_json(out/(name+'.json'),value)
        manifest(out,{'prepared_manifest_sha256':file_digest(OUTPUT/'prepared/manifest.json'),
                      'source_sha256':source_hashes()})
    return {'variants':{n:{k:v['audit'][k] for k in ('primary_groups','pending_works','alternative_count','pair_metrics')} for n,v in variants.items()},
            'resources':report['resources'],'integration_recommended':False}


def verify():
    verify_prepared();m=verify_dir(OUTPUT/'analysis')
    if m['source_sha256']!=source_hashes() or m['prepared_manifest_sha256']!=file_digest(OUTPUT/'prepared/manifest.json'):
        raise ValueError('analysis_chain_changed')
    count=len(m['files'])
    if (OUTPUT/'final').exists():
        f=verify_dir(OUTPUT/'final')
        import series2_review
        if f['renderer_sha256']!=series2_review.source_hashes() or f['analysis_manifest_sha256']!=file_digest(OUTPUT/'analysis/manifest.json'):
            raise ValueError('review_source_or_analysis_changed')
        count+=len(f['files'])
    return {'verified':True,'files':count,'rescored':False,'recalibrated':False}


def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('action',choices=('prepare','run','render','verify'));args=p.parse_args()
    install_signals();reject_symlinks(OUTPUT);OUTPUT.mkdir(mode=0o700,exist_ok=True,parents=True)
    with worker_lock(OUTPUT):
        if args.action=='prepare':result=prepare()
        elif args.action=='run':result=run()
        elif args.action=='render':
            verify()
            import series2_review
            result=series2_review.render()
        else:result=verify()
    print(json.dumps(result,ensure_ascii=False),flush=True)


if __name__=='__main__':main()
