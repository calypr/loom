import { executeScenario, runBrowserCase } from './common.mjs';
import { click, evaluate, reload, inspectAction, captureDOM, waitFor } from './browser.mjs';
import { isActionable, recordCheck, recordUntested } from './report.mjs';
import { addPatientTableRoot, configurePatientColumns, createBlankExplorer, previewPatientRows, publishPatientExplorer } from './workflows.mjs';

const runSuggestions = (context) => runBrowserCase(context, 'builder-authoring', 'suggestions', async ({ cdp, report }) => {
  const { explorer } = await createBlankExplorer(cdp, context.target, context.runID, 'suggestions', report);
  await addPatientTableRoot(cdp, report, 'Patients');
  await waitFor(cdp, "document.body.innerText.includes('dev-patient-001') && document.querySelector('[data-testid=construction-action-add-columns]:not(:disabled)')", 30000);
  await click(cdp, 'button', { includes: 'Add columns:' });
  await waitFor(cdp, "document.querySelector('[aria-label=\"Add columns editor\"]')", 10000);
  await click(cdp, 'button', { name: 'Fields and related data' });
  await waitFor(cdp, "document.querySelector('#feature-catalog-search')", 10000);
  await captureDOM(report, cdp, 'add-columns-editor');
  const candidateCount = await evaluate(cdp, "document.querySelectorAll('[aria-label=\"Add columns editor\"] input[aria-label^=\"Select Patient.\"]').length");
  await click(cdp, 'summary', { name: 'Raw FHIR fields (advanced)' });
  await waitFor(cdp, "document.querySelector('input[aria-label=\"Select Patient.id\"]')", 10000);
  await click(cdp, 'input[type=checkbox][aria-label]', { name: 'Select Patient.id' });
  const idCandidate = await inspectAction(cdp, 'input[type=checkbox][aria-label]', { name: 'Select Patient.id' });
  recordCheck(report, 'correctness', 'catalog-backed Patient candidates are rendered', Number(candidateCount) > 0, { candidateCount });
  recordCheck(report, 'usability', 'Patient ID candidate control is visible and actionable', isActionable(idCandidate), idCandidate ?? {});
  recordUntested(report, 'usability', 'lazy suggestion request failure and recovery', 'The isolated development catalog already includes Patient candidates, so ensureSuggestions returns without issuing a suggestions request.');
  report.target.explorer = explorer;
});

const runAuthoring = (context) => runBrowserCase(context, 'builder-authoring', 'authoring', async ({ cdp, report }) => {
  const { explorer, title } = await createBlankExplorer(cdp, context.target, context.runID, 'authoring', report);
  await addPatientTableRoot(cdp, report);
  await configurePatientColumns(cdp, report);
  await previewPatientRows(cdp, report);
  await publishPatientExplorer(cdp, report);
  await reload(cdp, "document.body.innerText.includes('DATASET WORKSPACE') && document.querySelector('button[aria-label^=\"Select Patient ID\"]') && document.querySelector('button[aria-label^=\"Select Gender\"]')");
  const restored = await evaluate(cdp, "({explorer:document.querySelector('select[aria-label=\"Explorer\"]')?.value,title:document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim(),id:Boolean(document.querySelector('button[aria-label^=\"Select Patient ID\"]')),gender:Boolean(document.querySelector('button[aria-label^=\"Select Gender\"]'))})");
  recordCheck(report, 'persistence', 'published Builder table and configured fields survive reload', restored.explorer === explorer && restored.title === title && Boolean(restored.id && restored.gender), restored);
  report.target.explorer = explorer;
  report.target.table = 'Patients';
});

export const runBuilderAuthoring = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) reports.push(await (caseName === 'suggestions' ? runSuggestions(context) : runAuthoring(context)));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-authoring', argv: process.argv.slice(2), runner: runBuilderAuthoring, mutating: true });
}
