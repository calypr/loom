import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { launchBrowser, sanitizeBody } from './lib/playwright-browser.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const explorer = process.argv[2] ?? process.env.LOOM_QA_EXPLORER;
const project = process.env.LOOM_CDA_PROJECT;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
assert(explorer && project, 'Usage: LOOM_CDA_PROJECT=... LOOM_CDA_UI_ORIGIN=... LOOM_CDA_API_ORIGIN=... LOOM_CDA_API_CONTAINER=... LOOM_CDA_COMPOSE_PROJECT=... node scripts/verify-cda-add-column-dialog.mjs EXPLORER_ID [EVIDENCE_DIR]');
const evidence = process.argv[3] ?? `/tmp/loom-add-column-dialog-${Date.now()}`;
const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const base = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
const report = { status: 'running', project, explorer, evidence, url, dialogs: [], transitions: [], target: { apiOrigin, uiOrigin, apiContainer, composeProject } };
await mkdir(evidence, { recursive: true });
let browser;
let sourceFreeze;
let apiBuildFreeze;

async function readBuilder() {
  const response = await fetch(`${apiOrigin}${base}/builder`, { signal: AbortSignal.timeout(30000) });
  const body = await response.text();
  report.apiReads ??= [];
  report.apiReads.push({ path: `${base}/builder`, status: response.status, ...(response.ok ? {} : { body: sanitizeBody(body) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(body)}`);
  return JSON.parse(body);
}

try {
  report.target.ownership = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot });
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFingerprint = { before: sourceFingerprint(sourceRoot) };
  let initialBuild;
  apiBuildFreeze = await captureApiBuildFreeze(async () => {
    initialBuild = await checkContainerApiBuildStamp(apiContainer);
    return initialBuild;
  });
  report.apiBuildIdentity = initialBuild.stdout.trim();
  report.before = await readBuilder();

  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  const { page } = browser;
  await page.setViewportSize({ width: 1280, height: 900 });
  report.activeAction = { label: 'navigate to Builder', locator: url, startedAt: Date.now() };
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  report.activeAction = undefined;
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 30000 });
  const suggestions = page.locator('[data-testid^="paired-column-suggestion-"]');
  await page.waitForFunction(() => document.querySelectorAll('[data-testid^="paired-column-suggestion-"]').length >= 3, null, { timeout: 30000 });
  const suggestionCount = await suggestions.count();
  assert(suggestionCount >= 3, `The current CDA table has ${suggestionCount} ready coded-value suggestions; expected at least three`);
  report.suggestionCount = suggestionCount;

  for (let index = 0; index < suggestionCount; index += 1) {
    const suggestion = suggestions.nth(index);
    await requireUnique(suggestion, `coded-value suggestion ${index + 1}`);
    assert.equal(await suggestion.isVisible(), true, `Coded-value suggestion ${index + 1} must be visible`);
    assert.equal(await suggestion.isEnabled(), true, `Coded-value suggestion ${index + 1} must be enabled`);
    const suggestionIdentity = await suggestion.evaluate(button => ({ testId: button.dataset.testid, label: button.getAttribute('aria-label'), text: button.innerText.trim() }));
    await performAction(report, `open ${suggestionIdentity.testId}`, suggestion, target => target.click());
    report.activeAction = { label: `wait for ${suggestionIdentity.testId} dialog`, locator: 'role=dialog', startedAt: Date.now() };
    const dialog = page.getByRole('dialog');
    await dialog.waitFor({ state: 'visible', timeout: 10000 });
    await requireUnique(dialog, `dialog for ${suggestionIdentity.testId}`);
    const details = await dialog.evaluate(element => {
      const rect = element.getBoundingClientRect();
      return {
        text: element.innerText.slice(0, 240),
        parent: element.parentElement?.parentElement?.tagName,
        rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
        viewport: { width: innerWidth, height: innerHeight },
        routeChoices: element.querySelectorAll('input[type="radio"]').length,
      };
    });
    assert(details.text.includes('Choose how to add these fields'), `${suggestionIdentity.label ?? suggestionIdentity.testId} opened an unexpected dialog`);
    assert.equal(details.parent, 'BODY', `${suggestionIdentity.label ?? suggestionIdentity.testId} dialog must be portaled outside a disclosure`);
    assert(details.rect.width > 0 && details.rect.height > 0 && details.rect.y >= 0 && details.rect.y < details.viewport.height, 'Dialog must be visible within the viewport');
    assert(details.routeChoices > 0, `${suggestionIdentity.label ?? suggestionIdentity.testId} has no route choice`);
    await page.screenshot({ path: join(evidence, `dialog-${index + 1}.png`), fullPage: true });
    const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
    await performAction(report, `cancel ${suggestionIdentity.testId} dialog`, cancel, target => target.click());
    report.activeAction = { label: `wait for ${suggestionIdentity.testId} dialog to close`, locator: 'role=dialog', startedAt: Date.now() };
    await dialog.waitFor({ state: 'hidden', timeout: 10000 });
    const current = await readBuilder();
    assert.deepEqual(current.workspace, report.before.workspace, 'Cancel must leave the Builder workspace unchanged');
    assert.equal(current.draftVersion, report.before.draftVersion, 'Cancel must not advance the draft version');
    assert.equal(current.draftDigest, report.before.draftDigest, 'Cancel must not change the draft digest');
    report.dialogs.push({ suggestion: suggestionIdentity, ...details, cancelled: true, workspaceUnchanged: true });
    report.activeAction = undefined;
  }
  assert.deepEqual(browser.diagnostics.console, [], 'Unexpected browser console errors');
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'Unexpected browser exceptions');
  assert.deepEqual(browser.diagnostics.httpFailures, [], 'Unexpected HTTP responses');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Unexpected local network failures');
  report.diagnostics = browser.diagnostics;
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, { phase: 'add-column-dialogs', action: report.activeAction, project, explorer, dialogsCompleted: report.dialogs.length });
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreezeError = String(error.stack ?? error); process.exitCode = 1; }
  }
  if (sourceFreeze) {
    try {
      report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
      report.sourceFreeze = await sourceFreeze.assertUnchanged();
    } catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreezeError = String(error.stack ?? error); process.exitCode = 1; }
  }
  report.finishedAt = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, evidence, dialogs: report.dialogs.length, error: report.error })}\n`);
