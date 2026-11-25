import type { Construction } from '../../../contracts/types';
import { sourceInputMatchesStage } from './constructionStages';
import { stageWithSourceInputs } from './editorShared';
import {
  candidateIntentFor, capabilityFor, codedGroupStepFromForm, expandStepFromForm, groupStepFromForm,
  groupFieldChoicesForStage, groupRowValueChoicesForStage, initialExpandForm, initialGroupForm, listColumnsFor,
  pivotStepFromForm, semanticGroupChoicesForStage, sourceGroupStepFromForm, unpivotStepFromForm,
  type CandidateEvaluation, type ConstructionReshapeEditorEntry, type ConstructionReshapeStep,
  type ExpandForm, type PivotForm, type ReshapeCapabilities, type ReshapeForm, type ReshapeStage,
  type SourceInputChoice, type UnpivotForm,
} from './reshapeForms';

export const pivotInputStage = (stage: ReshapeStage, form: PivotForm, choices: ReadonlyArray<SourceInputChoice>): ReshapeStage =>
  stageWithSourceInputs({ stage, choices, selections: form.pivotSources ?? [], kind: 'PIVOT' });

export const unpivotInputStage = (stage: ReshapeStage, form: UnpivotForm, choices: ReadonlyArray<{ readonly choiceId: string; readonly fieldPath: string; readonly label: string; readonly logicalType: string }>): ReshapeStage =>
  stageWithSourceInputs({ stage, choices, selections: form.unpivotSources ?? [], kind: 'UNPIVOT' });

export const expandInputStage = (stage: ReshapeStage, form: ExpandForm, choices: ReadonlyArray<{ readonly choiceId: string; readonly fieldPath: string; readonly label: string; readonly logicalType: string }>): ReshapeStage => {
  const binding = (form.expandSources ?? []).find((candidate) => candidate.columnId === form.inputColumnId);
  return stageWithSourceInputs({ stage, choices, selections: binding ? [binding] : [], kind: 'EXPAND' });
};

export const candidateFor = (args: {
  readonly form: ReshapeForm;
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly stage: ReshapeStage;
  readonly editingStep?: ConstructionReshapeStep;
  readonly pivotCategoriesKnown: boolean;
}): CandidateEvaluation => {
  const { form, construction, capabilities, stage, editingStep, pivotCategoriesKnown } = args;
  if (form.kind === 'source-group') {
    const choice = capabilities.sourceInput?.choices.find((candidate) => candidate.choiceId === form.choiceId);
    if (!capabilities.sourceInput?.supported || !sourceInputMatchesStage(capabilities.sourceInput.stageId, stage) ||
      !choice?.isPopulated) return { kind: 'incomplete' };
    const step = sourceGroupStepFromForm(form, stage, choice);
    return step ? candidateIntentFor({
      construction, editingStep, step,
      groupSources: [{ rowChoiceId: choice.choiceId, columnId: form.inputColumnId }, ...(form.additionalKeys ?? []).map((key) => ({ rowChoiceId: key.choiceId, columnId: key.inputColumnId }))],
    }) : { kind: 'incomplete' };
  }
  if (form.kind === 'group') {
    if (form.unmatchedSourceKeyOutputIds?.length || form.conflictingRelatedRouteIds?.length) return { kind: 'incomplete' };
    const result = groupStepFromForm({ stage, editingStep, form });
    return result ? candidateIntentFor({ construction, editingStep, step: result.step, ownedSteps: result.codedInput ? [result.codedInput] : [] }) : { kind: 'incomplete' };
  }
  if (form.kind === 'coded-group') {
    const step = codedGroupStepFromForm(stage, form, editingStep);
    return step ? candidateIntentFor({ construction, editingStep, step }) : { kind: 'incomplete' };
  }
  if (form.kind === 'expand') {
    const step = expandStepFromForm({ stage: expandInputStage(stage, form, capabilities.expandSourceInput?.choices ?? []), editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step, expandSources: form.expandSources }) : { kind: 'incomplete' };
  }
  if (form.kind === 'pivot') {
    if (!pivotCategoriesKnown && !form.discovery) return { kind: 'incomplete' };
    const step = pivotStepFromForm({ stage: pivotInputStage(stage, form, capabilities.pivotSourceInput?.choices ?? []), editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step, pivotSources: form.pivotSources }) : { kind: 'incomplete' };
  }
  if (form.kind === 'unpivot') {
    const step = unpivotStepFromForm({ stage: unpivotInputStage(stage, form, capabilities.unpivotSourceInput?.choices ?? []), editingStep, form });
    return step ? candidateIntentFor({ construction, editingStep, step, unpivotSources: form.unpivotSources }) : { kind: 'incomplete' };
  }
  return { kind: 'incomplete' };
};

export const createReshapeEditorEntry = (args: {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly kind: 'group' | 'expand';
  readonly selectedColumns?: ReadonlyArray<string>;
}): ConstructionReshapeEditorEntry | undefined => {
  const stage = args.capabilities.selectedStage;
  const selectedColumnId = args.selectedColumns?.length === 1 ? args.selectedColumns[0] : undefined;
  if (args.kind === 'expand' && (
      !capabilityFor(stage, 'EXPAND').supported ||
      selectedColumnId === undefined ||
      !listColumnsFor(stage).some((column) => column.id === selectedColumnId)
    )) return undefined;
  if (args.kind === 'group' && !capabilityFor(stage, 'GROUP').supported) return undefined;
  const form = args.kind === 'group'
    ? initialGroupForm(stage, args.selectedColumns ?? [], undefined, args.construction, semanticGroupChoicesForStage(args.capabilities, stage), groupFieldChoicesForStage(args.capabilities, stage), groupRowValueChoicesForStage(args.capabilities, stage))
    : initialExpandForm(stage, undefined, selectedColumnId);
  const evaluation = candidateFor({
    form,
    construction: args.construction,
    capabilities: args.capabilities,
    stage,
    pivotCategoriesKnown: false,
  });
  return evaluation.kind === 'ready'
    ? { form, candidateIntent: evaluation.intent }
    : { form };
};
