import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { launchBrowser, sanitizeBody } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const explorer = process.env.LOOM_QA_EXPLORER;
assert(project && explorer && ['group-related-summary-browser-', 'cohort-add-fields-browser-'].some(prefix => explorer.startsWith(prefix)),
  'Set LOOM_CDA_PROJECT and LOOM_QA_EXPLORER to an owned group-related-summary-browser or cohort-add-fields-browser QA Explorer');
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const evidence = process.argv[2] ?? `/tmp/loom-row-actions-clarity-${Date.now()}`;
const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const base = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const report = {
  status: 'running', project, explorer, evidence, cases: [], transitions: [], workspaceChecks: [],
  sourceFingerprint: {}, target: { apiOrigin, uiOrigin, apiContainer, composeProject },
};
await mkdir(evidence, { recursive: true });
let browser;
let sourceFreeze;
let apiBuildFreeze;

function extractBuildIdentity(observation) {
  assert.equal(observation.status, 0, 'API build stamp check must succeed');
  const values = observation.stdout.trim().match(/^([a-f0-9]{64})\s+([a-f0-9]{64})\s+([a-f0-9]{64})$/i);
  assert(values, 'API build stamp must contain three SHA-256 identities');
  return values.slice(1).join(':').toLowerCase();
}

const read = async () => {
  const response = await fetch(`${apiOrigin}${base}/builder`, { signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  report.apiReads ??= [];
  report.apiReads.push({ path: `${base}/builder`, status: response.status, ...(response.ok ? {} : { diagnosticBody: sanitizeBody(text) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
  return JSON.parse(text);
};
const wait = async condition => browser.page.waitForFunction(condition, null, { timeout: 5000 });
const stateOf = (locator, fn) => locator.evaluate(fn);
const action = async (name, locator, method) => performAction(report, name, locator, target => method(target));
const assertWorkspaceUnchanged = async phase => {
  const current = await read();
  assert.deepEqual(current.workspace, report.before.workspace, `${phase}: opening and closing row actions must not save workspace changes`);
  assert.equal(current.draftVersion, report.before.draftVersion, `${phase}: draft version must remain unchanged`);
  assert.equal(current.draftDigest, report.before.draftDigest, `${phase}: draft digest must remain unchanged`);
  report.workspaceChecks.push({ phase, unchanged: true, draftVersion: current.draftVersion, draftDigest: current.draftDigest });
};

async function timedTransition(name, locator, method, settledWhen) {
  const started = Date.now();
  report.activeAction = { label: name, locator: locator.toString(), startedAt: started };
  try {
    await action(name, locator, method);
    report.activeAction = { label: name, locator: locator.toString(), startedAt: started };
    await wait(settledWhen);
  } catch (error) {
    const durationMs = Date.now() - started;
    report.transitions.push({ name, durationMs, limitMs: 5000, status: 'failed', error: String(error) });
    report.firstFailedAction ??= { label: name, locator: locator.toString(), elapsedMs: durationMs };
    throw new Error(`${name} did not render within 5000 ms (observed ${durationMs} ms): ${error.message}`);
  }
  report.activeAction = undefined;
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms to render (limit 5000 ms)`);
  report.transitions.push({ name, durationMs, limitMs: 5000, status: 'passed' });
}

try {
  report.target.ownership = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot });
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { watchedFileCount: sourceFreeze.watchedFileCount };
  report.sourceFingerprint.before = sourceFingerprint(sourceRoot);
  let firstBuildObservation;
  apiBuildFreeze = await captureApiBuildFreeze(async () => {
    firstBuildObservation = await checkContainerApiBuildStamp(apiContainer);
    return firstBuildObservation;
  });
  report.apiBuildIdentity = { before: extractBuildIdentity(firstBuildObservation) };
  report.before = await read();
  const outputId = report.before.workspace.documents[0]?.output.id;
  assert(outputId, 'The owned QA Explorer must contain an output table');
  browser = await launchBrowser({ evidence, appOrigins: [uiOrigin, apiOrigin], noAuth: true });
  const { page } = browser;
  await page.setViewportSize({ width: 1280, height: 900 });
  const builderURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  report.activeAction = { label: 'navigate to QA Builder', locator: builderURL, startedAt: Date.now() };
  await page.goto(builderURL, { waitUntil: 'domcontentloaded' });
  report.activeAction = undefined;
  const tableCard = page.locator(`[data-testid="construction-table-${outputId}"]`);
  await tableCard.waitFor({ state: 'visible', timeout: 30000 });
  await action('open output table', tableCard, target => target.click());
  const rowsTrigger = page.getByTestId('construction-rows-settings-trigger');
  await rowsTrigger.waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await rowsTrigger.isEnabled(), true);

  const inspectCards = async name => {
    const device = name.startsWith('mobile') ? 'mobile' : 'desktop';
    const related = page.getByTestId('construction-action-related-rows');
    await timedTransition(
      `${device}-row-actions-open`, rowsTrigger, target => target.click(),
      () => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.getClientRects().length),
    );
    const relatedState = await stateOf(related, button => ({ disabled: button.disabled, text: button.innerText }));
    assert(!relatedState.disabled, `Related row action is disabled: ${relatedState.text}`);
    const cards = await page.locator('[data-testid^="construction-action-"][data-testid$="-rows"]').evaluateAll(buttons => buttons.map(button => {
      const kind = button.getAttribute('data-testid').replace(/^construction-action-/, '').replace(/-rows$/, '');
      const bounds = button.getBoundingClientRect();
      return {
        kind, text: button.innerText, accessibleName: button.getAttribute('aria-label'), disabled: button.disabled,
        visible: bounds.width > 0 && bounds.left >= 0 && bounds.right <= innerWidth,
      };
    }));
    const expectedKinds = ['expand', 'group', 'keep', 'pivot', 'related', 'table-pivot', 'unpivot'];
    assert.deepEqual(cards.map(card => card.kind).sort(), expectedKinds, 'All seven row/action controls must be rendered');
    assert(cards.every(card => card.visible), JSON.stringify(cards));
    const byKind = Object.fromEntries(cards.map(card => [card.kind, card]));
    for (const kind of ['group', 'keep', 'pivot', 'related', 'table-pivot', 'unpivot']) {
      assert.equal(byKind[kind].disabled, false, `${kind} action should be enabled`);
    }
    assert.equal(byKind.expand.disabled, true, 'Expand must remain unavailable without a supported array column');
    assert(byKind.expand.text.includes('Make one row per list value'));
    assert(byKind.expand.text.includes('public array-valued column'), 'Disabled Expand must explain why it is unavailable');
    assert(byKind.group.text.includes('Combine rows into groups'));
    assert(byKind.group.text.includes('matching values in the fields you choose'));
    assert(byKind.keep.text.includes('Filter rows'));
    assert(byKind.keep.accessibleName?.includes('Choose which rows appear in the table output'), 'Filter rows must explain its effect');
    assert(byKind.pivot.text.includes('Turn categories into columns'));
    assert(byKind.pivot.text.includes('Make a column for each category'));
    assert(byKind['table-pivot'].text.includes('Choose category and value fields'));
    assert(byKind.unpivot.text.includes('Turn columns into rows'));
    assert(byKind.unpivot.text.includes('Other columns repeat on each new row'));
    const relatedCard = cards.find(card => card.kind === 'related');
    assert(relatedCard?.text.includes('Make a row for each related record'));
    assert(relatedCard.text.toLowerCase().includes('existing values') || relatedCard.text.toLowerCase().includes('original values'));
    assert(relatedCard.text.includes('keep a current row once when no records match'));
    assert(relatedCard.text.includes('By default'));
    assert(cards.find(card => card.kind === 'group')?.text.includes('Combine rows into groups'));
    assert(cards.find(card => card.kind === 'unpivot')?.text.includes('Other columns repeat on each new row'));
    report.dialogLayout = await page.getByRole('dialog', { name: 'Row definition settings' }).evaluate(dialog => {
      const ancestors = [dialog, dialog.parentElement];
      let node = dialog.parentElement?.parentElement;
      while (node) { ancestors.push(node); node = node.parentElement; }
      return ancestors.map(element => ({
        tag: element.tagName, className: String(element.className), position: getComputedStyle(element).position,
        zIndex: getComputedStyle(element).zIndex, transform: getComputedStyle(element).transform,
        overflow: getComputedStyle(element).overflow, top: element.getBoundingClientRect().top,
      }));
    });
    await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
    report.cases.push({ name, actionInventory: cards, cards });
  };

  await inspectCards('desktop-actions');
  const relatedAction = page.getByTestId('construction-action-related-rows');
  await timedTransition(
    'desktop-related-editor-open', relatedAction, target => target.click(),
    () => Boolean(document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length),
  );
  const editorLocator = page.getByTestId('construction-related-expand-editor');
  const editor = await editorLocator.innerText();
  assert(editor.includes('Make a row for each related record'));
  assert(editor.includes('Start from'));
  const startControl = await editorLocator.evaluate(editorNode => {
    const label = [...editorNode.querySelectorAll('label,div')].find(node => node.innerText.trim().startsWith('Start from'));
    return { present: Boolean(label), hiddenInDetails: Boolean(label?.closest('details')), text: label?.innerText };
  });
  assert(startControl.present, 'Starting records must be explained in the main form');
  assert(!startControl.hiddenInDetails, 'Starting-record choice must not be hidden in Advanced options');
  report.startControl = startControl;
  const noMatchControl = await editorLocator.evaluate(editorNode => {
    const select = [...editorNode.querySelectorAll('select')].find(node => [...node.options].some(option => option.value === 'PRESERVE_PARENT'));
    return { present: Boolean(select), hiddenInDetails: Boolean(select?.closest('details')), value: select?.value };
  });
  assert(noMatchControl.present, 'The no-match choice must be available');
  assert(!noMatchControl.hiddenInDetails, 'The no-match outcome must not require opening Advanced options');
  assert.equal(noMatchControl.value, 'PRESERVE_PARENT');
  report.noMatchControl = noMatchControl;
  for (const text of ['What will change', 'Rows before and after', 'Patient A + Observation 1', 'Patient A + Observation 2', 'Fields you can add', 'changes which records those fields come from']) assert(editor.includes(text), `Related editor must explain: ${text}`);
  report.cases.push({ name: 'related-editor-explanation', editor });
  await assertWorkspaceUnchanged('desktop editor open');
  const closeEditor = page.getByTestId('construction-close-operation-editor');
  await timedTransition(
    'desktop-related-editor-close', closeEditor, target => target.click(),
    () => (!document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length)
      && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false,
  );

  await page.setViewportSize({ width: 390, height: 844 });
  await inspectCards('mobile-actions');
  await timedTransition(
    'mobile-related-editor-open', page.getByTestId('construction-action-related-rows'), target => target.click(),
    () => Boolean(document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length),
  );
  const mobileEditor = await editorLocator.innerText();
  for (const text of ['What will change', 'Rows before and after', 'Fields you can add']) assert(mobileEditor.includes(text));
  report.cases.push({ name: 'mobile-related-action', editor: mobileEditor });
  await assertWorkspaceUnchanged('mobile editor open');
  await timedTransition(
    'mobile-related-editor-close', closeEditor, target => target.click(),
    () => (!document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length)
      && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false,
  );
  await assertWorkspaceUnchanged('mobile editor closed');
  report.errors = [...browser.diagnostics.console, ...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
  report.incidentalAssets = browser.diagnostics.assetFailures;
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, { phase: 'row-action-clarity', action: report.activeAction, explorer, draftVersion: report.before?.draftVersion, draftDigest: report.before?.draftDigest });
  process.exitCode = 1;
} finally {
  await browser?.close();
  if (apiBuildFreeze) {
    try {
      report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged();
      assert.equal(report.apiBuildFreeze.checked, true);
      assert.equal(report.apiBuildFreeze.unchanged, true);
      assert.equal(report.apiBuildFreeze.invalidatesRun, false);
    } catch (error) {
      report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreezeError = String(error.stack ?? error); process.exitCode = 1;
    }
  }
  if (sourceFreeze) {
    try {
      report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
      report.sourceFreeze.check = await sourceFreeze.assertUnchanged();
      assert.equal(report.sourceFreeze.check.unchanged, true);
      assert.equal(report.sourceFreeze.check.invalidatesRun, false);
    } catch (error) {
      report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreezeError = String(error.stack ?? error); process.exitCode = 1;
    }
  }
  if (report.apiBuildIdentity.before) {
    try {
      const after = await checkContainerApiBuildStamp(apiContainer);
      report.apiBuildIdentity.after = extractBuildIdentity(after);
      assert.equal(report.apiBuildIdentity.after, report.apiBuildIdentity.before, 'Running API build identity changed during verification');
    } catch (error) {
      report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildIdentity.error = String(error.stack ?? error); process.exitCode = 1;
    }
  }
  report.finishedAt = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, evidence, cases: report.cases.map(value => value.name), transitions: report.transitions, error: report.error })}\n`);
