import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { requireUnique } from '../helpers/playwright-actions.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';
import { strictSubsetFilterOracle } from '../helpers/cda-authored-expand-filter-oracle.mjs';
import { selectCanceledSavedPreviewRequest } from '../helpers/saved-preview-binding.mjs';

export const assertExpandFilterRemovalProposal = (request, response, {
  outputId, snapshotToken, draftVersion, draftDigest, expandStepId, filterStepId, sourceRowCount,
}) => {
  const body = request?.body;
  assert(body?.removeStepIds?.includes(expandStepId), 'Removal request must identify the selected EXPAND step');
  assert.equal(body.outputId, outputId, 'Removal request must stay bound to the saved output');
  assert.equal(body.snapshotToken, snapshotToken, 'Removal request must stay bound to the saved snapshot');
  assert.equal(body.expectedDraftVersion, draftVersion, 'Removal request must stay bound to the saved draft version');
  assert.equal(body.expectedDraftDigest, draftDigest, 'Removal request must stay bound to the saved draft digest');

  assert.equal(response.outputId, outputId, 'Removal proposal response must preserve the saved output');
  assert.equal(response.snapshotToken, snapshotToken, 'Removal proposal response must preserve the saved snapshot');
  assert.equal(response.draftVersion, draftVersion, 'Removal proposal response must preserve the saved draft version');
  assert.equal(response.draftDigest, draftDigest, 'Removal proposal response must preserve the saved draft digest');
  const removedStepIds = response.dependencyImpact?.removedStepIds ?? [];
  assert(removedStepIds.includes(expandStepId), 'Removal proposal impact must include the selected EXPAND step');
  assert(removedStepIds.includes(filterStepId), 'Removal proposal impact must include the dependent Filter step');
  assert.deepEqual(response.candidateConstruction?.steps, [], 'Removal proposal response must remove both dependent operations');
  assert.equal(response.preview?.outputId, outputId, 'Removal preview must stay bound to the saved output');
  assert.equal(response.preview?.rowCount, sourceRowCount, 'Removal preview must restore the source RECORDS row count');
};

export async function waitForSourceCapabilities(requestMonitor, { fromIndex, deadlineAt, path, expected }) {
  const capabilities = await requestMonitor.waitFor(entry => {
    const body = requestMonitor.rawRequestBody(entry) ?? entry.body;
    const response = requestMonitor.rawResponseBody(entry) ?? entry.response;
    return entry.method === 'POST' && entry.path === path &&
      body?.snapshotToken === expected.snapshotToken &&
      body?.expectedDraftVersion === expected.draftVersion &&
      body?.expectedDraftDigest === expected.draftDigest &&
      body?.outputId === expected.outputId && body?.stageId === expected.stageId &&
      entry.status === 200 && entry.failure === undefined && entry.responseReadError === undefined &&
      response?.snapshotToken === expected.snapshotToken &&
      response?.draftVersion === expected.draftVersion &&
      response?.draftDigest === expected.draftDigest &&
      response?.outputId === expected.outputId && response?.stageId === expected.stageId &&
      response?.selectedStage?.id === expected.stageId;
  }, { fromIndex, timeoutMs: Math.max(1, deadlineAt - Date.now()) });
  assert(Number.isFinite(capabilities.completedAt), 'Source-projection capability response must reach a terminal state');
  assert(capabilities.completedAt <= deadlineAt, 'Source-projection capability response must complete within the shared action deadline');
  return capabilities;
}

export async function waitForAppliedSourceCapabilities(requestMonitor, {
  applyRequest, fromIndex, deadlineAt, path, outputId, stageId = 'source_projection',
}) {
  const completedApply = await requestMonitor.waitFor(entry => entry === applyRequest, {
    fromIndex, timeoutMs: Math.max(1, deadlineAt - Date.now()),
  });
  assert.equal(completedApply.status, 200, 'Cascade Apply command must complete successfully');
  assert.equal(completedApply.failure, undefined, 'Cascade Apply command must not fail at the network layer');
  assert.equal(completedApply.responseReadError, undefined, 'Cascade Apply command response body must be captured');

  const applyBody = requestMonitor.rawRequestBody(completedApply) ?? completedApply.body;
  const applyResponse = requestMonitor.rawResponseBody(completedApply) ?? completedApply.response;
  assert(applyBody?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' &&
    command.outputId === outputId), 'Terminal capability wait must follow the selected output Apply command');
  assert.equal(typeof applyBody.snapshotToken, 'string', 'Apply command must carry the pinned catalog snapshot');
  assert(Number.isSafeInteger(applyResponse?.draftVersion), 'Apply response must provide the successor draft version');
  assert.equal(typeof applyResponse.draftDigest, 'string', 'Apply response must provide the successor draft digest');

  const expected = {
    snapshotToken: applyBody.snapshotToken,
    draftVersion: applyResponse.draftVersion,
    draftDigest: applyResponse.draftDigest,
    outputId,
    stageId,
  };
  const capabilities = await waitForSourceCapabilities(requestMonitor, { fromIndex, deadlineAt, path, expected });
  return { applyRequest: completedApply, capabilities, expected };
}

export async function authoredExpandWorkflow({ page, cda, expect, mode = 'expand' }) {
const composedFilter = mode === 'expand-filter';
assert(composedFilter || mode === 'expand', `Unsupported authored EXPAND workflow mode: ${mode}`);
const values = {
  project: cda.project,
  generation: cda.generation ?? 'cda-fhir-v1',
  evidence: cda.evidence,
  'api-origin': cda.apiOrigin,
  'ui-origin': cda.uiOrigin,
  'api-container': cda.target.apiContainer,
  'arango-container': cda.target.arangoContainer,
  'compose-project': cda.target.composeProject,
};
const report = cda.report;
assert.equal(typeof expect, 'function', 'The native CDA workflow requires Playwright expect assertions');
Object.assign(report, {
  started: new Date().toISOString(), assertions: [], gaps: [], failures: [], requests: [], workflowTimings: [],
  cancelPreviewRestorations: [],
  browserErrors: { protocol: [], exceptions: [], console: [], modules: [], http: [], incidental: [] },
  evidencePaths: [],
});
report.browserRequests = report.nativeRequests;
assert.equal(values.generation, 'cda-fhir-v1', 'CDA authored EXPAND requires the cda-fhir-v1 fixture generation.');
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `cda-authored-expand-${Date.now()}`;
assert.notEqual(explorer, protectedExplorer, 'The run must own a fresh Explorer');
report.target = cda.target;
report.explorer = explorer;
report.target.protectedExplorer = protectedExplorer;
const projectRoot = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const root = `${projectRoot}/${encodeURIComponent(explorer)}`;
const base = `${root}/authoring/v2`;
let builder;
let outputId;
const browserPending = new Set();
const requestMonitor = cda.captureRequests(`/api/v1/projects/${encodeURIComponent(values.project)}/explorers/${encodeURIComponent(explorer)}`);

const inspectPage = (_page, body) => page.evaluate(`(()=>{${body}})()`);
const waitForBrowser = (page, condition, timeout = 5000) => waitForCondition(page, condition, Math.min(timeout, 5000));
const resolveActionLocator = async (page, selector, identity = {}, timeout = 5000) => {
  const candidates = page.locator(selector);
  const { name, includes } = identity;
  if (name === undefined && includes === undefined) return requireUnique(candidates, selector, { timeout });
  const matches = await candidates.evaluateAll((nodes, wanted) => nodes.flatMap((node, index) => {
    const label = String(node.getAttribute('aria-label') || node.innerText || node.textContent || '')
      .replace(/\\s+/g, ' ').trim();
    const matched = wanted.name !== undefined ? label === wanted.name
      : label.toLocaleLowerCase().includes(wanted.includes.toLocaleLowerCase());
    return matched ? [index] : [];
  }), { name, includes });
  assert.equal(matches.length, 1, `${selector}: expected one matching control, found ${matches.length}`);
  return requireUnique(candidates.nth(matches[0]), `${selector} ${name ?? includes}`, { timeout });
};
const click = async (page, selector, identity = {}, timeout = 5000) => {
  const label = `Click ${selector} ${identity.name ?? identity.includes ?? ''}`.trim();
  const locator = await resolveActionLocator(page, selector, identity, timeout);
  const elapsedMs = await cda.action(label, locator, target => target.click({ timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const fill = async (page, selector, value, timeout = 5000) => {
  const label = `Fill ${selector}`;
  const locator = await resolveActionLocator(page, selector);
  const elapsedMs = await cda.action(label, locator, target => target.fill(value, { timeout }), { timeout, editable: true });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const selectOption = async (page, selector, value, timeout = 5000) => {
  const label = `Select ${value} in ${selector}`;
  const locator = await resolveActionLocator(page, selector);
  const elapsedMs = await cda.action(label, locator, target => target.selectOption(value, { timeout }), { timeout });
  report.lastAction = { label, locator: locator.toString(), elapsedMs, startedAt: Date.now() - elapsedMs };
  return elapsedMs;
};
const navigate = (page, url) => page.goto(url, { waitUntil: 'load', timeout: 5000 });
let sourceRecords = [];
let idColumn;
let listColumn;
let originalRows;
let originalColumns;
let originalPopulation;
let appliedStep;
let fixtureGapReason;
let filterWitness;

const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });
const responseEvidence = (value) => {
  if (value === undefined) return undefined;
  const text = typeof value === 'string' ? value : String(value);
  try { return JSON.parse(text); } catch { return text.slice(0, 32768); }
};

const api = async (path, body, allowFailure = false) => {
  const requestId = `cda-authored-expand-${randomUUID()}`;
  const startedAt = Date.now();
  const response = await fetch(values['api-origin'] + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  const responseBody = responseEvidence(text);
  report.requests.push({ path, requestId, body, status: response.status, durationMs: Date.now() - startedAt, response: responseBody });
  if (!allowFailure) assert(response.ok, `${response.status} ${path}: ${JSON.stringify(responseBody)}`);
  return { status: response.status, body: responseBody };
};

const identity = (value = builder) => ({
  snapshotToken: value.catalog.snapshotToken,
  expectedDraftVersion: value.draftVersion,
  expectedDraftDigest: value.draftDigest,
});

const command = async (commands) => {
  await api(`${base}/commands`, {
    ...identity(), commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    commands,
  });
  builder = (await api(`${base}/builder`)).body;
};

const document = (value = builder) => value.workspace.documents.find((doc) => doc.output.id === outputId);
const previewTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const rowsReady = count => ({ kind: 'rows', selector: previewTableSelector, count });
const domText = () => inspectPage(page, 'return document.body.innerText;');

const boundedRawOracle = () => {
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(values.project)} AND r.dataset_generation == ${JSON.stringify(values.generation)} FILTER IS_ARRAY(r.payload.component) SORT r.id LIMIT 1000 RETURN {id:r.id,generation:r.dataset_generation,resourceType:r.payload.resourceType,components:r.payload.component}`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango oracle returned no JSON array: ${result.stdout.slice(0, 300)}`);
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'Raw source scan exceeded the 1000 Observation bound');

  const candidates = scanned.flatMap((resource) => {
    const components = resource.components;
    if (resource.resourceType !== 'Observation' || !Array.isArray(components) || components.length < 2 || components.length > 6) return [];
    const componentValues = components.map((component, ordinal) => ({ ordinal, value: component?.valueString }));
    if (!componentValues.every((item) => typeof item.value === 'string' && item.value.trim().length > 0)) return [];
    if (new Set(componentValues.map((item) => item.value)).size !== componentValues.length) return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: resource.resourceType, componentValues }];
  }).sort((left, right) => left.componentValues.length - right.componentValues.length || left.id.localeCompare(right.id));

  const selected = [];
  let totalComponents = 0;
  for (const candidate of candidates) {
    if (selected.length === 3) break;
    if (totalComponents + candidate.componentValues.length > 6) continue;
    selected.push(candidate);
    totalComponents += candidate.componentValues.length;
  }

  report.oracle = {
    source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length,
    selected: selected.map(({ id, generation, resourceType, componentValues }) => ({ id, generation, resourceType, componentValues })),
    expectedRows: selected.flatMap((resource) => resource.componentValues.map(({ ordinal, value }) => ({ id: resource.id, ordinal, value }))),
  };
  assert(selected.length <= 3, 'Raw oracle selected more than three Observation roots');
  assert(report.oracle.expectedRows.length <= 6, 'Raw oracle selected more than six component values');
  assert(new Set(selected.map((resource) => resource.id)).size === selected.length, 'Raw oracle selected duplicate Observation IDs');
  return selected;
};

