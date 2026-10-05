import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from './native-cda-workflow-tools.mjs';

export async function sourceFieldsWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, selectOption, navigate, inspect, clickControl, fillControl, selectControl,
    nativeClick, nativeFill, nativeSelect, navigatePage, inspectDOM, browserEval, inspectPage,
    waitForDOM, waitForBrowser, captureCDARequests, captureRequests, waitForCapturedResponse,
    performAction, requireUnique } = createNativeCdaWorkflowTools({ page, cda });
  const target = cda.target;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target, caseOptions.arangoContainer);
  assert(project && generation && apiOrigin && uiOrigin, 'The CDA fixture must bind project, generation, API origin, and UI origin explicitly.');
  assert.equal(generation, 'cda-fhir-v1', 'This verifier requires the loaded CDA FHIR generation.');
const explorer = `cda-source-fields-${Date.now()}-${randomUUID().slice(0, 8)}`;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
assert.notEqual(explorer, protectedExplorer, 'The verifier must own a fresh Explorer');

const projectRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const root = `${projectRoot}/${encodeURIComponent(explorer)}`;
const base = `${root}/authoring/v2`;
const apiCalls = [];
let nativeRequests = [];
const report = {
  started: new Date().toISOString(),
  explorer,
  protectedExplorer,
  target: { apiOrigin, uiOrigin, project, generation, arangoContainer },
  operation: 'direct raw source field projections',
  assertions: [],
  gaps: [{ assertion: 'related semantic sources and authored EXPAND', status: 'not-covered', reason: 'This verifier covers direct same-resource field projections only; related semantic selection and authored list expansion remain separate behavior classes.' }],
  apiCalls,
  nativeRequests,
  errors: [],
  timings: [],
  evidencePaths: [],
  proofBoundary: 'Bootstrap API calls seed a fresh Explorer and bounded population. Native browser requests, rendered values, and saved Builder state prove field availability and lifecycle behavior; an API status alone is never a native UI proof.',
};

let browserEvents;
let builder;
let outputId;
let rootColumnId;
let selection;
let originalWorkspace;
let sourceRecords = [];

const recordAssertion = (name, evidenceValue) => report.assertions.push({ name, status: 'passed', evidence: evidenceValue });
const recordTiming = (name, startedAt, evidenceValue = {}) => report.timings.push({ name, durationMs: Date.now() - startedAt, ...evidenceValue });

const responseValue = (raw) => {
  if (!raw) return undefined;
  try { return JSON.parse(raw); } catch { return raw.slice(0, 32768); }
};

const api = async (path, body) => {
  const startedAt = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `native-cda-${randomUUID()}` };
  const url = apiOrigin + path;
  const response = body === undefined
    ? await cda.request.get(url, { headers, timeout: 30000 })
    : await cda.request.post(url, { headers, data: body, timeout: 30000 });
  const value = await response.json();
  const apiEvidence = { path, status: response.status(), body, response: value, startedAt, completedAt: Date.now() };
  report.apiCalls?.push(apiEvidence);
  report.requests?.push(apiEvidence);
  assert(response.ok(), `${path}: ${JSON.stringify(value)}`);
  return value;
};

const command = async (commands) => {
  await api(`${base}/commands`, {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(`${base}/builder`);
};

const document = (state = builder) => state.workspace.documents.find((candidate) => candidate.output.id === outputId);
const fieldColumn = (state, path) => document(state)?.columns.find((column) => column.source?.field?.path === path);
const columnIdOf = (column) => column?.column ?? column?.id;
const normalizedPresentationLabel = (value) => String(value ?? '').normalize('NFKC').replace(/\s+/g, ' ').trim().toLowerCase();
const renderedHeaderMatches = (header, expected) => normalizedPresentationLabel(String(header ?? '').split('\n')[0]) === normalizedPresentationLabel(expected);
const identity = (state = builder) => ({
  snapshotToken: state.catalog.snapshotToken,
  expectedDraftVersion: state.draftVersion,
  expectedDraftDigest: state.draftDigest,
});

const boundedRawOracle = () => {
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} SORT r.id LIMIT 1000 RETURN {id:r.id,generation:r.dataset_generation,resourceType:r.payload.resourceType,status:r.payload.status,components:r.payload.component}`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango oracle returned no JSON array: ${result.stdout.slice(0, 300)}`);
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'The independent raw-source scan exceeded its 1000 Observation bound');

  const candidates = scanned.flatMap((row) => {
    if (row.resourceType !== 'Observation' || row.generation !== generation || typeof row.id !== 'string' ||
      typeof row.status !== 'string' || !row.status.trim() || !Array.isArray(row.components) ||
      row.components.length < 2 || row.components.length > 6) return [];
    const componentValues = row.components.map((component) => component?.valueString);
    if (!componentValues.every((value) => typeof value === 'string' && value.trim().length > 0)) return [];
    return [{ id: row.id, generation: row.generation, resourceType: row.resourceType, status: row.status, componentValues }];
  }).sort((left, right) => left.componentValues.length - right.componentValues.length || left.id.localeCompare(right.id));

  const selected = [];
  let totalValues = 0;
  for (const candidate of candidates) {
    if (selected.length >= 3) break;
    if (totalValues + candidate.componentValues.length > 6) continue;
    selected.push(candidate);
    totalValues += candidate.componentValues.length;
  }
  report.oracle = {
    source: 'ArangoDB raw Observation payloads',
    queryLimit: 1000,
    scanned: scanned.length,
    scalarPath: 'status',
    repeatedPath: 'component[].valueString',
    selected,
  };
  assert(selected.length <= 3);
  assert(selected.length === 0 || new Set(selected.map((row) => row.id)).size === selected.length);
  assert(selected.length === 0 || selected.reduce((sum, row) => sum + row.componentValues.length, 0) <= 6);
  return selected;
};

