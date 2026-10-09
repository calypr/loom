import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { sanitizeBody } from '../helpers/playwright-browser.mjs';
import { recordCheck } from '../helpers/report.mjs';
import { appendNullPaddingRows, builderRequestURL, builderResponseIdentity, constructionProposalPreviewEvidence, currentPublishedRevisionForOutput, displayAppendNullPaddingRows, findColumn, isCombineInputIDColumn, isNumericClickHouseType, isScalarStringColumn, joinOracleRows, appendEditorConfigurationEvidence, nativeCombineTargetBindingEvidence, sameSourceDocuments, snapshotSourceDocument, isOwnedConstructionCapabilitiesRequest, rootedEmptyTargetAppliedExpression, rootedEmptyTargetRestorationEvidence, savedAppendPreviewAppliedExpression, measureActionToDOMResult, combineCancellationPreservationEvidence } from '../helpers/builder-combine-helpers.mjs';

export const authoredColumn = (document, catalogColumn) => {
  const matches = (document.columns ?? []).filter(column => column.column === catalogColumn.name);
  if (matches.length !== 1) throw new Error(`Expected one authored ${catalogColumn.name} source column; found ${matches.length}.`);
  const wireColumn = matches[0];
  return { name: wireColumn.column, label: wireColumn.label };
};