const fastWait = async (startedAt, condition, message) => {
  const remainingMs = Math.max(100, 5000 - (Date.now() - startedAt));
  try { await waitForBrowser(page, condition, remainingMs); }
  catch (error) { throw new Error(`${message} within the five-second action budget: ${String(error)}`); }
};

const measure = async (name, action) => {
  const startedAt = Date.now();
  await action(startedAt);
  const durationMs = Date.now() - startedAt;
  report.workflowTimings.push({ name, durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  return durationMs;
};

const remainingActionMs = (startedAt) => Math.max(1, 5000 - (Date.now() - startedAt));

const openTable = async (expectedRows, name, afterRender) => measure(name, async (startedAt) => {
  const url = `${values['ui-origin']}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await navigate(page, url);
  await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-table-${outputId}"]` }, 'Explorer table discovery');
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await fastWait(startedAt, rowsReady(expectedRows), 'CDA table render');
  if (afterRender) await afterRender(startedAt);
});

const saveDOM = async (name) => {
  const path = join(values.evidence, `${name}.dom.txt`);
  await writeFile(path, await domText());
  report.evidencePaths.push(path);
};

const previewRows = async () => inspectPage(page, `
  const proposalRow=document.querySelector('[data-testid="construction-proposal-preview-row"]');
  const root=proposalRow?.closest('table')??document.querySelector(${JSON.stringify(previewTableSelector)});
  if(!root)return null;
  const proposal=Boolean(proposalRow);
  const headers=[...root.querySelectorAll(proposal?'thead th':'[role="columnheader"]')].map(cell=>cell.innerText.trim());
  const rows=[...root.querySelectorAll(proposal?'[data-testid="construction-proposal-preview-row"]':'[role="row"]')]
    .slice(proposal?0:1)
    .map(row=>[...row.querySelectorAll(proposal?'td':'[role="cell"]')].map(cell=>({text:cell.innerText.trim(),raw:cell.title})))
    .filter(row=>row.length);
  return {headers,rows,rowCount:root.getAttribute('aria-rowcount'),proposal};
`);

const parseCell = (cell) => {
  if (cell?.raw) {
    try { return JSON.parse(cell.raw); } catch { return cell.raw; }
  }
  return cell?.text ?? '';
};

const scalarCell = (cell, description) => {
  let value = parseCell(cell);
  if (Array.isArray(value)) {
    assert.equal(value.length, 1, `${description} should be one value per expanded row`);
    value = value[0];
  }
  return value;
};

const headerIndex = (preview, expectedLabel) => {
  assert(preview, 'The table result omitted its rendered preview');
  const normalizedExpected = expectedLabel.toLocaleLowerCase();
  const index = preview.headers.findIndex((header) =>
    header.split(String.fromCharCode(10))[0].trim().toLocaleLowerCase() === normalizedExpected);
  assert(index >= 0, `Preview is missing ${expectedLabel}: ${JSON.stringify(preview.headers)}`);
  return index;
};

const verifyOriginalRows = async (preview, phase) => {
  assert(preview, `${phase} omitted the visible source preview`);
  const idIndex = headerIndex(preview, 'Observation ID');
  const valueIndex = headerIndex(preview, 'Component Value String');
  assert.equal(preview.rows.length, sourceRecords.length, `${phase} must restore one source row per selected Observation`);
  const expected = new Map(report.oracle.selected.map((resource) => [resource.id, resource.componentValues.map((item) => item.value)]));
  const actual = new Map(preview.rows.map((row) => [String(scalarCell(row[idIndex], 'Observation ID')), parseCell(row[valueIndex])]));
  assert.deepEqual([...actual.keys()].sort(), [...expected.keys()].sort(), `${phase} must restore exact Observation identities`);
  for (const [id, valuesForId] of expected) {
    assert.equal(preview.rows.find((row) => String(scalarCell(row[idIndex], 'Observation ID')) === id)[valueIndex].text,
      valuesForId.join('; '), `${phase} displayed list must match raw source order`);
  }
  const entry = report.browserRequests.findLast((request) => request.path.endsWith(preview.proposal ? '/construction-proposals' : '/preview'));
  assert(entry, `${phase} omitted its native preview request`);
  const native = protocolPreview(await waitForCapturedResponse(entry));
  const nativeId = native.columns.find((column) => column.label === 'Observation ID')?.column;
  const nativeValue = native.columns.find((column) => column.label === 'Component Value String')?.column;
  assert(nativeId && nativeValue, `${phase} omitted protocol source columns`);
  assert.equal(native.rows.length, expected.size, `${phase} native source row count differs`);
  const nativeRows = new Map(native.rows.map((row) => [row[nativeId], row[nativeValue]]));
  assert.deepEqual([...nativeRows.keys()].sort(), [...expected.keys()].sort(), `${phase} native source identities differ`);
  for (const [id, valuesForId] of expected) {
    assert(Array.isArray(nativeRows.get(id)), `${phase} protocol value must be an actual list`);
    assert.deepEqual(nativeRows.get(id), valuesForId, `${phase} native list differs from raw source`);
  }
  return { headers: preview.headers, rowCount: preview.rows.length, observations: Object.fromEntries(actual) };
};

const expectedExpandTuples = () => report.oracle.expectedRows
  .map(({ id, ordinal, value }) => [id, ordinal, value])
  .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));

const sortedTuples = (tuples) => tuples
  .map((tuple) => [...tuple])
  .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));

const verifyFilteredRows = (preview, expectedLabel, expectedTuples, phase) => {
  assert(preview, `${phase} omitted its rendered FILTER preview`);
  const idIndex = headerIndex(preview, 'Observation ID');
  const valueIndex = headerIndex(preview, expectedLabel);
  const ordinalIndex = headerIndex(preview, 'Component position');
  const actual = preview.rows.map((row) => [
    String(scalarCell(row[idIndex], 'Observation ID')),
    Number(scalarCell(row[ordinalIndex], 'Component position')),
    String(scalarCell(row[valueIndex], expectedLabel)),
  ]);
  assert.equal(actual.length, expectedTuples.length, `${phase} row count must equal the strict-subset match count`);
  assert.deepEqual(sortedTuples(actual), sortedTuples(expectedTuples), `${phase} must contain the exact strict-subset Observation/value/ordinal tuples`);
  return { headers: preview.headers, rowCount: actual.length, tuples: sortedTuples(actual) };
};

const verifyExpandedRows = (preview, expectedLabel, phase) => {
  assert(preview, `${phase} omitted its rendered EXPAND preview`);
  const idIndex = headerIndex(preview, 'Observation ID');
  const valueIndex = headerIndex(preview, expectedLabel);
  const ordinalIndex = headerIndex(preview, 'Component position');
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, `${phase} row count must equal the raw component item count`);
  const actual = preview.rows.map((row) => [
    String(scalarCell(row[idIndex], 'Observation ID')),
    Number(scalarCell(row[ordinalIndex], 'Component position')),
    String(scalarCell(row[valueIndex], expectedLabel)),
  ]).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  assert.deepEqual(actual, expectedExpandTuples(), `${phase} must preserve exact Observation/value/zero-based ordinal tuples`);
  return { headers: preview.headers, rowCount: preview.rows.length, tuples: actual };
};

const protocolPreview = (response) => response?.preview ?? response;

const verifyProtocolExpandIdentities = (response, outputLabel, phase) => {
  const preview = protocolPreview(response);
  assert(preview?.columns && Array.isArray(preview.rows), `${phase} omitted native protocol preview rows`);
  const column = (label) => preview.columns.find((candidate) => candidate.label === label)?.column;
  const idColumn = column('Observation ID');
  const valueColumn = column(outputLabel);
  const ordinalColumn = column('Component position');
  assert(idColumn && valueColumn && ordinalColumn, `${phase} omitted an identity, item, or ordinal protocol column`);
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, `${phase} protocol row count differs from raw CDA`);

  const actualTuples = [];
  const mapping = [];
  const identities = [];
  const tupleKeys = new Set();
  for (const row of preview.rows) {
    const id = row[idColumn];
    const rawValue = row[valueColumn];
    const value = Array.isArray(rawValue)
      ? (assert.equal(rawValue.length, 1, `${phase} protocol item must be scalar per expanded row`), rawValue[0])
      : rawValue;
    const ordinal = Number(row[ordinalColumn]);
    assert.equal(typeof id, 'string', `${phase} protocol Observation ID must be a string`);
    assert.equal(typeof value, 'string', `${phase} protocol item value must be a string`);
    assert(Number.isInteger(ordinal) && ordinal >= 0, `${phase} protocol ordinal must be a non-negative integer`);
    assert.equal(typeof row.__loom_row_id, 'string', `${phase} native protocol omitted __loom_row_id`);
    assert(row.__loom_row_id.length > 0, `${phase} native protocol exposed an empty __loom_row_id`);
    const tupleKey = JSON.stringify([id, ordinal]);
    assert(!tupleKeys.has(tupleKey), `${phase} protocol duplicated Observation/ordinal tuple ${tupleKey}`);
    tupleKeys.add(tupleKey);
    actualTuples.push(JSON.stringify([id, ordinal, value]));
    mapping.push(JSON.stringify([id, ordinal, row.__loom_row_id]));
    identities.push(row.__loom_row_id);
  }
  const expectedTuples = report.oracle.expectedRows
    .map(({ id, ordinal, value }) => JSON.stringify([id, ordinal, value])).sort();
  assert.deepEqual(actualTuples.sort(), expectedTuples, `${phase} protocol tuples differ from raw CDA`);
  assert.equal(new Set(identities).size, preview.rows.length, `${phase} protocol row identities must be unique`);
  return mapping.sort();
};

const verifyProtocolTupleIdentities = (response, outputLabel, expectedTuples, phase) => {
  const preview = protocolPreview(response);
  assert(preview?.columns && Array.isArray(preview.rows), `${phase} omitted native protocol preview rows`);
  const column = (label) => preview.columns.find((candidate) => candidate.label === label)?.column;
  const idColumn = column('Observation ID');
  const valueColumn = column(outputLabel);
  const ordinalColumn = column('Component position');
  assert(idColumn && valueColumn && ordinalColumn, `${phase} omitted an identity, item, or ordinal protocol column`);
  assert.equal(preview.rows.length, expectedTuples.length, `${phase} protocol row count differs from the strict-subset oracle`);

  const actualTuples = [];
  const mapping = [];
  const identities = [];
  const tupleKeys = new Set();
  for (const row of preview.rows) {
    const id = row[idColumn];
    const rawValue = row[valueColumn];
    const value = Array.isArray(rawValue)
      ? (assert.equal(rawValue.length, 1, `${phase} protocol item must be scalar per expanded row`), rawValue[0])
      : rawValue;
    const ordinal = Number(row[ordinalColumn]);
    assert.equal(typeof id, 'string', `${phase} protocol Observation ID must be a string`);
    assert.equal(typeof value, 'string', `${phase} protocol item value must be a string`);
    assert(Number.isInteger(ordinal) && ordinal >= 0, `${phase} protocol ordinal must be a non-negative integer`);
    assert.equal(typeof row.__loom_row_id, 'string', `${phase} native protocol omitted __loom_row_id`);
    assert(row.__loom_row_id.length > 0, `${phase} native protocol exposed an empty __loom_row_id`);
    const tupleKey = JSON.stringify([id, ordinal]);
    assert(!tupleKeys.has(tupleKey), `${phase} protocol duplicated Observation/ordinal tuple ${tupleKey}`);
    tupleKeys.add(tupleKey);
    actualTuples.push([id, ordinal, value]);
    mapping.push(JSON.stringify([id, ordinal, row.__loom_row_id]));
    identities.push(row.__loom_row_id);
  }
  assert.deepEqual(sortedTuples(actualTuples), sortedTuples(expectedTuples), `${phase} protocol tuples differ from the strict-subset oracle`);
  assert.equal(new Set(identities).size, preview.rows.length, `${phase} protocol row identities must be unique`);
  return mapping.sort();
};

