import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `sourceaggregate-mixed-sibling-${Date.now()}-${randomUUID().slice(0, 8)}`;
const evidence = process.argv[2] ?? `/tmp/loom-${explorer}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
const apiBuildTarget = 'local-cda-api';
const sourceRoot = process.env.LOOM_SOURCE_ROOT ?? fileURLToPath(new URL('..', import.meta.url));
const patientType = 'Patient';
const targetTypes = ['Condition', 'Observation', 'Specimen'];
const seedLimit = 2000;
const witnessIDLimit = 25;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = `${root}/${explorer}/selections`;

assert.equal(apiOrigin, 'http://127.0.0.1:8188', 'This verifier is scoped to the explicitly configured local no-auth API');
assert.equal(uiOrigin, 'http://127.0.0.1:30008', 'This verifier is scoped to the explicitly configured local Builder UI');
assert.notEqual(explorer, protectedExplorer, 'The verifier must own a fresh QA Explorer');

const report = {
  started: new Date().toISOString(),
  explorer,
  protectedExplorer,
  protectedExplorerUntouched: true,
  target: { apiOrigin, uiOrigin, project, generation, arangoContainer },
  localScopeAssertion: {
    mode: 'localhost unrestricted, no-auth local API',
    apiOrigin,
    authorizationHeadersSent: false,
    catalogScopeDigest: null,
  },
  behavior: 'Patient-root table with sibling Condition, Observation, and Specimen SourceAggregate COUNT columns sharing the subject_Patient traversal prefix',
  compilerClass: {
    authoredSource: 'ADD_COLUMN_SOURCE with kind=aggregate and operation=COUNT on three distinct related occurrences',
    semanticLowering: 'SourceAggregate is attached to each occurrence SemanticNode aggregate list',
    physicalLowering: 'Generic sibling traversal lowering emits three top-level PhysicalSetOp operations',
    optimizationCandidate: 'The three PhysicalSetOps share Patient + subject_Patient prefix and differ by target resource type',
    sourceProof: [
      'internal/explorer/compilation/semantic_compile.go:367 attaches SourceAggregate to nodes[column.OccurrenceID].aggregates',
      'internal/dataframe/compiler/lower/child_sets.go:13 builds one correlated PhysicalSet for each child node',
      'internal/dataframe/compiler/lower/plan_orchestration.go:119-125 appends child PhysicalSetOps to plan.Operations',
      'internal/dataframe/compiler/optimize/optimize.go:22-24 groups top-level PhysicalSetOps by traversal prefix',
    ],
    assertionBoundary: 'Native Preview row values are checked against independently scoped distinct-resource counts; optimizer IR internals are source-proven, not exposed by the browser API.',
  },
  bounds: { sortedScopedPatientSeeds: seedLimit, returnedChildWitnessIDsPerType: witnessIDLimit },
  coverageBoundary: { group: 'deferred', pivot: 'deferred' },
  cases: [],
  apiRequests: [],
  nativeRequests: [],
  errors: [],
};
await mkdir(evidence, { recursive: true });

let browser;
let builder;
let outputId;
let rootColumnId;
let routeOccurrences = {};
let frozenSource;
let frozenApiBuild;
let originalWorkspace;
let originalDocument;
const nativeByID = new Map();
const pendingNativeReads = new Set();
const pause = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const pathOf = entry => entry.path.split('?')[0];
const doc = (state = builder) => state.workspace?.documents?.find(document => document.output.id === outputId);
const columnID = column => column?.column ?? column?.id;
const readApiBuildStamp = () => checkContainerApiBuildStamp(localCDAApiContainer());

class BoundedAbsenceError extends Error {
  constructor(message) { super(message); this.name = 'BoundedAbsenceError'; }
}

const record = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms; required maximum is 5000 ms`);
  report.cases.push({ name, durationMs, ...details });
};

