import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertOwnedDevSession, createDevSession, createFreshVerificationFixture, fixtureSourceDigest } from './loom-dev.mjs';

const mode = process.argv[2] === 'fixture-lifecycle' ? 'fixture-lifecycle' : 'full-population';
const evidence = mode === 'fixture-lifecycle'
  ? process.argv[3] ?? `/tmp/loom-root-quantity-pivot-lifecycle-${Date.now()}`
  : process.argv[2] ?? `/tmp/loom-root-quantity-category-${Date.now()}`;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const fixtureDirectory = process.env.LOOM_ROOT_QUANTITY_FIXTURE_DIR ?? fileURLToPath(new URL('../testdata/root-quantity-pivot-fixture', import.meta.url));
let project = process.env.LOOM_CDA_PROJECT;
let expectedGeneration = mode === 'full-population' ? 'cda-fhir-v1' : undefined;
const resourceType = 'Observation';
const explorer = `root-quantity-category-${Date.now()}`;
let apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
let uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
let root;
let base;
if (project) {
  root = `/api/v1/projects/${project}/explorers`;
  base = `${root}/${explorer}/authoring/v2`;
}
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
assert(process.env.LOOM_ARANGO_DATABASE, 'Set LOOM_ARANGO_DATABASE for the isolated CDA source database.');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer: process.env.LOOM_CDA_API_CONTAINER,
  composeProject: process.env.LOOM_CDA_COMPOSE_PROJECT, sourceRoot, arangoContainer,
  clickhouseContainer: process.env.LOOM_CLICKHOUSE_CONTAINER });
const report = { mode, project, expectedGeneration, resourceType, explorer, cases: [], requests: [], authoringRequests: [], nativeRequests: [], errors: [], browserErrors: { runtime: [], console: [], network: [] }, started: new Date().toISOString() };
const inspectPage = (page, inspect, argument) => page.evaluate(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 30000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  return page.waitForFunction(predicate, argument, { timeout }).then(() => undefined);
};
const gotoPage = (page, url) => page.goto(url, { waitUntil: 'domcontentloaded' });
const clickNative = (page, selector, identity = {}) => {
  const locator = selector === 'button' && identity.name
    ? page.getByRole('button', { name: identity.name, exact: true })
    : page.locator(selector);
  return performAction(report, `click ${identity.name ?? selector}`, locator, target => target.click({ timeout: 5000 }));
};
const selectNative = async (page, selector, value) => {
  const locator = page.locator(selector);
  await performAction(report, `select ${selector}`, locator, target => target.selectOption(value, { timeout: 5000 }));
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
let browserRequestCapture;
const syncAuthoringRequests = () => {
  const endpoints = new Set(['construction-capabilities', 'construction-category-discoveries', 'construction-proposals', 'commands', 'reconcile', 'preview']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => ({ ...request, pathname: request.path, url: `${request.origin}${request.path}`, requestStartedAtMs: request.startedAt,
      responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
      requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
      requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
      responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
      responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
      responseRowCount: request.response?.rowCount ?? request.response?.preview?.rowCount ?? request.response?.rows?.length,
      ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
    })));
  report.browserErrors.runtime = report.errors.filter(error => error.kind === 'runtime');
  report.browserErrors.console = report.errors.filter(error => error.kind === 'console');
  const ownedFailures = report.nativeRequests.filter(request => request.failure).map(request => ({
    pathname: request.path,
    errorText: request.failure,
    canceled: request.failure === 'net::ERR_ABORTED',
  }));
  const allFailures = (browser?.diagnostics.networkFailures ?? []).map(failure => ({
    pathname: new URL(failure.url, apiOrigin).pathname,
    errorText: failure.failure,
    canceled: failure.failure === 'net::ERR_ABORTED',
  }));
  report.browserErrors.network = [...ownedFailures, ...allFailures.filter(failure => !ownedFailures.some(owned => owned.pathname === failure.pathname && owned.errorText === failure.errorText))];
};
await mkdir(evidence, { recursive: true });
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
let apiBuildFreeze;
let browser;
let builder;
let outputId;
let fixtureTarget;
let fixtureDigest;
const parseJSON = value => { try { return JSON.parse(value); } catch { return value; } };
const drainResponseReads = async () => { await browserRequestCapture?.flush(); syncAuthoringRequests(); };

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `root-quantity-category-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};

const runRawCategoryOracle = generation => {
  const query = `FOR o IN Observation FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)} LET quantity = o.payload.valueQuantity LET present = IS_OBJECT(quantity) ? HAS(quantity, "code") : false LET value = o.payload.valueQuantity.code COLLECT categoryPresent = present, categoryValue = value WITH COUNT INTO rowCount SORT categoryPresent ASC, TYPENAME(categoryValue), categoryValue RETURN {present: categoryPresent, value: categoryValue, rowCount}`;
  const program = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', process.env.LOOM_ARANGO_DATABASE,
    '--javascript.execute-string', program,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const payloadLine = result.stdout.split(/\r?\n/).findLast(line => line.trimStart().startsWith('['));
  assert(payloadLine, `Raw root category query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const categories = JSON.parse(payloadLine);
  assert(categories.length > 0, 'The scoped Observation collection must have category values');
  for (const category of categories) {
    assert.equal(typeof category.present, 'boolean', `Raw category presence must be boolean: ${JSON.stringify(category)}`);
    if (!category.present) {
      assert.equal(category.value, null, `An absent code path must project to AQL null while retaining present=false: ${JSON.stringify(category)}`);
    } else {
      assert(category.value === null || typeof category.value === 'string', `Unexpected present code category type: ${JSON.stringify(category)}`);
    }
    assert(Number.isInteger(category.rowCount) && category.rowCount > 0, `Raw category row count must be positive: ${JSON.stringify(category)}`);
  }
  const missingRows = categories.filter(category => !category.present).reduce((sum, category) => sum + category.rowCount, 0);
  const explicitNullRows = categories.filter(category => category.present && category.value === null).reduce((sum, category) => sum + category.rowCount, 0);
  const presentStringRows = categories.filter(category => category.present && typeof category.value === 'string').reduce((sum, category) => sum + category.rowCount, 0);
  return {
    query,
    scope: { project, generation, collection: 'Observation', authorization: 'local Compose no-auth / unrestricted' },
    sourceRows: categories.reduce((sum, category) => sum + category.rowCount, 0),
    missingRows,
    explicitNullRows,
    presentStringRows,
    categories,
    categoryIdentities: categories.map(category => !category.present
      ? 'MISSING'
      : category.value === null
        ? 'NULL'
        : JSON.stringify({ kind: 'STRING', string: category.value })).sort(),
  };
};

const categoryIdentity = (present, value) => !present
  ? 'MISSING'
  : value === null
    ? 'NULL'
    : JSON.stringify({ kind: 'STRING', string: value });

