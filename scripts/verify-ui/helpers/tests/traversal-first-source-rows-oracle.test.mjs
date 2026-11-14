import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { sourceRowIdentityVectors, assertGroupMemberSourceValues, assertTraversalAliasValues, assertStableSourceStructure, sourceSubtreeStepIds } from '../traversal-first-source-rows-oracle.mjs';
import { traversalOracle } from '../traversal-first-oracle.mjs';
const uuidBytes=uuid=>Buffer.from(uuid.replaceAll('-',''),'hex');
function uuid(namespace,name,version){const bytes=createHash(version===3?'md5':'sha1').update(Buffer.concat([uuidBytes(namespace),Buffer.from(name,'utf8')])).digest().subarray(0,16);bytes[6]=(bytes[6]&15)|(version<<4);bytes[8]=(bytes[8]&63)|128;const hex=bytes.toString('hex');return`${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`}
const dns='6ba7b810-9dad-11d1-80b4-00c04fd430c8';
test('literal source identity vectors independently agree with RFC UUID3/5, including the preserved empty member',()=>{
  const original=uuid(dns,sourceRowIdentityVectors.originalNamespace,3),changed=uuid(dns,sourceRowIdentityVectors.changedNamespace,3);
  assert.equal(original,traversalOracle.defaultGroupMemberIdentity.namespaceUUID);assert.equal(changed,sourceRowIdentityVectors.changedNamespaceUUID);
  for(const row of [...sourceRowIdentityVectors.original,sourceRowIdentityVectors.preservedEmpty])assert.equal(uuid(original,`${row.group_id},${row.member_id??''}`,5),row.rowIdentity);
  for(const row of sourceRowIdentityVectors.changed)assert.equal(uuid(changed,`${row.group_id},${row.member_id}`,5),row.rowIdentity);
  assert.deepEqual(sourceRowIdentityVectors.original,traversalOracle.defaultGroupMemberIdentity.vectors.map(({group_id,member_id,rowIdentity})=>({group_id,member_id,rowIdentity})));
});
const groupDocument={output:{id:'GroupMember'},columns:[{column:'group',source:{imported:{outputColumn:'group_id'}}},{column:'member',source:{imported:{outputColumn:'member_id'}}}]};
function groupPreview(rows){return{outputId:'GroupMember',sampled:false,rows:rows.map(row=>({group:row.group_id,member:row.member_id,__loom_row_id:row.rowIdentity}))}}
test('GroupMember oracle distinguishes changed UUIDs, preserved empty null, missing keys and duplicate identities',()=>{
  assertGroupMemberSourceValues(groupDocument,groupPreview(sourceRowIdentityVectors.original));
  const rows=[...sourceRowIdentityVectors.original,sourceRowIdentityVectors.preservedEmpty];assertGroupMemberSourceValues(groupDocument,groupPreview(rows),{preserveEmpty:true});
  assertGroupMemberSourceValues(groupDocument,groupPreview(sourceRowIdentityVectors.changed),{changedIdentity:true});
  assert.throws(()=>assertGroupMemberSourceValues(groupDocument,groupPreview(sourceRowIdentityVectors.original),{changedIdentity:true}));
  const missing=groupPreview(rows);delete missing.rows[2].member;assert.throws(()=>assertGroupMemberSourceValues(groupDocument,missing,{preserveEmpty:true}),/Missing returned key/);
  const duplicate=groupPreview(sourceRowIdentityVectors.original);duplicate.rows[1].__loom_row_id=duplicate.rows[0].__loom_row_id;assert.throws(()=>assertGroupMemberSourceValues(groupDocument,duplicate));
});
test('Traversal alias expectations use explicit field identity mappings and literal four-record values',()=>{
  const expected=traversalOracle.defaultOutputs.ResearchSubject;
  const names=Object.keys(expected[0]);const document={output:{id:'ResearchSubject'},columns:names.map((name,index)=>({columnId:`c${index}`,column:`public${index}`,source:{imported:{outputColumn:name==='patient__observation__observation_id'?'patient__audited_observation__observation_id':name}}})),importedSource:{steps:[{id:'traversals[0].traversals[0].fields',kind:'FIELD_GROUP',parameters:{fieldGroup:{fields:[{fieldId:'original',name:'observation_id',expr:{select:'audited_observation.id'}}]}}}],fieldBindings:[{fieldId:'original',columnId:`c${names.indexOf('patient__observation__observation_id')}`} ]}};
  const preview={outputId:'ResearchSubject',sampled:false,rows:expected.map(row=>Object.fromEntries(names.map((name,index)=>[`public${index}`,row[name]])))};
  assertTraversalAliasValues(document,preview,'original');
  const retargeted=structuredClone(document);retargeted.importedSource.fieldBindings[0].columnId='c0';assert.throws(()=>assertTraversalAliasValues(retargeted,preview,'original'));
  const missing=structuredClone(preview);delete missing.rows[0][`public${names.indexOf('patient__observation__observation_id')}`];assert.throws(()=>assertTraversalAliasValues(document,missing,'original'),/Missing/);
});
test('Structural checks reject lost field IDs, altered pins and silently retargeted occurrences',()=>{
  const before={route:{occurrenceId:'base',resourceType:'ResearchSubject',children:[{occurrenceId:'obs',resourceType:'Observation',catalogEdgeId:'edge',matchMode:'OPTIONAL'}]},importedRecipe:{digest:'immutable'},importedSource:{steps:[{id:'fields',kind:'FIELD_GROUP',parameters:{fieldGroup:{fields:[{fieldId:'stable',name:'id'}]}}}],fieldBindings:[{fieldId:'stable',columnId:'c1'}]}};
  assertStableSourceStructure(before,structuredClone(before));
  for(const mutate of [doc=>doc.importedRecipe.digest='changed',doc=>doc.route.children[0].occurrenceId='different',doc=>doc.importedSource.steps[0].parameters.fieldGroup.fields[0].fieldId='lost']){const changed=structuredClone(before);mutate(changed);assert.throws(()=>assertStableSourceStructure(before,changed));}
});

