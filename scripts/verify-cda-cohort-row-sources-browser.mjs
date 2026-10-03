import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const resourceType = 'Specimen';
const explorer = `cohort-row-sources-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-cohort-row-sources-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2', '/selections');
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const apiBuildTarget = 'local-cda-api';
const readApiBuildStamp = () => checkContainerApiBuildStamp(localCDAApiContainer());
const report = { project, generation, resourceType, explorer, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });

let browser;
let builder;
let outputId;
let frozenApiBuild;
let frozenSource;
const nativeById = new Map();
const pendingNetworkReads = new Set();
const invalidateRun = (kind, reason) => {
  if (report.status !== 'invalidated') {
    report.priorStatus = report.status ?? 'not-started';
    if (report.error) report.priorError = report.error;
  }
  report.status = 'invalidated';
  report.productFailure = false;
  report.invalidations ??= [];
  if (!report.invalidations.some(item => item.kind === kind && item.reason === reason)) report.invalidations.push({ kind, reason });
  report.error = `${kind}: ${reason}`;
  process.exitCode = 1;
};
const finalizeFreeze = async (kind, check, failureDetails) => {
  const finishedAt = new Date().toISOString();
  try {
    const result = await check();
    report[kind] = { ...report[kind], ...result, finishedAt };
    if (result.invalidatesRun || result.unchanged === false) {
      invalidateRun(kind, result.reason ?? report[kind].reason ?? 'freeze was not established for the complete run');
    }
  } catch (error) {
    report[kind] = { ...report[kind], ...failureDetails(error), finishedAt };
    invalidateRun(kind, error.reason ?? String(error));
  }
};
const api = async (path, body, timeoutMs = 30000) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `cohort-row-sources-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(timeoutMs),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: path.endsWith('/builder') ? {
    draftVersion: value.draftVersion,
    draftDigest: value.draftDigest,
    catalog: { generation: value.catalog?.generation, authorizationScopeDigest: value.catalog?.authorizationScopeDigest },
    workspace: value.workspace,
  } : value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
