"""Read-only post-report audit and grouped review, never re-score or re-fit."""
from __future__ import annotations
import argparse
import base64
import collections
import hashlib
import itertools
import json
from pathlib import Path
import re
from core import atomic_bytes, atomic_json, digest, file_digest, install_signals
import series_data as data
import series_match as match
import series_review as review
import series_run as run

SOURCES = ('series_delivery.py','series_review.py','series_review.js','test_series_delivery.py')


def bundles(proposals):
    grouped = collections.defaultdict(list)
    for p in proposals:
        grouped[p['subject']].append(p)
    result = []
    for subject, values in sorted(grouped.items()):
        values.sort(key=lambda p: (p['tier'] != 'strong', p['id']))
        result.append({'subject':subject,'proposal_ids':[p['id'] for p in values]})
    return result


def summarize(report, baseline_audit):
    rows = [r for r in report['diagnostics'] if 'excluded_from_primary' not in r['flags']]
    covered = {tuple(sorted(edge)) for p in report['proposals'] for edge in itertools.combinations(p['work_ids'],2)}
    positives = [r for r in rows if r['label']=='same_series']
    found = sum(tuple(sorted(r['work_ids'])) in covered for r in positives)
    totals = {}
    for label in ('v2','v3'):
        choices = {r['pair_id']:'strong' if r['work_ids'][0]!=r['work_ids'][1] and
                   baseline_audit['predictions_fixed_'+label][r['pair_id']]['action']=='same_series' else 'none' for r in rows}
        totals[label]=match.metrics(rows,choices)
    by_subject = bundles(report['proposals'])
    return {'schema':'series-delivery-audit-v1','rescored':False,'parameters_changed':False,
            'known_positive_coverage_in_create_plans':{'found':found,'positive_pairs':len(positives),
                 'coverage':found/len(positives) if positives else None,
                 'scope':'seen_labeled_pair_coverage_in_complete_proposals_not_group_precision_or_library_recall'},
            'raw_proposal_count':len(report['proposals']),'review_bundle_count':len(by_subject),
            'largest_alternative_bundle':max((len(b['proposal_ids']) for b in by_subject),default=0),
            'bundles_do_not_infer_merged_series':True,'no_candidates_deleted':True,
            'baseline_actions_with_same_work_exclusion_only':totals,
            'baseline_context_note':'legacy_frozen_actions_with_self_work_exclusion_not_multi_edition_max_rescoring',
            'oof_strong_target_met':report['oof']['strong']['precision'] is not None and report['oof']['strong']['precision']>=.95,
            'oof_combined_target_met':report['oof']['combined']['precision'] is not None and report['oof']['combined']['precision']>=.85,
            'integration_recommended':False,
            'limitations':['strong_target_not_met','complete_group_coverage_below_pair_candidate_coverage',
                           'exact_common_subject_requirement_drops_related_titles','many_overlapping_unlabeled_combinations',
                           'virtual_series_only_no_real_group_accuracy','all420_seen_no_independent_holdout']}


EXTRA_JS = '''
function bundleCard(bundle){const wrapper=el('section',undefined,'panel');const lookup=new Map(R.proposals.map(p=>[p.id,p]));wrapper.append(el('p','同一题名主体下有 '+bundle.proposal_ids.length+' 种完整成员组合；这些是替代方案，不代表应合并为一个系列。','muted'));const select=el('select');select.setAttribute('aria-label','查看其他成员组合');for(const [n,id] of bundle.proposal_ids.entries()){const p=lookup.get(id),opt=el('option','组合 '+(n+1)+' · '+p.work_ids.length+' 个作品 · '+names[p.tier]);opt.value=id;select.append(opt);}const view=el('div');function choose(){view.replaceChildren();view.append(planCard(lookup.get(select.value||bundle.proposal_ids[0])));}select.onchange=choose;wrapper.append(select,view);choose();return wrapper;}
'''


def grouped_script():
    script=Path(review.__file__).with_suffix('.js').read_text()
    before="if(mode==='create')return R.proposals;"
    if before not in script:raise ValueError('renderer_contract_changed')
    script=script.replace(before,"if(mode==='create')return D.proposal_bundles;",1)
    before="if(mode==='create')content.append(planCard(r));"
    if before not in script:raise ValueError('renderer_contract_changed')
    script=script.replace(before,"if(mode==='create')content.append(bundleCard(r));",1)
    return EXTRA_JS+script+'''\nconst coverage=D.delivery_summary.known_positive_coverage_in_create_plans;document.getElementById('summary').append(el('p','完整成组方案覆盖已有系列正例 '+coverage.found+' / '+coverage.positive_pairs+'（'+pct(coverage.coverage)+'）。配对候选召回不能代替完整方案覆盖，本轮尚不建议接入应用。','risk'));'''


def source_hashes():
    return {n:file_digest(Path(__file__).parent/n) for n in SOURCES}


def audit():
    run.verify_policy();run.verify(data.OUTPUT/'final')
    report=data.old.read_json(data.OUTPUT/'final/report.json')
    baseline=data.old.read_json(data.OUTPUT/'calibration/baseline-audit.json')
    summary=summarize(report,baseline)
    html=(data.OUTPUT/'final/review.html').read_text()
    found=re.search(r'<script id="payload" type="application/json">(.*?)</script>',html,re.S)
    if found is None:raise ValueError('missing_review_payload')
    payload=json.loads(found[1])
    if digest(payload['report'])!=digest(report):raise ValueError('review_report_disagrees')
    for b in payload['books'].values():
        if b['image'] is not None:
            if not re.fullmatch(r'covers/[a-f0-9]{64}\.jpg',b['image']):raise ValueError('unsafe_review_image')
            b['image']='../final/'+b['image']
    payload['proposal_bundles']=bundles(report['proposals']);payload['delivery_summary']=summary
    script=grouped_script();sha='sha256-'+base64.b64encode(hashlib.sha256(script.encode()).digest()).decode()
    rendered=review.PAGE.replace('__SCRIPT_HASH__',sha).replace('__JAVASCRIPT__',script).replace('__PAYLOAD__',review.safe_json(payload))
    with run.publication(data.OUTPUT/'delivery') as output:
        atomic_json(output/'summary.json',summary)
        atomic_bytes(output/'review.html',rendered.encode())
        run.manifest(output,{'source_sha256':source_hashes(),
                     'final_manifest_sha256':file_digest(data.OUTPUT/'final/manifest.json'),
                     'algorithm_parameters_unchanged':True})
    return summary


def verify():
    run.verify_policy();run.verify(data.OUTPUT/'final');m=run.verify(data.OUTPUT/'delivery')
    if m['source_sha256']!=source_hashes() or m['final_manifest_sha256']!=file_digest(data.OUTPUT/'final/manifest.json'):
        raise ValueError('delivery_source_or_report_changed')
    return {'verified':True,'rescored':False,'parameters_changed':False}


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('action',choices=('audit','verify'));args=parser.parse_args()
    install_signals();print(json.dumps(audit() if args.action=='audit' else verify(),ensure_ascii=False))
