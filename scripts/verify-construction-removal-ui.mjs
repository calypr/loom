import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

const { values } = parseArgs({ options: {
  project: { type: 'string', default: 'loom_dev_cda_fhir' },
  explorer: { type: 'string' }, output: { type: 'string' }, step: { type: 'string' },
  origin: { type: 'string', default: 'http://127.0.0.1:30008' },
  'api-origin': { type: 'string', default: 'http://127.0.0.1:8188' },
  evidence: { type: 'string', default: `/tmp/loom-removal-ui-${Date.now()}` },
  apply: { type: 'boolean', default: false },
} });
assert(values.explorer && values.output && values.step, 'Pass --explorer, --output, and --step. Use --apply only with an isolated QA Explorer.');
const state = { failures: [], timingsMs: {}, target: values };
const tableSelector = `[data-testid=${JSON.stringify(`construction-table-${values.output}`)}]`;
const stepSelector = `[data-testid=${JSON.stringify(`construction-history-step-${values.step}`)}]`;
const readBuilder = async () => {
  const response = await fetch(`${values['api-origin']}/api/v1/projects/${encodeURIComponent(values.project)}/explorers/${encodeURIComponent(values.explorer)}/authoring/v2/builder`, { signal: AbortSignal.timeout(30000) });
  assert(response.ok);
  return response.json();
};
await mkdir(values.evidence, { recursive: true });
const browser = await launchBrowser(values.evidence);
const proposalReads = [];
state.browser = await browser.cdp.send('Browser.getVersion');
browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => state.failures.push({ kind: 'runtime', text: exceptionDetails.exception?.description ?? exceptionDetails.text }));
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/api/') && response.status >= 400) state.failures.push({ kind: 'http', url: response.url, status: response.status });
});
browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
  if (requestId === state.proposalRequestId) proposalReads.push(browser.cdp.send('Network.getResponseBody', { requestId }).then(({ body }) => {
    state.proposalResponse = JSON.parse(body);
  }));
});
browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
  if (response.url.endsWith('/construction-proposals')) state.proposalRequestId = requestId;
});
try {
  state.baseline = await readBuilder();
  await navigate(browser.cdp, `${values.origin}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(values.explorer)}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(tableSelector)})`);
  await click(browser.cdp, tableSelector);
  await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading your table') && document.querySelector(${JSON.stringify(stepSelector)})?.disabled === false`);
  await click(browser.cdp, stepSelector);
  const started = Date.now();
  await click(browser.cdp, `[data-testid="construction-remove-step-${values.step}"]`);
  await waitForBrowser(browser.cdp, `['needs-repair','ready','error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'))`);
  state.timingsMs.removeToResult = Date.now() - started;
  state.proposal = await browserEval(browser.cdp, `return {status:document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),text:document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,hasApply:Boolean(document.querySelector('[data-testid="construction-apply-proposal"]'))};`);
  assert.equal(state.proposal.status, 'ready', state.proposal.text);
  assert.equal(state.proposal.hasApply, true, 'The complete removal must be applicable');
  await Promise.all(proposalReads);
  assert(state.proposalResponse, 'Capture the exact removal proposal');
  const removedIds = state.proposalResponse.dependencyImpact.removedStepIds;
  const baselineDocument = state.baseline.workspace.documents.find(document => document.output.id === values.output);
  for (const step of baselineDocument.construction.steps.filter(step => removedIds.includes(step.id) && !step.ownerStepId)) {
    assert(await browserEval(browser.cdp, `return Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-removal-step-${step.id}"]`)}));`), `Removal warning must name ${step.id}`);
  }
  assert(state.proposal.text.includes('Nothing is saved until you apply this removal'));
  assert(state.timingsMs.removeToResult <= 5000);
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  state.after = await readBuilder();
  assert.equal(state.after.draftDigest, state.baseline.draftDigest);
  assert.equal(state.after.draftVersion, state.baseline.draftVersion);
  if (values.apply) {
    await click(browser.cdp, stepSelector);
    await click(browser.cdp, `[data-testid="construction-remove-step-${values.step}"]`);
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready' && document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled === false`);
    await Promise.all(proposalReads);
    const expectedConstruction = state.proposalResponse.candidateConstruction;
    await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
    await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 30000);
    state.applied = await readBuilder();
    assert(state.applied.draftVersion > state.baseline.draftVersion);
    const appliedDocument = state.applied.workspace.documents.find(document => document.output.id === values.output);
    assert.deepEqual(appliedDocument.construction, expectedConstruction, 'Apply must save the exact previewed construction');
    await navigate(browser.cdp, `${values.origin}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(values.explorer)}&mode=builder`);
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(tableSelector)})`);
    await click(browser.cdp, tableSelector);
    await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading your table')`);
    for (const id of removedIds) assert.equal(await browserEval(browser.cdp, `return Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-history-step-${id}"]`)}));`), false, 'Removed history must stay absent after reload');
    state.reloaded = await readBuilder();
    assert.equal(state.reloaded.draftDigest, state.applied.draftDigest);
  }
  assert.deepEqual(state.failures, []);
} catch (error) {
  state.failures.push({ kind: 'assertion', text: String(error.stack ?? error) });
} finally {
  state.body = await browserEval(browser.cdp, 'return document.body.innerText;').catch(String);
  await writeFile(join(values.evidence, 'report.json'), JSON.stringify(state, null, 2));
  await browser.close();
}
console.log(JSON.stringify({ evidence: values.evidence, failures: state.failures, timingsMs: state.timingsMs }, null, 2));
if (state.failures.length) process.exitCode = 1;
