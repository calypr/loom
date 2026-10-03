import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const explorer = `last-table-command-qa-${Date.now()}`;
const origin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const evidence = process.argv[2] ?? `/tmp/loom-last-table-command-${Date.now()}`;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, project, scope: 'API commands only; native controls are not verified', requests: [] };
await mkdir(evidence, { recursive: true });
let state;
let browser;
report.nativeChecks = [];
report.errors = [];
const api = async (path, body) => {
  assert(path === root || path.startsWith(`${root}/${explorer}/`));
  const response = await fetch(origin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': randomUUID() },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, status: response.status, ...(response.ok ? {} : { response: value }) });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
const command = async (commands) => {
  const result = await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: state.workspace?.semanticsVersion ?? 10,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest, commands,
  });
  assert(Array.isArray(result.workspace.documents), 'Command responses must serialize documents as an array, including an empty workspace');
  assert(Array.isArray(result.workspace.tabs), 'Command responses must serialize tabs as an array, including an empty workspace');
  state = await api(`${base}/builder`);
  return result;
};
try {
  await api(root, { name: explorer, title: 'Last table command QA' });
  state = await api(`${base}/builder`);
  assert.equal(state.catalog.generation, 'cda-fhir-v1');
  assert.equal(state.workspace?.documents.length ?? 0, 0);
  const node = state.catalog.nodes.find((candidate) => candidate.resourceType === 'Specimen');
  assert(node);
  await command([{ type: 'CREATE_TABLE', rootNodeId: node.nodeId, title: 'Only table' }]);
  assert.equal(state.workspace.documents.length, 1);
  const before = structuredClone(state.workspace);
  const outputId = before.documents[0].output.id;
  await command([{ type: 'DELETE_TABLE', outputId }]);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  assert(state.previousDraftRevisionId, 'Deletion must retain a revision for Undo');
  const previousRevision = state.previousDraftRevisionId;
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0, 'Empty workspace must persist after reload');
  await command([{ type: 'RESTORE_DRAFT_REVISION', draftRevisionId: previousRevision }]);
  assert.deepEqual(state.workspace, before, 'Undo must restore the only table exactly');
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, before, 'Undo must persist after reload');
  const field = state.catalog.candidates.find((candidate) => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(field);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const nativeBaseline = structuredClone(state.workspace);
  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', (event) => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Network.responseReceived', ({ response }) => {
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url });
  });
  await navigate(browser.cdp, `${process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008'}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rename-table-${outputId}"]')?.disabled===false`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus==='ready'`);
  const renderedRows = () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);`);
  const originalRows = await renderedRows();
  assert(originalRows.length > 0, 'The source table must render real data before deletion');
  const start = Date.now();
  await click(browser.cdp, '[data-testid="construction-delete-table"]');
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('Build your first table')`, 5000);
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000);
  report.nativeChecks.push({ name: 'delete-last-table-to-empty-creator', durationMs });
  const restoreStart = Date.now();
  await click(browser.cdp, '[data-testid="construction-undo"]');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`, 5000);
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, nativeBaseline);
  await waitForBrowser(browser.cdp, `(() => {const preview=document.querySelector('[data-testid="construction-preview"]');return preview?.dataset.previewStatus==='ready'&&Number(preview.dataset.currentDraftVersion)===${state.draftVersion}&&preview.dataset.currentDraftDigest===${JSON.stringify(state.draftDigest)};})()`, 5000);
  const restoredRows = await renderedRows();
  assert(restoredRows.length > 0);
  for (const row of restoredRows) assert(originalRows.some((original) => JSON.stringify(original) === JSON.stringify(row)), 'Undo must render the original source values');
  const restoreDurationMs = Date.now() - restoreStart;
  assert(restoreDurationMs <= 5000);
  report.nativeChecks.push({ name: 'undo-last-table-deletion', durationMs: restoreDurationMs });
  await navigate(browser.cdp, `${process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008'}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]'))`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus==='ready'`);
  assert.deepEqual((await api(`${base}/builder`)).workspace, nativeBaseline);
  for (const row of await renderedRows()) assert(originalRows.some((original) => JSON.stringify(original) === JSON.stringify(row)));
  assert.deepEqual(report.errors, []);
  report.scope = 'API deletion/revision restore plus native last-table deletion, Undo, and reload';
  report.status = 'passed';
  report.outputId = outputId;
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText.slice(-12000);').catch(String) : undefined;
  process.exitCode = 1;
} finally {
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, explorer, evidence, error: report.error }));
