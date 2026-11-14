import assert from'node:assert/strict';import{readFileSync,lstatSync}from'node:fs';import{isAbsolute}from'node:path';import{sha256,defaultRecipeSHA256}from'./traversal-first-target.mjs';
import{canonicalProjectID}from'../../loom-dev.mjs';
import{assertDefaultStepMetadata}from'./traversal-first-oracle.mjs';
const referenceRecipe=JSON.parse(readFileSync(new URL('../../../docs/work-packages/record-network-and-rows/fixtures/default-dataframer.json',import.meta.url)));
const outputIDs=['DocumentReference','ResearchSubject','MedicationAdministration','Specimen','GroupMember'];
export function readTraversalCreationProof(descriptor,target,identity,{protectedProjects=[]}={}){
 const forbidden=['loom_dev_verify_tmpfs-root','loom_dev_verify_trv-legacy-old-1',...protectedProjects].map(canonicalProjectID);assert(!forbidden.includes(canonicalProjectID(descriptor.project)),'Protected existing project cannot be a lifecycle fixture');
 const reference=descriptor.creationProof;assert(reference&&isAbsolute(reference.path),'Official fresh creation proof is required');assert.match(reference.sha256,/^[a-f0-9]{64}$/);const stat=lstatSync(reference.path);assert(stat.isFile()&&!stat.isSymbolicLink()&&stat.nlink===1);assert.equal(stat.mode&0o077,0);const bytes=readFileSync(reference.path);assert.equal(sha256(bytes),reference.sha256);const proof=JSON.parse(bytes);
 assert.deepEqual(Object.keys(proof).sort(),['kind','version','status','createdThrough','runId','seed','target','fixtureSourceDigest','referenceRecipeSHA256','source','initialWorkspace'].sort(),'Creation proof contains only public whitelisted fields');assert.equal(proof.kind,'TraversalLifecycleFixtureCreation');assert.equal(proof.version,1);assert.equal(proof.status,'passed');assert.equal(proof.createdThrough,'createFreshVerificationFixture');assert.match(proof.runId,/^[a-z0-9][a-z0-9-]{0,24}$/);assert.equal(descriptor.project,`loom_dev_verify_${proof.runId}`);
 assert.equal(proof.seed?.fresh,true);assert.equal(proof.seed?.reused,false);assert.deepEqual(proof.seed,{fresh:true,reused:false,bootstrapExplorerId:'loom-dev-bootstrap'});
 assert.deepEqual(proof.target,{project:descriptor.project,explorerId:descriptor.explorerId,generation:target.generation,snapshotToken:descriptor.snapshotToken,draftVersion:descriptor.draftVersion,draftDigest:descriptor.draftDigest});
 assert.equal(proof.fixtureSourceDigest,target.fixtureSourceDigest);assert.equal(proof.referenceRecipeSHA256,defaultRecipeSHA256);assert.equal(target.apiSource,identity.api.liveSource);assert.equal(target.apiBinary,identity.api.runningBinary);assert.deepEqual(proof.source,{sourceFingerprint:identity.sourceFingerprint,apiSource:identity.api.liveSource,apiBinary:identity.api.runningBinary});
 assert.deepEqual(proof.initialWorkspace.documents.map(x=>x.output.id),outputIDs);
 for(const document of proof.initialWorkspace.documents){assert.deepEqual(document.importedRecipe,{name:'calypr-meta-default',digest:'a8083ad446cbf016d4324d88c0601be83d69ed4a7a65c040843b5f0896410afb',output:document.output.id,translationVersion:'gen3-util-development-d461f11c7f0ccb078128349ccffd377e4014b62a-extension-columns-v2-research-subject-v2'});assert.equal(document.construction?.steps.length??0,0);assert.equal(document.importedSource?.relationships?.additions?.length??0,0);assert.equal(document.importedSource?.relationships?.removals?.length??0,0);assert.equal(document.importedSource?.relationships?.matchOverrides?.length??0,0);assertPristineSourceDescriptors(document)}
 return proof;
}
const columnMap=document=>{const result=new Map();for(const column of document.columns){assert(column.columnId,'Stable column identity required');assert(!result.has(column.columnId));result.set(column.columnId,column)}return result};
const exactBinding=(network,columnId)=>{const found=network.bindings.filter(x=>x.columnId===columnId);assert.equal(found.length,1,'Unambiguous actual network binding required');assert.equal(found[0].status,'EXACT');assert(found[0].sourceStepId);return found[0]};
export function assertTraversalSourceColumnIntegrity(before,after,beforeNetwork,afterNetwork,{sourceStepIds,fieldIds,allowFamilyShapeChanges=false}={}){
 assert.equal(before.output.id,after.output.id);assert.equal(beforeNetwork.outputId,before.output.id);assert.equal(afterNetwork.outputId,after.output.id);assert(Array.isArray(sourceStepIds)&&sourceStepIds.length>0);
 const a=columnMap(before),b=columnMap(after),oldFields=before.importedSource.fieldBindings??[],newFields=after.importedSource.fieldBindings??[];
 const fieldOwners=new Map();for(const step of after.importedSource.steps)for(const field of step.parameters?.fieldGroup?.fields??[]){assert(!fieldOwners.has(field.fieldId));fieldOwners.set(field.fieldId,step.id)}
 const fieldMap=new Map(),boundColumns=new Set();for(const binding of newFields){assert(!boundColumns.has(binding.columnId),'New field bindings must be one-to-one');boundColumns.add(binding.columnId);assert(fieldOwners.has(binding.fieldId),'Unknown newly hydrated field identity');assert(!fieldMap.has(binding.fieldId));assert(b.has(binding.columnId),'Field binding points to missing column');assert.equal(exactBinding(afterNetwork,binding.columnId).sourceStepId,fieldOwners.get(binding.fieldId));fieldMap.set(binding.fieldId,binding)}
 for(const binding of oldFields)assert.deepEqual(fieldMap.get(binding.fieldId),binding,'Existing field-to-column mapping must remain immutable');
 const affected=new Set();
 if(fieldIds){for(const id of fieldIds){const binding=fieldMap.get(id)??oldFields.find(x=>x.fieldId===id);assert(binding,'Intended field needs authoritative binding');affected.add(binding.columnId);assert(sourceStepIds.includes(exactBinding(afterNetwork,binding.columnId).sourceStepId))}}
 else for(const[document,network]of[[before,beforeNetwork],[after,afterNetwork]])for(const column of document.columns){const matches=network.bindings.filter(x=>x.columnId===column.columnId&&x.status==='EXACT'&&sourceStepIds.includes(x.sourceStepId));if(matches.length){assert.equal(matches.length,1);exactBinding(network,column.columnId);affected.add(column.columnId)}}
 assert.deepEqual(after.columns.filter(x=>!affected.has(x.columnId)),before.columns.filter(x=>!affected.has(x.columnId)),'Unrelated columns, order and stable IDs must remain exact');
 if(!allowFamilyShapeChanges)assert.deepEqual([...b.keys()],[...a.keys()],'A value-only edit cannot replace any existing column identity');
 for(const id of affected){if(a.has(id))assert(sourceStepIds.includes(exactBinding(beforeNetwork,id).sourceStepId));if(b.has(id))assert(sourceStepIds.includes(exactBinding(afterNetwork,id).sourceStepId))}
 return{affectedColumnIds:[...affected]};
}

