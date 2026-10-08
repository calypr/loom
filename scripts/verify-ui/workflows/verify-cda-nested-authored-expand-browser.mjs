import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { isDeepStrictEqual } from 'node:util';
import { requireUnique } from '../helpers/playwright-actions.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';
import { buildArangoShellInvocation, buildBoundedArangoQueryScript, summarizeArangoShellResult } from '../helpers/owned-arangosh-command.mjs';
import { waitForAppliedSourceCapabilities, waitForSourceCapabilities } from './verify-cda-authored-expand-browser.mjs';
import { assertVisibleListCell, selectNestedAuthoredExpandWitnesses } from '../helpers/nested-authored-expand-oracle.mjs';
import { selectCanceledSavedPreviewRequest } from '../helpers/saved-preview-binding.mjs';

export async function nestedAuthoredExpandWorkflow({ page, cda }) {
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
Object.assign(report, {
  started: new Date().toISOString(), assertions: [], gaps: [], failures: [], requests: [], workflowTimings: [], cancellationProofs: [],
  browserErrors: { protocol: [], exceptions: [], console: [], modules: [], http: [], incidental: [] },
  evidencePaths: [],
});
report.browserRequests = report.nativeRequests;
assert.equal(values.generation, 'cda-fhir-v1', 'CDA authored EXPAND requires the cda-fhir-v1 fixture generation.');
const witnessModeByCase = {
  'nested-coding-expand-lifecycle': 'multi-coding-component',
  'nested-coding-expand-lifecycle-single-per-component': 'single-coding-per-component',
};
const witnessMode = witnessModeByCase[cda.caseName];
assert(witnessMode, `Unsupported nested authored EXPAND witness case: ${cda.caseName}`);
report.witnessMode = witnessMode;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `cda-nested-authored-expand-${Date.now()}`;
assert.notEqual(explorer, protectedExplorer, 'The run must own a fresh Explorer');
report.target = cda.target;
report.explorer = explorer;
report.target.protectedExplorer = protectedExplorer;
const projectRoot = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const root = `${projectRoot}/${encodeURIComponent(explorer)}`;
const base = `${root}/authoring/v2`;
const labelFieldPath = 'component[].valueString';
const codeFieldPath = 'component[].code.coding[].code';
const labelColumnLabel = 'Component Value String';
const codeColumnLabel = 'Nested Coding Code';
const expandedItemName = 'nested_coding_item';
const expandedItemLabel = 'Expanded nested coding code';
const positionName = 'nested_coding_position';
const positionLabel = 'Nested coding position';
let builder;
let outputId;
const browserPending = new Set();
const requestMonitor = cda.captureRequests(`/api/v1/projects/${encodeURIComponent(values.project)}/explorers/${encodeURIComponent(explorer)}`);

const inspectPage = (_page, body) => page.evaluate(`(()=>{${body}})()`);
const waitForBrowser = (page, condition, timeout = 5000) => waitForCondition(page, condition, Math.min(timeout, 5000));
const resolveActionLocator = async (page, selector, identity = {}) => {
  const candidates = page.locator(selector);
  const { name, includes } = identity;
  if (name === undefined && includes === undefined) return requireUnique(candidates, selector);
  const matches = await candidates.evaluateAll((nodes, wanted) => nodes.flatMap((node, index) => {
    const label = String(node.getAttribute('aria-label') || node.innerText || node.textContent || '')
      .replace(/\\s+/g, ' ').trim();
    const matched = wanted.name !== undefined ? label === wanted.name
      : label.toLocaleLowerCase().includes(wanted.includes.toLocaleLowerCase());
    return matched ? [index] : [];
  }), { name, includes });
  assert.equal(matches.length, 1, `${selector}: expected one matching control, found ${matches.length}`);
  return requireUnique(candidates.nth(matches[0]), `${selector} ${name ?? includes}`);
};
const click = async (page, selector, identity = {}, timeout = 5000) => {
  const label = `Click ${selector} ${identity.name ?? identity.includes ?? ''}`.trim();
  const locator = await resolveActionLocator(page, selector, identity);
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
let appliedStep;

const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });
const responseEvidence = (value) => {
  if (value === undefined) return undefined;
  const text = typeof value === 'string' ? value : String(value);
  try { return JSON.parse(text); } catch { return text.slice(0, 32768); }
};

const api = async (path, body, allowFailure = false) => {
  const requestId = `cda-nested-authored-expand-${randomUUID()}`;
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
const capturePersistenceState = (state, preview, identityMapping = null) => {
  const savedDocument = document(state);
  assert(savedDocument, 'Saved output document is required for persistence evidence');
  return {
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
    outputId,
    document: structuredClone(savedDocument),
    preview: structuredClone(preview),
    identityMapping: identityMapping === null ? null : structuredClone(identityMapping),
  };
};
const persistenceEvidence = (before, after) => ({
  before,
  after,
  unchangedSavedState: isDeepStrictEqual(before, after),
});
const previewTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const rowsReady = count => ({ kind: 'rows', selector: previewTableSelector, count });
const domText = () => inspectPage(page, 'return document.body.innerText;');
const readSavedPreviewBinding = () => inspectPage(page, `const p=document.querySelector('[data-testid="construction-preview"]');return {
  status:p?.dataset.previewStatus??null,
  receiptId:p?.dataset.previewReceiptId??null,
  outputId:p?.dataset.previewOutputId??null,
  draftVersion:p?.dataset.currentDraftVersion??null,
  draftDigest:p?.dataset.currentDraftDigest??null,
};`);
const waitForSavedPreviewDraft = async (startedAt, state, phase) => {
  const timeout = Math.max(100, 5000 - (Date.now() - startedAt));
  await page.waitForFunction((expected) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' &&
      Boolean(preview.dataset.previewReceiptId) &&
      preview.dataset.previewOutputId === expected.outputId &&
      preview.dataset.currentDraftVersion === expected.draftVersion &&
      preview.dataset.currentDraftDigest === expected.draftDigest;
  }, { outputId: state.outputId, draftVersion: String(state.draftVersion), draftDigest: state.draftDigest }, { timeout });
  const binding = await readSavedPreviewBinding();
  assert.equal(binding.status, 'ready', `${phase} must restore a ready saved Preview`);
  return binding;
};

