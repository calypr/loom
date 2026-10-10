import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { collectPreviewRows } from '../helpers/playwright-preview-rows.mjs';
import { assertOwnedCdaTarget } from '../helpers/owned-cda-target.mjs';
import { runCDAQuantityCategoryOracle } from '../helpers/cda-quantity-category-oracle-runner.mjs';
import { compareCDAQuantityCategoryValues } from '../helpers/cda-quantity-category-oracle.mjs';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';


export async function runQuantityPivotBrowserWorkflow({ page, cda }, originalArgs = {}) {
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
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const sourceRoot = fileURLToPath(new URL('../../..', import.meta.url));
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
assert((cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), 'Set LOOM_ARANGO_DATABASE for the isolated CDA source database.');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer: (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER),
  composeProject: (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT), sourceRoot, arangoContainer: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),
  clickhouseContainer: (cda.target.clickhouseContainer ?? cda.env?.LOOM_CLICKHOUSE_CONTAINER) });
const sourceFreeze = await captureSourceFreeze(sourceRoot);
const apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp((cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER)));
const report = { explorer, cases: [], errors: [], requests: [], authoringRequests: [], nativeRequests: [],
  sourceFingerprint: { before: sourceFingerprint(sourceRoot) }, apiBuildIdentity: apiBuildFreeze.initial, started: new Date().toISOString() };
