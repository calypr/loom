import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { executeScenario, runBrowserCase, runPlaywrightCase, browserURL } from './common.mjs';
import { click, evaluate, fill, inspectAction, onCDP, recordBrowserTiming, reload, waitFor, waitForCDPEvent } from './browser.mjs';
import { isActionable, recordCheck } from './report.mjs';
import { createBlankExplorer } from './workflows.mjs';
import { appendNullPaddingRows, builderRequestURL, builderResponseIdentity, constructionProposalPreviewEvidence, currentPublishedRevisionForOutput, displayAppendNullPaddingRows, findColumn, isCombineInputIDColumn, isJoinableStringColumn, isNumericClickHouseType, isScalarStringColumn, joinOracleRows, appendEditorConfigurationEvidence, nativeCombineTargetBindingEvidence, sameSourceDocuments, snapshotSourceDocument, isOwnedConstructionCapabilitiesRequest, rootedEmptyTargetAppliedExpression, rootedEmptyTargetRestorationEvidence } from './builder-combine-helpers.mjs';
import { proposalPreviewReadinessExpression } from './proposal-preview-readiness.mjs';

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
const workspaceReady = "document.body.innerText.includes('DATASET WORKSPACE') && Boolean(document.querySelector('[data-testid=\"construction-workspace\"]'))";
const savedPreview = (count) =>
  "(()=>{const preview=document.querySelector('[data-testid=\"construction-preview\"]');const table=document.querySelector('[data-testid=\"preview-table-scroll\"] [role=\"table\"]');return Boolean(preview?.getAttribute('data-preview-status')==='ready'&&table?.getAttribute('aria-rowcount')===" +
  JSON.stringify(String(count + 1)) +
  "&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()";
const proposalPreview = (count, outputId) => proposalPreviewReadinessExpression(outputId, count);
const proposalReady = (outputId) => proposalPreviewReadinessExpression(outputId);

