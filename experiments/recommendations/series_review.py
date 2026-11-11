"""Read-only local series review. All private text is rendered with textContent."""
from __future__ import annotations
import base64
import hashlib
import json
from pathlib import Path
from core import INPUT, atomic_bytes, checkpoint, cover_file, file_digest
import series_data as data


def safe_json(value):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')


def display_material(report, corpus, output):
    exported = data.old.read_json(data.ROOT/'.runtime/tests/runs/recommendations/run-v2/books.json')
    originals = {b['id']: b for b in exported['books']}
    used = {i for p in report['proposals'] for i in p['work_ids']}
    for scenario in report['virtual_series_scenarios']:
        used.update(scenario['series']['work_ids']); used.add(scenario['query_work_id'])
    for q in report['forward_ranking']['queries']:
        used.add(q['query_work_id']); used.update(p['work_id'] for p in q['final_top20'])
    for r in report['diagnostics']: used.update(r['work_ids'])
    works = {wid: {'id': wid, 'edition_ids': corpus.works[wid]['edition_ids']} for wid in sorted(used)}
    books, assets, missing, total, hashes = {}, set(), 0, 0, {}
    for i in sorted({i for w in works.values() for i in w['edition_ids']}):
        checkpoint()
        b, original = corpus.books[i], originals.get(i)
        if original is None or any(original.get(k) != v for k, v in b.items()):
            raise ValueError('cover_metadata_whitelist_changed')
        raw = None; image = None
        try:
            path = cover_file(INPUT, original.get('cover_path'))
            if path: raw = path.read_bytes()
        except (OSError, ValueError):
            pass
        if raw is not None:
            sha = hashlib.sha256(raw).hexdigest(); image = 'covers/'+sha+'.jpg'
            hashes[str(path.relative_to(data.ROOT))] = sha
            if sha not in assets:
                atomic_bytes(output/image, raw); assets.add(sha); total += len(raw)
        else: missing += 1
        books[str(i)] = {'id':i,'title':b.get('title') or '', 'title_original':b.get('title_jpn') or '',
                         'pages':b.get('pages'),'language':b.get('language_code'),'tags':b.get('tags',[]),
                         'image':image,'image_status':'已有授权封面' if image else '封面缺失或不安全，未读取'}
    return {'works':works,'books':books}, {'works':len(works),'books':len(books),'unique_cover_assets':len(assets),
               'cover_bytes':total,'missing_cover_editions':missing,'cover_input_sha256':hashes}


PAGE = '''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self' file:; style-src 'unsafe-inline'; script-src '__SCRIPT_HASH__'; connect-src 'none'; base-uri 'none'; form-action 'none'; object-src 'none'">
<title>系列推荐 · 离线复核</title><style>
:root{color-scheme:light dark;font:15px/1.65 system-ui;color:light-dark(#223047,#e1e8f3);background:light-dark(#f4f6fa,#11161f)}body{max-width:1240px;margin:auto;padding:24px}h1{font-size:26px}h2{font-size:19px}h3{font-size:16px;margin:5px 0}p{margin:8px 0}.panel,article{border:1px solid light-dark(#d7dfeb,#39455b);border-radius:12px;background:light-dark(#fff,#1a2230);padding:20px;margin:16px 0}.warning{border-left:5px solid #bf8510}.toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:12px;position:sticky;top:0;z-index:1}button,select{font:inherit;color:inherit;background:light-dark(#fff,#222f43);border:1px solid #8796aa;border-radius:7px;padding:7px 12px}button{cursor:pointer}button:disabled{opacity:.4}.members{display:grid;grid-template-columns:repeat(auto-fit,minmax(235px,1fr));gap:12px}.work{padding:12px;background:light-dark(#f4f6fa,#111923);border-radius:8px;overflow-wrap:anywhere}.work img{width:100px;height:140px;object-fit:contain;float:left;margin:0 12px 8px 0}.work:after{content:'';display:block;clear:both}.muted{color:light-dark(#596a80,#a6b3c8)}.chip{display:inline-block;font-size:12px;padding:1px 8px;border-radius:10px;border:1px solid #8999ae;margin:2px}.strong{color:light-dark(#1257b3,#87b8ff)}.supplementary{color:light-dark(#8a5714,#edbd70)}.risk{color:light-dark(#9d3c21,#ffb39d)}.title{font-weight:650;overflow-wrap:anywhere}details{margin-top:12px}summary{cursor:pointer}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:7px;border-bottom:1px solid #8a98ac}.empty{padding:24px;text-align:center}.nowrap{white-space:nowrap}@media(max-width:640px){body{padding:10px}.panel,article{padding:12px}.toolbar{position:static}.members{grid-template-columns:1fr}table{font-size:12px}.work img{width:80px;height:112px}}@media(prefers-reduced-motion:no-preference){button{transition:background-color .2s}}
</style></head><body><h1>系列推荐 · 离线复核</h1><div class="panel warning"><p><strong>全部420条均为已见开发数据。较强层开发验证未达到95%精确率目标，所有方案都需人工核对。</strong></p><p>本页只读，不联网、不修改数据库。完整方案使用全数据拟合策略；下方五折指标与方案质量不同，未知关系不能当作正确或误报。</p><div id="summary"></div></div>
<div class="panel toolbar"><label>查看 <select id="mode"><option value="create">组成系列</option><option value="join">加入已有系列（虚拟场景）</option><option value="queries">Work候选排序</option><option value="errors">误报／漏报分析</option></select></label><button id="previous">上一页</button><button id="next">下一页</button><span id="progress" role="status"></span></div><main id="content"></main>
<script id="payload" type="application/json">__PAYLOAD__</script><script>__JAVASCRIPT__</script></body></html>'''


def render(report, corpus, output):
    display, audit = display_material(report, corpus, output)
    javascript = Path(__file__).with_suffix('.js').read_text()
    script_hash = 'sha256-'+base64.b64encode(hashlib.sha256(javascript.encode()).digest()).decode()
    # Substitute payload last: private strings cannot become template directives.
    page = PAGE.replace('__SCRIPT_HASH__', script_hash).replace('__JAVASCRIPT__', javascript)
    page = page.replace('__PAYLOAD__', safe_json({'report':report,**display}))
    atomic_bytes(output/'review.html',page.encode())
    return audit
