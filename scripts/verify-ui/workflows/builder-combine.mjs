import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { sanitizeBody } from '../helpers/playwright-browser.mjs';
import { recordCheck } from '../helpers/report.mjs';
import { appendNullPaddingRows, builderRequestURL, builderResponseIdentity, constructionProposalPreviewEvidence, currentPublishedRevisionForOutput, displayAppendNullPaddingRows, findColumn, isCombineInputIDColumn, isNumericClickHouseType, isScalarStringColumn, joinOracleRows, appendEditorConfigurationEvidence, nativeCombineTargetBindingEvidence, sameSourceDocuments, snapshotSourceDocument, isOwnedConstructionCapabilitiesRequest, rootedEmptyTargetRestorationEvidence } from '../helpers/builder-combine-helpers.mjs';

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
const joinHeaders = ['Observation ID', 'Observation status', 'Report ID', 'Report status'];
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
    const close = page.getByRole('button', { name: 'Close operation editor', exact: true });
    await action('close source operation editor', close, () => close.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'hidden' }),
    });
  };

  await addRootWithUI('Observation', 'Observations', expectedObservations.map(row => row.id));
  await addRawFieldsWithUI('Observation', rawFieldsByResource.Observation ?? ['status', 'valueInteger'], expectedObservations.length);
  const newTable = page.getByTestId('construction-new-table');
  await action('start DiagnosticReport source table', newTable, () => newTable.click(), {
    after: () => page.locator('#first-table-name').waitFor({ state: 'visible' }),
  });
  await addRootWithUI('DiagnosticReport', 'Diagnostic reports', expectedReports.map(row => row.id));
  await addRawFieldsWithUI('DiagnosticReport', rawFieldsByResource.DiagnosticReport ?? ['status'], expectedReports.length);
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
  return { explorer, docs, api };
};