const inspectPage = (_page, inspect, argument) => cda.inspect(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  return cda.wait(predicate, argument ?? {}, Math.min(timeout, 5000));
};
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (_page, selector, identity = {}) => cda.click(selector, identity, 5000);
const selectNative = async (_page, selector, value) => {
  const locator = page.locator(selector);
  await cda.selectOption(selector, value);
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
let browserRequestCapture;
const syncAuthoringRequests = () => {
  const endpoints = new Set(['commands', 'reconcile', 'preview', 'construction-proposals', 'construction-category-discoveries']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => ({ ...request, endpoint: request.path.split('/').at(-1), url: `${request.origin}${request.path}`, requestStartedAtMs: request.startedAt,
      responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
      requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
      requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
      responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
      responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
      responseRowCount: request.response?.rowCount ?? request.response?.preview?.rowCount ?? request.response?.rows?.length,
      ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
    })));
  report.browserCommands = report.authoringRequests.filter(request => request.endpoint === 'commands').map(request => ({
    requestId: request.requestId, body: request.body, status: request.status, response: request.response,
    requestStartedAtMs: request.requestStartedAtMs, responseFinishedAtMs: request.responseFinishedAtMs,
  }));
  correlateAuthoringRequests();
};
await mkdir(evidence, { recursive: true });
let builder, outputId;
let editedPivotPresentation=false;
const correlateAuthoringRequests = () => {
  for (const reconcile of report.authoringRequests.filter(request => request.endpoint === 'reconcile' && request.responseReceiptId)) {
    const command = [...report.authoringRequests].reverse().find(request =>
      request.endpoint === 'commands' &&
      request.responseDraftVersion === reconcile.requestDraftVersion &&
      request.responseDraftDigest === reconcile.requestDraftDigest &&
      request.requestStartedAtMs <= reconcile.requestStartedAtMs,
    );
    if (command) reconcile.commandRequestId = command.requestId;
    for (const preview of report.authoringRequests.filter(request =>
      request.endpoint === 'preview' && request.requestReceiptId === reconcile.responseReceiptId,
    )) {
      preview.reconcileRequestId = reconcile.requestId;
      preview.reconciledDraftVersion = reconcile.requestDraftVersion;
      preview.reconciledDraftDigest = reconcile.requestDraftDigest;
      if (reconcile.commandRequestId) preview.commandRequestId = reconcile.commandRequestId;
    }
  }
};
const drainPendingResponseReads = async () => {
  await browserRequestCapture?.flush();
  syncAuthoringRequests();
};
const captureFailureDOM = () => inspectPage(page, () => {
return (() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = preview?.querySelector('[role="table"]');
  const rows = selector => [...document.querySelectorAll(selector)].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"], td')].map(cell => cell.innerText.trim()),
  );
  return {
    capturedAt: new Date().toISOString(),
    proposal: proposal ? {
      status: proposal.dataset.proposalStatus,
      proposalId: proposal.dataset.proposalId,
      text: proposal.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row =>
        [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()),
      ),
    } : undefined,
    savedPreview: preview ? {
      visible: preview.getClientRects().length > 0,
      ariaRowCount: table?.getAttribute('aria-rowcount'),
      headers: [...preview.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: rows('[data-testid="preview-table-scroll"] [role="row"]'),
      loading: document.body.innerText.includes('Loading your table…'),
    } : undefined,
    bodyText: document.body.innerText,
  };
})();
});
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `quantity-category-browser-${randomUUID()}` },
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
  await waitForObservable(page, () => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await inspectPage(page, () => {
const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};
});
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
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await gotoPage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForObservable(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)));
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async (expectedRows, rowLimit = 25) => {
  await waitForObservable(page, ({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1) && !document.body.innerText.includes('Loading your table…'), { rowCount: Math.min(rowLimit, expectedRows.length) });
  const rows = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);
});
  assert(rows.length > 0 || expectedRows.length === 0);
  const savedRows = editedPivotPresentation ? expectedRows.map(row=>[row[1],row[0]]) : expectedRows;
  if(editedPivotPresentation){
    const headers=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(e=>e.innerText.trim());
});
    assert(headers.some(h=>h.toLowerCase().includes(report.oracle.source.id)),JSON.stringify(headers));
    assert(headers.some(h=>h.includes('OBSERVATION FHIR RESOURCE ID')),JSON.stringify(headers));
  }
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
const setPreviewLimit = async limit => {
  await selectNative(page, 'select[aria-label="Preview row limit"]', String(limit));
};
const typedCategoryIdentity = key => JSON.stringify(key);
const outputColumnValues = columns => columns.map(column => ({
  column: column.column,
  label: column.label,
  logicalType: column.logicalType,
}));
const proposalPreviewFor = async proposalRequest => {
  assert(proposalRequest?.response?.proposalId, 'Pivot proposal must return a proposal ID');
  const preview=proposalRequest.response.preview;
  assert(preview, 'Native proposal must embed its table preview');
  assert.equal(preview.receiptId,proposalRequest.response.proposalId,'Proposal preview must be bound to this exact proposal');
  assert.equal(preview.outputId,outputId);
  return preview;
};
const expectedPivotFor = (proposalRequest, preview, expectedDuplicatePolicy = 'ERROR') => {
  const step = proposalRequest.response.candidateConstruction.steps.find(candidate => candidate.operation.kind === 'PIVOT');
  assert(step, 'Native proposal must contain a PIVOT construction step');
  const operation = step.operation.pivot;
  const groupOutputs = operation.groupKeyIds.map(id => {
    const output = step.outputs.find(column => column.id === id);
    assert(output, `Pivot group output ${id} must be present`);
    return output;
  });
  const categoryOutputs = operation.categories.map(category => {
    const output = step.outputs.find(column => column.id === category.outputColumnId);
    assert(output, `Pivot category output ${category.outputColumnId} must be present`);
    return { key: category.key, output };
  });
  const groupIndex = output => {
    const label = output.label.toLowerCase();
    if (label.includes('specimen id')) return 0;
    if (label.includes('patient fhir resource id')) return 1;
    if (label.includes('observation fhir resource id')) return 2;
    if (label.includes('observation.valuecodeableconcept.text')) return 3;
    assert.fail(`Unexpected Pivot row identity field ${output.label}`);
  };
  assert.equal(groupOutputs.length, 4, 'Pivot must retain the discovery text key and the three independently witnessed row IDs');
  const buckets = new Map();
  for (const observation of report.oracle.observationRows) {
    const groupValues = groupOutputs.map(output => groupIndex(output) === 3 ? observation.text : observation.rowIds[groupIndex(output)]);
    const groupIdentity = JSON.stringify(groupValues);
    let bucket = buckets.get(groupIdentity);
    if (!bucket) {
      bucket = { groupValues, values: new Map(), categoryCounts: new Map(), sourceRows: 0 };
      buckets.set(groupIdentity, bucket);
    }
    bucket.sourceRows += 1;
    const quantity = observation.quantity;
    assert(quantity?.code == null || typeof quantity.code === 'string', 'CDA quantity code values must be strings or null');
    const key = quantity?.code == null
      ? { kind: 'NULL' }
      : { kind: 'STRING', string: quantity.code };
    const category = categoryOutputs.find(candidate => typedCategoryIdentity(candidate.key) === typedCategoryIdentity(key));
    assert(category, `Independent Observation value maps to undiscovered category ${JSON.stringify(key)}`);
    bucket.categoryCounts.set(category.output.name, (bucket.categoryCounts.get(category.output.name) ?? 0) + 1);
    if (typeof quantity?.value === 'number' && Number.isFinite(quantity.value)) {
      const current = bucket.values.get(category.output.name) ?? { count: 0, value: 0 };
      current.count += 1;
      current.value = expectedDuplicatePolicy === 'SUM' ? current.value + quantity.value : quantity.value;
      bucket.values.set(category.output.name, current);
    }
  }
  const duplicateBuckets = [...buckets.values()].reduce((count, bucket) => count + [...bucket.categoryCounts.values()].filter(value => value > 1).length, 0);
  assert.equal(duplicateBuckets, 0, 'The independently witnessed row IDs leave no duplicate group/category inputs');
  assert.equal(operation.duplicatePolicy, expectedDuplicatePolicy, `Native Pivot must use the requested ${expectedDuplicatePolicy} duplicate policy`);
  const previewColumns = outputColumnValues(preview.columns);
  const expectedRows = [...buckets.values()].map(bucket => Object.fromEntries(previewColumns.map(column => {
    const group = groupOutputs.find(output => output.name === column.column);
    if (group) return [column.column, bucket.groupValues[groupOutputs.indexOf(group)]];
    const category = categoryOutputs.find(candidate => candidate.output.name === column.column);
    assert(category, `Preview column ${column.column} is not a Pivot output`);
    const value = bucket.values.get(category.output.name);
    return [column.column, value ? value.value : null];
  })));
  const protocolRows=preview.rows.map(row=>Object.fromEntries(previewColumns.map(column=>[column.column,row[column.column]])));
  const sortRows=rows=>rows.map(row=>JSON.stringify(previewColumns.map(column=>row[column.column]))).sort();
  assert.deepEqual(sortRows(protocolRows),sortRows(expectedRows),'Native proposal protocol values and types must match raw CDA witnesses');
  const identities=preview.rows.map(row=>row.__loom_row_id);
  assert(identities.every(id=>typeof id==='string'&&id.length>0),'Pivot protocol must retain every row identity');
  assert.equal(new Set(identities).size,identities.length,'Pivot row identities must be unique');
  return { step, operation, columns: previewColumns, rows: expectedRows, duplicateBuckets, sourceRowCount: report.oracle.observationRows.length };

};
const assertPersistedPivotIdentity = async (proposalPreview, name) => {
  await drainPendingResponseReads();
  const saved = report.authoringRequests.findLast(request =>
    request.endpoint === 'preview' && request.status === 200 &&
    request.response?.outputId === outputId && request.response?.rowCount === proposalPreview.rowCount);
  assert(saved, `${name}: the saved table must have a completed native preview`);
  const expectedIds = proposalPreview.rows.map(row => row.__loom_row_id).sort();
  const savedIds = saved.response.rows.map(row => row.__loom_row_id).sort();
  assert(expectedIds.every(id => typeof id === 'string' && id.length > 0), `${name}: proposal identities must be nonempty`);
  assert.equal(new Set(savedIds).size, savedIds.length, `${name}: saved identities must be unique`);
  assert.deepEqual(savedIds, expectedIds, `${name}: Apply and reload must preserve the proposed row identities`);
  report.cases.push({name, rowCount:savedIds.length, receiptId:saved.response.receiptId});
};

