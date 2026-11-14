import assert from 'node:assert/strict';
import {EXPLORER_AUTHORING_SEMANTICS_VERSION} from '../../../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';
import {nextStressStep} from './graph-rows-history-stress.mjs';
import {assertDefaultTraversalPreview,assertDefaultTraversalHistory,sourceColumn} from '../helpers/traversal-first-oracle.mjs';
import {defaultRecipeSHA256} from '../helpers/traversal-first-target.mjs';

export function traversalPreparationDeadline(now=()=>performance.now()) {
 const start=now();
 return phase=>{const elapsedMs=now()-start;assert(Number.isFinite(elapsedMs)&&elapsedMs>=0&&elapsedMs<=900000,`Original900s fixture preparation deadline exceeded after ${phase}: ${elapsedMs}ms`);return elapsedMs};
}

// Called only by the owned fixture launcher, outside the native action clock.
// It uses the same real proposal/CAS commands as the product, never state injection.
export async function prepareTraversalDefaultHistory({request,apiPath,state,fixtureSourceDigest,target,record,now=()=>performance.now()}) {
  const start=now(),deadline=traversalPreparationDeadline(now),original=structuredClone(state.workspace);
  const document=state.workspace.documents.find(x=>x.output.id==='ResearchSubject');
  assert(document?.importedRecipe?.name==='calypr-meta-default');
  assert.equal(document.construction?.steps.length??0,0);
  const post=async(path,data)=>{try{deadline(`before ${path}`);const response=await request.post(apiPath+path,{data,timeout:30000});deadline(`response ${path}`);assert.equal(response.status(),200,`${path}: ${await response.text()}`);const value=await response.json();deadline(`decoded ${path}`);return value}catch(error){await record?.({status:'failed',phase:'fixture-setup',elapsedMs:now()-start,error:error.message});throw error}};
  const capabilities=await post('/construction-capabilities',{snapshotToken:state.catalog.snapshotToken,expectedDraftVersion:state.draftVersion,expectedDraftDigest:state.draftDigest,outputId:'ResearchSubject',stageId:'source_projection'});
  assert.equal(capabilities.selectedStage.id,'source_projection');assert.equal(capabilities.baseConstruction.steps.length,0);
  assert(capabilities.selectedStage.capabilities.some(x=>x.kind==='DERIVE'&&x.supported));
  let owner=state,construction=capabilities.baseConstruction,columns=capabilities.selectedStage.columns;
  const preparation=[],idColumn=sourceColumn(document,'id');
  for(let index=0;index<100;index++) {
    const started=now(),step=nextStressStep(index,columns);
    const proposal=await post('/construction-proposals',{snapshotToken:owner.catalog.snapshotToken,expectedDraftVersion:owner.draftVersion,expectedDraftDigest:owner.draftDigest,outputId:'ResearchSubject',changedStepId:step.id,candidateConstruction:{...construction,steps:[...construction.steps,step]},limit:100});
    assert.equal(proposal.previewStatus,'READY');assert(proposal.proposalId);assert.equal(proposal.draftVersion,owner.draftVersion);assert.equal(proposal.draftDigest,owner.draftDigest);assert.equal(proposal.snapshotToken,owner.catalog.snapshotToken);
    assertDefaultTraversalPreview(document,proposal.preview);
    for(const row of proposal.preview.rows)for(let prior=0;prior<=index;prior++)assert.equal(row[`stress_value_${String(prior).padStart(3,'0')}`],3);
    const applied=await post('/commands',{commandId:`traversal-default-history-${step.id}`,semanticsVersion:EXPLORER_AUTHORING_SEMANTICS_VERSION,snapshotToken:owner.catalog.snapshotToken,expectedDraftVersion:owner.draftVersion,expectedDraftDigest:owner.draftDigest,commands:[{type:'APPLY_CONSTRUCTION_PROPOSAL',outputId:'ResearchSubject',proposalId:proposal.proposalId}]});
    assert.equal(applied.draftVersion,owner.draftVersion+1);
    const saved=applied.workspace.documents.find(x=>x.output.id==='ResearchSubject');
    assert.deepEqual(saved.construction,proposal.candidateConstruction);
    assert.deepEqual(applied.workspace.documents.filter(x=>x.output.id!=='ResearchSubject'),original.documents.filter(x=>x.output.id!=='ResearchSubject'));
    for(const key of ['importedRecipe','route','rows','columns'])assert.deepEqual(saved[key],document[key]);
    const stage=proposal.stages.filter(x=>x.id===step.id);assert.equal(stage.length,1);columns=stage[0].columns;construction=saved.construction;owner={...owner,...applied};
    preparation.push({stepId:step.id,status:'passed',draftVersion:owner.draftVersion,elapsedMs:now()-started,values:proposal.preview.rows.map(row=>({id:row[idColumn],value:row[`stress_value_${String(index).padStart(3,'0')}`]}))});
    await record?.({index:index+1,preparation,owner,elapsedMs:now()-start});
    if(index===99)assertDefaultTraversalHistory(saved,proposal.preview);
  }
  let elapsedMs;try{elapsedMs=deadline('complete setup')}catch(error){await record?.({status:'failed',phase:'fixture-setup',elapsedMs:now()-start,error:error.message});throw error}
  return {kind:'TraversalDefaultHistoryPreparation',status:'passed',fixtureSourceDigest,referenceRecipeSHA256:defaultRecipeSHA256,target,sourceDocumentBefore:document,snapshotToken:owner.catalog.snapshotToken,savedIdentity:{version:owner.draftVersion,digest:owner.draftDigest},elapsedMs,preparation};
}
