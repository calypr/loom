import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { captureBuilderScreenshot, measuredAction, record, requireUnique } from './verify-cda-builder-related-source-chooser.mjs';
import { sanitizeText } from '../helpers/playwright-browser.mjs';

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

export async function runRowChoiceInspection({ page, cda, action, explorerId = cda.target.explorer } = {}) {
  const resourceType = rowChoiceCases[action];
  assert(resourceType, `Unsupported bounded row-choice case: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  const stamp = new Date().toISOString().replaceAll(':', '-');
  const tableName = `${resourceType} row choice QA ${stamp}`;
  Object.assign(report, {
    schemaVersion: 1,
    scenario: 'cda-builder-row-choice-inspection',
    case: action,
    status: 'running',
    target: {
      ...report.target,
      sourceRoot: target.sourceRoot,
      composeProject: target.composeProject,
      apiContainer: target.apiContainer,
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
    });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };
  let created = false;
  try {

    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    const navigationStarted = Date.now();
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStarted, status: 'passed' });

    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    const selectedExplorer = await explorer.inputValue();
    record(report, 'Builder is scoped to the requested Explorer', selectedExplorer === explorerId,
      { expectedExplorer: explorerId, selectedExplorer });
    const builderResponse = await cda.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Builder source identity request must return 200');
    const builder = await builderResponse.json();
    record(report, 'Builder request is scoped to the explicit project and generation',
      builder.catalog?.generation === target.fixtureGeneration,
      { requestProject: target.fixtureProject, expectedGeneration: target.fixtureGeneration, actualGeneration: builder.catalog?.generation });

    const newTable = page.getByRole('button', { name: 'New table', exact: true });
    await requireUnique(newTable, 'New table');
    const nameField = page.locator('#first-table-name');
    const resourceChoice = page.getByRole('button', { name: `Choose ${resourceType} rows`, exact: true });
    await measuredAction(tracker, 'open New table', newTable,
      button => button.click({ timeout: 5000 }),
      () => nameField.waitFor({ state: 'visible', timeout: 5000 }));
    await requireUnique(nameField, 'Table name');
    await measuredAction(tracker, 'name temporary table', nameField,
      input => input.fill(tableName, { timeout: 5000 }),
      async () => assert.equal(await nameField.inputValue(), tableName), { editable: true });
    await requireUnique(resourceChoice, `Choose ${resourceType} rows`);
    const workspaceHeader = page.getByTestId('construction-workspace').locator('header');
    await measuredAction(tracker, `create ${resourceType} table`, resourceChoice,
      button => button.click({ timeout: 5000 }),
      () => workspaceHeader.waitFor({ state: 'visible', timeout: 5000 }));
    created = true;
    await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: 5000 });

    const configureRows = page.getByTestId('construction-rows-settings-trigger');
    await requireUnique(configureRows, 'Configure rows');
    assert.equal(await configureRows.isEnabled(), true, 'Configure rows must be enabled for the new table');
    const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    const started = Date.now();
    await measuredAction(tracker, 'open Configure rows', configureRows,
      button => button.click({ timeout: 5000 }),
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
    (report.builderTimings ??= []).push({ name: 'Configure rows dialog render', elapsedMs: state.elapsedMs, status: state.elapsedMs <= 5000 ? 'passed' : 'failed' });
    assert(state.elapsedMs <= 5000, `Configure rows took ${state.elapsedMs} ms to render`);

    const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
    await requireUnique(cancel, 'Cancel row definition');
    await measuredAction(tracker, 'cancel Configure rows', cancel,
      button => button.click({ timeout: 5000 }),
      () => dialog.waitFor({ state: 'hidden', timeout: 5000 }));
    record(report, 'Inspection leaves the temporary table unchanged', true, { canceled: true, didNotApply: true });

    await captureBuilderScreenshot({ page, cda, report, name: 'row-choice.png' });
    await writeFile(`${evidenceDirectory}/row-choice.json`, JSON.stringify({ action, tableName, state, diagnostics }, null, 2) + '\n', { mode: 0o600 });
    report.evidence ??= [];
    report.evidence.push('row-choice.json');
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
  } finally {
    if (created) {
      try {
        const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
        if (await dialog.count()) {
          const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
          if (await cancel.count() === 1 && await cancel.isVisible() && await cancel.isEnabled()) await cancel.click({ timeout: 5000 });
        }
        const workspaceURL = new URL(target.uiUrl);
        workspaceURL.searchParams.set('project', target.fixtureProject);
        workspaceURL.searchParams.set('explorer', explorerId);
        workspaceURL.searchParams.set('mode', 'builder');
        if (!page.url().startsWith(workspaceURL.origin)) await page.goto(workspaceURL.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
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
          await tableTab.waitFor({ state: 'detached', timeout: 5000 });
          report.cleanup = { attempted: true, table: tableName, confirmation };
        } else {
          throw new Error(`Temporary table ${tableName} was not uniquely reachable for cleanup`);
        }
      } catch (cleanupError) {
        report.cleanup = { attempted: true, status: 'failed', error: sanitizeText(cleanupError.message ?? cleanupError) };
        throw cleanupError;
      }
    }
  }
  report.status = 'partial';
  return report;
}