const boundedRawOracle = () => {
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(values.project)} AND r.dataset_generation == ${JSON.stringify(values.generation)} AND r.payload.resourceType == "Observation" FILTER IS_ARRAY(r.payload.component) SORT r.id LIMIT 1000 RETURN {id:r.id,sourceKey:r._key,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,components:r.payload.component}`;
  const script = buildBoundedArangoQueryScript({
    query, maxRuntimeSeconds: 8, memoryLimitBytes: 256 * 1024 * 1024,
  });
  const invocation = buildArangoShellInvocation({ container: values['arango-container'], script, database: 'loom_dev' });
  const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  const processEvidence = summarizeArangoShellResult(result, { project: values.project, generation: values.generation });
  report.oracleProcess = {
    processSucceeded: processEvidence.processSucceeded,
    exitStatus: processEvidence.exitStatus,
    signal: processEvidence.signal,
    stdoutBytes: processEvidence.stdoutBytes,
    stderrBytes: processEvidence.stderrBytes,
    stdoutJsonComplete: processEvidence.stdoutJsonComplete,
    stdoutRowCount: processEvidence.stdoutRowCount,
    ...(processEvidence.processSucceeded ? {} : {
      spawnError: processEvidence.spawnError,
      stderrExcerpt: processEvidence.stderrExcerpt,
    }),
  };
  assert(processEvidence.processSucceeded && processEvidence.stdoutJsonComplete,
    `Bounded raw CDA query failed: ${processEvidence.spawnError ?? processEvidence.stderrExcerpt ?? 'incomplete JSON output'}`);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango oracle returned no JSON array: ${processEvidence.stdoutTailExcerpt}`);
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  const oracle = selectNestedAuthoredExpandWitnesses(scanned, {
    project: values.project, generation: values.generation, scanLimit: 1000, witnessMode,
  });
  report.oracle = oracle;
  return oracle.selected;
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
  const labelIndex = headerIndex(preview, labelColumnLabel);
  const codeIndex = headerIndex(preview, codeColumnLabel);
  assert.equal(preview.rows.length, sourceRecords.length, `${phase} must restore one source row per selected Observation`);
  const expected = new Map(report.oracle.selected.map((resource) => [resource.id, {
    labels: resource.componentLabels,
    codes: resource.codings.map(item => item.code),
  }]));
  const actualRows = preview.rows.map(row => ({
    id: String(scalarCell(row[idIndex], 'Observation ID')),
    row,
  }));
  assert.equal(new Set(actualRows.map(row => row.id)).size, actualRows.length, `${phase} duplicated a source Observation`);
  const actual = new Map(actualRows.map(item => [item.id, item.row]));
  assert.deepEqual([...actual.keys()].sort(), [...expected.keys()].sort(), `${phase} must restore exact Observation identities`);
  for (const [id, valuesForId] of expected) {
    const visible = actual.get(id);
    assertVisibleListCell(visible[labelIndex], valuesForId.labels,
      `${phase} displays raw component-label order`);
    assertVisibleListCell(visible[codeIndex], valuesForId.codes,
      `${phase} displays nested codes in raw order, including duplicate values`);
  }
  const entry = report.browserRequests.findLast((request) => request.path.endsWith(preview.proposal ? '/construction-proposals' : '/preview'));
  assert(entry, `${phase} omitted its native preview request`);
  const native = protocolPreview(await waitForCapturedResponse(entry));
  const nativeId = native.columns.find((column) => column.label === 'Observation ID')?.column;
  const nativeLabels = native.columns.find((column) => column.label === labelColumnLabel)?.column;
  const nativeCodes = native.columns.find((column) => column.label === codeColumnLabel)?.column;
  assert(nativeId && nativeLabels && nativeCodes, `${phase} omitted protocol source columns`);
  assert.equal(native.rows.length, expected.size, `${phase} native source row count differs`);
  const nativeRows = new Map(native.rows.map((row) => [row[nativeId], {
    labels: row[nativeLabels], codes: row[nativeCodes],
  }]));
  assert.deepEqual([...nativeRows.keys()].sort(), [...expected.keys()].sort(), `${phase} native source identities differ`);
  for (const [id, valuesForId] of expected) {
    assert.deepEqual(nativeRows.get(id), valuesForId, `${phase} native component-label and nested-code lists differ from raw source`);
  }
  const visibleObservations = Object.fromEntries([...actual].map(([id, row]) => [id, {
    id,
    labelsText: row[labelIndex].text,
    codesText: row[codeIndex].text,
  }]));
  return { headers: preview.headers, rowCount: preview.rows.length, observations: visibleObservations };
};

const expectedExpandTuples = () => report.oracle.expectedRows
  .map(({ id, ordinal, code }) => [id, ordinal, code])
  .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));

const verifyExpandedRows = (preview, expectedLabel, phase) => {
  assert(preview, `${phase} omitted its rendered EXPAND preview`);
  const idIndex = headerIndex(preview, 'Observation ID');
  const valueIndex = headerIndex(preview, expectedLabel);
  const labelsIndex = headerIndex(preview, labelColumnLabel);
  const ordinalIndex = headerIndex(preview, positionLabel);
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, `${phase} row count must include every nested coding and the preserved empty-parent row`);
  const expectedByID = new Map(report.oracle.selected.map(record => [record.id, {
    labels: record.componentLabels,
  }]));
  const actual = preview.rows.map((row) => {
    const id = String(scalarCell(row[idIndex], 'Observation ID'));
    assertVisibleListCell(row[labelsIndex], expectedByID.get(id)?.labels,
      `${phase} must render exact outer component label order for ${id}`);
    const rawOrdinal = scalarCell(row[ordinalIndex], positionLabel);
    const ordinal = rawOrdinal === null || rawOrdinal === undefined || rawOrdinal === '' || rawOrdinal === '—'
      ? null : Number(rawOrdinal);
    const expected = report.oracle.expectedRows.find(item => item.id === id && item.ordinal === ordinal);
    assert(expected, `${phase} produced an unexpected source/ordinal tuple ${JSON.stringify([id, ordinal])}`);
    assert.equal(row[valueIndex].text, expected.code === null ? '—' : expected.code,
      `${phase} rendered coding item differs from raw source at ${JSON.stringify([id, ordinal])}`);
    return [id, ordinal, expected.code];
  }).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  assert.deepEqual(actual, expectedExpandTuples(), `${phase} must preserve exact Observation/code/zero-based-ordinal tuples`);
  return { headers: preview.headers, rowCount: preview.rows.length, tuples: actual };
};

const protocolPreview = (response) => response?.preview ?? response;

