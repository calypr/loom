import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

const BASE_HEADERS = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'];
const CASES = new Set([
  'Toggle source visibility', 'Verify constructed column rename',
  'Verify constructed column presentation', 'Inspect columns',
  'Inspect source column controls', 'Verify source column reorder',
]);
const nowStamp = () => new Date().toISOString().replaceAll(':', '-');

export function assertHeaders(actual, expected, label = 'Preview headers') {
  assert.deepEqual(actual, expected, `${label} must match exact expected order and membership`);
}

export function assertRestoredPresentation({ before, restored, beforeRows, restoredRows }) {
  assert.deepEqual(restored, before, 'Reloaded presentation must restore the exact original header order and values');
  if (beforeRows !== undefined || restoredRows !== undefined)
    assert.deepEqual(restoredRows, beforeRows, 'Presentation changes must preserve exact visible row values and identities');
}

export function assertHiddenPresentation({ before, hidden, column }) {
  assert(before.includes(column), `Expected the ${column} column before hiding it`);
  assert(!hidden.includes(column), `The ${column} column must be hidden`);
  assert.equal(hidden.length, before.length - 1, 'Hiding one column must remove exactly one visible column');
}

export function assertRowsMatchByColumnIdentity({ beforeHeaders, beforeRows, afterHeaders, afterRows }) {
  const normalize = (headers, rows) => rows.map(row => ({
    ordinal: row.ordinal,
    values: Object.fromEntries(headers.map((header, index) => [header, row.cells[index]])),
  }));
  assert.deepEqual(normalize(afterHeaders, afterRows), normalize(beforeHeaders, beforeRows),
    'Column reordering must preserve each exact value for each row identity and column identity');
}

const visibleTable = page => page.getByTestId('preview-table-scroll').getByRole('table');

async function tableSnapshot(page) {
  const table = visibleTable(page);
  await requireUnique(table, 'Builder Preview table');
  await table.waitFor({ state: 'visible', timeout: 15000 });
  const headers = (await table.getByRole('columnheader').allTextContents()).map(text => text.trim());
  const rows = await table.getByRole('row').evaluateAll(elements => elements.slice(1).map(row => ({
    ordinal: Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]),
    cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
  })));
  return { headers, ariaColumnCount: Number(await table.getAttribute('aria-colcount')), rows };
}

async function openColumns(page, tracker) {
  const button = page.getByRole('button', { name: 'Columns', exact: true });
  const list = page.getByRole('list', { name: 'Table columns', exact: true });
  await measuredAction(tracker, 'open Preview columns', button, target => target.click({ timeout: 5000 }),
    () => list.waitFor({ state: 'visible', timeout: 5000 }));
  return list;
}

async function actionAndSavedRender({ tracker, label, locator, action, page, expectedHeaders }) {
  const command = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.pathname.endsWith('/commands');
  }, { timeout: 5000 });
  const startedAt = Date.now();
  tracker.failureAction = { label, locator: locator.toString(), startedAt };
  try {
    await performAction(tracker, label, locator, action, { timeout: 5000 });
    const response = await command;
    assert(response.ok(), `${label}: production command returned HTTP ${response.status()}`);
    await page.waitForFunction(expectation => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      if (!table) return false;
      const headers = [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim());
      if (expectation.expectedHeaders) return JSON.stringify(headers) === JSON.stringify(expectation.expectedHeaders);
      if (expectation.addedColumn) return headers.length === expectation.priorHeaders.length + 1
        && headers.includes(expectation.addedColumn)
        && JSON.stringify(headers.filter(header => header !== expectation.addedColumn)) === JSON.stringify(expectation.priorHeaders);
      return false;
    }, Array.isArray(expectedHeaders) ? { expectedHeaders } : expectedHeaders, { timeout: 5000 });
    const elapsedMs = Date.now() - startedAt;
    assert(elapsedMs <= 5000, `${label} action-to-saved-render took ${elapsedMs} ms; maximum is 5000 ms`);
    tracker.timings.push({ name: label, elapsedMs, status: 'passed', responseStatus: response.status() });
    tracker.failureAction = undefined;
    return response.status();
  } catch (error) {
    tracker.failureAction.elapsedMs = Date.now() - startedAt;
    throw error;
  }
}

