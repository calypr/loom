import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

// Run against the construction checkout's local stack and loaded CDA fixture.
const origin = process.env.LOOM_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const apiOrigin = process.env.LOOM_API_ORIGIN ?? 'http://127.0.0.1:8188';
const project = 'loom_dev_cda_fhir';
const observationId = '485e2567-b566-56f3-b5bd-5f025f37cd95';
const explorer = `compound-coded-qa-${Date.now()}`;
const evidenceDirectory = process.env.LOOM_VERIFY_OUTPUT ?? '/tmp/loom-compound-coded-verification';
const base = `/api/v1/projects/${project}/explorers/${explorer}`;
const state = { explorer, observationId, timingsMs: {}, failures: [], requests: [] };

const api = async (path, method = 'GET', body) => {
  const response = await fetch(apiOrigin + path, {
    method, headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert(response.ok, `${method} ${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};

const rowsReady = (cdp) => waitForBrowser(cdp,
  `!document.body.innerText.includes('Loading your table') && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);

const proposed = async (cdp, started, timing) => {
  await waitForBrowser(cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`);
  const result = await browserEval(cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText))};`);
  state.timingsMs[timing] = Date.now() - started;
  assert.equal(result.status, 'ready', result.text);
  assert(state.timingsMs[timing] <= 5000, `${timing} took ${state.timingsMs[timing]} ms`);
  return result;
};

const savedDocument = (builder) => {
  assert.equal(builder.workspace.documents.length, 1);
  return builder.workspace.documents[0];
};

await mkdir(evidenceDirectory, { recursive: true });
const browser = await launchBrowser(evidenceDirectory);
browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => state.failures.push({ kind: 'exception', text: exceptionDetails.exception?.description ?? exceptionDetails.text }));
browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
  if (type === 'error') state.failures.push({ kind: 'console', text: args.map(arg => arg.value ?? arg.description ?? '').join(' ') });
});
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/api/') && response.status >= 400) state.failures.push({ kind: 'http', status: response.status, url: response.url });
});
browser.cdp.on('Network.requestWillBeSent', ({ request }) => {
  if (request.url.includes('/api/') && request.method !== 'GET') state.requests.push({ method: request.method, url: request.url, body: request.postData ? JSON.parse(request.postData) : undefined });
});

