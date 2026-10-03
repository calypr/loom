import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const origin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const url = `${origin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const report = { url, clicks: [], responses: [], browserErrors: [], cleanup: false };
const browser = await launchBrowser('/private/tmp');
const screenshots = [];
let duplicateId;
await browser.cdp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });

browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => report.browserErrors.push(exceptionDetails?.exception?.description ?? exceptionDetails?.text ?? 'Browser exception'));
const requestStartedAt = new Map();
const commandRequests = [];
browser.cdp.on('Network.requestWillBeSent', (event) => {
  if (event.request.url.includes('/authoring/v2/')) requestStartedAt.set(event.requestId, Date.now());
  if (event.request.url.includes('/authoring/v2/commands')) commandRequests.push({ requestId: event.requestId, postData: event.request.postData });
});
browser.cdp.on('Network.responseReceived', (event) => {
  if (event.response.url.includes('/authoring/v2/')) {
    report.responses.push({ requestId: event.requestId, path: new URL(event.response.url).pathname, status: event.response.status,
      elapsedMs: Date.now() - (requestStartedAt.get(event.requestId) ?? Date.now()) });
  }
});

const click = async (expression, label) => {
  await browserEval(browser.cdp, `${expression};return true;`);
  report.clicks.push(label);
};
const tableIds = () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button=>button.dataset.testid.slice('construction-table-'.length));`);
const selectDuplicate = async () => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-table-${duplicateId}"]'))`, 30000);
  await click(`document.querySelector('[data-testid="construction-table-${duplicateId}"]').click()`, 'Select temporary Specimen copy');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${duplicateId}"]')?.getAttribute('aria-pressed')==='true'`, 30000);
};
const previewTable = () => browserEval(browser.cdp, `const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {headers:[...table.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...table.querySelectorAll('[role="row"]')].slice(1,6).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim()))};`);