const runFixtureOracle = async generation => {
  const fixtureContents = await readFile(join(fixtureDirectory, 'Observation.ndjson'), 'utf8');
  const fixtureObservations = fixtureContents.split(/\r?\n/).filter(Boolean).map((line, index) => {
    try { return JSON.parse(line); }
    catch (error) { throw new Error(`Quantity Pivot fixture Observation line ${index + 1} is invalid JSON: ${error.message}`); }
  });
  assert.equal(fixtureObservations.length, 4, 'The bounded fixture must contain the four declared quantity category witnesses');
  const fixtureRows = fixtureObservations.map(observation => {
    assert.equal(observation.resourceType, 'Observation');
    assert.equal(typeof observation.id, 'string');
    assert.equal(typeof observation.status, 'string', `Fixture ${observation.id} must retain its required status group value`);
    const quantity = observation.valueQuantity;
    assert(quantity && typeof quantity === 'object' && !Array.isArray(quantity), `Fixture ${observation.id} must have an ordinary valueQuantity object`);
    assert(Number.isFinite(quantity.value), `Fixture ${observation.id} must have a numeric quantity value`);
    const codePresent = Object.hasOwn(quantity, 'code');
    const codeValue = codePresent ? quantity.code : null;
    assert(!codePresent || codeValue === null || typeof codeValue === 'string', `Fixture ${observation.id} category code must be absent, explicit null, or a string`);
    return { id: observation.id, status: observation.status, codePresent, codeValue, value: quantity.value, category: categoryIdentity(codePresent, codeValue) };
  }).sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(fixtureRows.map(row => row.category).sort(), ['MISSING', 'NULL', JSON.stringify({ kind: 'STRING', string: 'd' }), JSON.stringify({ kind: 'STRING', string: 'd' })].sort(),
    'Fixture must contain one absent code, one explicit-null code, and two string d codes');

  const observedFixtureDigest = fixtureSourceDigest(fixtureDirectory);
  assert.equal(observedFixtureDigest, fixtureDigest, 'The independent raw fixture source must remain unchanged after its initial freeze');
  const query = `FOR o IN Observation FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)} LET quantity = o.payload.valueQuantity LET codePresent = IS_OBJECT(quantity) ? HAS(quantity, "code") : false LET valuePresent = IS_OBJECT(quantity) ? HAS(quantity, "value") : false SORT o.id RETURN {id: o.id, status: o.payload.status, codePresent, codeValue: codePresent ? quantity.code : null, valuePresent, value: valuePresent ? quantity.value : null}`;
  const program = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', process.env.LOOM_ARANGO_DATABASE,
    '--javascript.execute-string', program,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const payloadLine = result.stdout.split(/\r?\n/).findLast(line => line.trimStart().startsWith('['));
  assert(payloadLine, `Raw fixture query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const databaseRows = JSON.parse(payloadLine).map(row => ({
    id: row.id,
    status: row.status,
    codePresent: row.codePresent,
    codeValue: row.codeValue,
    valuePresent: row.valuePresent,
    value: row.value,
  }));
  const expectedDatabaseRows = fixtureRows.map(row => ({
    id: row.id,
    status: row.status,
    codePresent: row.codePresent,
    codeValue: row.codeValue,
    valuePresent: true,
    value: row.value,
  }));
  assert.deepEqual(databaseRows, expectedDatabaseRows, 'Raw scoped Arango rows must exactly match the independent fixture IDs, quantity values, and code presence');
  const categories = new Map();
  for (const row of fixtureRows) {
    const key = row.category;
    const entry = categories.get(key) ?? { present: row.codePresent, value: row.codeValue, rowCount: 0 };
    entry.rowCount += 1;
    categories.set(key, entry);
  }
  const categoryRows = [...categories.values()].sort((left, right) => categoryIdentity(left.present, left.value).localeCompare(categoryIdentity(right.present, right.value)));
  return {
    query,
    fixtureDigest: observedFixtureDigest,
    scope: { project, generation, collection: 'Observation', authorization: 'owned isolated dev verification project' },
    sourceRows: fixtureRows.length,
    missingRows: fixtureRows.filter(row => !row.codePresent).length,
    explicitNullRows: fixtureRows.filter(row => row.codePresent && row.codeValue === null).length,
    presentStringRows: fixtureRows.filter(row => row.codePresent && typeof row.codeValue === 'string').length,
    categories: categoryRows,
    categoryIdentities: categoryRows.map(row => categoryIdentity(row.present, row.value)).sort(),
    fixtureRows,
    databaseRows,
  };
};

const createOwnedFixtureProject = async () => {
  const session = createDevSession({ ...process.env, LOOM_DEV_FIXTURE_DIR: fixtureDirectory }, sourceRoot);
  await assertOwnedDevSession(session);
  const runID = `${Date.now().toString(36)}-${randomUUID().slice(0, 8)}`;
  const created = await createFreshVerificationFixture(session, runID);
  project = created.target.fixtureProject;
  expectedGeneration = created.target.fixtureGeneration;
  apiOrigin = created.target.apiUrl;
  uiOrigin = created.target.uiUrl;
  root = `/api/v1/projects/${project}/explorers`;
  base = `${root}/${explorer}/authoring/v2`;
  report.project = project;
  report.expectedGeneration = expectedGeneration;
  report.ownedFixtureProject = {
    project,
    generation: expectedGeneration,
    fixtureDirectory,
    fixtureSeed: created.seed,
    createdFresh: created.seed.fresh === true && created.seed.reused === false,
  };
  assert(report.ownedFixtureProject.createdFresh, 'Bounded Pivot lifecycle must use a fresh runner-owned verification project');
};

const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(base + '/builder');
};

const selectPivotSource = async (label, path) => {
  const selector = `select[aria-label=${JSON.stringify(label)}]`;
  const options = await inspectPage(browser.page, selector => [...document.querySelector(selector).options].map(option=>({label:option.textContent,value:option.value})), selector);
  const matches = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
  assert.equal(matches.length, 1, `Expected one exact root source option for ${path}: ${JSON.stringify(options)}`);
  await selectNative(browser.page, selector, matches[0].value);
  return matches[0].value;
};

const measure = (name, startedAt) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};

const pivotIdentity = key => key?.kind === 'MISSING'
  ? 'MISSING'
  : key?.kind === 'NULL'
    ? 'NULL'
    : JSON.stringify(key);

const waitForProposal = async (requestOffset, previousProposalId, name) => {
  await waitForObservable(browser.page, ({ previousProposalId }) => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalStatus==='ready'&&Boolean(panel.dataset.proposalId)&&panel.dataset.proposalId!==previousProposalId;
  }, { previousProposalId: previousProposalId ?? '' }, 15000);
  await drainResponseReads();
  const request = report.authoringRequests.slice(requestOffset).findLast(entry => entry.endpoint === 'construction-proposals');
  assert(request, `${name} must issue a native construction proposal`);
  assert.equal(request.status, 200, `${name} proposal failed: ${JSON.stringify(request.response)}`);
  assert.equal(request.responseReadError, undefined, `${name} proposal response must be readable`);
  assert(request.pathname.includes(`/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`), `${name} must remain in the same owned project and Explorer`);
  assert.equal(request.body?.outputId, outputId, `${name} must target the exact root Observation output`);
  assert.equal(request.body?.snapshotToken, builder.catalog.snapshotToken, `${name} must use the current fixture snapshot`);
  assert.equal(request.body?.expectedDraftVersion, builder.draftVersion, `${name} must use the current saved draft version`);
  assert.equal(request.body?.expectedDraftDigest, builder.draftDigest, `${name} must use the current saved draft digest`);
  assert.equal(request.response?.snapshotToken, builder.catalog.snapshotToken, `${name} response must retain the current fixture snapshot`);
  assert.equal(request.response?.outputId, outputId, `${name} response must retain the exact root output`);
  const panel = await inspectPage(browser.page, () => {
const element=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:element?.dataset.proposalStatus,proposalId:element?.dataset.proposalId,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};
});
  assert.equal(panel.status, 'ready');
  assert.equal(panel.proposalId, request.response.proposalId, `${name} DOM must show the exact returned proposal receipt`);
  return { request, panel };
};

const expectedPivot = (request, oracle, duplicatePolicy, labelOverrides = {}) => {
  const response = request.response;
  const step = response?.candidateConstruction?.steps?.find(candidate => candidate.operation?.kind === 'PIVOT');
  assert(step, 'Native candidate must contain a Pivot construction step');
  const operation = step.operation.pivot;
  assert.equal(operation.duplicatePolicy, duplicatePolicy);
  const outputsByID = new Map(step.outputs.map(output => [output.id, output]));
  const groups = operation.groupKeyIds.map(id => outputsByID.get(id));
  assert.equal(groups.length, 1, 'Fixture Pivot must retain one status group key');
  const categories = operation.categories.map(category => ({
    key: category.key,
    output: outputsByID.get(category.outputColumnId),
  }));
  assert(categories.every(category => category.output), 'Every category must have a typed Pivot output');
  assert.deepEqual(categories.map(category => pivotIdentity(category.key)).sort(), [...new Set(oracle.categoryIdentities)].sort(),
    'Pivot output categories must preserve MISSING, explicit NULL, and the string category identities');
  const categoryByIdentity = new Map(categories.map(category => [pivotIdentity(category.key), category.output]));
  const aggregates = new Map();
  for (const row of oracle.fixtureRows) {
    const key = row.category;
    const output = categoryByIdentity.get(key);
    assert(output, `Fixture record ${row.id} has no exact typed Pivot output for ${key}`);
    const current = aggregates.get(output.name);
    if (duplicatePolicy === 'SUM') aggregates.set(output.name, (current ?? 0) + row.value);
    else if (duplicatePolicy === 'MAX') aggregates.set(output.name, current === undefined ? row.value : Math.max(current, row.value));
    else assert.fail(`Fixture lifecycle oracle does not implement unexpected duplicate policy ${duplicatePolicy}`);
  }
  const preview = response.preview;
  assert(preview, 'Native construction proposal must include its typed table preview');
  assert.equal(preview.receiptId, response.proposalId, 'Preview must be bound to the exact proposal receipt');
  assert.equal(preview.outputId, outputId);
  assert.equal(preview.rowCount, 1, 'All four fixture Observations share the final status group');
  assert.equal(preview.rows.length, 1);
  const groupOutput = groups[0];
  const columns = preview.columns.map(column => ({ column: column.column, label: labelOverrides[column.column] ?? column.label, logicalType: column.logicalType }));
  const expectedRow = Object.fromEntries(columns.map(column => {
    if (column.column === groupOutput.name) return [column.column, 'final'];
    const category = categories.find(candidate => candidate.output.name === column.column);
    assert(category, `Unexpected Preview column ${column.column}`);
    return [column.column, aggregates.get(column.column) ?? null];
  }));
  const actualRow = Object.fromEntries(columns.map(column => [column.column, preview.rows[0][column.column] ?? null]));
  assert.deepEqual(actualRow, expectedRow, `Pivot ${duplicatePolicy} output must match the independent fixture values`);
  const identities = preview.rows.map(row => row.__loom_row_id);
  assert(identities.every(id => typeof id === 'string' && id.length > 0), 'Pivot preview must return a stable row identity');
  assert.equal(new Set(identities).size, identities.length);
  return { step, operation, preview, columns, expectedRows: [expectedRow], categories, outputByIdentity: categoryByIdentity };
};

const assertRendered = async (columns, rows, label) => {
  await waitForObservable(browser.page, ({ rowCount }) =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===String(rowCount + 1)&&!document.body.innerText.includes('Loading your table…'),
    { rowCount: rows.length }, 10000);
  const actual = await inspectPage(browser.page, () => {
const root=document.querySelector('[data-testid="preview-table-scroll"]');return {headers:[...root.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim().split('\\n')[0]),rows:[...root.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim()))};
});
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), columns.map(column => column.label.toLowerCase()), `${label} headers must match the native output labels`);
  const expectedCells = rows.map(row => columns.map(column => row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${label} rendered values must match the typed preview and raw oracle`);
  return actual;
};