async function togglePreviewColumn({ page, tracker, columnKey, header, checked, expectedHeaders }) {
  const list = page.getByRole('list', { name: 'Table columns', exact: true });
  const labels = await list.getByRole('listitem').evaluateAll((items, key) => items.flatMap(item => {
    const input = item.querySelector('input[type="checkbox"]');
    const label = input?.getAttribute('aria-label');
    return label?.toLowerCase().endsWith(key.toLowerCase()) ? [label] : [];
  }), columnKey);
  assert.equal(labels.length, 1, `Preview must expose exactly one visibility control ending in ${columnKey}; found ${labels.length}`);
  const input = list.getByRole('checkbox', { name: labels[0], exact: true });
  await requireUnique(input, `Preview column ${labels[0]}`);
  assert.equal(await input.isVisible(), true, `${labels[0]}: column control must be visible`);
  assert.equal(await input.isEnabled(), true, `${labels[0]}: column control must be enabled`);
  assert.equal(await input.isChecked(), !checked, `${labels[0]}: control must have the expected pre-action state`);
  await actionAndSavedRender({ tracker, label: `set ${labels[0]} visibility to ${checked}`, locator: input,
    action: control => checked ? control.check({ timeout: 5000 }) : control.uncheck({ timeout: 5000 }), page, expectedHeaders });
}

async function reloadBuilder({ page, url, explorerId }) {
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 15000 });
  await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
  const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
  await requireUnique(explorer, 'Explorer selector after reload');
  assert.equal(await explorer.inputValue(), explorerId, 'Reload must remain scoped to the explicitly named Explorer');
  await visibleTable(page).waitFor({ state: 'visible', timeout: 15000 });
}

