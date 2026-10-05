import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureCDARequests } from '../lib/cda-playwright-requests.mjs';
import { collectPreviewRows } from '../lib/playwright-preview-rows.mjs';
import { captureSourceFreeze } from '../lib/source-freeze.mjs';
import { assertBoundedPreviewCount, assertPreviewRowsMatchRawObservations, assertReloadPreviewContext } from '../lib/root-quantity-raw-preview.mjs';
import { captureValidationWaitFailure, refreshValidationWaitFailureRequests } from '../lib/validation-wait-evidence.mjs';
import { sourceFingerprint } from './source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from '../lib/api-build-freeze.mjs';
import { fixtureSourceDigest } from '../loom-dev.mjs';
import { browserURL } from './builder-url.mjs';

const requiredChecksByCase = {
  'full-population-discovery': [
    'independent raw Arango oracle covers the complete scoped CDA Observation population',
    'typed root category discovery preserves MISSING separately from explicit NULL and string categories',
    'native category summary and selection match every oracle category',
    'category discovery and render complete within the five-second budget',
    'watched source, API build, project, and generation remain stable',
  ],
  'fixture-lifecycle': [
    'fresh owned project contains the four exact raw Observation IDs and independent MISSING, NULL, and string d quantity-code states',
    'ERROR-policy preview reports only TABLE_PIVOT_CELL_CARDINALITY for the exact draft and offers visible SUM repair',
    'native SUM Pivot preview matches MISSING=3, NULL=5, and d=6',
    'Cancel leaves the saved pre-Pivot workspace unchanged and issues no command',
    'Apply and reload preserve the exact Pivot source bindings, generation, output, and source population',
    'editing duplicate policy to MAX and renaming d to d maximum produces d=4 and persists after reload',
    'removing Pivot restores the exact raw fixture IDs, code states, and numeric values after reload',
    'all fixture native lifecycle actions complete within five seconds each',
    'no unexpected browser/authoring errors occur and source, API build, and fixture fingerprints remain unchanged',
  ],
  'full-population-lifecycle': [
    'independent raw Arango oracle groups the complete scoped CDA Observation population by status and typed quantity.code state',
    'raw oracle proves a duplicate Pivot bucket with at least two numeric values and different SUM and MAX results',
    'typed category discovery matches the full raw MISSING, NULL, and scalar category domain',
    'native SUM Pivot preview matches every status/category aggregate from the raw oracle',
    'Cancel preserves the exact saved full-population workspace and source scope',
    'Apply and reload preserve the exact root output, source bindings, project, generation, and independent full raw source-scope count',
    'editing the duplicate bucket from SUM to MAX and renaming its heading matches the raw MAX oracle after reload',
    'removing Pivot restores the full raw oracle scope and exact bounded source tuples after reload',
    'all full-population native lifecycle actions complete within five seconds each',
    'no unexpected browser or authoring errors occur and source, API build, project, and generation remain stable',
  ],
};
export async function rootQuantityPivotWorkflow(page, nativeReport, action, check, fault, context) {
  const report = nativeReport.rootQuantityPivot ?? (nativeReport.rootQuantityPivot = {});
  const env = { ...process.env, ...(context.env ?? {}) };
  const target = context.target ?? {};
  const caseName = context.caseName ?? report.case ?? report.caseName;
  const mode = caseName === 'fixture-lifecycle' ? 'fixture-lifecycle'
    : caseName === 'full-population-lifecycle' ? 'full-population-lifecycle' : 'full-population';
  const isFixture = mode === 'fixture-lifecycle';
  const isFullCda = !isFixture;
  const reportCase = isFixture ? 'fixture-lifecycle'
    : mode === 'full-population-lifecycle' ? 'full-population-lifecycle' : 'full-population-discovery';
  const sourceRoot = target.sourceRoot ?? env.LOOM_SOURCE_FREEZE_ROOT ?? fileURLToPath(new URL('../..', import.meta.url));
  const fixtureDirectory = env.LOOM_ROOT_QUANTITY_FIXTURE_DIR ?? resolve(sourceRoot, 'testdata/root-quantity-pivot-fixture');
  const project = isFixture ? target.fixtureProject : (context.project ?? target.fixtureProject ?? target.project ?? env.LOOM_CDA_PROJECT);
  const expectedGeneration = isFixture ? target.fixtureGeneration : (target.fixtureGeneration ?? context.generation ?? 'cda-fhir-v1');
  const resourceType = 'Observation';
  const explorer = context.explorer ?? target.explorer ?? env.LOOM_CDA_EXPLORER ?? `root-quantity-category-${Date.now()}`;
  const apiOrigin = String(isFixture ? target.apiUrl : (context.apiOrigin ?? target.apiUrl ?? env.LOOM_CDA_API_ORIGIN)).replace(/\/$/, '');
  const uiOrigin = String(isFixture ? target.uiUrl : (context.uiOrigin ?? target.uiUrl ?? env.LOOM_CDA_UI_ORIGIN)).replace(/\/$/, '');
  const arangoContainer = isFixture
    ? (target.arangoContainer ?? `${target.composeProject}-arangodb-1`)
    : (target.arangoContainer ?? env.LOOM_CDA_ARANGO_CONTAINER ?? env.LOOM_ARANGO_CONTAINER);
  const arangoDatabase = env.LOOM_ARANGO_DATABASE ?? (isFixture ? 'loom_dev' : undefined);
  const apiBuildContainer = isFixture ? `${target.composeProject}-loom-api-1` : env.LOOM_CDA_API_CONTAINER;
  let root = `/api/v1/projects/${project}/explorers`;
  let base = `${root}/${explorer}/authoring/v2`;
  let outputId;
  let fixtureDigest;
  let apiBuildFreeze;
  let workflowError;
  let builder;
  let browserRequestCapture;
  const browserNetworkFailures = [];
  report.schemaVersion ??= 2;
  report.scenario = 'root-quantity-pivot';
  report.case = reportCase;
  nativeReport.scenario = 'root-quantity-pivot';
  nativeReport.case = reportCase;
  report.project = project;
  report.expectedGeneration = expectedGeneration;
  report.resourceType = resourceType;
  report.explorer = explorer;
  report.requiredChecks = requiredChecksByCase[reportCase];
  nativeReport.requiredChecks = requiredChecksByCase[reportCase];
  report.assertions ??= [];
  report.requests ??= [];
  report.authoringRequests ??= [];
  report.nativeRequests ??= [];
  report.cases ??= [];
  report.errors ??= [];
  report.browserErrors ??= { runtime: [], console: [], network: [] };
  report.browserErrors.runtime ??= [];
  report.browserErrors.console ??= [];
  report.browserErrors.network ??= [];
  report.cases ??= [];
  report.target = { ...(nativeReport.target ?? {}), explorer };
    const sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
  report.sourceFreeze = { watchedFileCount: sourceFreeze.watchedFileCount };
const inspectPage = (page, inspect, argument) => page.evaluate(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  return page.waitForFunction(predicate, argument, { timeout }).then(() => undefined);
};
const gotoPage = (page, url) => page.goto(url, { waitUntil: 'domcontentloaded' });
const clickNative = (page, selector, identity = {}) => {
  const locator = selector === 'button' && identity.name
    ? page.getByRole('button', { name: identity.name, exact: true })
    : page.locator(selector);
  return action(`click ${identity.name ?? selector}`, locator, target => target.click({ timeout: 5000 }));
};
const selectNative = async (page, selector, value) => {
  const locator = page.locator(selector);
  await action(`select ${selector}`, locator, target => target.selectOption(value, { timeout: 5000 }));
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
const browserEval = (page, source) => page.evaluate(async script => {
  const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
  return new AsyncFunction(script)();
}, source);
const waitForBrowser = (page, expression, timeout = 5000) => page.waitForFunction(
  source => new Function(`return (${source})`)(), expression, { timeout },
).then(() => undefined);
const navigate = (page, url) => gotoPage(page, url);
const click = (page, selector) => action(`click ${selector}`, page.locator(selector), target => target.click({ timeout: 5000 }));
const selectOption = async (page, selector, value, { settledWhen } = {}) => {
  await action(`select ${selector}`, page.locator(selector), target => target.selectOption(value, { timeout: 5000 }));
  if (settledWhen) await waitForBrowser(page, settledWhen, 5000);
};
const syncAuthoringRequests = () => {
  const endpoints = new Set(['construction-capabilities', 'construction-category-discoveries', 'construction-proposals', 'commands', 'reconcile', 'preview']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => {
      const authoringRequest = { ...request, pathname: request.path, url: `${request.origin}${request.path}`, requestStartedAtMs: request.startedAt,
        responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
        requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
        requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
        responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
        responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
        responseRowCount: request.response?.rowCount ?? request.response?.preview?.rowCount ?? request.response?.rows?.length,
        ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
      };
      const rawBody = browserRequestCapture.rawRequestBody(request);
      const rawResponse = browserRequestCapture.rawResponseBody(request);
      if (rawBody !== undefined) Object.defineProperty(authoringRequest, 'body', { value: rawBody });
      if (rawResponse !== undefined) Object.defineProperty(authoringRequest, 'response', { value: rawResponse });
      Object.defineProperty(authoringRequest, 'toJSON', {
        value() { return { ...this, body: request.body, response: request.response }; },
      });
      return authoringRequest;
    }));
  report.browserErrors.runtime = report.errors.filter(error => error.kind === 'runtime');
  report.browserErrors.console = report.errors.filter(error => error.kind === 'console');
  const ownedFailures = report.nativeRequests.filter(request => request.failure).map(request => ({ pathname: request.path, errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' }));
  const allFailures = browserNetworkFailures.map(failure => ({ pathname: new URL(failure.url, apiOrigin).pathname, errorText: failure.failure, canceled: failure.failure === 'net::ERR_ABORTED' }));
  report.browserErrors.network = [...ownedFailures, ...allFailures.filter(failure => !ownedFailures.some(owned => owned.pathname === failure.pathname && owned.errorText === failure.errorText))];
};const parseJSON = value => { try { return JSON.parse(value); } catch { return value; } };
const recordRequirement = (name, condition, evidence = {}) => {
  const passed = Boolean(condition);
  report.assertions.push({ dimension: 'correctness', name, status: passed ? 'passed' : 'failed', evidence });
  return check('correctness', name, passed, evidence);
};
const recordFact = (name, condition, evidence = {}) => {
  try { recordRequirement(name, condition, evidence); return Boolean(condition); }
  catch (error) { workflowError ??= error; return false; }
};
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
    'arangosh', '--server.database', arangoDatabase,
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

const runRawFullPopulationPivotOracle = generation => {
  const query = `FOR o IN Observation FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)} LET quantity = o.payload.valueQuantity LET codePresent = IS_OBJECT(quantity) ? HAS(quantity, "code") : false LET codeValue = codePresent ? quantity.code : null LET valuePresent = IS_OBJECT(quantity) ? HAS(quantity, "value") : false LET quantityValue = valuePresent ? quantity.value : null COLLECT status = o.payload.status, categoryPresent = codePresent, categoryValue = codeValue AGGREGATE rowCount = COUNT(), numericCount = SUM(IS_NUMBER(quantityValue) ? 1 : 0), valueSum = SUM(IS_NUMBER(quantityValue) ? quantityValue : 0), valueMax = MAX(IS_NUMBER(quantityValue) ? quantityValue : null) SORT status, categoryPresent ASC, TYPENAME(categoryValue), categoryValue RETURN {status, present: categoryPresent, value: categoryValue, rowCount, numericCount, valueSum, valueMax}`;
  const program = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', arangoDatabase ?? 'loom_dev',
    '--javascript.execute-string', program,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const payloadLine = result.stdout.split(/\r?\n/).findLast(line => line.trimStart().startsWith('['));
  assert(payloadLine, `Raw full-population Pivot query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const groups = JSON.parse(payloadLine).map(group => ({
    status: group.status,
    present: group.present,
    value: group.value,
    rowCount: group.rowCount,
    numericCount: group.numericCount,
    valueSum: group.valueSum,
    valueMax: group.valueMax,
  }));
  assert(groups.length > 0, 'The scoped CDA Observation source must contain at least one status/category group');
  for (const group of groups) {
    assert.equal(typeof group.status, 'string', `Raw Observation.status must be a scalar string for full Pivot grouping: ${JSON.stringify(group)}`);
    assert.equal(typeof group.present, 'boolean', `Raw category presence must be boolean: ${JSON.stringify(group)}`);
    if (!group.present) assert.equal(group.value, null, `A missing code path must project to null with present=false: ${JSON.stringify(group)}`);
    else assert(group.value === null || typeof group.value === 'string', `Unexpected typed quantity.code category: ${JSON.stringify(group)}`);
    assert(Number.isInteger(group.rowCount) && group.rowCount > 0, `Every raw aggregate bucket must have positive membership: ${JSON.stringify(group)}`);
    assert(Number.isInteger(group.numericCount) && group.numericCount >= 0 && group.numericCount <= group.rowCount, `Raw numeric value count is invalid: ${JSON.stringify(group)}`);
    if (group.numericCount > 0) {
      assert(Number.isFinite(group.valueSum), `Raw numeric SUM must be finite: ${JSON.stringify(group)}`);
      assert(Number.isFinite(group.valueMax), `Raw numeric MAX must be finite: ${JSON.stringify(group)}`);
    } else {
      assert.equal(group.valueMax, null, `An all-missing/non-numeric quantity bucket must have no numeric MAX: ${JSON.stringify(group)}`);
    }
  }
  const byCategory = new Map();
  for (const group of groups) {
    const identity = categoryIdentity(group.present, group.value);
    const current = byCategory.get(identity) ?? { present: group.present, value: group.value, rowCount: 0 };
    current.rowCount += group.rowCount;
    byCategory.set(identity, current);
  }
  const categories = [...byCategory.values()].sort((left, right) => categoryIdentity(left.present, left.value).localeCompare(categoryIdentity(right.present, right.value)));
  const duplicateBuckets = groups.filter(group => group.present && typeof group.value === 'string'
      && group.rowCount > 1 && group.numericCount > 1 && Math.abs(group.valueSum - group.valueMax) > 1e-9)
    .sort((left, right) => Number(right.value === 'd') - Number(left.value === 'd')
      || right.numericCount - left.numericCount
      || left.status.localeCompare(right.status)
      || categoryIdentity(left.present, left.value).localeCompare(categoryIdentity(right.present, right.value)));
  assert(duplicateBuckets.length > 0, 'The full CDA oracle must prove at least one status/category bucket with duplicate numeric values whose SUM differs from MAX');
  const duplicateWitness = duplicateBuckets[0];
  const sourceRows = groups.reduce((sum, group) => sum + group.rowCount, 0);
  const missingRows = categories.filter(category => !category.present).reduce((sum, category) => sum + category.rowCount, 0);
  const explicitNullRows = categories.filter(category => category.present && category.value === null).reduce((sum, category) => sum + category.rowCount, 0);
  const presentStringRows = categories.filter(category => category.present && typeof category.value === 'string').reduce((sum, category) => sum + category.rowCount, 0);
  return {
    query,
    scope: { project, generation, collection: 'Observation', authorization: 'local Compose no-auth / unrestricted' },
    sourceRows,
    missingRows,
    explicitNullRows,
    presentStringRows,
    categories,
    categoryIdentities: categories.map(category => categoryIdentity(category.present, category.value)).sort(),
    groups,
    duplicateWitness,
    rawGroupedOracle: 'status × MISSING/NULL/typed quantity.code with membership count, numeric value count, SUM, and MAX',
  };
};

const runRawObservationTupleOracle = (generation, requestedIDs) => {
  assert(Array.isArray(requestedIDs) && requestedIDs.length > 0 && requestedIDs.length <= 25, 'The bounded raw tuple query accepts only one to 25 native-preview IDs');
  assert(requestedIDs.every(id => typeof id === 'string' && id.length > 0), 'Every native-preview Observation ID must be a nonempty string');
  assert.equal(new Set(requestedIDs).size, requestedIDs.length, 'Native preview must not request duplicate source IDs');
  const query = `FOR o IN Observation FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)} AND o.id IN ${JSON.stringify(requestedIDs)} LET quantity = o.payload.valueQuantity LET codePresent = IS_OBJECT(quantity) ? HAS(quantity, "code") : false LET valuePresent = IS_OBJECT(quantity) ? HAS(quantity, "value") : false SORT o.id RETURN {id: o.id, status: o.payload.status, codePresent, codeValue: codePresent ? quantity.code : null, valuePresent, value: valuePresent ? quantity.value : null}`;
  const program = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', arangoDatabase ?? 'loom_dev',
    '--javascript.execute-string', program,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2 * 1024 * 1024 });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const payloadLine = result.stdout.split(/\r?\n/).findLast(line => line.trimStart().startsWith('['));
  assert(payloadLine, `Raw bounded Observation tuple query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const rawRows = JSON.parse(payloadLine);
  assert.deepEqual(rawRows.map(row => row.id).sort(), [...requestedIDs].sort(), 'Raw tuple query must return every requested source ID in the exact project/generation scope');
  return { query, scope: { project, generation, collection: 'Observation', authorization: 'local Compose no-auth / unrestricted' }, rawRows };
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
    'arangosh', '--server.database', arangoDatabase,
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
  const options = await inspectPage(page, selector => [...document.querySelector(selector).options].map(option=>({label:option.textContent,value:option.value})), selector);
  const matches = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
  assert.equal(matches.length, 1, `Expected one exact root source option for ${path}: ${JSON.stringify(options)}`);
  await selectNative(page, selector, matches[0].value);
  return matches[0].value;
};

const measure = (name, startedAt, finishedAt = Date.now()) => {
  const durationMs = finishedAt - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};

const pivotIdentity = key => key?.kind === 'MISSING'
  ? 'MISSING'
  : key?.kind === 'NULL'
    ? 'NULL'
    : JSON.stringify(key);
const stableRows = rows => rows.map(row => JSON.stringify(Object.entries(row).sort(([left], [right]) => left.localeCompare(right)))).sort();

const waitForProposal = async (requestOffset, previousProposalId, name) => {
  await waitForObservable(page, ({ previousProposalId }) => {
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
  const panel = await inspectPage(page, () => {
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
  let expectedRows;
  if (oracle.groups) {
    const statuses = [...new Set(oracle.groups.map(group => group.status))].sort();
    expectedRows = statuses.map(status => {
      const expectedRow = Object.fromEntries(groups.map(group => [group.name, status]));
      for (const category of categories) {
        const key = pivotIdentity(category.key);
        const rawGroup = oracle.groups.find(group => group.status === status && categoryIdentity(group.present, group.value) === key);
        if (!rawGroup || rawGroup.numericCount === 0) expectedRow[category.output.name] = null;
        else if (duplicatePolicy === 'SUM') expectedRow[category.output.name] = rawGroup.valueSum;
        else if (duplicatePolicy === 'MAX') expectedRow[category.output.name] = rawGroup.valueMax;
        else assert.fail(`Full-population lifecycle oracle does not implement unexpected duplicate policy ${duplicatePolicy}`);
      }
      return expectedRow;
    });
  } else {
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
    const expectedRow = { [groups[0].name]: 'final' };
    for (const category of categories) expectedRow[category.output.name] = aggregates.get(category.output.name) ?? null;
    expectedRows = [expectedRow];
  }
  const preview = response.preview;
  assert(preview, 'Native construction proposal must include its typed table preview');
  assert.equal(preview.receiptId, response.proposalId, 'Preview must be bound to the exact proposal receipt');
  assert.equal(preview.outputId, outputId);
  if (oracle.groups) {
    const statusCount = new Set(oracle.groups.map(group => group.status)).size;
    assert.equal(preview.rowCount, statusCount, 'Native Pivot must retain every raw Observation.status group');
    assert.equal(preview.rows.length, expectedRows.length);
  } else {
    assert.equal(preview.rowCount, 1, 'All four fixture Observations share the final status group');
    assert.equal(preview.rows.length, 1);
  }
  const groupOutput = groups[0];
  const columns = preview.columns.map(column => ({ column: column.column, label: labelOverrides[column.column] ?? column.label, logicalType: column.logicalType }));
  const actualRows = preview.rows.map(row => Object.fromEntries(columns.map(column => [column.column, row[column.column] ?? null])));
  assert.deepEqual(stableRows(actualRows), stableRows(expectedRows), `Pivot ${duplicatePolicy} output must match the independent raw values`);
  const identities = preview.rows.map(row => row.__loom_row_id);
  assert(identities.every(id => typeof id === 'string' && id.length > 0), 'Pivot preview must return a stable row identity');
  assert.equal(new Set(identities).size, identities.length);
  return { step, operation, preview, columns, expectedRows, categories, outputByIdentity: categoryByIdentity };
};

const assertRendered = async (columns, rows, label) => {
  await waitForObservable(page, ({ rowCount }) =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1)
      && !document.body.innerText.includes('Loading your table…'),
    { rowCount: rows.length }, 5000);
  const actual = await collectPreviewRows(page, { timeout: 5000 });
  assert.equal(actual.rowCount, rows.length, `${label} rendered preview must expose every expected row`);
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), columns.map(column => column.label.toLowerCase()), `${label} headers must match the native output labels`);
  const expectedCells = rows.map(row => columns.map(column => row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row.values)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${label} rendered values must match the typed preview and raw oracle`);
  return actual;
};

const assertProposalPanel = async (pivot, label) => {
  await waitForObservable(page, ({ rowCount }) => document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===rowCount, { rowCount: pivot.expectedRows.length }, 5000);
  const actual = await inspectPage(page, () => {
return {headers:[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].map(cell=>cell.innerText.trim().split('\\n')[0]),rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};
});
  assert.deepEqual(actual.headers.map(header => header.toLowerCase()), pivot.columns.map(column => column.label.toLowerCase()), `${label} proposal headers must show the native category labels`);
  const expectedCells = pivot.expectedRows.map(row => pivot.columns.map(column => row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
  assert.deepEqual(actual.rows.map(row => JSON.stringify(row)).sort(), expectedCells.map(row => JSON.stringify(row)).sort(), `${label} proposal values must match independent aggregation`);
};

const openPivotEditor = async () => {
  await clickNative(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled===false));
  await clickNative(page, '[data-testid="construction-action-table-pivot-rows"]');
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))));
};

const setPivotDuplicatePolicy = async (policy, requestOffset, name) => {
  const startedAt = Date.now();
  await clickNative(page, '[data-testid="construction-reshape-pivot-advanced"] > summary');
  if (policy === 'SUM') {
    try {
      await waitForObservable(page,
        () => Boolean((()=>{const alert=document.querySelector('[data-testid="construction-proposal-error"]');const select=document.querySelector('select[aria-label="Pivot duplicate policy"]');return Boolean(alert)&&select?.value==='ERROR'})()),
        5000);
    } catch (waitError) {
      let visibleState;
      try {
        visibleState = await inspectPage(page, () => {
          const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
          const alert=document.querySelector('[data-testid="construction-proposal-error"]');
          const select=document.querySelector('select[aria-label="Pivot duplicate policy"]');
          const summary=document.querySelector('[data-testid="construction-reshape-pivot-policy-summary"]');
          return { proposalStatus: panel?.dataset.proposalStatus ?? null, proposalId: panel?.dataset.proposalId ?? null,
            alert: alert?.innerText.trim() ?? null, policy: select?.value ?? null, summary: summary?.innerText.trim() ?? null,
            statusMessages: [...document.querySelectorAll('[role="status"],[role="alert"]')].map(node=>node.innerText.trim()).filter(Boolean),
            visibleText: (document.body?.innerText ?? '').slice(-1800) };
        });
      } catch (captureError) {
        visibleState = { captureError: String(captureError) };
      }
      const failureEvidence = captureValidationWaitFailure({
        requests: report.authoringRequests,
        requestOffset,
        pathname: `${base}/construction-proposals`,
        name,
        timeoutMs: 5000,
        error: waitError,
        visibleState,
      });
      report.validationWaitFailures ??= [];
      report.validationWaitFailures.push(failureEvidence);
      await drainResponseReads();
      refreshValidationWaitFailureRequests(failureEvidence, report.authoringRequests);
      throw new Error(`${name}: expected cardinality rejection did not render within the existing 5000ms budget; request evidence is recorded as an unexpected failure`);
    }
    await drainResponseReads();
    measure(`${name} expected duplicate validation to render`, startedAt);
    const failures = report.authoringRequests.slice(requestOffset)
      .filter(request => request.endpoint === 'construction-proposals' && request.status >= 400);
    assert.equal(failures.length, 1, `${name} must have exactly one preceding validation rejection before SUM: ${JSON.stringify(failures.map(request => ({ status: request.status, code: request.response?.error?.code })))}`);
    const rejection = failures[0];
    const alert = await inspectPage(page, () => {
const panel=document.querySelector('[data-testid="construction-proposal-error"]');const select=document.querySelector('select[aria-label="Pivot duplicate policy"]');const summary=document.querySelector('[data-testid="construction-reshape-pivot-policy-summary"]');const sum=[...select.options].find(option=>option.value==='SUM');return {alert:panel?.innerText.trim(),policy:select?.value,summary:summary?.innerText.trim(),sumOption:sum?{label:sum.textContent.trim(),disabled:sum.disabled}:null};
});
    let duplicateWitnesses;
    let duplicateBucketEvidence;
    if (report.oracle.groups) {
      const bucket = report.oracle.duplicateWitness;
      assert(bucket, `${name} ERROR policy must be justified by the raw full-population duplicate bucket`);
      assert(bucket.present && typeof bucket.value === 'string', `${name} editable duplicate witness must be a literal string quantity.code category`);
      assert(bucket.rowCount > 1 && bucket.numericCount > 1 && Math.abs(bucket.valueSum - bucket.valueMax) > 1e-9,
        `${name} raw status/category bucket must prove repeated numeric values whose SUM differs from MAX`);
      duplicateWitnesses = [bucket];
      duplicateBucketEvidence = {
        status: bucket.status,
        category: categoryIdentity(bucket.present, bucket.value),
        rowCount: bucket.rowCount,
        numericCount: bucket.numericCount,
        sum: bucket.valueSum,
        max: bucket.valueMax,
      };
    } else {
      const duplicateCode = JSON.stringify({ kind: 'STRING', string: 'd' });
      duplicateWitnesses = report.oracle.fixtureRows.filter(row => row.category === duplicateCode);
      assert.equal(duplicateWitnesses.length, 2, `${name} ERROR policy must be justified by two independent raw d witnesses`);
      assert(duplicateWitnesses.every(row => row.status === duplicateWitnesses[0].status), `${name} raw d witnesses must share the selected status group`);
      assert.equal(duplicateWitnesses[0].status, 'final');
      duplicateBucketEvidence = { rawWitnessIDs: duplicateWitnesses.map(row => row.id), status: duplicateWitnesses[0].status, category: duplicateCode };
    }
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
      rawDuplicateWitnessIDs: duplicateWitnesses.flatMap(row => row.id ? [row.id] : []),
      rawDuplicateBucket: duplicateBucketEvidence,
      visibleRepair: alert,
      classification: 'expected-domain-validation-repaired-by-user-selected-SUM',
    });
  }
const prior = await inspectPage(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId ?? '');
  const previewStartedAt = policy === 'SUM' ? Date.now() : startedAt;
  await selectNative(page, 'select[aria-label="Pivot duplicate policy"]', policy);
  const result = await waitForProposal(requestOffset, prior, name);
  measure(`${name} automatic preview action to render`, previewStartedAt);
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
  await clickNative(page, '[data-testid="construction-cancel-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), 5000);
  builder = await api(base + '/builder');
  assertBuilderScope(builder, 'After Cancel');
  assert.deepEqual(builder.workspace, prePivotWorkspace, 'Cancel must preserve the pre-Pivot saved workspace');
  assert.equal(report.authoringRequests.filter(entry => entry.endpoint === 'commands').length, commandCount, 'Cancel must not issue a native draft command');
  recordRequirement(requiredChecksByCase[reportCase][3], JSON.stringify(builder.workspace) === JSON.stringify(prePivotWorkspace)
    && report.authoringRequests.filter(entry => entry.endpoint === 'commands').length === commandCount,
  { project, generation: builder.catalog.generation, outputId, commandCount });
  measure('quantity Pivot preview cancel', cancelStarted);

  await openPivotEditor();
  await selectPivotSource('Add pivot group field', 'Observation.status');
  await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const requestOffset = report.authoringRequests.length;
  await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))&&!document.querySelector('[data-testid="construction-reshape-pivot"]')?.innerText.includes('Finding categories')), 5000);
  const applyPreview = await setPivotDuplicatePolicy('SUM', requestOffset, 'Reopened bounded Pivot preview');
  const applyPivot = expectedPivot(applyPreview.request, oracle, 'SUM');
  await assertProposalPanel(applyPivot, 'Reopened bounded Pivot preview');
  const applyStarted = Date.now();
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1), 5000);
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
  await gotoPage(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForObservable(page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 5000);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 5000);
  await assertRendered(applyPivot.columns, applyPivot.expectedRows, 'Reloaded quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedPivotScope(builder, 'SUM', undefined, 'After quantity Pivot reload');
  recordRequirement(requiredChecksByCase[reportCase][4], builder.catalog.generation === expectedGeneration
    && document.output.id === outputId
    && JSON.stringify(document.rows) === JSON.stringify(prePivotDocument.rows)
    && savedSourceBindings.groupKeyIds.length === 1
    && savedSourceBindings.categoryColumnId === applyPivot.operation.categoryColumnId
    && savedSourceBindings.valueColumnId === applyPivot.operation.valueColumnId,
  { project, generation: builder.catalog.generation, outputId, sourceBindings: savedSourceBindings, sourceRowCount: oracle.sourceRows });
  measure('quantity Pivot reload to render', reloadStarted);

  const history = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
  const pivotHistory = history.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(pivotHistory, 'Reloaded construction history must expose the saved quantity Pivot');
  await clickNative(page, `[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))), 5000);
  await clickNative(page, '[data-testid^="construction-edit-step-"]');
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))), 5000);
  const editorBefore = await inspectPage(page, () => {
const root=document.querySelector('[data-testid="construction-reshape-pivot"]');return {policy:root.querySelector('select[aria-label="Pivot duplicate policy"]')?.value,groups:[...root.querySelectorAll('input[aria-label^="Pivot group"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),category:root.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:root.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent,labels:[...root.querySelectorAll('input[aria-label^="Pivot output label "]')].map(input=>({ariaLabel:input.getAttribute('aria-label'),value:input.value}))};
});
  assert.equal(editorBefore.policy, 'SUM', 'Saved Pivot edit must restore SUM duplicate policy');
  assert(editorBefore.groups.some(label => label.includes('Observation.status')) || editorBefore.groups.some(label => /Observation status/i.test(label)), `Saved Pivot edit must retain its exact status grouping: ${JSON.stringify(editorBefore.groups)}`);
  assert(editorBefore.category?.includes('Observation.valueQuantity.code'));
  assert(editorBefore.value?.includes('Observation.valueQuantity.value'));
  const dLabel = editorBefore.labels.find(item => item.value === 'd');
  assert(dLabel, `Saved Pivot edit must expose the d output label: ${JSON.stringify(editorBefore.labels)}`);
  const editRequestOffset = report.authoringRequests.length;
  const priorEditProposal = await inspectPage(page, () => {
return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';
});
  const editStarted = Date.now();
  await clickNative(page, '[data-testid="construction-reshape-pivot-advanced"] > summary');
  await selectNative(page, 'select[aria-label="Pivot duplicate policy"]', 'MAX');
  const categoryLabel = page.getByLabel('Pivot output label d', { exact: true });
  await action('fill Pivot category label d', categoryLabel, locator => locator.fill('d maximum', { timeout: 5000 }), { editable: true });
  await waitForObservable(page, ({ previousProposalId }) => {
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
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1), 5000);
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
  await gotoPage(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForObservable(page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 5000);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 5000);
  await assertRendered(editPivot.columns, editPivot.expectedRows, 'Reloaded edited quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedPivotScope(builder, 'MAX', {
    column: editPivot.categories.find(category => pivotIdentity(category.key) === JSON.stringify({ kind: 'STRING', string: 'd' })).output.name,
    label: 'd maximum',
  }, 'After edited quantity Pivot reload');
  recordRequirement(requiredChecksByCase[reportCase][5], document.construction.steps.find(step => step.operation.kind === 'PIVOT')?.operation.pivot.duplicatePolicy === 'MAX'
    && document.construction.steps.find(step => step.operation.kind === 'PIVOT')?.outputs.some(output => output.label === 'd maximum')
    && document.output.id === outputId && builder.catalog.generation === expectedGeneration,
  { policy: 'MAX', editedHeading: 'd maximum', generation: builder.catalog.generation, sourceBindings: savedSourceBindings });
  measure('quantity Pivot edited reload to render', editReloadStarted);

  const removeHistory = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));
});
  const removeStep = removeHistory.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(removeStep, 'Edited Pivot history must remain available for removal');
  await clickNative(page, `[data-testid=${JSON.stringify(removeStep.testId)}]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))), 5000);
  const removeRequestOffset = report.authoringRequests.length;
  const removeStarted = Date.now();
  await clickNative(page, '[data-testid^="construction-remove-step-"]');
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'), 5000);
  await drainResponseReads();
  const removePreviewRenderedAt = Date.now();
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
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0), 5000);
  await assertRendered(sourceColumns, expectedSourceRows, 'Restored raw quantity source rows');
  measure('quantity Pivot removal Apply to render', removeApplyStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After Pivot removal Apply');
  assert.deepEqual(document.construction.steps, [], 'Pivot removal must restore the original root table definition');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Pivot removal must restore the exact root Observation population definition');
  const restoredColumnIDs = assertSourceBindingsRestored(prePivotDocument.columns, document.columns, 'Pivot removal');
  const finalReloadStarted = Date.now();
  await gotoPage(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForObservable(page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId, 5000);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false), 5000);
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
  recordRequirement(requiredChecksByCase[reportCase][6], document.output.id === outputId
    && builder.catalog.generation === expectedGeneration
    && JSON.stringify(document.rows) === JSON.stringify(prePivotDocument.rows)
    && JSON.stringify(oracle.fixtureRows.map(row => row.id).sort()) === JSON.stringify(sourceRecords.map(row => row.id).sort())
    && document.construction.steps.length === 0,
  { project, generation: builder.catalog.generation, outputId, sourceIDs: sourceRecords.map(row => row.id).sort(), restoredColumnIDs });
  report.fixtureLifecycle.restoredSourceRows = sourceRecords.map(row => ({ id: row.id, category: row.category, value: row.value }));
  report.fixtureLifecycle.editedDuplicatePolicy = 'MAX';
  report.fixtureLifecycle.editedDValue = 4;
  report.fixtureLifecycle.lifecycle = ['Preview', 'Cancel', 'Apply', 'reload', 'edit heading/policy', 'Apply', 'reload', 'remove', 'restore', 'reload'];
};

const runFullPopulationLifecycle = async (discovery, oracle, prePivotWorkspace, prePivotDocument, discoveryRequestOffset) => {
  const witness = oracle.duplicateWitness;
  const initial = await setPivotDuplicatePolicy('SUM', discoveryRequestOffset, 'Initial full CDA Pivot preview');
  const initialPivot = expectedPivot(initial.request, oracle, 'SUM');
  await assertProposalPanel(initialPivot, 'Initial full CDA Pivot preview');
  const initialActualRows = initialPivot.preview.rows.map(row => Object.fromEntries(initialPivot.columns.map(column => [column.column, row[column.column] ?? null])));
  recordRequirement(requiredChecksByCase[reportCase][3],
    initialPivot.preview.rowCount === new Set(oracle.groups.map(group => group.status)).size
      && JSON.stringify(stableRows(initialActualRows)) === JSON.stringify(stableRows(initialPivot.expectedRows)),
    { rowCount: initialPivot.preview.rowCount, expectedGroups: new Set(oracle.groups.map(group => group.status)).size, rows: initialPivot.expectedRows });

  report.fullPopulationLifecycle = {
    discoveryOutcome: discovery.response.outcome,
    typedCategories: discovery.response.categories.map(category => category.key),
    sourceRows: oracle.sourceRows,
    rawGroupCount: oracle.groups.length,
    duplicateWitness: {
      status: witness.status,
      category: categoryIdentity(witness.present, witness.value),
      rowCount: witness.rowCount,
      numericCount: witness.numericCount,
      sum: witness.valueSum,
      max: witness.valueMax,
    },
    initialDuplicatePolicy: 'SUM',
    initialExpected: initialPivot.expectedRows,
  };

  const commandCount = report.authoringRequests.filter(entry => entry.endpoint === 'commands').length;
  const cancelStarted = Date.now();
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  builder = await api(base + '/builder');
  const afterCancel = assertBuilderScope(builder, 'After full CDA Pivot Cancel');
  assert.deepEqual(builder.workspace, prePivotWorkspace, 'Cancel must preserve the exact pre-Pivot full-population workspace');
  assert.deepEqual(afterCancel.rows, prePivotDocument.rows, 'Cancel must preserve the exact full Observation source population definition');
  assert.equal(report.authoringRequests.filter(entry => entry.endpoint === 'commands').length, commandCount, 'Cancel must not issue a native draft command');
  recordRequirement(requiredChecksByCase[reportCase][4], builder.catalog.generation === expectedGeneration
    && afterCancel.output.id === outputId
    && JSON.stringify(afterCancel.rows) === JSON.stringify(prePivotDocument.rows)
    && report.authoringRequests.filter(entry => entry.endpoint === 'commands').length === commandCount,
  { project, generation: builder.catalog.generation, outputId, sourceRows: oracle.sourceRows, commandCount });
  measure('full CDA quantity Pivot preview cancel', cancelStarted);

  await openPivotEditor();
  await selectPivotSource('Add pivot group field', 'Observation.status');
  await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const requestOffset = report.authoringRequests.length;
  await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForBrowser(page,
    `Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))&&!document.querySelector('[data-testid="construction-reshape-pivot"]')?.innerText.includes('Finding categories')`,
    10000);
  const applyPreview = await setPivotDuplicatePolicy('SUM', requestOffset, 'Reopened full CDA Pivot preview');
  const applyPivot = expectedPivot(applyPreview.request, oracle, 'SUM');
  await assertProposalPanel(applyPivot, 'Reopened full CDA Pivot preview');
  assert.equal(report.expectedAuthoringValidations?.length, 2, 'Only the two explicit ERROR-policy full-population previews may be classified as expected validation');
  recordRequirement(requiredChecksByCase[reportCase][1], report.expectedAuthoringValidations.every(validation =>
    validation.code === 'TABLE_PIVOT_CELL_CARDINALITY' && validation.duplicatePolicy === 'ERROR'
      && validation.project === project && validation.outputId === outputId
      && validation.rawDuplicateBucket?.rowCount > 1 && validation.rawDuplicateBucket?.numericCount > 1
      && validation.rawDuplicateBucket?.sum !== validation.rawDuplicateBucket?.max),
  { validations: report.expectedAuthoringValidations });

  const applyStarted = Date.now();
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, `!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 10000);
  await assertRendered(applyPivot.columns, applyPivot.expectedRows, 'Applied full CDA quantity Pivot');
  measure('full CDA quantity Pivot Apply to render', applyStarted);
  builder = await api(base + '/builder');
  let document = assertBuilderScope(builder, 'After full CDA Pivot Apply');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Pivot Apply must preserve the exact full Observation root population definition');
  let savedPivot = document.construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(savedPivot, 'Applied full CDA quantity Pivot must be saved');
  assert.equal(savedPivot.operation.pivot.duplicatePolicy, 'SUM');
  const savedSourceBindings = {
    groupKeyIds: savedPivot.operation.pivot.groupKeyIds,
    categoryColumnId: savedPivot.operation.pivot.categoryColumnId,
    valueColumnId: savedPivot.operation.pivot.valueColumnId,
  };
  assert.deepEqual(savedSourceBindings.groupKeyIds, applyPivot.operation.groupKeyIds, 'Saved status grouping must match the accepted source binding');
  assert.equal(savedSourceBindings.categoryColumnId, applyPivot.operation.categoryColumnId, 'Saved category source must match the accepted source binding');
  assert.equal(savedSourceBindings.valueColumnId, applyPivot.operation.valueColumnId, 'Saved numeric value source must match the accepted source binding');
  const assertSavedScope = (state, expectedPolicy, expectedHeading, label) => {
    const scopedDocument = assertBuilderScope(state, label);
    assert.deepEqual(scopedDocument.rows, prePivotDocument.rows, `${label} must retain the exact root Observation population definition`);
    const pivot = scopedDocument.construction.steps.find(step => step.operation.kind === 'PIVOT');
    assert(pivot, `${label} must retain the saved full CDA quantity Pivot`);
    assert.equal(pivot.operation.pivot.duplicatePolicy, expectedPolicy);
    assert.deepEqual(pivot.operation.pivot.groupKeyIds, savedSourceBindings.groupKeyIds);
    assert.equal(pivot.operation.pivot.categoryColumnId, savedSourceBindings.categoryColumnId);
    assert.equal(pivot.operation.pivot.valueColumnId, savedSourceBindings.valueColumnId);
    if (expectedHeading) {
      const output = pivot.outputs.find(candidate => candidate.name === expectedHeading.column);
      assert.equal(output?.label, expectedHeading.label, `${label} must persist the edited category heading`);
    }
    return scopedDocument;
  };
  const reloadStarted = Date.now();
  await navigate(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`, 10000);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`, 10000);
  await assertRendered(applyPivot.columns, applyPivot.expectedRows, 'Reloaded full CDA quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedScope(builder, 'SUM', undefined, 'After full CDA quantity Pivot reload');
  recordRequirement(requiredChecksByCase[reportCase][5], builder.catalog.generation === expectedGeneration
    && document.output.id === outputId
    && JSON.stringify(document.rows) === JSON.stringify(prePivotDocument.rows)
    && savedSourceBindings.groupKeyIds.length === 1
    && savedSourceBindings.categoryColumnId === applyPivot.operation.categoryColumnId
    && savedSourceBindings.valueColumnId === applyPivot.operation.valueColumnId,
  { project, generation: builder.catalog.generation, outputId, sourceRows: oracle.sourceRows, savedSourceBindings });
  measure('full CDA quantity Pivot reload to render', reloadStarted);

  const history = await browserEval(page, `return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));`);
  const pivotHistory = history.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(pivotHistory, 'Reloaded full CDA construction history must expose the saved Pivot');
  await click(page, `[data-testid=${JSON.stringify(pivotHistory.testId)}]`);
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid^="construction-edit-step-"]'))`, 5000);
  await click(page, '[data-testid^="construction-edit-step-"]');
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))`, 5000);
  const categoryKey = categoryIdentity(witness.present, witness.value);
  const editedCategory = applyPivot.categories.find(category => pivotIdentity(category.key) === categoryKey);
  assert(editedCategory, `Raw duplicate witness category ${categoryKey} must have a native output`);
  const originalHeading = editedCategory.output.label;
  const editedHeading = `${originalHeading} maximum`;
  const editorBefore = await browserEval(page, `const root=document.querySelector('[data-testid="construction-reshape-pivot"]');return {policy:root.querySelector('select[aria-label="Pivot duplicate policy"]')?.value,groups:[...root.querySelectorAll('input[aria-label^="Pivot group"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label')),category:root.querySelector('select[aria-label="Pivot category field"]')?.selectedOptions[0]?.textContent,value:root.querySelector('select[aria-label="Pivot values field"]')?.selectedOptions[0]?.textContent,labels:[...root.querySelectorAll('input[aria-label^="Pivot output label "]')].map(input=>({ariaLabel:input.getAttribute('aria-label'),value:input.value}))};`);
  assert.equal(editorBefore.policy, 'SUM', 'Saved full CDA Pivot edit must restore SUM');
  assert(editorBefore.groups.some(label => label.includes('Observation.status')) || editorBefore.groups.some(label => /Observation status/i.test(label)), `Saved full CDA Pivot edit must retain status grouping: ${JSON.stringify(editorBefore.groups)}`);
  assert(editorBefore.category?.includes('Observation.valueQuantity.code'));
  assert(editorBefore.value?.includes('Observation.valueQuantity.value'));
  assert(editorBefore.labels.some(item => item.value === originalHeading), `Saved Pivot editor must expose category label ${originalHeading}`);
  const editRequestOffset = report.authoringRequests.length;
  const priorEditProposal = await browserEval(page, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId??'';`);
  const editStarted = Date.now();
  await click(page, '[data-testid="construction-reshape-pivot-advanced"] > summary');
  await selectOption(page, 'select[aria-label="Pivot duplicate policy"]', 'MAX', {
    settledWhen: `document.querySelector('select[aria-label="Pivot duplicate policy"]')?.value==='MAX'`,
  });
  const headingInput = page.locator('input[aria-label^="Pivot output label "]').filter({ hasValue: originalHeading });
  await action('fill Pivot category label', headingInput, locator => locator.fill(editedHeading), { editable: true });
  await waitForBrowser(page, `(()=>{const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return panel?.dataset.proposalStatus==='ready'&&panel.dataset.proposalId!==${JSON.stringify(priorEditProposal)}&&[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].some(cell=>cell.innerText.includes(${JSON.stringify(editedHeading)}))})()`, 15000);
  await drainResponseReads();
  const editRequest = report.authoringRequests.slice(editRequestOffset).findLast(entry => entry.endpoint === 'construction-proposals');
  assert(editRequest, 'Full CDA Pivot edit must issue a native construction proposal');
  assert.equal(editRequest.status, 200, `Full CDA Pivot edit proposal failed: ${JSON.stringify(editRequest.response)}`);
  assert.equal(editRequest.pathname, `${base}/construction-proposals`);
  assert.equal(editRequest.body?.outputId, outputId);
  assert.equal(editRequest.body?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(editRequest.body?.expectedDraftVersion, builder.draftVersion);
  assert.equal(editRequest.body?.expectedDraftDigest, builder.draftDigest);
  const editPivot = expectedPivot(editRequest, oracle, 'MAX', { [editedCategory.output.name]: editedHeading });
  assert.deepEqual(editPivot.operation.groupKeyIds, savedSourceBindings.groupKeyIds);
  assert.equal(editPivot.operation.categoryColumnId, savedSourceBindings.categoryColumnId);
  assert.equal(editPivot.operation.valueColumnId, savedSourceBindings.valueColumnId);
  const groupName = applyPivot.step.outputs.find(output => output.id === applyPivot.operation.groupKeyIds[0])?.name;
  const witnessSum = applyPivot.expectedRows.find(row => row[groupName] === witness.status)?.[editedCategory.output.name];
  const witnessMax = editPivot.expectedRows.find(row => row[groupName] === witness.status)?.[editedCategory.output.name];
  assert.equal(witnessSum, witness.valueSum, 'Native SUM result for the raw duplicate witness must equal the independent aggregate');
  assert.equal(witnessMax, witness.valueMax, 'Native MAX result for the raw duplicate witness must equal the independent aggregate');
  assert.notEqual(witnessSum, witnessMax, 'The selected duplicate witness must make the SUM→MAX edit observable');
  await assertProposalPanel(editPivot, 'Edited full CDA Pivot preview');
  measure('full CDA quantity Pivot edit preview', editStarted);
  const editApplyStarted = Date.now();
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, `!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===1`, 10000);
  await assertRendered(editPivot.columns, editPivot.expectedRows, 'Edited full CDA quantity Pivot');
  measure('full CDA quantity Pivot edit Apply to render', editApplyStarted);
  builder = await api(base + '/builder');
  document = assertSavedScope(builder, 'MAX', { column: editedCategory.output.name, label: editedHeading }, 'After full CDA Pivot edit Apply');
  const editReloadStarted = Date.now();
  await navigate(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`, 10000);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`, 10000);
  await assertRendered(editPivot.columns, editPivot.expectedRows, 'Reloaded edited full CDA quantity Pivot');
  builder = await api(base + '/builder');
  document = assertSavedScope(builder, 'MAX', { column: editedCategory.output.name, label: editedHeading }, 'After edited full CDA quantity Pivot reload');
  recordRequirement(requiredChecksByCase[reportCase][6], witnessSum === witness.valueSum && witnessMax === witness.valueMax
    && document.construction.steps.find(step => step.operation.kind === 'PIVOT')?.operation.pivot.duplicatePolicy === 'MAX'
    && document.construction.steps.find(step => step.operation.kind === 'PIVOT')?.outputs.some(output => output.name === editedCategory.output.name && output.label === editedHeading),
  { duplicateWitness: report.fullPopulationLifecycle.duplicateWitness, sum: witnessSum, max: witnessMax, persistedHeading: editedHeading, generation: builder.catalog.generation });
  measure('full CDA quantity Pivot edited reload to render', editReloadStarted);

  const removeHistory = await browserEval(page, `return [...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(button=>({testId:button.getAttribute('data-testid'),text:button.innerText}));`);
  const removeStep = removeHistory.findLast(item => /pivot|categories into columns/i.test(item.text));
  assert(removeStep, 'Edited full CDA Pivot history must remain available for removal');
  await click(page, `[data-testid=${JSON.stringify(removeStep.testId)}]`);
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid^="construction-remove-step-"]'))`, 5000);
  const removeRequestOffset = report.authoringRequests.length;
  const removeStarted = Date.now();
  await click(page, '[data-testid^="construction-remove-step-"]');
  await waitForBrowser(page, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus==='ready'`, 10000);
  await drainResponseReads();
  const removePreviewRenderedAt = Date.now();
  const removeRequest = report.authoringRequests.slice(removeRequestOffset).findLast(entry => entry.endpoint === 'construction-proposals');
  assert.equal(removeRequest?.status, 200, 'Removing the saved full CDA Pivot must produce a native proposal');
  assert.equal(removeRequest.pathname, `${base}/construction-proposals`);
  assert.equal(removeRequest.body?.outputId, outputId);
  assert.equal(removeRequest.body?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(removeRequest.body?.expectedDraftVersion, builder.draftVersion);
  assert.equal(removeRequest.body?.expectedDraftDigest, builder.draftDigest);
  assert.equal(removeRequest.response?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(removeRequest.response?.outputId, outputId);
  const removePreview = removeRequest.response.preview;
  assert(removePreview, 'Full CDA Pivot removal proposal must include a bounded source-row preview');
  const boundedCount = assertBoundedPreviewCount(removePreview, oracle.sourceRows);
  const sourceColumns = removePreview.columns.map(column => ({ column: column.column, label: column.label, logicalType: column.logicalType }));
  const idColumn = sourceColumns.find(column => column.label === 'Observation ID');
  const categoryColumn = sourceColumns.find(column => column.label === 'Quantity Code');
  const valueColumn = sourceColumns.find(column => column.label === 'Quantity Value');
  assert(idColumn && categoryColumn && valueColumn, `Removal must restore the exact configured source fields: ${JSON.stringify(sourceColumns)}`);
  const boundedIDs = removePreview.rows.map(row => row[idColumn.column]);
  const boundedRawOracle = runRawObservationTupleOracle(expectedGeneration, boundedIDs);
  const boundedRawTuples = assertPreviewRowsMatchRawObservations({
    previewRows: removePreview.rows,
    rawRows: boundedRawOracle.rawRows,
    idColumn: idColumn.column,
    categoryColumn: categoryColumn.column,
    valueColumn: valueColumn.column,
    rawAggregateGroups: oracle.groups,
  });
  const displayedRestoredRows = removePreview.rows.map(row => Object.fromEntries(sourceColumns.map(column => [column.column, row[column.column] ?? null])));
  await assertProposalPanel({ columns: sourceColumns, expectedRows: displayedRestoredRows }, 'Full CDA Pivot removal preview');
  report.fullPopulationLifecycle.boundedRestorationProof = {
    rawSourceRows: oracle.sourceRows,
    previewRowsConsumed: boundedCount.boundedPreviewRows,
    previewLimit: boundedCount.limit,
    sourceIDs: boundedIDs,
    rawTupleQuery: boundedRawOracle.query,
    rawTuples: boundedRawTuples,
  };
  measure('full CDA quantity Pivot removal preview', removeStarted, removePreviewRenderedAt);
  const removeApplyStarted = Date.now();
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, `!document.querySelector('[data-testid="construction-proposal-panel"]')&&document.querySelectorAll('[data-testid^="construction-history-step-"]').length===0`, 10000);
  await assertRendered(sourceColumns, displayedRestoredRows, 'Restored full CDA quantity source preview');
  measure('full CDA quantity Pivot removal Apply to render', removeApplyStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After full CDA Pivot removal Apply');
  assert.deepEqual(document.construction.steps, [], 'Pivot removal must restore the original full-population table definition');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Pivot removal must restore the exact full Observation root population definition');
  const restoredColumnIDs = assertSourceBindingsRestored(prePivotDocument.columns, document.columns, 'Full CDA Pivot removal');
  const reloadScope = {
    snapshotToken: builder.catalog.snapshotToken,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
  };
  const finalReloadStarted = Date.now();
  const finalReloadRequestOffset = report.authoringRequests.length;
  await navigate(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForBrowser(page, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`, 10000);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`, 10000);
  await waitForBrowser(page, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='${Math.min(25, oracle.sourceRows) + 1}'&&!document.body.innerText.includes('Loading your table…')`, 10000);
  await drainResponseReads();
  const reloadPreviewContext = assertReloadPreviewContext({
    requests: report.authoringRequests,
    requestOffset: finalReloadRequestOffset,
    basePath: base,
    outputId,
    generation: expectedGeneration,
    snapshotToken: reloadScope.snapshotToken,
    draftVersion: reloadScope.draftVersion,
    draftDigest: reloadScope.draftDigest,
  });
  const reloadPreview = reloadPreviewContext.preview;
  const reloadPreviewCount = assertBoundedPreviewCount(reloadPreview, oracle.sourceRows);
  const reloadSourceColumns = reloadPreview.columns.map(column => ({ column: column.column, label: column.label, logicalType: column.logicalType }));
  assert.deepEqual(reloadSourceColumns, sourceColumns, 'Reloaded automatic preview must restore the exact source columns and labels');
  const reloadPreviewIDs = reloadPreview.rows.map(row => row[idColumn.column]);
  const displayedReloadRows = reloadPreview.rows.map(row => Object.fromEntries(sourceColumns.map(column => [column.column, row[column.column] ?? null])));
  await assertRendered(sourceColumns, displayedReloadRows, 'Reloaded restored full CDA quantity source preview');
  measure('full CDA quantity Pivot restoration reload to render', finalReloadStarted);
  builder = await api(base + '/builder');
  document = assertBuilderScope(builder, 'After final full CDA restoration reload');
  assert.equal(builder.catalog.snapshotToken, reloadScope.snapshotToken, 'Final reload must use the exact post-removal snapshot');
  assert.equal(builder.draftVersion, reloadScope.draftVersion, 'Final reload must retain the exact post-removal draft version');
  assert.equal(builder.draftDigest, reloadScope.draftDigest, 'Final reload must retain the exact post-removal draft digest');
  assert.deepEqual(document.construction.steps, [], 'Final reload must retain the restored root table definition');
  assert.deepEqual(document.rows, prePivotDocument.rows, 'Final reload must retain the exact original full Observation population definition');
  assert.deepEqual(assertSourceBindingsRestored(prePivotDocument.columns, document.columns, 'Full CDA final reload'), restoredColumnIDs,
    'Final reload must preserve the same source field identities');
  assert.equal(document.output.id, outputId);
  assert.equal(builder.catalog.generation, expectedGeneration);
  assert.deepEqual(document.construction.steps, [], 'The fresh raw reload sample must come from the restored untransformed source');
  const restoredBoundedRawOracle = runRawObservationTupleOracle(expectedGeneration, reloadPreviewIDs);
  const restoredBoundedRawTuples = assertPreviewRowsMatchRawObservations({
    previewRows: reloadPreview.rows,
    rawRows: restoredBoundedRawOracle.rawRows,
    idColumn: idColumn.column,
    categoryColumn: categoryColumn.column,
    valueColumn: valueColumn.column,
    rawAggregateGroups: oracle.groups,
  });
  const restoredOracle = runRawFullPopulationPivotOracle(expectedGeneration);
  assert.deepEqual(restoredOracle.groups, oracle.groups, 'Full raw status/category membership and numeric aggregates must remain identical after removal and reload');
  recordRequirement(requiredChecksByCase[reportCase][7], boundedCount.rawSourceRows === oracle.sourceRows
    && boundedCount.boundedPreviewRows === Math.min(25, oracle.sourceRows)
    && reloadPreviewCount.rawSourceRows === oracle.sourceRows
    && reloadPreviewCount.boundedPreviewRows === Math.min(25, oracle.sourceRows)
    && JSON.stringify(document.rows) === JSON.stringify(prePivotDocument.rows)
    && document.construction.steps.length === 0
    && builder.catalog.generation === expectedGeneration
    && restoredOracle.sourceRows === oracle.sourceRows
    && JSON.stringify(restoredOracle.groups) === JSON.stringify(oracle.groups)
    && restoredBoundedRawTuples.length === reloadPreviewIDs.length,
  { rawSourceRows: oracle.sourceRows, removalPreviewRows: removePreview.rowCount, removalPreviewIDs: boundedIDs, reloadPreviewRows: reloadPreview.rowCount, reloadPreviewIDs, restoredRowsDefinition: document.rows, generation: builder.catalog.generation, snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, rawGroupsUnchanged: true, reloadedRawBoundedTuples: restoredBoundedRawTuples });

  const lifecycleActions = report.cases.filter(item => typeof item.durationMs === 'number');
  assert.equal(lifecycleActions.length, 14, `Full CDA lifecycle must record all 14 native action and validation timings, found ${lifecycleActions.length}`);
  assert(lifecycleActions.every(item => item.durationMs <= 5000), 'Every full CDA Pivot lifecycle transition must complete within five seconds');
  recordRequirement(requiredChecksByCase[reportCase][8], lifecycleActions.length === 14 && lifecycleActions.every(item => item.durationMs <= 5000),
    { actionCount: lifecycleActions.length, actions: lifecycleActions });
  report.fullPopulationLifecycle.restoredSourceRows = oracle.sourceRows;
  report.fullPopulationLifecycle.restoredPreviewRowsConsumed = boundedCount.boundedPreviewRows;
  report.fullPopulationLifecycle.restoredRawGroupCount = restoredOracle.groups.length;
  report.fullPopulationLifecycle.editedDuplicatePolicy = 'MAX';
  report.fullPopulationLifecycle.editedHeading = editedHeading;
  report.fullPopulationLifecycle.editedWitnessSum = witnessSum;
  report.fullPopulationLifecycle.editedWitnessMax = witnessMax;
  report.fullPopulationLifecycle.lifecycle = ['Preview', 'Cancel', 'Apply', 'reload', 'edit heading/policy', 'Apply', 'reload', 'remove', 'restore', 'reload'];
};
  try {
  if (isFixture) {
    fixtureDigest = fixtureSourceDigest(fixtureDirectory);
    report.ownedFixtureProject = { project, generation: expectedGeneration, fixtureDirectory, fixtureSeed: context.seed, createdFresh: context.seed?.fresh === true && context.seed?.reused === false };
    assert(report.ownedFixtureProject.createdFresh, 'Bounded Pivot lifecycle must use a fresh runner-owned verification project');
  }
  root ??= `/api/v1/projects/${project}/explorers`;
  base ??= `${root}/${explorer}/authoring/v2`;
  apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiBuildContainer));
  await api(root, { name: explorer, title: 'Root quantity category QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, expectedGeneration);
  const oracle = isFixture
    ? await runFixtureOracle(builder.catalog.generation)
    : mode === 'full-population-lifecycle'
      ? runRawFullPopulationPivotOracle(builder.catalog.generation)
      : runRawCategoryOracle(builder.catalog.generation);
  report.oracle = oracle;
  if (mode === 'full-population-lifecycle') {
    recordRequirement(requiredChecksByCase[reportCase][0], oracle.scope.project === project && oracle.scope.generation === expectedGeneration
      && oracle.sourceRows === oracle.groups.reduce((sum, group) => sum + group.rowCount, 0)
      && oracle.groups.length > 0,
    { project, generation: expectedGeneration, sourceRows: oracle.sourceRows, groupCount: oracle.groups.length, scope: oracle.scope });
    recordRequirement(requiredChecksByCase[reportCase][1], oracle.duplicateWitness?.rowCount > 1
      && oracle.duplicateWitness?.numericCount > 1
      && Math.abs(oracle.duplicateWitness.valueSum - oracle.duplicateWitness.valueMax) > 1e-9,
    { duplicateWitness: oracle.duplicateWitness });
  } else if (mode === 'full-population') {
    recordRequirement(requiredChecksByCase[reportCase][0], oracle.scope.project === project && oracle.scope.generation === expectedGeneration && oracle.sourceRows > 0,
      { project, generation: expectedGeneration, sourceRows: oracle.sourceRows, scope: oracle.scope });
  }

  const rootNode = builder.catalog.nodes.find(node => node.resourceType === resourceType);
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode?.nodeId && candidate.fieldPath === 'id');
  assert(rootNode && idField, 'The current catalog must expose the Observation root and its ID field');
  const quantityCodeField = !isFullCda || mode === 'full-population-lifecycle'
    ? builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'valueQuantity.code')
    : undefined;
  const quantityValueField = !isFullCda || mode === 'full-population-lifecycle'
    ? builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'valueQuantity.value')
    : undefined;
  if (!isFullCda || mode === 'full-population-lifecycle') assert(quantityCodeField && quantityValueField, 'The source catalog must expose ordinary valueQuantity.code and valueQuantity.value fields');
  await command([{ type: 'CREATE_TABLE', title: 'Root Observation quantity QA', rootNodeId: rootNode.nodeId }]);
  outputId = builder.workspace.documents[0]?.output.id;
  assert(outputId);
  const rootColumns = [{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID',
  }];
  if (!isFullCda || mode === 'full-population-lifecycle') {
    rootColumns.push({ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: quantityCodeField.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Quantity Code' });
    rootColumns.push({ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: quantityValueField.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Quantity Value' });
  }
  for (const rootColumn of rootColumns) await command([rootColumn]);

  browserRequestCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
    responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries|construction-capabilities)$/,
  });
  page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });
  await gotoPage(page, browserURL({ uiUrl: uiOrigin }, project, explorer, 'builder'));
  await waitForObservable(page, outputId => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), outputId);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false));
  await clickNative(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled===false));
  const reshapeActions = await inspectPage(page, () => {
const menu=document.querySelector('[aria-label="Choose a row change"]');return [...(menu?.querySelectorAll('button')??[])].map(button=>({testId:button.dataset.testid??'',label:button.innerText.trim().replace(/\s+/g,' ').slice(0,140),disabled:button.disabled}));
});
  report.reshapeActions = reshapeActions;
  const genericPivotAction = reshapeActions.find(action => action.testId === 'construction-action-table-pivot-rows' && !action.disabled);
  assert(genericPivotAction, 'Generic source Pivot must be enabled after capability discovery settles');
  const pivotActionTestId = 'construction-action-table-pivot-rows';
  report.pivotEntryControl = pivotActionTestId;
  await clickNative(page, `[data-testid="${pivotActionTestId}"]`);
  await waitForObservable(page, () => Boolean(Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))));

  const prePivotWorkspace = builder.workspace;
  const prePivotDocument = builder.workspace.documents.find(document => document.output.id === outputId);
  assert(prePivotDocument, 'Fresh root fixture must retain its base Observation table before Pivot');
  const groupPath = mode === 'full-population' ? 'Observation.id' : 'Observation.status';
  const groupSourceValue = await selectPivotSource('Add pivot group field', groupPath);
  const categorySourceValue = await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const startedAt = Date.now();
  const requestOffset = report.authoringRequests.length;
  const valueSourceValue = await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForObservable(page,
    () => Boolean((()=>{const editor=document.querySelector('[data-testid="construction-reshape-pivot"]');const text=editor?.innerText??'';return !text.includes('Finding categories')&&(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))||text.includes('Some records have no category field')||text.includes('more than 256 category values'))})()),
    15000);
  await drainResponseReads();
  const discovery = report.authoringRequests.slice(requestOffset).findLast(request => request.endpoint === 'construction-category-discoveries');
  const editorState = await inspectPage(page, () => {
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
      report.domainStatus = 'failed';
    report.productFailure = 'The root category domain exceeds the supported Pivot category limit.';
    report.error = report.productFailure;
    throw new Error(report.productFailure ?? 'Root quantity Pivot workflow did not pass');
  } else if (discovery.response?.outcome === 'MISSING_UNSUPPORTED') {
    assert(oracle.missingRows > 0, 'MISSING_UNSUPPORTED is valid only when the full scoped raw oracle proves absent category fields');
    assert.equal(discovery.response.complete, false);
    assert.deepEqual(discovery.response.categories, []);
    assert(editorState.text.includes('Some records have no category field'), 'The UI must show the typed MISSING limitation');
    report.categoryCorrectness = 'raw oracle distinguishes MISSING from explicit NULL; API declared MISSING_UNSUPPORTED and exposed the limitation';
      report.domainStatus = 'failed';
    report.productFailure = 'Root Observation quantity Pivot cannot complete while any scoped source row lacks valueQuantity.code.';
    report.error = report.productFailure;
    throw new Error(report.productFailure ?? 'Root quantity Pivot workflow did not pass');
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
    if (mode === 'full-population') {
      recordRequirement(requiredChecksByCase[reportCase][1], JSON.stringify(actual) === JSON.stringify(expected),
        { rawCategoryIdentities: expected, discoveredCategoryIdentities: actual, missingRows: oracle.missingRows, explicitNullRows: oracle.explicitNullRows });
      recordRequirement(requiredChecksByCase[reportCase][2], editorState.summary.startsWith(`Selected ${expected.length} of ${expected.length} categories`)
        && editorState.categoryControls.length === expectedLabels.length
        && editorState.categoryControls.every(item => item.checked),
      { summary: editorState.summary, selectedCount: editorState.categoryControls.filter(item => item.checked).length, categoryLabels: expectedLabels });
      recordRequirement(requiredChecksByCase[reportCase][3], durationMs <= 5000,
        { action: 'typed category discovery to native render', durationMs, rawCategories: expected.length });
    } else if (mode === 'full-population-lifecycle') {
      recordRequirement(requiredChecksByCase[reportCase][2], JSON.stringify(actual) === JSON.stringify(expected)
        && editorState.summary.startsWith(`Selected ${expected.length} of ${expected.length} categories`)
        && editorState.categoryControls.every(item => item.checked),
      { rawCategoryIdentities: expected, discoveredCategoryIdentities: actual, summary: editorState.summary, selectedCount: editorState.categoryControls.filter(item => item.checked).length });
    }
    if (mode === 'fixture-lifecycle') {
      assert.equal(oracle.sourceRows, 4, 'Bounded lifecycle oracle must contain exactly four independent Observation records');
      assert.equal(oracle.missingRows, 1);
      assert.equal(oracle.explicitNullRows, 1);
      assert.equal(oracle.presentStringRows, 2);
      recordRequirement(requiredChecksByCase[reportCase][0], report.ownedFixtureProject?.createdFresh === true
        && JSON.stringify(oracle.fixtureRows.map(row => row.id).sort()) === JSON.stringify(['quantity-pivot-missing', 'quantity-pivot-null', 'quantity-pivot-string-a', 'quantity-pivot-string-b']),
      { project, generation: expectedGeneration, sourceIDs: oracle.fixtureRows.map(row => row.id).sort(), fixtureDigest: oracle.fixtureDigest });
      await runFixtureLifecycle(discovery, oracle, prePivotWorkspace, prePivotDocument, requestOffset);
      await drainResponseReads();
      assert.equal(report.expectedAuthoringValidations?.length, 2, 'Only the two explicit ERROR-policy previews should be classified as expected validation');
      recordRequirement(requiredChecksByCase[reportCase][1], report.expectedAuthoringValidations.every(validation =>
        validation.code === 'TABLE_PIVOT_CELL_CARDINALITY' && validation.duplicatePolicy === 'ERROR'
          && validation.project === project && validation.outputId === outputId && validation.rawDuplicateWitnessIDs?.length === 2),
      { validations: report.expectedAuthoringValidations });
      recordRequirement(requiredChecksByCase[reportCase][2], report.fixtureLifecycle.initialExpected?.[0]?.missing_value === 3
        && report.fixtureLifecycle.initialExpected?.[0]?.null_value === 5
        && report.fixtureLifecycle.initialExpected?.[0]?.d === 6,
      { expectedRows: report.fixtureLifecycle.initialExpected });
      assert.deepEqual(report.browserErrors.runtime, [], `Browser runtime exceptions after lifecycle: ${JSON.stringify(report.browserErrors.runtime)}`);
      assert.deepEqual(report.browserErrors.console, [], `Browser console errors after lifecycle: ${JSON.stringify(report.browserErrors.console)}`);
      assert.deepEqual(report.browserErrors.network, [], `Unexpected browser network failures after lifecycle: ${JSON.stringify(report.browserErrors.network)}`);
      const expectedValidationRequestIDs = new Set((report.expectedAuthoringValidations ?? []).map(validation => validation.requestId));
      const failedAuthoringRequests = report.authoringRequests.filter(request => typeof request.status === 'number' && request.status >= 400 && !expectedValidationRequestIDs.has(request.requestId));
      assert.deepEqual(failedAuthoringRequests, [], `Unexpected authoring request errors after lifecycle: ${JSON.stringify(failedAuthoringRequests)}`);
      report.fixtureLifecycle.finalErrors = { runtime: [], console: [], network: [], authoringHTTP: [] };
      const timedActions = report.cases.filter(item => typeof item.durationMs === 'number');
      recordRequirement(requiredChecksByCase[reportCase][7], timedActions.length === 14 && timedActions.every(item => item.durationMs <= 5000),
        { actionCount: timedActions.length, actions: timedActions });
      report.domainStatus = 'passed';
    } else if (mode === 'full-population-lifecycle') {
      await runFullPopulationLifecycle(discovery, oracle, prePivotWorkspace, prePivotDocument, requestOffset);
      await drainResponseReads();
      assert.deepEqual(report.browserErrors.runtime, [], `Browser runtime exceptions after full lifecycle: ${JSON.stringify(report.browserErrors.runtime)}`);
      assert.deepEqual(report.browserErrors.console, [], `Browser console errors after full lifecycle: ${JSON.stringify(report.browserErrors.console)}`);
      const finalNetworkFailures = report.browserErrors.network.filter(failure => !failure.canceled);
      assert.deepEqual(finalNetworkFailures, [], `Unexpected browser network failures after full lifecycle: ${JSON.stringify(finalNetworkFailures)}`);
      const expectedValidationRequestIDs = new Set((report.expectedAuthoringValidations ?? []).map(validation => validation.requestId));
      const failedAuthoringRequests = report.authoringRequests.filter(request => typeof request.status === 'number' && request.status >= 400 && !request.loadingFailure?.canceled && !expectedValidationRequestIDs.has(request.requestId));
      assert.deepEqual(failedAuthoringRequests, [], `Unexpected authoring request errors after full lifecycle: ${JSON.stringify(failedAuthoringRequests)}`);
      report.fullPopulationLifecycle.finalErrors = { runtime: [], console: [], network: [], authoringHTTP: [] };
      report.domainStatus = 'passed';
    } else {
      report.domainStatus = 'passed';
    }
  } else {
    assert.fail(`Unexpected root category discovery outcome: ${JSON.stringify(discovery.response)}`);
    }
  } catch (error) {
    report.domainStatus = 'failed';
    report.error = String(error.stack ?? error);
    workflowError ??= error;
    try {
      report.failureDiagnostics = await inspectPage(page, () => ({ url: location.href, title: document.title, bodyText: (document.body?.innerText ?? '').slice(0, 5000), actions: [...document.querySelectorAll('[data-testid^=\"construction-action-\"]')].map(button => ({ testId: button.dataset.testid ?? '', label: button.innerText.trim(), disabled: button.disabled })), selects: [...document.querySelectorAll('select')].map(select => ({ label: select.getAttribute('aria-label'), value: select.value, disabled: select.disabled })), statusMessages: [...document.querySelectorAll('[role=\"status\"],[role=\"alert\"]')].map(node => node.innerText.trim()).filter(Boolean) }));
    } catch (diagnosticError) { report.failureDiagnosticsError = String(diagnosticError); }
  } finally {
  await drainResponseReads();
  if (isFixture && fixtureDigest) {
    const afterDigest = fixtureSourceDigest(fixtureDirectory);
    report.fixtureSourceFreeze = {
      before: fixtureDigest,
      after: afterDigest,
      unchanged: fixtureDigest === afterDigest,
      invalidatesRun: fixtureDigest !== afterDigest,
      productFailure: false,
    };
    if (fixtureDigest !== afterDigest) {
      report.priorStatus = report.domainStatus;
      report.domainStatus = 'invalidated';
      report.error = 'The independent quantity Pivot fixture changed during the browser run.';
      workflowError ??= new Error('Root quantity Pivot source/runtime identity changed');
    }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = await sourceFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.domainStatus;
      report.domainStatus = 'invalidated';
      report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false };
      workflowError ??= new Error('Root quantity Pivot source/runtime identity changed');
    }
  }
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.domainStatus;
      report.domainStatus = 'invalidated';
      report.apiBuildFreeze = { unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
      workflowError ??= new Error('Root quantity Pivot source/runtime identity changed');
    }
  }
  const afterFingerprint = sourceFingerprint(sourceRoot);
  report.sourceFingerprint.after = afterFingerprint;
  report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === afterFingerprint.sha256 && report.sourceFingerprint.before.files === afterFingerprint.files;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.domainStatus;
    report.domainStatus = 'invalidated';
    report.error = 'Source fingerprint changed during the browser run.';
    workflowError ??= new Error('Root quantity Pivot source/runtime identity changed');
  }
  const sourceAndRuntimeStable = report.domainStatus === 'passed'
    && report.sourceFreeze?.unchanged === true
    && report.apiBuildFreeze?.unchanged === true
    && report.sourceFingerprint.unchanged === true
    && (!isFixture || report.fixtureSourceFreeze?.unchanged === true)
    && (isFixture || (project === 'loom_dev_cda_fhir' && expectedGeneration === 'cda-fhir-v1'))
    && report.browserErrors.runtime.length === 0
    && report.browserErrors.console.length === 0
    && report.browserErrors.network.every(failure => failure.canceled)
    && report.authoringRequests.every(request => !Number.isInteger(request.status) || request.status < 400
      || (report.expectedAuthoringValidations ?? []).some(validation => validation.requestId === request.requestId));
  const stabilityRequirementIndex = requiredChecksByCase[reportCase].length - 1;
  recordFact(requiredChecksByCase[reportCase][stabilityRequirementIndex], sourceAndRuntimeStable, {
    sourceFreeze: report.sourceFreeze,
    apiBuildFreeze: report.apiBuildFreeze,
    sourceFingerprint: report.sourceFingerprint,
    fixtureSourceFreeze: report.fixtureSourceFreeze,
    project,
    expectedGeneration,
    browserErrors: report.browserErrors,
  });
  report.missingRequiredChecks = report.requiredChecks.filter(name => !report.assertions.some(assertion => assertion.name === name && assertion.status === 'passed'));
  if (report.missingRequiredChecks.length > 0 && report.domainStatus === 'passed') {
    report.domainStatus = 'failed';
    report.error = `Required checks were not recorded as passed: ${report.missingRequiredChecks.join('; ')}`;
    workflowError ??= new Error(report.error);
  }
  report.finished = new Date().toISOString();
  report.finishedAt = report.finished;
  for (const failure of report.validationWaitFailures ?? []) refreshValidationWaitFailureRequests(failure, report.authoringRequests);

  }
  if (workflowError) throw workflowError;
}