const startCombineTargetWithPlaywright = async (context, page, action, report, explorer, observationOutputId, sourceBuilder) => {
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
  check(report, 'correctness', 'native Combine creates a rooted empty Observation target without adding an authored step or output column', evidence.ok, evidence);
  if (!evidence.ok) throw new Error('Native Combine target identity did not bind its creation command, returned workspace, and mounted editor: ' + JSON.stringify(evidence));
  return { outputId: evidence.outputId, rootNodeId: evidence.rootNodeId };
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

const configureOutputWithPlaywright = async (page, action, index, name, label, sourceFields, kind) => {
  const nameSelector = `input[aria-label="Output field ${index} name"]`;
  await page.locator(nameSelector).waitFor({ state: 'visible', timeout: 5000 });
  await fillWithPlaywright(page, action, nameSelector, name, `name output field ${index}`);
  await fillWithPlaywright(page, action, `input[aria-label="Output field ${index} label"]`, label, `label output field ${index}`);
  for (const [inputIndex, columnId] of sourceFields) {
    const fieldKind = kind === 'APPEND' ? 'matching field in input ' : 'source field in input ';
    const value = kind === 'APPEND' ? (columnId === null ? 'empty-for-this-table' : 'column:' + columnId) : columnId;
    await selectInputWithPlaywright(page, action,
      `select[aria-label="Output field ${index} ${fieldKind}${inputIndex + 1}"]`, value,
      `map output field ${index} from input ${inputIndex + 1}`);
  }
};

const addOutputWithPlaywright = async (page, action, index, name, label, sourceFields, kind) => {
  const add = page.getByRole('button', { name: 'Add output field', exact: true });
  await action(`add output field ${index}`, add, () => add.click(), {
    after: () => page.locator(`input[aria-label="Output field ${index} name"]`).waitFor({ state: 'visible' }),
  });
  await configureOutputWithPlaywright(page, action, index, name, label, sourceFields, kind);
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
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  const builder = await readBuilder(context, explorer);
  const restored = readTargetDocument(builder, target.outputId);
  const restoration = rootedEmptyTargetRestorationEvidence(restored, baseline, target);
  check(report, 'persistence', 'removing ' + operation + ' and reloading restores the rooted empty target', restoration.ok, {
    target, rootResourceType: restored.rootResourceType, columns: restored.columns, construction: restored.construction,
    sameAsPreCombineDocument: restoration.unchanged, expectedDocument: baseline,
  });
};

export const joinWorkflow = async ({ page, report, action }, context) => {
  assert.equal(context.custom, false, 'Combine authoring requires an owned isolated fixture.');
  assert.equal(context.seed?.fresh, true, 'Combine authoring requires a fresh verification project.');
  const fixture = exactFixture(context.target.fixtureDir);
  report.target.fixtureRawOracle = fixture;
  check(report, 'correctness', 'fixture contains one bootstrap Patient, four Observations, and three DiagnosticReports',
    fixture.patients.length === 1 && fixture.observations.length === 4 && fixture.diagnosticReports.length === 3, fixture);

  const prepared = await createAndPublishSourcesWithPlaywright(context, page, action, report);
  const { explorer, docs, api } = prepared;
  const target = await startCombineTargetWithPlaywright(context, page, action, report, explorer, docs.observation.output.id, api.builder);
  report.target.combineTarget = target;
  const builderAtTarget = await readBuilder(context, explorer);
  const emptyTargetBaseline = readTargetDocument(builderAtTarget, target.outputId);
  const catalogEntries = await readPublishedInputs(context, explorer, builderAtTarget);
  const observationRevision = currentRevisionFor(catalogEntries, docs.observation);
  const reportRevision = currentRevisionFor(catalogEntries, docs.report);
  check(report, 'correctness', 'native Combine inputs pin the exact current Observation and DiagnosticReport revisions',
    publishedRef(observationRevision) === publishedRef(api.revisions.Observation) &&
    publishedRef(reportRevision) === publishedRef(api.revisions.DiagnosticReport),
    { observationRevision, reportRevision, expected: api.revisions });

  await chooseOperationWithPlaywright(page, action, 'KEY_JOIN', [observationRevision, reportRevision]);
  await selectInputWithPlaywright(page, action, 'select[aria-label="Matching pair 1 first field"]', api.columns.observationID.id, 'select Observation ID join key');
  await selectInputWithPlaywright(page, action, 'select[aria-label="Matching pair 1 second field"]', api.columns.reportID.id, 'select DiagnosticReport ID join key');
  await selectInputWithPlaywright(page, action, 'select[aria-label="If a row in the first table has no match"]', 'INNER', 'select INNER join policy');
  await addOutputWithPlaywright(page, action, 1, 'observation_id', 'Observation ID', [[0, api.columns.observationID.id]], 'KEY_JOIN');
  await addOutputWithPlaywright(page, action, 2, 'observation_status', 'Observation status', [[0, api.columns.observationStatus.id]], 'KEY_JOIN');
  await addOutputWithPlaywright(page, action, 3, 'report_id', 'Report ID', [[1, api.columns.reportID.id]], 'KEY_JOIN');
  await addOutputWithPlaywright(page, action, 4, 'report_status', 'Report status', [], 'KEY_JOIN');
  const reportStatus = page.locator('select[aria-label="Output field 4 source field in input 2"]');
  await action('render INNER Join preview', reportStatus, () => reportStatus.selectOption(api.columns.reportStatus.id), {
    timeout: 5000,
    budget: 5000,
    after: () => waitProposalWithPlaywright(page, target.outputId, expectedInnerRows.length),
  });
  await exactRowsWithPlaywright(report, 'INNER preview returns the three exact rows matched on shared required IDs',
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
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedInnerRows.length);
  await exactRowsWithPlaywright(report, 'INNER table reload retains the three exact rows', page, 'saved', joinHeaders, expectedInnerRows);

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
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedLeftRows.length);
  await exactRowsWithPlaywright(report, 'LEFT rows and nulls survive Builder reload', page, 'saved', joinHeaders, expectedLeftRows);

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

export const appendWorkflow = async ({ page, report, action }, context) => {
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

  const prepared = await createAndPublishSourcesWithPlaywright(context, page, action, report, true);
  const { explorer, docs, api } = prepared;
  report.target.fixtureRawOracle = { ...report.target.fixtureRawOracle, appendNullPaddingRows: rawAppendOracle };
  const target = await startCombineTargetWithPlaywright(context, page, action, report, explorer, docs.observation.output.id, api.builder);
  report.target.combineTarget = target;
  const capabilitiesFailures = captureConstructionCapabilitiesFailuresWithPlaywright(page, report, {
    uiUrl: context.target.uiUrl, project: context.target.fixtureProject, explorer,
  });
  const proposalCapture = captureOwnedConstructionProposals(page, {
    ...context.target, explorer, outputId: target.outputId,
  });

  try {
    const builderAtTarget = await readBuilder(context, explorer);
    const emptyTargetBaseline = readTargetDocument(builderAtTarget, target.outputId);
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
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
    await selectTargetWithPlaywright(page, action, target.outputId);
    await waitSavedPreviewWithPlaywright(page, appendedRows.length);
    await exactRowsWithPlaywright(report, 'APPEND null padding and complete input union survive Builder reload', page, 'saved', appendHeaders, appendedRows);

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
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
    await selectTargetWithPlaywright(page, action, target.outputId);
    await waitSavedPreviewWithPlaywright(page, appendedRows.length);
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
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 5000 });
    await selectTargetWithPlaywright(page, action, target.outputId);
    await waitSavedPreviewWithPlaywright(page, appendedRows.length);
    await exactRowsWithPlaywright(report, 'edited APPEND null padding survives reload', page, 'saved', ['Record ID', 'Status', 'Patient sex'], appendedRows);

    await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, savedStepId, 'APPEND');
    await assertSourceImmutability(context, explorer, docs, api, report);
    report.target.explorer = explorer;
    report.target.combineTarget = target;
  } finally {
    proposalCapture.stop();
    report.nativeAppendProposals = proposalCapture.entries.map(entry => ({
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
    capabilitiesFailures.stop();
  }
};
