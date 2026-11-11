const assert = require('node:assert/strict');
const {validateDocument, completeDocument, batchPairs} = require('./review_v2.js');
const data={pack_id:'anonymous-v2',pairs:Array.from({length:300},(_,i)=>({pair_id:'pair-'+i,batch:Math.floor(i/30)+1}))};
const state={};
for(let i=0;i<30;i++)state['pair-'+i]={label:'same_work',reason:'anonymous',collection_or_partial_overlap:false};
const exported=completeDocument(data,state);
assert.equal(exported.annotations.length,300);
assert.equal(exported.annotations.filter(a=>a.label).length,30);
const imported=validateDocument(data,JSON.parse(JSON.stringify(exported)));
const resumed=Object.fromEntries(imported.annotations.map(a=>[a.pair_id,a]));
resumed['pair-150']={label:'same_series',reason:'different volume',collection_or_partial_overlap:false};
resumed['pair-151']={label:'unrelated',reason:'anonymous different work',collection_or_partial_overlap:false};
resumed['pair-152']={label:'uncertain',reason:'partial overlap',collection_or_partial_overlap:true};
const again=completeDocument(data,resumed);
assert.equal(again.annotations.length,300);
assert.equal(again.annotations.filter(a=>a.label).length,33);
assert.equal(again.annotations[0].label,'same_work');
assert.equal(batchPairs(data,1,true,resumed).length,0);
assert.equal(batchPairs(data,6,true,resumed).length,27);
assert.equal(batchPairs(data,6,false,resumed).length,30);
for(const mutate of [d=>d.annotations.pop(),d=>d.schema='recommendation-labels-v1',d=>d.pack_id='wrong',d=>d.annotations[0].label='same',d=>d.annotations[1]=d.annotations[0],d=>d.annotations[0].collection_or_partial_overlap='yes']){
 const bad=JSON.parse(JSON.stringify(again));mutate(bad);assert.throws(()=>validateDocument(data,bad));
}
assert.equal(again.annotations[0].label,'same_work');
console.log('complete 300-row export/import, four relations, batches and invalid import: PASS');
