import assert from 'node:assert/strict';
import { expect } from '../helpers/cda-fixtures.mjs';
import { writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

export const requireUnique = async (locator, label) => {
  await expect(locator, `${label} must resolve to one element`).toHaveCount(1, { timeout: 5000 });
  return locator;
};

export const captureBuilderScreenshot = async ({ page, cda, report, name }) => {
  const enabled = cda.env?.LOOM_CDA_CAPTURE_SCREENSHOTS === 'true'
    || process.env.LOOM_CDA_CAPTURE_SCREENSHOTS === 'true';
  if (!enabled) return false;
  await page.screenshot({ path: resolve(cda.evidence, name), fullPage: true });
  report.evidence ??= [];
  if (!report.evidence.includes(name)) report.evidence.push(name);
  return true;
};

export const record = (report, name, passed, evidence = {}) => {
  if (typeof report.nativeCheck === 'function') return report.nativeCheck(name, passed, evidence);
  report.assertions.push({ name, status: passed ? 'passed' : 'failed', evidence });
  assert(passed, name);
};

export const measuredAction = async (tracker, name, locator, action, rendered, { editable = false } = {}) => {
  const started = Date.now();
  tracker.actionStartedAt = started;
  let actionMs = 0;
  await tracker.cda.action(name, locator, async (...args) => {
    const actionStarted = Date.now();
    const result = await action(...args);
    actionMs = Date.now() - actionStarted;
    return result;
  }, { timeout: 5000, editable, after: rendered });
  const elapsedMs = Date.now() - started;
  tracker.timings.push({ name, actionMs, elapsedMs, status: 'passed' });
  assert(elapsedMs <= 5000, `${name} took ${elapsedMs} ms to render; maximum is 5000 ms`);
  return elapsedMs;
};

export async function runRelatedSourceChooser({ page, cda, explorerId = cda.target.explorer } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  const uiOrigin = target.uiUrl;
  const apiOrigin = target.apiUrl;
  Object.assign(report, {
    schemaVersion: 1,
    scenario: 'cda-builder-related-source-chooser',
    status: 'running',
    target: {
      ...report.target,
      sourceRoot: target.sourceRoot,
      composeProject: target.composeProject,
      apiContainer: target.apiContainer,
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
  });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };

    const projectURL = new URL(uiOrigin);
    projectURL.searchParams.set('project', target.fixtureProject);
    projectURL.searchParams.set('explorer', explorerId);
    projectURL.searchParams.set('mode', 'builder');
    const navigationStarted = Date.now();
    tracker.actionStartedAt = navigationStarted;
    await page.goto(projectURL.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStarted, status: 'passed' });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to the requested Explorer', await explorer.inputValue() === explorerId,
      { expectedExplorer: explorerId, actualExplorer: await explorer.inputValue() });

    const addColumns = page.locator('button[aria-label^="Add columns:"]');
    await requireUnique(addColumns, 'Add columns');
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
    (report.builderTimings ??= []).push(...tracker.timings);
    await captureBuilderScreenshot({ page, cda, report, name: 'related-source-chooser.png' });

    const expectedBrowserErrors = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', expectedBrowserErrors, diagnostics);
    report.status = 'partial';
  return report;
}
