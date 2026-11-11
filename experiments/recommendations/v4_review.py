"""Offline V4 review renderer. Private strings only reach text nodes.

Cover references are mapped into the explicitly provided copy, then SHA256-named
local assets. No paths, custom labels or source organizational hints become model
inputs. No source DB, network resource, editor or label export is opened.
"""
from __future__ import annotations
import hashlib
import html
import json
from pathlib import Path
from core import INPUT, atomic_bytes, checkpoint, cover_file, file_digest, reject_symlinks
from v4_data import ROOT, read_json

EXPORT = ROOT / '.runtime/tests/runs/recommendations/run-v2/books.json'


def safe_json(value):
    # HTML's script parser terminates on literal </script> even in JSON strings.
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')


def display_books(report, books, output, export=EXPORT, source=INPUT):
    reject_symlinks(source); exported = read_json(export)
    by_id = {b['id']: b for b in exported['books']}
    used = {i for r in report['diagnostics']['rows'] for i in r['book_ids']}
    used.update(c['supporting_edition_id'] for q in report['forward_retrieval']['queries'] for c in q['final_top20'])
    display = {}; missing = 0; assets = set(); total = 0; hashes = {str(export.relative_to(ROOT)): file_digest(export)}
    covers = output / 'covers'; covers.mkdir(mode=0o700, exist_ok=True)
    for i in sorted(used):
        checkpoint(); book = books[i]; original = by_id.get(i)
        if original is None or any(original.get(k) != v for k, v in book.items()): raise ValueError('cover_export_metadata_changed')
        path = None; status = '没有封面引用'; image = None
        raw = None
        try:
            path = cover_file(source, original.get('cover_path'))
            if path: raw = path.read_bytes()
        except (ValueError, OSError):
            status = '封面缺失或不安全，未读取'; image = None
        if raw is not None:
            sha = hashlib.sha256(raw).hexdigest(); image = 'covers/' + sha + '.jpg'; status = '来自授权副本的已有封面'
            hashes[str(path.relative_to(ROOT))] = sha
            if sha not in assets:
                # Output errors (e.g. disk full) fail publication, not a fake missing cover.
                atomic_bytes(output / image, raw); total += len(raw); assets.add(sha)
        if image is None: missing += 1
        display[str(i)] = {'id': i, 'title': book.get('title') or '', 'title_jpn': book.get('title_jpn') or '', 'pages': book.get('pages'),
                           'tags': book.get('tags', []), 'language': book.get('language_code'), 'image': image, 'image_status': status,
                           'custom_labels_status': '本导出未包含人工版本标签，未核对；不代表实际为空',
                           'manual_properties_status': '本导出未包含人工属性归属，未核对'}
    return display, {'display_books': len(display), 'unique_cover_assets': len(assets), 'cover_bytes': total, 'missing_or_rejected_books': missing, 'private_input_sha256': hashes}