const verifyProtocolExpandIdentities = (response, outputLabel, phase, step) => {
  const preview = protocolPreview(response);
  assert(preview?.columns && Array.isArray(preview.rows), `${phase} omitted native protocol preview rows`);
  const column = (label) => preview.columns.find((candidate) => candidate.label === label)?.column;
  const idColumn = column('Observation ID');
  const valueColumn = column(outputLabel);
  const labelColumn = column(labelColumnLabel);
  const ordinalColumn = column(positionLabel);
  assert(idColumn && valueColumn && labelColumn && ordinalColumn,
    `${phase} omitted an identity, component-label, item, or ordinal protocol column`);
  const constructionId = step?.operation?.expand?.constructionId;
  assert.equal(typeof constructionId, 'string', `${phase} authored EXPAND omitted its construction identity`);
  assert.equal(preview.rows.length, report.oracle.expectedRows.length, `${phase} protocol row count differs from raw CDA`);

  const actualTuples = [];
  const mapping = [];
  const identities = [];
  const tupleKeys = new Set();
  const expectedByTuple = new Map(report.oracle.expectedRows.map(expected => [JSON.stringify([expected.id, expected.ordinal]), expected]));
  const sourceByID = new Map(report.oracle.selected.map(record => [record.id, record]));
  for (const row of preview.rows) {
    const id = row[idColumn];
    const rawValue = row[valueColumn];
    const value = Array.isArray(rawValue)
      ? (assert.equal(rawValue.length, 1, `${phase} protocol item must be scalar per expanded row`), rawValue[0])
      : rawValue;
    const rawOrdinal = row[ordinalColumn];
    const ordinal = rawOrdinal === null || rawOrdinal === undefined ? null : Number(rawOrdinal);
    assert.equal(typeof id, 'string', `${phase} protocol Observation ID must be a string`);
    assert(value === null || typeof value === 'string', `${phase} protocol item value must be a string or preserved null`);
    assert(ordinal === null || (Number.isInteger(ordinal) && ordinal >= 0), `${phase} protocol ordinal must be null or a non-negative integer`);
    assert.equal(typeof row.__loom_row_id, 'string', `${phase} native protocol omitted __loom_row_id`);
    assert(row.__loom_row_id.length > 0, `${phase} native protocol exposed an empty __loom_row_id`);
    const tupleKey = JSON.stringify([id, ordinal]);
    assert(!tupleKeys.has(tupleKey), `${phase} protocol duplicated Observation/ordinal tuple ${tupleKey}`);
    tupleKeys.add(tupleKey);
    const expected = expectedByTuple.get(tupleKey);
    assert(expected, `${phase} protocol returned an unexpected Observation/ordinal identity ${tupleKey}`);
    assert.equal(value, expected.code, `${phase} coding code differs from raw nested source for ${tupleKey}`);
    const rawSource = sourceByID.get(id);
    assert(rawSource, `${phase} returned an unselected Observation ${id}`);
    assert.equal(expected.sourceKey, rawSource.sourceKey, `${phase} flat ordinal must map to the raw Observation source key`);
    assert.deepEqual(row[labelColumn], sourceByID.get(id)?.componentLabels,
      `${phase} must retain exact outer component labels for each source Observation`);
    const expectedIdentity = JSON.stringify([
      ['input', expected.sourceKey],
      ['construction', constructionId],
      ['ordinal', expected.ordinal],
    ]);
    assert.equal(row.__loom_row_id, expectedIdentity,
      `${phase} encoded row identity must preserve raw source key and authored ordinal`);
    actualTuples.push(JSON.stringify([id, ordinal, value]));
    mapping.push(JSON.stringify([id, ordinal, expected.sourceKey, row.__loom_row_id]));
    identities.push(row.__loom_row_id);
  }
  const expectedTuples = report.oracle.expectedRows
    .map(({ id, ordinal, code }) => JSON.stringify([id, ordinal, code])).sort();
  assert.deepEqual(actualTuples.sort(), expectedTuples, `${phase} protocol tuples differ from raw CDA`);
  assert.equal(new Set(identities).size, preview.rows.length, `${phase} protocol row identities must be unique`);
  return mapping.sort();
};

const nativePreviewAfter = async (requestIndex, phase) => {
  const entry = report.browserRequests.findLast((candidate, index) => index >= requestIndex && candidate.path.endsWith('/preview'));
  assert(entry, `${phase} did not trigger a native /preview protocol request`);
  const response = await waitForCapturedResponse(entry);
  assert(protocolPreview(response)?.rows, `${phase} native /preview response omitted rows`);
  assert.notEqual(protocolPreview(response)?.sampled, true, `${phase} native preview must contain the complete witness`);
  return { entry, response };
};

const captureSavedCancelState = async (state, expectedLabel, phase) => {
  const savedDocument = document(state);
  assert(savedDocument, `${phase} omitted its saved output document`);
  const visibleRows = verifyExpandedRows(await previewRows(), expectedLabel, `${phase} visible rows`);
  return {
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
    outputId,
    construction: structuredClone(savedDocument.construction ?? { version: 1, steps: [] }),
    preview: await readSavedPreviewBinding(),
    document: structuredClone(savedDocument),
    visibleRows,
  };
};

