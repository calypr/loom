import { join } from 'node:path';
import { runRelatedSourceChooser } from './verify-cda-builder-related-source-chooser.mjs';
import { runPatientRelatedInspection } from './verify-cda-builder-related-patient-inspection.mjs';
import { patientRelatedStepInspectionCases, runPatientRelatedApplyReload, runPatientRelatedEditRemove, runPatientRelatedStepInspection } from './verify-cda-builder-patient-related.mjs';
import { runPreviewLimits } from './verify-cda-builder-preview-limits.mjs';
import { runBuilderTableManagement } from './verify-cda-builder-table-management.mjs';
import { tableManagementActions } from './verify-cda-builder-table-management-contract.mjs';
import { rowChoiceCases, runRowChoiceInspection } from './verify-cda-builder-row-choice-inspection.mjs';
import { runBuilderColumnPresentation } from './verify-cda-builder-column-presentation.mjs';
import { runBuilderFilterCase } from './verify-cda-builder-filters.mjs';

const action = process.argv[2] ?? 'Keep rows';
const patientRelatedInspectionActions = new Set(['Inspect Patient field choice', 'Inspect selected Patient route', 'Inspect Patient proposal']);
const patientRelatedApplyReloadAction = action === 'Verify Patient related column';
const patientRelatedEditRemoveAction = action === 'Edit and remove Patient related column';
const previewLimitsAction = action === 'Preview limits';
const tableManagementAction = tableManagementActions.has(action);
const rowChoiceAction = Object.hasOwn(rowChoiceCases, action);
const columnPresentationActions = new Set(['Toggle source visibility', 'Verify constructed column rename', 'Verify constructed column presentation', 'Inspect columns', 'Inspect source column controls', 'Verify source column reorder']);
const columnPresentationAction = columnPresentationActions.has(action);
const filterActions = new Set(['Apply missing', 'Missing proposal', 'Remove saved filter', 'Edit saved missing filter', 'Edit saved filter', 'Apply known filter', 'Toggle filter flag', 'Inspect filters']);
const filterAction = filterActions.has(action);
const availableCases = [
  'Verify related source chooser',
  ...patientRelatedInspectionActions,
  'Verify Patient related column',
  'Edit and remove Patient related column',
  ...patientRelatedStepInspectionCases,
  'Preview limits',
  ...tableManagementActions,
  ...Object.keys(rowChoiceCases),
  ...columnPresentationActions,
  ...filterActions,
];

if (!availableCases.includes(action)) {
  throw new Error(
    `Unsupported CDA Builder verifier case: "${action}". `
    + `Available migrated Playwright cases: ${[...new Set(availableCases)].join(', ')}. `
    + 'This verifier case has not been migrated; that coverage gap does not mean the product action is unsupported.',
  );
}

const explorerId = process.argv[3];
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const report = action === 'Verify related source chooser'
  ? await runRelatedSourceChooser({ explorerId })
  : patientRelatedApplyReloadAction
    ? await runPatientRelatedApplyReload({ explorerId })
    : patientRelatedEditRemoveAction
      ? await runPatientRelatedEditRemove({ explorerId })
      : patientRelatedStepInspectionCases.includes(action)
        ? await runPatientRelatedStepInspection({ action, explorerId })
        : previewLimitsAction
          ? await runPreviewLimits({ explorerId })
          : tableManagementAction
            ? await runBuilderTableManagement({ action, explorerId, secondExplorerId: process.argv[4], expectedSecondExplorerTable: process.argv[5] })
            : rowChoiceAction
              ? await runRowChoiceInspection({ action, explorerId })
              : columnPresentationAction
                ? await runBuilderColumnPresentation({ action, explorerId })
                : filterAction
                  ? await runBuilderFilterCase({ action, explorerId })
                  : await runPatientRelatedInspection({ action, explorerId });

console.log(JSON.stringify({
  action,
  status: report.status,
  report: join(report.evidenceDirectory, 'report.json'),
  evidenceDirectory: report.evidenceDirectory ?? evidenceDirectory,
  assertions: report.assertions,
  lifecycle: report.lifecycle,
}, null, 2));
