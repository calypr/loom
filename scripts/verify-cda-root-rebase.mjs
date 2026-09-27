import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const apiOrigin = (process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188').replace(/\/$/, '');
const pageURL = `${uiOrigin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const authoringURL = `${apiOrigin}/api/v1/projects/loom_dev_cda_fhir/explorers/${explorerId}/authoring/v2`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const tableName = `CDA root rebase QA ${Date.now()}`;
const state = { pageURL, tableName, clicks: [], timingsMs: {}, responses: [] };
const browser = await launchBrowser('/private/tmp');
let outputId;

browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
  if (response.url.includes('/authoring/v2/')) {
    state.responses.push({ requestId, path: new URL(response.url).pathname, status: response.status });
  }
});

const builder = async () => {
  const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30_000) });
  assert.equal(response.status, 200);
  return response.json();
};

const waitForRoot = async (outputId, root) => {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const value = await builder();
    if (value.workspace.documents.find((document) => document.output.id === outputId)?.rootResourceType === root) return value;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for ${root} rows`);
};

const waitForRowAssessment = async (after) => {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const response = state.responses.slice(after).find((candidate) => candidate.path.endsWith('/row-change'));
    if (response) {
      const result = await browser.cdp.send('Network.getResponseBody', { requestId: response.requestId });
      return JSON.parse(result.body);
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error('The row-change assessment did not return');
};

const applyReviewedRowChange = async (stage, after) => {
  const started = Date.now();
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="row-change-preview-panel"] button')?.textContent?.includes('Apply row change')`, 30_000);
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="row-change-preview-panel"] button')?.disabled`, 30_000);
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} candidate preview took ${state.timingsMs[stage]} ms`);
  state[stage] = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="row-change-preview-panel"]');return {text:panel?.innerText,rows:[...panel.querySelectorAll('table tr')].map(row=>[...row.querySelectorAll('th,td')].map(cell=>cell.innerText.trim()))};`);
  assert(state[stage].rows.length > 1, `${stage} did not render proposed rows`);
  const requestsBeforeApply = state.responses.slice(after).map(({ path, status }) => ({ path, status }));
  assert(requestsBeforeApply.some((response) => response.path.endsWith('/preview') && response.status === 200), `${stage} did not request a successful candidate preview`);
  assert(!requestsBeforeApply.some((response) => response.path.endsWith('/commands')), `${stage} changed the saved table before Apply`);
  await click(`[...document.querySelectorAll('[data-testid="row-change-preview-panel"] button')].find(button=>button.textContent?.trim()==='Apply row change'&&!button.disabled).click();return true;`, `Apply ${stage}`);
};

const command = async (commands) => {
  const before = await builder();
  const response = await fetch(`${authoringURL}/commands`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      commandId: crypto.randomUUID(),
      semanticsVersion: 9,
      snapshotToken: before.catalog.snapshotToken,
      expectedDraftVersion: before.draftVersion,
      expectedDraftDigest: before.draftDigest,
      commands,
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const body = await response.json();
  assert.equal(response.status, 200, JSON.stringify(body));
  return body;
};

const click = async (script, label) => {
  await browserEval(browser.cdp, script);
  state.clicks.push(label);
};

const selectTable = async () => {
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('button')].some(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)}))`, 30_000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim().endsWith(${JSON.stringify(tableName)})).click();return true;`, 'Select temporary table');
};

const openRowControl = async () => {
  const open = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-source-setup"]')?.open===true;`);
  if (!open) await click(`document.querySelector('[data-testid="construction-source-setup"] summary').click();return true;`, 'Open source and column setup');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('select[aria-label="One row per"]'))`, 30_000);
  return browserEval(browser.cdp, `const select=document.querySelector('select[aria-label="One row per"]');return {disabled:select.disabled,options:[...select.options].map(option=>({value:option.value,label:option.textContent,disabled:option.disabled}))};`);
};

const preview = async (stage) => {
  const started = Date.now();
  await click(`[...document.querySelectorAll('button')].find(button=>button.textContent?.trim()==='Preview'&&!button.disabled).click();return true;`, `Preview ${stage}`);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length>1`, 30_000);
  const value = await browserEval(browser.cdp, `return {headers:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length)};`);
  state.timingsMs[stage] = Date.now() - started;
  assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
  state[stage] = value;
  return value;
};

const rawPatients = (ids) => {
  const query = `FOR d IN Patient FILTER d.project == "loom_dev_cda_fhir" AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN d.id`;
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
  const output = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
};

const rawObservationSubjects = (ids) => {
  const query = `FOR d IN Observation FILTER d.project == "loom_dev_cda_fhir" AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN {id:d.id,subject:d.payload.subject.reference}`;
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
  const output = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
};

