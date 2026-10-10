import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { captureBuilderScreenshot, measuredAction, record, requireUnique } from './verify-cda-builder-related-source-chooser.mjs';

const caseActions = new Set(['Inspect Patient field choice', 'Inspect selected Patient route', 'Inspect Patient proposal']);

export async function runPatientRelatedInspection({ page, cda, action, explorerId = cda.target.explorer } = {}) {
  assert(caseActions.has(action), `Unsupported Patient related inspection action: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  Object.assign(report, {
    schemaVersion: 1,
    scenario: 'cda-builder-patient-related-inspection',
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
    path: 'Builder > Add columns > Fields and related data > Patient > Patient.id > choose a displayed value form',
    expectedVisibleResult: action === 'Inspect Patient field choice'
      ? 'Patient.id opens a field-choice dialog with the visible matching-value forms and an actionable Add column control.'
      : action === 'Inspect selected Patient route'
        ? 'The chosen Patient.id form displays the route used to relate each Builder row to Patient records.'
        : 'Choosing Keep all matching values produces a ready proposal with a visible Apply control.',
    independentOracle: 'This is a control and route inspection only. It asserts accessible labels and visible state; it does not claim computed CDA row values.',
    lifecycle: { sourceSelection: 'untested', choiceDialog: 'untested', proposal: 'not applicable', apply: 'not applicable', reload: 'not applicable' },
    evidenceDirectory,
    });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };

    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    const navigationStart = Date.now();
    tracker.actionStartedAt = navigationStart;
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    const selectedExplorer = await explorer.inputValue();
    record(report, 'Builder is scoped to the requested Explorer', selectedExplorer === explorerId,
      { expectedExplorer: explorerId, selectedExplorer });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStart, status: 'passed' });

    const addColumns = page.locator('button[aria-label^="Add columns:"]');
    await requireUnique(addColumns, 'Add columns');
    const sourcePanel = page.getByTestId('construction-add-columns-source');
    await measuredAction(tracker, 'open Add columns', addColumns,
      button => button.click({ timeout: 5000 }),
      () => sourcePanel.waitFor({ state: 'visible', timeout: 5000 }));
    record(report, 'Add columns opens the source chooser', await sourcePanel.isVisible());

    const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await requireUnique(fieldsTab, 'Fields and related data tab');
    if (await fieldsTab.getAttribute('aria-pressed') === 'false') {
      await measuredAction(tracker, 'open Fields and related data', fieldsTab,
        button => button.click({ timeout: 5000 }),
        async () => {
          await page.getByRole('checkbox', { name: 'Select Patient.id', exact: true })
            .waitFor({ state: 'visible', timeout: 5000 });
        });
    }

    const patientSource = sourcePanel.getByRole('button', { name: /^Patient,/ });
    await requireUnique(patientSource, 'Patient related source');
    await measuredAction(tracker, 'select Patient related source', patientSource,
      button => button.click({ timeout: 5000 }),
      () => page.getByRole('checkbox', { name: 'Select Patient.id', exact: true })
        .waitFor({ state: 'visible', timeout: 5000 }));

    const patientID = page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    await requireUnique(patientID, 'Select Patient.id');
    assert.equal(await patientID.isEnabled(), true, 'Patient.id must be selectable');
    assert.equal(await patientID.isChecked(), false, 'Patient.id should not begin selected');
    const addField = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await requireUnique(addField, 'Add 1 selected feature');
    await measuredAction(tracker, 'select Patient.id', patientID,
      checkbox => checkbox.check({ timeout: 5000 }),
      async () => assert.equal(await addField.isEnabled(), true));
    record(report, 'Patient.id is selected as the only catalog feature',
      await patientID.isChecked() && await addField.isEnabled());
    const dialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
    await measuredAction(tracker, 'open Patient.id choice dialog', addField,
      button => button.click({ timeout: 5000 }),
      () => dialog.waitFor({ state: 'visible', timeout: 5000 }));
    record(report, 'Choice dialog identifies the Patient.id field',
      await dialog.getByRole('heading', { name: /Patient ID/ }).isVisible());

    const matchingValue = dialog.getByRole('radio', { name: 'Patient ID: Use the matching value', exact: true });
    const allValues = dialog.getByRole('radio', { name: 'Patient ID: Keep all matching values', exact: true });
    await requireUnique(matchingValue, 'Patient ID: Use the matching value');
    await requireUnique(allValues, 'Patient ID: Keep all matching values');
    assert.equal(await matchingValue.isEnabled(), true);
    assert.equal(await allValues.isEnabled(), true);
    const addColumn = dialog.getByRole('button', { name: 'Add 1 column', exact: true });
    await requireUnique(addColumn, 'Add 1 column');
    record(report, 'Both matching-value forms are visible and Add is available',
      await matchingValue.isVisible() && await allValues.isVisible() && await addColumn.isEnabled());
    report.lifecycle.sourceSelection = 'passed';
    report.lifecycle.choiceDialog = 'passed';

    let dialogEvidence;
    let proposalEvidence;
    if (action === 'Inspect Patient field choice') {
      dialogEvidence = await dialog.evaluate(element => ({ text: element.innerText, radios: [...element.querySelectorAll('input[type="radio"]')].map(input => ({
        label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
      })) }));
    } else {
      if (action === 'Inspect selected Patient route') {
        await measuredAction(tracker, 'select matching Patient value form', matchingValue,
          radio => radio.check({ timeout: 5000 }),
          async () => assert.equal(await matchingValue.isChecked(), true));
        dialogEvidence = await dialog.evaluate(element => ({ text: element.innerText, radios: [...element.querySelectorAll('input[type="radio"]')].map(input => ({
          label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
        })) }));
        const traversal = dialog.getByTestId('construction-traversal-path');
        await requireUnique(traversal, 'Patient ID traversal route');
        const routeText = (await traversal.innerText()).trim();
        const routeBranches = await traversal.getByTestId('construction-traversal-branch').count();
        record(report, 'Patient ID route form and visible traversal are selected',
          await matchingValue.isChecked() && routeText.includes('Patient') && routeBranches > 0,
          { dialogEvidence, routeText, routeBranches });
      } else {
        await measuredAction(tracker, 'select Keep all matching values', allValues,
          radio => radio.check({ timeout: 5000 }),
          async () => assert.equal(await allValues.isChecked(), true));
        dialogEvidence = await dialog.evaluate(element => ({ text: element.innerText, radios: [...element.querySelectorAll('input[type="radio"]')].map(input => ({
          label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
        })) }));
        const proposal = page.getByTestId('construction-proposal-panel');
        await measuredAction(tracker, 'submit Patient ID choice', addColumn,
          button => button.click({ timeout: 5000 }),
          () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', undefined, { timeout: 5000 }));
        const apply = page.getByTestId('construction-apply-proposal');
        await requireUnique(apply, 'Apply proposal');
        const ready = await proposal.getAttribute('data-proposal-status') === 'ready';
        const applyEnabled = await apply.isEnabled();
        proposalEvidence = {
          status: await proposal.getAttribute('data-proposal-status'),
          text: (await proposal.innerText()).slice(0, 5000),
          preview: (await page.getByTestId('construction-proposal-preview').innerText()).slice(0, 5000),
        };
        record(report, 'Patient ID proposal is ready and can be applied', ready && applyEnabled,
          { proposalStatus: proposalEvidence.status, applyEnabled, dialogEvidence });
        report.lifecycle.proposal = 'passed';
      }
    }

    (report.builderTimings ??= []).push(...tracker.timings);
    await captureBuilderScreenshot({ page, cda, report, name: 'patient-related-inspection.png' });
    await writeFile(`${evidenceDirectory}/patient-related-inspection.json`, JSON.stringify({ action, dialogEvidence, proposalEvidence, timings: tracker.timings }, null, 2) + '\n', { mode: 0o600 });
    report.evidence ??= [];
    report.evidence.push('patient-related-inspection.json');
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
  return report;
}
