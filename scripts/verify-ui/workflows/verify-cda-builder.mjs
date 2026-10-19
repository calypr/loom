import { runRelatedSourceChooser } from './verify-cda-builder-related-source-chooser.mjs';
import { runPatientRelatedInspection } from './verify-cda-builder-related-patient-inspection.mjs';
import {
  patientRelatedStepInspectionCases,
  runPatientRelatedApplyReload,
  runPatientRelatedEditRemove,
  runPatientRelatedStepInspection,
} from './verify-cda-builder-patient-related.mjs';
import { runPreviewLimits } from './verify-cda-builder-preview-limits.mjs';
import { runBuilderTableManagement } from './verify-cda-builder-table-management.mjs';
import { tableManagementActions } from '../helpers/verify-cda-builder-table-management-contract.mjs';
import { rowChoiceCases, runRowChoiceInspection } from './verify-cda-builder-row-choice-inspection.mjs';
import { runBuilderColumnPresentation } from './verify-cda-builder-column-presentation.mjs';
import { runBuilderFilterCase } from './verify-cda-builder-filters.mjs';

const patientRelatedInspectionActions = Object.freeze([
  'Inspect Patient field choice',
  'Inspect selected Patient route',
  'Inspect Patient proposal',
]);
const columnPresentationActions = Object.freeze([
  'Toggle source visibility',
  'Verify constructed column rename',
  'Verify constructed column presentation',
  'Inspect columns',
  'Inspect source column controls',
  'Verify source column reorder',
]);
const filterActions = Object.freeze([
  'Apply missing',
  'Missing proposal',
  'Remove saved filter',
  'Edit saved missing filter',
  'Edit saved filter',
  'Apply known filter',
  'Toggle filter flag',
  'Inspect filters',
]);

export const builderCaseActions = Object.freeze([
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
]);

export async function runBuilderCase({ page, cda, action, ...originalArgs }) {
  if (!builderCaseActions.includes(action)) {
    throw new Error(`No native CDA Builder case is registered for ${JSON.stringify(action)}`);
  }
  const args = {
    page,
    cda,
    action,
    explorerId: cda.target.explorer,
    ...originalArgs,
  };
  if (action === 'Verify related source chooser') return runRelatedSourceChooser(args);
  if (patientRelatedInspectionActions.includes(action)) return runPatientRelatedInspection(args);
  if (action === 'Verify Patient related column') return runPatientRelatedApplyReload(args);
  if (action === 'Edit and remove Patient related column') return runPatientRelatedEditRemove(args);
  if (patientRelatedStepInspectionCases.includes(action)) return runPatientRelatedStepInspection(args);
  if (action === 'Preview limits') return runPreviewLimits(args);
  if (tableManagementActions.has(action)) return runBuilderTableManagement(args);
  if (Object.hasOwn(rowChoiceCases, action)) return runRowChoiceInspection(args);
  if (columnPresentationActions.includes(action)) return runBuilderColumnPresentation(args);
  if (filterActions.includes(action)) return runBuilderFilterCase(args);
  throw new Error(`Native CDA Builder case dispatch is incomplete for ${JSON.stringify(action)}`);
}