PAGE = '''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self' file:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'none'; base-uri 'none'; form-action 'none'; object-src 'none'">
<title>同作品推荐V4 · 离线复核</title><style>
:root{color-scheme:light dark;font:14px/1.6 system-ui;background:light-dark(#f3f5f8,#12151b);color:light-dark(#182235,#e5eaf3)}body{margin:0 auto;padding:24px;max-width:1240px}h1{font-size:24px}h2{font-size:18px}p{margin:8px 0}.notice,.toolbar,article,.example{background:light-dark(white,#1c2330);border:1px solid light-dark(#dce3ec,#354156);border-radius:12px;padding:18px;margin:16px 0}.toolbar{position:sticky;top:6px;z-index:2;display:flex;gap:12px;align-items:center;flex-wrap:wrap}button,select{font:inherit;padding:7px 12px;border:1px solid #8b97a8;border-radius:7px;background:light-dark(white,#273246);color:inherit}button{cursor:pointer}button:disabled{opacity:.45}.books{display:grid;grid-template-columns:1fr 1fr;gap:20px}.book{min-width:0}.book img{display:block;max-width:100%;height:230px;object-fit:contain;margin:auto}.cover{background:light-dark(#eef1f6,#10151e);padding:12px;min-height:230px}.title{font-weight:650;overflow-wrap:anywhere}.muted{color:light-dark(#57657b,#acb8cc)}.bad{color:light-dark(#ac3b25,#ffb7a6)}.chip{display:inline-block;border:1px solid #8c9bb1;border-radius:12px;padding:1px 9px;margin:3px}.candidate{border-top:1px solid #8c9bb1;padding:10px 0}.candidate img{width:72px;height:96px;object-fit:contain;float:left;margin-right:12px}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px}details{margin:12px 0}summary{cursor:pointer}table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:8px;border-bottom:1px solid #8c9bb1}.mini{font-size:12px}@media(max-width:640px){body{padding:10px}.books{gap:10px}.book img{height:160px}.cover{min-height:160px}.toolbar{position:static}article{padding:12px}h1{font-size:21px}}@media(prefers-reduced-motion:no-preference){button{transition:background-color .15s ease}}
</style></head><body><h1>同作品推荐V4 · 离线复核</h1>
<div class="notice"><p><strong>420条均为已见开发标注，不是新留出。</strong>匹配分不是概率，所有建议均须人工确认，不修改真实资料。</p><p>主指标是已标配对；下方前向检索从查询Edition找单成员虚拟Work，按查询所属折的四折训练参数。未知整库候选不当负例或正确结果，真实多成员精度尚未验证。</p><p>版本标签是<strong>假设加入候选Work后</strong>的保守建议。真实导出未包含人工版本标签／人工属性归属，均标为未核对；匿名示例才验证人工覆盖与自定义保护。</p><div id="summary"></div></div>
<div class="toolbar"><label>复核范围 <select id="filter"><option value="all">全部420对</option><option value="errors">误报或漏报</option><option value="combined_false_positive">合计误报</option><option value="combined_false_negative">合计漏报</option><option value="strong_false_negative">强层漏报</option><option value="supplementary_candidate">补充候选</option><option value="excluded_from_primary">排除主指标</option></select></label><button id="previous">上一页</button><button id="next">下一页</button><span id="progress" role="status"></span></div>
<main id="rows"></main><details class="example"><summary>匿名规则示例（不是实际书籍）</summary><div id="examples"></div></details>
<script id="payload" type="application/json">__PAYLOAD__</script><script>
'use strict';
const D=JSON.parse(document.getElementById('payload').textContent);const R=D.report;const books=D.books;const queryMap=new Map(R.forward_retrieval.queries.map(q=>[q.query_edition_id,q]));let page=0;const pageSize=20;
const names={same_work:'同一作品不同版本',same_series:'同系列不同作品',unrelated:'两者都不是',uncertain:'无法确定',strong:'较强建议',supplementary:'补充候选',none:'未形成建议',unknown_unlabeled:'未标注／未知'};
function node(tag,text,cls){const n=document.createElement(tag);if(text!==undefined)n.textContent=String(text);if(cls)n.className=cls;return n;}
function details(title,value){const n=node('details');n.append(node('summary',title),node('pre',JSON.stringify(value,null,2)));return n;}
function pct(v){return v===null?'—':(v*100).toFixed(2)+'%';}
function makeBook(id){const b=books[id];const s=node('section',undefined,'book');const cover=node('div',undefined,'cover');if(b.image){const img=node('img');img.src=b.image;img.alt='本地封面';img.loading='lazy';cover.append(img);}else cover.append(node('p',b.image_status));s.append(cover,node('p',b.title,'title'),node('p',b.title_jpn,'muted'),node('p','页数 '+(b.pages??'未知')+' · 语言 '+(b.language??'未知')),node('p',b.custom_labels_status,'muted mini'),details('展开已有元数据标签',b.tags));return s;}
function showLabels(value){const box=node('div');box.append(node('p','假设归入同一Work后的版本标签建议','title'));for(const e of value.editions){const line=node('p','Edition '+e.edition_id+' · 语言 '+(e.languages.join('、')||'未知')+' · ');for(const t of e.suggested_presets)line.append(node('span',t.label,'chip'));if(!e.suggested_presets.length)line.append(node('span','无需额外区分标签','muted'));if(e.needs_manual_review)line.append(node('span',' 仍有无法区分的成员，请人工确认','bad'));box.append(line);}box.append(details('属性值、证据和不确定性',value));return box;}
function candidates(query){const wrapper=node('details');wrapper.append(node('summary','展开该查询最终总top20（未知关系不能当正确或误报）'));const q=queryMap.get(query);wrapper.append(node('p','完整候选数 '+q.candidate_counts.combined+' · 其中未标注 '+(q.candidate_counts.unlabeled_selected||0)+'；只保留总top20供查看，不是每路线top20并集。'));for(const c of q.final_top20){const b=books[c.supporting_edition_id];const item=node('section',undefined,'candidate');if(b.image){const img=node('img');img.src=b.image;img.loading='lazy';img.alt='候选本地封面';item.append(img);}item.append(node('p',b.title,'title'),node('p',(names[c.tier]||c.tier)+' · SSCD '+(c.matching_score===null?'缺失':c.matching_score.toFixed(4))+' · 人工关系 '+(names[c.annotated_relation]||c.annotated_relation)),node('p','Work '+c.work_id+'，支持Edition '+c.supporting_edition_id));item.append(details('依据与风险',c.member_evidence),showLabels(c.label_suggestions_if_joined));wrapper.append(item);}return wrapper;}
function render(){const filter=document.getElementById('filter').value;const rows=R.diagnostics.rows.filter(r=>filter==='all'||(filter==='errors'?r.flags.some(f=>f.endsWith('false_positive')||f.endsWith('false_negative')):r.flags.includes(filter)));const pages=Math.max(1,Math.ceil(rows.length/pageSize));page=Math.min(page,pages-1);const main=document.getElementById('rows');main.replaceChildren();for(const r of rows.slice(page*pageSize,(page+1)*pageSize)){const card=node('article');card.append(node('h2',r.pair_id),node('p','人工：'+names[r.label]+' · 算法：'+names[r.prediction.tier]),node('p','原始标注备注：'+(r.reason||'未填写')),node('p','单向排名：'+(r.forward_rank??'未进入候选')+' · '+r.flags.join('、'),'muted'));const pair=node('div',undefined,'books');r.book_ids.forEach(id=>pair.append(makeBook(id)));card.append(pair,details('配对判断及漏报／风险原因',{prediction:r.prediction,causes:r.causes,signal:r.signal}),showLabels(r.label_suggestions_if_joined),candidates(r.book_ids[0]));main.append(card);}document.getElementById('progress').textContent='第 '+(page+1)+'/'+pages+' 页 · '+rows.length+' 对';document.getElementById('previous').disabled=page===0;document.getElementById('next').disabled=page+1===pages;}
const table=node('table');const header=node('tr');['层级','TP / FP / FN','精确率','召回率'].forEach(x=>header.append(node('th',x)));table.append(header);for(const [name,m]of Object.entries(R.oof_pair_metrics)){const tr=node('tr');[name,m.tp+' / '+m.fp+' / '+m.fn,pct(m.precision),pct(m.recall)].forEach(x=>tr.append(node('td',x)));table.append(tr);}document.getElementById('summary').append(table,node('p','补充层单独质量与合计不同，请结合误报成本判断。95%和90%为点目标，不是置信下限保证。'));
for(const ex of R.explicit_multi_member_examples){const n=node('section');n.append(node('h2','完整人工三角 · 多成员示范（不计准确率）'),details('直接标注依据与多成员结果',ex.result),showLabels(ex.labels_if_joined));document.getElementById('examples').append(n);}
for(const ex of R.anonymous_label_examples){const n=node('section');n.append(node('h2',ex.name),showLabels(ex.suggestions));document.getElementById('examples').append(n);}
document.getElementById('filter').onchange=()=>{page=0;render();};document.getElementById('previous').onclick=()=>{page--;render();};document.getElementById('next').onclick=()=>{page++;render();};render();
</script></body></html>'''


def render(report, books, output, export=EXPORT, source=INPUT):
    display, summary = display_books(report, books, output, export, source)
    payload = safe_json({'report': report, 'books': display})
    page = PAGE.replace('__PAYLOAD__', payload)
    atomic_bytes(output / 'review.html', page.encode())
    return summary