const expectedMatchingIdentityMapping = (allMapping, matchingTuples) => {
  const matchingKeys = new Set(matchingTuples.map(([id, ordinal]) => JSON.stringify([id, ordinal])));
  return allMapping.filter((entry) => {
    const [id, ordinal] = JSON.parse(entry);
    return matchingKeys.has(JSON.stringify([id, ordinal]));
  }).sort();
};

const nativePreviewAfter = async (requestIndex, phase) => {
  const entry = report.browserRequests.findLast((candidate, index) => index >= requestIndex && candidate.path.endsWith('/preview'));
  assert(entry, `${phase} did not trigger a native /preview protocol request`);
  const response = await waitForCapturedResponse(entry);
  assert(protocolPreview(response)?.rows, `${phase} native /preview response omitted rows`);
  return { entry, response };
};

const readSavedPreviewBinding = () => inspectPage(page, `const preview=document.querySelector('[data-testid="construction-preview"]');
  return preview ? {
    status: preview.dataset.previewStatus,
    receiptId: preview.dataset.previewReceiptId,
    outputId: preview.dataset.previewOutputId,
    draftVersion: preview.dataset.currentDraftVersion,
    draftDigest: preview.dataset.currentDraftDigest,
  } : null;`);

const captureSavedPreviewState = async (savedBuilder) => {
  const savedDocument = savedBuilder.workspace.documents.find(doc => doc.output.id === outputId);
  assert(savedDocument, `Saved builder state omitted output ${outputId}`);
  return {
    snapshotToken: savedBuilder.catalog?.snapshotToken,
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    outputId: savedDocument.output.id,
    construction: structuredClone(savedDocument.construction),
    preview: await readSavedPreviewBinding(),
  };
};

const nativePreviewAfterCancel = async (requestIndex, phase, before) => {
  const after = await captureSavedPreviewState(builder);
  const previewPath = `${base}/preview`;
  const selected = selectCanceledSavedPreviewRequest(report.browserRequests, {
    startIndex: requestIndex,
    path: previewPath,
    outputId,
    before,
    after,
  });
  const previewRequestEvidence = (entry) => ({
    path: entry.path,
    status: entry.status,
    completedAt: entry.completedAt,
    triggerAction: entry.triggerAction,
    requestReceiptId: entry.body?.receiptId,
    requestOutputId: entry.body?.outputId,
    responseReceiptId: entry.response?.receiptId,
    responseOutputId: entry.response?.outputId,
    proposalPreviewReceiptId: entry.response?.preview?.receiptId,
    rowCount: entry.response?.rows?.length ?? entry.response?.preview?.rows?.length,
  });
  report.cancelPreviewRestorations.push({
    phase,
    source: selected?.source ?? null,
    selectedRequest: selected ? previewRequestEvidence(selected.request) : null,
    before,
    after,
    matchingReceiptRequests: report.browserRequests
      .filter((entry) => [before.preview?.receiptId, after.preview?.receiptId].some((receiptId) =>
        receiptId && (entry.body?.receiptId === receiptId || entry.response?.preview?.receiptId === receiptId)))
      .map(previewRequestEvidence),
    previewRequestsSinceCancel: report.browserRequests.slice(requestIndex)
      .filter((entry) => entry.path === previewPath)
      .map(previewRequestEvidence),
  });
  assert(selected, `${phase} must restore a preview bound to the unchanged saved draft, snapshot, output, and construction`);
  const response = await waitForCapturedResponse(selected.request);
  assert(protocolPreview(response)?.rows, `${phase} native Preview response omitted rows`);
  return { entry: selected.request, response, source: selected.source };
};

const monitorBrowser = () => {
  report.browserRequests = report.nativeRequests;
  return requestMonitor;
};

const waitForCapturedResponse = async (entry) => {
  assert(entry, 'Expected browser protocol request was not captured');
  if (entry.status === undefined) {
    await requestMonitor.waitFor(candidate => candidate === entry, { timeoutMs: 5000 });
  }
  await requestMonitor.flush();
  assert.equal(entry.status, 200, `Browser request failed: ${entry.path} ${JSON.stringify(entry.response)}`);
  assert.equal(entry.responseReadError, undefined, `Could not read the browser response body: ${entry.responseReadError}`);
  return entry.response;
};

const latestExpandProposal = (expectedLabel) => report.browserRequests.findLast((entry) =>
  entry.path.endsWith('/construction-proposals') &&
  entry.body?.candidateConstruction?.steps?.some((step) => step.operation?.kind === 'EXPAND' &&
    (!expectedLabel || step.outputs.some((column) => column.id === step.operation.expand.outputColumnId && column.label === expectedLabel))));

const latestCommand = (type) => report.browserRequests.findLast((entry) =>
  entry.path.endsWith('/commands') && entry.body?.commands?.some((commandValue) => commandValue.type === type));

const waitForProposal = async (startedAt, outputLabel) => {
  const selector = '[data-testid="construction-proposal-panel"]';
  await fastWait(startedAt, { kind: 'all', conditions: [
    { kind: 'status-in', selector, statuses: ['ready'] },
    { kind: 'text-includes', selector: '[data-testid="construction-proposal-preview"]', text: outputLabel },
  ] }, 'Native EXPAND proposal preview');
  const preview = await previewRows();
  assert(preview?.proposal, 'EXPAND must render a proposal table before Apply');
  return preview;
};

const openExpandEditor = async () => measure('native-EXPAND-editor-discovery', async (startedAt) => {
  if (!composedFilter) {
    const editorAlreadyOpen = await inspectPage(page, `return Boolean(document.querySelector('[data-testid="construction-reshape-editor"]'));`);
    if (!editorAlreadyOpen) {
      await click(page, '[data-testid="construction-rows-settings-trigger"]');
      await fastWait(startedAt, { kind: 'present', selector: '[role="dialog"][aria-label="Row definition settings"]' }, 'Row settings discovery');
      const unpivot = await inspectPage(page, `const button=document.querySelector('[data-testid="construction-action-unpivot-rows"]');return {found:Boolean(button),disabled:button?.disabled};`);
      assert.equal(unpivot.found, true, 'The native reshape entry is missing from row settings');
      assert.equal(unpivot.disabled, false, 'The native reshape entry must be enabled');
      await click(page, '[data-testid="construction-action-unpivot-rows"]');
      await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-editor"]' }, 'Reshape editor discovery');
    }
    const changeOperation = await inspectPage(page, `return Boolean([...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Change row operation'));`);
    if (changeOperation) await click(page, 'button', { name: 'Change row operation' });
    await fastWait(startedAt, { kind: 'enabled', selector: '[data-testid="construction-reshape-choice-expand"]' }, 'EXPAND choice discovery');
    return;
  }

  const expandEditorAlreadyOpen = await inspectPage(page, `return Boolean(document.querySelector('[data-testid="construction-reshape-expand"]'));`);
  if (expandEditorAlreadyOpen) {
    await expect(page.locator('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]'))
      .toHaveCount(1, { timeout: remainingActionMs(startedAt) });
    return;
  }

  const editorAlreadyOpen = await inspectPage(page, `return Boolean(document.querySelector('[data-testid="construction-reshape-editor"]'));`);
  if (!editorAlreadyOpen) {
    await click(page, '[data-testid="construction-rows-settings-trigger"]', {}, remainingActionMs(startedAt));
    await expect(page.locator('[role="dialog"][aria-label="Row definition settings"]'))
      .toHaveCount(1, { timeout: remainingActionMs(startedAt) });
    const actionSelector = '[data-testid="construction-action-expand-rows"]';
    const action = await resolveActionLocator(page, actionSelector, {}, remainingActionMs(startedAt));
    await expect(action).toBeEnabled({ timeout: remainingActionMs(startedAt) });
    const expand = await inspectPage(page, `const button=document.querySelector('[data-testid="construction-action-expand-rows"]');return {found:Boolean(button),disabled:button?.disabled};`);
    assert.equal(expand.found, true, 'The native authored list EXPAND entry is missing from row settings');
    assert.equal(expand.disabled, false, 'The native authored list EXPAND entry must be enabled for the projected list');
    await click(page, actionSelector, {}, remainingActionMs(startedAt));
  } else {
    const changeOperation = await inspectPage(page, `return Boolean([...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Change row operation'));`);
    if (changeOperation) {
      await click(page, 'button', { name: 'Change row operation' });
      await fastWait(startedAt, { kind: 'enabled', selector: '[data-testid="construction-reshape-choice-expand"]' }, 'EXPAND choice discovery');
      await click(page, '[data-testid="construction-reshape-choice-expand"]');
    }
  }
  if (composedFilter) {
    await expect(page.locator('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]'))
      .toHaveCount(1, { timeout: remainingActionMs(startedAt) });
  } else {
    await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 'EXPAND editor controls');
  }
});

const setInput = (label, value) => fill(page, `input[aria-label=${JSON.stringify(label)}]`, value);

const configureExpand = async (expectedLabel) => {
  await openExpandEditor();
  const startedAt = Date.now();
  if (!composedFilter) await click(page, '[data-testid="construction-reshape-choice-expand"]');
  await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 5000);
  const choices = await inspectPage(page, `return [...document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]').options].map(option=>({value:option.value,label:option.text,disabled:option.disabled}));`);
  const matching = choices.filter((option) => option.label.startsWith('Component Value String') && !option.disabled);
  assert.equal(matching.length, 1, `EXPAND must offer exactly the projected component list: ${JSON.stringify(choices)}`);
  await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]', matching[0].value);
  const advanced = '[data-testid="construction-reshape-expand-advanced"]';
  await click(page, `${advanced} summary`);
  await setInput('Expanded item name', 'component_value_item');
  await setInput('Expanded item label', expectedLabel);
  const checked = await inspectPage(page, `return document.querySelector('input[aria-label="Include item position"]')?.checked;`);
  if (!checked) await click(page, 'input[aria-label="Include item position"]');
  await setInput('Position column name', 'component_value_position');
  await setInput('Position column label', 'Component position');
  await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Empty list policy"]', 'PRESERVE_PARENT');
  const preview = await waitForProposal(startedAt, expectedLabel);
  const durationMs = Date.now() - startedAt;
  report.workflowTimings.push({ name: 'native-EXPAND-configure-to-preview', durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `EXPAND configure-to-preview took ${durationMs}ms`);
  return preview;
};

const assertExpandRequest = async (expectedLabel, expectedInputColumnId, expectedStepId) => {
  const entry = latestExpandProposal(expectedLabel);
  assert(entry, 'Native EXPAND editor did not send a construction proposal');
  const request = entry.body;
  const step = request.candidateConstruction.steps.findLast((candidate) => candidate.operation?.kind === 'EXPAND');
  assert(step, 'Construction proposal must contain an EXPAND operation');
  assert.equal(step.operation.expand.inputColumnId, expectedInputColumnId, 'EXPAND must bind the projected component[].valueString list');
  assert.equal(step.operation.expand.emptyPolicy, 'PRESERVE_PARENT', 'EXPAND must carry the selected empty-list policy');
  assert(step.operation.expand.ordinalColumnId, 'EXPAND must include the optional zero-based item position column');
  if (expectedStepId) assert.equal(step.id, expectedStepId, 'Editing must retain the saved operation identity');
  assert.equal(step.outputs.find((column) => column.id === step.operation.expand.outputColumnId)?.name, 'component_value_item');
  assert.equal(step.outputs.find((column) => column.id === step.operation.expand.outputColumnId)?.label, expectedLabel);
  assert(step.outputs.some((column) => column.id === step.operation.expand.ordinalColumnId && column.name === 'component_value_position' && column.label === 'Component position'));
  const response = await waitForCapturedResponse(entry);
  assert.equal(response.preview?.rowCount, report.oracle.expectedRows.length, 'Construction API preview count must equal the independent raw item count');
  return { request, step, response };
};

const latestFilterProposal = () => report.browserRequests.findLast((entry) =>
  entry.path.endsWith('/construction-proposals') &&
  entry.body?.candidateConstruction?.steps?.some((step) => step.operation?.kind === 'FILTER'));

