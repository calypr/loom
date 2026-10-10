import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';


export async function runImplicitPivotWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const waitNative = (callback, args = {}, timeout = 5000) => cda.wait(callback, args ?? {}, Math.min(timeout, 5000));
const project = cda.project;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
const explorerId = cda.explorer;
assert(project, 'Set LOOM_CDA_PROJECT for the isolated CDA deployment.');
assert(explorerId, 'Set LOOM_CDA_EXPLORER_ID to an owned isolated explorer.');
assert.notEqual(explorerId, 'cda-builder-full-qa-1790440983382', 'The protected shared Explorer cannot be targeted.');

const mode = originalArgs.mode ?? originalArgs[0] ?? 'root';
assert(['root', 'related', 'related-source-key'].includes(mode), 'Mode must be root, related, or related-source-key.');
const related = mode !== 'root';
const relatedSourceKey = mode === 'related-source-key';
const resourceType = related ? 'Specimen' : 'Observation';
const selectedResourceId = related
  ? '230b352c-99de-50f3-a3b1-a4f6680615ab'
  : '485e2567-b566-56f3-b5bd-5f025f37cd95';
const anchorObservationId = '485e2567-b566-56f3-b5bd-5f025f37cd95';
const evidence = cda.evidence;
const artifact = join(evidence, 'report.json');
const tableName = `Implicit pivot fields QA ${randomUUID().slice(0, 8)}`;
const explorerPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}`;
const authoringPath = `${explorerPath}/authoring/v2`;
const baseURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const report = {
  project, explorerId, mode, resourceType, selectedResourceId, tableName,
  cases: [], actions: [], timingsMs: {}, nativeRequests: [], setupRequests: [], errors: [],
  started: new Date().toISOString(),
};

await mkdir(evidence, { recursive: true });
const target = cda.target;
report.target = target;
const arangoDatabase = (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE) ?? 'loom_dev';
const rawQuery = query => {
  const output = execFileSync('docker', [
    'exec', target.arangoContainer, 'arangosh', '--server.database', arangoDatabase,
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 200000 });
  const start = output.indexOf('[');
  assert(start >= 0, 'Raw CDA oracle returned no JSON array.');
  return JSON.parse(output.slice(start));
};
const api = async (path, body) => {
  const request = { path, method: body ? 'POST' : 'GET', requestId: randomUUID(), ...(body ? { body } : {}) };
  report.setupRequests.push(request);
  const response = await fetch(apiOrigin + path, {
    method: request.method,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': request.requestId },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  request.status = response.status;
  request.response = await response.json();
  assert(response.ok, JSON.stringify(request));
  return request.response;
};
const recordAction = async (selector, label, identity = {}) => {
  const state = await cda.click(selector, identity);
  report.actions.push({ label, ...state });
};
const responseSince = async (startIndex, predicate, timeout = 5000) => {
  await browserEvents.flush();
  const existing = report.nativeRequests.slice(startIndex).find(predicate);
  return existing ?? cda.waitForCapturedResponse(browserEvents,
    entry => report.nativeRequests.indexOf(entry) >= startIndex && predicate(entry), timeout);
};
const clickLastControl = async (prefix, label) => {
  const testIds = await cda.inspect( ([start]) => [...document.querySelectorAll(`[data-testid^="${start}"]`)]
    .map(element => element.getAttribute('data-testid')), [prefix]);
  assert(testIds.length > 0, `${label}: no matching control exists.`);
  const selector = `[data-testid=${JSON.stringify(testIds.at(-1))}]`;
  await recordAction(selector, label);
};
const capturedResponses = () => report.nativeRequests
  .filter(request => request.response && request.path.startsWith(authoringPath))
  .map(request => ({ path: request.path, method: request.method, status: request.status, body: request.response }));

let browserEvents;
let builder;
let selectionURL = baseURL;
let created = false;
let allowCleanupDialog = false;

try {
  builder = await api(`${authoringPath}/builder`);
  const generation = builder.catalog.generation;
  assert(generation, 'Builder catalog did not provide a dataset generation.');
  report.generation = generation;
  const [anchor] = rawQuery(`FOR o IN Observation
    FILTER o.project == ${JSON.stringify(project)}
    FILTER o.dataset_generation == ${JSON.stringify(generation)}
    FILTER o.id == ${JSON.stringify(anchorObservationId)}
    RETURN {id:o.id,status:o.payload.status,subject:o.payload.subject.reference,specimen:o.payload.specimen.reference}`);
  assert(anchor, `Source Observation ${anchorObservationId} is missing for project ${project}, generation ${generation}.`);
  report.oracle = anchor;
  assert.equal(anchor.status, 'final');
  assert.equal(anchor.specimen, `Specimen/${selectedResourceId}`);
  assert.equal(anchor.subject, 'Patient/da65b4e6-3946-50d9-ab1a-65af2e560b1c');

  const selection = await api(`/api/v1/projects/${encodeURIComponent(project)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `implicit-pivot-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: [{ project, generation, resourceType, id: selectedResourceId }] } },
  });
  assert(selection.id, 'Production selection request returned no selection identity.');
  selectionURL = `${baseURL}&selection=${encodeURIComponent(selection.id)}`;
  report.selection = { id: selection.id, project, generation, resourceType, id: selectedResourceId };
  cda.onDialog(async ({ type }) => {
    if (!allowCleanupDialog) report.errors.push({ kind: 'dialog', type });
    return { accept: allowCleanupDialog };
  });
  browserEvents = cda.captureRequests(authoringPath, { apiOrigin, uiOrigin });

  const wait = (predicate, args = {}, timeout = 5000) => cda.wait(predicate, args ?? {}, Math.min(timeout, 5000));
  const apply = async expectedHistoryCount => {
    const startIndex = report.nativeRequests.length;
    await recordAction('[data-testid="construction-apply-proposal"]', 'Apply construction proposal');
    await responseSince(startIndex, request => request.path.endsWith('/commands') && request.method === 'POST');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    await wait((count) => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === count, expectedHistoryCount);
  };
  const openTable = async () => {
    await cda.navigate(selectionURL);
    await wait(() => document.body.innerText.includes('DATASET WORKSPACE'));
    const testIds = await cda.inspect( ([name]) => [...document.querySelectorAll('[data-testid^="construction-table-"]')]
      .filter(button => button.innerText.trim().endsWith(name)).map(button => button.getAttribute('data-testid')), [tableName]);
    assert.equal(testIds.length, 1, `Created table must have one visible table control: ${JSON.stringify(testIds)}`);
    await recordAction(`[data-testid=${JSON.stringify(testIds[0])}]`, 'Open saved table');
    await wait(({ name }) => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), { name: tableName });
  };
  await cda.navigate(selectionURL);
  await wait(() => document.body.innerText.includes('DATASET WORKSPACE'));
  assert.equal(await cda.inspect( () => [...document.querySelectorAll('button')].some(button => button.innerText.trim() === 'Preview')), false,
    'Manual Preview button should not exist before table selection.');
  await recordAction('button', 'Create new table', { name: 'New table' });
  await wait(({ resourceType }) => Boolean(document.querySelector(`button[aria-label="Choose ${resourceType} rows"]:not(:disabled)`)), { resourceType });
  await cda.fill('#first-table-name', tableName);
  await recordAction(`button[aria-label="Choose ${resourceType} rows"]`, `Choose ${resourceType} rows`);
  created = true;
  await wait(({ name }) => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), { name: tableName });
  await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
  await recordAction('[data-testid="construction-rows-settings-trigger"]', 'Open row settings');
  await wait(() => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"]')));
  await wait(() => [...document.querySelectorAll('[aria-label="Starting collection"] button')]
    .some(button => button.innerText === 'Use selected resources' && !button.disabled));
  await recordAction('[aria-label="Starting collection"] button', 'Use selected resources', { name: 'Use selected resources' });
  await wait(([type]) => document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes(`1 ${type} resources attached`), [resourceType]);
  await recordAction('[role="dialog"][aria-label="Row definition settings"] button', 'Back to table', { name: 'Back to table' });
  await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
  await recordAction('[data-testid="construction-rows-settings-trigger"]', 'Open row settings');

  if (related) {
    await wait(() => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false);
    await recordAction('[data-testid="construction-action-related-rows"]', 'Expand related rows');
    const relatedType = '[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]';
    await wait(({ selector }) => Boolean(document.querySelector(selector)), { selector: relatedType });
    await cda.selectOption(relatedType, 'Observation');
    await wait(() => [...document.querySelectorAll('[data-testid="construction-related-expand-editor"] input[type="radio"]')]
      .some(input => input.getAttribute('aria-label')?.toLowerCase().includes('specimen') && !input.disabled));
    const specimenRadio = '[data-testid="construction-related-expand-editor"] input[type="radio"]';
    await cda.click(specimenRadio, { includes: 'specimen' });
    await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready');
    await apply(1);
    await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false);
    await recordAction('[data-testid="construction-rows-settings-trigger"]', 'Open row settings');
  }

  await wait(() => Boolean(document.querySelector('[data-testid="construction-action-pivot-rows"]')));
  await wait(() => document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled === false);
  report.choice = await cda.inspect( () => {
    const button = document.querySelector('[data-testid="construction-action-pivot-rows"]');
    return { disabled: button.disabled, text: button.innerText };
  });
  assert.equal(report.choice.disabled, false, report.choice.text);
  await recordAction('[data-testid="construction-action-pivot-rows"]', 'Pivot rows');
  if (!related) {
    await wait(() => [...document.querySelectorAll('button')].some(button => button.innerText.trim() === 'Change row operation'));
    await recordAction('button', 'Change row operation', { name: 'Change row operation' });
    await wait(() => Boolean(document.querySelector('[data-testid="construction-reshape-choice-pivot"]:not(:disabled)')));
    await recordAction('[data-testid="construction-reshape-choice-pivot"]', 'Choose pivot operation');
  }
  await wait(() => document.querySelectorAll('select[aria-label="Pivot category field"] optgroup option').length > 0);
  report.options = await cda.inspect( () => [...document.querySelectorAll('select[aria-label="Pivot category field"] option')]
    .map(option => ({ label: option.text, value: option.value, disabled: option.disabled })));
  const category = report.options.find(option => option.label.toLowerCase().includes('status'));
  const value = report.options.find(option => option.label.toLowerCase().includes('subject.reference'));
  assert(category && value, JSON.stringify(report.options));
  await cda.selectOption('select[aria-label="Pivot values field"]', value.value);
  if (related && !relatedSourceKey) {
    await recordAction('input[aria-label^="Pivot group "]', `Group by ${resourceType} ID`, { name: `Pivot group ${resourceType} ID` });
  } else {
    if (relatedSourceKey) {
      const checkedGroups = await cda.inspect( () => [...document.querySelectorAll('input[aria-label^="Pivot group "]:checked')]
        .map(input => input.getAttribute('aria-label')));
      for (const name of checkedGroups) {
        await recordAction('input[aria-label^="Pivot group "]', `Uncheck ${name}`, { name });
      }
    }
    const group = report.options.find(option => option.label.toLowerCase().includes('specimen.reference'));
    assert(group, JSON.stringify(report.options));
    await cda.selectOption('select[aria-label="Add pivot group field"]', group.value);
  }
  await cda.selectOption('select[aria-label="Pivot category field"]', category.value);
  const scanStarted = Date.now();
  const categoryRequestStart = report.nativeRequests.length;
  await recordAction('button', 'Find category values', { name: 'Find category values' });
  await wait(() => Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')));
  report.timingsMs.categories = Date.now() - scanStarted;
  const categoryResponse = await responseSince(categoryRequestStart, request => /construction-(?:choice-)?proposals/.test(request.path) && request.status === 200);
  assert(categoryResponse.response, 'Category scan must have a retained production response.');
  await wait(() => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')));
  report.proposal = await cda.inspect( () => ({
    status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),
    text: document.body.innerText.slice(-1600),
  }));
  assert.equal(report.proposal.status, 'ready', report.proposal.text);
  assert(report.proposal.text.includes(report.oracle.subject), report.proposal.text);
  await apply(related ? 2 : 1);

  await openTable();
  await wait(({ subject }) => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(subject), { subject: report.oracle.subject });
  report.saved = await cda.inspect( () => ({
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText),
    rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText)),
  }));
  assert.deepEqual(report.saved.rows, [[related && !relatedSourceKey ? anchorObservationId : report.oracle.specimen.slice('Specimen/'.length), report.oracle.subject]]);
  await clickLastControl('construction-history-step-', 'Select last construction step');
  await wait(() => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')));
  await clickLastControl('construction-edit-step-', 'Edit last construction step');
  await wait(() => Boolean(document.querySelector('select[aria-label="Pivot category field"]')));
  report.reopened = await cda.inspect( () => [...document.querySelectorAll('select[aria-label^="Pivot"]')]
    .map(select => ({ label: select.getAttribute('aria-label'), text: select.selectedOptions[0]?.text, value: select.value })));
  assert(report.reopened.find(select => select.label === 'Pivot category field')?.text.toLowerCase().includes('status'), JSON.stringify(report.reopened));
  await recordAction('[data-testid="construction-reshape-pivot-advanced"] summary', 'Open advanced Pivot settings');
  await cda.selectOption('select[aria-label="Pivot missing cell policy"]', 'ERROR');
  const editStarted = Date.now();
  const editRequestStart = report.nativeRequests.length;
  const editedResponse = await responseSince(editRequestStart, request => /construction-(?:choice-)?proposals/.test(request.path) && request.method === 'POST', 5000);
  assert.equal(editedResponse.status, 200, 'Editing Pivot settings must obtain a new successful proposal response.');
  await wait(() => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')));
  report.timingsMs.editProposal = Date.now() - editStarted;
  assert.equal(await cda.inspect( () => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), 'ready');
  await apply(related ? 2 : 1);

  await openTable();
  await clickLastControl('construction-history-step-', 'Select last construction step');
  await wait(() => Boolean(document.querySelector('[data-testid^="construction-remove-step-"]:not(:disabled)')));
  const removeStarted = report.nativeRequests.length;
  await clickLastControl('construction-remove-step-', 'Remove last construction step');
  await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready');
  const removeResponse = await responseSince(removeStarted, request => /construction-(?:choice-)?proposals/.test(request.path) && request.method === 'POST');
  assert.equal(removeResponse.status, 200, 'Removing the construction step must obtain a successful proposal response.');
  await apply(related ? 1 : 0);

  await openTable();
  await wait(([id]) => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(id), [anchorObservationId]);
  report.restored = await cda.inspect( () => ({
    historyCount: document.querySelectorAll('[data-testid^="construction-history-step-"]').length,
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText),
  }));
  assert.equal(report.restored.historyCount, related ? 1 : 0);
  assert.deepEqual(report.restored.headers, related ? ['SPECIMEN ID', 'OBSERVATION FHIR RESOURCE ID'] : ['OBSERVATION ID']);

  await browserEvents.flush();
  report.responses = capturedResponses();
  assert.equal(report.nativeRequests.filter(request => request.status >= 400).length, 0, JSON.stringify(report.nativeRequests));
  assert.equal(report.setupRequests.filter(request => request.status >= 400).length, 0, JSON.stringify(report.setupRequests));
  assert(Object.values(report.timingsMs).every(ms => ms < 5000), JSON.stringify(report.timingsMs));
  includeFixtureDiagnostics(report);
  assert.equal(report.errors.length, 0, JSON.stringify(report.errors));
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (browserEvents) await browserEvents.flush().catch(() => undefined);
if (page) {
    includeFixtureDiagnostics(report);
    report.failureUI = await cda.inspect( () => document.body.innerText).catch(() => undefined);
    await captureFailure(error, {
      phase: 'implicit-pivot-lifecycle', mode, project, explorerId, generation: report.generation,
      draftVersion: builder?.draftVersion, draftDigest: builder?.draftDigest,
      selectionId: report.selection?.id, action: report.activeAction,
    });
  }
  report.__nativeFailure = true;
} finally {
  if (created) {
    try {
      await cda.navigate(selectionURL);
      await waitNative(({ tableName }) => [...document.querySelectorAll('button')]
        .some(button => button.innerText.trim().endsWith(tableName)), { tableName }, 5000);
      await recordAction('button', 'Open temporary table for cleanup', { includes: tableName });
      await waitNative( () => document.querySelector('[data-testid="construction-delete-table"]')?.disabled === false, {}, 5000);
      allowCleanupDialog = true;
      await recordAction('[data-testid="construction-delete-table"]', 'Delete temporary table');
      await waitNative(({ tableName }) => ![...document.querySelectorAll('button')]
        .some(button => button.innerText.trim().endsWith(tableName)), { tableName }, 5000);
      report.cleanup = 'deleted';
    } catch (error) {
      report.cleanup = 'failed';
      report.cleanupError = String(error.message ?? error);
      report.status = 'failed';
      report.__nativeFailure = true;
    }
  }
  if (browserEvents) {
    await browserEvents.flush().catch(() => undefined);
    report.responses = capturedResponses();
    includeFixtureDiagnostics(report);
  }
  report.finished = new Date().toISOString();
  await writeFile(artifact, JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-implicit-pivot.mjs workflow failed');
  await cda.attachReport('verify-cda-implicit-pivot.mjs', report);
  return report;
}