test('affected alias families come from actual source parent identities and exclude siblings',()=>{
 const document={importedSource:{steps:[{id:'root',kind:'OUTPUT',parameters:{output:{rowGrain:'resource'}}},{id:'observations',kind:'TRAVERSAL',parameters:{traversal:{name:'observations',toResourceType:'Observation'}}},{id:'observations.fields',parentId:'observations',kind:'FIELD_GROUP',parameters:{fieldGroup:{fields:[]}}},{id:'observations.pivot',parentId:'observations',kind:'PIVOT',parameters:{pivot:{name:'values'}}},{id:'conditions',kind:'TRAVERSAL',parameters:{traversal:{name:'conditions',toResourceType:'Condition'}}}]}};
 assert.deepEqual(sourceSubtreeStepIds(document,'observations'),['observations','observations.fields','observations.pivot']);assert.throws(()=>sourceSubtreeStepIds(document,'missing'));
});

test('first mapping hydration is allowed while every previously known mapping remains exact',()=>{
 const before={route:{occurrenceId:'base',resourceType:'Group'},importedRecipe:{digest:'pin'},importedSource:{steps:[{id:'fields',kind:'FIELD_GROUP',parameters:{fieldGroup:{fields:[{fieldId:'f',name:'id'}]}}}]}};
 const hydrated=structuredClone(before);hydrated.importedSource.fieldBindings=[{fieldId:'f',columnId:'stable-column'}];assertStableSourceStructure(before,hydrated);
 const replaced=structuredClone(hydrated);replaced.importedSource.fieldBindings[0].columnId='replacement';assert.throws(()=>assertStableSourceStructure(hydrated,replaced));
});