const trackBrowser = () => {
  const tracker = cda.captureRequests(root);
  nativeRequests = report.nativeRequests;
  page.on('request', request => {
    if (new URL(request.url()).pathname.includes(protectedExplorer)) report.errors.push({ kind: 'protected-explorer-request', path: new URL(request.url()).pathname });
  });
  return tracker;
};

const waitNative = async (predicate, fromIndex = 0, timeoutMs = 10000) => {
  const match = entry => entry.status !== undefined && entry.completedAt && entry.response !== undefined && !entry.failure && predicate(entry);
  const existing = nativeRequests.slice(fromIndex).findLast(match);
  const entry = existing ?? await waitForCapturedResponse(page, browserEvents, candidate =>
    nativeRequests.indexOf(candidate) >= fromIndex && match(candidate), timeoutMs);
  assert.equal(entry.status, 200, `Native ${entry.path} returned ${entry.status}: ${JSON.stringify(entry.response).slice(0, 1200)}`);
  return entry;
};

const flushNativeReads = async () => {
  await browserEvents?.flush();
};

const nativeIndex = () => nativeRequests.length;
const nativePreviewBody = (response) => response?.preview?.rows !== undefined ? response.preview : response;

const assertPreview = (preview, expectedDocument, expectedPaths, phase) => {
  assert(preview && Array.isArray(preview.rows) && Array.isArray(preview.columns), `${phase} omitted native preview rows or columns`);
  assert.equal(preview.outputId, outputId, `${phase} preview belongs to another table`);
  assert(preview.receiptId, `${phase} saved preview must carry a fresh server receipt`);
  const doc = expectedDocument;
  assert(doc, `${phase} has no saved Builder document`);
  const idColumn = doc.columns.find((column) => column.source?.field?.path === 'id');
  assert(idColumn, `${phase} omitted the Observation.id identity field`);
  const idColumnId = columnIdOf(idColumn);
  const byPath = new Map(expectedPaths.map((path) => [path, doc.columns.find((column) => column.source?.field?.path === path)]));
  const previewColumn = (column) => preview.columns.find((candidate) => candidate.column === columnIdOf(column));
  assert(previewColumn(idColumn), `${phase} native preview omitted the ID output column`);
  for (const path of expectedPaths) {
    const column = byPath.get(path);
    assert(column, `${phase} saved document omitted ${path}`);
    assert(previewColumn(column), `${phase} native preview omitted the ${path} output column`);
  }
  assert.equal(preview.rows.length, sourceRecords.length, `${phase} preview row count must equal the bounded CDA witness population`);
  const actual = new Map();
  for (const row of preview.rows) {
    const id = row[idColumnId];
    assert.equal(typeof id, 'string', `${phase} Observation IDs must be strings`);
    assert(!actual.has(id), `${phase} duplicated source Observation ${id}`);
    const rowValues = { id };
    for (const path of expectedPaths) {
      const value = row[columnIdOf(byPath.get(path))];
      rowValues[path] = value;
      if (path === 'status') assert.equal(typeof value, 'string', `${phase} status must retain its scalar string type`);
      if (path === 'component[].valueString') {
        assert(Array.isArray(value), `${phase} component.valueString must remain a list`);
        assert(value.every((item) => typeof item === 'string'), `${phase} repeated component values must retain string item types`);
      }
    }
    actual.set(id, rowValues);
  }
  assert.deepEqual([...actual.keys()].sort(), sourceRecords.map((record) => record.id).sort(), `${phase} must preserve the exact selected CDA Observation IDs`);
  for (const expected of sourceRecords) {
    const row = actual.get(expected.id);
    assert.equal(row.id, expected.id);
    if (expectedPaths.includes('status')) assert.equal(row.status, expected.status, `${phase} status differs from raw CDA for ${expected.id}`);
    if (expectedPaths.includes('component[].valueString')) {
      assert.deepEqual(row['component[].valueString'], expected.componentValues, `${phase} repeated values or source ordering differs from raw CDA for ${expected.id}`);
    }
  }
  return {
    receiptId: preview.receiptId,
    rows: actual.size,
    columnIds: preview.columns.map((column) => column.column),
    values: Object.fromEntries([...actual.entries()]),
  };
};