const assertProposalPanel = async (pivot, label) => {
  await waitForObservable(browser.page, ({ rowCount }) => document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===rowCount, { rowCount: pivot.expectedRows.length }, 10000);
  const actual = await inspectPage(browser.page, () => {
return {headers:[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].map(cell=>cell.innerText.trim().split('\\n')[0]),rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};
});
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), pivot.columns.map(column => column.label.toLowerCase()), `${label} proposal headers must show the native category labels`);
  const expectedCells = pivot.expectedRows.map(row => pivot.columns.map(column => row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${label} proposal values must match independent aggregation`);
};

const openPivotEditor = async () => {
  await clickNative(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled===false));
  await clickNative(browser.page, '[data-testid="construction-action-table-pivot-rows"]');
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))));
};

const setPivotDuplicatePolicy = async (policy, requestOffset, name) => {
  const startedAt = Date.now();
  await clickNative(browser.page, '[data-testid="construction-reshape-pivot-advanced"] > summary');
  if (policy === 'SUM') {
    await waitForObservable(browser.page,
      () => Boolean((()=>{const alert=document.querySelector('[data-testid="construction-proposal-error"]');const select=document.querySelector('select[aria-label="Pivot duplicate policy"]');return Boolean(alert)&&select?.value==='ERROR'})()),
      5000);
    await drainResponseReads();
    const failures = report.authoringRequests.slice(requestOffset)
      .filter(request => request.endpoint === 'construction-proposals' && request.status >= 400);
    assert.equal(failures.length, 1, `${name} must have exactly one preceding validation rejection before SUM: ${JSON.stringify(failures.map(request => ({ status: request.status, code: request.response?.error?.code })))}`);
    const rejection = failures[0];
    const alert = await inspectPage(browser.page, () => {
const panel=document.querySelector('[data-testid="construction-proposal-error"]');const select=document.querySelector('select[aria-label="Pivot duplicate policy"]');const summary=document.querySelector('[data-testid="construction-reshape-pivot-policy-summary"]');const sum=[...select.options].find(option=>option.value==='SUM');return {alert:panel?.innerText.trim(),policy:select?.value,summary:summary?.innerText.trim(),sumOption:sum?{label:sum.textContent.trim(),disabled:sum.disabled}:null};
});
    const duplicateCode = JSON.stringify({ kind: 'STRING', string: 'd' });
    const duplicateWitnesses = report.oracle.fixtureRows.filter(row => row.category === duplicateCode);
    assert.equal(duplicateWitnesses.length, 2, `${name} ERROR policy must be justified by two independent raw d witnesses`);
    assert(duplicateWitnesses.every(row => row.status === duplicateWitnesses[0].status), `${name} raw d witnesses must share the selected status group`);
    assert.equal(duplicateWitnesses[0].status, 'final');
    assert.equal(rejection.status, 422, `${name} expected cardinality validation must use HTTP 422`);
    assert.equal(rejection.response?.error?.code, 'TABLE_PIVOT_CELL_CARDINALITY', `${name} must report the exact Pivot cell-cardinality validation code`);
    assert.equal(rejection.response?.error?.diagnostic?.code, 'TABLE_PIVOT_CELL_CARDINALITY');
    assert.deepEqual(rejection.response?.diagnostics?.map(diagnostic => diagnostic.code), ['TABLE_PIVOT_CELL_CARDINALITY'], `${name} must report only the expected cardinality diagnostic`);
    assert.equal(rejection.response?.error?.diagnostic?.stage, 'preview');
    assert.equal(rejection.response?.error?.diagnostic?.severity, 'error');
    assert.equal(rejection.pathname, `${base}/construction-proposals`, `${name} validation must come from the exact owned project and Explorer route`);
    assert.equal(rejection.body?.outputId, outputId, `${name} validation must be scoped to the exact root output`);
    assert.equal(rejection.body?.snapshotToken, builder.catalog.snapshotToken, `${name} validation must use the active fixture snapshot`);
    assert.equal(rejection.body?.expectedDraftVersion, builder.draftVersion, `${name} validation must use the current saved draft version`);
    assert.equal(rejection.body?.expectedDraftDigest, builder.draftDigest, `${name} validation must use the current saved draft digest`);
    const pivotSteps = rejection.body?.candidateConstruction?.steps?.filter(step => step.operation?.kind === 'PIVOT') ?? [];
    assert.equal(pivotSteps.length, 1, `${name} rejected candidate must contain exactly one Pivot`);
    const rejectedPivot = pivotSteps[0].operation.pivot;
    assert.equal(rejectedPivot.duplicatePolicy, 'ERROR', `${name} rejection must be caused by the ERROR duplicate policy`);
    assert.deepEqual(rejectedPivot.categories.map(category => pivotIdentity(category.key)).sort(), [...new Set(report.oracle.categoryIdentities)].sort(), `${name} rejected Pivot must cover the raw typed categories`);
    const boundColumnIDs = rejection.body.pivotSources?.map(source => source.columnId) ?? [];
    const operationColumnIDs = [...rejectedPivot.groupKeyIds, rejectedPivot.categoryColumnId, rejectedPivot.valueColumnId];
    assert.equal(boundColumnIDs.length, 3, `${name} rejection must retain the exact group/category/value capability bindings`);
    assert.deepEqual([...boundColumnIDs].sort(), [...operationColumnIDs].sort(), `${name} rejection source binding IDs must exactly match its Pivot operation`);
    assert.equal(alert.policy, 'ERROR');
    assert.equal(alert.alert, rejection.response?.error?.message, `${name} UI must explain the exact validation the API returned`);
    assert.match(alert.alert, /more than one record matched a pivot cell.*choose how to handle duplicates/i, `${name} UI must explain the duplicate-cell repair`);
    assert.match(alert.summary, /duplicate values: stop the pivot with an error/i, `${name} policy summary must make the active ERROR behavior understandable`);
    assert.deepEqual(alert.sumOption, { label: 'Add them together', disabled: false }, `${name} UI must offer SUM as an available repair for the numeric quantity values`);
    report.expectedAuthoringValidations ??= [];
    report.expectedAuthoringValidations.push({
      requestId: rejection.requestId,
      backendRequestId: rejection.response?.error?.requestId,
      code: rejection.response?.error?.code,
      duplicatePolicy: rejectedPivot.duplicatePolicy,
      project,
      explorer,
      outputId,
      snapshotToken: rejection.body.snapshotToken,
      draftVersion: rejection.body.expectedDraftVersion,
      rawDuplicateWitnessIDs: duplicateWitnesses.map(row => row.id),
      visibleRepair: alert,
      classification: 'expected-domain-validation-repaired-by-user-selected-SUM',
    });
  }
  const prior = await inspectPage(browser.page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
  await selectNative(browser.page, 'select[aria-label="Pivot duplicate policy"]', policy);
  const result = await waitForProposal(requestOffset, prior, name);
  measure(`${name} automatic preview action to render`, startedAt);
  return result;
};

const sourceColumnDefinition = column => {
  const { columnId, ...definition } = column;
  return definition;
};

const assertSourceBindingsRestored = (before, after, label) => {
  assert.equal(after.length, before.length, `${label} must restore the exact number of base source fields`);
  const afterByColumn = new Map(after.map(column => [column.column, column]));
  assert.equal(afterByColumn.size, after.length, `${label} must keep unique source output columns`);
  for (const original of before) {
    const restored = afterByColumn.get(original.column);
    assert(restored, `${label} omitted original source output ${original.column}`);
    assert.deepEqual(sourceColumnDefinition(restored), sourceColumnDefinition(original), `${label} changed source binding ${original.label}`);
    if (original.columnId) assert.equal(restored.columnId, original.columnId, `${label} changed an existing exact source binding identity`);
  }
  const identities = after.map(column => column.columnId).filter(Boolean);
  assert.equal(new Set(identities).size, identities.length, `${label} must keep stable source column identities unique`);
  return identities;
};

const assertBuilderScope = (state, label) => {
  assert.equal(state.catalog?.generation, expectedGeneration, `${label} must remain in the exact fixture generation`);
  const document = state.workspace.documents.find(item => item.output.id === outputId);
  assert(document, `${label} must retain the same root output membership`);
  return document;
};

const runFixtureLifecycle = async (discovery, oracle, prePivotWorkspace, prePivotDocument, discoveryRequestOffset) => {
  const sourceRecords = oracle.fixtureRows;
  const initial = await setPivotDuplicatePolicy('SUM', discoveryRequestOffset, 'Initial bounded Pivot preview');
  const initialPivot = expectedPivot(initial.request, oracle, 'SUM');
  await assertProposalPanel(initialPivot, 'Initial bounded Pivot preview');
  report.fixtureLifecycle = {
    discoveryOutcome: discovery.response.outcome,
    typedCategories: discovery.response.categories.map(category => category.key),
    sourceIDs: sourceRecords.map(row => row.id),
    initialDuplicatePolicy: 'SUM',
    initialExpected: initialPivot.expectedRows,
  };

  const commandCount = report.authoringRequests.filter(entry => entry.endpoint === 'commands').length;
  const cancelStarted = Date.now();
  await clickNative(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForObservable(browser.page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), 5000);
  builder = await api(base + '/builder');
  assertBuilderScope(builder, 'After Cancel');
  assert.deepEqual(builder.workspace, prePivotWorkspace, 'Cancel must preserve the pre-Pivot saved workspace');
  assert.equal(report.authoringRequests.filter(entry => entry.endpoint === 'commands').length, commandCount, 'Cancel must not issue a native draft command');
  measure('quantity Pivot preview cancel', cancelStarted);

  await openPivotEditor();
  await selectPivotSource('Add pivot group field', 'Observation.status');
  await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const requestOffset = report.authoringRequests.length;
  await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))&&!document.querySelector('[data-testid="construction-reshape-pivot"]')?.innerText.includes('Finding categories')), 10000);
  const applyPreview = await setPivotDuplicatePolicy('SUM', requestOffset, 'Reopened bounded Pivot preview');
  const applyPivot = expectedPivot(applyPreview.request, oracle, 'SUM');
  await assertProposalPanel(applyPivot, 'Reopened bounded Pivot preview');
  const applyStarted = Date.now();
  await clickNative(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(browser.page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1), 10000);
  await assertRendered(applyPivot.columns, applyPivot.expectedRows, 'Applied quantity Pivot');
  measure('quantity Pivot Apply to render', applyStarted);
  builder = await api(base + '/builder');
  let document = assertBuilderScope(builder, 'After initial Pivot Apply');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Pivot Apply must retain the same root Observation population definition');
  let savedPivot = document.construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(savedPivot, 'Applied bounded category Pivot must be present in the saved construction');
  assert.equal(savedPivot.operation.pivot.duplicatePolicy, 'SUM');
  const savedSourceBindings = {
    groupKeyIds: savedPivot.operation.pivot.groupKeyIds,
    categoryColumnId: savedPivot.operation.pivot.categoryColumnId,
    valueColumnId: savedPivot.operation.pivot.valueColumnId,
  };
  const assertSavedPivotScope = (state, expectedPolicy, expectedHeading, label) => {
    const scopedDocument = assertBuilderScope(state, label);
    assert.deepEqual(scopedDocument.rows, prePivotDocument.rows, `${label} must preserve the exact root Observation population definition`);
    const scopedPivot = scopedDocument.construction.steps.find(step => step.operation.kind === 'PIVOT');
    assert(scopedPivot, `${label} must retain the saved quantity Pivot`);
    assert.equal(scopedPivot.operation.pivot.duplicatePolicy, expectedPolicy, `${label} must retain the accepted duplicate policy`);
    assert.deepEqual(scopedPivot.operation.pivot.groupKeyIds, savedSourceBindings.groupKeyIds, `${label} must retain the exact group source identity`);
    assert.equal(scopedPivot.operation.pivot.categoryColumnId, savedSourceBindings.categoryColumnId, `${label} must retain the exact category source identity`);
    assert.equal(scopedPivot.operation.pivot.valueColumnId, savedSourceBindings.valueColumnId, `${label} must retain the exact value source identity`);
    if (expectedHeading) {
      const heading = scopedPivot.outputs.find(output => output.name === expectedHeading.column);
      assert.equal(heading?.label, expectedHeading.label, `${label} must retain the accepted category heading`);
    }
    return scopedDocument;
  };
  assert.deepEqual(savedSourceBindings.groupKeyIds, applyPivot.operation.groupKeyIds, 'Saved group source identity must match the accepted proposal binding');
  assert.equal(savedSourceBindings.categoryColumnId, applyPivot.operation.categoryColumnId, 'Saved category binding must match the accepted proposal source');
  assert.equal(savedSourceBindings.valueColumnId, applyPivot.operation.valueColumnId, 'Saved value binding must match the accepted proposal source');

  const reloadStarted = Date.now();
  await gotoPage(browser.page, `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitForObservable(browser.page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 10000);
  await clickNative(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 10000);
  await assertRendered(applyPivot.columns, applyPivot.expectedRows, 'Reloaded quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedPivotScope(builder, 'SUM', undefined, 'After quantity Pivot reload');
  measure('quantity Pivot reload to render', reloadStarted);

  const history = await inspectPage(browser.page, () => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
  const pivotHistory = history.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(pivotHistory, 'Reloaded construction history must expose the saved quantity Pivot');
  await clickNative(browser.page, `[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))), 5000);
  await clickNative(browser.page, '[data-testid^="construction-edit-step-"]');
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))), 5000);
  const editorBefore = await inspectPage(browser.page, () => {
const root=document.querySelector('[data-testid="construction-reshape-pivot"]');return {policy:root.querySelector('select[aria-label="Pivot duplicate policy"]')?.value,groups:[...root.querySelectorAll('input[aria-label^="Pivot group"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),category:root.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:root.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent,labels:[...root.querySelectorAll('input[aria-label^="Pivot output label "]')].map(input=>({ariaLabel:input.getAttribute('aria-label'),value:input.value}))};
});
  assert.equal(editorBefore.policy, 'SUM', 'Saved Pivot edit must restore SUM duplicate policy');
  assert(editorBefore.groups.some(label => label.includes('Observation.status')) || editorBefore.groups.some(label => /Observation status/i.test(label)), `Saved Pivot edit must retain its exact status grouping: ${JSON.stringify(editorBefore.groups)}`);
  assert(editorBefore.category?.includes('Observation.valueQuantity.code'));
  assert(editorBefore.value?.includes('Observation.valueQuantity.value'));
  const dLabel = editorBefore.labels.find(item => item.value === 'd');
  assert(dLabel, `Saved Pivot edit must expose the d output label: ${JSON.stringify(editorBefore.labels)}`);
  const editRequestOffset = report.authoringRequests.length;
  const priorEditProposal = await inspectPage(browser.page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
  const editStarted = Date.now();
  await clickNative(browser.page, '[data-testid="construction-reshape-pivot-advanced"] > summary');
  await selectNative(browser.page, 'select[aria-label="Pivot duplicate policy"]', 'MAX');
  const categoryLabel = browser.page.getByLabel('Pivot output label d', { exact: true });
  await performAction(report, 'fill Pivot category label d', categoryLabel, locator => locator.fill('d maximum', { timeout: 5000 }), { editable: true });
  await waitForObservable(browser.page, ({ previousProposalId }) => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalStatus==='ready'&&panel.dataset.proposalId!==previousProposalId&&[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].some(cell=>cell.innerText.includes('d maximum'));
  }, { previousProposalId: priorEditProposal }, 15000);
  await drainResponseReads();
  const editRequest = report.authoringRequests.slice(editRequestOffset).findLast(entry => entry.endpoint === 'construction-proposals');
  const editPivot = expectedPivot(editRequest, oracle, 'MAX', {
    [applyPivot.categories.find(category => pivotIdentity(category.key) === JSON.stringify({ kind: 'STRING', string: 'd' })).output.name]: 'd maximum',
  });
  assert.deepEqual(editPivot.expectedRows[0], Object.fromEntries(editPivot.columns.map(column => [column.column,
    column.label === 'd maximum' ? 4 : applyPivot.expectedRows[0][column.column]])), 'MAX must change the duplicate d bucket from SUM 6 to MAX 4 while retaining other typed categories');
  await assertProposalPanel(editPivot, 'Edited bounded Pivot preview');
  measure('quantity Pivot edit preview', editStarted);
  const editApplyStarted = Date.now();
  await clickNative(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(browser.page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1), 10000);
  await assertRendered(editPivot.columns, editPivot.expectedRows, 'Edited quantity Pivot');
  measure('quantity Pivot edit Apply to render', editApplyStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After edited Pivot Apply');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Editing Pivot must preserve the exact root Observation population definition');
  savedPivot = document.construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert.equal(savedPivot.operation.pivot.duplicatePolicy, 'MAX');
  assert.deepEqual(savedPivot.operation.pivot.groupKeyIds, savedSourceBindings.groupKeyIds, 'Edit must retain the original group source identity');
  assert.equal(savedPivot.operation.pivot.categoryColumnId, savedSourceBindings.categoryColumnId, 'Edit must retain the original category source identity');
  assert.equal(savedPivot.operation.pivot.valueColumnId, savedSourceBindings.valueColumnId, 'Edit must retain the original value source identity');
  const editedLabel = savedPivot.outputs.find(output => output.name === editPivot.categories.find(category => pivotIdentity(category.key) === JSON.stringify({ kind: 'STRING', string: 'd' })).output.name)?.label;
  assert.equal(editedLabel, 'd maximum', 'Accepted Pivot edit must save the changed d heading');

  const editReloadStarted = Date.now();
  await gotoPage(browser.page, `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitForObservable(browser.page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 10000);
  await clickNative(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 10000);
  await assertRendered(editPivot.columns, editPivot.expectedRows, 'Reloaded edited quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedPivotScope(builder, 'MAX', {
    column: editPivot.categories.find(category => pivotIdentity(category.key) === JSON.stringify({ kind: 'STRING', string: 'd' })).output.name,
    label: 'd maximum',
  }, 'After edited quantity Pivot reload');
  measure('quantity Pivot edited reload to render', editReloadStarted);

  const removeHistory = await inspectPage(browser.page, () => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
  const removeStep = removeHistory.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(removeStep, 'Edited Pivot history must remain available for removal');
  await clickNative(browser.page, `[data-testid=${JSON.stringify(removeStep.testId)}]`);
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))), 5000);
  const removeRequestOffset = report.authoringRequests.length;
  const removeStarted = Date.now();
  await clickNative(browser.page, '[data-testid^="construction-remove-step-"]');
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'), 10000);
  await drainResponseReads();
  const removeRequest = report.authoringRequests.slice(removeRequestOffset).findLast(entry => entry.endpoint === 'construction-proposals');
  assert.equal(removeRequest?.status, 200, 'Removing the saved quantity Pivot must produce a native proposal');
  const removePreview = removeRequest.response.preview;
  assert(removePreview, 'Pivot removal proposal must include source rows');
  assert.equal(removePreview.rowCount, sourceRecords.length);
  assert.equal(removePreview.rows.length, sourceRecords.length);
  const sourceColumns = removePreview.columns.map(column => ({ column: column.column, label: column.label, logicalType: column.logicalType }));
  const idColumn = sourceColumns.find(column => column.label === 'Observation ID');
  const categoryColumn = sourceColumns.find(column => column.label === 'Quantity Code');
  const valueColumn = sourceColumns.find(column => column.label === 'Quantity Value');
  assert(idColumn && categoryColumn && valueColumn, `Removal must restore the exact ordinary source fields: ${JSON.stringify(sourceColumns)}`);
  const expectedSourceRows = sourceRecords.map(record => ({
    [idColumn.column]: record.id,
    [categoryColumn.column]: record.codeValue,
    [valueColumn.column]: record.value,
  }));
  const actualSourceRows = removePreview.rows.map(row => Object.fromEntries(sourceColumns.map(column => [column.column, row[column.column] ?? null])));
  const sortJSON = rows => rows.map(row => JSON.stringify(sourceColumns.map(column => row[column.column]))).sort();
  assert.deepEqual(sortJSON(actualSourceRows), sortJSON(expectedSourceRows), 'Removing Pivot must restore every raw Observation ID and numeric value from the independent fixture oracle');
  await assertProposalPanel({ columns: sourceColumns, expectedRows: expectedSourceRows }, 'Quantity Pivot removal preview');
  measure('quantity Pivot removal preview', removeStarted);
  const removeApplyStarted = Date.now();
  await clickNative(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(browser.page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0), 10000);
  await assertRendered(sourceColumns, expectedSourceRows, 'Restored raw quantity source rows');
  measure('quantity Pivot removal Apply to render', removeApplyStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After Pivot removal Apply');
  assert.deepEqual(document.construction.steps, [], 'Pivot removal must restore the original root table definition');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Pivot removal must restore the exact root Observation population definition');
  const restoredColumnIDs = assertSourceBindingsRestored(prePivotDocument.columns, document.columns, 'Pivot removal');
  const finalReloadStarted = Date.now();
  await gotoPage(browser.page, `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
  await waitForObservable(browser.page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 10000);
  await clickNative(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 10000);
  await assertRendered(sourceColumns, expectedSourceRows, 'Reloaded restored raw quantity source rows');
  measure('quantity Pivot restoration reload to render', finalReloadStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After final restoration reload');
  assert.deepEqual(document.construction.steps, [], 'Final reload must retain the restored pre-Pivot definition');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Final reload must retain the original root Observation population definition');
  assert.deepEqual(assertSourceBindingsRestored(prePivotDocument.columns, document.columns, 'Final reload'), restoredColumnIDs,
    'Final reload must preserve the same exact restored source column identities');
  assert.equal(builder.catalog.generation, expectedGeneration);
  assert.deepEqual(oracle.fixtureRows.map(row => row.id).sort(), sourceRecords.map(row => row.id).sort(), 'Restored root membership must equal the independent raw fixture IDs');
  report.fixtureLifecycle.restoredSourceRows = sourceRecords.map(row => ({ id: row.id, category: row.category, value: row.value }));
  report.fixtureLifecycle.editedDuplicatePolicy = 'MAX';
  report.fixtureLifecycle.editedDValue = 4;
  report.fixtureLifecycle.lifecycle = ['Preview', 'Cancel', 'Apply', 'reload', 'edit heading/policy', 'Apply', 'reload', 'remove', 'restore', 'reload'];
};

try {
  if (mode === 'fixture-lifecycle') {
    fixtureDigest = fixtureSourceDigest(fixtureDirectory);
    await createOwnedFixtureProject();
  }
  root ??= `/api/v1/projects/${project}/explorers`;
  base ??= `${root}/${explorer}/authoring/v2`;
  apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(process.env.LOOM_CDA_API_CONTAINER));
  await api(root, { name: explorer, title: 'Root quantity category QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, expectedGeneration);
  const oracle = mode === 'full-population'
    ? runRawCategoryOracle(builder.catalog.generation)
    : await runFixtureOracle(builder.catalog.generation);
  report.oracle = oracle;

  const rootNode = builder.catalog.nodes.find(node => node.resourceType === resourceType);
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode?.nodeId && candidate.fieldPath === 'id');
  assert(rootNode && idField, 'The current catalog must expose the Observation root and its ID field');
  const quantityCodeField = mode === 'fixture-lifecycle'
    ? builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'valueQuantity.code')
    : undefined;
  const quantityValueField = mode === 'fixture-lifecycle'
    ? builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'valueQuantity.value')
    : undefined;
  if (mode === 'fixture-lifecycle') assert(quantityCodeField && quantityValueField, 'The bounded fixture catalog must expose ordinary valueQuantity.code and valueQuantity.value fields');
  await command([{ type: 'CREATE_TABLE', title: 'Root Observation quantity QA', rootNodeId: rootNode.nodeId }]);
  outputId = builder.workspace.documents[0]?.output.id;
  assert(outputId);
  const rootColumns = [{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID',
  }];
  if (mode === 'fixture-lifecycle') {
    rootColumns.push({ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: quantityCodeField.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Quantity Code' });
    rootColumns.push({ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: quantityValueField.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Quantity Value' });
  }
  for (const rootColumn of rootColumns) await command([rootColumn]);

  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  browserRequestCapture = captureCDARequests(browser.page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
    responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries|construction-capabilities)$/,
  });
  browser.page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });
  await gotoPage(browser.page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForObservable(browser.page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId);
  await clickNative(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false));
  await clickNative(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(browser.page, () => Boolean(document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled===false));
  const reshapeActions = await inspectPage(browser.page, () => {
const menu=document.querySelector('[aria-label="Choose a row change"]');return [...(menu?.querySelectorAll('button')??[])].map(button=>({testId:button.dataset.testid??'',label:button.innerText.trim().replace(/\s+/g,' ').slice(0,140),disabled:button.disabled}));
});
  report.reshapeActions = reshapeActions;
  const genericPivotAction = reshapeActions.find(action => action.testId === 'construction-action-table-pivot-rows' && !action.disabled);
  assert(genericPivotAction, 'Generic source Pivot must be enabled after capability discovery settles');
  const pivotActionTestId = 'construction-action-table-pivot-rows';
  report.pivotEntryControl = pivotActionTestId;
  await clickNative(browser.page, `[data-testid="${pivotActionTestId}"]`);
  await waitForObservable(browser.page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))));

  const prePivotWorkspace = builder.workspace;
  const prePivotDocument = builder.workspace.documents.find(document => document.output.id === outputId);
  assert(prePivotDocument, 'Fresh root fixture must retain its base Observation table before Pivot');
  const groupPath = mode === 'full-population' ? 'Observation.id' : 'Observation.status';
  const groupSourceValue = await selectPivotSource('Add pivot group field', groupPath);
  const categorySourceValue = await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const startedAt = Date.now();
  const requestOffset = report.authoringRequests.length;
  const valueSourceValue = await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForObservable(browser.page,
    () => Boolean((()=>{const editor=document.querySelector('[data-testid="construction-reshape-pivot"]');const text=editor?.innerText??'';return !text.includes('Finding categories')&&(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))||text.includes('Some records have no category field')||text.includes('more than 256 category values'))})()),
    15000);
  await drainResponseReads();
  const discovery = report.authoringRequests.slice(requestOffset).findLast(request => request.endpoint === 'construction-category-discoveries');
  const editorState = await inspectPage(browser.page, () => {
const editor=document.querySelector('[data-testid="construction-reshape-pivot"]');const prefix='Include category ';return {text:editor?.innerText??'',summary:document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.innerText,categoryControls:[...document.querySelectorAll('[data-testid="construction-reshape-pivot-categories"] input[aria-label^="Include category "]')].map(input=>({label:input.getAttribute('aria-label')?.slice(prefix.length),checked:input.checked})).filter(item=>item.label)};
});
  const durationMs = Date.now() - startedAt;
  const selectedChoiceId = value => typeof value === 'string' && value.startsWith('source:') ? value.slice('source:'.length) : undefined;
  const recordedBindings = discovery?.body?.pivotSources ?? [];
  const bindingFor = value => recordedBindings.find(binding => binding.choiceId === selectedChoiceId(value));
  const groupBinding = bindingFor(groupSourceValue);
  const categoryBinding = bindingFor(categorySourceValue);
  const valueBinding = bindingFor(valueSourceValue);
  const currentSteps = builder.workspace.documents.find(document => document.output.id === outputId)?.construction?.steps ?? [];
  report.cases.push({
    name: 'root-quantity-category-discovery-to-render',
    observedStatus: discovery?.status,
    durationMs,
    summary: editorState.summary,
    visibleMessage: editorState.text,
    rawSourceRows: oracle.sourceRows,
    rawCategoryCount: oracle.categories.length,
    missingRows: oracle.missingRows,
    explicitNullRows: oracle.explicitNullRows,
    discoveryOutcome: discovery?.response?.outcome,
    discoveryComplete: discovery?.response?.complete,
    discoveryMessage: discovery?.response?.message,
    discoveryDurationMs: discovery?.durationMs,
    prePivot: {
      savedStepCount: currentSteps.length,
      candidateStepCount: discovery?.body?.candidateConstruction?.steps?.length,
      pivotStepIdPresent: Boolean(discovery?.body?.pivotStepId),
      groupColumnId: groupBinding?.columnId,
      categoryColumnId: categoryBinding?.columnId,
      valueColumnId: valueBinding?.columnId,
      groupKeyIds: discovery?.body?.groupKeyIds,
      exactBindingsSelected: Boolean(groupBinding && categoryBinding && valueBinding),
    },
  });
  assert(discovery, 'Selecting root quantity fields must issue category discovery');
  assert.equal(discovery.status, 200, JSON.stringify(discovery.response));
  assert.equal(discovery.responseReadError, undefined, `Category discovery response body must be readable: ${discovery.responseReadError}`);
  assert.equal(discovery.loadingFailure, undefined, JSON.stringify(discovery.loadingFailure));
  assert.equal(discovery.body?.outputId, outputId);
  assert.equal(discovery.body?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(discovery.body?.expectedDraftVersion, builder.draftVersion);
  assert.equal(discovery.body?.expectedDraftDigest, builder.draftDigest);
  assert.equal(discovery.response?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(discovery.response?.draftVersion, builder.draftVersion);
  assert.equal(discovery.response?.draftDigest, builder.draftDigest);
  assert.equal(discovery.response?.outputId, outputId);
  assert.equal(discovery.response?.stageId, discovery.body?.stageId);
  const candidateConstruction = discovery.body?.candidateConstruction;
  assert(candidateConstruction, 'Root source Pivot discovery must carry its pre-Pivot construction');
  assert.equal(candidateConstruction.version, 1);
  assert.deepEqual(currentSteps, [], 'This root fixture must have no saved construction steps before Pivot');
  assert.deepEqual(candidateConstruction.steps, currentSteps, 'Discovery must receive the empty root pre-Pivot construction');
  assert.equal(typeof discovery.body?.pivotStepId, 'string');
  assert(discovery.body.pivotStepId.length > 0, 'The candidate Pivot identity must travel separately from pre-Pivot construction');
  const expectedChoiceIds = [groupSourceValue, categorySourceValue, valueSourceValue].map(selectedChoiceId);
  assert(expectedChoiceIds.every(Boolean), 'Each selected source binding must be an exact source-choice value');
  assert.equal(recordedBindings.length, 3, 'Discovery must receive exactly the selected root group/category/value bindings');
  assert.deepEqual(recordedBindings.map(binding => binding.choiceId).sort(), [...expectedChoiceIds].sort());
  assert(groupBinding && categoryBinding && valueBinding, 'All selected root source choices must map to binding IDs');
  assert.equal(new Set(recordedBindings.map(binding => binding.columnId)).size, 3, 'Root source binding column IDs must be distinct');
  assert.deepEqual(discovery.body.groupKeyIds, [groupBinding.columnId]);
  assert.equal(discovery.body.categoryColumnId, categoryBinding.columnId);
  assert.equal(discovery.body.valueColumnId, valueBinding.columnId);
  assert(groupBinding.columnId !== discovery.body.pivotStepId);
  assert(categoryBinding.columnId !== discovery.body.pivotStepId);
  assert(valueBinding.columnId !== discovery.body.pivotStepId);
  assert.deepEqual(report.browserErrors.runtime, [], `Browser runtime exceptions: ${JSON.stringify(report.browserErrors.runtime)}`);
  assert.deepEqual(report.browserErrors.console, [], `Browser console errors: ${JSON.stringify(report.browserErrors.console)}`);
  assert.deepEqual(report.browserErrors.network, [], `Unexpected browser network failures: ${JSON.stringify(report.browserErrors.network)}`);
  assert(durationMs <= 5000, `Root quantity category discovery took ${durationMs}ms`);
  report.rawCategoryPresence = {
    missingRows: oracle.missingRows,
    explicitNullRows: oracle.explicitNullRows,
    categories: oracle.categories,
    semantics: 'MISSING and explicit NULL remain distinct in the independent raw source oracle; the discovery outcome is checked separately without merging identities',
  };
  if (oracle.categories.length > 256) {
    assert.equal(discovery.response?.outcome, 'LIMIT_EXCEEDED', JSON.stringify(discovery.response));
    assert.equal(discovery.response?.complete, false);
    assert.deepEqual(discovery.response?.categories, []);
    assert(editorState.text.includes('more than 256 category values'), 'The UI must show the typed category limit response');
    report.categoryCorrectness = 'raw full scoped category domain exceeds the documented 256-value limit; API returned no partial set';
    report.status = 'failed';
    report.productFailure = 'The root category domain exceeds the supported Pivot category limit.';
    report.error = report.productFailure;
    process.exitCode = 1;
  } else if (discovery.response?.outcome === 'MISSING_UNSUPPORTED') {
    assert(oracle.missingRows > 0, 'MISSING_UNSUPPORTED is valid only when the full scoped raw oracle proves absent category fields');
    assert.equal(discovery.response.complete, false);
    assert.deepEqual(discovery.response.categories, []);
    assert(editorState.text.includes('Some records have no category field'), 'The UI must show the typed MISSING limitation');
    report.categoryCorrectness = 'raw oracle distinguishes MISSING from explicit NULL; API declared MISSING_UNSUPPORTED and exposed the limitation';
    report.status = 'failed';
    report.productFailure = 'Root Observation quantity Pivot cannot complete while any scoped source row lacks valueQuantity.code.';
    report.error = report.productFailure;
    process.exitCode = 1;
  } else if (discovery.response?.outcome === 'COMPLETE') {
    assert.equal(discovery.response.complete, true);
    const expected = oracle.categoryIdentities;
    const actual = discovery.response.categories.map(category => category.key.kind === 'MISSING'
      ? 'MISSING'
      : category.key.kind === 'NULL'
        ? 'NULL'
        : JSON.stringify(category.key)).sort();
    assert.deepEqual(actual, expected, 'Complete root categories must exactly match every scoped raw identity, keeping MISSING, NULL, and typed values distinct');
    const expectedLabels = discovery.response.categories.map(category => category.label).sort();
    assert(editorState.summary, 'Complete discovery must render the Pivot category summary');
    assert(editorState.summary.startsWith(`Selected ${expected.length} of ${expected.length} categories`), 'Visible category summary must show every typed category selected');
    assert.deepEqual(editorState.categoryControls.map(item => item.label).sort(), expectedLabels, 'Visible Pivot category controls must preserve every API category label');
    assert(editorState.categoryControls.every(item => item.checked), 'Every complete raw category identity must be selected in the initial Pivot form');
    report.categoryCorrectness = 'complete raw project/generation Observation oracle matched MISSING, NULL, and typed scalar categories';
    if (mode === 'fixture-lifecycle') {
      assert.equal(oracle.sourceRows, 4, 'Bounded lifecycle oracle must contain exactly four independent Observation records');
      assert.equal(oracle.missingRows, 1);
      assert.equal(oracle.explicitNullRows, 1);
      assert.equal(oracle.presentStringRows, 2);
      await runFixtureLifecycle(discovery, oracle, prePivotWorkspace, prePivotDocument, requestOffset);
      await drainResponseReads();
      assert.equal(report.expectedAuthoringValidations?.length, 2, 'Only the two explicit ERROR-policy previews should be classified as expected validation');
      assert.deepEqual(report.browserErrors.runtime, [], `Browser runtime exceptions after lifecycle: ${JSON.stringify(report.browserErrors.runtime)}`);
      assert.deepEqual(report.browserErrors.console, [], `Browser console errors after lifecycle: ${JSON.stringify(report.browserErrors.console)}`);
      assert.deepEqual(report.browserErrors.network, [], `Unexpected browser network failures after lifecycle: ${JSON.stringify(report.browserErrors.network)}`);
      const expectedValidationRequestIDs = new Set((report.expectedAuthoringValidations ?? []).map(validation => validation.requestId));
      const failedAuthoringRequests = report.authoringRequests.filter(request => typeof request.status === 'number' && request.status >= 400 && !expectedValidationRequestIDs.has(request.requestId));
      assert.deepEqual(failedAuthoringRequests, [], `Unexpected authoring request errors after lifecycle: ${JSON.stringify(failedAuthoringRequests)}`);
      report.fixtureLifecycle.finalErrors = { runtime: [], console: [], network: [], authoringHTTP: [] };
      report.status = 'passed';
    } else {
      report.status = 'passed';
    }
  } else {
    assert.fail(`Unexpected root category discovery outcome: ${JSON.stringify(discovery.response)}`);
  }
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  await browser?.captureFailure(error, { phase: report.activeAction?.label ?? report.cases.at(-1)?.name,
    elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : undefined,
    action: report.activeAction, requestEvidence: report.nativeRequests.slice(-20), latestCase: report.cases.at(-1) });
  if (browser) {
    try {
      report.failureDiagnostics = await inspectPage(browser.page, () => {
return {
        url: location.href,
        title: document.title,
        bodyText: (document.body?.innerText??'').slice(0,5000),
        actions: [...document.querySelectorAll('[data-testid^="construction-action-"]')].map(button=>({testId:button.dataset.testid??'',label:button.innerText.trim().replace(/\s+/g,' ').slice(0,140),disabled:button.disabled})),
        selects: [...document.querySelectorAll('select')].map(select=>({label:select.getAttribute('aria-label'),disabled:select.disabled,value:select.value,options:[...select.options].map(option=>({label:option.textContent,value:option.value})).slice(0,100)})),
        statusMessages: [...document.querySelectorAll('[role="status"],[role="alert"]')].map(node=>node.innerText.trim()).filter(Boolean).slice(0,60),
        editorKinds: {pivot:Boolean(document.querySelector('[data-testid="construction-reshape-pivot"]')),codedPivot:Boolean(document.querySelector('[data-testid="construction-coded-pivot-editor"],[data-testid="construction-reshape-coded-pivot"]'))}
      };
});
    } catch (diagnosticError) {
      report.failureDiagnosticsError = String(diagnosticError);
    }
  }
  process.exitCode = 1;
} finally {
  await drainResponseReads();
  if (mode === 'fixture-lifecycle' && fixtureDigest) {
    const afterDigest = fixtureSourceDigest(fixtureDirectory);
    report.fixtureSourceFreeze = {
      before: fixtureDigest,
      after: afterDigest,
      unchanged: fixtureDigest === afterDigest,
      invalidatesRun: fixtureDigest !== afterDigest,
      productFailure: false,
    };
    if (fixtureDigest !== afterDigest) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.error = 'The independent quantity Pivot fixture changed during the browser run.';
      process.exitCode = 1;
    }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = await sourceFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false };
      process.exitCode = 1;
    }
  }
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
      process.exitCode = 1;
    }
  }
  const afterFingerprint = sourceFingerprint(sourceRoot);
  report.sourceFingerprint.after = afterFingerprint;
  report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === afterFingerprint.sha256 && report.sourceFingerprint.before.files === afterFingerprint.files;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.error = 'Source fingerprint changed during the browser run.';
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases, error: report.error }));
