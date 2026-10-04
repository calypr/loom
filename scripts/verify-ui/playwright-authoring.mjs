import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { launchBrowser, sanitizeText } from '../lib/playwright-browser.mjs';
import { browserURL } from './common.mjs';
import { makeReportLocation, scenarioFor } from './cli.mjs';
import { createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { requiredChecksFor } from './registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';

const patientIds = target => readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8')
  .trim().split('\n').filter(Boolean).map(line => JSON.parse(line).id).sort();

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

const waitVisible = (locator, timeout = 30000) => locator.waitFor({ state: 'visible', timeout });

export const assertPreviewPatientIds = async (page, expectedIds) => {
  const rows = await page.getByTestId('preview-table-scroll').getByRole('row').all();
  const visibleIds = (await Promise.all(rows.slice(1).map(async row =>
    (await row.getByRole('cell').first().innerText()).trim()))).sort();
  assert.deepEqual(visibleIds, expectedIds, 'Preview must contain the exact independent fixture Patient IDs');
  return visibleIds;
};

export const assertRestoredBuilder = async (page, expected) => {
  const explorerControl = page.getByRole('combobox', { name: 'Explorer' });
  const actual = {
    explorer: await explorerControl.inputValue(),
    title: (await explorerControl.locator('option:checked').textContent())?.trim(),
    patientId: await page.getByRole('button', { name: /^Select Patient ID/ }).count(),
    gender: await page.getByRole('button', { name: /^Select Gender/ }).count(),
  };
  assert.deepEqual(actual, { ...expected, patientId: 1, gender: 1 },
    'Published Builder configuration must survive reload on the same Explorer');
  return actual;
};

export const runPlaywrightAuthoring = async context => {
  const location = makeReportLocation(context, 'builder-authoring', 'authoring');
  const registration = scenarioFor('builder-authoring');
  const target = context.target;
  const report = createReport({
    scenario: 'builder-authoring',
    caseName: 'authoring',
    target: {
      kind: target.kind ?? 'owned-dev-fixture',
      uiUrl: new URL(target.uiUrl).origin,
      sourceRoot: target.sourceRoot,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
    },
    evidenceDirectory: location.evidenceDirectory,
    requiredChecks: requiredChecksFor(registration, 'authoring', context.custom),
  });
  report.registryCoverage = registration.coverage;
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  report.target.sourceFingerprint = sourceAtStart.fingerprint;
  report.sourceFingerprintManifest = { before: sourceAtStart.manifest };
  const expectedIds = patientIds(target);
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    ids: expectedIds,
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
    const act = async (name, locator, action) => {
      activeAction = { name, locator: locator.toString() };
      activeLocator = locator;
      const started = Date.now();
      activeStartedAt = started;
      await action();
      report.actions.push({ name, status: 'passed', elapsedMs: Date.now() - started });
    };
    const title = `Verify ${context.runID.slice(-10)} authoring`;
    activeAction = { name: 'open Builder', locator: 'Builder URL' };
    activeLocator = undefined;
    activeStartedAt = Date.now();
    await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
    await page.waitForFunction(() => document.body.innerText.includes('Dataset graph')
      || document.body.innerText.includes('Build your first table'));
    await act('open Explorer creation', page.getByText('New explorer', { exact: true }),
      () => page.getByText('New explorer', { exact: true }).click());
    await act('name Explorer', page.locator('#new-explorer-name'),
      () => page.locator('#new-explorer-name').fill(title));
    await act('create blank Explorer', page.getByRole('button', { name: 'Create blank' }),
      () => page.getByRole('button', { name: 'Create blank' }).click());
    await page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, title);
    await waitVisible(page.getByText('Build your first table', { exact: true }));
    const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
    assert(explorer && explorer !== target.bootstrapExplorerId, 'Explorer creation must select a fresh Explorer');
    report.target.explorer = explorer;
    await act('name Patient table', page.locator('#first-table-name'),
      () => page.locator('#first-table-name').fill('Patients'));
    const rootStarted = Date.now();
    await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
      () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
    await waitVisible(page.getByTestId('construction-workspace'));
    await waitVisible(page.getByTestId('preview-table-scroll').getByRole('table'));
    await waitVisible(page.getByText(expectedIds[0], { exact: true }));
    const rootRenderMs = Date.now() - rootStarted;
    report.timings['choose Patient rows action-to-render'] = rootRenderMs;
    check(report, 'performance', 'choose Patient rows action-to-render within budget', rootRenderMs <= 5000,
      { elapsedMs: rootRenderMs, budgetMs: 5000 });
    await act('open Add columns', page.getByRole('button', { name: /Add columns:/ }),
      () => page.getByRole('button', { name: /Add columns:/ }).click());
    await waitVisible(page.locator('[aria-label="Add columns editor"]'));
    await act('choose Fields and related data', page.getByRole('button', { name: 'Fields and related data' }),
      () => page.getByRole('button', { name: 'Fields and related data' }).click());
    await act('open raw FHIR fields', page.getByText('Raw FHIR fields (advanced)', { exact: true }),
      () => page.getByText('Raw FHIR fields (advanced)', { exact: true }).click());
    await act('select Patient.gender', page.getByRole('checkbox', { name: 'Select Patient.gender' }),
      () => page.getByRole('checkbox', { name: 'Select Patient.gender' }).check());
    await act('add selected feature', page.getByRole('button', { name: 'Add 1 selected feature' }),
      () => page.getByRole('button', { name: 'Add 1 selected feature' }).click());
    const apply = page.getByRole('button', { name: 'Apply columns' });
    await waitVisible(apply);
    const renderStarted = Date.now();
    await act('apply Gender column', apply, () => apply.click());
    await waitVisible(page.getByRole('button', { name: /^Select Gender/ }));
    await waitVisible(page.getByTestId('preview-table-scroll').getByText(expectedIds[0], { exact: true }));
    const renderMs = Date.now() - renderStarted;
    report.timings['apply Gender column action-to-render'] = renderMs;
    check(report, 'performance', 'apply Gender column action-to-render within budget', renderMs <= 5000, { elapsedMs: renderMs, budgetMs: 5000 });
    await act('close operation editor', page.getByRole('button', { name: 'Close operation editor' }),
      () => page.getByRole('button', { name: 'Close operation editor' }).click());

    const visibleIds = await assertPreviewPatientIds(page, expectedIds);
    check(report, 'correctness', 'Preview renders both independent fixture Patients', true,
      { expectedIds, visibleIds });

    const publishResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/authoring/v2/publish'));
    const publishStarted = Date.now();
    await act('publish Explorer', page.getByRole('button', { name: 'Publish', exact: true }),
      () => page.getByRole('button', { name: 'Publish', exact: true }).click());
    const published = await publishResponse;
    await page.waitForFunction(() => [...document.querySelectorAll('button')]
      .some(button => button.textContent?.trim() === 'Publish' && button.disabled));
    const publishMs = Date.now() - publishStarted;
    report.timings['publish Explorer action-to-render'] = publishMs;
    check(report, 'performance', 'publish Explorer action-to-render within budget', publishMs <= 5000,
      { elapsedMs: publishMs, budgetMs: 5000 });
    check(report, 'correctness', 'publish endpoint returned success', published.ok(), {
      path: new URL(published.url()).pathname, status: published.status(),
    });

    activeAction = { name: 'reload Builder', locator: 'page' };
    activeLocator = undefined;
    activeStartedAt = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded' });
    await waitVisible(page.getByRole('button', { name: /^Select Patient ID/ }));
    await waitVisible(page.getByRole('button', { name: /^Select Gender/ }));
    const restored = await assertRestoredBuilder(page, { explorer, title });
    check(report, 'persistence', 'published Builder table and configured fields survive reload', true,
      { expected: { explorer, title }, restored });
    const incidentalAssetErrors = browser.diagnostics.console.filter(item =>
      /Failed to load resource:.*404/.test(item.text)
      && browser.diagnostics.httpFailures.length === 0
      && item.location && new URL(item.location).pathname === '/favicon.ico');
    report.assetFailures = [...browser.diagnostics.assetFailures, ...incidentalAssetErrors];
    assert.deepEqual(browser.diagnostics.console.filter(item => !incidentalAssetErrors.includes(item)), []);
    assert.deepEqual(browser.diagnostics.pageErrors, []);
    const expectedAbortPaths = ['/frame-source-options', '/semantic-inventory'];
    const cancelledReads = browser.diagnostics.networkFailures.filter(item =>
      item.failure === 'net::ERR_ABORTED'
      && item.method === 'POST'
      && expectedAbortPaths.some(path => new URL(item.url).pathname.endsWith(path)));
    report.cancelledOwnedReads = cancelledReads;
    assert.deepEqual(browser.diagnostics.networkFailures.filter(item => !cancelledReads.includes(item)), []);
    assert.deepEqual(browser.diagnostics.httpFailures, []);
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
      await browser.close().catch(error => {
        recordCheck(report, 'correctness', 'browser closed cleanly', false,
          { message: sanitizeText(error.message ?? error) });
      });
    }
    if (initialBuild) {
      try { check(report, 'correctness', 'API build identity stayed unchanged during browser run', buildIdentity(target) === initialBuild); }
      catch (error) {
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
    finishReport(report);
    writeReport(location.reportPath, report);
    console.log(`UI_VERIFY scenario=builder-authoring case=authoring status=${report.status} report=${location.reportPath}`);
  }
  return report;
};
