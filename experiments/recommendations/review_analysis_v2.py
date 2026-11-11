"""Render already-sealed reports only; no prediction, calibration or label editing."""
from pathlib import Path
import html
import json
from core import INPUT, atomic_bytes, file_digest, reject_symlinks
from review_v2 import display_data

CAPTIONS = {'same_work':'同一作品，不同版本','same_series':'同一系列，不同作品或卷篇','unrelated':'两者都不是','uncertain':'无法确定','defer':'待人工复核'}
REASONS = {
 'missing_title':'标题缺失','explicit_creator_conflict':'创作者标签存在明确冲突',
 'competing_work_and_series_evidence':'作品与系列证据冲突',
 'strong_title_with_auxiliary_support':'标题较强，其他信号辅助',
 'related_title_distinct_logical_entry_evidence':'标题相关且具有不同条目依据',
 'multiple_low_similarity_signals':'多路相似信号均较低',
 'insufficient_or_ambiguous_evidence':'证据不足或有歧义',
 'possible_partial_overlap':'页数等信号提示合集或局部重叠',
 'ordinal_kind_ambiguity':'卷／话等序号类型存在歧义',
 'compatible_high_image_semantic_auxiliary_route':'封面、语义、创作者与页数兼容的辅助路线',
 'high_image_and_semantic_with_compatible_creator_and_pages':'封面和语义较强，创作者与页数兼容',
 'possible_compilation_or_partial_overlap':'页数等信号提示合集或部分内容重叠',
}


def load_json(path):
    reject_symlinks(path)
    return json.loads(path.read_text())


def category(row):
    if row['label']=='uncertain' or row['collection_or_partial_overlap']: return 'excluded'
    if row['prediction']['action']=='defer': return 'defer'
    return 'wrong' if row['prediction']['action']!=row['label'] else 'correct'


def render_reports(reports, books, source, output, freeze_sha):
    e=lambda value: html.escape(str(value if value is not None else '未知'), quote=True)
    rows=[(stage,row) for stage,report in reports.items() for row in report['review_items']]
    pairs=[{'pair_id':stage+'-'+row['pair_id'],'book_ids':row['book_ids'],'batch':1} for stage,row in rows]
    display, images=display_data({'pack_id':freeze_sha,'pairs':pairs},books,source)
    cards=[]
    for stage,row in rows:
        label=CAPTIONS[row['label']];prediction=row['prediction'];kind=category(row)
        columns=[]
        for i in row['book_ids']:
            b=display['books'][str(i)]
            image='<img loading="lazy" alt="现有封面缩略图" src="'+e(b['image'])+'">' if b['image'] else '<p>'+e(b['image_status'])+'</p>'
            columns.append('<section><div class="cover">'+image+'</div><p class="title">'+e(b['title'])+'</p><p>'+e(b['title_jpn'])+'</p><p>页数 '+e(b['pages'])+' · 语言 '+e(b['language'])+'</p><details><summary>已有标签</summary><p>'+e(' · '.join(b['tags']))+'</p></details></section>')
        sig=row['signals'];scores={**sig['scores'],'清理后标题':sig['title'],'系列主体':sig['series_title'],'页数比':sig['pages']}
        fields=' · '.join(e(name)+'='+e(round(value,4) if isinstance(value,(int,float)) else value) for name,value in scores.items())
        reasons='；'.join(REASONS.get(r,r) for r in prediction['reasons'])
        flags='已标记合集／部分重叠' if row['collection_or_partial_overlap'] else '未标记合集／部分重叠'
        tag={'wrong':'预测与人工判断不同','defer':'算法保留为待复核','excluded':'排除主指标，仍完整呈现'}[kind]
        cards.append('<article data-stage="'+e(stage)+'" data-kind="'+kind+'"><h2>'+('测试' if stage=='heldout' else '校准')+' · '+e(row['pair_id'])+'</h2><p class="badge">'+tag+'</p><p><strong>人工：'+e(label)+'</strong>　算法：'+e(CAPTIONS[prediction['action']])+'</p><p>'+e(flags)+'；人工备注：'+e(row['reason'] or '未填写')+'</p><div class="books">'+''.join(columns)+'</div><p><strong>算法依据：</strong>'+e(reasons)+'</p><p class="signals">'+fields+'</p><details><summary>详细冲突与决策信号</summary><pre>'+e(json.dumps({'decision_scores':prediction['scores'],'creator':sig['creator'],'explicit_difference':sig['explicit_difference'],'ordinal_ambiguity':sig['ordinal_ambiguity'],'possible_partial_overlap':sig.get('possible_partial_overlap'),'ordinal_kind_ambiguity':sig.get('ordinal_kind_ambiguity')},ensure_ascii=False,indent=2))+'</pre></details></article>')
    page='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'none'; base-uri 'none'; form-action 'none'">
