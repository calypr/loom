import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const origin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const url = `${origin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const report = { url, clicks: [], responses: [], browserErrors: [], previews: [], cleanup: false };
const browser = await launchBrowser('/private/tmp');
let duplicateId;

browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
  report.browserErrors.push(exceptionDetails?.text ?? 'Browser exception');
});
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/authoring/v2/')) {
    report.responses.push({ path: new URL(response.url).pathname, status: response.status });
  }
});

const click = async (selector, label) => {
  await browserEval(browser.cdp, `const button=document.querySelector(${JSON.stringify(selector)});if(!button||button.disabled)throw new Error(${JSON.stringify(`${label} unavailable`)});button.click();return true;`);
  report.clicks.push(label);
};

const tableIds = () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-table-"]')].map(button=>button.dataset.testid.slice('construction-table-'.length));`);
const selectDuplicate = async () => {
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-table-${duplicateId}"]'))`, 30000);
  await click(`[data-testid="construction-table-${duplicateId}"]`, 'Select temporary Specimen copy');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${duplicateId}"]')?.getAttribute('aria-pressed')==='true'`, 30000);
};
const visibleColumns = () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button=>button.offsetParent!==null).map(button=>button.innerText.trim());`);

try {
  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30000);
  const source = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="construction-table-"]')].find(button=>button.innerText.trim().endsWith('Specimen'))?.dataset.testid;`);
  assert(source, 'Source Specimen table is missing');
  await click(`[data-testid="${source}"]`, 'Select source Specimen table');
  const originalIds = await tableIds();
  await click('[data-testid="construction-duplicate-table"]', 'Duplicate Specimen table');
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="construction-table-"]').length>${originalIds.length}`, 30000);
  duplicateId = (await tableIds()).find((id) => !originalIds.includes(id));
  assert(duplicateId, 'Duplicate output ID was not found');
  await navigate(browser.cdp, url);
  await selectDuplicate();
  report.initialColumns = await visibleColumns();
  assert(report.initialColumns.length >= 1, 'Duplicate has no selectable source columns');

  await click('[data-testid="construction-action-add-columns"]', 'Add columns');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[aria-label="Add columns editor"] input[aria-label="Select identifier[]"]')].some(input=>input.parentElement?.parentElement?.innerText.includes('https://cda.readthedocs.io/diagnosis'))`, 30000);
  await browserEval(browser.cdp, `const editor=document.querySelector('[aria-label="Add columns editor"]');const input=[...editor.querySelectorAll('input[aria-label="Select identifier[]"]')].find(input=>input.parentElement?.parentElement?.innerText.includes('https://cda.readthedocs.io/diagnosis'));input.click();return true;`);
  report.clicks.push('Select Condition diagnosis identifier');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[aria-label="Add columns editor"] button')].some(button=>button.innerText.trim()==='Add 1 selected feature'&&!button.disabled)`, 10000);
  await browserEval(browser.cdp, `[...document.querySelectorAll('[aria-label="Add columns editor"] button')].find(button=>button.innerText.trim()==='Add 1 selected feature').click();return true;`);
  report.clicks.push('Choose selected feature');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input=>input.getAttribute('aria-label')?.includes('via Subject then Subject'))`, 30000);
  await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.includes('via Subject then Subject')).click();return true;`);
  report.clicks.push('Choose Specimen to Patient to Condition');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].some(input=>input.getAttribute('aria-label')?.includes('Keep all matching values'))`, 30000);
  await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] input[type="radio"]')].find(input=>input.getAttribute('aria-label')?.includes('Keep all matching values')).click();return true;`);
  report.clicks.push('Keep all matching values');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].some(button=>button.innerText.trim()==='Add 1 column'&&!button.disabled)`, 10000);
  const proposalStart = Date.now();
  await browserEval(browser.cdp, `[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.innerText.trim()==='Add 1 column').click();return true;`);
  report.clicks.push('Preview new column');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('h3')].some(heading=>heading.innerText==='Preview new columns'&&heading.parentElement?.innerText.includes('Apply columns'))`, 30000);
  report.proposalMs = Date.now() - proposalStart;
  report.proposal = await browserEval(browser.cdp, `return [...document.querySelectorAll('h3')].find(heading=>heading.innerText==='Preview new columns')?.parentElement?.innerText;`);
  assert(report.proposal.includes('25 of 25 displayed rows contain a value'), 'Proposal did not render expected CDA coverage');
  assert(report.proposalMs < 5000, `Proposal took ${report.proposalMs} ms`);
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Apply columns'&&!button.disabled).click();return true;`);
  report.clicks.push('Apply columns');
  await new Promise((resolve) => setTimeout(resolve, 800));
  report.afterApplyInspection = await browserEval(browser.cdp, `return {selected:[...document.querySelectorAll('[data-testid^="construction-table-"]')].filter(button=>button.getAttribute('aria-pressed')==='true').map(button=>button.dataset.testid),columns:[...document.querySelectorAll('[data-testid^="construction-column-"]')].map(button=>({testid:button.dataset.testid,text:button.innerText.trim(),visible:button.offsetParent!==null})),alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText)};`);
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button=>button.offsetParent!==null).length===${report.initialColumns.length + 1}`, 30000);
  report.appliedColumns = await visibleColumns();
  assert(report.appliedColumns.some((column) => column.includes('identifier[]')), 'Applied identifier column is not visible');
  await browserEval(browser.cdp, `[...document.querySelectorAll('button')].find(button=>button.innerText.trim()==='Preview'&&!button.disabled).click();return true;`);
  report.clicks.push('Preview applied table');
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]'))`, 30000);
  report.previews.push(await browserEval(browser.cdp, `const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return {headers:[...table.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...table.querySelectorAll('[role="row"]')].slice(1,4).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim()))};`));
  const rendered = report.previews.at(-1);
  const identifierIndex = rendered.headers.findIndex((header) => header.toLowerCase().includes('identifier[]'));
  assert(identifierIndex >= 0, 'Rendered table has no Condition identifier header');
  assert(rendered.rows[0]?.[identifierIndex]?.includes('CGCI-BLGSP.BLGSP-71-06-00169.BLGSP-71-06-00169_diagnosis'), 'Rendered first identifier differs from the CDA Condition source');

  await navigate(browser.cdp, url);
  await selectDuplicate();
  assert((await visibleColumns()).some((column) => column.includes('identifier[]')), 'Identifier column disappeared on reload');
  await click('button:not(:disabled)[data-testid="construction-action-add-columns"]', 'Reopen Add columns after reload');
  await click('[data-testid="construction-close-operation-editor"]', 'Close Add columns');
  const removal = await browserEval(browser.cdp, `return [...document.querySelectorAll('button[aria-label^="Remove "]')].find(button=>button.getAttribute('aria-label')?.includes('identifier[]'))?.getAttribute('aria-label');`);
  assert(removal, 'Saved identifier column has no removal control');
  await click(`button[aria-label="${removal}"]`, 'Remove identifier column');
  await waitForBrowser(browser.cdp, `[...document.querySelectorAll('[data-testid^="construction-column-"]:not([data-testid="construction-column-selection"])')].filter(button=>button.offsetParent!==null).length===${report.initialColumns.length}`, 30000);
  await navigate(browser.cdp, url);
  await selectDuplicate();
  report.restoredColumns = await visibleColumns();
  assert.deepEqual(report.restoredColumns, report.initialColumns, 'Identifier column remained after removal and reload');
  assert(report.responses.every((response) => response.status < 400), 'An authoring request failed');
  assert.deepEqual(report.browserErrors, [], 'Browser exceptions occurred');
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
  await writeFile(join(evidenceDirectory, 'identifier-multiplicity-lifecycle.json'), JSON.stringify(report, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, clicks: report.clicks.length, proposalMs: report.proposalMs, cleanup: report.cleanup, errors: report.browserErrors, cleanupError: report.cleanupError }, null, 2));
}