export function assertPristineSourceDescriptors(document){
 assertDefaultStepMetadata(document);
 const output=referenceRecipe.outputs.find(x=>x.name===document.output.id);assert(output);
 const at=path=>path==='output'?output:path.split('.').reduce((node,part)=>{const match=/^(\w+)(?:\[(\d+)\])?$/.exec(part);assert(match);return match[2]===undefined?node[match[1]]:node[match[1]][Number(match[2])]},output);
 const present=(object,keys)=>Object.fromEntries(keys.filter(key=>Object.hasOwn(object,key)&&object[key]!==''&&!(Array.isArray(object[key])&&object[key].length===0)).map(key=>[key,object[key]]));
 for(const step of document.importedSource.steps){assert.equal(step.enabled,true);const raw=at(step.id);let expected;
  if(step.kind==='OUTPUT')expected={output:present(raw,['rowGrain','collisionPolicy'])};
  else if(step.kind==='FIELD_GROUP')expected={fieldGroup:{fields:raw.map((field,index)=>({fieldId:step.id.replace(/fields$/,'fields['+index+']'),enabled:true,...present(field,['name','label','expr','fallbacks','valueMode'])}))}};
  else if(step.kind==='CATALOG_PROJECTION')expected={catalog:present(raw,['name','includePaths','excludePaths','kinds','naming'])};
  else if(step.kind==='DYNAMIC_COLUMN'){expected={dynamic:present(raw,['name','source','key','value'])};if(Object.hasOwn(raw,'columnPrefix'))expected.dynamic.columnPrefix=raw.columnPrefix}
  else if(step.kind==='EXTENSION_COLUMN'){expected={extension:present(raw,['name','source'])};if(Object.hasOwn(raw,'columnPrefix'))expected.extension.columnPrefix=raw.columnPrefix}
  else if(step.kind==='PIVOT'){expected={pivot:present(raw,['name','discovery','columnExpr','valueExpr','valueFallbacks','itemSource','itemResourceType','columns','columnAliases'])};if(raw.discovery)expected.pivot.discovery=present(raw.discovery,['family','path'])}
  else if(step.kind==='TRAVERSAL')expected={traversal:present(raw,['name','toResourceType','alias','from'])};
  else if(step.kind==='EXPANSION')expected={expansion:present(raw,['from','as','ordinality','emptyPolicy'])};
  else if(step.kind==='IDENTITY')expected={identity:present(raw,['name','expr'])};
  assert.deepEqual(step.parameters,expected,'Fresh hydrated descriptors must match the pin, not authored overrides');
  if(['CATALOG_PROJECTION','DYNAMIC_COLUMN','EXTENSION_COLUMN','PIVOT'].includes(step.kind))assert.equal(step.maxColumns,step.kind==='PIVOT'?raw.discovery?.maxColumns??raw.maxColumns??0:raw.maxColumns??0);else assert.equal(step.maxColumns,undefined);
  if(['CATALOG_PROJECTION','DYNAMIC_COLUMN'].includes(step.kind))assert.equal(step.valueMode,raw.valueMode||'AUTO');
  if(step.kind==='PIVOT')assert.equal(step.valueMode,raw.projectionMode||'FIRST');else if(!['CATALOG_PROJECTION','DYNAMIC_COLUMN'].includes(step.kind))assert.equal(step.valueMode,undefined);
  if(step.kind==='TRAVERSAL')assert.equal(step.matchMode,raw.matchMode||'OPTIONAL');else assert.equal(step.matchMode,undefined);
 }
}
