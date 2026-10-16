import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientRowDetailsOracle } from '../helpers/viewer-row-details-oracle.mjs';
import { browserURL } from './builder-url.mjs';

const outputId = 'patients';
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;

const workspaceForBasicPatient = oracle => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Basic Patient row details' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients', rowLabel: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [{
      columnId: 'patient-id',
      column: oracle.patientIDColumn,
      label: 'Patient ID',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: oracle.patientIDSourcePath, projectionMode: 'VALUE' } },
      table: { visible: true, order: 0 },
    }],
  }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const requestBody = request => {
  try { return request.postDataJSON(); }
  catch { return null; }
};

const selectorsMatch = (actual, expected) => actual?.recipe === expected?.recipe &&
  actual?.translationVersion === expected?.translationVersion && actual?.output === expected?.output;

const matchingGraphRequest = (request, { origin, project, output, columns, selector }) => {
  try {
    const url = new URL(request.url());
    const input = requestBody(request)?.variables?.input;
    return request.method() === 'POST' && url.origin === origin && url.pathname === '/graphql/graph' &&
      input?.projectId === project && input.selector?.output === output &&
      isDeepStrictEqual(input.columns, columns) && (!selector || selectorsMatch(input.selector, selector));
  } catch { return false; }
};

const rowIDsFromConnection = connection => {
  const columns = Array.isArray(connection?.columns) ? connection.columns : [];
  const rows = Array.isArray(connection?.rows) ? connection.rows : [];
  const columnIndex = columns.indexOf('patient_id');
  return rows.map(row => Array.isArray(row) ? row[columnIndex] : row?.patient_id);
};

const detailValue = async (dialog, expect) => {
  await expect(dialog).toBeVisible({ timeout: 5_000 });
  const field = dialog.locator('dt').filter({ hasText: /^patient_id$/ });
  await expect(field).toHaveCount(1, { timeout: 5_000 });
  const value = field.locator('xpath=following-sibling::dd[1]');
  await expect(value).toHaveCount(1, { timeout: 5_000 });
  return value.innerText({ timeout: 5_000 });
};

