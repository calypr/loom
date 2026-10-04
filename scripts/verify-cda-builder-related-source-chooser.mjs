import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { createDevSession, assertOwnedDevSession } from './loom-dev.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

export const requiredEnvironment = [
  'LOOM_CDA_SOURCE_ROOT', 'LOOM_CDA_DATASET_DIR', 'LOOM_CDA_COMPOSE_PROJECT',
  'LOOM_CDA_API_PORT', 'LOOM_CDA_UI_PORT', 'LOOM_CDA_API_ORIGIN',
  'LOOM_CDA_UI_ORIGIN', 'LOOM_CDA_API_CONTAINER',
  'LOOM_CDA_PROJECT', 'LOOM_CDA_GENERATION',
];

export const targetFromEnvironment = async (env = process.env) => {
  const missing = requiredEnvironment.filter(name => !String(env[name] ?? '').trim());
  assert.equal(missing.length, 0, `Set an explicit isolated CDA target: ${missing.join(', ')}`);
  const sourceRoot = realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT));
  const apiContainer = env.LOOM_CDA_API_CONTAINER.trim();
  const expectedContainer = `${env.LOOM_CDA_COMPOSE_PROJECT}-loom-api-1`;
  assert.equal(apiContainer, expectedContainer,
    `LOOM_CDA_API_CONTAINER must name this Compose project's API container (${expectedContainer})`);

  const target = createDevSession({
    LOOM_DEV_SOURCE_ROOT: sourceRoot,
    LOOM_DEV_FIXTURE_DIR: realpathSync(resolve(env.LOOM_CDA_DATASET_DIR)),
    LOOM_DEV_COMPOSE_PROJECT: env.LOOM_CDA_COMPOSE_PROJECT,
    LOOM_DEV_API_PORT: env.LOOM_CDA_API_PORT,
    LOOM_DEV_UI_PORT: env.LOOM_CDA_UI_PORT,
    LOOM_DEV_API_URL: env.LOOM_CDA_API_ORIGIN,
    LOOM_DEV_UI_URL: env.LOOM_CDA_UI_ORIGIN,
    LOOM_DEV_PROJECT: env.LOOM_CDA_PROJECT,
    LOOM_DEV_GENERATION: env.LOOM_CDA_GENERATION,
    LOOM_DEV_ARTIFACTS: resolve(sourceRoot, '.artifacts/cda-builder'),
  }, sourceRoot);
  assert.equal(target.sourceRoot, sourceRoot, 'The validated Compose source must be this isolated checkout');
  await assertOwnedDevSession(target);
  const labels = execFileSync('docker', ['inspect', '--format', '{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}', apiContainer], { encoding: 'utf8', timeout: 10000 }).trim();
  assert.equal(labels, `${target.composeProject}|loom-api`, 'Named API container does not belong to the validated isolated stack');
  return target;
};

export const apiBuildIdentity = target => {
  const output = execFileSync('docker', [
    'exec', `${target.composeProject}-loom-api-1`, '/workspace/loom-dev-build-stamp.sh', '--check',
  ], { encoding: 'utf8', timeout: 10000 }).trim();
  assert.match(output, /^[a-f0-9]{64}\s+[a-f0-9]{64}\s+[a-f0-9]{64}$/i,
    'API did not expose a complete source/build identity');
  return output.split(/\s+/).join(':').toLowerCase();
};

export const record = (report, name, passed, evidence = {}) => {
  report.assertions.push({ name, status: passed ? 'passed' : 'failed', evidence });
  assert(passed, name);
};

export const measuredAction = async (tracker, name, locator, action, rendered, { editable = false } = {}) => {
  const started = Date.now();
  tracker.actionStartedAt = started;
  const actionMs = await performAction(tracker, name, locator, action, { timeout: 5000, editable });
  await rendered();
  const elapsedMs = Date.now() - started;
  tracker.timings.push({ name, actionMs, elapsedMs, status: 'passed' });
  assert(elapsedMs <= 5000, `${name} took ${elapsedMs} ms to render; maximum is 5000 ms`);
  return elapsedMs;
};

