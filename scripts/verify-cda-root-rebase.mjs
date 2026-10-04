import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import {
  assertOwnedTarget, browserEval, captureRequests, click, fill, launchBrowser, navigate,
  selectOption, waitForBrowser, waitForCapturedResponse, includeBrowserDiagnostics,
} from './lib/cda-playwright.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const explorerId = process.argv[3] ?? `cda-root-rebase-${Date.now()}`;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN?.replace(/\/$/, '');
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN?.replace(/\/$/, '');
const evidenceDirectory = process.argv[2] ?? join('.artifacts', 'cda-root-rebase', new Date().toISOString().replaceAll(':', '-'));
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorerId}&mode=builder`;
const authoringURL = `${apiOrigin}/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`;
const tableName = `CDA root rebase QA ${Date.now()}`;
const state = { project, explorer: explorerId, pageURL, tableName, actions: [], timingsMs: {}, nativeRequests: [], errors: [], started: new Date().toISOString() };
await assertOwnedTarget({ project, apiOrigin, uiOrigin });
await mkdir(evidenceDirectory, { recursive: true });
const browser = await launchBrowser(evidenceDirectory, undefined, { noAuth: true, apiOrigin, uiOrigin });
const browserEvents = captureRequests(browser, state, `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`, { apiOrigin, uiOrigin, responsePaths: /commands|row-change|preview|construction-proposals|construction-choice-proposals/ });
let outputId;

const builder = async () => {
  const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30_000) });
  assert.equal(response.status, 200);
  return response.json();
};
const waitForRoot = async (targetId, root) => {
  const value = await builder();
  assert.equal(value.workspace.documents.find(document => document.output.id === targetId)?.rootResourceType, root);
  return value;
};
const waitForRowAssessment = async (after) => {
  const response = state.nativeRequests.slice(after).find(candidate => candidate.path.endsWith('/row-change') && candidate.response);
  const entry = response ?? await waitForCapturedResponse(browser.page, browserEvents,
    candidate => candidate.path.endsWith('/row-change'), 30_000);
  return entry.response;
};
const actClick = async (selector, name, identity = { name }) => {
  await click(browser.page, selector, identity);
  state.actions.push(name);
};
const selectTable = async () => {
  const locator = browser.page.locator('button[data-testid^="construction-table-"]');
  await waitForBrowser(browser.page, ([title]) => [...document.querySelectorAll('button[data-testid^="construction-table-"]')]
    .some(button => button.innerText.trim().endsWith(title)), [tableName]);
  await actClick('button[data-testid^="construction-table-"]', 'Select temporary table', { includes: tableName });
};
const openRowControl = async () => {
  const details = browser.page.locator('[data-testid="construction-source-setup"]');
  if (!await details.evaluate(element => element.open)) await actClick('[data-testid="construction-source-setup"] summary', 'Open source and column setup');
  await waitForBrowser(browser.page, () => Boolean(document.querySelector('select[aria-label="One row per"]')));
  return browserEval(browser.page, () => {
    const select = document.querySelector('select[aria-label="One row per"]');
    return { disabled: select.disabled, options: [...select.options].map(option => ({ value: option.value, label: option.textContent, disabled: option.disabled })) };
  });
};
const preview = async stage => {
  const started = Date.now();
  await actClick('button', `Preview ${stage}`, { name: 'Preview' });
  await waitForBrowser(browser.page, () => document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length > 1);
  const value = await browserEval(browser.page, () => ({
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText.trim()),
    rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
  }));
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
  state[stage] = value;
  return value;
};
const rawQuery = query => {
  const container = process.env.LOOM_ARANGO_CONTAINER;
  assert(container, 'Set LOOM_ARANGO_CONTAINER to the isolated CDA source database container.');
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
  const output = execFileSync('rtk', ['docker', 'exec', container, 'arangosh', '--server.database', process.env.LOOM_CDA_DATABASE ?? 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
};
const rawPatients = ids => rawQuery(`FOR d IN Patient FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN d.id`);
const rawObservationSubjects = ids => rawQuery(`FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN {id:d.id,subject:d.payload.subject.reference}`);
const command = async commands => {
  const before = await builder();
  const response = await fetch(`${authoringURL}/commands`, {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ commandId: crypto.randomUUID(), semanticsVersion: before.workspace?.semanticsVersion ?? 9,
      snapshotToken: before.catalog.snapshotToken, expectedDraftVersion: before.draftVersion, expectedDraftDigest: before.draftDigest, commands }),
    signal: AbortSignal.timeout(30_000),
  });
  const body = await response.json();
  assert.equal(response.status, 200, JSON.stringify(body));
  return body;
};
const applyReviewedRowChange = async (stage, after) => {
  const started = Date.now();
  await waitForBrowser(browser.page, () => Boolean([...document.querySelectorAll('[data-testid="row-change-preview-panel"] button')]
    .find(button => button.textContent?.includes('Apply row change') && !button.disabled)));
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} candidate preview took ${state.timingsMs[stage]} ms`);
  state[stage] = await browserEval(browser.page, () => {
    const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
    return { text: panel?.innerText, rows: [...panel.querySelectorAll('table tr')].map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())) };
  });
  assert(state[stage].rows.length > 1, `${stage} did not render proposed rows`);
  const requests = state.nativeRequests.slice(after).map(({ path, status }) => ({ path, status }));
  assert(requests.some(response => response.path.endsWith('/preview') && response.status === 200), `${stage} did not request a successful candidate preview`);
  assert(!requests.some(response => response.path.endsWith('/commands')), `${stage} changed the saved table before Apply`);
  await actClick('[data-testid="row-change-preview-panel"] button', `Apply ${stage}`, { name: 'Apply row change' });
};

