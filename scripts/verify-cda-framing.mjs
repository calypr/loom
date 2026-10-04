import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { assertOwnedTarget, browserEval, click, fill, launchBrowser, navigate, selectOption, waitForBrowser, captureRequests, includeBrowserDiagnostics } from './lib/cda-playwright.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const explorerId = process.env.LOOM_CDA_EXPLORER_ID ?? process.argv[2];
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN?.replace(/\/$/, '');
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN?.replace(/\/$/, '');
assert(project, 'Set LOOM_CDA_PROJECT for the isolated CDA deployment.');
assert(explorerId, 'Set LOOM_CDA_EXPLORER_ID to an isolated Explorer containing a Specimen source table.');
assert.notEqual(explorerId, 'cda-builder-full-qa-1790440983382', 'The protected shared Explorer cannot be targeted.');
const explorerPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}`;
const requestPrefix = `${explorerPath}/authoring/v2`;
const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const evidenceDirectory = process.argv[3] ?? join('.artifacts', 'cda-builder', `framing-${Date.now()}`);
const report = { url, project, explorerId, clicks: [], responses: [], commandRequests: [], browserErrors: [], errors: [], incidentalErrors: [], nativeRequests: [], cleanup: false };
const screenshots = [];
let browser;
let browserEvents;
let duplicateId;
let fatal;
let cleanupDialogs = false;

const userClick = async (selector, label, identity = {}) => {
  await click(browser.page, selector, identity);
  report.clicks.push(label);
};
const tableIds = () => browserEval(browser.page, () => [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button => button.dataset.testid.slice('construction-table-'.length)));
const selectDuplicate = async () => {
  await waitForBrowser(browser.page, ([id]) => Boolean(document.querySelector(`[data-testid="construction-table-${id}"]`)), [duplicateId], 30000);
  await userClick(`[data-testid="construction-table-${duplicateId}"]`, 'Select temporary Specimen copy');
  await waitForBrowser(browser.page, ([id]) => document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-pressed') === 'true', [duplicateId], 30000);
};
const previewTable = () => browserEval(browser.page, () => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return { headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1, 6).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())) };
});
const captureScreenshot = async file => {
  const path = join(evidenceDirectory, file);
  await browser.page.screenshot({ path, fullPage: true });
  screenshots.push(file);
};

try {
  report.target = await assertOwnedTarget({ project, apiOrigin, uiOrigin });
  await mkdir(evidenceDirectory, { recursive: true });
  browser = await launchBrowser(evidenceDirectory, async () => ({ accept: cleanupDialogs }), { noAuth: true, apiOrigin, uiOrigin });
  browserEvents = captureRequests(browser, report, requestPrefix, { apiOrigin, uiOrigin });
  await navigate(browser.page, url);
  await waitForBrowser(browser.page, () => document.body.innerText.includes('DATASET WORKSPACE'), [], 30000);
  const sourceTestId = await browserEval(browser.page, () => [...document.querySelectorAll('[data-testid^="construction-table-"]')]
    .find(button => button.innerText.trim().endsWith('Specimen'))?.dataset.testid);
  assert(sourceTestId, 'Specimen source table is missing');
  await userClick(`[data-testid="${sourceTestId}"]`, 'Select Specimen table');
  const originalIds = await tableIds();
  const duplicateRequestStarted = Date.now();
  await userClick('[data-testid="construction-duplicate-table"]', 'Duplicate Specimen table');
  const duplicatePredicate = request => request.path === `${requestPrefix}/commands` && request.startedAt >= duplicateRequestStarted;
  report.nativeRequests.findLast(duplicatePredicate) ?? await waitForCapturedResponse(browser.page, browserEvents, duplicatePredicate, 30000);
  await waitForBrowser(browser.page, ([count]) => document.querySelectorAll('[data-testid^="construction-table-"]').length > count, [originalIds.length], 30000);
  report.afterDuplicate = await browserEval(browser.page, () => ({ text: document.body.innerText.slice(0, 1800), alerts: [...document.querySelectorAll('[role="alert"]')].map(item => item.innerText) }));
  duplicateId = (await tableIds()).find(id => !originalIds.includes(id));
  assert(duplicateId, 'Temporary output was not created');
  await navigate(browser.page, url);
  await selectDuplicate();

  const sourcePanel = '[data-testid="frame-source-panel"]';
  const sourceSearch = `${sourcePanel} input[aria-label="Search framing sources"]`;
  await waitForBrowser(browser.page, ([selector]) => Boolean(document.querySelector(selector)), [sourceSearch], 30000);
  await fill(browser.page, sourceSearch, 'days_to_collection');
  await userClick(`${sourcePanel} form button`, 'Search framing sources for days_to_collection');
  const relationshipSelector = `${sourcePanel} select[aria-label^="Relationship path for Observation component values (integer)"]`;
  await waitForBrowser(browser.page, ([selector]) => Boolean([...document.querySelectorAll(`${selector} option`)].find(option => option.innerText.includes('via specimen'))), [relationshipSelector], 30000);
  report.defaultPath = await browserEval(browser.page, ([selector]) => document.querySelector(selector).selectedOptions[0]?.textContent, [relationshipSelector]);
  assert(report.defaultPath.includes('via specimen'), `The source picker defaulted to ${report.defaultPath}`);
  await captureScreenshot('framing-source-picker.png');
  report.sourceOptions = await browserEval(browser.page, () => [...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].slice(0, 5).map(button => button.parentElement.innerText));
  const specimenOptionValue = await browserEval(browser.page, ([selector]) => [...document.querySelector(selector).options]
    .find(option => option.textContent.includes('via specimen'))?.value, [relationshipSelector]);
  assert(specimenOptionValue, 'The source picker omitted the via specimen relationship path.');
  await selectOption(browser.page, relationshipSelector, specimenOptionValue);
  await waitForBrowser(browser.page, ([selector]) => Boolean([...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].find(button =>
    button.parentElement?.querySelector(selector)?.selectedOptions[0]?.textContent.includes('via specimen'))), [relationshipSelector], 5000);
  const choiceTestId = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll('[data-testid^="frame-source-choice-"]')]
    .find(button => button.parentElement?.querySelector(selector)?.selectedOptions[0]?.textContent.includes('via specimen'))?.dataset.testid, [relationshipSelector]);
  assert(choiceTestId, 'Observation values via Specimen source choice is missing.');
  const sourceStarted = Date.now();
  await userClick(`[data-testid="${choiceTestId}"]`, 'Choose Observation values via Specimen reference');
  await waitForBrowser(browser.page, () => Boolean(document.querySelector('[data-testid^="saved-frame-"]')), [], 30000);
  report.saveSourceMs = Date.now() - sourceStarted;
  const frameId = await browserEval(browser.page, () => document.querySelector('[data-testid^="saved-frame-"]')?.dataset.testid.slice('saved-frame-'.length));
  assert(frameId, 'Saved framing source has no identity.');
  const frameCategories = `[data-testid="frame-categories-${frameId}"]`;
  await waitForBrowser(browser.page, ([selector]) => Boolean(document.querySelector(`${selector} input[type="search"]`)), [frameCategories], 30000);
  await captureScreenshot('framing-categories.png');
  const categorySearch = `${frameCategories} input[type="search"]`;
  await fill(browser.page, categorySearch, 'days_to_collection');
  await userClick(`${frameCategories} form button`, 'Search values in chosen frame');
  const categoryCheckbox = `${frameCategories} input[aria-label="Select days_to_collection"]`;
  await waitForBrowser(browser.page, ([selector]) => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled), [categoryCheckbox], 30000);
  await userClick(categoryCheckbox, 'Select days_to_collection');
  const proposalStarted = Date.now();
  const previewColumnButton = `${frameCategories} button`;
  const previewColumnLabel = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.includes('Preview 1 column'))?.innerText.trim(), [previewColumnButton]);
  assert(previewColumnLabel, 'Preview 1 column action is missing.');
  await userClick(previewColumnButton, 'Preview framed column', { includes: 'Preview 1 column' });
  await waitForBrowser(browser.page, () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', [], 30000);
  report.proposalMs = Date.now() - proposalStarted;
  report.proposal = await browserEval(browser.page, () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0, 1800));
  const applyProposal = '[data-testid="construction-choice-proposal-panel"] button';
  const applyLabel = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Apply columns' && !button.disabled)?.innerText.trim(), [applyProposal]);
  assert.equal(applyLabel, 'Apply columns', 'The proposal did not offer an enabled Apply columns action.');
  await userClick(applyProposal, 'Apply framed column', { name: 'Apply columns' });
  await waitForBrowser(browser.page, () => Boolean([...document.querySelectorAll('[data-testid^="construction-column-"]')].find(button => button.innerText.includes('days_to_collection'))), [], 30000);
  const previewButton = 'button';
  const previewLabel = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Preview' && !button.disabled)?.innerText.trim(), [previewButton]);
  assert.equal(previewLabel, 'Preview', 'The enabled Preview action is missing.');
  await userClick(previewButton, 'Preview applied table', { name: 'Preview' });
  await waitForBrowser(browser.page, () => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')), [], 30000);
  report.preview = await previewTable();
  const valueIndex = report.preview.headers.findIndex(header => header.toLowerCase().includes('days_to_collection'));
  assert(valueIndex >= 0, 'Framed column is absent from rendered preview');
  const valuedRow = report.preview.rows.find(row => /\d+/.test(row[valueIndex] ?? ''));
  assert(valuedRow, 'No Specimen preview row has a days_to_collection value');
  const specimenId = valuedRow[0];
  const oracleQuery = `FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.payload.specimen.reference == ${JSON.stringify(`Specimen/${specimenId}`)} LIMIT 10 RETURN d.payload.component`;
  const oracleScript = `print(JSON.stringify(db._query(${JSON.stringify(oracleQuery)}).toArray()))`;
  const oracleOutput = execFileSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', oracleScript], { encoding: 'utf8', maxBuffer: 200000 });
  const sourceValues = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('['))).flatMap(components => (components ?? []).filter(component =>
    component.code?.coding?.some(coding => coding.system === 'https://cda.readthedocs.io' && coding.code === 'days_to_collection'),
  ).map(component => component.valueInteger));
  report.sourceComparison = { specimenId, sourceValues, displayed: valuedRow[valueIndex] };
  assert(sourceValues.length > 0 && sourceValues.every(value => report.sourceComparison.displayed.includes(String(value))), 'Framed values differ from raw CDA code/value components');

  await navigate(browser.page, url);
  await selectDuplicate();
  await waitForBrowser(browser.page, ([id]) => Boolean(document.querySelector(`[data-testid="saved-frame-${id}"]`)), [frameId], 30000);
  report.persistedFrame = await browserEval(browser.page, ([id]) => document.querySelector(`[data-testid="saved-frame-${id}"]`)?.innerText, [frameId]);
  assert(report.persistedFrame.includes('Observation component values'), 'Framing source did not persist after reload');
  const frameRemove = `[data-testid="saved-frame-${frameId}"] button`;
  const removeLabel = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Remove')?.innerText.trim(), [frameRemove]);
  assert.equal(removeLabel, 'Remove', 'Saved framing source Remove action is missing.');
  await userClick(frameRemove, 'Remove saved framing source', { name: 'Remove' });
  await waitForBrowser(browser.page, ([selector]) => Boolean([...document.querySelectorAll(selector)].find(button => button.innerText === 'Apply this change')), [frameRemove], 30000);
  const confirmRemovalLabel = await browserEval(browser.page, ([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText === 'Apply this change')?.innerText, [frameRemove]);
  assert.equal(confirmRemovalLabel, 'Apply this change');
  await userClick(frameRemove, 'Confirm removal of framed columns', { name: 'Apply this change' });
  await waitForBrowser(browser.page, ([id]) => !document.querySelector(`[data-testid="saved-frame-${id}"]`), [frameId], 30000);
  await navigate(browser.page, url);
  await selectDuplicate();
  assert.equal(await browserEval(browser.page, () => document.querySelectorAll('[data-testid^="saved-frame-"]').length), 0, 'Framing source remained after reload');
  assert.equal(await browserEval(browser.page, () => [...document.querySelectorAll('[data-testid^="construction-column-"]')].filter(button => button.innerText.includes('days_to_collection')).length), 0, 'Framed column remained after source removal');
  await browserEvents.flush();
  report.responses = report.nativeRequests.filter(response => response.status !== undefined).map(response => ({
    requestId: response.requestId, path: response.path, status: response.status,
    elapsedMs: (response.completedAt ?? response.responseReceivedAt ?? Date.now()) - response.startedAt,
  }));
  report.commandRequests = report.nativeRequests.filter(request => request.path.endsWith('/commands'));
  report.failedResponses = report.nativeRequests.filter(response => response.status >= 400).map(response => ({ ...response, code: response.response?.error?.code }));
  assert(report.failedResponses.every(response => response.status === 409 && response.path.endsWith('/construction-capabilities') &&
    (response.code === 'STALE_DRAFT_VERSION' || response.code === 'STALE_DRAFT_DIGEST')),
  `Unexpected Builder error response: ${JSON.stringify(report.failedResponses)}`);
  report.browserErrors = browser.diagnostics.pageErrors;
  assert.deepEqual(report.browserErrors, [], 'Browser exceptions occurred');
  assert.deepEqual(browser.diagnostics.console, [], 'Unexpected browser console errors occurred');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'Unexpected application request failures occurred');
  const unexpectedHttpFailures = browser.diagnostics.httpFailures.filter(failure => !report.failedResponses.some(response =>
    response.status === failure.status && response.path === new URL(failure.url).pathname && response.path.endsWith('/construction-capabilities') &&
    (response.code === 'STALE_DRAFT_VERSION' || response.code === 'STALE_DRAFT_DIGEST'),
  ));
  assert.deepEqual(unexpectedHttpFailures, [], 'Unexpected application HTTP failures occurred');
  assert(report.proposalMs < 5000, `Framed column proposal took ${report.proposalMs}ms`);
} catch (error) {
  fatal = error;
  if (browser) await browser.captureFailure(error, { phase: 'framing-lifecycle', explorerId, project });
} finally {
  if (browser && duplicateId) {
    try {
      cleanupDialogs = true;
      await navigate(browser.page, url);
      await selectDuplicate();
      await userClick('[data-testid="construction-delete-table"]', 'Delete temporary framed table');
      await waitForBrowser(browser.page, ([id]) => !document.querySelector(`[data-testid="construction-table-${id}"]`), [duplicateId], 30000);
      report.cleanup = true;
      cleanupDialogs = false;
    } catch (error) {
      cleanupDialogs = false;
      report.cleanupError = String(error);
    }
  }
  if (browser) {
    includeBrowserDiagnostics(browser, report);
    report.browserDiagnostics = browser.diagnostics;
    await browser.close();
  }
  await mkdir(evidenceDirectory, { recursive: true });
  report.screenshots = screenshots;
  await writeFile(join(evidenceDirectory, 'framing-lifecycle.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ evidenceDirectory, clicks: report.clicks.length, proposalMs: report.proposalMs,
    saveSourceMs: report.saveSourceMs, cleanup: report.cleanup, cleanupError: report.cleanupError,
    failedResponses: report.failedResponses, errors: report.errors }, null, 2));
}
if (fatal) throw fatal;