const captureConstructionCapabilitiesFailures = (cdp, report, owner) => {
  const requests = new Map();
  const responseReads = [];
  const stopRequest = onCDP(cdp, 'Network.requestWillBeSent', (event) => {
    if (!isOwnedConstructionCapabilitiesRequest({
      requestURL: event.request.url,
      method: event.request.method,
      ...owner,
    })) return;
    let parsed;
    try { parsed = event.request.postData ? JSON.parse(event.request.postData) : undefined; } catch { parsed = undefined; }
    requests.set(event.requestId, {
      requestURL: new URL(event.request.url).origin + new URL(event.request.url).pathname,
      owner: { origin: new URL(owner.uiUrl).origin, project: owner.project, explorer: owner.explorer },
      requestBody: parsed ? {
        snapshotToken: parsed.snapshotToken,
        expectedDraftVersion: parsed.expectedDraftVersion,
        expectedDraftDigest: parsed.expectedDraftDigest,
        outputId: parsed.outputId,
        stageId: parsed.stageId,
      } : null,
    });
  });
  const stopResponse = onCDP(cdp, 'Network.responseReceived', (event) => {
    const request = requests.get(event.requestId);
    if (!request || event.response.status < 400) return;
    request.status = event.response.status;
    request.mimeType = event.response.mimeType;
    request.diagnostic = {
      ...request,
      responseBody: null,
    };
    report.constructionCapabilitiesFailures ??= [];
    report.constructionCapabilitiesFailures.push(request.diagnostic);
  });
  const stopFinished = onCDP(cdp, 'Network.loadingFinished', (event) => {
    const request = requests.get(event.requestId);
    if (!request?.diagnostic) return;
    const read = cdp.send('Network.getResponseBody', { requestId: event.requestId }).then((result) => {
      const raw = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      try { request.diagnostic.responseBody = JSON.parse(raw); }
      catch { request.diagnostic.responseBody = raw.slice(0, 4000); }
    }).catch((error) => {
      request.diagnostic.responseReadError = error instanceof Error ? error.message : String(error);
    });
    responseReads.push(read);
  });
  const stopFailure = onCDP(cdp, 'Network.loadingFailed', (event) => {
    const request = requests.get(event.requestId);
    if (request) request.loadingFailure = event.errorText ?? 'request failed';
  });
  return {
    stop: async () => {
      stopRequest();
      stopResponse();
      stopFinished();
      stopFailure();
      await Promise.all(responseReads);
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

const setSelectValue = async (cdp, selector, value) => {
  const action = await inspectAction(cdp, selector);
  if (!isActionable(action)) throw new Error('Select control is not actionable: ' + JSON.stringify(action));
  return evaluate(cdp,
    "(()=>{const select=document.querySelector(" + JSON.stringify(selector) + ");if(!select)throw Error('select not found');const setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')?.set;if(!setter)throw Error('native select setter unavailable');setter.call(select," +
      JSON.stringify(value) +
      ");select.dispatchEvent(new Event('input',{bubbles:true}));select.dispatchEvent(new Event('change',{bubbles:true}));return select.value})()");
};

const readGrid = async (cdp, kind = 'saved') => evaluate(cdp, "(()=>{" +
  "const proposal=" + JSON.stringify(kind === 'proposal') + ";" +
  "const table=proposal?document.querySelector('[data-testid=\"construction-proposal-preview\"][data-preview-status=\"ready\"] table'):document.querySelector('[data-testid=\"preview-table-scroll\"] [role=\"table\"]');" +
  "if(!table)return {ready:false,headers:[],rows:[],ariaRowCount:null};" +
  "const tidy=value=>String(value??'').replace(/\\s+/g,' ').trim();" +
  "const headers=proposal?[...table.querySelectorAll('thead th')].map(cell=>tidy(cell.querySelector('span')?.textContent??cell.textContent)):[...table.querySelectorAll('[role=\"columnheader\"]')].map(cell=>tidy(cell.textContent));" +
  "const rows=proposal?[...table.querySelectorAll('tbody tr[data-testid=\"construction-proposal-preview-row\"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>tidy(cell.innerText))):[...table.querySelectorAll('[role=\"row\"]')].slice(1).map(row=>[...row.querySelectorAll('[role=\"cell\"]')].map(cell=>tidy(cell.innerText)));" +
  "return {ready:true,headers,rows,ariaRowCount:table.getAttribute('aria-rowcount')};})()");

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

const waitForSavedRows = async (cdp, count) => waitFor(cdp, savedPreview(count), 30000);

const addRoot = async (cdp, report, resourceType, tableTitle, expectedIDs) => {
  await fill(cdp, '#first-table-name', tableTitle);
  await recordBrowserTiming(report, cdp, {
    name: 'create ' + resourceType + ' source table with its direct identity',
    action: () => click(cdp, 'button', { name: 'Choose ' + resourceType + ' rows' }),
    after: workspaceReady + ' && document.body.innerText.includes(' + JSON.stringify(tableTitle) + ') && Boolean(document.querySelector(\'[data-testid="preview-table-scroll"] [role="table"]\'))',
    timeout: 30000,
  });
  await waitForSavedRows(cdp, expectedIDs.length);
  const grid = await readGrid(cdp);
  const wanted = resourceType.toLowerCase() + ' id';
  const idIndex = grid.headers.findIndex((header) => header.toLowerCase() === wanted || header.toLowerCase() === 'id');
  const ids = idIndex < 0 ? [] : grid.rows.map((row) => row[idIndex]).sort();
  check(report, 'correctness', resourceType + ' source starts with every literal fixture identity',
    JSON.stringify(ids) === JSON.stringify([...expectedIDs].sort()), { headers: grid.headers, ids, expectedIDs });
};

const addRawFields = async (cdp, report, resourceType, fieldPaths, expectedRows) => {
  await click(cdp, 'button[data-testid="construction-action-add-columns"]');
  await waitFor(cdp, "Boolean(document.querySelector('[aria-label=\"Add columns editor\"]'))", 10000);
  await click(cdp, 'button', { name: 'Fields and related data' });
  await click(cdp, 'summary', { name: 'Raw FHIR fields (advanced)' });
  for (const path of fieldPaths) {
    const checkbox = 'input[type="checkbox"][aria-label=' + JSON.stringify('Select ' + resourceType + '.' + path) + ']';
    await waitFor(cdp, 'Boolean(document.querySelector(' + JSON.stringify(checkbox) + '))', 10000);
    const state = await inspectAction(cdp, checkbox);
    if (!isActionable(state)) throw new Error('Raw source field is unavailable: ' + JSON.stringify(state));
    await click(cdp, checkbox);
  }
  const addLabel = 'Add ' + fieldPaths.length + ' selected feature' + (fieldPaths.length === 1 ? '' : 's');
  await waitFor(cdp, '[...document.querySelectorAll("button")].some(button=>button.innerText.trim()===' + JSON.stringify(addLabel) + '&&!button.disabled)', 10000);
  await click(cdp, 'button', { name: addLabel });
  await waitFor(cdp, "[...document.querySelectorAll('button')].some(button=>button.innerText.trim()==='Apply columns'&&!button.disabled)", 30000);
  await recordBrowserTiming(report, cdp, {
    name: 'apply ' + resourceType + ' source fields and render its preview',
    action: () => click(cdp, 'button', { name: 'Apply columns' }),
    after: savedPreview(expectedRows),
    timeout: 30000,
  });
  await click(cdp, 'button', { name: 'Close operation editor' });
};

export const createAndPublishSources = async (context, cdp, report, includePatient = false, rawFieldsByResource = {}) => {
  const created = await createBlankExplorer(cdp, context.target, context.runID, 'combine', report);
  const explorer = created.explorer;
  report.target.explorer = explorer;
  await addRoot(cdp, report, 'Observation', 'Observations', expectedObservations.map((row) => row.id));
  await addRawFields(cdp, report, 'Observation', rawFieldsByResource.Observation ?? ['status', 'valueInteger'], expectedObservations.length);

  await click(cdp, 'button[data-testid="construction-new-table"]');
  await waitFor(cdp, "document.querySelector('#first-table-name') && document.body.innerText.includes('Build another table')", 10000);
  await addRoot(cdp, report, 'DiagnosticReport', 'Diagnostic reports', expectedReports.map((row) => row.id));
  await addRawFields(cdp, report, 'DiagnosticReport', rawFieldsByResource.DiagnosticReport ?? ['status'], expectedReports.length);

  if (includePatient) {
    await click(cdp, 'button[data-testid="construction-new-table"]');
    await waitFor(cdp, "document.querySelector('#first-table-name') && document.body.innerText.includes('Build another table')", 10000);
    await addRoot(cdp, report, 'Patient', 'Patients', expectedPatients.map((row) => row.id));
    await addRawFields(cdp, report, 'Patient', rawFieldsByResource.Patient ?? ['gender'], expectedPatients.length);
  }

  let publishEvent;
  const pendingPublish = waitForCDPEvent(cdp, 'Network.responseReceived', (event) =>
    new URL(event.response.url).pathname.endsWith('/authoring/v2/publish') &&
    event.response.status >= 200 && event.response.status < 300, 60000);
  await recordBrowserTiming(report, cdp, {
    name: includePatient ? 'publish all three exact source tables' : 'publish both exact source tables',
    action: () => click(cdp, 'button', { name: 'Publish' }),
    after: workspaceReady,
    settle: async () => { publishEvent = await pendingPublish; },
    timeout: 60000,
    budget: 30000,
  });
  check(report, 'correctness', 'native source-table publication completed successfully',
    Boolean(publishEvent?.response?.status >= 200 && publishEvent.response.status < 300),
    { status: publishEvent?.response?.status });

  const builder = await readBuilder(context, explorer);
  const observation = documentByRoot(builder, 'Observation');
  const diagnosticReport = documentByRoot(builder, 'DiagnosticReport');
  const docs = { observation, report: diagnosticReport, documents: [observation, diagnosticReport] };
  if (includePatient) {
    docs.patient = documentByRoot(builder, 'Patient');
    docs.documents.push(docs.patient);
  }
  const api = await sourceAPI(context, explorer, created.title, docs, report);
  check(report, 'correctness', 'source revisions are published in the exact project, generation, and authorization scope',
    api.revisions.Observation.tableId && api.revisions.DiagnosticReport.tableId &&
    (!includePatient || api.revisions.Patient.tableId) &&
    api.builder.catalog.generation === context.target.fixtureGeneration &&
    api.builder.catalog.authorizationScopeDigest && api.entries.length >= 2,
    {
      project: context.target.fixtureProject,
      generation: api.builder.catalog.generation,
      authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest,
      revisions: Object.fromEntries(Object.entries(api.revisions).map(([key, entry]) => [key, {
        tableId: entry.tableId, revisionId: entry.revisionId, outputId: entry.outputId, isCurrent: entry.isCurrent,
      }])),
    });
  check(report, 'performance', 'published source API and schema fingerprint captured within five seconds',
    api.apiElapsedMs <= 5000,
    { elapsedMs: api.apiElapsedMs, fingerprint: api.apiFingerprint, generation: api.builder.catalog.generation, authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest });
  report.target.sourceExplorer = explorer;
  report.target.sourceExplorerTitle = created.title;
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
    tableId: entry.tableId,
    revisionId: entry.revisionId,
    outputId: entry.outputId,
    tableTitle: entry.tableTitle,
    outputTitle: entry.outputTitle,
    generation: api.builder.catalog.generation,
    authorizationScopeDigest: api.builder.catalog.authorizationScopeDigest,
  }]));
  report.target.sourceApiFingerprint = api.apiFingerprint;
  return { explorer, docs, api };
};