try {
  await navigate(browser.page, pageURL);
  await waitForBrowser(browser.page, () => document.body.innerText.includes('DATASET WORKSPACE'));
  state.tablesBefore = await browserEval(browser.page, () => [...document.querySelectorAll('button[data-testid^="construction-table-"]')].map(button => button.innerText.trim().split(String.fromCharCode(10)).at(-1)));
  await actClick('button', 'New table', { name: 'New table' });
  await waitForBrowser(browser.page, () => Boolean(document.querySelector('button[aria-label="Choose Patient rows"]') && !document.querySelector('button[aria-label="Choose Patient rows"]').disabled));
  await fill(browser.page, '#first-table-name', tableName);
  await actClick('button[aria-label="Choose Patient rows"]', 'Choose Patient rows');
  await waitForBrowser(browser.page, () => document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length > 1);
  const created = await builder();
  const initial = created.workspace.documents.find(document => document.output.title === tableName);
  assert(initial, 'The temporary Patient table was not created');
  outputId = initial.output.id;
  assert.equal(initial.rootResourceType, 'Patient');
  assert.equal(initial.columns.length, 1);
  const patientNode = created.catalog.nodes.find(node => node.resourceType === 'Patient');
  const observationNode = created.catalog.nodes.find(node => node.resourceType === 'Observation');
  const subjectEdge = created.catalog.edges.find(edge => edge.fromNodeId === patientNode.nodeId && edge.toNodeId === observationNode.nodeId && edge.label === 'subject_Patient');
  assert(subjectEdge, 'CDA catalog has no Patient to Observation Subject relationship');
  state.setupRoute = { edgeId: subjectEdge.edgeId, label: subjectEdge.label };
  await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: subjectEdge.edgeId }]);

  await navigate(browser.page, pageURL);
  await selectTable();
  state.beforeControl = await openRowControl();
  assert.equal(state.beforeControl.disabled, false, 'Row-root control is disabled after adding a related Observation');
  const observationChoice = state.beforeControl.options.find(option => option.label.includes('Observation'));
  assert(observationChoice && !observationChoice.disabled, 'Related Observation cannot be chosen as rows');
  const before = await builder();
  state.before = { draftVersion: before.draftVersion, document: before.workspace.documents.find(document => document.output.id === outputId) };
  const originalPreview = await preview('patientPreview');
  assert.deepEqual(originalPreview.headers, ['PATIENT ID']);
  assert.deepEqual(new Set(originalPreview.rows.map(row => row[0])), new Set(rawPatients(originalPreview.rows.map(row => row[0]))));
  let assessmentStart = state.nativeRequests.length;
  await selectOption(browser.page, 'select[aria-label="One row per"]', observationChoice.value);
  state.actions.push('Choose Observation rows');
  state.observationAssessment = await waitForRowAssessment(assessmentStart);
  if (state.observationAssessment.status === 'BLOCKED') {
    await waitForBrowser(browser.page, () => document.body.innerText.includes('Choose how to preserve this table'));
    state.repairPanel = await browserEval(browser.page, () => {
      const panel = document.getElementById('row-change-repair-title')?.closest('section');
      return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])].map(button => ({ text: button.innerText, disabled: button.disabled })) };
    });
    const repairButton = state.repairPanel.buttons.find(button => button.text.includes('Match through Subject') && !button.disabled);
    assert(repairButton, `No Subject relationship repair was offered: ${JSON.stringify(state.repairPanel)}`);
    const repairStarted = state.nativeRequests.length;
    await actClick('section button', 'Preserve Patient ID through Observation Subject', { name: repairButton.text });
    state.repairedAssessment = await waitForRowAssessment(repairStarted);
  }
  await applyReviewedRowChange('observationRootProposal', assessmentStart);
  await waitForBrowser(browser.page, () => !document.querySelector('[data-testid="row-change-preview-panel"]'));
  const changed = await waitForRoot(outputId, 'Observation');
  state.rootChangeRequests = state.nativeRequests.slice(assessmentStart).map(({ path, status }) => ({ path, status }));
  state.changed = { draftVersion: changed.draftVersion, document: changed.workspace.documents.find(document => document.output.id === outputId) };
  assert.equal(state.changed.document?.rootResourceType, 'Observation', 'Apply did not change the root resource');
  assert.deepEqual(state.changed.document.columns.map(column => column.column), state.before.document.columns.map(column => column.column), 'Root change lost the existing Patient ID feature');
  await navigate(browser.page, pageURL);
  await selectTable();
  const observationPreview = await preview('observationPreview');
  assert(observationPreview.rows.length > 0, 'The Observation-root table has no rendered CDA rows');
  assert.deepEqual(new Set(observationPreview.rows.map(row => row[0])), new Set(rawPatients(observationPreview.rows.map(row => row[0]))), 'Rebased Patient IDs differ from CDA source');
  await actClick('button[aria-label^="Add columns:"]', 'Open Add columns on Observation rows', { includes: 'Add columns' });
  await waitForBrowser(browser.page, () => Boolean(document.querySelector('input[aria-label="Select Observation.id"]') && !document.querySelector('input[aria-label="Select Observation.id"]').disabled));
  await actClick('input[aria-label="Select Observation.id"]', 'Select Observation ID');
  await actClick('[aria-label="Add columns editor"] button', 'Preview Observation ID column', { name: 'Add 1 selected feature' });
  await waitForBrowser(browser.page, () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready');
  state.observationColumnProposal = await browserEval(browser.page, () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0, 1000));
  const columnApplyStart = state.nativeRequests.length;
  await actClick('[data-testid="construction-choice-proposal-panel"] button', 'Apply Observation ID column', { name: 'Apply columns' });
  const commandResponse = await waitForCapturedResponse(browser.page, browserEvents,
    request => request.path.endsWith('/commands') && request.method === 'POST', 30_000);
  assert.equal(commandResponse.status, 200, 'Applying Observation ID failed');
  const withObservationID = await builder();
  state.withObservationID = withObservationID.workspace.documents.find(document => document.output.id === outputId);
  await navigate(browser.page, pageURL);
  await selectTable();
  const pairedPreview = await preview('pairedPreview');
  const patientIndex = pairedPreview.headers.findIndex(header => header === 'PATIENT ID');
  const observationIndex = pairedPreview.headers.findIndex((header, index) => index !== patientIndex && header.endsWith('ID'));
  assert(patientIndex >= 0 && observationIndex >= 0, 'The rebased preview lacks Patient and Observation identifiers');
  const observations = rawObservationSubjects(pairedPreview.rows.map(row => row[observationIndex]));
  const sourceByID = new Map(observations.map(record => [record.id, record.subject]));
  assert.equal(sourceByID.size, pairedPreview.rows.length, 'Displayed Observation IDs do not match CDA records');
  for (const row of pairedPreview.rows) assert.equal(sourceByID.get(row[observationIndex]), `Patient/${row[patientIndex]}`, `Patient relationship differs for Observation ${row[observationIndex]}`);
  state.pairOracle = observations;
  state.afterControl = await openRowControl();
  const patientChoice = state.afterControl.options.find(option => option.label.includes('Patient'));
  assert(patientChoice && !patientChoice.disabled, 'Patient rows cannot be restored');
  assessmentStart = state.nativeRequests.length;
  await selectOption(browser.page, 'select[aria-label="One row per"]', patientChoice.value);
  state.actions.push('Restore Patient rows');
  state.patientAssessment = await waitForRowAssessment(assessmentStart);
  if (state.patientAssessment.status === 'BLOCKED') {
    await waitForBrowser(browser.page, () => document.body.innerText.includes('Choose how to preserve this table'));
    state.restorationRepairPanel = await browserEval(browser.page, () => {
      const panel = document.getElementById('row-change-repair-title')?.closest('section');
      return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])].map(button => ({ text: button.innerText, disabled: button.disabled })) };
    });
    const repairButton = state.restorationRepairPanel.buttons.find(button => button.text.includes('Match through Subject') && !button.disabled);
    assert(repairButton, `No Subject relationship restoration was offered: ${JSON.stringify(state.restorationRepairPanel)}`);
    const repairStarted = state.nativeRequests.length;
    await actClick('section button', 'Restore Subject relationship', { name: repairButton.text });
    state.restorationRepairedAssessment = await waitForRowAssessment(repairStarted);
  }
  await applyReviewedRowChange('patientRootProposal', assessmentStart);
  await waitForBrowser(browser.page, () => !document.querySelector('[data-testid="row-change-preview-panel"]'));
  await waitForRoot(outputId, 'Patient');
  state.restorationRequests = state.nativeRequests.slice(assessmentStart).map(({ path, status }) => ({ path, status }));
  await navigate(browser.page, pageURL);
  await selectTable();
  const restored = await builder();
  state.restored = { draftVersion: restored.draftVersion, document: restored.workspace.documents.find(document => document.output.id === outputId) };
  assert.equal(state.restored.document?.rootResourceType, 'Patient');
  const restoredPreview = await preview('restoredPreview');
  const restoredPatientIndex = restoredPreview.headers.indexOf('PATIENT ID');
  assert(restoredPatientIndex >= 0, 'Restored Patient ID column is missing');
  assert.deepEqual(restoredPreview.rows.map(row => row[restoredPatientIndex]), originalPreview.rows.map(row => row[0]));
  assert(state.nativeRequests.every(response => response.status < 400), 'Browser received an authoring API error');
  await browserEvents.flush();
  includeBrowserDiagnostics(browser, state);
  assert.equal(state.errors.length, 0, `Unexpected browser or owned API errors: ${JSON.stringify(state.errors)}`);
} catch (error) {
  state.error = error instanceof Error ? error.message : String(error);
  state.failureUI = await browserEval(browser.page, () => ({ text: document.body.innerText.slice(0, 3500), alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.innerText) })).catch(() => undefined);
  includeBrowserDiagnostics(browser, state);
  await browser.captureFailure(error, { phase: 'root-rebase-lifecycle', project, explorer: explorerId, outputId });
  throw error;
} finally {
  if (outputId) {
    try {
      await command([{ type: 'DELETE_TABLE', outputId }]);
      state.cleanup = 'temporary table deleted';
    } catch (error) {
      state.cleanup = error instanceof Error ? error.message : String(error);
    }
  }
  await browserEvents.flush();
  includeBrowserDiagnostics(browser, state);
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'root-rebase-lifecycle.json'), JSON.stringify(state, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, actions: state.actions, timingsMs: state.timingsMs, error: state.error, cleanup: state.cleanup }, null, 2));
}