const api = async (path, body) => {
  const method = body === undefined ? 'GET' : 'POST';
  const requestID = `sourceaggregate-mixed-sibling-count-${randomUUID()}`;
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': requestID };
  assert.equal(Object.keys(headers).some(name => name.toLowerCase() === 'authorization'), false);
  const response = await fetch(apiOrigin + path, {
    method, headers,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : undefined; } catch { value = text; }
  report.apiRequests.push({ requestID, method, path, body, status: response.status,
    response: path.endsWith('/builder') ? {
      draftVersion: value?.draftVersion,
      draftDigest: value?.draftDigest,
      catalog: { generation: value?.catalog?.generation, authorizationScopeDigest: value?.catalog?.authorizationScopeDigest },
      documentCount: value?.workspace?.documents?.length ?? 0,
    } : value });
  assert(response.ok, `${response.status} ${method} ${path}: ${JSON.stringify(value)}`);
  return value;
};

const command = async commands => {
  const before = builder;
  const response = await api(`${base}/commands`, {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, before.catalog.generation);
  assert.equal(builder.catalog.authorizationScopeDigest, before.catalog.authorizationScopeDigest);
  return response;
};

const rawQuery = query => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const opening = result.stdout.indexOf('[');
  assert(opening >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1200)}`);
  return JSON.parse(result.stdout.slice(opening));
};

const childCountSubquery = `
  LET distinctChildIDs = (
    FOR edge IN fhir_edge
      FILTER edge._to == patient._id
        AND edge.label == "subject_Patient"
        AND edge.project == ${JSON.stringify(project)}
        AND edge.dataset_generation == ${JSON.stringify(generation)}
        AND STARTS_WITH(edge._from, CONCAT(targetType, "/"))
      LET child = DOCUMENT(edge._from)
      FILTER child != null
        AND child.project == ${JSON.stringify(project)}
        AND child.dataset_generation == ${JSON.stringify(generation)}
        AND child.resourceType == targetType
      COLLECT childID = child._id
      SORT childID
      RETURN childID
  )
  RETURN { resourceType: targetType, count: LENGTH(distinctChildIDs),
    witnessOverflow: LENGTH(distinctChildIDs) > ${witnessIDLimit},
    witnessIDs: SLICE(distinctChildIDs, 0, ${witnessIDLimit}) }
`;

const seedQuery = `
LET patientSeeds = (
  FOR patient IN Patient
    FILTER patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND patient.resourceType == ${JSON.stringify(patientType)}
    SORT patient._key
    LIMIT ${seedLimit}
    RETURN patient
)
FOR patient IN patientSeeds
  LET siblingCounts = (
    FOR targetType IN ${JSON.stringify(targetTypes)}
      ${childCountSubquery}
  )
  FILTER LENGTH(siblingCounts) == ${targetTypes.length}
    AND siblingCounts[0].count > 0
    AND siblingCounts[1].count > 0
    AND siblingCounts[2].count > 0
  SORT patient._key
  LIMIT 1
  RETURN {
    patient: { id: patient.id, _id: patient._id, resourceType: patient.resourceType,
      project: patient.project, generation: patient.dataset_generation },
    siblingCounts
  }
`;

const exactOracleQuery = patientID => `
FOR patient IN Patient
  FILTER patient.id == ${JSON.stringify(patientID)}
    AND patient.project == ${JSON.stringify(project)}
    AND patient.dataset_generation == ${JSON.stringify(generation)}
    AND patient.resourceType == ${JSON.stringify(patientType)}
  LET siblingCounts = (
    FOR targetType IN ${JSON.stringify(targetTypes)}
      ${childCountSubquery}
  )
  RETURN {
    patient: { id: patient.id, _id: patient._id, resourceType: patient.resourceType,
      project: patient.project, generation: patient.dataset_generation },
    siblingCounts
  }
`;

const trackBrowser = () => {
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime }) => {
    const url = new URL(request.url);
    if (url.pathname.includes(protectedExplorer)) {
      report.protectedExplorerUntouched = false;
      report.errors.push({ kind: 'protected-explorer-request', path: url.pathname });
      return;
    }
    if (!url.pathname.startsWith(`${base}/`)) return;
    const headers = request.headers ?? {};
    const authHeader = Object.keys(headers).find(name => name.toLowerCase() === 'authorization');
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = request.postData?.slice(0, 32768); }
    const entry = {
      requestId, path: url.pathname + url.search, method: request.method,
      startedAt: wallTime ? Math.round(wallTime * 1000) : Date.now(), body,
      authorizationHeaderObserved: Boolean(authHeader), complete: false,
    };
    if (authHeader) report.errors.push({ kind: 'unexpected-auth-header', path: entry.path });
    nativeByID.set(requestId, entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeByID.get(requestId);
    if (entry) {
      entry.status = response.status;
      entry.serverRequestId = Object.entries(response.headers ?? {}).find(([name]) => name.toLowerCase() === 'x-request-id')?.[1];
    }
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico') &&
      (response.url.startsWith(apiOrigin) || response.url.startsWith(uiOrigin))) {
      report.errors.push({ kind: 'http', path: entry?.path ?? new URL(response.url).pathname, status: response.status });
    }
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeByID.get(requestId);
    if (!entry) return;
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const raw = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      try { entry.response = raw ? JSON.parse(raw) : undefined; } catch { entry.response = raw; }
      entry.complete = true;
    }).catch(error => {
      entry.complete = true;
      entry.responseReadError = String(error);
    }).finally(() => pendingNativeReads.delete(read));
    pendingNativeReads.add(read);
  });
  browser.cdp.on('Network.loadingFailed', ({ requestId, errorText, type }) => {
    const entry = nativeByID.get(requestId);
    if (entry) { entry.complete = true; entry.failure = errorText; }
    if (type === 'Script' && errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: errorText });
  });
  browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => report.errors.push({
    kind: 'runtime', message: exceptionDetails.exception?.description ?? exceptionDetails.text,
  }));
  browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.errors.push({ kind: 'console', message: args.map(arg => arg.value ?? arg.description ?? '').join(' ').slice(0, 500) });
  });
};

const waitNative = async (predicate, fromIndex, timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const entry = report.nativeRequests.slice(fromIndex).find(candidate => candidate.complete && !candidate.failure && predicate(candidate));
    if (entry) {
      assert.equal(entry.status, 200, `Native ${entry.path} returned ${entry.status}: ${JSON.stringify(entry.response).slice(0, 1500)}`);
      return entry;
    }
    await pause(25);
  }
  assert.fail(`Timed out waiting for native request: ${JSON.stringify(report.nativeRequests.slice(fromIndex).map(({ path, status, complete, failure }) => ({ path, status, complete, failure })))}`);
};

const nativeIndex = () => report.nativeRequests.length;
const previewBody = response => response?.preview?.rows !== undefined ? response.preview : response;
const assertPreviewValues = (preview, expectedTypes, phase) => {
  assert(preview && Array.isArray(preview.rows) && Array.isArray(preview.columns), `${phase} omitted typed Preview rows/columns`);
  assert.equal(preview.outputId, outputId, `${phase} Preview belongs to another output`);
  assert(preview.receiptId, `${phase} Preview has no receipt`);
  assert.equal(preview.rowCount, 1, `${phase} must retain one selected Patient row`);
  assert.equal(preview.rows.length, 1, `${phase} must return exactly one row`);
  const row = preview.rows[0];
  assert.equal(row[rootColumnId], report.oracle.patient.id, `${phase} changed the exact selected Patient identity`);
  for (const targetType of expectedTypes) {
    const column = report.savedColumns[targetType];
    assert(column, `${phase} has no saved ${targetType} output identity`);
    assert(preview.columns.some(candidate => candidate.column === column.name), `${phase} Preview omitted ${targetType} output column`);
    assert.equal(row[column.name], report.oracle.counts[targetType],
      `${phase} ${targetType} COUNT differs from independent raw scoped Arango oracle`);
  }
  assert.equal(preview.columns.length, 1 + expectedTypes.length, `${phase} has unexpected Preview outputs`);
  return { receiptId: preview.receiptId, rowCount: preview.rowCount, values: Object.fromEntries(expectedTypes.map(type => [type, row[report.savedColumns[type].name]])) };
};

const prepareAdvancedSourceSetup = async () => {
  const setupSelector = '[data-testid="construction-source-setup"]';
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(setupSelector)}))`);
  const sourceSetupOpen = await browserEval(browser.cdp,
    `return document.querySelector(${JSON.stringify(setupSelector)})?.open===true;`);
  if (!sourceSetupOpen) await click(browser.cdp, `${setupSelector} > summary`);
  await waitForBrowser(browser.cdp,
    `document.querySelector(${JSON.stringify(setupSelector)})?.open===true`);

  const graphButtonSelector = '[aria-label="Feature authoring view"] button';
  const graphSelected = await browserEval(browser.cdp,
    `return [...document.querySelectorAll(${JSON.stringify(graphButtonSelector)})].some(button=>button.innerText.trim()==='Advanced graph'&&button.getAttribute('aria-pressed')==='true');`);
  if (!graphSelected) await click(browser.cdp, graphButtonSelector, { name: 'Advanced graph' });
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('[aria-label="Current traversal"] button[data-occurrence-id]')) && [...document.querySelectorAll(${JSON.stringify(graphButtonSelector)})].some(button=>button.innerText.trim()==='Advanced graph'&&button.getAttribute('aria-pressed')==='true')`);
};

const renderTable = async (expectedTypes, phase) => {
  const expectedColumns = 1 + expectedTypes.length;
  await waitForBrowser(browser.cdp, `(() => {
    const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return Boolean(table && table.getAttribute('aria-rowcount')==='2' &&
      table.getAttribute('aria-colcount')===${JSON.stringify(String(expectedColumns))} &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:'));
  })()`);
  const view = await browserEval(browser.cdp, `const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {
    rowCount:table?.getAttribute('aria-rowcount'),columnCount:table?.getAttribute('aria-colcount'),
    headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),
    rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length)
  };`);
  assert.equal(view.rowCount, '2', `${phase} rendered the wrong data row count`);
  assert.equal(view.columnCount, String(expectedColumns), `${phase} rendered the wrong column count`);
  assert.equal(view.rows.length, 1, `${phase} must render the one selected Patient row`);
  const row = view.rows[0];
  const outputKeys = [rootColumnId, ...expectedTypes.map(type => report.savedColumns[type].name)];
  assert.equal(row.length, outputKeys.length, `${phase} rendered the wrong number of bound outputs`);
  const byColumn = new Map(outputKeys.map((column, index) => [column, row[index]]));
  assert.equal(byColumn.get(rootColumnId), report.oracle.patient.id, `${phase} visible row has the wrong Patient ID`);
  for (const targetType of expectedTypes) {
    const saved = report.savedColumns[targetType];
    assert.equal(byColumn.get(saved.name), String(report.oracle.counts[targetType]), `${phase} visible ${targetType} count differs from raw oracle`);
  }
  return view;
};

const openTable = async (expectedTypes, phase) => {
  const startedAt = Date.now();
  const fromIndex = nativeIndex();
  await navigate(browser.cdp, `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await prepareAdvancedSourceSetup();
  const view = await renderTable(expectedTypes, phase);
  const request = await waitNative(entry => pathOf(entry).endsWith('/preview') && entry.body?.outputId === outputId, fromIndex);
  const preview = previewBody(request.response);
  const values = assertPreviewValues(preview, expectedTypes, phase);
  record(phase, startedAt, { rowCount: preview.rowCount, columnCount: preview.columns.length, receiptId: preview.receiptId });
  return { view, request, preview, values };
};

