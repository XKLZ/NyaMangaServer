'use strict';
const D=JSON.parse(document.getElementById('payload').textContent), R=D.report;
let page=0;
const pageSize=8;
const names={strong:'较强候选',supplementary:'补充候选',none:'未形成候选',same_series:'同系列',same_work:'同作品',unrelated:'无关',uncertain:'无法确定'};
const riskNames={explicit_creator_conflict:'创作者资料存在明确差异',different_title_subject:'题名主体支持偏弱',ordinal_type_ambiguity:'卷、话或上下篇类型不同',same_position_needs_work_or_part_review:'序号相同，请核对是否同作品或不同篇章',creator_support_missing:'创作者支持缺失',semantic_feature_missing:'语义特征缺失',member_relationship_needs_review:'与部分系列成员关系需核对',already_in_other_series:'已经属于其他系列，不能直接转移'};
function el(tag,text,cls){const x=document.createElement(tag);if(text!==undefined)x.textContent=String(text);if(cls)x.className=cls;return x;}
function pct(v){return v===null?'—':(v*100).toFixed(2)+'%';}
function work(wid){const w=D.works[wid], box=el('section',undefined,'work');if(!w){box.append(el('p','资料不可用'));return box;}
const b=D.books[w.edition_ids[0]];if(b.image){const img=el('img');img.src=b.image;img.alt='本地封面';img.loading='lazy';box.append(img);}else box.append(el('p',b.image_status,'muted'));
box.append(el('h3',b.title_original||b.title),el('p',b.title,'muted'),el('p',w.edition_ids.length+'个版本 · '+(b.language||'语言未知'),'muted'));
const more=el('details');more.append(el('summary','版本与创作者资料'));for(const id of w.edition_ids){const e=D.books[id];more.append(el('p',e.title_original||e.title),el('p','页数 '+(e.pages??'未知')+' · '+(e.language||'语言未知'),'muted'));for(const [ns,n] of e.tags.filter(t=>t[0]==='artist'||t[0]==='group'))more.append(el('span',ns+': '+n,'chip'));}box.append(more);return box;}
function members(ids){const box=el('div',undefined,'members');for(const id of ids.slice(0,12))box.append(work(id));if(ids.length>12){const more=el('details');more.append(el('summary','展开其余'+(ids.length-12)+'个作品'));const rest=el('div',undefined,'members');for(const id of ids.slice(12))rest.append(work(id));more.append(rest);box.append(more);}return box;}
function riskText(risks){return (risks||[]).map(r=>riskNames[r]||'其他关系差异，请核对').join('；');}
function planCard(p){const card=el('article');card.append(el('h2',(p.kind==='create_series'?'组成系列：':'加入系列：')+p.suggested_name),el('span',names[p.tier]||p.tier,'chip '+p.tier));
if(p.blocked)card.append(el('p','存在其他系列归属，本方案不可直接采纳。','risk'));
if(p.risks?.length)card.append(el('p',riskText(p.risks),'risk'));
if(p.known_conflict_work_pairs?.length)card.append(el('p','其中'+p.known_conflict_work_pairs.length+'对与已有人工标注冲突，请勿直接整理。','risk'));
if(p.unlabeled_work_pairs)card.append(el('p','其中'+p.unlabeled_work_pairs+'对尚无人工关系标注，不能据此声称整组正确。','muted'));
if(p.existing_work_ids){card.append(el('h3','系列已有作品'),members(p.existing_work_ids),el('h3','建议加入'));}card.append(members(p.work_ids));
if(p.order.suggested_order){const order=el('details');order.append(el('summary','按明确序号建议的完整顺序'));const list=el('ol');for(const wid of p.order.suggested_order){const b=D.books[D.works[wid].edition_ids[0]];list.append(el('li',b.title_original||b.title));}order.append(list);card.append(order);}else card.append(el('p','顺序需要人工确认；已有系列相对顺序保留。','muted'));return card;}
function rowsFor(mode){if(mode==='create')return R.proposals;if(mode==='join')return R.virtual_series_scenarios;if(mode==='queries')return R.forward_ranking.queries;return R.diagnostics.filter(r=>r.flags.some(f=>f==='false_positive'||f==='candidate_not_retrieved'||f==='score_or_gate')&&!r.flags.includes('excluded_from_primary'));}
function render(){const mode=document.getElementById('mode').value,rows=rowsFor(mode),pages=Math.max(1,Math.ceil(rows.length/pageSize));page=Math.min(page,pages-1);const content=document.getElementById('content');content.replaceChildren();
for(const r of rows.slice(page*pageSize,(page+1)*pageSize)){
if(mode==='create')content.append(planCard(r));
else if(mode==='join'){const note=el('div',undefined,'panel');note.append(el('p','明确人工关系构造的虚拟已有系列，仅验证加入逻辑，不代表实际业务组织。','muted'));if(r.proposal)note.append(planCard(r.proposal));else note.append(el('p','此虚拟场景未形成加入方案。'),members(r.series.work_ids),work(r.query_work_id));content.append(note);}
else if(mode==='queries'){const card=el('article');card.append(el('h2','查询作品'),work(r.query_work_id),el('p','召回候选 '+r.candidate_count+' 个，达标候选 '+r.selected_count+' 个；下方最多展示最终总前20个，不是三路各20个。','muted'));for(const p of r.final_top20){const candidate=el('details');candidate.append(el('summary',(names[p.tier]||p.tier)+' · 展开候选作品'),work(p.work_id),el('p',riskText(p.risks),'risk'));card.append(candidate);}content.append(card);}
else{const card=el('article');card.append(el('h2',r.label==='same_series'?'已知系列关系未推荐':'候选与已有标注不同'),el('p','已有标注：'+names[r.label]+'；算法：'+names[r.tier]),members([...new Set(r.work_ids)]));const reasons=[];if(r.flags.includes('candidate_not_retrieved'))reasons.push('目标未进入三路召回');if(r.flags.includes('score_or_gate'))reasons.push('分数或不同条目条件不足');if(r.signal.risks)reasons.push(riskText(r.signal.risks));card.append(el('p',reasons.filter(Boolean).join('；'),'muted'));if(r.reason)card.append(el('p','原标注备注：'+r.reason,'muted'));content.append(card);}}
if(!rows.length)content.append(el('p','此范围暂无方案；不会用假数据填充。','panel empty'));
document.getElementById('progress').textContent='第 '+(page+1)+' / '+pages+' 页 · '+rows.length+' 项';document.getElementById('previous').disabled=page===0;document.getElementById('next').disabled=page+1>=pages;}
const table=el('table'),header=el('tr');for(const name of ['五折开发验证','找对 / 误报 / 漏报','精确率','召回率'])header.append(el('th',name));table.append(header);
for(const [name,m]of [['固定V2',R.baselines.v2],['固定V3',R.baselines.v3],['较强候选',R.oof.strong],['补充单独',R.oof.supplementary],['全部候选',R.oof.combined]]){const tr=el('tr');for(const value of [name,m.tp+' / '+m.fp+' / '+m.fn,pct(m.precision),pct(m.recall)])tr.append(el('td',value));table.append(tr);}document.getElementById('summary').append(table,el('p','补充层单独质量与合计不同；点精确率不是置信下限保证。','muted'));
document.getElementById('mode').onchange=()=>{page=0;render();};document.getElementById('previous').onclick=()=>{page--;render();};document.getElementById('next').onclick=()=>{page++;render();};render();