const assertExactRows = async ({ name, columns, rows, rowLimit = 1000, panel = false }) => {
  const start = Date.now();
  const prefix = panel ? '[data-testid="construction-proposal-preview"]' : '[data-testid="preview-table-scroll"]';
  const rowSelector = panel ? '[data-testid="construction-proposal-preview-row"]' : '[role="row"]';
  const cellSelector = panel ? 'td' : '[role="cell"]';
  if (!panel) await setPreviewLimit(rowLimit, rows.length);
  const ready = panel
    ? ({ prefix, rowSelector, rowCount }) => Boolean(document.querySelector(`${prefix} table`)) && document.querySelectorAll(`${prefix} ${rowSelector}`).length === rowCount
    : ({ prefix, rowCount }) => document.querySelector(`${prefix} [role="table"]`)?.getAttribute('aria-rowcount') === String(rowCount + 1) && !document.body.innerText.includes('Loading your table…');
  await waitForObservable(page, ready, { prefix, rowSelector, rowCount: Math.min(rowLimit, rows.length) }, 10000);
  const actual = panel
    ? await inspectPage(page, ({ prefix, rowSelector, cellSelector }) => { const root=document.querySelector(prefix); return {headers:[...root.querySelectorAll('th,[role="columnheader"]')].map(cell=>cell.innerText.trim().split('\n')[0]),rows:[...root.querySelectorAll(rowSelector)].map(row=>[...row.querySelectorAll(cellSelector)].map(cell=>cell.innerText.trim()))}; }, { prefix, rowSelector, cellSelector })
    : await collectPreviewRows(page, { containerSelector: prefix, tableSelector: '[role="table"]', rowSelector, cellSelector });
  if (!panel) actual.rows = actual.rows.map(row => row.values);
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), columns.map(column => column.label.trim().toLowerCase()), `${name} column labels must match the native Pivot preview`);
  const expectedCells = rows.map(row => columns.map(column => {
    const value = row[column.column];
    return value === null || value === undefined ? '—' : String(value);
  }));
  assert.equal(actual.rows.length, Math.min(rowLimit, rows.length), `${name} must render every row allowed by the selected preview limit`);
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${name} must match every independent CDA row and value`);
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} exact row check took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: rows.length, visibleRowCount: actual.rows.length, columns });
  return actual;
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), 'arangosh', '--server.database', (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Pivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Pivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  browserRequestCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
    responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries|construction-capabilities)$/,
  });
  page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',(cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),'arangosh','--server.database',(cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE),'--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
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
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await clickNative(page,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForObservable(page, ({ panel }) => document.querySelector(`${panel} select[aria-label="Related record type"]`)?.disabled === false, { panel });
    let start=Date.now();
    await selectNative(page,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForObservable(page, ({ panel, label }) => Boolean(document.querySelector(`${panel} input[aria-label="${label}"]`)), { panel, label }, 5000);
    await clickNative(page,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const fullPopulation=originalArgs.fullPopulation === true || (cda.env?.LOOM_QUANTITY_FULLPOP ?? process.env.LOOM_QUANTITY_FULLPOP)==='1';
  if(fullPopulation){
    await command([{type:'CLEAR_TABLE_POPULATION',outputId}]);
    const loadStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)));
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    recordRender('full-population-editor-ready',loadStart);
  }
  if(!fullPopulation) await setPreviewLimit(100,expected.length);
  const expanded=builder;
  const observations=witnesses.filter(w=>w.anchor).map(w=>({...rawQuery(`FOR o IN Observation FILTER o._id==${JSON.stringify(w.anchor)} AND o.project=="${project}" AND o.dataset_generation=="cda-fhir-v1" RETURN {id:o.id,quantity:o.payload.valueQuantity,text:o.payload.valueCodeableConcept.text}`)[0],rowIds:w.values}));
  assert(observations.length<=1000,'Independent relationship oracle must remain bounded');
  const quantities=observations.filter(o=>typeof o?.quantity?.code==='string' && typeof o.quantity.value==='number');
  assert(quantities.length>0,'Selected indirect route needs populated numeric quantity witnesses');
  report.oracle.scope=fullPopulation?'bounded selected-Specimen witnesses retained only for discovery latency; not a full-population correctness oracle':'single selected-Specimen relationship witnesses';
  report.oracle.quantities=quantities;
  const expectedCategories=[...new Set(quantities.map(o=>o.quantity.code))].sort();
  report.oracle.expectedCategories=expectedCategories;
  await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
  await clickNative(page,'[data-testid="construction-action-pivot-rows"]');
  await waitForObservable(page,() => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
  await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot group "]'))));
  for(const label of ['Specimen ID','Patient FHIR resource ID','Observation FHIR resource ID']){
    const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
    const state=await inspectPage(page, ({ selector }) => { const i=document.querySelector(selector); return {found:Boolean(i),checked:i?.checked,disabled:i?.disabled}; }, { selector: selector });
    assert(state.found&&!state.disabled,`Pivot group field ${label} must be available`);
    if(state.checked)await clickNative(page,selector);
  }
  const chooseSource=async(label,path)=>{
    const options=await inspectPage(page, ({ label }) => [...document.querySelector(`select[aria-label="${label}"]`).options].map(o=>({value:o.value,label:o.textContent})), { label: label });
    const matching=options.filter(o=>o.value.startsWith('source:') && o.label.includes(path));
    assert.equal(matching.length,1,'Require one exact active related source choice: '+path+' '+JSON.stringify(options));
    await selectNative(page,`select[aria-label="${label}"]`,matching[0].value);
  };
  await chooseSource('Add pivot group field','Observation.valueCodeableConcept.text');
  await chooseSource('Pivot category field','Observation.valueQuantity.code');
  const start=Date.now();
  const requestIndex=report.authoringRequests.length;
  await chooseSource('Pivot values field','Observation.valueQuantity.value');
  await waitForObservable(page,() => Boolean(!document.body.innerText.includes('Finding categories') && (document.querySelector('[data-testid="construction-proposal-panel"]') || document.body.innerText.includes('exceeded') || document.body.innerText.includes('No categories'))),10000);
  await drainPendingResponseReads();
  const discovery=report.authoringRequests.slice(requestIndex).findLast(r=>r.endpoint==='construction-category-discoveries');
  assert(discovery,'Native source pair must automatically discover categories');
  report.initialDiscovery=discovery;
  report.discoveryDOM=await captureFailureDOM();
  recordRender('quantity-category-discovery-to-render',start);
  assert.equal(discovery.status,200,'Quantity discovery must succeed');
  assert.equal(discovery.response.complete,true,'Category discovery must be complete');
  if(!fullPopulation){
    assert.equal(discovery.body.groupKeyIds.length,1,'Initial quantity discovery must retain the native value-text group key');
    const expectedKeys=[...new Set(observations.map(o=>o?.quantity?.code==null?'NULL':JSON.stringify({kind:'STRING',string:o.quantity.code})))].sort();
    const actualKeys=discovery.response.categories.map(c=>c.key.kind==='NULL'?'NULL':JSON.stringify(c.key)).sort();
    assert.deepEqual(actualKeys,expectedKeys,'Complete categories must match every scoped related Observation, including missing values');
    report.categoryCorrectness='bounded independent route oracle passed';
    report.oracle.observationRows=observations.map(({rowIds,id,quantity,text})=>({rowIds,id,quantity,text}));
    await rendered(expected,100);
    const groupLabels=['Specimen ID','Patient FHIR resource ID','Observation FHIR resource ID'];
    const groupKeysStart=Date.now();
    const pivotPreviewStart=Date.now();
    for(const label of groupLabels){
      const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
      const state=await inspectPage(page, ({ selector }) => { const i=document.querySelector(selector); return {found:Boolean(i),checked:i?.checked,disabled:i?.disabled}; }, { selector: selector });
      assert(state.found&&!state.disabled,`Pivot group field ${label} must be available`);
      if(!state.checked)await clickNative(page,selector);
    }
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label="Pivot group Observation.valueCodeableConcept.text"]:checked'))));
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))&&document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.innerText.includes('2 of 2 categories')),10000);
    await drainPendingResponseReads();
    const refreshedDiscovery=report.authoringRequests.slice(requestIndex).findLast(request=>request.endpoint==='construction-category-discoveries'&&request.body?.groupKeyIds?.length===4) ?? discovery;
    // Existing ID group keys preserve the source/category pair, so reusing its complete discovery is valid.
    assert.equal(refreshedDiscovery.status,200,'Refreshed quantity discovery must succeed');
    assert.equal(refreshedDiscovery.response.complete,true,'Refreshed quantity discovery must be complete');
    const refreshedKeys=refreshedDiscovery.response.categories.map(category=>category.key.kind==='NULL'?'NULL':JSON.stringify(category.key)).sort();
    const refreshedExpectedKeys=[...new Set(observations.map(observation=>observation?.quantity?.code==null?'NULL':JSON.stringify({kind:'STRING',string:observation.quantity.code})))].sort();
    assert.deepEqual(refreshedKeys,refreshedExpectedKeys,'Changing row keys must preserve the independently witnessed category set');
    report.discovery=refreshedDiscovery;
    report.oracle.categoryDiscoveryGroupKeyCounts=[discovery.body.groupKeyIds.length,refreshedDiscovery.body.groupKeyIds.length];
    report.oracle.categoryDiscoveryReused=refreshedDiscovery===discovery;
    recordRender('quantity-category-state-after-row-keys',groupKeysStart);
    report.categoryCorrectness='bounded independent route oracle passed; category domain remains unchanged when existing ID keys are added';
    const prePivotWorkspace=expanded.workspace;
    assert.deepEqual((await api(base+'/builder')).workspace,prePivotWorkspace,'Category discovery and candidate previews must not mutate the saved draft');
    await waitForObservable(page,() => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)),10000);
    await drainPendingResponseReads();
    recordRender('pivot-preview-to-ready',pivotPreviewStart);
    const firstPivotProposal=report.authoringRequests.slice(requestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(firstPivotProposal?.status,200,'Native Pivot preview request must succeed');
    const firstPivotPreview=await proposalPreviewFor(firstPivotProposal);
    const expectedPivot=expectedPivotFor(firstPivotProposal,firstPivotPreview);
    assert.equal(firstPivotPreview.rowCount,expectedPivot.sourceRowCount,'Native Pivot preview row count must match the independent Observation oracle');
    assert.equal(firstPivotPreview.rows.length,expectedPivot.rows.length,'Preview must include every bounded row at the selected 100-row limit');
    await assertExactRows({name:'pivot-preview-independent-values-and-ids',columns:expectedPivot.columns,rows:expectedPivot.rows,rowLimit:100,panel:true});
    assert.equal(expectedPivot.operation.duplicatePolicy,'ERROR');
    assert.equal(expectedPivot.duplicateBuckets,0);
    await clickNative(page,'[data-testid="construction-reshape-pivot-advanced"] > summary');
    const duplicateOptions=await inspectPage(page,() => {
return [...document.querySelector('select[aria-label="Pivot duplicate policy"]').options].map(option=>({value:option.value,disabled:option.disabled}));
});
    assert(duplicateOptions.some(option=>option.value==='SUM'&&!option.disabled),'Numeric Pivot values must expose the native SUM duplicate option');
    const browserCommandCount=report.browserCommands?.length??0;
    const cancelStart=Date.now();
    await clickNative(page,'[data-testid="construction-cancel-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')),5000);
    assert.deepEqual((await api(base+'/builder')).workspace,prePivotWorkspace,'Cancel must leave the saved construction unchanged');
    assert.equal(report.browserCommands?.length??0,browserCommandCount,'Cancel must not issue a draft command');
    recordRender('pivot-preview-cancel-without-mutation',cancelStart);
    // Cancel closes the candidate editor. Reopen and configure through native controls.
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await clickNative(page,'[data-testid="construction-action-pivot-rows"]');
    await waitForObservable(page,() => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
    for(const label of groupLabels) {
      const selector=`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`;
      const checked=await inspectPage(page, ({ selector }) => document.querySelector(selector)?.checked, { selector: selector });
      if(!checked) await clickNative(page,selector);
    }
    await chooseSource('Add pivot group field','Observation.valueCodeableConcept.text');
    await chooseSource('Pivot category field','Observation.valueQuantity.code');
    await chooseSource('Pivot values field','Observation.valueQuantity.value');
    await clickNative(page,'[data-testid="construction-reshape-pivot-advanced"] > summary');
    const sumRequestIndex=report.authoringRequests.length;
    const sumStart=Date.now();
    await selectNative(page,'select[aria-label="Pivot duplicate policy"]','SUM');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'&&Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-id]')?.dataset.proposalId)),10000);
    await drainPendingResponseReads();
    recordRender('pivot-sum-policy-preview',sumStart);
    const sumProposal=report.authoringRequests.slice(sumRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(sumProposal?.status,200,'Native numeric SUM duplicate policy must preview successfully');
    const sumPreview=await proposalPreviewFor(sumProposal);
    const sumExpected=expectedPivotFor(sumProposal,sumPreview,'SUM');
    assert.equal(sumExpected.duplicateBuckets,0);
    assert.equal(sumPreview.rowCount,sumExpected.sourceRowCount);
    await assertExactRows({name:'pivot-sum-policy-independent-values-and-ids',columns:sumExpected.columns,rows:sumExpected.rows,rowLimit:100,panel:true});
    const secondPreviewStart=Date.now();
    const secondRequestIndex=report.authoringRequests.length;
    const sumProposalId=sumProposal.response.proposalId;
    await selectNative(page,'select[aria-label="Pivot duplicate policy"]','ERROR');
    await waitForObservable(page, ({ previousId }) => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]'); return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== previousId; }, { previousId: sumProposalId }, 10000);
    await drainPendingResponseReads();
    recordRender('pivot-repreview-after-cancel',secondPreviewStart);
    const proposalAgain=report.authoringRequests.slice(secondRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(proposalAgain?.status,200,'Pivot must preview again after a canceled proposal');
    const appliedPreview=await proposalPreviewFor(proposalAgain);
    const appliedExpected=expectedPivotFor(proposalAgain,appliedPreview,'ERROR');
    assert.equal(appliedExpected.operation.duplicatePolicy,'ERROR');
    assert.equal(appliedExpected.duplicateBuckets,0,'Every independent group/category pair must be unique under the selected duplicate policy');
    assert.equal(appliedPreview.rowCount,appliedExpected.sourceRowCount);
    await assertExactRows({name:'pivot-repreview-independent-values-and-ids',columns:appliedExpected.columns,rows:appliedExpected.rows,rowLimit:100,panel:true});
    const applyStart=Date.now();
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===3),10000);
    await assertExactRows({name:'pivot-apply-independent-values-and-ids',columns:appliedExpected.columns,rows:appliedExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(appliedPreview,'pivot-apply-stable-protocol-identities');
    recordRender('pivot-apply-to-render',applyStart);
    builder=await api(base+'/builder');
    const appliedDocument=doc(builder);
    const savedPivot=appliedDocument.construction?.steps?.find(step=>step.operation.kind==='PIVOT');
    assert(savedPivot,'Applied Pivot must be present in the saved draft');
    assert.equal(savedPivot.operation.pivot.duplicatePolicy,'ERROR');
    report.lifecycle={duplicatePolicy:'ERROR',duplicateBuckets:appliedExpected.duplicateBuckets,sourceRows:appliedExpected.sourceRowCount,categoryKeys:refreshedKeys,nativeNumericDuplicatePolicies:duplicateOptions.filter(option=>!option.disabled).map(option=>option.value)};
    const reloadPivotStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-reload-independent-values-and-ids',columns:appliedExpected.columns,rows:appliedExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(appliedPreview,'pivot-reload-stable-protocol-identities');
    recordRender('pivot-reload-to-render',reloadPivotStart);
    const history=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
    const pivotHistory=history.findLast(item=>/pivot|categories into columns/i.test(item.text));
    assert(pivotHistory,'Reloaded construction history must retain the Pivot step');
    const editStart=Date.now();
    await clickNative(page,`[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))),5000);
    await clickNative(page,'[data-testid^="construction-edit-step-"]');
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))),5000);
    const restoredEditor=await inspectPage(page,() => {
const p=document.querySelector('[data-testid="construction-reshape-pivot"]');return {category:p.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:p.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent,groups:[...p.querySelectorAll('input[aria-label^="Pivot group"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),duplicatePolicy:p.querySelector('select[aria-label="Pivot duplicate policy"]')?.value,outputs:[...p.querySelectorAll('input[aria-label^="Pivot output label"]')].map(input=>({label:input.getAttribute('aria-label'),value:input.value}))};
});
    assert(restoredEditor.category.includes('Observation.valueQuantity.code'));
    assert(restoredEditor.value.includes('Observation.valueQuantity.value'));
    assert.deepEqual(new Set(restoredEditor.groups),new Set(['Pivot group Specimen ID','Pivot group Patient FHIR resource ID','Pivot group Observation FHIR resource ID','Pivot group Observation.valueCodeableConcept.text']));
    assert.equal(restoredEditor.duplicatePolicy,'ERROR');
    recordRender('pivot-edit-restores-saved-fields',editStart);
    const editRequestIndex=report.authoringRequests.length;
    const previousEditProposalId=await inspectPage(page,() => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
    const editChangeStart=Date.now();
    await cda.action('fill Pivot output label d', page.getByLabel('Pivot output label d', { exact: true }), locator => locator.fill('Observed quantity d', { timeout: 5000 }), { timeout: 5000, budget: 5000, editable: true });
    await waitForObservable(page, ({ previousId }) => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]'); return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== previousId; }, { previousId: previousEditProposalId }, 10000);
    await drainPendingResponseReads();
    recordRender('pivot-edit-preview-to-ready',editChangeStart);
    const editProposal=report.authoringRequests.slice(editRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(editProposal?.status,200,'Edited Pivot must produce a native proposal');
    const editPreview=await proposalPreviewFor(editProposal);
    const editExpected=expectedPivotFor(editProposal,editPreview);
    assert.equal(editExpected.operation.duplicatePolicy,'ERROR');
    assert.equal(editExpected.duplicateBuckets,0);
    await assertExactRows({name:'pivot-edit-preview-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100,panel:true});
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===3),10000);
    await assertExactRows({name:'pivot-edit-apply-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(editPreview,'pivot-edit-apply-stable-protocol-identities');
    builder=await api(base+'/builder');
    const editedPivot=doc(builder).construction.steps.find(step=>step.operation.kind==='PIVOT');
    assert.equal(editedPivot.outputs.find(column=>column.name===editExpected.columns.find(column=>column.label==='Observed quantity d')?.column)?.label,'Observed quantity d');
    const reloadEditedStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-edited-reload-independent-values-and-ids',columns:editExpected.columns,rows:editExpected.rows,rowLimit:100});
    await assertPersistedPivotIdentity(editPreview,'pivot-edited-reload-stable-protocol-identities');
    recordRender('pivot-edited-reload-to-render',reloadEditedStart);
    const removeStart=Date.now();
    const editedHistory=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
    const editedPivotHistory=editedHistory.findLast(item=>/pivot|categories into columns/i.test(item.text));
    assert(editedPivotHistory,'Edited Pivot history control must remain available');
    await clickNative(page,`[data-testid=${JSON.stringify(editedPivotHistory.testId)}]`);
    await waitForObservable(page,() => Boolean(Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))),5000);
    const removalRequestIndex=report.authoringRequests.length;
    await clickNative(page,'[data-testid^="construction-remove-step-"]');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'),10000);
    await drainPendingResponseReads();
    const removalProposal=report.authoringRequests.slice(removalRequestIndex).findLast(request=>request.endpoint==='construction-proposals');
    assert.equal(removalProposal?.status,200,'Pivot removal must produce a native proposal');
    const removalPreview=await proposalPreviewFor(removalProposal);
    const sourcePreviewColumns=outputColumnValues(removalPreview.columns);
    const sourceRows=expected.map(values=>Object.fromEntries(sourcePreviewColumns.map(column=>{
      const label=column.label.toLowerCase();
      const index=label.includes('specimen id')?0:label.includes('patient fhir resource id')?1:label.includes('observation fhir resource id')?2:-1;
      assert(index>=0,`Pivot removal preview must restore a witnessed ID column, got ${column.label}`);
      return [column.column,values[index]];
    })));
    assert.equal(removalPreview.rowCount,sourceRows.length,'Pivot removal must restore the complete bounded source row count');
    assert.equal(removalPreview.rows.length,sourceRows.length,'Pivot removal preview must contain all 31 source rows at the selected 100-row limit');
    await assertExactRows({name:'pivot-remove-preview-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100,panel:true});
    await clickNative(page,'[data-testid="construction-apply-proposal"]');
    await waitForObservable(page,() => Boolean(document.querySelectorAll('[data-testid^="construction-history-step-"]').length===2&&!document.querySelector('[data-testid="construction-proposal-panel"]')),10000);
    await assertExactRows({name:'pivot-remove-apply-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100});
    recordRender('pivot-remove-to-render',removeStart);
    builder=await api(base+'/builder');
    assert(!doc(builder).construction.steps.some(step=>step.operation.kind==='PIVOT'),'Applied removal must delete the Pivot step from the saved draft');
    const restoredWorkspace=builder.workspace;
    const reloadRestoredStart=Date.now();
    await gotoPage(page,`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitForObservable(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), 10000);
    await clickNative(page,`[data-testid="construction-table-${outputId}"]`);
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false),10000);
    await assertExactRows({name:'pivot-remove-reload-restores-source-ids',columns:sourcePreviewColumns,rows:sourceRows,rowLimit:100});
    recordRender('pivot-remove-reload-to-render',reloadRestoredStart);
    report.lifecycle={...report.lifecycle,removed:true,restoredSourceRows:sourceRows.length,editOutputLabel:'Observed quantity d'};
    assert.deepEqual((await api(base+'/builder')).workspace,restoredWorkspace,'Pivot lifecycle reload must preserve the restored saved workspace');
    report.gaps=['Full-population category discovery remains unproven; its own independent full-population oracle is required. The bounded native lifecycle uses only the independently queried one-Specimen route witnesses.'];
    report.status='passed';
  }else{
    // This driver targets the local Compose --no-auth API. A different API must
    // supply its actual authorization scope before reusing this unrestricted oracle.
    const independent=runCDAQuantityCategoryOracle({
      project,dataset_generation:source.generation,scope_allowed:true,
      auth_resource_paths:[],auth_resource_paths_unrestricted:true,
    }, { container: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), database: (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE) });
    compareCDAQuantityCategoryValues(independent.oracle,discovery.response.categories);
    report.fullPopulationOracle=independent;
    report.oracle.scope='complete scoped Specimen→Patient→Observation population, local Compose no-auth authorization';
    report.categoryCorrectness='complete independent full-population route oracle passed';
    report.gaps=['Full-population discovery regression verified; native Pivot Apply/edit/removal is covered separately by the bounded mode.'];
    report.status='passed';
  }
  await drainPendingResponseReads();
  report.expectedValidationErrors=report.errors.filter(error => {
    const request=error.request;
    const pivot=request?.body?.candidateConstruction?.steps?.find(step=>step.operation.kind==='PIVOT')?.operation.pivot;
    return error.kind==='http' && error.status===422 && request?.endpoint==='construction-proposals' &&
      request.response?.error?.code==='TABLE_PIVOT_CELL_CARDINALITY' &&
      pivot?.duplicatePolicy==='ERROR' && pivot.groupKeyIds?.length===1 &&
      error.observedAfter==='quantity-category-discovery-to-render';
  });
  assert.deepEqual(report.errors.filter(error=>!report.expectedValidationErrors.includes(error)),[],
    'Unexpected HTTP, JavaScript or module errors must fail the Pivot regression');
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); report.__nativeFailure = true;
  await captureFailure(error, { phase: report.activeAction?.label ?? report.cases.at(-1)?.name,
    elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : undefined,
    action: report.activeAction, requestEvidence: report.nativeRequests.slice(-20), latestCase: report.cases.at(-1) });
  await drainPendingResponseReads();
  report.savedBuilderAtFailure=await api(base + '/builder').catch(error=>({readError:String(error)}));
  report.failureDOM = page ? await captureFailureDOM().catch(error=>({captureError:String(error)})) : undefined;
  report.failureUI = report.failureDOM?.bodyText;
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  report.pendingAuthoringRequests=report.authoringRequests.filter(request=>request.responseFinishedAtMs===undefined&&!request.loadingFailure).map(request=>({endpoint:request.endpoint,requestId:request.requestId,method:request.method,url:request.url,requestStartedAtMs:request.requestStartedAtMs,status:request.status,requestDraftVersion:request.requestDraftVersion,requestDraftDigest:request.requestDraftDigest,requestOutputId:request.requestOutputId,requestReceiptId:request.requestReceiptId}));
} finally {
  await drainPendingResponseReads();
  try {
    report.sourceFreeze = await sourceFreeze.assertUnchanged();
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === report.sourceFingerprint.after.sha256
      && report.sourceFingerprint.before.files === report.sourceFingerprint.after.files;
    if (!report.sourceFingerprint.unchanged) throw Object.assign(new Error('Source fingerprint changed during verification.'), { invalidatesRun: true });
  } catch (error) {
    report.priorStatus = report.status; report.status = 'invalidated';
    report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false };
    report.__nativeFailure = true;
  }
  try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
  catch (error) {
    report.priorStatus = report.status; report.status = 'invalidated';
    report.apiBuildFreeze = { checked: true, unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
    report.__nativeFailure = true;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-quantity-pivot-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-quantity-pivot-browser.mjs', report);
  return report;
}
