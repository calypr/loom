import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { launchBrowser, sanitizeBody } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const { values: args } = parseArgs({ options: {
  project: { type: 'string', default: process.env.LOOM_CDA_PROJECT }, explorer: { type: 'string' },
  output: { type: 'string' }, step: { type: 'string' },
  origin: { type: 'string', default: process.env.LOOM_CDA_UI_ORIGIN },
  'api-origin': { type: 'string', default: process.env.LOOM_CDA_API_ORIGIN },
  'api-container': { type: 'string', default: process.env.LOOM_CDA_API_CONTAINER },
  'compose-project': { type: 'string', default: process.env.LOOM_CDA_COMPOSE_PROJECT },
  evidence: { type: 'string', default: `/tmp/loom-removal-ui-${Date.now()}` }, apply: { type: 'boolean', default: false },
} });
assert(args.project && args.explorer && args.output && args.step && args.origin && args['api-origin'] && args['api-container'] && args['compose-project'],
  'Set explicit owned project/explorer/output/step and isolated UI/API origins, API container, and Compose project');
const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const apiBase = `${args['api-origin']}/api/v1/projects/${encodeURIComponent(args.project)}/explorers/${encodeURIComponent(args.explorer)}/authoring/v2`;
const builderURL = `${args.origin}/?project=${encodeURIComponent(args.project)}&explorer=${encodeURIComponent(args.explorer)}&mode=builder`;
const ids = { table: `construction-table-${args.output}`, step: `construction-history-step-${args.step}`, remove: `construction-remove-step-${args.step}` };
const report = { status: 'running', target: args, builderURL, applyRequested: args.apply, evidence: args.evidence, transitions: [], apiReads: [], proposalResponses: [] };
await mkdir(args.evidence, { recursive: true });
let browser; let sourceFreeze; let apiBuildFreeze;
const locator = id => browser.page.getByTestId(id);
async function click(label, control) { return performAction(report, label, control, target => target.click()); }
async function readBuilder() {
  const response = await fetch(`${apiBase}/builder`, { signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  report.apiReads.push({ path: '/builder', status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
  return JSON.parse(text);
}
async function observeProposal(label, startedAt, responsePromise) {
  report.activeAction = { label, locator: '[data-testid="construction-proposal-panel"]', startedAt };
  const response = await responsePromise;
  const body = await response.text();
  report.proposalResponses.push({ path: new URL(response.url()).pathname, status: response.status(), ...(response.ok() ? {} : { body: sanitizeBody(body) }) });
  assert(response.ok(), `Removal proposal returned ${response.status()}: ${sanitizeBody(body)}`);
  const proposal = JSON.parse(body);
  const panel = locator('construction-proposal-panel');
  await panel.waitFor({ state: 'visible', timeout: 30000 });
  await browser.page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', null, { timeout: 30000 });
  const view = await panel.evaluate(node => ({ status: node.getAttribute('data-proposal-status'), text: node.innerText, hasApply: Boolean(document.querySelector('[data-testid="construction-apply-proposal"]')) }));
  const elapsedMs = Date.now() - startedAt;
  report.transitions.push({ name: label, elapsedMs, limitMs: 5000, passed: elapsedMs <= 5000 });
  assert(elapsedMs <= 5000, `${label} took ${elapsedMs} ms`);
  assert.equal(view.status, 'ready', view.text);
  assert.equal(view.hasApply, true, 'Complete removal must be applicable');
  assert(view.text.includes('Nothing is saved until you apply this removal'));
  assert(proposal.dependencyImpact?.removedStepIds?.length > 0, 'Proposal must identify the complete removed-step set');
  const document = report.baseline.workspace.documents.find(value => value.output.id === args.output);
  const topLevel = document.construction.steps.filter(step => proposal.dependencyImpact.removedStepIds.includes(step.id) && !step.ownerStepId);
  for (const step of topLevel) {
    const warning = locator(`construction-removal-step-${step.id}`);
    assert.equal(await warning.count(), 1, `Removal warning for ${step.id} must be unique`);
    assert.equal(await warning.isVisible(), true, `Removal warning must name ${step.id}`);
  }
  report.activeAction = undefined;
  return proposal;
}
try {
  report.ownership = await assertOwnedCdaTarget({ project: args.project, apiOrigin: args['api-origin'], uiOrigin: args.origin, apiContainer: args['api-container'], composeProject: args['compose-project'], sourceRoot });
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
  let initialStamp;
  apiBuildFreeze = await captureApiBuildFreeze(async () => { initialStamp = await checkContainerApiBuildStamp(args['api-container']); return initialStamp; });
  report.apiBuildIdentity = initialStamp.stdout.trim();
  report.baseline = await readBuilder();
  const initialDocument = report.baseline.workspace.documents.find(document => document.output.id === args.output);
  assert(initialDocument, `Output ${args.output} is absent`);
  assert(initialDocument.construction.steps.some(step => step.id === args.step), `Step ${args.step} is absent`);
  browser = await launchBrowser({ evidence: args.evidence, appOrigins: [args.origin, args['api-origin']], noAuth: true });
  const { page } = browser;
  await page.setViewportSize({ width: 1280, height: 900 });
  report.activeAction = { label: 'navigate to Builder', locator: builderURL, startedAt: Date.now() };
  await page.goto(builderURL, { waitUntil: 'domcontentloaded' });
  report.activeAction = undefined;
  const table = locator(ids.table);
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await click('select output table', table);
  const step = locator(ids.step);
  await step.waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await step.isEnabled(), true, 'Requested history step must be enabled');
  await click('select construction step', step);
  const responsePromise = page.waitForResponse(response => response.url().startsWith(`${apiBase}/construction-proposals`) && response.request().method() === 'POST', { timeout: 30000 });
  const startedAt = Date.now();
  await click('request removal proposal', locator(ids.remove));
  const proposal = await observeProposal('remove-to-result', startedAt, responsePromise);
  report.preview = { candidateConstruction: proposal.candidateConstruction, removedStepIds: proposal.dependencyImpact.removedStepIds };
  await click('cancel removal proposal', locator('construction-cancel-proposal'));
  await locator('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 30000 });
  const afterCancel = await readBuilder();
  assert.deepEqual(afterCancel.workspace, report.baseline.workspace, 'Cancel must leave workspace unchanged');
  assert.equal(afterCancel.draftVersion, report.baseline.draftVersion, 'Cancel must not advance draft version');
  assert.equal(afterCancel.draftDigest, report.baseline.draftDigest, 'Cancel must not change draft digest');
  report.cancelCheck = { unchanged: true, draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest };
  if (args.apply) {
    await click('reselect construction step', step);
    const applyResponse = page.waitForResponse(response => response.url().startsWith(`${apiBase}/construction-proposals`) && response.request().method() === 'POST', { timeout: 30000 });
    const applyStartedAt = Date.now();
    await click('reopen removal proposal', locator(ids.remove));
    const applyProposal = await observeProposal('apply-proposal-ready', applyStartedAt, applyResponse);
    assert.deepEqual(applyProposal.candidateConstruction, proposal.candidateConstruction, 'Reopened proposal must match preview');
    await click('apply exact preview', locator('construction-apply-proposal'));
    await locator('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 30000 });
    report.applied = await readBuilder();
    assert(report.applied.draftVersion > report.baseline.draftVersion, 'Apply must advance the draft');
    const appliedDocument = report.applied.workspace.documents.find(document => document.output.id === args.output);
    assert.deepEqual(appliedDocument.construction, proposal.candidateConstruction, 'Apply must save the exact previewed construction');
    await page.reload({ waitUntil: 'domcontentloaded' });
    await table.waitFor({ state: 'visible', timeout: 30000 });
    await click('reopen output after reload', table);
    for (const id of proposal.dependencyImpact.removedStepIds) await locator(`construction-history-step-${id}`).waitFor({ state: 'detached', timeout: 30000 });
    report.reloaded = await readBuilder();
    assert.equal(report.reloaded.draftDigest, report.applied.draftDigest, 'Saved removal must persist after reload');
  }
  report.diagnostics = browser.diagnostics;
  assert.deepEqual(browser.diagnostics.console, [], 'Unexpected browser console error');
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'Unexpected browser exception');
  assert.deepEqual(browser.diagnostics.httpFailures, [], 'Unexpected local HTTP failure');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Unexpected local network failure');
  report.status = args.apply ? 'passed' : 'partial';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, { phase: 'construction-removal', action: report.activeAction, project: args.project, explorer: args.explorer, output: args.output, step: args.step, draftVersion: report.baseline?.draftVersion, draftDigest: report.baseline?.draftDigest });
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (apiBuildFreeze) { try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); } catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreezeError = String(error.stack ?? error); process.exitCode = 1; } }
  if (sourceFreeze) { try { report.sourceFingerprint.after = sourceFingerprint(sourceRoot); report.sourceFreeze = await sourceFreeze.assertUnchanged(); } catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreezeError = String(error.stack ?? error); process.exitCode = 1; } }
  report.finishedAt = new Date().toISOString();
  await writeFile(join(args.evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, evidence: args.evidence, transitions: report.transitions, error: report.error })}\n`);
if (report.status === 'partial') process.exitCode ||= 2;
