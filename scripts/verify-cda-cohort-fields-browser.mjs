import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const memberField=process.env.LOOM_COHORT_FIELD??'resourceType';
assert(['resourceType','id'].includes(memberField));
const memberLabel=memberField==='id'?'Specimen ID':'Resource Type';
const authoredFilter=process.env.LOOM_COHORT_COMPOSITION==='1';
const filterOneMember=process.env.LOOM_COHORT_FILTER_ONE==='1';
const editCohortPolicy=process.env.LOOM_COHORT_POLICY_EDIT==='1';
const postCohortFilter=process.env.LOOM_COHORT_POST_FILTER==='1';
const collectionRoundTrip=process.env.LOOM_COHORT_COLLECTION_ROUND_TRIP==='1';
const removeCohortAnchor=process.env.LOOM_COHORT_REMOVE_ANCHOR==='1';
const authoredExpand=process.env.LOOM_COHORT_AUTHORED_EXPAND==='1';
assert(!filterOneMember || authoredFilter, 'LOOM_COHORT_FILTER_ONE requires LOOM_COHORT_COMPOSITION=1');
assert(!removeCohortAnchor || (authoredFilter&&!filterOneMember), 'Anchor removal requires the source EXISTS composition case');
assert(!authoredExpand || (memberField==='id' && !authoredFilter && !filterOneMember && !editCohortPolicy && !postCohortFilter && !collectionRoundTrip && !removeCohortAnchor), 'Authored cohort EXPAND requires the isolated Specimen.id ALL-member lifecycle mode');
const project = 'loom_dev_cda_fhir';
const explorer = `cohort-add-fields-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-cohort-add-fields-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const sourceRoot=fileURLToPath(new URL('..',import.meta.url));
const apiBuildTarget='local-cda-api';
const readApiBuildStamp=()=>checkContainerApiBuildStamp(localCDAApiContainer());
const report = { authoredFilter, filterOneMember, editCohortPolicy, postCohortFilter, collectionRoundTrip, removeCohortAnchor, authoredExpand, memberField, explorer, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
let frozenApiBuild, frozenSource;
const pendingNetworkReads=new Set();
const builderEvidence = value => {
  if (!value.catalog) return value;
  const { catalog, ...state } = value;
  return { ...state, catalog: {
    snapshotToken: catalog.snapshotToken,
    authorizationScopeDigest: catalog.authorizationScopeDigest,
    nodeCount: catalog.nodes?.length,
    candidateCount: catalog.candidates?.length,
  } };
};
const invalidateRun=(kind,reason)=>{
  if(report.status!=='invalidated'){
    report.priorStatus=report.status??'not-started';
    if(report.error) report.priorError=report.error;
  }
  report.status='invalidated';
  report.productFailure=false;
  report.invalidations??=[];
  report.invalidations.push({kind,reason});
  report.error=`${kind}: ${reason}`;
  process.exitCode=1;
};
const finalizeFreeze=async(kind,check,failureDetails)=>{
  const finishedAt=new Date().toISOString();
  try{
    const result=await check();
    report[kind]={...report[kind],...result,finishedAt};
    if(result.invalidatesRun||result.unchanged===false) invalidateRun(kind,result.reason??report[kind].reason??'freeze changed during the run');
  }catch(error){
    report[kind]={...report[kind],...failureDetails(error),finishedAt};
    invalidateRun(kind,error.reason??String(error));
  }
};
const api = async (path, body) => {
  const startedAt=Date.now();
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `cohort-add-fields-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, startedAt, completedAt:Date.now(), status: response.status, response: path.endsWith('/builder') ? structuredClone(builderEvidence(value)) : value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const waitForSavedPreview = async (startedAt, action) => {
  while (Date.now() - startedAt <= 5000) {
    const preview = report.nativeRequests.find(request => request.path === base + '/preview' && request.startedAt >= startedAt && request.completedAt && request.status === 200);
    if (preview) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(`${action} did not complete a fresh saved preview within five seconds`);
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === ${JSON.stringify(String(expectedRows[0]?.length ?? 2))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  const savedRows = expectedRows;
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const sourceFreezeStartedAt=new Date().toISOString();
  report.sourceFreeze={root:sourceRoot,startedAt:sourceFreezeStartedAt,available:false,invalidatesRun:false,productFailure:false};
  report.sourceFingerprint={root:sourceRoot,startedAt:sourceFreezeStartedAt,checked:false,invalidatesRun:false,productFailure:false};
  try{
    report.sourceFingerprint.before=sourceFingerprint(sourceRoot);
    report.sourceFingerprint.checked=true;
    frozenSource=await captureSourceFreeze(sourceRoot);
    report.sourceFreeze={...report.sourceFreeze,available:true,watchedFileCount:frozenSource.watchedFileCount};
  }catch(error){
    report.sourceFingerprint={...report.sourceFingerprint,checked:Boolean(report.sourceFingerprint.before),error:String(error)};
    report.sourceFreeze={...report.sourceFreeze,unchanged:false,changedPaths:error.changedPaths??[],invalidatesRun:true,productFailure:false,error:String(error)};
    error.initialSourceFreezeFailure=true;
    throw error;
  }
  const apiBuildStartedAt=new Date().toISOString();
  report.apiBuildFreeze={target:apiBuildTarget,startedAt:apiBuildStartedAt,invalidatesRun:false,productFailure:false};
  frozenApiBuild=await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze={...report.apiBuildFreeze,initial:frozenApiBuild.initial};
  const query=`FOR s IN Specimen FILTER s.project=="${project}" AND s.dataset_generation=="cda-fhir-v1" SORT s.id LIMIT 2 RETURN {id:s.id,resourceType:s.resourceType,generation:s.dataset_generation,project:s.project}`;
  const raw=spawnSync('rtk',['proxy','docker','exec',process.env.LOOM_ARANGO_CONTAINER??'loom-dev-6d7df93d6a37-arangodb-1','arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
  assert.equal(raw.status,0,raw.stderr);
  const sources=JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert.equal(sources.length,2);
  assert.equal(new Set(sources.map(source=>source.id)).size,2,'The independent cohort fixture requires two distinct FHIR IDs');
  assert(sources.every(source=>source.project===project&&source.generation==='cda-fhir-v1'&&source.resourceType==='Specimen'),'The raw fixture oracle must remain scoped to the pinned CDA project, generation, and resource type');
  const expectedMembers=filterOneMember?sources.slice(0,1):sources;
  report.oracle={query,sources,expectedMembers};
  await api(root,{name:explorer,title:'Cohort member fields QA'});
  builder=await api(base+'/builder');
  const node=builder.catalog.nodes.find(n=>n.resourceType==='Specimen');
  await command([{type:'CREATE_TABLE',title:'Named cohort QA',rootNodeId:node.nodeId}]);
  outputId=builder.workspace.documents[0].output.id;
  const field=builder.catalog.candidates.find(c=>c.nodeId===node.nodeId&&c.fieldPath==='id');
  await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:field.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:'Original Specimen ID'}]);
  const selections=base.replace('/authoring/v2','/selections');
  const selection=await api(selections,{snapshotToken:builder.catalog.snapshotToken,idempotencyKey:explorer,source:{kind:'resources',resources:{refs:sources.map(s=>({project,generation:s.generation,resourceType:'Specimen',id:s.id}))}}});
  const routes=await api(base+'/population-routes',{snapshotToken:builder.catalog.snapshotToken,outputId,selectionRevisionId:selection.id,limit:50});
  await command([{type:'SET_TABLE_POPULATION',outputId,selectionRevisionId:selection.id,routeChoiceId:routes.choices.find(c=>c.route.length===0).routeChoiceId}]);
  const scopeDigest=builder.catalog.authorizationScopeDigest;
  assert(scopeDigest,'The active CDA catalog must expose its authorization scope');
  assert.equal(selection.project,project);
  assert.equal(selection.generation,'cda-fhir-v1');
  assert.equal(selection.resourceType,'Specimen');
  assert.equal(selection.scopeDigest,scopeDigest);
  assert.equal(selection.memberCount,sources.length);
  const selectionPage=await api(selections+'/'+selection.id+'?limit=100');
  assert.equal(selectionPage.revision.id,selection.id);
  assert.equal(selectionPage.revision.scopeDigest,scopeDigest);
  assert.equal(selectionPage.revision.project,project);
  assert.equal(selectionPage.revision.generation,'cda-fhir-v1');
  assert.equal(selectionPage.revision.resourceType,'Specimen');
  assert.equal(selectionPage.revision.memberCount,sources.length);
  const members=selectionPage.members;
  const selectedRefs=members.map(member=>member.ref).map(ref=>`${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const oracleRefs=sources.map(source=>`${project}/cda-fhir-v1/Specimen/${source.id}`).sort();
  assert.deepEqual(selectedRefs,oracleRefs,'The immutable source selection must exactly match the independent scoped CDA witnesses');
  assert(members.every(member=>member.memberKey),'Every source witness must have an opaque pinned selection member key');
  const cohort=await api(selections+'/'+selection.id+'/explicit-groups',{snapshotToken:builder.catalog.snapshotToken,idempotencyKey:randomUUID(),groups:[{id:'qa-cohort',label:'Two Specimens',ordinal:0,memberIds:members.map(m=>m.memberKey)}]});
  assert.equal(cohort.sourceSelectionRevisionId,selection.id);
  assert.equal(cohort.groupCount,1);
  assert.equal(cohort.memberCount,sources.length);
  assert.equal(cohort.groups?.[0]?.label,'Two Specimens');
  assert.equal(cohort.groups?.[0]?.memberCount,sources.length);
  report.cohort=cohort;
  report.cohortWitness={project,generation:'cda-fhir-v1',resourceType:'Specimen',scopeDigest,selectionRevisionId:selection.id,revisionId:cohort.revisionId,refs:selectedRefs,ids:sources.map(source=>source.id)};
  if(authoredFilter){
    const capabilities=await api(base+'/construction-capabilities',{snapshotToken:builder.catalog.snapshotToken,expectedDraftVersion:builder.draftVersion,expectedDraftDigest:builder.draftDigest,outputId,stageId:'source_projection'});
    const columns=capabilities.selectedStage.columns.filter(column=>!column.internal);
    const filter=filterOneMember?{columnId:columns[0].id,operator:'EQUALS',values:[{kind:'STRING',string:sources[0].id}]}:{columnId:columns[0].id,operator:'EXISTS'};
    report.sourceFilter=filter;
    const step={id:'qa-source-filter',inputs:[{kind:'SOURCE_PROJECTION'}],operation:{kind:'FILTER',filter},outputs:columns.map(({id,name,label,type})=>({id,name,label,type}))};
    const proposal=await api(base+'/construction-proposals',{snapshotToken:builder.catalog.snapshotToken,expectedDraftVersion:builder.draftVersion,expectedDraftDigest:builder.draftDigest,outputId,changedStepId:step.id,candidateConstruction:{version:1,steps:[step]},limit:25});
    assert.equal(proposal.previewStatus,'READY');
    await command([{type:'APPLY_CONSTRUCTION_PROPOSAL',outputId,proposalId:proposal.proposalId}]);
  }
  browser=await launchBrowser(evidence);
  const nativeById=new Map();
  const evidenceBody=text=>{
    if(!text) return undefined;
    if(text.length>32768) return {truncated:true,length:text.length,text:text.slice(0,32768)};
    try { return JSON.parse(text); } catch { return text; }
  };
  browser.cdp.on('Network.requestWillBeSent',({requestId,request,wallTime,timestamp})=>{
    const url=new URL(request.url);
    if(!url.pathname.startsWith(root+'/'+explorer+'/')) return;
    const entry={requestId,path:url.pathname+url.search,method:request.method,startedAt:wallTime?Math.round(wallTime*1000):Date.now(),monotonicTimestamp:timestamp,body:evidenceBody(request.postData)};
    nativeById.set(requestId,entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived',({requestId,response})=>{
    const entry=nativeById.get(requestId);
    if(!entry) return;
    entry.status=response.status;
    entry.responseReceivedAt=Date.now();
    entry.serverRequestId=Object.entries(response.headers).find(([name])=>name.toLowerCase()==='x-request-id')?.[1];
  });
  browser.cdp.on('Network.loadingFinished',({requestId})=>{
    const entry=nativeById.get(requestId);
    if(entry) entry.completedAt=Date.now();
    if(!entry || !(entry.status>=400 || /proposal|preview|commands|construction-capabilities/.test(entry.path))) return;
    const read=browser.cdp.send('Network.getResponseBody',{requestId})
      .then(result=>{ entry.response=evidenceBody(result.base64Encoded?Buffer.from(result.body,'base64').toString('utf8'):result.body); })
      .catch(error=>{ entry.responseReadError=String(error); })
      .finally(()=>pendingNetworkReads.delete(read));
    pendingNetworkReads.add(read);
  });
  browser.cdp.on('Runtime.exceptionThrown',e=>report.errors.push({kind:'runtime',details:e.exceptionDetails}));
  browser.cdp.on('Runtime.consoleAPICalled',e=>{if(e.type==='error')report.errors.push({kind:'console',args:e.args});});
  browser.cdp.on('Network.loadingFailed',e=>{if(e.type==='Script'&&e.errorText!=='net::ERR_ABORTED')report.errors.push({kind:'module',error:e.errorText});});
  browser.cdp.on('Network.responseReceived',({response})=>{if(response.status>=400&&!response.url.endsWith('/favicon.ico'))report.errors.push({kind:'http',url:response.url,status:response.status});});
  await open(expectedMembers.map(s=>[s.id]));
  await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="What should each row represent?"]')?.disabled===false`);
  const cohortOptions=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="What should each row represent?"]').options].map(option=>({value:option.value,disabled:option.disabled,text:option.text}));`);
  report.cohortOptions=cohortOptions;
  assert(cohortOptions.some(option=>option.value==='explicit:'+cohort.revisionId&&!option.disabled),'A saved cohort must remain usable with a source filter: '+JSON.stringify(cohortOptions));
  let start=Date.now();
  await selectOption(browser.cdp,'select[aria-label="What should each row represent?"]','explicit:'+cohort.revisionId);
  await waitForBrowser(browser.cdp,`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(b=>b.innerText==='Apply row definition'&&!b.disabled)`);
  recordRender('cohort-preview',start);
  start=Date.now();
  await click(browser.cdp,'[aria-label="Row definition settings"] button',{name:'Cancel'});
  await waitForBrowser(browser.cdp,`!document.querySelector('[aria-label="Row definition settings"]')`);
  await rendered(expectedMembers.map(source=>[source.id]));
  recordRender('cohort-cancel',start);
  assert.deepEqual((await api(base+'/builder')).workspace,builder.workspace);
  await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="What should each row represent?"]')?.disabled===false`);
  start=Date.now();
  await selectOption(browser.cdp,'select[aria-label="What should each row represent?"]','explicit:'+cohort.revisionId);
  await waitForBrowser(browser.cdp,`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(b=>b.innerText==='Apply row definition'&&!b.disabled)`);
  recordRender('confirmed-cohort-preview',start);
  start=Date.now();
  await click(browser.cdp,'[aria-label="Row definition settings"] button',{name:'Apply row definition'});
  await waitForBrowser(browser.cdp,`document.body.innerText.includes('Preview failed:')||(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='3'&&document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]').length===3&&!document.body.innerText.includes('Loading your table…'))`);
  const cohortApplyError=await browserEval(browser.cdp,`return document.body.innerText.split(String.fromCharCode(10)).find(line=>line.startsWith('Preview failed:'));`);
  assert.equal(cohortApplyError,undefined,'Saved cohort preview must execute the accepted receipt: '+cohortApplyError);
  recordRender('cohort-apply',start);
  builder=await api(base+'/builder');
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId,cohort.revisionId);
  if(authoredFilter) assert.deepEqual(doc(builder).construction.steps.find(step=>step.id==='qa-source-filter').operation.filter,report.sourceFilter);
  const memberCells=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
  assert.equal(memberCells.length,3,'Saved cohort must render its three declared columns');
  assert(expectedMembers.every(source=>memberCells.at(-1).includes(source.id)),'Cohort members must include every retained source ID');
  if(filterOneMember) assert(!memberCells.at(-1).includes(sources[1].id),'Source filter must remove the excluded member before cohort materialization');
  report.cohortMemberCells=memberCells;
  let beforeField=builder;
  await click(browser.cdp,'[data-testid="construction-action-add-columns"]');
  await click(browser.cdp,'[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-add-columns-source"]')`);
  const chooseField=async()=>{
    await click(browser.cdp,'[data-testid="feature-catalog-raw-fields"] summary');
    await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Select Specimen.${memberField}"]:not(:disabled)')`);
    start=Date.now();
    await click(browser.cdp,'input[aria-label="Select Specimen.'+memberField+'"]');
    await click(browser.cdp,'[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(browser.cdp,`['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
    const result=await browserEval(browser.cdp,`const p=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p.dataset.proposalStatus,text:p.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
    report.fieldProposal=result;
    assert.equal(result.status,'ready',result.text);
    assert.equal(result.rows.length,1);
    const fieldCell=result.rows[0].at(-1);
    if(memberField==='resourceType') assert.equal(fieldCell,'Specimen','Cohort ALL must contain the distinct member resource type');
    else {
      assert(expectedMembers.every(source=>fieldCell.includes(source.id)),'Cohort ALL must retain exactly the filtered source IDs: '+fieldCell);
      if(filterOneMember) assert(!fieldCell.includes(sources[1].id),'Excluded member leaked into field preview: '+fieldCell);
    }
    recordRender('cohort-member-field-preview',start);
  };
  await chooseField();
  await click(browser.cdp,'[data-testid="construction-choice-proposal-panel"] button',{name:'Cancel'});
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  await click(browser.cdp,'[data-testid="feature-catalog-raw-fields"] summary');
  await chooseField();
  start=Date.now();
  await click(browser.cdp,'[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='4'`);
  recordRender('cohort-member-field-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  let cohortExpandBaseline;
  let cohortExpandMemberColumn;
  if(authoredExpand){
    cohortExpandBaseline=structuredClone(doc(builder));
    assert.equal(cohortExpandBaseline.rows.kind,'GROUPS');
    assert.equal(cohortExpandBaseline.rows.groups.source.explicit.revisionId,cohort.revisionId);
    assert.equal(cohortExpandBaseline.population.selectionRevisionId,selection.id);
    const memberBinding=cohortExpandBaseline.rows.groups.rowValues.find(binding=>binding.policy==='ALL');
    assert(memberBinding,'Named cohort member field must retain its native ALL policy');
    cohortExpandMemberColumn=cohortExpandBaseline.columns.find(column=>column.columnId===memberBinding.columnId);
    assert(cohortExpandMemberColumn,'ALL binding must resolve to a saved member-field column');
    assert.equal(cohortExpandMemberColumn.source?.field?.path,'id');
    assert.equal(cohortExpandMemberColumn.label,'Specimen ID');
    report.cohortExpandBaseline={revisionId:cohort.revisionId,selectionRevisionId:selection.id,memberColumnId:memberBinding.columnId,physicalColumn:cohortExpandMemberColumn.column,label:cohortExpandMemberColumn.label,policy:memberBinding.policy,ids:sources.map(source=>source.id)};
  }
  const verifyReload=async(withField)=>{
    start=Date.now();
    await navigate(browser.cdp,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
    await click(browser.cdp,`[data-testid="construction-table-${outputId}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')`);
    const values=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(c=>c.innerText.trim());`);
    const headers=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim());`);
    assert.equal(values.length,withField?4:3,JSON.stringify(values));
    if(withField){
      assert(headers.at(-1).toLowerCase().startsWith(memberLabel.toLowerCase()),'Saved column order must match the proposal: '+JSON.stringify(headers));
      if(memberField==='resourceType') assert.equal(values.at(-1),'Specimen');
      else {
        assert(expectedMembers.every(source=>values.at(-1).includes(source.id)),JSON.stringify(values));
        if(filterOneMember) assert(!values.at(-1).includes(sources[1].id),'Excluded member leaked after reload');
      }
    }
    assert(values.includes('Two Specimens'));
    report.reloads??=[];
    report.reloads.push({withField,headers,fieldCell:withField?values.at(-1):undefined});
    recordRender('cohort-reload-'+withField,start);
  };
  await verifyReload(true);
  if(authoredExpand){
    const readGrid=async()=>browserEval(browser.cdp,`return (()=>{
      const proposal=document.querySelector('[data-testid="construction-proposal-preview"]');
      const proposalTable=proposal?.querySelector('table');
      const saved=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();
      const headers=proposalTable
        ? [...proposalTable.querySelectorAll('thead th')].map(cell=>normalize(cell.querySelector('span')?.innerText??cell.innerText))
        : [...(saved?.querySelectorAll('[role="columnheader"]')??[])].map(cell=>normalize(cell.innerText));
      const rows=proposalTable
        ? [...proposalTable.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>normalize(cell.innerText)))
        : [...(saved?.querySelectorAll('[role="row"]')??[])].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>normalize(cell.innerText)));
      return {headers,rows,proposal:Boolean(proposalTable)};
    })()`);
    const sortedPairs=pairs=>pairs.sort((left,right)=>JSON.stringify(left).localeCompare(JSON.stringify(right)));
    const expectedPairs=sources.map(source=>['Two Specimens',source.id]);
    const assertExpanded=async(label,phase)=>{
      const grid=await readGrid();
      const groupIndex=grid.headers.findIndex(header=>header.toLowerCase()==='group label');
      const itemIndex=label?grid.headers.findIndex(header=>header.toLowerCase()===label.toLowerCase()):grid.headers.length-1;
      assert(groupIndex>=0,`${phase} must retain the named cohort label column: ${JSON.stringify(grid.headers)}`);
      assert(itemIndex>=0&&itemIndex<grid.headers.length,`${phase} must expose the expanded member ID column: ${JSON.stringify(grid.headers)}`);
      const pairs=sortedPairs(grid.rows.map(row=>[row[groupIndex],row[itemIndex]]));
      assert.deepEqual(pairs,sortedPairs(expectedPairs),`${phase} must render the exact two independently scoped CDA member IDs: ${JSON.stringify(grid)}`);
      return {headers:grid.headers,rowCount:grid.rows.length,pairs};
    };
    const assertCohortList=async(phase,expectedSavedSteps=0)=>{
      const grid=await readGrid();
      const groupIndex=grid.headers.findIndex(header=>header.toLowerCase()==='group label');
      const memberIndex=grid.headers.findIndex(header=>header.toLowerCase()==='specimen id');
      assert.equal(grid.rows.length,1,`${phase} must restore exactly one named cohort row: ${JSON.stringify(grid)}`);
      assert(groupIndex>=0&&memberIndex>=0,`${phase} must show cohort label and member list: ${JSON.stringify(grid.headers)}`);
      assert.equal(grid.rows[0][groupIndex],'Two Specimens');
      const ids=grid.rows[0][memberIndex].split(/;\s*/).filter(Boolean).sort();
      assert.deepEqual(ids,sources.map(source=>source.id).sort(),`${phase} must restore the exact ALL array from the independent scoped CDA members`);
      const current=await api(base+'/builder');
      builder=current;
      const saved=doc(current);
      assert.equal(saved.rows.groups.source.explicit.revisionId,cohort.revisionId);
      assert.equal(saved.population.selectionRevisionId,selection.id);
      assert.deepEqual(saved.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId),cohortExpandBaseline.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId));
      assert.equal(saved.construction?.steps?.length??0,expectedSavedSteps,`${phase} must have the expected saved EXPAND history`);
      return {grid,document:saved,draftVersion:current.draftVersion,draftDigest:current.draftDigest};
    };
    const openRowsDialog=async()=>{
      if(await browserEval(browser.cdp,`return Boolean(document.querySelector('[aria-label="Row definition settings"]'))`)) return;
      if(!await browserEval(browser.cdp,`return Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]'))`)){
        const back=await browserEval(browser.cdp,`return Boolean(document.querySelector('[data-testid="construction-close-operation-editor"]'))`);
        assert(back,'Rows settings trigger and operation-editor return control are both unavailable');
        await click(browser.cdp,'[data-testid="construction-close-operation-editor"]');
      }
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
      await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
      await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[aria-label="Row definition settings"]'))`);
    };
    const nativeExpandProposals=fromIndex=>report.nativeRequests.slice(fromIndex).filter(request=>
      request.path.split('?')[0]===base+'/construction-proposals'&&request.method==='POST'&&
      request.body?.candidateConstruction?.steps?.some(step=>step.operation?.kind==='EXPAND'));
    const waitNativeExpandProposal=async(fromIndex,phase)=>{
      const started=Date.now();
      let request;
      while(Date.now()-started<=5000){
        request=nativeExpandProposals(fromIndex).at(-1);
        if(request?.completedAt&&request.response) break;
        await new Promise(resolve=>setTimeout(resolve,40));
      }
      assert(request?.response,`${phase} must be driven by a completed native construction-proposal request`);
      assert.equal(request.status,200,`${phase} native proposal must succeed: ${JSON.stringify(request.response)}`);
      const step=request.body.candidateConstruction.steps.findLast(candidate=>candidate.operation?.kind==='EXPAND');
      assert.equal(step.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId,`${phase} must bind the saved Specimen.id ALL member field`);
      assert.equal(step.operation.expand.emptyPolicy,'PRESERVE_PARENT');
      return {request,step};
    };
    const enterExpand=async(phase)=>{
      await openRowsDialog();
      const action=await browserEval(browser.cdp,`return (()=>{const button=document.querySelector('[data-testid="construction-action-expand-rows"]');return {found:Boolean(button),disabled:button?.disabled,text:button?.innerText?.trim()}})()`);
      assert.equal(action.found,true,`${phase}: native Rows EXPAND action is missing`);
      assert.equal(action.disabled,false,`${phase}: native Rows EXPAND action is disabled: ${JSON.stringify(action)}`);
      const fromIndex=report.nativeRequests.length;
      start=Date.now();
      await click(browser.cdp,'[data-testid="construction-action-expand-rows"]');
      await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[aria-label="Expand repeated values"]'))&&document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'&&document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===2`);
      const selectionState=await browserEval(browser.cdp,`return (()=>{const select=document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]');return {value:select?.value,label:select?.selectedOptions?.[0]?.textContent?.trim(),disabled:select?.disabled,options:[...(select?.options??[])].map(option=>({value:option.value,label:option.textContent?.trim(),disabled:option.disabled}))}})()`);
      assert(selectionState.value&&selectionState.label?.toLowerCase().includes('specimen id'),`${phase}: default EXPAND source must be the saved Specimen.id ALL list: ${JSON.stringify(selectionState)}`);
      assert.equal(selectionState.disabled,false);
      const preview=await assertExpanded(undefined,phase+' automatic Preview');
      recordRender(phase,start);
      const native=await waitNativeExpandProposal(fromIndex,phase);
      return {selectionState,preview,native};
    };
    const baselineBuilder=await api(base+'/builder');
    builder=baselineBuilder;
    assert.deepEqual(doc(baselineBuilder),cohortExpandBaseline,'Authored EXPAND must start from the saved cohort and ALL member list');
    const first=await enterExpand('cohort-expand-auto-preview-cancel');
    const firstOutputLabel=first.preview.headers.at(-1);
    assert(firstOutputLabel,`EXPAND preview must expose an item output label: ${JSON.stringify(first.preview)}`);
    assert.equal(first.native.step.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId);
    assert.equal(first.native.step.outputs.find(column=>column.id===first.native.step.operation.expand.outputColumnId)?.label,firstOutputLabel);
    const cancelStart=Date.now();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')`);
    const canceled=await assertCohortList('EXPAND Cancel');
    assert.equal(canceled.draftVersion,baselineBuilder.draftVersion);
    assert.equal(canceled.draftDigest,baselineBuilder.draftDigest);
    recordRender('cohort-expand-cancel',cancelStart);
    report.cohortExpandCancel={preview:first.preview,draftVersion:canceled.draftVersion,draftDigest:canceled.draftDigest};

    const appliedProposal=await enterExpand('cohort-expand-auto-preview-apply');
    assert.deepEqual(appliedProposal.selectionState,first.selectionState,'Cancel and re-entry must preserve the same member-list source selection');
    const applyStart=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='3'&&!document.body.innerText.includes('Loading your table…')`);
    recordRender('cohort-expand-apply',applyStart);
    builder=await api(base+'/builder');
    let expandedDoc=doc(builder);
    let expandStep=expandedDoc.construction.steps.find(step=>step.operation.kind==='EXPAND');
    assert(expandStep,'Native Apply must persist the EXPAND operation');
    assert.equal(expandedDoc.construction.steps.length,1);
    // The first authored step assigns IDs to legacy source columns.
    for(const baselineColumn of cohortExpandBaseline.columns){
      const assigned=expandedDoc.columns.find(column=>column.column===baselineColumn.column);
      assert(assigned?.columnId,'Every retained source column must have a stable authored ID');
      const withoutAssignedId=structuredClone(assigned);
      if(!baselineColumn.columnId) delete withoutAssignedId.columnId;
      assert.deepEqual(withoutAssignedId,baselineColumn,'Authored normalization must preserve the exact source binding');
      baselineColumn.columnId=assigned.columnId;
    }
    cohortExpandBaseline.construction={version:1,steps:[]};
    beforeField=structuredClone(beforeField);
    for(const column of doc(beforeField).columns){
      const normalized=cohortExpandBaseline.columns.find(candidate=>candidate.column===column.column);
      assert(normalized?.columnId,'The original source column must retain its normalized ID');
      column.columnId=normalized.columnId;
    }
    doc(beforeField).construction={version:1,steps:[]};
    assert.equal(expandStep.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId);
    assert.equal(expandStep.operation.expand.emptyPolicy,'PRESERVE_PARENT');
    assert.equal(expandStep.id,appliedProposal.native.step.id);
    const initialOutput=expandStep.outputs.find(column=>column.id===expandStep.operation.expand.outputColumnId);
    assert.equal(initialOutput?.label,firstOutputLabel);
    let appliedPreview=await assertExpanded(initialOutput.label,'Applied cohort EXPAND');
    assert.equal(expandedDoc.rows.groups.source.explicit.revisionId,cohort.revisionId);
    assert.equal(expandedDoc.population.selectionRevisionId,selection.id);
    assert.deepEqual(expandedDoc.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId),cohortExpandBaseline.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId));
    report.cohortExpandApplied={stepId:expandStep.id,inputColumnId:expandStep.operation.expand.inputColumnId,outputColumnId:expandStep.operation.expand.outputColumnId,outputLabel:initialOutput.label,emptyPolicy:expandStep.operation.expand.emptyPolicy,preview:appliedPreview,draftVersion:builder.draftVersion,draftDigest:builder.draftDigest};

    const reloadExpanded=async(label,caseName)=>{
      start=Date.now();
      await navigate(browser.cdp,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
      await click(browser.cdp,`[data-testid="construction-table-${outputId}"]`);
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='3'&&!document.body.innerText.includes('Loading your table…')`);
      appliedPreview=await assertExpanded(label,caseName);
      builder=await api(base+'/builder');
      expandedDoc=doc(builder);
      const saved=expandedDoc.construction.steps.find(step=>step.id===expandStep.id);
      assert(saved,'Reload must retain the same authored EXPAND step ID');
      assert.equal(saved.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId);
      assert.equal(saved.operation.expand.outputColumnId,expandStep.operation.expand.outputColumnId);
      assert.equal(saved.outputs.find(column=>column.id===saved.operation.expand.outputColumnId)?.label,label);
      assert.equal(expandedDoc.rows.groups.source.explicit.revisionId,cohort.revisionId);
      assert.equal(expandedDoc.population.selectionRevisionId,selection.id);
      assert.deepEqual(expandedDoc.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId),cohortExpandBaseline.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId));
      recordRender(caseName,start);
      return {preview:appliedPreview,document:expandedDoc};
    };
    await reloadExpanded(initialOutput.label,'cohort-expand-reload');

    await openRowsDialog();
    await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[data-testid="construction-row-edit-${expandStep.id}"]:not(:disabled)'))`);
    await click(browser.cdp,`[data-testid="construction-row-edit-${expandStep.id}"]`);
    await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[aria-label="Expand repeated values"]'))`);
    const advanced='[data-testid="construction-reshape-expand-advanced"]';
    const advancedOpen=await browserEval(browser.cdp,`return document.querySelector(${JSON.stringify(advanced)})?.open===true`);
    if(!advancedOpen) await click(browser.cdp,`${advanced} summary`);
    const reopened=await browserEval(browser.cdp,`return (()=>({field:document.querySelector('select[aria-label="Repeated field"]')?.value,label:document.querySelector('input[aria-label="Expanded item label"]')?.value,emptyPolicy:document.querySelector('select[aria-label="Empty list policy"]')?.value}))()`);
    assert.equal(reopened.field,cohortExpandMemberColumn.columnId,'Saved Edit must reopen the exact ALL-list source column');
    assert.equal(reopened.label,initialOutput.label);
    assert.equal(reopened.emptyPolicy,'PRESERVE_PARENT');
    const editedLabel='Expanded Specimen ID';
    const editFrom=report.nativeRequests.length;
    start=Date.now();
    await browserEval(browser.cdp,`return (()=>{const input=document.querySelector('input[aria-label="Expanded item label"]');if(!input)throw Error('Expanded item label input is missing');const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')?.set;setter.call(input,${JSON.stringify(editedLabel)});input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));return input.value})()`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'&&document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===2&&[...document.querySelectorAll('[data-testid="construction-proposal-preview"] thead th')].some(header=>(header.querySelector('span')?.innerText??header.innerText).trim().toLowerCase()===${JSON.stringify(editedLabel.toLowerCase())})`);
    const editedNative=await waitNativeExpandProposal(editFrom,'Saved cohort EXPAND edit');
    const editedPreview=await assertExpanded(editedLabel,'Edited cohort EXPAND automatic Preview');
    recordRender('cohort-expand-edit-preview',start);
    assert.equal(editedNative.step.id,expandStep.id);
    assert.equal(editedNative.step.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId);
    assert.equal(editedNative.step.operation.expand.outputColumnId,expandStep.operation.expand.outputColumnId);
    assert.equal(editedNative.step.outputs.find(column=>column.id===expandStep.operation.expand.outputColumnId)?.label,editedLabel);
    const editApply=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='3'&&!document.body.innerText.includes('Loading your table…')`);
    recordRender('cohort-expand-edit-apply',editApply);
    builder=await api(base+'/builder');
    expandedDoc=doc(builder);
    expandStep=expandedDoc.construction.steps.find(step=>step.id===expandStep.id);
    assert(expandStep,'Saved label edit must retain the EXPAND operation');
    assert.equal(expandStep.operation.expand.inputColumnId,cohortExpandMemberColumn.columnId);
    assert.equal(expandStep.operation.expand.outputColumnId,editedNative.step.operation.expand.outputColumnId);
    assert.equal(expandStep.outputs.find(column=>column.id===expandStep.operation.expand.outputColumnId)?.label,editedLabel);
    assert.equal(expandedDoc.rows.groups.source.explicit.revisionId,cohort.revisionId);
    assert.equal(expandedDoc.population.selectionRevisionId,selection.id);
    assert.deepEqual(expandedDoc.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId),cohortExpandBaseline.rows.groups.rowValues.find(binding=>binding.columnId===cohortExpandMemberColumn.columnId));
    const editedSavedPreview=await assertExpanded(editedLabel,'Applied edited cohort EXPAND');
    assert.deepEqual(editedSavedPreview.pairs,editedPreview.pairs);
    assert.equal(editedSavedPreview.rowCount,editedPreview.rowCount);
    assert.deepEqual(editedSavedPreview.headers.map(header=>header.toLowerCase()),editedPreview.headers.map(header=>header.toLowerCase()));
    report.cohortExpandEdited={stepId:expandStep.id,inputColumnId:expandStep.operation.expand.inputColumnId,outputColumnId:expandStep.operation.expand.outputColumnId,label:editedLabel,preview:editedPreview,draftVersion:builder.draftVersion,draftDigest:builder.draftDigest};
    await reloadExpanded(editedLabel,'cohort-expand-edited-reload');

    await openRowsDialog();
    await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[data-testid="construction-row-remove-${expandStep.id}"]:not(:disabled)'))`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-row-remove-${expandStep.id}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'&&document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===1`);
    const removePreview=await assertCohortList('Cohort EXPAND removal proposal',1);
    recordRender('cohort-expand-remove-preview',start);
    const removeApply=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')`);
    const restored=await assertCohortList('Cohort EXPAND removal Apply');
    assert.deepEqual(restored.document,cohortExpandBaseline,'Removing EXPAND must restore the exact saved cohort and ALL-list document');
    recordRender('cohort-expand-remove-apply',removeApply);
    report.cohortExpandRemoved={preview:removePreview.grid,document:restored.document,draftVersion:restored.draftVersion,draftDigest:restored.draftDigest};

    start=Date.now();
    await navigate(browser.cdp,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
    await click(browser.cdp,`[data-testid="construction-table-${outputId}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')`);
    const finalRestored=await assertCohortList('Final cohort EXPAND restoration reload');
    assert.deepEqual(finalRestored.document,cohortExpandBaseline);
    recordRender('cohort-expand-removal-reload',start);
    report.cohortExpandFinalReload={document:finalRestored.document,draftVersion:finalRestored.draftVersion,draftDigest:finalRestored.draftDigest};
  }
  if(editCohortPolicy){
    const beforePolicy=await api(base+'/builder');
    const policyChoice='explicit:'+cohort.revisionId+':EXCLUDE';
    const proposePolicy=async()=>{
      await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
      await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="Unmatched record policy"]')?.disabled===false`);
      start=Date.now();
      await selectOption(browser.cdp,'select[aria-label="Unmatched record policy"]',policyChoice);
      await waitForBrowser(browser.cdp,`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
      recordRender('cohort-policy-preview',start);
    };
    await proposePolicy();
    await click(browser.cdp,'[aria-label="Row definition settings"] button',{name:'Cancel'});
    assert.deepEqual((await api(base+'/builder')).workspace,beforePolicy.workspace,'Policy Cancel must retain fields, filter and anchor');
    await proposePolicy();
    start=Date.now();
    await click(browser.cdp,'[aria-label="Row definition settings"] button',{name:'Apply row definition'});
    await waitForBrowser(browser.cdp,`!document.querySelector('[aria-label="Row definition settings"]')&&!document.body.innerText.includes('Loading your table…')`);
    await waitForSavedPreview(start,'Applying the cohort policy');
    builder=await api(base+'/builder');
    const expectedDocument=structuredClone(doc(beforePolicy));
    expectedDocument.rows.groups.source.explicit.unassignedMemberPolicy='EXCLUDE';
    assert.deepEqual(doc(builder),expectedDocument,'Policy edit must preserve selected member values, insertion boundary, columns and construction');
    recordRender('cohort-policy-apply',start);
    await verifyReload(true);
    beforeField=structuredClone(beforeField);
    doc(beforeField).rows.groups.source.explicit.unassignedMemberPolicy='EXCLUDE';
    report.savedPolicyDocument=doc(builder);
  }
  if(collectionRoundTrip){
    const beforeCollection=await api(base+'/builder');
    const fullRow=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled)`);
    recordRender('cohort-collection-controls-ready',start);
    start=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use all authorized rows'});
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use selected resources'&&!button.disabled)&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')||document.body.innerText.includes('Preview failed:')`);
    const collectionError=await browserEval(browser.cdp,`return document.body.innerText.split(String.fromCharCode(10)).find(line=>line.startsWith('Preview failed:'));`);
    assert.equal(collectionError,undefined,'Clearing the collection must render the pinned cohort: '+collectionError);
    await waitForSavedPreview(start,'Clearing the collection');
    const clearedRow=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    assert.deepEqual(clearedRow,fullRow,'Pinned cohort membership and retained filters must survive the full CDA starting collection');
    recordRender('cohort-clear-collection',start);
    builder=await api(base+'/builder');
    const expectedCleared=structuredClone(doc(beforeCollection));
    delete expectedCleared.population;
    assert.deepEqual(doc(builder),expectedCleared,'Only the starting collection may change');
    await verifyReload(true);
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use selected resources'&&!button.disabled)`);
    start=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use selected resources'});
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled)&&!document.body.innerText.includes('Loading your table…')`);
    await waitForSavedPreview(start,'Restoring the collection');
    const attachedRow=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    assert.deepEqual(attachedRow,fullRow);
    recordRender('cohort-reattach-collection',start);
    builder=await api(base+'/builder');
    assert.deepEqual(doc(builder),doc(beforeCollection));
    await verifyReload(true);
    report.collectionRoundTripDocument=doc(builder);
  }
  if(postCohortFilter){
    const beforePostFilter=structuredClone(await api(base+'/builder'));
    const fullRow=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    const filterProposal=async(name,expectedRows)=>{
      await waitForBrowser(browser.cdp,`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
      const result=await browserEval(browser.cdp,`const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:panel.dataset.proposalStatus,text:panel.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
      assert.equal(result.status,'ready',result.text);
      assert.deepEqual(result.rows,expectedRows,'Post-cohort filter must use cohort rows and preserve exact values');
      recordRender(name,start);
    };
    const filterTable=async(count)=>{
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(count+1))}&&!document.body.innerText.includes('Loading your table…')`);
      const rows=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);`);
      assert.deepEqual(rows,count?[fullRow]:[]);
    };
    const openFilter=async()=>{
      start=Date.now();
      await click(browser.cdp,'[data-testid="construction-action-keep-rows"]');
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
      const labelOption=await browserEval(browser.cdp,`return [...document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]').options].find(option=>option.textContent.trim().startsWith('Group label ('))?.value;`);
      assert(labelOption,'The cohort label must be available to a downstream filter');
      recordRender('post-cohort-filter-open',start);
      await selectOption(browser.cdp,'[data-testid="construction-filter-editor"] select[aria-label="Column"]',labelOption);
      await selectOption(browser.cdp,'[data-testid="construction-filter-editor"] select[aria-label="Condition"]','EQUALS');
      start=Date.now();
      await selectOption(browser.cdp,'[data-testid="construction-filter-editor"] select[aria-label="Condition"]','MISSING');
      await filterProposal('post-cohort-filter-preview',[]);
    };
    await openFilter();
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await filterTable(1);
    recordRender('post-cohort-filter-cancel',start);
    assert.deepEqual((await api(base+'/builder')).workspace,beforePostFilter.workspace);
    await openFilter();
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await filterTable(0);
    recordRender('post-cohort-filter-apply',start);
    builder=await api(base+'/builder');
    // A first construction edit assigns source slot identities once. Removal
    // retains that metadata while restoring every user-authored setting.
    if(!doc(beforePostFilter).construction){
      beforeField=structuredClone(beforeField);
      const upgraded=doc(builder);
      for(const state of [beforePostFilter,beforeField]){
        const expected=doc(state);
        for(const column of expected.columns){
          if(!column.columnId){
            const source=upgraded.columns.find(value=>value.column===column.column);
            assert(source?.columnId,'The construction upgrade must retain a stable source identity');
            column.columnId=source.columnId;
          }
        }
        expected.construction={version:1,steps:[]};
      }
    }
    const savedFilter=doc(builder).construction.steps.find(step=>step.operation.kind==='FILTER'&&step.id!=='qa-source-filter');
    assert(savedFilter);
    start=Date.now();
    await navigate(browser.cdp,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
    await click(browser.cdp,`[data-testid="construction-table-${outputId}"]`);
    await filterTable(0);
    recordRender('post-cohort-filter-empty-reload',start);
    await click(browser.cdp,`[data-testid="construction-history-step-${savedFilter.id}"]`);
    await click(browser.cdp,`[data-testid="construction-edit-step-${savedFilter.id}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
    start=Date.now();
    await selectOption(browser.cdp,'[data-testid="construction-filter-editor"] select[aria-label="Condition"]','EXISTS');
    await filterProposal('post-cohort-filter-edit-preview',[fullRow]);
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await filterTable(1);
    recordRender('post-cohort-filter-edit-apply',start);
    await verifyReload(true);
    await click(browser.cdp,`[data-testid="construction-history-step-${savedFilter.id}"]`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-remove-step-${savedFilter.id}"]`);
    await filterProposal('post-cohort-filter-remove-preview',[fullRow]);
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await filterTable(1);
    recordRender('post-cohort-filter-remove-apply',start);
    builder=await api(base+'/builder');
    assert.deepEqual(doc(builder),doc(beforePostFilter),'Removing downstream filter must restore cohort fields and prior operations exactly');
    await verifyReload(true);
    report.postCohortFilterDocument=doc(builder);
  }
  if(removeCohortAnchor){
    const beforeRemoval=await api(base+'/builder');
    assert.equal(doc(beforeRemoval).rows.groups.afterStepId,'qa-source-filter');
    const fullRow=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    const previewRemoval=async()=>{
      await click(browser.cdp,'[data-testid="construction-history-step-qa-source-filter"]');
      start=Date.now();
      await click(browser.cdp,'[data-testid="construction-remove-step-qa-source-filter"]');
      await waitForBrowser(browser.cdp,`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
      const result=await browserEval(browser.cdp,`const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:panel.dataset.proposalStatus,text:panel.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
      assert.equal(result.status,'ready',result.text);
      assert.deepEqual(result.rows,[fullRow],'Removing the insertion anchor must preserve the cohort and member fields');
      recordRender('cohort-anchor-remove-preview',start);
    };
    await previewRemoval();
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    assert.deepEqual((await api(base+'/builder')).workspace,beforeRemoval.workspace);
    recordRender('cohort-anchor-remove-cancel',start);
    await previewRemoval();
    start=Date.now();
    await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp,`(!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…'))||document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='error'`);
    const anchorApplyError=await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='error' ? document.body.innerText.slice(-6000) : undefined;`);
    assert.equal(anchorApplyError,undefined,'Removing the cohort insertion anchor must save the accepted preview: '+anchorApplyError);
    await waitForSavedPreview(start,'Removing the cohort insertion anchor');
    const cells=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    assert.deepEqual(cells,fullRow);
    recordRender('cohort-anchor-remove-apply',start);
    builder=await api(base+'/builder');
    const expected=structuredClone(doc(beforeRemoval));
    expected.construction.steps=expected.construction.steps.filter(step=>step.id!=='qa-source-filter');
    delete expected.rows.groups.afterStepId;
    assert.deepEqual(doc(builder),expected,'Anchor removal must rebase the cohort to source projection without changing its intent');
    beforeField=structuredClone(beforeField);
    doc(beforeField).construction.steps=doc(beforeField).construction.steps.filter(step=>step.id!=='qa-source-filter');
    delete doc(beforeField).rows.groups.afterStepId;
    await verifyReload(true);
    report.anchorRemovalDocument=doc(builder);
  }
  const columnsControl=await browserEval(browser.cdp,`return (()=>{const button=[...document.querySelectorAll('button')].find(item=>item.textContent?.trim()==='Columns');return {found:Boolean(button),disabled:button?.disabled}})()`);
  if(!columnsControl.found||columnsControl.disabled){
    const closeEditor=await browserEval(browser.cdp,`return Boolean(document.querySelector('[data-testid="construction-close-operation-editor"]'))`);
    if(closeEditor) await click(browser.cdp,'[data-testid="construction-close-operation-editor"]');
  }
  await waitForBrowser(browser.cdp,`[...document.querySelectorAll('button')].some(button=>button.textContent?.trim()==='Columns'&&!button.disabled)`);
  await click(browser.cdp,'button',{name:'Columns'});
  await waitForBrowser(browser.cdp,`Boolean(document.querySelector('[aria-label="Table columns"]'))`);
  start=Date.now();
  await click(browser.cdp,'button[aria-label="Remove '+memberLabel+' column"]');
  await waitForBrowser(browser.cdp,`!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='3'`);
  recordRender('cohort-member-field-remove',start);
  builder=await api(base+'/builder');
  assert.deepEqual(doc(builder),doc(beforeField));
  await verifyReload(false);
  assert.deepEqual(report.errors,[]);
  report.status='passed';
} catch (error) {
  const apiBuildInvalidated=error instanceof ApiBuildFreezeError;
  const sourceFreezeInvalidated=error.initialSourceFreezeFailure;
  report.status=apiBuildInvalidated||sourceFreezeInvalidated?'invalidated':'failed';
  report.error=String(error.stack??error);
  process.exitCode=1;
  if(apiBuildInvalidated){
    report.apiBuildFreeze={...report.apiBuildFreeze,initial:error.before,...(error.after?.checked?{after:error.after}:{}),unchanged:false,invalidatesRun:true,productFailure:false,reason:error.reason};
    report.priorStatus='not-started';
    report.productFailure=false;
    report.invalidations=[{kind:'apiBuildFreeze',reason:error.reason}];
  }else if(sourceFreezeInvalidated){
    report.sourceFreeze={...report.sourceFreeze,unchanged:false,changedPaths:error.changedPaths??[],invalidatesRun:true,productFailure:false,error:report.sourceFreeze.error??String(error)};
    report.priorStatus='not-started';
    report.productFailure=false;
    report.invalidations=[{kind:'sourceFreeze',reason:error.message}];
  }
  report.savedBuilderAtFailure=builderEvidence(await api(base+'/builder').catch(error=>({readError:String(error)})));
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
  report.failureBuilderPanels = browser ? await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="construction-filter-editor"], section[aria-label="Starting collection"], [data-testid="construction-proposal-panel"]')].map(panel => ({
    name: panel.getAttribute('aria-label') ?? panel.getAttribute('data-testid'),
    text: panel.innerText,
  }));`).catch(String) : undefined;
  report.failureControls = browser ? await browserEval(browser.cdp, `return [...document.querySelectorAll('section[aria-label="Starting collection"] button, [data-testid="construction-filter-editor"] select')].map(control => ({
    tag: control.tagName,
    label: control.getAttribute('aria-label'),
    text: control.innerText,
    disabled: control.disabled,
    value: control.value,
    options: control.tagName === 'SELECT' ? [...control.options].map(option => ({value: option.value, text: option.text, disabled: option.disabled})) : undefined,
  }));`).catch(String) : undefined;
} finally {
  await Promise.allSettled([...pendingNetworkReads]);
  if(frozenSource) await finalizeFreeze('sourceFreeze',()=>frozenSource.assertUnchanged(),error=>({unchanged:false,changedPaths:error.changedPaths??[],invalidatesRun:true,productFailure:false,error:String(error)}));
  await finalizeFreeze('apiBuildFreeze',async()=>{
    if(frozenApiBuild) return frozenApiBuild.assertUnchanged();
    const finalOnly=await captureApiBuildFreeze(readApiBuildStamp);
    return {after:finalOnly.initial,unchanged:false,invalidatesRun:true,productFailure:false,reason:'initial API build stamp was not captured'};
  },error=>({
    ...(frozenApiBuild?{...(error.before?{initial:error.before}:{}),...(error.after?{after:error.after}:{})}:error.before?{after:error.before}:{}),
    unchanged:false,invalidatesRun:true,productFailure:false,...(error.reason?{reason:error.reason}:{}),error:String(error),
  }));
  const fingerprintFinishedAt=new Date().toISOString();
  try{
    const after=sourceFingerprint(sourceRoot);
    const before=report.sourceFingerprint?.before;
    assert(before,'Initial source fingerprint was not captured');
    const unchanged=before.sha256===after.sha256&&before.files===after.files;
    report.sourceFingerprint={...report.sourceFingerprint,after,unchanged,invalidatesRun:!unchanged,productFailure:false,finishedAt:fingerprintFinishedAt};
    assert(unchanged,'Watched source fingerprint changed during the run');
  }catch(error){
    if(report.status!=='invalidated'){report.priorStatus=report.status??'not-started';if(report.error)report.priorError=report.error;}
    report.status='invalidated';
    report.productFailure=false;
    report.invalidations=[...(report.invalidations??[]),{kind:'sourceFingerprint',reason:String(error)}];
    report.sourceFingerprint={...report.sourceFingerprint,unchanged:false,invalidatesRun:true,productFailure:false,error:String(error),finishedAt:fingerprintFinishedAt};
    report.error=`sourceFingerprint: ${String(error)}`;
    process.exitCode=1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
