import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const explorer = `group-related-values-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-group-related-values-browser-${Date.now()}`;
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
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `group-related-values-browser-${randomUUID()}` },
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
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === ${JSON.stringify(String(expectedRows[0]?.length ?? 2))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  const savedRows = expectedRows;
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation,resourceType:s.resourceType}`;
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
    if(request.url.includes('/related-expand-choices') || request.url.includes('/construction-choice-proposals') || request.url.includes('/construction-proposals'))networkRequests.set(requestId,request.postData);
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
  const grouped=[[source.id,String(expected.length)]];
  await open(expected);
  const configureGroup=async()=>{
    await click(browser.cdp,'[data-testid="construction-rows-settings-trigger"]');
    await click(browser.cdp,'[data-testid="construction-action-group-rows"]');
    await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Group by Specimen ID"]:not(:disabled)')`);
    start=Date.now();
    await click(browser.cdp,'input[aria-label="Group by Specimen ID"]');
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
  await click(browser.cdp,'[data-testid="construction-action-add-columns"]');
  await click(browser.cdp,'[aria-label="Column types"] button',{includes:'Fields and related data'});
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-add-columns-source"]')`);
  report.addFieldsUI=await browserEval(browser.cdp, `return {text:document.querySelector('[aria-label="Add columns editor"]').innerText,controls:[...document.querySelectorAll('[aria-label="Add columns editor"] input,[aria-label="Add columns editor"] select,[aria-label="Add columns editor"] button')].map(e=>({tag:e.tagName,label:e.getAttribute('aria-label'),testId:e.dataset.testid,text:e.innerText,disabled:e.disabled}))};`);
  await click(browser.cdp,'[aria-label="Related resources"] summary');
  await click(browser.cdp,'[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  await click(browser.cdp,'[data-testid="feature-catalog-raw-fields"] summary');
  await waitForBrowser(browser.cdp,`document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)')`);
  const beforeField=builder;
  const contributorIDs=[...new Set(witnesses.map(witness=>witness.values.at(-1)))].sort();
  const withField=[[...grouped[0],contributorIDs.join('; ')]];
  const configureRelatedField=async()=>{
    await click(browser.cdp,'input[aria-label="Select Observation.id"]');
    await click(browser.cdp,'[aria-label="Add columns editor"] button',{includes:'Add 1 selected feature'});
    await waitForBrowser(browser.cdp,`document.querySelector('[role="dialog"]')`);
    await click(browser.cdp,'[role="dialog"] summary',{includes:'Other relationship paths'});
    await click(browser.cdp,'[role="dialog"] input[aria-label="Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation"]');
    start=Date.now();
    await click(browser.cdp,'[role="dialog"] input[aria-label="Observation ID: Keep all matching values"]');
    await click(browser.cdp,'[role="dialog"] button',{name:'Add 1 column'});
    await waitForBrowser(browser.cdp,`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus) || ['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
    report.relatedProposal=await browserEval(browser.cdp,`const p=document.querySelector('[data-testid="construction-proposal-panel"]')??document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:p.dataset.proposalStatus,text:p.innerText};`);
    assert.equal(report.relatedProposal.status,'ready',report.relatedProposal.text);
    const cells=await browserEval(browser.cdp,`return [...document.querySelector('[data-testid="construction-proposal-preview-row"]').querySelectorAll('td')].map(cell=>({text:cell.innerText,raw:cell.title}));`);
    assert.equal(cells.length,3);
    assert.deepEqual(cells.slice(0,2).map(cell=>cell.text),grouped[0]);
    const values=JSON.parse(cells[2].raw);
    assert(Array.isArray(values)&&values.every(value=>typeof value==='string'),'Grouped IDs must be scalar values, not nested contributor lists');
    assert.deepEqual([...values].sort(),contributorIDs);
    recordRender('group-related-all-values-preview',start);
  };
  await configureRelatedField();
  await click(browser.cdp,'[data-testid="construction-choice-proposal-panel"] button',{name:'Cancel'});
  await rendered(grouped);
  assert.deepEqual((await api(base+'/builder')).workspace,beforeField.workspace);
  await configureRelatedField();
  start=Date.now();
  await click(browser.cdp,'[data-testid="construction-choice-proposal-panel"] button',{name:'Apply columns'});
  await waitForBrowser(browser.cdp,`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`);
  await rendered(withField);
  recordRender('group-related-all-values-apply',start);
  builder=await api(base+'/builder');
  report.savedFieldDocument=doc(builder);
  await open(withField);
  const fieldBaseline = structuredClone(builder.workspace);
  const savedGroup = doc(builder).construction.steps.find(step => step.operation.kind === 'GROUP');
  assert(savedGroup);
  const configureDistinctGroup = async () => {
    await click(browser.cdp, `[data-testid="construction-history-step-${savedGroup.id}"]`);
    await click(browser.cdp, `[data-testid="construction-edit-step-${savedGroup.id}"]`);
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Summary 1"]:not(:disabled)')`);
    const priorId = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId;`);
    start = Date.now();
    await selectOption(browser.cdp, 'select[aria-label="Summary 1"]', 'COUNT_DISTINCT');
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Summary field 1"]:not(:disabled)')`);
    const fields = await browserEval(browser.cdp, `return [...document.querySelector('select[aria-label="Summary field 1"]').options].map(option=>({value:option.value,label:option.textContent}));`);
    const observation = fields.find(field => field.label.startsWith('Observation FHIR resource ID'));
    assert(observation, JSON.stringify(fields));
    await selectOption(browser.cdp, 'select[aria-label="Summary field 1"]', observation.value);
    await waitForBrowser(browser.cdp, `(() => {const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return panel?.dataset.proposalStatus==='ready'&&panel.dataset.proposalId!==${JSON.stringify(priorId)}&&JSON.stringify([...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText)))===${JSON.stringify(JSON.stringify(withField))};})()`, 5000);
    await proposal('edit-group-with-retained-related-field-preview', start, withField);
  };
  await configureDistinctGroup();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await rendered(withField);
  assert.deepEqual((await api(base + '/builder')).workspace, fieldBaseline, 'Cancel must preserve the group and related field binding');
  await configureDistinctGroup();
  await apply(withField);
  assert.equal(doc(builder).construction.steps.find(step => step.id === savedGroup.id).operation.group.aggregates[0].operation, 'COUNT_DISTINCT');
  assert.deepEqual(doc(builder).columns, report.savedFieldDocument.columns, 'Editing the group aggregate must preserve authored column bindings');
  await open(withField);
  await click(browser.cdp, `[data-testid="construction-history-step-${savedGroup.id}"]`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${savedGroup.id}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Summary 1"]:not(:disabled)')`);
  start = Date.now();
  await selectOption(browser.cdp, 'select[aria-label="Summary 1"]', 'COUNT_ROWS');
  await proposal('restore-group-count-with-related-field-preview', start, withField);
  await apply(withField);
  await open(withField);
  assert.deepEqual(doc(builder).construction, report.savedFieldDocument.construction, 'Aggregate round trip must preserve the grouped related projection');
  await click(browser.cdp,'button',{name:'Columns'});
  start=Date.now();
  await click(browser.cdp,'button[aria-label="Remove Observation ID column"]');
  await rendered(grouped);
  recordRender('remove-group-related-field',start);
  builder=await api(base+'/builder');
  await open(grouped);
  assert.deepEqual(doc(builder).construction,doc(beforeField).construction);
  assert.deepEqual(report.errors,[]);
  report.status='passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.savedBuilderAtFailure=await api(base+'/builder').catch(error=>({readError:String(error)}));
  report.failureProposal = browser ? await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {id:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>({text:cell.innerText,title:cell.title}))) };`).catch(String) : undefined;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  await Promise.all(failedResponses);
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