const verifyCanceledSavedExpand = async ({ startIndex, before, preCancelPreview, expectedLabel, expectedIdentityMapping, phase }) => {
  const afterBuilder = (await api(`${base}/builder`)).body;
  const after = await captureSavedCancelState(afterBuilder, expectedLabel, `${phase} after Cancel`);
  const savedPreview = selectCanceledSavedPreviewRequest(report.browserRequests, {
    startIndex, path: `${base}/preview`, outputId, before, after,
  });
  const previewRequests = report.browserRequests.slice(startIndex)
    .filter(entry => entry.path === `${base}/preview`)
    .map(entry => ({
      path: entry.path,
      status: entry.status,
      completedAt: entry.completedAt ?? null,
      requestReceiptId: entry.body?.receiptId ?? null,
      requestOutputId: entry.body?.outputId ?? null,
      responseReceiptId: entry.response?.receiptId ?? null,
      responseOutputId: entry.response?.outputId ?? null,
    }));
  const selectedRequest = savedPreview ? {
    source: savedPreview.source,
    path: savedPreview.request.path,
    status: savedPreview.request.status,
    completedAt: savedPreview.request.completedAt ?? null,
    requestReceiptId: savedPreview.request.body?.receiptId ?? null,
    requestOutputId: savedPreview.request.body?.outputId ?? null,
    responseReceiptId: savedPreview.request.response?.receiptId ?? null,
    responseOutputId: savedPreview.request.response?.outputId ?? null,
  } : null;
  report.cancellationProofs.push({
    phase,
    startIndex,
    before: {
      snapshotToken: before.snapshotToken,
      draftVersion: before.draftVersion,
      draftDigest: before.draftDigest,
      outputId: before.outputId,
      construction: before.construction,
      preview: before.preview,
    },
    preCancelPreview,
    after: {
      snapshotToken: after.snapshotToken,
      draftVersion: after.draftVersion,
      draftDigest: after.draftDigest,
      outputId: after.outputId,
      construction: after.construction,
      preview: after.preview,
    },
    previewRequests,
    selectedRequest,
  });
  assert(savedPreview, `${phase} must bind the unchanged saved Preview receipt to the canceled draft`);
  assert.equal(after.snapshotToken, before.snapshotToken, `${phase} Cancel changed the snapshot token`);
  assert.equal(after.draftVersion, before.draftVersion, `${phase} Cancel changed the saved draft version`);
  assert.equal(after.draftDigest, before.draftDigest, `${phase} Cancel changed the saved draft digest`);
  assert.deepEqual(after.construction, before.construction, `${phase} Cancel changed the saved construction`);
  assert.deepEqual(after.document, before.document, `${phase} Cancel changed saved columns, source bindings, or row definition`);
  assert.deepEqual(after.visibleRows, before.visibleRows, `${phase} Cancel did not restore the exact saved visible tuples`);
  const savedStep = before.document.construction?.steps?.findLast(step => step.operation?.kind === 'EXPAND');
  assert(savedStep, `${phase} saved document has no authored EXPAND step`);
  const identityMapping = verifyProtocolExpandIdentities(
    savedPreview.request.response, expectedLabel, `${phase} bound saved Preview receipt`, savedStep,
  );
  assert.deepEqual(identityMapping, expectedIdentityMapping,
    `${phase} Cancel must retain exact raw source-key/ordinal row identities`);
  builder = afterBuilder;
  return {
    receiptSource: savedPreview.source,
    previewRequest: {
      path: savedPreview.request.path,
      status: savedPreview.request.status,
      receiptId: savedPreview.request.response?.receiptId,
      outputId: savedPreview.request.response?.outputId,
    },
    before: {
      snapshotToken: before.snapshotToken,
      draftVersion: before.draftVersion,
      draftDigest: before.draftDigest,
      preview: before.preview,
      construction: before.construction,
    },
    after: {
      snapshotToken: after.snapshotToken,
      draftVersion: after.draftVersion,
      draftDigest: after.draftDigest,
      preview: after.preview,
      construction: after.construction,
    },
    savedDocumentPreserved: true,
    visibleRowsPreserved: true,
    visibleRows: after.visibleRows,
    identityMapping,
  };
};