const publishedRef = (entry) => JSON.stringify([entry.tableId, entry.revisionId, entry.outputId]);

const chooseOperation = async (report, cdp, kind, inputs) => {
  const initialInputs = inputs.slice(0, 2);
  const inputSelectorsReady = initialInputs.map((_, index) =>
    'Boolean(document.querySelector(\'select[aria-label="Input table ' + (index + 1) + '"]:not(:disabled)\'))').join('&&');
  await recordBrowserTiming(report, cdp, {
    name: 'choose ' + kind + ' and load the initial two input selectors',
    action: () => click(cdp, 'button[data-testid="construction-combine-choice-' + kind.toLowerCase() + '"]'),
    after: inputSelectorsReady,
    timeout: 30000,
  });
  for (let index = 0; index < inputs.length; index += 1) {
    if (index >= 2) {
      if (kind !== 'APPEND') throw new Error('Only APPEND can add input tables beyond the initial two.');
      const selectorReady = 'Boolean(document.querySelector(\'select[aria-label="Input table ' + (index + 1) + '"]:not(:disabled)\'))';
      await recordBrowserTiming(report, cdp, {
        name: 'add APPEND input table ' + (index + 1),
        action: () => click(cdp, 'button', { name: 'Add another table' }),
        after: selectorReady,
        timeout: 30000,
      });
    }
    const value = publishedRef(inputs[index]);
    const actual = await setSelectValue(cdp, 'select[aria-label="Input table ' + (index + 1) + '"]', value);
    if (actual !== value) throw new Error('Combine input ' + (index + 1) + ' did not retain its exact published revision tuple.');
  }
};

const configureOutput = async (cdp, index, name, label, sourceFields, kind) => {
  await waitFor(cdp, 'Boolean(document.querySelector(\'input[aria-label="Output field ' + index + ' name"]\'))', 10000);
  await fill(cdp, 'input[aria-label="Output field ' + index + ' name"]', name);
  await fill(cdp, 'input[aria-label="Output field ' + index + ' label"]', label);
  for (const [inputIndex, columnId] of sourceFields) {
    const fieldKind = kind === 'APPEND' ? 'matching field in input ' : 'source field in input ';
    const optionValue = kind === 'APPEND'
      ? columnId === null ? 'empty-for-this-table' : 'column:' + columnId
      : columnId;
    await setSelectValue(cdp, 'select[aria-label="Output field ' + index + ' ' + fieldKind + (inputIndex + 1) + '"]', optionValue);
  }
};

const addOutput = async (cdp, index, name, label, sourceFields, kind) => {
  await click(cdp, 'button', { name: 'Add output field' });
  await configureOutput(cdp, index, name, label, sourceFields, kind);
};