const currentDocument = async () => {
  builder = await api(`${base}/builder`);
  return document();
};

const assertDirectFieldBinding = (column, candidate, projection, phase) => {
  assert(column, `${phase} omitted ${candidate.fieldPath}`);
  assert.equal(column.occurrenceId, 'base', `${phase} field must stay bound to the source row`);
  assert.equal(column.source?.kind, 'field', `${phase} must remain a direct field binding`);
  assert.equal(column.source?.field?.path, candidate.fieldPath, `${phase} changed its field path`);
  assert.equal(column.source?.field?.projectionMode, projection, `${phase} changed its source projection form`);
  assert.equal(candidate.logicalType, 'string', `${phase} candidate must advertise a string type`);
  assert.equal(column.logicalType, candidate.logicalType, `${phase} changed its advertised logical type`);
  return { columnId: columnIdOf(column), candidateId: candidate.candidateId, path: candidate.fieldPath, projection, logicalType: column.logicalType, cardinality: candidate.cardinality };
};

const rendered = async (expectedRows, expectedColumns, phase) => {
  await waitForBrowser(page, ([__arg0, __arg1]) => Boolean((() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-rowcount')===__arg0&&table.getAttribute('aria-colcount')===__arg1&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'));})()), [String(expectedRows + 1), String(expectedColumns)]);
  const view = await browserEval(page, () => { const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {rowCount:table?.getAttribute('aria-rowcount'),columnCount:table?.getAttribute('aria-colcount'),headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))).filter(row=>row.length)}; });
  assert.equal(view.rowCount, String(expectedRows + 1), `${phase} rendered the wrong number of rows`);
  assert.equal(view.columnCount, String(expectedColumns), `${phase} rendered the wrong number of columns`);
  assert.equal(view.rows.length, expectedRows, `${phase} DOM table omitted visible witness rows`);
  return view;
};