const expectedPatients = [{ id: 'combine-fixture-patient', gender: 'female' }];
const expectedObservations = [
  { id: 'combine-observation-final-1', status: 'final', valueInteger: 10 },
  { id: 'combine-observation-final-2', status: 'final', valueInteger: 20 },
  { id: 'combine-observation-preliminary', status: 'preliminary', valueInteger: 30 },
  { id: 'combine-observation-unmatched', status: 'unknown', valueInteger: 40 },
];
const expectedReports = [
  { id: 'combine-observation-final-1', status: 'final' },
  { id: 'combine-observation-final-2', status: 'final' },
  { id: 'combine-observation-preliminary', status: 'preliminary' },
];
const captureConstructionCapabilitiesFailuresWithPlaywright = (page, report, owner) => {
  const requests = new Map();
  const onRequest = request => {
    if (!isOwnedConstructionCapabilitiesRequest({ requestURL: request.url(), method: request.method(), ...owner })) return;
    let parsed;
    try { parsed = request.postDataJSON(); } catch { parsed = undefined; }
    requests.set(request, {
      requestURL: new URL(request.url()).origin + new URL(request.url()).pathname,
      owner: { origin: new URL(owner.uiUrl).origin, project: owner.project, explorer: owner.explorer },
      requestBody: parsed ? {
        snapshotToken: parsed.snapshotToken,
        expectedDraftVersion: parsed.expectedDraftVersion,
        expectedDraftDigest: parsed.expectedDraftDigest,
        outputId: parsed.outputId,
        stageId: parsed.stageId,
      } : null,
    });
  };
  const onResponse = response => {
    const request = requests.get(response.request());
    if (!request || response.status() < 400) return;
    const diagnostic = { ...request, status: response.status(), mimeType: response.headers()['content-type'] ?? null, responseBody: null };
    report.constructionCapabilitiesFailures ??= [];
    report.constructionCapabilitiesFailures.push(diagnostic);
    void response.text().then(text => { diagnostic.responseBody = sanitizeBody(text); }, error => {
      diagnostic.responseReadError = error instanceof Error ? error.message : String(error);
    });
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  return { stop: () => {
    page.off('request', onRequest);
    page.off('response', onResponse);
  } };
};

const captureOwnedConstructionProposals = (page, target) => {
  const entries = [];
  const byRequest = new Map();
  const project = target.fixtureProject;
  const path = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(target.explorer)}/authoring/v2/construction-proposals`;
  const onRequest = request => {
    if (request.method() !== 'POST' || new URL(request.url()).origin !== new URL(target.uiUrl).origin || new URL(request.url()).pathname !== path) return;
    let body;
    try { body = request.postDataJSON(); } catch { return; }
    if (body?.outputId !== target.outputId) return;
    const entry = { request, sequence: entries.length + 1, url: new URL(request.url()).origin + path, body, status: null, responsePromise: null };
    entries.push(entry);
    byRequest.set(request, entry);
  };
  const onResponse = response => {
    const entry = byRequest.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    entry.responsePromise = response.json().then(value => {
      entry.response = value;
      return value;
    }).catch(error => {
      entry.response = { responseReadError: error instanceof Error ? error.message : String(error) };
      return entry.response;
    });
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  return {
    entries,
    stop: () => {
      page.off('request', onRequest);
      page.off('response', onResponse);
    },
  };
};

const check = (report, dimension, name, condition, evidence = {}) => {
  if (!['correctness', 'persistence', 'usability', 'performance'].includes(dimension) ||
      typeof name !== 'string' || name.trim() === '' || typeof condition !== 'boolean') {
    throw new TypeError('Combine checks require a known dimension, a string name, and a boolean condition.');
  }
  recordCheck(report, dimension, name, Boolean(condition), evidence);
  if (!condition) throw new Error('required Combine check failed: ' + name + '; evidence=' + JSON.stringify(evidence).slice(0, 1400));
};

const recordReloadTiming = (report, name, startedAt, evidence = {}) => {
  const elapsedMs = Date.now() - startedAt;
  report.timings[name] = elapsedMs;
  check(report, 'performance', name, elapsedMs <= 5000, { elapsedMs, limitMs: 5000, ...evidence });
};

export const openAppendNativeRequestScope = (nativeRequestLedger, target) => {
  if (!nativeRequestLedger?.openScope || typeof target?.fixtureProject !== 'string' ||
      !target.fixtureProject.trim() || typeof target.uiUrl !== 'string') {
    throw new TypeError('APPEND native request scope requires the fixture project, UI URL, and fixture request ledger.');
  }
  const origin = new URL(target.uiUrl).origin;
  return nativeRequestLedger.openScope({ project: target.fixtureProject, origin });
};

export const flushAppendNativeRequestScope = async ({
  nativeRequestLedger, scope, explorer, report, timeoutMs = 5000,
} = {}) => {
  if (!nativeRequestLedger?.flush || !scope || !report || typeof report !== 'object') {
    throw new TypeError('APPEND native request flush requires its ledger scope and workflow report.');
  }
  const snapshot = await nativeRequestLedger.flush(scope, { explorer, timeoutMs });
  Object.assign(report, {
    nativeRequests: snapshot.nativeRequests,
    nativeRequestDrainEvidence: snapshot.nativeRequestDrainEvidence,
    excludedNativeRequests: snapshot.excludedNativeRequests,
    excludedNativeRequestDrainEvidence: snapshot.excludedNativeRequestDrainEvidence,
    nativeRequestCorrelationErrors: snapshot.nativeRequestCorrelationErrors,
    nativeRequestTerminalLedger: snapshot.nativeRequestTerminalLedger,
  });
  if (!snapshot.nativeRequestTerminalLedger.complete) {
    const error = new Error('APPEND native request ledger did not reach a complete terminal state: ' +
      JSON.stringify(snapshot.nativeRequestTerminalLedger));
    error.nativeRequestSnapshot = snapshot;
    throw error;
  }
  return snapshot;
};

const parseNDJSON = (path) => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map((line) => JSON.parse(line));

const exactFixture = (fixtureDir) => {
  const ndjsonFiles = readdirSync(fixtureDir).filter((name) => name.endsWith('.ndjson')).sort();
  assert.deepEqual(ndjsonFiles, ['DiagnosticReport.ndjson', 'Observation.ndjson', 'Patient.ndjson']);
  const patients = parseNDJSON(join(fixtureDir, 'Patient.ndjson')).map(({ id, gender }) => ({ id, gender }));
  const observations = parseNDJSON(join(fixtureDir, 'Observation.ndjson')).map(({ id, status, valueInteger }) => ({ id, status, valueInteger }));
  const reports = parseNDJSON(join(fixtureDir, 'DiagnosticReport.ndjson')).map(({ id, status }) => ({ id, status }));
  assert.deepEqual(patients, expectedPatients);
  assert.deepEqual(observations, expectedObservations);
  assert.deepEqual(reports, expectedReports);
  return { patients, observations, diagnosticReports: reports };
};

const exactRows = (report, name, grid, headers, rows) => {
  const actualRows = [...grid.rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expectedRows = [...rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const ok = grid.ready && JSON.stringify(grid.headers) === JSON.stringify(headers) && JSON.stringify(actualRows) === JSON.stringify(expectedRows);
  check(report, 'correctness', name, ok, { headers: grid.headers, expectedHeaders: headers, rows: actualRows, expectedRows, ariaRowCount: grid.ariaRowCount });
};

const exactGridMatches = (grid, headers, rows) => {
  const actualRows = [...grid.rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expectedRows = [...rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const ok = grid.ready && JSON.stringify(grid.headers) === JSON.stringify(headers) && JSON.stringify(actualRows) === JSON.stringify(expectedRows);
  return { ok, headers: grid.headers, expectedHeaders: headers, rows: actualRows, expectedRows, ariaRowCount: grid.ariaRowCount };
};

const requestJSON = async (url, body) => {
  const response = await fetch(url, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : null; } catch { value = { responseText: text.slice(0, 500) }; }
  if (!response.ok) throw new Error('API request ' + new URL(url).pathname + ' returned HTTP ' + response.status + ': ' + JSON.stringify(value).slice(0, 900));
  return value;
};

const apiRoot = (context, explorer) =>
  context.target.apiUrl + '/api/v1/projects/' + encodeURIComponent(context.target.fixtureProject) +
  '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2';
const readBuilder = (context, explorer) => requestJSON(builderRequestURL(
  context.target.apiUrl, context.target.fixtureProject, explorer,
).toString());

const readPublishedInputs = async (context, explorer, builder) => {
  const endpoint = apiRoot(context, explorer) + '/construction-inputs';
  const entries = [];
  let cursor;
  do {
    const response = await requestJSON(endpoint, {
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      ...(cursor ? { cursor } : {}),
      limit: 100,
    });
    if (
      response.snapshotToken !== builder.catalog.snapshotToken ||
      response.draftVersion !== builder.draftVersion ||
      response.draftDigest !== builder.draftDigest ||
      response.datasetGeneration !== builder.catalog.generation
    ) throw new Error('Published-input read did not preserve the exact Builder snapshot and generation.');
    entries.push(...response.entries);
    cursor = response.nextCursor || undefined;
  } while (cursor);
  return entries;
};

const documentByRoot = (builder, resourceType) => {
  const matches = (builder.workspace?.documents ?? []).filter((document) => document.rootResourceType === resourceType);
  if (matches.length !== 1) throw new Error('Expected exactly one ' + resourceType + ' source table; found ' + matches.length + '.');
  return matches[0];
};

const documentByOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter((document) => document.output?.id === outputId);
  if (matches.length !== 1) throw new Error('Expected one target table ' + outputId + '; found ' + matches.length + '.');
  return matches[0];
};

const sourceDocumentSnapshot = snapshotSourceDocument;

const currentRevisionFor = (entries, document) =>
  currentPublishedRevisionForOutput(entries, document.output?.id);

const sourceAPI = async (context, explorer, explorerTitle, sourceDocs, report) => {
  const started = Date.now();
  const builder = await readBuilder(context, explorer);
  const initialScope = builder.catalog?.authorizationScopeDigest;
  const expectedSourceIdentity = [
    { rootResourceType: 'Observation', title: 'Observations', outputId: sourceDocs.observation.output.id },
    { rootResourceType: 'DiagnosticReport', title: 'Diagnostic reports', outputId: sourceDocs.report.output.id },
  ];
  if (sourceDocs.patient) expectedSourceIdentity.push({ rootResourceType: 'Patient', title: 'Patients', outputId: sourceDocs.patient.output.id });
  const identity = builderResponseIdentity(builder, context.target.apiUrl, context.target.fixtureProject, explorer, explorerTitle, expectedSourceIdentity);
  check(report, 'correctness', 'Builder response matches the exact project-scoped route, workspace title, and source output identities',
    identity.bound, { ...identity, project: context.target.fixtureProject, expectedWorkspaceTitle: explorerTitle });
  if (!identity.bound) throw new Error('The Builder route and workspace do not match the created Explorer and source tables.');
  const scopeInvalid =
    builder.catalog?.generation !== context.target.fixtureGeneration ||
    !builder.catalog?.snapshotToken ||
    !builder.draftDigest ||
    !initialScope;
  check(report, 'correctness', 'source Builder snapshot is bound to the expected generation, draft, and authorization scope',
    !scopeInvalid, { generation: builder.catalog?.generation, expectedGeneration: context.target.fixtureGeneration,
      snapshotToken: builder.catalog?.snapshotToken, draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest, authorizationScopeDigest: initialScope });
  if (scopeInvalid) throw new Error('The source Builder snapshot is not bound to the expected generation, draft, and authorization scope.');
  const entries = await readPublishedInputs(context, explorer, builder);
  report.publishedSourceCatalog = { snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, entries };
  const revisions = {
    Observation: currentRevisionFor(entries, sourceDocs.observation),
    DiagnosticReport: currentRevisionFor(entries, sourceDocs.report),
  };
  if (sourceDocs.patient) revisions.Patient = currentRevisionFor(entries, sourceDocs.patient);
  const columns = {
    observationID: findColumn(revisions.Observation, 'Observation', 'id'),
    observationStatus: findColumn(revisions.Observation, 'Observation', 'status'),
    observationInteger: findColumn(revisions.Observation, 'Observation', 'valueInteger'),
    reportID: findColumn(revisions.DiagnosticReport, 'DiagnosticReport', 'id'),
    reportStatus: findColumn(revisions.DiagnosticReport, 'DiagnosticReport', 'status'),
  };
  if (sourceDocs.patient) {
    columns.patientID = findColumn(revisions.Patient, 'Patient', 'id');
    columns.patientGender = findColumn(revisions.Patient, 'Patient', 'gender');
  }
  const appendSources = Boolean(sourceDocs.patient);
  const idColumnSupportsCase = (column) => isCombineInputIDColumn(column, appendSources ? 'APPEND' : 'KEY_JOIN');
  const schemaSupportsCase =
    idColumnSupportsCase(columns.observationID) &&
    idColumnSupportsCase(columns.reportID) &&
    isScalarStringColumn(columns.observationStatus) &&
    isScalarStringColumn(columns.reportStatus) &&
    isNumericClickHouseType(columns.observationInteger.clickhouseType) &&
    (!sourceDocs.patient || (isScalarStringColumn(columns.patientID) && isScalarStringColumn(columns.patientGender)));
  const schemaCheckName = sourceDocs.patient
    ? 'source schemas expose scalar IDs, status and gender fields, and a numeric Observation value'
    : 'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value';
  check(report, 'correctness', schemaCheckName,
    schemaSupportsCase, {
      observationID: columns.observationID,
      reportID: columns.reportID,
      observationStatus: columns.observationStatus,
      reportStatus: columns.reportStatus,
      patientID: columns.patientID ?? null,
      patientGender: columns.patientGender ?? null,
      observationInteger: columns.observationInteger,
    });
  if (!schemaSupportsCase) throw new Error('Published source schema does not support the declared Combine case.');
  const sourceSnapshot = sourceDocs.documents.map(sourceDocumentSnapshot);
  return {
    builder,
    builderIdentity: identity,
    entries,
    revisions,
    columns,
    sourceSnapshot,
    sourceOutputIds: sourceDocs.documents.map((document) => document.output.id).sort(),
    apiFingerprint: createHash('sha256').update(JSON.stringify({
      project: context.target.fixtureProject,
      generation: builder.catalog.generation,
      explorer,
      authorizationScopeDigest: initialScope,
      snapshotToken: builder.catalog.snapshotToken,
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
      revisions: Object.fromEntries(Object.entries(revisions).map(([key, entry]) => [key, {
        tableId: entry.tableId,
        revisionId: entry.revisionId,
        outputId: entry.outputId,
        columns: entry.columns,
      }])),
    })).digest('hex'),
    apiElapsedMs: Date.now() - started,
  };
};

const publishedRef = (entry) => JSON.stringify([entry.tableId, entry.revisionId, entry.outputId]);

const readTargetDocument = (builder, outputId) => documentByOutput(builder, outputId);

const assertPinnedInputs = (report, step, api) => {
  const actual = step.inputs.map((input) => [input.tableId, input.revisionId, input.outputId]);
  const expected = [api.revisions.Observation, api.revisions.DiagnosticReport, api.revisions.Patient]
    .slice(0, step.inputs.length)
    .map((entry) => [entry.tableId, entry.revisionId, entry.outputId]);
  check(report, 'persistence', 'saved Combine step refers to the exact published source revisions',
    JSON.stringify(actual) === JSON.stringify(expected), { actual, expected });
};

const assertSourceImmutability = async (context, explorer, docs, before, report) => {
  const after = await readBuilder(context, explorer);
  const current = docs.documents.map((document) => sourceDocumentSnapshot(readTargetDocument(after, document.output.id)));
  const identity = builderResponseIdentity(after, context.target.apiUrl, context.target.fixtureProject, explorer,
    before.builder.workspace.explorer.title, docs.documents.map((document) => ({
      rootResourceType: document.rootResourceType,
      title: document.output.title,
      outputId: document.output.id,
    })));
  const sourceTableNames = docs.patient
    ? 'Patient, Observation, and DiagnosticReport'
    : 'Observation and DiagnosticReport';
  check(report, 'persistence', 'published ' + sourceTableNames + ' source tables remain unchanged',
    sameSourceDocuments(current, before.sourceSnapshot),
    { outputIds: before.sourceOutputIds, before: before.sourceSnapshot, after: current });
  const scopeStable = identity.bound &&
    after.catalog?.generation === context.target.fixtureGeneration &&
    after.catalog?.authorizationScopeDigest === before.builder.catalog.authorizationScopeDigest;
  check(report, 'persistence', 'project, generation, Explorer, and authorization scope remain stable', scopeStable, {
    project: context.target.fixtureProject,
    generation: after.catalog?.generation,
    explorerRoute: identity.requestURL,
    workspaceTitle: identity.workspaceTitle,
    sourceOutputIds: identity.outputIDs,
    authorizationScopeDigest: after.catalog?.authorizationScopeDigest,
    expectedAuthorizationScopeDigest: before.builder.catalog.authorizationScopeDigest,
    snapshotToken: after.catalog?.snapshotToken,
    draftVersion: after.draftVersion,
    draftDigest: after.draftDigest,
  });
  return after;
};

const expectedInnerRows = joinOracleRows(expectedObservations, expectedReports, 'INNER');
const expectedLeftRows = joinOracleRows(expectedObservations, expectedReports, 'LEFT');
const joinHeaders = ['Observation ID', 'Status', 'DiagnosticReport ID', 'Status'];
const appendHeaders = ['Record ID', 'Status', 'Patient gender'];

const readGridWithPlaywright = async (page, kind = 'saved') => page.evaluate((viewKind) => {
  const proposal = viewKind === 'proposal';
  const table = proposal
    ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"] table')
    : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return { ready: false, headers: [], rows: [], ariaRowCount: null };
  const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const headers = proposal
    ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
  const rows = proposal
    ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
  return { ready: true, headers, rows, ariaRowCount: table.getAttribute('aria-rowcount') };
}, kind);

const exactRowsWithPlaywright = async (report, name, page, kind, headers, rows) => {
  const grid = await readGridWithPlaywright(page, kind);
  exactRows(report, name, grid, headers, rows);
  return grid;
};

const waitSavedPreviewWithPlaywright = async (page, count) => page.waitForFunction(expected => {
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return preview?.getAttribute('data-preview-status') === 'ready' && table?.getAttribute('aria-rowcount') === String(expected + 1) &&
    !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
}, count, { timeout: 5000 });

const waitProposalWithPlaywright = async (page, outputId, count) => page.waitForFunction(({ expectedOutput, expectedRows }) => {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]');
  const preview = document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]');
  return Boolean(panel && preview && panel.getAttribute('data-proposal-id') &&
    preview.getAttribute('data-preview-receipt-id') === panel.getAttribute('data-proposal-id') &&
    preview.getAttribute('data-preview-output-id') === expectedOutput &&
    (expectedRows === undefined || preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === expectedRows));
}, { expectedOutput: outputId, expectedRows: count }, { timeout: 5000 });

const selectInputWithPlaywright = async (page, action, selector, value, label) => {
  const control = page.locator(selector);
  await action(label, control, () => control.selectOption(String(value)));
  const actual = await control.inputValue();
  assert.equal(actual, String(value), `${label} must retain its exact selected value`);
};

const fillWithPlaywright = async (page, action, selector, value, label) => {
  const control = page.locator(selector);
  await action(label, control, () => control.fill(value), { editable: true });
};

const applyProposalWithPlaywright = async (page, action, label, savedRows, outputId) => {
  const apply = page.getByTestId('construction-apply-proposal');
  await action(label, apply, () => apply.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 5000 });
      if (savedRows === null) {
        await page.waitForFunction(expectedOutput => {
          const selected = document.querySelector(`[data-testid="construction-table-${CSS.escape(expectedOutput)}"]`);
          const preview = document.querySelector('[data-testid="preview-table-scroll"]');
          return selected?.getAttribute('aria-current') === 'page' &&
            !document.querySelector('[data-testid="construction-history"]') &&
            !document.querySelector('[data-testid="construction-combine-editor"]') &&
            preview?.textContent?.trim() === 'Add a column to see your table.';
        }, outputId, { timeout: 5000 });
      } else {
        await page.getByTestId('construction-history').waitFor({ state: 'visible', timeout: 5000 });
        await waitSavedPreviewWithPlaywright(page, savedRows);
      }
    },
  });
};

const selectTargetWithPlaywright = async (page, action, outputId) => {
  const table = page.getByTestId(`construction-table-${outputId}`);
  if (await table.getAttribute('aria-current') !== 'page') {
    await action('select Combine target table', table, () => table.click(), {
      after: () => page.waitForFunction(id => document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-current') === 'page', outputId),
    });
  }
};

const appendReloadToVisibleResultWithPlaywright = async ({ page, action, report, name, outputId, headers, rows, rootedEmpty = false }) => {
  const expression = rootedEmpty
    ? rootedEmptyTargetAppliedExpression(outputId)
    : savedAppendPreviewAppliedExpression(outputId, headers, rows);
  const measurement = await measureActionToDOMResult({
    action: async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: 5000 });
      await selectTargetWithPlaywright(page, action, outputId);
    },
    waitForResult: timeoutMs => page.waitForFunction(expression, undefined, { timeout: timeoutMs }),
  });
  report.timings[name] = measurement.elapsedMs;
  check(report, 'performance', name, measurement.withinBudget, {
    elapsedMs: measurement.elapsedMs,
    limitMs: measurement.budgetMs,
    outputId,
    result: rootedEmpty ? 'exact rooted empty target DOM' : 'exact saved APPEND row/header DOM',
    rowCount: rootedEmpty ? 0 : rows.length,
    headers: rootedEmpty ? [] : headers,
  });
  return measurement;
};

const editSavedStepWithPlaywright = async (page, action, stepId) => {
  const history = page.getByTestId(`construction-history-step-${stepId}`);
  await action('select saved Combine step', history, () => history.click(), {
    after: async () => {
      await page.getByTestId(`construction-edit-step-${stepId}`).waitFor({ state: 'visible' });
      await page.waitForFunction(id => {
        const button = document.querySelector(`[data-testid="construction-edit-step-${CSS.escape(id)}"]`);
        return Boolean(button && !button.disabled);
      }, stepId, { timeout: 5000 });
    },
  });
  const edit = page.getByTestId(`construction-edit-step-${stepId}`);
  await action('open saved Combine editor and load pinned sources', edit, () => edit.click(), {
    after: async () => {
      await page.locator('[data-testid="construction-combine-editor"]').waitFor({ state: 'visible' });
      await page.locator('select[aria-label="Input table 1"]').waitFor({ state: 'visible' });
      await page.locator('input[aria-label="Output field 1 label"]').waitFor({ state: 'visible' });
      await page.waitForFunction(() => !document.querySelector('select[aria-label="Input table 1"]')?.disabled &&
        !document.querySelector('input[aria-label="Output field 1 label"]')?.disabled, undefined, { timeout: 5000 });
    },
  });
};

const createAndPublishSourcesWithPlaywright = async (context, page, action, report, includePatient = false, rawFieldsByResource = {}) => {
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await action('open source Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
  });
  const title = `Verify ${context.runID.slice(-10)} combine`;
  await fillWithPlaywright(page, action, '#new-explorer-name', title, 'name source Explorer');
  const create = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank source Explorer', create, () => create.click(), {
    timeout: 5000,
    after: () => page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected &&
      document.body.innerText.includes('Build your first table'), title),
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  report.target.explorer = explorer;

  const addRootWithUI = async (resourceType, tableTitle, expectedIDs) => {
    await fillWithPlaywright(page, action, '#first-table-name', tableTitle, `name ${resourceType} source table`);
    const choose = page.getByRole('button', { name: `Choose ${resourceType} rows`, exact: true });
    await action(`create ${resourceType} source table with its direct identity`, choose, () => choose.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
        await waitSavedPreviewWithPlaywright(page, expectedIDs.length);
      },
    });
    const grid = await readGridWithPlaywright(page);
    const wanted = resourceType.toLowerCase() + ' id';
    const idIndex = grid.headers.findIndex(header => header.toLowerCase() === wanted || header.toLowerCase() === 'id');
    const ids = idIndex < 0 ? [] : grid.rows.map(row => row[idIndex]).sort();
    check(report, 'correctness', resourceType + ' source starts with every literal fixture identity',
      JSON.stringify(ids) === JSON.stringify([...expectedIDs].sort()), { headers: grid.headers, ids, expectedIDs });
  };

  const addRawFieldsWithUI = async (resourceType, fieldPaths, expectedRows) => {
    const addColumns = page.getByTestId('construction-action-add-columns');
    await action(`open ${resourceType} source Add columns`, addColumns, () => addColumns.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' }),
    });
    const fields = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await action('open Fields and related data', fields, () => fields.click());
    const raw = page.getByText('Raw FHIR fields (advanced)', { exact: true });
    await action('open raw FHIR fields', raw, () => raw.click());
    for (const path of fieldPaths) {
      const checkbox = page.getByRole('checkbox', { name: `Select ${resourceType}.${path}`, exact: true });
      await action(`select ${resourceType}.${path}`, checkbox, () => checkbox.check());
    }
    const addLabel = `Add ${fieldPaths.length} selected feature${fieldPaths.length === 1 ? '' : 's'}`;
    const add = page.getByRole('button', { name: addLabel, exact: true });
    await action(`add ${resourceType} source fields`, add, () => add.click(), {
      after: () => page.getByRole('button', { name: 'Apply columns', exact: true }).waitFor({ state: 'visible' }),
    });
    const apply = page.getByRole('button', { name: 'Apply columns', exact: true });
    await action(`apply ${resourceType} source fields and render its preview`, apply, () => apply.click(), {
      timeout: 5000,
      budget: 5000,
      after: () => waitSavedPreviewWithPlaywright(page, expectedRows),
    });
    const sourcePreview = await readGridWithPlaywright(page);
    if (!sourcePreview.ready) throw new Error(`${resourceType} source preview did not expose its authored headers.`);
    const close = page.getByRole('button', { name: 'Close operation editor', exact: true });
    await action('close source operation editor', close, () => close.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'hidden' }),
    });
    return sourcePreview;
  };

  await addRootWithUI('Observation', 'Observations', expectedObservations.map(row => row.id));
  const observationSourcePreview = await addRawFieldsWithUI('Observation', rawFieldsByResource.Observation ?? ['status', 'valueInteger'], expectedObservations.length);
  const newTable = page.getByTestId('construction-new-table');
  await action('start DiagnosticReport source table', newTable, () => newTable.click(), {
    after: () => page.locator('#first-table-name').waitFor({ state: 'visible' }),
  });
  await addRootWithUI('DiagnosticReport', 'Diagnostic reports', expectedReports.map(row => row.id));
  const reportSourcePreview = await addRawFieldsWithUI('DiagnosticReport', rawFieldsByResource.DiagnosticReport ?? ['status'], expectedReports.length);
  const sourcePreviewHeaders = { Observation: observationSourcePreview.headers, DiagnosticReport: reportSourcePreview.headers };
  if (includePatient) {
    await action('start Patient source table', newTable, () => newTable.click(), {
      after: () => page.locator('#first-table-name').waitFor({ state: 'visible' }),
    });
    await addRootWithUI('Patient', 'Patients', expectedPatients.map(row => row.id));
    await addRawFieldsWithUI('Patient', rawFieldsByResource.Patient ?? ['gender'], expectedPatients.length);
  }

  const publishPath = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/publish`;
  const publishResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
    new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === publishPath, { timeout: 5000 });
  const publish = page.getByRole('button', { name: 'Publish', exact: true });
  await action(includePatient ? 'publish all three exact source tables' : 'publish both exact source tables', publish, () => publish.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      const response = await publishResponse;
      assert(response.status() >= 200 && response.status() < 300, `source table publication returned HTTP ${response.status()}`);
    },
  });
  const publishedResponse = await publishResponse;
  check(report, 'correctness', 'native source-table publication completed successfully', publishedResponse.status() >= 200 && publishedResponse.status() < 300,
    { status: publishedResponse.status(), path: publishPath });

  const builder = await readBuilder(context, explorer);
  const observation = documentByRoot(builder, 'Observation');
  const diagnosticReport = documentByRoot(builder, 'DiagnosticReport');
  const docs = { observation, report: diagnosticReport, documents: [observation, diagnosticReport] };
  if (includePatient) {
    docs.patient = documentByRoot(builder, 'Patient');
    docs.documents.push(docs.patient);
  }
  const api = await sourceAPI(context, explorer, title, docs, report);
  check(report, 'correctness', 'source revisions are published in the exact project, generation, and authorization scope',
    api.revisions.Observation.tableId && api.revisions.DiagnosticReport.tableId &&
    (!includePatient || api.revisions.Patient.tableId) && api.builder.catalog.generation === context.target.fixtureGeneration &&
    api.builder.catalog.authorizationScopeDigest && api.entries.length >= 2,
    { project: context.target.fixtureProject, generation: api.builder.catalog.generation,
      authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest,
      revisions: Object.fromEntries(Object.entries(api.revisions).map(([key, entry]) => [key, {
        tableId: entry.tableId, revisionId: entry.revisionId, outputId: entry.outputId, isCurrent: entry.isCurrent,
      }])) });
  check(report, 'performance', 'published source API and schema fingerprint captured within five seconds', api.apiElapsedMs <= 5000,
    { elapsedMs: api.apiElapsedMs, fingerprint: api.apiFingerprint, generation: api.builder.catalog.generation,
      authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest });
  report.target.sourceExplorer = explorer;
  report.target.sourceExplorerTitle = title;
  report.target.fixtureRawOracle = {
    fixtureDirectory: context.target.fixtureDir,
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    patients: expectedPatients,
    observations: expectedObservations,
    diagnosticReports: expectedReports,
    expectedJoin: { innerRows: 3, leftRows: 4, key: 'required resource id', unmatchedObservation: 'combine-observation-unmatched' },
  };
  report.target.combineSourceInputs = Object.fromEntries(Object.entries(api.revisions).map(([key, entry]) => [key, {
    tableId: entry.tableId, revisionId: entry.revisionId, outputId: entry.outputId,
    tableTitle: entry.tableTitle, outputTitle: entry.outputTitle, generation: api.builder.catalog.generation,
    authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest,
  }]));
  report.target.sourceApiFingerprint = api.apiFingerprint;
  return { explorer, docs, api, sourceExplorerTitle: title, sourcePreviewHeaders };
};