const record = (name, started) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const openTable = async (expectedRowCount, expectedColumnCount) => {
  const started = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(expectedRowCount))}&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(expectedColumnCount))}&&!document.body.innerText.includes('Loading your table…')`);
  record('load-table', started);
};
const waitForLineage = async (started) => {
  const deadline = started + 5000;
  while (Date.now() < deadline) {
    const response = report.nativeRequests.findLast(request => request.path === base + '/row-lineage' && request.startedAt >= started && request.completedAt && request.response);
    if (response) return response;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail('Native row inspection did not complete a fresh row-lineage request within five seconds');
};
const inspectCohortRow = async name => {
  const started = Date.now();
  await click(browser.cdp, 'button[aria-label="Inspect row 1 identity"]');
  const selector = '[role="dialog"][aria-label="Row 1 identity"]';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(selector)})`);
  const panelText = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(selector)}).innerText;`);
  assert(panelText.includes('Source records in this row'), panelText);
  assert(!/cannot be listed|unavailable|could not be fully listed|Could not load/i.test(panelText), panelText);
  await waitForBrowser(browser.cdp, `document.querySelectorAll(${JSON.stringify(selector + ' ul li')}).length===${report.oracle.sources.length}&&!document.querySelector(${JSON.stringify(selector)}).innerText.includes('Loading source records…')`);
  const native = await waitForLineage(started);
  assert.equal(native.status, 200, JSON.stringify(native));
  assert.equal(native.response.status, 'COMPLETE', JSON.stringify(native.response));
  assert.equal(native.response.receiptId, native.body.receiptId);
  assert.equal(native.response.outputId, outputId);
  assert.equal(native.response.rowId, native.body.rowId);
  const expected = report.oracle.sources.map(source => `${resourceType}/${source.id}`).sort();
  const listed = await browserEval(browser.cdp, `return [...document.querySelectorAll(${JSON.stringify(selector + ' ul li')})].map(item=>item.innerText.trim()).sort();`);
  assert.deepEqual(listed, expected, 'Native source-record panel must list exactly the independently pinned cohort members');
  assert.deepEqual(native.response.contributors.map(item => `${item.resourceType}/${item.resourceId}`).sort(), expected,
    'Row-lineage response must have the exact independently pinned contributor set');
  const sourceContributors = native.response.contributors
    .map(({ resourceType: contributorType, resourceId, occurrenceKey }) => ({ resourceType: contributorType, resourceId, occurrenceKey }))
    .sort((left, right) => `${left.resourceType}/${left.resourceId}`.localeCompare(`${right.resourceType}/${right.resourceId}`));
  assert.equal(new Set(listed).size, expected.length, 'Each pinned member must appear once');
  assert.equal(native.response.hasMore ?? false, false, 'The complete two-member cohort must fit in one lineage page');
  const identity = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(selector + ' p.font-mono')})?.textContent;`);
  assert(identity);
  const rowId = native.body.rowId;
  assert.equal(typeof rowId, 'string', 'The native row inspector must submit the displayed row identity as a string');
  let rowIdObject;
  try { rowIdObject = JSON.parse(rowId); } catch { assert.fail(`Cohort row identity is not serialized JSON: ${rowId}`); }
  assert.equal(identity, rowId, 'The displayed Preview identity and the native inspector request must match exactly');
  assert(rowIdObject && typeof rowIdObject === 'object' && !Array.isArray(rowIdObject), `Cohort row identity must be a JSON object: ${rowId}`);
  assert.deepEqual(Object.keys(rowIdObject).sort(), ['group_id', 'group_revision_id']);
  assert.equal(rowIdObject.group_id, 'qa-cohort');
  assert.equal(rowIdObject.group_revision_id, report.cohort.revisionId);
  report.inspections ??= [];
  report.inspections.push({ name, identity, rowId, rowIdObject, count: listed.length, contributors: listed, sourceContributors, lineageStatus: native.response.status, receiptId: native.response.receiptId });
  await browserEval(browser.cdp, `const button=[...document.querySelectorAll(${JSON.stringify(selector + ' button')})].find(item=>item.innerText==='Close');button.dataset.qaCohortClose='true';return true;`);
  await click(browser.cdp, '[data-qa-cohort-close="true"]');
  await waitForBrowser(browser.cdp, `!document.querySelector(${JSON.stringify(selector)})`);
  record(name, started);
  return { identity, rowId, rowIdObject, receiptId: native.body.receiptId, sourceContributors };
};