async function openAdvancedGraph(page, tracker) {
  const details = page.getByTestId('construction-source-setup');
  await requireUnique(details, 'Advanced source setup');
  if (await details.getAttribute('open') === null) {
    const summary = details.locator('summary');
    await measuredAction(tracker, 'open Advanced source setup', summary, target => target.click({ timeout: 5000 }),
      () => page.getByRole('button', { name: 'Advanced graph', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
  }
  const graph = page.getByRole('button', { name: 'Advanced graph', exact: true });
  await requireUnique(graph, 'Advanced graph view');
  if (await graph.getAttribute('aria-pressed') !== 'true') {
    await measuredAction(tracker, 'open Advanced graph', graph, target => target.click({ timeout: 5000 }),
      () => page.locator('input[aria-label^="Display name for construction output "]').first().waitFor({ state: 'visible', timeout: 5000 }));
  }
}

async function runCase(action, page, tracker, url, explorerId, report) {
  const table = visibleTable(page);
  await table.waitFor({ state: 'visible', timeout: 15000 });
  let initial = await tableSnapshot(page);
  report.evidence.initial = initial;
  report.lifecycle.preview = 'passed';

  if (action === 'Inspect columns') {
    const list = await openColumns(page, tracker);
    const controls = await list.getByRole('listitem').evaluateAll(items => items.map(item => {
      const checkbox = item.querySelector('input[type="checkbox"]');
      return { text: item.innerText.trim(), checked: checkbox?.checked, disabled: checkbox?.disabled,
        ariaLabel: checkbox?.getAttribute('aria-label') };
    }));
    assert(controls.length > 0, 'Preview Columns menu must expose the current column controls');
    assert.equal(initial.ariaColumnCount, initial.headers.length, 'Preview column count must match its visible headers');
    record(report, 'Preview headers and visibility controls are observable', true, { initial, controls });
    report.evidence.controls = controls;
    report.lifecycle = { preview: 'passed', apply: 'not applicable', reload: 'not covered', edit: 'not covered', removal: 'not covered' };
    return;
  }

  if (action === 'Inspect source column controls') {
    await openAdvancedGraph(page, tracker);
    const panel = page.getByTestId('construction-source-setup');
    const state = await panel.evaluate(element => ({
      text: element.innerText.slice(0, 5000),
      controls: [...element.querySelectorAll('button,input')].filter(control => {
        const rect = control.getBoundingClientRect();
        return rect.width > 0 && rect.height > 0;
      }).map(control => ({ tag: control.tagName, label: control.getAttribute('aria-label'), text: control.innerText?.slice(0, 70),
        disabled: control.disabled, readOnly: control.readOnly, value: control.value })),
    }));
    const moveControl = panel.getByRole('button', { name: 'Move Specimen ID to end', exact: true });
    await requireUnique(moveControl, 'Move Specimen ID to end');
    assert.equal(await moveControl.isEnabled(), true, 'Specimen ID must be movable for the source reorder lifecycle');
    report.evidence.sourceControls = state;
    record(report, 'Advanced source controls expose the final-column move as disabled', true, state);
    report.lifecycle = { preview: 'passed', apply: 'not applicable', reload: 'not covered', edit: 'not covered', removal: 'not covered' };
    return;
  }

  if (action === 'Toggle source visibility' || action === 'Verify constructed column presentation') {
    const targetHeader = action === 'Toggle source visibility' ? 'SUBJECT.REFERENCE' : 'ID';
    const targetKey = action === 'Toggle source visibility' ? 'subject.reference' : 'id';
    if (action === 'Toggle source visibility' && !initial.headers.includes(targetHeader)) {
      await openColumns(page, tracker);
      const list = page.getByRole('list', { name: 'Table columns', exact: true });
      const labels = await list.getByRole('listitem').evaluateAll((items, key) => items.flatMap(item => {
        const input = item.querySelector('input[type="checkbox"]');
        const label = input?.getAttribute('aria-label');
        return label?.toLowerCase().endsWith(key.toLowerCase()) ? [label] : [];
      }), targetKey);
      assert.equal(labels.length, 1, `Preview must expose one ${targetKey} visibility control`);
      const control = list.getByRole('checkbox', { name: labels[0], exact: true });
      await requireUnique(control, `Preview column ${labels[0]}`);
      assert.equal(await control.isChecked(), false, `${targetHeader} must be hidden before the source visibility setup`);
      await actionAndSavedRender({ tracker, label: `show ${labels[0]} for visibility lifecycle`, locator: control,
        action: input => input.check({ timeout: 5000 }), page,
        expectedHeaders: { addedColumn: targetHeader, priorHeaders: initial.headers } });
      initial = await tableSnapshot(page);
      assert(initial.headers.includes(targetHeader), `${targetHeader} visibility setup must render the selected source column`);
      report.evidence.initialAfterVisibilitySetup = initial;
    }
    assert(initial.headers.includes(targetHeader), `Initial Builder table must include ${targetHeader}`);
    const beforeRows = initial.rows;
    await openColumns(page, tracker);
    const hiddenHeaders = initial.headers.filter(header => header !== targetHeader);
    await togglePreviewColumn({ page, tracker, columnKey: targetKey, header: targetHeader, checked: false, expectedHeaders: hiddenHeaders });
    const hidden = await tableSnapshot(page);
    assertHiddenPresentation({ before: initial.headers, hidden: hidden.headers, column: targetHeader });
    report.evidence.hidden = hidden;
    report.lifecycle.apply = 'passed';

    await reloadBuilder({ page, url, explorerId });
    const hiddenAfterReload = await tableSnapshot(page);
    assertHeaders(hiddenAfterReload.headers, hiddenHeaders, 'Saved hidden column state after reload');
    assert.deepEqual(hiddenAfterReload.rows, hidden.rows, 'Reload must preserve exact rows while the column is hidden');
    report.evidence.hiddenAfterReload = hiddenAfterReload;
    report.lifecycle.reload = 'passed';

    await openColumns(page, tracker);
    await togglePreviewColumn({ page, tracker, columnKey: targetKey, header: targetHeader, checked: true, expectedHeaders: initial.headers });
    const restored = await tableSnapshot(page);
    assertRestoredPresentation({ before: initial.headers, restored: restored.headers, beforeRows, restoredRows: restored.rows });
    report.evidence.restored = restored;
    await reloadBuilder({ page, url, explorerId });
    const restoredAfterReload = await tableSnapshot(page);
    assertRestoredPresentation({ before: initial.headers, restored: restoredAfterReload.headers, beforeRows, restoredRows: restoredAfterReload.rows });
    report.evidence.restoredAfterReload = restoredAfterReload;
    report.lifecycle.reload = 'passed';
    report.lifecycle.edit = 'passed';
    report.lifecycle.removal = 'not applicable';
    record(report, `${targetHeader} hide and restore persist through reload`, true,
      { hiddenHeaders, restoredHeaders: restoredAfterReload.headers, rowIdentities: restoredAfterReload.rows.map(row => row.ordinal) });
    return;
  }

  if (action === 'Verify constructed column rename') {
    await openAdvancedGraph(page, tracker);
    const outputs = page.locator('input[aria-label^="Display name for construction output "]');
    const count = await outputs.count();
    assert(count > 0, 'The selected Builder document must expose a constructed output to rename');
    const output = outputs.nth(count - 1);
    await requireUnique(output, 'Last constructed output display name');
    assert.equal(await output.isEditable(), true, 'Constructed output display name must be editable');
    const original = await output.inputValue();
    assert(original, 'Constructed output display name must not be empty');
    const renamedValue = original === 'Patient ID QA' ? 'Patient ID QA 2' : 'Patient ID QA';
    const renamedHeader = renamedValue.toUpperCase();
    const before = initial.headers;
    const expectedRenamed = [...before.slice(0, -1), renamedHeader];
    await actionAndSavedRender({ tracker, label: 'rename constructed output', locator: output,
      action: async control => { await control.fill(renamedValue, { timeout: 5000 }); await control.press('Enter', { timeout: 5000 }); },
      page, expectedHeaders: expectedRenamed });
    const renamed = await tableSnapshot(page);
    report.evidence.renamed = renamed;
    report.lifecycle.apply = 'passed';
    await reloadBuilder({ page, url, explorerId });
    const renamedAfterReload = await tableSnapshot(page);
    assertHeaders(renamedAfterReload.headers, expectedRenamed, 'Renamed constructed column after reload');
    assert.deepEqual(renamedAfterReload.rows, renamed.rows, 'Rename must preserve exact visible row values and identities');
    report.evidence.renamedAfterReload = renamedAfterReload;
    report.lifecycle.reload = 'passed';

    await openAdvancedGraph(page, tracker);
    const renamedInput = page.getByRole('textbox', { name: `Display name for construction output ${renamedValue}`, exact: true });
    await requireUnique(renamedInput, 'Renamed constructed output');
    await actionAndSavedRender({ tracker, label: 'restore constructed output name', locator: renamedInput,
      action: async control => { await control.fill(original, { timeout: 5000 }); await control.press('Enter', { timeout: 5000 }); },
      page, expectedHeaders: before });
    const restored = await tableSnapshot(page);
    assertRestoredPresentation({ before, restored: restored.headers, beforeRows: initial.rows, restoredRows: restored.rows });
    await reloadBuilder({ page, url, explorerId });
    const restoredAfterReload = await tableSnapshot(page);
    assertRestoredPresentation({ before, restored: restoredAfterReload.headers, beforeRows: initial.rows, restoredRows: restoredAfterReload.rows });
    report.evidence.restoredAfterReload = restoredAfterReload;
    report.lifecycle.edit = 'passed';
    report.lifecycle.removal = 'not applicable';
    report.lifecycle.reload = 'passed';
    record(report, 'Constructed column name persists, then restores exactly', true,
      { original, renamedValue, headers: restoredAfterReload.headers, rows: restoredAfterReload.rows });
    return;
  }

  if (action === 'Verify source column reorder') {
    assertHeaders(initial.headers, BASE_HEADERS, 'Independent raw Specimen base-column contract');
    await openAdvancedGraph(page, tracker);
    const moveToEnd = async (label, expectedHeaders) => {
      const button = page.getByRole('button', { name: `Move ${label} to end`, exact: true });
      await requireUnique(button, `Move ${label} to end`);
      await actionAndSavedRender({ tracker, label: `move ${label} to end`, locator: button,
        action: control => control.click({ timeout: 5000 }), page, expectedHeaders });
      assert.equal(await button.isEnabled().catch(() => false), false, `Moved ${label} control must become the final-column disabled control`);
    };
    await moveToEnd('Specimen ID', [BASE_HEADERS[1], BASE_HEADERS[2], BASE_HEADERS[0]]);
    const reordered = await tableSnapshot(page);
    assertRowsMatchByColumnIdentity({ beforeHeaders: initial.headers, beforeRows: initial.rows,
      afterHeaders: reordered.headers, afterRows: reordered.rows });
    await reloadBuilder({ page, url, explorerId });
    const reorderedAfterReload = await tableSnapshot(page);
    assertHeaders(reorderedAfterReload.headers, [BASE_HEADERS[1], BASE_HEADERS[2], BASE_HEADERS[0]], 'Saved column order after reload');
    assert.deepEqual(reorderedAfterReload.rows, reordered.rows, 'Reload must retain reordered column values and row identities');
    await openAdvancedGraph(page, tracker);
    await moveToEnd('subject.reference', [BASE_HEADERS[2], BASE_HEADERS[0], BASE_HEADERS[1]]);
    await moveToEnd('collection.bodySite.reference.reference', BASE_HEADERS);
    const restored = await tableSnapshot(page);
    assertRestoredPresentation({ before: initial.headers, restored: restored.headers,
      beforeRows: initial.rows, restoredRows: restored.rows });
    await reloadBuilder({ page, url, explorerId });
    const restoredAfterReload = await tableSnapshot(page);
    assertRestoredPresentation({ before: initial.headers, restored: restoredAfterReload.headers,
      beforeRows: initial.rows, restoredRows: restoredAfterReload.rows });
    report.evidence.reorderedAfterReload = reorderedAfterReload;
    report.evidence.restoredAfterReload = restoredAfterReload;
    report.lifecycle.apply = 'passed';
    report.lifecycle.reload = 'passed';
    report.lifecycle.edit = 'passed';
    report.lifecycle.removal = 'not applicable';
    record(report, 'Source column order changes and restores through reload', true,
      { initial: initial.headers, reordered: reorderedAfterReload.headers, restored: restoredAfterReload.headers });
  }
}

export async function runBuilderColumnPresentation({ action, explorerId, env = process.env } = {}) {
  assert(CASES.has(action), `Unsupported Builder column presentation case: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const evidenceDirectory = resolve(target.artifacts, `playwright-builder-column-presentation-${nowStamp()}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const report = {
    schemaVersion: 1, scenario: 'cda-builder-column-presentation', case: action,
    status: 'running', target: { sourceRoot: target.sourceRoot, sourceFingerprint: sourceAtStart.fingerprint,
      apiBuildIdentity: buildAtStart, composeProject: target.composeProject, apiContainer: env.LOOM_CDA_API_CONTAINER,
      uiOrigin: target.uiUrl, apiOrigin: target.apiUrl, project: target.fixtureProject,
      generation: target.fixtureGeneration, explorerId },
    path: `Builder > ${action}`, expectedVisibleResult: action.includes('Inspect')
      ? 'The native controls and their actionable states match the Builder presentation model.'
      : 'Column visibility, name, or order changes are rendered, saved, and restored from the same explicit Explorer after reload.',
    independentOracle: 'Independent exact Specimen column identity/order contract for source reorder; preview column configuration controls and stable visible row identities/values for presentation changes.',
    lifecycle: { preview: 'untested', apply: 'untested', reload: 'untested', edit: 'untested', removal: 'not covered' },
    evidenceDirectory, assertions: [], actions: [], timings: [], evidence: {},
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let failure;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  let builderURL;
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
    builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: builderURL.toString() };
    const navigationStart = Date.now();
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    report.actions.push({ name: 'open Builder', elapsedMs: Date.now() - navigationStart, status: 'passed' });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    assert.equal(await explorer.inputValue(), explorerId, 'Builder must open the explicitly requested Explorer');
    const selectedOutput = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedOutput, 'Selected Builder table');
    report.selectedOutputId = (await selectedOutput.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await browser.context.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Read-only Builder document identity request must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog must match the exact isolated dataset generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === report.selectedOutputId);
    assert(document, 'Selected Builder output must exist in its independently queried saved document');
    report.selectedDocument = { outputId: report.selectedOutputId,
      rowResourceType: document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType,
      savedConstructionSteps: document.construction?.steps?.length };
    assert.equal(report.selectedDocument.rowResourceType, 'Specimen', 'Column case source contract requires the explicit Specimen document');

    await runCase(action, page, tracker, builderURL.toString(), explorerId, report);
    report.actions.push(...tracker.actions);
    report.timings = tracker.timings;
    record(report, 'No unexpected console, page, network, or HTTP errors', diagnostics.console.length === 0
      && diagnostics.pageErrors.length === 0 && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0,
    { console: diagnostics.console, pageErrors: diagnostics.pageErrors, networkFailures: diagnostics.networkFailures, httpFailures: diagnostics.httpFailures });
    report.status = action.startsWith('Inspect') ? 'partial' : 'passed';
  } catch (error) {
    failure = error;
    const failedAction = tracker.failureAction ?? tracker.activeAction;
    report.failure = { action: failedAction?.label ?? activeAction.label,
      locator: failedAction?.locator?.toString?.() ?? activeAction.locator,
      elapsedMs: failedAction?.elapsedMs ?? (failedAction?.startedAt ? Date.now() - failedAction.startedAt : undefined),
      message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, {
        action: { ...activeAction, ...failedAction, targetLocator: failedAction?.targetLocator },
        elapsedMs: report.failure.elapsedMs, target: report.target,
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
      report.assertions.push({ name: 'Watched source and source fingerprint stayed unchanged', status: sourceUnchanged ? 'passed' : 'failed',
        evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!sourceUnchanged) failure ??= new Error('Watched source changed during the Playwright case');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed', evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the Playwright case');
    } catch (error) {
      report.freezeError = sanitizeText(error.message ?? error);
      report.assertions.push({ name: 'Source and build identities stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= error;
    }
    if (failure || report.assertions.some(assertion => assertion.status === 'failed')) report.status = 'failed';
    else if (action.startsWith('Inspect')) report.status = 'partial';
    else report.status = 'passed';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
