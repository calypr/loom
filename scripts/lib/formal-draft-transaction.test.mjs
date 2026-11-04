import test from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { root,digest } from './formal-tools.mjs';
import { draftTransactionFrontier, draftTransactionModel, draftTransactionProperties, draftTransactionSourceHashes, validateDraftTransactionGraph, checkDraftTransactionGraphControls, checkDraftTransactionResults, draftTransactionCounterexampleWitness, draftTransactionSourceProofs, draftTransactionModelControls } from './formal-draft-transaction.mjs';
const copy=value=>structuredClone(value);
function graph() {
 const frontier=draftTransactionFrontier();
 const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(name=>[name,canonical(value[name])])):value;
 const byKey=new Map(frontier.states.map((state,index)=>[JSON.stringify(canonical(state)),String(index)]));
 return{schemaVersion:1,model:draftTransactionModel,modelSha256:digest(join(root,'verification/builder-interactions/DraftSaveTransaction.tla')),configSha256:digest(join(root,'verification/builder-interactions/DraftSaveTransaction.cfg')),productionSha256:draftTransactionSourceHashes(),states:frontier.states.map((vars,index)=>({id:String(index),start:index===0,vars})),steps:frontier.steps.map(([from,action,to])=>({from:byKey.get(from),action,to:byKey.get(to)}))};
}
function validRecord(cases) {return{schemaVersion:1,results:cases.map(row=>{const o=row.outcomes,queries=[];if(o.begin==='ok')for(const stage of ['read','archive','update']){queries.push(stage);if(o[stage]!=='ok')break;}return{id:row.id,queries,result:row.expected.result,backendConflict:row.expected.backendConflict,history:row.expected.history,head:row.expected.head,exactHistory:true,exactHead:true,stablePublished:true,exactReturned:true,callbackError:['read','archive','update'].some(stage=>!['none','ok'].includes(o[stage])),transactions:1};})};}
const cases=()=>validateDraftTransactionGraph(graph());
function conflictTrace() {
 const states=draftTransactionFrontier().states,initial=states[0],before=states.find(state=>state.phase==='return'&&state.outcomes.begin==='conflict'),after=copy(states.find(state=>state.phase==='done'&&state.outcomes.begin==='conflict'));after.result='other-error';
 return{property:draftTransactionProperties[1],states:[{action:'Initial',vars:initial},{action:'Begin',vars:before},{action:'Return',vars:after}]};
}
test('transaction frontier covers every declared query failure and unknown commit without claiming database isolation',()=>{
 const rows=cases();assert.equal(rows.length,14);assert.equal(graph().states.length,41);assert.equal(graph().steps.length,40);
 const unknown=rows.find(row=>row.outcomes.commit==='unknown');assert.equal(unknown.expected.history,true);assert.equal(unknown.expected.head,true);assert.equal(unknown.expected.result,'other-error');
 assert.deepEqual(checkDraftTransactionResults(validRecord(rows),rows),{cases:14,success:1,conflicts:7,unknownCommit:1});
 for(const stage of ['read','archive','update'])for(const outcome of stage==='archive'?['conflict','other']:['missing','conflict','other'])assert.ok(rows.some(row=>row.outcomes[stage]===outcome));
});
test('graph validation rejects missing, duplicate, unknown, stale and changed semantic evidence',async()=>{assert.equal(await checkDraftTransactionGraphControls(graph()),8);for(const source of ['',null,{},' '])assert.throws(()=>validateDraftTransactionGraph(graph(),{productionSource:source}));});
test('all six promises have actual source-fault coverage with one shared rollback variant',()=>{
 assert.equal(draftTransactionSourceProofs.length,5);assert.deepEqual([...new Set(draftTransactionSourceProofs.flatMap(proof=>proof.checkedProperties||[proof.property]))].sort(),draftTransactionProperties.slice().sort());assert.equal(draftTransactionModelControls.length,6);
});
test('successful nil return cannot masquerade as an acknowledged saved head',()=>{
 const rows=cases(),record=validRecord(rows);record.results.find(row=>row.result==='success').exactReturned=false;
 assert.throws(()=>checkDraftTransactionResults(record,rows),error=>error.code==='FORMAL_PROPERTY'&&error.property===draftTransactionProperties[2]);
});
test('named property scan retains early conformance and reaches a later concrete domain failure',()=>{
 const rows=cases(),record=validRecord(rows);record.results[0].queries=['foreign'];const index=rows.findIndex(row=>row.expected.backendConflict);record.results[index].result='other-error';
 assert.throws(()=>checkDraftTransactionResults(record,rows,{property:draftTransactionProperties[1]}),error=>error.code==='FORMAL_PROPERTY'&&error.property===draftTransactionProperties[1]);
 record.results[index].result='draft-conflict';assert.throws(()=>checkDraftTransactionResults(record,rows),/FORMAL_CONFORMANCE/);
});
test('an unrelated earlier failure cannot hide a later mismatch when a different named property was requested',()=>{
 const rows=cases(),record=validRecord(rows);record.results[0].head=!record.results[0].head;record.results.at(-1).queries=['foreign'];
 assert.throws(()=>checkDraftTransactionResults(record,rows,{property:draftTransactionProperties[4]}),error=>error.code!=='FORMAL_PROPERTY'&&/FORMAL_CONFORMANCE/.test(error.message));
});
test('only a genuine selected invariant gets its own witness when several fail on one row',()=>{
 const rows=cases(),record=validRecord(rows),index=rows.findIndex(row=>row.outcomes.update==='missing');record.results[index].history=true;record.results[index].result='success';record.results[index].callbackError=false;
 assert.throws(()=>checkDraftTransactionResults(record,rows,{property:draftTransactionProperties[3]}),error=>error.code==='FORMAL_PROPERTY'&&error.property===draftTransactionProperties[3]);
 assert.throws(()=>checkDraftTransactionResults(record,rows,{property:draftTransactionProperties[4]}),error=>error.code==='FORMAL_CONFORMANCE');
});
test('malformed or duplicate Go observations fail closed before property credit',()=>{
 const rows=cases();for(const change of [record=>record.results.pop(),record=>record.results.push(record.results[0]),record=>record.results[0].foreign=true,record=>record.results[0].head='yes',record=>record.results[0].transactions=2,record=>record.results[0].id='foreign']){const record=validRecord(rows);change(record);assert.throws(()=>checkDraftTransactionResults(record,rows));}
});
test('decoded domain-conflict counterexample requires its named failure and the actual action path',()=>{
 const trace=conflictTrace(),witness=draftTransactionCounterexampleWitness(trace);assert.equal(witness.outcomes.begin,'conflict');assert.equal(witness.expected.result,'draft-conflict');
 for(const change of [value=>value.states[0].action='Begin',value=>value.states[1].action='Query',value=>value.states[1].vars.history=true,value=>value.states.at(-1).vars.result='draft-conflict',value=>value.property=draftTransactionProperties[4],value=>value.states.splice(1,1),value=>value.states.at(-1).vars.foreign=true]){const changed=copy(trace);change(changed);assert.throws(()=>draftTransactionCounterexampleWitness(changed));}
});
test('partial-history counterexample can end at commit before Return',()=>{
 const states=draftTransactionFrontier().states,find=(phase,condition)=>copy(states.find(state=>state.phase===phase&&condition(state.outcomes)));
 const initial=copy(states[0]),read=find('read',o=>o.begin==='ok'),archive=find('archive',o=>o.read==='ok'),update=find('update',o=>o.archive==='ok'),broken=find('abort',o=>o.update==='other');broken.phase='commit';
 const committed=copy(broken);committed.phase='return';committed.outcomes.commit='ok';committed.history=true;committed.head=false;committed.transactionResult='ok';
 const trace={property:draftTransactionProperties[0],states:[{action:'Initial',vars:initial},{action:'Begin',vars:read},{action:'Query',vars:archive},{action:'Query',vars:update},{action:'Query',vars:broken},{action:'Commit',vars:committed}]};
 const witness=draftTransactionCounterexampleWitness(trace);assert.equal(witness.outcomes.update,'other');assert.equal(witness.outcomes.commit,'none');assert.equal(witness.expected.history,false);assert.equal(witness.expected.head,false);
 const forged=copy(trace);forged.states.at(-1).vars.result='other-error';assert.throws(()=>draftTransactionCounterexampleWitness(forged));
});