try {
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30_000);
  state.tablesBefore = await browserEval(browser.cdp, `return [...document.querySelectorAll('button[data-testid^="construction-table-"]')].map(button=>button.innerText.trim().split(String.fromCharCode(10)).at(-1));`);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='New table').click();return true;`, 'New table');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)'))`, 30_000);
  await click(`const input=document.querySelector('#first-table-name');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(tableName)});input.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('button[aria-label="Choose Patient rows"]').click();return true;`, 'Choose Patient rows');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length>1`, 30_000);
  const created = await builder();
  const initial = created.workspace.documents.find((document) => document.output.title === tableName);
  assert(initial, 'The temporary Patient table was not created');
  outputId = initial.output.id;
  assert.equal(initial.rootResourceType, 'Patient');
  assert.equal(initial.columns.length, 1);
  const patientNode = created.catalog.nodes.find((node) => node.resourceType === 'Patient');
  const observationNode = created.catalog.nodes.find((node) => node.resourceType === 'Observation');
  const subjectEdge = created.catalog.edges.find((edge) =>
    edge.fromNodeId === patientNode.nodeId && edge.toNodeId === observationNode.nodeId && edge.label === 'subject_Patient');
  assert(subjectEdge, 'CDA catalog has no Patient to Observation Subject relationship');
  state.setupRoute = { edgeId: subjectEdge.edgeId, label: subjectEdge.label };
  await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: subjectEdge.edgeId }]);

  await navigate(browser.cdp, pageURL);
  await selectTable();
  const beforeControl = await openRowControl();
  state.beforeControl = beforeControl;
  assert.equal(beforeControl.disabled, false, 'Row-root control is disabled after adding a related Observation');
  const observationChoice = beforeControl.options.find((option) => option.label.includes('Observation'));
  assert(observationChoice && !observationChoice.disabled, 'Related Observation cannot be chosen as rows');
  const before = await builder();
  state.before = { draftVersion: before.draftVersion, document: before.workspace.documents.find((document) => document.output.id === outputId) };
  const originalPreview = await preview('patientPreview');
  assert.deepEqual(originalPreview.headers, ['PATIENT ID']);
  assert.deepEqual(new Set(originalPreview.rows.map((row) => row[0])), new Set(rawPatients(originalPreview.rows.map((row) => row[0]))));
  const assessmentStart = state.responses.length;
  await click(`const select=document.querySelector('select[aria-label="One row per"]');select.value=${JSON.stringify(observationChoice.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, 'Choose Observation rows');
  state.observationAssessment = await waitForRowAssessment(assessmentStart);
  if (state.observationAssessment.status === 'BLOCKED') {
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Choose how to preserve this table')`, 30_000);
    state.repairPanel = await browserEval(browser.cdp, `const title=document.getElementById('row-change-repair-title');const panel=title?.closest('section');return {text:panel?.innerText,buttons:[...panel.querySelectorAll('button')].map(button=>({text:button.innerText,disabled:button.disabled}))};`);
    const repairButton = state.repairPanel.buttons.find((button) => button.text.includes('Match through Subject') && !button.disabled);
    assert(repairButton, `No Subject relationship repair was offered: ${JSON.stringify(state.repairPanel)}`);
    const repairStarted = state.responses.length;
    await click(`[...document.querySelectorAll('section button')].find(button=>button.innerText.trim()===${JSON.stringify(repairButton.text)}).click();return true;`, 'Preserve Patient ID through Observation Subject');
    state.repairedAssessment = await waitForRowAssessment(repairStarted);
  }
  await applyReviewedRowChange('observationRootProposal', assessmentStart);
  const changed = await waitForRoot(outputId, 'Observation');
  state.rootChangeRequests = state.responses.slice(assessmentStart).map(({ path, status }) => ({ path, status }));
  state.changed = { draftVersion: changed.draftVersion, document: changed.workspace.documents.find((document) => document.output.id === outputId) };
  assert.equal(state.changed.document?.rootResourceType, 'Observation', 'Apply did not change the root resource');
  assert.deepEqual(state.changed.document.columns.map((column) => column.column), state.before.document.columns.map((column) => column.column), 'Root change lost the existing Patient ID feature');
  await navigate(browser.cdp, pageURL);
  await selectTable();
  const observationPreview = await preview('observationPreview');
  assert(observationPreview.rows.length > 0, 'The Observation-root table has no rendered CDA rows');
  assert.deepEqual(new Set(observationPreview.rows.map((row) => row[0])), new Set(rawPatients(observationPreview.rows.map((row) => row[0]))), 'Rebased Patient IDs differ from CDA source');
  await click(`document.querySelector('button[aria-label^="Add columns:"]').click();return true;`, 'Open Add columns on Observation rows');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)'))`, 30_000);
  await click(`document.querySelector('input[aria-label="Select Observation.id"]').click();return true;`, 'Select Observation ID');
  await click(`[...document.querySelectorAll('[aria-label="Add columns editor"] button')].find(button=>button.textContent?.trim()==='Add 1 selected feature'&&!button.disabled).click();return true;`, 'Preview Observation ID column');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30_000);
  state.observationColumnProposal = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0,1000);`);
  const columnApplyStart = state.responses.length;
  await click(`[...document.querySelectorAll('[data-testid="construction-choice-proposal-panel"] button')].find(button=>button.textContent?.trim()==='Apply columns'&&!button.disabled).click();return true;`, 'Apply Observation ID column');
  const columnDeadline = Date.now() + 30_000;
  while (!state.responses.slice(columnApplyStart).some((response) => response.path.endsWith('/commands')) && Date.now() < columnDeadline) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.equal(state.responses.slice(columnApplyStart).find((response) => response.path.endsWith('/commands'))?.status, 200, 'Applying Observation ID failed');
  const withObservationID = await builder();
  state.withObservationID = withObservationID.workspace.documents.find((document) => document.output.id === outputId);
  await navigate(browser.cdp, pageURL);
  await selectTable();
  const pairedPreview = await preview('pairedPreview');
  const patientIndex = pairedPreview.headers.findIndex((header) => header === 'PATIENT ID');
  const observationIndex = pairedPreview.headers.findIndex((header, index) => index !== patientIndex && header.endsWith('ID'));
  assert(patientIndex >= 0 && observationIndex >= 0, 'The rebased preview lacks Patient and Observation identifiers');
  const observations = rawObservationSubjects(pairedPreview.rows.map((row) => row[observationIndex]));
  const sourceByID = new Map(observations.map((record) => [record.id, record.subject]));
  assert.equal(sourceByID.size, pairedPreview.rows.length, 'Displayed Observation IDs do not match CDA records');
  for (const row of pairedPreview.rows) {
    assert.equal(sourceByID.get(row[observationIndex]), `Patient/${row[patientIndex]}`, `Patient relationship differs for Observation ${row[observationIndex]}`);
  }
  state.pairOracle = observations;
  const afterControl = await openRowControl();
  state.afterControl = afterControl;
  const patientChoice = afterControl.options.find((option) => option.label.includes('Patient'));
  assert(patientChoice && !patientChoice.disabled, 'Patient rows cannot be restored');
  const restorationStart = state.responses.length;
  await click(`const select=document.querySelector('select[aria-label="One row per"]');select.value=${JSON.stringify(patientChoice.value)};select.dispatchEvent(new Event('change',{bubbles:true}));return true;`, 'Restore Patient rows');
  state.patientAssessment = await waitForRowAssessment(restorationStart);
  if (state.patientAssessment.status === 'BLOCKED') {
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('Choose how to preserve this table')`, 30_000);
    state.restorationRepairPanel = await browserEval(browser.cdp, `const title=document.getElementById('row-change-repair-title');const panel=title?.closest('section');return {text:panel?.innerText,buttons:[...panel.querySelectorAll('button')].map(button=>({text:button.innerText,disabled:button.disabled}))};`);
    const repairButton = state.restorationRepairPanel.buttons.find((button) => button.text.includes('Match through Subject') && !button.disabled);
    assert(repairButton, `No Subject relationship restoration was offered: ${JSON.stringify(state.restorationRepairPanel)}`);
    const repairStarted = state.responses.length;
    await click(`[...document.querySelectorAll('section button')].find(button=>button.innerText.trim()===${JSON.stringify(repairButton.text)}).click();return true;`, 'Restore Subject relationship');
    state.restorationRepairedAssessment = await waitForRowAssessment(repairStarted);
  }
  await applyReviewedRowChange('patientRootProposal', restorationStart);
  await waitForRoot(outputId, 'Patient');
  state.restorationRequests = state.responses.slice(restorationStart).map(({ path, status }) => ({ path, status }));
  await navigate(browser.cdp, pageURL);
  await selectTable();
  const restored = await builder();
  state.restored = { draftVersion: restored.draftVersion, document: restored.workspace.documents.find((document) => document.output.id === outputId) };
  assert.equal(state.restored.document?.rootResourceType, 'Patient');
  const restoredPreview = await preview('restoredPreview');
  const restoredPatientIndex = restoredPreview.headers.indexOf('PATIENT ID');
  assert(restoredPatientIndex >= 0, 'Restored Patient ID column is missing');
  assert.deepEqual(restoredPreview.rows.map((row) => row[restoredPatientIndex]), originalPreview.rows.map((row) => row[0]));
  assert(state.responses.every((response) => response.status < 400), 'Browser received an authoring API error');
} catch (error) {
  state.error = error instanceof Error ? error.message : String(error);
  state.failureUI = await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,3500),alerts:[...document.querySelectorAll('[role="alert"]')].map(node=>node.innerText)};`).catch(() => undefined);
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
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'root-rebase-lifecycle.json'), JSON.stringify(state, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, clicks: state.clicks, timingsMs: state.timingsMs, error: state.error, cleanup: state.cleanup }, null, 2));
}
