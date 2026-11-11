"""Read-only offline V2 review; images come only from the authorized cover copy."""
from __future__ import annotations
import base64
import hashlib
from pathlib import Path
from core import INPUT, atomic_bytes, atomic_json, checkpoint, cover_file, file_digest
import series2_run as run
import series_review as old_review


def source_hashes():
    return {n:file_digest(Path(__file__).parent/n) for n in ('series2_review.py','series2_review.js')}


def summary(report):
    return {'schema':'series-v2-delivery','all420_seen':True,'group_accuracy_claimed':False,
            'default_variant':report['config']['default'],'prior_v1':report['prior_v1'],
            'variants':{n:{'before_boundary':v['before_boundary_audit'],'after_boundary':v['audit'],
                           'oof':report['oof'][n]['metrics'],'oof_before_boundary':report['oof'][n]['before_boundary_metrics']}
                        for n,v in report['fullfit'].items()},
            'resources':report['resources'],'integration_recommended':False,
            'notes':['primary_disjointness_not_proof_of_correctness','unknown_pairs_not_counted_correct_or_wrong',
                     'alternatives_are_mutually_exclusive_possibilities_not_additional_primary_groups',
                     'fullfit_and_transductive_grouped_oof_reported_separately']}


def material(report, output):
    evidence=run.read(run.OUTPUT/'prepared/evidence.json')
    books=run.read(run.ROOT/'.runtime/tests/runs/recommendations/run-v2/books.json')['books']
    approved=run.read(run.data.old.SOURCE/'evaluation/books.json')['books']
    whitelist={b['id']:b for b in approved}
    by_id={b['id']:b for b in books};works=evidence['works'];used=set()
    for value in report['fullfit'].values():
        for p in value['proposals']:used.update(p['work_ids'])
        for p in value['pending']:
            used.add(p['work_id'])
            for o in p['options']:used.update(o['target_work_ids'])
    for row in evidence['rows']:used.update(row['work_ids'])
    for scenario in report['virtual_series']:
        used.update(scenario['series']['work_ids']);used.add(scenario['query_work_id'])
    displayed={i:works[i] for i in sorted(used)};images={};display_books={};missing=0;total=0
    for i in sorted({i for w in displayed.values() for i in w['edition_ids']}):
        checkpoint();b=by_id[i];image=None
        if i not in whitelist or any(b.get(k)!=v for k,v in whitelist[i].items()):
            raise ValueError('display_whitelist_changed')
        try:
            path=cover_file(INPUT,b.get('cover_path'))
            raw=path.read_bytes() if path else None
        except (OSError,ValueError):raw=None
        if raw is not None:
            sha=hashlib.sha256(raw).hexdigest();image='covers/'+sha+'.jpg'
            if image not in images:
                atomic_bytes(output/image,raw);images[image]=sha;total+=len(raw)
        else:missing+=1
        display_books[str(i)]={'id':i,'title':b.get('title') or '', 'title_original':b.get('title_jpn') or '',
                               'pages':b.get('pages'),'language':b.get('language_code'),
                               'tags':b.get('tags',[]),'image':image}
    return {'works':displayed,'books':display_books,'rows':evidence['rows']}, {
            'works':len(displayed),'editions':len(display_books),'unique_covers':len(images),
            'cover_bytes':total,'missing_covers':missing}