const startCombineTargetWithPlaywright = async (context, page, action, report, explorer, observationOutputId, sourceBuilder, { recordTargetCheck = true } = {}) => {
  await selectTargetWithPlaywright(page, action, observationOutputId);
  const project = context.target.fixtureProject;
  const commandPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const createRequest = request => {
    if (request.method() !== 'POST' || new URL(request.url()).origin !== new URL(context.target.uiUrl).origin ||
      new URL(request.url()).pathname !== commandPath) return false;
    let body;
    try { body = request.postDataJSON(); } catch { return false; }
    return body?.commands?.some(command => command?.type === 'CREATE_TABLE');
  };
  const commandResponse = page.waitForResponse(response => createRequest(response.request()), { timeout: 5000 });
  const operationChooser = page.getByTestId('construction-action-combine');
  await action('open Combine operation chooser', operationChooser, () => operationChooser.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]').waitFor({ state: 'visible', timeout: 5000 });
      await page.getByTestId('construction-combine-editor').waitFor({ state: 'visible', timeout: 5000 });
      await page.getByTestId('construction-combine-choice-key_join').waitFor({ state: 'visible', timeout: 5000 });
    },
  });
  const response = await commandResponse;
  const request = response.request();
  const body = request.postDataJSON();
  const responseBody = await response.json();
  const mountedOutputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]').getAttribute('data-output-id');
  const expectedRootNodeIds = (sourceBuilder.catalog?.nodes ?? []).filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
  const previousOutputIds = (sourceBuilder.workspace?.documents ?? []).map(document => document.output?.id).filter(Boolean);
  const evidence = nativeCombineTargetBindingEvidence({
    requestBody: body,
    responseStatus: response.status(),
    response: responseBody,
    expectedRootNodeIds,
    expectedRootResourceType: 'Observation',
    previousOutputIds,
    mountedOutputId,
  });
  if (recordTargetCheck) {
    check(report, 'correctness', 'native Combine creates a rooted empty Observation target without adding an authored step or output column', evidence.ok, evidence);
  }
  if (!evidence.ok) throw new Error('Native Combine target identity did not bind its creation command, returned workspace, and mounted editor: ' + JSON.stringify(evidence));
  return { outputId: evidence.outputId, rootNodeId: evidence.rootNodeId, creationEvidence: evidence };
};

