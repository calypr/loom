import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import {
  apiBuildIdentity,
  measuredAction,
  record,
  targetFromEnvironment,
} from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import {
  sourceFingerprintChangedPaths,
  sourceFingerprintWithManifest,
} from './verify-ui/source-fingerprint.mjs';
import { assertExactTableNames, assertPreviewShape, tableManagementActions } from './verify-cda-builder-table-management-contract.mjs';

const tableNames = async page => page.locator('[data-testid^="construction-table-"]')
  .evaluateAll(buttons => buttons.map(button => (button.innerText || button.textContent || '')
    .trim().split('\n').at(-1).trim()));

const tableState = async page => page.locator('[data-testid^="construction-table-"]')
  .evaluateAll(buttons => buttons.map(button => ({
    outputId: button.getAttribute('data-testid')?.replace('construction-table-', ''),
    title: (button.innerText || button.textContent || '').trim().split('\n').at(-1).trim(),
    selected: button.getAttribute('aria-pressed') === 'true',
  })));

const builderIdentity = async page => page.getByTestId('construction-workspace').evaluate(workspace => ({
  draftVersion: workspace.getAttribute('data-draft-version'),
  draftDigest: workspace.getAttribute('data-draft-digest'),
}));

const previewState = async page => {
  const table = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
  await requireUnique(table, 'Builder preview table');
  return table.evaluate(element => ({
    rowCount: element.getAttribute('aria-rowcount'),
    columnCount: element.getAttribute('aria-colcount'),
    headers: [...element.querySelectorAll('[role="columnheader"]')].map(header => header.innerText.trim()),
  }));
};

const navButton = (page, name) => page.getByRole('navigation', { name: 'Tables', exact: true })
  .getByRole('button', { name, exact: true });

const snapshot = async page => ({
  explorerId: await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue(),
  tables: await tableState(page),
  draft: await builderIdentity(page),
});

const actionWithRender = async (tracker, name, locator, action, rendered, options = {}) => {
  const startedAt = Date.now();
  tracker.actionStartedAt = startedAt;
  const actionMs = await measuredAction(tracker, name, locator, action, rendered, options);
  const elapsedMs = Date.now() - startedAt;
  assert(elapsedMs <= 5000, `${name} took ${elapsedMs} ms to render (maximum 5000 ms)`);
  return { actionMs, elapsedMs };
};

const waitForNames = async (page, expected) => page.waitForFunction(
  expectedNames => {
    const actual = [...document.querySelectorAll('[data-testid^="construction-table-"]')]
      .map(button => (button.innerText || button.textContent || '').trim().split('\n').at(-1).trim());
    return JSON.stringify(actual) === JSON.stringify(expectedNames);
  }, expected, { timeout: 5000 },
);

const openBuilder = async (page, target, explorerId) => {
  const url = new URL(target.uiUrl);
  url.searchParams.set('project', target.fixtureProject);
  url.searchParams.set('explorer', explorerId);
  url.searchParams.set('mode', 'builder');
  await page.goto(url.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
  await requireUnique(explorer, 'Explorer selector');
  assert.equal(await explorer.inputValue(), explorerId, 'Builder opened on a different Explorer');
  return url.toString();
};

async function runVerifyDuplicateAndDelete({ page, report, tracker }) {
  const copy = page.getByRole('button', { name: 'Specimen copy', exact: true });
  await requireUnique(copy, 'Specimen copy table');
  await actionWithRender(tracker, 'select Specimen copy', copy,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('heading', { name: 'Specimen copy', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));

  const preview = page.getByRole('button', { name: 'Preview', exact: true });
  await requireUnique(preview, 'Preview');
  await actionWithRender(tracker, 'preview Specimen copy', preview,
    button => button.click({ timeout: 5000 }),
    () => page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 5000 }));
  const copied = await previewState(page);
  assertPreviewShape(copied, 5);
  record(report, 'Duplicate preview retains five visible columns', true, copied);

  const deleteButton = navButton(page, 'Delete table');
  await requireUnique(deleteButton, 'Delete selected table');
  let confirmation;
  page.once('dialog', async dialog => {
    confirmation = dialog.message();
    await dialog.accept();
  });
  await actionWithRender(tracker, 'delete Specimen copy', deleteButton,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('button', { name: 'Specimen copy', exact: true }).waitFor({ state: 'detached', timeout: 5000 }));
  assert.equal(confirmation, 'Delete Specimen copy?', 'Delete confirmation did not identify the requested table');
  const remaining = await tableNames(page);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  await waitForNames(page, ['Specimen']);
  const reloaded = await tableNames(page);
  assertExactTableNames(reloaded, ['Specimen'], 'Tables after duplicate deletion and reload');
  record(report, 'Deleting duplicate persists after reload', true, { remaining, reloaded, confirmation });
  report.lifecycle = { preview: 'passed', duplicate: 'passed', removal: 'passed', reload: 'passed', edit: 'untested', restoration: 'not applicable' };
}

