import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { launchBrowser, sanitizeText } from '../lib/playwright-browser.mjs';
import { browserURL } from './common.mjs';
import { makeReportLocation, scenarioFor } from './cli.mjs';
import { createReport, finishReport, recordCheck, writeReport } from './report.mjs';
import { requiredChecksFor } from './registry.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './source-fingerprint.mjs';

const patientSource = target => {
  const contents = readFileSync(join(target.fixtureDir, 'Patient.ndjson'));
  return {
    sha256: createHash('sha256').update(contents).digest('hex'),
    ids: contents.toString('utf8').trim().split('\n').filter(Boolean)
      .map(line => JSON.parse(line).id).sort(),
  };
};

const patientSourceDigest = target => createHash('sha256')
  .update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');

const sourceIdentifierValues = (target, ids) => {
  const wanted = new Set(ids);
  const values = new Map();
  for (const line of readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8').split('\n')) {
    if (!line) continue;
    const patient = JSON.parse(line);
    if (wanted.has(patient.id)) {
      values.set(patient.id, (patient.identifier ?? []).map(identifier => identifier.value ?? null));
    }
  }
  assert.equal(values.size, wanted.size, 'Independent CDA source must contain every expected preview Patient');
  return values;
};

const identifierDisplay = values => {
  const visible = values.filter(value => value !== null).map(String);
  return visible.length ? visible.join('; ') : '—';
};

export const patientWindow = (target, ids) => {
  if (ids.length <= 25) return ids;
  assert.equal(new Set(ids).size, ids.length, 'Patient source IDs must have distinct identities');
  const storageKey = id => createHash('sha256')
    .update(['vertex', target.fixtureProject, target.fixtureGeneration, 'Patient', id, ''].join('\0'))
    .digest('hex');
  return ids.map(id => ({ id, key: storageKey(id) }))
    .sort((left, right) => left.key.localeCompare(right.key))
    .slice(0, 25).map(item => item.id);
};

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