const proposeExpandRemoval = async (phase) => {
  await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
  await waitForBrowser(page, { kind: 'enabled', selector: `[data-testid="construction-remove-step-${appliedStep.id}"]` }, 5000);
  const startedAt = Date.now();
  await click(page, `[data-testid="construction-remove-step-${appliedStep.id}"]`);
  await fastWait(startedAt, { kind: 'status-in', selector: '[data-testid="construction-proposal-panel"]', statuses: ['ready'] }, `${phase} proposal`);
  const durationMs = Date.now() - startedAt;
  report.workflowTimings.push({ name: phase, durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `${phase} took ${durationMs}ms`);
  const visible = await verifyOriginalRows(await previewRows(), `${phase} source restoration proposal`);
  const request = report.browserRequests.findLast((entry) =>
    entry.path.endsWith('/construction-proposals') && entry.body?.removeStepIds?.includes(appliedStep.id));
  assert(request, `${phase} must propose deletion of the saved EXPAND step`);
  assert(!request.body.candidateConstruction.steps.some((step) => step.id === appliedStep.id),
    `${phase} candidate must omit the saved EXPAND step`);
  const response = await waitForCapturedResponse(request);
  assert.equal(response.preview?.rowCount, sourceRecords.length, `${phase} must restore one source row per Observation`);
  return { durationMs, visible, request, response };
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

const openExpandChoice = async () => measure('native-EXPAND-editor-discovery', async (startedAt) => {
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
});

const setInput = (label, value) => fill(page, `input[aria-label=${JSON.stringify(label)}]`, value);

const configureExpand = async (expectedLabel) => {
  await openExpandChoice();
  const startedAt = Date.now();
  await click(page, '[data-testid="construction-reshape-choice-expand"]');
  await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 5000);
  const choices = await inspectPage(page, `return [...document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]').options].map(option=>({value:option.value,label:option.text,disabled:option.disabled}));`);
  const matching = choices.filter((option) => option.label.startsWith(codeColumnLabel) && !option.disabled);
  assert.equal(matching.length, 1, `EXPAND must offer exactly the projected nested coding list: ${JSON.stringify(choices)}`);
  await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]', matching[0].value);
  const advanced = '[data-testid="construction-reshape-expand-advanced"]';
  await click(page, `${advanced} summary`);
  await setInput('Expanded item name', expandedItemName);
  await setInput('Expanded item label', expectedLabel);
  const checked = await inspectPage(page, `return document.querySelector('input[aria-label="Include item position"]')?.checked;`);
  if (!checked) await click(page, 'input[aria-label="Include item position"]');
  await setInput('Position column name', positionName);
  await setInput('Position column label', positionLabel);
  await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Empty list policy"]', 'PRESERVE_PARENT');
  const preview = await waitForProposal(startedAt, expectedLabel);
  const durationMs = Date.now() - startedAt;
  report.workflowTimings.push({ name: 'native-EXPAND-configure-to-preview', durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, `EXPAND configure-to-preview took ${durationMs}ms`);
  return preview;
};

const openSavedExpandEditor = async (phase) => measure(phase, async (startedAt) => {
  const editorAlreadyOpen = await inspectPage(page, `return Boolean(document.querySelector('[data-testid="construction-reshape-editor"]'));`);
  if (!editorAlreadyOpen) {
    await click(page, `[data-testid="construction-history-step-${appliedStep.id}"]`);
    await fastWait(startedAt, { kind: 'enabled', selector: `[data-testid="construction-edit-step-${appliedStep.id}"]` }, 'Saved EXPAND edit action');
    await click(page, `[data-testid="construction-edit-step-${appliedStep.id}"]`);
  }
  await fastWait(startedAt, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 'Saved EXPAND editor');
  const advanced = '[data-testid="construction-reshape-expand-advanced"]';
  const advancedOpen = await inspectPage(page, `return document.querySelector(${JSON.stringify(advanced)})?.open;`);
  if (!advancedOpen) await click(page, `${advanced} summary`);
});

const assertExpandRequest = async (expectedLabel, expectedInputColumnId, expectedStepId) => {
  const entry = latestExpandProposal(expectedLabel);
  assert(entry, 'Native EXPAND editor did not send a construction proposal');
  const request = entry.body;
  const step = request.candidateConstruction.steps.findLast((candidate) => candidate.operation?.kind === 'EXPAND');
  assert(step, 'Construction proposal must contain an EXPAND operation');
  assert.equal(step.operation.expand.inputColumnId, expectedInputColumnId, `EXPAND must bind the projected ${codeFieldPath} list`);
  assert.equal(step.operation.expand.emptyPolicy, 'PRESERVE_PARENT', 'EXPAND must carry the selected empty-list policy');
  assert(step.operation.expand.ordinalColumnId, 'EXPAND must include the optional zero-based item position column');
  if (expectedStepId) assert.equal(step.id, expectedStepId, 'Editing must retain the saved operation identity');
  assert.equal(step.outputs.find((column) => column.id === step.operation.expand.outputColumnId)?.name, expandedItemName);
  assert.equal(step.outputs.find((column) => column.id === step.operation.expand.outputColumnId)?.label, expectedLabel);
  assert(step.outputs.some((column) => column.id === step.operation.expand.ordinalColumnId && column.name === positionName && column.label === positionLabel));
  const response = await waitForCapturedResponse(entry);
  assert.equal(response.preview?.rowCount, report.oracle.expectedRows.length, 'Construction API preview count must include every raw coding and the preserved empty parent');
  assert.notEqual(response.preview?.sampled, true, 'A complete nested EXPAND preview must not be sampled');
  return { request, step, response };
};

const finish = async () => {
  report.finished = new Date().toISOString();
  report.status = report.failures.length ? 'failed'
    : report.gaps.length ? 'partial'
      : report.assertions.length && report.assertions.every(assertion => assertion.status === 'passed') ? 'passed' : 'untested';
  await cda.attachReport('nested-authored-expand-evidence.json', report);
};

try {
  sourceRecords = boundedRawOracle();
  if (sourceRecords.length < 1 || report.oracle.expectedRows.length < 2) {
    throw new Error('The bounded raw oracle must select a populated Observation with a repeated nested coding list.');
  } else {
    report.gaps.push(...report.oracle.gaps);
    for (const gap of report.oracle.gaps) {
      report.assertions.push({ name: gap.assertion, status: 'untested', evidence: { reason: gap.reason, scanLimit: report.oracle.scanLimit } });
    }
    assert(sourceRecords.every((resource) => resource.generation === values.generation && resource.resourceType === 'Observation'));
    recordAssertion('bounded independent raw oracle selected a populated nested coding witness', {
      selectedRoots: sourceRecords.length, expectedItems: report.oracle.expectedRows.length,
      witness: report.oracle.witness,
      selected: report.oracle.selected.map((resource) => ({
        id: resource.id, sourceKey: resource.sourceKey, componentLabels: resource.componentLabels,
        codingShapes: resource.codingShapes, codes: resource.codings.map(item => item.code),
        systems: resource.codings.map(item => item.system),
      })),
    });

    await api(projectRoot, { name: explorer, title: 'CDA authored EXPAND QA' });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(builder.catalog.generation, values.generation, 'Fresh QA Explorer must use the requested CDA generation');
    const observationNode = builder.catalog.nodes.find((node) => node.resourceType === 'Observation');
    assert(observationNode, 'CDA catalog has no Observation root');
    await command([{ type: 'CREATE_TABLE', title: 'Authored list expansion', rootNodeId: observationNode.nodeId }]);
    const createdDocument = builder.workspace.documents.find((doc) => doc.output.title === 'Authored list expansion');
    assert(createdDocument, 'API seed did not create the isolated QA table');
    outputId = createdDocument.output.id;

    const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
    const labelCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === labelFieldPath);
    const valueCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === codeFieldPath);
    assert(idCandidate, 'Observation ID field is unavailable');
    assert(labelCandidate, `Observation.${labelFieldPath} list field is unavailable`);
    assert(valueCandidate, `Observation.${codeFieldPath} list field is unavailable`);
    await command([
      { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' },
      { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: labelCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: labelColumnLabel },
      { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: codeColumnLabel },
    ]);

    const selection = (await api(`${projectRoot}/${encodeURIComponent(explorer)}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `cda-nested-authored-expand-${explorer}`,
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
    assert.equal(saved.columns.length, 3, 'Source baseline must contain source ID, outer labels, and nested codes');
    idColumn = saved.columns.find((column) => column.source?.field?.path === 'id');
    listColumn = saved.columns.find((column) => column.source?.field?.path === codeFieldPath);
    const componentLabelColumn = saved.columns.find((column) => column.source?.field?.path === labelFieldPath);
    assert(idColumn && listColumn && componentLabelColumn, 'Source baseline must retain exact Observation ID, label-list, and nested-code bindings');
    assert.equal(idColumn.occurrenceId, 'base');
    assert.equal(listColumn.occurrenceId, 'base');
    assert.equal(componentLabelColumn.occurrenceId, 'base');
    assert.equal(listColumn.source.field.projectionMode, 'ALL', 'EXPAND input must be a projected list');
    assert.equal(componentLabelColumn.source.field.projectionMode, 'ALL');
    let listColumnId = listColumn.column ?? listColumn.id;
    const idColumnId = idColumn.column ?? idColumn.id;
    assert.equal(typeof listColumnId, 'string', 'Projected list column must expose its stable column ID');
    assert.equal(typeof idColumnId, 'string', 'Observation identity column must expose its stable column ID');
    originalRows = structuredClone(saved.rows);
    report.seed = { outputId, selectionRevisionId: selection.id, idColumn, componentLabelColumn, listColumn, rows: saved.rows, construction: saved.construction ?? { version: 1, steps: [] } };
    assert.equal(saved.construction?.steps?.length ?? 0, 0, 'QA table must start without construction operations');
    recordAssertion('fresh QA document binds source ID, outer labels, nested code ALL list, and RECORDS rows', report.seed);

    monitorBrowser();
    await openTable(sourceRecords.length, 'fresh-owned-explorer-load-to-render');
    const initialPreview = await previewRows();
    report.initialSource = await verifyOriginalRows(initialPreview, 'Initial RECORDS preview');
    await saveDOM('initial-records-with-nested-coding-lists');
    recordAssertion('initial table renders exact Observation IDs and raw outer-label/nested-code lists', report.initialSource);

    await openExpandChoice();
    const defaultExpandReadyAt = Date.now();
    await click(page, '[data-testid="construction-reshape-choice-expand"]');
    await waitForBrowser(page, { kind: 'present', selector: '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]' }, 5000);
    const initialFieldOptions = await inspectPage(page, `return [...document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]').options].map(option=>({value:option.value,label:option.text,disabled:option.disabled}));`);
    const initialListOption = initialFieldOptions.find((option) => option.label.startsWith(codeColumnLabel) && !option.disabled);
    assert(initialListOption, `Native EXPAND did not offer projected ${codeFieldPath}: ${JSON.stringify(initialFieldOptions)}`);
    // The editor binds the compiler-owned source projection, not the authored UI column ID.
    listColumnId = initialListOption.value;
    await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]', initialListOption.value);
    await click(page, '[data-testid="construction-reshape-expand-advanced"] summary');
    await setInput('Expanded item name', expandedItemName);
    await setInput('Expanded item label', expandedItemLabel);
    await click(page, 'input[aria-label="Include item position"]');
    await setInput('Position column name', positionName);
    await setInput('Position column label', positionLabel);
    await selectOption(page, '[data-testid="construction-reshape-expand"] select[aria-label="Empty list policy"]', 'PRESERVE_PARENT');
    const initialProposalPreview = await waitForProposal(defaultExpandReadyAt, expandedItemLabel);
    const initialDurationMs = Date.now() - defaultExpandReadyAt;
    report.workflowTimings.push({ name: 'native-EXPAND-configure-to-preview', durationMs: initialDurationMs, limitMs: 5000 });
    assert(initialDurationMs <= 5000, `EXPAND configure-to-preview took ${initialDurationMs}ms`);
    report.expansionPreview = verifyExpandedRows(initialProposalPreview, expandedItemLabel, 'Initial native nested EXPAND preview');
    const firstProposal = await assertExpandRequest(expandedItemLabel, listColumnId);
    report.nativeProposal = { request: firstProposal.request, step: firstProposal.step, response: firstProposal.response };
    await saveDOM('expand-proposal-before-cancel');
    recordAssertion('native EXPAND preview emits the projected list input, item output, zero-based ordinal, empty policy, and exact raw tuples', {
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
    recordAssertion('Cancel restores the exact source records and leaves RECORDS unchanged', { draftDigest: builder.draftDigest, preview: report.canceledSource });

    const configuredPreview = await configureExpand(expandedItemLabel);
    report.expansionPreview = verifyExpandedRows(configuredPreview, expandedItemLabel, 'Confirmed native nested EXPAND preview');
    const configuredProposal = await assertExpandRequest(expandedItemLabel, listColumnId);
    const authoredIdentityMapping = verifyProtocolExpandIdentities(
      configuredProposal.response, expandedItemLabel, 'Authored nested EXPAND proposal', configuredProposal.step,
    );
    report.rowIdentityMappings = { proposal: authoredIdentityMapping };
    recordAssertion('native proposal protocol emits literal coding values and exact source-key/ordinal identities', {
      rowCount: authoredIdentityMapping.length, mapping: authoredIdentityMapping,
    });
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
    assert.equal(appliedOutput?.name, expandedItemName);
    assert.equal(appliedOutput?.label, expandedItemLabel);
    assert(appliedStep.outputs.some((column) => column.name === idColumnId && column.label === 'Observation ID'), 'Expanded rows must retain the authored Observation ID through its source projection');
    assert(appliedStep.outputs.some((column) => column.id === appliedStep.operation.expand.ordinalColumnId));
    assert(!appliedStep.outputs.some((column) => column.id === listColumnId), 'EXPAND must replace the list with its item output at this stage');
    report.applied = { step: appliedStep, rows: document().rows, columns: document().columns };
    report.appliedPreview = verifyExpandedRows(await previewRows(), expandedItemLabel, 'Applied native nested EXPAND table');
    const appliedProtocol = await nativePreviewAfter(applyPreviewRequestIndex, 'Applied native EXPAND');
    report.rowIdentityMappings.applied = verifyProtocolExpandIdentities(
      appliedProtocol.response, expandedItemLabel, 'Applied native EXPAND', appliedStep,
    );
    assert.deepEqual(report.rowIdentityMappings.applied, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive Apply for every Observation/ordinal tuple');
    const appliedPersistenceBefore = capturePersistenceState(
      builder, report.appliedPreview, report.rowIdentityMappings.applied,
    );
    const applyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
    assert(applyRequest, 'Native Apply must use APPLY_CONSTRUCTION_PROPOSAL');
    await waitForCapturedResponse(applyRequest);
    assert.equal(configuredProposal.step.id, appliedStep.id, 'Applied operation identity must match the authored proposal');
    await saveDOM('expand-applied');
    recordAssertion('Apply persists EXPAND while row definition stays RECORDS and IDs, values, ordinals, and bindings match raw CDA', {
      step: appliedStep, preview: report.appliedPreview,
    });

    const appliedReloadRequestIndex = report.browserRequests.length;
    await openTable(report.oracle.expectedRows.length, 'reload-applied-expand');
    report.reloadPreview = verifyExpandedRows(await previewRows(), expandedItemLabel, 'Reloaded native nested EXPAND table');
    const appliedReloadProtocol = await nativePreviewAfter(appliedReloadRequestIndex, 'Reloaded native EXPAND');
    report.rowIdentityMappings.appliedReload = verifyProtocolExpandIdentities(
      appliedReloadProtocol.response, expandedItemLabel, 'Reloaded native EXPAND', appliedStep,
    );
    assert.deepEqual(report.rowIdentityMappings.appliedReload, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive reload for every Observation/ordinal tuple');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    assert.equal(document().construction.steps.find((step) => step.id === appliedStep.id)?.operation.kind, 'EXPAND');
    const appliedPersistenceAfter = capturePersistenceState(
      builder, report.reloadPreview, report.rowIdentityMappings.appliedReload,
    );
    await saveDOM('expand-reloaded');
    const appliedPersistence = persistenceEvidence(appliedPersistenceBefore, appliedPersistenceAfter);
    cda.check('persistence', 'Reload preserves native EXPAND output and unchanged RECORDS row definition',
      appliedPersistence.unchangedSavedState, { ...appliedPersistence, stepId: appliedStep.id });

    const beforeEditCancel = await captureSavedCancelState(builder, expandedItemLabel, 'Saved EXPAND before edit Cancel');
    await openSavedExpandEditor('native-EXPAND-edit-editor-discovery');
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
    assert.equal(reopened.name, expandedItemName);
    assert.equal(reopened.label, expandedItemLabel);
    assert.equal(reopened.ordinal, true);
    assert.equal(reopened.positionName, positionName);
    assert.equal(reopened.positionLabel, positionLabel);
    assert.equal(reopened.emptyPolicy, 'PRESERVE_PARENT');
    recordAssertion('native Edit reopens the saved input, output identity, ordinal, and empty policy', reopened);

    const canceledEditLabel = 'Canceled edit label';
    const cancelEditStartedAt = Date.now();
    await setInput('Expanded item label', canceledEditLabel);
    const cancelEditPreview = await waitForProposal(cancelEditStartedAt, canceledEditLabel);
    const cancelEditDurationMs = Date.now() - cancelEditStartedAt;
    report.workflowTimings.push({ name: 'native-EXPAND-edit-cancel-preview', durationMs: cancelEditDurationMs, limitMs: 5000 });
    assert(cancelEditDurationMs <= 5000, `EXPAND edit Cancel preview took ${cancelEditDurationMs}ms`);
    report.editCancelPreview = verifyExpandedRows(cancelEditPreview, canceledEditLabel, 'Canceled native EXPAND edit proposal');
    const canceledEditProposal = await assertExpandRequest(canceledEditLabel, listColumnId, appliedStep.id);
    report.rowIdentityMappings.editCancelProposal = verifyProtocolExpandIdentities(
      canceledEditProposal.response, canceledEditLabel, 'Canceled native EXPAND edit proposal', canceledEditProposal.step,
    );
    assert.deepEqual(report.rowIdentityMappings.editCancelProposal, report.rowIdentityMappings.appliedReload,
      'Canceled label edit proposal must keep the same raw source-key/ordinal identities');
    await saveDOM('expand-edit-cancel-preview');

    const editPreCancelPreview = await readSavedPreviewBinding();
    const editCancelRequestStart = report.browserRequests.length;
    await measure('native-EXPAND-edit-cancel-restoration', async (startedAt) => {
      await click(page, '[data-testid="construction-cancel-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(report.oracle.expectedRows.length),
        { kind: 'present', selector: '[data-testid="construction-history"]' },
      ] }, 'Canceled EXPAND edit saved-table restoration');
      await waitForSavedPreviewDraft(startedAt, beforeEditCancel, 'Canceled EXPAND edit');
    });
    const editCancelEvidence = await verifyCanceledSavedExpand({
      startIndex: editCancelRequestStart,
      before: beforeEditCancel,
      preCancelPreview: editPreCancelPreview,
      expectedLabel: expandedItemLabel,
      expectedIdentityMapping: report.rowIdentityMappings.appliedReload,
      phase: 'Native EXPAND edit Cancel',
    });
    report.rowIdentityMappings.editCancel = editCancelEvidence.identityMapping;
    await saveDOM('expand-edit-cancel-restored');
    recordAssertion('Canceling a saved EXPAND edit preserves draft, construction, bindings, cached tuples, and exact identities', editCancelEvidence);

    await openSavedExpandEditor('native-EXPAND-reopen-after-edit-Cancel');
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
      editedProposal.response, 'Edited component value', 'Edited native EXPAND proposal', editedProposal.step,
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
      editAppliedProtocol.response, 'Edited component value', 'Applied edited EXPAND', appliedStep,
    );
    assert.deepEqual(report.rowIdentityMappings.editApplied, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive label edit Apply for every Observation/ordinal tuple');
    const editedPersistenceBefore = capturePersistenceState(
      builder, report.editAppliedPreview, report.rowIdentityMappings.editApplied,
    );
    await saveDOM('expand-edit-applied');
    recordAssertion('Editing changes the item label while preserving the operation and output identities', { step: appliedStep, preview: report.editAppliedPreview });

    const editedReloadRequestIndex = report.browserRequests.length;
    await openTable(report.oracle.expectedRows.length, 'reload-edited-expand');
    report.editReloadPreview = verifyExpandedRows(await previewRows(), 'Edited component value', 'Reloaded edited EXPAND table');
    const editedReloadProtocol = await nativePreviewAfter(editedReloadRequestIndex, 'Reloaded edited EXPAND');
    report.rowIdentityMappings.editReload = verifyProtocolExpandIdentities(
      editedReloadProtocol.response, 'Edited component value', 'Reloaded edited EXPAND', appliedStep,
    );
    assert.deepEqual(report.rowIdentityMappings.editReload, authoredIdentityMapping,
      'Native __loom_row_id mapping must survive edited-table reload for every Observation/ordinal tuple');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    const editedPersistenceAfter = capturePersistenceState(
      builder, report.editReloadPreview, report.rowIdentityMappings.editReload,
    );
    await saveDOM('expand-edit-reloaded');
    const editedPersistence = persistenceEvidence(editedPersistenceBefore, editedPersistenceAfter);
    cda.check('persistence', 'Edited EXPAND output and unchanged RECORDS definition persist after reload',
      editedPersistence.unchangedSavedState, editedPersistence);

    const beforeRemovalCancel = await captureSavedCancelState(builder, 'Edited component value', 'Saved EXPAND before removal Cancel');
    const priorExpandProposal = latestExpandProposal('Edited component value');
    assert(priorExpandProposal, 'The saved and edited EXPAND proposal evidence should remain available');
    const canceledRemoval = await proposeExpandRemoval('native-EXPAND-removal-cancel-preview');
    report.removalCancelPreview = canceledRemoval.visible;
    await saveDOM('expand-removal-cancel-preview');

    const removalPreCancelPreview = await readSavedPreviewBinding();
    const removalCancelRequestStart = report.browserRequests.length;
    await measure('native-EXPAND-removal-cancel-restoration', async (startedAt) => {
      await click(page, '[data-testid="construction-cancel-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(report.oracle.expectedRows.length),
        { kind: 'present', selector: '[data-testid="construction-history"]' },
      ] }, 'Canceled EXPAND removal saved-table restoration');
      await waitForSavedPreviewDraft(startedAt, beforeRemovalCancel, 'Canceled EXPAND removal');
    });
    const removalCancelEvidence = await verifyCanceledSavedExpand({
      startIndex: removalCancelRequestStart,
      before: beforeRemovalCancel,
      preCancelPreview: removalPreCancelPreview,
      expectedLabel: 'Edited component value',
      expectedIdentityMapping: report.rowIdentityMappings.editReload,
      phase: 'Native EXPAND removal Cancel',
    });
    report.rowIdentityMappings.removalCancel = removalCancelEvidence.identityMapping;
    await saveDOM('expand-removal-cancel-restored');
    recordAssertion('Canceling EXPAND removal preserves draft, construction, bindings, cached tuples, and exact identities', removalCancelEvidence);

    const appliedRemoval = await proposeExpandRemoval('native-EXPAND-removal-reopen-to-preview');
    report.removalPreview = appliedRemoval.visible;
    const removalRequest = appliedRemoval.request;
    await saveDOM('expand-remove-preview-restored-records');
    const removalApplyRequestIndex = report.browserRequests.length;
    let removalApplySourceIdentity;
    await measure('native-EXPAND-remove-apply-restoration', async (startedAt) => {
      await click(page, '[data-testid="construction-apply-proposal"]');
      await fastWait(startedAt, { kind: 'all', conditions: [
        { kind: 'hidden', selector: '[data-testid="construction-proposal-panel"]' },
        rowsReady(sourceRecords.length),
        { kind: 'count', selector: '[data-testid^="construction-history-step-"]', count: 0 },
      ] }, 'Applied EXPAND removal restoration');
      const removeApplyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');
      assert(removeApplyRequest, 'Applying removal must use the native construction command');
      const settled = await waitForAppliedSourceCapabilities(requestMonitor, {
        applyRequest: removeApplyRequest,
        fromIndex: removalApplyRequestIndex,
        deadlineAt: startedAt + 5000,
        path: `${base}/construction-capabilities`,
        outputId,
      });
      removalApplySourceIdentity = settled.expected;
      report.removalApplyCapabilities = {
        requestId: settled.capabilities.requestId,
        browserRequestId: settled.capabilities.browserRequestId,
        status: settled.capabilities.status,
        completedAt: settled.capabilities.completedAt,
        outputId: settled.expected.outputId,
        stageId: settled.expected.stageId,
        draftVersion: settled.expected.draftVersion,
        draftDigestMatched: true,
        snapshotMatched: true,
      };
    });
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.deepEqual(document().rows, originalRows);
    assert.equal(document().construction?.steps?.length ?? 0, 0, 'Removing EXPAND must restore the original construction');
    assert.equal(document().columns.length, 3);
    assert(document().columns.some((column) => column.source?.field?.path === 'id' && column.occurrenceId === 'base'));
    assert(document().columns.some((column) => column.source?.field?.path === labelFieldPath && column.occurrenceId === 'base' && column.source.field.projectionMode === 'ALL'));
    assert(document().columns.some((column) => column.source?.field?.path === codeFieldPath && column.occurrenceId === 'base' && column.source.field.projectionMode === 'ALL'));
    report.removedPreview = await verifyOriginalRows(await previewRows(), 'Removed EXPAND table');
    await saveDOM('expand-removed-source-records-restored');
    recordAssertion('Remove restores the exact source IDs, list values, bindings, and RECORDS row definition', {
      rows: document().rows, columns: document().columns, preview: report.removedPreview,
      sourceCapabilitiesBeforeReload: report.removalApplyCapabilities,
    });
    const restoredSourcePersistenceBefore = capturePersistenceState(
      builder, report.removedPreview,
    );

    const removalReloadSourceIdentity = {
      snapshotToken: builder.catalog.snapshotToken,
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
      outputId,
      stageId: 'source_projection',
    };
    assert.deepEqual(removalReloadSourceIdentity, removalApplySourceIdentity,
      'Pre-reload capabilities must match the exact saved source identity');
    const removalReloadRequestIndex = report.browserRequests.length;
    await openTable(sourceRecords.length, 'reload-after-expand-removal', async (startedAt) => {
      const capabilities = await waitForSourceCapabilities(requestMonitor, {
        fromIndex: removalReloadRequestIndex,
        deadlineAt: startedAt + 5000,
        path: `${base}/construction-capabilities`,
        expected: removalReloadSourceIdentity,
      });
      report.removalReloadCapabilities = {
        requestId: capabilities.requestId,
        browserRequestId: capabilities.browserRequestId,
        status: capabilities.status,
        completedAt: capabilities.completedAt,
        outputId: removalReloadSourceIdentity.outputId,
        stageId: removalReloadSourceIdentity.stageId,
        draftVersion: removalReloadSourceIdentity.draftVersion,
        draftDigestMatched: true,
        snapshotMatched: true,
      };
    });
    report.removalReloadPreview = await verifyOriginalRows(await previewRows(), 'Reload after EXPAND removal');
    builder = (await api(`${base}/builder`)).body;
    assert.equal(document().rows.kind, 'RECORDS');
    assert.equal(document().construction?.steps?.length ?? 0, 0);
    assert.deepEqual(document().rows, originalRows);
    const restoredSourcePersistenceAfter = capturePersistenceState(
      builder, report.removalReloadPreview,
    );
    await saveDOM('expand-removal-reloaded');
    const restoredSourcePersistence = persistenceEvidence(
      restoredSourcePersistenceBefore, restoredSourcePersistenceAfter,
    );
    cda.check('persistence', 'Reload after removal preserves source-record restoration',
      restoredSourcePersistence.unchangedSavedState, {
        ...restoredSourcePersistence,
        sourceCapabilitiesBeforeReload: report.removalApplyCapabilities,
        sourceCapabilities: report.removalReloadCapabilities,
      });

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
    const workflowCheckpoints = report.workflowTimings.map(({ name, durationMs }) => ({ name, durationMs }));
    const failedNativeActions = report.actions.filter(action => action.status !== 'passed' ||
      !Number.isFinite(action.elapsedMs) || action.elapsedMs < 0 || action.elapsedMs > 5000);
    const workflowWithinBudget = workflowCheckpoints.length > 0 && workflowCheckpoints.every(checkpoint =>
      typeof checkpoint.name === 'string' && checkpoint.name.trim().length > 0 &&
      Number.isFinite(checkpoint.durationMs) && checkpoint.durationMs >= 0 && checkpoint.durationMs <= 5000);
    const allWithinBudget = workflowWithinBudget && report.actions.length > 0 && failedNativeActions.length === 0;
    cda.check('performance', 'All native action and action-to-render checkpoints complete within five seconds', allWithinBudget, {
      workflowCheckpoints,
      workflowCheckpointCount: workflowCheckpoints.length,
      maximumWorkflowCheckpointMs: Math.max(0, ...workflowCheckpoints.map(checkpoint => checkpoint.durationMs)),
      nativeActionCount: report.actions.length,
      maximumNativeActionMs: Math.max(0, ...report.actions.map(action => action.elapsedMs)),
      failedNativeActions,
      actionBudgetMs: 5000,
      workflowCheckpointBudgetMs: 5000,
    });
    recordAssertion('browser protocol, runtime, module, and console capture is clean; incidental favicon errors are retained separately', report.browserErrors);
  }
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
