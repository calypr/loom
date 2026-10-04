import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

export const rowChoiceCases = Object.freeze({
  'Inspect bounded Patient row choices': 'Patient',
  'Inspect bounded BodyStructure row choices': 'BodyStructure',
  'Inspect bounded Observation row choices': 'Observation',
});

export function summarizeRowChoiceDialog(dialogText, optionRows, resourceType, namedGroupsCollapsed) {
  assert.equal(typeof dialogText, 'string');
  assert(Array.isArray(optionRows));
  const options = optionRows.map(option => ({
    label: String(option.label ?? '').trim(),
    value: String(option.value ?? ''),
    disabled: Boolean(option.disabled),
    selected: Boolean(option.selected),
  }));
  assert(options.length > 0, 'The row shape selector has no choices');
  assert(options.some(option => option.selected), 'The row shape selector has no selected choice');
  assert(options.some(option => option.selected && !option.disabled), 'The current row shape must be enabled');
  assert(options.every(option => option.label && option.value), 'Every row choice must have a visible label and a value');
  return {
    dialogText,
    resourceType,
    options,
    namedGroupsCollapsed: Boolean(namedGroupsCollapsed),
    avoidsServerTerminology: !dialogText.includes('The server has no complete explicit group revisions'),
  };
}

export async function runRowChoiceInspection({ action, explorerId, env = process.env } = {}) {
  const resourceType = rowChoiceCases[action];
  assert(resourceType, `Unsupported bounded row-choice case: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const stamp = new Date().toISOString().replaceAll(':', '-');
  const evidenceDirectory = resolve(target.artifacts, `playwright-${resourceType.toLowerCase()}-row-choice-${stamp}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const tableName = `${resourceType} row choice QA ${stamp}`;
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-row-choice-inspection',
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
    },
    path: `Builder > New table > ${resourceType} rows > Configure rows`,
    expectedVisibleResult: 'The row definition dialog exposes labeled choices, keeps optional named-group setup collapsed, and avoids leaking server implementation language.',
    independentOracle: 'This inspection asserts UI affordances only; it makes no claim about preview values or persisted row results.',
    lifecycle: { rowChoiceDialog: 'untested', preview: 'not applicable', apply: 'not applicable', reload: 'not applicable', edit: 'not applicable', removal: 'not applicable' },
    evidenceDirectory,
    assertions: [],
    actions: [],
    timings: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let created = false;
  let failure;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: builderURL.toString() };
    const navigationStarted = Date.now();
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStarted, status: 'passed' });

    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    const selectedExplorer = await explorer.inputValue();
    record(report, 'Builder is scoped to the requested Explorer', selectedExplorer === explorerId,
      { expectedExplorer: explorerId, selectedExplorer });
    const builderResponse = await browser.context.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Builder source identity request must return 200');
    const builder = await builderResponse.json();
    record(report, 'Builder request is scoped to the explicit project and generation',
      builder.catalog?.generation === target.fixtureGeneration,
      { requestProject: target.fixtureProject, expectedGeneration: target.fixtureGeneration, actualGeneration: builder.catalog?.generation });

    const newTable = page.getByRole('button', { name: 'New table', exact: true });
    await requireUnique(newTable, 'New table');
    activeAction = { label: 'open New table', locator: newTable.toString(), targetLocator: newTable };
    const nameField = page.locator('#first-table-name');
    const resourceChoice = page.getByRole('button', { name: `Choose ${resourceType} rows`, exact: true });
    await measuredAction(tracker, 'open New table', newTable,
      (button, { timeout }) => button.click({ timeout }),
      () => nameField.waitFor({ state: 'visible', timeout: 5000 }));
    await requireUnique(nameField, 'Table name');
    activeAction = { label: 'name temporary table', locator: nameField.toString(), targetLocator: nameField };
    await measuredAction(tracker, 'name temporary table', nameField,
      (input, { timeout }) => input.fill(tableName, { timeout }),
      async () => assert.equal(await nameField.inputValue(), tableName), { editable: true });
    await requireUnique(resourceChoice, `Choose ${resourceType} rows`);
    activeAction = { label: `create ${resourceType} table`, locator: resourceChoice.toString(), targetLocator: resourceChoice };
    const workspaceHeader = page.getByTestId('construction-workspace').locator('header');
    await measuredAction(tracker, `create ${resourceType} table`, resourceChoice,
      (button, { timeout }) => button.click({ timeout }),
      () => workspaceHeader.waitFor({ state: 'visible', timeout: 5000 }));
    created = true;
    await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: 30000 });

    const configureRows = page.getByTestId('construction-rows-settings-trigger');
    await requireUnique(configureRows, 'Configure rows');
    assert.equal(await configureRows.isEnabled(), true, 'Configure rows must be enabled for the new table');
    activeAction = { label: 'open Configure rows', locator: configureRows.toString(), targetLocator: configureRows };
    const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    const started = Date.now();
    await measuredAction(tracker, 'open Configure rows', configureRows,
      (button, { timeout }) => button.click({ timeout }),
      () => dialog.waitFor({ state: 'visible', timeout: 5000 }));
    const select = dialog.getByLabel('New row shape', { exact: true });
    await requireUnique(select, 'New row shape');
    await select.waitFor({ state: 'visible', timeout: 5000 });
    const options = await select.locator('option').evaluateAll(nodes => nodes.map(option => ({
      label: option.textContent,
      value: option.value,
      disabled: option.disabled,
      selected: option.selected,
    })));
    const namedGroups = dialog.getByText('Create named groups from a saved selection', { exact: true });
    const namedGroupDetails = dialog.locator('details').filter({ has: namedGroups });
    const namedGroupCount = await namedGroupDetails.count();
    assert(namedGroupCount <= 1, `Named-group controls are ambiguous (${namedGroupCount})`);
    const namedGroupsCollapsed = namedGroupCount === 0 || !(await namedGroupDetails.first().evaluate(node => node.open));
    const dialogText = await dialog.innerText();
    const state = summarizeRowChoiceDialog(dialogText, options, resourceType, namedGroupsCollapsed);
    state.elapsedMs = Date.now() - started;
    assert(namedGroupsCollapsed, 'Optional named-group controls should remain collapsed by default');
    assert(state.avoidsServerTerminology, 'Row definition dialog exposed server implementation terminology');
    record(report, 'Row definition choices are visible and labeled', state.options.length > 0, { options: state.options });
    record(report, 'Optional named-group setup is collapsed', namedGroupsCollapsed);
    record(report, 'Dialog copy avoids server implementation details', state.avoidsServerTerminology);
    report.lifecycle.rowChoiceDialog = 'passed';
    report.timings.push({ name: 'Configure rows dialog render', elapsedMs: state.elapsedMs, status: state.elapsedMs <= 5000 ? 'passed' : 'failed' });
    assert(state.elapsedMs <= 5000, `Configure rows took ${state.elapsedMs} ms to render`);

    const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
    await requireUnique(cancel, 'Cancel row definition');
    activeAction = { label: 'cancel Configure rows', locator: cancel.toString(), targetLocator: cancel };
    await measuredAction(tracker, 'cancel Configure rows', cancel,
      (button, { timeout }) => button.click({ timeout }),
      () => dialog.waitFor({ state: 'hidden', timeout: 5000 }));
    record(report, 'Inspection leaves the temporary table unchanged', true, { canceled: true, didNotApply: true });

    await page.screenshot({ path: `${evidenceDirectory}/row-choice.png`, fullPage: true });
    await writeFile(`${evidenceDirectory}/row-choice.json`, JSON.stringify({ action, tableName, state, diagnostics }, null, 2) + '\n', { mode: 0o600 });
    report.evidence = ['row-choice.png', 'row-choice.json'];
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
  } catch (error) {
    failure = error;
    report.failure = { action: activeAction.label, locator: activeAction.locator, elapsedMs: tracker.activeAction?.startedAt ? Date.now() - tracker.activeAction.startedAt : undefined, message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, {
        action: { ...activeAction, startedAt: tracker.activeAction?.startedAt },
        elapsedMs: report.failure.elapsedMs,
        target: report.target,
      });
      report.browserDiagnostics = browser.diagnostics;
    }
  } finally {
    if (browser && created) {
      try {
        const page = browser.page;
        const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
        if (await dialog.count()) {
          const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
          if (await cancel.count() === 1 && await cancel.isVisible() && await cancel.isEnabled()) await cancel.click({ timeout: 5000 });
        }
        const workspaceURL = new URL(target.uiUrl);
        workspaceURL.searchParams.set('project', target.fixtureProject);
        workspaceURL.searchParams.set('explorer', explorerId);
        workspaceURL.searchParams.set('mode', 'builder');
        if (!page.url().startsWith(workspaceURL.origin)) await page.goto(workspaceURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
        const tableTab = page.getByRole('button', { name: tableName, exact: true });
        if (await tableTab.count() === 1 && await tableTab.isVisible() && await tableTab.isEnabled()) {
          await tableTab.click({ timeout: 5000 });
          const deleteTable = page.getByTestId('construction-delete-table');
          await requireUnique(deleteTable, 'Delete temporary table');
          assert.equal(await deleteTable.isEnabled(), true, 'Delete temporary table is disabled');
          let confirmation;
          page.once('dialog', async dialog => {
            confirmation = dialog.message();
            await dialog.accept();
          });
          await deleteTable.click({ timeout: 5000 });
          assert.equal(confirmation, `Delete ${tableName}?`, 'Unexpected table deletion confirmation');
          await tableTab.waitFor({ state: 'detached', timeout: 10000 });
          report.cleanup = { attempted: true, table: tableName, confirmation };
        } else {
          throw new Error(`Temporary table ${tableName} was not uniquely reachable for cleanup`);
        }
      } catch (cleanupError) {
        report.cleanup = { attempted: true, status: 'failed', error: sanitizeText(cleanupError.message ?? cleanupError) };
        failure ??= cleanupError;
      }
    }
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const sourceUnchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: sourceUnchanged ? 'passed' : 'failed', evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!sourceUnchanged) failure ??= new Error('Watched source changed during the browser run');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed', evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the browser run');
    } catch (freezeError) {
      report.freezeError = sanitizeText(freezeError.message ?? freezeError);
      report.assertions.push({ name: 'Watched source and API build stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= freezeError;
    }
    report.actions.push(...tracker.actions);
    report.timings = tracker.timings;
    report.status = failure || report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'partial';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const [action, explorerId] = process.argv.slice(2);
  runRowChoiceInspection({ action, explorerId }).then(report => {
    process.stdout.write(`${JSON.stringify({ status: report.status, evidenceDirectory: report.evidenceDirectory }, null, 2)}\n`);
  }).catch(error => {
    process.stderr.write(`${sanitizeText(error.stack ?? error)}\n`);
    process.exitCode = 1;
  });
}
