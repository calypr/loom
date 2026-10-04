import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';
import { basename, resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeBody, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { assertPersistedFilterState, assertVisibleIdentityMembership } from './lib/filter-oracle-assertions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

const actions = new Set([
  'Apply missing', 'Missing proposal', 'Remove saved filter', 'Edit saved missing filter',
  'Edit saved filter', 'Apply known filter', 'Toggle filter flag', 'Inspect filters',
]);
const KNOWN_ID = 'b7cad184-db67-5542-a975-10fffa3e89e7';
const NEXT_ID = '77d5efff-e239-57d9-88ac-bbb6394872fe';
const BODY_SITE = 'BodyStructure/4e5ae09f-f81e-5126-a6d9-97ac10405700';
const PREVIEW_LIMIT = 25;
const sha256 = value => createHash('sha256').update(value).digest('hex');
const storageKey = (project, generation, id) => sha256(['vertex', project, generation, 'Specimen', id, ''].join('\0'));

/** Derive filter membership and preview order only from the mounted raw CDA resource. */
export async function readFilterOracle({ datasetDir, project, generation }) {
  assert(project && generation, 'Filter oracle requires explicit project and generation');
  const directory = basename(resolve(datasetDir)) === 'META' ? resolve(datasetDir) : resolve(datasetDir, 'META');
  const path = resolve(directory, 'Specimen.ndjson');
  const hash = createHash('sha256');
  const ids = new Map();
  const first = [];
  const missing = [];
  const bodySiteMatches = [];
  let count = 0;
  const input = createReadStream(path);
  input.on('data', chunk => hash.update(chunk));
  for await (const line of createInterface({ input, crlfDelay: Infinity })) {
    if (!line.trim()) continue;
    const resource = JSON.parse(line);
    assert.equal(resource.resourceType, 'Specimen', 'Specimen source contains another resource type');
    assert(typeof resource.id === 'string' && resource.id, 'Specimen source contains an empty id');
    count += 1;
    const bodySite = resource.collection?.bodySite?.reference?.reference ?? null;
    if ([KNOWN_ID, NEXT_ID].includes(resource.id)) {
      assert(!ids.has(resource.id), `Raw source repeats filter sentinel ${resource.id}`);
      ids.set(resource.id, { id: resource.id, bodySite });
    }
    const entry = { key: storageKey(project, generation, resource.id), id: resource.id, bodySite };
    first.push(entry);
    if (bodySite === null) missing.push(entry);
    if (bodySite === BODY_SITE) bodySiteMatches.push(entry);
    if (first.length > PREVIEW_LIMIT) first.sort((a, b) => a.key.localeCompare(b.key)).pop();
    if (missing.length > PREVIEW_LIMIT) missing.sort((a, b) => a.key.localeCompare(b.key)).pop();
    if (bodySiteMatches.length > PREVIEW_LIMIT) bodySiteMatches.sort((a, b) => a.key.localeCompare(b.key)).pop();
  }
  assert(count > 0, 'Raw Specimen source is empty');
  assert(ids.has(KNOWN_ID), `Known filter sentinel ${KNOWN_ID} is absent from raw Specimen source`);
  assert(ids.has(NEXT_ID), `Edited filter sentinel ${NEXT_ID} is absent from raw Specimen source`);
  assert.equal(ids.get(NEXT_ID).bodySite, BODY_SITE, 'Raw source no longer supports the saved missing-filter edit expectation');
  const order = entries => entries.sort((a, b) => a.key.localeCompare(b.key)).map(({ id, bodySite }) => ({ id, bodySite }));
  return {
    path,
    sha256: hash.digest('hex'),
    specimenCount: count,
    firstRows: order(first),
    missingRows: order(missing),
    bodySiteMatchRows: order(bodySiteMatches),
    sentinels: Object.fromEntries(ids),
  };
}

const currentRows = async (page, expectedCount) => {
  const table = page.getByTestId('preview-table-scroll').getByRole('table');
  const scroll = page.getByTestId('preview-table-scroll');
  await requireUnique(table, 'Builder preview table');
  const headers = (await table.getByRole('columnheader').allTextContents()).map(text => text.trim());
  const ariaRowCount = await table.getAttribute('aria-rowcount');
  const observed = new Map();
  const readVisible = async () => {
    const visible = await table.getByRole('row').evaluateAll(rows => rows.slice(1).map(row => ({
      ordinal: Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]),
      cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
    })).filter(row => Number.isInteger(row.ordinal) && row.cells.length));
    for (const row of visible) observed.set(row.ordinal, row);
  };
  await readVisible();
  let lastOrdinal = Math.max(0, ...observed.keys());
  while (observed.size < expectedCount) {
    const bounds = await scroll.boundingBox();
    assert(bounds, 'Preview scroll area must be visible to inspect every row');
    await page.mouse.move(bounds.x + Math.min(bounds.width / 2, 80), bounds.y + Math.min(bounds.height / 2, 100));
    await page.mouse.wheel(0, 420);
    await page.waitForFunction(previous => {
      const rows = [...(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.querySelectorAll('[role="row"] button[aria-label^="Inspect row "]') ?? [])];
      return rows.some(button => Number(button.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]) > previous);
    }, lastOrdinal, { timeout: 5000 });
    await readVisible();
    const nextOrdinal = Math.max(...observed.keys());
    assert(nextOrdinal > lastOrdinal, 'Native scrolling must reveal a later preview source row');
    lastOrdinal = nextOrdinal;
    assert(observed.size <= expectedCount, 'Preview exposes more rows than the independent source window');
  }
  const rows = [...observed.values()].sort((left, right) => left.ordinal - right.ordinal);
  assert.equal(rows.length, expectedCount, 'Every expected preview row must be observed exactly once');
  return { ariaRowCount, ariaColCount: await table.getAttribute('aria-colcount'), headers,
    rows: rows.map(row => row.cells), ordinals: rows.map(row => row.ordinal) };
};

