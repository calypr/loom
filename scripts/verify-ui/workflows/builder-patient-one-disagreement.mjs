import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openFreshPatientTable } from './builder-group-entry.mjs';
import { findRenderedBuilderHeaderIndex, waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';
import { observeAuthoringAttempt } from '../helpers/fast-fixtures.mjs';
import { createPolicyOneTransportObserver } from '../helpers/policy-one-transport-observer.mjs';
import { sanitizePayload } from '../helpers/playwright-browser.mjs';
import { buildPolicyOneTrace } from '../helpers/policy-one-trace.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const renderedTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';

export const isNativeGroupProposalReady = ({ expectedCount }, doc = globalThis.document) => {
  const panel = doc.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = doc.querySelector('[data-testid="construction-proposal-preview"]');
  const summary = doc.querySelector('select[aria-label="Summary 1"]');
  const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
    .map(cell => (cell.querySelector('span')?.innerText ?? cell.querySelector('span')?.textContent
      ?? cell.innerText ?? cell.textContent ?? '').replace(/\s+/g, ' ').trim().toLowerCase());
  const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])];
  const cells = [...(rows[0]?.querySelectorAll('td') ?? [])]
    .map(cell => (cell.innerText ?? cell.textContent ?? '').replace(/\s+/g, ' ').trim());
  const countIndex = headers.findIndex(header => header === 'row count');
  return Boolean(panel?.dataset.proposalStatus === 'ready'
    && preview?.dataset.previewStatus === 'ready'
    && summary?.value === 'COUNT_ROWS'
    && countIndex >= 0
    && cells.length === headers.length
    && cells[countIndex] === expectedCount
    && rows.length === 1);
};

export const readNativeGroupSummary = page =>
  page.getByRole('combobox', { name: 'Summary 1', exact: true }).inputValue();

export const readNativeGroupProposal = (doc = globalThis.document) => {
  const panel = doc.querySelector('[data-testid="construction-proposal-panel"]');
  const ready = panel?.querySelector('[data-testid="construction-proposal-ready"]');
  const editor = doc.querySelector('[data-testid="construction-reshape-editor"]');
  const preview = doc.querySelector('[data-testid="construction-proposal-preview"]');
  const table = preview?.querySelector('table');
  const text = element => (element?.innerText ?? element?.textContent ?? '').replace(/\s+/g, ' ').trim();
  const headers = [...(table?.querySelectorAll('thead th') ?? [])]
    .map(cell => text(cell.querySelector('span') ?? cell));
  const rows = [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
    .map(row => [...row.querySelectorAll('td')].map(cell => ({ text: text(cell), raw: cell.getAttribute('title') })));
  const groupKeys = [...(editor?.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]') ?? [])]
    .filter(input => input.checked).map(input => input.getAttribute('aria-label'));
  return { proposalSummary: text(ready?.querySelector('p')), headers, rows, groupKeys };
};

export const readPatientOnePreviewBinding = (doc = globalThis.document) => {
  const preview = doc.querySelector('[data-testid="construction-preview"]');
  if (!preview) return null;
  const draftVersion = Number(preview.getAttribute('data-current-draft-version'));
  return {
    status: preview.getAttribute('data-preview-status'),
    receiptId: preview.getAttribute('data-preview-receipt-id'),
    outputId: preview.getAttribute('data-preview-output-id'),
    draftVersion: Number.isInteger(draftVersion) ? draftVersion : null,
    draftDigest: preview.getAttribute('data-current-draft-digest'),
  };
};

export const capturePatientOnePreOneState = ({ builder, previewBinding, renderedGrid, project, explorerId, outputId }) => {
  const savedDocument = builder?.workspace?.documents?.find(document => document.output?.id === outputId);
  assert(savedDocument, `Builder state omitted saved output ${outputId}`);
  const groupStep = savedDocument.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(groupStep?.id, `Saved output ${outputId} has no applied Group step`);
  assert.equal(builder.kind, 'ExplorerBuilderState', 'pre-ONE state must come from the native BuilderState endpoint');
  assert(Number.isInteger(builder.draftVersion) && builder.draftVersion > 0, 'saved draft version must be present');
  assert(builder.draftDigest, 'saved draft digest must be present');
  assert(builder.catalog?.generation, 'saved Builder catalog generation must be present');
  assert(builder.catalog?.snapshotToken, 'saved Builder catalog snapshot must be present');
  assert.equal(previewBinding?.status, 'ready', 'saved Group output preview must be ready before the ONE request');
  assert(previewBinding.receiptId, 'saved Group output preview must expose its actual receipt identity');
  assert.equal(previewBinding.outputId, outputId, 'saved Group preview must bind the same output');
  assert.equal(previewBinding.draftVersion, builder.draftVersion, 'saved preview must bind the Builder draft version');
  assert.equal(previewBinding.draftDigest, builder.draftDigest, 'saved preview must bind the Builder draft digest');
  assert(Array.isArray(renderedGrid?.headers) && Array.isArray(renderedGrid?.rows), 'saved Group rendered grid is required');

  return sanitizePayload({
    schemaVersion: 1,
    capturePoint: 'saved Builder state immediately before the raw Patient.id ONE proposal',
    project,
    explorerId,
    draftBinding: {
      version: builder.draftVersion,
      digest: builder.draftDigest,
      generation: builder.catalog.generation,
      snapshotToken: builder.catalog.snapshotToken,
    },
    outputBinding: {
      outputId,
      title: savedDocument.output.title,
      rootResourceType: savedDocument.rootResourceType,
      receiptId: previewBinding.receiptId,
      previewStatus: previewBinding.status,
    },
    savedDocument,
    renderedPreview: {
      outputId: previewBinding.outputId,
      receiptId: previewBinding.receiptId,
      draftVersion: previewBinding.draftVersion,
      draftDigest: previewBinding.draftDigest,
      headers: renderedGrid.headers,
      rows: renderedGrid.rows,
    },
  });
};

export const patientOneSavedState = (builder, outputId) => {
  const document = builder?.workspace?.documents?.find(item => item.output?.id === outputId);
  assert(document, `Builder state omitted saved output ${outputId}`);
  const groupStep = document.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  return sanitizePayload({
    outputId,
    rootResourceType: document.rootResourceType,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    snapshotToken: builder.catalog?.snapshotToken,
    workspace: builder.workspace,
    sourceColumns: document.columns?.map(column => ({
      id: column.columnId ?? column.id,
      label: column.label,
      sourcePath: column.source?.field?.path ?? null,
      projectionMode: column.source?.field?.projectionMode ?? null,
    })) ?? [],
    group: groupStep ? {
      stepId: groupStep.id,
      keys: groupStep.operation.group?.keys?.map(key => key.inputColumnId) ?? [],
      aggregate: groupStep.operation.group?.aggregates?.[0]?.operation ?? null,
      rowValues: groupStep.rowValues ?? [],
      outputs: groupStep.outputs?.map(output => ({ id: output.id, type: output.type })) ?? [],
    } : null,
  });
};

const policyGroupProjection = (savedState, { inputColumnId, renderedRows }) => {
  const group = savedState.group;
  if (!group) return {
    present: false, keys: [], aggregate: 'NO_AGGREGATE', count: 0,
    rowValuePolicy: 'NONE', inputColumnId: 'NoColumn',
    outputColumnBinding: { columnId: 'NoColumn', type: 'NoType' },
    inputColumnBinding: null,
  };
  const rowValue = inputColumnId
    ? group.rowValues.find(value => value.inputColumnId === inputColumnId) ?? null
    : group.rowValues[0] ?? null;
  if (inputColumnId) {
    assert(rowValue, `saved Group omitted row-value input ${inputColumnId}`);
  }
  const inputColumn = rowValue
    ? savedState.sourceColumns.find(column => column.id === rowValue.inputColumnId)
    : null;
  const outputColumn = rowValue
    ? group.outputs.find(column => column.id === rowValue.outputColumnId)
    : null;
  if (rowValue) {
    assert.notEqual(rowValue.inputColumnId, rowValue.outputColumnId,
      'saved Group row values must keep their input and output column IDs distinct');
    assert(inputColumn, 'saved Group row-value input must identify a saved source column');
    assert(outputColumn, 'saved Group row-value output must be declared by the Group step');
    if (rowValue.policy === 'ALL') {
      assert.equal(outputColumn.type, 'array', 'saved ALL row-value outputs must be declared as arrays');
    } else if (rowValue.policy === 'ONE') {
      assert.notEqual(outputColumn.type, 'array', 'saved ONE row-value outputs must be scalar');
    }
  }
  const rows = renderedRows?.rows ?? renderedRows?.cells ?? [];
  const countIndex = findRenderedBuilderHeaderIndex(renderedRows?.headers ?? [], 'Row count');
  const countCell = countIndex < 0 ? null : rows[0]?.[countIndex];
  const count = countCell === null ? null : Number(countCell?.text ?? countCell);
  assert(Number.isInteger(count) && count > 0, 'saved Group projection must bind its observed row count');
  return {
    present: true,
    keys: group.keys,
    aggregate: group.aggregate,
    rowCount: rows.length,
    count,
    rowValuePolicy: rowValue?.policy ?? 'NONE',
    inputColumnId: rowValue?.inputColumnId ?? 'NoColumn',
    outputColumnBinding: outputColumn
      ? { columnId: outputColumn.id, type: outputColumn.type }
      : { columnId: 'NoColumn', type: 'NoType' },
    inputColumnBinding: inputColumn ? {
      columnId: inputColumn.id,
      field: `${savedState.rootResourceType}.${inputColumn.sourcePath}`,
    } : null,
  };
};

export const policyApplyTraceExpectation = ({ savedState, inputColumnId, renderedRows, status }) => {
  const group = policyGroupProjection(savedState, { inputColumnId, renderedRows });
  return {
    policy: group.rowValuePolicy,
    status,
    inputColumnBinding: group.inputColumnBinding,
    outputColumnBinding: group.outputColumnBinding,
  };
};

export const policyWorkspaceProjection = ({ savedState, renderedRows, sourceState, fixtureIDs, addedColumnId }) => {
  const rows = renderedRows.rows ?? renderedRows.cells ?? [];
  const sourceColumns = sourceState.sourceColumns.filter(column => column.sourcePath !== null);
  const sourceColumnIds = sourceColumns.map(column => column.id);
  const sourceBindings = sourceColumns.map(column => `${column.id}|${sourceState.rootResourceType}.${column.sourcePath}`);
  const sourceIDIndex = findRenderedBuilderHeaderIndex(renderedRows.headers, sourceColumns
    .find(column => column.sourcePath === 'id')?.label);
  const renderedPatientIDs = sourceIDIndex < 0
    ? []
    : rows.flatMap(row => {
      const cell = row[sourceIDIndex];
      if (cell?.raw && typeof cell.raw === 'string' && cell.raw.startsWith('[')) {
        return JSON.parse(cell.raw);
      }
      return [cell?.text ?? cell];
    });
  const addedBindings = savedState.sourceColumns
    .filter(column => !sourceColumnIds.includes(column.id) && column.sourcePath !== null)
    .map(column => `${column.id}|${sourceState.rootResourceType}.${column.sourcePath}`);
  const group = policyGroupProjection(savedState, { inputColumnId: addedColumnId, renderedRows });
  const rowCount = rows.length;
  return {
    documentId: savedState.outputId,
    rootResourceType: 'Patient',
    sourceColumnIds,
    sourceBindings,
    sourceRows: { patientIds: [...fixtureIDs], count: fixtureIDs.length },
    additionalBindings: addedBindings,
    group,
    renderedRows: group.rowValuePolicy === 'ALL'
      ? [{ patientIds: sorted(renderedPatientIDs), count: group.count }]
      : group.present
        ? [{ count: group.count }]
        : { patientIds: sorted(renderedPatientIDs), count: rowCount },
  };
};

const editorTraceObservation = state => ({
  documentId: state.editorDocumentId,
  editorId: state.editorMountIdentity,
  documentTimeOrigin: state.documentTimeOrigin,
  mounted: state.editorOpen,
  selectedField: state.selectedField,
  fieldSelected: state.fieldSelected,
  policy: state.policy,
  availablePolicies: state.availablePolicies,
  errorVisible: state.errorVisible,
});

const policyRequestCapture = (started, body, transportIndex) => ({
  ...started.request,
  requestObject: started.rawRequest,
  transportIndex,
  body,
});

const policyResponseCapture = (started, exchange, body, extra = {}) => ({
  ...exchange.response,
  errorCode: exchange.response?.errorCode ?? null,
  requestObject: started.rawRequest,
  responseObject: exchange.rawResponse,
  body,
  ...extra,
});

export const readPatientOneRenderedGrid = (doc = globalThis.document) => {
  const table = doc.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  const rows = [...(table?.querySelectorAll('[role="row"]') ?? [])];
  const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
    .map(cell => (cell.innerText ?? cell.textContent ?? '').trim());
  const cells = rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => ({
    text: (cell.innerText ?? cell.textContent ?? '').trim(),
    raw: cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title') ?? null,
  })));
  return { headers, cells };
};