const openTable = async (expectedRows, expectedColumns, phase) => {
  const startedAt = Date.now();
  const fromIndex = nativeIndex();
  const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await navigate(page, url);
  await waitForBrowser(page, ([__arg0]) => Boolean(Boolean(document.querySelector(__arg0))), [`[data-testid="construction-table-${outputId}"]`]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  const view = await rendered(expectedRows, expectedColumns, phase);
  const native = await waitNative((entry) => entry.path.endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  const preview = nativePreviewBody(native.response);
  assert.equal(preview.rowCount, expectedRows, `${phase} native preview row count differs from CDA witness count`);
  recordTiming(phase, startedAt, { rows: expectedRows, columns: expectedColumns, receiptId: preview.receiptId });
  return { view, native, preview };
};

const setSearchInput = async (selector, value) => {
  await fill(page, selector, value);
};

const selectRawField = async (path) => {
  const selector = `[aria-label=${JSON.stringify(`Select Observation.${path}`)}]`;
  let present = await browserEval(page, ([__arg0]) => { return Boolean(document.querySelector(__arg0)); }, [selector]);
  if (!present) {
    const search = '[aria-label="Search features by field name, concept, or code"]';
    await setSearchInput(search, path.includes('[]') ? path.split('[]')[0] : path);
    await click(page, '[aria-label="Add columns editor"] form button', { name: 'Search' });
    await waitForBrowser(page, ([__arg0]) => Boolean(Boolean(document.querySelector(__arg0))), [selector]);
    present = true;
  }
  assert(present, `Native raw field catalog did not expose ${path}`);
  const state = await browserEval(page, ([__arg0]) => { const input=document.querySelector(__arg0);return input?{disabled:input.disabled,checked:input.checked}:null; }, [selector]);
  assert(state && !state.disabled, `Native raw field selection is unavailable for ${path}: ${JSON.stringify(state)}`);
  if (!state.checked) await click(page, selector);
};

const openAddColumns = async () => {
  const editorOpen = await browserEval(page, () => { return Boolean(document.querySelector('[data-testid="construction-operation-editor"]')); });
  if (!editorOpen) await click(page, '[data-testid="construction-action-add-columns"]');
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-operation-editor"]'))), []);
  await click(page, '[data-testid="construction-operation-editor"] [aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))), []);
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[data-testid="feature-catalog-raw-fields"]'))), []);
  const rootScope = await browserEval(page, ([__arg0]) => { const button=[...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')].find(item=>item.dataset.sourceKind==='ROOT'&&item.dataset.sourceKey===__arg0);return button?{label:button.getAttribute('aria-label'),selected:button.getAttribute('aria-pressed')==='true'}:null; }, [`root:Observation`]);
  assert(rootScope, 'Native Add columns did not offer the Observation table-row source scope');
  if (!rootScope.selected) await click(page, '[data-testid="construction-add-columns-source-option"]', { name: rootScope.label });
  await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-add-columns-source-option"][data-source-key="root:Observation"]')?.getAttribute('aria-pressed')==='true'), []);
  const rawFieldsOpen = await browserEval(page, () => { return document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true; });
  if (!rawFieldsOpen) await click(page, '[data-testid="feature-catalog-raw-fields"] summary');
};

const addNativeFields = async (candidateByPath, cancelProposal) => {
  const actionStartedAt = Date.now();
  await openAddColumns();
  await selectRawField('component[].valueString');
  await selectRawField('status');
  await waitForBrowser(page, () => Boolean(Boolean([...document.querySelectorAll('[aria-label="Add columns editor"] button')].find(button=>button.innerText.includes('Add 2 selected features')&&!button.disabled))), []);
  const fromIndex = nativeIndex();
  await click(page, '[aria-label="Add columns editor"] button', { includes: 'Add 2 selected features' });
  await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[role="dialog"]')||document.querySelector('[data-testid="construction-choice-proposal-panel"]'))), []);

  const dialog = await browserEval(page, () => { return Boolean(document.querySelector('[role="dialog"]')); });
  if (dialog) {
    const choiceInputs = await browserEval(page, () => { return [...document.querySelectorAll('[role="dialog"] input[type="radio"][aria-label]')].map(input=>({name:input.name,label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled})); });
    const scalar = candidateByPath.get('status');
    const repeated = candidateByPath.get('component[].valueString');
    const nativeForm = (candidate, formLabel, fieldPath) => {
      const choiceId = candidate?.constructionChoice?.choiceId;
      assert(choiceId, `Catalog field ${fieldPath} has no source choice identity`);
      const name = `construction-choice-${choiceId}`;
      const controls = choiceInputs.filter((input) => input.name === name);
      assert(controls.length > 0, `Native choices omitted the choice group for ${fieldPath} (${choiceId}): ${JSON.stringify(choiceInputs)}`);
      const control = controls.find((input) => input.label.endsWith(`: ${formLabel}`));
      assert(control, `Native choices omitted ${formLabel} for ${fieldPath} (${choiceId}): ${JSON.stringify(controls)}`);
      return control;
    };
    const scalarValueChoice = nativeForm(scalar, 'Use the matching value', 'Observation.status');
    const repeatedChoice = nativeForm(repeated, 'Keep all matching values', 'Observation.component[].valueString');
    const nativeChoiceSelector = (control) => `[role="dialog"] input[type="radio"][name=${JSON.stringify(control.name)}][aria-label=${JSON.stringify(control.label)}]`;
    const repeatedSelector = nativeChoiceSelector(repeatedChoice);
    const scalarSelector = nativeChoiceSelector(scalarValueChoice);
    if (!repeatedChoice.checked) await click(page, repeatedSelector);
    if (!scalarValueChoice.checked) await click(page, scalarSelector);
    await waitForBrowser(page, ([__arg0, __arg1]) => Boolean(document.querySelector(__arg0)?.checked===true && document.querySelector(__arg1)?.checked===true), [repeatedSelector, scalarSelector]);
    await waitForBrowser(page, () => Boolean(Boolean([...document.querySelectorAll('[role="dialog"] button')].find(button=>button.innerText.trim()==='Add 2 columns'&&!button.disabled))), []);
    await click(page, '[role="dialog"] button', { name: 'Add 2 columns' });
  }

  await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus==='ready'), []);
  const nativeProposal = await waitNative((entry) => entry.path.endsWith('/construction-choice-proposals') && entry.body?.constructionChoices?.length === 2, fromIndex);
  const proposal = nativeProposal.response;
  assert.equal(proposal.previewStatus, 'READY', 'Native field preflight must carry a successful preview receipt');
  assert.equal(proposal.outputId, outputId);
  assert.deepEqual(proposal.constructionChoices, nativeProposal.body.constructionChoices, 'Native response must confirm the exact selected field choices');
  assert.equal(proposal.candidateColumnIds.length, 2, 'Native field preflight must assign two candidate output IDs');
  assert.equal(proposal.previewDurationMs <= 5000, true, `Native field preflight took ${proposal.previewDurationMs} ms`);
  const expectedForms = new Map(proposal.constructionChoices.map((choice, index) => {
    const candidate = [...candidateByPath.values()].find((item) => item.constructionChoice?.choiceId === choice.choiceId);
    assert(candidate, `Native proposal choice ${choice.choiceId} did not bind a raw field candidate`);
    return [candidate.fieldPath, { form: choice.form, columnId: proposal.candidateColumnIds[index], candidateId: candidate.candidateId, title: choice.title }];
  }));
  assert.equal(expectedForms.get('status')?.form, 'VALUE', 'Scalar source status must use its direct VALUE form');
  assert.equal(expectedForms.get('component[].valueString')?.form, 'ALL', 'Repeated source values must use the direct ALL list form');
  assert.equal(proposal.preview.rows.length, sourceRecords.length, 'Native preflight must retain every selected source Observation');
  const proposalById = new Map(proposal.preview.rows.map((row) => [row[rootColumnId], row]));
  assert.deepEqual([...proposalById.keys()].sort(), sourceRecords.map((row) => row.id).sort(), 'Native preflight must retain the raw selected Observation IDs');
  for (const source of sourceRecords) {
    const row = proposalById.get(source.id);
    assert.equal(typeof row[rootColumnId], 'string');
    assert.equal(row[expectedForms.get('status').columnId], source.status, `Native preflight status differs for ${source.id}`);
    assert.equal(typeof row[expectedForms.get('status').columnId], 'string');
    assert.deepEqual(row[expectedForms.get('component[].valueString').columnId], source.componentValues, `Native preflight component list differs for ${source.id}`);
    assert(Array.isArray(row[expectedForms.get('component[].valueString').columnId]));
  }
  recordAssertion('native Add columns resolved a direct scalar VALUE and repeated ALL field batch against raw CDA', {
    choices: [...expectedForms.entries()].map(([path, choice]) => ({ path, ...choice })),
    previewReceiptId: proposal.preview.receiptId,
    rowCount: proposal.preview.rows.length,
  });

  if (cancelProposal) {
    const workspaceBeforeCancel = structuredClone(builder.workspace);
    const commandCountBeforeCancel = nativeRequests.slice(fromIndex).filter((entry) => entry.path.endsWith('/commands')).length;
    await click(page, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-choice-proposal-panel"]')), []);
    builder = await api(`${base}/builder`);
    assert.deepEqual(builder.workspace, workspaceBeforeCancel, 'Canceling the native proposed fields must preserve the saved workspace');
    assert.equal(nativeRequests.slice(fromIndex).filter((entry) => entry.path.endsWith('/commands')).length, commandCountBeforeCancel,
      'Canceling native field preflight must not send a save command');
    recordAssertion('native proposal Cancel leaves the draft and source population unchanged', { draftDigest: builder.draftDigest, commandCountAfterCancel: commandCountBeforeCancel });
    recordTiming('native Add columns proposal cancellation', actionStartedAt, { choices: 2, savedCommandCount: commandCountBeforeCancel });
    return expectedForms;
  }

  await click(page, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  const applyCommand = await waitNative((entry) => entry.path.endsWith('/commands') &&
    entry.body?.commands?.length === 2 && entry.body.commands.every((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'), fromIndex);
  assert.equal(applyCommand.body.commandId, proposal.commandId, 'Native Add columns save must consume the preflight command identity');
  const choiceIdentity = (choice) => Object.fromEntries(['choiceId', 'form', 'frameId', 'rowValuePolicy']
    .filter((key) => choice?.[key] !== undefined).map((key) => [key, choice[key]]));
  assert.deepEqual(applyCommand.body.commands.map((item) => choiceIdentity(item.constructionChoice)),
    proposal.constructionChoices.map(choiceIdentity),
    'Native save must preserve each preflight choice identity, form, frame, and row-value policy');
  assert.deepEqual(applyCommand.body.commands.map((item) => item.title ?? null),
    proposal.constructionChoices.map((choice) => choice.title ?? null),
    'Native save must preserve field presentation titles separately from choice identity');
  builder = await api(`${base}/builder`);
  recordTiming('native Add columns apply', actionStartedAt, { choices: 2, columns: document().columns.length, nativeCommandStatus: applyCommand.status });
  return expectedForms;
};

const saveDOM = async name => cda.attachReport(`${name}.json`, await cda.inspect(() => ({ url: location.href, title: document.title, text: document.body.innerText.slice(-16000) })));


report.target ??= { ...cda.report.target };
report.nativeRequests = cda.report.nativeRequests;
report.errors = cda.report.errors;

  try {
sourceRecords = boundedRawOracle();
if (sourceRecords.length < 2) {
  report.gaps.push({ assertion: 'bounded raw CDA witness with two or more Observation IDs, populated status, and at least two repeated component values each', status: 'untested', reason: `The 1000-row raw scan selected ${sourceRecords.length} eligible Observation(s); the native lifecycle requires at least two.` });
  report.final = { status: 'untested', reason: report.gaps[0].reason };
} else {
  assert(sourceRecords.length <= 3);
  assert(sourceRecords.every((record) => record.resourceType === 'Observation' && record.generation === generation));
  report.oracle.expectedTypes = { id: 'string', status: 'string', 'component[].valueString': 'array<string>' };
  recordAssertion('bounded independent raw CDA oracle selected unique populated Observation witnesses', {
    limit: 1000,
    selected: sourceRecords.map(({ id, status, componentValues }) => ({ id, status, componentValues })),
    maxSelectedIds: 3,
    maxRepeatedValues: 6,
  });

  await api(projectRoot, { name: explorer, title: 'CDA direct source field lifecycle QA' });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation, 'Fresh QA Explorer must use the requested CDA generation');
  const observationNode = builder.catalog.nodes.find((node) => node.resourceType === 'Observation');
  assert(observationNode, 'CDA catalog has no Observation root');
  const candidates = builder.catalog.candidates.filter((candidate) => candidate.nodeId === observationNode.nodeId);
  const idCandidate = candidates.find((candidate) => candidate.fieldPath === 'id');
  const statusCandidate = candidates.find((candidate) => candidate.fieldPath === 'status');
  const repeatedCandidate = candidates.find((candidate) => candidate.fieldPath === 'component[].valueString');
  assert(idCandidate, 'Observation.id is unavailable as the stable baseline identity field');
  assert(statusCandidate, 'Observation.status is unavailable as the scalar source field');
  assert(repeatedCandidate, 'Observation.component[].valueString is unavailable as the repeated source field');
  assert.equal(statusCandidate.logicalType, 'string');
  assert.equal(statusCandidate.repeated, false);
  assert.equal(repeatedCandidate.logicalType, 'string');
  assert.equal(repeatedCandidate.repeated, true);
  assert(repeatedCandidate.projectionModes.includes('ALL'), 'The repeated source candidate must advertise ALL list projection');
  for (const candidate of [statusCandidate, repeatedCandidate]) {
    const source = candidate.constructionChoice?.source;
    assert.equal(source?.kind, 'FIELD', `${candidate.fieldPath} must be a direct field choice`);
    assert.equal(source?.resourceType, 'Observation', `${candidate.fieldPath} must belong to Observation`);
    assert.equal(source?.nodeId, observationNode.nodeId, `${candidate.fieldPath} must bind to the selected Observation node`);
    assert.equal(source?.candidateId, candidate.candidateId, `${candidate.fieldPath} source identity must match its candidate`);
    assert.equal(source?.path, candidate.fieldPath, `${candidate.fieldPath} source identity must match its exact path`);
    assert.equal(candidate.constructionChoice?.route?.length ?? 0, 0, `${candidate.fieldPath} must have an empty direct-source route`);
  }

  await command([{ type: 'CREATE_TABLE', title: 'Direct source field rows', rootNodeId: observationNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
  selection = await api(`${projectRoot}/${encodeURIComponent(explorer)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cda-source-fields-${explorer}`,
    source: { kind: 'resources', resources: { refs: sourceRecords.map((record) => ({ project, generation, resourceType: 'Observation', id: record.id })) } },
  });
  const populationRoutes = await api(`${base}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  });
  const directRoute = populationRoutes.choices.find((choice) => choice.route.length === 0);
  assert(directRoute, 'Scoped CDA selection has no direct Observation row route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const baseline = document();
  assert(baseline, 'Fresh QA table disappeared');
  assert.equal(baseline.rootResourceType, 'Observation');
  assert.equal(baseline.rows.kind, 'RECORDS', 'Direct source field QA must begin with one row per source Observation');
  assert.equal(baseline.population.selectionRevisionId, selection.id);
  assert.equal(baseline.population.route?.length ?? 0, 0);
  assert.equal(baseline.columns.length, 1);
  rootColumnId = columnIdOf(baseline.columns[0]);
  assert.equal(baseline.columns[0].source.field.path, 'id');
  originalWorkspace = structuredClone(builder.workspace);
  report.seed = {
    outputId,
    selectionRevisionId: selection.id,
    rootResourceType: baseline.rootResourceType,
    rows: baseline.rows,
    population: baseline.population,
    identityColumnId: rootColumnId,
    identityCandidateId: idCandidate.candidateId,
    setupMethod: 'bounded API bootstrap; the subsequent Add columns and lifecycle assertions use native browser interactions',
  };

  browserEvents = trackBrowser();
  const initial = await openTable(sourceRecords.length, 1, 'initial scoped Observation table');
  const baselineDOM = await cda.inspect(() => { return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),cells:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim())}; }, {});
  assert.equal(initial.native.status, 200);
  assert.deepEqual([...baselineDOM.cells].sort(), sourceRecords.map((record) => record.id).sort(), 'Native base table must render the exact selected Observation IDs');
  recordAssertion('native base table rendered the independently selected Observation membership', { receiptId: initial.preview.receiptId, ids: baselineDOM.cells });

  const candidateByPath = new Map([
    ['status', statusCandidate],
    ['component[].valueString', repeatedCandidate],
  ]);
  await addNativeFields(candidateByPath, true);
  report.cancelledProposal = { status: 'passed', restoredDraftDigest: builder.draftDigest };

  const forms = await addNativeFields(candidateByPath, false);
  let savedDocument = document();
  assert.equal(savedDocument.columns.length, 3, 'Saved native field addition must retain the identity and two selected field columns');
  assert.equal(savedDocument.rows.kind, 'RECORDS', 'Direct fields must not replace or expand the source row definition');
  assert.equal(savedDocument.population.selectionRevisionId, selection.id);
  assert.equal(savedDocument.population.route?.length ?? 0, 0);
  const statusColumn = fieldColumn(builder, 'status');
  const repeatedColumn = fieldColumn(builder, 'component[].valueString');
  const statusBinding = assertDirectFieldBinding(statusColumn, statusCandidate, 'VALUE', 'Saved scalar source field');
  const repeatedBinding = assertDirectFieldBinding(repeatedColumn, repeatedCandidate, 'ALL', 'Saved repeated source field');
  assert.equal(repeatedColumn.logicalType, 'string');
  assert.equal(repeatedCandidate.cardinality, 'many');
  assert.equal(forms.get('status').candidateId, statusCandidate.candidateId);
  assert.equal(forms.get('component[].valueString').candidateId, repeatedCandidate.candidateId);
  assert.equal(statusColumn.label, forms.get('status').title, 'Saved scalar presentation label must match the native field choice title');
  assert.equal(repeatedColumn.label, forms.get('component[].valueString').title, 'Saved repeated presentation label must match the native field choice title');
  report.savedBindings = { status: statusBinding, repeated: repeatedBinding, sourcePopulation: savedDocument.population, rowDefinition: savedDocument.rows };
  recordAssertion('saved Builder state binds both direct fields to the base Observation and retains source membership', report.savedBindings);

  const beforeRename = {
    status: structuredClone(statusColumn.source.field),
    repeated: structuredClone(repeatedColumn.source.field),
    statusColumnId: columnIdOf(statusColumn),
    repeatedColumnId: columnIdOf(repeatedColumn),
  };
  const renameTo = 'CDA component values QA';
  const renameStart = nativeIndex();
  const renameStartedAt = Date.now();
  await nativeClick(page, 'button', { name: 'Columns' });
  const renameSelector = `[aria-label=${JSON.stringify(`Column name for ${repeatedColumn.label}`)}]`;
  await waitForBrowser(page, ([__arg0]) => Boolean(Boolean(document.querySelector(__arg0))), [renameSelector], 5000);
  const renameInput = page.locator(renameSelector);
  await nativeFill(page, renameSelector, renameTo, {});
  await press(page, renameSelector, 'Enter');
  const renameCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'UPDATE_COLUMN'), renameStart);
  builder = await api(`${base}/builder`);
  savedDocument = document();
  const renamedRepeated = fieldColumn(builder, 'component[].valueString');
  assert.equal(renamedRepeated.label, renameTo, 'Native presentation edit must persist the requested label');
  assert.equal(columnIdOf(renamedRepeated), beforeRename.repeatedColumnId, 'Presentation edits must preserve the repeated output column ID');
  assert.deepEqual(renamedRepeated.source.field, beforeRename.repeated, 'Presentation edits must preserve the exact repeated source binding');
  assert.deepEqual(fieldColumn(builder, 'status').source.field, beforeRename.status, 'Editing one presentation must preserve the scalar source binding');
  assert(renameCommand.body.commands.some((item) => item.type === 'UPDATE_COLUMN' && item.column === beforeRename.repeatedColumnId), 'Native edit must update the selected authored column');
  recordAssertion('native presentation edit changes the label while preserving column and field binding identities', {
    label: renamedRepeated.label, columnId: columnIdOf(renamedRepeated), field: renamedRepeated.source.field,
  });
  recordTiming('native presentation edit', renameStartedAt, { columnId: columnIdOf(renamedRepeated), label: renameTo, commandStatus: renameCommand.status });

  const afterSave = await openTable(sourceRecords.length, 3, 'reload after native direct-field save and presentation edit');
  const loadedDocument = document(await api(`${base}/builder`));
  const loadedStatus = fieldColumn({ workspace: { documents: [loadedDocument] } }, 'status');
  const loadedRepeated = fieldColumn({ workspace: { documents: [loadedDocument] } }, 'component[].valueString');
  const savedValueEvidence = assertPreview(afterSave.preview, loadedDocument, ['status', 'component[].valueString'], 'Saved native field reload');
  const rowIds = new Set(sourceRecords.map((record) => record.id));
  const domRows = afterSave.view.rows;
  const idIndex = afterSave.view.headers.findIndex((label) => renderedHeaderMatches(label, 'Observation ID'));
  const statusIndex = afterSave.view.headers.findIndex((label) => renderedHeaderMatches(label, statusColumn.label));
  const repeatedIndex = afterSave.view.headers.findIndex((label) => renderedHeaderMatches(label, renameTo));
  assert(idIndex >= 0 && statusIndex >= 0 && repeatedIndex >= 0, `Rendered native table omitted expected source headers: ${JSON.stringify(afterSave.view.headers)}`);
  assert.deepEqual(domRows.map((row) => row[idIndex].text).sort(), [...rowIds].sort(), 'Rendered native table must show exact CDA Observation IDs');
  for (const source of sourceRecords) {
    const rowIndex = sourceRecords.findIndex((record) => record.id === source.id);
    const matchingDOM = domRows.find((row) => row[idIndex].text === source.id);
    assert(matchingDOM, `Rendered native table omitted ${source.id}`);
    assert.equal(matchingDOM[statusIndex].text, source.status, `Rendered scalar source field differs for ${source.id}`);
    for (const value of source.componentValues) assert(matchingDOM[repeatedIndex].text.includes(value), `Rendered repeated source field omitted ${value} for ${source.id}`);
    assert(rowIndex >= 0);
  }
  assert.equal(loadedDocument.population.selectionRevisionId, selection.id, 'Reload must persist the exact selected CDA population');
  assert.equal(loadedDocument.population.route?.length ?? 0, 0, 'Reload must preserve direct Observation rows');
  assert.equal(loadedStatus.source.field.path, 'status');
  assert.equal(loadedRepeated.label, renameTo);
  assert.equal(loadedRepeated.source.field.projectionMode, 'ALL');
  recordAssertion('reload renders exact raw scalar/list values and retains saved types, source IDs, and population', {
    ...savedValueEvidence,
    statusType: typeof sourceRecords[0].status,
    repeatedType: 'array<string>',
    bindings: { status: loadedStatus.source.field, repeated: loadedRepeated.source.field },
    population: loadedDocument.population,
  });
  await saveDOM('saved-direct-fields-after-rename');

  for (const target of [loadedRepeated, loadedStatus]) {
    const current = document();
    const targetColumnId = columnIdOf(target);
    const currentColumn = current.columns.find((column) => columnIdOf(column) === targetColumnId);
    assert(currentColumn, `Column ${target.label} disappeared before native removal`);
    const currentColumnId = columnIdOf(currentColumn);
    const fromIndex = nativeIndex();
    const removalStartedAt = Date.now();
    const columnsMenuOpen = await cda.inspect(() => { return Boolean(document.querySelector('[aria-label="Table columns"]')); }, {});
    if (!columnsMenuOpen) await nativeClick(page, 'button', { name: 'Columns' });
    await waitForBrowser(page, () => Boolean(Boolean(document.querySelector('[aria-label="Table columns"]'))), [], 5000);
    await nativeClick(page, '[aria-label="Table columns"] button', { name: `Remove ${currentColumn.label} column` });
    const removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.body?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === currentColumnId), fromIndex);
    assert.equal(removeCommand.status, 200);
    builder = await api(`${base}/builder`);
    const afterRemove = document();
    const remainingColumnIds = current.columns.filter((column) => columnIdOf(column) !== currentColumnId).map(columnIdOf);
    assert.deepEqual(afterRemove.columns.map(columnIdOf), remainingColumnIds, 'Native removal must remove exactly the selected authored output column');
    assert.equal(afterRemove.population.selectionRevisionId, selection.id);
    assert.equal(afterRemove.rows.kind, 'RECORDS');
    await rendered(sourceRecords.length, afterRemove.columns.length, `native removal of ${currentColumn.label}`);
    recordTiming(`native removal of ${currentColumn.label}`, removalStartedAt, { columnId: columnIdOf(currentColumn), commandStatus: removeCommand.status, remainingColumns: afterRemove.columns.length });
    report.removals ??= [];
    report.removals.push({ label: currentColumn.label, columnId: currentColumnId, remainingColumns: afterRemove.columns.map(columnIdOf) });
  }
  builder = await api(`${base}/builder`);
  const restoredDocument = document();
  assert.deepEqual(builder.workspace, originalWorkspace, 'Removing both added source fields must restore the exact original workspace and population');
  assert.equal(restoredDocument.columns.length, 1);
  assert.equal(restoredDocument.columns[0].column, rootColumnId);
  assert.equal(restoredDocument.population.selectionRevisionId, selection.id);
  assert.equal(restoredDocument.population.route?.length ?? 0, 0);
  const restored = await openTable(sourceRecords.length, 1, 'reload after removing direct fields restores original population');
  const restoredDocumentAfterReload = document(await api(`${base}/builder`));
  assertPreview(restored.preview, restoredDocumentAfterReload, [], 'Restored source population reload');
  const restoredIds = await cda.inspect(() => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="cell"]')].map(cell=>cell.innerText.trim()); }, {});
  assert.deepEqual(restoredIds.sort(), sourceRecords.map((record) => record.id).sort(), 'Restored native table must retain exact Observation membership after reload');
  recordAssertion('native removal and reload restore the original ID field, direct RECORDS rows, and exact CDA population', {
    outputId,
    rootColumnId,
    selectionRevisionId: selection.id,
    restoredIds,
    columns: restoredDocumentAfterReload.columns.map((column) => ({ column: column.column, path: column.source?.field?.path })),
  });
  await saveDOM('restored-original-population-after-remove-reload');

  cda.includeBrowserDiagnostics();
  assert.deepEqual(report.errors, [], `Browser reported unexpected errors: ${JSON.stringify(report.errors)}`);
  assert(nativeRequests.every((entry) => !entry.path.includes(protectedExplorer)), 'Browser must never request the protected Explorer');
  report.final = {
    status: 'passed',
    outputId,
    rootResourceType: 'Observation',
    selectedIds: sourceRecords.map((record) => record.id),
    addedFields: [statusBinding, repeatedBinding],
    restoredPopulation: { selectionRevisionId: selection.id, rowCount: sourceRecords.length, columnCount: restoredDocumentAfterReload.columns.length },
    nativeRequestCount: nativeRequests.length,
  };
}
  } finally {
    try { await browserEvents?.flush(); } catch (error) { report.requestFlushError = String(error); }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
  }
}
