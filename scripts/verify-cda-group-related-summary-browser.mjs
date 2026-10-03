import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const groupedRelatedFilter = process.env.LOOM_GROUPED_RELATED_FILTER === '1';
const groupedFilterDirect = process.env.LOOM_GROUPED_FILTER_DIRECT === '1';
assert(!groupedFilterDirect || groupedRelatedFilter, 'Direct grouped filtering requires LOOM_GROUPED_RELATED_FILTER=1');
const expandAfterGroup = process.env.LOOM_EXPAND_AFTER_GROUP === '1';
assert(!groupedRelatedFilter || expandAfterGroup, 'Grouped related filtering requires LOOM_EXPAND_AFTER_GROUP=1');
const zeroMatches = process.env.LOOM_RELATED_ZERO === '1';
const absentID = `loom-summary-no-match-${randomUUID()}`;
const resultForm = process.env.LOOM_RELATED_FORM ?? 'COUNT';
assert(['COUNT','PRESENCE'].includes(resultForm), 'LOOM_RELATED_FORM must be COUNT or PRESENCE');
const collectionRoundTrip=process.env.LOOM_COLLECTION_ROUND_TRIP==='1';
const upstreamGroupEdit=process.env.LOOM_UPSTREAM_GROUP_EDIT==='1';
const summaryShape=process.env.LOOM_SUMMARY_SHAPE??'GROUP';
assert(['GROUP','PIVOT','UNPIVOT'].includes(summaryShape),'LOOM_SUMMARY_SHAPE must be GROUP, PIVOT, or UNPIVOT');
assert(!collectionRoundTrip||(summaryShape==='GROUP'&&!upstreamGroupEdit&&!zeroMatches&&resultForm==='COUNT'),'Collection round trip requires unchanged Group and positive COUNT');
assert(!upstreamGroupEdit||summaryShape==='GROUP','Upstream Group edit requires GROUP shape');
const project = 'loom_dev_cda_fhir';
const explorer = `group-related-summary-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-group-related-summary-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { groupedFilterDirect, groupedRelatedFilter, expandAfterGroup, collectionRoundTrip, upstreamGroupEdit, summaryShape, resultForm, zeroMatches, explorer, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
const failedResponses=[];
const networkRequests=new Map();
const nativeById=new Map();
const pendingNetworkReads=new Set();
const failedRequests=new Map();
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `group-related-summary-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows) => {
  const deadline = start + 5000;
  let response;
  while (!(response = report.nativeRequests.findLast(request =>
    /\/construction-(?:choice-)?proposals$/.test(request.path) &&
    request.startedAt >= start && request.completedAt && request.response))) {
    assert(Date.now() < deadline, `${name} did not complete a fresh proposal within five seconds`);
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  if (response.status === 200 && response.response.proposalId) {
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === ${JSON.stringify(response.response.proposalId)}`);
  }
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
  if(expandAfterGroup) assert.equal(new Set(result.rows.map(row=>JSON.stringify(row))).size,result.rows.length,'Grouped-source preview must not duplicate terminal rows');
  for (const row of result.rows) assert(permitted.has(JSON.stringify(row)), 'Preview row must match an independent CDA relationship witness: '+JSON.stringify(row));
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async (expectedRows, columnCount) => {
  const start = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await waitForBrowser(browser.cdp,`!document.body.innerText.includes('Loading your table…')`);
  const deadline=start+5000;
  while(!report.nativeRequests.some(request=>request.path===base+'/preview'&&request.startedAt>=start&&request.completedAt&&request.status===200)) {
    assert(Date.now()<deadline,'Apply did not complete a fresh saved preview within five seconds');
    await new Promise(resolve=>setTimeout(resolve,50));
  }
  await rendered(expectedRows, columnCount);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async (expectedRows, columnCount) => {
  const start = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await rendered(expectedRows, columnCount);
  recordRender('load-to-render', start);
};
const rendered = async (expectedRows, columnCount = expectedRows[0]?.length ?? 2) => {
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === ${JSON.stringify(String(columnCount))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  if(expectedRows.length<=5) assert.equal(rows.length,expectedRows.length);
  else assert(rows.length>0&&rows.length<=Math.min(25,expectedRows.length),'Visible virtualized rows must fit the preview page');
  if(expandAfterGroup) assert.equal(new Set(rows.map(row=>JSON.stringify(row))).size,rows.length,'Grouped-source terminal union must not duplicate rows');
  const savedRows = expectedRows;
  for (const row of rows) assert(savedRows.some(expected=>row.length===expected.length&&row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 FOR e IN fhir_edge FILTER e._from == s._id AND e.label == "subject_Patient" AND e.project == s.project AND e.dataset_generation == s.dataset_generation FILTER STARTS_WITH(e._to,"Patient/") LET members=(FOR se IN fhir_edge FILTER se._to == e._to AND se.label == "subject_Patient" AND se.project == s.project AND se.dataset_generation == s.dataset_generation FILTER STARTS_WITH(se._from,"Specimen/") LIMIT 2 LET d=DOCUMENT(se._from) FILTER d.project == s.project AND d.dataset_generation == s.dataset_generation RETURN {id:d.id,_id:d._id}) FILTER LENGTH(members)==2 RETURN {id:members[0].id,_id:members[0]._id,resourceType:"Specimen",generation:s.dataset_generation,sources:members}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Group Add fields QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Group QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  if(summaryShape==='PIVOT'){
    const kindField=builder.catalog.candidates.find(candidate=>candidate.nodeId===node.nodeId&&candidate.fieldPath==='resourceType');
    assert(kindField,'Pivot fixture needs an independent source grouping field');
    await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:kindField.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:'Specimen resource type'}]);
  }
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: source.sources.map(member=>({ project, generation: source.generation, resourceType: 'Specimen', id: member.id })) } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', e => report.errors.push({kind:'runtime',details:e.exceptionDetails}));
  browser.cdp.on('Runtime.consoleAPICalled', e => {if(e.type==='error')report.errors.push({kind:'console',args:e.args});});
  browser.cdp.on('Network.requestWillBeSent',({requestId,request,wallTime})=>{
    const path=new URL(request.url).pathname;
    if(path.startsWith(root+'/'+explorer+'/')) {
      const entry={requestId,path,method:request.method,startedAt:wallTime?Math.round(wallTime*1000):Date.now()};
      if(request.postData) {try {entry.body=JSON.parse(request.postData);} catch {entry.body=request.postData.slice(0,32768);}}
      nativeById.set(requestId,entry);report.nativeRequests.push(entry);
    }
    if(request.url.includes('/related-expand-choices') || request.url.includes('/construction-choice-proposals') || request.url.includes('/construction-proposals'))networkRequests.set(requestId,request.postData);
  });
  browser.cdp.on('Network.loadingFinished',({requestId})=>{
    const entry=nativeById.get(requestId);
    if(entry) entry.completedAt=Date.now();
    if(entry&&(/proposal|preview|commands/.test(entry.path)||entry.status>=400)) {
      const read=browser.cdp.send('Network.getResponseBody',{requestId}).then(result=>{
        const body=result.base64Encoded?Buffer.from(result.body,'base64').toString('utf8'):result.body;
        if(body.length>32768)entry.response={truncated:true,length:body.length,text:body.slice(0,32768)};
        else {try {entry.response=JSON.parse(body);} catch {entry.response=body;}}
      }).catch(error=>{entry.responseReadError=String(error);}).finally(()=>pendingNetworkReads.delete(read));
      pendingNetworkReads.add(read);
    }
    const error=failedRequests.get(requestId);
    if(error)failedResponses.push(browser.cdp.send('Network.getResponseBody',{requestId}).then(body=>{error.body=body.body;}).catch(e=>{error.bodyError=String(e);}));
  });
  browser.cdp.on('Network.responseReceived', ({response,requestId})=>{
    const entry=nativeById.get(requestId);
    if(entry){entry.status=response.status;entry.responseReceivedAt=Date.now();entry.serverRequestId=Object.entries(response.headers).find(([name])=>name.toLowerCase()==='x-request-id')?.[1];}
    if(response.status>=400&&!response.url.endsWith('/favicon.ico')){
      const error={kind:'http',url:response.url,status:response.status,observedAfter:report.cases.at(-1)?.name,request:networkRequests.get(requestId)};
      report.errors.push(error);
      failedRequests.set(requestId,error);
    }
  });
  browser.cdp.on('Network.loadingFailed', e=>{if(e.type==='Script'&&e.errorText!=='net::ERR_ABORTED')report.errors.push({kind:'module',error:e.errorText});});
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',process.env.LOOM_ARANGO_CONTAINER??'loom-dev-6d7df93d6a37-arangodb-1','arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
    assert.equal(r.status,0,r.stderr);return JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  };
  assert.equal(source.sources.length,2);
  let witnesses=source.sources.map(member=>({anchor:member._id,values:summaryShape==='PIVOT'?[member.id,'Specimen']:[member.id]}));
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Observation',label:'subject_Patient',field:'subject',direction:'INBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches=witness.anchor?rawQuery(query):[];
      if(matches.length)for(const match of matches)next.push({anchor:match._id,values:[...witness.values,match.id]});
      else next.push({anchor:null,values:[...witness.values,'—']});
    }
    assert(next.length<=1000,'Use a bounded CDA chain fixture');
    witnesses=next;expected=witnesses.map(w=>w.values);
    report.oracle.chain??=[];report.oracle.chain.push({hop,witnesses});
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`);
    await click(browser.cdp,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(browser.cdp,`document.querySelector('${panel} select[aria-label="Related record type"]')?.disabled===false`);
    let start=Date.now();
    await selectOption(browser.cdp,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(browser.cdp,`document.querySelector(${JSON.stringify(panel+' input[aria-label="'+label+'"]')})`,5000);
    await click(browser.cdp,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  assert(expected.length>1,'CDA fixture must exercise many related records before grouping');
  const patientIndex=summaryShape==='PIVOT'?2:1;
  assert.equal(new Set(witnesses.map(w=>w.values[patientIndex])).size,1,'Sibling Specimens must share the same Patient grouping key');
  const grouped=[summaryShape==='PIVOT'?[witnesses[0].values[patientIndex],'Specimen',String(expected.length)]:[witnesses[0].values[patientIndex],String(expected.length)]];
  await open(expected);
  const configureGroup=async()=>{
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await click(browser.cdp,'[data-testid="construction-action-group-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]:not(:disabled)')`);
    start=Date.now();
    await click(browser.cdp,'input[aria-label="Group by Patient FHIR resource ID"]');
    if(summaryShape==='PIVOT') await click(browser.cdp,'input[aria-label="Group by Specimen resource type"]');
  };
  let start;
  await configureGroup();
  await proposal('related-many-group-preview',start,grouped);
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configureGroup();
  await proposal('confirmed-related-many-group-preview',start,grouped);
  await apply(grouped);
  await open(grouped);
  if (expandAfterGroup) {
    assert.equal(summaryShape, 'GROUP');
    const groupedBuilder = builder;
    const relatedPatients = rawQuery(`FOR e IN fhir_edge FILTER e._from IN ${JSON.stringify(source.sources.map(member=>member._id))} AND e.label=="subject_Patient" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" FILTER STARTS_WITH(e._to,"Patient/") LET d=DOCUMENT(e._to) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`);
    assert.equal(relatedPatients.length, 1, 'The two source members must share one terminal Patient');
    const patientRows = relatedPatients.map(patient=>[...grouped[0],patient.id]);
    const distinctPriorObservationIds=new Set(witnesses.map(witness=>witness.anchor));
    assert(!distinctPriorObservationIds.has(null),'The positive grouped expansion fixture must retain exact prior terminal identities');
    report.oracle.groupedExpansion = {relatedPatients,patientRows,priorRows:witnesses.length,distinctPriorObservationCount:distinctPriorObservationIds.size};
    const verifyRelatedFilter = async filterRows => {
      const beforeRelatedFilter=builder;
      const configureRelatedFilter=async()=>{
        const started=Date.now();
        await click(browser.cdp,'[data-testid="construction-action-keep-rows"]');
        await waitForBrowser(browser.cdp,`[...document.querySelectorAll('button')].some(button=>button.innerText==='Related records'&&!button.disabled)`);
        await click(browser.cdp,'button',{name:'Related records'});
        await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="Related eligibility record type"]:not(:disabled)')`);
        const anchorSelector=await browserEval(browser.cdp,`return Boolean(document.querySelector('select[aria-label="Related eligibility anchor"]'));`);
        if(anchorSelector) await selectOption(browser.cdp,'select[aria-label="Related eligibility anchor"]','__loom_root_contributor_keys');
        const startingRecords=await browserEval(browser.cdp,`const editor=document.querySelector('[data-testid="construction-related-eligibility-editor"]');return {text:editor.innerText,anchor:editor.querySelector('select[aria-label="Related eligibility anchor"]')?.value};`);
        assert(startingRecords.text.includes('Start from')&&startingRecords.text.includes('Specimen'),'Related filtering must show its contributing source records without an extra click');
        if(anchorSelector) assert.equal(startingRecords.anchor,'__loom_root_contributor_keys');
        report.relatedFilterStartingRecords=startingRecords;
        await selectOption(browser.cdp,'select[aria-label="Related eligibility record type"]','Patient');
        await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Specimen -[subject]-> Patient"]:not(:disabled)')`,5000);
        await click(browser.cdp,'input[aria-label="Specimen -[subject]-> Patient"]');
        await proposal('grouped-contributors-related-filter-exists',started,filterRows);
        start=Date.now();
        await selectOption(browser.cdp,'select[aria-label="Related eligibility rule"]','ABSENT');
        await proposal('grouped-contributors-related-filter-absent',start,[]);
        start=Date.now();
        await selectOption(browser.cdp,'select[aria-label="Related eligibility rule"]','EXISTS');
        await proposal('grouped-contributors-related-filter-exists-restored',start,filterRows);
        start=Date.now();
        await selectOption(browser.cdp,'select[aria-label="Related eligibility rule"]','COUNT_AT_LEAST');
        const countExplanation=await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-related-eligibility-editor"]').innerText;`);
        assert(countExplanation.includes('Each matching related record is counted once per current row, even if several starting records link to it.'),'Distinct-match counting must be explained in the editor');
        await proposal('grouped-contributors-related-filter-count-distinct-two',start,[]);
      };
      await configureRelatedFilter();
      await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
      await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
      await rendered(filterRows);
      assert.deepEqual((await api(base+'/builder')).workspace,beforeRelatedFilter.workspace);
      await configureRelatedFilter();
      await apply([], filterRows[0].length);
      await open([], filterRows[0].length);
      const relatedFilter=doc(builder).construction.steps.at(-1);
      assert.equal(relatedFilter.operation.kind,'RELATED_ELIGIBILITY');
      assert.equal(relatedFilter.operation.relatedEligibility.anchorColumnId,'__loom_root_contributor_keys');
      await click(browser.cdp,`[data-testid="construction-history-step-${relatedFilter.id}"]`);
      await click(browser.cdp,`[data-testid="construction-edit-step-${relatedFilter.id}"]`);
      await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Minimum matching records"]:not(:disabled)')`);
      start=Date.now();
      await browserEval(browser.cdp,`const input=document.querySelector('input[aria-label="Minimum matching records"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'1');input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
      await proposal('edit-grouped-related-filter-count-one',start,filterRows);
      await apply(filterRows);
      await open(filterRows);
      await click(browser.cdp,`[data-testid="construction-history-step-${relatedFilter.id}"]`);
      start=Date.now();
      await click(browser.cdp,`[data-testid="construction-remove-step-${relatedFilter.id}"]`);
      await proposal('remove-grouped-related-filter',start,filterRows);
      await apply(filterRows);
      await open(filterRows);
      assert.deepEqual(doc(builder).construction,doc(beforeRelatedFilter).construction,'Removing related filtering must restore exact grouped expansion');
    };
    if(groupedRelatedFilter && groupedFilterDirect) await verifyRelatedFilter(grouped);
    const configureRelated = async () => {
      const started = Date.now();
      await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-action-related-rows"]')`);
      const action=await browserEval(browser.cdp,`const button=document.querySelector('[data-testid="construction-action-related-rows"]');return {disabled:button.disabled,text:button.innerText};`);
      assert(!action.disabled, 'Grouped rows retain source members but related expansion is disabled: '+action.text);
      await click(browser.cdp,'[data-testid="construction-action-related-rows"]');
      await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]')?.disabled===false`);
      const explanation=await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-related-expand-editor"]').innerText;`);
      assert(explanation.includes('Start from')&&explanation.includes('Specimen'),'Grouped expansion must clearly identify the contributing source record type');
      report.groupedStartingRecords=explanation;
      await selectOption(browser.cdp,'[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]','Patient');
      await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Specimen -[subject]-> Patient"]:not(:disabled)')`,5000);
      await click(browser.cdp,'input[aria-label="Specimen -[subject]-> Patient"]');
      await proposal('grouped-source-union-related-preview',started,patientRows);
    };
    await configureRelated();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await rendered(grouped);
    assert.deepEqual((await api(base+'/builder')).workspace,groupedBuilder.workspace,'Cancel must retain grouped membership and construction');
    await configureRelated();
    await apply(patientRows);
    await open(patientRows);
    const groupedExpansion=doc(builder).construction.steps.at(-1);
    assert.equal(groupedExpansion.operation.kind,'RELATED_EXPAND');
    assert.equal(groupedExpansion.operation.relatedExpand.anchorColumnId,'__loom_root_contributor_keys');
    if(groupedRelatedFilter && !groupedFilterDirect) await verifyRelatedFilter(patientRows);
    const patientBuilder=builder;
    const onwardMatches=rawQuery(`FOR e IN fhir_edge FILTER e._to==${JSON.stringify(relatedPatients[0]._id)} AND e.label=="subject_Patient" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" FILTER STARTS_WITH(e._from,"Observation/") LET d=DOCUMENT(e._from) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`);
    assert(onwardMatches.length>1&&onwardMatches.length<=1000,'Use bounded one-to-many onward CDA matches');
    assert.deepEqual(onwardMatches.map(match=>match._id).sort(),[...distinctPriorObservationIds].sort(),'Onward expansion must recover the distinct source-witness terminal set, not the duplicated pre-Group rows');
    const onwardRows=onwardMatches.map(match=>[...patientRows[0],match.id]);
    report.oracle.groupedExpansion.onward={matches:onwardMatches,rows:onwardRows};
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await click(browser.cdp,'[data-testid="construction-action-related-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="Related record type"]')?.disabled===false`);
    start=Date.now();
    await selectOption(browser.cdp,'select[aria-label="Related record type"]','Observation');
    await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Patient <-[subject]- Observation"]:not(:disabled)')`,5000);
    await click(browser.cdp,'input[aria-label="Patient <-[subject]- Observation"]');
    await proposal('onward-from-grouped-related-identity',start,onwardRows);
    await apply(onwardRows);
    const acceptedOnwardPreview=report.nativeRequests.filter(request=>request.path===base+'/preview'&&request.status===200&&request.completedAt).at(-1);
    assert(acceptedOnwardPreview?.body?.receiptId);
    const acceptedCompletePreview=await api(base+'/preview',{receiptId:acceptedOnwardPreview.body.receiptId,outputId,limit:100});
    await open(onwardRows);
    const lastSavedPreview=report.nativeRequests.filter(request=>request.path===base+'/preview'&&request.status===200&&request.completedAt).at(-1);
    assert(lastSavedPreview?.body?.receiptId,'A native saved preview must supply the exact receipt for the full identity check');
    const completePreview=await api(base+'/preview',{receiptId:lastSavedPreview.body.receiptId,outputId,limit:100});
    assert.equal(completePreview.rows.length,onwardRows.length,'The full saved result must contain each distinct terminal once');
    assert.equal(completePreview.rowCount,onwardRows.length);
    const fullCells=completePreview.rows.map(row=>completePreview.columns.map(column=>row[column.column]==null?'—':String(row[column.column])));
    assert.deepEqual(fullCells.map(row=>JSON.stringify(row)).sort(),onwardRows.map(row=>JSON.stringify(row)).sort(),'All saved rows must match the independent CDA terminal witnesses');
    assert.deepEqual(completePreview.rows,acceptedCompletePreview.rows,'Reload must preserve every expanded row value and identity');
    const identities=completePreview.rows.map(row=>row.__loom_row_id);
    assert(identities.every(identity=>typeof identity==='string'&&identity.length>0),'Expanded rows must carry exact row identities');
    assert.equal(new Set(identities).size,identities.length,'Every grouped onward terminal must have a distinct row identity');
    report.completeGroupedOnwardPreview=completePreview;
    const onwardStep=doc(builder).construction.steps.at(-1);
    assert.equal(onwardStep.operation.kind,'RELATED_EXPAND');
    assert.notEqual(onwardStep.operation.relatedExpand.anchorColumnId,'__loom_root_contributor_keys','Onward expansion must start from the exact active Patient identity');
    await click(browser.cdp,`[data-testid="construction-history-step-${onwardStep.id}"]`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-remove-step-${onwardStep.id}"]`);
    await proposal('remove-onward-grouped-expansion',start,patientRows);
    await apply(patientRows);
    await open(patientRows);
    assert.deepEqual(doc(builder).construction,doc(patientBuilder).construction,'Onward removal must restore exact prior grouped expansion');
    await click(browser.cdp,`[data-testid="construction-history-step-${groupedExpansion.id}"]`);
    await click(browser.cdp,`[data-testid="construction-edit-step-${groupedExpansion.id}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="If a current row has no matches"]')`);
    start=Date.now();
    await selectOption(browser.cdp,'select[aria-label="If a current row has no matches"]','EXCLUDE');
    await proposal('edit-grouped-related-empty-policy',start,patientRows);
    await apply(patientRows);
    await open(patientRows);
    assert.equal(doc(builder).construction.steps.at(-1).operation.relatedExpand.emptyPolicy,'EXCLUDE');
    await click(browser.cdp,`[data-testid="construction-history-step-${groupedExpansion.id}"]`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-remove-step-${groupedExpansion.id}"]`);
    await proposal('remove-grouped-related-preview',start,grouped);
    await apply(grouped);
    await open(grouped);
    assert.deepEqual(doc(builder).construction,doc(groupedBuilder).construction,'Removal must restore the exact grouped construction');
  } else {
  let shaped=grouped;
  if(summaryShape!=='GROUP'){
    const beforeShape=builder;
    shaped=summaryShape==='PIVOT'?[['Specimen',String(expected.length)]]:[[grouped[0][0],'Row count',String(expected.length)]];
    const configureShape=async()=>{
      const discoveryStart=Date.now();
      await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
      if(summaryShape==='PIVOT'){
        await click(browser.cdp,'[data-testid="construction-action-pivot-rows"]');
        await waitForBrowser(browser.cdp,`document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')`);
        await click(browser.cdp,'input[aria-label="Pivot group Specimen resource type"]');
        const chooseField=async(label,prefix)=>{
          const options=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="${label}"]').options].map(option=>({value:option.value,label:option.textContent}));`);
          const field=options.find(option=>option.label.startsWith(prefix));assert(field,JSON.stringify(options));
          await selectOption(browser.cdp,`select[aria-label="${label}"]`,field.value);
        };
        await chooseField('Pivot category field','Patient FHIR resource ID');
        start=Date.now();
        await chooseField('Pivot values field','Row count');
      }else{
        await click(browser.cdp,'button',{name:'Turn columns into rows'});
        await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Unpivot Row count"]:not(:disabled)')`);
        start=Date.now();
        await click(browser.cdp,'input[aria-label="Unpivot Row count"]');
      }
      await proposal(summaryShape.toLowerCase()+'-preview',start,shaped);
      recordRender(summaryShape.toLowerCase()+'-discovery-to-preview',discoveryStart);
    };
    await configureShape();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await rendered(grouped);
    assert.deepEqual((await api(base+'/builder')).workspace,beforeShape.workspace);
    await configureShape();
    await apply(shaped);
    await open(shaped);
  }
  const openRelatedFields=async()=>{
  await click(browser.cdp,'[data-testid="construction-action-add-columns"]');
  await click(browser.cdp,'[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-add-columns-source"]')`);
  report.addFieldsUI=await browserEval(browser.cdp, `return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))};`);
  if(!await browserEval(browser.cdp,`return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open;`)){
    await click(browser.cdp,'[aria-label="Related resources"] summary');
  }
  await click(browser.cdp,'[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  if(!await browserEval(browser.cdp,`return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open;`)){
    await click(browser.cdp,'[data-testid="feature-catalog-raw-fields"] summary');
  }
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)')`);
  };
  await openRelatedFields();
  const beforeField=builder;
  const matchingRecords=new Map(witnesses.filter(witness=>witness.anchor!==null).map(witness=>[witness.anchor,witness.values.at(-1)]));
  assert(![...matchingRecords.values()].includes(absentID),'Zero-match predicate must exclude every independently observed target');
  const matchingIDs=zeroMatches?[]:[...matchingRecords.keys()].sort();
  report.oracle.matchingRecordIDs=matchingIDs;
  const expectedSummary=resultForm==='COUNT'?matchingIDs.length:matchingIDs.length>0;
  let withField=[[...shaped[0],String(expectedSummary)]];
  const configureRelatedField=async()=>{
    const selectionStart=Date.now();
    await click(browser.cdp,'input[aria-label="Select Observation.id"]');
    await click(browser.cdp,'[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(browser.cdp,`document.querySelector('[role="dialog"]')`);
    if(!await browserEval(browser.cdp,`return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open;`)){
      await click(browser.cdp,'[role="dialog"] summary',{includes:'Other relationship paths'});
    }
    await click(browser.cdp,'[role="dialog"] input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]');
    start=Date.now();
    await click(browser.cdp,`[role="dialog"] input[aria-label="Observation ID: ${resultForm==='COUNT'?'Count matching records':'Show whether a match exists'}"]`);
    if(zeroMatches){
      if(!await browserEval(browser.cdp,`return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Matching records:'))?.parentElement.open;`)){
        await click(browser.cdp,'[role="dialog"] summary',{includes:'Matching records:'});
      }
      await click(browser.cdp,'[role="dialog"] label',{includes:'Only records where Observation ID equals'});
      await click(browser.cdp,'input[aria-label="Observation ID exact value"]');
      await browser.cdp.send('Input.insertText',{text:absentID});
    }
    await click(browser.cdp,'[role="dialog"] button',{name:'Add 1 column'});
    await waitForBrowser(browser.cdp,`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus) || document.querySelector('[role="dialog"]')?.innerText.includes('root document identity')`);
    report.relatedProposal=await browserEval(browser.cdp,`const p=document.querySelector('[data-testid="construction-proposal-panel"]')??document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p?.dataset.proposalStatus,text:p?.innerText??document.querySelector('[role="dialog"]')?.innerText};`);
    assert.equal(report.relatedProposal.status,'ready',report.relatedProposal.text);
    const cells=await browserEval(browser.cdp,`return [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')].map(cell=>({text:cell.innerText,raw:cell.title}));`);
    assert.equal(cells.length,shaped[0].length+1);
    assert.deepEqual(cells.slice(0,-1).map(cell=>cell.text),shaped[0]);
    assert.equal(cells.at(-1).text,String(expectedSummary));
    recordRender(summaryShape.toLowerCase()+'-related-'+resultForm.toLowerCase()+'-preview',start);
    recordRender('related-field-discovery-to-preview',selectionStart);
  };
  const proposalPanel=async()=>await browserEval(browser.cdp,`return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?'construction-choice-proposal-panel':'construction-proposal-panel';`);
  await configureRelatedField();
  await click(browser.cdp,`[data-testid="${await proposalPanel()}"] button`,{name:'Cancel'});
  await rendered(shaped);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  await openRelatedFields();
  await configureRelatedField();
  start=Date.now();
  const panel=await proposalPanel();
  if(panel==='construction-choice-proposal-panel') await click(browser.cdp,'[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  else await click(browser.cdp,'[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="${panel}"]')`);
  await rendered(withField);
  recordRender('group-related-summary-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  await open(withField);
  const addedStep=doc(builder).construction.steps.find(step=>!doc(beforeField).construction.steps.some(prior=>prior.id===step.id));
  assert.equal(addedStep?.operation.kind,'RELATED_SOURCE','Summary must retain its related-record operation');
  assert.equal(addedStep.operation.relatedSource.form,resultForm);
  if(collectionRoundTrip){
    const beforeCollection=builder;
    const collectionSettingsStart=Date.now();
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled)`);
    recordRender('starting-collection-controls-ready',collectionSettingsStart);
    const clearStart=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use all authorized rows'});
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use selected resources'&&!button.disabled) && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='26' && !document.body.innerText.includes('Loading your table…') || document.body.innerText.includes('Preview failed:')`);
    const collectionError=await browserEval(browser.cdp,`return document.body.innerText.split(String.fromCharCode(10)).find(line=>line.startsWith('Preview failed:'));`);
    assert.equal(collectionError,undefined,'Changing the starting collection must render the authored dataframe: '+collectionError);
    recordRender('clear-collection-under-group-related-summary',clearStart);
    const visible=await browserEval(browser.cdp,`const scroll=document.querySelector('[data-testid="preview-table-scroll"]');
      const rows=new Map();
      for(let page=0;page<25;page++){
        for(const row of scroll.querySelectorAll('[role="row"]')){
          const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim());
          if(cells.length) rows.set(cells[0],cells);
        }
        if(rows.size===25) break;
        const next=Math.min(scroll.scrollTop+Math.max(1,scroll.clientHeight/2),scroll.scrollHeight-scroll.clientHeight);
        if(next===scroll.scrollTop) break;
        scroll.scrollTop=next;
        await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
      }
      scroll.scrollTop=0;
      return [...rows.values()];`);
    assert.equal(visible.length,25,'Independent source checks must cover every virtualized preview row');
    assert(visible.every(row=>row.length===3&&row[0]!=='—'),'This oracle requires rendered Patient groups');
    const patientIDs=visible.map(row=>row[0]);
    const independent=rawQuery(`LET scopedPatients=(FOR p IN Patient
      FILTER p.id IN ${JSON.stringify(patientIDs)} AND p.resourceType=="Patient" AND p.project=="${project}" AND p.dataset_generation=="cda-fhir-v1"
      RETURN {id:p.id,_id:p._id,project:p.project,dataset_generation:p.dataset_generation})
      FOR groupID IN ${JSON.stringify(patientIDs)}
      LET contributors=(FOR p IN scopedPatients FILTER p.id==groupID
        LET specimens=(FOR edge IN fhir_edge
          FILTER edge._to==p._id AND edge.label=="subject_Patient" AND edge.to_type=="Patient" AND edge.project==p.project AND edge.dataset_generation==p.dataset_generation
          FILTER IS_SAME_COLLECTION("Specimen",edge._from)
          LET s=DOCUMENT(edge._from) FILTER s!=null AND s.project==p.project AND s.dataset_generation==p.dataset_generation
          RETURN DISTINCT s._id)
        LET observations=(FOR edge IN fhir_edge
          FILTER edge._to==p._id AND edge.label=="subject_Patient" AND edge.from_type=="Observation" AND edge.project==p.project AND edge.dataset_generation==p.dataset_generation
          LET o=DOCUMENT(edge._from) FILTER o!=null AND o.resourceType=="Observation" AND o.project==p.project AND o.dataset_generation==p.dataset_generation
          RETURN DISTINCT o._id)
        RETURN {specimens,rows:LENGTH(specimens)*MAX([1,LENGTH(observations)])})
      LET roots=SORTED_UNIQUE(FLATTEN(contributors[*].specimens,1))
      LET related=(FOR rootID IN roots FOR patientEdge IN fhir_edge
        FILTER patientEdge._from==rootID AND patientEdge.label=="subject_Patient" AND patientEdge.to_type=="Patient" AND patientEdge.project=="${project}" AND patientEdge.dataset_generation=="cda-fhir-v1"
        LET p=DOCUMENT(patientEdge._to) FILTER p!=null AND p.resourceType=="Patient" AND p.project==patientEdge.project AND p.dataset_generation==patientEdge.dataset_generation
        FOR observationEdge IN fhir_edge
          FILTER observationEdge._to==p._id AND observationEdge.label=="subject_Patient" AND observationEdge.from_type=="Observation" AND observationEdge.project==p.project AND observationEdge.dataset_generation==p.dataset_generation
          LET o=DOCUMENT(observationEdge._from) FILTER o!=null AND o.resourceType=="Observation" AND o.project==p.project AND o.dataset_generation==p.dataset_generation
          RETURN DISTINCT o._id)
      RETURN [groupID,TO_STRING(SUM(contributors[*].rows)),TO_STRING(LENGTH(related))]`);
    assert.deepEqual([...visible].sort(),independent.sort(),'Group multiplicity and distinct related counts must recompute from scoped raw source records');
    report.fullCollectionOracle={visible,independent};
    builder=await api(base+'/builder');
    assert.equal(doc(builder).population,undefined);
    assert.deepEqual(doc(builder).construction,doc(beforeCollection).construction,'Changing the starting collection must preserve authored operations');
    const attachStart=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use selected resources'});
    await rendered(withField);
    await waitForBrowser(browser.cdp,`[...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled)`);
    recordRender('reattach-collection-under-group-related-summary',attachStart);
    builder=await api(base+'/builder');
    assert.deepEqual(doc(builder).population,doc(beforeCollection).population);
    assert.deepEqual(doc(builder).construction,doc(beforeCollection).construction);
    await open(withField);
  }
  let restoredConstruction=doc(beforeField).construction;
  if(upstreamGroupEdit){
    const beforeEdit=builder;
    const groupStep=doc(builder).construction.steps.find(step=>step.operation.kind==='GROUP');
    assert(groupStep,'The saved Group must exist');
    const editedShape=[[shaped[0].at(-1)]];
    const editedWithField=[[...editedShape[0],String(expectedSummary)]];
    const editGroup=async()=>{
      await click(browser.cdp,`[data-testid="construction-history-step-${groupStep.id}"]`);
      await click(browser.cdp,`[data-testid="construction-edit-step-${groupStep.id}"]`);
      await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]:not(:disabled)')`);
      assert(await browserEval(browser.cdp,`return document.querySelector('input[aria-label="Group by Patient FHIR resource ID"]').checked;`));
      start=Date.now();
      await click(browser.cdp,'input[aria-label="Group by Patient FHIR resource ID"]');
      await proposal('upstream-group-key-removal-retains-summary',start,editedWithField);
      assert.equal(await browserEval(browser.cdp,`return document.querySelectorAll('[data-testid^="construction-removal-step-"]').length;`),0,'Removing a visible key must retain a summary anchored to contributing records');
    };
    await editGroup();
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await rendered(withField);
    assert.deepEqual((await api(base+'/builder')).workspace,beforeEdit.workspace);
    await editGroup();
    await apply(editedWithField);
    await open(editedWithField);
    const saved=doc(builder).construction;
    assert.equal((saved.steps.find(step=>step.id===groupStep.id).operation.group.keys??[]).length,0);
    const savedSummary=saved.steps.find(step=>step.id===addedStep.id);
    assert(savedSummary,'The related summary must survive upstream Group editing');
    assert.deepEqual(savedSummary.operation,addedStep.operation,'The signed source route and contributor predicate must survive the edit');
    shaped=editedShape;
    withField=editedWithField;
    restoredConstruction={...saved,steps:saved.steps.filter(step=>step.id!==addedStep.id)};
    report.upstreamEditedDocument=doc(builder);
  }
  if(addedStep){
    await click(browser.cdp,`[data-testid="construction-history-step-${addedStep.id}"]`);
    await click(browser.cdp,`[data-testid="construction-edit-step-${addedStep.id}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]:not(:disabled)')`);
    const columnID=addedStep.operation.relatedSource.outputColumnId;
    const originalLabel=addedStep.outputs.find(column=>column.id===columnID).label;
    await click(browser.cdp,'[data-testid="related-source-step-editor"] input[aria-label="Output column label"]');
    start=Date.now();
    await browser.cdp.send('Input.insertText',{text:' QA'});
    await proposal('edit-related-summary-label-preview',start,withField);
    await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
    await rendered(withField);
    assert.deepEqual((await api(base+'/builder')).workspace,builder.workspace,'Cancel edit must preserve the saved summary');
    await click(browser.cdp,`[data-testid="construction-history-step-${addedStep.id}"]`);
    await click(browser.cdp,`[data-testid="construction-edit-step-${addedStep.id}"]`);
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]:not(:disabled)')`);
    await click(browser.cdp,'[data-testid="related-source-step-editor"] input[aria-label="Output column label"]');
    start=Date.now();
    await browser.cdp.send('Input.insertText',{text:' QA'});
    const editedLabel=await browserEval(browser.cdp,`return document.querySelector('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]').value;`);
    assert.notEqual(editedLabel,originalLabel);
    assert.equal(editedLabel.replace(' QA',''),originalLabel,'Native typing must preserve the original label around the inserted text');
    await proposal('confirmed-edit-related-summary-label-preview',start,withField);
    await apply(withField);
    await open(withField);
    assert.equal(doc(builder).construction.steps.find(step=>step.id===addedStep.id).outputs.find(column=>column.id===columnID).label,editedLabel);
    await click(browser.cdp,`[data-testid="construction-history-step-${addedStep.id}"]`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-remove-step-${addedStep.id}"]`);
    await proposal('remove-related-summary-preview',start,shaped);
    await apply(shaped);
  } else {
    await click(browser.cdp,'button',{name:'Columns'});
    start=Date.now();
    await click(browser.cdp,'button[aria-label="Remove Observation ID column"]');
    await rendered(shaped);
    recordRender('remove-group-related-summary',start);
  }
  builder=await api(base+'/builder');
  await open(shaped);
  assert.deepEqual(doc(builder).construction,restoredConstruction);
  }
  assert.deepEqual(report.errors,[]);
  report.status='passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.savedBuilderAtFailure=await api(base+'/builder').catch(error=>({readError:String(error)}));
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  await Promise.all([...failedResponses,...pendingNetworkReads]);
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