const chooseOperationWithPlaywright = async (page, action, kind, inputs) => {
  const choice = page.getByTestId(`construction-combine-choice-${kind.toLowerCase()}`);
  await action(`choose ${kind} and load the initial two input selectors`, choice, () => choice.click(), {
    timeout: 5000,
    after: async () => {
      await page.locator('select[aria-label="Input table 1"]').waitFor({ state: 'visible' });
      await page.locator('select[aria-label="Input table 2"]').waitFor({ state: 'visible' });
    },
  });
  for (let index = 0; index < inputs.length; index += 1) {
    if (index >= 2) {
      assert.equal(kind, 'APPEND', 'Only APPEND may add inputs beyond two');
      const addAnother = page.getByRole('button', { name: 'Add another table', exact: true });
      await action(`add APPEND input table ${index + 1}`, addAnother, () => addAnother.click(), {
        after: () => page.locator(`select[aria-label="Input table ${index + 1}"]`).waitFor({ state: 'visible' }),
      });
    }
    const value = publishedRef(inputs[index]);
    await selectInputWithPlaywright(page, action, `select[aria-label="Input table ${index + 1}"]`, value, `select exact published input revision ${index + 1}`);
  }
};

const outputDefaultName = (column, previousNames) => {
  const base = String(column.name ?? '').trim().replace(/[^A-Za-z0-9_]+/g, '_').replace(/^[^A-Za-z_]+/, '') || 'column';
  let candidate = base;
  let suffix = 2;
  while (previousNames.includes(candidate)) candidate = `${base}_${suffix++}`;
  return candidate;
};

const outputDefaultEvidenceWithPlaywright = async (page, index, expectedColumn, expectedName = expectedColumn.name) => {
  const name = await page.locator(`input[aria-label="Output field ${index} name"]`).inputValue();
  const label = await page.locator(`input[aria-label="Output field ${index} label"]`).inputValue();
  const expectedLabel = String(expectedColumn.label ?? '').trim() || expectedColumn.name;
  return { index, name, label, expectedName, expectedLabel, ok: name === expectedName && label === expectedLabel };
};

const configureOutputWithPlaywright = async (page, action, index, name, label, sourceFields, kind, { automaticDefault = false, expectedName } = {}) => {
  const nameSelector = `input[aria-label="Output field ${index} name"]`;
  await page.locator(nameSelector).waitFor({ state: 'visible', timeout: 5000 });
  if (!automaticDefault) {
    await fillWithPlaywright(page, action, nameSelector, name, `name output field ${index}`);
    await fillWithPlaywright(page, action, `input[aria-label="Output field ${index} label"]`, label, `label output field ${index}`);
  }
  for (const [inputIndex, columnId] of sourceFields) {
    const fieldKind = kind === 'APPEND' ? 'matching field in input ' : 'source field in input ';
    const value = kind === 'APPEND' ? (columnId === null ? 'empty-for-this-table' : 'column:' + columnId) : columnId;
    await selectInputWithPlaywright(page, action,
      `select[aria-label="Output field ${index} ${fieldKind}${inputIndex + 1}"]`, value,
      `map output field ${index} from input ${inputIndex + 1}`);
  }
  return automaticDefault && sourceFields.length === 1
    ? outputDefaultEvidenceWithPlaywright(page, index, sourceFields[0][2], expectedName)
    : undefined;
};

const addOutputWithPlaywright = async (page, action, index, name, label, sourceFields, kind, options) => {
  const add = page.getByRole('button', { name: 'Add output field', exact: true });
  await action(`add output field ${index}`, add, () => add.click(), {
    after: () => page.locator(`input[aria-label="Output field ${index} name"]`).waitFor({ state: 'visible' }),
  });
  return configureOutputWithPlaywright(page, action, index, name, label, sourceFields, kind, options);
};

const removeCombineAndRestoreEmptyRootWithPlaywright = async (context, page, action, report, explorer, target, baseline, stepId, operation) => {
  const history = page.getByTestId(`construction-history-step-${stepId}`);
  await action('select saved Combine step for removal', history, () => history.click(), {
    after: () => page.getByTestId(`construction-remove-step-${stepId}`).waitFor({ state: 'visible' }),
  });
  const remove = page.getByTestId(`construction-remove-step-${stepId}`);
  await action(`preview ${operation} removal`, remove, () => remove.click(), {
    timeout: 5000,
    after: () => waitProposalWithPlaywright(page, target.outputId),
  });
  check(report, 'correctness', operation + ' removal proposal is ready for the rooted empty target', true, { outputId: target.outputId, stepId });
  await applyProposalWithPlaywright(page, action, `Remove ${operation} and restore the rooted empty target`, null, target.outputId);
  const reloadTimingName = operation === 'KEY_JOIN'
    ? 'KEY_JOIN removal reload through exact rooted empty restoration within five seconds'
    : operation === 'APPEND'
      ? 'APPEND removal reload reaches the exact rooted empty target within five seconds'
      : undefined;
  const reloadStartedAt = reloadTimingName && operation === 'KEY_JOIN' ? Date.now() : undefined;
  if (operation === 'APPEND') {
    await appendReloadToVisibleResultWithPlaywright({
      page, action, report, name: reloadTimingName, outputId: target.outputId, rootedEmpty: true,
    });
  } else {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
    await selectTargetWithPlaywright(page, action, target.outputId);
    if (reloadTimingName) {
      await page.waitForFunction(rootedEmptyTargetAppliedExpression(target.outputId), undefined, { timeout: 5000 });
    }
  }
  const builder = await readBuilder(context, explorer);
  const restored = readTargetDocument(builder, target.outputId);
  const restoration = rootedEmptyTargetRestorationEvidence(restored, baseline, target);
  check(report, 'persistence', 'removing ' + operation + ' and reloading restores the rooted empty target', restoration.ok, {
    target, rootResourceType: restored.rootResourceType, columns: restored.columns, construction: restored.construction,
    sameAsPreCombineDocument: restoration.unchanged, expectedDocument: baseline,
  });
  if (reloadTimingName && operation === 'KEY_JOIN') {
    recordReloadTiming(report, reloadTimingName, reloadStartedAt, { outputId: target.outputId, stepId });
  }
};