try {
  const raw = execFileSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db.Observation.byExample({id:"${observationId}"}).toArray().map(d=>({id:d.id,project:d.project,generation:d.dataset_generation,component:d.payload.component}))))`], { encoding: 'utf8', timeout: 30000 });
  const [source] = JSON.parse(raw.slice(raw.indexOf('[')));
  assert.equal(source?.project, project, 'The real CDA source record is required');
  const expectedValue = source.component.find(component => component.code?.coding?.some(coding => coding.system === 'https://cda.readthedocs.io' && coding.code === 'specimen_type'))?.valueString;
  const expectedDisease = source.component.find(component => component.code?.coding?.some(coding => coding.system === 'https://cda.readthedocs.io' && coding.code === 'primary_disease_type'))?.valueString;
  assert.equal(expectedValue, 'analyte');
  assert.equal(typeof expectedDisease, 'string');
  state.oracle = { generation: source.generation, specimenType: expectedValue, primaryDiseaseType: expectedDisease, count: 1 };
  await api(`/api/v1/projects/${project}/explorers`, 'POST', { name: explorer, title: 'Compound coded grouping verification' });
  const initial = await api(base + '/authoring/v2/builder');
  assert.equal(initial.catalog.generation, state.oracle.generation);
  const selection = await api(base + '/selections', 'POST', {
    snapshotToken: initial.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: initial.catalog.generation, resourceType: 'Observation', id: observationId }] } },
  });
  const url = `${origin}/?project=${project}&explorer=${explorer}&mode=builder&selection=${encodeURIComponent(selection.id)}`;
  state.url = url;
  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)')`);
  await click(browser.cdp, 'button', { name: 'Choose Observation rows' });
  await rowsReady(browser.cdp);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.innerText.includes('Observation')`);
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[aria-label="Starting collection"] button')].some(button => button.innerText === 'Use selected resources' && !button.disabled)`);
  await click(browser.cdp, '[aria-label="Starting collection"] button', { name: 'Use selected resources' });
  await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('1 Observation resources attached')`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false`);
  state.baseline = await api(base + '/authoring/v2/builder');
  const requestStart = state.requests.length;
  const openedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-action-group-rows"]');
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Group by coded value: Specimen type"]:not(:disabled)')`);
  state.timingsMs.openPicker = Date.now() - openedAt;
  assert(state.timingsMs.openPicker <= 5000, `Opening the coded picker took ${state.timingsMs.openPicker} ms`);
  state.groupChoices = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Group by"]')].map(input=>({label:input.getAttribute('aria-label'),disabled:input.disabled,checked:input.checked}));`);
  assert(!await browserEval(browser.cdp, `return document.body.innerText.includes('Need a coded-value column first?');`));
  const selectedAt = Date.now();
  await click(browser.cdp, 'input[aria-label="Group by coded value: Specimen type"]');
  state.proposal = await proposed(browser.cdp, selectedAt, 'selectToPreview');
  assert.deepEqual(state.proposal.rows, [[state.oracle.specimenType, '1']], 'The grouped preview must match the raw CDA value and record count');
  const beforeApply = await api(base + '/authoring/v2/builder');
  assert.equal(beforeApply.draftDigest, state.baseline.draftDigest, 'Selecting a code must not save its prerequisite');
  assert.deepEqual(beforeApply.workspace, state.baseline.workspace);
  const proposalRequests = state.requests.slice(requestStart);
  assert(!proposalRequests.some(request => request.url.includes('construction-choice-proposals') || request.url.endsWith('/commands')), 'The coded selection must not issue a separate saved-column command');
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1`);
  state.saved = await api(base + '/authoring/v2/builder');
  assert.deepEqual(state.requests.slice(requestStart).filter(request => request.url.endsWith('/commands')).flatMap(request => request.body.commands.map(command => command.type)), ['APPLY_CONSTRUCTION_PROPOSAL'], 'One Apply must save both parts in one command');
  const construction = savedDocument(state.saved).construction;
  assert.equal(construction.steps.length, 2, 'The extraction and grouping must be saved together');
  const [helper, group] = construction.steps;
  assert.equal(helper.operation.kind, 'CODED_PIVOT');
  assert.equal(group.operation.kind, 'GROUP');
  assert.equal(helper.ownerStepId, group.id);
  assert.equal(helper.operation.codedPivot.categories.length, 1);
  assert.equal(savedDocument(state.saved).columns.length, savedDocument(state.baseline).columns.length, 'The prerequisite must not become a standalone source column');

  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-history-step-${group.id}"]')`);
  await rowsReady(browser.cdp);
  await click(browser.cdp, `[data-testid="construction-history-step-${group.id}"]`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${group.id}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Group by coded value: Specimen type"]')?.checked`);
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Group by coded value: Primary disease type"]:not(:disabled)')`);
  const editedAt = Date.now();
  await click(browser.cdp, 'input[aria-label="Group by coded value: Primary disease type"]');
  state.editedProposal = await proposed(browser.cdp, editedAt, 'editToPreview');
  assert.equal(state.editedProposal.rows.length, 1);
  assert(state.editedProposal.rows[0].includes(state.oracle.specimenType));
  assert(state.editedProposal.rows[0].includes(state.oracle.primaryDiseaseType));
  assert(state.editedProposal.rows[0].includes('1'));
  await waitForBrowser(browser.cdp, `document.querySelector('input[aria-label="Group by Observation ID"]:not(:disabled)')`);
  const ordinaryKeyAt = Date.now();
  await click(browser.cdp, 'input[aria-label="Group by Observation ID"]');
  state.mixedKeyProposal = await proposed(browser.cdp, ordinaryKeyAt, 'ordinaryKeyToPreview');
  assert.equal(state.mixedKeyProposal.rows.length, 1);
  assert(state.mixedKeyProposal.rows[0].includes(observationId), 'An existing source field must survive the coded prerequisite');
  assert(state.mixedKeyProposal.rows[0].includes(state.oracle.specimenType));
  assert(state.mixedKeyProposal.rows[0].includes(state.oracle.primaryDiseaseType));
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  state.edited = await api(base + '/authoring/v2/builder');
  const editedSteps = savedDocument(state.edited).construction.steps;
  assert.equal(editedSteps.length, 2);
  assert.equal(editedSteps[0].id, helper.id, 'Editing must reuse the owned prerequisite');
  assert.equal(editedSteps[0].operation.codedPivot.categories.length, 2);
  assert.equal(editedSteps[0].rowValues.length, 1);
  assert.equal(editedSteps[0].rowValues[0].policy, 'ONE');

  await click(browser.cdp, `[data-testid="construction-history-step-${group.id}"]`);
  const removedAt = Date.now();
  await click(browser.cdp, `[data-testid="construction-remove-step-${group.id}"]`);
  state.removalProposal = await proposed(browser.cdp, removedAt, 'removeToPreview');
  assert.deepEqual(state.removalProposal.rows, [[observationId]], 'Removing the compound group must restore its original source table');
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  state.afterRemovalCancel = await api(base + '/authoring/v2/builder');
  assert.equal(state.afterRemovalCancel.draftDigest, state.edited.draftDigest, 'Cancel must preserve the grouped draft');
  assert.deepEqual(state.afterRemovalCancel.workspace, state.edited.workspace);
  await click(browser.cdp, `[data-testid="construction-history-step-${group.id}"]`);
  const confirmedRemovalAt = Date.now();
  await click(browser.cdp, `[data-testid="construction-remove-step-${group.id}"]`);
  state.confirmedRemovalProposal = await proposed(browser.cdp, confirmedRemovalAt, 'confirmedRemovalToPreview');
  assert.deepEqual(state.confirmedRemovalProposal.rows, [[observationId]]);
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0`);
  state.restored = await api(base + '/authoring/v2/builder');
  assert.equal(savedDocument(state.restored).construction?.steps?.length ?? 0, 0, 'Removing GROUP must remove its owned extraction');
  const sourceColumnSemantics = (columns) => columns.map(({ columnId: generatedStageId, ...column }) => column);
  assert.deepEqual(sourceColumnSemantics(savedDocument(state.restored).columns), sourceColumnSemantics(savedDocument(state.baseline).columns));
  await navigate(browser.cdp, url);
  await rowsReady(browser.cdp);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0 && document.body.innerText.includes(${JSON.stringify(observationId)})`);
  state.restoredAfterReload = await api(base + '/authoring/v2/builder');
  assert.deepEqual(state.restoredAfterReload.workspace, state.restored.workspace, 'The restored table must persist after fresh reload');
  assert.deepEqual(state.failures, [], 'The browser lifecycle must not hide HTTP or runtime failures');
} catch (error) {
  state.failures.push({ kind: 'assertion', text: String(error.stack ?? error) });
  process.exitCode = 1;
} finally {
  state.body = await browserEval(browser.cdp, 'return document.body.innerText;').catch(String);
  await writeFile(join(evidenceDirectory, `${explorer}.json`), JSON.stringify(state, null, 2));
  await browser.close();
}
if (state.failures.length) process.exitCode = 1;
console.log(JSON.stringify({ explorer, evidenceDirectory, failures: state.failures, timingsMs: state.timingsMs }, null, 2));
