/* The same complete-document validator is exercised in anonymous Node tests. */
const LABEL_SCHEMA_V2 = 'recommendation-labels-v2';
const RELATIONS_V2 = [null, 'same_work', 'same_series', 'unrelated', 'uncertain'];
function blankAnnotation(pair_id) { return {pair_id, label:null, reason:'', collection_or_partial_overlap:false}; }
function validateDocument(data, value) {
  if (!value || typeof value !== 'object' || value.schema !== LABEL_SCHEMA_V2 || value.pack_id !== data.pack_id || !Array.isArray(value.annotations) || value.annotations.length !== data.pairs.length) throw Error('文件版本、候选包或完整条目数不匹配');
  const valid = new Set(data.pairs.map(p=>p.pair_id)), seen = new Set();
  for (const row of value.annotations) {
    if (!row || typeof row !== 'object' || !valid.has(row.pair_id) || seen.has(row.pair_id) || !RELATIONS_V2.includes(row.label) || typeof row.reason !== 'string' || [...row.reason].length > 4000 || typeof row.collection_or_partial_overlap !== 'boolean') throw Error('存在未知／重复题目或无效标注');
    seen.add(row.pair_id);
  }
  return value;
}
function completeDocument(data, state) {
  return validateDocument(data, {schema:LABEL_SCHEMA_V2, pack_id:data.pack_id, annotations:data.pairs.map(p=>({...blankAnnotation(p.pair_id), ...(state[p.pair_id]||{}), pair_id:p.pair_id}))});
}
function batchPairs(data, batch, pending, state) { return data.pairs.filter(p=>p.batch===batch && (!pending || !(state[p.pair_id]||{}).label)); }
if (typeof module !== 'undefined') module.exports = {validateDocument, completeDocument, blankAnnotation, batchPairs};
if (typeof document !== 'undefined') {
  const $=id=>document.getElementById(id), key='recommendation-relations-v2-'+D.pack_id;
  let state={}, currentBatch=1, currentId=null, dirty=false, storageOK=true;
  function notice(text) { $('message').textContent=text; }
  try { const saved=localStorage.getItem(key); if(saved) { const doc=validateDocument(D,JSON.parse(saved)); state=Object.fromEntries(doc.annotations.map(a=>[a.pair_id,a])); } } catch(e) { notice('未能读取浏览器草稿，请导入已保存的JSON。'); }
  function el(tag,text) { const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n; }
  function row(id) { return state[id]||blankAnnotation(id); }
  function progress() {
    const all=D.pairs.filter(p=>row(p.pair_id).label).length, local=D.pairs.filter(p=>p.batch===currentBatch&&row(p.pair_id).label).length;
    $('progress').textContent=`全部 ${all}/${D.pairs.length} · 本批 ${local}/30`;
  }
  function persist() {
    dirty=true;
    try { localStorage.setItem(key,JSON.stringify(completeDocument(D,state))); } catch(e) { storageOK=false; }
    notice(storageOK?'浏览器草稿已更新，尚未导出。完成一批后请导出完整JSON。':'浏览器无法保存草稿，请立即导出JSON以免丢失。');progress();
  }
  const batchCount=Math.ceil(D.pairs.length/30);
  for(let n=1;n<=batchCount;n++){let option=el('option',`第${n}批（${(n-1)*30+1}–${Math.min(n*30,D.pairs.length)}）`);option.value=n;$('batch').append(option);}
  function draw() {
    const visible=batchPairs(D,currentBatch,$('pending').checked,state), card=$('card');card.replaceChildren();
    let index=visible.findIndex(p=>p.pair_id===currentId);if(index<0)index=0;
    if(!visible.length){currentId=null;card.append(el('h2','本批没有待标注题目'));card.append(el('p','可以切换批次，或取消“只看本批未完成”查看已完成标注。'));progress();return;}
    const p=visible[index];currentId=p.pair_id;const a=row(currentId), globalIndex=D.pairs.findIndex(x=>x.pair_id===currentId)+1;
    const heading=el('h2',`第${globalIndex}对 · 第${currentBatch}批`);heading.id='pairTitle';heading.tabIndex=-1;card.append(heading);
    const grid=el('div');grid.className='books';
    for(const id of p.book_ids){const b=D.books[id], col=el('section');col.className='book';const cover=el('div');cover.className='cover';if(b.image){const image=el('img');image.src=b.image;image.alt='现有漫画封面';cover.append(image);}else cover.append(el('p',b.image_status));col.append(cover);let title=el('p',b.title||'（无标题）');title.className='title';col.append(title);if(b.title_jpn){const original=el('p',b.title_jpn);original.className='original';col.append(original);}const meta=el('p',`页数：${b.pages??'未知'} · 语言：${b.language||'未知'}`);meta.className='meta';col.append(meta);const details=el('details');details.append(el('summary','查看已有标签'));const tags=el('p',b.tags.join(' · ')||'无标签');tags.className='tags';details.append(tags);col.append(details);grid.append(col);}card.append(grid);
    const choices=el('div');choices.className='choices';const captions=[['same_work','同一作品，不同版本'],['same_series','同一系列，不同作品或卷篇'],['unrelated','两者都不是'],['uncertain','无法确定']];
    const answer=el('p',a.label?'本题已标注，可直接修改。':'本题尚未标注。');answer.className='answer-state';
    for(let n=0;n<captions.length;n++){const [value,text]=captions[n], label=el('label');label.className='choice';const input=el('input');input.type='radio';input.name='relation';input.value=value;input.checked=a.label===value;input.onchange=()=>{state[currentId]={...row(currentId),label:value};answer.textContent='本题已标注，可直接修改。';persist();};label.append(input,document.createTextNode(`${n+1}. ${text}`));choices.append(label);}card.append(choices,answer);
    const flagLabel=el('label'),flag=el('input');flag.type='checkbox';flag.checked=a.collection_or_partial_overlap;flag.onchange=()=>{state[currentId]={...row(currentId),collection_or_partial_overlap:flag.checked};persist();};flagLabel.append(flag,document.createTextNode(' 涉及合集／部分内容重叠（独立备注标记）'));card.append(flagLabel);
    const reasonLabel=el('label','备注（可选）');reasonLabel.htmlFor='reason';const reason=el('textarea');reason.id='reason';reason.maxLength=4000;reason.placeholder='例如：同一卷不同语言、不同卷、疑似合集、缺少判断证据';reason.value=a.reason;reason.oninput=()=>{state[currentId]={...row(currentId),reason:reason.value};persist();};card.append(reasonLabel,reason);
    const footer=el('div');footer.className='footer';
    function move(delta){const next=visible[index+delta];if(next){currentId=next.pair_id;draw();$('pairTitle')?.focus({preventScroll:true});card.scrollIntoView({block:'start',behavior:'instant'});}}
    const previous=el('button','← 上一对');previous.id='previous';previous.disabled=index===0;previous.onclick=()=>move(-1);
    const clear=el('button','清除本题关系');clear.onclick=()=>{state[currentId]={...row(currentId),label:null};persist();draw();};
    const next=el('button',index===visible.length-1?'本批最后一对':'下一对 →');next.id='next';next.disabled=index===visible.length-1;next.onclick=()=>move(1);footer.append(previous,clear,next);card.append(footer);progress();
  }
  $('batch').onchange=()=>{currentBatch=Number($('batch').value);currentId=null;draw();};$('pending').onchange=()=>{currentId=null;draw();};
  $('export').onclick=()=>{const value=completeDocument(D,state), url=URL.createObjectURL(new Blob([JSON.stringify(value,null,2)],{type:'application/json'}));const link=el('a');link.href=url;link.download='labels.user.v2.json';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);dirty=false;notice('已发起完整JSON下载，请确认文件已保存。可在下一次标注前导入此文件。');};
  $('importButton').onclick=()=>$('import').click();
  $('import').onchange=async(e)=>{try{const file=e.target.files[0];if(!file)return;if(file.size>16*1024*1024)throw Error('文件过大');const imported=validateDocument(D,JSON.parse(await file.text()));if(dirty&&!confirm('导入文件将替换当前浏览器草稿。是否继续？建议先导出当前草稿。'))return;state=Object.fromEntries(imported.annotations.map(a=>[a.pair_id,a]));try{localStorage.setItem(key,JSON.stringify(imported));}catch(e){storageOK=false;}dirty=false;draw();notice('完整标注已导入，可以继续填写。');}catch(error){notice('导入失败：'+error.message+'；当前草稿已保留。');}finally{e.target.value='';}};
  window.addEventListener('beforeunload',e=>{if(dirty){e.preventDefault();e.returnValue='';}});
  window.addEventListener('keydown',e=>{if(e.altKey||e.ctrlKey||e.metaKey||['INPUT','TEXTAREA','SELECT','BUTTON'].includes(e.target.tagName)||e.target.isContentEditable)return;if(['1','2','3','4'].includes(e.key)){const input=document.querySelectorAll('input[name="relation"]')[Number(e.key)-1];if(input){e.preventDefault();input.click();}}else if(e.key==='ArrowLeft'||e.key==='ArrowRight'){const target=$(e.key==='ArrowLeft'?'previous':'next');if(target&&!target.disabled){e.preventDefault();target.click();}}});
  draw();
}
