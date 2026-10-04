import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { launchBrowser, sanitizeBody } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

assert.equal(process.argv.length, 3, 'usage: LOOM_CDA_PROJECT=... LOOM_CDA_UI_ORIGIN=... LOOM_CDA_API_ORIGIN=... LOOM_CDA_API_CONTAINER=... LOOM_CDA_COMPOSE_PROJECT=... node scripts/verify-population-row-ui.mjs SELECTION_EVIDENCE.json');
const selectionEvidencePath = resolve(process.argv[2]);
const evidenceInput = JSON.parse(await readFile(selectionEvidencePath, 'utf8'));
const selection = evidenceInput.selections?.explicit;
assert.ok(selection?.id, 'Selection evidence has no explicit revision');
assert.ok(evidenceInput.target?.uiUrl && evidenceInput.target?.project && evidenceInput.explorerId, 'Selection evidence has no browser target');
const project = process.env.LOOM_CDA_PROJECT;
const explorer = evidenceInput.explorerId;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
assert.equal(project, evidenceInput.target.project, 'Selection handoff project must match the explicitly owned project');
assert.equal(new URL(evidenceInput.target.uiUrl).origin, uiOrigin, 'Selection handoff UI origin must match the explicit isolated UI origin');
const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const apiBase = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const query = new URLSearchParams({ project, explorer, mode: 'builder', selection: selection.id });
const url = `${uiOrigin}/?${query}`;
const evidence = `/tmp/loom-population-row-ui-${Date.now()}`;
const artifact = join(evidence, 'population-row-ui.png');
const report = { status: 'running', scope: 'selection handoff and visible Builder population lifecycle', selectionEvidencePath, selectionRevisionId: selection.id, project, explorer, url, evidence, assertions: [], apiReads: [] };
await mkdir(evidence, { recursive: true });
let browser; let sourceFreeze; let apiBuildFreeze;
const button = name => browser.page.getByRole('button', { name, exact: true });
async function click(label, control) { await performAction(report, label, control, target => target.click()); }
async function readBuilder() {
  const response = await fetch(`${apiBase}/builder`, { signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  report.apiReads.push({ path: '/builder', status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
  return JSON.parse(text);
}
async function waitText(text, timeout = 60000) {
  await browser.page.getByText(text, { exact: true }).waitFor({ state: 'visible', timeout });
}
try {
  report.ownership = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot });
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
  let initialStamp;
  apiBuildFreeze = await captureApiBuildFreeze(async () => { initialStamp = await checkContainerApiBuildStamp(apiContainer); return initialStamp; });
  report.apiBuildIdentity = initialStamp.stdout.trim();
  report.before = await readBuilder();
  browser = await launchBrowser({ evidence, appOrigins: [uiOrigin, apiOrigin], noAuth: true });
  const { page } = browser;
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.screenshot({ path: artifact, fullPage: true });
  await page.waitForFunction(() => document.body.innerText.includes('3 selected DocumentReference resources are ready to constrain this table.')
    || [...document.querySelectorAll('button')].some(control => control.textContent.trim() === 'Use all authorized rows'), null, { timeout: 30000 });
  const useAll = button('Use all authorized rows');
  const useAllCount = await useAll.count();
  assert(useAllCount <= 1, `Expected at most one Use all authorized rows control, found ${useAllCount}`);
  if (useAllCount === 1 && await useAll.isVisible()) {
    await click('use all authorized rows for selection handoff', useAll);
    await waitText('3 selected DocumentReference resources are ready to constrain this table.');
  }
  await click('attach selected resources', button('Use selected resources'));
  await waitText('3 DocumentReference resources constrain one row per Specimen.');
  const preview = button('Preview');
  await preview.waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await preview.isEnabled(), true);
  await click('preview selected-resource population', preview);
  await page.getByText('dev-specimen-001', { exact: false }).first().waitFor({ state: 'visible', timeout: 60000 });
  let previewText = await page.locator('body').innerText();
  assert.equal(previewText.includes('dev-specimen-002'), false, 'Preview included a specimen outside the attached selection');
  const coverage = button('Check selected-resource coverage');
  await coverage.waitFor({ state: 'visible', timeout: 30000 });
  await click('check selected-resource coverage', coverage);
  await waitText('3 selected · 2 produce rows · 1 needs attention');
  const reportText = await page.locator('body').innerText();
  assert(reportText.includes('DocumentReference/dev-file-004'), 'Coverage report omitted the bounded unmatched resource');
  report.assertions.push('Builder loads immutable selection handoff', 'preview excludes the unselected specimen', 'coverage report retains the exact unmatched DocumentReference');

  await click('remove unmatched resource from collection', button('Remove from collection'));
  await waitText('2 DocumentReference resources constrain one row per Specimen.');
  await preview.waitFor({ state: 'visible', timeout: 30000 });
  await click('preview revised selection', preview);
  await page.getByText('dev-specimen-001', { exact: false }).first().waitFor({ state: 'visible', timeout: 60000 });
  await coverage.waitFor({ state: 'visible', timeout: 30000 });
  await click('check revised selection coverage', coverage);
  await waitText('2 selected · 2 produce rows · 0 needs attention');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitText('2 DocumentReference resources constrain one row per Specimen.');
  const reloaded = await page.locator('body').innerText();
  assert.equal(reloaded.includes('2 selected · 2 produce rows · 0 needs attention'), false, 'Reload must clear stale coverage evidence');
  assert.equal(reloaded.includes('3 DocumentReference resources constrain one row per Specimen.'), false, 'Reload restored the old population selection');
  report.after = await readBuilder();
  report.assertions.push('removing DocumentReference/dev-file-004 creates and attaches a new two-member selection', 'revised selection survives reload and stale coverage evidence clears');
  report.scopeLimit = 'Expected preview membership is preserved from the original fixed fixture IDs; this script does not independently derive the three-file selection from raw CDA-FHIR source.';
  report.diagnostics = browser.diagnostics;
  assert.deepEqual(browser.diagnostics.console, [], 'Unexpected browser console error');
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'Unexpected browser exception');
  assert.deepEqual(browser.diagnostics.httpFailures, [], 'Unexpected local HTTP failure');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Unexpected local network failure');
  report.status = 'partial';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, { phase: 'population-row-ui', action: report.activeAction, project, explorer, selectionRevisionId: selection.id, draftVersion: report.before?.draftVersion, draftDigest: report.before?.draftDigest });
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (apiBuildFreeze) { try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); } catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreezeError = String(error.stack ?? error); process.exitCode = 1; } }
  if (sourceFreeze) { try { report.sourceFingerprint.after = sourceFingerprint(sourceRoot); report.sourceFreeze = await sourceFreeze.assertUnchanged(); } catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreezeError = String(error.stack ?? error); process.exitCode = 1; } }
  report.finishedAt = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, evidence, assertions: report.assertions, error: report.error })}\n`);
if (report.status === 'partial') process.exitCode ||= 2;