export const viewerRowDetailsWorkflow = async ({ page, report, action, check }, context) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const explorer = 'default';
  const oracle = readBasicPatientRowDetailsOracle(target.fixtureDir);
  report.target.project = project;
  report.target.fixtureProjectAlias = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorer;
  report.target.viewerOutputId = outputId;
  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: target.sourceRoot,
    encoding: 'utf8',
  }).trim();
  const sourceHashesBefore = { patient: oracle.patientSHA256, recipe: oracle.recipeSHA256 };
  report.target.fixtureOracle = {
    patientPath: oracle.patientPath,
    patientSHA256: oracle.patientSHA256,
    recipePath: oracle.recipePath,
    recipeSHA256: oracle.recipeSHA256,
    patientIDs: oracle.patientIDs,
    projectedColumn: oracle.patientIDColumn,
    sourcePath: oracle.patientIDSourcePath,
  };

  const configPath = repositoryConfigPath(project, generation);
  const configured = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Loom-Source-Commit': sourceCommit,
    },
    body: JSON.stringify(workspaceForBasicPatient(oracle)),
    signal: AbortSignal.timeout(30_000),
  });
  const configuredText = await configured.text();
  let configuredBody;
  try { configuredBody = JSON.parse(configuredText); }
  catch { configuredBody = { body: configuredText.slice(0, 1_000) }; }
  const configuredCorrectly = configured.ok && configuredBody.project === canonicalProject &&
    configuredBody.generation === generation && configuredBody.explorerId === 'default' && configuredBody.activated === true;
  check('correctness', 'Basic Patient ID output publishes to the exact fixture generation', configuredCorrectly,
    { status: configured.status, path: configPath, project, canonicalProject, generation, explorer: configuredBody.explorerId, response: configuredBody });

  const origin = new URL(target.uiUrl).origin;
  const runtimeRequestPath = runtimePath(project, explorer);
  const runtimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === origin && new URL(response.url()).pathname === runtimeRequestPath, { timeout: 5_000 });
  const graphResponsePromise = page.waitForResponse(response => matchingGraphRequest(response.request(), {
    origin, project, output: outputId, columns: [oracle.patientIDColumn],
  }), { timeout: 5_000 });
  const initialRowsStarted = performance.now();
  await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded', timeout: 5_000 });
  const [runtimeResponse, graphResponse] = await Promise.all([runtimeResponsePromise, graphResponsePromise]);
  const runtimeEnvelope = await runtimeResponse.json();
  const runtime = runtimeEnvelope.runtime;
  const runtimeOutput = runtime?.outputs?.find(output => output.outputId === outputId);
  const runtimeGeneration = runtime?.generation ?? runtime?.publication?.generation;
  const runtimeMatchesFixture = runtimeResponse.status() >= 200 && runtimeResponse.status() < 300 &&
    runtimeEnvelope.project === canonicalProject && runtimeEnvelope.explorerId === explorer &&
    runtimeGeneration === generation && runtimeOutput?.outputId === outputId;
  check('correctness', 'Viewer runtime retains the exact fixture project, generation, and default Explorer',
    runtimeMatchesFixture,
    { status: runtimeResponse.status(), project: runtimeEnvelope.project, expectedProject: canonicalProject,
      explorer: runtimeEnvelope.explorerId, expectedExplorer: explorer,
      generation: runtimeGeneration, expectedGeneration: generation, outputId: runtimeOutput?.outputId });
  assert(runtimeOutput, 'published Basic Viewer runtime must contain the Patient output');
  assert(runtimeOutput.selector, 'published Basic Viewer output must contain its exact runtime selector');
  const graphBody = requestBody(graphResponse.request());
  const graphInput = graphBody?.variables?.input;
  const requestMatches = graphResponse.status() >= 200 && graphResponse.status() < 300 &&
    matchingGraphRequest(graphResponse.request(), {
      origin, project, output: outputId, columns: [oracle.patientIDColumn], selector: runtimeOutput.selector,
    });
  const graphPayload = await graphResponse.json();
  const graphErrors = Array.isArray(graphPayload.errors) ? graphPayload.errors : [];
  const connection = graphPayload.data?.dataframeRows;
  const returnedIDs = rowIDsFromConnection(connection);
  const responseMatchesSource = graphErrors.length === 0 && isDeepStrictEqual([...returnedIDs].sort(), [...oracle.patientIDs].sort());
  check('correctness', 'Viewer query returns the exact Patient IDs projected from the Basic recipe',
    requestMatches && responseMatchesSource,
    { status: graphResponse.status(), project: graphInput?.projectId, requestedColumns: graphInput?.columns,
      responseColumns: connection?.columns, patientIDs: returnedIDs, expectedPatientIDs: oracle.patientIDs,
      graphErrorCount: graphErrors.length, selectorMatchesRuntime: requestMatches });

  const table = page.locator('table[aria-label$=" results"]');
  await table.waitFor({ state: 'visible', timeout: 5_000 });
  await page.waitForFunction(ids => {
    const rows = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')].map(row => row.innerText);
    return rows.length === ids.length && ids.every(id => rows.some(text => text.includes(id)));
  }, oracle.patientIDs, { timeout: 5_000 });
  const visibleRows = await table.locator('tbody tr').allInnerTexts();
  const renderedIDs = visibleRows.map(text => oracle.patientIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  const initialRowsMs = Math.round(performance.now() - initialRowsStarted);
  check('correctness', 'Basic Viewer table renders the exact independent Patient IDs',
    isDeepStrictEqual(renderedIDs, [...oracle.patientIDs].sort()),
    { rows: visibleRows, patientIDs: renderedIDs, expectedPatientIDs: oracle.patientIDs });
  check('performance', 'Viewer initial query renders the exact Patient rows within five seconds',
    initialRowsMs <= 5_000 && isDeepStrictEqual(renderedIDs, [...oracle.patientIDs].sort()),
    { elapsedMs: initialRowsMs, budgetMs: 5_000, rows: visibleRows,
      patientIDs: renderedIDs, expectedPatientIDs: oracle.patientIDs });

  const expectedPatientID = oracle.patientIDs[0];
  const trigger = table.getByRole('row').filter({ hasText: expectedPatientID })
    .getByRole('button', { name: expectedPatientID, exact: true });
  const dialog = page.getByRole('dialog', { name: 'Row details' });
  const openAndCheckDetails = async phase => {
    let actual;
    await action(`open ${phase} Patient row details`, trigger, () => trigger.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        actual = await detailValue(dialog, expect);
      },
      requiredCheck: `${phase} Patient row details action-to-render within budget`,
    });
    check('correctness', `${phase} row details display the exact independent patient_id`,
      actual === expectedPatientID,
      { field: oracle.patientIDColumn, expected: expectedPatientID, actual, patientPath: oracle.patientPath,
        recipePath: oracle.recipePath, patientSHA256: oracle.patientSHA256, recipeSHA256: oracle.recipeSHA256 });
  };

  await openAndCheckDetails('initial');
  const close = dialog.getByRole('button', { name: 'Close row details', exact: true });
  await action('close Patient row details', close, () => close.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => expect(dialog).toBeHidden({ timeout: 5_000 }),
    requiredCheck: 'close Patient row details action-to-render within budget',
  });
  await openAndCheckDetails('reopened');

  const reloadStarted = performance.now();
  const reloadRuntimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === origin && new URL(response.url()).pathname === runtimeRequestPath, { timeout: 5_000 });
  const reloadGraphResponsePromise = page.waitForResponse(response => matchingGraphRequest(response.request(), {
    origin, project, output: outputId, columns: [oracle.patientIDColumn], selector: runtimeOutput.selector,
  }), { timeout: 5_000 });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 });
  const [reloadRuntimeResponse, reloadGraphResponse] = await Promise.all([
    reloadRuntimeResponsePromise,
    reloadGraphResponsePromise,
  ]);
  const reloadRuntimeEnvelope = await reloadRuntimeResponse.json();
  const reloadRuntime = reloadRuntimeEnvelope.runtime;
  const reloadRuntimeOutput = reloadRuntime?.outputs?.find(output => output.outputId === outputId);
  const reloadGeneration = reloadRuntime?.generation ?? reloadRuntime?.publication?.generation;
  const reloadRuntimeMatches = reloadRuntimeResponse.status() >= 200 && reloadRuntimeResponse.status() < 300 &&
    reloadRuntimeEnvelope.project === canonicalProject && reloadRuntimeEnvelope.explorerId === explorer &&
    reloadGeneration === generation && selectorsMatch(reloadRuntimeOutput?.selector, runtimeOutput.selector);
  check('correctness', 'Viewer reload runtime retains the exact fixture generation and Patient selector',
    reloadRuntimeMatches,
    { status: reloadRuntimeResponse.status(), project: reloadRuntimeEnvelope.project, expectedProject: canonicalProject,
      explorer: reloadRuntimeEnvelope.explorerId, generation: reloadGeneration, expectedGeneration: generation,
      selector: reloadRuntimeOutput?.selector, expectedSelector: runtimeOutput.selector });

  const dialogClosedAfterReload = await dialog.isHidden();
  check('correctness', 'Viewer row details dialog is closed after reload before reopening', dialogClosedAfterReload,
    { dialogVisible: !dialogClosedAfterReload });

  const reloadGraphBody = requestBody(reloadGraphResponse.request());
  const reloadGraphInput = reloadGraphBody?.variables?.input;
  const reloadRequestMatches = matchingGraphRequest(reloadGraphResponse.request(), {
    origin, project, output: outputId, columns: [oracle.patientIDColumn], selector: runtimeOutput.selector,
  });
  check('correctness', 'Viewer reload request uses the exact project, runtime selector, and patient_id column',
    reloadRequestMatches,
    { status: reloadGraphResponse.status(), project: reloadGraphInput?.projectId, expectedProject: project,
      selector: reloadGraphInput?.selector, expectedSelector: runtimeOutput.selector,
      columns: reloadGraphInput?.columns, expectedColumns: [oracle.patientIDColumn] });
  const reloadGraphPayload = await reloadGraphResponse.json();
  const reloadGraphErrors = Array.isArray(reloadGraphPayload.errors) ? reloadGraphPayload.errors : [];
  const reloadConnection = reloadGraphPayload.data?.dataframeRows;
  const reloadReturnedIDs = rowIDsFromConnection(reloadConnection);
  const reloadResponseMatchesSource = reloadGraphResponse.status() >= 200 && reloadGraphResponse.status() < 300 &&
    reloadGraphErrors.length === 0 &&
    isDeepStrictEqual([...reloadReturnedIDs].sort(), [...oracle.patientIDs].sort());
  check('correctness', 'Viewer reload response returns the exact independent Patient IDs without GraphQL errors',
    reloadResponseMatchesSource,
    { status: reloadGraphResponse.status(), responseColumns: reloadConnection?.columns,
      patientIDs: reloadReturnedIDs, expectedPatientIDs: oracle.patientIDs,
      graphErrorCount: reloadGraphErrors.length });

  const reloadedTable = page.locator('table[aria-label$=" results"]');
  await reloadedTable.waitFor({ state: 'visible', timeout: 5_000 });
  await page.waitForFunction(ids => {
    const rows = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')].map(row => row.innerText);
    return rows.length === ids.length && ids.every(id => rows.some(text => text.includes(id)));
  }, oracle.patientIDs, { timeout: 5_000 });
  const reloadedRows = await reloadedTable.locator('tbody tr').allInnerTexts();
  const reloadedIDs = reloadedRows.map(text => oracle.patientIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  const reloadMs = Math.round(performance.now() - reloadStarted);
  check('performance', 'Viewer reload restores the exact Patient rows within five seconds',
    reloadMs <= 5_000 && isDeepStrictEqual(reloadedIDs, [...oracle.patientIDs].sort()),
    { elapsedMs: reloadMs, budgetMs: 5_000, rows: reloadedRows, patientIDs: reloadedIDs, expectedPatientIDs: oracle.patientIDs });
  await openAndCheckDetails('post-reload');

  const patientBytesAfter = readFileSync(oracle.patientPath);
  const recipeBytesAfter = readFileSync(oracle.recipePath);
  const patientSHA256After = createHash('sha256').update(patientBytesAfter).digest('hex');
  const recipeSHA256After = createHash('sha256').update(recipeBytesAfter).digest('hex');
  const unchangedSources = sourceHashesBefore.patient === patientSHA256After && sourceHashesBefore.recipe === recipeSHA256After;
  check('correctness', 'Basic Patient fixture and recipe stayed unchanged during the row-details lifecycle', unchangedSources,
    { patientPath: oracle.patientPath, recipePath: oracle.recipePath, sourceHashesBefore,
      patientSHA256After, recipeSHA256After });
};