const assertFilterProposal = async (expectedLabel, expectedOutputColumnId, witness, expectedExpandStepId, expectedFilterStepId) => {
  const request = latestFilterProposal();
  assert(request, 'Native Filter editor did not send a construction proposal');
  const steps = request.body.candidateConstruction.steps;
  const expand = steps.find((step) => step.operation?.kind === 'EXPAND');
  const filter = steps.findLast((step) => step.operation?.kind === 'FILTER');
  assert(expand && filter, 'Native Filter proposal must retain EXPAND and include FILTER');
  assert.deepEqual(steps.map((step) => step.operation.kind), ['EXPAND', 'FILTER'],
    'Native Filter proposal must contain exactly the authored EXPAND followed by its dependent FILTER');
  assert.equal(expand.id, expectedExpandStepId, 'FILTER proposal must retain the saved EXPAND step identity');
  assert.equal(expand.operation.expand.outputColumnId, expectedOutputColumnId, 'FILTER proposal must retain the authored EXPAND item output identity');
  assert.equal(expand.outputs.find((column) => column.id === expectedOutputColumnId)?.label, expectedLabel,
    'FILTER proposal must use the current EXPAND item presentation label');
  if (expectedFilterStepId) assert.equal(filter.id, expectedFilterStepId, 'Editing upstream EXPAND must preserve the saved Filter identity');
  assert.equal(filter.operation.filter.columnId, expectedOutputColumnId, 'FILTER must bind the authored EXPAND item output column by stable ID');
  assert.equal(filter.operation.filter.operator, 'EQUALS', 'Authored item Filter must use equality');
  assert.deepEqual(filter.operation.filter.values, [{ kind: 'STRING', string: witness.predicateValue }],
    'Authored item Filter must retain the exact raw value predicate');
  assert(steps.indexOf(expand) < steps.indexOf(filter), 'FILTER must remain downstream of EXPAND');
  const response = await waitForCapturedResponse(request);
  assert.equal(response.preview?.rowCount, witness.matchingTuples.length,
    'Native Filter proposal preview row count must equal the raw strict-subset oracle');
  return { request, response, expand, filter };
};