export const assertPreviewPatientWindow = async (page, expectedIds, identifierValues) => {
  const scroll = page.getByTestId('preview-table-scroll');
  const table = scroll.getByRole('table');
  assert.equal(Number(await table.getAttribute('aria-rowcount')), expectedIds.length + 1,
    'Preview row count must equal the bounded independent Patient window');
  const dimensions = await scroll.evaluate(element => ({
    top: element.scrollTop, max: element.scrollHeight - element.clientHeight,
    step: Math.max(1, Math.floor(element.clientHeight * 0.7)),
  }));
  const seen = new Map();
  const inspect = async () => {
    const rows = await table.evaluate(element => [...element.querySelectorAll('[role="row"]')].slice(1).map(row => ({
      ordinal: Number(row.firstElementChild?.textContent?.trim()),
      id: row.querySelector('[role="cell"]')?.textContent?.trim(),
      identifierValue: row.querySelectorAll('[role="cell"]')[1]?.textContent?.trim(),
    })));
    for (const row of rows) {
      assert(Number.isInteger(row.ordinal) && row.ordinal >= 1 && row.ordinal <= expectedIds.length,
        `Preview row has an invalid ordinal: ${JSON.stringify(row)}`);
      assert.equal(row.id, expectedIds[row.ordinal - 1], `Patient identity differs at preview row ${row.ordinal}`);
      assert.equal(row.identifierValue, identifierDisplay(identifierValues.get(row.id)),
        `Identifier ALL value differs from the independent CDA source at preview row ${row.ordinal}`);
      seen.set(row.ordinal, row.id);
    }
  };
  await inspect();
  await scroll.hover();
  let top = dimensions.top;
  while (top < dimensions.max - 1) {
    await page.mouse.wheel(0, Math.min(dimensions.step, dimensions.max - top));
    await page.waitForFunction(previous => {
      const element = document.querySelector('[data-testid="preview-table-scroll"]');
      return element && element.scrollTop > previous + 0.5;
    }, top);
    top = await scroll.evaluate(element => element.scrollTop);
    await inspect();
  }
  assert.equal(seen.size, expectedIds.length, 'Vertical sweep must inspect every Patient in the preview window');
  if (Math.abs(top - dimensions.top) > 1) {
    await page.mouse.wheel(0, dimensions.top - top);
    await page.waitForFunction(original => {
      const element = document.querySelector('[data-testid="preview-table-scroll"]');
      return element && Math.abs(element.scrollTop - original) <= 1;
    }, dimensions.top);
  }
  return [...seen.entries()].sort(([left], [right]) => left - right).map(([, id]) => id);
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

const assertRestoredCdaBuilder = async (page, expected) => {
  const explorerControl = page.getByRole('combobox', { name: 'Explorer' });
  const actual = {
    explorer: await explorerControl.inputValue(),
    title: (await explorerControl.locator('option:checked').textContent())?.trim(),
    patientId: await page.getByRole('button', { name: /^Select Patient ID/ }).count(),
    identifierValue: await page.getByRole('button', { name: /^Select Identifier Value/ }).count(),
  };
  assert.deepEqual(actual, { ...expected, patientId: 1, identifierValue: 1 },
    'Published CDA Builder configuration must survive reload on the same Explorer');
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
  const { ids: allIds, sha256: fixtureSHA256 } = patientSource(target);
  const expectedIds = patientWindow(target, allIds);
  const cdaData = allIds.length > 25;
  const identifierValues = cdaData ? sourceIdentifierValues(target, expectedIds) : null;
  if (cdaData) report.requiredChecks.push('CDA identifier ALL preview matches independent raw values including duplicates');
  const addedField = cdaData
    ? { path: 'identifier[].value', label: 'Identifier Value' }
    : { path: 'gender', label: 'Gender' };
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sha256: fixtureSHA256,
    sourcePatientCount: allIds.length,
    previewLimit: 25,
    previewIds: expectedIds,
  };
  if (cdaData) {
    report.target.fixtureOracle.identifierValues = Object.fromEntries(identifierValues);
  }
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
    await waitVisible(page.getByText('New explorer', { exact: true }));
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
    await page.waitForFunction(rowCount =>
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
    expectedIds.length + 1);
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
    const fieldCheckbox = page.getByRole('checkbox', { name: `Select Patient.${addedField.path}` });
    await act(`select Patient.${addedField.path}`, fieldCheckbox, () => fieldCheckbox.check());
    await act('add selected feature', page.getByRole('button', { name: 'Add 1 selected feature' }),
      () => page.getByRole('button', { name: 'Add 1 selected feature' }).click());
    if (cdaData) {
      const choiceDialog = page.getByRole('dialog', { name: 'Choose how to add these fields' });
      await waitVisible(choiceDialog);
      const allValues = choiceDialog.getByRole('radio', { name: 'Identifier Value: Keep all matching values' });
      await act('keep all Patient identifier values', allValues, () => allValues.check());
      assert(await allValues.isChecked(), 'Identifier Value must use ALL without deduplication');
      const addColumn = choiceDialog.getByRole('button', { name: 'Add 1 column' });
      await act('confirm Identifier Value column', addColumn, () => addColumn.click());
    }
    const apply = page.getByRole('button', { name: 'Apply columns' });
    await waitVisible(apply);
    const renderStarted = Date.now();
    await act(`apply ${addedField.label} column`, apply, () => apply.click());
    const fieldButton = page.getByRole('button', { name: new RegExp(`^Select ${addedField.label}`) });
    await waitVisible(fieldButton);
    await page.waitForFunction(rowCount =>
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
    expectedIds.length + 1);
    const renderMs = Date.now() - renderStarted;
    report.timings[`apply ${addedField.label} column action-to-render`] = renderMs;
    check(report, 'performance', `apply ${addedField.label} column action-to-render within budget`, renderMs <= 5000, { elapsedMs: renderMs, budgetMs: 5000 });
    await act('close operation editor', page.getByRole('button', { name: 'Close operation editor' }),
      () => page.getByRole('button', { name: 'Close operation editor' }).click());

    const visibleIds = allIds.length <= 25
      ? await assertPreviewPatientIds(page, expectedIds)
      : await assertPreviewPatientWindow(page, expectedIds, identifierValues);
    check(report, 'correctness', 'Preview renders both independent fixture Patients', true,
      { expectedIds, visibleIds });
    if (cdaData) {
      const hasDuplicate = [...identifierValues.values()].some(values =>
        values.filter(value => value !== null).length > new Set(values.filter(value => value !== null)).size);
      check(report, 'correctness', 'CDA identifier ALL preview matches independent raw values including duplicates',
        hasDuplicate, { visibleIds, rawIdentifierValues: Object.fromEntries(identifierValues) });
    }

    const publishResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/authoring/v2/publish'));
    const publishStarted = Date.now();
    await act('publish Explorer', page.getByRole('button', { name: 'Publish', exact: true }),
      () => page.getByRole('button', { name: 'Publish', exact: true }).click());
    const published = await publishResponse;
    await page.waitForFunction(() => [...document.querySelectorAll('button')]
      .some(button => button.textContent?.trim() === 'Publish' && button.disabled));
    const publishMs = Date.now() - publishStarted;
    report.timings['publish Explorer action-to-render'] = publishMs;
    const publishWithinBudget = publishMs <= 5000;
    recordCheck(report, 'performance', 'publish Explorer action-to-render within budget', publishWithinBudget,
      { elapsedMs: publishMs, budgetMs: 5000 });
    if (!publishWithinBudget) {
      const error = new Error(`Publish rendered after ${publishMs} ms; budget is 5000 ms`);
      report.errors.push({ kind: 'performance', message: error.message });
      report.failureAction = {
        name: 'publish Explorer', locator: page.getByRole('button', { name: 'Publish', exact: true }).toString(),
        elapsedMs: publishMs,
      };
      report.failureTrace = await browser.captureFailure(error, { action: {
        label: 'publish Explorer',
        locator: report.failureAction.locator,
        targetLocator: page.getByRole('button', { name: 'Publish', exact: true }),
      }, elapsedMs: publishMs, explorer });
    }
    check(report, 'correctness', 'publish endpoint returned success', published.ok(), {
      path: new URL(published.url()).pathname, status: published.status(),
    });

    activeAction = { name: 'reload Builder', locator: 'page' };
    activeLocator = undefined;
    activeStartedAt = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded' });
    await waitVisible(page.getByRole('button', { name: /^Select Patient ID/ }));
    await waitVisible(page.getByRole('button', { name: new RegExp(`^Select ${addedField.label}`) }));
    const restored = cdaData
      ? await assertRestoredCdaBuilder(page, { explorer, title })
      : await assertRestoredBuilder(page, { explorer, title });
    check(report, 'persistence', 'published Builder table and configured fields survive reload', true,
      { expected: { explorer, title }, restored });
    const incidentalAssetErrors = browser.diagnostics.console.filter(item =>
      /Failed to load resource:.*404/.test(item.text)
      && browser.diagnostics.httpFailures.length === 0
      && item.location && new URL(item.location).pathname === '/favicon.ico');
    report.assetFailures = [...browser.diagnostics.assetFailures, ...incidentalAssetErrors];
    assert.deepEqual(browser.diagnostics.console.filter(item => !incidentalAssetErrors.includes(item)), []);
    assert.deepEqual(browser.diagnostics.pageErrors, []);
    const expectedAbortPaths = [
      '/frame-source-options',
      '/semantic-inventory',
      `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(target.bootstrapExplorerId)}/authoring/v2/construction-capabilities`,
    ];
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
      report.failureAction ??= { ...activeAction, state, elapsedMs: Date.now() - activeStartedAt };
      report.failureTrace ??= await browser.captureFailure(error, { action: {
        label: activeAction.name,
        locator: activeAction.locator,
        targetLocator: activeLocator,
      }, elapsedMs: Date.now() - activeStartedAt });
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
    const fixtureSHA256After = patientSourceDigest(target);
    recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during browser run',
      fixtureSHA256After === fixtureSHA256,
      { before: fixtureSHA256, after: fixtureSHA256After });
    finishReport(report);
    writeReport(location.reportPath, report);
    console.log(`UI_VERIFY scenario=builder-authoring case=authoring status=${report.status} report=${location.reportPath}`);
  }
  return report;
};