async function runCleanupOrphanPatient({ page, report, tracker }) {
  const before = await tableNames(page);
  const patientTables = before.filter(name => name === 'Patient');
  assert.equal(patientTables.length, 1, `Expected one orphan Patient table, found ${patientTables.length}`);
  const patient = page.getByRole('button', { name: 'Patient', exact: true });
  await requireUnique(patient, 'Orphan Patient table');
  await actionWithRender(tracker, 'select orphan Patient table', patient,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('heading', { name: 'Patient', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
  const deleteButton = navButton(page, 'Delete table');
  await requireUnique(deleteButton, 'Delete selected Patient table');
  let confirmation;
  page.once('dialog', async dialog => {
    confirmation = dialog.message();
    await dialog.accept();
  });
  await actionWithRender(tracker, 'delete orphan Patient table', deleteButton,
    button => button.click({ timeout: 5000 }),
    () => patient.waitFor({ state: 'detached', timeout: 5000 }));
  assert.equal(confirmation, 'Delete Patient?', 'Delete confirmation did not identify the Patient table');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const after = await tableNames(page);
  assert(!after.includes('Patient'), 'Orphan Patient table remains after reload');
  record(report, 'Orphan Patient removal persists after reload', true, { before, after, confirmation });
  report.lifecycle = { removal: 'passed', reload: 'passed', restoration: 'not applicable', preview: 'untested', edit: 'untested' };
}

async function runDuplicateTable({ page, report, tracker }) {
  const namesBefore = await tableNames(page);
  const duplicateTitle = 'Specimen copy';
  assert(!namesBefore.includes(duplicateTitle), `Refusing to duplicate: ${duplicateTitle} already exists`);
  const duplicateButton = navButton(page, 'Duplicate table');
  await requireUnique(duplicateButton, 'Duplicate table');
  await actionWithRender(tracker, 'duplicate selected table', duplicateButton,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('button', { name: duplicateTitle, exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
  const copiedNames = await tableNames(page);
  assert(copiedNames.includes(duplicateTitle), 'Duplicate action did not create Specimen copy');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const reloadedNames = await tableNames(page);
  assertExactTableNames(reloadedNames, ['Specimen', 'Specimen copy'], 'Duplicated table names after reload');
  const inputs = page.locator('input[aria-label^="Table name for "]');
  const values = await inputs.evaluateAll(elements => elements.map(input => input.value));
  assert.deepEqual(values, ['Specimen', 'Specimen copy'], 'Duplicate names were not restored from persisted table state');
  const successfulCommands = report.browserDiagnostics?.apiResponses?.filter(response =>
    response.url.endsWith('/commands') && response.status === 200) ?? [];
  assert.equal(successfulCommands.length, 1, 'Expected exactly one successful commands API response');
  record(report, 'Duplicate table is visible and persisted', true, { copiedNames, reloadedNames, values, successfulCommands });
  report.lifecycle = { duplicate: 'passed', reload: 'passed', edit: 'untested', removal: 'untested', restoration: 'untested' };
}

async function runRenameReorderUndo({ page, report, tracker }) {
  const starting = await tableNames(page);
  assert(starting.includes('BodyStructure'), 'BodyStructure table is required for rename/reorder/undo case');
  const table = page.getByRole('button', { name: 'BodyStructure', exact: true });
  await requireUnique(table, 'BodyStructure table');
  await actionWithRender(tracker, 'select BodyStructure table', table,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('heading', { name: 'BodyStructure', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
  const rename = navButton(page, 'Rename BodyStructure');
  await requireUnique(rename, 'Rename BodyStructure');
  let promptMessage;
  page.once('dialog', async dialog => {
    promptMessage = dialog.message();
    await dialog.accept('Body structures QA');
  });
  await actionWithRender(tracker, 'rename BodyStructure table', rename,
    button => button.click({ timeout: 5000 }),
    () => page.getByRole('button', { name: 'Body structures QA', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
  assert.equal(promptMessage, 'Table name', 'Rename prompt was not the expected table-name prompt');
  const moveUp = navButton(page, 'Move Body structures QA up');
  await requireUnique(moveUp, 'Move Body structures QA up');
  await actionWithRender(tracker, 'move Body structures QA up', moveUp,
    button => button.click({ timeout: 5000 }),
    () => page.waitForFunction(() => {
      const names = [...document.querySelectorAll('[data-testid^="construction-table-"]')]
        .map(button => (button.innerText || button.textContent || '').trim().split('\n').at(-1).trim());
      return names[0] === 'Body structures QA';
    }, undefined, { timeout: 5000 }));
  const moved = await tableNames(page);
  assert.equal(moved[0], 'Body structures QA', 'Rename and move-up did not put the table first');
  const undo = page.getByRole('button', { name: 'Undo last saved draft change', exact: true });
  await requireUnique(undo, 'Undo last saved draft change');
  await actionWithRender(tracker, 'undo last saved table change', undo,
    button => button.click({ timeout: 5000 }),
    () => page.waitForFunction(() => {
      const names = [...document.querySelectorAll('[data-testid^="construction-table-"]')]
        .map(button => (button.innerText || button.textContent || '').trim().split('\n').at(-1).trim());
      return names[1] === 'Body structures QA';
    }, undefined, { timeout: 5000 }));
  const undone = await tableNames(page);
  assert.equal(undone[1], 'Body structures QA', 'Undo did not restore the table order');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const reloaded = await tableNames(page);
  assertExactTableNames(reloaded, undone, 'Renamed table inventory after reload');
  record(report, 'Rename, reorder, undo, and reload match', true, { starting, moved, undone, reloaded });
  report.lifecycle = { edit: 'passed', reorder: 'passed', undo: 'passed', reload: 'passed', preview: 'untested', removal: 'untested' };
}

async function runInspectTables({ page, report }) {
  const tables = await tableState(page);
  assert(tables.length > 0, 'No table controls are visible in this Explorer');
  assert.equal(new Set(tables.map(table => table.outputId)).size, tables.length, 'Table output IDs are ambiguous');
  assert(tables.every(table => table.outputId && table.title), 'A table control has no identity or visible name');
  const state = {
    inputs: await page.locator('input').evaluateAll(inputs => inputs.map(input => ({
      label: input.getAttribute('aria-label'), value: input.value, disabled: input.disabled,
    }))),
    tables,
    alerts: await page.getByRole('alert').allInnerTexts(),
    workspaceText: (await page.locator('body').innerText()).slice(0, 1200),
  };
  report.inspection = state;
  record(report, 'Visible table inventory has distinct identities', true, { tableNames: tables.map(table => table.title), tables });
  report.lifecycle = { inspection: 'passed', preview: 'untested', apply: 'not applicable', reload: 'untested', edit: 'untested', removal: 'untested' };
}

async function runSwitchExplorers({ page, report, tracker, explorerId, secondExplorerId, expectedSecondExplorerTable }) {
  assert(String(secondExplorerId ?? '').trim(), 'Switch explorers requires an explicit second Explorer ID');
  assert.notEqual(secondExplorerId, explorerId, 'Switch explorer IDs must be distinct');
  assert(String(expectedSecondExplorerTable ?? '').trim(), 'Switch explorers requires the expected table name in the second Explorer');
  const first = await snapshot(page);
  const select = page.getByRole('combobox', { name: 'Explorer', exact: true });
  await requireUnique(select, 'Explorer selector');
  const options = await select.locator('option').evaluateAll(items => items.map(option => ({
    value: option.value, label: option.label,
  })));
  assert(options.some(option => option.value === secondExplorerId), 'The explicit second Explorer is not selectable in the current project');
  await actionWithRender(tracker, 'switch to second Explorer', select,
    control => control.selectOption(secondExplorerId, { timeout: 5000 }),
    async () => {
      await page.waitForFunction(expectedId => document.querySelector('select[aria-label="Explorer"]')?.value === expectedId,
        secondExplorerId, { timeout: 5000 });
      await page.getByRole('button', { name: expectedSecondExplorerTable, exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    });
  const switched = await snapshot(page);
  assert.equal(switched.explorerId, secondExplorerId, 'Explorer selection did not switch');
  assert(switched.tables.some(table => table.title === expectedSecondExplorerTable), 'Second Explorer table marker is absent');
  assert.notDeepEqual(switched.tables, first.tables, 'Explorer switch did not change the visible table set');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
  await requireUnique(explorer, 'Explorer selector after reload');
  assert.equal(await explorer.inputValue(), explorerId, 'Reload did not restore the requested starting Explorer');
  const restored = await snapshot(page);
  assert.deepEqual(restored.tables, first.tables, 'Reload did not restore the starting Explorer tables');
  record(report, 'Explorer switch and URL reload restore distinct table inventories', true, { first, switched, restored, options });
  report.lifecycle = { switch: 'passed', reload: 'passed', preview: 'untested', apply: 'not applicable', edit: 'untested', removal: 'untested' };
}

export async function runBuilderTableManagement({ action, explorerId, secondExplorerId, expectedSecondExplorerTable, env = process.env } = {}) {
  assert(tableManagementActions.has(action), `Unsupported Builder table action: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const timestamp = new Date().toISOString().replaceAll(':', '-');
  const caseName = action.toLowerCase().replaceAll(' ', '-');
  const evidenceDirectory = resolve(target.artifacts, `playwright-table-management-${caseName}-${timestamp}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-table-management',
    case: action,
    status: 'running',
    target: {
      sourceRoot: target.sourceRoot,
      sourceFingerprint: sourceAtStart.fingerprint,
      apiBuildIdentity: buildAtStart,
      composeProject: target.composeProject,
      apiContainer: env.LOOM_CDA_API_CONTAINER,
      uiOrigin: target.uiUrl,
      apiOrigin: target.apiUrl,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
      explorerId,
      secondExplorerId: secondExplorerId || undefined,
    },
    path: `Builder > ${action}`,
    expectedVisibleResult: 'The requested table or Explorer state is reached through visible Builder controls and remains correct after reload where the case applies.',
    independentOracle: 'Table identity, exact table inventory, visible preview shape, browser API responses, and persisted Builder state are checked independently of click success.',
    lifecycle: {},
    evidenceDirectory,
    assertions: [],
    actions: [],
    timings: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let pageURL;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  let failure;
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
    report.browserDiagnostics = diagnostics;
    activeAction = { label: 'open Builder', locator: target.uiUrl };
    const navigationStarted = Date.now();
    pageURL = await openBuilder(page, target, explorerId);
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStarted, status: 'passed' });
    switch (action) {
      case 'Verify duplicate and delete':
        await runVerifyDuplicateAndDelete({ page, report, tracker });
        break;
      case 'Cleanup orphan Patient table':
        await runCleanupOrphanPatient({ page, report, tracker });
        break;
      case 'Duplicate table':
        await runDuplicateTable({ page, report, tracker });
        break;
      case 'Rename reorder undo table':
        await runRenameReorderUndo({ page, report, tracker });
        break;
      case 'Inspect tables':
        await runInspectTables({ page, report });
        break;
      case 'Switch explorers':
        await runSwitchExplorers({ page, report, tracker, explorerId, secondExplorerId, expectedSecondExplorerTable });
        break;
    }
    report.browserDiagnostics = diagnostics;
    const unexpectedErrors = diagnostics.console.length > 0 || diagnostics.pageErrors.length > 0
      || diagnostics.networkFailures.length > 0 || diagnostics.httpFailures.length > 0;
    record(report, 'No unexpected console, page, request, or API failures', !unexpectedErrors, diagnostics);
    report.status = action === 'Inspect tables' ? 'partial' : 'partial';
    report.statusReason = 'This focused table-management case does not establish the complete Builder preview/edit/removal lifecycle.';
    await page.screenshot({ path: resolve(evidenceDirectory, 'final-state.png'), fullPage: true });
    report.evidence = ['final-state.png'];
  } catch (error) {
    failure = error;
    report.failure = { action: tracker.activeAction?.label ?? activeAction.label, message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, {
        action: tracker.activeAction ?? activeAction,
        elapsedMs: tracker.actionStartedAt ? Date.now() - tracker.actionStartedAt : undefined,
        target: report.target,
        pageURL,
        lifecycle: report.lifecycle,
        draftIdentity: await builderIdentity(browser.page).catch(() => undefined),
      });
      report.browserDiagnostics = browser.diagnostics;
    }
  } finally {
    report.actions.push(...tracker.actions);
    report.timings = tracker.timings;
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const sourceUnchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: sourceUnchanged ? 'passed' : 'failed',
        evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!sourceUnchanged) failure ??= new Error('Watched source changed during the browser run');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtEnd === buildAtStart;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed',
        evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the browser run');
    } catch (error) {
      failure ??= error;
      report.identityCheckError = sanitizeText(error.message ?? error);
    }
    const failedAssertion = report.assertions.some(assertion => assertion.status === 'failed');
    const missingAssertions = report.assertions.length === 0 || report.assertions.some(assertion => !assertion.name || !assertion.status);
    if (failure || failedAssertion || missingAssertions) report.status = 'failed';
    else if (report.status === 'running') report.status = 'partial';
    await writeFile(resolve(evidenceDirectory, 'report.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
  }
  if (failure) throw Object.assign(failure, { reportPath: resolve(evidenceDirectory, 'report.json'), report });
  return report;
}