export async function runRelatedSourceChooser({ explorerId, env = process.env } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const evidenceDirectory = resolve(target.artifacts, `playwright-related-source-chooser-${new Date().toISOString().replaceAll(':', '-')}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const uiOrigin = target.uiUrl;
  const apiOrigin = target.apiUrl;
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-related-source-chooser',
    status: 'running',
    target: {
      sourceRoot: target.sourceRoot,
      sourceFingerprint: sourceAtStart.fingerprint,
      apiBuildIdentity: buildAtStart,
      composeProject: target.composeProject,
      apiContainer: env.LOOM_CDA_API_CONTAINER,
      uiOrigin,
      apiOrigin,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
      explorerId,
    },
    path: 'Builder > Add columns > Fields and related data > search Observation > select Observation source',
    expectedVisibleResult: 'ALL is initially selected; ROOT is unselected; exactly the eight expected related resource types are listed; filtering to Obser shows only Observation; choosing it clears search and selects its source.',
    independentOracle: 'The expected related-resource category set is asserted independently of the rendered chooser. This inspection case has no preview or computed-row assertion.',
    lifecycle: { chooser: 'untested', preview: 'not applicable', apply: 'not applicable', reload: 'not applicable', edit: 'not applicable', removal: 'not applicable' },
    evidenceDirectory,
    assertions: [],
    actions: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  let failure;
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [uiOrigin, apiOrigin], noAuth: true });
    const { page, diagnostics } = browser;
    const projectURL = new URL(uiOrigin);
    projectURL.searchParams.set('project', target.fixtureProject);
    projectURL.searchParams.set('explorer', explorerId);
    projectURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: projectURL.toString() };
    const navigationStarted = Date.now();
    tracker.actionStartedAt = navigationStarted;
    await page.goto(projectURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStarted, status: 'passed' });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to the requested Explorer', await explorer.inputValue() === explorerId,
      { expectedExplorer: explorerId, actualExplorer: await explorer.inputValue() });

    const addColumns = page.locator('button[aria-label^="Add columns:"]');
    await requireUnique(addColumns, 'Add columns');
    activeAction = { label: 'open Add columns', locator: addColumns.toString(), targetLocator: addColumns };
    const panel = page.getByTestId('construction-add-columns-source');
    await measuredAction(tracker, 'open Add columns', addColumns,
      target => target.click({ timeout: 5000 }),
      () => panel.waitFor({ state: 'visible', timeout: 5000 }));
    record(report, 'Add columns opens the visible source chooser', await panel.isVisible());

    const sourceOptions = panel.getByTestId('construction-add-columns-source-option');
    const before = await sourceOptions.evaluateAll(buttons => buttons.filter(button => {
      const rect = button.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0;
    }).map(button => ({
      label: button.getAttribute('aria-label'),
      kind: button.getAttribute('data-source-kind'),
      selected: button.getAttribute('aria-pressed') === 'true',
      disabled: button.disabled || button.getAttribute('aria-disabled') === 'true',
    })));
    record(report, 'ALL is selected and ROOT is unselected',
      before.some(option => option.kind === 'ALL' && option.selected && !option.disabled)
      && before.some(option => option.kind === 'ROOT' && !option.selected && !option.disabled), { before });
    const expectedKinds = ['BodyStructure', 'Condition', 'Medication', 'MedicationAdministration', 'Observation', 'Patient', 'ResearchStudy', 'ResearchSubject'];
    const relatedKinds = before.filter(option => option.kind === 'RELATED').map(option => option.label?.split(',')[0]).sort();
    record(report, 'Related source inventory matches independent expected categories',
      JSON.stringify(relatedKinds) === JSON.stringify(expectedKinds), { expectedKinds, relatedKinds });

    const search = panel.getByRole('searchbox', { name: 'Search related resources', exact: true });
    await requireUnique(search, 'Search related resources');
    activeAction = { label: 'filter related sources', locator: search.toString(), targetLocator: search };
    await measuredAction(tracker, 'filter related sources', search,
      target => target.fill('Obser', { timeout: 5000 }),
      () => page.waitForFunction(() => {
        const visible = [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"][data-source-kind="RELATED"]')]
          .filter(button => { const rect = button.getBoundingClientRect(); return rect.width > 0 && rect.height > 0; });
        return visible.length === 1 && visible[0].getAttribute('aria-label')?.startsWith('Observation,');
      }, undefined, { timeout: 5000 }), { editable: true });
    const filtered = await sourceOptions.evaluateAll(buttons => buttons.filter(button => {
      const rect = button.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0;
    }).map(button => button.getAttribute('aria-label')));
    record(report, 'Obser filtering shows only Observation',
      filtered.length === 1 && filtered[0]?.startsWith('Observation,'), { filtered });

    const observation = panel.getByRole('button', { name: /^Observation,/ });
    await requireUnique(observation, 'Observation related source');
    activeAction = { label: 'select Observation source', locator: observation.toString(), targetLocator: observation };
    await measuredAction(tracker, 'select Observation source', observation,
      target => target.click({ timeout: 5000 }),
      async () => {
        await page.waitForFunction(() => {
          const button = [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')]
            .find(candidate => candidate.getAttribute('aria-label')?.startsWith('Observation,'));
          const searchInput = document.querySelector('input[aria-label="Search related resources"]');
          return button?.getAttribute('aria-pressed') === 'true' && searchInput?.value === '';
        }, undefined, { timeout: 5000 });
      });
    const selectedLabel = await panel.locator('[data-testid="construction-add-columns-source-option"][aria-pressed="true"]')
      .evaluateAll(buttons => buttons.map(button => button.getAttribute('aria-label')));
    const searchAfter = await search.inputValue();
    record(report, 'Selecting Observation clears search and selects its source',
      searchAfter === '' && selectedLabel.some(label => label?.startsWith('Observation,')), { searchAfter, selectedLabel });
    report.lifecycle.chooser = 'passed';
    report.timings = tracker.timings;
    await page.screenshot({ path: `${evidenceDirectory}/related-source-chooser.png`, fullPage: true });
    report.evidence = ['related-source-chooser.png'];

    const expectedBrowserErrors = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', expectedBrowserErrors, diagnostics);
  } catch (error) {
    failure = error;
    report.failure = { action: activeAction.label, locator: activeAction.locator, message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, {
        action: { ...activeAction, startedAt: tracker.activeAction?.startedAt },
        elapsedMs: tracker.actionStartedAt ? Date.now() - tracker.actionStartedAt : undefined,
        target: report.target,
      });
      report.browserDiagnostics = browser.diagnostics;
    }
  } finally {
    report.actions.push(...tracker.actions);
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const sourceUnchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: sourceUnchanged ? 'passed' : 'failed',
        evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!sourceUnchanged) failure ??= new Error('Watched source changed during the browser run');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed',
        evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the browser run');
    } catch (freezeError) {
      report.freezeError = sanitizeText(freezeError.message ?? freezeError);
      report.assertions.push({ name: 'Watched source and API build stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= freezeError;
    }
    const failed = report.assertions.some(assertion => assertion.status === 'failed') || Boolean(failure);
    report.status = failed ? 'failed' : report.assertions.length ? 'partial' : 'untested';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
