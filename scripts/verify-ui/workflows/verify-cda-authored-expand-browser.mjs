import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { requireUnique } from '../helpers/playwright-actions.mjs';
import { waitForCondition } from '../helpers/playwright-observations.mjs';

export async function authoredExpandWorkflow({ page, cda }) {
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
  started: new Date().toISOString(), assertions: [], gaps: [], failures: [], requests: [], workflowTimings: [],
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
let fixtureGapReason;

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

const openTable = async (expectedRows, name) => measure(name, async (startedAt) => {
  const url = `${values['ui-origin']}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await navigate(page, url);
  await fastWait(startedAt, { kind: 'present', selector: `[data-testid="construction-table-${outputId}"]` }, 'Explorer table discovery');
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await fastWait(startedAt, rowsReady(expectedRows), 'CDA table render');
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

const nativePreviewAfter = async (requestIndex, phase) => {
  const entry = report.browserRequests.findLast((candidate, index) => index >= requestIndex && candidate.path.endsWith('/preview'));
  assert(entry, `${phase} did not trigger a native /preview protocol request`);
  const response = await waitForCapturedResponse(entry);
  assert(protocolPreview(response)?.rows, `${phase} native /preview response omitted rows`);
  return { entry, response };
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

const finish = async () => {
  report.finished = new Date().toISOString();
  report.status = report.failures.length ? 'failed'
    : report.gaps.length ? 'partial'
      : report.assertions.length && report.assertions.every(assertion => assertion.status === 'passed') ? 'passed' : 'untested';
  await cda.attachReport('authored-expand-evidence.json', report);
};

try {
  sourceRecords = boundedRawOracle();
  if (sourceRecords.length === 0 || report.oracle.expectedRows.length < 2) {
    fixtureGapReason = 'The bounded 1000-record scan found no selection of up to three Observations and six components with distinct non-empty valueString items.';
    report.gaps.push({ assertion: 'bounded Observation.component[].valueString raw oracle', status: 'untested', reason: fixtureGapReason });
  } else {
    assert(sourceRecords.every((resource) => resource.generation === values.generation && resource.resourceType === 'Observation'));
    recordAssertion('bounded independent raw oracle selected at most three roots and six distinct list items', {
      selectedRoots: sourceRecords.length, expectedItems: report.oracle.expectedRows.length,
      selected: report.oracle.selected.map((resource) => ({ id: resource.id, values: resource.componentValues.map((item) => item.value) })),
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
    report.seed = { outputId, selectionRevisionId: selection.id, idColumn, listColumn, rows: saved.rows, construction: saved.construction ?? { version: 1, steps: [] } };
    assert.equal(saved.construction?.steps?.length ?? 0, 0, 'QA table must start without construction operations');
    recordAssertion('fresh QA document binds a scalar Observation ID and a base-occurrence ALL list while rows remain RECORDS', report.seed);

    monitorBrowser();
    await openTable(sourceRecords.length, 'fresh-owned-explorer-load-to-render');
    const initialPreview = await previewRows();
    report.initialSource = await verifyOriginalRows(initialPreview, 'Initial RECORDS preview');
    await saveDOM('initial-records-with-projected-list');
    recordAssertion('initial table renders the exact selected Observation IDs and component list values', report.initialSource);

    await openExpandChoice();
    const defaultExpandReadyAt = Date.now();
    await click(page, '[data-testid="construction-reshape-choice-expand"]');
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

    const configuredPreview = await configureExpand('Expanded component value');
    report.expansionPreview = verifyExpandedRows(configuredPreview, 'Expanded component value', 'Confirmed native EXPAND preview');
    const configuredProposal = await assertExpandRequest('Expanded component value', listColumnId);
    const authoredIdentityMapping = verifyProtocolExpandIdentities(
      configuredProposal.response, 'Expanded component value', 'Authored EXPAND proposal',
    );
    report.rowIdentityMappings = { proposal: authoredIdentityMapping };
    recordAssertion('native proposal protocol exposes unique __loom_row_id values keyed by Observation ID and ordinal', {
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
    recordAssertion('Apply persists EXPAND while row definition stays RECORDS and IDs, values, ordinals, and bindings match raw CDA', {
      step: appliedStep, preview: report.appliedPreview,
    });

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
    recordAssertion('Reload preserves native EXPAND output and unchanged RECORDS row definition', { preview: report.reloadPreview, stepId: appliedStep.id });

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
    recordAssertion('browser protocol, runtime, module, and console capture is clean; incidental favicon errors are retained separately', report.browserErrors);
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