const configureNativeFilter = async (expectedLabel, outputColumnId, witness) => {
  const startedAt = Date.now();
  const filterEditor = '[data-testid="construction-filter-editor"]';
  await click(page, '[data-testid="construction-action-keep-rows"]');
  await fastWait(startedAt, { kind: 'enabled', selector: `${filterEditor} select[aria-label="Column"]` }, 'Native Filter column control');
  const options = await inspectPage(page, `return [...document.querySelector(${JSON.stringify(filterEditor)}+' select[aria-label="Column"]').options].map(option=>({value:option.value,label:option.textContent.trim(),disabled:option.disabled}));`);
  const output = options.find((option) => option.value === outputColumnId && !option.disabled);
  assert(output, `Native Filter must offer the saved EXPAND output column ID ${outputColumnId}: ${JSON.stringify(options)}`);
  assert(output.label.startsWith(expectedLabel), `Native Filter column label must reflect ${expectedLabel}: ${output.label}`);
  await selectOption(page, `${filterEditor} select[aria-label="Column"]`, output.value);
  await selectOption(page, `${filterEditor} select[aria-label="Condition"]`, 'EQUALS');
  await fill(page, `${filterEditor} input[aria-label="Value"]`, witness.predicateValue);
  const preview = await waitForProposal(startedAt, expectedLabel);
  const durationMs = Date.now() - startedAt;
  report.workflowTimings.push({ name: 'native-FILTER-configure-to-preview', durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `FILTER configure-to-preview took ${durationMs}ms`);
  return preview;
};

const composedFilterLifecycle = async (authoredIdentityMapping, witness) => {
  let cascadeAppliedSourceIdentity;
  const expectedMatchingTuples = sortedTuples(witness.matchingTuples);
  const matchingIdentityMapping = expectedMatchingIdentityMapping(authoredIdentityMapping, expectedMatchingTuples);
  assert.equal(matchingIdentityMapping.length, expectedMatchingTuples.length,
    'The raw strict-subset tuples must map to their existing EXPAND row identities');
  report.filterIdentityMappings = { expected: matchingIdentityMapping };

  const firstFilterPreview = await configureNativeFilter('Expanded component value', appliedStep.operation.expand.outputColumnId, witness);
  report.filterProposalPreview = verifyFilteredRows(firstFilterPreview, 'Expanded component value', expectedMatchingTuples, 'Native Filter proposal');
  const firstFilterProposal = await assertFilterProposal(
    'Expanded component value', appliedStep.operation.expand.outputColumnId, witness, appliedStep.id,
  );
  report.filterIdentityMappings.proposal = verifyProtocolTupleIdentities(
    firstFilterProposal.response, 'Expanded component value', expectedMatchingTuples, 'Native Filter proposal',
  );
  assert.deepEqual(report.filterIdentityMappings.proposal, matchingIdentityMapping,
    'Filter proposal must preserve the exact __loom_row_id for each matching Observation/ordinal tuple');
  recordAssertion('native Filter binds the authored EXPAND item output column by stable column identity', {
    columnId: appliedStep.operation.expand.outputColumnId,
    label: 'Expanded component value',
    filter: firstFilterProposal.filter.operation.filter,
    stepIds: [firstFilterProposal.expand.id, firstFilterProposal.filter.id],
  });
  recordAssertion('Filter proposal previews exactly the strict-subset tuples and stable item identities', {
    predicateValue: witness.predicateValue,
    preview: report.filterProposalPreview,
    identities: report.filterIdentityMappings.proposal,
  });
  await saveDOM('expand-filter-proposal');

  const beforeFilterCancel = (await api(`${base}/builder`)).body;
  const beforeFilterCancelPreviewState = await captureSavedPreviewState(beforeFilterCancel);
  const filterCancelRequestIndex = report.browserRequests.length;
  await measure('native-FILTER-cancel-restoration', async (startedAt) => {
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, rowsReady(report.oracle.expectedRows.length),
    ] }, 'Canceled Filter full EXPAND restoration');
  });
  builder = (await api(`${base}/builder`)).body;
  assert.equal(builder.draftDigest, beforeFilterCancel.draftDigest, 'Cancel must leave saved EXPAND construction unchanged');
  assert.equal(document().construction.steps.length, 1, 'Cancel must not persist the Filter step');
  assert.equal(document().construction.steps[0].id, appliedStep.id, 'Cancel must preserve the saved EXPAND step');
  report.filterCanceledPreview = verifyExpandedRows(await previewRows(), 'Expanded component value', 'Canceled Filter table');
  const filterCancelProtocol = await nativePreviewAfterCancel(
    filterCancelRequestIndex, 'Canceled Filter full EXPAND', beforeFilterCancelPreviewState,
  );
  report.filterIdentityMappings.canceledExpand = verifyProtocolExpandIdentities(
    filterCancelProtocol.response, 'Expanded component value', 'Canceled Filter full EXPAND',
  );
  assert.deepEqual(report.filterIdentityMappings.canceledExpand, authoredIdentityMapping,
    'Filter Cancel must preserve every full EXPAND row identity');
  await saveDOM('expand-filter-canceled');
  recordAssertion('Filter Cancel preserves the full expanded rows and does not save the Filter', {
    construction: document().construction,
    preview: report.filterCanceledPreview,
    identities: report.filterIdentityMappings.canceledExpand,
  });

  const appliedFilterPreview = await configureNativeFilter('Expanded component value', appliedStep.operation.expand.outputColumnId, witness);
  report.filterAppliedProposalPreview = verifyFilteredRows(appliedFilterPreview, 'Expanded component value', expectedMatchingTuples, 'Confirmed Filter proposal');
  const appliedFilterProposal = await assertFilterProposal(
    'Expanded component value', appliedStep.operation.expand.outputColumnId, witness, appliedStep.id,
  );
  const filterStepId = appliedFilterProposal.filter.id;
  report.filterIdentityMappings.confirmedProposal = verifyProtocolTupleIdentities(
    appliedFilterProposal.response, 'Expanded component value', expectedMatchingTuples, 'Confirmed Filter proposal',
  );
  assert.deepEqual(report.filterIdentityMappings.confirmedProposal, matchingIdentityMapping,
    'Confirmed Filter proposal must preserve exact matching item identities');

  const filterApplyPreviewRequestIndex = report.browserRequests.length;
  await measure('native-FILTER-apply', async (startedAt) => {
    await click(page, '[data-testid="construction-apply-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
      rowsReady(expectedMatchingTuples.length),
      { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 2 },
    ] }, 'Applied native Filter result');
  });
  builder = (await api(`${base}/builder`)).body;
  const savedExpand = document().construction.steps.find((step) => step.id === appliedStep.id);
  const savedFilter = document().construction.steps.find((step) => step.id === filterStepId);
  assert(savedExpand && savedFilter, 'Filter Apply must persist both EXPAND and FILTER');
  assert.equal(savedFilter.operation.filter.columnId, savedExpand.operation.expand.outputColumnId);
  assert.equal(savedFilter.operation.filter.columnId, appliedStep.operation.expand.outputColumnId);
  assert.deepEqual(savedFilter.operation.filter, appliedFilterProposal.filter.operation.filter,
    'Filter Apply must persist the exact proposed output binding and predicate');
  assert.equal(document().construction.steps.length, 2);
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().rows, originalRows);
  report.filterAppliedPreview = verifyFilteredRows(await previewRows(), 'Expanded component value', expectedMatchingTuples, 'Applied native Filter table');
  const filterAppliedProtocol = await nativePreviewAfter(filterApplyPreviewRequestIndex, 'Applied native Filter');
  report.filterIdentityMappings.applied = verifyProtocolTupleIdentities(
    filterAppliedProtocol.response, 'Expanded component value', expectedMatchingTuples, 'Applied native Filter',
  );
  assert.deepEqual(report.filterIdentityMappings.applied, matchingIdentityMapping,
    'Filter Apply must preserve exact matching item identities');
  const filterApplyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
  assert(filterApplyRequest, 'Native Filter Apply must use APPLY_CONSTRUCTION_PROPOSAL');
  await waitForCapturedResponse(filterApplyRequest);
  await saveDOM('expand-filter-applied');

  const filterReloadRequestIndex = report.browserRequests.length;
  await openTable(expectedMatchingTuples.length, 'reload-applied-expand-filter');
  report.filterReloadPreview = verifyFilteredRows(await previewRows(), 'Expanded component value', expectedMatchingTuples, 'Reloaded native Filter table');
  const filterReloadProtocol = await nativePreviewAfter(filterReloadRequestIndex, 'Reloaded native Filter');
  report.filterIdentityMappings.appliedReload = verifyProtocolTupleIdentities(
    filterReloadProtocol.response, 'Expanded component value', expectedMatchingTuples, 'Reloaded native Filter',
  );
  assert.deepEqual(report.filterIdentityMappings.appliedReload, matchingIdentityMapping,
    'Filter reload must preserve exact matching item identities');
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().construction.steps.find((step) => step.id === filterStepId)?.operation.filter.columnId,
    appliedStep.operation.expand.outputColumnId);
  report.filterAppliedSaved = { expand: savedExpand, filter: savedFilter };
  await saveDOM('expand-filter-reloaded');
  recordAssertion('Filter Apply and reload preserve exact matching tuples and stable row identities', {
    proposal: report.filterAppliedProposalPreview,
    applied: report.filterAppliedPreview,
    reloaded: report.filterReloadPreview,
    identities: report.filterIdentityMappings,
  });

  const reopenExpandEditor = async (expectedLabel) => {
    const startedAt = Date.now();
    await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
    await fastWait(startedAt, { kind: 'enabled', selector: `[data-testid="construction-edit-step-${appliedStep.id}"]` }, 'EXPAND edit control');
    await click(page, `[data-testid="construction-edit-step-${appliedStep.id}"]`);
    await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 'EXPAND editor controls');
    const advanced = '[data-testid="construction-reshape-expand-advanced"]';
    const advancedOpen = await inspectPage(page, `return document.querySelector(${JSON.stringify(advanced)})?.open;`);
    if (!advancedOpen) await click(page, `${advanced} summary`);
    const reopened = await inspectPage(page, `return {
    field:document.querySelector('select[aria-label="Repeated field"]')?.value,
    name:document.querySelector('input[aria-label="Expanded item name"]')?.value,
    label:document.querySelector('input[aria-label="Expanded item label"]')?.value,
    ordinal:document.querySelector('input[aria-label="Include item position"]')?.checked,
    positionName:document.querySelector('input[aria-label="Position column name"]')?.value,
    positionLabel:document.querySelector('input[aria-label="Position column label"]')?.value,
    emptyPolicy:document.querySelector('select[aria-label="Empty list policy"]')?.value,
  };`);
    const durationMs = Date.now() - startedAt;
    report.workflowTimings.push({ name: 'native-EXPAND-editor-reopen-to-controls', durationMs, limitMs: 5000 });
    assert(durationMs <= 5000, `EXPAND editor reopen took ${durationMs}ms`);
    assert.equal(reopened.field, appliedStep.operation.expand.inputColumnId);
    assert.equal(reopened.name, 'component_value_item');
    assert.equal(reopened.label, expectedLabel);
    assert.equal(reopened.ordinal, true);
    assert.equal(reopened.positionName, 'component_value_position');
    assert.equal(reopened.positionLabel, 'Component position');
    assert.equal(reopened.emptyPolicy, 'PRESERVE_PARENT');
    return reopened;
  };

  const beforeExpandEdit = (await api(`${base}/builder`)).body;
  const beforeExpandEditConstruction = structuredClone(document(beforeExpandEdit).construction);
  const beforeExpandEditPreviewState = await captureSavedPreviewState(beforeExpandEdit);
  await reopenExpandEditor('Expanded component value');

  const expandEditStartedAt = Date.now();
  await setInput('Expanded item label', 'Edited component value');
  const editedFilterProposalPreview = await waitForProposal(expandEditStartedAt, 'Edited component value');
  const expandEditDurationMs = Date.now() - expandEditStartedAt;
  report.workflowTimings.push({ name: 'native-EXPAND-label-edit-with-FILTER-to-preview', durationMs: expandEditDurationMs, limitMs: 5000 });
  assert(expandEditDurationMs <= 5000, `EXPAND label edit with FILTER took ${expandEditDurationMs}ms`);
  report.expandEditCanceledProposalPreview = verifyFilteredRows(
    editedFilterProposalPreview, 'Edited component value', expectedMatchingTuples, 'Canceled EXPAND label edit proposal',
  );
  const editedFilterProposal = await assertFilterProposal(
    'Edited component value', appliedStep.operation.expand.outputColumnId, witness, appliedStep.id, filterStepId,
  );
  assert.deepEqual(editedFilterProposal.filter.operation.filter, savedFilter.operation.filter,
    'Changing only the EXPAND presentation label must preserve the exact saved Filter binding and predicate');
  const canceledEditProposalMapping = verifyProtocolTupleIdentities(
    editedFilterProposal.response, 'Edited component value', expectedMatchingTuples, 'Canceled EXPAND label edit proposal',
  );
  assert.deepEqual(canceledEditProposalMapping, matchingIdentityMapping,
    'EXPAND label edit proposal must preserve exact filtered row identities before Cancel');

  const expandEditCancelRequestIndex = report.browserRequests.length;
  await measure('native-EXPAND-label-edit-cancel', async (startedAt) => {
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
      rowsReady(expectedMatchingTuples.length),
      { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 2 },
    ] }, 'Canceled upstream EXPAND label edit');
  });
  builder = (await api(`${base}/builder`)).body;
  assert.equal(builder.draftDigest, beforeExpandEdit.draftDigest,
    'Canceling the upstream label edit must restore the saved draft digest');
  assert.deepEqual(document().construction, beforeExpandEditConstruction,
    'Canceling the upstream label edit must preserve the exact saved EXPAND and Filter construction');
  const canceledExpand = document().construction.steps.find((step) => step.id === appliedStep.id);
  const canceledFilter = document().construction.steps.find((step) => step.id === filterStepId);
  assert(canceledExpand && canceledFilter, 'Cancel must preserve both saved operations by identity');
  assert.equal(canceledExpand.outputs.find((column) => column.id === canceledExpand.operation.expand.outputColumnId)?.label,
    'Expanded component value', 'Cancel must restore the original EXPAND item label');
  assert.equal(canceledFilter.operation.filter.columnId, canceledExpand.operation.expand.outputColumnId,
    'Cancel must preserve the saved Filter binding to the EXPAND item column');
  assert.deepEqual(canceledFilter.operation.filter, savedFilter.operation.filter,
    'Cancel must preserve the exact saved Filter operator and predicate');
  report.expandEditCanceledPreview = verifyFilteredRows(
    await previewRows(), 'Expanded component value', expectedMatchingTuples, 'Canceled upstream EXPAND label edit',
  );
  const expandEditCancelProtocol = await nativePreviewAfterCancel(
    expandEditCancelRequestIndex, 'Canceled upstream EXPAND label edit', beforeExpandEditPreviewState,
  );
  report.filterIdentityMappings.expandEditCanceled = verifyProtocolTupleIdentities(
    expandEditCancelProtocol.response, 'Expanded component value', expectedMatchingTuples, 'Canceled upstream EXPAND label edit',
  );
  assert.deepEqual(report.filterIdentityMappings.expandEditCanceled, matchingIdentityMapping,
    'Canceling the upstream label edit must preserve exact filtered item identities');
  await saveDOM('expand-filter-label-edit-canceled');
  report.expandEditCanceledDefaults = await reopenExpandEditor('Expanded component value');

  const confirmedEditStartedAt = Date.now();
  await setInput('Expanded item label', 'Edited component value');
  const confirmedEditPreview = await waitForProposal(confirmedEditStartedAt, 'Edited component value');
  const confirmedEditDurationMs = Date.now() - confirmedEditStartedAt;
  report.workflowTimings.push({ name: 'native-EXPAND-label-edit-after-cancel-to-preview', durationMs: confirmedEditDurationMs, limitMs: 5000 });
  assert(confirmedEditDurationMs <= 5000, `EXPAND label edit after Cancel took ${confirmedEditDurationMs}ms`);
  report.expandEditedFilterProposal = verifyFilteredRows(
    confirmedEditPreview, 'Edited component value', expectedMatchingTuples, 'Confirmed EXPAND label edit with saved Filter proposal',
  );
  const confirmedFilterProposal = await assertFilterProposal(
    'Edited component value', appliedStep.operation.expand.outputColumnId, witness, appliedStep.id, filterStepId,
  );
  assert.deepEqual(confirmedFilterProposal.filter.operation.filter, savedFilter.operation.filter,
    'Confirmed upstream label edit must preserve the exact saved Filter binding and predicate');
  const editedFilterMapping = verifyProtocolTupleIdentities(
    confirmedFilterProposal.response, 'Edited component value', expectedMatchingTuples, 'Confirmed EXPAND label edit with saved Filter proposal',
  );
  assert.deepEqual(editedFilterMapping, matchingIdentityMapping,
    'Confirmed EXPAND label edit must preserve exact filtered row identities');

  const editedFilterApplyRequestIndex = report.browserRequests.length;
  await measure('native-EXPAND-label-edit-with-FILTER-apply', async (startedAt) => {
    await click(page, '[data-testid="construction-apply-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
      rowsReady(expectedMatchingTuples.length),
      { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 2 },
    ] }, 'Applied upstream EXPAND label edit with saved Filter');
  });
  builder = (await api(`${base}/builder`)).body;
  appliedStep = document().construction.steps.find((step) => step.id === appliedStep.id);
  const filterAfterExpandEdit = document().construction.steps.find((step) => step.id === filterStepId);
  assert(appliedStep && filterAfterExpandEdit, 'EXPAND label edit must preserve both saved operations');
  assert.equal(appliedStep.operation.expand.outputColumnId, savedExpand.operation.expand.outputColumnId,
    'EXPAND label edit must preserve its saved output column ID');
  assert.equal(appliedStep.outputs.find((column) => column.id === appliedStep.operation.expand.outputColumnId)?.label, 'Edited component value');
  assert.deepEqual(filterAfterExpandEdit.operation.filter, savedFilter.operation.filter,
    'Saved Filter must retain its original output column ID and exact predicate after EXPAND label edit');
  assert.equal(filterAfterExpandEdit.operation.filter.columnId, appliedStep.operation.expand.outputColumnId);
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().rows, originalRows);
  report.expandEditAppliedPreview = verifyFilteredRows(
    await previewRows(), 'Edited component value', expectedMatchingTuples, 'Applied EXPAND label edit with saved Filter',
  );
  const editedFilterAppliedProtocol = await nativePreviewAfter(editedFilterApplyRequestIndex, 'Applied EXPAND label edit with saved Filter');
  report.filterIdentityMappings.expandEditApplied = verifyProtocolTupleIdentities(
    editedFilterAppliedProtocol.response, 'Edited component value', expectedMatchingTuples, 'Applied EXPAND label edit with saved Filter',
  );
  assert.deepEqual(report.filterIdentityMappings.expandEditApplied, matchingIdentityMapping,
    'Applied EXPAND label edit must preserve exact filtered row identities');
  await saveDOM('expand-filter-label-edited-applied');

  const expandEditReloadRequestIndex = report.browserRequests.length;
  await openTable(expectedMatchingTuples.length, 'reload-edited-expand-filter');
  report.expandEditReloadPreview = verifyFilteredRows(
    await previewRows(), 'Edited component value', expectedMatchingTuples, 'Reloaded EXPAND label edit with saved Filter',
  );
  const expandEditReloadProtocol = await nativePreviewAfter(expandEditReloadRequestIndex, 'Reloaded EXPAND label edit with saved Filter');
  report.filterIdentityMappings.expandEditReload = verifyProtocolTupleIdentities(
    expandEditReloadProtocol.response, 'Edited component value', expectedMatchingTuples, 'Reloaded EXPAND label edit with saved Filter',
  );
  assert.deepEqual(report.filterIdentityMappings.expandEditReload, matchingIdentityMapping,
    'Reload after EXPAND label edit must preserve exact filtered row identities');
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().construction.steps.find((step) => step.id === filterStepId)?.operation.filter.columnId,
    appliedStep.operation.expand.outputColumnId);
  await saveDOM('expand-filter-label-edited-reloaded');
  recordAssertion('upstream EXPAND presentation edit Cancel and Apply/reload preserve Filter binding and exact rows', {
    outputColumnId: appliedStep.operation.expand.outputColumnId,
    filter: filterAfterExpandEdit.operation.filter,
    canceled: {
      construction: beforeExpandEditConstruction,
      proposalPreview: report.expandEditCanceledProposalPreview,
      preview: report.expandEditCanceledPreview,
      identities: report.filterIdentityMappings.expandEditCanceled,
      reopenedDefaults: report.expandEditCanceledDefaults,
    },
    proposal: report.expandEditedFilterProposal,
    applied: report.expandEditAppliedPreview,
    reloaded: report.expandEditReloadPreview,
    identities: [editedFilterMapping, report.filterIdentityMappings.expandEditApplied, report.filterIdentityMappings.expandEditReload],
  });

  const beforeRemoval = (await api(`${base}/builder`)).body;
  const beforeRemovalDocument = structuredClone(document(beforeRemoval));
  const beforeRemovalConstruction = structuredClone(beforeRemovalDocument.construction);
  const beforeRemovalPreviewState = await captureSavedPreviewState(beforeRemoval);
  const removeExpand = async () => {
    await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
    await waitForBrowser(page, { kind: 'enabled', selector: `[data-testid="construction-remove-step-${appliedStep.id}"]` }, 5000);
    const startedAt = Date.now();
    await click(page, `[data-testid="construction-remove-step-${appliedStep.id}"]`);
    await fastWait(startedAt, { kind: 'status-in', selector: '[data-testid="construction-proposal-panel"]', statuses: ['ready'] }, 'EXPAND removal cascade proposal');
    const durationMs = Date.now() - startedAt;
    report.workflowTimings.push({ name: 'native-EXPAND-remove-with-dependent-FILTER-to-preview', durationMs, limitMs: 5000 });
    assert(durationMs <= 5000, `EXPAND removal with dependent FILTER took ${durationMs}ms`);
    const warning = await inspectPage(page, `return [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map(node=>({testId:node.dataset.testid,text:node.innerText.trim()}));`);
    assert(warning.some((item) => item.testId === `construction-removal-step-${filterStepId}`),
      `EXPAND removal warning must name dependent Filter step ${filterStepId}: ${JSON.stringify(warning)}`);
    assert(warning.some((item) => /filter/i.test(item.text)), `EXPAND removal warning must visibly name Filter: ${JSON.stringify(warning)}`);
    const request = report.browserRequests.findLast((entry) =>
      entry.path.endsWith('/construction-proposals') && entry.body?.removeStepIds?.includes(appliedStep.id));
    assert(request, 'Native upstream removal must propose deletion of the saved EXPAND step');
    const response = await waitForCapturedResponse(request);
    assertExpandFilterRemovalProposal(request, response, {
      outputId: beforeRemovalDocument.output.id,
      snapshotToken: beforeRemoval.catalog?.snapshotToken,
      draftVersion: beforeRemoval.draftVersion,
      draftDigest: beforeRemoval.draftDigest,
      expandStepId: appliedStep.id,
      filterStepId,
      sourceRowCount: sourceRecords.length,
    });
    report.expandRemovalProposal = { warning, request: request.body, response };
  };
  await removeExpand();
  const removalPreview = await previewRows();
  report.expandRemovalCanceledProposal = await verifyOriginalRows(removalPreview, 'Canceled EXPAND cascade removal proposal');
  await saveDOM('expand-filter-cascade-removal-proposal');
  const removalCancelRequestIndex = report.browserRequests.length;
  await measure('native-EXPAND-cascade-removal-cancel', async (startedAt) => {
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, rowsReady(expectedMatchingTuples.length),
      { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 2 },
    ] }, 'Canceled EXPAND cascade preserves both operations');
  });
  builder = (await api(`${base}/builder`)).body;
  assert.deepEqual(document().construction, beforeRemovalConstruction, 'Cancel must preserve both saved operations exactly');
  report.expandRemovalCanceledPreview = verifyFilteredRows(
    await previewRows(), 'Edited component value', expectedMatchingTuples, 'Canceled EXPAND cascade removal table',
  );
  const removalCancelProtocol = await nativePreviewAfterCancel(
    removalCancelRequestIndex, 'Canceled EXPAND cascade removal', beforeRemovalPreviewState,
  );
  report.filterIdentityMappings.removalCancel = verifyProtocolTupleIdentities(
    removalCancelProtocol.response, 'Edited component value', expectedMatchingTuples, 'Canceled EXPAND cascade removal',
  );
  assert.deepEqual(report.filterIdentityMappings.removalCancel, matchingIdentityMapping,
    'Canceling EXPAND cascade must preserve exact filtered row identities');
  assert.deepEqual(document().construction.steps.map((step) => step.id), beforeRemovalConstruction.steps.map((step) => step.id));
  await saveDOM('expand-filter-cascade-removal-canceled');
  recordAssertion('upstream EXPAND removal warning names Filter and Cancel preserves both operations and exact rows', {
    warning: report.expandRemovalProposal.warning,
    construction: document().construction,
    proposalRestoration: report.expandRemovalCanceledProposal,
    canceled: report.expandRemovalCanceledPreview,
    identities: report.filterIdentityMappings.removalCancel,
  });

  await removeExpand();
  const cascadePreviewRequestIndex = report.browserRequests.length;
  await measure('native-EXPAND-cascade-removal-apply', async (startedAt) => {
    await click(page, '[data-testid="construction-apply-proposal"]');
    await fastWait(startedAt, { kind: 'all', conditions: [
      { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, rowsReady(sourceRecords.length),
      { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 0 },
    ] }, 'Applied EXPAND and Filter cascade removal');
    const cascadeApplyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
    assert(cascadeApplyRequest, 'Confirming cascade removal must use the native construction command');
    const settled = await waitForAppliedSourceCapabilities(requestMonitor, {
      applyRequest: cascadeApplyRequest, fromIndex: cascadePreviewRequestIndex, deadlineAt: startedAt + 5000,
      path: `${base}/construction-capabilities`, outputId,
    });
    cascadeAppliedSourceIdentity = settled.expected;
    report.cascadeApplyCapabilities = {
      requestId: settled.capabilities.requestId, browserRequestId: settled.capabilities.browserRequestId,
      status: settled.capabilities.status, completedAt: settled.capabilities.completedAt,
      outputId: settled.expected.outputId, stageId: settled.expected.stageId,
      draftVersion: settled.expected.draftVersion, draftDigestMatched: true, snapshotMatched: true,
    };
  });
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().rows, originalRows);
  assert.equal(document().construction?.steps?.length ?? 0, 0, 'Cascade removal must remove EXPAND and dependent Filter');
  assert.deepEqual(document().columns, originalColumns, 'Cascade removal must restore the exact original source columns');
  assert.deepEqual(document().population, originalPopulation, 'Cascade removal must restore the original scoped source membership');
  assert.equal(document().population?.selectionRevisionId, report.seed.selectionRevisionId,
    'Cascade removal must preserve the exact scoped selection revision');
  report.cascadeRemovedPreview = await verifyOriginalRows(await previewRows(), 'Applied EXPAND cascade removal');
  const cascadeApplyProtocol = await nativePreviewAfter(cascadePreviewRequestIndex, 'Applied EXPAND cascade removal');
  const restoredProtocol = protocolPreview(cascadeApplyProtocol.response);
  assert.equal(restoredProtocol.rows.length, sourceRecords.length, 'Cascade removal apply must restore exact source row count');
  const restoredIDColumn = restoredProtocol.columns.find((column) => column.label === 'Observation ID')?.column;
  assert(restoredIDColumn, 'Cascade removal apply protocol must retain the Observation ID source column');
  assert.deepEqual(restoredProtocol.rows.map((row) => row[restoredIDColumn]).sort(),
    report.oracle.selected.map((resource) => resource.id).sort(), 'Cascade removal apply must restore exact selected Observation identities');
  await saveDOM('expand-filter-cascade-removed-records');

  const cascadeReloadRequestIndex = report.browserRequests.length;
  await openTable(sourceRecords.length, 'reload-after-expand-filter-cascade-removal', async (startedAt) => {
    const capabilities = await waitForSourceCapabilities(requestMonitor, {
      fromIndex: cascadeReloadRequestIndex, deadlineAt: startedAt + 5000,
      path: `${base}/construction-capabilities`, expected: cascadeAppliedSourceIdentity,
    });
    report.cascadeReloadCapabilities = {
      requestId: capabilities.requestId, browserRequestId: capabilities.browserRequestId,
      status: capabilities.status, completedAt: capabilities.completedAt,
      outputId: cascadeAppliedSourceIdentity.outputId, stageId: cascadeAppliedSourceIdentity.stageId,
      draftVersion: cascadeAppliedSourceIdentity.draftVersion, draftDigestMatched: true, snapshotMatched: true,
    };
  });
  report.cascadeReloadPreview = await verifyOriginalRows(await previewRows(), 'Reload after EXPAND cascade removal');
  builder = (await api(`${base}/builder`)).body;
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().rows, originalRows);
  assert.equal(document().construction?.steps?.length ?? 0, 0);
  assert.deepEqual(document().columns, originalColumns, 'Reload after cascade must restore exact source columns');
  assert.deepEqual(document().population, originalPopulation, 'Reload after cascade must restore exact scoped source membership');
  const cascadeReloadProtocol = await nativePreviewAfter(cascadeReloadRequestIndex, 'Reloaded EXPAND cascade removal');
  const reloadedProtocol = protocolPreview(cascadeReloadProtocol.response);
  const reloadedIDColumn = reloadedProtocol.columns.find((column) => column.label === 'Observation ID')?.column;
  assert(reloadedIDColumn, 'Reloaded source protocol must retain the Observation ID column');
  assert.deepEqual(reloadedProtocol.rows.map((row) => row[reloadedIDColumn]).sort(),
    report.oracle.selected.map((resource) => resource.id).sort(), 'Reload after cascade must restore exact scoped Observation membership');
  await saveDOM('expand-filter-cascade-removal-reloaded');
  cda.check('persistence', 'confirmed EXPAND cascade removal and reload restore exact source RECORDS rows and columns', true, {
    rows: document().rows,
    columns: document().columns,
    population: document().population,
    applied: report.cascadeRemovedPreview,
    reloaded: report.cascadeReloadPreview,
  });
};

