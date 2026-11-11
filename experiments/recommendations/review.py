"""Self-contained offline review booklet with explicit JSON import/export."""
import base64
import json
import html
from core import cover_file, atomic_bytes


def render(pack, books, source, output):
    display = {}
    errors = 0
    for i in {i for p in pack["pairs"] for i in p["book_ids"]}:
        b = books[i]
        image = None
        try:
            path = cover_file(source, b["cover_path"])
            if path:
                image = "data:image/jpeg;base64," + base64.b64encode(path.read_bytes()).decode()
        except (OSError, ValueError):
            errors += 1
        display[str(i)] = {"title": b["title"], "title_jpn": b["title_jpn"], "image": image,
                           "pages": b["pages"], "language": b["language_code"],
                           "tags": [ns + ":" + name for ns, name in b["tags"]]}
    data = json.dumps({"pack_id": pack["pack_id"], "pairs": pack["pairs"], "books": display}, ensure_ascii=False).replace("<", "\\u003c")
    template = '''<!doctype html><html lang="zh-CN"><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'none'; base-uri 'none'; form-action 'none'">
<title>推荐实验 · 私有离线候选册</title><style>
body{font:16px/1.6 system-ui;margin:0;background:#f3f4f6;color:#172033}header{padding:24px;background:#172033;color:white}main{max-width:1100px;margin:auto;padding:20px}.toolbar{position:sticky;top:0;background:white;padding:12px;z-index:2;border:1px solid #ddd}button,select,input,textarea{font:inherit;padding:7px}button{cursor:pointer}.pair{background:white;border:1px solid #ccd2da;border-radius:10px;margin:24px 0;padding:20px}.books{display:grid;grid-template-columns:1fr 1fr;gap:24px}img{max-height:300px;max-width:100%;object-fit:contain}h2{font-size:18px}.title{white-space:pre-wrap;overflow-wrap:anywhere}details{font-size:13px}textarea{width:95%;height:70px}label{margin-right:18px}.answered{border-left:5px solid #20805a}.note{font-size:14px}#status{margin:0 12px}@media(max-width:600px){.books{grid-template-columns:1fr}header{padding:16px}main{padding:8px}}
</style><header><h1>版本 / 系列推荐 · 人工核对</h1><p>共80对。现有组织仅是选样线索，不是真值。匹配分不是概率。未标注时不得报告真实准确率。</p></header><main>
<p>版本：同一作品的语言、修订、彩色等不同版本；不同卷/话通常选 different。系列：同一连续作品的不同逻辑条目；仅同作者、同角色或同题材不足以选 same。证据不足请选 uncertain 并写明理由。</p>
<p class="note">本页不联网，不自动保存到磁盘。浏览器本地草稿仅为便利功能；请定期导出JSON。导出的文件包含私人标注，不要提交Git。重新打开可导入JSON继续。封面是现有缩略图，无法证明完整内容相同。</p>
<div class="toolbar"><select id="filter"><option value="all">全部</option><option value="version">版本40对</option><option value="series">系列40对</option><option value="pending">未标注</option></select><button id="export">导出标注JSON</button><input type="file" id="import" accept="application/json"><span id="status"></span></div><div id="pairs"></div></main>
<script>const D=__DATA__;const key='recommendation-review-'+D.pack_id;let state={};function validState(s){if(!s||typeof s!=='object'||Array.isArray(s))return false;let valid=new Set(D.pairs.map(p=>p.pair_id));return Object.entries(s).every(([id,x])=>valid.has(id)&&x&&typeof x==='object'&&[null,'same','different','uncertain'].includes(x.label)&&typeof x.reason==='string'&&x.reason.length<=4000)}try{let saved=JSON.parse(localStorage.getItem(key)||'{}');if(validState(saved))state=saved}catch(e){};
function el(t,text){let n=document.createElement(t);if(text!==undefined)n.textContent=text;return n}
function persist(){try{localStorage.setItem(key,JSON.stringify(state))}catch(e){}document.getElementById('status').textContent=Object.values(state).filter(x=>x.label).length+'/80 已标注'}
function draw(){let root=document.getElementById('pairs');root.replaceChildren();let f=document.getElementById('filter').value;for(const p of D.pairs){let s=state[p.pair_id]||{};if(f==='pending'&&s.label)continue;if(['version','series'].includes(f)&&p.task!==f)continue;let card=el('article');card.className='pair'+(s.label?' answered':'');card.append(el('h2',(p.task==='version'?'版本':'系列')+' · '+p.pair_id));let grid=el('div');grid.className='books';for(let id of p.book_ids){let b=D.books[id],col=el('div');if(b.image){let img=el('img');img.src=b.image;img.alt='书籍封面';col.append(img)}else col.append(el('p','无封面'));let title=el('p',b.title||'（无标题）');title.className='title';col.append(title);if(b.title_jpn)col.append(el('p',b.title_jpn));col.append(el('p','页数 '+(b.pages??'未知')+' · 语言 '+(b.language||'未知')));let details=el('details');details.append(el('summary','查看现有标签'));details.append(el('p',b.tags.join(' · ')));col.append(details);grid.append(col)}card.append(grid);let choices=el('div');for(let [v,text] of [['same','same · 相同作品/系列'],['different','different · 不同'],['uncertain','uncertain · 无法确定']]){let label=el('label'),input=el('input');input.type='radio';input.name=p.pair_id;input.value=v;input.checked=s.label===v;input.onchange=()=>{state[p.pair_id]={label:v,reason:reason.value};card.classList.add('answered');persist()};label.append(input,document.createTextNode(text));choices.append(label)}card.append(choices);let reason=el('textarea');reason.placeholder='判断理由、证据不足或怀疑漏掉的同族关系';reason.value=s.reason||'';reason.oninput=()=>{state[p.pair_id]={label:(state[p.pair_id]||{}).label||null,reason:reason.value};persist()};card.append(reason);root.append(card)}persist()}
document.getElementById('filter').onchange=draw;document.getElementById('export').onclick=()=>{let rows=D.pairs.map(p=>({pair_id:p.pair_id,label:(state[p.pair_id]||{}).label||null,reason:(state[p.pair_id]||{}).reason||''}));let out={schema:'recommendation-labels-v1',pack_id:D.pack_id,annotations:rows};let url=URL.createObjectURL(new Blob([JSON.stringify(out,null,2)],{type:'application/json'}));let a=el('a');a.href=url;a.download='recommendation-labels.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)};
document.getElementById('import').onchange=async(e)=>{try{let out=JSON.parse(await e.target.files[0].text());if(!out||typeof out!=='object'||out.schema!=='recommendation-labels-v1'||out.pack_id!==D.pack_id||!Array.isArray(out.annotations))throw Error('候选册不匹配');let valid=new Set(D.pairs.map(p=>p.pair_id)),next={},seen=new Set();for(let a of out.annotations){if(!a||typeof a!=='object'||!valid.has(a.pair_id)||seen.has(a.pair_id)||![null,'same','different','uncertain'].includes(a.label)||typeof a.reason!=='string'||a.reason.length>4000)throw Error('标注格式无效');seen.add(a.pair_id);next[a.pair_id]={label:a.label,reason:a.reason}}state=next;persist();draw()}catch(err){alert('导入失败：'+err.message)}};draw();</script></html>'''
    template = template.replace("reason.placeholder=", "reason.maxLength=4000;reason.placeholder=")
    count = len(pack["pairs"])
    template = template.replace("共80对", "共" + str(count) + "对").replace("+'/80 已标注'", "+'/' + D.pairs.length + ' 已标注'")
    for task, label in (("version", "版本"), ("series", "系列")):
        template = template.replace(label + "40对", label + str(sum(p["task"] == task for p in pack["pairs"])) + "对")
    atomic_bytes(output, template.replace("__DATA__", data).encode())
    return {"image_mapping_errors": errors, "displayed_books": len(display)}