export const joinWorkflow = async ({ page, report, action }, context) => {
  assert.equal(context.custom, false, 'Combine authoring requires an owned isolated fixture.');
  assert.equal(context.seed?.fresh, true, 'Combine authoring requires a fresh verification project.');
  const fixture = exactFixture(context.target.fixtureDir);
  report.target.fixtureRawOracle = fixture;
  check(report, 'correctness', 'fixture contains one bootstrap Patient, four Observations, and three DiagnosticReports',
    fixture.patients.length === 1 && fixture.observations.length === 4 && fixture.diagnosticReports.length === 3, fixture);

  const prepared = await createAndPublishSourcesWithPlaywright(context, page, action, report);
  const { explorer, docs, api, sourceExplorerTitle, sourcePreviewHeaders } = prepared;
  const target = await startCombineTargetWithPlaywright(context, page, action, report, explorer, docs.observation.output.id, api.builder);
  report.target.combineTarget = target;
  const builderAtTarget = await readBuilder(context, explorer);
  const emptyTargetBaseline = readTargetDocument(builderAtTarget, target.outputId);
  const catalogEntries = await readPublishedInputs(context, explorer, builderAtTarget);
  const observationRevision = currentRevisionFor(catalogEntries, docs.observation);
  const reportRevision = currentRevisionFor(catalogEntries, docs.report);
  const authored = {
    observationID: authoredColumn(docs.observation, api.columns.observationID),
    observationStatus: authoredColumn(docs.observation, api.columns.observationStatus),
    reportID: authoredColumn(docs.report, api.columns.reportID),
    reportStatus: authoredColumn(docs.report, api.columns.reportStatus),
  };
  const authoredSourceMetadataEvidence = {
    observation: {
      tableTitle: observationRevision.tableTitle,
      expectedTableTitle: sourceExplorerTitle,
      outputTitle: observationRevision.outputTitle,
      expectedOutputTitle: docs.observation.output.title,
      previewHeaders: sourcePreviewHeaders.Observation,
      columns: [
        { name: api.columns.observationID.name, label: api.columns.observationID.label, authoredName: authored.observationID.name, authoredLabel: authored.observationID.label, expectedLabel: 'Observation ID' },
        { name: api.columns.observationStatus.name, label: api.columns.observationStatus.label, authoredName: authored.observationStatus.name, authoredLabel: authored.observationStatus.label, expectedLabel: 'Status' },
      ],
    },
    diagnosticReport: {
      tableTitle: reportRevision.tableTitle,
      expectedTableTitle: sourceExplorerTitle,
      outputTitle: reportRevision.outputTitle,
      expectedOutputTitle: docs.report.output.title,
      previewHeaders: sourcePreviewHeaders.DiagnosticReport,
      columns: [
        { name: api.columns.reportID.name, label: api.columns.reportID.label, authoredName: authored.reportID.name, authoredLabel: authored.reportID.label, expectedLabel: 'DiagnosticReport ID' },
        { name: api.columns.reportStatus.name, label: api.columns.reportStatus.label, authoredName: authored.reportStatus.name, authoredLabel: authored.reportStatus.label, expectedLabel: 'Status' },
      ],
    },
  };
  const opaqueDisplayIdentifier = /^(?:out|col)_[0-9a-f]{24}$/;
  authoredSourceMetadataEvidence.ok = [authoredSourceMetadataEvidence.observation, authoredSourceMetadataEvidence.diagnosticReport].every(source =>
    source.tableTitle === source.expectedTableTitle && source.outputTitle === source.expectedOutputTitle &&
    source.columns.every(column => column.name === column.authoredName && column.label === column.authoredLabel &&
      column.label === column.expectedLabel && Boolean(String(column.authoredLabel ?? '').trim()) &&
      !opaqueDisplayIdentifier.test(String(column.label ?? '').trim()) && source.previewHeaders.includes(column.authoredLabel)),
  );
  check(report, 'correctness', 'published Join source labels match independently authored columns and source previews',
    authoredSourceMetadataEvidence.ok, authoredSourceMetadataEvidence);
  if (!authoredSourceMetadataEvidence.ok) throw new Error('Published Join source presentation differs from its authored source tables: ' + JSON.stringify(authoredSourceMetadataEvidence));
  check(report, 'correctness', 'native Combine inputs pin the exact current Observation and DiagnosticReport revisions',
    publishedRef(observationRevision) === publishedRef(api.revisions.Observation) &&
    publishedRef(reportRevision) === publishedRef(api.revisions.DiagnosticReport),
    { observationRevision, reportRevision, expected: api.revisions });

  await chooseOperationWithPlaywright(page, action, 'KEY_JOIN', [observationRevision, reportRevision]);
  const schemaDetails = page.locator('[data-testid="construction-combine-editor"] details');
  for (const [index, resourceType] of ['Observation', 'DiagnosticReport'].entries()) {
    const details = schemaDetails.nth(index);
    await action(`open pinned ${resourceType} schema`, details.locator('summary'), () => details.locator('summary').click(), {
      after: () => details.locator('ul').waitFor({ state: 'visible', timeout: 5000 }),
    });
  }
  const pinnedSchemaDisplayEvidence = await Promise.all([
    { resourceType: 'Observation', revision: observationRevision },
    { resourceType: 'DiagnosticReport', revision: reportRevision },
  ].map(async ({ resourceType, revision }, index) => {
    const labels = (await schemaDetails.nth(index).locator('li > span.font-medium').allTextContents()).map(label => label.trim());
    const expectedLabels = revision.columns.map(column => String(column.label ?? '').trim());
    return {
      resourceType,
      labels,
      expectedLabels,
      opaqueLabels: labels.filter(label => opaqueDisplayIdentifier.test(label)),
      ok: labels.length === expectedLabels.length && JSON.stringify(labels) === JSON.stringify(expectedLabels) &&
        labels.every(label => Boolean(label) && !opaqueDisplayIdentifier.test(label)),
    };
  }));
  const pinnedSchemaLabelsVisible = pinnedSchemaDisplayEvidence.every(source => source.ok);
  check(report, 'usability', 'pinned Combine schemas show every authored human column label',
    pinnedSchemaLabelsVisible, { sources: pinnedSchemaDisplayEvidence });
  if (!pinnedSchemaLabelsVisible) throw new Error('Pinned Combine schemas did not render every authored human label: ' + JSON.stringify(pinnedSchemaDisplayEvidence));

  const displayedSources = await Promise.all([
    page.locator('select[aria-label="Input table 1"] option:checked').textContent(),
    page.locator('select[aria-label="Input table 2"] option:checked').textContent(),
    page.locator('select[aria-label="Matching pair 1 first field"] option').filter({ hasText: api.columns.observationID.label }).first().textContent(),
    page.locator('select[aria-label="Matching pair 1 second field"] option').filter({ hasText: api.columns.reportID.label }).first().textContent(),
  ]);
  const sourceDisplayEvidence = {
    observation: displayedSources[0] ?? '',
    diagnosticReport: displayedSources[1] ?? '',
    observationIDField: displayedSources[2] ?? '',
    diagnosticReportIDField: displayedSources[3] ?? '',
  };
  sourceDisplayEvidence.ok = sourceDisplayEvidence.observation.includes(observationRevision.tableTitle) &&
    sourceDisplayEvidence.observation.includes(observationRevision.outputTitle) &&
    sourceDisplayEvidence.diagnosticReport.includes(reportRevision.tableTitle) &&
    sourceDisplayEvidence.diagnosticReport.includes(reportRevision.outputTitle) &&
    sourceDisplayEvidence.observationIDField.includes(api.columns.observationID.label) &&
    sourceDisplayEvidence.diagnosticReportIDField.includes(api.columns.reportID.label);
  check(report, 'correctness', 'pinned published source cards show immutable authored output and column labels',
    sourceDisplayEvidence.ok, sourceDisplayEvidence);
  if (!sourceDisplayEvidence.ok) throw new Error('Pinned published source cards did not show their authored labels: ' + JSON.stringify(sourceDisplayEvidence));
  await selectInputWithPlaywright(page, action, 'select[aria-label="Matching pair 1 first field"]', api.columns.observationID.id, 'select Observation ID join key');
  await selectInputWithPlaywright(page, action, 'select[aria-label="Matching pair 1 second field"]', api.columns.reportID.id, 'select DiagnosticReport ID join key');
  const matchingFieldEvidence = await Promise.all([
    { selector: 'select[aria-label="Matching pair 1 first field"]', column: api.columns.observationID, label: 'Observation ID' },
    { selector: 'select[aria-label="Matching pair 1 second field"]', column: api.columns.reportID, label: 'DiagnosticReport ID' },
  ].map(async ({ selector, column, label }) => {
    const control = page.locator(selector);
    const option = control.locator('option:checked');
    const visibleText = (await option.textContent() ?? '').trim();
    const value = await control.inputValue();
    return { label, visibleText, value, expectedValue: column.id,
      ok: visibleText.startsWith(label + ' ·') && !opaqueDisplayIdentifier.test(visibleText.split(' · ')[0] ?? '') && value === column.id };
  }));
  const matchingFieldLabelsVisible = matchingFieldEvidence.every(field => field.ok);
  check(report, 'usability', 'selected Join key options show human labels and preserve exact column IDs',
    matchingFieldLabelsVisible, { fields: matchingFieldEvidence });
  if (!matchingFieldLabelsVisible) throw new Error('Selected Join key options did not show their human labels and exact column IDs: ' + JSON.stringify(matchingFieldEvidence));
  await selectInputWithPlaywright(page, action, 'select[aria-label="If a row in the first table has no match"]', 'INNER', 'select INNER join policy');
  const outputDefaults = [];
  const priorOutputNames = [];
  const addAutomaticOutput = async (index, inputIndex, column) => {
    const expectedName = outputDefaultName(column, priorOutputNames);
    priorOutputNames.push(expectedName);
    const evidence = await addOutputWithPlaywright(page, action, index, '', '', [[inputIndex, column.id, column]], 'KEY_JOIN', {
      automaticDefault: true,
      expectedName,
    });
    outputDefaults.push(evidence);
  };
  const observationIDSource = { ...api.columns.observationID, name: authored.observationID.name, label: authored.observationID.label };
  const observationStatusSource = { ...api.columns.observationStatus, name: authored.observationStatus.name, label: authored.observationStatus.label };
  const reportIDSource = { ...api.columns.reportID, name: authored.reportID.name, label: authored.reportID.label };
  const reportStatusSource = { ...api.columns.reportStatus, name: authored.reportStatus.name, label: authored.reportStatus.label };
  await addAutomaticOutput(1, 0, observationIDSource);
  await addAutomaticOutput(2, 0, observationStatusSource);
  await addAutomaticOutput(3, 1, reportIDSource);
  const reportStatusExpectedName = outputDefaultName(reportStatusSource, priorOutputNames);
  priorOutputNames.push(reportStatusExpectedName);
  await addOutputWithPlaywright(page, action, 4, '', '', [], 'KEY_JOIN', { automaticDefault: true });
  const reportStatus = page.locator('select[aria-label="Output field 4 source field in input 2"]');
  let reportStatusDefault;
  await action('render INNER Join preview', reportStatus, () => reportStatus.selectOption(api.columns.reportStatus.id), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await waitProposalWithPlaywright(page, target.outputId, expectedInnerRows.length);
      reportStatusDefault = await outputDefaultEvidenceWithPlaywright(page, 4, reportStatusSource, reportStatusExpectedName);
      if (!reportStatusDefault.ok) throw new Error('Selected published field did not supply the expected output defaults: ' + JSON.stringify(reportStatusDefault));
    },
  });
  outputDefaults.push(reportStatusDefault);
  const outputDefaultCheck = { outputs: outputDefaults, ok: outputDefaults.length === 4 && outputDefaults.every(evidence => evidence?.ok) };
  check(report, 'correctness', 'Join output names and labels default from selected pinned fields without manual entry',
    outputDefaultCheck.ok, outputDefaultCheck);
  if (!outputDefaultCheck.ok) throw new Error('Join output defaults did not match the selected published fields: ' + JSON.stringify(outputDefaultCheck));

  const outputSourceDisplayEvidence = await Promise.all([
    { index: 1, inputIndex: 1, column: api.columns.observationID, label: 'Observation ID' },
    { index: 2, inputIndex: 1, column: api.columns.observationStatus, label: 'Status' },
    { index: 3, inputIndex: 2, column: api.columns.reportID, label: 'DiagnosticReport ID' },
    { index: 4, inputIndex: 2, column: api.columns.reportStatus, label: 'Status' },
  ].map(async ({ index, inputIndex, column, label }) => {
    const control = page.locator(`select[aria-label="Output field ${index} source field in input ${inputIndex}"]`);
    const option = control.locator('option:checked');
    const visibleText = (await option.textContent() ?? '').trim();
    const value = await control.inputValue();
    return { index, inputIndex, label, visibleText, value, expectedValue: column.id,
      ok: visibleText.startsWith(label + ' ·') && !opaqueDisplayIdentifier.test(visibleText.split(' · ')[0] ?? '') && value === column.id };
  }));
  const outputSourceLabelsVisible = outputSourceDisplayEvidence.every(field => field.ok);
  check(report, 'usability', 'selected Join output-source options show human labels and preserve exact column IDs',
    outputSourceLabelsVisible, { fields: outputSourceDisplayEvidence });
  if (!outputSourceLabelsVisible) throw new Error('Selected Join output-source options did not show their human labels and exact column IDs: ' + JSON.stringify(outputSourceDisplayEvidence));

  await exactRowsWithPlaywright(report, 'INNER preview shows literal human headers and the three exact matched rows',
    page, 'proposal', joinHeaders, expectedInnerRows);

  await applyProposalWithPlaywright(page, action, 'Apply INNER Join to the new target', expectedInnerRows.length);
  const appliedInner = await readBuilder(context, explorer);
  let targetDocument = readTargetDocument(appliedInner, target.outputId);
  let step = targetDocument.construction?.steps?.[0];
  if (!step || targetDocument.construction.steps.length !== 1 || step.operation?.combine?.joinType !== 'INNER' || step.operation?.combine?.kind !== 'KEY_JOIN') {
    throw new Error('Applying INNER did not persist exactly one KEY_JOIN step: ' + JSON.stringify(targetDocument.construction));
  }
  assertPinnedInputs(report, step, api);
  await exactRowsWithPlaywright(report, 'INNER Apply preserves the exact joined rows', page, 'saved', joinHeaders, expectedInnerRows);
  const innerReloadStartedAt = Date.now();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedInnerRows.length);
  await exactRowsWithPlaywright(report, 'INNER table reload retains the three exact rows', page, 'saved', joinHeaders, expectedInnerRows);
  recordReloadTiming(report, 'INNER Apply reload through exact saved rows within five seconds', innerReloadStartedAt, { outputId: target.outputId, rowCount: expectedInnerRows.length });

  await editSavedStepWithPlaywright(page, action, step.id);
  const noMatch = page.locator('select[aria-label="If a row in the first table has no match"]');
  await action('render LEFT Join preview', noMatch, () => noMatch.selectOption('LEFT'), {
    timeout: 5000,
    budget: 5000,
    after: () => waitProposalWithPlaywright(page, target.outputId, expectedLeftRows.length),
  });
  await exactRowsWithPlaywright(report, 'LEFT preview retains the unmatched Observation with empty right-side values',
    page, 'proposal', joinHeaders, expectedLeftRows);

  const cancel = page.getByTestId('construction-cancel-proposal');
  await action('Cancel LEFT Join edit', cancel, () => cancel.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden' });
      await page.getByTestId('construction-combine-editor').waitFor({ state: 'hidden' });
      await page.getByTestId('construction-history').waitFor({ state: 'visible' });
    },
  });
  const cancelReloadStartedAt = Date.now();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedInnerRows.length);
  const cancelledBuilder = await readBuilder(context, explorer);
  targetDocument = readTargetDocument(cancelledBuilder, target.outputId);
  step = targetDocument.construction?.steps?.[0];
  const originalStep = readTargetDocument(appliedInner, target.outputId).construction.steps[0];
  check(report, 'persistence', 'Canceling the LEFT edit leaves the saved INNER operation unchanged',
    Boolean(step?.id === originalStep.id && step?.operation?.combine?.joinType === 'INNER'), { step: step ?? null });
  await exactRowsWithPlaywright(report, 'cancelled LEFT edit keeps the saved INNER rows after reload', page, 'saved', joinHeaders, expectedInnerRows);
  recordReloadTiming(report, 'Cancel reload through saved INNER state and exact rows within five seconds', cancelReloadStartedAt, { outputId: target.outputId, stepId: step?.id, rowCount: expectedInnerRows.length });

  await editSavedStepWithPlaywright(page, action, step.id);
  const leftPolicy = page.locator('select[aria-label="If a row in the first table has no match"]');
  await action('repreview LEFT Join before Apply', leftPolicy, () => leftPolicy.selectOption('LEFT'), {
    timeout: 5000,
    budget: 5000,
    after: () => waitProposalWithPlaywright(page, target.outputId, expectedLeftRows.length),
  });
  await exactRowsWithPlaywright(report, 'LEFT preview before Apply includes null projections for the unmatched row',
    page, 'proposal', joinHeaders, expectedLeftRows);
  await applyProposalWithPlaywright(page, action, 'Apply LEFT Join edit', expectedLeftRows.length);
  const appliedLeft = await readBuilder(context, explorer);
  targetDocument = readTargetDocument(appliedLeft, target.outputId);
  step = targetDocument.construction?.steps?.[0];
  if (!step || targetDocument.construction.steps.length !== 1 || step.id !== originalStep.id || step.operation?.combine?.joinType !== 'LEFT') {
    throw new Error('Applying LEFT did not preserve and update the existing KEY_JOIN step: ' + JSON.stringify(targetDocument.construction));
  }
  assertPinnedInputs(report, step, api);
  await exactRowsWithPlaywright(report, 'LEFT Apply preserves exact matches and unmatched null fields', page, 'saved', joinHeaders, expectedLeftRows);
  const leftReloadStartedAt = Date.now();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedLeftRows.length);
  await exactRowsWithPlaywright(report, 'LEFT rows and nulls survive Builder reload', page, 'saved', joinHeaders, expectedLeftRows);
  recordReloadTiming(report, 'LEFT Apply reload through exact saved rows and nulls within five seconds', leftReloadStartedAt, { outputId: target.outputId, rowCount: expectedLeftRows.length, unmatchedNulls: 1 });

  await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, step.id, 'KEY_JOIN');
  await assertSourceImmutability(context, explorer, docs, api, report);
  report.target.explorer = explorer;
  report.target.combineTarget = target;
};