const traceCohortCell = async (name, inspection, cellColumn) => {
  assert(cellColumn, 'The saved cohort must expose a traceable resourceType output column');
  const traceStarted = Date.now();
  const fetchCellTracePage = async offset => api(base + '/cell-trace', {
    receiptId: inspection.receiptId,
    outputId,
    rowId: inspection.rowId,
    column: cellColumn,
    offset,
    limit: 10,
  }, 5000);
  const firstPage = await fetchCellTracePage(0);
  const pages = [firstPage];
  let pageOffset = 0;
  while (pages.at(-1).trace.hasMore) {
    const previous = pages.at(-1);
    const nextOffset = previous.trace.nextOffset;
    assert(Number.isInteger(nextOffset) && nextOffset > pageOffset,
      `CellTrace pagination must advance beyond offset ${pageOffset}`);
    assert(pages.length < 20, 'CellTrace exceeded the verifier page bound');
    const nextPage = await fetchCellTracePage(nextOffset);
    assert.equal(nextPage.binding.receiptId, firstPage.binding.receiptId);
    assert.equal(nextPage.binding.outputId, firstPage.binding.outputId);
    assert.equal(nextPage.binding.project, firstPage.binding.project);
    assert.equal(nextPage.binding.generation, firstPage.binding.generation);
    assert.equal(nextPage.binding.scopeDigest, firstPage.binding.scopeDigest);
    assert.equal(nextPage.feature.column, firstPage.feature.column);
    assert.equal(nextPage.trace.rowId, firstPage.trace.rowId);
    assert.equal(nextPage.trace.column, firstPage.trace.column);
    assert.equal(nextPage.trace.status, firstPage.trace.status);
    assert.deepEqual(nextPage.trace.value, firstPage.trace.value);
    pages.push(nextPage);
    pageOffset = nextOffset;
  }
  const cellTrace = firstPage;
  const contributions = pages.flatMap(page => page.trace.contributions ?? []);
  const traceDurationMs = Date.now() - traceStarted;
  assert(traceDurationMs <= 5000, `${name} CellTrace took ${traceDurationMs}ms`);
  assert.equal(cellTrace.binding.receiptId, inspection.receiptId);
  assert.equal(cellTrace.binding.outputId, outputId);
  assert.equal(cellTrace.binding.project, project);
  assert.equal(cellTrace.binding.explorerId, explorer);
  assert.equal(cellTrace.binding.generation, generation);
  assert.equal(cellTrace.binding.scopeDigest, report.cohort.sourceScopeDigest);
  assert.equal(cellTrace.feature.column, cellColumn);
  assert.equal(cellTrace.feature.sourceResourceType, resourceType);
  assert.equal(cellTrace.feature.sourcePath, 'resourceType');
  assert.deepEqual(JSON.parse(cellTrace.trace.rowId), inspection.rowIdObject, 'CellTrace must resolve the Preview row identity submitted by the native inspector');
  assert.equal(cellTrace.trace.column, cellColumn);
  assert.equal(cellTrace.trace.complete, true, JSON.stringify(cellTrace.trace));
  assert.equal(cellTrace.trace.status, 'VALUE', JSON.stringify(cellTrace.trace));
  assert.deepEqual(cellTrace.trace.value, [resourceType], 'The ALL member-field trace must return exactly the distinct scoped Specimen value');
  assert.equal(pages.at(-1).trace.hasMore, false, 'CellTrace contributor pagination must finish before exact comparison');
  const expectedContributorTuples = report.oracle.cellTraceContributors;
  const tracedContributorTuples = contributions.map(item => {
    assert.equal(item.resourceType, resourceType, 'Each CellTrace contributor must identify the authorized source resource type');
    assert(item.resourceId, 'Each CellTrace contributor must identify its authorized source resource ID');
    return { resourceType: item.resourceType, resourceId: item.resourceId, value: item.value };
  }).sort((left, right) => `${left.resourceType}/${left.resourceId}/${JSON.stringify(left.value)}`
    .localeCompare(`${right.resourceType}/${right.resourceId}/${JSON.stringify(right.value)}`));
  assert.deepEqual(tracedContributorTuples, expectedContributorTuples,
    'CellTrace must return the exact scoped source ID/value tuples independently read from the pinned FHIR documents');
  const tupleKeys = tracedContributorTuples.map(item => `${item.resourceType}\u0000${item.resourceId}\u0000${JSON.stringify(item.value)}`);
  assert.equal(new Set(tupleKeys).size, expectedContributorTuples.length, 'Each source/value contributor tuple must appear once');
  const expectedRefs = expectedContributorTuples.map(item => `${item.resourceType}/${item.resourceId}`).sort();
  const tracedRefs = tracedContributorTuples.map(item => `${item.resourceType}/${item.resourceId}`).sort();
  assert.deepEqual(tracedRefs, expectedRefs, 'CellTrace contributor source IDs must remain inside the exact authorized cohort');
  report.cellTraces ??= [];
  const result = {
    name,
    mode: 'API-driven receipt-bound CellTrace after the native row inspector',
    rowId: inspection.rowId,
    rowIdObject: inspection.rowIdObject,
    rowLineageReceiptId: inspection.receiptId,
    receiptId: cellTrace.binding.receiptId,
    outputId,
    column: cellColumn,
    status: cellTrace.trace.status,
    complete: cellTrace.trace.complete,
    value: cellTrace.trace.value,
    sourceContributors: inspection.sourceContributors,
    contributorTuples: tracedContributorTuples,
    contributorPageCount: pages.length,
    contributorPages: pages.map(page => ({ hasMore: page.trace.hasMore, nextOffset: page.trace.nextOffset, count: page.trace.contributions?.length ?? 0 })),
    cellTraceSourceRefs: tracedRefs,
    cellTraceSourceContributorsSupported: true,
    contributions,
    durationMs: traceDurationMs,
  };
  report.cellTraces.push(result);
  record(`${name}-cell-trace`, traceStarted);
  return { ...result, trace: { ...cellTrace.trace, contributions, hasMore: false } };
};

