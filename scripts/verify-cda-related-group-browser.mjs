import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { launchBrowser, sanitizePayload } from './lib/playwright-browser.mjs';
import { createCDAPlaywrightControls } from './lib/cda-playwright-controls.mjs';
import { assertVisibleRowsMatchOracle } from './lib/cda-row-oracle.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';

const project = process.env.LOOM_CDA_PROJECT;
assert(project, 'Set LOOM_CDA_PROJECT to the isolated CDA project');
const explorer = `related-group-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-related-group-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const sourceFreezeStartedAt = new Date().toISOString();
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, cases: [], errors: [], requests: [], nativeRequests: [], sourceFreeze: { startedAt: sourceFreezeStartedAt }, started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId;
let sourceFreeze, frozenApiBuild, controls, ownedTarget;
const click = (...args) => controls.click(...args);
const selectOption = (...args) => controls.selectOption(...args);
const fill = (...args) => controls.fill(...args);
const browserEval = (...args) => controls.evaluate(...args);
const waitForBrowser = (...args) => controls.wait(...args);
const navigate = (...args) => controls.navigate(...args);
const sanitizeReportValue = sanitizePayload;
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-group-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, ...(body ? { body: sanitizeReportValue(body) } : {}), status: response.status, response: sanitizeReportValue(value) });
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
  await waitForBrowser(() => (['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await browserEval(() => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
  assert.equal(result.status, 'ready', result.text);
  assertVisibleRowsMatchOracle(result.rows, expectedRows, { label: `${name} preview` });
  const durationMs = Date.now() - start;
  if (browser) browser.lastElapsedMs = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  if (browser) browser.lastElapsedMs = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async expectedRows => {
  const start = Date.now();
  await click( '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate( `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(selector => Boolean(document.querySelector(selector)), `[data-testid="construction-table-${outputId}"]`);
  await click( `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForBrowser(({ rowCount }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1) && !document.body.innerText.includes('Loading your table…');
  }, { rowCount: expectedRows.length });
  const rows = await browserEval(() => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length); });
  const savedRows = expectedRows;
  assertVisibleRowsMatchOracle(rows, savedRows, { label: 'saved table' });
};
try {
  ownedTarget = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
  report.target = ownedTarget;
  report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze.watchedFileCount = sourceFreeze.watchedFileCount;
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiContainer));
  report.apiBuildFreeze = { target: 'running isolated CDA API build stamp', container: apiContainer, initial: frozenApiBuild.initial, invalidatesRun: true, productFailure: false };
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Group composition QA' });
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
  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  controls = createCDAPlaywrightControls({ browser, browserApiOrigin: uiOrigin, ownedPathPrefix: `${root}/${explorer}`, report });
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',arangoContainer,'arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
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
    await click('[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(() => (document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await click('[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(selector => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, `${panel} select[aria-label="Related record type"]`);
    let start=Date.now();
    await selectOption(panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(selector => Boolean(document.querySelector(selector)), `${panel} input[aria-label="${label}"]`, 5000);
    await click(panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  assert(expected.length>1,'CDA fixture must exercise many related records before grouping');
  const grouped=[[source.id,String(expected.length)]];
  await open(expected);
  const configureGroup=async()=>{
    await click('[data-testid="construction-rows-settings-trigger"]');
    await click('[data-testid="construction-action-group-rows"]');
    await waitForBrowser(() => (document.querySelector('input[aria-label="Group by Specimen ID"]:not(:disabled)')));
    start=Date.now();
    await click('input[aria-label="Group by Specimen ID"]');
  };
  let start;
  await configureGroup();
  await proposal('related-many-group-preview',start,grouped);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configureGroup();
  await proposal('confirmed-related-many-group-preview',start,grouped);
  await apply(grouped);
  await open(grouped);
  const group=doc(builder).construction.steps.find(s=>s.operation.kind==='GROUP');
  assert(group);
  await click(`[data-testid="construction-history-step-${group.id}"]`);
  await click(`[data-testid="construction-edit-step-${group.id}"]`);
  await waitForBrowser(() => (document.querySelector('select[aria-label="Summary 1"]:not(:disabled)')));
  await selectOption('select[aria-label="Summary 1"]','COUNT_DISTINCT');
  await waitForBrowser(() => (document.querySelector('select[aria-label="Summary field 1"]:not(:disabled)')));
  const fields=await browserEval(() => { return [...document.querySelector('select[aria-label="Summary field 1"]').options].map(o=>({value:o.value,label:o.textContent})); });
  const observation=fields.find(o=>o.label.startsWith('Observation FHIR resource ID'));
  assert(observation,JSON.stringify(fields));
  start=Date.now();
  await selectOption('select[aria-label="Summary field 1"]',observation.value);
  const distinct=new Set(expected.map(row=>row.at(-1)).filter(id=>id!=='—')).size;
  const editedGrouped=[[source.id,String(distinct)]];
  await proposal('edit-related-group-distinct-observations-preview',start,editedGrouped);
  await apply(editedGrouped);
  await open(editedGrouped);
  assert.equal(doc(builder).construction.steps.find(s=>s.id===group.id).operation.group.aggregates[0].operation,'COUNT_DISTINCT');
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    await click('[data-testid="construction-action-keep-rows"]');
    await waitForBrowser(selector => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, `${filterPanel} select[aria-label="Condition"]`);
    const options=await browserEval(selector => [...document.querySelector(`${selector} select[aria-label="Column"]`).options].map(option => ({ value: option.value, label: option.textContent })), filterPanel);
    report.filterColumns=options;
    const key=options.find(o=>o.label.startsWith('Specimen ID'));
    assert(key,'Grouped key must be available to Filter: '+JSON.stringify(options));
    await selectOption(filterPanel+' select[aria-label="Column"]',key.value);
    await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
    start=Date.now();
    await selectOption(filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('group-key-missing-preview',start,[]);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,beforeFilter.workspace);
  await configureMissing();
  await proposal('confirmed-group-key-missing-preview',start,[]);
  await apply([]);
  await open([]);
  const filter=doc(builder).construction.steps.find(s=>s.operation.kind==='FILTER');
  assert(filter);
  await click(`[data-testid="construction-history-step-${filter.id}"]`);
  await click(`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(selector => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, `${filterPanel} select[aria-label="Condition"]`);
  await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
  await click(filterPanel+' input[aria-label="Value"]');
  start=Date.now();
  await fill(filterPanel+' input[aria-label="Value"]', source.id);
  await proposal('group-key-equality-preview',start,editedGrouped);
  await apply(editedGrouped);
  await open(editedGrouped);
  const beforeRemoval=builder;
  const removeGroup=async()=>{
    await click(`[data-testid="construction-history-step-${group.id}"]`);
    start=Date.now();
    await click(`[data-testid="construction-remove-step-${group.id}"]`);
    await proposal('remove-group-and-dependent-filter-preview',start,expected);
    const removed=await browserEval(() => { return [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map(e=>e.dataset.testid); });
    assert(removed.includes('construction-removal-step-'+group.id),JSON.stringify(removed));
    assert(removed.includes('construction-removal-step-'+filter.id),'Removal warning must name the dependent filter: '+JSON.stringify(removed));
  };
  await removeGroup();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,beforeRemoval.workspace);
  await removeGroup();
  await apply(expected);
  await open(expected);
  assert.deepEqual(doc(builder).construction,doc(expanded).construction,'Removing Group must restore the exact related chain');
  await controls?.flush();
  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  if (browser) {
    const action = browser.activeAction ?? controls?.lastAction;
    await browser.captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: browser.activeAction ? Date.now() - browser.activeAction.startedAt : browser.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.failureUI = browser ? await browserEval(() => document.body.innerText).catch(String) : undefined;
} finally {
  if (frozenApiBuild) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await frozenApiBuild.assertUnchanged() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after }; process.exitCode = 1; }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged(), finishedAt: new Date().toISOString() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error), finishedAt: new Date().toISOString() }; process.exitCode = 1; }
  }
  if (report.sourceFingerprint) {
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.after.sha256 === report.sourceFingerprint.before.sha256 && report.sourceFingerprint.after.files === report.sourceFingerprint.before.files;
    report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
    if (!report.sourceFingerprint.unchanged) { report.priorStatus = report.status; report.status = 'invalidated'; process.exitCode = 1; }
  }
  await controls?.flush();
  if (report.status === 'passed' && report.errors.length) {
    const error = new Error(`Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`);
    report.status = 'failed';
    report.error = String(error.stack);
    process.exitCode = 1;
    const action = browser?.activeAction ?? controls?.lastAction;
    if (browser) await browser.captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: browser.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.browserDiagnostics = browser?.diagnostics;
  report.incidentalAssetFailures = browser?.diagnostics.assetFailures ?? [];
  report.actions = browser?.actions ?? [];
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.browser.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