const finish = async () => {
  report.finished = new Date().toISOString();
  report.status = report.failures.length ? 'failed'
    : report.gaps.length ? 'partial'
      : report.assertions.length && report.assertions.every(assertion => assertion.status === 'passed') ? 'passed' : 'untested';
  await cda.attachReport(composedFilter ? 'authored-expand-filter-evidence.json' : 'authored-expand-evidence.json', report);
};

try {
  sourceRecords = boundedRawOracle();
  if (composedFilter) {
    filterWitness = strictSubsetFilterOracle(sourceRecords);
    if (filterWitness === null) {
      fixtureGapReason = 'The bounded raw Observation fixture has no nonempty strict-subset exact-value witness for an EXPAND item Filter.';
      report.gaps.push({ assertion: 'bounded raw CDA oracle contains a nonempty strict-subset exact-value predicate over expanded items', status: 'untested', reason: fixtureGapReason });
    } else {
      assert.equal(typeof filterWitness.predicateValue, 'string', 'Strict-subset Filter witness must use an exact string value');
      assert(filterWitness.predicateValue.length > 0, 'Strict-subset Filter witness value must be nonempty');
      assert(Array.isArray(filterWitness.allTuples) && Array.isArray(filterWitness.matchingTuples),
        'Strict-subset Filter witness must expose all and matching raw tuples');
      assert.deepEqual(sortedTuples(filterWitness.allTuples), expectedExpandTuples(),
        'Strict-subset Filter oracle must describe the exact bounded EXPAND source tuples');
      assert(filterWitness.matchingTuples.length > 0, 'Strict-subset Filter witness must match at least one tuple');
      assert(filterWitness.matchingTuples.length < filterWitness.allTuples.length,
        'Strict-subset Filter witness must exclude at least one expanded tuple');
      for (const tuple of filterWitness.allTuples) {
        assert.equal(tuple.length, 3, 'Strict-subset Filter oracle tuples must be [ObservationID, ordinal, valueString]');
        assert.equal(typeof tuple[0], 'string', 'Strict-subset Filter tuple Observation ID must be a string');
        assert(Number.isInteger(tuple[1]) && tuple[1] >= 0, 'Strict-subset Filter tuple ordinal must be a non-negative integer');
        assert.equal(typeof tuple[2], 'string', 'Strict-subset Filter tuple item value must be a string');
      }
      const allTupleKeys = new Set(filterWitness.allTuples.map((tuple) => JSON.stringify(tuple)));
      assert.equal(allTupleKeys.size, filterWitness.allTuples.length, 'Strict-subset Filter oracle must not duplicate raw tuples');
      const exactMatches = filterWitness.allTuples.filter((tuple) => tuple[2] === filterWitness.predicateValue);
      assert.deepEqual(sortedTuples(filterWitness.matchingTuples), sortedTuples(exactMatches),
        'Strict-subset Filter oracle must include every and only tuple matching its exact value predicate');
      for (const tuple of filterWitness.matchingTuples) {
        assert.equal(tuple[2], filterWitness.predicateValue, 'Strict-subset Filter oracle must match the exact equality value');
        assert(allTupleKeys.has(JSON.stringify(tuple)), 'Strict-subset Filter match must be an exact member of the raw expanded tuples');
      }
      report.oracle.strictSubsetFilter = structuredClone(filterWitness);
    }
  }
  const rawFixtureGap = sourceRecords.length === 0 || report.oracle.expectedRows.length < 2;
  if (rawFixtureGap) {
    const reason = 'The bounded 1000-record scan found no selection of up to three Observations and six components with distinct non-empty valueString items.';
    fixtureGapReason ??= reason;
    report.gaps.push({ assertion: 'bounded Observation.component[].valueString raw oracle', status: 'untested', reason });
  }
  if (!rawFixtureGap && (!composedFilter || filterWitness !== null)) {
    assert(sourceRecords.every((resource) => resource.generation === values.generation && resource.resourceType === 'Observation'));
    recordAssertion(composedFilter
      ? 'bounded raw CDA oracle contains a nonempty strict-subset exact-value predicate over expanded items'
      : 'bounded independent raw oracle selected at most three roots and six distinct list items', {
      selectedRoots: sourceRecords.length, expectedItems: report.oracle.expectedRows.length,
      selected: report.oracle.selected.map((resource) => ({ id: resource.id, values: resource.componentValues.map((item) => item.value) })),
      ...(composedFilter ? { filterWitness } : {}),
    });

    await api(projectRoot, { name: explorer, title: composedFilter ? 'CDA authored EXPAND FILTER QA' : 'CDA authored EXPAND QA' });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(builder.catalog.generation, values.generation, 'Fresh QA Explorer must use the requested CDA generation');
    const observationNode = builder.catalog.nodes.find((node) => node.resourceType === 'Observation');
    assert(observationNode, 'CDA catalog has no Observation root');
    const tableTitle = composedFilter ? 'Authored item filtering' : 'Authored list expansion';
    await command([{ type: 'CREATE_TABLE', title: tableTitle, rootNodeId: observationNode.nodeId }]);
    const createdDocument = builder.workspace.documents.find((doc) => doc.output.title === tableTitle);
    assert(createdDocument, 'API seed did not create the isolated QA table');
    outputId = createdDocument.output.id;

    const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
    const valueCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'component[].valueString');
    assert(idCandidate, 'Observation ID field is unavailable');
    assert(valueCandidate, 'Observation.component[].valueString list field is unavailable');
    await command([
      { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' },
      { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: 'Component Value String' },
    ]);

    const selection = (await api(`${projectRoot}/${encodeURIComponent(explorer)}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `cda-authored-expand-${explorer}`,
      source: { kind: 'resources', resources: { refs: sourceRecords.map((resource) => ({
        project: values.project, generation: resource.generation, resourceType: 'Observation', id: resource.id,
      })) } },
    })).body;
    const routes = (await api(`${base}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
    })).body;
    const rootRoute = routes.choices.find((choice) => choice.route.length === 0);
    assert(rootRoute, 'Scoped source selection has no direct Observation population route');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: rootRoute.routeChoiceId }]);

    const saved = document();
    assert(saved, 'Seeded QA document disappeared');
    assert.equal(saved.rows.kind, 'RECORDS', 'Source baseline must keep one row per Observation');
    assert.equal(saved.columns.length, 2, 'Source baseline must contain only the ID and list projections');
    idColumn = saved.columns.find((column) => column.source?.field?.path === 'id');
    listColumn = saved.columns.find((column) => column.source?.field?.path === 'component[].valueString');
    assert(idColumn && listColumn, 'Source baseline must retain both Observation field bindings');
    assert.equal(idColumn.occurrenceId, 'base');
    assert.equal(listColumn.occurrenceId, 'base');
    assert.equal(listColumn.source.field.projectionMode, 'ALL', 'EXPAND input must be a projected list');
    let listColumnId = listColumn.column ?? listColumn.id;
    const idColumnId = idColumn.column ?? idColumn.id;
    assert.equal(typeof listColumnId, 'string', 'Projected list column must expose its stable column ID');
    assert.equal(typeof idColumnId, 'string', 'Observation identity column must expose its stable column ID');
    originalRows = structuredClone(saved.rows);
    originalColumns = structuredClone(saved.columns);
    originalPopulation = structuredClone(saved.population);
    report.seed = { outputId, selectionRevisionId: selection.id, idColumn, listColumn, rows: saved.rows, construction: saved.construction ?? { version: 1, steps: [] } };
    assert.equal(saved.construction?.steps?.length ?? 0, 0, 'QA table must start without construction operations');
    if (!composedFilter) {
      recordAssertion('fresh QA document binds a scalar Observation ID and a base-occurrence ALL list while rows remain RECORDS', report.seed);
    }

    monitorBrowser();
    await openTable(sourceRecords.length, 'fresh-owned-explorer-load-to-render');
    const initialPreview = await previewRows();
    report.initialSource = await verifyOriginalRows(initialPreview, 'Initial RECORDS preview');
    await saveDOM('initial-records-with-projected-list');
    recordAssertion(composedFilter
      ? 'initial source preview preserves exact scoped Observation identities, list values, and RECORDS row definition'
      : 'initial table renders the exact selected Observation IDs and component list values', report.initialSource);

    await openExpandEditor();
    const defaultExpandReadyAt = Date.now();
    if (!composedFilter) await click(page, '[data-testid="construction-reshape-choice-expand"]');
    await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 5000);
    const initialFieldOptions = await inspectPage(page, `return [...document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]').options].map(option=>({value:option.value,label:option.text,disabled:option.disabled}));`);
    const initialListOption = initialFieldOptions.find((option) => option.label.startsWith('Component Value String') && !option.disabled);
    assert(initialListOption, `Native EXPAND did not offer the projected component list: ${JSON.stringify(initialFieldOptions)}`);
    // The editor binds the compiler-owned source projection, not the authored UI column ID.
    listColumnId = initialListOption.value;
    await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]', initialListOption.value);
    await click(page, '[data-testid="construction-reshape-expand-advanced"] summary');
    await setInput('Expanded item name', 'component_value_item');
    await setInput('Expanded item label', 'Expanded component value');
    await click(page, 'input[aria-label="Include item position"]');
    await setInput('Position column name', 'component_value_position');
    await setInput('Position column label', 'Component position');
    await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Empty list policy"]', 'PRESERVE_PARENT');
    const initialProposalPreview = await waitForProposal(defaultExpandReadyAt, 'Expanded component value');
    const initialDurationMs = Date.now() - defaultExpandReadyAt;
    report.workflowTimings.push({ name: 'native-EXPAND-configure-to-preview', durationMs: initialDurationMs, limitMs: 5000 });
    assert(initialDurationMs <= 5000, `EXPAND configure-to-preview took ${initialDurationMs}ms`);
    report.expansionPreview = verifyExpandedRows(initialProposalPreview, 'Expanded component value', 'Initial native EXPAND preview');
    const firstProposal = await assertExpandRequest('Expanded component value', listColumnId);
    report.nativeProposal = { request: firstProposal.request, step: firstProposal.step, response: firstProposal.response };
    await saveDOM('expand-proposal-before-cancel');
    recordAssertion(composedFilter
      ? 'native EXPAND proposal exposes the projected list and exact item/value/ordinal tuples'
      : 'native EXPAND preview emits the projected list input, item output, zero-based ordinal, empty policy, and exact raw tuples', {
      operation: firstProposal.step.operation, output: firstProposal.step.outputs, preview: report.expansionPreview,
    });

    const beforeCancel = (await api(`${base}/builder`)).body;
    await measure('native-EXPAND-cancel-restoration', async (startedAt) => {
      await click(page, '[data-testid="construction-cancel-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' }, rowsReady(sourceRecords.length),
      ] }, 'Canceled EXPAND source-row restoration');
    });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(builder.draftDigest, beforeCancel.draftDigest, 'Cancel must leave the saved construction unchanged');
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows, 'Cancel must preserve the original RECORDS row definition');
    assert.equal(document().construction?.steps?.length ?? 0, 0, 'Cancel must not persist EXPAND');
    report.canceledSource = await verifyOriginalRows(await previewRows(), 'Canceled EXPAND source preview');
    await saveDOM('expand-canceled-records-restored');
    recordAssertion(composedFilter
      ? 'Cancel restores exact source RECORDS rows without persisting EXPAND'
      : 'Cancel restores the exact source records and leaves RECORDS unchanged', { draftDigest: builder.draftDigest, preview: report.canceledSource });

    const configuredPreview = await configureExpand('Expanded component value');
    report.expansionPreview = verifyExpandedRows(configuredPreview, 'Expanded component value', 'Confirmed native EXPAND preview');
    const configuredProposal = await assertExpandRequest('Expanded component value', listColumnId);
    const authoredIdentityMapping = verifyProtocolExpandIdentities(
      configuredProposal.response, 'Expanded component value', 'Authored EXPAND proposal',
    );
    report.rowIdentityMappings = { proposal: authoredIdentityMapping };
    if (!composedFilter) {
      recordAssertion('native proposal protocol exposes unique __loom_row_id values keyed by Observation ID and ordinal', {
        rowCount: authoredIdentityMapping.length, mapping: authoredIdentityMapping,
      });
    }
    await saveDOM('expand-confirmed-preview');
    const applyPreviewRequestIndex = report.browserRequests.length;
    await measure('native-EXPAND-apply', async (startedAt) => {
      await click(page, '[data-testid="construction-apply-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(report.oracle.expectedRows.length),
        { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 1 },
      ] }, 'Applied native EXPAND result');
    });
    builder = (await api(`${base}/builder`)).body;
    appliedStep = document().construction.steps.find((step) => step.operation.kind === 'EXPAND');
    assert(appliedStep, 'Apply must persist the native EXPAND operation');
    assert.equal(document().rows.kind, 'RECORDS', 'Authored EXPAND must leave row-definition RECORDS intact');
    assert.deepEqual(document().rows, originalRows, 'Authored EXPAND must preserve the source row definition exactly');
    assert.equal(document().construction.steps.length, 1, 'Only the single authored EXPAND belongs in construction history');
    assert.equal(appliedStep.operation.expand.inputColumnId, listColumnId);
    assert.equal(appliedStep.operation.expand.emptyPolicy, 'PRESERVE_PARENT');
    assert(appliedStep.operation.expand.ordinalColumnId);
    const appliedOutput = appliedStep.outputs.find((column) => column.id === appliedStep.operation.expand.outputColumnId);
    assert.equal(appliedOutput?.name, 'component_value_item');
    assert.equal(appliedOutput?.label, 'Expanded component value');
    assert(appliedStep.outputs.some((column) => column.name === idColumnId && column.label === 'Observation ID'), 'Expanded rows must retain the authored Observation ID through its source projection');
    assert(appliedStep.outputs.some((column) => column.id === appliedStep.operation.expand.ordinalColumnId));
    assert(!appliedStep.outputs.some((column) => column.id === listColumnId), 'EXPAND must replace the list with its item output at this stage');
    report.applied = { step: appliedStep, rows: document().rows, columns: document().columns };
    report.appliedPreview = verifyExpandedRows(await previewRows(), 'Expanded component value', 'Applied native EXPAND table');
    const appliedProtocol = await nativePreviewAfter(applyPreviewRequestIndex, 'Applied native EXPAND');
    report.rowIdentityMappings.applied = verifyProtocolExpandIdentities(
      appliedProtocol.response, 'Expanded component value', 'Applied native EXPAND',
    );
    assert.deepEqual(report.rowIdentityMappings.applied, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive Apply for every Observation/ordinal tuple');
    const applyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
    assert(applyRequest, 'Native Apply must use APPLY_CONSTRUCTION_PROPOSAL');
    await waitForCapturedResponse(applyRequest);
    assert.equal(configuredProposal.step.id, appliedStep.id, 'Applied operation identity must match the authored proposal');
    await saveDOM('expand-applied');
    if (!composedFilter) {
      recordAssertion('Apply persists EXPAND while row definition stays RECORDS and IDs, values, ordinals, and bindings match raw CDA', {
        step: appliedStep, preview: report.appliedPreview,
      });
    }

    const appliedReloadRequestIndex = report.browserRequests.length;
    await openTable(report.oracle.expectedRows.length, 'reload-applied-expand');
    report.reloadPreview = verifyExpandedRows(await previewRows(), 'Expanded component value', 'Reloaded EXPAND table');
    const appliedReloadProtocol = await nativePreviewAfter(appliedReloadRequestIndex, 'Reloaded native EXPAND');
    report.rowIdentityMappings.appliedReload = verifyProtocolExpandIdentities(
      appliedReloadProtocol.response, 'Expanded component value', 'Reloaded native EXPAND',
    );
    assert.deepEqual(report.rowIdentityMappings.appliedReload, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive reload for every Observation/ordinal tuple');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    assert.equal(document().construction.steps.find((step) => step.id === appliedStep.id)?.operation.kind, 'EXPAND');
    await saveDOM('expand-reloaded');
    if (composedFilter) {
      recordAssertion('EXPAND Apply and reload preserve exact item tuples and stable row identities', {
        applied: report.appliedPreview, reloaded: report.reloadPreview,
        identities: [authoredIdentityMapping, report.rowIdentityMappings.applied, report.rowIdentityMappings.appliedReload],
      });
    } else {
      recordAssertion('Reload preserves native EXPAND output and unchanged RECORDS row definition', { preview: report.reloadPreview, stepId: appliedStep.id });
    }

    if (composedFilter) {
      await composedFilterLifecycle(authoredIdentityMapping, filterWitness);
    } else {
    await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
    await waitForBrowser(page, { kind: 'enabled', selector: `[data-testid="construction-edit-step-${appliedStep.id}"]` }, 5000);
    await click(page, `[data-testid="construction-edit-step-${appliedStep.id}"]`);
    await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 5000);
    const advanced = '[data-testid="construction-reshape-expand-advanced"]';
    const advancedOpen = await inspectPage(page, `return document.querySelector(${JSON.stringify(advanced)})?.open;`);
    if (!advancedOpen) await click(page, `${advanced} summary`);
    const reopened = await inspectPage(page, `return {
      field:document.querySelector('select[aria-label="Repeated field"]')?.value,
      name:document.querySelector('input[aria-label="Expanded item name"]')?.value,
      label:document.querySelector('input[aria-label="Expanded item label"]')?.value,
      ordinal:document.querySelector('input[aria-label="Include item position"]')?.checked,
      positionName:document.querySelector('input[aria-label="Position column name"]')?.value,
      positionLabel:document.querySelector('input[aria-label="Position column label"]')?.value,
      emptyPolicy:document.querySelector('select[aria-label="Empty list policy"]')?.value,
    };`);
    assert.equal(reopened.field, appliedStep.operation.expand.inputColumnId, 'Edit must reopen the same list input binding');
    assert.equal(reopened.name, 'component_value_item');
    assert.equal(reopened.label, 'Expanded component value');
    assert.equal(reopened.ordinal, true);
    assert.equal(reopened.positionName, 'component_value_position');
    assert.equal(reopened.positionLabel, 'Component position');
    assert.equal(reopened.emptyPolicy, 'PRESERVE_PARENT');
    recordAssertion('native Edit reopens the saved input, output identity, ordinal, and empty policy', reopened);

    const editStartedAt = Date.now();
    await setInput('Expanded item label', 'Edited component value');
    const editedPreview = await waitForProposal(editStartedAt, 'Edited component value');
    const editDurationMs = Date.now() - editStartedAt;
    report.workflowTimings.push({ name: 'native-EXPAND-edit-to-preview', durationMs: editDurationMs, limitMs: 5000 });
    assert(editDurationMs <= 5000, `EXPAND edit-to-preview took ${editDurationMs}ms`);
    report.editedPreview = verifyExpandedRows(editedPreview, 'Edited component value', 'Edited native EXPAND preview');
    const editedProposal = await assertExpandRequest('Edited component value', listColumnId, appliedStep.id);
    assert.equal(editedProposal.step.operation.expand.outputColumnId, appliedStep.operation.expand.outputColumnId, 'Edit must retain item output identity');
    assert.equal(editedProposal.step.operation.expand.ordinalColumnId, appliedStep.operation.expand.ordinalColumnId, 'Edit must retain ordinal output identity');
    report.rowIdentityMappings.editedProposal = verifyProtocolExpandIdentities(
      editedProposal.response, 'Edited component value', 'Edited native EXPAND proposal',
    );
    assert.deepEqual(report.rowIdentityMappings.editedProposal, authoredIdentityMapping,
      'Changing only the output label must preserve native __loom_row_id by Observation/ordinal tuple');
    await saveDOM('expand-edit-preview');
    const editApplyPreviewRequestIndex = report.browserRequests.length;
    await measure('native-EXPAND-edit-apply', async (startedAt) => {
      await click(page, '[data-testid="construction-apply-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(report.oracle.expectedRows.length),
        { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 1 },
      ] }, 'Applied native EXPAND edit');
    });
    builder = (await api(`${base}/builder`)).body;
    appliedStep = document().construction.steps.find((step) => step.id === appliedStep.id);
    assert(appliedStep, 'Editing must preserve the operation ID');
    assert.equal(appliedStep.operation.expand.inputColumnId, listColumnId);
    assert.equal(appliedStep.operation.expand.outputColumnId, editedProposal.step.operation.expand.outputColumnId);
    assert.equal(appliedStep.operation.expand.ordinalColumnId, editedProposal.step.operation.expand.ordinalColumnId);
    assert.equal(appliedStep.outputs.find((column) => column.id === appliedStep.operation.expand.outputColumnId)?.label, 'Edited component value');
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    report.editAppliedPreview = verifyExpandedRows(await previewRows(), 'Edited component value', 'Applied edited EXPAND table');
    const editAppliedProtocol = await nativePreviewAfter(editApplyPreviewRequestIndex, 'Applied edited EXPAND');
    report.rowIdentityMappings.editApplied = verifyProtocolExpandIdentities(
      editAppliedProtocol.response, 'Edited component value', 'Applied edited EXPAND',
    );
    assert.deepEqual(report.rowIdentityMappings.editApplied, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive label edit Apply for every Observation/ordinal tuple');
    await saveDOM('expand-edit-applied');
    recordAssertion('Editing changes the item label while preserving the operation and output identities', { step: appliedStep, preview: report.editAppliedPreview });

    const editedReloadRequestIndex = report.browserRequests.length;
    await openTable(report.oracle.expectedRows.length, 'reload-edited-expand');
    report.editReloadPreview = verifyExpandedRows(await previewRows(), 'Edited component value', 'Reloaded edited EXPAND table');
    const editedReloadProtocol = await nativePreviewAfter(editedReloadRequestIndex, 'Reloaded edited EXPAND');
    report.rowIdentityMappings.editReload = verifyProtocolExpandIdentities(
      editedReloadProtocol.response, 'Edited component value', 'Reloaded edited EXPAND',
    );
    assert.deepEqual(report.rowIdentityMappings.editReload, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive edited-table reload for every Observation/ordinal tuple');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    await saveDOM('expand-edit-reloaded');
    recordAssertion('Edited EXPAND output and unchanged RECORDS definition persist after reload', { preview: report.editReloadPreview });

    await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
    await waitForBrowser(page, { kind: 'enabled', selector: `[data-testid="construction-remove-step-${appliedStep.id}"]` }, 5000);
    const removeStartedAt = Date.now();
    await click(page, `[data-testid="construction-remove-step-${appliedStep.id}"]`);
    await fastWait(removeStartedAt, { kind: 'status-in', selector: '[data-testid="construction-proposal-panel"]', statuses: ['ready'] }, 'EXPAND removal proposal');
    const removalDurationMs = Date.now() - removeStartedAt;
    report.workflowTimings.push({ name: 'native-EXPAND-remove-to-preview', durationMs: removalDurationMs, limitMs: 5000 });
    assert(removalDurationMs <= 5000, `EXPAND removal-to-preview took ${removalDurationMs}ms`);
    const removalPreview = await previewRows();
    report.removalPreview = await verifyOriginalRows(removalPreview, 'EXPAND removal proposal');
    const removeRequest = latestExpandProposal('Edited component value');
    const removalRequest = report.browserRequests.findLast((entry) =>
      entry.path.endsWith('/construction-proposals') && entry.body?.removeStepIds?.includes(appliedStep.id));
    assert(removalRequest, 'Native Remove must propose deletion of the saved EXPAND step');
    assert(!removalRequest.body.candidateConstruction.steps.some((step) => step.id === appliedStep.id), 'Removal proposal must omit the EXPAND step');
    await waitForCapturedResponse(removalRequest);
    assert(removeRequest, 'The applied and edited EXPAND proposal evidence should remain available');
    await saveDOM('expand-remove-preview-restored-records');
    await measure('native-EXPAND-remove-apply-restoration', async (startedAt) => {
      await click(page, '[data-testid="construction-apply-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(sourceRecords.length),
        { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 0 },
      ] }, 'Applied EXPAND removal restoration');
    });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    assert.equal(document().construction?.steps?.length ?? 0, 0, 'Removing EXPAND must restore the original construction');
    assert.equal(document().columns.length, 2);
    assert(document().columns.some((column) => column.source?.field?.path === 'id' && column.occurrenceId === 'base'));
    assert(document().columns.some((column) => column.source?.field?.path === 'component[].valueString' && column.occurrenceId === 'base' && column.source.field.projectionMode === 'ALL'));
    report.removedPreview = await verifyOriginalRows(await previewRows(), 'Removed EXPAND table');
    const removeApplyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
    assert(removeApplyRequest, 'Applying removal must use the native construction command');
    await waitForCapturedResponse(removeApplyRequest);
    await saveDOM('expand-removed-source-records-restored');
    recordAssertion('Remove restores the exact source IDs, list values, bindings, and RECORDS row definition', {
      rows: document().rows, columns: document().columns, preview: report.removedPreview,
    });

    await openTable(sourceRecords.length, 'reload-after-expand-removal');
    report.removalReloadPreview = await verifyOriginalRows(await previewRows(), 'Reload after EXPAND removal');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.equal(document().construction?.steps?.length ?? 0, 0);
    assert.deepEqual(document().rows, originalRows);
    await saveDOM('expand-removal-reloaded');
    recordAssertion('Reload after removal preserves source-record restoration', { preview: report.removalReloadPreview });
    }

    await requestMonitor.flush();
    assert.deepEqual(report.browserErrors.protocol, [], 'Browser reported a failed network protocol request');
    assert.deepEqual(report.browserErrors.exceptions, [], 'Browser raised JavaScript exceptions');
    assert.deepEqual(report.browserErrors.console, [], 'Browser logged console errors');
    assert.deepEqual(report.browserErrors.modules, [], 'Browser failed to load a module');
    assert.deepEqual(report.browserErrors.http, [], 'Browser received unexpected HTTP errors');
  assert.deepEqual(report.errors, [], 'Playwright request capture reported an owned API, runtime, or console failure');
    assert(!report.browserRequests.some((entry) => `${entry.origin}${entry.path}`.includes(protectedExplorer)), 'Browser must not select or mutate the protected Explorer');
    assert(!report.requests.some((entry) => entry.path.includes(protectedExplorer)), 'API setup must not select or mutate the protected Explorer');
    assert(!report.browserRequests.some((entry) => entry.path.endsWith('/row-definition-proposals')),
      'Native EXPAND must use construction proposals and leave row definition untouched');
    if (composedFilter) {
      const workflowWithinBudget = report.workflowTimings.length > 0 &&
        report.workflowTimings.every((timing) => Number.isFinite(timing.durationMs) && timing.durationMs <= 5000);
      const pendingOwnedRequests = report.nativeRequests.filter((entry) => !Number.isFinite(entry.completedAt));
      const terminalCensusPassed = pendingOwnedRequests.length === 0;
      cda.check('performance',
        'all native actions and lifecycle checkpoints complete within five seconds with no unexpected browser or network errors',
        workflowWithinBudget && terminalCensusPassed,
        {
          workflowCheckpoints: report.workflowTimings.map(({ name, durationMs }) => ({ name, durationMs })),
          maximumDurationMs: Math.max(0, ...report.workflowTimings.map((timing) => timing.durationMs)),
          terminalRequestCensus: { captured: report.nativeRequests.length, terminal: report.nativeRequests.length - pendingOwnedRequests.length,
            pending: pendingOwnedRequests.map(({ requestId, browserRequestId, method, path, status, startedAt, failure }) =>
              ({ requestId, browserRequestId, method, path, status, startedAt, failure })) },
          browserErrors: report.browserErrors, requestErrors: report.errors,
        });
    } else {
      recordAssertion('browser protocol, runtime, module, and console capture is clean; incidental favicon errors are retained separately', report.browserErrors);
    }
  }
  if (fixtureGapReason) return { skipReason: fixtureGapReason };
  return { status: 'passed' };
} catch (error) {
  report.status = 'failed';
  report.failures.push({ error: String(error.stack ?? error), phase: report.assertions.length });
  try { report.failureDOM = await domText(); } catch { /* Page may not have opened. */ }
  try { report.failureBuilder = (await api(`${base}/builder`)).body; } catch (readError) { report.builderReadError = String(readError); }
  throw error;
} finally {
  await requestMonitor.flush();
  await finish();
}
}

export async function authoredExpandFilterWorkflow({ page, cda, expect }) {
  return authoredExpandWorkflow({ page, cda, expect, mode: 'expand-filter' });
}