def render_diagnostics(pack, books, source, annotations, scored_rows, diagnostics, output):
    """Read-only subset: corrections belong to the original full annotation pack."""
    e = lambda value: html.escape(str(value if value is not None else "未知"))
    scores = {r["pair_id"]: r for r in scored_rows}
    kinds = {
        "human_same_outside_route_top5": "人工标为same，该路线未排入top5",
        "human_same_at_or_below_observed_different_max": "人工标为same，分数落入本次different分数范围",
        "human_different_at_or_above_observed_same_min": "人工标为different，分数与本次same范围重叠",
        "explicit_ordinal_guard_conflicts_with_human_same": "显式卷/话保护与人工same判断存在冲突，需检查解析依据",
    }
    grouped = {}
    for row in diagnostics:
        grouped.setdefault(row["pair_id"], []).append(row)
    cards = []
    for pair in pack["pairs"]:
        pid = pair["pair_id"]
        if pid not in grouped:
            continue
        columns = []
        for i in pair["book_ids"]:
            b = books[i];image = ""
            try:
                path = cover_file(source, b["cover_path"])
                if path:
                    image = '<img alt="现有封面" src="data:image/jpeg;base64,' + base64.b64encode(path.read_bytes()).decode() + '">'
            except (OSError, ValueError):
                image = "<p>封面不可用</p>"
            columns.append('<div>' + image + '<p>' + e(b['title']) + '</p><p>' + e(b['title_jpn']) + '</p></div>')
        score = scores[pid]
        values = {"规则": score['rules'][pair['task'] + '_score'], **score['scores']}
        reasons = ''.join('<li>' + e(r['algorithm']) + '：' + e(kinds[r['kind']]) + (('；匹配分 ' + e(round(r['score'], 5))) if 'score' in r else '') + '</li>' for r in grouped[pid])
        cards.append('<article><h2>' + ('版本' if pair['task']=='version' else '系列') + ' · ' + e(pid) + '</h2><p>当前人工标注：<strong>' + e(annotations[pid]['label']) + '</strong>；理由：' + e(annotations[pid]['reason'] or '未填写') + '</p><div class="books">' + ''.join(columns) + '</div><p>匹配信号（不是概率）：' + e(' · '.join(k + '=' + str(round(v, 5)) for k, v in values.items() if v is not None)) + '</p><ul>' + reasons + '</ul><p>规则依据：' + e(' / '.join(score['rules']['reasons']) or '没有明确规则依据') + '</p></article>')
    page = '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; img-src data:; style-src \'unsafe-inline\'; base-uri \'none\'; form-action \'none\'"><title>私有只读复核册</title><style>body{font:16px/1.6 system-ui;background:#f3f4f6;color:#172033;max-width:1100px;margin:auto;padding:20px}article{padding:20px;background:white;border:1px solid #ccd2da;border-radius:10px;margin:24px 0}.books{display:grid;grid-template-columns:1fr 1fr;gap:24px}img{max-width:100%;max-height:260px}p{overflow-wrap:anywhere}h2{font-size:18px}@media(max-width:600px){.books{grid-template-columns:1fr}}</style><h1>只读复核册 · ' + str(len(cards)) + '对</h1><p>这些是模型排名遗漏、低分或规则冲突，不是已确认误判，也不要求把人工标签改成模型意见。所有分数仅描述当前pilot。</p><p>本页不导出子集标注，避免丢失原80条。若确需修正，请返回<a href="review.html">原完整候选册</a>，导入原labels.user.json，修正后再次导出完整JSON。本页不会覆盖原册或原标注。</p>' + ''.join(cards) + '</html>'
    page = page.replace('所有分数仅描述当前pilot。', '所有分数仅描述当前pilot。困难候选按规则分优先采样，对规则有利；本轮排名覆盖不能证明规则优于图像模型，也没有全面覆盖图像相近而标题不同的例子。')
    atomic_bytes(output, page.encode())
