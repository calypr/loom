import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { performAction } from './lib/playwright-actions.mjs';

export async function cohortRowSourcesWorkflow({ page, cda, cohortRowValueCase = process.env.LOOM_COHORT_ROW_VALUE_CASE ?? 'default' }) {
const project = cda.project;
const generation = cda.generation;
const resourceType = 'Specimen';
const explorer = `cohort-row-sources-${Date.now()}`;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = cda.target.arangoContainer;
assert(apiOrigin && uiOrigin, 'The CDA fixture must provide an isolated API and UI origin.');
assert.equal(generation, 'cda-fhir-v1', 'Cohort row sources require the cda-fhir-v1 fixture generation.');
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
const selections = base.replace('/authoring/v2', '/selections');
const report = cda.report;
Object.assign(report, { project, generation, resourceType, explorer, cohortRowValueCase, cases: [], requests: [], started: new Date().toISOString() });
report.target = cda.target;
let builder;
let outputId;
const requestCapture = cda.captureRequests(base);
const inspect = callback => page.evaluate(callback);
const waitUI = (condition, timeout = 5000) => page.waitForFunction(condition, undefined, { timeout: Math.min(timeout, 5000) });
const navigateUI = url => page.goto(url, { waitUntil: 'domcontentloaded', timeout: 5000 });
const clickUI = (selector, options = {}) => {
  let locator = page.locator(selector);
  if (options.name) locator = locator.and(page.getByRole('button', { name: options.name, exact: true }));
  if (options.includes) locator = locator.and(page.getByRole('button', { name: new RegExp(options.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') }));
  return performAction(report, options.name ?? options.includes ?? selector, locator, target => target.click({ timeout: 5000 }));
};
const selectUI = async (selector, value, options = {}) => {
  const locator = page.locator(selector);
  await performAction(report, `Select ${value}`, locator, (target, { timeout }) => target.selectOption(value, { timeout }));
  if (options.settledWhen) await waitUI(options.settledWhen, 5000);
  else await waitUI(`document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(value)}`, 5000);
  if (options.dismissSelector) await clickUI(options.dismissSelector);
};
const requestBody = entry => requestCapture.rawRequestBody(entry) ?? entry.body;
const responseBody = entry => requestCapture.rawResponseBody(entry) ?? entry.response;
const safeEvidence = (value, key = '') => {
  if (/authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i.test(key)) return '[REDACTED]';
  if (Array.isArray(value)) return value.map(item => safeEvidence(item));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([childKey, child]) => [childKey, safeEvidence(child, childKey)]));
  return value;
};
const api = async (path, body, timeoutMs = 30000) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `cohort-row-sources-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(timeoutMs),
  });
  const value = await response.json();
  report.requests.push({ path, body: safeEvidence(body), status: response.status, response: safeEvidence(path.endsWith('/builder') ? {
    draftVersion: value.draftVersion,
    draftDigest: value.draftDigest,
    catalog: { generation: value.catalog?.generation, authorizationScopeDigest: value.catalog?.authorizationScopeDigest },
    workspace: value.workspace,
  } : value) });
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
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: 5000 });
  await waitUI(`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(expectedRowCount))}&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(expectedColumnCount))}&&!document.body.innerText.includes('Loading your table…')`);
  record('load-table', started);
};
const waitForNativeRequest = async (fromIndex, predicate, label, timeout = 5000) => {
  const captured = await requestCapture.waitFor(entry =>
    report.nativeRequests.indexOf(entry) >= fromIndex && predicate({
      ...entry, body: requestBody(entry), response: responseBody(entry),
    }), { timeout, label });
  return { ...captured, body: requestBody(captured), response: responseBody(captured) };
};
const waitForLineage = async started => waitForNativeRequest(0,
  entry => entry.path === base + '/row-lineage' && entry.startedAt >= started && entry.response,
  'Native row inspection did not complete a fresh row-lineage request within five seconds', Math.max(1, started + 5000 - Date.now()));
const waitForNativePreview = async (fromIndex, label) => {
  const preview = await waitForNativeRequest(fromIndex,
    entry => entry.path.endsWith('/preview') && entry.body?.outputId === outputId,
    label);
  preview.body = requestBody(preview);
  preview.response = responseBody(preview);
  assert.equal(preview.status, 200, JSON.stringify(preview));
  assert(preview.response, `${label} response body was unavailable: ${preview.responseReadError ?? JSON.stringify(preview)}`);
  assert(Array.isArray(preview.response.rows), `${label} response has no rows: ${JSON.stringify(preview.response).slice(0, 1000)}`);
  return preview;
};
const inspectCohortRow = async name => {
  const started = Date.now();
  await clickUI('button[aria-label="Inspect row 1 identity"]');
  const selector = '[role="dialog"][aria-label="Row 1 identity"]';
  await page.locator(selector).waitFor({ state: 'visible', timeout: 5000 });
  const panelText = await page.locator(selector).innerText();
  assert(panelText.includes('Source records in this row'), panelText);
  assert(!/cannot be listed|unavailable|could not be fully listed|Could not load/i.test(panelText), panelText);
  await waitUI(`document.querySelectorAll(${JSON.stringify(selector + ' ul li')}).length===${report.oracle.sources.length}&&!document.querySelector(${JSON.stringify(selector)}).innerText.includes('Loading source records…')`, 5000);
  const native = await waitForLineage(started);
  assert.equal(native.status, 200, JSON.stringify(native));
  assert.equal(native.response.status, 'COMPLETE', JSON.stringify(native.response));
  assert.equal(native.response.receiptId, native.body.receiptId);
  assert.equal(native.response.outputId, outputId);
  assert.equal(native.response.rowId, native.body.rowId);
  const expected = report.oracle.sources.map(source => `${resourceType}/${source.id}`).sort();
  const listed = await page.locator(`${selector} ul li`).evaluateAll(items => items.map(item=>item.innerText.trim()).sort());
  assert.deepEqual(listed, expected, 'Native source-record panel must list exactly the independently pinned cohort members');
  assert.deepEqual(native.response.contributors.map(item => `${item.resourceType}/${item.resourceId}`).sort(), expected,
    'Row-lineage response must have the exact independently pinned contributor set');
  const sourceContributors = native.response.contributors
    .map(({ resourceType: contributorType, resourceId, occurrenceKey }) => ({ resourceType: contributorType, resourceId, occurrenceKey }))
    .sort((left, right) => `${left.resourceType}/${left.resourceId}`.localeCompare(`${right.resourceType}/${right.resourceId}`));
  assert.equal(new Set(listed).size, expected.length, 'Each pinned member must appear once');
  assert.equal(native.response.hasMore ?? false, false, 'The complete two-member cohort must fit in one lineage page');
  const identity = await page.locator(`${selector} p.font-mono`).textContent();
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
  await clickUI(`${selector} button`, { name: 'Close' });
  await page.locator(selector).waitFor({ state: 'detached', timeout: 5000 });
  record(name, started);
  return { identity, rowId, rowIdObject, receiptId: native.body.receiptId, sourceContributors };
};

const traceCohortCell = async (
  name,
  inspection,
  cellColumn,
  expectedValue = [resourceType],
  policy = 'ALL',
  sourcePath = 'resourceType',
  expectedContributorTuples = report.oracle.cellTraceContributors,
) => {
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
  assert.equal(cellTrace.feature.sourcePath, sourcePath);
  assert.deepEqual(JSON.parse(cellTrace.trace.rowId), inspection.rowIdObject, 'CellTrace must resolve the Preview row identity submitted by the native inspector');
  assert.equal(cellTrace.trace.column, cellColumn);
  assert.equal(cellTrace.trace.complete, true, JSON.stringify(cellTrace.trace));
  assert.equal(cellTrace.trace.status, 'VALUE', JSON.stringify(cellTrace.trace));
  assert.deepEqual(cellTrace.trace.value, expectedValue, `The ${policy} member-field trace must return the exact distinct scoped Specimen value`);
  assert.equal(pages.at(-1).trace.hasMore, false, 'CellTrace contributor pagination must finish before exact comparison');
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
    policy,
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

const chooseRootFieldWithPolicy = async (policy, fieldPath = 'resourceType') => {
  const editorOpen = await page.getByTestId('construction-add-columns-source').count().then(count => count > 0);
  if (!editorOpen) {
    await clickUI('[data-testid="construction-action-add-columns"]');
    await clickUI('[aria-label="Column types"] button', { includes: 'Fields and related data' });
  } else {
    const fieldsMode = await page.locator('[aria-label="Column types"] button').evaluateAll(buttons => buttons.some(button=>button.innerText.includes('Fields and related data')&&button.getAttribute('aria-pressed')==='true'));
    if (!fieldsMode) await clickUI('[aria-label="Column types"] button', { includes: 'Fields and related data' });
  }
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 5000);
  const policyControl = await page.locator('select[aria-label="Values per grouped row"]').evaluate(control => [...control.options].map(option=>option.value));
  assert.deepEqual(policyControl, ['ALL', 'ONE'], 'The native grouped-row policy selector must retain both supported choices');
  await selectUI('select[aria-label="Values per grouped row"]', policy);
  const rawFieldsOpen = await page.getByTestId('feature-catalog-raw-fields').evaluate(details => details.open);
  if (!rawFieldsOpen) await clickUI('[data-testid="feature-catalog-raw-fields"] summary');
  const checkbox = `input[aria-label=${JSON.stringify(`Select Specimen.${fieldPath.replace(/^root\./, '')}`)}]`;
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(`${checkbox}:not(:disabled)`)}))`, 5000);
  const checked = await page.locator(checkbox).isChecked();
  if (!checked) await clickUI(checkbox);
  await clickUI('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitUI(`['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`, 5000);
  return page.getByTestId('construction-choice-proposal-panel').evaluate(panel => ({status:panel.dataset.proposalStatus,text:panel.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}));
};

const changeSavedMemberPolicy = async (columnId, expectedPolicy, nextPolicy, { expectRejected = false, allowTransformed = false } = {}) => {
  const currentDocument = doc(builder);
  assert.equal(currentDocument.rows.kind, 'GROUPS');
  assert.equal(currentDocument.rows.groups.source.kind, 'EXPLICIT');
  const binding = currentDocument.rows.groups.rowValues.find(value => value.columnId === columnId);
  assert(binding, `Saved member policy must retain stable columnId ${columnId}`);
  assert.equal(binding.policy, expectedPolicy, `Stable columnId ${columnId} must begin at ${expectedPolicy}`);
  const column = currentDocument.columns.find(value => value.columnId === columnId);
  assert(column, `Saved member policy columnId ${columnId} must resolve to a configured column`);
  assert.equal(column.occurrenceId, 'base', 'The policy-edit target must remain a root FHIR field');
  assert.equal(column.source.kind, 'field');
  assert(column.source.field, 'The policy-edit target must remain a source field');
  if (allowTransformed) {
    assert.equal(column.valueTransformation?.kind, 'EXACT_CATEGORY_RECODE');
    assert.equal(column.logicalType?.toLowerCase(), 'string');
  } else {
    assert(!column.valueTransformation, 'Untransformed policy-edit cases must remain untransformed');
  }
  const previewAriaRowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  assert(previewAriaRowCount, 'The saved cohort preview must expose its existing row count');
  const snapshot = {
    previewAriaRowCount,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    snapshotToken: builder.catalog.snapshotToken,
    document: structuredClone(currentDocument),
    groupSource: structuredClone(currentDocument.rows.groups.source),
    rowValueBindings: structuredClone(currentDocument.rows.groups.rowValues),
    population: structuredClone(currentDocument.population),
    columns: structuredClone(currentDocument.columns),
  };

  const columnsMenuOpen = await page.getByLabel('Table columns').count().then(count => count > 0);
  if (!columnsMenuOpen) await clickUI('button', { name: 'Columns' });
  await waitUI(`Boolean(document.querySelector('[aria-label="Table columns"]'))`, 5000);
  const selector = `[aria-label="Table columns"] [data-column-name=${JSON.stringify(column.column)}] select[aria-label^="Values per cohort member for "]`;
  await waitUI(`(()=>{const rows=[...document.querySelectorAll('[aria-label="Table columns"] [role="listitem"]')].filter(row=>row.getAttribute('data-column-name')===${JSON.stringify(column.column)});const select=rows[0]?.querySelector('select[aria-label^="Values per cohort member for "]');return rows.length===1&&select&&!select.disabled&&select.value===${JSON.stringify(expectedPolicy)};})()`, 5000);

  const requestFrom = report.nativeRequests.length;
  let request;
  await selectUI(selector, nextPolicy, {
    ...(expectRejected ? { settledWhen: `document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(expectedPolicy)}` } : {}),
  });
  request = await waitForNativeRequest(requestFrom,
    entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item =>
      item.type === 'UPDATE_COLUMN_ROW_VALUE_POLICY' && item.outputId === outputId &&
      item.column === column.column && item.rowValuePolicy === nextPolicy),
    `Saved ${expectedPolicy} to ${nextPolicy} row-value policy update for ${column.column}`);
  assert.deepEqual(request.body.commands, [{
    type: 'UPDATE_COLUMN_ROW_VALUE_POLICY',
    outputId,
    column: column.column,
    rowValuePolicy: nextPolicy,
  }], 'Editing a saved policy must send exactly one update for the existing physical column');
  assert.equal(request.body.snapshotToken, snapshot.snapshotToken);
  assert.equal(request.body.expectedDraftVersion, snapshot.draftVersion);
  assert.equal(request.body.expectedDraftDigest, snapshot.draftDigest);
  let previewRequest;
  if (!expectRejected) {
    assert.equal(request.status, 200, JSON.stringify(request));
    previewRequest = await waitForNativeRequest(requestFrom,
      entry => entry.path.endsWith('/preview') && entry.startedAt >= request.startedAt && entry.body?.outputId === outputId,
      `Fresh automatic Preview after saved row-value policy change for ${column.column}`);
    assert.equal(previewRequest.status, 200, JSON.stringify(previewRequest));
  } else if (request.status >= 400) {
    request.expectedPolicyValidation = true;
    const captured = report.nativeRequests.find(entry => entry.browserRequestId === request.browserRequestId);
    assert(captured, 'Expected policy validation must correlate to its captured native request');
    captured.expectedPolicyValidation = true;
    cda.expectHttpFailure(captured, 'The server rejects an ambiguous ONE policy for a member field with different per-member values.', {
      control: 'saved member-field policy select', outputId, column: column.column,
      previousPolicy: expectedPolicy, attemptedPolicy: nextPolicy,
      draftDigest: snapshot.draftDigest, stableColumnId: columnId,
    });
    await waitForNativeRequest(requestFrom,
      entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item =>
        item.type === 'UPDATE_COLUMN_ROW_VALUE_POLICY' && item.outputId === outputId && item.column === column.column &&
        item.rowValuePolicy === nextPolicy) && Boolean(entry.response || entry.responseReadError),
      `Readable validation response for ambiguous ONE policy on ${column.column}`);
  }
  const policyMenuStillMounted = await page.locator(selector).count().then(count => count > 0);
  if (!policyMenuStillMounted) await clickUI('button', { name: 'Columns' });
  await waitUI(`Boolean(document.querySelector('[aria-label="Table columns"]'))`, 5000);
  const expectedVisiblePolicy = expectRejected ? expectedPolicy : nextPolicy;
  const previewSettled = expectRejected ? '' :
    `&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(snapshot.previewAriaRowCount)}`;
  await waitUI(`document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(expectedVisiblePolicy)}${previewSettled}`, 5000);
  await clickUI('button', { name: 'Columns' });
  await waitUI(`!document.querySelector('[aria-label="Table columns"]')`, 5000);
  return { request, binding, column, snapshot, previewRequest };
};

const assertSavedPolicyState = (state, edit, expectedPolicy) => {
  const currentDocument = doc(state);
  assert.equal(currentDocument.rows.kind, 'GROUPS');
  assert.deepEqual(currentDocument.rows.groups.source, edit.snapshot.groupSource,
    'Saved policy edit must preserve the exact named-group source revision and policy');
  const expectedBindings = edit.snapshot.rowValueBindings.map(binding =>
    binding.columnId === edit.binding.columnId ? { ...binding, policy: expectedPolicy } : binding,
  );
  assert.deepEqual(currentDocument.rows.groups.rowValues, expectedBindings,
    'Saved policy edit must update the existing stable columnId binding without adding or removing bindings');
  assert.deepEqual(currentDocument.columns, edit.snapshot.columns,
    'Saved policy edit must preserve the exact source fields, physical names, and stable column IDs');
  assert.deepEqual(currentDocument.population, edit.snapshot.population,
    'Saved policy edit must preserve the pinned population and route');
};

const assertSavedPolicyEdit = (state, edit, expectedPolicy) => {
  assertSavedPolicyState(state, edit, expectedPolicy);
  assert.equal(state.draftVersion, edit.snapshot.draftVersion + 1,
    'A successful saved policy edit must persist as one versioned draft change');
};

const readAmbiguousMemberFieldOracle = async (savedBuilder, rootNodeId, pinnedSources) => {
  const query = `FOR s IN Specimen FILTER s.project=="${project}" AND s.dataset_generation=="${generation}" AND s.id IN ${JSON.stringify(pinnedSources.map(source => source.id))} SORT s._key RETURN {id:s.id,payload:s.payload}`;
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(raw.status, 0, raw.stderr);
  const documents = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert.deepEqual(documents.map(document => document.id).sort(), pinnedSources.map(source => source.id).sort(),
    'The ambiguity oracle must read the exact same independently pinned source IDs');
  const configuredPaths = new Set(doc(savedBuilder).columns.flatMap(column =>
    column.source.kind === 'field' && column.source.field?.path ? [column.source.field.path] : [],
  ));
  const paths = [...new Set(savedBuilder.catalog.candidates
    .filter(candidate => candidate.nodeId === rootNodeId && typeof candidate.fieldPath === 'string')
    .map(candidate => candidate.fieldPath.replace(/^root\./, '')))]
    .filter(path => path !== 'id' && path !== 'resourceType' && !configuredPaths.has(path) &&
      path.split('.').every(part => /^[A-Za-z_][A-Za-z0-9_]*$/.test(part)))
    .sort();
  const scalar = value => typeof value === 'string';
  const pathValue = (payload, path) => path.split('.').reduce((value, key) =>
    value && typeof value === 'object' && !Array.isArray(value) ? value[key] : undefined, payload);
  for (const fieldPath of paths) {
    const contributors = documents.map(document => ({
      resourceType,
      resourceId: document.id,
      value: pathValue(document.payload, fieldPath),
    }));
    if (!contributors.every(item => scalar(item.value)) || new Set(contributors.map(item => item.value)).size !== pinnedSources.length) continue;
    const candidate = savedBuilder.catalog.candidates.find(item => item.nodeId === rootNodeId && typeof item.fieldPath === 'string' && item.fieldPath.replace(/^root\./, '') === fieldPath);
    if (!candidate) continue;
    return {
      candidateId: candidate.candidateId,
      fieldPath: candidate.fieldPath,
      contributors: contributors.sort((left, right) => `${left.resourceType}/${left.resourceId}`.localeCompare(`${right.resourceType}/${right.resourceId}`)),
    };
  }
  assert.fail('The pinned fixture must expose an unused root scalar field with distinct values for the expected ONE rejection case');
};

const removeColumnByIdentity = async (columnName) => {
  const currentDocument = doc(builder);
  const rowValueIDs = new Set(currentDocument.rows.groups.rowValues.map(value => value.columnId));
  // PreviewTable's compact Columns list contains explicit-group row-value
  // source columns sorted by authored table order. Its labels are not unique,
  // so map the saved physical name to that exact rendered list position.
  const configuredFields = currentDocument.columns
    .filter(column => rowValueIDs.has(column.columnId))
    .map((column, documentIndex) => ({ column, documentIndex }))
    .sort((left, right) => (left.column.table?.order ?? Number.MAX_SAFE_INTEGER) - (right.column.table?.order ?? Number.MAX_SAFE_INTEGER) || left.documentIndex - right.documentIndex)
    .map(entry => entry.column);
  const targetIndex = configuredFields.findIndex(column => column.column === columnName);
  assert(targetIndex >= 0, `Saved explicit-group row values do not include ${columnName}`);
  assert.equal(configuredFields.filter(column => column.column === columnName).length, 1,
    `Saved explicit-group presentation order does not uniquely identify ${columnName}`);
  const targetColumn = configuredFields[targetIndex];
  const targetBinding = currentDocument.rows.groups.rowValues.find(value => value.columnId === targetColumn.columnId);
  assert.equal(targetBinding?.policy, 'ONE', 'The removal target must be the saved ONE row-value binding');
  const expectedRows = configuredFields.map(column => ({
    label: column.label,
    removeLabel: `Remove ${column.label} column`,
  }));
  const duplicateLabels = expectedRows.filter(row => row.label === targetColumn.label);
  assert.equal(duplicateLabels.length, 2, 'The fixture should expose two same-labeled Resource Type rows for the identity check');

  const panelOpen = await page.getByLabel('Table columns').count().then(count => count > 0);
  if (!panelOpen) await clickUI('button', { name: 'Columns' });
  await waitUI(`Boolean(document.querySelector('[aria-label="Table columns"]'))`, 5000);
  const renderedRows = await page.locator('[aria-label="Table columns"] [role="listitem"]').evaluateAll(rows => rows.map(row=>({label:row.querySelector('input[aria-label^="Column name for "]')?.value,removeLabel:row.querySelector('button[aria-label^="Remove "]')?.getAttribute('aria-label')})));
  assert.deepEqual(renderedRows, expectedRows,
    'Compact Columns rows must match the saved explicit-group fields in PreviewTable presentation order');
  assert.equal(renderedRows.filter(row => row.label === targetColumn.label).length, 2,
    'Both duplicate Resource Type labels must be present before selecting the ONE row by saved order');
  const targetRow = page.locator('[aria-label="Table columns"] [role="listitem"]').nth(targetIndex);
  const targetLabel = await targetRow.locator('input[aria-label^="Column name for "]').inputValue();
  assert.equal(targetLabel, targetColumn.label, `Could not map saved ONE output ${columnName} to its compact Columns row`);
  await performAction(report, `Remove ${columnName}`, targetRow.getByRole('button', { name: `Remove ${targetColumn.label} column`, exact: true }), target => target.click({ timeout: 5000 }));
};

try {
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
  assert.deepEqual([...new Set(sources.map(source => source.fieldValue))], [resourceType],
    'The two distinct selected IDs must share one scalar resourceType value for a valid ONE witness');
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
  const ambiguousFieldOracle = await readAmbiguousMemberFieldOracle(builder, node.nodeId, sources);
  report.oracle.ambiguousMemberField = ambiguousFieldOracle;

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

  await openTable(sources.length + 1, 1);
  const startRows = Date.now();
  await clickUI('[data-testid="construction-rows-settings-trigger"]');
  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  await waitUI(`document.querySelector(${JSON.stringify(rowShapeSelector)})?.disabled===false`);
  const cohortShape = `explicit:${cohort.revisionId}`;
  const options = await page.locator(rowShapeSelector).evaluate(select => [...select.options].map(option=>({value:option.value,disabled:option.disabled,text:option.text})));
  report.rowShapeOptions = options;
  assert(options.some(option => option.value === cohortShape && !option.disabled), 'The saved named cohort must appear as a usable row shape: ' + JSON.stringify(options));
  await selectUI(rowShapeSelector, cohortShape);
  const policySelector = 'select[aria-label="Unmatched record policy"]';
  await waitUI(`document.querySelector(${JSON.stringify(policySelector)})?.disabled===false`);
  await selectUI(policySelector, `explicit:${cohort.revisionId}:ERROR`);
  await waitUI(`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  const comparison = await page.getByLabel('Row definition preview').innerText();
  assert(comparison?.includes('2 rows → 1 rows'), comparison ?? 'The row definition preview must collapse the two pinned records to one cohort row');
  await clickUI('[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitUI(`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:')`);
  record('apply-named-cohort-row-shape', startRows);
  builder = await api(base + '/builder');
  const groupedDocument = doc(builder);
  assert.equal(groupedDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(groupedDocument.rows.groups.source.explicit.unassignedMemberPolicy, 'ERROR');
  assert.equal(groupedDocument.population.selectionRevisionId, selection.id);
  const savedInspection = await inspectCohortRow('inspect-named-cohort-row');

  const beforeField = builder;
  const fieldStart = Date.now();
  await clickUI('[data-testid="construction-action-add-columns"]');
  await clickUI('[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitUI(`document.querySelector('[data-testid="construction-add-columns-source"]')`);
  await clickUI('[data-testid="feature-catalog-raw-fields"] summary');
  await waitUI(`document.querySelector('input[aria-label="Select Specimen.resourceType"]:not(:disabled)')`);
  await clickUI('input[aria-label="Select Specimen.resourceType"]');
  await clickUI('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitUI(`['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
  const fieldProposal = await page.getByTestId('construction-choice-proposal-panel').evaluate(panel => ({status:panel.dataset.proposalStatus,text:panel.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}));
  report.fieldProposal = fieldProposal;
  assert.equal(fieldProposal.status, 'ready', fieldProposal.text);
  assert.equal(fieldProposal.rows.length, 1, 'The named cohort field must preview one group row');
  assert.equal(fieldProposal.rows[0].at(-1), resourceType, 'The retained member field must be computed from the pinned cohort');
  await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  await waitUI(`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='4'`);
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
  const cells = await page.locator('[data-testid="preview-table-scroll"] [role="cell"]').evaluateAll(items => items.map(cell=>cell.innerText.trim()));
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

  const policyEditBaseline = await api(base + '/builder');
  builder = policyEditBaseline;
  const allDocument = doc(policyEditBaseline);
  const allResourceTypeColumn = allDocument.columns.find(column => column.source.kind === 'field' && column.source.field?.path === 'resourceType');
  assert(allResourceTypeColumn?.columnId);
  const allResourceTypeBinding = allDocument.rows.groups.rowValues.find(value => value.columnId === allResourceTypeColumn.columnId);
  assert.equal(allResourceTypeBinding?.policy, 'ALL');

  // This is a saved-binding edit: the same stable columnId and physical source
  // are preserved through both policy changes. The later add-new-ONE path is
  // separately reported as construction-choice coverage.
  const savedAllToOneStarted = Date.now();
  const savedAllToOneEdit = await changeSavedMemberPolicy(allResourceTypeColumn.columnId, 'ALL', 'ONE');
  record('edit-saved-member-policy-all-to-one', savedAllToOneStarted);
  builder = await api(base + '/builder');
  assertSavedPolicyEdit(builder, savedAllToOneEdit, 'ONE');
  const editedOneInspection = await inspectCohortRow('inspect-cohort-row-after-saved-all-to-one-edit');
  assert.equal(editedOneInspection.rowId, reloadedInspection.rowId,
    'Editing the existing member policy must preserve the exact named-group row identity');
  assert.deepEqual(editedOneInspection.sourceContributors, reloadedInspection.sourceContributors,
    'Editing the existing member policy must preserve the exact pinned contributors');
  const editedOneTrace = await traceCohortCell(
    'saved-member-policy-one-cell-trace', editedOneInspection, allResourceTypeColumn.column, resourceType, 'ONE',
  );
  assert.deepEqual(editedOneTrace.contributorTuples, savedCellTrace.contributorTuples,
    'Saved ALL→ONE edit must retain exact raw source ID/value pairs while emitting one scalar');

  await openTable(2, 4);
  const reloadedPolicyOne = await api(base + '/builder');
  assertSavedPolicyEdit(reloadedPolicyOne, savedAllToOneEdit, 'ONE');
  const reloadedEditedOneInspection = await inspectCohortRow('inspect-cohort-row-after-saved-one-reload');
  assert.equal(reloadedEditedOneInspection.rowId, editedOneInspection.rowId);
  assert.deepEqual(reloadedEditedOneInspection.sourceContributors, editedOneInspection.sourceContributors);
  const reloadedEditedOneTrace = await traceCohortCell(
    'reloaded-saved-member-policy-one-cell-trace', reloadedEditedOneInspection,
    allResourceTypeColumn.column, resourceType, 'ONE',
  );
  assert.deepEqual(reloadedEditedOneTrace.contributorTuples, editedOneTrace.contributorTuples);

  const savedOneToAllStarted = Date.now();
  const savedOneToAllEdit = await changeSavedMemberPolicy(allResourceTypeColumn.columnId, 'ONE', 'ALL');
  record('edit-saved-member-policy-one-to-all', savedOneToAllStarted);
  builder = await api(base + '/builder');
  assertSavedPolicyEdit(builder, savedOneToAllEdit, 'ALL');
  const editedAllInspection = await inspectCohortRow('inspect-cohort-row-after-saved-one-to-all-edit');
  assert.equal(editedAllInspection.rowId, editedOneInspection.rowId);
  assert.deepEqual(editedAllInspection.sourceContributors, editedOneInspection.sourceContributors);
  const editedAllTrace = await traceCohortCell(
    'saved-member-policy-all-cell-trace', editedAllInspection, allResourceTypeColumn.column, [resourceType], 'ALL',
  );
  assert.deepEqual(editedAllTrace.contributorTuples, savedCellTrace.contributorTuples,
    'Saved ONE→ALL edit must restore the exact array value and raw source ID/value pairs');

  const undoPolicyEditStarted = Date.now();
  const policyColumnsMenuOpen = await page.getByLabel('Table columns').count().then(count => count > 0);
  if (policyColumnsMenuOpen) await clickUI('button', { name: 'Columns' });
  const undoRequestFrom = report.nativeRequests.length;
  await clickUI('[data-testid="construction-undo"]');
  const undoPolicyRequest = await waitForNativeRequest(undoRequestFrom,
    entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item => item.type === 'RESTORE_DRAFT_REVISION'),
    'Undo of the saved ONE-to-ALL policy edit');
  assert.equal(undoPolicyRequest.status, 200, JSON.stringify(undoPolicyRequest));
  record('undo-saved-member-policy-one-to-all', undoPolicyEditStarted);
  builder = await api(base + '/builder');
  assertSavedPolicyState(builder, savedAllToOneEdit, 'ONE');
  assert.equal(builder.draftVersion, savedOneToAllEdit.snapshot.draftVersion + 2,
    'Undo must persist a new revision restoring the prior saved ONE policy');
  await openTable(2, 4);
  const afterPolicyUndoReload = await api(base + '/builder');
  assertSavedPolicyState(afterPolicyUndoReload, savedAllToOneEdit, 'ONE');
  const afterUndoInspection = await inspectCohortRow('inspect-cohort-row-after-policy-undo-reload');
  assert.equal(afterUndoInspection.rowId, editedOneInspection.rowId);
  assert.deepEqual(afterUndoInspection.sourceContributors, editedOneInspection.sourceContributors);
  const afterUndoTrace = await traceCohortCell(
    'after-policy-undo-reload-one-cell-trace', afterUndoInspection,
    allResourceTypeColumn.column, resourceType, 'ONE',
  );
  assert.deepEqual(afterUndoTrace.contributorTuples, editedOneTrace.contributorTuples);

  const restoreBaselineStarted = Date.now();
  const restoreBaselineEdit = await changeSavedMemberPolicy(allResourceTypeColumn.columnId, 'ONE', 'ALL');
  record('restore-saved-member-policy-all-after-undo', restoreBaselineStarted);
  builder = await api(base + '/builder');
  assertSavedPolicyEdit(builder, restoreBaselineEdit, 'ALL');
  const policyEditCoverage = {
    columnId: allResourceTypeColumn.columnId,
    physicalColumn: allResourceTypeColumn.column,
    source: allResourceTypeColumn.source,
    groupRevisionId: allDocument.rows.groups.source.explicit.revisionId,
    savedTransitions: ['ALL→ONE', 'ONE→ALL'],
    undoneTransition: 'ONE→ALL restored the same columnId to ONE',
    reloads: ['ONE after save', 'ONE after Undo'],
    rawContributorTuples: savedCellTrace.contributorTuples,
    oneValue: editedOneTrace.value,
    allValue: editedAllTrace.value,
    sameSourceBindingAndGroupRevision: true,
  };

  const beforeOne = await api(base + '/builder');
  const beforeOneDocument = doc(beforeOne);
  assert.deepEqual(beforeOneDocument.rows.groups.rowValues, allDocument.rows.groups.rowValues,
    'The saved-edit lifecycle must restore the baseline ALL binding before the separate add-new-ONE scenario');
  assert.deepEqual(beforeOneDocument.columns, allDocument.columns,
    'The saved-edit lifecycle must restore the same source columns before the add-new-ONE scenario');
  const beforeOneInspection = reloadedInspection;
  const onePreviewStarted = Date.now();
  const oneProposalFrom = report.nativeRequests.length;
  const onePreview = await chooseRootFieldWithPolicy('ONE');
  assert.equal(onePreview.status, 'ready', onePreview.text);
  assert.equal(onePreview.rows.length, 1, 'The ONE member-field proposal must preview the same single named cohort row');
  assert.equal(onePreview.rows[0].at(-1), resourceType, 'ONE must accept the shared resourceType value from both selected members');
  const onePreviewRequest = await waitForNativeRequest(oneProposalFrom,
    entry => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 1,
    'Native ONE member-field preview');
  assert.equal(onePreviewRequest.status, 200);
  assert.equal(onePreviewRequest.body.constructionChoices[0].rowValuePolicy, 'ONE');
  report.oneProposal = { status: onePreviewRequest.status, policy: onePreviewRequest.body.constructionChoices[0].rowValuePolicy, previewRows: onePreview.rows };
  record('preview-cohort-member-field-one', onePreviewStarted);

  const cancelOneStarted = Date.now();
  await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
  await waitUI(`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`, 5000);
  const afterOneCancel = await api(base + '/builder');
  assert.deepEqual(afterOneCancel.workspace, beforeOne.workspace, 'Canceling ONE must preserve the exact saved ALL workspace and two-member cohort');
  record('cancel-cohort-member-field-one', cancelOneStarted);

  const oneApplyStarted = Date.now();
  const oneApplyFrom = report.nativeRequests.length;
  const oneApplyPreview = await chooseRootFieldWithPolicy('ONE');
  assert.equal(oneApplyPreview.status, 'ready', oneApplyPreview.text);
  assert.equal(oneApplyPreview.rows[0].at(-1), resourceType);
  await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  const oneApplyRequest = await waitForNativeRequest(oneApplyFrom,
    entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item => item.type === 'APPLY_CONSTRUCTION_CHOICE'),
    'Native ONE member-field Apply');
  assert.equal(oneApplyRequest.status, 200);
  const oneApplyCommand = oneApplyRequest.body.commands.find(item => item.type === 'APPLY_CONSTRUCTION_CHOICE');
  assert.equal(oneApplyCommand.constructionChoice.rowValuePolicy, 'ONE');
  await waitUI(`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='5'`, 5000);
  builder = await api(base + '/builder');
  const bothPolicyDocument = doc(builder);
  assert.equal(bothPolicyDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(bothPolicyDocument.population.selectionRevisionId, selection.id);
  assert.deepEqual(bothPolicyDocument.rows.groups.rowValues.map(value => value.policy).sort(), ['ALL', 'ONE']);
  const oneBinding = bothPolicyDocument.rows.groups.rowValues.find(value => value.policy === 'ONE');
  const oneColumn = bothPolicyDocument.columns.find(column => column.columnId === oneBinding?.columnId);
  assert(oneColumn?.columnId && oneColumn.source.kind === 'field' && oneColumn.source.field?.path === 'resourceType',
    'The ONE policy must bind the second native resourceType source column by stable ID');
  assert.notEqual(oneColumn.columnId, allResourceTypeColumn.columnId, 'The added ONE field must have its own stable column binding');
  const oneApplyDoc = bothPolicyDocument;
  record('apply-cohort-member-field-one', oneApplyStarted);

  const oneAppliedInspection = await inspectCohortRow('inspect-cohort-row-after-one-apply');
  assert.equal(oneAppliedInspection.rowId, beforeOneInspection.rowId, 'Adding ONE must preserve the named cohort row identity');
  assert.deepEqual(oneAppliedInspection.sourceContributors, beforeOneInspection.sourceContributors,
    'ONE must retain both exact independently pinned member IDs');
  const oneCellTrace = await traceCohortCell('cohort-one-cell-trace', oneAppliedInspection, oneColumn.column, resourceType, 'ONE');
  assert.deepEqual(oneCellTrace.contributorTuples, savedCellTrace.contributorTuples,
    'ONE must preserve both source ID/value contributors even though it emits one scalar');

  await openTable(2, 5);
  const afterOneReload = await api(base + '/builder');
  const reloadedOneDocument = doc(afterOneReload);
  assert.deepEqual(reloadedOneDocument.rows.groups.rowValues.map(value => value.policy).sort(), ['ALL', 'ONE'],
    'Reload must retain the separately bound ALL and ONE policies');
  assert.equal(reloadedOneDocument.columns.find(column => column.columnId === oneColumn.columnId)?.column, oneColumn.column,
    'Reload must retain the exact ONE output column identity');
  const reloadedOneInspection = await inspectCohortRow('inspect-cohort-row-after-one-reload');
  assert.equal(reloadedOneInspection.rowId, oneAppliedInspection.rowId);
  assert.deepEqual(reloadedOneInspection.sourceContributors, oneAppliedInspection.sourceContributors);
  const reloadedOneTrace = await traceCohortCell('reloaded-cohort-one-cell-trace', reloadedOneInspection, oneColumn.column, resourceType, 'ONE');
  assert.deepEqual(reloadedOneTrace.contributorTuples, oneCellTrace.contributorTuples);
  assert.equal(reloadedOneTrace.value, resourceType);

  const removeOneStarted = Date.now();
  const removeOneFrom = report.nativeRequests.length;
  await removeColumnByIdentity(oneColumn.column);
  await waitUI(`!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')==='4'`, 5000);
  const removeOneRequest = await waitForNativeRequest(removeOneFrom,
    entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item => item.type === 'REMOVE_COLUMN' && item.column === oneColumn.column),
    'Native removal of the ONE member field');
  assert.equal(removeOneRequest.status, 200);
  builder = await api(base + '/builder');
  const afterOneRemoval = doc(builder);
  assert.deepEqual(afterOneRemoval.rows.groups.rowValues, allDocument.rows.groups.rowValues,
    'Removing ONE must preserve the previously saved ALL binding');
  assert(afterOneRemoval.columns.some(column => column.columnId === allResourceTypeColumn.columnId));
  assert.equal(afterOneRemoval.population.selectionRevisionId, selection.id);
  record('remove-cohort-member-field-one', removeOneStarted);

  await openTable(2, 4);
  const afterOneRemovalReload = await api(base + '/builder');
  assert.deepEqual(doc(afterOneRemovalReload).rows.groups.rowValues, allDocument.rows.groups.rowValues);
  assert.deepEqual(doc(afterOneRemovalReload).population, allDocument.population);
  const finalInspection = await inspectCohortRow('inspect-cohort-row-after-one-removal-reload');
  assert.deepEqual(finalInspection.sourceContributors, beforeOneInspection.sourceContributors,
    'Removing the ONE output must not alter exact selected cohort membership');
  assert.equal(finalInspection.rowId, beforeOneInspection.rowId);

  // Add an ALL member field with independently observed distinct source values
  // only to construct the negative case. The rejection below edits this saved
  // binding; it is not counted as an add-new-ONE success.
  const ambiguousSetupStarted = Date.now();
  const currentRenderedColumnCount = Number(await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-colcount'));
  assert(Number.isInteger(currentRenderedColumnCount) && currentRenderedColumnCount > 0);
  const ambiguousRenderedColumnCount = currentRenderedColumnCount + 1;
  const ambiguousProposalFrom = report.nativeRequests.length;
  const ambiguousProposal = await chooseRootFieldWithPolicy('ALL', ambiguousFieldOracle.fieldPath);
  assert.equal(ambiguousProposal.status, 'ready', ambiguousProposal.text);
  assert.equal(ambiguousProposal.rows.length, 1, 'The ambiguous-field setup must retain the single pinned named group');
  await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  const ambiguousApplyRequest = await waitForNativeRequest(ambiguousProposalFrom,
    entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item => item.type === 'APPLY_CONSTRUCTION_CHOICE'),
    'Native add-time ALL setup for the distinct-value member field');
  assert.equal(ambiguousApplyRequest.status, 200, JSON.stringify(ambiguousApplyRequest));
  const ambiguousApplyCommand = ambiguousApplyRequest.body.commands.find(item => item.type === 'APPLY_CONSTRUCTION_CHOICE');
  assert.equal(ambiguousApplyCommand.constructionChoice.rowValuePolicy, 'ALL',
    'The distinct-value setup must begin with the saved ALL policy');
  await waitUI(`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(ambiguousProposal.rows.length + 1))}&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(ambiguousRenderedColumnCount))}`, 5000);
  record('add-distinct-value-member-field-with-all-for-rejection-case', ambiguousSetupStarted);
  builder = await api(base + '/builder');
  const ambiguousSetupDocument = doc(builder);
  let ambiguousSetupDraftVersion = builder.draftVersion;
  let ambiguousSetupDraftDigest = builder.draftDigest;
  assert.equal(ambiguousSetupDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(ambiguousSetupDocument.population.selectionRevisionId, selection.id);
  const ambiguousFieldPath = ambiguousFieldOracle.fieldPath.replace(/^root\./, '');
  const ambiguousColumn = ambiguousSetupDocument.columns.find(column =>
    column.source.kind === 'field' && column.source.field?.path.replace(/^root\./, '') === ambiguousFieldPath &&
    column.columnId !== allResourceTypeColumn.columnId,
  );
  assert(ambiguousColumn?.columnId && ambiguousColumn.source.kind === 'field');
  const ambiguousBinding = ambiguousSetupDocument.rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId);
  assert.equal(ambiguousBinding?.policy, 'ALL');
  const ambiguousExpectedValues = [...new Set(ambiguousFieldOracle.contributors.map(item => item.value))].sort();
  assert.equal(ambiguousExpectedValues.length, sources.length,
    'The independent raw-field oracle must show more than one distinct value in the named cohort');
  const ambiguousSetupInspection = await inspectCohortRow('inspect-cohort-row-with-distinct-member-field');
  assert.equal(ambiguousSetupInspection.rowIdObject.group_revision_id, cohort.revisionId);
  assert.deepEqual(ambiguousSetupInspection.sourceContributors, finalInspection.sourceContributors);
  const ambiguousAllTrace = await traceCohortCell(
    'distinct-member-field-saved-all-cell-trace', ambiguousSetupInspection, ambiguousColumn.column,
    ambiguousExpectedValues, 'ALL', ambiguousColumn.source.field.path, ambiguousFieldOracle.contributors,
  );
  assert.deepEqual(ambiguousAllTrace.contributorTuples, ambiguousFieldOracle.contributors,
    'Saved ALL values must preserve each pinned source ID beside its exact independently queried distinct value');

  if (cohortRowValueCase === 'transformed-category') {
    assert.equal(ambiguousColumn.logicalType?.toLowerCase(), 'string',
      'The transformed case requires a scalar string source column');
    const recodedCategory = 'Shared cohort category';
    const recodingTransformation = {
      kind: 'EXACT_CATEGORY_RECODE',
      exactCategoryRecode: {
        mappings: ambiguousExpectedValues.map(from => ({ from, to: recodedCategory })),
        unknownPolicy: 'KEEP_ORIGINAL',
      },
    };
    assert.equal(recodingTransformation.exactCategoryRecode.mappings.length, sources.length,
      'The raw oracle must show a separate exact input for each selected cohort member');
    const ensurePreviewColumnsRecoder = async () => {
      const operationEditor = await page.getByTestId('construction-operation-editor').count().then(count => count > 0);
      if (operationEditor) {
        const backToTable = 'button[data-testid="construction-close-operation-editor"]';
        const backToTableState = await page.locator(backToTable).evaluate(button => ({found:true,label:button.getAttribute('aria-label'),text:button.innerText.trim()})).catch(() => ({found:false}));
        assert(backToTableState.found && backToTableState.label === 'Close operation editor' && backToTableState.text === 'Back to table',
          `The verifier-opened operation editor must expose its native Back to table action: ${JSON.stringify(backToTableState)}`);
        await clickUI(backToTable, { name: 'Close operation editor' });
        await waitUI(`!document.querySelector('[data-testid="construction-operation-editor"]')&&Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 5000);
      }
      const columnsOpen = await page.getByLabel('Table columns').count().then(count => count > 0);
      if (!columnsOpen) await clickUI('button', { name: 'Columns' });
      await waitUI(`Boolean(document.querySelector('[aria-label="Table columns"]'))`, 5000);
      const sourceSetup = await page.getByTestId('construction-source-setup').evaluate(section => ({found:true,open:section.open})).catch(() => ({found:false,open:false}));
      assert(sourceSetup.found && !sourceSetup.open,
        `Ordinary PreviewTable recoding must work while Advanced source setup remains closed: ${JSON.stringify(sourceSetup)}`);
      const selector = `[aria-label="Table columns"] [role="listitem"][data-column-name=${JSON.stringify(ambiguousColumn.column)}]`;
      const row = await page.locator(selector).evaluateAll(rows => ({count:rows.length,names:rows.map(item=>item.getAttribute('data-column-name')),text:rows[0]?.innerText.trim()}));
      assert(row.count === 1 && row.names[0] === ambiguousColumn.column,
        `The ordinary Columns menu must contain exactly one stable physical source row ${ambiguousColumn.column}: ${JSON.stringify(row)}`);
      report.memberFieldRecodeAccess ??= {
        path: 'Preview and configure → Columns → grouped member field → Recode exact category values',
        sourceColumn: ambiguousColumn.column,
        advancedSourceSetupOpened: false,
        advancedSourceSetupRemainedClosed: true,
        operationEditorClosedByVerifier: false,
        ordinaryMenuRowCount: row.count,
      };
      report.memberFieldRecodeAccess.operationEditorClosedByVerifier ||= operationEditor;
      return selector;
    };
    const configuredFieldRowSelector = ensurePreviewColumnsRecoder;
    const configuredFieldControl = async suffix => `${await configuredFieldRowSelector()} ${suffix}`;
    const clickConfiguredFieldControl = async (suffix, name) => clickUI(await configuredFieldControl(suffix), { name });
    const openConfiguredFieldEditor = async summaryText => {
      const selector = await configuredFieldControl('summary');
      const state = await page.locator(selector).evaluate(summary => ({found:true,text:summary.innerText.trim(),open:Boolean(summary.closest('details')?.open)})).catch(() => ({found:false,open:false}));
      assert(state.found && state.text === summaryText, `Native FeaturePolicyEditor did not expose ${summaryText} for ${ambiguousColumn.column}: ${JSON.stringify(state)}`);
      if (!state.open) await clickUI(selector, { name: summaryText });
    };
    const setConfiguredFieldInput = async (ariaLabel, value) => {
      const selector = await configuredFieldControl(`input[aria-label=${JSON.stringify(ariaLabel)}]`);
      await clickUI(selector);
      await performAction(report, `Edit ${ariaLabel}`, page.locator(selector), (target, { timeout }) => target.fill(value, { timeout }), { editable: true });
      assert.equal(await page.locator(selector).inputValue(), value, `The native configured editor must retain ${ariaLabel}`);
    };
    await openConfiguredFieldEditor('Recode exact category values');
    for (let index = 0; index < recodingTransformation.exactCategoryRecode.mappings.length; index += 1) {
      const mapping = recodingTransformation.exactCategoryRecode.mappings[index];
      await clickConfiguredFieldControl('button', 'Add mapping');
      await waitUI(`Boolean(document.querySelector(${JSON.stringify(await configuredFieldControl(`input[aria-label=${JSON.stringify(`Recorded category ${index + 1} for ${ambiguousColumn.label}`)}]`))}))`, 5000);
      await setConfiguredFieldInput(`Recorded category ${index + 1} for ${ambiguousColumn.label}`, mapping.from);
      await setConfiguredFieldInput(`Replacement value ${index + 1} for ${ambiguousColumn.label}`, mapping.to);
    }
    await selectUI(await configuredFieldControl(`select[aria-label=${JSON.stringify(`Unmapped value policy for ${ambiguousColumn.label}`)}]`),
      'KEEP_ORIGINAL', {
        dismissSelector: 'div:has(> [aria-label="Table columns"]) > p',
      });
    const recodingRequestFrom = report.nativeRequests.length;
    const recodingStarted = Date.now();
    await clickConfiguredFieldControl('button', 'Save recoding');
    const recodingRequest = await waitForNativeRequest(recodingRequestFrom,
      entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item =>
        item.type === 'UPDATE_COLUMN_TRANSFORMATION' && item.outputId === outputId && item.column === ambiguousColumn.column),
      `Native exact recoding for grouped member field ${ambiguousFieldPath}`);
    assert.equal(recodingRequest.status, 200, JSON.stringify(recodingRequest));
    assert.deepEqual(recodingRequest.body.commands, [{
      type: 'UPDATE_COLUMN_TRANSFORMATION', outputId, column: ambiguousColumn.column,
      transformationChange: { kind: 'SET', transformation: recodingTransformation },
    }]);
    const recodedAllPreview = await waitForNativePreview(recodingRequestFrom,
      'Automatic preview after recoding explicit-group members');
    assert.deepEqual(recodedAllPreview.response.rows[0]?.[ambiguousColumn.column], [recodedCategory],
      'ALL must recode each member before returning the sorted unique categories');
    builder = await api(base + '/builder');
    assert.deepEqual(doc(builder).columns.find(value => value.columnId === ambiguousColumn.columnId)?.valueTransformation, recodingTransformation);
    assert.equal(doc(builder).rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId)?.policy, 'ALL');
    record('recode-distinct-cohort-member-values-before-all', recodingStarted);

    const recodedOneStarted = Date.now();
    const recodedOneEdit = await changeSavedMemberPolicy(
      ambiguousColumn.columnId, 'ALL', 'ONE', { allowTransformed: true },
    );
    assert.equal(recodedOneEdit.previewRequest.response.rows[0]?.[ambiguousColumn.column], recodedCategory,
      'ONE must compare recoded unique values and accept different raw values mapped to the same category');
    record('saved-one-accepts-member-values-recoded-to-the-same-category', recodedOneStarted);
    await openTable(ambiguousProposal.rows.length + 1, ambiguousRenderedColumnCount);
    builder = await api(base + '/builder');
    assert.deepEqual(doc(builder).columns.find(value => value.columnId === ambiguousColumn.columnId)?.valueTransformation, recodingTransformation);
    assert.equal(doc(builder).rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId)?.policy, 'ONE');

    const recodedAllAgain = await changeSavedMemberPolicy(
      ambiguousColumn.columnId, 'ONE', 'ALL', { allowTransformed: true },
    );
    assert.deepEqual(recodedAllAgain.previewRequest.response.rows[0]?.[ambiguousColumn.column], [recodedCategory]);
    await openConfiguredFieldEditor('Edit exact category recoding');
    const removeRecodingFrom = report.nativeRequests.length;
    const removeRecodingStarted = Date.now();
    await clickConfiguredFieldControl('button', 'Remove recoding');
    const removeRecodingRequest = await waitForNativeRequest(removeRecodingFrom,
      entry => entry.path.endsWith('/commands') && entry.body?.commands?.some(item =>
        item.type === 'UPDATE_COLUMN_TRANSFORMATION' && item.column === ambiguousColumn.column && item.transformationChange?.kind === 'REMOVE'),
      'Native removal of the temporary explicit-group member recoding');
    assert.equal(removeRecodingRequest.status, 200, JSON.stringify(removeRecodingRequest));
    const rawAllPreview = await waitForNativePreview(removeRecodingFrom,
      'Automatic preview after removing explicit-group member recoding');
    assert.deepEqual(rawAllPreview.response.rows[0]?.[ambiguousColumn.column], ambiguousExpectedValues,
      'Removing recoding must restore each independently observed raw member value under ALL');
    builder = await api(base + '/builder');
    assert.deepEqual(doc(builder).columns, ambiguousSetupDocument.columns,
      'The temporary recode cycle must preserve source columns and stable IDs');
    assert.deepEqual(doc(builder).rows.groups.rowValues, ambiguousSetupDocument.rows.groups.rowValues,
      'The temporary recode cycle must restore ALL on the same stable binding');
    ambiguousSetupDraftVersion = builder.draftVersion;
    ambiguousSetupDraftDigest = builder.draftDigest;
    const menuOpen = await page.getByLabel('Table columns').count().then(count => count > 0);
    if (menuOpen) await clickUI('button', { name: 'Columns' });
    record('restore-untransformed-all-after-category-row-policy-cycle', removeRecodingStarted);
  } else {
    assert.equal(cohortRowValueCase, 'default',
      `Unsupported LOOM_COHORT_ROW_VALUE_CASE=${cohortRowValueCase}`);
  }

  const ambiguousEditStarted = Date.now();
  const ambiguousAllToOneAttempt = await changeSavedMemberPolicy(
    ambiguousColumn.columnId, 'ALL', 'ONE', { expectRejected: true },
  );
  builder = await api(base + '/builder');
  const afterAmbiguousAttempt = doc(builder);
  const afterAmbiguousBinding = afterAmbiguousAttempt.rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId);
  assert.equal(afterAmbiguousBinding?.policy, 'ALL',
    'An ambiguous ONE edit must leave the exact existing stable columnId binding persisted as ALL');
  assert.deepEqual(afterAmbiguousAttempt.rows.groups.source, ambiguousSetupDocument.rows.groups.source,
    'Ambiguous ONE rejection must preserve the exact named-group revision');
  assert.deepEqual(afterAmbiguousAttempt.rows.groups.rowValues, ambiguousSetupDocument.rows.groups.rowValues,
    'Ambiguous ONE rejection must preserve every saved row-value binding and policy');
  assert.deepEqual(afterAmbiguousAttempt.columns, ambiguousSetupDocument.columns,
    'Ambiguous ONE rejection must preserve all source columns and stable IDs');
  assert.deepEqual(afterAmbiguousAttempt.population, ambiguousSetupDocument.population,
    'Ambiguous ONE rejection must preserve the pinned population and route');
  assert.equal(builder.draftVersion, ambiguousSetupDraftVersion,
    'Rejected ambiguous ONE edit must not advance the saved draft version');
  assert.equal(builder.draftDigest, ambiguousSetupDraftDigest,
    'Rejected ambiguous ONE edit must not change the saved draft digest');
  assert(ambiguousAllToOneAttempt.request.status >= 400 && ambiguousAllToOneAttempt.request.status < 500,
    `The ambiguous saved ONE edit must be rejected by the server: ${JSON.stringify(ambiguousAllToOneAttempt.request)}`);
  assert.equal(ambiguousAllToOneAttempt.request.expectedPolicyValidation, true,
    'Only the explicitly expected 4xx validation response may be excluded from incidental HTTP failures');
  const rejectionDetails = JSON.stringify(ambiguousAllToOneAttempt.request.response ?? ambiguousAllToOneAttempt.request.responseReadError ?? '');
  assert.match(rejectionDetails, /one|unique|distinct|multiple|ambiguous|row.value/i,
    `The ambiguous ONE rejection must explain its row-value validation: ${rejectionDetails}`);
  await waitUI(`document.body.innerText.includes('This cohort has multiple distinct values for this field. Keep All unique values.')`, 5000);
  record('reject-saved-ambiguous-member-policy-one-and-retain-all', ambiguousEditStarted);

  await openTable(ambiguousProposal.rows.length + 1, ambiguousRenderedColumnCount);
  builder = await api(base + '/builder');
  const afterAmbiguousReload = doc(builder);
  assert.equal(afterAmbiguousReload.rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId)?.policy, 'ALL',
    'Reload must retain ALL after the rejected ambiguous saved edit');
  assert.deepEqual(afterAmbiguousReload.rows.groups.source, ambiguousSetupDocument.rows.groups.source);
  assert.deepEqual(afterAmbiguousReload.columns, ambiguousSetupDocument.columns);
  assert.deepEqual(afterAmbiguousReload.population, ambiguousSetupDocument.population);
  const afterAmbiguousReloadInspection = await inspectCohortRow('inspect-cohort-row-after-ambiguous-one-rejection-reload');
  assert.equal(afterAmbiguousReloadInspection.rowId, ambiguousSetupInspection.rowId);
  assert.deepEqual(afterAmbiguousReloadInspection.sourceContributors, ambiguousSetupInspection.sourceContributors);
  const afterAmbiguousReloadTrace = await traceCohortCell(
    'distinct-member-field-all-cell-trace-after-rejected-edit-reload', afterAmbiguousReloadInspection,
    ambiguousColumn.column, ambiguousExpectedValues, 'ALL', ambiguousColumn.source.field.path, ambiguousFieldOracle.contributors,
  );
  assert.deepEqual(afterAmbiguousReloadTrace.contributorTuples, ambiguousAllTrace.contributorTuples);

  report.memberFieldPolicyCoverage = {
    mode: 'Native saved ALL↔ONE edits on the same stable binding with exact CellTrace, Undo/reload, plus a separate add-new-ONE lifecycle and strict ambiguous-ONE rejection',
    selectedResourceIDs: report.oracle.sources.map(source => source.id).sort(),
    selectedResourceTypeValues: report.oracle.cellTraceContributors.map(item => item.value),
    allColumnId: allResourceTypeColumn.columnId,
    oneColumnId: oneColumn.columnId,
    allTrace: { value: savedCellTrace.value, contributors: savedCellTrace.contributorTuples },
    oneTrace: { value: oneCellTrace.value, contributors: oneCellTrace.contributorTuples },
    reloadedOneTrace: { value: reloadedOneTrace.value, contributors: reloadedOneTrace.contributorTuples },
    exactSameContributorSet: JSON.stringify(oneCellTrace.contributorTuples) === JSON.stringify(savedCellTrace.contributorTuples),
    savedPolicyEditSupported: true,
    savedPolicyEdit: policyEditCoverage,
    newOneAdditionSeparateFromSavedEdits: true,
    ambiguousPolicyEdit: {
      fieldPath: ambiguousFieldPath,
      columnId: ambiguousColumn.columnId,
      physicalColumn: ambiguousColumn.column,
      rawContributorTuples: ambiguousFieldOracle.contributors,
      allValue: ambiguousAllTrace.value,
      attemptedPolicy: 'ONE',
      rejectionStatus: ambiguousAllToOneAttempt.request.status,
      rejection: ambiguousAllToOneAttempt.request.response,
      persistedPolicyAfterRejection: afterAmbiguousBinding.policy,
      persistedPolicyAfterReload: afterAmbiguousReload.rows.groups.rowValues.find(value => value.columnId === ambiguousColumn.columnId)?.policy,
      allTraceAfterReload: afterAmbiguousReloadTrace.value,
    },
  };
  assert.equal(report.memberFieldPolicyCoverage.exactSameContributorSet, true);
  assert.equal(oneApplyDoc.rows.groups.rowValues.find(value => value.columnId === oneColumn.columnId)?.policy, 'ONE');
  assert.equal(report.memberFieldPolicyCoverage.savedPolicyEditSupported, true);
  await requestCapture.flush();
  assert.deepEqual(report.errors.filter(error => !(error.expected || error.expectedHttpFailure || error.expectedCancellation || error.expectedInjectedFault)), []);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  throw error;
} finally {
  if (report.activeAction) {
    report.failedAction = { label: report.activeAction.label, locator: report.activeAction.locator,
      elapsedMs: Date.now() - report.activeAction.startedAt };
    delete report.activeAction;
  }
  await requestCapture.flush();
  await cda.attachReport(`cohort-row-sources-${cohortRowValueCase}-evidence.json`, report);
}
}