const addRelatedSourceAggregateCount = async targetType => {
  const start = Date.now();
  const fromIndex = nativeIndex();
  const occurrenceId = routeOccurrences[targetType];
  assert(occurrenceId, `Patient-to-${targetType} route occurrence is missing`);
  await prepareAdvancedSourceSetup();
  await click(browser.cdp, '[aria-label="Current traversal"] button[data-occurrence-id]', { name: targetType });
  await waitForBrowser(browser.cdp,
    `document.querySelector('[aria-label="Current traversal"] [data-traversal-label]')?.innerText.includes(${JSON.stringify(`Selected parent: ${targetType}`)})`);
  await waitForBrowser(browser.cdp,
    `[...document.querySelectorAll('aside > div:first-child > div:nth-child(2) button')].some(button=>button.innerText.trim()==='Count'&&!button.disabled)`);
  await click(browser.cdp, 'aside > div:first-child > div:nth-child(2) button', { name: 'Count' });

  const add = await waitNative(entry => pathOf(entry).endsWith('/commands') &&
    entry.body?.commands?.some(command => command.type === 'ADD_COLUMN_SOURCE' &&
      command.outputId === outputId && command.occurrenceId === occurrenceId &&
      command.source?.kind === 'aggregate' && command.source?.aggregate?.operation === 'COUNT'), fromIndex);
  const authored = add.body.commands.find(command => command.type === 'ADD_COLUMN_SOURCE');
  assert.deepEqual(authored.source, { kind: 'aggregate', aggregate: { operation: 'COUNT' } },
    'Native Advanced RelatedFeatureCreator must submit the exact legacy COUNT SourceAggregate');
  assert.equal(authored.title, `${targetType} count`);
  builder = await api(`${base}/builder`);
  const addedColumn = doc().columns.find(column => column.occurrenceId === occurrenceId &&
    column.source?.kind === 'aggregate' && column.source.aggregate?.operation === 'COUNT');
  assert(addedColumn, `Native Count did not persist a ${targetType} SourceAggregate column`);
  assert.equal(addedColumn.label, `${targetType} count`);
  assert.equal(addedColumn.logicalType, 'integer');
  report.savedColumns[targetType] = {
    column: addedColumn.column,
    columnId: addedColumn.columnId,
    name: addedColumn.column,
    label: addedColumn.label,
    occurrenceId,
  };
  const expectedTypes = targetTypes.filter(type => Object.hasOwn(report.savedColumns, type));
  assert.equal(doc().columns.length, originalDocument.columns.length + expectedTypes.length,
    'Each native related Count must append one legacy source column');
  assert.equal(doc().construction, undefined, 'SourceAggregate Count must remain in the legacy source column contract');
  assert.equal(doc().rootResourceType, patientType);
  assert.equal(doc().population.selectionRevisionId, report.oracle.selectionRevisionId);
  assert.equal(doc().population.route?.length ?? 0, 0, 'Sibling aggregate counts must preserve direct Patient population');
  assert.deepEqual(doc().route.children.map(child => child.occurrenceId), targetTypes.map(type => routeOccurrences[type]));

  const previewRequest = await waitNative(entry => pathOf(entry).endsWith('/preview') &&
    entry.body?.outputId === outputId && previewBody(entry.response)?.rows?.length === 1, fromIndex);
  const preview = previewBody(previewRequest.response);
  const values = assertPreviewValues(preview, expectedTypes, `add-${targetType}-source-aggregate-count`);
  const view = await renderTable(expectedTypes, `add-${targetType}-source-aggregate-count`);
  record(`native-${targetType}-related-feature-count`, start, {
    commandStatus: add.status,
    requestCommand: authored,
    occurrenceId,
    column: { column: addedColumn.column, columnId: addedColumn.columnId, label: addedColumn.label, logicalType: addedColumn.logicalType },
    receiptId: preview.receiptId,
    values: values.values,
    visibleRows: view.rows,
  });
  return { add, addedColumn, preview, view };
};

