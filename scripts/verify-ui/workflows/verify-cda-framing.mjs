import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

export async function framingWorkflow({ page, cda }) {
const project = cda.project;
const explorerId = cda.explorer ?? process.env.LOOM_CDA_EXPLORER_ID;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
assert(project, 'Set LOOM_CDA_PROJECT for the isolated CDA deployment.');
assert(explorerId, 'Set LOOM_CDA_EXPLORER_ID to an isolated Explorer containing a Specimen source table.');
assert.notEqual(explorerId, 'cda-builder-full-qa-1790440983382', 'The protected shared Explorer cannot be targeted.');
const explorerPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}`;
const requestPrefix = `${explorerPath}/authoring/v2`;
const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const evidenceDirectory = cda.evidence;
const report = Object.assign(cda.report, { url, project, explorerId, clicks: [], responses: [], commandRequests: [], browserErrors: [], incidentalErrors: [], cleanup: false });
const screenshots = [];
let browserEvents;
let duplicateId;
let fatal;
let cleanupDialogs = false;
cda.onDialog(async dialog => ({ accept: cleanupDialogs }));

const userClick = async (selector, label, identity = {}) => {
  await cda.click(selector, identity);
  report.clicks.push(label);
};
const tableIds = () => cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button => button.dataset.testid.slice('construction-table-'.length)));
const selectDuplicate = async () => {
  await cda.wait(([id]) => Boolean(document.querySelector(`[data-testid="construction-table-${id}"]`)), [duplicateId], 5000);
  await userClick(`[data-testid="construction-table-${duplicateId}"]`, 'Select temporary Specimen copy');
  await cda.wait(([id]) => document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-pressed') === 'true', [duplicateId], 5000);
};
const previewTable = () => cda.inspect(() => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return { headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1, 6).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())) };
});
const captureScreenshot = async file => {
  const path = join(evidenceDirectory, file);
  if (process.env.LOOM_VERIFY_SCREENSHOTS === '1') {
    await page.screenshot({ path, fullPage: true });
    screenshots.push(file);
  }
};

try {
  browserEvents = cda.captureRequests(requestPrefix);
  await cda.navigate(url);
  await cda.wait(() => document.body.innerText.includes('DATASET WORKSPACE'), [], 5000);
  const sourceTestId = await cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-table-"]')]
    .find(button => button.innerText.trim().endsWith('Specimen'))?.dataset.testid);
  assert(sourceTestId, 'Specimen source table is missing');
  await userClick(`[data-testid="${sourceTestId}"]`, 'Select Specimen table');
  const originalIds = await tableIds();
  const duplicateRequestStarted = Date.now();
  await userClick('[data-testid="construction-duplicate-table"]', 'Duplicate Specimen table');
  const duplicatePredicate = request => request.path === `${requestPrefix}/commands` && request.startedAt >= duplicateRequestStarted;
  report.nativeRequests.findLast(duplicatePredicate) ?? await cda.waitForCapturedResponse(browserEvents, duplicatePredicate, 5000);
  await cda.wait(([count]) => document.querySelectorAll('[data-testid^="construction-table-"]').length > count, [originalIds.length], 5000);
  report.afterDuplicate = await cda.inspect(() => ({ text: document.body.innerText.slice(0, 1800), alerts: [...document.querySelectorAll('[role="alert"]')].map(item => item.innerText) }));
  duplicateId = (await tableIds()).find(id => !originalIds.includes(id));
  assert(duplicateId, 'Temporary output was not created');
  await cda.navigate(url);
  await selectDuplicate();

  const sourcePanel = '[data-testid="frame-source-panel"]';
  const sourceSearch = `${sourcePanel} input[aria-label="Search framing sources"]`;
  await cda.wait(([selector]) => Boolean(document.querySelector(selector)), [sourceSearch], 5000);
  await cda.fill(sourceSearch, 'days_to_collection');
  await userClick(`${sourcePanel} form button`, 'Search framing sources for days_to_collection');
  const relationshipSelector = `${sourcePanel} select[aria-label^="Relationship path for Observation component values (integer)"]`;
  await cda.wait(([selector]) => Boolean([...document.querySelectorAll(`${selector} option`)].find(option => option.innerText.includes('via specimen'))), [relationshipSelector], 5000);
  report.defaultPath = await cda.inspect(([selector]) => document.querySelector(selector).selectedOptions[0]?.textContent, [relationshipSelector]);
  assert(report.defaultPath.includes('via specimen'), `The source picker defaulted to ${report.defaultPath}`);
  await captureScreenshot('framing-source-picker.png');
  report.sourceOptions = await cda.inspect(() => [...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].slice(0, 5).map(button => button.parentElement.innerText));
  const specimenOptionValue = await cda.inspect(([selector]) => [...document.querySelector(selector).options]
    .find(option => option.textContent.includes('via specimen'))?.value, [relationshipSelector]);
  assert(specimenOptionValue, 'The source picker omitted the via specimen relationship path.');
  await cda.selectOption(relationshipSelector, specimenOptionValue);
  await cda.wait(([selector]) => Boolean([...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].find(button =>
    button.parentElement?.querySelector(selector)?.selectedOptions[0]?.textContent.includes('via specimen'))), [relationshipSelector], 5000);
  const choiceTestId = await cda.inspect(([selector]) => [...document.querySelectorAll('[data-testid^="frame-source-choice-"]')]
    .find(button => button.parentElement?.querySelector(selector)?.selectedOptions[0]?.textContent.includes('via specimen'))?.dataset.testid, [relationshipSelector]);
  assert(choiceTestId, 'Observation values via Specimen source choice is missing.');
  const sourceStarted = Date.now();
  await userClick(`[data-testid="${choiceTestId}"]`, 'Choose Observation values via Specimen reference');
  await cda.wait(() => Boolean(document.querySelector('[data-testid^="saved-frame-"]')), [], 5000);
  report.saveSourceMs = Date.now() - sourceStarted;
  const frameId = await cda.inspect(() => document.querySelector('[data-testid^="saved-frame-"]')?.dataset.testid.slice('saved-frame-'.length));
  assert(frameId, 'Saved framing source has no identity.');
  const frameCategories = `[data-testid="frame-categories-${frameId}"]`;
  await cda.wait(([selector]) => Boolean(document.querySelector(`${selector} input[type="search"]`)), [frameCategories], 5000);
  await captureScreenshot('framing-categories.png');
  const categorySearch = `${frameCategories} input[type="search"]`;
  await cda.fill(categorySearch, 'days_to_collection');
  await userClick(`${frameCategories} form button`, 'Search values in chosen frame');
  const categoryCheckbox = `${frameCategories} input[aria-label="Select days_to_collection"]`;
  await cda.wait(([selector]) => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled), [categoryCheckbox], 5000);
  await userClick(categoryCheckbox, 'Select days_to_collection');
  const proposalStarted = Date.now();
  const previewColumnButton = `${frameCategories} button`;
  const previewColumnLabel = await cda.inspect(([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.includes('Preview 1 column'))?.innerText.trim(), [previewColumnButton]);
  assert(previewColumnLabel, 'Preview 1 column action is missing.');
  await userClick(previewColumnButton, 'Preview framed column', { includes: 'Preview 1 column' });
  await cda.wait(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', [], 5000);
  report.proposalMs = Date.now() - proposalStarted;
  report.proposal = await cda.inspect(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0, 1800));
  const applyProposal = '[data-testid="construction-choice-proposal-panel"] button';
  const applyLabel = await cda.inspect(([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Apply columns' && !button.disabled)?.innerText.trim(), [applyProposal]);
  assert.equal(applyLabel, 'Apply columns', 'The proposal did not offer an enabled Apply columns action.');
  await userClick(applyProposal, 'Apply framed column', { name: 'Apply columns' });
  await cda.wait(() => Boolean([...document.querySelectorAll('[data-testid^="construction-column-"]')].find(button => button.innerText.includes('days_to_collection'))), [], 5000);
  const previewButton = 'button';
  const previewLabel = await cda.inspect(([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Preview' && !button.disabled)?.innerText.trim(), [previewButton]);
  assert.equal(previewLabel, 'Preview', 'The enabled Preview action is missing.');
  await userClick(previewButton, 'Preview applied table', { name: 'Preview' });
  await cda.wait(() => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')), [], 5000);
  report.preview = await previewTable();
  const valueIndex = report.preview.headers.findIndex(header => header.toLowerCase().includes('days_to_collection'));
  assert(valueIndex >= 0, 'Framed column is absent from rendered preview');
  const valuedRow = report.preview.rows.find(row => /\d+/.test(row[valueIndex] ?? ''));
  assert(valuedRow, 'No Specimen preview row has a days_to_collection value');
  const specimenId = valuedRow[0];
  const oracleQuery = `FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.payload.specimen.reference == ${JSON.stringify(`Specimen/${specimenId}`)} LIMIT 10 RETURN d.payload.component`;
  const oracleScript = `print(JSON.stringify(db._query(${JSON.stringify(oracleQuery)}).toArray()))`;
  const oracleOutput = execFileSync('rtk', ['proxy', 'docker', 'exec', cda.target.arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', oracleScript], { encoding: 'utf8', maxBuffer: 200000 });
  const sourceValues = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('['))).flatMap(components => (components ?? []).filter(component =>
    component.code?.coding?.some(coding => coding.system === 'https://cda.readthedocs.io' && coding.code === 'days_to_collection'),
  ).map(component => component.valueInteger));
  report.sourceComparison = { specimenId, sourceValues, displayed: valuedRow[valueIndex] };
  assert(sourceValues.length > 0 && sourceValues.every(value => report.sourceComparison.displayed.includes(String(value))), 'Framed values differ from raw CDA code/value components');

  await cda.navigate(url);
  await selectDuplicate();
  await cda.wait(([id]) => Boolean(document.querySelector(`[data-testid="saved-frame-${id}"]`)), [frameId], 5000);
  report.persistedFrame = await cda.inspect(([id]) => document.querySelector(`[data-testid="saved-frame-${id}"]`)?.innerText, [frameId]);
  assert(report.persistedFrame.includes('Observation component values'), 'Framing source did not persist after reload');
  const frameRemove = `[data-testid="saved-frame-${frameId}"] button`;
  const removeLabel = await cda.inspect(([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText.trim() === 'Remove')?.innerText.trim(), [frameRemove]);
  assert.equal(removeLabel, 'Remove', 'Saved framing source Remove action is missing.');
  await userClick(frameRemove, 'Remove saved framing source', { name: 'Remove' });
  await cda.wait(([selector]) => Boolean([...document.querySelectorAll(selector)].find(button => button.innerText === 'Apply this change')), [frameRemove], 5000);
  const confirmRemovalLabel = await cda.inspect(([selector]) => [...document.querySelectorAll(selector)].find(button => button.innerText === 'Apply this change')?.innerText, [frameRemove]);
  assert.equal(confirmRemovalLabel, 'Apply this change');
  await userClick(frameRemove, 'Confirm removal of framed columns', { name: 'Apply this change' });
  await cda.wait(([id]) => !document.querySelector(`[data-testid="saved-frame-${id}"]`), [frameId], 5000);
  await cda.navigate(url);
  await selectDuplicate();
  assert.equal(await cda.inspect(() => document.querySelectorAll('[data-testid^="saved-frame-"]').length), 0, 'Framing source remained after reload');
  assert.equal(await cda.inspect(() => [...document.querySelectorAll('[data-testid^="construction-column-"]')].filter(button => button.innerText.includes('days_to_collection')).length), 0, 'Framed column remained after source removal');
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
  report.browserErrors = cda.diagnostics.pageErrors;
  assert.deepEqual(report.browserErrors, [], 'Browser exceptions occurred');
  assert.deepEqual(cda.diagnostics.console, [], 'Unexpected browser console errors occurred');
  assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected application request failures occurred');
  const unexpectedHttpFailures = cda.diagnostics.httpFailures.filter(failure => !report.failedResponses.some(response =>
    response.status === failure.status && response.path === new URL(failure.url).pathname && response.path.endsWith('/construction-capabilities') &&
    (response.code === 'STALE_DRAFT_VERSION' || response.code === 'STALE_DRAFT_DIGEST'),
  ));
  assert.deepEqual(unexpectedHttpFailures, [], 'Unexpected application HTTP failures occurred');
  assert(report.proposalMs < 5000, `Framed column proposal took ${report.proposalMs}ms`);
} catch (error) {
  fatal = error;
  report.failure = { message: error instanceof Error ? error.message : String(error), stack: error instanceof Error ? error.stack : undefined };
} finally {
  if (duplicateId) {
    try {
      cleanupDialogs = true;
      await cda.navigate(url);
      await selectDuplicate();
      await userClick('[data-testid="construction-delete-table"]', 'Delete temporary framed table');
      await cda.wait(([id]) => !document.querySelector(`[data-testid="construction-table-${id}"]`), [duplicateId], 5000);
      report.cleanup = true;
      cleanupDialogs = false;
    } catch (error) {
      cleanupDialogs = false;
      report.cleanupError = String(error);
    }
  }
  cda.includeBrowserDiagnostics();
  report.browserDiagnostics = cda.diagnostics;
  report.screenshots = screenshots;
  await cda.attachReport('framing-lifecycle-domain-report.json');
}
if (fatal) throw fatal;
return report;
}