export async function runBuilderFilterCase({ action, explorerId, env = process.env } = {}) {
  assert(actions.has(action), `Unsupported Builder filter action: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const oracle = await readFilterOracle({ datasetDir: target.fixtureDir, project: target.fixtureProject, generation: target.fixtureGeneration });
  const evidenceDirectory = resolve(target.artifacts, `playwright-filter-${action.toLowerCase().replaceAll(' ', '-')}-${new Date().toISOString().replaceAll(':', '-')}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-filter-lifecycle',
    case: action,
    status: 'running',
    target: { sourceRoot: target.sourceRoot, sourceFingerprint: sourceAtStart.fingerprint, apiBuildIdentity: buildAtStart,
      composeProject: target.composeProject, apiContainer: env.LOOM_CDA_API_CONTAINER, uiOrigin: target.uiUrl,
      apiOrigin: target.apiUrl, project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    sourceOracle: { file: oracle.path, sha256: oracle.sha256, specimenCount: oracle.specimenCount,
      firstPreviewIds: oracle.firstRows.map(row => row.id), firstMissingIds: oracle.missingRows.map(row => row.id),
      bodySiteMatchIds: oracle.bodySiteMatchRows.map(row => row.id), sentinels: oracle.sentinels },
    path: 'Builder > Filter rows > choose column/condition/value > preview > Apply or Cancel > reload; saved filter edit/remove paths where requested',
    expectedVisibleResult: action === 'Inspect filters'
      ? 'Every visible Use … as filter option is uniquely named and reports its checked and disabled state.'
      : action === 'Missing proposal'
        ? 'A missing-value filter proposal reaches a reported ready or error state; this inspection remains partial until its product outcome has an independent contract.'
        : 'The resulting saved filter state and visible Specimen identities agree with the independently ordered raw CDA source after reload.',
    independentOracle: 'Raw CDA Specimen.ndjson, exact vertex storage-key ordering, and raw nested collection.bodySite.reference.reference values; no UI preview supplies expected values.',
    lifecycle: { filterEditor: 'untested', proposal: 'untested', apply: action === 'Missing proposal' || action === 'Inspect filters' || action === 'Toggle filter flag' ? 'not covered' : 'untested',
      cancel: 'not covered', reload: action === 'Inspect filters' ? 'not covered' : 'untested', edit: action.startsWith('Edit') ? 'untested' : 'not covered',
      removal: action === 'Remove saved filter' ? 'untested' : 'not covered', restoration: action === 'Toggle filter flag' ? 'untested' : 'not covered' },
    evidenceDirectory, assertions: [], actions: [], timings: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let page;
  let diagnostics;
  let failure;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  const setActive = (label, locator) => { activeAction = { label, locator: locator.toString(), targetLocator: locator }; };
  const measured = async (name, locator, actionFn, rendered) => {
    setActive(name, locator);
    return measuredAction(tracker, name, locator, actionFn, rendered);
  };
  const waitForProposal = async (expected = ['ready']) => {
    const panel = page.getByTestId('construction-proposal-panel');
    await panel.waitFor({ state: 'visible', timeout: 5000 });
    await page.waitForFunction(status => {
      const current = document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status');
      return status.includes(current);
    }, expected, { timeout: 5000 });
    return { status: await panel.getAttribute('data-proposal-status'), text: await panel.innerText().catch(() => '') };
  };
  const doReload = async () => {
    setActive('reload Builder', page);
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Reload remains scoped to requested Explorer', await explorer.inputValue() === explorerId,
      { expectedExplorer: explorerId, actualExplorer: await explorer.inputValue() });
  };
  const showPreview = async expectedCount => {
    const preview = page.getByTestId('preview-table-scroll').getByRole('table');
    if (await preview.count() === 0) {
      const button = page.getByRole('button', { name: 'Preview', exact: true });
      await requireUnique(button, 'Preview');
      await measured('render Builder preview', button, b => b.click({ timeout: 5000 }), () =>
        page.waitForFunction(count => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(count + 1), expectedCount, { timeout: 5000 }));
    } else {
      await preview.waitFor({ state: 'visible', timeout: 5000 });
    }
    const result = await currentRows(page, expectedCount);
    assert.equal(Number(result.ariaRowCount), expectedCount + 1, 'Preview row count differs from independent oracle window');
    return result;
  };
  const historySteps = () => page.locator('[data-testid^="construction-history-step-"]');
  const applyProposal = async (expectedHistoryCount, expectedRows) => {
    const apply = page.getByTestId('construction-apply-proposal');
    await requireUnique(apply, 'Apply proposal');
    assert.equal(await apply.isEnabled(), true, 'Apply is unavailable for a ready proposal');
    await measured('Apply filter proposal', apply, b => b.click({ timeout: 5000 }), () =>
      page.waitForFunction(expected => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === expected.count
        && !document.querySelector('[data-testid="construction-proposal-panel"]')
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(expected.expectedRows + 1),
      { count: expectedHistoryCount, expectedRows }, { timeout: 5000 }));
    report.lifecycle.apply = 'passed';
  };
  const chooseCondition = async (columnPredicate, condition, proposalExpected) => {
    const editor = page.getByTestId('construction-filter-editor');
    await editor.waitFor({ state: 'visible', timeout: 5000 });
    const column = editor.getByLabel('Column', { exact: true });
    await requireUnique(column, 'Filter column');
    if (columnPredicate) {
      const optionValue = await column.locator('option').evaluateAll((options, phrase) => {
        const match = options.find(option => (option.textContent ?? '').toLowerCase().includes(phrase.toLowerCase()));
        return match?.value;
      }, String(columnPredicate));
      assert(optionValue, `Filter column matching ${columnPredicate} is absent`);
      await measured('choose filter column', column, select => select.selectOption(optionValue, { timeout: 5000 }), async () =>
        assert.equal(await column.inputValue(), optionValue));
    }
    const conditionSelect = editor.getByLabel('Condition', { exact: true });
    await requireUnique(conditionSelect, 'Filter condition');
    await measured(`choose ${condition} filter condition`, conditionSelect,
      select => select.selectOption(condition, { timeout: 5000 }), async () => {
        assert.equal(await conditionSelect.inputValue(), condition);
        if (proposalExpected) await waitForProposal(proposalExpected);
      });
    report.lifecycle.filterEditor = 'passed';
    return { editor, column, conditionSelect };
  };
  const fillValue = async (editor, value, proposalExpected) => {
    const input = editor.getByRole('textbox', { name: 'Value', exact: true });
    await requireUnique(input, 'Filter value');
    await measured('fill filter value', input, field => field.fill(value, { timeout: 5000 }), async () => {
      assert.equal(await input.inputValue(), value);
      if (proposalExpected) await waitForProposal(proposalExpected);
    });
  };
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    ({ page, diagnostics } = browser);
    const requestRecords = new Map();
    page.on('request', request => {
      const url = new URL(request.url());
      if (!url.pathname.includes('/authoring/v2/')) return;
      const requestRecord = { method: request.method(), path: url.pathname, startedAt: Date.now() };
      const postData = request.postData();
      if (postData) requestRecord.body = sanitizeBody(postData);
      requestRecords.set(request, requestRecord);
      report.apiRequests ??= [];
      report.apiRequests.push(requestRecord);
    });
    page.on('response', async response => {
      const requestRecord = requestRecords.get(response.request());
      if (!requestRecord) return;
      requestRecord.status = response.status();
      requestRecord.elapsedMs = Date.now() - requestRecord.startedAt;
      requestRecord.serverRequestId = response.headers()['x-request-id'] ?? null;
      if (/proposal|preview|commands/.test(requestRecord.path) || response.status() >= 400) {
        requestRecord.response = sanitizeBody(await response.text().catch(error => `response read failed: ${sanitizeText(error.message)}`));
      }
    });
    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: builderURL.toString() };
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to requested Explorer', await explorer.inputValue() === explorerId,
      { expectedExplorer: explorerId, actualExplorer: await explorer.inputValue() });
    const selectedTable = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedTable, 'Selected Builder table');
    const outputId = (await selectedTable.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await browser.context.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Read-only Builder identity request must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation differs from the explicit raw CDA generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, `Selected output ${outputId} is absent from the independent Builder document`);
    const rootType = document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType;
    assert.equal(rootType, 'Specimen', 'Raw filter oracle is scoped to a Specimen-root Builder table');
    record(report, 'Selected table matches the raw Specimen project and generation', true,
      { outputId, rootType, generation: builder.catalog.generation, responseStatus: builderResponse.status() });

    if (action === 'Toggle filter flag' || action === 'Inspect filters') {
      const sourceSetup = page.getByTestId('construction-source-setup');
      await requireUnique(sourceSetup, 'Advanced source setup');
      if (await sourceSetup.getAttribute('open') === null) {
        const summary = sourceSetup.getByText('Advanced source setup', { exact: true });
        await measured('open advanced source setup', summary, target => target.click({ timeout: 5000 }), () =>
          page.waitForFunction(() => document.querySelector('[data-testid="construction-source-setup"]')?.open === true, undefined, { timeout: 5000 }));
      }
      const flags = page.locator('input[aria-label^="Use "][type="checkbox"]');
      const count = await flags.count();
      assert(count > 0, 'Source setup must expose filter flags');
      const initial = await flags.evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked,
        disabled: input.disabled, visible: input.getBoundingClientRect().width > 0 && input.getBoundingClientRect().height > 0 })));
      assert(initial.every(input => input.label && input.visible), 'Every filter flag must have a unique accessible name and be visible');
      assert.equal(new Set(initial.map(input => input.label)).size, initial.length, 'Filter flags must have unique names');
      record(report, 'Filter flag controls have unique visible accessible labels', true, initial);
      if (action === 'Inspect filters') {
        report.lifecycle.filterEditor = 'passed';
        report.lifecycle.apply = 'not applicable';
        report.lifecycle.reload = 'not applicable';
        report.status = 'partial';
        report.partialReason = 'Control inventory only; no filter evaluation or persistence is claimed.';
        report.controlInventory = initial;
      } else {
        report.status = 'partial';
        report.partialReason = 'The flag toggle and restoration persist, but this action does not exercise a filtered preview result.';
        const targetFlag = page.getByRole('checkbox', { name: 'Use Specimen ID as filter', exact: true });
        await requireUnique(targetFlag, 'Use Specimen ID as filter');
        assert.equal(await targetFlag.isEnabled(), true, 'Specimen ID filter flag must be enabled');
        const before = await targetFlag.isChecked();
        const changeResponse = page.waitForResponse(response => response.request().method() === 'POST'
          && new URL(response.url()).pathname.endsWith('/authoring/v2/commands'), { timeout: 5000 });
        await measured('toggle Specimen ID filter flag', targetFlag, checkbox => checkbox.setChecked(!before, { timeout: 5000 }), async () => {
          assert.equal(await targetFlag.isChecked(), !before);
          assert.equal((await changeResponse).status(), 200, 'Filter flag command must save successfully');
        });
        await doReload();
        const reloaded = page.getByRole('checkbox', { name: 'Use Specimen ID as filter', exact: true });
        await requireUnique(reloaded, 'Reloaded Specimen ID filter flag');
        assert.equal(await reloaded.isChecked(), !before, 'Filter flag did not persist through reload');
        const restoreResponse = page.waitForResponse(response => response.request().method() === 'POST'
          && new URL(response.url()).pathname.endsWith('/authoring/v2/commands'), { timeout: 5000 });
        await measured('restore Specimen ID filter flag', reloaded, checkbox => checkbox.setChecked(before, { timeout: 5000 }), async () => {
          assert.equal(await reloaded.isChecked(), before);
          assert.equal((await restoreResponse).status(), 200, 'Filter flag restoration command must save successfully');
        });
        await doReload();
        const restored = page.getByRole('checkbox', { name: 'Use Specimen ID as filter', exact: true });
        await requireUnique(restored, 'Restored Specimen ID filter flag');
        assert.equal(await restored.isChecked(), before, 'Filter flag restoration did not persist through reload');
        report.lifecycle.restoration = 'passed';
        report.lifecycle.reload = 'passed';
        record(report, 'Filter flag toggle and restoration persist after reload', true, { before, changed: !before, restored: await restored.isChecked() });
      }
    } else if (action === 'Apply missing' || action === 'Missing proposal' || action === 'Apply known filter') {
      const filterButton = page.locator('button[aria-label^="Filter rows:"]');
      await requireUnique(filterButton, 'Filter rows action');
      await measured('open Filter rows', filterButton, button => button.click({ timeout: 5000 }), () =>
        page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: 5000 }));
      const proposalExpected = action === 'Missing proposal' ? ['ready', 'error'] : ['ready'];
      const { editor } = await chooseCondition(action === 'Apply known filter' ? null : 'collection.bodySite',
        action === 'Apply known filter' ? 'EQUALS' : 'MISSING', action === 'Apply known filter' ? undefined : proposalExpected);
      const selectedColumn = await editor.getByLabel('Column', { exact: true }).locator('option:checked').innerText();
      assert.match(selectedColumn.toLowerCase(), action === 'Apply known filter' ? /specimen.*id|^id/ : /collection\.bodysite/,
        'Filter form selected a different column than the independent raw CDA oracle');
      if (action === 'Apply known filter') await fillValue(editor, KNOWN_ID, proposalExpected);
      const proposal = await waitForProposal(action === 'Missing proposal' ? ['ready', 'error'] : ['ready']);
      if (action === 'Missing proposal') {
        report.lifecycle.proposal = 'untested';
        report.assertions.push({ name: 'Missing-filter proposal outcome retained for review', status: 'partial', evidence: proposal });
        report.status = 'partial';
        report.partialReason = 'Original case records proposal behavior without asserting a product outcome; retained as an inspection and cannot count as a pass.';
        report.proposal = proposal;
      } else {
        report.lifecycle.proposal = 'passed';
        record(report, 'Filter proposal reaches the required ready state', proposal.status === 'ready', proposal);
        await applyProposal(1, action === 'Apply known filter' ? 1 : PREVIEW_LIMIT);
        const history = historySteps();
        assert.equal(await history.count(), 1, 'Applied filter must create exactly one saved history step');
        await doReload();
          assert.equal(await historySteps().count(), 1, 'Applied filter did not persist through reload');
        report.lifecycle.reload = 'passed';
        const shown = await showPreview(action === 'Apply known filter' ? 1 : PREVIEW_LIMIT);
        if (action === 'Apply known filter') {
          assert(shown.rows.some(row => row[0] === KNOWN_ID), 'Applied equality preview omitted the independent source ID');
          const source = oracle.sentinels[KNOWN_ID];
          assert(source, 'Known filter ID must come from the raw source oracle');
          assert.deepEqual(shown.rows.map(row => row[0]), [source.id], 'Equality result must be the exact raw CDA identity');
        } else {
          const expectedIds = oracle.missingRows.map(row => row.id);
          assertPersistedFilterState({ actualHistoryCount: await historySteps().count(), expectedHistoryCount: 1,
            actualIds: shown.rows.map(row => row[0]), expectedIds, context: 'Missing filter reload preview' });
          assert(!shown.rows.some(row => row[0] === NEXT_ID), 'Missing filter must exclude the independent non-null body-site record');
        }
        record(report, 'Saved filter preview identities match raw CDA oracle', true, shown);
        report.lifecycle.preview = 'passed';
      }
    } else if (action === 'Edit saved filter' || action === 'Edit saved missing filter' || action === 'Remove saved filter') {
      const history = historySteps();
      await history.first().waitFor({ state: 'visible', timeout: 5000 });
      await requireUnique(history, 'Saved filter history step');
      assert.equal(await history.count(), 1, 'Lifecycle case requires exactly one saved filter');
      const stepId = (await history.getAttribute('data-testid')).replace('construction-history-step-', '');
      await measured('select saved filter', history, button => button.click({ timeout: 5000 }), () =>
        page.waitForFunction(id => document.querySelector(`[data-testid="construction-history-step-${CSS.escape(id)}"]`)?.getAttribute('aria-pressed') === 'true', stepId, { timeout: 5000 }));
      if (action === 'Remove saved filter') {
        const remove = page.getByTestId(`construction-remove-step-${stepId}`);
        await requireUnique(remove, 'Remove saved filter');
        await measured('remove saved filter', remove, button => button.click({ timeout: 5000 }), () =>
          waitForProposal(['ready']));
        const proposal = await waitForProposal(['ready']);
        report.lifecycle.proposal = 'passed';
        record(report, 'Filter removal proposal is ready', proposal.status === 'ready', proposal);
        await applyProposal(0, PREVIEW_LIMIT);
        await doReload();
        const historyCountAfterReload = await historySteps().count();
        report.lifecycle.removal = 'passed';
        report.lifecycle.reload = 'passed';
        const shown = await showPreview(PREVIEW_LIMIT);
        assert(shown.headers.includes('TYPE.CODING[].CODE'), 'Restored base preview lost the expected specimen type column');
        assert(!shown.headers.some(header => header.includes('ITEM')), 'Restored base preview still exposes a removed filter artifact');
        assertPersistedFilterState({ actualHistoryCount: historyCountAfterReload, expectedHistoryCount: 0,
          actualIds: shown.rows.map(row => row[0]), expectedIds: oracle.firstRows.map(row => row.id), context: 'Filter removal preview' });
        record(report, 'Removing filter restores raw unfiltered source identities', true, { proposal, ...shown });
        report.lifecycle.preview = 'passed';
      } else {
        const edit = page.getByTestId(`construction-edit-step-${stepId}`);
        await requireUnique(edit, 'Edit saved filter');
        await measured('edit saved filter', edit, button => button.click({ timeout: 5000 }), () =>
          page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: 5000 }));
        const editor = page.getByTestId('construction-filter-editor');
        const condition = editor.getByLabel('Condition', { exact: true });
        await requireUnique(condition, 'Saved filter condition');
        const originalCondition = await condition.inputValue();
        if (action === 'Edit saved missing filter') assert.equal(originalCondition, 'MISSING');
        else {
          assert.equal(originalCondition, 'EQUALS', 'Saved equality filter must reopen its exact persisted condition');
          const value = editor.getByRole('textbox', { name: 'Value', exact: true });
          await requireUnique(value, 'Saved filter value');
          const original = await value.inputValue();
          assert.equal(original, KNOWN_ID, 'Saved equality filter must reopen its exact persisted value');
        }
        if (action === 'Edit saved missing filter') {
          const selectedColumn = await editor.getByLabel('Column', { exact: true }).locator('option:checked').innerText();
          assert.match(selectedColumn.toLowerCase(), /collection\.bodysite/, 'Saved missing filter column changed from the raw body-site field');
          await measured('change saved filter condition to EQUALS', condition, select => select.selectOption('EQUALS', { timeout: 5000 }), async () =>
            { assert.equal(await condition.inputValue(), 'EQUALS'); await editor.getByRole('textbox', { name: 'Value', exact: true }).waitFor({ state: 'visible', timeout: 5000 }); });
          await fillValue(editor, BODY_SITE, ['ready']);
        } else {
          await fillValue(editor, NEXT_ID, ['ready']);
        }
        const proposal = await waitForProposal(['ready']);
        report.lifecycle.proposal = 'passed';
        record(report, 'Edited filter proposal is ready', proposal.status === 'ready', proposal);
        const proposalText = await page.getByTestId('construction-proposal-preview').innerText();
        assert(proposalText.includes(NEXT_ID), 'Edited equality proposal omits the independently sourced matching record');
        await applyProposal(1, action === 'Edit saved missing filter' ? PREVIEW_LIMIT : 1);
        await doReload();
        assert.equal(await historySteps().count(), 1, 'Edited filter history step did not persist');
        report.lifecycle.edit = 'passed';
        report.lifecycle.reload = 'passed';
        const shown = await showPreview(action === 'Edit saved missing filter' ? PREVIEW_LIMIT : 1);
        if (action === 'Edit saved missing filter') {
          assertVisibleIdentityMembership(shown.rows, oracle.bodySiteMatchRows.map(row => row.id), 'Edited body-site equality preview');
          assert(shown.rows.some(row => row[0] === NEXT_ID), 'Edited filter omitted the source row with the saved body-site value');
          assert(shown.rows.every(row => row[2] === BODY_SITE), 'Edited equality preview contains a different body-site value');
          assert.equal(oracle.sentinels[NEXT_ID].bodySite, BODY_SITE, 'Raw CDA body-site oracle changed');
        } else {
          assertPersistedFilterState({ actualHistoryCount: await historySteps().count(), expectedHistoryCount: 1,
            actualIds: shown.rows.map(row => row[0]), expectedIds: [NEXT_ID], context: 'Edited equality filter reload' });
          assert(!shown.rows.some(row => row[0] === KNOWN_ID), 'Edited equality preview retained the previous source ID');
        }
        record(report, 'Edited saved filter output matches raw CDA source', true, { proposal, proposalText, ...shown });
        report.lifecycle.preview = 'passed';
      }
    }

    const errors = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', errors, diagnostics);
    report.browserDiagnostics = diagnostics;
    report.timings = tracker.timings;
    report.evidence = ['filter-case.json', 'final.png'];
    await page.screenshot({ path: `${evidenceDirectory}/final.png`, fullPage: true });
    await writeFile(`${evidenceDirectory}/filter-case.json`, JSON.stringify({ action, oracle: report.sourceOracle,
      assertions: report.assertions, lifecycle: report.lifecycle, timings: tracker.timings, diagnostics }, null, 2) + '\n', { mode: 0o600 });
  } catch (error) {
    failure = error;
    report.failure = { action: activeAction.label, locator: activeAction.locator,
      elapsedMs: tracker.actionStartedAt ? Date.now() - tracker.actionStartedAt : undefined,
      message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, { action: { ...activeAction, startedAt: tracker.activeAction?.startedAt },
        elapsedMs: report.failure.elapsedMs, target: report.target });
      report.browserDiagnostics = browser.diagnostics;
    }
  } finally {
    report.actions.push(...tracker.actions);
    report.timings = tracker.timings;
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const unchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: unchanged ? 'passed' : 'failed', evidence: {
        before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!unchanged) failure ??= new Error('Watched source changed during the filter verification');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed', evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the filter verification');
    } catch (error) {
      report.freezeError = sanitizeText(error.message ?? error);
      report.assertions.push({ name: 'Watched source and API build stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= error;
    }
    if (failure || report.assertions.some(item => item.status === 'failed')) report.status = 'failed';
    else if (report.status !== 'partial') report.status = 'passed';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
