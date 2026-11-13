#!/usr/bin/env node
import assert from 'node:assert/strict';
import {mkdirSync,writeFileSync,readFileSync,lstatSync} from 'node:fs';
import {isAbsolute,join} from 'node:path';
import {request as requests} from 'playwright';
import {createDevSession,assertOwnedDevSession,createFreshVerificationFixture,fixtureSourceDigest} from './loom-dev.mjs';
import {fixtureProjectExplorerCollectionPath} from './verify-ui/helpers/fixture-context.mjs';
import {graphRowsNativeIdentity,assertGraphRowsNativeIdentity} from './verify-ui/helpers/graph-rows-native-identity.mjs';
import {defaultRecipeSHA256,sha256,validateTraversalTarget,validateDefaultHistoryProof} from './verify-ui/helpers/traversal-first-target.mjs';
import {readTraversalCreationProof} from './verify-ui/helpers/traversal-source-integrity.mjs';
import {publicTraversalSeed,traversalProgressRecorder} from './verify-ui/helpers/traversal-preparation-record.mjs';
import {prepareTraversalDefaultHistory} from './verify-ui/workflows/traversal-first-history-preparation.mjs';

const options={};
for(let i=2;i<process.argv.length;i+=2){assert(process.argv[i].startsWith('--')&&process.argv[i+1]&&!process.argv[i+1].startsWith('--'),'Use named option/value pairs');options[process.argv[i].slice(2)]=process.argv[i+1];}
assert(['compare','lifecycle'].includes(options.mode));assert(isAbsolute(options.out));assert.match(options.run,/^[a-z0-9][a-z0-9-]{0,15}$/);
for(const key of Object.keys(options))assert(['mode','out','run','ui-right','ui-below','ui','legacy-record'].includes(key),`Unknown option ${key}`);
const directory=options.out;mkdirSync(directory,{mode:0o700});
const save=(name,value)=>{const path=join(directory,name+'.json');writeFileSync(path,JSON.stringify(value,null,2)+'\n',{mode:0o600,flag:'wx'});return{path,sha256:sha256(readFileSync(path))};};
const session=createDevSession({...process.env,LOOM_DEV_FIXTURE_DIR:join(process.env.LOOM_DEV_SOURCE_ROOT??process.cwd(),'testdata/traversal-first')});
await assertOwnedDevSession(session,{uiRouting:'explicit-query'});
const before=graphRowsNativeIdentity(session);save('source-before',before);
const digest=fixtureSourceDigest(session.fixtureDir),client=await requests.newContext({timeout:30000});
const publicSource={sourceFingerprint:before.sourceFingerprint,apiSource:before.api.liveSource,apiBinary:before.api.runningBinary};
const baseTarget={version:1,referenceRecipeSHA256:defaultRecipeSHA256,fixtureSourceDigest:digest,generation:session.fixtureGeneration,apiOrigin:session.apiUrl,...publicSource};
const outputIDs=['DocumentReference','ResearchSubject','MedicationAdministration','Specimen','GroupMember'];
const apiPath=project=>`${session.apiUrl}${fixtureProjectExplorerCollectionPath(project)}/dataframe-default/authoring/v2`;
const state=async project=>{const response=await client.get(apiPath(project)+'/builder');assert.equal(response.status(),200);return response.json();};
const fresh=async suffix=>{
 const runId=`${options.run}-${suffix}`;assert(runId.length<=25);
 const fixture=await createFreshVerificationFixture(session,runId,{populateBootstrap:false});
 const seed=publicTraversalSeed(fixture.seed);
 const project=fixture.target.fixtureProject;assert.equal(project,`loom_dev_verify_${runId}`);assert.notEqual(project,session.fixtureProject);
 const value=await state(project);assert.equal(value.kind,'ExplorerBuilderState');assert.equal(value.catalog.generation,session.fixtureGeneration);assert.deepEqual(value.workspace.documents.map(x=>x.output.id),outputIDs);
 const descriptor={project,explorerId:'dataframe-default',snapshotToken:value.catalog.snapshotToken,draftVersion:value.draftVersion,draftDigest:value.draftDigest};
 const proof={kind:'TraversalLifecycleFixtureCreation',version:1,status:'passed',createdThrough:'createFreshVerificationFixture',runId,seed,target:{...descriptor,generation:session.fixtureGeneration},fixtureSourceDigest:digest,referenceRecipeSHA256:defaultRecipeSHA256,source:publicSource,initialWorkspace:value.workspace};
 descriptor.creationProof=save(`creation-${suffix}`,proof);
 readTraversalCreationProof(descriptor,baseTarget,before,{protectedProjects:[session.fixtureProject]});
 save(`initial-${suffix}`,value);return{descriptor,value};
};
let failure;
try{
 if(options.mode==='lifecycle'){
  assert(options.ui,'Set --ui to the chosen verified composition URL');
  const lifecycle={},journeys={};
  for(const[kind,suffix]of [['OUTPUT','out'],['FIELD_GROUP','fld'],['CATALOG_PROJECTION','cat'],['DYNAMIC_COLUMN','dyn'],['EXTENSION_COLUMN','ext'],['PIVOT','piv'],['TRAVERSAL','trav'],['EXPANSION','exp'],['IDENTITY','ident']])lifecycle[kind]=(await fresh(suffix)).descriptor;
  for(const[kind,suffix]of [['network','net'],['related-rows','rel'],['group','grp']])journeys[kind]=(await fresh(suffix)).descriptor;
  save('target',{...baseTarget,kind:'OwnedTraversalLifecycleTarget',uiUrl:options.ui,lifecycle,journeys});
 }else{
  assert(options['ui-right']&&options['ui-below'],'Set both verified composition URLs');
  const baseline=await fresh('base'),history=await fresh('hist');
  const proof=await prepareTraversalDefaultHistory({request:client,apiPath:apiPath(history.descriptor.project),state:history.value,fixtureSourceDigest:digest,target:{project:history.descriptor.project,explorerId:'dataframe-default',generation:session.fixtureGeneration},record:traversalProgressRecorder(join(directory,'history-progress.json'))});
  const saved=await state(history.descriptor.project);assert.deepEqual(proof.savedIdentity,{version:saved.draftVersion,digest:saved.draftDigest});assert.equal(proof.snapshotToken,saved.catalog.snapshotToken);
  const target={...baseTarget,kind:'OwnedTraversalComparisonTarget',compositions:Object.fromEntries([['inspector-right',options['ui-right']],['inspector-below',options['ui-below']]].map(([key,uiUrl])=>[key,{uiUrl,buildFlag:key,sourceFingerprint:before.sourceFingerprint}])),baseline:{...baseline.descriptor,outputId:'ResearchSubject'},history:{...history.descriptor,outputId:'ResearchSubject',draftVersion:saved.draftVersion,draftDigest:saved.draftDigest,snapshotToken:saved.catalog.snapshotToken},historyProof:save('history-preparation',proof)};
  if(options['legacy-record']){
   assert(isAbsolute(options['legacy-record']));const stat=lstatSync(options['legacy-record']);assert(stat.isFile()&&!stat.isSymbolicLink()&&(stat.mode&0o077)===0);const legacy=JSON.parse(readFileSync(options['legacy-record']));assert.equal(legacy.status,'passed');assert.equal(legacy.fixtureSourceDigest,digest);assert.equal(legacy.referenceRecipeSHA256,defaultRecipeSHA256);assert.equal(legacy.sourceFingerprint,'134faf8b921e6fb0eae54851ff4a25de1091bb0640d6aa2b110e99a27d950e54');
   const current=await state(legacy.target.project);assert.equal(current.draftVersion,legacy.target.draftVersion);assert.equal(current.draftDigest,legacy.target.draftDigest);assert.equal(current.catalog.snapshotToken,legacy.target.snapshotToken);assert(current.workspace.documents.every(doc=>doc.importedSource.steps.every(step=>step.parameters===undefined)));
   target.legacy={...legacy.target,outputId:'ResearchSubject'};save('legacy-record',{path:options['legacy-record'],sha256:sha256(readFileSync(options['legacy-record'])),scope:'Actual old-source creation; not relabeled as current-source creation'});
  }
  validateTraversalTarget(target);validateDefaultHistoryProof(target);save('target',target);
 }
 assert.equal(fixtureSourceDigest(session.fixtureDir),digest);const after=graphRowsNativeIdentity(session);assertGraphRowsNativeIdentity(before,after);save('source-after',after);save('result',{status:'passed',mode:options.mode,scope:'Official disposable fixture creation; browser acceptance remains separate'});
}catch(error){failure=error;save('failure',{status:'failed',error:error.message});try{save('source-after-failure',graphRowsNativeIdentity(session));}catch(identityError){save('source-integrity-failure',{error:identityError.message});}}
finally{await client.dispose();}
if(failure)throw failure;
