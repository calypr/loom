import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

const caseActions = new Set(['Inspect Patient field choice', 'Inspect selected Patient route', 'Inspect Patient proposal']);

export async function runPatientRelatedInspection({ action, explorerId, env = process.env } = {}) {
  assert(caseActions.has(action), `Unsupported Patient related inspection action: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const evidenceDirectory = resolve(target.artifacts, `playwright-patient-related-${action.toLowerCase().replaceAll(' ', '-')}-${new Date().toISOString().replaceAll(':', '-')}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-patient-related-inspection',
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
    path: 'Builder > Add columns > Fields and related data > Patient > Patient.id > choose a displayed value form',
    expectedVisibleResult: action === 'Inspect Patient field choice'
      ? 'Patient.id opens a field-choice dialog with the visible matching-value forms and an actionable Add column control.'
      : action === 'Inspect selected Patient route'
        ? 'The chosen Patient.id form displays the route used to relate each Builder row to Patient records.'
        : 'Choosing Keep all matching values produces a ready proposal with a visible Apply control.',
    independentOracle: 'This is a control and route inspection only. It asserts accessible labels and visible state; it does not claim computed CDA row values.',
    lifecycle: { sourceSelection: 'untested', choiceDialog: 'untested', proposal: 'not applicable', apply: 'not applicable', reload: 'not applicable' },
    evidenceDirectory,
    assertions: [],
    actions: [],
    timings: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  let failure;
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: builderURL.toString() };
    const navigationStart = Date.now();
    tracker.actionStartedAt = navigationStart;
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    const selectedExplorer = await explorer.inputValue();
    record(report, 'Builder is scoped to the requested Explorer', selectedExplorer === explorerId,
      { expectedExplorer: explorerId, selectedExplorer });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStart, status: 'passed' });

    const addColumns = page.locator('button[aria-label^="Add columns:"]');
    await requireUnique(addColumns, 'Add columns');
    activeAction = { label: 'open Add columns', locator: addColumns.toString(), targetLocator: addColumns };
    const sourcePanel = page.getByTestId('construction-add-columns-source');
    await measuredAction(tracker, 'open Add columns', addColumns,
      button => button.click({ timeout: 5000 }),
      () => sourcePanel.waitFor({ state: 'visible', timeout: 5000 }));
    record(report, 'Add columns opens the source chooser', await sourcePanel.isVisible());

    const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await requireUnique(fieldsTab, 'Fields and related data tab');
    if (await fieldsTab.getAttribute('aria-pressed') === 'false') {
      activeAction = { label: 'open Fields and related data', locator: fieldsTab.toString(), targetLocator: fieldsTab };
      await measuredAction(tracker, 'open Fields and related data', fieldsTab,
        button => button.click({ timeout: 5000 }),
        async () => {
          await page.getByRole('checkbox', { name: 'Select Patient.id', exact: true })
            .waitFor({ state: 'visible', timeout: 5000 });
        });
    }

    const patientSource = sourcePanel.getByRole('button', { name: /^Patient,/ });
    await requireUnique(patientSource, 'Patient related source');
    activeAction = { label: 'select Patient related source', locator: patientSource.toString(), targetLocator: patientSource };
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
    activeAction = { label: 'select Patient.id', locator: patientID.toString(), targetLocator: patientID };
    await measuredAction(tracker, 'select Patient.id', patientID,
      checkbox => checkbox.check({ timeout: 5000 }),
      async () => assert.equal(await addField.isEnabled(), true));
    record(report, 'Patient.id is selected as the only catalog feature',
      await patientID.isChecked() && await addField.isEnabled());

    activeAction = { label: 'open Patient.id choice dialog', locator: addField.toString(), targetLocator: addField };
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
        activeAction = { label: 'select matching Patient value form', locator: matchingValue.toString(), targetLocator: matchingValue };
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
        activeAction = { label: 'select Keep all matching values', locator: allValues.toString(), targetLocator: allValues };
        await measuredAction(tracker, 'select Keep all matching values', allValues,
          radio => radio.check({ timeout: 5000 }),
          async () => assert.equal(await allValues.isChecked(), true));
        dialogEvidence = await dialog.evaluate(element => ({ text: element.innerText, radios: [...element.querySelectorAll('input[type="radio"]')].map(input => ({
          label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled,
        })) }));
        activeAction = { label: 'submit Patient ID choice', locator: addColumn.toString(), targetLocator: addColumn };
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

    report.timings = tracker.timings;
    await page.screenshot({ path: `${evidenceDirectory}/patient-related-inspection.png`, fullPage: true });
    await writeFile(`${evidenceDirectory}/patient-related-inspection.json`, JSON.stringify({ action, dialogEvidence, proposalEvidence, timings: tracker.timings }, null, 2) + '\n', { mode: 0o600 });
    report.evidence = ['patient-related-inspection.png', 'patient-related-inspection.json'];
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
  } catch (error) {
    failure = error;
    report.failure = { action: activeAction.label, locator: activeAction.locator, elapsedMs: tracker.actionStartedAt ? Date.now() - tracker.actionStartedAt : undefined, message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, {
        action: { ...activeAction, startedAt: tracker.activeAction?.startedAt },
        elapsedMs: report.failure.elapsedMs,
        target: report.target,
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
    report.status = failure || report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'partial';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
