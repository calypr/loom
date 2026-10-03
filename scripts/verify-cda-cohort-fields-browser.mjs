import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const memberField=process.env.LOOM_COHORT_FIELD??'resourceType';
assert(['resourceType','id'].includes(memberField));
const memberLabel=memberField==='id'?'Specimen ID':'Resource Type';
const authoredFilter=process.env.LOOM_COHORT_COMPOSITION==='1';
const filterOneMember=process.env.LOOM_COHORT_FILTER_ONE==='1';
const editCohortPolicy=process.env.LOOM_COHORT_POLICY_EDIT==='1';
const postCohortFilter=process.env.LOOM_COHORT_POST_FILTER==='1';
const collectionRoundTrip=process.env.LOOM_COHORT_COLLECTION_ROUND_TRIP==='1';
const removeCohortAnchor=process.env.LOOM_COHORT_REMOVE_ANCHOR==='1';
assert(!filterOneMember || authoredFilter, 'LOOM_COHORT_FILTER_ONE requires LOOM_COHORT_COMPOSITION=1');
assert(!removeCohortAnchor || (authoredFilter&&!filterOneMember), 'Anchor removal requires the source EXISTS composition case');
const project = 'loom_dev_cda_fhir';
const explorer = `cohort-add-fields-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-cohort-add-fields-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { authoredFilter, filterOneMember, editCohortPolicy, postCohortFilter, collectionRoundTrip, removeCohortAnchor, memberField, explorer, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
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
  const query=`FOR s IN Specimen FILTER s.project=="${project}" AND s.dataset_generation=="cda-fhir-v1" LIMIT 2 RETURN {id:s.id,resourceType:s.resourceType,generation:s.dataset_generation}`;
  const raw=spawnSync('rtk',['proxy','docker','exec',process.env.LOOM_ARANGO_CONTAINER??'loom-dev-6d7df93d6a37-arangodb-1','arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
  assert.equal(raw.status,0,raw.stderr);
  const sources=JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert.equal(sources.length,2);
  assert.equal(new Set(sources.map(source=>source.id)).size,2,'The independent cohort fixture requires two distinct FHIR IDs');
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
  const members=(await api(selections+'/'+selection.id+'?limit=100')).members;
  const cohort=await api(selections+'/'+selection.id+'/explicit-groups',{snapshotToken:builder.catalog.snapshotToken,idempotencyKey:randomUUID(),groups:[{id:'qa-cohort',label:'Two Specimens',ordinal:0,memberIds:members.map(m=>m.memberKey)}]});
  report.cohort=cohort;
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
  await click(browser.cdp,'button',{name:'Columns'});
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
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
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
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
