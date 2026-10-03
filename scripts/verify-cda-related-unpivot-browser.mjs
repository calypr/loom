import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const explorer = `related-unpivot-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-related-unpivot-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, cases: [], errors: [], requests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
const failedResponses=[];
const networkRequests=new Map();
const failedRequests=new Map();
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-unpivot-browser-${randomUUID()}` },
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
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
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
const apply = async expectedRows => {
  const start = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
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
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  // Saved presentation puts the new Unpivot key/value columns first; proposal order follows stage outputs.
  const savedRows = expectedRows.map(row=>row.at(-2)==='Specimen ID'?[...row.slice(-2),...row.slice(0,-2)]:row);
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Unpivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Unpivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', e => report.errors.push({kind:'runtime',details:e.exceptionDetails}));
  browser.cdp.on('Runtime.consoleAPICalled', e => {if(e.type==='error')report.errors.push({kind:'console',args:e.args});});
  browser.cdp.on('Network.requestWillBeSent',({requestId,request})=>{
    if(request.url.includes('/related-expand-choices'))networkRequests.set(requestId,request.postData);
  });
  browser.cdp.on('Network.loadingFinished',({requestId})=>{
    const error=failedRequests.get(requestId);
    if(error)failedResponses.push(browser.cdp.send('Network.getResponseBody',{requestId}).then(body=>{error.body=body.body;}).catch(e=>{error.bodyError=String(e);}));
  });
  browser.cdp.on('Network.responseReceived', ({response,requestId})=>{
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
  let witnesses=[{anchor:source._id,values:[source.id]}];
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Condition',label:'subject_Patient',field:'subject',direction:'INBOUND'},
    {from:'Condition',to:'Observation',label:'focus_Condition',field:'focus',direction:'INBOUND'},
    {from:'Observation',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
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
  await open(expected);
  await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp,`[...document.querySelectorAll('button')].some(b=>b.innerText==='Turn columns into rows'&&!b.disabled)`);
  await click(browser.cdp,'button',{name:'Turn columns into rows'});
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false`);
  let start=Date.now();
  await click(browser.cdp,'input[aria-label="Unpivot Specimen ID"]');
  const unpivotExpected=expected.map(row=>[...row.slice(1),'Specimen ID',row[0]]);
  await proposal('related-chain-unpivot-preview',start,unpivotExpected);
  assert.equal((await api(base+'/builder')).draftDigest,expanded.draftDigest);
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
  await click(browser.cdp,'button',{name:'Turn columns into rows'});
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false`);
  start=Date.now();
  await click(browser.cdp,'input[aria-label="Unpivot Specimen ID"]');
  await proposal('confirmed-related-chain-unpivot-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  const unpivot=doc(builder).construction.steps.find(s=>s.operation.kind==='UNPIVOT');
  assert(unpivot);
  await click(browser.cdp,`[data-testid="construction-history-step-${unpivot.id}"]`);
  await click(browser.cdp,`[data-testid="construction-edit-step-${unpivot.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.checked`);
  await click(browser.cdp,'[data-testid="construction-unpivot-advanced"] summary');
  start=Date.now();
  await selectOption(browser.cdp,'select[aria-label="Unpivot null row policy"]','DROP');
  await proposal('edit-unpivot-policy-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  assert.equal(doc(builder).construction.steps.find(s=>s.id===unpivot.id).operation.unpivot.nullRowPolicy,'DROP');
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    await click(browser.cdp,'[data-testid="construction-action-keep-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('${filterPanel} select[aria-label="Condition"]:not(:disabled)')`);
    const options=await browserEval(browser.cdp,`return [...document.querySelector('${filterPanel} select[aria-label="Column"]').options].map(o=>({value:o.value,label:o.textContent}));`);
    report.filterColumns=options;
    const value=options.find(o=>/^Value(?: \(|$)/.test(o.label));
    assert(value,'Unpivot Value must be available to Filter: '+JSON.stringify(options));
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Column"]',value.value);
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','EQUALS');
    start=Date.now();
    await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('unpivot-value-missing-preview',start,[]);
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeFilter.workspace);
  await configureMissing();
  await proposal('confirmed-unpivot-value-missing-preview',start,[]);
  await apply([]);
  await open([]);
  const filter=doc(builder).construction.steps.find(s=>s.operation.kind==='FILTER');
  assert(filter);
  await click(browser.cdp,`[data-testid="construction-history-step-${filter.id}"]`);
  await click(browser.cdp,`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('${filterPanel} select[aria-label="Condition"]:not(:disabled)')`);
  await selectOption(browser.cdp,filterPanel+' select[aria-label="Condition"]','EQUALS');
  await click(browser.cdp,filterPanel+' input[aria-label="Value"]');
  start=Date.now();
  await browser.cdp.send('Input.insertText',{text:source.id});
  await proposal('unpivot-value-equality-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  const beforeRemoval=builder;
  const removeUnpivot=async()=>{
    await click(browser.cdp,`[data-testid="construction-history-step-${unpivot.id}"]`);
    start=Date.now();
    await click(browser.cdp,`[data-testid="construction-remove-step-${unpivot.id}"]`);
    await proposal('remove-unpivot-and-dependent-filter-preview',start,expected);
    const removed=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map(e=>e.dataset.testid);`);
    assert(removed.includes('construction-removal-step-'+unpivot.id),JSON.stringify(removed));
    assert(removed.includes('construction-removal-step-'+filter.id),'Removal warning must name the dependent filter: '+JSON.stringify(removed));
  };
  await removeUnpivot();
  await click(browser.cdp,'[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeRemoval.workspace);
  await removeUnpivot();
  await apply(expected);
  await open(expected);
  assert.deepEqual(doc(builder).construction,doc(expanded).construction,'Removing Unpivot must restore the exact related chain');
  await Promise.all(failedResponses);
  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
