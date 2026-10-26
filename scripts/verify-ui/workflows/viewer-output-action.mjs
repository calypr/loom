import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import { join } from 'node:path';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { browserURL } from './builder-url.mjs';
import { assertViewerPatientRows } from './viewer-query.mjs';

const outputId = 'patients';
const actionType = 'download-patient-csv';
const actionTitle = 'Download patient CSV';
const fileName = 'patients.csv';
const columns = ['patient_id', 'gender'];
const exportHeaders = { patient_id: 'Patient ID', gender: 'Gender' };
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;

const readPatientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const rows = patients.map(patient => ({ id: patient.id, gender: patient.gender ?? '' })).sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(rows, [
    { id: 'dev-patient-001', gender: 'female' },
    { id: 'dev-patient-002', gender: '' },
  ], 'configured CSV fixture must retain the exact independent Patient identities and gender values');
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    rows,
    expectedCSVRows: rows.map(row => `${row.id},${row.gender}`),
  };
};

const workspaceForAction = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Configured Patient CSV export' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients', rowLabel: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [
      {
        columnId: 'patient-id',
        column: columns[0],
        label: exportHeaders.patient_id,
        logicalType: 'string',
        occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
        table: { visible: true, order: 0 },
      },
      {
        columnId: 'patient-gender',
        column: columns[1],
        label: exportHeaders.gender,
        logicalType: 'string',
        occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } },
        table: { visible: true, order: 1 },
      },
    ],
    actions: [{
      type: actionType,
      title: actionTitle,
      fileName,
      columns: [
        { column: columns[0], exportHeader: exportHeaders.patient_id },
        { column: columns[1], exportHeader: exportHeaders.gender },
      ],
    }],
  }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const matchesSelector = (actual, expected) => actual?.recipe === expected?.recipe &&
  actual?.translationVersion === expected?.translationVersion && actual?.output === expected?.output;

const waitForViewerRows = (page, expectedIDs) => page.waitForFunction(ids => {
  const rows = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')].map(row => row.innerText);
  return rows.length === ids.length && ids.every(id => rows.some(text => text.includes(id)));
}, expectedIDs, { timeout: 5_000 });

const captureAction = async ({ page, observer, performAction, output, project, label }) => {
  const fromIndex = observer.requests.length;
  const responsePromise = observer.waitForResponse(entry => {
    const input = entry.request?.variables?.input;
    return entry.method === 'POST' && entry.path === '/graphql/graph' &&
      input?.projectId === project && input.selector?.output === output.outputId &&
      Array.isArray(input.columns) && input.columns.join('\u0000') === columns.join('\u0000');
  }, { fromIndex, timeoutMs: 5_000 });
  const downloadPromise = page.waitForEvent('download', { timeout: 5_000 });
  const button = page.getByRole('button', { name: actionTitle, exact: true });
  let responseEntry;
  let download;
  await performAction(label, button, () => button.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      [responseEntry, download] = await Promise.all([responsePromise, downloadPromise]);
    },
  });
  return { responseEntry, download };
};

const assertCSVDownload = async ({ download, testInfo, check, phase, expectedCSVRows }) => {
  check('correctness', `${phase} CSV download uses the configured filename`,
    download.suggestedFilename() === fileName,
    { expected: fileName, actual: download.suggestedFilename() });
  const path = testInfo.outputPath(`${phase}.csv`);
  await download.saveAs(path);
  const csv = await readFile(path, 'utf8');
  const lines = csv.split('\n');
  const actualRows = lines.slice(1, -1).sort();
  const exactCSV = csv.endsWith('\n') && lines[0] === 'Patient ID,Gender' &&
    actualRows.length === expectedCSVRows.length &&
    actualRows.every((row, index) => row === [...expectedCSVRows].sort()[index]);
  check('correctness', `${phase} CSV contains the exact raw Patient header and rows`, exactCSV,
    { expectedHeader: 'Patient ID,Gender', actualHeader: lines[0], expectedRows: [...expectedCSVRows].sort(), actualRows });
};