try {
  const apiBuildStartedAt = new Date().toISOString();
  report.apiBuildFreeze = { target: apiBuildTarget, startedAt: apiBuildStartedAt };
  frozenApiBuild = await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: frozenApiBuild.initial };
  const sourceFreezeStartedAt = new Date().toISOString();
  report.sourceFreeze = { startedAt: sourceFreezeStartedAt, available: false };
  try {
    frozenSource = await captureSourceFreeze(sourceRoot);
    report.sourceFreeze = { ...report.sourceFreeze, available: true, watchedFileCount: frozenSource.watchedFileCount };
  } catch (error) {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
    };
    const captureError = new Error(`Initial source freeze capture failed: ${String(error)}`);
    captureError.initialSourceFreezeFailure = true;
    throw captureError;
  }
  const query = `FOR s IN Specimen FILTER s.project=="${project}" AND s.dataset_generation=="${generation}" SORT s._key LIMIT 2 RETURN {id:s.id,resourceType:s.resourceType,fieldValue:s.payload.resourceType,generation:s.dataset_generation,project:s.project}`;
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const sources = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert.equal(sources.length, 2, 'The independent pinned cohort fixture must contain exactly two rows');
  assert.equal(new Set(sources.map(source => source.id)).size, 2, 'The pinned FHIR IDs must be distinct');
  assert(sources.every(source => source.project === project && source.generation === generation && source.resourceType === resourceType));
  const cellTraceContributors = sources.map(source => ({
    resourceType: source.resourceType,
    resourceId: source.id,
    value: source.fieldValue,
  })).sort((left, right) => `${left.resourceType}/${left.resourceId}/${JSON.stringify(left.value)}`
    .localeCompare(`${right.resourceType}/${right.resourceId}/${JSON.stringify(right.value)}`));
  report.oracle = { query, sources, cellTraceContributors };

  await api(root, { name: explorer, title: 'Named cohort source inspection QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const scopeDigest = builder.catalog.authorizationScopeDigest;
  assert(scopeDigest, 'The active catalog must expose its authorization scope digest');
  const node = builder.catalog.nodes.find(candidate => candidate.resourceType === resourceType);
  assert(node, `The ${resourceType} catalog node must exist`);
  await command([{ type: 'CREATE_TABLE', title: 'Pinned named cohort QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The starting table must have a direct FHIR ID field');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'FHIR ID' }]);

  const selection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: sources.map(source => ({ project, generation, resourceType, id: source.id })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, resourceType);
  assert.equal(selection.scopeDigest, scopeDigest, 'Pinned selection must use the active catalog authorization scope');
  assert.equal(selection.memberCount, sources.length);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct, 'The pinned source selection must have a direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);

  const selectionPage = await api(`${selections}/${selection.id}?limit=100`);
  assert.equal(selectionPage.revision.id, selection.id);
  assert.equal(selectionPage.revision.scopeDigest, scopeDigest);
  assert.equal(selectionPage.revision.project, project);
  assert.equal(selectionPage.revision.generation, generation);
  assert.equal(selectionPage.revision.resourceType, resourceType);
  assert.equal(selectionPage.revision.memberCount, sources.length);
  const members = selectionPage.members;
  assert.equal(members.length, sources.length);
  const selectedRefs = members.map(member => member.ref).map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const oracleRefs = sources.map(source => `${project}/${generation}/${resourceType}/${source.id}`).sort();
  assert.deepEqual(selectedRefs, oracleRefs, 'Selection members must match only the pinned project, generation, type, and FHIR IDs');
  assert(members.every(member => member.memberKey), 'Every selected member must have an opaque key for exact cohort binding');
  const cohort = await api(`${selections}/${selection.id}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: randomUUID(),
    groups: [{ id: 'qa-cohort', label: 'Two Specimens', ordinal: 0, memberIds: members.map(member => member.memberKey) }],
  });
  assert.equal(cohort.sourceSelectionRevisionId, selection.id);
  assert.equal(cohort.groupCount, 1);
  assert.equal(cohort.memberCount, sources.length);
  assert.deepEqual(cohort.groups.map(group => ({ id: group.id, label: group.label, memberCount: group.memberCount })), [{ id: 'qa-cohort', label: 'Two Specimens', memberCount: sources.length }]);
  report.cohort = { ...cohort, sourceScopeDigest: scopeDigest, sourceRefs: selectedRefs };

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.consoleAPICalled', event => { if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args }); });
  browser.cdp.on('Runtime.exceptionThrown', event => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Network.loadingFailed', event => { if (event.type === 'Script' && event.errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: event.errorText }); });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeById.get(requestId);
    if (entry) entry.status = response.status;
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url });
  });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime }) => {
    const url = new URL(request.url);
    if (!url.pathname.startsWith(base + '/')) return;
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = request.postData; }
    const entry = { requestId, path: url.pathname, method: request.method, startedAt: wallTime ? Math.round(wallTime * 1000) : Date.now(), body };
    nativeById.set(requestId, entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeById.get(requestId);
    if (!entry) return;
    entry.completedAt = Date.now();
    if (!entry.path.endsWith('/row-lineage')) return;
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const text = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      entry.response = JSON.parse(text);
    }).catch(error => { entry.responseReadError = String(error); }).finally(() => pendingNetworkReads.delete(read));
    pendingNetworkReads.add(read);
  });

  await openTable(sources.length + 1, 1);
  const startRows = Date.now();
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(rowShapeSelector)})?.disabled===false`);
  const cohortShape = `explicit:${cohort.revisionId}`;
  const options = await browserEval(browser.cdp, `return [...document.querySelector(${JSON.stringify(rowShapeSelector)}).options].map(option=>({value:option.value,disabled:option.disabled,text:option.text}));`);
  report.rowShapeOptions = options;
  assert(options.some(option => option.value === cohortShape && !option.disabled), 'The saved named cohort must appear as a usable row shape: ' + JSON.stringify(options));
  await selectOption(browser.cdp, rowShapeSelector, cohortShape);
  const policySelector = 'select[aria-label="Unmatched record policy"]';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(policySelector)})?.disabled===false`);
  await selectOption(browser.cdp, policySelector, `explicit:${cohort.revisionId}:ERROR`);
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  const comparison = await browserEval(browser.cdp, `return document.querySelector('[aria-label="Row definition preview"]')?.innerText;`);
  assert(comparison?.includes('2 rows → 1 rows'), comparison ?? 'The row definition preview must collapse the two pinned records to one cohort row');
  await click(browser.cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:')`);
  record('apply-named-cohort-row-shape', startRows);
  builder = await api(base + '/builder');
  const groupedDocument = doc(builder);
  assert.equal(groupedDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(groupedDocument.rows.groups.source.explicit.unassignedMemberPolicy, 'ERROR');
  assert.equal(groupedDocument.population.selectionRevisionId, selection.id);
  const savedInspection = await inspectCohortRow('inspect-named-cohort-row');

  const beforeField = builder;
  const fieldStart = Date.now();
  await click(browser.cdp, '[data-testid="construction-action-add-columns"]');
  await click(browser.cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-add-columns-source"]')`);
  await click(browser.cdp, '[data-testid="feature-catalog-raw-fields"] summary');
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Select Specimen.resourceType"]:not(:disabled)')`);
  await click(browser.cdp, 'input[aria-label="Select Specimen.resourceType"]');
  await click(browser.cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(browser.cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
  const fieldProposal = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:panel.dataset.proposalStatus,text:panel.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  report.fieldProposal = fieldProposal;
  assert.equal(fieldProposal.status, 'ready', fieldProposal.text);
  assert.equal(fieldProposal.rows.length, 1, 'The named cohort field must preview one group row');
  assert.equal(fieldProposal.rows[0].at(-1), resourceType, 'The retained member field must be computed from the pinned cohort');
  await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='4'`);
  record('apply-retained-cohort-member-field', fieldStart);
  builder = await api(base + '/builder');
  const fieldDocument = doc(builder);
  assert.equal(fieldDocument.rows.groups.source.explicit.revisionId, cohort.revisionId, 'Adding a member field must retain the named cohort row binding');
  assert.equal(fieldDocument.population.selectionRevisionId, selection.id, 'Adding a member field must retain the pinned source selection');
  assert.deepEqual(doc(beforeField).rows.groups, groupedDocument.rows.groups);
  assert.deepEqual(fieldDocument.rows.groups.rowValues.map(value => value.policy), ['ALL'], 'The saved resourceType member field must retain its native ALL policy');
  const resourceTypeColumn = fieldDocument.columns.find(column => column.source.kind === 'field' && column.source.field?.path === 'resourceType');
  assert(resourceTypeColumn?.column, 'The saved cohort must expose the applied Specimen.resourceType output column');
  const savedFieldInspection = await inspectCohortRow('inspect-saved-cohort-row-after-field-apply');
  assert.equal(savedFieldInspection.rowId, savedInspection.rowId, 'Applying the member field must retain the cohort row identity');
  const savedCellTrace = await traceCohortCell('saved-cohort-cell-trace', savedFieldInspection, resourceTypeColumn.column);

  await openTable(2, 4);
  const afterReload = await api(base + '/builder');
  assert.equal(doc(afterReload).rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(doc(afterReload).population.selectionRevisionId, selection.id);
  const reloadedResourceTypeColumn = doc(afterReload).columns.find(column => column.source.kind === 'field' && column.source.field?.path === 'resourceType');
  assert.equal(reloadedResourceTypeColumn?.column, resourceTypeColumn.column, 'Reload must retain the exact typed resourceType output column');
  const cells = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim());`);
  assert.equal(cells.length, 4);
  assert(cells.includes('Two Specimens'), 'Reloaded cohort row must retain its named group label');
  assert(cells.includes(resourceType), 'Reloaded cohort row must retain the member field');
  assert(sources.every(source => cells.some(cell => cell.includes(source.id))), 'Reloaded cohort row must retain both pinned member IDs');
  const reloadedInspection = await inspectCohortRow('inspect-reloaded-named-cohort-row');
  assert.equal(reloadedInspection.identity, savedFieldInspection.identity, 'The row identity must survive the field edit and reload');
  assert.equal(reloadedInspection.rowId, savedFieldInspection.rowId, 'CellTrace must receive the same serialized object row identity after reload');
  assert.deepEqual(reloadedInspection.rowIdObject, savedFieldInspection.rowIdObject);
  assert.deepEqual(
    reloadedInspection.sourceContributors.map(item => `${item.resourceType}/${item.resourceId}`).sort(),
    savedFieldInspection.sourceContributors.map(item => `${item.resourceType}/${item.resourceId}`).sort(),
    'Reload must retain the exact typed source contributors',
  );
  const reloadedCellTrace = await traceCohortCell('reloaded-cohort-cell-trace', reloadedInspection, reloadedResourceTypeColumn.column);
  assert.deepEqual(JSON.parse(reloadedCellTrace.trace.rowId), JSON.parse(savedCellTrace.trace.rowId), 'CellTrace must retain the same complete structured identity after reload');
  assert.equal(reloadedCellTrace.cellTraceSourceContributorsSupported, savedCellTrace.cellTraceSourceContributorsSupported, 'CellTrace contributor support must be stable after reload');
  assert.deepEqual(reloadedCellTrace.cellTraceSourceRefs, savedCellTrace.cellTraceSourceRefs, 'Typed CellTrace contributors must be stable after reload');
  assert.deepEqual(reloadedCellTrace.contributorTuples, savedCellTrace.contributorTuples, 'Exact CellTrace source/value tuples must be stable after reload');
  report.cellTraceCoverage = {
    mode: 'API-driven receipt-bound CellTrace for a native Preview object identity after member-field Apply',
    saved: { receiptId: savedCellTrace.receiptId, rowId: savedFieldInspection.rowId, value: savedCellTrace.value, status: savedCellTrace.status },
    reloaded: { receiptId: reloadedCellTrace.receiptId, rowId: reloadedInspection.rowId, value: reloadedCellTrace.value, status: reloadedCellTrace.status },
    stableRowIdentity: savedFieldInspection.rowId === reloadedInspection.rowId,
    sameReceiptWithinEachCellTrace: report.cellTraces.every(trace => trace.receiptId === trace.rowLineageReceiptId),
    sourceContributors: savedFieldInspection.sourceContributors,
    cellTraceSourceContributorsSupported: savedCellTrace.cellTraceSourceContributorsSupported,
    contributorRefs: report.oracle.cellTraceContributors.map(item => `${item.resourceType}/${item.resourceId}`).sort(),
    expectedContributorTuples: report.oracle.cellTraceContributors,
    savedContributorTuples: savedCellTrace.contributorTuples,
    reloadedContributorTuples: reloadedCellTrace.contributorTuples,
  };
  assert(report.cellTraceCoverage.stableRowIdentity);
  assert(report.cellTraceCoverage.sameReceiptWithinEachCellTrace);
  assert(report.cases.length <= 10, `The verifier must stay within ten bounded sequences; saw ${report.cases.length}`);
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  const apiBuildInvalidated = error instanceof ApiBuildFreezeError;
  const sourceFreezeInvalidated = error.initialSourceFreezeFailure;
  report.status = apiBuildInvalidated || sourceFreezeInvalidated ? 'invalidated' : 'failed';
  report.error = String(error.stack ?? error);
  if (apiBuildInvalidated) {
    report.apiBuildFreeze = {
      ...report.apiBuildFreeze,
      initial: error.before,
      ...(error.after?.checked ? { after: error.after } : {}),
      unchanged: false,
      invalidatesRun: true,
      productFailure: false,
      reason: error.reason,
    };
    report.priorStatus = 'not-started';
    report.productFailure = false;
    report.invalidations = [{ kind: 'apiBuildFreeze', reason: error.reason }];
  } else if (sourceFreezeInvalidated) {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: report.sourceFreeze.error ?? String(error),
    };
    report.priorStatus = 'not-started';
    report.productFailure = false;
    report.invalidations = [{ kind: 'sourceFreeze', reason: error.message }];
  }
  process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  await Promise.allSettled([...pendingNetworkReads]);
  if (frozenSource) await finalizeFreeze('sourceFreeze', () => frozenSource.assertUnchanged(), error => ({
    unchanged: false,
    changedPaths: error.changedPaths ?? [],
    invalidatesRun: true,
    productFailure: false,
    error: String(error),
  }));
  await finalizeFreeze('apiBuildFreeze', async () => {
    if (frozenApiBuild) return frozenApiBuild.assertUnchanged();
    const finalOnly = await captureApiBuildFreeze(readApiBuildStamp);
    return { after: finalOnly.initial, unchanged: false, invalidatesRun: true, productFailure: false };
  }, error => ({
    ...(frozenApiBuild
      ? { ...(error.before ? { initial: error.before } : {}), ...(error.after ? { after: error.after } : {}) }
      : error.before ? { after: error.before } : {}),
    unchanged: false,
    invalidatesRun: true,
    productFailure: false,
    ...(error.reason ? { reason: error.reason } : {}),
    error: String(error),
  }));
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(item => ({ name: item.name, durationMs: item.durationMs })), error: report.error }));