const captureNativeCreateTableCommand = (cdp) => {
  const entries = [];
  const byRequestId = new Map();
  const stopRequest = onCDP(cdp, 'Network.requestWillBeSent', (event) => {
    let url;
    try { url = new URL(event.request.url); } catch { return; }
    if (!url.pathname.endsWith('/authoring/v2/commands') || event.request.method !== 'POST') return;
    let body;
    try { body = event.request.postData ? JSON.parse(event.request.postData) : undefined; } catch { return; }
    if (!body?.commands?.some((command) => command?.type === 'CREATE_TABLE')) return;
    const entry = { requestId: event.requestId, body, status: undefined, response: undefined };
    entries.push(entry);
    byRequestId.set(event.requestId, entry);
  });
  const stopResponse = onCDP(cdp, 'Network.responseReceived', (event) => {
    const entry = byRequestId.get(event.requestId);
    if (entry) entry.status = event.response.status;
  });
  const stopBody = onCDP(cdp, 'Network.loadingFinished', (event) => {
    const entry = byRequestId.get(event.requestId);
    if (!entry) return;
    cdp.send('Network.getResponseBody', { requestId: event.requestId }).then((result) => {
      const raw = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      entry.response = JSON.parse(raw);
    }).catch((error) => { entry.responseReadError = String(error); });
  });
  const stopFailure = onCDP(cdp, 'Network.loadingFailed', (event) => {
    const entry = byRequestId.get(event.requestId);
    if (entry) entry.responseReadError = event.errorText ?? 'command response failed';
  });
  return {
    stop: () => { stopRequest(); stopResponse(); stopBody(); stopFailure(); },
    read: async () => {
      const deadline = Date.now() + 10000;
      while (Date.now() < deadline) {
        const candidates = entries.filter((entry) => entry.body?.commands?.some((command) => command?.type === 'CREATE_TABLE'));
        if (candidates.length > 1) throw new Error('Expected one native CREATE_TABLE request while opening Combine; found ' + candidates.length);
        const entry = candidates[0];
        if (entry?.responseReadError) throw new Error('Could not read the native Combine target command response: ' + entry.responseReadError);
        if (entry?.response) return entry;
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      throw new Error('Timed out capturing the native CREATE_TABLE command and returned workspace.');
    },
  };
};

const readTargetDocument = (builder, outputId) => documentByOutput(builder, outputId);

const assertPinnedInputs = (report, step, api) => {
  const actual = step.inputs.map((input) => [input.tableId, input.revisionId, input.outputId]);
  const expected = [api.revisions.Observation, api.revisions.DiagnosticReport, api.revisions.Patient]
    .slice(0, step.inputs.length)
    .map((entry) => [entry.tableId, entry.revisionId, entry.outputId]);
  check(report, 'persistence', 'saved Combine step refers to the exact published source revisions',
    JSON.stringify(actual) === JSON.stringify(expected), { actual, expected });
};

const selectTarget = async (cdp, outputId) => {
  const selector = '[data-testid="construction-table-' + outputId + '"]';
  const selected = await evaluate(cdp, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"');
  if (!selected) await click(cdp, selector);
  await waitFor(cdp, 'document.querySelector(' + JSON.stringify(selector) + ')?.getAttribute("aria-current")==="page"', 10000);
};

const startCombineTarget = async (context, cdp, report, explorer, observationOutputId, sourceBuilder) => {
  await selectTarget(cdp, observationOutputId);
  const commandCapture = captureNativeCreateTableCommand(cdp);
  try {
    await recordBrowserTiming(report, cdp, {
      name: 'open Combine operation chooser',
      action: () => click(cdp, 'button[data-testid="construction-action-combine"]'),
      after: 'Boolean(document.querySelector(\'[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]\')) && Boolean(document.querySelector(\'[data-testid="construction-combine-editor"]\')) && Boolean(document.querySelector(\'button[data-testid="construction-combine-choice-key_join"]:not(:disabled)\')) && Boolean(document.querySelector(\'button[data-testid="construction-combine-choice-append"]:not(:disabled)\'))',
      timeout: 30000,
    });
    const command = await commandCapture.read();
    const mountedOutputId = await evaluate(cdp, 'document.querySelector(\'[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]\')?.getAttribute("data-output-id") ?? null');
    const expectedRootNodeIds = (sourceBuilder.catalog?.nodes ?? [])
      .filter((node) => node.resourceType === 'Observation' && node.rowRootEligible)
      .map((node) => node.nodeId);
    const previousOutputIds = (sourceBuilder.workspace?.documents ?? [])
      .map((document) => document.output?.id)
      .filter(Boolean);
    const evidence = nativeCombineTargetBindingEvidence({
      requestBody: command.body,
      responseStatus: command.status,
      response: command.response,
      expectedRootNodeIds,
      expectedRootResourceType: 'Observation',
      previousOutputIds,
      mountedOutputId,
    });
    check(report, 'correctness', 'native Combine creates a rooted empty Observation target without adding an authored step or output column',
      evidence.ok, evidence);
    if (!evidence.ok) throw new Error('Native Combine target identity did not bind its creation command, returned workspace, and mounted editor: ' + JSON.stringify(evidence));
    return { outputId: evidence.outputId, rootNodeId: evidence.rootNodeId };
  } finally {
    commandCapture.stop();
  }
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
}, count, { timeout: 30000 });

const waitProposalWithPlaywright = async (page, outputId, count) => page.waitForFunction(({ expectedOutput, expectedRows }) => {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]');
  const preview = document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]');
  return Boolean(panel && preview && panel.getAttribute('data-proposal-id') &&
    preview.getAttribute('data-preview-receipt-id') === panel.getAttribute('data-proposal-id') &&
    preview.getAttribute('data-preview-output-id') === expectedOutput &&
    (expectedRows === undefined || preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === expectedRows));
}, { expectedOutput: outputId, expectedRows: count }, { timeout: 30000 });

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
    timeout: 30000,
    budget: 5000,
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 30000 });
      if (savedRows === null) {
        await page.waitForFunction(expectedOutput => {
          const selected = document.querySelector(`[data-testid="construction-table-${CSS.escape(expectedOutput)}"]`);
          const preview = document.querySelector('[data-testid="preview-table-scroll"]');
          return selected?.getAttribute('aria-current') === 'page' &&
            !document.querySelector('[data-testid="construction-history"]') &&
            !document.querySelector('[data-testid="construction-combine-editor"]') &&
            preview?.textContent?.trim() === 'Add a column to see your table.';
        }, outputId, { timeout: 30000 });
      } else {
        await page.getByTestId('construction-history').waitFor({ state: 'visible', timeout: 30000 });
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
      }, stepId, { timeout: 10000 });
    },
  });
  const edit = page.getByTestId(`construction-edit-step-${stepId}`);
  await action('open saved Combine editor and load pinned sources', edit, () => edit.click(), {
    after: async () => {
      await page.locator('[data-testid="construction-combine-editor"]').waitFor({ state: 'visible' });
      await page.locator('select[aria-label="Input table 1"]').waitFor({ state: 'visible' });
      await page.locator('input[aria-label="Output field 1 label"]').waitFor({ state: 'visible' });
      await page.waitForFunction(() => !document.querySelector('select[aria-label="Input table 1"]')?.disabled &&
        !document.querySelector('input[aria-label="Output field 1 label"]')?.disabled, undefined, { timeout: 10000 });
    },
  });
};