export const viewerOutputActionCSVWorkflow = async ({ page, report, action, check, networkObserver }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const explorer = 'default';
  const oracle = readPatientOracle(target);
  const sourceHashBefore = oracle.sha256;
  report.target.project = project;
  report.target.fixtureProjectAlias = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorer;
  report.target.fixtureOracle = {
    path: oracle.path,
    sha256: oracle.sha256,
    patientIDs: oracle.rows.map(row => row.id),
    csvRows: oracle.expectedCSVRows,
  };

  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: target.sourceRoot,
    encoding: 'utf8',
  }).trim();
  const configPath = repositoryConfigPath(project, generation);
  const seedResponse = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Loom-Source-Commit': sourceCommit,
    },
    body: JSON.stringify(workspaceForAction()),
    signal: AbortSignal.timeout(30_000),
  });
  const seedText = await seedResponse.text();
  let seedBody;
  try { seedBody = JSON.parse(seedText); } catch { seedBody = { body: seedText.slice(0, 1_000) }; }
  const seedPassed = seedResponse.ok && seedBody.project === canonicalProject && seedBody.generation === generation &&
    seedBody.explorerId === explorer && seedBody.activated === true;
  check('correctness', 'repository config publishes the configured Viewer to the exact fixture generation', seedPassed,
    { status: seedResponse.status, path: configPath, requestedProjectAlias: project, expectedCanonicalProject: canonicalProject,
      generation, explorer, response: seedBody, sourceCommit });
  report.target.repositoryConfigSeed = {
    project, fixtureProjectAlias: project, canonicalProject: seedBody.project,
    generation: seedBody.generation, explorer: seedBody.explorerId, sourceCommit, response: seedBody,
    receiptId: seedBody.receiptId,
    revisionId: seedBody.revisionId,
  };

  const runtimeAPIPath = runtimePath(project, explorer);
  const runtimeStart = networkObserver.requests.length;
  const runtimePromise = networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === runtimeAPIPath,
  { fromIndex: runtimeStart, timeoutMs: 5_000 });
  await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded' });
  const runtimeEntry = await runtimePromise;
  const runtimeEnvelope = runtimeEntry.response;
  const runtime = runtimeEnvelope?.runtime;
  const runtimeGeneration = runtime?.generation ?? runtime?.publication?.generation;
  check('correctness', 'Viewer runtime retains the exact fixture project, generation, and default Explorer',
    runtimeEntry.status >= 200 && runtimeEntry.status < 300 && runtimeGeneration === generation &&
      runtimeEnvelope?.project === canonicalProject && runtimeEnvelope?.explorerId === explorer,
    { status: runtimeEntry.status, project: runtimeEnvelope?.project, requestedProjectAlias: project,
      expectedCanonicalProject: canonicalProject, explorer: runtimeEnvelope?.explorerId,
      generation: runtimeGeneration, expectedGeneration: generation });
  assert(runtime && Array.isArray(runtime.outputs) && runtime.outputs.length === 1,
    'the configured Viewer must expose exactly one Patient output');
  const output = runtime.outputs[0];
  const configuredAction = output.actions?.find(candidate => candidate.type === actionType);
  const expectedAction = {
    type: actionType,
    title: actionTitle,
    fileName,
    output: outputId,
    columns,
    exportHeaders,
  };
  check('correctness', 'configured output action is projected with its exact title, filename, output, and export columns',
    isDeepStrictEqual(configuredAction, expectedAction) &&
      (configuredAction.output === output.outputId || configuredAction.output === output.name),
    { expected: expectedAction, actual: configuredAction, outputId: output.outputId, outputName: output.name });
  report.target.viewerOutputId = output.outputId;
  report.target.viewerQueryOwnership = {
    project, fixtureProjectAlias: project, canonicalProject, explorer, generation,
    outputId: output.outputId, selector: output.selector,
  };

  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: 5_000 });
  await waitForViewerRows(page, oracle.rows.map(row => row.id));
  const viewerRows = await outputTable.locator('tbody tr').allInnerTexts();
  const actualIDs = assertViewerPatientRows(viewerRows, oracle.rows.map(row => row.id));
  check('correctness', 'Viewer output renders exactly both independent Patient fixture identities',
    actualIDs.length === oracle.rows.length,
    { actualIDs, expectedIDs: oracle.rows.map(row => row.id), rows: viewerRows });

  const first = await captureAction({ page, observer: networkObserver, performAction: action,
    output, project, label: 'download configured Patient CSV' });
  const firstInput = first.responseEntry.request?.variables?.input;
  const firstRequestPassed = first.responseEntry.origin === new URL(target.uiUrl).origin &&
    matchesSelector(firstInput?.selector, output.selector) && firstInput.projectId === project &&
    firstInput.columns?.join('\u0000') === columns.join('\u0000');
  check('correctness', 'first action read uses the exact project, published selector, and action columns', firstRequestPassed,
    { origin: first.responseEntry.origin, actualProjectId: firstInput.projectId, expectedProjectId: project,
      fixtureProjectAlias: project, canonicalProject, selector: firstInput.selector,
      expectedSelector: output.selector, columns: firstInput.columns, expectedColumns: columns });
  check('correctness', 'first action read returned HTTP success before the browser download',
    first.responseEntry.status >= 200 && first.responseEntry.status < 300,
    { status: first.responseEntry.status, path: first.responseEntry.path });
  await assertCSVDownload({ download: first.download, testInfo, check, phase: 'first', expectedCSVRows: oracle.expectedCSVRows });

  const reloadStart = networkObserver.requests.length;
  const reloadRuntimePromise = networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === runtimeAPIPath,
  { fromIndex: reloadStart, timeoutMs: 5_000 });
  await page.reload({ waitUntil: 'domcontentloaded' });
  const reloadRuntimeEntry = await reloadRuntimePromise;
  await waitForViewerRows(page, oracle.rows.map(row => row.id));
  const reloadEnvelope = reloadRuntimeEntry.response;
  const reloadRuntime = reloadEnvelope?.runtime;
  const reloadOutput = reloadRuntime?.outputs?.find(candidate => candidate.outputId === output.outputId);
  const reloadRows = await outputTable.locator('tbody tr').allInnerTexts();
  const reloadIDs = assertViewerPatientRows(reloadRows, oracle.rows.map(row => row.id));
  check('persistence', 'configured action and exact Patient output survive a full Viewer reload',
    reloadRuntimeEntry.status >= 200 && reloadRuntimeEntry.status < 300 &&
      reloadEnvelope?.project === canonicalProject && reloadEnvelope?.explorerId === explorer &&
      (reloadRuntime?.generation ?? reloadRuntime?.publication?.generation) === generation &&
      reloadOutput?.actions?.some(candidate => isDeepStrictEqual(candidate, expectedAction)) &&
      reloadIDs.length === oracle.rows.length,
    { status: reloadRuntimeEntry.status, project: reloadEnvelope?.project, requestedProjectAlias: project,
      expectedCanonicalProject: canonicalProject, explorer: reloadEnvelope?.explorerId,
      generation: reloadRuntime?.generation ?? reloadRuntime?.publication?.generation,
      expectedGeneration: generation, actions: reloadOutput?.actions, patientIDs: reloadIDs });

  const second = await captureAction({ page, observer: networkObserver, performAction: action,
    output, project, label: 'repeat configured Patient CSV download after reload' });
  const secondInput = second.responseEntry.request?.variables?.input;
  const secondRequestPassed = second.responseEntry.origin === new URL(target.uiUrl).origin &&
    matchesSelector(secondInput?.selector, output.selector) && secondInput.projectId === project &&
    secondInput.columns?.join('\u0000') === columns.join('\u0000');
  check('correctness', 'reloaded action read uses the exact project, published selector, and action columns', secondRequestPassed,
    { origin: second.responseEntry.origin, actualProjectId: secondInput.projectId, expectedProjectId: project,
      fixtureProjectAlias: project, canonicalProject, selector: secondInput.selector,
      expectedSelector: output.selector, columns: secondInput.columns, expectedColumns: columns });
  check('correctness', 'reloaded action read returned HTTP success before the browser download',
    second.responseEntry.status >= 200 && second.responseEntry.status < 300,
    { status: second.responseEntry.status, path: second.responseEntry.path });
  await assertCSVDownload({ download: second.download, testInfo, check, phase: 'after-reload', expectedCSVRows: oracle.expectedCSVRows });

  const sourceHashAfter = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient.ndjson input stays unchanged during both CSV downloads',
    sourceHashAfter === sourceHashBefore,
    { path: oracle.path, before: sourceHashBefore, after: sourceHashAfter });
};