test('actual native intent guard rejects no-op and wrong parameters for all three source families',async()=>{
  const {readFileSync}=await import('node:fs');
  const text=readFileSync(new URL('../../workflows/traversal-first-source-rows.mjs',import.meta.url),'utf8');
  const start=text.indexOf('function expectTypedEdit('),end=text.indexOf('function assertCandidateRows(',start);
  const expectedSteps={TRAVERSAL:['traversals[0].traversals[0]','traversals[0].traversals[0].fields'],EXPANSION:['expand','identity','fields'],IDENTITY:['identity']};
  const check=Function('assert','expectedSteps','sourceStep','sourceRowIdentityVectors',text.slice(start,end)+';return expectTypedEdit;')(assert,expectedSteps,null,sourceRowIdentityVectors);
  for(const kind of Object.keys(expectedSteps)){
    const field=(name,expr)=>({fieldId:name,name,enabled:true,expr});
    const steps=kind==='TRAVERSAL'?[{id:expectedSteps[kind][0],parameters:{traversal:{alias:'observation'}}},{id:expectedSteps[kind][1],parameters:{fieldGroup:{fields:[field('observation_id',{select:'observation.id'}),field('sibling',{select:'root.id'})]}}}]:[{id:'expand',parameters:{expansion:{as:'member',emptyPolicy:'EXCLUDE'}}},{id:'identity',parameters:{identity:{expr:{call:'uuid5',args:[{literal:sourceRowIdentityVectors.originalNamespace},{select:'root.id'},{call:'concat',args:[{literal:','},{call:'reference_id',args:[{select:'member.entity.reference'}]}]}]}}}},{id:'fields',parameters:{fieldGroup:{fields:[field('member_id',{call:'reference_id',args:[{select:'member.entity.reference'}]}),field('sibling',{select:'root.id'})]}}}];
    steps.forEach(step=>step.enabled=true);steps.push({id:'unrelated',enabled:true,parameters:{output:{name:'unchanged'}}});
    const original={output:{id:kind},importedSource:{steps}}, candidate=structuredClone(original);
    const get=id=>candidate.importedSource.steps.find(step=>step.id===id);
    if(kind==='TRAVERSAL'){get(expectedSteps[kind][0]).parameters.traversal.alias='audited_observation';get(expectedSteps[kind][1]).parameters.fieldGroup.fields[0].expr={select:'audited_observation.id'}}
    if(kind==='EXPANSION'){get('expand').parameters.expansion={as:'participant',emptyPolicy:'PRESERVE_PARENT'};get('identity').parameters.identity.expr.args[2].args[1].args[0].select='participant.entity.reference';get('fields').parameters.fieldGroup.fields[0].expr.args[0].select='participant.entity.reference'}
    if(kind==='IDENTITY')get('identity').parameters.identity.expr.args[0].literal=sourceRowIdentityVectors.changedNamespace;
    const request={outputId:kind};const changes=expectedSteps[kind].map(id=>{const step=get(id);return{stepId:id,enabled:step.enabled,parameters:structuredClone(step.parameters)}});request[kind==='IDENTITY'?'sourceStep':'sourceSteps']=kind==='IDENTITY'?changes[0]:changes;
    check(kind,request,original,{candidate});
    const noop=structuredClone(request);for(const change of noop.sourceSteps??[noop.sourceStep])change.parameters=structuredClone(steps.find(step=>step.id===change.stepId).parameters);assert.throws(()=>check(kind,noop,original));
    for(const changeIndex of changes.keys()){const wrong=structuredClone(request),list=wrong.sourceSteps??[wrong.sourceStep];list[changeIndex].parameters={wrong:'parameter'};assert.throws(()=>check(kind,wrong,original));}
    const disabled=structuredClone(request);(disabled.sourceSteps??[disabled.sourceStep])[0].enabled=false;assert.throws(()=>check(kind,disabled,original));
    const sibling=structuredClone(candidate);sibling.importedSource.steps.at(-1).parameters.output.name='changed';assert.throws(()=>check(kind,request,original,{candidate:sibling}));
    const restore={outputId:kind};const originals=expectedSteps[kind].map(id=>{const step=steps.find(step=>step.id===id);return{stepId:id,enabled:step.enabled,parameters:step.parameters}});restore[kind==='IDENTITY'?'sourceStep':'sourceSteps']=kind==='IDENTITY'?originals[0]:originals;check(kind,restore,original,{restore:true,candidate:original});
  }
});