<title>推荐v2 · 只读结果复核</title><style>body{font:15px/1.7 system-ui;background:#f3f5f9;color:#192438;max-width:1120px;margin:auto;padding:20px}article,.toolbar{padding:18px;background:white;border:1px solid #d6dfeb;border-radius:10px;margin:18px 0}.toolbar{position:sticky;top:6px;z-index:2;display:flex;gap:16px;flex-wrap:wrap}select{font:inherit;padding:5px}.books{display:grid;grid-template-columns:1fr 1fr;gap:22px}.books section{min-width:0}.cover{height:240px;background:#f3f5f9;display:flex;justify-content:center}img{max-height:100%;max-width:100%;object-fit:contain}p,pre{overflow-wrap:anywhere;white-space:pre-wrap}.title{font-weight:600}.badge{color:#9c4310}.signals{font-size:13px;color:#58647a}h2{font-size:17px}[hidden]{display:none!important}@media(max-width:600px){body{padding:9px}.books{gap:12px}.cover{height:180px}.toolbar{position:static}}</style>
<h1>推荐v2 · 只读结果复核</h1><p>只展示误报、待复核和排除主指标案例。这里的分数是匹配信号，不是概率；待复核不等于人工标错。</p><p>模型在校准后冻结，测试只评估一次。原始人工标签保留，不在本页修改或导出子集。若需修订标签，请在<a href="../review.html">原完整300对候选册</a>导入完整JSON后另存新版本；本次测试结果仍是历史冻结结果。</p>
<div class="toolbar"><label>来源 <select id="stage"><option value="heldout">测试集</option><option value="calibration">校准集</option><option value="all">全部</option></select></label><label>类型 <select id="kind"><option value="all">全部复核项</option><option value="wrong">预测与人工不同</option><option value="defer">待人工复核</option><option value="excluded">排除主指标</option></select></label><span id="count" role="status"></span></div>__CARDS__
<script>function filter(){let count=0;for(const card of document.querySelectorAll('article')){const show=(document.getElementById('stage').value==='all'||card.dataset.stage===document.getElementById('stage').value)&&(document.getElementById('kind').value==='all'||card.dataset.kind===document.getElementById('kind').value);card.hidden=!show;count+=show;}document.getElementById('count').textContent='显示 '+count+' 对';}document.getElementById('stage').onchange=filter;document.getElementById('kind').onchange=filter;filter();</script></html>'''
    atomic_bytes(output,page.replace('__CARDS__',''.join(cards)).encode())
    return {'review_pairs':len(rows),'by_stage':{stage:len(report['review_items']) for stage,report in reports.items()},**images}


def main():
    root=Path(__file__).resolve().parents[2]/'.runtime/tests/runs/recommendations/run-v2'
    directory=root/'analysis';frozen=load_json(directory/'frozen-policy.json');sha=file_digest(directory/'frozen-policy.json')
    calibration=load_json(directory/'calibration-report.json');heldout=load_json(directory/'heldout-report.json')
    if heldout['frozen_policy_sha256']!=sha or frozen['calibration_report_sha256']!=file_digest(directory/'calibration-report.json'):
        raise ValueError('review_reports_do_not_match_approved_policy')
    exported=load_json(root/'books.json');books={b['id']:b for b in exported['books']}
    summary=render_reports({'calibration':calibration,'heldout':heldout},books,INPUT,directory/'review-results.html',sha)
    print(json.dumps(summary))


if __name__=='__main__': main()
