import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { sanitizePayload } from './lib/playwright-browser.mjs';
import { assertVisibleRowsMatchOracle } from './lib/cda-row-oracle.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';


export async function runRelatedUnpivotBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const project = cda.project;
assert(project, 'Set LOOM_CDA_PROJECT to the isolated CDA project');
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const apiContainer = (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER);
const arangoContainer = (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER);
const composeProject = (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT);
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const sourceFreezeStartedAt = new Date().toISOString();
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, cases: [], errors: [], requests: [], nativeRequests: [], sourceFreeze: { startedAt: sourceFreezeStartedAt }, started: new Date().toISOString() };
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
await mkdir(evidence, { recursive: true });
let builder, outputId;
let sourceFreeze, frozenApiBuild, controls, ownedTarget;
const click = (...args) => controls.click(...args);
const selectOption = (...args) => controls.selectOption(...args);
const fill = (...args) => controls.fill(...args);
const browserEval = (...args) => controls.inspect(...args);
const waitForBrowser = (callback, args = {}, timeout = 5000) => cda.wait(callback, args ?? {}, Math.min(timeout, 5000));
const navigate = (...args) => cda.navigate(...args);
const sanitizeReportValue = sanitizePayload;
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-unpivot-browser-${randomUUID()}` },
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
  const result = await browserEval(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { status: panel?.dataset.proposalStatus, proposalId: panel?.dataset.proposalId, text: panel?.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText)) };
  });
  assert.equal(result.status, 'ready', result.text);
  assertVisibleRowsMatchOracle(result.rows, expectedRows, { label: `${name} preview`, exactWindow: true });
  const durationMs = Date.now() - start;
if (page) report.lastElapsedMs = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
if (page) report.lastElapsedMs = durationMs;
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
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
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
  const rows = await browserEval(() => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  assertVisibleRowsMatchOracle(rows, expectedRows, { label: 'saved table', exactWindow: true });
};
try {
  ownedTarget = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
  report.target = ownedTarget;
  report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze.watchedFileCount = sourceFreeze.watchedFileCount;
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiContainer));
  report.apiBuildFreeze = { target: 'running isolated CDA API build stamp', container: apiContainer, initial: frozenApiBuild.initial, invalidatesRun: true, productFailure: false };
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" SORT s.id LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
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
  controls = cda;
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',arangoContainer,'arangosh','--server.database','loom_dev','--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
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
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" SORT d.id RETURN DISTINCT {id:d.id,_id:d._id}`;
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
    await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${panel} select[aria-label="Related record type"]` });
    let start=Date.now();
    await selectOption(panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${panel} input[aria-label="${label}"]` }, 5000);
    await click(panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  await open(expected);
  await click('[data-testid="construction-rows-settings-trigger"]');
  const unpivotTile = page.getByTestId('construction-action-unpivot-rows');
  await unpivotTile.waitFor({ state: 'visible', timeout: 5000 });
  assert(await unpivotTile.isEnabled(), 'Turn columns into rows must be enabled');
  await click('[data-testid="construction-action-unpivot-rows"]');
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false));
  let start=Date.now();
  await click('input[aria-label="Unpivot Specimen ID"]');
  const unpivotExpected=expected.map(row=>[...row.slice(1),'Specimen ID',row[0]]);
  await proposal('related-chain-unpivot-preview',start,unpivotExpected);
  assert.equal((await api(base+'/builder')).draftDigest,expanded.draftDigest);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await click('[data-testid="construction-rows-settings-trigger"]');
  await click('[data-testid="construction-action-unpivot-rows"]');
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false));
  start=Date.now();
  await click('input[aria-label="Unpivot Specimen ID"]');
  await proposal('confirmed-related-chain-unpivot-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  const unpivot=doc(builder).construction.steps.find(s=>s.operation.kind==='UNPIVOT');
  assert(unpivot);
  await click(`[data-testid="construction-history-step-${unpivot.id}"]`);
  await click(`[data-testid="construction-edit-step-${unpivot.id}"]`);
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.checked));
  await click('[data-testid="construction-unpivot-advanced"] summary');
  start=Date.now();
  await selectOption('select[aria-label="Unpivot null row policy"]','DROP');
  await proposal('edit-unpivot-policy-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  assert.equal(doc(builder).construction.steps.find(s=>s.id===unpivot.id).operation.unpivot.nullRowPolicy,'DROP');
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    await click('[data-testid="construction-action-keep-rows"]');
    await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${filterPanel} select[aria-label="Condition"]` });
    const options=await browserEval(({ selector }) => [...document.querySelector(`${selector} select[aria-label="Column"]`).options].map(option => ({ value: option.value, label: option.textContent })), { selector: filterPanel });
    report.filterColumns=options;
    const value=options.find(o=>/^Value(?: \(|$)/.test(o.label));
    assert(value,'Unpivot Value must be available to Filter: '+JSON.stringify(options));
    await selectOption(filterPanel+' select[aria-label="Column"]',value.value);
    await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
    start=Date.now();
    await selectOption(filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('unpivot-value-missing-preview',start,[]);
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,beforeFilter.workspace);
  await configureMissing();
  await proposal('confirmed-unpivot-value-missing-preview',start,[]);
  await apply([]);
  await open([]);
  const filter=doc(builder).construction.steps.find(s=>s.operation.kind==='FILTER');
  assert(filter);
  await click(`[data-testid="construction-history-step-${filter.id}"]`);
  await click(`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${filterPanel} select[aria-label="Condition"]` });
  await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
  await click(filterPanel+' input[aria-label="Value"]');
  start=Date.now();
  await fill(filterPanel+' input[aria-label="Value"]', source.id);
  await proposal('unpivot-value-equality-preview',start,unpivotExpected);
  await apply(unpivotExpected);
  await open(unpivotExpected);
  const beforeRemoval=builder;
  const removeUnpivot=async()=>{
    await click(`[data-testid="construction-history-step-${unpivot.id}"]`);
    start=Date.now();
    await click(`[data-testid="construction-remove-step-${unpivot.id}"]`);
    await proposal('remove-unpivot-and-dependent-filter-preview',start,expected);
    const removed=await browserEval(() => [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map(element => element.dataset.testid));
    assert(removed.includes('construction-removal-step-'+unpivot.id),JSON.stringify(removed));
    assert(removed.includes('construction-removal-step-'+filter.id),'Removal warning must name the dependent filter: '+JSON.stringify(removed));
  };
  await removeUnpivot();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,beforeRemoval.workspace);
  await removeUnpivot();
  await apply(expected);
  await open(expected);
  assert.deepEqual(doc(builder).construction,doc(expanded).construction,'Removing Unpivot must restore the exact related chain');

  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); report.__nativeFailure = true;
if (page) {
    const action = report.activeAction ?? controls?.lastAction;
    await captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : report.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.failureUI = page ? await browserEval(() => document.body.innerText).catch(String) : undefined;
} finally {
  if (frozenApiBuild) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await frozenApiBuild.assertUnchanged() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after }; report.__nativeFailure = true; }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged(), finishedAt: new Date().toISOString() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error), finishedAt: new Date().toISOString() }; report.__nativeFailure = true; }
  }
  if (report.sourceFingerprint) {
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.after.sha256 === report.sourceFingerprint.before.sha256 && report.sourceFingerprint.after.files === report.sourceFingerprint.before.files;
    report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
    if (!report.sourceFingerprint.unchanged) { report.priorStatus = report.status; report.status = 'invalidated'; report.__nativeFailure = true; }
  }

  if (report.status === 'passed' && report.errors.length) {
    const error = new Error(`Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`);
    report.status = 'failed';
    report.error = String(error.stack);
    report.__nativeFailure = true;
    const action = report.activeAction ?? controls?.lastAction;
if (page) await captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: report.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.browserDiagnostics = cda.diagnostics;
  report.incidentalAssetFailures = cda.diagnostics.assetFailures ?? [];
  report.actions = cda.report.actions ?? [];
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-related-unpivot-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-related-unpivot-browser.mjs', report);
  return report;
}
