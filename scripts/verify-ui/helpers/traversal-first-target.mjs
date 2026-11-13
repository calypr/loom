import assert from 'node:assert/strict';
import {createHash}from'node:crypto';import{readFileSync,lstatSync}from'node:fs';import{isAbsolute,join}from'node:path';
export const defaultRecipeSHA256='998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad';
export const sha256=bytes=>createHash('sha256').update(bytes).digest('hex');
const localURL=value=>{const u=new URL(value);assert.equal(u.protocol,'http:');assert(['127.0.0.1','localhost'].includes(u.hostname));assert(!u.username&&!u.password&&!u.hash);return u};
export function validateTraversalTarget(value) {
  assert.equal(value.kind,'OwnedTraversalComparisonTarget');assert.equal(value.version,1);
  assert.equal(value.referenceRecipeSHA256,defaultRecipeSHA256);assert.match(value.fixtureSourceDigest,/^sha256:[a-f0-9]{64}$/);
  assert.match(value.sourceFingerprint,/^[a-f0-9]{64}$/);assert.equal(value.generation,'fixture-v2');
  localURL(value.apiOrigin);
  assert.deepEqual(Object.keys(value.compositions).sort(),['inspector-below','inspector-right']);
  for(const [id,item]of Object.entries(value.compositions)){localURL(item.uiUrl);assert.equal(item.buildFlag,id);assert.equal(item.sourceFingerprint,value.sourceFingerprint)}
  assert.notEqual(value.compositions['inspector-right'].uiUrl,value.compositions['inspector-below'].uiUrl);
  for(const descriptor of [value.baseline,value.history,...(value.legacy?[value.legacy]:[])]) {
    assert.match(descriptor.project,/^loom_dev_verify_[A-Za-z0-9_/-]+$/);assert.equal(descriptor.explorerId,'dataframe-default');assert.equal(descriptor.outputId,'ResearchSubject');
    assert.match(descriptor.snapshotToken,/^sha256:[a-f0-9]{64}$/);assert(Number.isSafeInteger(descriptor.draftVersion)&&descriptor.draftVersion>0);assert.match(descriptor.draftDigest,/^sha256:[a-f0-9]{64}$/);
  }
  assert.notEqual(value.baseline.project,value.history.project,'Pristine and100-operation targets remain independently preserved');
  assert(isAbsolute(value.historyProof.path));assert.match(value.historyProof.sha256,/^[a-f0-9]{64}$/);
  assert(!('token'in value)&&!('authorization'in value),'Credential-free target metadata only');
  return value;
}
export function readTraversalTarget(path,expectedSHA256) {
  assert.equal(typeof path,'string','Set LOOM_TRAVERSAL_COMPARISON_TARGET');assert(isAbsolute(path));assert.match(expectedSHA256,/^[a-f0-9]{64}$/);const stat=lstatSync(path);
  assert(stat.isFile()&&!stat.isSymbolicLink()&&stat.nlink===1);assert.equal(stat.mode&0o077,0,'Protected target metadata');
  const bytes=readFileSync(path);assert.equal(sha256(bytes),expectedSHA256,'Target artifact identity');return validateTraversalTarget(JSON.parse(bytes));
}
export function validateDefaultHistoryProof(target) {
  const bytes=readFileSync(target.historyProof.path);assert.equal(sha256(bytes),target.historyProof.sha256);
  const proof=JSON.parse(bytes);assert.equal(proof.kind,'TraversalDefaultHistoryPreparation');assert.equal(proof.status,'passed');assert(Number.isFinite(proof.elapsedMs)&&proof.elapsedMs>=0&&proof.elapsedMs<=900000,'Preparation proof must satisfy the original900s deadline');
  assert.equal(proof.fixtureSourceDigest,target.fixtureSourceDigest);assert.equal(proof.referenceRecipeSHA256,target.referenceRecipeSHA256);
  assert.equal(proof.target.project,target.history.project);assert.equal(proof.target.explorerId,target.history.explorerId);assert.equal(proof.target.generation,target.generation);
  assert.deepEqual(proof.savedIdentity,{version:target.history.draftVersion,digest:target.history.draftDigest});assert.equal(proof.snapshotToken,target.history.snapshotToken);
  assert.equal(proof.sourceDocumentBefore.rootResourceType,'ResearchSubject');assert.equal(proof.sourceDocumentBefore.importedRecipe?.name,'calypr-meta-default');assert.equal(proof.sourceDocumentBefore.construction?.steps.length??0,0);
  assert.equal(proof.preparation.length,100);for(const [i,step]of proof.preparation.entries()){assert.equal(step.stepId,`stress_calc_${String(i).padStart(3,'0')}`);assert.equal(step.status,'passed');assert.deepEqual([...step.values].sort((a,b)=>a.id.localeCompare(b.id)),['rs-many','rs-one','rs-shared','rs-zero'].map(id=>({id,value:3}))) }
  return proof;
}
export function assertTraversalOwner(descriptor,state,generation) {
  assert.equal(state.kind,'ExplorerBuilderState');assert.equal(state.catalog.generation,generation);assert.equal(state.catalog.snapshotToken,descriptor.snapshotToken);
  assert.equal(state.draftVersion,descriptor.draftVersion);assert.equal(state.draftDigest,descriptor.draftDigest);
  assert.deepEqual(state.workspace.documents.map(x=>x.output.id),['DocumentReference','ResearchSubject','MedicationAdministration','Specimen','GroupMember']);
  assert(state.workspace.documents.every(doc=>doc.importedRecipe?.name==='calypr-meta-default'));
}