const removeRelatedCount = async targetType => {
  const saved = report.savedColumns[targetType];
  assert(saved, `No saved ${targetType} count exists to remove`);
  const currentTypes = targetTypes.filter(type => Object.hasOwn(report.savedColumns, type));
  const remainingTypes = currentTypes.filter(type => type !== targetType);
  const start = Date.now();
  const fromIndex = nativeIndex();
  const columnsListSelector = '[role="list"][aria-label="Table columns"]';
  const columnItemSelector = `[role="listitem"][data-column-name=${JSON.stringify(saved.column)}]`;
  const removeButtonSelector = `${columnItemSelector} button[aria-label=${JSON.stringify(`Remove ${saved.label} column`)}]`;
  const columnsMenuOpen = await browserEval(browser.cdp,
    `return Boolean(document.querySelector(${JSON.stringify(columnsListSelector)}));`);
  if (!columnsMenuOpen) await click(browser.cdp, 'button', { name: 'Columns' });
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector(${JSON.stringify(removeButtonSelector)}))`);
  await click(browser.cdp, removeButtonSelector, { name: `Remove ${saved.label} column` });
  const removal = await waitNative(entry => pathOf(entry).endsWith('/commands') &&
    entry.body?.commands?.some(item => item.type === 'REMOVE_COLUMN' && item.outputId === outputId && item.column === saved.column), fromIndex);
  delete report.savedColumns[targetType];
  builder = await api(`${base}/builder`);
  assert(!doc().columns.some(column => column.column === saved.column), `Removing ${targetType} left its source column in Builder state`);
  assert.equal(doc().columns.length, originalDocument.columns.length + remainingTypes.length);
  assert.equal(doc().population.selectionRevisionId, report.oracle.selectionRevisionId);
  assert.equal(doc().construction, undefined, 'Removing a legacy source aggregate must not introduce staged construction state');
  const view = await renderTable(remainingTypes, `remove-${targetType}-count`);
  const expectedColumnCount = 1 + remainingTypes.length;
  const previewRequest = await waitNative(entry => pathOf(entry).endsWith('/preview') && entry.body?.outputId === outputId &&
    previewBody(entry.response)?.columns?.length === expectedColumnCount, fromIndex);
  const preview = previewBody(previewRequest.response);
  const values = assertPreviewValues(preview, remainingTypes, `remove-${targetType}-count`);
  record(`remove-${targetType}-count-to-render`, start, { commandStatus: removal.status, receiptId: preview.receiptId, values: values.values });
  return { view, preview };
};

try {
  const sourceStartedAt = new Date().toISOString();
  report.sourceFreeze = { startedAt: sourceStartedAt };
  frozenSource = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze.watchedFileCount = frozenSource.watchedFileCount;
  const apiBuildStartedAt = new Date().toISOString();
  report.apiBuildFreeze = { target: apiBuildTarget, startedAt: apiBuildStartedAt };
  frozenApiBuild = await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze.initial = frozenApiBuild.initial;

  const [seed] = rawQuery(seedQuery);
  if (!seed) {
    report.status = 'unverified';
    report.productFailure = false;
    report.unverifiedReason = `No Patient with all three nonzero sibling counts was found among the first ${seedLimit} scoped Patient records; this bounded scan does not establish absence elsewhere.`;
    report.oracle = { status: 'bounded-absence', seedQuery, seedLimit, targetTypes };
    throw new BoundedAbsenceError(report.unverifiedReason);
  }
  assert.equal(seed.patient.project, project);
  assert.equal(seed.patient.generation, generation);
  assert.equal(seed.patient.resourceType, patientType);
  const exactQuery = exactOracleQuery(seed.patient.id);
  const [exact] = rawQuery(exactQuery);
  assert(exact, 'Exact raw oracle could not reread the bounded Patient witness');
  assert.deepEqual(exact.patient, seed.patient, 'Bounded finder and exact Patient oracle disagree');
  assert.deepEqual(exact.siblingCounts, seed.siblingCounts, 'Bounded finder and exact sibling-count oracle disagree');
  const counts = Object.fromEntries(exact.siblingCounts.map(entry => [entry.resourceType, entry.count]));
  assert.deepEqual(Object.keys(counts).sort(), [...targetTypes].sort());
  for (const type of targetTypes) assert(Number.isInteger(counts[type]) && counts[type] > 0, `Witness must have at least one ${type}`);
  report.oracle = {
    status: 'selected',
    seedQuery,
    exactQuery,
    seedLimit,
    patient: exact.patient,
    counts,
    childWitnesses: exact.siblingCounts.map(({ resourceType, count, witnessOverflow, witnessIDs }) => ({ resourceType, count, witnessOverflow, witnessIDs })),
    project,
    generation,
    deduplicationIdentity: 'distinct scoped FHIR document _id',
  };

  await api(root, { name: explorer, title: 'Mixed target sibling SourceAggregate COUNT QA' });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation, 'Fresh QA Explorer must use the requested CDA generation');
  const scopeDigest = builder.catalog.authorizationScopeDigest;
  assert(scopeDigest, 'The active local API catalog must expose its explicit authorization scope digest');
  report.localScopeAssertion.catalogScopeDigest = scopeDigest;
  const patientNode = builder.catalog.nodes.find(node => node.resourceType === patientType);
  assert(patientNode, 'CDA catalog has no Patient root');
  for (const type of targetTypes) assert(builder.catalog.nodes.some(node => node.resourceType === type), `CDA catalog has no ${type} node`);

  await command([{ type: 'CREATE_TABLE', title: 'Mixed target sibling SourceAggregate counts', rootNodeId: patientNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const patientIDCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
  assert(patientIDCandidate, 'Patient.id is unavailable as the stable baseline field');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientIDCandidate.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
  rootColumnId = columnID(doc().columns[0]);
  assert(rootColumnId, 'Baseline Patient identity output has no stable column ID');
  for (const targetType of targetTypes) {
    const targetNode = builder.catalog.nodes.find(node => node.resourceType === targetType);
    const edge = builder.catalog.edges.find(candidate => candidate.fromNodeId === patientNode.nodeId &&
      candidate.toNodeId === targetNode.nodeId && candidate.label === 'subject_Patient' && candidate.populated !== false);
    assert(edge, `CDA catalog has no populated Patient to ${targetType} subject_Patient edge`);
    const addedRoute = await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: edge.edgeId }]);
    const occurrenceId = addedRoute.results.find(result => result.type === 'ROUTE_ADDED')?.occurrenceId;
    assert(occurrenceId, `ADD_ROUTE returned no ${targetType} occurrence ID`);
    routeOccurrences[targetType] = occurrenceId;
  }
  report.authoredRoutes = targetTypes.map(targetType => ({
    occurrenceId: routeOccurrences[targetType], targetType, relationship: 'subject_Patient', parentOccurrenceId: 'base',
  }));
  const selection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `sourceaggregate-mixed-sibling-count-${explorer}`,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType: patientType, id: exact.patient.id }] } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, patientType);
  assert.equal(selection.scopeDigest, scopeDigest);
  assert.equal(selection.memberCount, 1);
  const routes = await api(`${base}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'Exact selected Patient must expose a direct table population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const selectionPage = await api(`${selections}/${selection.id}?limit=10`);
  assert.equal(selectionPage.revision.project, project);
  assert.equal(selectionPage.revision.generation, generation);
  assert.equal(selectionPage.revision.resourceType, patientType);
  assert.equal(selectionPage.revision.scopeDigest, scopeDigest);
  assert.equal(selectionPage.revision.memberCount, 1);
  assert.deepEqual(selectionPage.members.map(member => member.ref.id), [exact.patient.id]);
  assert.equal(doc().rootResourceType, patientType);
  assert.equal(doc().population.selectionRevisionId, selection.id);
  assert.equal(doc().population.route?.length ?? 0, 0);
  assert.equal(doc().rows.kind, 'RECORDS');
  assert.equal(doc().columns.length, 1);
  originalWorkspace = structuredClone(builder.workspace);
  originalDocument = structuredClone(doc());
  report.oracle.selectionRevisionId = selection.id;
  report.seed = { outputId, rootColumnId, patientNodeId: patientNode.nodeId, patientCandidateId: patientIDCandidate.candidateId,
    selectionRevisionId: selection.id, scopeDigest, population: originalDocument.population, routeOccurrences };
  report.savedColumns = {};
  assert.deepEqual(report.oracle.patient, { id: exact.patient.id, _id: exact.patient._id, resourceType: patientType, project, generation });

  browser = await launchBrowser(evidence);
  trackBrowser();
  const initial = await openTable([], 'load-base-Patient-table');
  assertPreviewValues(initial.preview, [], 'base Patient table');
  assert.deepEqual(initial.view.rows[0], [exact.patient.id]);
  const originalRenderedRow = initial.view.rows[0];

  for (const targetType of targetTypes) await addRelatedSourceAggregateCount(targetType);
  report.authoredSourceAggregates = doc().columns.filter(column => column.source?.kind === 'aggregate').map(column => ({
    column: column.column,
    columnId: column.columnId,
    occurrenceId: column.occurrenceId,
    targetType: targetTypes.find(type => routeOccurrences[type] === column.occurrenceId),
    operation: column.source.aggregate?.operation,
    label: column.label,
  }));
  assert.deepEqual(report.authoredSourceAggregates.map(column => column.targetType), targetTypes,
    'The same Patient-root output must retain one SourceAggregate column on each typed sibling route occurrence');
  assert(report.authoredSourceAggregates.every(column => column.operation === 'COUNT'));
  assert.equal(doc().construction, undefined, 'This regression must exercise legacy SourceAggregate lowering, not RELATED_SOURCE construction stages');
  assert.equal(doc().columns.length, originalDocument.columns.length + targetTypes.length);

  const reloaded = await openTable(targetTypes, 'reload-all-three-source-aggregate-sibling-counts');
  assert.deepEqual(reloaded.view.rows[0][0], exact.patient.id);
  for (const targetType of targetTypes) {
    const saved = doc().columns.find(column => column.column === report.savedColumns[targetType].column);
    assert(saved, `Reload omitted saved ${targetType} SourceAggregate column`);
    assert.equal(saved.occurrenceId, routeOccurrences[targetType]);
    assert.equal(saved.source.kind, 'aggregate');
    assert.equal(saved.source.aggregate.operation, 'COUNT');
  }

  for (const targetType of [...targetTypes].reverse()) await removeRelatedCount(targetType);
  builder = await api(`${base}/builder`);
  assert.deepEqual(builder.workspace, originalWorkspace,
    'Removing all three source aggregates must restore the exact Patient workspace and its three authored route occurrences');
  assert.deepEqual(doc(), originalDocument,
    'Final removal must restore the exact original Patient source columns, direct population, and sibling route bindings');
  const restored = await openTable([], 'reload-after-removing-all-source-aggregate-siblings');
  assert.deepEqual(restored.view.rows[0], originalRenderedRow, 'Final reload must restore the original Patient row');
  assert.deepEqual(doc().columns.map(columnID), [rootColumnId]);
  assert.equal(doc().construction, undefined, 'Removing legacy source columns must not leave staged construction state');
  assert.equal(doc().population.selectionRevisionId, selection.id);
  assert.equal(doc().population.route?.length ?? 0, 0);
  assert.deepEqual(report.errors, [], `Unexpected native browser errors: ${JSON.stringify(report.errors)}`);
  assert.equal(report.protectedExplorerUntouched, true, 'Protected full-QA Explorer must remain untouched');
  assert.equal(doc().rows.kind, 'RECORDS', 'This regression must keep grouped/pivoted row shaping out of the legacy source aggregate plan');
  assert.deepEqual(report.authoredRoutes.map(route => route.targetType), targetTypes);
  assert.deepEqual(report.authoredRoutes.map(route => route.relationship), targetTypes.map(() => 'subject_Patient'));
  assert.deepEqual(report.authoredSourceAggregates.map(column => column.targetType), targetTypes);
  assert(report.authoredSourceAggregates.every(column => column.operation === 'COUNT'));
  report.persistedWorkspaceDigest = builder.draftDigest;
  report.sourceFreeze = { ...report.sourceFreeze, ...(await frozenSource.assertUnchanged()) };
  report.status = 'passed';
} catch (error) {
  const invalidated = Boolean(error.invalidatesRun);
  report.status = invalidated ? 'invalidated' : error instanceof BoundedAbsenceError ? 'unverified' : 'failed';
  report.error = String(error.stack ?? error);
  if (error instanceof ApiBuildFreezeError) {
    report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: error.before,
      ...(error.after?.checked ? { after: error.after } : {}), unchanged: false,
      invalidatesRun: true, productFailure: false, reason: error.reason };
  } else if (invalidated) {
    report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true };
  }
  if (!(error instanceof BoundedAbsenceError) && !invalidated) process.exitCode = 1;
  if (browser) report.failureUI = await browserEval(browser.cdp, `return {
    tail:document.body.innerText.slice(-8000),
    headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),
    proposal:(()=>{const p=document.querySelector('[data-testid="construction-proposal-panel"]');return p?{proposalId:p.dataset.proposalId,status:p.dataset.proposalStatus,text:p.innerText}:null})(),
    dialog:(()=>{const d=document.querySelector('[role="dialog"]');return d?{text:d.innerText,radios:[...d.querySelectorAll('input[type="radio"]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}))}:null})()
  };`).catch(String);
} finally {
  await Promise.allSettled([...pendingNativeReads]);
  if (frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()), finishedAt: new Date().toISOString() };
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.error = String(error.stack ?? error);
      report.apiBuildFreeze = { ...report.apiBuildFreeze,
        ...(error.before ? { initial: error.before } : {}), ...(error.after ? { after: error.after } : {}),
        unchanged: false, invalidatesRun: true, productFailure: false,
        ...(error.reason ? { reason: error.reason } : {}), finishedAt: new Date().toISOString() };
      process.exitCode = 1;
    }
  } else {
    try {
      const finalOnly = await captureApiBuildFreeze(readApiBuildStamp);
      report.apiBuildFreeze = { ...report.apiBuildFreeze, after: finalOnly.initial, unchanged: false,
        invalidatesRun: true, productFailure: false, finishedAt: new Date().toISOString() };
    } catch (error) {
      report.apiBuildFreeze = { ...report.apiBuildFreeze,
        ...(error.before ? { after: error.before } : {}), unchanged: false,
        invalidatesRun: true, productFailure: false, ...(error.reason ? { reason: error.reason } : {}),
        finishedAt: new Date().toISOString() };
    }
    report.priorStatus ??= report.status;
    report.status = 'invalidated';
    process.exitCode = 1;
  }
  if (frozenSource && !report.sourceFreeze?.unchanged) {
    try {
      report.sourceFreeze = { ...report.sourceFreeze, ...(await frozenSource.assertUnchanged()) };
    } catch (error) {
      report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, error: String(error) };
      if (report.status === 'passed') {
        report.status = 'invalidated';
        report.error = String(error.stack ?? error);
        process.exitCode = 1;
      }
    }
  }
  report.finished = new Date().toISOString();
  report.nativeRequestSummary = report.nativeRequests.map(({ path, method, status, body, response, failure }) => ({ path, method, status, body, response, failure }));
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, explorer, evidence, cases: report.cases.map(({ name, durationMs }) => ({ name, durationMs })), error: report.error }));