try {
  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  const sourceTestId = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-table-"]')].find(button=>button.innerText.trim().endsWith('Specimen'))?.dataset.testid;`);
  assert(sourceTestId, 'Specimen source table is missing');
  await click(`document.querySelector('[data-testid="${sourceTestId}"]').click()`, 'Select Specimen table');
  const originalIds = await tableIds();
  await click(`document.querySelector('[data-testid="construction-duplicate-table"]').click()`, 'Duplicate Specimen table');
  await new Promise((resolve) => setTimeout(resolve, 900));
  report.commandRequests = commandRequests;
  report.afterDuplicate = await browserEval(browser.cdp, `return {text:document.body.innerText.slice(0,1800),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`);
  const failedCommand = report.responses.find((response) => response.path.endsWith('/commands') && response.status >= 400);
  if (failedCommand) {
    report.failedCommandBody = await browser.cdp.send('Network.getResponseBody', { requestId: failedCommand.requestId });
    throw new Error(`Duplicate command failed: ${JSON.stringify(report.failedCommandBody)}`);
  }
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-table-"]').length>${originalIds.length}`, 30000);
  duplicateId = (await tableIds()).find((id) => !originalIds.includes(id));
  assert(duplicateId, 'Temporary output was not created');
  await navigate(browser.cdp, url);
  await selectDuplicate();

  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="frame-source-panel"] input[aria-label="Search framing sources"]'))`, 30000);
  await browserEval(browser.cdp, `document.querySelector('[data-testid="frame-source-panel"]').scrollIntoView({block:'start'});return true;`);
  await browserEval(browser.cdp, `const input=document.querySelector('[data-testid="frame-source-panel"] input[aria-label="Search framing sources"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'days_to_collection');input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
  await click(`document.querySelector('[data-testid="frame-source-panel"] form button').click()`, 'Search framing sources for days_to_collection');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid="frame-source-panel"] select[aria-label^="Relationship path for Observation component values (integer)"] option')].find(option=>option.innerText.includes('via specimen')))`, 30000);
  report.defaultPath = await browserEval(browser.cdp, `return document.querySelector('[data-testid="frame-source-panel"] select[aria-label^="Relationship path for Observation component values (integer)"]').selectedOptions[0]?.textContent;`);
  assert(report.defaultPath.includes('via specimen'), `The source picker defaulted to ${report.defaultPath}`);
  await browserEval(browser.cdp, `document.querySelector('[data-testid="frame-source-panel"]').scrollIntoView({block:'start'});return true;`);
  screenshots.push({ file: 'framing-source-picker.png', data: (await browser.cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })).data });
  report.sourceOptions = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].slice(0,5).map(button=>button.parentElement.innerText);`);
  await browserEval(browser.cdp, `const selector=document.querySelector('[data-testid="frame-source-panel"] select[aria-label^="Relationship path for Observation component values (integer)"]');const option=[...selector.options].find(option=>option.textContent.includes('via specimen'));Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(selector,option.value);selector.dispatchEvent(new Event('change',{bubbles:true}));return true;`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].find(button=>button.parentElement?.querySelector('select[aria-label^="Relationship path for Observation component values (integer)"]')?.selectedOptions[0]?.textContent.includes('via specimen')))`, 5000);
  const sourceStarted = Date.now();
  await click(`[...document.querySelectorAll('[data-testid^="frame-source-choice-"]')].find(button=>button.parentElement?.querySelector('select[aria-label^="Relationship path for Observation component values (integer)"]')).click()`, 'Choose Observation values via Specimen reference');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid^="saved-frame-"]'))`, 30000);
  report.saveSourceMs = Date.now() - sourceStarted;
  const frameId = await browserEval(browser.cdp, `return document.querySelector('[data-testid^="saved-frame-"]')?.dataset.testid.slice('saved-frame-'.length);`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="frame-categories-${frameId}"] input[type="search"]'))`, 30000);
  await browserEval(browser.cdp, `document.querySelector('[data-testid="saved-frame-${frameId}"]').scrollIntoView({block:'start'});return true;`);
  screenshots.push({ file: 'framing-categories.png', data: (await browser.cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })).data });
  await browserEval(browser.cdp, `const input=document.querySelector('[data-testid="frame-categories-${frameId}"] input[type="search"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'days_to_collection');input.dispatchEvent(new Event('input',{bubbles:true}));return true;`);
  await click(`document.querySelector('[data-testid="frame-categories-${frameId}"] form button').click()`, 'Search values in chosen frame');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="frame-categories-${frameId}"] input[aria-label="Select days_to_collection"]:not(:disabled)'))`, 30000);
  await click(`document.querySelector('[data-testid="frame-categories-${frameId}"] input[aria-label="Select days_to_collection"]').click()`, 'Select days_to_collection');
  const proposalStarted = Date.now();
  await click(`[...document.querySelectorAll('[data-testid="frame-categories-${frameId}"] button')].find(button=>button.innerText.includes('Preview 1 column')).click()`, 'Preview framed column');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.getAttribute('data-proposal-status')==='ready'`, 30000);
  report.proposalMs = Date.now() - proposalStarted;
  report.proposal = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0,1800);`);
  await click(`[...document.querySelectorAll('[data-testid="construction-choice-proposal-panel"] button')].find(button=>button.innerText.trim()==='Apply columns'&&!button.disabled).click()`, 'Apply framed column');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid^="construction-column-"]')].find(button=>button.innerText.includes('days_to_collection')))`, 30000);
  await click(`[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Preview'&&!button.disabled).click()`, 'Preview applied table');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
  report.preview = await previewTable();
  const valueIndex = report.preview.headers.findIndex((header) => header.toLowerCase().includes('days_to_collection'));
  assert(valueIndex >= 0, 'Framed column is absent from rendered preview');
  const valuedRow = report.preview.rows.find((row) => /\d+/.test(row[valueIndex] ?? ''));
  assert(valuedRow, 'No Specimen preview row has a days_to_collection value');
  const specimenId = valuedRow[0];
  const oracleQuery = `FOR d IN Observation FILTER d.project == "loom_dev_cda_fhir" AND d.dataset_generation == "cda-fhir-v1" AND d.payload.specimen.reference == ${JSON.stringify(`Specimen/${specimenId}`)} LIMIT 10 RETURN d.payload.component`;
  const oracleScript = `print(JSON.stringify(db._query(${JSON.stringify(oracleQuery)}).toArray()))`;
  const oracleOutput = execFileSync('rtk', ['docker', 'exec', 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', oracleScript], { encoding: 'utf8', maxBuffer: 200000 });
  const sourceValues = JSON.parse(oracleOutput.slice(oracleOutput.indexOf('['))).flatMap((components) => (components ?? []).filter((component) =>
    component.code?.coding?.some((coding) => coding.system === 'https://cda.readthedocs.io' && coding.code === 'days_to_collection'),
  ).map((component) => component.valueInteger));
  report.sourceComparison = { specimenId, sourceValues, displayed: valuedRow[valueIndex] };
  assert(sourceValues.length > 0 && sourceValues.every((value) => report.sourceComparison.displayed.includes(String(value))), 'Framed values differ from raw CDA code/value components');

  await navigate(browser.cdp, url);
  await selectDuplicate();
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="saved-frame-${frameId}"]'))`, 30000);
  report.persistedFrame = await browserEval(browser.cdp, `return document.querySelector('[data-testid="saved-frame-${frameId}"]')?.innerText;`);
  assert(report.persistedFrame.includes('Observation component values'), 'Framing source did not persist after reload');
  await click(`[...document.querySelectorAll('[data-testid="saved-frame-${frameId}"] button')].find(button=>button.innerText==='Remove').click()`, 'Remove saved framing source');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('[data-testid="saved-frame-${frameId}"] button')].find(button=>button.innerText==='Apply this change'))`, 30000);
  await click(`[...document.querySelectorAll('[data-testid="saved-frame-${frameId}"] button')].find(button=>button.innerText==='Apply this change').click()`, 'Confirm removal of framed columns');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="saved-frame-${frameId}"]')`, 30000);
  await navigate(browser.cdp, url);
  await selectDuplicate();
  assert.equal(await browserEval(browser.cdp, `return document.querySelectorAll('[data-testid^="saved-frame-"]').length;`), 0, 'Framing source remained after reload');
  assert.equal((await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-column-"]')].filter(button=>button.innerText.includes('days_to_collection')).length;`)), 0, 'Framed column remained after source removal');
  report.failedResponses = [];
  for (const response of report.responses.filter((item) => item.status >= 400)) {
    const payload = await browser.cdp.send('Network.getResponseBody', { requestId: response.requestId });
    const code = JSON.parse(payload.body).error?.code;
    report.failedResponses.push({ ...response, code });
  }
  assert(report.failedResponses.every((response) => response.status === 409 &&
    response.path.endsWith('/construction-capabilities') &&
    (response.code === 'STALE_DRAFT_VERSION' || response.code === 'STALE_DRAFT_DIGEST')),
  `Unexpected Builder error response: ${JSON.stringify(report.failedResponses)}`);
  assert.deepEqual(report.browserErrors, [], 'Browser exceptions occurred');
  assert(report.proposalMs < 5000, `Framed column proposal took ${report.proposalMs}ms`);
} finally {
  if (duplicateId) {
    try {
      await navigate(browser.cdp, url);
      await selectDuplicate();
      await browserEval(browser.cdp, `window.confirm=()=>true;document.querySelector('[data-testid="construction-delete-table"]').click();return true;`);
      await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-table-${duplicateId}"]')`, 30000);
      report.cleanup = true;
    } catch (error) {
      report.cleanupError = String(error);
    }
  }
  await mkdir(evidenceDirectory, { recursive: true });
  for (const screenshot of screenshots) await writeFile(join(evidenceDirectory, screenshot.file), Buffer.from(screenshot.data, 'base64'));
  await writeFile(join(evidenceDirectory, 'framing-lifecycle.json'), JSON.stringify(report, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, clicks: report.clicks.length, proposalMs: report.proposalMs,
    saveSourceMs: report.saveSourceMs, cleanup: report.cleanup, cleanupError: report.cleanupError,
    failedResponses: report.responses.filter((response) => response.status >= 400), browserErrors: report.browserErrors }, null, 2));
}