PAGE='''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self' file:; style-src 'unsafe-inline'; script-src '__SCRIPT_HASH__'; connect-src 'none'; base-uri 'none'; form-action 'none'; object-src 'none'">
<title>系列聚类V2 · 离线复核</title><style>
:root{color-scheme:light dark;font:15px/1.6 system-ui;background:light-dark(#f4f6fa,#111720);color:light-dark(#203149,#e2e9f4)}body{max-width:1240px;margin:auto;padding:24px}h1{font-size:26px}h2{font-size:20px}h3{font-size:16px}.panel,article{border:1px solid light-dark(#d4dfec,#3c485d);border-radius:12px;background:light-dark(white,#1a2535);padding:18px;margin:14px 0}.warning{border-left:5px solid #c28616}.toolbar{display:flex;align-items:center;gap:12px;flex-wrap:wrap;position:sticky;top:0;z-index:1}button,select{font:inherit;background:light-dark(white,#25344c);color:inherit;border:1px solid #8997ad;border-radius:7px;padding:7px 12px}button{cursor:pointer}button:disabled{opacity:.4}.members{display:grid;grid-template-columns:repeat(auto-fit,minmax(245px,1fr));gap:12px}.work{padding:12px;background:light-dark(#f1f5fa,#111c2b);border-radius:8px;overflow-wrap:anywhere}.work img{width:90px;height:128px;object-fit:contain;float:left;margin:0 12px 8px 0}.work:after{content:'';display:block;clear:both}.muted{color:light-dark(#536781,#acb9cc)}.risk{color:light-dark(#a24720,#f6be91)}.strong{color:light-dark(#2459aa,#9fc8ff)}.title{font-weight:650}.chip{display:inline-block;padding:2px 8px;font-size:12px;border:1px solid #8fa0b7;border-radius:10px;margin:2px}details{margin:10px 0}summary{cursor:pointer}.table-wrap{overflow:auto}table{border-collapse:collapse;width:100%;font-size:13px}td,th{border-bottom:1px solid #8798ae;text-align:left;padding:8px;white-space:nowrap}.member-tools{display:flex;gap:10px;align-items:center;margin:10px 0}.empty{padding:25px;text-align:center}button:focus-visible,select:focus-visible,summary:focus-visible{outline:3px solid #488adb;outline-offset:3px}@media(max-width:640px){body{padding:10px}.panel,article{padding:12px}.toolbar{position:static}.members{grid-template-columns:1fr}}@media(prefers-reduced-motion:no-preference){button{transition:background-color .2s}}
</style></head><body><h1>系列聚类V2 · 离线复核</h1><section class="panel warning"><p><strong>实验方案，需要人工核对；本页只读，不联网、不修改应用。</strong></p><p>420条标注均为已见数据。主方案不重复归属，但这不代表系列一定正确；无标注关系保持未知。备选只表示可能去向，不能全部同时采纳。</p><div id="summary"></div></section>
<div class="panel toolbar"><label>对照 <select id="variant"><option value="average_profile">平均连接＋主体画像（主方案）</option><option value="complete_profile">完全连接＋主体画像</option><option value="average_legacy">平均连接＋旧主体</option><option value="complete_legacy">完全连接＋旧主体</option></select></label><label>查看 <select id="mode"><option value="primary">组成系列</option><option value="pending">待核对与备选</option><option value="join">加入已有系列（虚拟）</option><option value="comparison">四项对照与覆盖</option><option value="cases">已知漏组／混组</option></select></label><button id="previous">上一页</button><button id="next">下一页</button><span id="progress" role="status" aria-live="polite"></span></div><main id="content"></main>
<script id="payload" type="application/json">__PAYLOAD__</script><script>__JAVASCRIPT__</script></body></html>'''


def page(payload):
    script=Path(__file__).with_suffix('.js').read_text()
    sha='sha256-'+base64.b64encode(hashlib.sha256(script.encode()).digest()).decode()
    return PAGE.replace('__SCRIPT_HASH__',sha).replace('__JAVASCRIPT__',script).replace('__PAYLOAD__',old_review.safe_json(payload))


def render():
    report=run.read(run.OUTPUT/'analysis/report.json');start,cpu=run.time.monotonic(),run.time.process_time()
    result=summary(report)
    with run.publication(run.OUTPUT/'final') as output:
        displayed,display_audit=material(report,output)
        atomic_bytes(output/'review.html',page({'report':report,'summary':result,**displayed}).encode())
        atomic_json(output/'summary.json',result);atomic_json(output/'display-audit.json',display_audit)
        atomic_json(output/'resources.json',run.resource(start,cpu))
        run.manifest(output,{'renderer_sha256':source_hashes(),
                            'analysis_manifest_sha256':file_digest(run.OUTPUT/'analysis/manifest.json')})
    return {'review':str(run.OUTPUT/'final/review.html'),'display':display_audit,'rescored':False}