const assertAppendNullPaddingStep = (report, name, step, api) => {
  const expectedProjections = [
    ['record_id', 0, api.columns.observationID.id],
    ['record_id', 1, api.columns.reportID.id],
    ['record_id', 2, api.columns.patientID.id],
    ['status', 0, api.columns.observationStatus.id],
    ['status', 1, api.columns.reportStatus.id],
    ['patient_gender', 2, api.columns.patientGender.id],
  ];
  const actualProjections = (step.operation?.combine?.projections ?? []).map((projection) => [
    step.outputs?.find((output) => output.id === projection.outputColumnId)?.name ?? null,
    projection.inputIndex,
    projection.inputColumnId,
  ]);
  const projectionOutputOrder = new Map([['record_id', 0], ['status', 1], ['patient_gender', 2]]);
  const projectionOrder = (left, right) =>
    (projectionOutputOrder.get(left[0]) ?? 99) - (projectionOutputOrder.get(right[0]) ?? 99) ||
    left[1] - right[1] || String(left[2]).localeCompare(String(right[2]));
  actualProjections.sort(projectionOrder);
  expectedProjections.sort(projectionOrder);
  const expectedInputs = [api.revisions.Observation, api.revisions.DiagnosticReport, api.revisions.Patient]
    .map((entry) => [entry.tableId, entry.revisionId, entry.outputId]);
  const actualInputs = (step.inputs ?? []).map((input) => [input.tableId, input.revisionId, input.outputId]);
  const outputNullability = ['record_id', 'status', 'patient_gender'].map((name) => {
    const output = step.outputs?.find((candidate) => candidate.name === name);
    return [name, output?.type, Boolean(output?.nullable)];
  });
  const expectedNullability = [
    ['record_id', 'string', true],
    ['status', 'string', true],
    ['patient_gender', 'string', true],
  ];
  const valid = step.operation?.combine?.kind === 'APPEND' &&
    JSON.stringify(actualInputs) === JSON.stringify(expectedInputs) &&
    JSON.stringify(actualProjections) === JSON.stringify(expectedProjections) &&
    JSON.stringify(outputNullability) === JSON.stringify(expectedNullability);
  check(report, 'correctness', name, valid, {
    inputs: actualInputs,
    expectedInputs,
    projections: actualProjections,
    expectedProjections,
    outputNullability,
    expectedNullability,
    combine: step.operation?.combine,
  });
  return { actualInputs, actualProjections, outputNullability };
};