const editSavedStep = async (report, cdp, stepId) => {
  await click(cdp, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(cdp, 'Boolean(document.querySelector(\'[data-testid="construction-edit-step-' + stepId + '"]:not(:disabled)\'))', 10000);
  await recordBrowserTiming(report, cdp, {
    name: 'open saved Combine editor and load pinned sources',
    action: () => click(cdp, '[data-testid="construction-edit-step-' + stepId + '"]'),
    after: "Boolean(document.querySelector('[data-testid=\"construction-combine-editor\"]') && document.querySelector('select[aria-label=\"Input table 1\"]:not(:disabled)') && document.querySelector('input[aria-label=\"Output field 1 label\"]:not(:disabled)'))",
    timeout: 5000,
    budget: 5000,
  });
};

const applyProposal = async (cdp, report, name, after) => {
  await recordBrowserTiming(report, cdp, {
    name,
    action: () => click(cdp, '[data-testid="construction-apply-proposal"]'),
    after,
    timeout: 30000,
  });
};

const removeCombineAndRestoreEmptyRoot = async (context, cdp, report, explorer, target, baseline, stepId, operation) => {
  await click(cdp, '[data-testid="construction-history-step-' + stepId + '"]');
  await waitFor(cdp, 'Boolean(document.querySelector(\'[data-testid="construction-remove-step-' + stepId + '"]:not(:disabled)\'))', 10000);
  await click(cdp, '[data-testid="construction-remove-step-' + stepId + '"]');
  await waitFor(cdp, proposalReady(target.outputId), 30000);
  check(report, 'correctness', operation + ' removal proposal is ready for the rooted empty target',
    Boolean(await evaluate(cdp, proposalReady(target.outputId))), { outputId: target.outputId, stepId });
  await applyProposal(cdp, report, 'Remove ' + operation + ' and restore the rooted empty target',
    rootedEmptyTargetAppliedExpression(target.outputId));
  await reload(cdp, workspaceReady);
  await selectTarget(cdp, target.outputId);
  const builder = await readBuilder(context, explorer);
  const restored = readTargetDocument(builder, target.outputId);
  const restoration = rootedEmptyTargetRestorationEvidence(restored, baseline, target);
  check(report, 'persistence', 'removing ' + operation + ' and reloading restores the rooted empty target', restoration.ok, {
    target,
    rootResourceType: restored.rootResourceType,
    columns: restored.columns,
    construction: restored.construction,
    sameAsPreCombineDocument: restoration.unchanged,
    expectedDocument: baseline,
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
    timeout: 10000,
    after: () => page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected &&
      document.body.innerText.includes('Build your first table'), title),
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  report.target.explorer = explorer;

  const addRootWithUI = async (resourceType, tableTitle, expectedIDs) => {
    await fillWithPlaywright(page, action, '#first-table-name', tableTitle, `name ${resourceType} source table`);
    const choose = page.getByRole('button', { name: `Choose ${resourceType} rows`, exact: true });
    await action(`create ${resourceType} source table with its direct identity`, choose, () => choose.click(), {
      timeout: 30000,
      budget: 5000,
      after: async () => {
        await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 30000 });
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
      timeout: 30000,
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
    new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === publishPath, { timeout: 60000 });
  const publish = page.getByRole('button', { name: 'Publish', exact: true });
  await action(includePatient ? 'publish all three exact source tables' : 'publish both exact source tables', publish, () => publish.click(), {
    timeout: 60000,
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
  const commandResponse = page.waitForResponse(response => createRequest(response.request()), { timeout: 15000 });
  const operationChooser = page.getByTestId('construction-action-combine');
  await action('open Combine operation chooser', operationChooser, () => operationChooser.click(), {
    timeout: 30000,
    budget: 5000,
    after: async () => {
      await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]').waitFor({ state: 'visible', timeout: 30000 });
      await page.getByTestId('construction-combine-editor').waitFor({ state: 'visible', timeout: 30000 });
      await page.getByTestId('construction-combine-choice-key_join').waitFor({ state: 'visible', timeout: 30000 });
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
    timeout: 30000,
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
  await page.locator(nameSelector).waitFor({ state: 'visible', timeout: 10000 });
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
    timeout: 30000,
    after: () => waitProposalWithPlaywright(page, target.outputId),
  });
  check(report, 'correctness', operation + ' removal proposal is ready for the rooted empty target', true, { outputId: target.outputId, stepId });
  await applyProposalWithPlaywright(page, action, `Remove ${operation} and restore the rooted empty target`, null, target.outputId);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 30000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  const builder = await readBuilder(context, explorer);
  const restored = readTargetDocument(builder, target.outputId);
  const restoration = rootedEmptyTargetRestorationEvidence(restored, baseline, target);
  check(report, 'persistence', 'removing ' + operation + ' and reloading restores the rooted empty target', restoration.ok, {
    target, rootResourceType: restored.rootResourceType, columns: restored.columns, construction: restored.construction,
    sameAsPreCombineDocument: restoration.unchanged, expectedDocument: baseline,
  });
};

const runJoin = context => runPlaywrightCase(context, 'builder-combine', 'join', async ({ page, report, action }) => {
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
    timeout: 30000,
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
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 30000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedInnerRows.length);
  await exactRowsWithPlaywright(report, 'INNER table reload retains the three exact rows', page, 'saved', joinHeaders, expectedInnerRows);

  await editSavedStepWithPlaywright(page, action, step.id);
  const noMatch = page.locator('select[aria-label="If a row in the first table has no match"]');
  await action('render LEFT Join preview', noMatch, () => noMatch.selectOption('LEFT'), {
    timeout: 30000,
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
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 30000 });
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
    timeout: 30000,
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
  await page.getByTestId(`construction-table-${target.outputId}`).waitFor({ state: 'visible', timeout: 30000 });
  await selectTargetWithPlaywright(page, action, target.outputId);
  await waitSavedPreviewWithPlaywright(page, expectedLeftRows.length);
  await exactRowsWithPlaywright(report, 'LEFT rows and nulls survive Builder reload', page, 'saved', joinHeaders, expectedLeftRows);

  await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, step.id, 'KEY_JOIN');
  await assertSourceImmutability(context, explorer, docs, api, report);
  report.target.explorer = explorer;
  report.target.combineTarget = target;
});

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

const runAppend = (context) => runBrowserCase(context, 'builder-combine', 'append', async ({ cdp, report }) => {
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
      ...fixture,
      appendNullPaddingRows: rawAppendOracle,
      expectedUnionRows: fixture.patients.length + fixture.observations.length + fixture.diagnosticReports.length,
    });

  const prepared = await createAndPublishSources(context, cdp, report, true);
  report.target.fixtureRawOracle = { ...report.target.fixtureRawOracle, appendNullPaddingRows: rawAppendOracle };
  const explorer = prepared.explorer;
  const docs = prepared.docs;
  const api = prepared.api;
  const target = await startCombineTarget(context, cdp, report, explorer, docs.observation.output.id, api.builder);
  report.target.combineTarget = target;
  const capabilitiesFailures = captureConstructionCapabilitiesFailures(cdp, report, {
    uiUrl: context.target.uiUrl,
    project: context.target.fixtureProject,
    explorer,
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
      ][index])),
      { sourceRevisions, expected: api.revisions });

    const proposalRequests = [];
    report.nativeAppendProposals = proposalRequests;
    const proposalRequestsById = new Map();
    const stopProposalCapture = onCDP(cdp, 'Network.requestWillBeSent', (event) => {
      let url;
      try { url = new URL(event.request.url); } catch { return; }
      if (!url.pathname.endsWith('/construction-proposals') || event.request.method !== 'POST') return;
      let body;
      try { body = event.request.postData ? JSON.parse(event.request.postData) : undefined; } catch { return; }
      if (body?.outputId !== target.outputId) return;
      const entry = { body, requestId: event.requestId, status: undefined, response: undefined };
      proposalRequests.push(entry);
      proposalRequestsById.set(event.requestId, entry);
    });
    const stopProposalResponseCapture = onCDP(cdp, 'Network.responseReceived', (event) => {
      const entry = proposalRequestsById.get(event.requestId);
      if (entry) entry.status = event.response.status;
    });
    const stopProposalBodyCapture = onCDP(cdp, 'Network.loadingFinished', (event) => {
      const entry = proposalRequestsById.get(event.requestId);
      if (!entry) return;
      cdp.send('Network.getResponseBody', { requestId: event.requestId }).then((result) => {
        const text = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
        entry.response = JSON.parse(text);
      }).catch((error) => { entry.responseReadError = String(error); });
    });
    const stopProposalFailureCapture = onCDP(cdp, 'Network.loadingFailed', (event) => {
      const entry = proposalRequestsById.get(event.requestId);
      if (entry) entry.responseReadError = event.errorText ?? 'proposal response failed';
    });
    const readCapturedProposal = async (entry) => {
      const deadline = Date.now() + 10000;
      while (!entry.response && !entry.responseReadError && Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
      if (!entry.response) throw new Error('Could not read the exact APPEND proposal response body: ' + (entry.responseReadError ?? 'timed out'));
      return entry.response;
    };

    await chooseOperation(report, cdp, 'APPEND', sourceRevisions);
    await addOutput(cdp, 1, 'record_id', 'Record ID', [], 'APPEND');
    await addOutput(cdp, 2, 'status', 'Status', [], 'APPEND');
    await addOutput(cdp, 3, 'numeric_value', 'Numeric value', [], 'APPEND');
    await setSelectValue(cdp, 'select[aria-label="Output field 3 matching field in input 1"]', 'column:' + api.columns.observationInteger.id);
    const incompatibleOptions = await evaluate(cdp,
      '[...document.querySelector(\'select[aria-label="Output field 3 matching field in input 2"]\').options].map(option=>({value:option.value,text:option.innerText.trim()}))');
    check(report, 'correctness', 'APPEND field choices reject a source with a different scalar type',
      !incompatibleOptions.some((option) => option.value === 'column:' + api.columns.reportStatus.id),
      { observationNumeric: api.columns.observationInteger, diagnosticReportChoices: incompatibleOptions });
    await click(cdp, 'button[aria-label="Remove output field 3"]');
    await waitFor(cdp, "Boolean(document.querySelector('select[aria-label=\"Output field 2 matching field in input 3\"]'))", 10000);

    await configureOutput(cdp, 1, 'record_id', 'Record ID', [
      [0, api.columns.observationID.id],
      [1, api.columns.reportID.id],
      [2, api.columns.patientID.id],
    ], 'APPEND');
    await configureOutput(cdp, 2, 'status', 'Status', [
      [0, api.columns.observationStatus.id],
      [1, api.columns.reportStatus.id],
      [2, null],
    ], 'APPEND');
    await addOutput(cdp, 3, 'patient_gender', 'Patient gender', [
      [0, null],
      [1, null],
    ], 'APPEND');
    const emptySelections = await evaluate(cdp, `(()=>({
      statusPatient:document.querySelector('select[aria-label="Output field 2 matching field in input 3"]')?.value,
      genderObservation:document.querySelector('select[aria-label="Output field 3 matching field in input 1"]')?.value,
      genderReport:document.querySelector('select[aria-label="Output field 3 matching field in input 2"]')?.value,
      genderPatientUnconfigured:document.querySelector('select[aria-label="Output field 3 matching field in input 3"]')?.value==='',
      incompletePreviewCleared:!document.querySelector('[data-testid="construction-proposal-panel"]'),
      explicitOptionVisible:[...document.querySelectorAll('select[aria-label^="Output field"]')].every(select=>[...select.options].some(option=>option.value==='empty-for-this-table'))
    }))()`);
    check(report, 'usability', 'APPEND editor requires explicit Empty for this table choices while blank mappings stay unconfigured',
      emptySelections.statusPatient === 'empty-for-this-table' &&
      emptySelections.genderObservation === 'empty-for-this-table' &&
      emptySelections.genderReport === 'empty-for-this-table' &&
      emptySelections.genderPatientUnconfigured && emptySelections.incompletePreviewCleared &&
      emptySelections.explicitOptionVisible,
      emptySelections);

    const actualOutputRows = await evaluate(cdp, `(()=>{
      const editor=document.querySelector('[data-testid="construction-combine-editor"]');
      if(!editor)return [];
      const nameFields=[...editor.querySelectorAll('input[aria-label^="Output field "][aria-label$=" name"]')];
      return nameFields.map((nameField,rowIndex)=>({
        name:nameField.value,
        label:editor.querySelector('input[aria-label="Output field '+(rowIndex+1)+' label"]')?.value??null,
        mappings:Array.from({length:3},(_,inputIndex)=>editor.querySelector('select[aria-label="Output field '+(rowIndex+1)+' matching field in input '+(inputIndex+1)+'"]')?.value??null)
      }));
    })()`);
    const expectedOutputRows = [
      { name: 'record_id', label: 'Record ID', mappings: [
        'column:' + api.columns.observationID.id,
        'column:' + api.columns.reportID.id,
        'column:' + api.columns.patientID.id,
      ] },
      { name: 'status', label: 'Status', mappings: [
        'column:' + api.columns.observationStatus.id,
        'column:' + api.columns.reportStatus.id,
        'empty-for-this-table',
      ] },
      { name: 'patient_gender', label: 'Patient gender', mappings: [
        'empty-for-this-table',
        'empty-for-this-table',
        '',
      ] },
    ];
    const outputConfiguration = appendEditorConfigurationEvidence(actualOutputRows, expectedOutputRows);
    check(report, 'correctness', 'APPEND editor has exactly three named output rows with the intended mappings before preview',
      outputConfiguration.ok, outputConfiguration);

    await recordBrowserTiming(report, cdp, {
      name: 'automatically preview three-input APPEND with explicit absent-field mappings',
      action: () => setSelectValue(cdp, 'select[aria-label="Output field 3 matching field in input 3"]', 'column:' + api.columns.patientGender.id),
      after: proposalPreview(appendedRows.length, target.outputId),
      timeout: 30000,
    });
    const candidateRequest = [...proposalRequests].reverse().find(({ body }) => {
      const last = body?.candidateConstruction?.steps?.at(-1);
      return last?.operation?.combine?.kind === 'APPEND';
    });
    assert(candidateRequest, 'completing all APPEND slots must issue an automatic construction proposal');
    const candidateResponse = await readCapturedProposal(candidateRequest);
    const candidateStep = candidateRequest.body.candidateConstruction.steps.at(-1);
    const candidateShape = assertAppendNullPaddingStep(report,
      'automatic APPEND proposal omits only explicit-empty source pairs and marks padded outputs nullable',
      candidateStep, api);
    check(report, 'correctness', 'automatic APPEND proposal is bound to the exact target output and current Builder snapshot',
      candidateRequest.body.outputId === target.outputId &&
      candidateRequest.body.snapshotToken === builderAtTarget.catalog.snapshotToken &&
      candidateRequest.body.expectedDraftVersion === builderAtTarget.draftVersion &&
      candidateRequest.body.expectedDraftDigest === builderAtTarget.draftDigest,
      { requestId: candidateRequest.requestId, outputId: candidateRequest.body.outputId, targetOutputId: target.outputId,
        snapshotTokenMatched: candidateRequest.body.snapshotToken === builderAtTarget.catalog.snapshotToken,
        expectedDraftVersion: builderAtTarget.draftVersion,
        expectedDraftDigestMatched: candidateRequest.body.expectedDraftDigest === builderAtTarget.draftDigest });
    const domIdentity = await evaluate(cdp, `(()=>({
      proposalId:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id')??null,
      receiptId:document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id')??null
    }))()`);
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
      responseEvidence.ok, { ...responseEvidence, inputs: candidateShape.actualInputs, requestId: candidateRequest.requestId });
    exactRows(report, 'APPEND preview matches the literal null-padding oracle and contains the complete eight-row input union',
      await readGrid(cdp, 'proposal'), appendHeaders, appendedRows);
    stopProposalCapture();
    stopProposalResponseCapture();
    stopProposalBodyCapture();
    stopProposalFailureCapture();

    await applyProposal(cdp, report, 'Apply absent-field APPEND to the rooted target',
      '!document.querySelector(\'[data-testid="construction-proposal-panel"]\') && ' + savedPreview(appendedRows.length));
    const applied = await readBuilder(context, explorer);
    let targetDocument = readTargetDocument(applied, target.outputId);
    let step = targetDocument.construction?.steps?.[0];
    if (!step || targetDocument.construction.steps.length !== 1 || step.operation?.combine?.kind !== 'APPEND') {
      throw new Error('Applying absent-field APPEND did not persist exactly one APPEND step: ' + JSON.stringify(targetDocument.construction));
    }
    assertPinnedInputs(report, step, api);
    assertAppendNullPaddingStep(report, 'Apply persists the exact sparse three-input APPEND mapping and nullable outputs', step, api);
    exactRows(report, 'APPEND Apply preserves the exact null-padded row union', await readGrid(cdp), appendHeaders, appendedRows);
    await reload(cdp, workspaceReady);
    await selectTarget(cdp, target.outputId);
    await waitForSavedRows(cdp, appendedRows.length);
    exactRows(report, 'APPEND null padding and complete input union survive Builder reload',
      await readGrid(cdp), appendHeaders, appendedRows);

    const savedStepId = step.id;
    const savedLabel = step.outputs[2]?.label;
    await editSavedStep(report, cdp, step.id);
    const reconstructedEmptyChoices = await evaluate(cdp, `(()=>({
      statusPatient:document.querySelector('select[aria-label="Output field 2 matching field in input 3"]')?.value,
      genderObservation:document.querySelector('select[aria-label="Output field 3 matching field in input 1"]')?.value,
      genderReport:document.querySelector('select[aria-label="Output field 3 matching field in input 2"]')?.value
    }))()`);
    check(report, 'persistence', 'editing a saved APPEND reconstructs each omitted mapping as explicit Empty for this table',
      reconstructedEmptyChoices.statusPatient === 'empty-for-this-table' &&
      reconstructedEmptyChoices.genderObservation === 'empty-for-this-table' &&
      reconstructedEmptyChoices.genderReport === 'empty-for-this-table',
      reconstructedEmptyChoices);
    await recordBrowserTiming(report, cdp, {
      name: 'preview APPEND edit after saved empty choices are reconstructed',
      action: () => fill(cdp, 'input[aria-label="Output field 3 label"]', 'Patient sex'),
      after: proposalPreview(appendedRows.length, target.outputId) + ' && [...document.querySelectorAll(\'[data-testid="construction-proposal-preview"] th span\')].some(span=>span.innerText.trim()===\'Patient sex\')',
      timeout: 30000,
    });
    exactRows(report, 'APPEND edit preview keeps exact padded rows',
      await readGrid(cdp, 'proposal'), ['Record ID', 'Status', 'Patient sex'], appendedRows);
    await click(cdp, '[data-testid="construction-cancel-proposal"]');
    await waitFor(cdp, "Boolean(document.querySelector('[data-testid=\"construction-history\"]')) && !document.querySelector('[data-testid=\"construction-combine-editor\"]') && !document.querySelector('[data-testid=\"construction-proposal-panel\"]')", 10000);
    await reload(cdp, workspaceReady);
    await selectTarget(cdp, target.outputId);
    await waitForSavedRows(cdp, appendedRows.length);
    const cancelled = await readBuilder(context, explorer);
    targetDocument = readTargetDocument(cancelled, target.outputId);
    step = targetDocument.construction?.steps?.[0];
    check(report, 'persistence', 'Cancel leaves the original absent-field APPEND schema and label unchanged',
      Boolean(step?.id === savedStepId && step.outputs?.[2]?.label === savedLabel),
      { stepId: step?.id, outputLabels: step?.outputs?.map((output) => output.label), expectedStepId: savedStepId, expectedThirdLabel: savedLabel });
    assertAppendNullPaddingStep(report, 'Cancel preserves the saved sparse APPEND mapping', step, api);
    exactRows(report, 'cancelled APPEND edit retains null padding and all source rows after reload',
      await readGrid(cdp), appendHeaders, appendedRows);

    await editSavedStep(report, cdp, savedStepId);
    await recordBrowserTiming(report, cdp, {
      name: 'repreview edited APPEND output label with absent-field mappings intact',
      action: () => fill(cdp, 'input[aria-label="Output field 3 label"]', 'Patient sex'),
      after: proposalPreview(appendedRows.length, target.outputId),
      timeout: 30000,
    });
    exactRows(report, 'edited APPEND output label preview retains the exact null-padded union',
      await readGrid(cdp, 'proposal'), ['Record ID', 'Status', 'Patient sex'], appendedRows);
    await applyProposal(cdp, report, 'Apply APPEND label edit with absent-field mappings',
      '!document.querySelector(\'[data-testid="construction-proposal-panel"]\') && ' + savedPreview(appendedRows.length));
    const edited = await readBuilder(context, explorer);
    targetDocument = readTargetDocument(edited, target.outputId);
    step = targetDocument.construction?.steps?.[0];
    check(report, 'persistence', 'edited APPEND label and exact sparse mappings survive Apply',
      Boolean(step?.id === savedStepId && step.outputs?.[2]?.label === 'Patient sex'),
      { stepId: step?.id, outputLabels: step?.outputs?.map((output) => output.label) });
    assertAppendNullPaddingStep(report, 'edited APPEND preserves omitted mappings and nullable outputs', step, api);
    await reload(cdp, workspaceReady);
    await selectTarget(cdp, target.outputId);
    await waitForSavedRows(cdp, appendedRows.length);
    exactRows(report, 'edited APPEND null padding survives reload',
      await readGrid(cdp), ['Record ID', 'Status', 'Patient sex'], appendedRows);

    await removeCombineAndRestoreEmptyRoot(context, cdp, report, explorer, target, emptyTargetBaseline, savedStepId, 'APPEND');
    await assertSourceImmutability(context, explorer, docs, api, report);
    report.target.explorer = explorer;
    report.target.combineTarget = target;
  } finally {
    await capabilitiesFailures.stop();
  }
});

export const runBuilderCombine = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) reports.push(await (caseName === 'join' ? runJoin(context) : runAppend(context)));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-combine', argv: process.argv.slice(2), runner: runBuilderCombine, mutating: true });
}
