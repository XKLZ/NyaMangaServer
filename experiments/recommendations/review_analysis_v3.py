"""Read-only review of sealed V3 reports; never predict or change annotations."""
import html
import json
import collections
from pathlib import Path
from core import INPUT, RUNTIME, atomic_bytes, atomic_json, file_digest
from sampling_v2 import read_json
from review_v2 import display_data

CAPTIONS = {'same_work': '同一作品，不同版本', 'same_series': '同一系列，不同作品或卷篇',
            'unrelated': '两者都不是', 'uncertain': '无法确定', 'defer': '待人工复核'}
REASONS = {
    'ordinal_conflict': '卷篇序号不同', 'ordinal_kind_conflict': '序号类型冲突',
    'creator_conflict': '创作者存在明确冲突', 'possible_partial_overlap': '可能是合集或部分内容重叠',
    'missing_cover': '封面特征缺失', 'missing_title': '标题缺失',
    'competing_evidence': '作品与系列证据竞争', 'insufficient_evidence': '证据不足',
    'explicit_creator_conflict': '创作者存在明确冲突',
    'ordinal_type_ambiguity': '卷／话等序号类型不可直接比较',
    'compilation_vs_single_work': '合集与单篇特征不一致',
    'large_page_count_difference': '页数差距较大',
    'missing_creator_metadata': '创作者资料缺失', 'missing_cover_feature': '封面特征缺失',
    'missing_page_count': '页数缺失', 'different_explicit_volume_or_chapter': '明确卷篇序号不同',
    'different_ambiguous_position': '疑似序号不同，仍有歧义',
    'ordinal_missing_one_side': '仅一方有明确卷篇序号',
    'competing_work_and_series_evidence': '同作品与同系列证据竞争',
    'sscd_cover_primary_compatible_positions': '封面相似度较强，卷篇位置无冲突',
    'related_structural_semantic_title_distinct_entry_shared_creator': '题名关系较强，有不同条目依据且创作者相交',
    'insufficient_or_conflicting_evidence': '证据不足或存在冲突，保留人工判断',
}


def classify(row):
    if row['label'] == 'uncertain' or row['collection_or_partial_overlap']:
        return 'excluded'
    prediction = row['prediction']
    action = prediction['action']
    if action not in CAPTIONS or action == 'uncertain':
        raise ValueError('invalid_report_prediction_action')
    if prediction['tier'] not in ('strong', 'review'):
        raise ValueError('invalid_report_prediction_tier')
    if action == 'defer' or prediction['tier'] == 'review':
        return 'review'
    return 'wrong' if action != row['label'] else 'correct'


