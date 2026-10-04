import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { launchBrowser, sanitizeText } from '../lib/playwright-browser.mjs';
import { browserURL } from './common.mjs';
import { makeReportLocation, scenarioFor } from './cli.mjs';
import { createReport, finishReport, recordCheck, recordUntested, writeReport } from './report.mjs';
import { requiredChecksFor } from './registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';
import { patientWindow } from './playwright-authoring.mjs';

const buildIdentity = target => {
  const stdout = execFileSync('docker', [
    'exec', `${target.composeProject}-loom-api-1`,
    '/workspace/loom-dev-build-stamp.sh', '--check',
  ], { encoding: 'utf8', timeout: 10000 }).trim();
  assert.match(stdout, /^[a-f0-9]{64}\s+[a-f0-9]{64}\s+[a-f0-9]{64}$/i);
  return stdout.split(/\s+/).join(':').toLowerCase();
};

const check = (report, dimension, name, passed, evidence = {}) => {
  recordCheck(report, dimension, name, passed, evidence);
  assert(passed, name);
};

const unique = async locator => {
  const count = await locator.count();
  assert.equal(count, 1, `Expected one target for ${locator.toString()}, found ${count}`);
  return locator;
};

export const runPlaywrightSuggestions = async context => {
  const location = makeReportLocation(context, 'builder-authoring', 'suggestions');
  const registration = scenarioFor('builder-authoring');
  const target = context.target;
  const report = createReport({
    scenario: 'builder-authoring',
    caseName: 'suggestions',
    target: {
      kind: target.kind ?? 'owned-dev-fixture',
      uiUrl: new URL(target.uiUrl).origin,
      sourceRoot: target.sourceRoot,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
    },
    evidenceDirectory: location.evidenceDirectory,
    requiredChecks: requiredChecksFor(registration, 'suggestions', context.custom),
  });
  report.registryCoverage = registration.coverage;
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  report.target.sourceFingerprint = sourceAtStart.fingerprint;
  report.sourceFingerprintManifest = { before: sourceAtStart.manifest };
  const sourceContents = readFileSync(join(target.fixtureDir, 'Patient.ndjson'));
  const sourceSHA256 = createHash('sha256').update(sourceContents).digest('hex');
  const sourceIds = sourceContents.toString('utf8').trim().split('\n').filter(Boolean)
    .map(line => JSON.parse(line).id).sort();
  const expectedIds = patientWindow(target, sourceIds);
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourcePatientCount: sourceIds.length,
    sha256: sourceSHA256,
    previewIds: expectedIds,
  };
  let browser;
  let activeAction = { name: 'launch browser', locator: null };
  let activeLocator;
  let activeStartedAt = Date.now();
  let initialBuild;
  try {
    initialBuild = buildIdentity(target);
    report.target.apiBuildIdentity = initialBuild;
    browser = await launchBrowser({ evidence: location.evidenceDirectory,
      appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const page = browser.page;
    const act = async (name, locator, action, { editable = false } = {}) => {
      await unique(locator);
      activeAction = { name, locator: locator.toString() };
      activeLocator = locator;
      activeStartedAt = Date.now();
      await locator.waitFor({ state: 'visible' });
      assert(await locator.isEnabled(), `${name}: target is disabled`);
      if (editable) assert(await locator.isEditable(), `${name}: target is not editable`);
      await action();
      report.actions.push({ name, status: 'passed', elapsedMs: Date.now() - activeStartedAt });
    };

    const title = `Verify ${context.runID.slice(-10)} suggestions`;
    activeAction = { name: 'open Builder', locator: 'Builder URL' };
    activeStartedAt = Date.now();
    await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
    await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible' });
    await act('open Explorer creation', page.getByText('New explorer', { exact: true }),
      () => page.getByText('New explorer', { exact: true }).click());
    const nameInput = page.locator('#new-explorer-name');
    await act('name Explorer', nameInput, () => nameInput.fill(title), { editable: true });
    const createBlank = page.getByRole('button', { name: 'Create blank' });
    await act('create blank Explorer', createBlank, () => createBlank.click());
    await page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, title);
    const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
    check(report, 'correctness', 'created a fresh Explorer distinct from the bootstrap',
      Boolean(explorer && explorer !== target.bootstrapExplorerId), { title });
    report.target.explorer = explorer;

    const tableName = page.locator('#first-table-name');
    await act('name Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
      () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
    await page.waitForFunction(rowCount =>
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
    expectedIds.length + 1);
    await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
    await page.waitForFunction(() => {
      const button = document.querySelector('[data-testid="construction-action-add-columns"]');
      return button && !button.disabled;
    });
    const addColumns = page.getByRole('button', { name: /Add columns:/ });
    await act('open Add columns', addColumns, () => addColumns.click());
    await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
    const fieldsRelated = page.getByRole('button', { name: 'Fields and related data' });
    await act('choose Fields and related data', fieldsRelated, () => fieldsRelated.click());
    const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
    await act('open Raw FHIR fields', rawFields, () => rawFields.click());
    const rawFieldSection = page.getByTestId('feature-catalog-raw-fields');
    const catalogChoices = rawFieldSection.locator('input[aria-label^="Select Patient."]');
    await catalogChoices.first().waitFor({ state: 'visible' });
    const candidateCount = await catalogChoices.count();
    report.target.candidateEvidence = {
      source: 'Builder rendered catalog-backed Patient candidate controls',
      count: candidateCount,
      labels: await catalogChoices.evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')).sort()),
    };
    const idCandidate = rawFieldSection.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    await unique(idCandidate);
    await idCandidate.waitFor({ state: 'visible' });
    const idCandidateState = {
      visible: await idCandidate.isVisible(),
      enabled: await idCandidate.isEnabled(),
      checked: await idCandidate.isChecked(),
    };
    check(report, 'correctness', 'catalog-backed Patient candidates are rendered', Number(candidateCount) > 0,
      { candidateCount, labels: report.target.candidateEvidence.labels });
    check(report, 'usability', 'Patient ID candidate control is visible and actionable',
      idCandidateState.visible && idCandidateState.enabled && !idCandidateState.checked,
      idCandidateState);
    activeAction = { name: 'select Patient ID candidate', locator: idCandidate.toString() };
    activeLocator = idCandidate;
    activeStartedAt = Date.now();
    await idCandidate.check();
    report.actions.push({ name: activeAction.name, status: 'passed', elapsedMs: Date.now() - activeStartedAt });
    recordUntested(report, 'usability', 'lazy suggestion request failure and recovery',
      'The isolated development catalog already includes Patient candidates, so ensureSuggestions returns without issuing a suggestions request.');
  } catch (error) {
    report.errors.push({ kind: 'scenario-error', message: sanitizeText(error.message ?? error) });
    recordCheck(report, 'correctness', 'scenario completed without an uncaught error', false,
      { message: sanitizeText(error.message ?? error) });
    if (browser) {
      const state = activeLocator ? await Promise.all([
        activeLocator.count(), activeLocator.isVisible(), activeLocator.isEnabled(), activeLocator.isEditable(),
      ]).then(([count, visible, enabled, editable]) => ({ count, visible, enabled, editable }), () => null) : null;
      report.failureAction = { ...activeAction, state, elapsedMs: Date.now() - activeStartedAt };
      report.failureTrace = await browser.captureFailure(error, { action: {
        label: activeAction.name,
        locator: activeAction.locator,
        targetLocator: activeLocator,
      }, elapsedMs: report.failureAction.elapsedMs });
    }
  } finally {
    if (browser) {
      report.browserDiagnostics = browser.diagnostics;
      const incidentalAssetErrors = browser.diagnostics.console.filter(item =>
        /Failed to load resource:.*404/.test(item.text)
        && browser.diagnostics.httpFailures.length === 0
        && item.location && new URL(item.location).pathname === '/favicon.ico');
      const expectedAbortPaths = [
        '/frame-source-options',
        '/semantic-inventory',
        `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(target.bootstrapExplorerId)}/authoring/v2/construction-capabilities`,
      ];
      const cancelledReads = browser.diagnostics.networkFailures.filter(item =>
        item.failure === 'net::ERR_ABORTED'
        && item.method === 'POST'
        && expectedAbortPaths.some(path => new URL(item.url).pathname.endsWith(path)));
      report.assetFailures = [...browser.diagnostics.assetFailures, ...incidentalAssetErrors];
      report.cancelledOwnedReads = cancelledReads;
      const unexpectedDiagnostics = {
        console: browser.diagnostics.console.filter(item => !incidentalAssetErrors.includes(item)),
        pageErrors: browser.diagnostics.pageErrors,
        networkFailures: browser.diagnostics.networkFailures.filter(item => !cancelledReads.includes(item)),
        httpFailures: browser.diagnostics.httpFailures,
      };
      recordCheck(report, 'correctness', 'no unexpected network, API, or browser errors',
        Object.values(unexpectedDiagnostics).every(items => items.length === 0), unexpectedDiagnostics);
      await browser.close().catch(error => {
        recordCheck(report, 'correctness', 'browser closed cleanly', false,
          { message: sanitizeText(error.message ?? error) });
      });
    }
    if (initialBuild) {
      try {
        check(report, 'correctness', 'API build identity stayed unchanged during browser run',
          buildIdentity(target) === initialBuild);
      } catch (error) {
        report.errors.push({ kind: 'build-identity-error', message: sanitizeText(error.message ?? error) });
        recordCheck(report, 'correctness', 'API build identity stayed unchanged during browser run', false,
          { message: sanitizeText(error.message ?? error) });
      }
    }
    const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
    const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
    recordCheck(report, 'correctness', 'watched source stayed unchanged during browser run',
      sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256,
      { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths });
    report.sourceFingerprintManifest.changedPaths = changedPaths;
    const sourceSHA256After = createHash('sha256')
      .update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
    recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during browser run',
      sourceSHA256After === sourceSHA256,
      { before: sourceSHA256, after: sourceSHA256After });
    finishReport(report);
    writeReport(location.reportPath, report);
    console.log(`UI_VERIFY scenario=builder-authoring case=suggestions status=${report.status} report=${location.reportPath}`);
  }
  return report;
};
