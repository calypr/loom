import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const longRoute=process.env.LOOM_COLLECTION_LONG_ROUTE==='1';
const project = 'loom_dev_cda_fhir';
const evidence = process.argv[2] ?? `/tmp/loom-collection-repair-${Date.now()}`;
const explorer = `collection-repair-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { longRoute, explorer, cases: [], requests: [], exceptions: [], http: [], incidental: [], responses: [], responseCaptureErrors: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
const recordCase = result => {
  report.cases.push(result);
  console.log(JSON.stringify({ case: result.name, durationMs: result.durationMs, populationRouteHops: result.populationRouteHops }));
};
const api = async (path, body) => {
  const request = { path, body, requestId: `collection-repair-${randomUUID()}` };
  report.requests.push(request);
  const start = Date.now();
  const response = await fetch(apiOrigin + path, { method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': request.requestId }, ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000) });
  request.status = response.status;
  request.response = await response.json();
  request.durationMs = Date.now() - start;
  assert(response.ok, JSON.stringify(request));
  return request.response;
};
let builder;
let browser;
let outputId;
const pendingResponses = new Set();
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10, snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const rawQuery = query => {
  const result = spawnSync('rtk', ['proxy', 'docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const open = async () => {
  const loadStart=Date.now();
  await Promise.all([...pendingResponses]);
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='1' && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='1' && !document.body.innerText.includes('Loading your table…')`);
  assert.equal(await browserEval(browser.cdp, `return document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]').length;`),0,'The independently unmapped selection must produce no table rows');
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('section[aria-label="Starting collection"]')`);
  const durationMs=Date.now()-loadStart;assert(durationMs<=5000,'Empty table and settings must render within five seconds');recordCase({name:'empty-table-load-to-settings',durationMs});
};
const checkCoverage = async (name, counts) => {
  const start = Date.now();
  await click(browser.cdp, 'section[aria-label="Starting collection"] button', {name:'Check selected-resource coverage'});
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="population-coverage-report"]') || document.querySelector('section[aria-label="Starting collection"] [role="alert"]')`);
  const text = await browserEval(browser.cdp, `return document.querySelector('[data-testid="population-coverage-report"]')?.innerText;`);
  assert((text ?? '').includes(counts), `Coverage must match independent CDA records: ${text}`);
  assert(Date.now()-start <= 5000, 'Coverage must render within five seconds');
  recordCase({name,durationMs:Date.now()-start,text});
};
try {
  const specimens = rawQuery(`FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 10 LET parents = (FOR e IN fhir_edge FILTER e._from == s._id AND e.label == "parent" AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" RETURN e._to) LET children = (FOR e IN fhir_edge FILTER e._to == s._id AND e.label == "parent" AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" LET child = DOCUMENT(e._from) FILTER child.project == "${project}" AND child.dataset_generation == "cda-fhir-v1" RETURN child.id) RETURN {id:s.id, parents, children}`);
  const selected = specimens.find(s=>s.parents.length===0 && s.children.length>0);
  assert(selected, 'A real CDA Specimen with children but no parent is needed to detect reversed self-relationship traversal');
  report.oracle = selected;
  await api(root, {name:explorer,title:'Collection repair browser QA'});
  builder = await api(base+'/builder');
  const node = builder.catalog.nodes.find(n=>n.resourceType===(longRoute?'Observation':'Specimen'));
  await command([{type:'CREATE_TABLE',title:'Unmapped collection QA',rootNodeId:node.nodeId}]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c=>c.nodeId===node.nodeId && c.fieldPath==='id');
  await command([{type:'ADD_COLUMN',outputId,occurrenceId:'base',candidateId:field.candidateId,projectionMode:'VALUE',initialPresentation:'TABLE',title:longRoute?'Observation ID':'Specimen ID'}]);
  const selection = await api(base.replace('/authoring/v2','/selections'), {snapshotToken:builder.catalog.snapshotToken,idempotencyKey:explorer,source:{kind:'resources',resources:{refs:[{project,generation:builder.catalog.generation,resourceType:'Specimen',id:selected.id}]}}});
  const routes = await api(base+'/population-routes', {snapshotToken:builder.catalog.snapshotToken,outputId,selectionRevisionId:selection.id,limit:50});
  report.parentChoices = routes.choices.filter(c=>c.route.length===1 && c.route[0].relationship==='parent');
  // Routes run from table roots to selected members: parent roots reach child members inbound.
  const parent = longRoute
    ? routes.choices.find(c=>c.route.length===2&&c.route[0].fromResourceType==='Observation'&&c.route[0].toResourceType==='Specimen'&&c.route[0].relationship==='specimen_Specimen'&&c.route[0].storageDirection==='OUTBOUND'&&c.route[1].relationship==='parent'&&c.route[1].storageDirection==='INBOUND')
    : report.parentChoices.find(c=>c.route[0].storageDirection==='INBOUND');
  assert(parent, 'The exact requested parent connection must be available: '+JSON.stringify(routes.choices.map(c=>c.route)));
  report.connection=parent.route;
  assert.equal(parent.route.length,longRoute?2:1);
  await command([{type:'SET_TABLE_POPULATION',outputId,selectionRevisionId:selection.id,routeChoiceId:parent.routeChoiceId}]);
  const original = builder.workspace.documents[0];
  browser = await launchBrowser(evidence);
  const responseInfo = new Map();
  browser.cdp.on('Runtime.exceptionThrown', e => report.exceptions.push(e.exceptionDetails));
  browser.cdp.on('Runtime.consoleAPICalled', e => { if (e.type === 'error') report.exceptions.push(e.args); });
  browser.cdp.on('Network.loadingFailed', e => { if (e.type === 'Script' && e.errorText !== 'net::ERR_ABORTED') report.exceptions.push(e); });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    if (response.status >= 400) (response.url.endsWith('/favicon.ico') ? report.incidental : report.http).push({ url: response.url, status: response.status });
    if (response.url.includes('/authoring/v2/')) responseInfo.set(requestId, { path: new URL(response.url).pathname, status: response.status });
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const info = responseInfo.get(requestId);
    if (info) {
      const pending = browser.cdp.send('Network.getResponseBody', { requestId }).then(r => report.responses.push({ ...info, body: JSON.parse(r.body) })).catch(e => report.responseCaptureErrors.push({ ...info, error: String(e) }));
      pendingResponses.add(pending);
      void pending.finally(() => pendingResponses.delete(pending));
    }
  });
  await open();
  await checkCoverage('unmapped-parent-coverage','1 selected · 0 produce rows · 1 needs attention');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid="population-coverage-report"] button')].some(b=>b.innerText==='Remove from collection'&&!b.disabled)`, 5000);
  const before = builder.draftDigest;
  const remove = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="population-coverage-report"] button')].map(b=>({label:b.innerText,disabled:b.disabled}));`);
  assert(remove.some(b=>b.label==='Remove from collection'&&!b.disabled), 'The saved parent connection must make an unmapped record removable');
  const start = Date.now();
  await click(browser.cdp, '[data-testid="population-coverage-report"] button', {name:'Remove from collection'});
  await waitForBrowser(browser.cdp, `document.querySelector('section[aria-label="Starting collection"]')?.dataset.attachedSelectionRevisionId !== ${JSON.stringify(selection.id)}`);
  builder = await api(base+'/builder');
  const revised = builder.workspace.documents[0];
  assert.notEqual(builder.draftDigest,before);
  assert.notEqual(revised.population.selectionRevisionId,selection.id);
  assert.deepEqual(revised.population.route,original.population.route,'Excluding a record must preserve the exact connection');
  assert.deepEqual(revised.columns,original.columns);
  assert.deepEqual(revised.construction,original.construction);
  assert(Date.now()-start<=5000,'Collection repair must finish within five seconds');
  recordCase({name:'remove-unmapped-record',durationMs:Date.now()-start});
  await open();
  await checkCoverage('empty-collection-reload','0 selected · 0 produce rows · 0 needs attention');
  if(longRoute){
    const clearStart=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use all authorized rows'});
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='26' && [...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use selected resources'&&!button.disabled)`);
    const clearDuration=Date.now()-clearStart;
    assert(clearDuration<=5000,'Clearing the collection must render authorized records within five seconds');
    const visibleIDs=await browserEval(browser.cdp,`return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
    assert(visibleIDs.length>0,'Authorized table must show records after clearing its collection');
    const verifiedIDs=rawQuery(`FOR d IN Observation FILTER d.id IN ${JSON.stringify(visibleIDs)} AND d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN d.id`);
    assert.deepEqual([...new Set(visibleIDs)].sort(),verifiedIDs.sort(),'Visible rows must belong to the scoped CDA source');
    recordCase({name:'clear-long-collection-to-authorized-rows',durationMs:clearDuration,visibleIDs});
    const options=await browserEval(browser.cdp,`return [...document.querySelector('select[aria-label="Population connection"]').options].map(option=>({value:option.value,label:option.text}));`);
    const exact=options.find(option=>option.label.includes('via specimen_Specimen (outgoing,')&&option.label.includes('via parent (incoming,'));
    assert(exact,'The native dropdown must offer the exact previous connection: '+JSON.stringify(options));
    await selectOption(browser.cdp,'select[aria-label="Population connection"]',exact.value);
    const attachStart=Date.now();
    await click(browser.cdp,'section[aria-label="Starting collection"] button',{name:'Use selected resources'});
    await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='1' && [...document.querySelectorAll('section[aria-label="Starting collection"] button')].some(button=>button.innerText==='Use all authorized rows'&&!button.disabled)`);
    const attachDuration=Date.now()-attachStart;
    assert(attachDuration<=5000,'Reattaching the empty collection must render within five seconds');
    builder=await api(base+'/builder');
    const attached=builder.workspace.documents[0];
    assert.deepEqual(attached.population,revised.population,'Native reattachment must preserve the revised selection and exact route direction');
    assert.deepEqual(attached.columns,original.columns);
    recordCase({name:'reattach-long-collection',durationMs:attachDuration});
    await open();
    await checkCoverage('reattached-long-collection-reload','0 selected · 0 produce rows · 0 needs attention');
  }
  await Promise.all([...pendingResponses]);
  assert.equal(report.http.length,0,JSON.stringify(report.http));
  assert.equal(report.exceptions.length,0,JSON.stringify(report.exceptions));
  report.status='passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(()=>undefined) : undefined;
  process.exitCode = 1;
} finally {
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, error: report.error }, null, 2));