def render_reports(reports, books, source, output, freeze_sha):
    selected = [(stage, row, classify(row)) for stage, report in reports.items() for row in report['predictions']]
    # Keep all errors, review candidates and excluded labels, without filtering on score.
    selected = [(stage, row, kind) for stage, row, kind in selected if kind != 'correct']
    pairs = [{'pair_id': stage + '-' + row['pair_id'], 'book_ids': row['book_ids'], 'batch': 1}
             for stage, row, _ in selected]
    display, images = display_data({'pack_id': freeze_sha, 'pairs': pairs}, books, source)
    e = lambda value: html.escape(str(value if value is not None else '未知'), quote=True)
    cards = []
    for stage, row, kind in selected:
        prediction = row['prediction'];columns = []
        for i in row['book_ids']:
            book = display['books'][str(i)]
            image = '<img loading="lazy" alt="本地封面缩略图" src="' + e(book['image']) + '">' if book['image'] else '<p>' + e(book['image_status']) + '</p>'
            columns.append('<section><div class="cover">' + image + '</div><p class="title">' + e(book['title']) + '</p><p>' + e(book['title_jpn']) + '</p><p>页数 ' + e(book['pages']) + ' · 语言 ' + e(book['language']) + '</p><details><summary>已有标签</summary><p>' + e(' · '.join(book['tags'])) + '</p></details></section>')
        explanations = {k: prediction.get(k, []) for k in ('reasons', 'risks')}
        explanation_text = '；'.join(REASONS.get(value, value) for values in explanations.values() for value in values)
        details = {k: prediction.get(k) for k in ('action', 'relation', 'tier', 'scores', 'reasons', 'risks', 'algorithm_version')}
        candidate = '未形成明确关系建议' if prediction['tier'] == 'review' and prediction['relation'] == 'unrelated' else CAPTIONS[prediction['relation']]
        cards.append('<article data-stage="' + e(stage) + '" data-kind="' + e(kind) + '"><h2>' + ('新留出' if stage == 'heldout' else '开发回归') + ' · ' + e(row['pair_id']) + '</h2><p><strong>人工：' + e(CAPTIONS[row['label']]) + '</strong>　结果：' + e(CAPTIONS[prediction['action']]) + ' · ' + ('较强建议' if prediction['tier'] == 'strong' else '待复核候选') + '</p><p>候选关系：' + e(candidate) + '</p><p>' + ('已标记合集／部分重叠' if row['collection_or_partial_overlap'] else '未标记合集／部分重叠') + '；人工备注：' + e(row['reason'] or '未填写') + '</p><div class="books">' + ''.join(columns) + '</div><p><strong>依据与风险：</strong>' + e(explanation_text or '见详细记录') + '</p><details><summary>详细匹配信号（不是概率）</summary><pre>' + e(json.dumps(details, ensure_ascii=False, indent=2)) + '</pre></details></article>')
    page = '''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'none'; base-uri 'none'; form-action 'none'">
<title>推荐V3 · 冻结结果复核</title><style>body{font:15px/1.7 system-ui;background:#f3f5f9;color:#192438;max-width:1120px;margin:auto;padding:20px}article,.toolbar{padding:18px;background:white;border:1px solid #d6dfeb;border-radius:10px;margin:18px 0}.toolbar{position:sticky;top:6px;z-index:2;display:flex;gap:16px;flex-wrap:wrap}select{font:inherit;padding:5px}.books{display:grid;grid-template-columns:1fr 1fr;gap:22px}.books section{min-width:0}.cover{height:240px;background:#f3f5f9;display:flex;justify-content:center}img{max-height:100%;max-width:100%;object-fit:contain}p,pre{overflow-wrap:anywhere;white-space:pre-wrap}.title{font-weight:600}h2{font-size:17px}[hidden]{display:none!important}@media(max-width:600px){body{padding:9px}.books{gap:12px}.cover{height:180px}.toolbar{position:static}}</style>
<h1>推荐V3 · 只读结果复核</h1><p>完整展示误报、待复核及排除主指标的案例。待复核不等于人工标错；匹配分不是概率。较强建议也须人工确认，不会自动修改组织关系。</p><p>原300对已经全部用于开发。新120对在冻结审批后只评估一次，结果不用于返调。此页不修改或导出人工标注，原件保留；如需修订，请另存版本并保留本次结果。</p>
<div class="toolbar"><label>数据 <select id="stage"><option value="heldout">新留出</option><option value="development">开发回归</option><option value="all">全部</option></select></label><label>类型 <select id="kind"><option value="all">全部复核项</option><option value="wrong">较强建议与人工不同</option><option value="review">待复核候选</option><option value="excluded">排除主指标</option></select></label><span id="count" role="status"></span></div>__CARDS__
<script>function filter(){let count=0;for(const card of document.querySelectorAll('article')){const show=(document.getElementById('stage').value==='all'||card.dataset.stage===document.getElementById('stage').value)&&(document.getElementById('kind').value==='all'||card.dataset.kind===document.getElementById('kind').value);card.hidden=!show;count+=show;}document.getElementById('count').textContent='显示 '+count+' 对';}document.getElementById('stage').onchange=filter;document.getElementById('kind').onchange=filter;filter();</script></html>'''
    atomic_bytes(output, page.replace('__CARDS__', ''.join(cards)).encode())
    return {'review_pairs': len(selected), 'by_stage': dict(collections.Counter(stage for stage, _, _ in selected)),
            'by_category': dict(collections.Counter(kind for _, _, kind in selected)), **images}


def main():
    root = RUNTIME / 'run-v3';directory = root / 'analysis'
    freeze_sha = file_digest(directory / 'frozen-policy-v3.json')
    approval = read_json(directory / 'approval.json')
    if approval != {'frozen_policy_sha256': freeze_sha, 'approved': True}:
        raise ValueError('review_requires_matching_root_approval')
    reports = {stage: read_json(directory / name) for stage, name in
               (('development', 'development-report.json'), ('heldout', 'heldout-report.json'))}
    frozen = read_json(directory / 'frozen-policy-v3.json')
    if frozen['development_report_sha256'] != file_digest(directory / 'development-report.json'):
        raise ValueError('development_report_freeze_mismatch')
    if reports['heldout']['frozen_policy_sha256'] != freeze_sha:
        raise ValueError('heldout_report_freeze_mismatch')
    books = {b['id']: b for b in read_json(RUNTIME / 'run-v2/books.json')['books']}
    summary = render_reports(reports, books, INPUT, directory / 'review-results.html', freeze_sha)
    summary['report_sha256'] = {stage: file_digest(directory / name) for stage, name in
                              (('development', 'development-report.json'), ('heldout', 'heldout-report.json'))}
    atomic_json(directory / 'review-summary.json', summary)
    print(json.dumps(summary))


if __name__ == '__main__': main()
