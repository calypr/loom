import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';
import { expect } from '@playwright/test';
import { createDevSession, createFreshVerificationFixture, assertOwnedDevSession } from '../../loom-dev.mjs';
import {
  readBasicRuntimeScopeIsolationOracle,
  assertVisibleRowsMatchRuntimeScope,
  inspectRuntimeEnvelopeIdentity,
} from '../helpers/basic-runtime-scope-isolation-oracle.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';
import { browserURL } from './builder-url.mjs';

const ACTIVE_GENERATION = 'scope-001-active';
const FOREIGN_BASE_PROJECT = 'loom_dev_scope_001_foreign';
const GRAPHQL_PATH = '/graphql/graph';
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;

const requestJSON = async (url, options = {}, timeoutMs = 30_000) => {
  const response = await fetch(url, { ...options, signal: AbortSignal.timeout(timeoutMs) });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : {}; } catch { value = { text }; }
  return { response, value };
};

const generationStatusURL = (target, generation) =>
  `${target.apiUrl}/api/v1/datasets/${encodeURIComponent(target.fixtureProject)}/generations/${encodeURIComponent(generation)}`;

const uploadGeneration = async (target, generation, fixtureDir) => {
  const statusURL = generationStatusURL(target, generation);
  const before = await fetch(statusURL, { signal: AbortSignal.timeout(5000) });
  assert.equal(before.status, 404, `scope fixture generation must be new: ${target.fixtureProject}/${generation}`);

  const form = new FormData();
  for (const name of ['Observation.ndjson', 'Patient.ndjson']) {
    form.append('file', new Blob([readFileSync(join(fixtureDir, name))]), name);
  }
  form.append('defer_activation', 'false');
  const submitted = await requestJSON(statusURL, { method: 'POST', body: form }, 180_000);
  assert(submitted.response.ok, `alternate generation upload returned HTTP ${submitted.response.status}: ${JSON.stringify(submitted.value).slice(0, 500)}`);
  assert.equal(submitted.value.project, target.fixtureProject);
  assert.equal(submitted.value.generation, generation);
  assert.equal(submitted.value.activated, true, 'alternate generation must become the active generation before authoring');

  const started = Date.now();
  let lastState = 'not visible';
  while (Date.now() - started < 180_000) {
    const status = await requestJSON(statusURL, {}, 10_000);
    if (status.response.ok) {
      lastState = String(status.value.state ?? 'unknown').toUpperCase();
      if (['STAGED', 'READY', 'ACTIVE'].includes(lastState)) return { submitted: submitted.value, status: status.value };
      if (['FAILED', 'ERROR'].includes(lastState)) throw new Error(`alternate generation failed: ${JSON.stringify(status.value).slice(0, 500)}`);
    } else if (status.response.status !== 404) {
      lastState = `HTTP ${status.response.status}`;
    }
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  throw new Error(`timed out waiting for alternate generation ${generation}: ${lastState}`);
};

const makeForeignProject = async (target, runID) => {
  const fixtureDir = join(target.sourceRoot, 'testdata/scope-001-runtime-scope-isolation/foreign-project');
  const session = createDevSession({
    ...process.env,
    LOOM_DEV_SOURCE_ROOT: target.sourceRoot,
    LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
    LOOM_DEV_API_PORT: String(target.apiPort),
    LOOM_DEV_UI_PORT: String(target.uiPort),
    LOOM_DEV_API_URL: target.apiUrl,
    LOOM_DEV_UI_URL: target.uiUrl,
    LOOM_DEV_PROJECT: FOREIGN_BASE_PROJECT,
    LOOM_DEV_GENERATION: ACTIVE_GENERATION,
    LOOM_DEV_FIXTURE_DIR: fixtureDir,
    LOOM_DEV_ARTIFACTS: target.artifacts,
  }, target.sourceRoot);
  await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
  const fixture = await createFreshVerificationFixture(session, `scope-${runID.slice(-8)}`);
  assert.notEqual(fixture.target.fixtureProject, target.fixtureProject, 'foreign scope must be a separate verification project');
  assert.equal(fixture.target.fixtureGeneration, ACTIVE_GENERATION);
  return fixture.target;
};

const requestBody = request => {
  try { return request.postDataJSON(); } catch { return null; }
};

const selectorMatches = (left, right) => Boolean(left && right &&
  left.recipe === right.recipe && left.translationVersion === right.translationVersion && left.output === right.output);

const fixtureFileHashes = paths => Object.fromEntries(paths.map(path => [
  path, createHash('sha256').update(readFileSync(path)).digest('hex'),
]));

const readLoadedScopeWitnesses = ({ target, project, primaryGeneration, foreignProject }) => {
  const query = `LET primary = (
    FOR r IN Patient
      FILTER r.project == @primaryProject AND r.dataset_generation == @primaryGeneration
        AND r.id IN @primaryIDs AND r.payload.resourceType == "Patient"
      SORT r.id
      RETURN {_id: r._id, id: r.id, project: r.project, generation: r.dataset_generation, resourceType: r.payload.resourceType}
  )
  LET active = (
    FOR r IN Patient
      FILTER r.project == @primaryProject AND r.dataset_generation == @activeGeneration
        AND r.id IN @activeIDs AND r.payload.resourceType == "Patient"
      SORT r.id
      RETURN {_id: r._id, id: r.id, project: r.project, generation: r.dataset_generation, resourceType: r.payload.resourceType}
  )
  LET foreign = (
    FOR r IN Patient
      FILTER r.project == @foreignProject AND r.dataset_generation == @activeGeneration
        AND r.id IN @foreignIDs AND r.payload.resourceType == "Patient"
      SORT r.id
      RETURN {_id: r._id, id: r.id, project: r.project, generation: r.dataset_generation, resourceType: r.payload.resourceType}
  )
  RETURN {primary, active, foreign}`;
  const bindVars = {
    primaryProject: project,
    primaryGeneration,
    primaryIDs: ['dev-patient-001', 'dev-patient-002'],
    activeGeneration: ACTIVE_GENERATION,
    activeIDs: ['scope-active-patient-001'],
    foreignProject: foreignProject.fixtureProject,
    foreignIDs: ['scope-foreign-patient-001'],
  };
  const marker = '__LOOM_SCOPE_WITNESSES__';
  const script = `print(${JSON.stringify(marker)} + JSON.stringify(db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}, {maxRuntime: 5, memoryLimit: 16777216}).toArray()));`;
  const invocation = buildArangoShellInvocation({
    container: `${target.composeProject}-arangodb-1`,
    database: 'loom_dev',
    script,
  });
  const result = spawnSync(invocation.command, invocation.args, {
    encoding: 'utf8', timeout: 30_000, maxBuffer: 1024 * 1024,
  });
  assert(!result.error, `scoped raw Patient witness query failed to start: ${result.error?.message}`);
  assert.equal(result.status, 0, `scoped raw Patient witness query failed: ${String(result.stderr).slice(0, 1000)}`);
  const markerIndex = result.stdout.indexOf(marker);
  assert(markerIndex >= 0, 'scoped raw Patient witness query did not return its JSON marker');
  const jsonLine = result.stdout.slice(markerIndex + marker.length).split(/\r?\n/, 1)[0];
  const values = JSON.parse(jsonLine);
  assert.equal(values.length, 1, 'scoped raw Patient witness query must return one grouped result');
  return values[0];
};

export const basicRuntimeScopeIsolationWorkflow = async ({ page, report, action, check }, context) => {
  const target = context.target;
  const project = target.fixtureProject;
  const primaryGeneration = target.fixtureGeneration;
  const activeFixtureDir = join(target.sourceRoot, 'testdata/scope-001-runtime-scope-isolation/active-generation');
  const foreignProject = await makeForeignProject(target, context.runID);
  const oracle = await readBasicRuntimeScopeIsolationOracle({
    primaryFixtureDir: target.fixtureDir,
    activeGenerationFixtureDir: activeFixtureDir,
    foreignProjectFixtureDir: foreignProject.fixtureDir,
    primaryProject: project,
    primaryGeneration,
    activeGeneration: ACTIVE_GENERATION,
    foreignProject: foreignProject.fixtureProject,
  });
  const fixturePaths = [
    join(target.fixtureDir, 'Patient.ndjson'),
    join(target.fixtureDir, 'Observation.ndjson'),
    join(activeFixtureDir, 'Patient.ndjson'),
    join(activeFixtureDir, 'Observation.ndjson'),
    join(foreignProject.fixtureDir, 'Patient.ndjson'),
    join(foreignProject.fixtureDir, 'Observation.ndjson'),
  ];
  const fixtureHashesBefore = fixtureFileHashes(fixturePaths);
  check('correctness', 'literal fixtures identify three disjoint Basic project-generation scopes',
    oracle.primary.project === oracle.selected.project && oracle.primary.generation !== oracle.selected.generation &&
    oracle.foreign.project !== oracle.selected.project && oracle.foreign.generation === oracle.selected.generation,
    { primary: { project, generation: primaryGeneration, ids: oracle.primary.ids },
      selected: { project, generation: ACTIVE_GENERATION, ids: oracle.selected.ids },
      foreign: { project: foreignProject.fixtureProject, generation: ACTIVE_GENERATION, ids: oracle.foreign.ids } });

  const alternateLoad = await uploadGeneration(target, ACTIVE_GENERATION, activeFixtureDir);
  check('correctness', 'the exact second generation is loaded and activated in the primary project',
    alternateLoad.submitted.project === project && alternateLoad.submitted.generation === ACTIVE_GENERATION &&
    alternateLoad.submitted.activated === true && ['STAGED', 'READY', 'ACTIVE'].includes(String(alternateLoad.status.state).toUpperCase()),
    { project, generation: ACTIVE_GENERATION, activated: alternateLoad.submitted.activated, state: alternateLoad.status.state });
  check('correctness', 'the foreign project contains the exact distinct active-generation sentinel',
    foreignProject.fixtureProject !== project && foreignProject.fixtureGeneration === ACTIVE_GENERATION &&
    oracle.foreign.ids.length === 1 && oracle.foreign.ids[0] === 'scope-foreign-patient-001',
    { project: foreignProject.fixtureProject, generation: foreignProject.fixtureGeneration, ids: oracle.foreign.ids });
  await assertOwnedDevSession(target, { uiRouting: 'explicit-query' });
  const loadedWitnesses = readLoadedScopeWitnesses({
    target, project, primaryGeneration, foreignProject,
  });
  const compactWitnesses = Object.fromEntries(Object.entries(loadedWitnesses).map(([scope, rows]) => [
    scope, rows.map(({ id, project: rowProject, generation, resourceType }) => ({
      id, project: rowProject, generation, resourceType,
    })),
  ]));
  check('correctness', 'old-generation and foreign-project sentinel documents remain loaded after alternate activation',
    JSON.stringify(compactWitnesses) === JSON.stringify({
      primary: [
        { id: 'dev-patient-001', project, generation: primaryGeneration, resourceType: 'Patient' },
        { id: 'dev-patient-002', project, generation: primaryGeneration, resourceType: 'Patient' },
      ],
      active: [{ id: 'scope-active-patient-001', project, generation: ACTIVE_GENERATION, resourceType: 'Patient' }],
      foreign: [{ id: 'scope-foreign-patient-001', project: foreignProject.fixtureProject,
        generation: ACTIVE_GENERATION, resourceType: 'Patient' }],
    }) && Object.values(loadedWitnesses).flat().every(row => typeof row._id === 'string' && row._id.includes('/')) &&
      new Set(Object.values(loadedWitnesses).flat().map(row => row._id)).size === 4,
    { witnesses: loadedWitnesses, queryScope: 'exact project, dataset_generation, and Patient id predicates' });

  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  const openExplorer = page.getByText('New explorer', { exact: true });
  await action('open fresh scope-isolation Explorer', openExplorer, () => openExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
  });
  const title = `Scope ${context.runID.slice(-10)} runtime isolation`;
  const nameExplorer = page.locator('#new-explorer-name');
  await action('name scope-isolation Explorer', nameExplorer, () => nameExplorer.fill(title), { editable: true });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create scope-isolation Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const picker = page.getByRole('combobox', { name: 'Explorer' });
      await picker.waitFor({ state: 'visible' });
      await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected, title);
    },
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'scope-isolation lifecycle must use its newly created Explorer');
  report.target.explorer = explorer;
  report.target.project = project;
  report.target.primaryGeneration = primaryGeneration;
  report.target.selectedGeneration = ACTIVE_GENERATION;
  report.target.foreignProject = foreignProject.fixtureProject;
  report.target.foreignGeneration = foreignProject.fixtureGeneration;

  const tableName = page.locator('#first-table-name');
  await action('name selected-generation Patient table', tableName, () => tableName.fill('Active Generation Patients'), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  const previewStarted = performance.now();
  await action('select active-generation Patient rows and render Preview', choosePatients, () => choosePatients.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
      await page.waitForFunction(count => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(count + 1), oracle.selected.count, { timeout: 5000 });
      await waitForBuilderRenderedGrid(page, {
        tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
        expectedRows: oracle.selected.ids.map(id => ({ 'Patient ID': id })),
      });
    },
    requiredCheck: 'native Preview renders the exact active-generation Patient identity within five seconds',
  });
  const previewRows = await page.locator('[data-testid="preview-table-scroll"] [role="table"] [role="row"]').allInnerTexts();
  const previewIDs = assertVisibleRowsMatchRuntimeScope({
    rows: previewRows.slice(1), selectedIDs: oracle.selected.ids,
    excludedIDs: [...oracle.primary.ids, ...oracle.foreign.ids],
  });
  check('correctness', 'native Builder Preview excludes the other project and generation identities',
    JSON.stringify(previewIDs) === JSON.stringify(oracle.selected.ids),
    { visibleIDs: previewIDs, expectedSelectedIDs: oracle.selected.ids,
      excludedPrimaryGenerationIDs: oracle.primary.ids, excludedForeignProjectIDs: oracle.foreign.ids });
  check('performance', 'native Preview settles exact scope rows within five seconds',
    performance.now() - previewStarted <= 5000,
    { elapsedMs: Math.round(performance.now() - previewStarted), budgetMs: 5000 });

  const uiOrigin = new URL(target.uiUrl).origin;
  const publishURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/publish`;
  const publishResponsePromise = page.waitForResponse(response => response.request().method() === 'POST' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === new URL(publishURL).pathname,
  { timeout: 5000 });
  const publish = page.getByRole('button', { name: 'Publish', exact: true });
  await action('publish the exact active-generation output', publish, () => publish.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      const response = await publishResponsePromise;
      assert(response.status() >= 200 && response.status() < 300, `Publish returned HTTP ${response.status()}`);
      await expect(publish).toBeDisabled({ timeout: 5000 });
    },
    requiredCheck: 'native Publish returns success for the exact selected-generation output within five seconds',
  });
  const publishResponse = await publishResponsePromise;
  check('correctness', 'native Publish succeeded for the selected-generation Explorer', publishResponse.ok(),
    { project, explorer, status: publishResponse.status() });

  const queryStarted = performance.now();
  const expectedRuntimePath = runtimePath(project, explorer);
  const runtimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === expectedRuntimePath,
  { timeout: 5000 });
  const queryRequestPromise = page.waitForRequest(request => {
    try {
      const url = new URL(request.url());
      return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === GRAPHQL_PATH &&
        requestBody(request)?.variables?.input?.projectId === project;
    } catch { return false; }
  }, { timeout: 5000 });
  await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded' });
  const [runtimeResponse, queryRequest] = await Promise.all([runtimeResponsePromise, queryRequestPromise]);
  assert(runtimeResponse.ok(), `Viewer runtime returned HTTP ${runtimeResponse.status()}`);
  const envelope = await runtimeResponse.json();
  const runtime = envelope.runtime;
  assert(runtime && Array.isArray(runtime.outputs), 'Viewer runtime must expose its output catalog');
  const runtimeGeneration = runtime.generation ?? runtime.publication?.generation;
  const envelopeIdentity = inspectRuntimeEnvelopeIdentity(envelope, { project, explorer });
  check('correctness', 'Viewer runtime envelope identifies the exact project and Explorer',
    envelopeIdentity.matches,
    { expectedProject: envelopeIdentity.expectedProject, actualProject: envelope.project,
      expectedExplorer: explorer, actualExplorer: envelope.explorerId });
  check('correctness', 'Viewer runtime is bound to the exact active generation in the selected project',
    runtimeGeneration === ACTIVE_GENERATION && new URL(runtimeResponse.url()).pathname === expectedRuntimePath,
    { project, generation: runtimeGeneration, expectedGeneration: ACTIVE_GENERATION, explorer });
  const input = requestBody(queryRequest)?.variables?.input;
  assert.equal(input?.projectId, project, 'Viewer GraphQL request must remain in the selected project');
  const selectedOutputs = runtime.outputs.filter(output => selectorMatches(output.selector, input?.selector));
  assert.equal(selectedOutputs.length, 1, 'Viewer query must select exactly one output from this Explorer runtime');
  const output = selectedOutputs[0];
  report.target.viewerOutputId = output.outputId;
  check('correctness', 'Viewer query uses one exact output receipt from the selected Explorer',
    typeof output.outputId === 'string' && output.outputId.length > 0 && selectorMatches(output.selector, input.selector),
    { project, explorer, outputId: output.outputId, selector: input.selector });

  const results = page.locator('table[aria-label$=" results"]');
  await results.waitFor({ state: 'visible', timeout: 5000 });
  await expect(results.locator('tbody tr')).toHaveCount(oracle.selected.count, { timeout: 5000 });
  const renderedRows = await results.locator('tbody tr').allInnerTexts();
  const renderedIDs = assertVisibleRowsMatchRuntimeScope({
    rows: renderedRows,
    selectedIDs: oracle.selected.ids,
    excludedIDs: [...oracle.primary.ids, ...oracle.foreign.ids],
  });
  check('correctness', 'Viewer rows equal only the exact selected project-generation source identities',
    JSON.stringify(renderedIDs) === JSON.stringify(oracle.selected.ids),
    { project, generation: ACTIVE_GENERATION, outputId: output.outputId,
      visibleIDs: renderedIDs, expectedSelectedIDs: oracle.selected.ids,
      excludedPrimaryGenerationIDs: oracle.primary.ids, excludedForeignProjectIDs: oracle.foreign.ids });
  check('performance', 'Viewer runtime and exact isolated rows settle within five seconds',
    performance.now() - queryStarted <= 5000,
    { elapsedMs: Math.round(performance.now() - queryStarted), budgetMs: 5000 });

  const reloadStarted = performance.now();
  const reloadRemaining = () => {
    const remaining = 5000 - (performance.now() - reloadStarted);
    assert(remaining > 0, 'Viewer reload exceeded its five-second deadline');
    return remaining;
  };
  const reloadedRuntimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === expectedRuntimePath,
  { timeout: 5000 });
  const reloadedQueryRequestPromise = page.waitForRequest(request => {
    try {
      const url = new URL(request.url());
      const input = requestBody(request)?.variables?.input;
      return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === GRAPHQL_PATH &&
        input?.projectId === project && selectorMatches(input?.selector, output.selector);
    } catch { return false; }
  }, { timeout: 5000 });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: reloadRemaining() });
  const [reloadedRuntimeResponse, reloadedQueryRequest] = await Promise.all([
    reloadedRuntimeResponsePromise, reloadedQueryRequestPromise,
  ]);
  assert(reloadedRuntimeResponse.ok(), `reloaded Viewer runtime returned HTTP ${reloadedRuntimeResponse.status()}`);
  const reloadedEnvelope = await reloadedRuntimeResponse.json();
  const reloadedRuntime = reloadedEnvelope.runtime;
  assert(reloadedRuntime && Array.isArray(reloadedRuntime.outputs), 'reloaded Viewer runtime must expose its output catalog');
  const reloadedEnvelopeIdentity = inspectRuntimeEnvelopeIdentity(reloadedEnvelope, { project, explorer });
  assert(reloadedEnvelopeIdentity.matches, 'Viewer reload must retain the same canonical project and Explorer envelope');
  const reloadedGeneration = reloadedRuntime.generation ?? reloadedRuntime.publication?.generation;
  assert.equal(reloadedGeneration, ACTIVE_GENERATION, 'Viewer reload must retain the selected generation');
  const reloadedInput = requestBody(reloadedQueryRequest)?.variables?.input;
  assert.equal(reloadedInput?.projectId, project, 'Viewer reload query must remain in the selected project');
  assert(selectorMatches(reloadedInput?.selector, output.selector), 'Viewer reload must retain the exact output selector');
  const reloadedOutputs = reloadedRuntime.outputs.filter(candidate =>
    candidate.outputId === output.outputId && selectorMatches(candidate.selector, output.selector));
  assert.equal(reloadedOutputs.length, 1, 'Viewer reload must retain one exact selected output identity');
  const reloadResults = page.locator('table[aria-label$=" results"]');
  await reloadResults.waitFor({ state: 'visible', timeout: reloadRemaining() });
  await expect(reloadResults.locator('tbody tr')).toHaveCount(oracle.selected.count, { timeout: reloadRemaining() });
  const reloadRows = await reloadResults.locator('tbody tr').allInnerTexts();
  const reloadIDs = assertVisibleRowsMatchRuntimeScope({
    rows: reloadRows,
    selectedIDs: oracle.selected.ids,
    excludedIDs: [...oracle.primary.ids, ...oracle.foreign.ids],
  });
  check('correctness', 'Viewer reload retains the same project, generation, output selector, and exact scope exclusions',
    reloadedEnvelopeIdentity.matches && JSON.stringify(reloadIDs) === JSON.stringify(oracle.selected.ids),
    { project: reloadedEnvelope.project, expectedProject: reloadedEnvelopeIdentity.expectedProject,
      explorer: reloadedEnvelope.explorerId,
      generation: reloadedGeneration, outputId: reloadedOutputs[0].outputId,
      selector: reloadedInput.selector, visibleIDs: reloadIDs,
      expectedSelectedIDs: oracle.selected.ids,
      excludedPrimaryGenerationIDs: oracle.primary.ids, excludedForeignProjectIDs: oracle.foreign.ids });
  check('performance', 'Viewer reload restores exact isolated rows within five seconds',
    performance.now() - reloadStarted <= 5000,
    { elapsedMs: Math.round(performance.now() - reloadStarted), budgetMs: 5000 });

  const fixtureHashesAfter = fixtureFileHashes(fixturePaths);
  check('correctness', 'all literal Basic scope fixtures remain unchanged',
    JSON.stringify(fixtureHashesAfter) === JSON.stringify(fixtureHashesBefore),
    { before: fixtureHashesBefore, after: fixtureHashesAfter });
  report.target.runtimeScopeIsolation = {
    selected: { project, generation: ACTIVE_GENERATION, outputId: output.outputId, ids: renderedIDs },
    excluded: [
      { project, generation: primaryGeneration, ids: oracle.primary.ids },
      { project: foreignProject.fixtureProject, generation: ACTIVE_GENERATION, ids: oracle.foreign.ids },
    ],
    scope: 'Basic synthetic fixtures only; CDA and authorization isolation are not established',
  };
};