export const readPatientOneChoiceEditor = (doc = globalThis.document) => {
  const editor = doc.querySelector('[aria-label="Add columns editor"]');
  const policy = editor?.querySelector('select[aria-label="Values per grouped row"]');
  const field = editor?.querySelector('[data-testid="feature-catalog-raw-fields"] button[aria-label="Select Patient.id"]');
  const actionError = editor?.querySelector('[aria-label="Selected feature action"] [role="alert"]');
  const view = doc.defaultView;
  let editorMountIdentity = null;
  if (editor && view) {
    const registryKey = '__loomPolicyOneEditorIdentityRegistry';
    let registry = view[registryKey];
    if (!registry) {
      registry = { next: 0, ids: new WeakMap() };
      Object.defineProperty(view, registryKey, { configurable: false, enumerable: false, value: registry });
    }
    editorMountIdentity = registry.ids.get(editor) ?? `editor-${++registry.next}`;
    registry.ids.set(editor, editorMountIdentity);
  }
  return {
    documentTimeOrigin: view?.performance?.timeOrigin ?? null,
    editorMountIdentity,
    editorDocumentId: editor?.getAttribute('data-output-id') ?? null,
    selectedField: field?.getAttribute('aria-pressed') === 'true'
      ? field.getAttribute('aria-label')?.replace(/^Select\s+/, '') ?? null
      : null,
    editorOpen: Boolean(editor),
    fieldSelected: field?.getAttribute('aria-pressed') === 'true',
    policy: policy?.value ?? null,
    availablePolicies: [...(policy?.options ?? [])].map(option => option.value),
    allAvailable: [...(policy?.options ?? [])].some(option => option.value === 'ALL'),
    errorVisible: Boolean(actionError),
    errorText: (actionError?.innerText ?? actionError?.textContent ?? '').replace(/\s+/g, ' ').trim(),
  };
};

