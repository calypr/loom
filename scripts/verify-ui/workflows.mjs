import { browserURL } from './common.mjs';
import { click, evaluate, fill, goto, recordBrowserTiming, waitFor, waitForCDPEvent } from './browser.mjs';
import { recordCheck } from './report.mjs';

const requireCheck = (report, dimension, name, passed, evidence = {}) => {
  recordCheck(report, dimension, name, passed, evidence);
  if (!passed) throw new Error('required UI transition failed: ' + name);
};

const transition = (report, cdp, name, action, after, timeout = 5000, settle) =>
  recordBrowserTiming(report, cdp, { name, action, after, timeout, settle });

const builderReady = "document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph')";

export const createBlankExplorer = async (cdp, target, runID, label, report) => {
  await goto(cdp, browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'));
  await waitFor(cdp, builderReady, 30000);
  const title = 'Verify ' + runID.slice(-10) + ' ' + label;
  await transition(report, cdp, 'open Explorer creation', () => click(cdp, 'summary', { name: 'New explorer' }), "document.querySelector('#new-explorer-name')", 5000);
  await fill(cdp, '#new-explorer-name', title);
  await transition(report, cdp, 'create blank Explorer', () => click(cdp, 'button', { name: 'Create blank' }),
    "document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim() === " + JSON.stringify(title) + " && document.body.innerText.includes('Build your first table')", 10000);
  const explorer = await evaluate(cdp, "document.querySelector('select[aria-label=\"Explorer\"]')?.value || ''");
  requireCheck(report, 'correctness', 'created a fresh Explorer distinct from the bootstrap', Boolean(explorer && explorer !== target.bootstrapExplorerId), { title });
  return { explorer, title };
};

export const addPatientTableRoot = async (cdp, report, tableTitle = 'Patients') => {
  await fill(cdp, '#first-table-name', tableTitle);
  await transition(report, cdp, 'choose Patient row root and create first table', () => click(cdp, 'button', { name: 'Choose Patient rows' }),
    "document.body.innerText.includes('DATASET WORKSPACE') && document.body.innerText.includes(" + JSON.stringify(tableTitle) + ") && !document.body.innerText.includes('Build your first table')", 30000);
};

export const configurePatientColumns = async (cdp, report) => {
  await waitFor(cdp, "document.body.innerText.includes('dev-patient-001') && document.querySelector('[data-testid=construction-action-add-columns]:not(:disabled)')", 30000);
  await click(cdp, 'button', { includes: 'Add columns:' });
  await waitFor(cdp, "document.querySelector('[aria-label=\"Add columns editor\"]')", 10000);
  await click(cdp, 'button', { name: 'Fields and related data' });
  await click(cdp, 'summary', { name: 'Raw FHIR fields (advanced)' });
  await click(cdp, 'input[type=checkbox][aria-label]', { name: 'Select Patient.gender' });
  await click(cdp, 'button', { name: 'Add 1 selected feature' });
  await waitFor(cdp, "[...document.querySelectorAll('button')].some((button)=>button.innerText.trim()==='Apply columns'&&!button.disabled)", 30000);
  await transition(report, cdp, 'apply Gender column and render preview', () => click(cdp, 'button', { name: 'Apply columns' }),
    "document.querySelector('[data-testid=preview-table-scroll] [role=columnheader]') && document.body.innerText.includes('dev-patient-001') && document.querySelector('button[aria-label^=\"Select Gender\"]')", 30000);
  await waitFor(cdp, "document.querySelector('button[aria-label^=\"Select Gender\"]')", 30000);
  await click(cdp, 'button', { name: 'Close operation editor' });
  const controls = await evaluate(cdp, "({id:Boolean(document.querySelector('button[aria-label^=\"Select Patient ID\"]')),gender:Boolean(document.querySelector('button[aria-label^=\"Select Gender\"]'))})");
  requireCheck(report, 'correctness', 'configured Patient ID and Gender controls are present', Boolean(controls.id && controls.gender), controls);
};

export const previewPatientRows = async (cdp, report) => {
  await waitFor(cdp, "document.querySelector('[data-testid=preview-table-scroll] [role=table] [role=row]') && document.body.innerText.includes('dev-patient-001')", 30000);
  const preview = await evaluate(cdp, "(()=>{const table=document.querySelector('[data-testid=preview-table-scroll] [role=table]');const rows=[...(table?.querySelectorAll('[role=row]')||[])];return {headers:[...(rows[0]?.querySelectorAll('[role=columnheader]')||[])].map((cell)=>cell.innerText.trim()),rows:rows.slice(1).map((row)=>[...row.querySelectorAll('[role=cell]')].map((cell)=>cell.innerText.trim()))}})()");
  const ids = preview.rows.map((row) => row[0]).sort();
  requireCheck(report, 'correctness', 'Preview renders both independent fixture Patients', JSON.stringify(ids) === JSON.stringify(['dev-patient-001', 'dev-patient-002']), preview);
  recordCheck(report, 'correctness', 'automatic Preview is visible after authoring', true);
  return preview;
};

export const publishPatientExplorer = async (cdp, report) => {
  const publishedResponse = waitForCDPEvent(cdp, 'Network.responseReceived', (event) => event.response.url.includes('/authoring/v2/publish'), 60000);
  let response;
  await transition(report, cdp, 'publish Explorer', () => click(cdp, 'button', { name: 'Publish' }),
    "[...document.querySelectorAll('button')].some((button)=>button.innerText.trim()==='Publish'&&button.disabled)",
    15000, async () => { response = await publishedResponse; });
  requireCheck(report, 'correctness', 'publish endpoint returned success', response.response.status >= 200 && response.response.status < 300, { status: response.response.status });
  return response;
};

export const verifyPublishedViewer = async (cdp, target, project, explorer, report) => {
  await goto(cdp, browserURL(target, project, explorer, 'viewer'));
  await waitFor(cdp, "document.body.innerText.includes('Published') && document.querySelector('table[aria-label$=\" results\"] tbody tr')", 60000);
  const rows = await evaluate(cdp, "[...document.querySelectorAll('table[aria-label$=\" results\"] tbody tr')].map((row)=>row.innerText.trim())");
  requireCheck(report, 'correctness', 'published Viewer displays real fixture rows', rows.some((row) => row.includes('dev-patient-001')) && rows.some((row) => row.includes('dev-patient-002')), { rowCount: rows.length, rows });
};

export const publishDefaultPatientExplorer = async (cdp, context, report, label = 'viewer') => {
  const { explorer, title } = await createBlankExplorer(cdp, context.target, context.runID, label, report);
  await addPatientTableRoot(cdp, report);
  await configurePatientColumns(cdp, report);
  await previewPatientRows(cdp, report);
  await publishPatientExplorer(cdp, report);
  await verifyPublishedViewer(cdp, context.target, context.target.fixtureProject, explorer, report);
  return { explorer, title };
};