export const appendWorkflow = async ({ page, report, action, nativeRequestLedger }, context) => {
  assert.equal(context.custom, false, 'Combine authoring requires an owned isolated fixture.');
  assert.equal(context.seed?.fresh, true, 'Combine authoring requires a fresh verification project.');
  const fixture = exactFixture(context.target.fixtureDir);
  const rawAppendOracle = appendNullPaddingRows(fixture);
  const literalAppendOracle = [
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-observation-unmatched', 'unknown', null],
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-fixture-patient', null, 'female'],
  ];
  assert.deepEqual(rawAppendOracle, literalAppendOracle, 'fixture rows must match the literal three-input APPEND null-padding oracle');
  const appendedRows = displayAppendNullPaddingRows(rawAppendOracle);
  assert.equal(appendedRows.length, fixture.observations.length + fixture.diagnosticReports.length + fixture.patients.length);
  report.target.fixtureRawOracle = { ...fixture, appendNullPaddingRows: rawAppendOracle };
  check(report, 'correctness', 'fixture contains one bootstrap Patient, four Observations, and three DiagnosticReports',
    fixture.patients.length === 1 && fixture.observations.length === 4 && fixture.diagnosticReports.length === 3, {
      ...fixture, appendNullPaddingRows: rawAppendOracle,
      expectedUnionRows: fixture.patients.length + fixture.observations.length + fixture.diagnosticReports.length,
    });

  const nativeRequestScope = openAppendNativeRequestScope(nativeRequestLedger, context.target);
  let explorer;
  let docs;
  let api;
  let target;
  let capabilitiesFailures;
  let proposalCapture;
  let workflowFailed = false;

  try {
    const prepared = await createAndPublishSourcesWithPlaywright(context, page, action, report, true);
    ({ explorer, docs, api } = prepared);
    report.target.fixtureRawOracle = { ...report.target.fixtureRawOracle, appendNullPaddingRows: rawAppendOracle };
    const initialCancelProbeTarget = await startCombineTargetWithPlaywright(context, page, action, report, explorer, docs.observation.output.id, api.builder);
    target = initialCancelProbeTarget;
    report.target.combineTarget = target;
    report.target.initialCancelProbeTarget = initialCancelProbeTarget;
    capabilitiesFailures = captureConstructionCapabilitiesFailuresWithPlaywright(page, report, {
      uiUrl: context.target.uiUrl, project: context.target.fixtureProject, explorer,
    });
    let builderAtTarget = await readBuilder(context, explorer);
    const builderBeforeInitialProbe = builderAtTarget;
    const initialProbeEmptyTargetBaseline = snapshotSourceDocument(readTargetDocument(builderAtTarget, target.outputId));
    let emptyTargetBaseline = initialProbeEmptyTargetBaseline;
    const catalogEntries = await readPublishedInputs(context, explorer, builderAtTarget);
    const sourceRevisions = [
      currentRevisionFor(catalogEntries, docs.observation),
      currentRevisionFor(catalogEntries, docs.report),
      currentRevisionFor(catalogEntries, docs.patient),
    ];
    check(report, 'correctness', 'native APPEND inputs pin exact current Observation, DiagnosticReport, and Patient revisions',
      sourceRevisions.every((entry, index) => publishedRef(entry) === publishedRef([
        api.revisions.Observation, api.revisions.DiagnosticReport, api.revisions.Patient,
      ][index])), { sourceRevisions, expected: api.revisions });

    const idOnlyRows = appendedRows.map(([id]) => [id]);
    await chooseOperationWithPlaywright(page, action, 'APPEND', sourceRevisions);
    await addOutputWithPlaywright(page, action, 1, 'record_id', 'Record ID', [
      [0, api.columns.observationID.id], [1, api.columns.reportID.id], [2, api.columns.patientID.id],
    ], 'APPEND');
    await waitProposalWithPlaywright(page, target.outputId, idOnlyRows.length);
    const initialProposalGrid = await readGridWithPlaywright(page, 'proposal');
    const initialProposalEvidence = exactGridMatches(initialProposalGrid, ['Record ID'], idOnlyRows);
    check(report, 'correctness', 'initial APPEND proposal previews the literal eight-row ID union before Cancel',
      initialProposalEvidence.ok, initialProposalEvidence);
    const cancelInitialProposal = page.getByTestId('construction-cancel-proposal');
    await action('Cancel the initial APPEND proposal before configuring the saved schema', cancelInitialProposal,
      () => cancelInitialProposal.click(), {
        after: async () => {
          await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 5000 });
          await page.getByTestId('construction-combine-editor').waitFor({ state: 'hidden', timeout: 5000 });
          await page.waitForFunction(rootedEmptyTargetAppliedExpression(target.outputId), undefined, { timeout: 5000 });
        },
      });
    const emptyBeforeCancelReloadBuilder = await readBuilder(context, explorer);
    const emptyBeforeCancelReload = readTargetDocument(emptyBeforeCancelReloadBuilder, target.outputId);
    const emptyBeforeCancelReloadEvidence = rootedEmptyTargetRestorationEvidence(emptyBeforeCancelReload,
      initialProbeEmptyTargetBaseline, target);
    const initialCancelRootVisible = await page.evaluate(rootedEmptyTargetAppliedExpression(target.outputId));
    await appendReloadToVisibleResultWithPlaywright({
      page, action, report,
      name: 'Initial APPEND proposal Cancel reload reaches the exact rooted empty target within five seconds',
      outputId: target.outputId,
      rootedEmpty: true,
    });
    const emptyAfterCancelReloadBuilder = await readBuilder(context, explorer);
    const emptyAfterCancelReload = readTargetDocument(emptyAfterCancelReloadBuilder, target.outputId);
    const emptyAfterCancelReloadEvidence = rootedEmptyTargetRestorationEvidence(emptyAfterCancelReload,
      initialProbeEmptyTargetBaseline, target);
    const restartedTarget = await startCombineTargetWithPlaywright(context, page, action, report, explorer,
      docs.observation.output.id, emptyAfterCancelReloadBuilder, { recordTargetCheck: false });
    target = restartedTarget;
    report.target.combineTarget = target;
    builderAtTarget = await readBuilder(context, explorer);
    const reconfiguredTargetBaseline = readTargetDocument(builderAtTarget, target.outputId);
    emptyTargetBaseline = snapshotSourceDocument(reconfiguredTargetBaseline);
    const probeAfterRestart = readTargetDocument(builderAtTarget, initialCancelProbeTarget.outputId);
    const probeAfterRestartEvidence = rootedEmptyTargetRestorationEvidence(
      probeAfterRestart, initialProbeEmptyTargetBaseline, initialCancelProbeTarget);
    proposalCapture = captureOwnedConstructionProposals(page, {
      ...context.target, explorer, outputId: target.outputId,
    });
    const initialCancelEvidence = {
      ok: initialCancelRootVisible && emptyBeforeCancelReloadEvidence.ok && emptyAfterCancelReloadEvidence.ok &&
        builderBeforeInitialProbe.draftVersion === emptyAfterCancelReloadBuilder.draftVersion &&
        builderBeforeInitialProbe.draftDigest === emptyAfterCancelReloadBuilder.draftDigest &&
        restartedTarget.creationEvidence.ok &&
        restartedTarget.outputId !== initialCancelProbeTarget.outputId && probeAfterRestartEvidence.ok &&
        reconfiguredTargetBaseline.rootResourceType === 'Observation' &&
        reconfiguredTargetBaseline.columns?.length === 0 && (reconfiguredTargetBaseline.construction?.steps?.length ?? 0) === 0,
      rootedEmptyBeforeReload: emptyBeforeCancelReloadEvidence,
      rootedEmptyAfterReload: emptyAfterCancelReloadEvidence,
      initialProbeTargetRemainsEmptyAfterStartingFreshTarget: probeAfterRestartEvidence,
      reconfiguredTargetCreation: restartedTarget.creationEvidence,
      reconfiguredTargetBaseline: {
        outputId: target.outputId,
        rootResourceType: reconfiguredTargetBaseline.rootResourceType,
        columns: reconfiguredTargetBaseline.columns,
        construction: reconfiguredTargetBaseline.construction,
      },
      draftUnchangedThroughInitialCancelReload: builderBeforeInitialProbe.draftVersion === emptyAfterCancelReloadBuilder.draftVersion &&
        builderBeforeInitialProbe.draftDigest === emptyAfterCancelReloadBuilder.draftDigest,
      initialCancelRootVisible,
      beforeDraftVersion: builderBeforeInitialProbe.draftVersion,
      afterDraftVersion: emptyAfterCancelReloadBuilder.draftVersion,
      beforeDraftDigest: builderBeforeInitialProbe.draftDigest,
      afterDraftDigest: emptyAfterCancelReloadBuilder.draftDigest,
    };
    check(report, 'persistence', 'Canceling initial APPEND proposal preserves the exact fresh rooted target before creating a distinct target for reconfiguration',
      initialCancelEvidence.ok, initialCancelEvidence);

    await chooseOperationWithPlaywright(page, action, 'APPEND', sourceRevisions);
    await addOutputWithPlaywright(page, action, 1, 'record_id', 'Record ID', [], 'APPEND');
    await addOutputWithPlaywright(page, action, 2, 'status', 'Status', [], 'APPEND');
    await addOutputWithPlaywright(page, action, 3, 'numeric_value', 'Numeric value', [], 'APPEND');
    await selectInputWithPlaywright(page, action, 'select[aria-label="Output field 3 matching field in input 1"]',
      'column:' + api.columns.observationInteger.id, 'select numeric Observation value');
    const incompatibleOptions = await page.locator('select[aria-label="Output field 3 matching field in input 2"]').evaluate(select =>
      [...select.options].map(option => ({ value: option.value, text: option.innerText.trim() })));
    check(report, 'correctness', 'APPEND field choices reject a source with a different scalar type',
      !incompatibleOptions.some(option => option.value === 'column:' + api.columns.reportStatus.id),
      { observationNumeric: api.columns.observationInteger, diagnosticReportChoices: incompatibleOptions });
    const removeNumeric = page.getByRole('button', { name: 'Remove output field 3', exact: true });
    await action('remove incompatible numeric output field', removeNumeric, () => removeNumeric.click(), {
      after: () => page.locator('select[aria-label="Output field 2 matching field in input 3"]').waitFor({ state: 'visible' }),
    });

    await configureOutputWithPlaywright(page, action, 1, 'record_id', 'Record ID', [
      [0, api.columns.observationID.id], [1, api.columns.reportID.id], [2, api.columns.patientID.id],
    ], 'APPEND');
    await configureOutputWithPlaywright(page, action, 2, 'status', 'Status', [
      [0, api.columns.observationStatus.id], [1, api.columns.reportStatus.id], [2, null],
    ], 'APPEND');
    await addOutputWithPlaywright(page, action, 3, 'patient_gender', 'Patient gender', [[0, null], [1, null]], 'APPEND');
    const emptySelections = await page.evaluate(() => {
      const value = selector => document.querySelector(selector)?.value ?? null;
      const selects = [...document.querySelectorAll('select[aria-label^="Output field"]')];
      return {
        statusPatient: value('select[aria-label="Output field 2 matching field in input 3"]'),
        genderObservation: value('select[aria-label="Output field 3 matching field in input 1"]'),
        genderReport: value('select[aria-label="Output field 3 matching field in input 2"]'),
        genderPatientUnconfigured: value('select[aria-label="Output field 3 matching field in input 3"]') === '',
        incompletePreviewCleared: !document.querySelector('[data-testid="construction-proposal-panel"]'),
        explicitOptionVisible: selects.every(select => [...select.options].some(option => option.value === 'empty-for-this-table')),
      };
    });
    check(report, 'usability', 'APPEND editor requires explicit Empty for this table choices while blank mappings stay unconfigured',
      emptySelections.statusPatient === 'empty-for-this-table' && emptySelections.genderObservation === 'empty-for-this-table' &&
      emptySelections.genderReport === 'empty-for-this-table' && emptySelections.genderPatientUnconfigured &&
      emptySelections.incompletePreviewCleared && emptySelections.explicitOptionVisible, emptySelections);
    const actualOutputRows = await page.evaluate(() => {
      const editor = document.querySelector('[data-testid="construction-combine-editor"]');
      if (!editor) return [];
      const nameFields = [...editor.querySelectorAll('input[aria-label^="Output field "][aria-label$=" name"]')];
      return nameFields.map((nameField, rowIndex) => ({
        name: nameField.value,
        label: editor.querySelector(`input[aria-label="Output field ${rowIndex + 1} label"]`)?.value ?? null,
        mappings: Array.from({ length: 3 }, (_, inputIndex) =>
          editor.querySelector(`select[aria-label="Output field ${rowIndex + 1} matching field in input ${inputIndex + 1}"]`)?.value ?? null),
      }));
    });
    const expectedOutputRows = [
      { name: 'record_id', label: 'Record ID', mappings: ['column:' + api.columns.observationID.id, 'column:' + api.columns.reportID.id, 'column:' + api.columns.patientID.id] },
      { name: 'status', label: 'Status', mappings: ['column:' + api.columns.observationStatus.id, 'column:' + api.columns.reportStatus.id, 'empty-for-this-table'] },
      { name: 'patient_gender', label: 'Patient gender', mappings: ['empty-for-this-table', 'empty-for-this-table', ''] },
    ];
    const outputConfiguration = appendEditorConfigurationEvidence(actualOutputRows, expectedOutputRows);
    check(report, 'correctness', 'APPEND editor has exactly three named output rows with the intended mappings before preview',
      outputConfiguration.ok, outputConfiguration);

    const genderMapping = page.locator('select[aria-label="Output field 3 matching field in input 3"]');
    await action('automatically preview three-input APPEND with explicit absent-field mappings', genderMapping,
      () => genderMapping.selectOption('column:' + api.columns.patientGender.id), {
        timeout: 5000, budget: 5000,
        after: () => waitProposalWithPlaywright(page, target.outputId, appendedRows.length),
      });
    const candidateRequest = [...proposalCapture.entries].reverse().find(entry =>
      entry.body?.candidateConstruction?.steps?.at(-1)?.operation?.combine?.kind === 'APPEND');
    assert(candidateRequest, 'completing all APPEND slots must issue an automatic construction proposal');
    const candidateResponse = await candidateRequest.responsePromise;
    assert(candidateResponse && !candidateResponse.responseReadError, 'APPEND proposal response body must be retained');
    const candidateStep = candidateRequest.body.candidateConstruction.steps.at(-1);
    const candidateShape = assertAppendNullPaddingStep(report,
      'automatic APPEND proposal omits only explicit-empty source pairs and marks padded outputs nullable', candidateStep, api);
    check(report, 'correctness', 'automatic APPEND proposal is bound to the exact target output and current Builder snapshot',
      candidateRequest.body.outputId === target.outputId &&
      candidateRequest.body.snapshotToken === builderAtTarget.catalog.snapshotToken &&
      candidateRequest.body.expectedDraftVersion === builderAtTarget.draftVersion &&
      candidateRequest.body.expectedDraftDigest === builderAtTarget.draftDigest,
      { requestSequence: candidateRequest.sequence, requestURL: candidateRequest.url, outputId: candidateRequest.body.outputId,
        targetOutputId: target.outputId, stageId: candidateRequest.body.stageId,
        snapshotTokenMatched: candidateRequest.body.snapshotToken === builderAtTarget.catalog.snapshotToken,
        expectedDraftVersion: builderAtTarget.draftVersion,
        expectedDraftDigestMatched: candidateRequest.body.expectedDraftDigest === builderAtTarget.draftDigest });
    const domIdentity = await page.evaluate(() => ({
      proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
      receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
    }));
    const responseEvidence = constructionProposalPreviewEvidence({
      responseStatus: candidateRequest.status,
      response: candidateResponse,
      requestBody: candidateRequest.body,
      expectedOutputId: target.outputId,
      expectedColumns: ['record_id', 'status', 'patient_gender'],
      expectedRows: rawAppendOracle,
      domProposalId: domIdentity.proposalId,
      domReceiptId: domIdentity.receiptId,
    });
    check(report, 'correctness', 'APPEND proposal response has literal JSON nulls at exactly omitted source positions',
      responseEvidence.ok, { ...responseEvidence, inputs: candidateShape.actualInputs,
        requestSequence: candidateRequest.sequence, stageId: candidateRequest.body.stageId });
    await exactRowsWithPlaywright(report, 'APPEND preview matches the literal null-padding oracle and contains the complete eight-row input union',
      page, 'proposal', appendHeaders, appendedRows);

    await applyProposalWithPlaywright(page, action, 'Apply absent-field APPEND to the rooted target', appendedRows.length);
    const applied = await readBuilder(context, explorer);
    let targetDocument = readTargetDocument(applied, target.outputId);
    let step = targetDocument.construction?.steps?.[0];
    if (!step || targetDocument.construction.steps.length !== 1 || step.operation?.combine?.kind !== 'APPEND') {
      throw new Error('Applying absent-field APPEND did not persist exactly one APPEND step: ' + JSON.stringify(targetDocument.construction));
    }
    assertPinnedInputs(report, step, api);
    assertAppendNullPaddingStep(report, 'Apply persists the exact sparse three-input APPEND mapping and nullable outputs', step, api);
    await exactRowsWithPlaywright(report, 'APPEND Apply preserves the exact null-padded row union', page, 'saved', appendHeaders, appendedRows);
    const appliedDocument = snapshotSourceDocument(targetDocument);
    await appendReloadToVisibleResultWithPlaywright({
      page, action, report,
      name: 'APPEND Apply reload reaches the exact saved rows and headers within five seconds',
      outputId: target.outputId, headers: appendHeaders, rows: appendedRows,
    });
    const appliedReloadBuilder = await readBuilder(context, explorer);
    const appliedReloadDocument = readTargetDocument(appliedReloadBuilder, target.outputId);
    const appliedReloadGrid = await readGridWithPlaywright(page);
    const appliedReloadGridEvidence = exactGridMatches(appliedReloadGrid, appendHeaders, appendedRows);
    const appliedReloadDocumentUnchanged = sameSourceDocuments(appliedDocument, appliedReloadDocument);
    check(report, 'persistence', 'APPEND null padding and complete input union survive Builder reload',
      appliedReloadGridEvidence.ok && appliedReloadDocumentUnchanged, {
        ...appliedReloadGridEvidence,
        persistedDocumentUnchanged: appliedReloadDocumentUnchanged,
        persistedStep: appliedReloadDocument.construction?.steps?.[0] ?? null,
      });
    targetDocument = appliedReloadDocument;
    step = targetDocument.construction?.steps?.[0];

    const savedStepId = step.id;
    const savedLabel = step.outputs[2]?.label;
    await editSavedStepWithPlaywright(page, action, savedStepId);
    const reconstructedEmptyChoices = await page.evaluate(() => ({
      statusPatient: document.querySelector('select[aria-label="Output field 2 matching field in input 3"]')?.value,
      genderObservation: document.querySelector('select[aria-label="Output field 3 matching field in input 1"]')?.value,
      genderReport: document.querySelector('select[aria-label="Output field 3 matching field in input 2"]')?.value,
    }));
    check(report, 'persistence', 'editing a saved APPEND reconstructs each omitted mapping as explicit Empty for this table',
      reconstructedEmptyChoices.statusPatient === 'empty-for-this-table' &&
      reconstructedEmptyChoices.genderObservation === 'empty-for-this-table' &&
      reconstructedEmptyChoices.genderReport === 'empty-for-this-table', reconstructedEmptyChoices);
    const patientSexLabel = page.locator('input[aria-label="Output field 3 label"]');
    await action('preview APPEND edit after saved empty choices are reconstructed', patientSexLabel,
      () => patientSexLabel.fill('Patient sex'), { editable: true, timeout: 5000, budget: 5000,
        after: async () => {
          await waitProposalWithPlaywright(page, target.outputId, appendedRows.length);
          await page.getByTestId('construction-proposal-preview').getByText('Patient sex', { exact: true }).waitFor({ state: 'visible' });
        } });
    await exactRowsWithPlaywright(report, 'APPEND edit preview keeps exact padded rows', page, 'proposal', ['Record ID', 'Status', 'Patient sex'], appendedRows);
    const cancelEdit = page.getByTestId('construction-cancel-proposal');
    await action('Cancel APPEND label edit', cancelEdit, () => cancelEdit.click(), {
      after: async () => {
        await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden' });
        await page.getByTestId('construction-combine-editor').waitFor({ state: 'hidden' });
        await page.getByTestId('construction-history').waitFor({ state: 'visible' });
      },
    });
    await appendReloadToVisibleResultWithPlaywright({
      page, action, report,
      name: 'APPEND saved-edit Cancel reload reaches the exact saved rows and headers within five seconds',
      outputId: target.outputId, headers: appendHeaders, rows: appendedRows,
    });
    const cancelled = await readBuilder(context, explorer);
    targetDocument = readTargetDocument(cancelled, target.outputId);
    step = targetDocument.construction?.steps?.[0];
    check(report, 'persistence', 'Cancel leaves the original absent-field APPEND schema and label unchanged',
      Boolean(step?.id === savedStepId && step.outputs?.[2]?.label === savedLabel),
      { stepId: step?.id, outputLabels: step?.outputs?.map(output => output.label), expectedStepId: savedStepId, expectedThirdLabel: savedLabel });
    assertAppendNullPaddingStep(report, 'Cancel preserves the saved sparse APPEND mapping', step, api);
    await exactRowsWithPlaywright(report, 'cancelled APPEND edit retains null padding and all source rows after reload', page, 'saved', appendHeaders, appendedRows);

    await editSavedStepWithPlaywright(page, action, savedStepId);
    const editedLabel = page.locator('input[aria-label="Output field 3 label"]');
    await action('repreview edited APPEND output label with absent-field mappings intact', editedLabel,
      () => editedLabel.fill('Patient sex'), { editable: true, timeout: 5000, budget: 5000,
        after: () => waitProposalWithPlaywright(page, target.outputId, appendedRows.length) });
    await exactRowsWithPlaywright(report, 'edited APPEND output label preview retains the exact null-padded union',
      page, 'proposal', ['Record ID', 'Status', 'Patient sex'], appendedRows);
    await applyProposalWithPlaywright(page, action, 'Apply APPEND label edit with absent-field mappings', appendedRows.length);
    const edited = await readBuilder(context, explorer);
    targetDocument = readTargetDocument(edited, target.outputId);
    step = targetDocument.construction?.steps?.[0];
    check(report, 'persistence', 'edited APPEND label and exact sparse mappings survive Apply',
      Boolean(step?.id === savedStepId && step.outputs?.[2]?.label === 'Patient sex'),
      { stepId: step?.id, outputLabels: step?.outputs?.map(output => output.label) });
    assertAppendNullPaddingStep(report, 'edited APPEND preserves omitted mappings and nullable outputs', step, api);
    const editedDocument = snapshotSourceDocument(targetDocument);
    const editedHeaders = ['Record ID', 'Status', 'Patient sex'];
    await appendReloadToVisibleResultWithPlaywright({
      page, action, report,
      name: 'APPEND edit Apply reload reaches the exact saved rows and headers within five seconds',
      outputId: target.outputId, headers: editedHeaders, rows: appendedRows,
    });
    const editedReloadBuilder = await readBuilder(context, explorer);
    const editedReloadDocument = readTargetDocument(editedReloadBuilder, target.outputId);
    const editedReloadGrid = await readGridWithPlaywright(page);
    const editedReloadGridEvidence = exactGridMatches(editedReloadGrid, editedHeaders, appendedRows);
    const editedReloadDocumentUnchanged = sameSourceDocuments(editedDocument, editedReloadDocument);
    check(report, 'persistence', 'edited APPEND null padding survives reload',
      editedReloadGridEvidence.ok && editedReloadDocumentUnchanged, {
        ...editedReloadGridEvidence,
        persistedDocumentUnchanged: editedReloadDocumentUnchanged,
        persistedStep: editedReloadDocument.construction?.steps?.[0] ?? null,
      });
    targetDocument = editedReloadDocument;
    step = targetDocument.construction?.steps?.[0];

    const removalBaselineDocument = snapshotSourceDocument(targetDocument);
    const removalBaselineBuilder = editedReloadBuilder;
    const removalHistory = page.getByTestId(`construction-history-step-${savedStepId}`);
    await action('select APPEND step before previewing a cancellable removal', removalHistory,
      () => removalHistory.click(), {
        after: () => page.getByTestId(`construction-remove-step-${savedStepId}`).waitFor({ state: 'visible' }),
      });
    const removeBeforeCancel = page.getByTestId(`construction-remove-step-${savedStepId}`);
    await action('preview APPEND removal before Cancel', removeBeforeCancel, () => removeBeforeCancel.click(), {
      timeout: 5000,
      after: async () => {
        await waitProposalWithPlaywright(page, target.outputId);
        await page.getByTestId(`construction-removal-step-${savedStepId}`).waitFor({ state: 'visible', timeout: 5000 });
      },
    });
    const cancelRemovalProposal = page.getByTestId('construction-cancel-proposal');
    await action('Cancel the APPEND removal proposal before actual removal', cancelRemovalProposal,
      () => cancelRemovalProposal.click(), {
        after: async () => {
          await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 5000 });
          await page.getByTestId('construction-history').waitFor({ state: 'visible', timeout: 5000 });
          await page.waitForFunction(
            savedAppendPreviewAppliedExpression(target.outputId, editedHeaders, appendedRows), undefined, { timeout: 5000 });
        },
      });
    const removalCancelBuilder = await readBuilder(context, explorer);
    const removalCancelDocument = readTargetDocument(removalCancelBuilder, target.outputId);
    const removalCancelGrid = await readGridWithPlaywright(page);
    const removalCancelGridEvidence = exactGridMatches(removalCancelGrid, editedHeaders, appendedRows);
    const removalCancelEvidence = combineCancellationPreservationEvidence({
      beforeDocument: removalBaselineDocument,
      afterDocument: removalCancelDocument,
      beforeDraftVersion: removalBaselineBuilder.draftVersion,
      afterDraftVersion: removalCancelBuilder.draftVersion,
      beforeDraftDigest: removalBaselineBuilder.draftDigest,
      afterDraftDigest: removalCancelBuilder.draftDigest,
      visibleResultMatches: removalCancelGridEvidence.ok,
    });
    check(report, 'persistence', 'Canceling APPEND removal preserves the exact saved schema, step, and rows before removal',
      removalCancelEvidence.ok, {
        ...removalCancelEvidence,
        ...removalCancelGridEvidence,
        savedStepId: removalCancelDocument.construction?.steps?.[0]?.id ?? null,
        savedOperation: removalCancelDocument.construction?.steps?.[0]?.operation?.combine?.kind ?? null,
        savedOutputLabels: removalCancelDocument.construction?.steps?.[0]?.outputs?.map(output => output.label) ?? [],
      });

    await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, savedStepId, 'APPEND');
    await assertSourceImmutability(context, explorer, docs, api, report);
    report.target.explorer = explorer;
    report.target.combineTarget = target;
  } catch (error) {
    workflowFailed = true;
    throw error;
  } finally {
    proposalCapture?.stop();
    report.nativeAppendProposals = (proposalCapture?.entries ?? []).map(entry => ({
      requestSequence: entry.sequence,
      url: entry.url,
      requestBody: entry.body,
      status: entry.status,
      response: entry.response ?? null,
      stageId: entry.body?.stageId ?? null,
      outputId: entry.body?.outputId ?? null,
      snapshotToken: entry.body?.snapshotToken ?? null,
      expectedDraftVersion: entry.body?.expectedDraftVersion ?? null,
      expectedDraftDigest: entry.body?.expectedDraftDigest ?? null,
    }));
    capabilitiesFailures?.stop();
    try {
      await flushAppendNativeRequestScope({ nativeRequestLedger, scope: nativeRequestScope, explorer, report });
    } catch (error) {
      report.nativeRequestFlushFailure = {
        message: error instanceof Error ? error.message : String(error),
        terminalLedger: error?.nativeRequestSnapshot?.nativeRequestTerminalLedger ?? report.nativeRequestTerminalLedger ?? null,
      };
      if (!workflowFailed) throw error;
    }
  }
};