export const markPolicyOneExpectedHttp422 = ({
  networkObserver,
  report,
  request,
  response,
  requestCaptureId,
  requestBody,
  responseBody,
}) => {
  assert.equal(response.request(), request,
    'the expected Policy ONE response must belong to its exact submitted request');
  assert.equal(response.url(), request.url(),
    'the expected Policy ONE response must retain the exact submitted request URL');
  assert.equal(response.status(), 422,
    'the expected Policy ONE response must retain its native 422 status');
  assert.equal(typeof requestCaptureId, 'string');
  assert(requestCaptureId.length > 0);
  assert.equal(typeof requestBody?.commandId, 'string');
  assert(requestBody.commandId.length > 0);
  assert.equal(requestBody.constructionChoices?.length, 1,
    'the expected Policy ONE request must carry one selected construction choice');
  const choice = requestBody.constructionChoices[0];
  assert.equal(choice.rowValuePolicy, 'ONE');
  assert.equal(typeof choice.choiceId, 'string');
  assert(choice.choiceId.length > 0);
  assert.equal(responseBody?.error?.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');

  const entry = networkObserver?.byRequest?.get(request);
  assert(entry, 'the fixture observer must retain the exact submitted Playwright Request object');
  assert.equal(entry.method, request.method().toUpperCase());
  const requestURL = new URL(request.url());
  assert.equal(entry.url, `${requestURL.origin}${requestURL.pathname}`);
  assert.deepEqual(networkObserver.requestBody(entry), sanitizePayload(requestBody),
    'fixture network evidence must retain the exact native ONE request body after its standard redaction');
  assert.equal(entry.status, 422);
  assert.equal(entry.failure?.kind, 'http');
  assert.equal(entry.networkComplete, true);
  assert.equal(entry.responseCapture, 'captured');
  assert.equal(entry.responseCaptureComplete, true);
  assert.equal(entry.responseBodyCaptured, true);
  assert.deepEqual(networkObserver.responseBody(entry), sanitizePayload(responseBody),
    'fixture network evidence must capture the body from the exact native 422');
  assert.equal(networkObserver.failures.filter(failure => failure.entry === entry && failure.kind === 'http').length, 1,
    'the exact native 422 must appear once in the fixture HTTP failure stream before classification');

  const diagnostic = entry.reportNetwork;
  assert(diagnostic, 'the exact native 422 must retain its fixture report diagnostic');
  assert.equal(diagnostic.kind, 'http');
  assert.equal(diagnostic.url, entry.url);
  assert.equal(diagnostic.method, entry.method);
  assert.equal(diagnostic.status, 422);
  assert.equal(diagnostic.browserRequestId, entry.browserRequestId);
  assert.equal(diagnostic.observerRequestIndex, entry.index);
  const reportDiagnostics = report.network.filter(record => record.kind === 'http'
    && record.browserRequestId === entry.browserRequestId
    && record.observerRequestIndex === entry.index);
  assert.equal(reportDiagnostics.length, 1,
    'fixture network evidence must contain one HTTP diagnostic for the exact physical request');
  assert.strictEqual(reportDiagnostics[0], diagnostic);
  const reportFailures = report.errors.filter(record => record.kind === 'http'
    && record.browserRequestId === entry.browserRequestId);
  assert.equal(reportFailures.length, 1,
    'fixture error evidence must contain one HTTP failure for the exact physical request');
  assert.strictEqual(reportFailures[0], entry.reportError);

  const classification = networkObserver.markExpectedValidation(entry, {
    status: 422,
    reason: 'the native Patient.id ONE request was checked against its exact request and response evidence',
    proof: {
      requestCaptureId,
      commandId: requestBody.commandId,
      outputId: requestBody.outputId,
      choiceId: choice.choiceId,
      rowValuePolicy: choice.rowValuePolicy,
      responseErrorCode: responseBody.error.code,
    },
  });
  assert.equal(entry.httpFailureExempted, true);
  assert.equal(entry.failure, undefined);
  return {
    diagnostic,
    classification,
    requestCaptureId,
    commandId: requestBody.commandId,
    outputId: requestBody.outputId,
    choiceId: choice.choiceId,
    rowValuePolicy: choice.rowValuePolicy,
    responseErrorCode: responseBody.error.code,
    browserRequestId: entry.browserRequestId,
    observerRequestIndex: entry.index,
    responseCapture: entry.responseCapture,
    responseBodyCaptured: entry.responseBodyCaptured,
  };
};

const returnPolicyOneRowSettingsToTable = async (workflow, { readBuilder, expectedSavedState, actionLabel }) => {
  const { page } = workflow;
  const rowSettings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
  const backToTable = rowSettings.getByRole('button', { name: 'Back to table', exact: true });
  await workflow.action(actionLabel, backToTable,
    () => backToTable.click(), {
      after: () => rowSettings.waitFor({ state: 'hidden' }),
    });
  const afterNavigation = await readBuilder();
  assert.deepEqual(afterNavigation.workspace, expectedSavedState.workspace,
    'returning to the table must preserve the exact saved workspace');
  assert.equal(afterNavigation.draftVersion, expectedSavedState.draftVersion,
    'returning to the table must preserve the saved draft version');
  assert.equal(afterNavigation.draftDigest, expectedSavedState.draftDigest,
    'returning to the table must preserve the saved draft digest');
};

export const openPolicyOneAddColumnsEditor = async (workflow, { readBuilder, expectedSavedState }) => {
  const { page } = workflow;
  await returnPolicyOneRowSettingsToTable(workflow, {
    readBuilder,
    expectedSavedState,
    actionLabel: 'return to table after applying empty-key COUNT_ROWS Group',
  });

  const addColumns = page.getByTestId('construction-action-add-columns');
  await workflow.action('open Add Columns for raw Patient ID', addColumns, () => addColumns.click(), {
    after: () => page.getByRole('region', { name: 'Add columns editor', exact: true }).waitFor({ state: 'visible' }),
  });
};

const requireCheck = (workflow, dimension, name, passed, evidence = {}) =>
  workflow.check(dimension, name, passed, evidence);

const sorted = values => [...values].sort((left, right) => left.localeCompare(right));
const normalizeResponseHeader = value => String(value ?? '').trim().toUpperCase().replace(/\s+/g, ' ');

export const policySnapshot = ({ builder, outputId, sourceState, fixtureIDs, renderedRows,
  addedColumnId = null, choiceId = null }) => {
  const state = patientOneSavedState(builder, outputId);
  const group = policyGroupProjection(state, { inputColumnId: addedColumnId, renderedRows });
  return {
    outputId,
    snapshotToken: builder.catalog?.snapshotToken,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    workspace: builder.workspace,
    projection: policyWorkspaceProjection({ savedState: state, renderedRows, sourceState,
      fixtureIDs, addedColumnId }),
    group,
    policy: group.rowValuePolicy,
    ...(choiceId ? { choiceId } : {}),
  };
};

export const patientOneDisagreementWorkflow = async (workflow, context) => {
  const { page, report } = workflow;
  const transportObserver = createPolicyOneTransportObserver(page);
  const { fixtureIDs, fixtureOracle, explorer, explorerName, apiRoot, outputId, sourceIdColumn, sourceIdColumnId } =
    await openFreshPatientTable(workflow, context, 'Patient ONE disagreement');
  const fixturePath = join(context.target.fixtureDir, 'Patient.ndjson');
  report.target.qaIsolation.fixtureOracle = {
    ...fixtureOracle,
    sha256: createHash('sha256').update(readFileSync(fixturePath)).digest('hex'),
  };
  report.target.qaIsolation.title = explorerName;
  report.target.qaIsolation.outputId = outputId;
  report.target.qaIsolation.setupMutations = [...report.target.qaIsolation.workflowMutations];
  report.target.qaIsolation.workflowMutations.push('native UI created a two-Patient root table from the raw fixture records');
  report.target.qaIsolation.workflowCleanup = 'the UI removes the case-owned Group after ALL persistence is proved; bracket teardown retains the fresh verification project and Explorer, so an earlier failure leaves partial saved state';

  const readBuilder = async () => {
    const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Builder read returned HTTP ${response.status}.`);
    return value;
  };
  const readTable = async () => page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    if (!table) return { headers: [], rows: [] };
    const rows = [...table.querySelectorAll('[role="row"]')];
    return {
      headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    };
  }, { tableSelector: '[data-testid="preview-table-scroll"] [role="table"]' });
  assert.equal(fixtureIDs.length, 2, 'ONE disagreement requires the two-record fixture precondition');
  let sourceBuilder;
  let sourceSavedState;
  let sourceGrid;
  let sourcePreviewBinding;
  let sourceEventIndex;
  let policyOneFormalEntry;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await transportObserver.waitUntilIdle();
    sourceEventIndex = transportObserver.eventIndex;
    sourceBuilder = await readBuilder();
    sourceSavedState = patientOneSavedState(sourceBuilder, outputId);
    sourceGrid = await page.evaluate(readPatientOneRenderedGrid);
    sourcePreviewBinding = await page.evaluate(readPatientOnePreviewBinding);
    await transportObserver.waitUntilIdle();
    if (transportObserver.eventIndex !== sourceEventIndex) continue;
    policyOneFormalEntry = await transportObserver.beginFormalSpan({
      ...sourceSavedState,
      snapshotToken: sourceBuilder.catalog.snapshotToken,
      workspaceProjection: policyWorkspaceProjection({
        savedState: sourceSavedState, renderedRows: sourceGrid, sourceState: sourceSavedState,
        fixtureIDs, addedColumnId: null,
      }),
    }, { expectedEventIndex: sourceEventIndex });
    break;
  }
  assert(policyOneFormalEntry,
    'fresh Patient saved state must be read after every setup authoring request is terminal');
  const sourceIdIndex = findRenderedBuilderHeaderIndex(sourceGrid.headers, sourceIdColumn.label);
  assert(sourceIdIndex >= 0, `fresh Patient table must render ${sourceIdColumn.label}`);
  assert.deepEqual(sorted(sourceGrid.cells.map(row => row[sourceIdIndex]?.text)), fixtureIDs,
    'fresh Patient table must render exactly the two independent Patient IDs');
  const policyOneSourceObservation = {
    fixtureIDs,
    saved: sourceSavedState,
    rendered: sourceGrid,
    previewBinding: sourcePreviewBinding,
    workspaceProjection: policyWorkspaceProjection({
      savedState: sourceSavedState, renderedRows: sourceGrid, sourceState: sourceSavedState,
      fixtureIDs, addedColumnId: null,
    }),
  };
  const policyOneInitialObservation = {
    workspace: policyOneSourceObservation.workspaceProjection,
    outputId,
    snapshotToken: sourceBuilder.catalog.snapshotToken,
    draftVersion: sourceSavedState.draftVersion,
    draftDigest: sourceSavedState.draftDigest,
  };
  const policyOneInitialPreview = {
    outputId,
    binding: sourcePreviewBinding,
    headers: sourceGrid.headers,
    cells: sourceGrid.cells,
    rowCount: sourceGrid.cells.length,
    patientIds: sorted(sourceGrid.cells.map(row => row[sourceIdIndex]?.text)),
  };
  const transportIndexForRequest = request => transportObserver.transportIndexForRequest(request);
  const policyOneEvidence = [];
  requireCheck(workflow, 'correctness', 'fresh Patient table renders the exact two fixture IDs', true,
    { fixtureIDs, headers: sourceGrid.headers, cells: sourceGrid.cells });
  const openRows = page.getByTestId('construction-rows-settings-trigger');
  await workflow.action('open Patient row settings for empty-key Group', openRows, () => openRows.click(), {
    after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
  });
  const groupAction = page.getByTestId('construction-action-group-rows');
  const groupPreviewAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals',
    owner: { kind: 'policy-one-group-preview', attempt: 'empty-key' }, timeout: 5000,
  });
  await workflow.action('open native empty-key Group proposal', groupAction, () => groupAction.click(), {
    after: () => page.waitForFunction(isNativeGroupProposalReady,
      { expectedCount: String(fixtureIDs.length) }, { timeout: 5000 }),
  });
  const groupPreviewStarted = await groupPreviewAttempt.started;
  const groupPreviewExchange = await groupPreviewAttempt.settled;
  assert.equal(groupPreviewExchange.response?.status, 200,
    'empty-key Group preview must settle from the exact observed proposal request');
  assert.equal(groupPreviewExchange.request.captureId, groupPreviewExchange.response.captureId,
    'empty-key Group preview response must retain its actual request capture identity');
  const groupPreviewBody = groupPreviewStarted.rawRequest.postDataJSON();
  assert.equal(groupPreviewBody?.commandId, undefined,
    'construction proposal preview must not invent a commandId for an API without that field');
  assert.equal(groupPreviewBody?.outputId, outputId);
  assert.equal(groupPreviewBody?.expectedDraftVersion, sourceBuilder.draftVersion);
  assert.equal(groupPreviewBody?.expectedDraftDigest, sourceBuilder.draftDigest);
  assert.equal(groupPreviewBody?.snapshotToken, sourceBuilder.catalog.snapshotToken);
  assert.equal(groupPreviewExchange.payload?.outputId, outputId);
  assert.equal(groupPreviewExchange.payload?.draftVersion, sourceBuilder.draftVersion);
  assert.equal(groupPreviewExchange.payload?.draftDigest, sourceBuilder.draftDigest);
  assert.equal(groupPreviewExchange.payload?.snapshotToken, sourceBuilder.catalog.snapshotToken);
  assert(groupPreviewExchange.response?.proposalId,
    'empty-key Group preview response must expose its actual candidate proposalId');
  assert.equal(groupPreviewExchange.response?.proposalPreviewStatus, 'READY');
  const groupPreview = await page.evaluate(readNativeGroupProposal);
  const groupResponsePreview = groupPreviewExchange.payload?.preview;
  assert.equal(groupResponsePreview?.rowCount, 1);
  const groupCountColumn = groupResponsePreview?.columns?.find(column =>
    normalizeResponseHeader(column.label) === 'ROW COUNT');
  assert(groupCountColumn?.column, 'Group preview response must identify its Row count output');
  assert.equal(groupResponsePreview.rows?.[0]?.[groupCountColumn.column], fixtureIDs.length,
    'Group preview response must carry the exact two-record COUNT_ROWS result');
  const countIndex = findRenderedBuilderHeaderIndex(groupPreview.headers, 'row count');
  assert.deepEqual(groupPreview.groupKeys, [], 'ONE disagreement requires one empty-key group across both Patients');
  assert.match(groupPreview.proposalSummary, /^1 row and 1 column\b/,
    'the separate proposal panel must summarize one row and one count column');
  assert.equal(await readNativeGroupSummary(page), 'COUNT_ROWS');
  assert(countIndex >= 0, `empty-key Group proposal must expose Row count: ${JSON.stringify(groupPreview.headers)}`);
  assert.equal(groupPreview.rows.length, 1);
  assert.equal(groupPreview.rows[0]?.[countIndex]?.text, String(fixtureIDs.length));
  requireCheck(workflow, 'correctness', 'empty-key Group prepares one row for the two raw Patient records', true,
    { groupPreview, rawPatientIDs: fixtureIDs, expectedCount: fixtureIDs.length });
  const beforeGroupApply = await readBuilder();
  assert.deepEqual(beforeGroupApply.workspace, sourceBuilder.workspace,
    'empty-key Group preview must not mutate the saved source workspace');
  assert.equal(beforeGroupApply.draftVersion, sourceBuilder.draftVersion);
  assert.equal(beforeGroupApply.draftDigest, sourceBuilder.draftDigest);
  policyOneEvidence.push({
    label: 'GroupPreviewSubmitted',
    event: 'preview',
    request: { ...groupPreviewExchange.request, requestObjectId: groupPreviewExchange.request.captureId,
      transportIndex: transportIndexForRequest(groupPreviewStarted.rawRequest), body: groupPreviewBody },
    observation: {
      workspace: policyOneSourceObservation.workspaceProjection,
      draftVersion: sourceSavedState.draftVersion,
      draftDigest: sourceSavedState.draftDigest,
    },
  });
  policyOneEvidence.push({
    label: 'GroupPreviewReady',
    event: 'preview-response',
    response: { ...groupPreviewExchange.response, requestObjectId: groupPreviewExchange.request.captureId,
      body: groupPreviewExchange.payload },
    settlement: { kind: 'http-response', captureId: groupPreviewExchange.request.captureId },
    requestCaptureId: groupPreviewExchange.request.captureId,
    preview: groupPreview,
    savedBefore: sourceSavedState,
    savedAfter: patientOneSavedState(beforeGroupApply, outputId),
    observation: {
      workspace: policyOneSourceObservation.workspaceProjection,
      preview: { rowCount: groupPreview.rows.length, keys: groupPreview.groupKeys,
        aggregate: await readNativeGroupSummary(page), count: fixtureIDs.length },
    },
  });
  const groupApplyAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'commands',
    owner: { kind: 'policy-one-group-apply', attempt: 'empty-key' }, timeout: 5000,
  });
  await workflow.action('Apply empty-key COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: [{ 'Row count': String(fixtureIDs.length) }] }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI applied empty-key COUNT_ROWS Group to combine the two Patient records');
  const groupApplyStarted = await groupApplyAttempt.started;
  const groupApplyExchange = await groupApplyAttempt.settled;
  const groupApplyBody = groupApplyStarted.rawRequest.postDataJSON();
  assert(groupApplyStarted.request.commandId, 'Group Apply must carry its actual commandId');
  assert.equal(groupApplyBody?.commandId, groupApplyStarted.request.commandId);
  assert.equal(groupApplyBody?.expectedDraftVersion, beforeGroupApply.draftVersion);
  assert.equal(groupApplyBody?.expectedDraftDigest, beforeGroupApply.draftDigest);
  assert.equal(groupApplyBody?.snapshotToken, beforeGroupApply.catalog.snapshotToken);
  assert.equal(groupApplyBody?.commands?.length, 1);
  assert.equal(groupApplyBody?.commands?.[0]?.type, 'APPLY_CONSTRUCTION_PROPOSAL');
  assert.equal(groupApplyBody?.commands?.[0]?.outputId, outputId);
  assert.equal(groupApplyBody?.commands?.[0]?.proposalId, groupPreviewExchange.response.proposalId,
    'Group Apply must consume the actual proposal receipt returned to the preview request');
  assert.notEqual(groupApplyStarted.request.commandId, groupPreviewExchange.response.proposalId,
    'Group Apply must retain its generated commandId separately from the proposal receipt');
  assert.equal(groupApplyExchange.response?.status, 200,
    'Group Apply must return HTTP 200 from the exact observed command request');
  assert.equal(groupApplyExchange.request.captureId, groupApplyExchange.response.captureId,
    'Group Apply response must retain its actual request capture identity');
  assert.equal(groupApplyExchange.response.commandId, groupApplyStarted.request.commandId,
    'Group Apply response must belong to the submitted commandId');
  const groupedBuilder = await readBuilder();
  assert.equal(groupApplyExchange.payload?.commandId, groupApplyStarted.request.commandId,
    'Group Apply payload must echo its exact generated commandId');
  assert.equal(groupApplyExchange.payload?.draftVersion, groupedBuilder.draftVersion,
    'Group Apply payload must match the independent saved draft version');
  assert.equal(groupApplyExchange.payload?.draftDigest, groupedBuilder.draftDigest,
    'Group Apply payload must match the independent saved draft digest');
  assert.deepEqual(groupApplyExchange.payload?.workspace, groupedBuilder.workspace,
    'Group Apply payload must match the exact saved workspace read');
  const groupedDocument = groupedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  const groupedStep = groupedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(groupedStep?.id, 'native Group Apply must save the exact Group step before the ONE choice');
  assert.deepEqual(groupedStep.operation.group.keys ?? [], []);
  assert.equal(groupedStep.operation.group.aggregates?.[0]?.operation, 'COUNT_ROWS');
  const groupedGrid = await readTable();
  const groupedCountIndex = findRenderedBuilderHeaderIndex(groupedGrid.headers, 'row count');
  assert.equal(groupedGrid.rows.length, 1);
  assert.equal(groupedGrid.rows[0]?.[groupedCountIndex], String(fixtureIDs.length));
  policyOneEvidence.push({
    label: 'GroupApplySubmitted',
    event: 'begin-apply',
    request: { ...groupApplyExchange.request, requestObjectId: groupApplyExchange.request.captureId,
      transportIndex: transportIndexForRequest(groupApplyStarted.rawRequest), body: groupApplyBody },
    observation: {
      workspace: policyOneSourceObservation.workspaceProjection,
      draftVersion: beforeGroupApply.draftVersion,
      draftDigest: beforeGroupApply.draftDigest,
      candidateId: groupPreviewExchange.response.proposalId,
    },
  });
  policyOneEvidence.push({
    label: 'GroupApplied',
    event: 'finish-apply',
    response: { ...groupApplyExchange.response, requestObjectId: groupApplyExchange.request.captureId,
      body: groupApplyExchange.payload },
    settlement: { kind: 'http-response', captureId: groupApplyExchange.request.captureId },
    requestCaptureId: groupApplyExchange.request.captureId,
    savedBefore: patientOneSavedState(beforeGroupApply, outputId),
    savedAfter: patientOneSavedState(groupedBuilder, outputId),
    rendered: groupedGrid,
    observation: {
      workspace: policyWorkspaceProjection({ savedState: patientOneSavedState(groupedBuilder, outputId),
        renderedRows: groupedGrid, sourceState: sourceSavedState, fixtureIDs, addedColumnId: null }),
      draftVersion: groupedBuilder.draftVersion,
      draftDigest: groupedBuilder.draftDigest,
    },
  });

  await openPolicyOneAddColumnsEditor(workflow, { readBuilder, expectedSavedState: groupedBuilder });
  const addColumnsPolicy = page.getByRole('combobox', { name: 'Values per grouped row', exact: true });
  await addColumnsPolicy.waitFor({ state: 'visible' });
  await workflow.action('set Patient ID grouped-row policy to ONE', addColumnsPolicy,
    () => addColumnsPolicy.selectOption('ONE'), {
      after: async () => assert.equal(await addColumnsPolicy.inputValue(), 'ONE'),
    });
  const rawFieldsTab = page.getByRole('tab', { name: 'Other fields', exact: true });
  await workflow.action('open Other fields for raw Patient.id', rawFieldsTab, () => rawFieldsTab.click(), {
    after: () => page.getByRole('button', { name: 'Select Patient.id', exact: true }).waitFor({ state: 'visible' }),
  });
  const patientIDChoice = page.locator('[data-testid="feature-catalog-raw-fields"] button[aria-label="Select Patient.id"]');
  assert.equal(await patientIDChoice.count(), 1, 'raw Patient.id must be the unique native field choice');
  await workflow.action('select raw Patient.id with ONE', patientIDChoice, () => patientIDChoice.click(), {
    after: async () => assert.equal(await patientIDChoice.getAttribute('aria-pressed'), 'true'),
  });
  const selectedOneState = await page.evaluate(readPatientOneChoiceEditor);
  assert(Number.isFinite(selectedOneState.documentTimeOrigin),
    'the ONE editor must expose its actual finite browser document time origin');
  assert(selectedOneState.editorMountIdentity, 'the ONE editor must expose an identity bound to its actual mounted DOM element');
  assert.equal(selectedOneState.editorOpen, true);
  assert.equal(selectedOneState.fieldSelected, true);
  assert.equal(selectedOneState.policy, 'ONE');
  assert.equal(selectedOneState.allAvailable, true);
  const beforeONE = await readBuilder();
  assert.equal(beforeONE.draftVersion, groupedBuilder.draftVersion);
  assert.equal(beforeONE.draftDigest, groupedBuilder.draftDigest);
  assert.equal(beforeONE.catalog?.generation, context.target.fixtureGeneration);
  assert.equal(beforeONE.catalog?.snapshotToken, groupedBuilder.catalog?.snapshotToken);
  const preOneState = capturePatientOnePreOneState({
    builder: beforeONE,
    previewBinding: await page.evaluate(readPatientOnePreviewBinding),
    renderedGrid: await readTable(),
    project: context.target.fixtureProject,
    explorerId: explorer,
    outputId,
  });
  const preOneStateBytes = Buffer.from(`${JSON.stringify(preOneState, null, 2)}\n`);
  const preOneStateFile = 'patient-one-pre-one-saved-state.json';
  writeFileSync(join(report.evidenceDirectory, preOneStateFile), preOneStateBytes, { flag: 'wx', mode: 0o600 });
  const preOneStateDigest = createHash('sha256').update(preOneStateBytes).digest('hex');
  report.preOneSavedStateArtifact = {
    fileName: preOneStateFile,
    sha256: preOneStateDigest,
    byteLength: preOneStateBytes.length,
    ...preOneState,
  };
  requireCheck(workflow, 'correctness', 'pre-ONE evidence binds the saved Group document to its exact receipt and draft', true,
    { fileName: preOneStateFile, sha256: preOneStateDigest, outputId,
      receiptId: preOneState.outputBinding.receiptId, draftVersion: preOneState.draftBinding.version,
      draftDigest: preOneState.draftBinding.digest });
  const choiceProposalPath = `${apiRoot}/authoring/v2/construction-choice-proposals`;
  const uiOrigin = new URL(context.target.uiUrl).origin;
  const oneAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-choice-proposals',
    owner: { kind: 'policy-one-choice-preview', attempt: 'ONE' }, timeout: 5000,
  });
  const expectedONEResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === uiOrigin && url.pathname === choiceProposalPath
      && response.request().method() === 'POST' && response.status() === 422;
  }, { timeout: 5000 });
  const addSelected = page.getByRole('button', { name: /Add 1 selected feature/ });
  await workflow.action('submit raw Patient.id with ONE for preview', addSelected, () => addSelected.click(), {
    after: () => page.waitForFunction(() => Boolean(document.querySelector(
      '[data-testid="construction-operation-editor"] [aria-label="Selected feature action"] [role="alert"]')),
    undefined, { timeout: 5000 }),
  });
  const oneStarted = await oneAttempt.started;
  const oneExchange = await oneAttempt.settled;
  const oneResponse = await expectedONEResponse;
  const oneResponseBody = await oneResponse.json();
  const oneRequestBody = oneStarted.rawRequest.postDataJSON();
  const oneChoice = oneRequestBody?.constructionChoices?.[0];
  assert.equal(oneResponse, oneExchange.rawResponse,
    'the exact native 422 response must belong to the observed ONE request exchange');
  assert.equal(oneResponse.request(), oneStarted.rawRequest,
    'the ONE 422 response must settle on the exact submitted Playwright request object');
  assert(oneStarted.request.captureId, 'the ONE request must retain its observed captureId');
  assert(oneStarted.request.commandId, 'the ONE request must retain its actual application commandId');
  assert.equal(oneExchange.request.captureId, oneExchange.response.captureId,
    'the ONE response must retain the captureId of its actual request');
  assert.equal(oneResponse.status(), 422, 'raw Patient.id ONE disagreement must return exact HTTP 422');
  assert.equal(oneResponseBody?.error?.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  assert.equal(oneRequestBody?.outputId, outputId);
  assert.equal(oneRequestBody?.expectedDraftVersion, beforeONE.draftVersion);
  assert.equal(oneRequestBody?.expectedDraftDigest, beforeONE.draftDigest);
  assert.equal(oneRequestBody?.snapshotToken, beforeONE.catalog.snapshotToken);
  assert.equal(oneRequestBody?.constructionChoices?.length, 1);
  assert.equal(oneChoice?.rowValuePolicy, 'ONE');
  assert.equal(oneChoice?.title, sourceIdColumn.label);
  assert.equal(typeof oneChoice?.choiceId, 'string', 'captured ONE request must identify its exact native Patient.id field choice');
  assert(oneChoice.choiceId, 'captured ONE request must have a non-empty Patient.id choice identity');
  const oneFailureState = await page.evaluate(readPatientOneChoiceEditor);
  assert.equal(oneFailureState.documentTimeOrigin, selectedOneState.documentTimeOrigin);
  assert.equal(oneFailureState.editorMountIdentity, selectedOneState.editorMountIdentity);
  assert.equal(oneFailureState.selectedField, 'Patient.id');
  assert.equal(oneFailureState.editorOpen, true);
  assert.equal(oneFailureState.fieldSelected, true);
  assert.equal(oneFailureState.policy, 'ONE');
  assert.deepEqual(oneFailureState.availablePolicies, ['ALL', 'ONE']);
  assert.equal(oneFailureState.errorVisible, true);
  assert(oneFailureState.errorText.length > 0,
    'the native selected-feature action must display the ONE rejection');
  const afterONE = await readBuilder();
  assert.deepEqual(afterONE.workspace, beforeONE.workspace, 'rejected Patient.id ONE must not change the saved Group workspace');
  assert.equal(afterONE.draftVersion, beforeONE.draftVersion);
  assert.equal(afterONE.draftDigest, beforeONE.draftDigest);
  const afterOneSavedState = patientOneSavedState(afterONE, outputId);
  assert.deepEqual(afterOneSavedState, patientOneSavedState(beforeONE, outputId),
    'the exact saved Builder state must remain unchanged after the ONE refusal');
  const expectedHTTPFailure = markPolicyOneExpectedHttp422({
    networkObserver: workflow.networkObserver,
    report,
    request: oneStarted.rawRequest,
    response: oneResponse,
    requestCaptureId: oneStarted.request.captureId,
    requestBody: oneRequestBody,
    responseBody: oneResponseBody,
  });
  const expectedDiagnostic = [expectedHTTPFailure.diagnostic];
  report.expectedFailures ??= [];
  report.expectedFailures.push({ method: 'POST', path: choiceProposalPath, status: 422,
    code: oneResponseBody.error.code, outputId, draftVersion: beforeONE.draftVersion,
    draftDigest: beforeONE.draftDigest, snapshotToken: beforeONE.catalog.snapshotToken,
    preOneSavedStateArtifact: { fileName: preOneStateFile, sha256: preOneStateDigest },
    field: { choiceId: oneChoice.choiceId, title: oneChoice.title, rowValuePolicy: oneChoice.rowValuePolicy },
    rawPatientIDs: fixtureIDs, networkDiagnostics: expectedDiagnostic,
    fixtureNetwork: {
      requestCaptureId: expectedHTTPFailure.requestCaptureId,
      commandId: expectedHTTPFailure.commandId,
      outputId: expectedHTTPFailure.outputId,
      choiceId: expectedHTTPFailure.choiceId,
      rowValuePolicy: expectedHTTPFailure.rowValuePolicy,
      responseErrorCode: expectedHTTPFailure.responseErrorCode,
      browserRequestId: expectedHTTPFailure.browserRequestId,
      observerRequestIndex: expectedHTTPFailure.observerRequestIndex,
      responseCapture: expectedHTTPFailure.responseCapture,
      responseBodyCaptured: expectedHTTPFailure.responseBodyCaptured,
      classification: expectedHTTPFailure.classification,
    } });
  requireCheck(workflow, 'correctness', 'Patient.id ONE returns the exact multiple-values 422 for both raw IDs', true,
    { status: oneResponse.status(), code: oneResponseBody.error.code, path: choiceProposalPath,
      request: { outputId: oneRequestBody.outputId, draftVersion: oneRequestBody.expectedDraftVersion,
        draftDigest: oneRequestBody.expectedDraftDigest, snapshotToken: oneRequestBody.snapshotToken,
        choiceId: oneChoice.choiceId, title: oneChoice.title, rowValuePolicy: oneChoice.rowValuePolicy },
      rawPatientIDs: fixtureIDs, savedWorkspaceUnchanged: true,
      fixtureNetwork: { browserRequestId: expectedHTTPFailure.browserRequestId,
        observerRequestIndex: expectedHTTPFailure.observerRequestIndex, responseCapture: expectedHTTPFailure.responseCapture,
        responseBodyCaptured: expectedHTTPFailure.responseBodyCaptured } });
  requireCheck(workflow, 'persistence', 'rejected Patient.id ONE leaves the saved Group and exact draft unchanged', true,
    { draftVersion: afterONE.draftVersion, draftDigest: afterONE.draftDigest,
      groupStepID: groupedStep.id, workspaceUnchanged: true });

  await workflow.action('change the retained Patient.id selection to ALL', addColumnsPolicy,
    () => addColumnsPolicy.selectOption('ALL'), {
      after: async () => assert.equal(await addColumnsPolicy.inputValue(), 'ALL'),
    });
  const recoveryState = await page.evaluate(readPatientOneChoiceEditor);
  assert.equal(recoveryState.documentTimeOrigin, selectedOneState.documentTimeOrigin);
  assert.equal(recoveryState.editorMountIdentity, selectedOneState.editorMountIdentity);
  assert.equal(recoveryState.selectedField, 'Patient.id');
  assert.equal(recoveryState.editorOpen, true);
  assert.equal(recoveryState.fieldSelected, true);
  assert.equal(recoveryState.policy, 'ALL');
  assert.deepEqual(recoveryState.availablePolicies, ['ALL', 'ONE']);
  assert.equal(recoveryState.errorVisible, true,
    'the visible native error must remain while changing the retained field to ALL');
  assert(recoveryState.errorText.length > 0);
  requireCheck(workflow, 'usability', 'failed Patient.id ONE retains its selected field for same-editor ALL repair', true,
    { afterONE: oneFailureState, afterSwitchToALL: recoveryState });
  const beforeAllPreview = await readBuilder();
  assert.deepEqual(beforeAllPreview.workspace, beforeONE.workspace,
    'changing policy to ALL before preview must leave the saved workspace unchanged');
  assert.equal(beforeAllPreview.draftVersion, beforeONE.draftVersion);
  assert.equal(beforeAllPreview.draftDigest, beforeONE.draftDigest);
  const allAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-choice-proposals',
    owner: { kind: 'policy-one-choice-preview', attempt: 'ALL' }, timeout: 5000,
  });
  const expectedALLResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === uiOrigin && url.pathname === choiceProposalPath
      && response.request().method() === 'POST' && response.status() === 200;
  }, { timeout: 5000 });
  const allApplyAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'commands',
    owner: { kind: 'policy-one-choice-apply', attempt: 'ALL' }, timeout: 5000,
  });
  let allApplyObservedRequest;
  const allApplyEditorAtRequest = page.waitForRequest(request => {
    const url = new URL(request.url());
    if (request.method() !== 'POST' || url.pathname !== `${apiRoot}/authoring/v2/commands`) return false;
    try {
      return request.postDataJSON()?.commands?.[0]?.type === 'APPLY_CONSTRUCTION_CHOICE';
    } catch {
      return false;
    }
  }, { timeout: 5000 }).then(request => {
    allApplyObservedRequest = request;
    return page.evaluate(readPatientOneChoiceEditor);
  });
  let allStarted;
  let afterAllSubmittedState;
  let allExchange;
  let allResponse;
  let allResponseBody;
  let allRequestBody;
  let allChoice;
  let allApplyStarted;
  let allApplyEditorAtSubmit;
  let allApplyExchange;
  let allApplyBody;
  let allReadyState;
  await workflow.action('resubmit the retained Patient.id field with ALL and apply it', addSelected,
    () => addSelected.click(), {
      after: async () => {
        allStarted = await allAttempt.started;
        afterAllSubmittedState = await page.evaluate(readPatientOneChoiceEditor);
        allExchange = await allAttempt.settled;
        allResponse = await expectedALLResponse;
        allResponseBody = await allResponse.json();
        allReadyState = await page.evaluate(readPatientOneChoiceEditor);
        allApplyStarted = await allApplyAttempt.started;
        allApplyEditorAtSubmit = await allApplyEditorAtRequest;
        allApplyExchange = await allApplyAttempt.settled;
        allRequestBody = allStarted.rawRequest.postDataJSON();
        allChoice = allRequestBody?.constructionChoices?.[0];
        allApplyBody = allApplyStarted.rawRequest.postDataJSON();
        await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
          expectedRows: [{ [sourceIdColumn.label]: { titleJsonArray: fixtureIDs } }] });
        await page.waitForFunction(() => !document.querySelector('[aria-label="Add columns editor"]'),
          undefined, { timeout: 5000 });
      },
    });
  assert.equal(allResponse, allExchange.rawResponse,
    'the successful ALL response must belong to the observed ALL request exchange');
  assert.equal(allResponse.request(), allStarted.rawRequest,
    'the ALL response must settle on the exact submitted Playwright request object');
  assert(allStarted.request.captureId, 'the ALL request must retain its observed captureId');
  assert(allStarted.request.commandId, 'the ALL request must retain its actual application commandId');
  assert.equal(allExchange.request.captureId, allExchange.response.captureId,
    'the ALL response must retain the captureId of its actual request');
  assert.equal(allRequestBody?.outputId, outputId);
  assert.equal(allRequestBody?.expectedDraftVersion, beforeONE.draftVersion);
  assert.equal(allRequestBody?.expectedDraftDigest, beforeONE.draftDigest);
  assert.equal(allRequestBody?.snapshotToken, beforeONE.catalog.snapshotToken);
  assert.equal(allRequestBody?.constructionChoices?.length, 1);
  assert.equal(allChoice?.choiceId, oneChoice.choiceId,
    'ALL repair must use the same raw Patient.id catalog selection as the rejected ONE request');
  assert.equal(allChoice?.rowValuePolicy, 'ALL');
  assert.equal(allChoice?.title, sourceIdColumn.label);
  assert.equal(allResponseBody?.previewStatus, 'READY');
  assert.equal(allResponseBody?.outputId, outputId);
  assert.equal(allResponseBody?.snapshotToken, beforeONE.catalog.snapshotToken);
  assert.equal(allResponseBody?.draftVersion, beforeONE.draftVersion);
  assert.equal(allResponseBody?.draftDigest, beforeONE.draftDigest);
  assert.equal(allResponseBody?.commandId, allRequestBody.commandId,
    'the successful ALL proposal must echo the exact UI commandId');
  assert.notEqual(allRequestBody.commandId, oneRequestBody.commandId,
    'the retry must use its own actual commandId rather than reuse the rejected request');
  const responsePreview = allResponseBody?.preview;
  assert.equal(responsePreview?.outputId, outputId);
  assert.equal(responsePreview?.rowCount, 1);
  assert.equal(typeof responsePreview?.receiptId, 'string');
  const responseIDColumn = responsePreview?.columns?.find(column => column.label === sourceIdColumn.label);
  const responseCountColumn = responsePreview?.columns?.find(column =>
    normalizeResponseHeader(column.label) === 'ROW COUNT');
  assert(responseIDColumn?.column, 'ALL response preview must expose the selected Patient.id output column');
  assert(responseCountColumn?.column, 'ALL response preview must expose the existing Row count column');
  const responsePreviewRow = responsePreview.rows?.[0];
  assert(responsePreviewRow, 'ALL response preview must contain its actual grouped row');
  const responsePreviewIDs = responsePreviewRow[responseIDColumn.column];
  const responsePreviewCount = responsePreviewRow[responseCountColumn.column];
  assert(Array.isArray(responsePreviewIDs), 'ALL response preview must return literal Patient ID members');
  assert.deepEqual(sorted(responsePreviewIDs), fixtureIDs,
    'ALL response payload must contain exactly the independent fixture Patient IDs');
  assert.equal(responsePreviewCount, fixtureIDs.length,
    'ALL response payload must preserve the two-Patient COUNT_ROWS value');
  const policyOnePreview = {
    outputId: responsePreview.outputId,
    receiptId: responsePreview.receiptId,
    status: allResponseBody.previewStatus,
    rowCount: responsePreview.rowCount,
    rows: [{ memberIds: sorted(responsePreviewIDs), count: responsePreviewCount }],
  };
  assert.equal(responsePreviewCount, fixtureIDs.length);
  const responsePreviewHeaders = responsePreview.columns.map(column => column.label);
  const responsePreviewRows = responsePreview.rows.map(row => ({
    patientIds: row[responseIDColumn.column],
    count: row[responseCountColumn.column],
  }));
  assert.equal(afterAllSubmittedState.editorMountIdentity, selectedOneState.editorMountIdentity,
    'ALL request must retain the same mounted editor as the ONE refusal');
  assert.equal(afterAllSubmittedState.editorOpen, true);
  assert.equal(afterAllSubmittedState.fieldSelected, true);
  assert.equal(afterAllSubmittedState.policy, 'ALL');
  assert.equal(allReadyState.editorOpen, true,
    'successful ALL proposal must remain in the open Add Columns editor before Apply');
  assert.equal(allReadyState.documentTimeOrigin, selectedOneState.documentTimeOrigin);
  assert.equal(allReadyState.editorMountIdentity, selectedOneState.editorMountIdentity,
    'ALL preview must still be shown in the same actual mounted editor element');
  assert.equal(allReadyState.editorOpen, true);
  assert.equal(allReadyState.fieldSelected, true);
  assert.equal(allReadyState.policy, 'ALL');
  assert.equal(allApplyEditorAtSubmit.editorOpen, true,
    'the command must be issued before the successful ALL editor closes');
  assert.equal(allApplyObservedRequest, allApplyStarted.rawRequest,
    'the editor observation must be taken on the exact observed Apply request event');
  assert.equal(allApplyEditorAtSubmit.editorMountIdentity, selectedOneState.editorMountIdentity,
    'ALL Apply must use the same actual mounted editor as the ONE refusal');
  requireCheck(workflow, 'correctness', 'same-editor ALL repair response contains both literal raw Patient IDs', true,
    { status: allResponse.status(), code: allResponseBody?.error?.code ?? null,
      choiceId: allChoice.choiceId, rowValuePolicy: allChoice.rowValuePolicy,
      headers: responsePreviewHeaders, rows: responsePreviewRows,
      rawPatientIDs: sorted(responsePreviewIDs), expectedPatientIDs: fixtureIDs,
      uiAtProposalResponse: allReadyState, uiAtApplyRequest: allApplyEditorAtSubmit });
  assert.equal(allApplyEditorAtSubmit.documentTimeOrigin, selectedOneState.documentTimeOrigin);
  assert.equal(allApplyEditorAtSubmit.editorMountIdentity, selectedOneState.editorMountIdentity,
    'ALL Apply request must be submitted by the same mounted editor element');
  const allAppliedEditor = await page.evaluate(readPatientOneChoiceEditor);
  assert.equal(allAppliedEditor.editorOpen, false,
    'the native Add Columns editor must close after successful ALL Apply');
  assert.equal(allApplyStarted.request.commandId, allResponseBody.commandId,
    'ALL Apply must reuse the successful proposal commandId from the actual response');
  assert.equal(allApplyBody?.commandId, allResponseBody.commandId);
  assert.equal(allApplyBody?.expectedDraftVersion, allResponseBody.draftVersion);
  assert.equal(allApplyBody?.expectedDraftDigest, allResponseBody.draftDigest);
  assert.equal(allApplyBody?.snapshotToken, allResponseBody.snapshotToken);
  assert.equal(allApplyBody?.commands?.length, 1);
  assert.equal(allApplyBody?.commands?.[0]?.type, 'APPLY_CONSTRUCTION_CHOICE');
  assert.equal(allApplyBody?.commands?.[0]?.outputId, outputId);
  assert.equal(allApplyBody?.commands?.[0]?.constructionChoice?.choiceId, allChoice.choiceId);
  assert.equal(allApplyBody?.commands?.[0]?.constructionChoice?.rowValuePolicy, 'ALL');
  assert.equal(allApplyExchange.response?.status, 200,
    'ALL Apply must return HTTP 200 from the exact observed command request');
  assert.equal(allApplyExchange.request.captureId, allApplyExchange.response.captureId,
    'ALL Apply response must retain its actual request capture identity');
  assert.equal(allApplyExchange.response.commandId, allApplyStarted.request.commandId,
    'ALL Apply response must belong to the submitted commandId');
  report.target.qaIsolation.workflowMutations.push('native UI repaired the Patient.id row-value policy with ALL and applied it');
  const allAppliedBuilder = await readBuilder();
  assert.equal(allApplyExchange.payload?.commandId, allResponseBody.commandId,
    'ALL Apply payload must echo the successful ALL proposal commandId');
  assert.equal(allApplyExchange.payload?.draftVersion, allAppliedBuilder.draftVersion,
    'ALL Apply payload must match the independent saved draft version');
  assert.equal(allApplyExchange.payload?.draftDigest, allAppliedBuilder.draftDigest,
    'ALL Apply payload must match the independent saved draft digest');
  assert.deepEqual(allApplyExchange.payload?.workspace, allAppliedBuilder.workspace,
    'ALL Apply payload must match the exact saved workspace read');
  const allAppliedSavedState = patientOneSavedState(allAppliedBuilder, outputId);
  const allAppliedGrid = await page.evaluate(readPatientOneRenderedGrid);
  const appliedPatientIDIndex = findRenderedBuilderHeaderIndex(allAppliedGrid.headers, sourceIdColumn.label);
  const appliedRowCountIndex = findRenderedBuilderHeaderIndex(allAppliedGrid.headers, 'Row count');
  assert.equal(allAppliedGrid.cells.length, 1,
    'post-Apply Builder grid must show exactly one empty-key Group row');
  assert(appliedPatientIDIndex >= 0, 'post-Apply Builder grid must render the Patient.id column');
  assert(appliedRowCountIndex >= 0, 'post-Apply Builder grid must render the Row count column');
  const appliedPatientIDCell = allAppliedGrid.cells[0]?.[appliedPatientIDIndex];
  assert.equal(typeof appliedPatientIDCell?.raw, 'string',
    'post-Apply Patient.id cell must expose its raw JSON array');
  const appliedRawPatientIDs = JSON.parse(appliedPatientIDCell.raw);
  const appliedRenderedRowCount = Number(allAppliedGrid.cells[0][appliedRowCountIndex]?.text);
  assert.deepEqual(sorted(appliedRawPatientIDs), fixtureIDs,
    'post-Apply Builder row must display the exact raw Patient IDs');
  assert.equal(appliedRenderedRowCount, fixtureIDs.length,
    'post-Apply Builder row count must match the fixture source-row count');
  let appliedBuilder = allAppliedBuilder;
  let appliedDocument = appliedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  let appliedGroup = appliedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const addedPatientIDColumns = appliedDocument?.columns?.filter(column =>
    column.source?.field?.path === 'id' && (column.columnId ?? column.id) !== sourceIdColumnId) ?? [];
  assert.equal(addedPatientIDColumns.length, 1, 'ALL repair must save one new native Patient.id field column');
  const addedPatientIDColumnId = addedPatientIDColumns[0].columnId ?? addedPatientIDColumns[0].id;
  const appliedPatientIDValue = appliedGroup?.rowValues?.find(value => value.inputColumnId === addedPatientIDColumnId);
  assert.equal(appliedPatientIDValue?.policy, 'ALL', 'saved Group must retain ALL for the exact Patient.id input');
  assert.notEqual(appliedPatientIDValue?.inputColumnId, appliedPatientIDValue?.outputColumnId,
    'saved ALL row value must distinguish its raw Patient.id input from its grouped output');
  const appliedPatientIDOutput = appliedGroup?.outputs?.find(output => output.id === appliedPatientIDValue?.outputColumnId);
  assert(appliedPatientIDOutput, 'saved ALL row-value output must be declared by the Group step');
  assert.equal(appliedPatientIDOutput.type, 'array', 'saved ALL row-value output must be declared as an array');
  await page.reload({ waitUntil: 'domcontentloaded' });
  const tableSelector = `[data-testid="construction-table-${outputId}"]`;
  await page.locator(tableSelector).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.locator(tableSelector).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: [{ [sourceIdColumn.label]: { titleJsonArray: fixtureIDs } }] });
  appliedBuilder = await readBuilder();
  assert.deepEqual(appliedBuilder.workspace, allAppliedBuilder.workspace,
    'reload must preserve the exact saved ALL workspace');
  assert.equal(appliedBuilder.draftVersion, allAppliedBuilder.draftVersion);
  assert.equal(appliedBuilder.draftDigest, allAppliedBuilder.draftDigest);
  appliedDocument = appliedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  appliedGroup = appliedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const reloadedPatientIDColumns = appliedDocument?.columns?.filter(column =>
    column.source?.field?.path === 'id' && (column.columnId ?? column.id) !== sourceIdColumnId) ?? [];
  assert.equal(reloadedPatientIDColumns.length, 1, 'reload must retain exactly one added Patient.id field column');
  const reloadedPatientIDColumnId = reloadedPatientIDColumns[0].columnId ?? reloadedPatientIDColumns[0].id;
  const reloadedPatientIDValue = appliedGroup?.rowValues?.find(value => value.inputColumnId === reloadedPatientIDColumnId);
  assert.equal(reloadedPatientIDValue?.policy, 'ALL', 'reloaded saved Group must retain ALL for Patient.id');
  assert.notEqual(reloadedPatientIDValue?.inputColumnId, reloadedPatientIDValue?.outputColumnId,
    'reloaded ALL row value must retain distinct input and output IDs');
  const reloadedPatientIDOutput = appliedGroup?.outputs?.find(output => output.id === reloadedPatientIDValue?.outputColumnId);
  assert(reloadedPatientIDOutput, 'reloaded ALL row-value output must remain declared by the Group step');
  assert.equal(reloadedPatientIDOutput.type, 'array', 'reloaded ALL row-value output must remain an array');
  const allReloadTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  assert.notEqual(allReloadTimeOrigin, selectedOneState.documentTimeOrigin,
    'ALL persistence must be verified from a fresh browser document after reload');
  const allReloadSavedState = patientOneSavedState(appliedBuilder, outputId);
  assert.deepEqual(allReloadSavedState, allAppliedSavedState,
    'reload must preserve the exact saved ALL workspace and draft identity');
  const rendered = await page.evaluate(readPatientOneRenderedGrid);
  const renderedIDIndex = findRenderedBuilderHeaderIndex(rendered.headers, sourceIdColumn.label);
  const renderedRowCountIndex = findRenderedBuilderHeaderIndex(rendered.headers, 'Row count');
  assert(renderedIDIndex >= 0, 'reloaded Builder grid must render the Patient.id column');
  assert(renderedRowCountIndex >= 0, 'reloaded Builder grid must render the Row count column');
  assert.equal(typeof rendered.cells[0]?.[renderedIDIndex]?.raw, 'string',
    'reloaded Patient.id cell must expose its raw JSON array');
  const renderedRawIDs = rendered.cells.map(row => JSON.parse(row[renderedIDIndex].raw));
  const reloadedRenderedRowCount = Number(rendered.cells[0]?.[renderedRowCountIndex]?.text);
  assert.equal(rendered.cells.length, 1, 'reloaded ALL repair must still show exactly one empty-key group row');
  assert.deepEqual(sorted(renderedRawIDs[0] ?? []), fixtureIDs,
    'reloaded Builder row must display the exact raw Patient IDs after ALL repair');
  assert.equal(reloadedRenderedRowCount, fixtureIDs.length,
    'reloaded Builder row count must match the fixture source-row count');
  requireCheck(workflow, 'persistence', 'ALL repair applies and persists exact Patient IDs after reload', true,
    { draftVersion: appliedBuilder.draftVersion, draftDigest: appliedBuilder.draftDigest,
      groupStepID: appliedGroup?.id, rowValuePolicy: reloadedPatientIDValue.policy,
      inputColumnId: reloadedPatientIDValue.inputColumnId,
      outputColumnId: reloadedPatientIDValue.outputColumnId,
      outputType: reloadedPatientIDOutput.type,
      headers: rendered.headers, cells: rendered.cells, rawPatientIDs: sorted(renderedRawIDs[0]), expectedPatientIDs: fixtureIDs,
      postApply: { headers: allAppliedGrid.headers, cells: allAppliedGrid.cells,
        rawPatientIDs: sorted(appliedRawPatientIDs), rowCount: appliedRenderedRowCount },
      postReloadRowCount: reloadedRenderedRowCount,
      fixtureOracleSha256: report.target.qaIsolation.fixtureOracle.sha256 });
  policyOneEvidence.push({
    label: 'ReloadedAll',
    event: 'reload',
    documentTimeOrigin: allReloadTimeOrigin,
    savedBefore: allAppliedSavedState,
    savedAfter: allReloadSavedState,
    rendered,
    rawPatientIDs: sorted(renderedRawIDs[0]),
    observation: {
      workspace: policyWorkspaceProjection({ savedState: allReloadSavedState,
        renderedRows: { headers: rendered.headers, cells: rendered.cells }, sourceState: sourceSavedState,
        fixtureIDs, addedColumnId: reloadedPatientIDColumnId }),
      draftVersion: appliedBuilder.draftVersion,
      draftDigest: appliedBuilder.draftDigest,
      documentTimeOrigin: allReloadTimeOrigin,
    },
  });

  const reloadedAllGroupStepId = allReloadSavedState.group?.stepId;
  assert.equal(reloadedAllGroupStepId, appliedGroup?.id,
    'the removal target must use the exact Group step observed after ALL reload');
  const rowsHistory = page.getByTestId('construction-row-operation-history');
  if (!await rowsHistory.isVisible().catch(() => false)) {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await workflow.action('open saved row operations for Group restoration', settings, () => settings.click(), {
      after: () => rowsHistory.waitFor({ state: 'visible', timeout: 5000 }),
    });
  }
  const removalControl = page.getByTestId(`construction-row-remove-${appliedGroup.id}`);
  const removalPreviewAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals',
    owner: { kind: 'policy-one-group-removal-preview', attempt: 'cancel-check' }, timeout: 5000,
  });
  await workflow.action('preview removal of the ALL-backed Group', removalControl, () => removalControl.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && Boolean(document.querySelector('[data-testid="construction-removal-summary"]')), undefined, { timeout: 5000 }),
  });
  const removalPreviewStarted = await removalPreviewAttempt.started;
  const removalPreviewExchange = await removalPreviewAttempt.settled;
  assert.equal(removalPreviewExchange.response?.status, 200,
    'Group removal preview must settle from the exact observed proposal request');
  assert.equal(removalPreviewExchange.request.captureId, removalPreviewExchange.response.captureId,
    'Group removal preview response must retain its actual request capture identity');
  const removalPreviewBody = removalPreviewStarted.rawRequest.postDataJSON();
  assert.equal(removalPreviewBody?.commandId, undefined,
    'Group removal proposal preview must not invent a commandId for an API without that field');
  assert.equal(removalPreviewBody?.outputId, outputId);
  assert.equal(removalPreviewBody?.expectedDraftVersion, appliedBuilder.draftVersion);
  assert.equal(removalPreviewBody?.expectedDraftDigest, appliedBuilder.draftDigest);
  assert.equal(removalPreviewBody?.snapshotToken, appliedBuilder.catalog.snapshotToken);
  assert.equal(removalPreviewExchange.payload?.outputId, outputId);
  assert.equal(removalPreviewExchange.payload?.draftVersion, appliedBuilder.draftVersion);
  assert.equal(removalPreviewExchange.payload?.draftDigest, appliedBuilder.draftDigest);
  assert.equal(removalPreviewExchange.payload?.snapshotToken, appliedBuilder.catalog.snapshotToken);
  assert(removalPreviewExchange.response?.proposalId,
    'first Group removal response must expose its actual candidate proposalId');
  assert.equal(removalPreviewExchange.response?.proposalPreviewStatus, 'READY');
  assert.deepEqual(removalPreviewBody?.removeStepIds, [appliedGroup.id],
    'Group removal proposal must target the exact applied Group step');
  const removalPreview = await page.evaluate(readNativeGroupProposal);
  const firstRemovalResponsePreview = removalPreviewExchange.payload?.preview;
  const firstRemovalIDColumn = firstRemovalResponsePreview?.columns?.find(column => column.label === sourceIdColumn.label);
  assert.equal(firstRemovalResponsePreview?.rowCount, fixtureIDs.length);
  assert(firstRemovalIDColumn?.column, 'first removal response must expose the original Patient.id column');
  assert.deepEqual(sorted(firstRemovalResponsePreview.rows.map(row => row[firstRemovalIDColumn.column])), fixtureIDs,
    'first removal response must contain the exact original Patient IDs');
  const removalPreviewIDIndex = findRenderedBuilderHeaderIndex(removalPreview.headers, sourceIdColumn.label);
  assert(removalPreviewIDIndex >= 0,
    `Group removal preview must expose original ${sourceIdColumn.label}: ${JSON.stringify(removalPreview.headers)}`);
  assert.equal(removalPreview.rows.length, fixtureIDs.length,
    'Group removal preview must restore both source Patient rows');
  assert.deepEqual(sorted(removalPreview.rows.map(row => row[removalPreviewIDIndex]?.text)), fixtureIDs,
    'Group removal preview must restore the exact original Patient IDs');
  requireCheck(workflow, 'correctness', 'Group removal previews both original Patient rows', true,
    { headers: removalPreview.headers, rows: removalPreview.rows, patientIDs: fixtureIDs });
  const beforeRemovalCancel = await readBuilder();
  const beforeRemovalCancelState = patientOneSavedState(beforeRemovalCancel, outputId);
  assert.deepEqual(beforeRemovalCancelState, allReloadSavedState,
    'the removal preview must leave the reloaded ALL Group saved state unchanged');
  await workflow.action('Cancel Group removal preview', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'),
        undefined, { timeout: 5000 }),
    });
  const afterRemovalCancel = await readBuilder();
  const afterRemovalCancelState = patientOneSavedState(afterRemovalCancel, outputId);
  assert.deepEqual(afterRemovalCancel.workspace, beforeRemovalCancel.workspace,
    'Cancel must preserve the full saved workspace');
  assert.equal(afterRemovalCancel.draftVersion, beforeRemovalCancel.draftVersion,
    'Cancel must preserve the saved draft version');
  assert.equal(afterRemovalCancel.draftDigest, beforeRemovalCancel.draftDigest,
    'Cancel must preserve the saved draft digest');
  assert.deepEqual(afterRemovalCancelState, beforeRemovalCancelState,
    'Cancel must preserve the exact saved ALL workspace, Group and draft identity');
  const cancelledGroupGrid = await page.evaluate(readPatientOneRenderedGrid);
  const cancelledGroupIDIndex = findRenderedBuilderHeaderIndex(cancelledGroupGrid.headers, sourceIdColumn.label);
  assert.equal(cancelledGroupGrid.cells.length, 1,
    'Cancel must keep the applied one-row Group visible');
  assert.deepEqual(sorted(JSON.parse(cancelledGroupGrid.cells[0]?.[cancelledGroupIDIndex]?.raw ?? '[]')), fixtureIDs,
    'Cancel must keep the applied ALL values visible');
  policyOneEvidence.push({
    label: 'RemovalPreviewSubmitted',
    event: 'preview',
    request: { ...removalPreviewExchange.request, requestObjectId: removalPreviewExchange.request.captureId,
      transportIndex: transportIndexForRequest(removalPreviewStarted.rawRequest), body: removalPreviewBody },
    observation: {
      workspace: policyWorkspaceProjection({ savedState: beforeRemovalCancelState,
        renderedRows: allAppliedGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
      draftVersion: beforeRemovalCancel.draftVersion,
      draftDigest: beforeRemovalCancel.draftDigest,
    },
  });
  policyOneEvidence.push({
    label: 'RemovalPreviewReady',
    event: 'preview-response',
    response: { ...removalPreviewExchange.response, requestObjectId: removalPreviewExchange.request.captureId,
      body: removalPreviewExchange.payload },
    settlement: { kind: 'http-response', captureId: removalPreviewExchange.request.captureId },
    requestCaptureId: removalPreviewExchange.request.captureId,
    preview: removalPreview,
    savedBefore: beforeRemovalCancelState,
    observation: { workspace: policyWorkspaceProjection({ savedState: beforeRemovalCancelState,
      renderedRows: allAppliedGrid, sourceState: sourceSavedState, fixtureIDs,
      addedColumnId: addedPatientIDColumnId }),
      candidateId: removalPreviewExchange.response.proposalId,
      previewRows: removalPreview.rows },
  });
  policyOneEvidence.push({
    label: 'RemovalCancelled',
    event: 'cancel',
    savedBefore: beforeRemovalCancelState,
    savedAfter: afterRemovalCancelState,
    rendered: cancelledGroupGrid,
    observation: {
      savedBefore: beforeRemovalCancelState,
      savedAfter: afterRemovalCancelState,
      workspace: policyWorkspaceProjection({ savedState: afterRemovalCancelState,
        renderedRows: cancelledGroupGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
    },
  });
  const afterCancelGroupStepId = afterRemovalCancelState.group?.stepId;
  assert.equal(afterCancelGroupStepId, reloadedAllGroupStepId,
    'Cancel must preserve the exact reloaded ALL Group step identity');
  assert.equal(afterCancelGroupStepId, appliedGroup?.id,
    'Cancel must preserve the current saved ALL Group step, not a pre-ONE snapshot');
  assert.equal(await page.getByRole('dialog', { name: 'Row definition settings', exact: true }).count(), 0,
    'the saved-step Remove action must leave the row-settings dialog closed');
  assert.equal(await page.getByTestId(`construction-history-step-${afterCancelGroupStepId}`).getAttribute('aria-pressed'), 'true',
    'Cancel must leave the current Group selected in the visible Row operations history');
  requireCheck(workflow, 'persistence', 'Cancel preserves the applied ALL Group and exact draft', true,
    { groupStepID: afterCancelGroupStepId, draftVersion: afterRemovalCancel.draftVersion,
      draftDigest: afterRemovalCancel.draftDigest, rowSettingsDialogOpen: false,
      selectedHistoryStepId: afterCancelGroupStepId });

  const removalRetryAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals',
    owner: { kind: 'policy-one-group-removal-preview', attempt: 'apply' }, timeout: 5000,
  });
  const removalRetryControl = page.getByTestId(`construction-remove-step-${afterCancelGroupStepId}`);
  await workflow.action('reopen Group removal after Cancel', removalRetryControl,
    () => removalRetryControl.click(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
        && Boolean(document.querySelector('[data-testid="construction-removal-summary"]')), undefined, { timeout: 5000 }),
    });
  const removalRetryStarted = await removalRetryAttempt.started;
  const removalRetryExchange = await removalRetryAttempt.settled;
  assert.equal(removalRetryExchange.response?.status, 200,
    'reopened Group removal preview must settle from its exact request');
  assert.equal(removalRetryExchange.request.captureId, removalRetryExchange.response.captureId,
    'reopened Group removal response must retain its actual request capture identity');
  const removalRetryBody = removalRetryStarted.rawRequest.postDataJSON();
  assert.equal(removalRetryBody?.commandId, undefined,
    'reopened construction proposal preview must remain a proposal request without commandId');
  assert.equal(removalRetryBody?.outputId, outputId);
  assert.equal(removalRetryBody?.expectedDraftVersion, afterRemovalCancel.draftVersion);
  assert.equal(removalRetryBody?.expectedDraftDigest, afterRemovalCancel.draftDigest);
  assert.equal(removalRetryBody?.snapshotToken, afterRemovalCancel.catalog.snapshotToken);
  assert.equal(removalRetryExchange.payload?.outputId, outputId);
  assert.equal(removalRetryExchange.payload?.draftVersion, afterRemovalCancel.draftVersion);
  assert.equal(removalRetryExchange.payload?.draftDigest, afterRemovalCancel.draftDigest);
  assert.equal(removalRetryExchange.payload?.snapshotToken, afterRemovalCancel.catalog.snapshotToken);
  assert(removalRetryExchange.response?.proposalId,
    'reopened Group removal response must expose its actual proposalId');
  assert.equal(removalRetryExchange.response?.proposalPreviewStatus, 'READY');
  assert.deepEqual(removalRetryBody?.removeStepIds, [afterCancelGroupStepId],
    'reopened Group removal must target the exact post-Cancel saved Group step');
  const removalRetryPreview = await page.evaluate(readNativeGroupProposal);
  const retryRemovalResponsePreview = removalRetryExchange.payload?.preview;
  const retryRemovalIDColumn = retryRemovalResponsePreview?.columns?.find(column => column.label === sourceIdColumn.label);
  assert.equal(retryRemovalResponsePreview?.rowCount, fixtureIDs.length);
  assert(retryRemovalIDColumn?.column, 'reopened removal response must expose the original Patient.id column');
  assert.deepEqual(sorted(retryRemovalResponsePreview.rows.map(row => row[retryRemovalIDColumn.column])), fixtureIDs,
    'reopened removal response must contain the exact original Patient IDs');
  assert.notEqual(removalRetryExchange.request.captureId, removalPreviewExchange.request.captureId,
    'reopening a cancelled removal must use a distinct actual request capture');
  const removalRetryIDIndex = findRenderedBuilderHeaderIndex(removalRetryPreview.headers, sourceIdColumn.label);
  assert(removalRetryIDIndex >= 0,
    `reopened removal preview must expose original ${sourceIdColumn.label}`);
  assert.deepEqual(sorted(removalRetryPreview.rows.map(row => row[removalRetryIDIndex]?.text)), fixtureIDs,
    'the removal preview used for Apply must still contain the exact original Patient IDs');
  policyOneEvidence.push({
    label: 'RemovalPreviewSubmitted',
    event: 'preview',
    request: { ...removalRetryExchange.request, requestObjectId: removalRetryExchange.request.captureId,
      transportIndex: transportIndexForRequest(removalRetryStarted.rawRequest), body: removalRetryBody },
    observation: {
      workspace: policyWorkspaceProjection({ savedState: afterRemovalCancelState,
        renderedRows: cancelledGroupGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
      draftVersion: afterRemovalCancel.draftVersion,
      draftDigest: afterRemovalCancel.draftDigest,
    },
  });
  policyOneEvidence.push({
    label: 'RemovalPreviewReady',
    event: 'preview-response',
    response: { ...removalRetryExchange.response, requestObjectId: removalRetryExchange.request.captureId,
      body: removalRetryExchange.payload },
    settlement: { kind: 'http-response', captureId: removalRetryExchange.request.captureId },
    requestCaptureId: removalRetryExchange.request.captureId,
    preview: removalRetryPreview,
    savedBefore: afterRemovalCancelState,
    observation: { workspace: policyWorkspaceProjection({ savedState: afterRemovalCancelState,
      renderedRows: cancelledGroupGrid, sourceState: sourceSavedState, fixtureIDs,
      addedColumnId: addedPatientIDColumnId }),
      candidateId: removalRetryExchange.response.proposalId,
      previewRows: removalRetryPreview.rows },
  });
  const beforeRemovalApply = await readBuilder();
  assert.deepEqual(patientOneSavedState(beforeRemovalApply, outputId), afterRemovalCancelState,
    'reopening removal must leave the saved ALL Group and draft unchanged');
  const removalApplyAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'commands',
    owner: { kind: 'policy-one-group-removal-apply', attempt: 'restore-source' }, timeout: 5000,
  });
  await workflow.action('Apply Group removal to restore Patient rows', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) }),
    });
  const removalApplyStarted = await removalApplyAttempt.started;
  const removalApplyExchange = await removalApplyAttempt.settled;
  const removalApplyBody = removalApplyStarted.rawRequest.postDataJSON();
  assert(removalApplyStarted.request.commandId, 'Group removal Apply must carry its actual commandId');
  assert.equal(removalApplyBody?.commandId, removalApplyStarted.request.commandId);
  assert.equal(removalApplyBody?.expectedDraftVersion, beforeRemovalApply.draftVersion);
  assert.equal(removalApplyBody?.expectedDraftDigest, beforeRemovalApply.draftDigest);
  assert.equal(removalApplyBody?.snapshotToken, beforeRemovalApply.catalog.snapshotToken);
  assert.equal(removalApplyBody?.commands?.length, 1);
  assert.equal(removalApplyBody?.commands?.[0]?.type, 'APPLY_CONSTRUCTION_PROPOSAL');
  assert.equal(removalApplyBody?.commands?.[0]?.outputId, outputId);
  assert.equal(removalApplyBody?.commands?.[0]?.proposalId, removalRetryExchange.response.proposalId,
    'removal Apply must consume the receipt from the reopened removal preview');
  assert.notEqual(removalApplyStarted.request.commandId, removalRetryExchange.response.proposalId,
    'removal Apply must retain its own generated commandId separately from the candidate receipt');
  assert.equal(removalApplyExchange.response?.status, 200,
    'Group removal Apply must return HTTP 200 from its exact observed command request');
  assert.equal(removalApplyExchange.request.captureId, removalApplyExchange.response.captureId,
    'Group removal Apply response must retain its actual request capture identity');
  assert.equal(removalApplyExchange.response.commandId, removalApplyStarted.request.commandId,
    'Group removal Apply response must belong to the submitted commandId');
  report.target.qaIsolation.workflowMutations.push('native UI removed the applied Group to restore the source Patient rows');
  report.target.qaIsolation.cleanupMutations.push('native UI removed the case-owned Group after proving the ALL lifecycle');
  const removedBuilder = await readBuilder();
  assert.equal(removalApplyExchange.payload?.commandId, removalApplyStarted.request.commandId,
    'Group removal Apply payload must echo its exact generated commandId');
  assert.equal(removalApplyExchange.payload?.draftVersion, removedBuilder.draftVersion,
    'Group removal payload must match the independent saved draft version');
  assert.equal(removalApplyExchange.payload?.draftDigest, removedBuilder.draftDigest,
    'Group removal payload must match the independent saved draft digest');
  assert.deepEqual(removalApplyExchange.payload?.workspace, removedBuilder.workspace,
    'Group removal Apply payload must match the exact saved workspace read');
  const removedState = patientOneSavedState(removedBuilder, outputId);
  const removedGrid = await page.evaluate(readPatientOneRenderedGrid);
  assert.equal(removedState.group, null, 'applied removal must remove the exact saved Group');
  for (const sourceColumn of sourceSavedState.sourceColumns) {
    assert.deepEqual(removedState.sourceColumns.find(column => column.id === sourceColumn.id), sourceColumn,
      `Group removal must retain original source column ${sourceColumn.id}`);
  }
  policyOneEvidence.push({
    label: 'RemovalApplySubmitted',
    event: 'begin-apply',
    request: { ...removalApplyExchange.request, requestObjectId: removalApplyExchange.request.captureId,
      transportIndex: transportIndexForRequest(removalApplyStarted.rawRequest), body: removalApplyBody },
    observation: {
      workspace: policyWorkspaceProjection({ savedState: afterRemovalCancelState,
        renderedRows: cancelledGroupGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
      draftVersion: beforeRemovalApply.draftVersion,
      draftDigest: beforeRemovalApply.draftDigest,
      candidateId: removalRetryExchange.response.proposalId,
    },
  });
  policyOneEvidence.push({
    label: 'RemovalApplied',
    event: 'finish-apply',
    response: { ...removalApplyExchange.response, requestObjectId: removalApplyExchange.request.captureId,
      body: removalApplyExchange.payload },
    settlement: { kind: 'http-response', captureId: removalApplyExchange.request.captureId },
    savedBefore: afterRemovalCancelState,
    savedAfter: removedState,
    observation: {
      workspace: policyWorkspaceProjection({ savedState: removedState,
        renderedRows: removedGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
      draftVersion: removedBuilder.draftVersion,
      draftDigest: removedBuilder.draftDigest,
      removedProposalId: removalRetryExchange.response.proposalId,
    },
  });
  await page.getByRole('dialog', { name: 'Row definition settings', exact: true })
    .waitFor({ state: 'hidden' });
  const afterRemovalCloseFrame = await readBuilder();
  assert.deepEqual(afterRemovalCloseFrame.workspace, removedBuilder.workspace,
    'closing the already-dismissed row editor after removal must preserve the exact restored workspace');
  assert.equal(afterRemovalCloseFrame.draftVersion, removedBuilder.draftVersion,
    'closing the row editor after removal must preserve the restored draft version');
  assert.equal(afterRemovalCloseFrame.draftDigest, removedBuilder.draftDigest,
    'closing the row editor after removal must preserve the restored draft digest');
  await page.reload({ waitUntil: 'domcontentloaded' });
  const sourceReloadTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  assert.notEqual(sourceReloadTimeOrigin, allReloadTimeOrigin,
    'source restoration must be observed in a fresh browser document');
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator(renderedTableSelector).isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator(renderedTableSelector).waitFor({ state: 'visible' });
  const restoredBuilder = await readBuilder();
  assert.deepEqual(restoredBuilder.workspace, removedBuilder.workspace,
    'reload after removal must preserve the exact restored workspace');
  assert.equal(restoredBuilder.draftVersion, removedBuilder.draftVersion);
  assert.equal(restoredBuilder.draftDigest, removedBuilder.draftDigest);
  const restoredState = patientOneSavedState(restoredBuilder, outputId);
  assert.equal(restoredState.group, null, 'reload must retain the absence of the removed Group');
  for (const sourceColumn of sourceSavedState.sourceColumns) {
    assert.deepEqual(restoredState.sourceColumns.find(column => column.id === sourceColumn.id), sourceColumn,
      `reload after removal must retain original source column ${sourceColumn.id}`);
  }
  const restoredGrid = await page.evaluate(readPatientOneRenderedGrid);
  const restoredSourceIDIndex = findRenderedBuilderHeaderIndex(restoredGrid.headers, sourceIdColumn.label);
  assert(restoredSourceIDIndex >= 0,
    `source restoration must render original ${sourceIdColumn.label}: ${JSON.stringify(restoredGrid.headers)}`);
  assert.equal(restoredGrid.cells.length, fixtureIDs.length,
    'removing Group and reloading must restore both source Patient rows');
  assert.deepEqual(sorted(restoredGrid.cells.map(row => row[restoredSourceIDIndex]?.text)), fixtureIDs,
    'removing Group and reloading must restore the exact source Patient IDs');
  const restoredIDs = sorted(restoredGrid.cells.map(row => row[restoredSourceIDIndex]?.text));
  requireCheck(workflow, 'persistence', 'removing Group restores the original Patient fields and rows after reload', true,
    { headers: restoredGrid.headers, cells: restoredGrid.cells, patientIDs: restoredIDs,
      originalSourceColumns: sourceSavedState.sourceColumns, retainedColumns: restoredState.sourceColumns,
      draftVersion: restoredBuilder.draftVersion, draftDigest: restoredBuilder.draftDigest });
  policyOneEvidence.push({
    label: 'ReloadedSource',
    event: 'reload',
    documentTimeOrigin: sourceReloadTimeOrigin,
    savedBefore: removedState,
    savedAfter: restoredState,
    rendered: restoredGrid,
    rawPatientIDs: restoredIDs,
    observation: {
      workspace: policyWorkspaceProjection({ savedState: restoredState,
        renderedRows: restoredGrid, sourceState: sourceSavedState, fixtureIDs,
        addedColumnId: addedPatientIDColumnId }),
      draftVersion: restoredBuilder.draftVersion,
      draftDigest: restoredBuilder.draftDigest,
      documentTimeOrigin: sourceReloadTimeOrigin,
    },
  });
  report.target.qaIsolation.requiredLifecycleChecks = [
    'empty-key Group prepares one row for the two raw Patient records',
    'Patient.id ONE returns the exact multiple-values 422 for both raw IDs',
    'rejected Patient.id ONE leaves the saved Group and exact draft unchanged',
    'failed Patient.id ONE retains its selected field for same-editor ALL repair',
    'same-editor ALL repair response and applied grid match both literal raw Patient IDs',
    'ALL repair applies and persists exact Patient IDs after reload',
    'Cancel preserves the applied ALL Group and exact draft',
    'Group removal previews both original Patient rows',
    'removing Group restores the original Patient fields and rows after reload',
  ];

  const observedCaptures = [
    [groupPreviewStarted.rawRequest, groupPreviewExchange.request.captureId],
    [groupApplyStarted.rawRequest, groupApplyExchange.request.captureId],
    [oneStarted.rawRequest, oneExchange.request.captureId],
    [allStarted.rawRequest, allExchange.request.captureId],
    [allApplyStarted.rawRequest, allApplyExchange.request.captureId],
    [removalPreviewStarted.rawRequest, removalPreviewExchange.request.captureId],
    [removalRetryStarted.rawRequest, removalRetryExchange.request.captureId],
    [removalApplyStarted.rawRequest, removalApplyExchange.request.captureId],
  ];
  for (const [requestObject, requestCaptureId] of observedCaptures) {
    transportObserver.bindRequestCaptureId(requestObject, requestCaptureId);
  }
  const { captures: transportCaptures } = await transportObserver.finish();
  const { journeyTrace } = buildPolicyOneTrace({
    expected: {
      outputId,
      choiceId: oneChoice.choiceId,
      field: 'Patient.id',
      snapshotToken: beforeONE.catalog.snapshotToken,
      draftVersion: beforeONE.draftVersion,
      draftDigest: beforeONE.draftDigest,
      proposalPath: 'construction-choice-proposals',
      commandPath: 'commands',
      one: { policy: 'ONE', status: oneResponse.status(), errorCode: oneResponseBody.error.code },
      all: {
        policy: 'ALL', status: allResponse.status(), previewStatus: allResponseBody.previewStatus,
        rowCount: policyOnePreview.rowCount, memberIdsField: 'memberIds', countField: 'count',
        count: fixtureIDs.length,
      },
      apply: policyApplyTraceExpectation({
        savedState: allReloadSavedState,
        inputColumnId: reloadedPatientIDColumnId,
        renderedRows: rendered,
        status: allApplyExchange.response.status,
      }),
      editorIdentity: {
        editorId: selectedOneState.editorMountIdentity,
        documentTimeOrigin: selectedOneState.documentTimeOrigin,
      },
    },
    requests: {
      one: policyRequestCapture(oneStarted, oneRequestBody, transportIndexForRequest(oneStarted.rawRequest)),
      all: policyRequestCapture(allStarted, allRequestBody, transportIndexForRequest(allStarted.rawRequest)),
      apply: policyRequestCapture(allApplyStarted, allApplyBody, transportIndexForRequest(allApplyStarted.rawRequest)),
    },
    responses: {
      one: policyResponseCapture(oneStarted, oneExchange, oneResponseBody),
      all: policyResponseCapture(allStarted, allExchange, allResponseBody, { preview: policyOnePreview }),
      apply: policyResponseCapture(allApplyStarted, allApplyExchange, allApplyExchange.payload),
    },
    saved: {
      before: policySnapshot({ builder: beforeONE, outputId, sourceState: sourceSavedState,
        fixtureIDs, renderedRows: groupedGrid }),
      afterOne: policySnapshot({ builder: afterONE, outputId, sourceState: sourceSavedState,
        fixtureIDs, renderedRows: groupedGrid }),
      afterApply: policySnapshot({ builder: allAppliedBuilder, outputId, sourceState: sourceSavedState,
        fixtureIDs, renderedRows: allAppliedGrid, addedColumnId: addedPatientIDColumnId,
        choiceId: oneChoice.choiceId }),
    },
    dom: {
      beforeOne: editorTraceObservation(selectedOneState),
      afterOne: editorTraceObservation(oneFailureState),
      afterSelectAll: editorTraceObservation(recoveryState),
      afterAllSubmitted: editorTraceObservation(afterAllSubmittedState),
      afterAllReady: editorTraceObservation(allReadyState),
      afterApplySubmitted: editorTraceObservation(allApplyEditorAtSubmit),
      afterApply: editorTraceObservation(allAppliedEditor),
    },
    fixtureIDs,
    context: {
      name: 'builder-authoring:patient-one-disagreement',
      model: 'PolicyOneDisagreement',
      initialObservation: policyOneInitialObservation,
      initialPreview: policyOneInitialPreview,
      actions: policyOneEvidence,
      transportCaptures,
      formalEntry: policyOneFormalEntry,
      outcome: {
        kind: 'source-restored',
        workspace: policyWorkspaceProjection({ savedState: restoredState,
          renderedRows: restoredGrid, sourceState: sourceSavedState, fixtureIDs,
          addedColumnId: addedPatientIDColumnId }),
        patientIds: restoredIDs,
        draftVersion: restoredBuilder.draftVersion,
        draftDigest: restoredBuilder.draftDigest,
      },
    },
  });
  assert.deepEqual(journeyTrace.actions.map(action => action.label), [
    'GroupPreviewSubmitted', 'GroupPreviewReady', 'GroupApplySubmitted', 'GroupApplied',
    'OneSubmitted', 'OneRejected', 'SelectAll', 'AllSubmitted', 'AllReady',
    'AllApplySubmitted', 'AllApplied', 'ReloadedAll',
    'RemovalPreviewSubmitted', 'RemovalPreviewReady', 'RemovalCancelled',
    'RemovalPreviewSubmitted', 'RemovalPreviewReady', 'RemovalApplySubmitted',
    'RemovalApplied', 'ReloadedSource',
  ], 'Policy One journey trace must retain the complete observed Group, repair, reload, and restoration lifecycle');
  const { writeAndReplayBuilderJourneyTrace } = await import('../helpers/builder-journey-trace.mjs');
  const formalProof = writeAndReplayBuilderJourneyTrace({
    scenarioID: report.scenario,
    caseName: context.args.caseName,
    journeyTrace,
    report,
    fixture: {
      project: context.target.fixtureProject,
      generation: context.target.fixtureGeneration,
      explorer,
      outputId,
      patientIDs: [...fixtureIDs],
    },
    repositoryRoot,
  });
  requireCheck(workflow, 'correctness',
    'Captured Patient.id ONE/ALL browser journey replays through PolicyOneDisagreement TLC and passes all registered mutation controls',
    formalProof.status === 'pass' && formalProof.replayStatus === 'pass' && formalProof.selfTestStatus === 'pass',
    {
      tracePath: formalProof.tracePath,
      actionCount: journeyTrace.actions.length,
      replayStatus: formalProof.replayStatus,
      selfTestStatus: formalProof.selfTestStatus,
      mutationControlCount: formalProof.mutationControls?.length ?? 0,
    });
};
