import type { CodedPivotContext } from './codedPivotContext';
import { fieldPickerList, fieldPickerToggleRow } from '../components/fieldPickerStyles';
import { createOpaqueId, normalizedName, uniqueName, editorContextKey, stageWithSourceInputs } from '../model/editorShared';
import React, { useCallback, useId, useReducer, useRef, useState } from 'react';
import { useQuery } from '../../../react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionTableScalar,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../contracts/types';
import { RelatedExpandEditor } from './RelatedExpandEditor';
import type { RelatedExpandQueryOwner } from '../session/commandQueue';
import { CodedPivotEditor } from './CodedPivotEditor';
import { GroupEditor } from './GroupEditor';
import { PivotEditor, type PivotListSelection } from './PivotEditor';
import { ListColumnExpandChoices } from './ListColumnExpandChoices';
import { classifySourcePickerEmptiness, sourcePickerBoundSet } from '../model/sourcePickerExplanations';
import { existingColumnForFieldPath } from '../model/existingFieldReuse';
import { SourcePickerExplanation } from '../components/SourcePickerExplanation';
import {
  constructionInputStageFor,
  isSourceProjectionStage,
  sourceInputMatchesStage,
} from '../model/constructionStages';
import {
  allDiscoveredPivotCategoriesSelected,
  candidateIntentFor,
  capabilityFor,
  codedGroupStepFromForm,
  codingPathLabel,
  convertScalarKind,
  createPivotCategoryForm,
  defaultKeyForColumn,
  expandOutputsFor,
  expandStepFromForm,
  formForStep,
  groupFieldChoicesForStage,
  groupRowValueChoicesForStage,
  groupStepFromForm,
  initialExpandForm,
  initialGroupForm,
  initialPivotForm,
  initialSourceGroupForm,
  initialUnpivotForm,
  isListColumn,
  listColumnsFor,
  outputNamesAreValid,
  pivotCategoriesAreKnown,
  pivotCategoryMemoryKey,
  pivotDiscoveryMatches,
  pivotDiscoveryRequestIdentity,
  pivotOriginalPairApplies,
  pivotSelectionIdentity,
  pivotStepFromForm,
  scalarColumnsFor,
  scalarFromText,
  scalarIdentity,
  scalarKindFromInput,
  scalarLabel,
  semanticGroupChoicesForStage,
  sourceGroupStepFromForm,
  unpivotOutputsFor,
  unpivotStepFromForm,
  type AvailableSourceField,
  type CandidateEvaluation,
  type CandidateIntent,
  type CodedGroupForm,
  type CompletePivotDiscovery,
  type ConstructionReshapeEditorEntry,
  type ConstructionReshapePivotCategory,
  type ConstructionReshapePivotDiscovery,
  type ConstructionReshapePivotDiscoveryRequest,
  type ConstructionReshapeStep,
  type EmptyListPolicy,
  type ExpandForm,
  type PivotForm,
  type ReshapeCapabilities,
  type ReshapeColumn,
  type ReshapeEntryKind,
  type ReshapeForm,
  type ReshapeStage,
  type SourceGroupForm,
  type SourceInputChoice,
  type UnpivotForm,
  type UnpivotInputForm,
} from '../model/reshapeForms';
import {
  initialPivotDiscoveryState,
  pivotDiscoveryReducer,
  transitionPivotDiscovery,
  type PendingPivotDiscovery,
  type PivotDiscoverySnapshot,
  type ScheduledPivotDiscovery,
} from '../model/pivotDiscoveryReducer';

export {
  publicScalarListColumnIds,
} from '../model/reshapeForms';
export type {
  CompletePivotDiscovery,
  ConstructionReshapeEditorEntry,
  ConstructionReshapePivotDiscovery,
  ConstructionReshapePivotDiscoveryRequest,
  ReshapeEntryKind,
  ReshapeForm,
} from '../model/reshapeForms';

const reshapeFormLabels = {
  group: 'Group rows',
  'source-group': 'Group by source field',
  'coded-group': 'Group by coded value',
  expand: 'Expand repeated values',
  'related-expand': 'One row per related record',
  'coded-pivot': 'Pivot coded values into columns',
  pivot: 'Pivot categories into columns',
  unpivot: 'Unpivot columns into rows',
} satisfies Record<Exclude<ReshapeForm['kind'], 'choose' | 'unsupported'>, string>;

interface ConstructionReshapeEditorProps {
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly editingStep?: ConstructionReshapeStep;
  readonly rowValuesOnly?: boolean;
  readonly initialKind?: ReshapeEntryKind;
  readonly initialEntry?: ConstructionReshapeEditorEntry;
  readonly hideChangeRowOperation?: boolean;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly useExternalColumnSelection?: boolean;
  readonly onFormKindChange?: (kind: ReshapeForm['kind']) => void;
  readonly pivotDiscovery?: ConstructionReshapePivotDiscovery;
  readonly onDiscoverCategories?: (
    request: ConstructionReshapePivotDiscoveryRequest,
    onComplete?: (discovery: CompletePivotDiscovery) => void,
  ) => void;
  readonly disabled: boolean;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
  readonly onEditStep: (stepId: string) => void;
  readonly onCancel?: () => void;
  readonly relatedExpandContext?: {
    readonly project: string;
    readonly explorerId: string;
    readonly authResourcePath?: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly catalog: ExplorerBuilderCatalog;
  };
  readonly relatedExpandQueryOwnerRef?: React.RefObject<RelatedExpandQueryOwner | null>;
  readonly codedPivotContext?: CodedPivotContext;
}

const candidateFor = (args: {
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

export const ConstructionReshapeEditor = (props: ConstructionReshapeEditorProps) => {
  const id = useId();
  const { construction, capabilities, editingStep, disabled, onCandidateChange } = props;
  const stage = capabilities.selectedStage;
  const rootStage = capabilities.stages.find(isSourceProjectionStage) ?? stage;
  const savedGroupInputStageId = editingStep?.operation.kind === 'GROUP'
    ? constructionInputStageFor(construction, editingStep.id)
    : undefined;
  const savedGroupInputStage = savedGroupInputStageId === undefined
    ? undefined
    : capabilities.stages.find((candidate) => candidate.id === savedGroupInputStageId);
  const savedGroupInputStageAvailable = savedGroupInputStageId === undefined ||
    (capabilities.stageId === savedGroupInputStageId && savedGroupInputStage !== undefined);
  const savedOwnedCodedInput = editingStep?.operation.kind === 'GROUP'
    ? construction.steps.find((candidate) => candidate.ownerStepId === editingStep.id
      && candidate.id === savedGroupInputStageId
      && candidate.operation.kind === 'CODED_PIVOT'
      && candidate.inputs[0]?.kind === 'SOURCE_PROJECTION')
    : undefined;
  const groupStage = savedOwnedCodedInput
    ? rootStage
    : savedGroupInputStageAvailable && savedGroupInputStage
      ? savedGroupInputStage
      : stage;
  const sourceInput = capabilities.sourceInput;
  const sourceGroupChoices = sourceInput?.choices ?? [];
  const sourceGroupStageMatches = sourceInputMatchesStage(sourceInput?.stageId, groupStage);
  const sourceGroupHasPopulatedChoice = sourceGroupChoices.some((choice) => choice.isPopulated);
  const sourceGroupSupported = Boolean(sourceInput?.supported && sourceGroupStageMatches && sourceGroupHasPopulatedChoice);
  const availableSourceFields: ReadonlyArray<AvailableSourceField> = sourceGroupSupported
    ? sourceGroupChoices.filter((choice) => choice.isPopulated).map((choice) => ({
      choice,
      examples: props.relatedExpandContext?.catalog.availability?.fields.find((field) =>
        field.resourceType === props.codedPivotContext?.rowRoot && field.path === choice.fieldPath,
      )?.examples.slice(0, 3) ?? [],
    }))
    : [];
  const sourceGroupReason = !sourceGroupStageMatches
    ? 'Source field grouping is available only at the starting source stage.'
    : sourceInput?.supported
      ? sourceGroupHasPopulatedChoice ? '' : 'No source field with recorded values is available for grouping.'
      : sourceInput?.reason ?? 'No source field is available for grouping.';
  const groupCodedPivotCapability = capabilityFor(groupStage, 'CODED_PIVOT');
  const groupCodedValuesSupported = savedGroupInputStageAvailable && Boolean(savedOwnedCodedInput || groupCodedPivotCapability.supported) &&
    isSourceProjectionStage(groupStage);
  const editorIdentity = editorContextKey(capabilities, editingStep, props.initialKind ?? '');
  const contextKey = JSON.stringify([editorIdentity, props.selectedColumns ?? []]);
  const [form, setForm] = useState<ReshapeForm>(() => editingStep
    ? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind)
    : props.initialEntry?.form
      ?? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind));
  const [formContext, setFormContext] = useState({ key: contextKey, editorIdentity });
  const formContextKey = formContext.key;
  const [contractUnavailable, setContractUnavailable] = useState(false);
  const [pivotBeforeSplit, setPivotBeforeSplit] = useState<PivotForm>();
  const [pivotListSelection, setPivotListSelection] = useState<PivotListSelection>();
  const [groupPurpose, setGroupPurpose] = useState<'group' | 'summarize' | undefined>();
  if (formContextKey !== contextKey) {
    const initialForm = !editingStep && form.kind === 'unpivot' && formContext.editorIdentity === editorIdentity
      ? initialUnpivotForm(stage, props.selectedColumns ?? [])
      : editingStep
      ? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind)
      : props.initialEntry?.form
        ?? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind);
    setForm(initialForm);
    setFormContext({ key: contextKey, editorIdentity });
    setPivotBeforeSplit(undefined);
    setPivotListSelection(undefined);
    setContractUnavailable(false);
  }
  const groupSupport = savedGroupInputStageAvailable
    ? capabilityFor(groupStage, 'GROUP')
    : { supported: false, reason: 'The saved Group input stage is not the active capabilities stage. Reload this table before editing.' };
  const codedGroupSupport = capabilityFor(stage, 'CODED_GROUP');
  const expandSupport = capabilityFor(stage, 'EXPAND');
  const relatedExpandSupport = capabilityFor(stage, 'RELATED_EXPAND');
  const savedPivotSupport = capabilityFor(stage, 'PIVOT');
  const pivotSupport = capabilities.pivotSourceInput?.supported ? { supported: true, reason: '' } : savedPivotSupport;
  const codedPivotSupport = capabilityFor(stage, 'CODED_PIVOT');
  const unpivotSupport = capabilityFor(stage, 'UNPIVOT');
  const newPivotSupport = props.onDiscoverCategories
    ? pivotSupport
    : { supported: false, reason: pivotSupport.supported ? 'Stage-scoped category discovery is not available yet.' : pivotSupport.reason };
  const pivotDiscovery = formContextKey === contextKey ? props.pivotDiscovery : undefined;
  const populationForm = form.kind === 'pivot' ? form : undefined;
  const populationStage = populationForm
    ? pivotInputStage(stage, populationForm, capabilities.pivotSourceInput?.choices ?? [])
    : stage;
  const populationColumns = scalarColumnsFor(populationStage).map((column) => column.id);
  const populationIdentity = props.codedPivotContext && form.kind === 'pivot' && !form.pivotSources?.length
    ? JSON.stringify([capabilities.snapshotToken, capabilities.draftVersion, capabilities.draftDigest,
      capabilities.outputId, populationStage.id, populationColumns])
    : undefined;
  const populationQuery = useQuery(async (signal) => {
    const context = props.codedPivotContext;
    if (!context || !populationIdentity) return { identity: '', statuses: {} as Record<string, 'POPULATED' | 'EMPTY' | 'UNKNOWN'> };
    const batches: string[][] = [];
    for (let index = 0; index < populationColumns.length; index += 16) batches.push(populationColumns.slice(index, index + 16));
    const statuses: Record<string, 'POPULATED' | 'EMPTY' | 'UNKNOWN'> = {};
    await Promise.all(batches.map(async (columnIds) => {
      try {
        const response = await context.client.getConstructionColumnPopulation({
          project: context.project,
          explorerId: context.explorerId,
          authResourcePath: context.authResourcePath,
          snapshotToken: capabilities.snapshotToken,
          expectedDraftVersion: capabilities.draftVersion,
          expectedDraftDigest: capabilities.draftDigest,
          outputId: capabilities.outputId,
          stageId: populationStage.id,
          columnIds,
        }, signal);
        if (signal.aborted || response.snapshotToken !== capabilities.snapshotToken || response.draftVersion !== capabilities.draftVersion ||
          response.draftDigest !== capabilities.draftDigest || response.outputId !== capabilities.outputId || response.stageId !== populationStage.id) return;
        for (const column of response.columns) statuses[column.columnId] = column.status;
      } catch {
        if (!signal.aborted) for (const columnId of columnIds) statuses[columnId] = 'UNKNOWN';
      }
    }));
    const current = pivotStateRef.current.snapshot;
    if (Object.values(statuses).includes('EMPTY') && current.form.kind === 'pivot' &&
      current.formContextKey === contextKey && [current.form.categoryColumnId, current.form.valueColumnId]
        .some((columnId) => columnId && statuses[columnId] === 'EMPTY')) {
      const scheduled = pivotStateRef.current.scheduled;
      if (scheduled) {
        window.clearTimeout(scheduled.timer);
        sendPivotAction({ type: 'schedule-canceled', identity: scheduled.identity, revision: scheduled.revision });
      }
      current.onCandidateChange(undefined);
    }
    return { identity: populationIdentity, statuses };
  }, [props.codedPivotContext?.client, populationIdentity], Boolean(populationIdentity));
  const populationStatuses = populationQuery.data?.identity === populationIdentity ? populationQuery.data?.statuses : undefined;
  const populationStatusFor = (columnId: string): 'POPULATED' | 'EMPTY' | 'UNKNOWN' => populationStatuses?.[columnId] ?? 'UNKNOWN';
  const pivotEvaluationContextKey = JSON.stringify([
    groupSupport,
    groupCodedValuesSupported,
    sourceGroupSupported,
    sourceGroupReason,
    codedGroupSupport,
    expandSupport,
    relatedExpandSupport,
    pivotSupport,
    codedPivotSupport,
    unpivotSupport,
  ]);
  const pivotSnapshot: PivotDiscoverySnapshot = {
    contextKey,
    formContextKey,
    owner: props.onDiscoverCategories,
    stageId: stage.id,
    form,
    construction,
    capabilities,
    editingStep,
    stage,
    groupStage,
    groupSupport,
    groupCodedValuesSupported,
    sourceGroupSupport: { supported: sourceGroupSupported, reason: sourceGroupReason },
    codedGroupSupport,
    expandSupport,
    relatedExpandSupport,
    pivotSupport,
    codedPivotSupport,
    unpivotSupport,
    discovery: pivotDiscovery,
    disabled,
    pivotSupported: pivotSupport.supported,
    onCandidateChange,
    onFormKindChange: props.onFormKindChange,
  };
  const [pivotState, dispatchPivotAction] = useReducer(
    pivotDiscoveryReducer,
    pivotSnapshot,
    initialPivotDiscoveryState,
  );
  const pivotStateRef = useRef(pivotState);
  const sendPivotAction = useCallback((action: Parameters<typeof transitionPivotDiscovery>[1]) => {
    const transition = transitionPivotDiscovery(pivotStateRef.current, action);
    if (transition.state !== pivotStateRef.current) {
      pivotStateRef.current = transition.state;
      dispatchPivotAction(action);
    }
    return transition.accepted;
  }, []);

  const pivotDiscoveryRootRef = useCallback((element: HTMLElement | null) => {
    if (!element) {
      const current = pivotStateRef.current;
      if (current.activationTimer !== undefined) window.clearTimeout(current.activationTimer);
      if (current.scheduled) window.clearTimeout(current.scheduled.timer);
      sendPivotAction({ type: 'root-detached' });
      return;
    }

    const machineSnapshot = pivotStateRef.current.snapshot;
    const attachedSnapshot = {
      ...pivotSnapshot,
      form: machineSnapshot.contextKey === pivotSnapshot.contextKey
          && machineSnapshot.formContextKey === pivotSnapshot.formContextKey
        ? machineSnapshot.form
        : pivotSnapshot.form,
    };
    sendPivotAction({ type: 'root-attached', snapshot: attachedSnapshot });
    const initialStateReported = sendPivotAction({
      type: 'initial-state-reported',
      contextKey,
      hasInitialEntry: Boolean(props.initialEntry),
    });
    if (initialStateReported && !props.initialEntry) {
      const initialForm = attachedSnapshot.form;
      props.onFormKindChange?.(initialForm.kind);
      const initialCandidate = (!editingStep
        && (initialForm.kind === 'coded-group' && codedGroupSupport.supported
          || initialForm.kind === 'source-group' && sourceGroupSupported
          || initialForm.kind === 'unpivot' && unpivotSupport.supported))
        || (editingStep !== undefined && initialForm.kind === 'expand' && editingStep.operation.kind === 'EXPAND'
          && (expandSupport.supported || (capabilities.expandSourceInput?.supported ?? false)))
        ? candidateFor({
            form: initialForm,
            construction,
            capabilities,
            stage: groupStage,
            editingStep,
            pivotCategoriesKnown: false,
          })
        : undefined;
      const reportInitialCandidate = editingStep !== undefined || initialForm.kind !== 'group';
      if (initialCandidate?.kind === 'ready') onCandidateChange(initialCandidate.intent);
      else if (reportInitialCandidate) onCandidateChange(undefined);
    }

    const timer = window.setTimeout(() => {
      if (!sendPivotAction({ type: 'activation-fired', contextKey, timer })) return;
      const current = pivotStateRef.current;
      const currentForm = current.snapshot.form;
      if (currentForm.kind !== 'pivot') return;
      const cachedDiscovery = current.snapshot.discovery;
      if (cachedDiscovery?.status === 'complete'
          && !pivotCategoriesAreKnown(currentForm, current.snapshot.stageId, cachedDiscovery)
          && adoptPivotDiscovery(
            pivotDiscoveryRequestIdentity(currentForm, current.snapshot.stageId, current.snapshot.contextKey),
            cachedDiscovery,
          )) return;
      if (!current.snapshot.disabled) schedulePivotDiscovery(currentForm);
    }, 0);
    sendPivotAction({ type: 'activation-scheduled', contextKey, timer });
  }, [
    contextKey,
    formContextKey,
    disabled,
    props.initialEntry,
    props.onDiscoverCategories,
    props.onCandidateChange,
    props.onFormKindChange,
    props.pivotDiscovery,
    props.capabilities,
    construction,
    editingStep,
    groupStage,
    stage,
    stage.id,
    pivotSupport.supported,
    pivotEvaluationContextKey,
  ]);

  const pivotDiscoveryInFlight = ((): boolean => {
    const owner = props.onDiscoverCategories;
    const awaiting = pivotState.awaiting;
    if (!owner || disabled || form.kind !== 'pivot' || !pivotState.mounted) return false;
    if (formContextKey !== contextKey || !pivotSupport.supported) return false;
    if (awaiting?.contextKey !== contextKey
        || awaiting.owner !== owner
        || awaiting.revision !== pivotState.revision
        || awaiting.identity !== pivotDiscoveryRequestIdentity(form, stage.id, contextKey)) return false;
    if (pivotDiscovery === awaiting.seen) return true;
    return !pivotDiscoveryMatches(form, stage.id, pivotDiscovery) || pivotDiscovery.status === 'loading';
  })();

  const capabilityForForm = (next: ReshapeForm, snapshot: PivotDiscoverySnapshot) => {
    switch (next.kind) {
      case 'group': return snapshot.groupCodedValuesSupported ? { supported: true, reason: '' } : snapshot.groupSupport;
      case 'source-group': return snapshot.sourceGroupSupport;
      case 'coded-group': return snapshot.codedGroupSupport;
      case 'expand': return snapshot.expandSupport.supported || (snapshot.capabilities.expandSourceInput?.supported ?? false)
        ? { supported: true, reason: '' }
        : snapshot.expandSupport;
      case 'related-expand': return snapshot.relatedExpandSupport;
      case 'pivot': return snapshot.pivotSupport;
      case 'coded-pivot': return snapshot.codedPivotSupport;
      case 'unpivot': return snapshot.unpivotSupport;
      default: return undefined;
    }
  };

  const evaluate = (
    next: ReshapeForm,
    discovery: ConstructionReshapePivotDiscovery | undefined,
    snapshot: PivotDiscoverySnapshot,
  ): CandidateEvaluation => {
    const support = capabilityForForm(next, snapshot);
    if (!support?.supported) return { kind: 'incomplete' };
    const pivotCategoriesKnown = next.kind === 'pivot' && pivotCategoriesAreKnown(next, snapshot.stage.id, discovery);
    if (
      next.kind === 'pivot'
      && next.unlistedCategoryPolicy === 'ERROR'
      && !next.discovery
      && pivotDiscoveryMatches(next, snapshot.stage.id, discovery)
      && discovery.status === 'complete'
      && !allDiscoveredPivotCategoriesSelected(next, snapshot.stage.id, discovery)
    ) return { kind: 'incomplete' };
    return candidateFor({
      form: next,
      construction: snapshot.construction,
      capabilities: snapshot.capabilities,
      stage: next.kind === 'group' ? snapshot.groupStage : snapshot.stage,
      editingStep: snapshot.editingStep,
      pivotCategoriesKnown,
    });
  };

  const autoSelectDiscoveredCategories = (
    pivot: PivotForm,
    discovery: ConstructionReshapePivotDiscovery | undefined,
  ): PivotForm => {
    const current = pivotStateRef.current;
    const snapshot = current.snapshot;
    if (pivot.discovery) return pivot;
    if (snapshot.formContextKey !== snapshot.contextKey
        || pivotOriginalPairApplies(pivot)
        || !pivotDiscoveryMatches(pivot, snapshot.stageId, discovery)
        || discovery.status !== 'complete') return pivot;

    const selectionIdentity = pivotSelectionIdentity(pivot, snapshot.stageId);
    const manualSelectionApplies = current.manuallySelectedPairs.has(selectionIdentity)
      && pivot.categories.length > 0 && pivot.categoriesPair !== undefined;
    if (current.automaticallySelectedPairs.has(selectionIdentity) || manualSelectionApplies) return pivot;

    sendPivotAction({ type: 'automatic-pair-selected', identity: selectionIdentity });
    const savedStep = snapshot.editingStep;
    const savedCategories = savedStep?.operation.kind === 'PIVOT'
      && savedStep.operation.pivot.categoryColumnId === pivot.categoryColumnId
      && savedStep.operation.pivot.valueColumnId === pivot.valueColumnId
      ? initialPivotForm(snapshot.stage, [], savedStep).categories : [];
    const excluded = current.categoryMemory.get(pivotCategoryMemoryKey(pivot, snapshot.stageId, snapshot.contextKey))?.excluded;
    const usedNames = new Set([
      ...snapshot.stage.columns.map((column) => column.name.toLowerCase()),
      ...pivot.categories.map((category) => category.name.toLowerCase()),
      ...savedCategories.map((category) => category.name.toLowerCase()),
    ]);
    const categories = discovery.categories.flatMap((available) => {
      const identity = scalarIdentity(available.key);
      const current = pivot.categories.find((category) => scalarIdentity(category.key) === identity);
      if (!current && excluded?.has(identity)) return [];
      const category = current
        ?? savedCategories.find((saved) => scalarIdentity(saved.key) === identity)
        ?? createPivotCategoryForm(available, usedNames);
      usedNames.add(category.name.toLowerCase());
      return [category];
    });
    return {
      ...pivot,
      categories,
      categoriesPair: { categoryColumnId: pivot.categoryColumnId, valueColumnId: pivot.valueColumnId },
    };
  };

  const updateForm = (
    nextForm: ReshapeForm,
    discovery: ConstructionReshapePivotDiscovery | undefined = pivotStateRef.current.snapshot.discovery,
    reviewingPivotSplit = Boolean(pivotBeforeSplit),
  ) => {
    const current = pivotStateRef.current;
    const previousForm = current.snapshot.form;
    const next = nextForm.kind === 'pivot' ? autoSelectDiscoveredCategories(nextForm, discovery) : nextForm;
    const previousRequestIdentity = previousForm.kind === 'pivot'
      ? pivotDiscoveryRequestIdentity(previousForm, current.snapshot.stageId, current.snapshot.contextKey)
      : undefined;
    const nextRequestIdentity = next.kind === 'pivot'
      ? pivotDiscoveryRequestIdentity(next, current.snapshot.stageId, current.snapshot.contextKey)
      : undefined;
    if (next.kind === 'pivot' && next.discovery && previousForm.kind === 'pivot' && !previousForm.discovery) {
      const scheduled = current.scheduled;
      if (scheduled && scheduled.identity === previousRequestIdentity) {
        window.clearTimeout(scheduled.timer);
        sendPivotAction({ type: 'schedule-canceled', identity: scheduled.identity, revision: scheduled.revision });
      }
    }
    if (previousForm.kind === 'pivot' && next.kind === 'pivot' && previousRequestIdentity !== nextRequestIdentity) {
      sendPivotAction({
        type: 'automatic-pair-forgotten',
        identity: pivotSelectionIdentity(previousForm, current.snapshot.stageId),
      });
    }
    const requestIdentityChanged = previousRequestIdentity !== nextRequestIdentity;
    if (requestIdentityChanged && current.scheduled) {
      window.clearTimeout(current.scheduled.timer);
      sendPivotAction({ type: 'schedule-canceled' });
    }
    sendPivotAction({ type: 'form-changed', form: next, requestIdentityChanged });
    if (next.kind !== 'expand') setPivotBeforeSplit(undefined);
    if (next.kind !== 'pivot' && !(next.kind === 'expand' && reviewingPivotSplit)) setPivotListSelection(undefined);
    setForm(next);
    current.snapshot.onFormKindChange?.(reviewingPivotSplit && next.kind === 'expand' ? 'pivot' : next.kind);
    const evaluation = evaluate(next, discovery, pivotStateRef.current.snapshot);
    setContractUnavailable(evaluation.kind === 'schema-pending');
    current.snapshot.onCandidateChange(evaluation.kind === 'ready' ? evaluation.intent : undefined);
    if (next.kind === 'pivot') schedulePivotDiscovery(next);
  };

  const adoptPivotDiscovery = (
    identity: string,
    discovery: CompletePivotDiscovery,
    expected?: PendingPivotDiscovery,
  ): boolean => {
    const current = pivotStateRef.current;
    const snapshot = current.snapshot;
    const pending = expected ?? (snapshot.owner ? { identity, revision: current.revision, owner: snapshot.owner } : undefined);
    if (!pending || pending.identity !== identity || !sendPivotAction({
      type: 'discovery-arrived',
      pending,
      contextKey: snapshot.contextKey,
      stageId: snapshot.stageId,
      discovery,
    })) return false;
    const latest = pivotStateRef.current.snapshot.form;
    if (latest.kind !== 'pivot') return false;
    updateForm(latest, discovery);
    return true;
  };

  const expandListColumn = (columnId: string, reviewingPivotSplit = false) => {
    if (!expandSupport.supported || !listColumnsFor(stage).some((column) => column.id === columnId)) return;
    updateForm(initialExpandForm(stage, undefined, columnId), undefined, reviewingPivotSplit);
  };

  const chooseSourceField = (choice: SourceInputChoice) => {
    const next = initialSourceGroupForm(construction, [choice, ...sourceGroupChoices.filter((candidate) => candidate.choiceId !== choice.choiceId)]);
    if (next) updateForm(next);
  };

  const rememberManualPivotSelection = (pivot: PivotForm) => {
    if (pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    const current = pivotStateRef.current;
    const { stageId, contextKey: currentContextKey, form: currentForm } = current.snapshot;
    const memoryKey = pivotCategoryMemoryKey(pivot, stageId, currentContextKey);
    const excluded = new Set(current.categoryMemory.get(memoryKey)?.excluded ?? []);
    if (currentForm.kind !== 'pivot') {
      sendPivotAction({
        type: 'manual-pair-selected',
        identity: pivotSelectionIdentity(pivot, stageId),
        memoryKey,
        excluded,
      });
      return;
    }
    const previousSelected = new Set(currentForm.categories.map((category) => scalarIdentity(category.key)));
    const nextSelected = new Set(pivot.categories.map((category) => scalarIdentity(category.key)));
    for (const identity of previousSelected) {
      if (!nextSelected.has(identity)) excluded.add(identity);
    }
    for (const identity of nextSelected) {
      excluded.delete(identity);
    }
    sendPivotAction({
      type: 'manual-pair-selected',
      identity: pivotSelectionIdentity(pivot, stageId),
      memoryKey,
      excluded,
    });
  };

  const forgetPivotPairSelection = (previous: PivotForm, next: PivotForm) => {
    const { stageId, contextKey: currentContextKey } = pivotStateRef.current.snapshot;
    sendPivotAction({
      type: 'pivot-pairs-forgotten',
      identities: [pivotSelectionIdentity(previous, stageId), pivotSelectionIdentity(next, stageId)],
      memoryKeys: [
        pivotCategoryMemoryKey(previous, stageId, currentContextKey),
        pivotCategoryMemoryKey(next, stageId, currentContextKey),
      ],
    });
  };

  const requestPivotCategories = (requestedForm: PivotForm, force = false) => {
    const current = pivotStateRef.current;
    const snapshot = current.snapshot;
    const owner = snapshot.owner;
    const pivot = snapshot.form.kind === 'pivot' ? snapshot.form : requestedForm;
    if (!owner || !current.mounted || snapshot.disabled || snapshot.formContextKey !== snapshot.contextKey
        || !snapshot.pivotSupported || pivot.discovery
        || (pivot.pivotSources?.length && pivot.groupKeyIds.length === 0)
        || pivot.categoryColumnId === '' || pivot.valueColumnId === ''
        || [pivot.categoryColumnId, pivot.valueColumnId].some((columnId) => populationStatusFor(columnId) === 'EMPTY')) return;

    const identity = pivotDiscoveryRequestIdentity(pivot, snapshot.stageId, snapshot.contextKey);
    const revision = current.revision;
    const requested = current.requested;
    if (!force && requested?.identity === identity && requested.revision === revision && requested.owner === owner) return;

    if (current.scheduled) {
      window.clearTimeout(current.scheduled.timer);
      sendPivotAction({ type: 'schedule-canceled', identity: current.scheduled.identity, revision: current.scheduled.revision });
    }
    const pending: PendingPivotDiscovery = { identity, revision, owner };
    if (!sendPivotAction({ type: 'request-started', pending })) return;
    const request: ConstructionReshapePivotDiscoveryRequest = {
      stageId: snapshot.stageId,
      ...(pivot.pivotSources?.length ? {
        pivotStepId: pivot.stepId,
        groupKeyIds: [...pivot.groupKeyIds],
        pivotSources: pivot.pivotSources,
        candidateConstruction: snapshot.construction,
      } : {}),
      categoryColumnId: pivot.categoryColumnId,
      valueColumnId: pivot.valueColumnId,
    };
    owner(request, (discovery) => {
      adoptPivotDiscovery(identity, discovery, pending);
    });
  };

  const schedulePivotDiscovery = (requestedForm: PivotForm) => {
    const current = pivotStateRef.current;
    const snapshot = current.snapshot;
    const owner = snapshot.owner;
    const pivot = snapshot.form.kind === 'pivot' ? snapshot.form : requestedForm;
    if (!owner || !current.mounted || snapshot.disabled || snapshot.formContextKey !== snapshot.contextKey
        || !snapshot.pivotSupported || pivot.discovery
        || (pivot.pivotSources?.length && pivot.groupKeyIds.length === 0)
        || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;

    const identity = pivotDiscoveryRequestIdentity(pivot, snapshot.stageId, snapshot.contextKey);
    if (pivotDiscoveryMatches(pivot, snapshot.stageId, snapshot.discovery) && snapshot.discovery?.status === 'complete') return;
    if (pivotCategoriesAreKnown(pivot, snapshot.stageId, snapshot.discovery)) {
      const scheduled = current.scheduled;
      if (scheduled?.identity === identity) {
        window.clearTimeout(scheduled.timer);
        sendPivotAction({ type: 'schedule-canceled', identity: scheduled.identity, revision: scheduled.revision });
      }
      sendPivotAction({ type: 'awaiting-cleared', identity, revision: current.revision });
      return;
    }
    const revision = current.revision;
    const requested = current.requested;
    if (requested?.identity === identity && requested.revision === revision && requested.owner === owner) return;
    const pending = current.scheduled;
    if (pending?.identity === identity && pending.revision === revision && pending.owner === owner) return;
    if (pending) {
      window.clearTimeout(pending.timer);
      sendPivotAction({ type: 'schedule-canceled', identity: pending.identity, revision: pending.revision });
    }

    const timer = window.setTimeout(() => {
      const scheduled = { identity, revision, owner, timer };
      if (!sendPivotAction({ type: 'schedule-fired', scheduled })) return;
      const latest = pivotStateRef.current.snapshot.form;
      if (latest.kind === 'pivot') requestPivotCategories(latest);
    }, 250);
    const scheduled: ScheduledPivotDiscovery = { identity, revision, owner, timer };
    sendPivotAction({ type: 'schedule-created', scheduled });
  };

  const showReshapeTabs = !editingStep && (form.kind === 'pivot' || form.kind === 'unpivot' || form.kind === 'coded-pivot' ||
    (form.kind === 'expand' && Boolean(pivotBeforeSplit)));

  const backFromPivotSplit = () => {
    if (pivotBeforeSplit && !disabled) updateForm(pivotBeforeSplit);
  };
  useEscapeToClose(Boolean(pivotBeforeSplit || props.onCancel), pivotBeforeSplit ? backFromPivotSplit : props.onCancel ?? (() => {}));

  return (
    <section ref={pivotDiscoveryRootRef} aria-label={props.rowValuesOnly ? 'Add columns from Group contributors' : 'Reshape editor'} data-testid="construction-reshape-editor" className="grid content-start gap-3">

      {form.kind === 'unsupported' ? (
        <p role="status" data-testid="construction-reshape-edit-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{form.message}</p>
      ) : null}
      {form.kind === 'coded-pivot' && !props.codedPivotContext ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Coded source discovery is unavailable for these starting records. Use category and value fields already in this table.</p>
      ) : null}
      {contractUnavailable ? (
        <p role="status" data-testid="construction-reshape-schema-unavailable" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">The construction schema did not accept this reshape, so Loom has not previewed it.</p>
      ) : null}

      {showReshapeTabs ? (
        <div role="tablist" aria-label="Reshape operation" className="flex gap-1 border-b border-slate-200">
          {([['pivot', 'Pivot'], ['unpivot', 'Unpivot']] as const).map(([tab, label]) => (
            <button key={tab} type="button" role="tab" id={`${id}-reshape-tab-${tab}`}
              aria-selected={tab === 'pivot' ? form.kind !== 'unpivot' : form.kind === 'unpivot'}
              data-testid={`construction-reshape-tab-${tab}`}
              disabled={disabled}
              onClick={() => {
                if (tab === 'pivot') setPivotListSelection(undefined);
                updateForm(tab === 'pivot'
                  ? initialPivotForm(stage, props.selectedColumns ?? [])
                  : initialUnpivotForm(stage, props.selectedColumns ?? []));
              }}
              className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium disabled:opacity-50 ${(tab === 'pivot' ? form.kind !== 'unpivot' : form.kind === 'unpivot') ? 'border-blue-700 text-blue-800' : 'border-transparent text-slate-600 hover:text-slate-900'}`}>
              {label}
            </button>
          ))}
        </div>
      ) : null}

      {!showReshapeTabs && !props.rowValuesOnly && form.kind !== 'choose' && form.kind !== 'unsupported' && form.kind !== 'group' && form.kind !== 'related-expand' ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm">
          <h2 className="m-0 font-semibold text-slate-900">{reshapeFormLabels[form.kind]}</h2>
          {!editingStep && !props.hideChangeRowOperation ? <button type="button" disabled={disabled} onClick={() => updateForm(initialPivotForm(stage, props.selectedColumns ?? []))}
            className="font-medium text-blue-800 hover:underline disabled:opacity-50">Change row operation</button> : null}
        </div>
      ) : null}

      {form.kind === 'related-expand' && props.relatedExpandContext ? (
        <RelatedExpandEditor
          key={`${id}:related-expand:${contextKey}`}
          {...props.relatedExpandContext}
          construction={construction}
          capabilities={capabilities}
          step={editingStep?.operation.kind === 'RELATED_EXPAND' ? editingStep as Extract<ConstructionStep, { readonly operation: { readonly kind: 'RELATED_EXPAND' } }> : undefined}
          disabled={disabled || !relatedExpandSupport.supported}
          queryOwnerRef={props.relatedExpandQueryOwnerRef}
          onCandidateChange={onCandidateChange}
        />
      ) : null}

      {form.kind === 'coded-pivot' && props.codedPivotContext ? (
        <CodedPivotEditor
          key={`${id}:coded-pivot:${contextKey}`}
          {...props.codedPivotContext}
          construction={construction}
          editingStep={editingStep}
          disabled={disabled || !codedPivotSupport.supported}
          unavailableReason={!codedPivotSupport.supported ? codedPivotSupport.reason : undefined}
          onCandidateChange={onCandidateChange}
        />
      ) : null}

      {form.kind === 'group' ? (
        <GroupEditor
          key={`${id}:group:${contextKey}`}
          form={form}
          stage={groupStage}
          groupFieldChoices={groupFieldChoicesForStage(capabilities, groupStage)}
          groupRowValueChoices={groupRowValueChoicesForStage(capabilities, groupStage)}
          rowValuesOnly={props.rowValuesOnly}
          purpose={groupPurpose}
          semanticGroupChoices={semanticGroupChoicesForStage(capabilities, groupStage)}
          semanticGroupChoicesIncomplete={capabilities.semanticGroupChoicesIncomplete ?? false}
          semanticGroupSupported={groupSupport.supported}
          codedPivotContext={groupCodedValuesSupported ? props.codedPivotContext : undefined}
          codedPivotSupported={groupCodedValuesSupported}
          supported={groupSupport.supported || groupCodedValuesSupported}
          reason={groupSupport.supported || groupCodedValuesSupported ? '' : groupSupport.reason}
          disabled={disabled}
          expandSupported={groupStage.id === stage.id && expandSupport.supported}
          onExpandListColumn={groupStage.id === stage.id ? expandListColumn : undefined}
          onChange={(next) => updateForm(next)}
        />
      ) : null}

      {form.kind === 'coded-group' ? (
        <CodedGroupEditor
          form={form}
          stage={stage}
          supported={codedGroupSupport.supported}
          reason={codedGroupSupport.reason}
          disabled={disabled}
          onChange={updateForm}
        />
      ) : null}

      {form.kind === 'source-group' ? (
        <SourceGroupEditor
          form={form}
          fields={availableSourceFields}
          supported={sourceGroupSupported}
          reason={sourceGroupReason}
          disabled={disabled}
          canClear={!editingStep}
          onClear={() => updateForm(initialGroupForm(groupStage, props.selectedColumns ?? [], undefined, construction, semanticGroupChoicesForStage(capabilities, groupStage), groupFieldChoicesForStage(capabilities, groupStage), groupRowValueChoicesForStage(capabilities, groupStage)))}
          onChange={updateForm}
        />
      ) : null}

      {form.kind === 'expand' ? (
        <>
          {pivotBeforeSplit ? (
            <section aria-label="Split before pivoting" className="grid gap-2 rounded-md border border-slate-200 bg-slate-50 p-3">
              <h4 className="text-sm font-semibold text-slate-900">Split before pivoting</h4>
              <p className="text-xs text-slate-700">Review and apply the split. Then open Pivot again and choose “{form.outputLabel}” in place of the list. The split is saved as its own step.</p>
              <button type="button" disabled={disabled} className="justify-self-start text-sm font-medium text-blue-800"
                onClick={backFromPivotSplit}>
                Back to pivot fields
              </button>
            </section>
          ) : null}
          <ExpandEditor
            key={`${id}:expand:${contextKey}`}
            form={form}
            lockInput={Boolean(pivotBeforeSplit)}
            stage={expandInputStage(stage, form, capabilities.expandSourceInput?.choices ?? [])}
            sourceInput={capabilities.expandSourceInput}
            sourceProjections={construction.sourceProjections}
            supported={expandSupport.supported || (capabilities.expandSourceInput?.supported ?? false)}
            reason={expandSupport.reason}
            disabled={disabled}
            onChange={(next) => updateForm(next)}
          />
        </>
      ) : null}
      {form.kind === 'pivot' ? (
        <PivotEditor
          key={`${id}:pivot:${contextKey}`}
          form={form}
          stage={pivotInputStage(stage, form, capabilities.pivotSourceInput?.choices ?? [])}
          sourceChoices={capabilities.pivotSourceInput?.choices ?? []}
          supported={pivotSupport.supported}
          reason={pivotSupport.reason}
          disabled={disabled}
          discovery={pivotDiscovery}
          discoveryAwaited={pivotDiscoveryInFlight}
          canDiscover={Boolean(props.onDiscoverCategories)}
          showRowSelection={!props.useExternalColumnSelection}
          populationStatusFor={populationStatusFor}
          populationChecking={populationQuery.isLoading && Boolean(populationIdentity)}
          populationUnavailable={Boolean(populationIdentity) && !populationQuery.isLoading && (
            populationQuery.data?.identity !== populationIdentity || Object.values(populationStatuses ?? {}).some((status) => status === 'UNKNOWN'))}
          pendingList={pivotListSelection}
          onPendingListChange={setPivotListSelection}
          onExpandListColumn={!editingStep && expandSupport.supported &&
            (pivotSupport.supported || savedPivotSupport.reasonCode === 'INSUFFICIENT_SCALAR_COLUMNS') ? (columnId) => {
            setPivotBeforeSplit(form);
            expandListColumn(columnId, true);
          } : undefined}
          onDiscover={() => requestPivotCategories(form, true)}
          onManualCategorySelection={rememberManualPivotSelection}
          onForgetPairSelection={forgetPivotPairSelection}
          onChange={updateForm}
        />
      ) : null}
      {form.kind === 'unpivot' ? (
        <UnpivotEditor
          key={`${id}:unpivot:${contextKey}`}
          form={form}
          stage={unpivotInputStage(stage, form, capabilities.unpivotSourceInput?.choices ?? [])}
          sourceProjections={construction.sourceProjections}
          sourceInput={capabilities.unpivotSourceInput}
          supported={unpivotSupport.supported}
          reason={unpivotSupport.reason}
          disabled={disabled}
          expandSupported={expandSupport.supported}
          showInputSelection={!props.useExternalColumnSelection}
          onExpandListColumn={expandListColumn}
          onChange={updateForm}
        />
      ) : null}
    </section>
  );
};

const ReshapeChoice = (props: {
  readonly testId: string;
  readonly title: string;
  readonly rows: string;
  readonly columns: string;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChoose: () => void;
}) => (
  <button
    type="button"
    data-testid={props.testId}
    aria-label={`${props.title}. ${props.rows} ${props.columns}${props.reason ? ` ${props.reason}` : ''}`}
    disabled={props.disabled || !props.supported}
    onClick={props.onChoose}
    className="grid gap-1 rounded-lg border border-slate-200 px-3 py-2 text-left enabled:hover:border-blue-400 enabled:hover:bg-blue-50 disabled:cursor-not-allowed disabled:bg-slate-50"
  >
    <span className="text-sm font-semibold text-slate-900">{props.title}</span>
    <span className="grid gap-x-3 gap-y-0.5 text-xs text-slate-600 sm:grid-cols-2">
      <span><span className="font-medium text-slate-700">Rows:</span> {props.rows}</span>
      <span><span className="font-medium text-slate-700">Columns:</span> {props.columns}</span>
    </span>
    {!props.supported || props.reason ? <span className="text-xs text-amber-900">{props.reason}</span> : null}
  </button>
);

const SourceFieldChoices = (props: {
  readonly fields: ReadonlyArray<AvailableSourceField>;
  readonly disabled: boolean;
  readonly onChoose: (choice: SourceInputChoice) => void;
}) => props.fields.length > 0 ? (
  <section aria-label="Available dataset fields" data-testid="construction-reshape-source-field-choices" className="grid gap-2 rounded-lg border border-slate-200 bg-slate-50 p-3">
    <h4 className="m-0 text-sm font-semibold text-slate-900">Available dataset fields</h4>
    <ul className="m-0 grid list-none gap-2 p-0 sm:grid-cols-2">
      {props.fields.map((field) => (
        <li key={field.choice.choiceId}>
          <button type="button" aria-label={`Group by ${field.choice.label}`} disabled={props.disabled}
            onClick={() => props.onChoose(field.choice)}
            className="grid w-full gap-1 rounded border border-slate-200 bg-white px-3 py-2 text-left hover:border-blue-400 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50">
            <span className="text-sm font-medium text-slate-900">{field.choice.label}</span>
            <span className="text-xs text-slate-600">{field.choice.logicalType}</span>
            {field.examples.length > 0 ? <span className="text-xs text-slate-600">Examples: {field.examples.join(' · ')}</span> : null}
          </button>
        </li>
      ))}
    </ul>
  </section>
) : null;

const SourceFieldDescription = ({ field }: { readonly field: AvailableSourceField }) => (
  <span className="grid gap-0.5">
    <span className="text-sm font-medium text-slate-900">{field.choice.label}</span>
    <span className="text-xs text-slate-600">{field.choice.logicalType}</span>
    {field.examples.length > 0 ? <span className="text-xs text-slate-600">Examples: {field.examples.join(' · ')}</span> : null}
  </span>
);

const SourceGroupEditor = (props: {
  readonly form: SourceGroupForm;
  readonly fields: ReadonlyArray<AvailableSourceField>;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly canClear: boolean;
  readonly onClear: () => void;
  readonly onChange: (form: SourceGroupForm) => void;
}) => {
  const selectedCount = 1 + (props.form.additionalKeys?.length ?? 0);
  const selected = (choiceId: string) => props.form.choiceId === choiceId || props.form.additionalKeys?.some((key) => key.choiceId === choiceId) === true;
  const toggleChoice = (field: AvailableSourceField, checked: boolean) => {
    const { choice } = field;
    if (checked) {
      if (selected(choice.choiceId)) return;
      const usedNames = new Set([props.form.keyOutput.name, props.form.countOutput.name, ...(props.form.additionalKeys ?? []).map((key) => key.keyOutput.name)]);
      props.onChange({ ...props.form, additionalKeys: [...(props.form.additionalKeys ?? []), {
        choiceId: choice.choiceId,
        inputColumnId: createOpaqueId('group-source'),
        keyOutput: {
          id: createOpaqueId('group-key'),
          name: uniqueName(normalizedName(choice.fieldPath.split('.').at(-1) ?? choice.label), usedNames),
          label: choice.label,
          type: choice.logicalType,
        },
      }] });
      return;
    }
    if (props.form.choiceId === choice.choiceId) {
      const [nextPrimary, ...remainingKeys] = props.form.additionalKeys ?? [];
      if (!nextPrimary) {
        if (props.canClear) props.onClear();
        return;
      }
      props.onChange({
        ...props.form,
        choiceId: nextPrimary.choiceId,
        inputColumnId: nextPrimary.inputColumnId,
        keyOutput: nextPrimary.keyOutput,
        additionalKeys: remainingKeys,
      });
      return;
    }
    props.onChange({ ...props.form, additionalKeys: props.form.additionalKeys?.filter((key) => key.choiceId !== choice.choiceId) });
  };

  return (
    <section aria-label="Group by a source field" data-testid="construction-reshape-source-group" className="grid content-start gap-2">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      <fieldset disabled={props.disabled || !props.supported} className="grid gap-1">
        <legend className="m-0 text-sm font-medium text-slate-800">Available dataset fields</legend>
        {props.fields.map((field) => {
          const isSelected = selected(field.choice.choiceId);
          const cannotClearLast = isSelected && selectedCount === 1 && !props.canClear;
          return (
            <label key={field.choice.choiceId} className={fieldPickerToggleRow}>
              <input type="checkbox" aria-label={`Group by ${field.choice.label}`} checked={isSelected}
                disabled={cannotClearLast}
                onChange={(event) => toggleChoice(field, event.currentTarget.checked)} />
              <SourceFieldDescription field={field} />
            </label>
          );
        })}
        {selectedCount === 1 && !props.canClear ? <p className="m-0 text-xs text-slate-600">Keep at least one grouping field while editing this step.</p> : null}
      </fieldset>
      <p role="status" data-testid="construction-source-group-summary" className="m-0 text-xs text-slate-600">One row per distinct combination of {[props.form.keyOutput.label, ...(props.form.additionalKeys ?? []).map((key) => key.keyOutput.label)].join(' and ')}. Each row retains its contributing records for Add columns.</p>
      <details className="rounded border border-slate-200 p-2 text-sm">
        <summary className="cursor-pointer font-medium text-slate-800">Advanced: missing values</summary>
        <label className="mt-2 grid gap-1 text-slate-700">
          When a source record has no value for this field
          <select
            data-testid="construction-source-group-missing"
            value={props.form.missingKeyPolicy}
            disabled={props.disabled || !props.supported}
            onChange={(event) => {
              const value = event.currentTarget.value;
              if (value === 'GROUP' || value === 'EXCLUDE' || value === 'ERROR') {
                props.onChange({ ...props.form, missingKeyPolicy: value });
              }
            }}
            className="rounded border border-slate-300 bg-white px-2 py-2"
          >
            <option value="GROUP">Count them in a missing-value row</option>
            <option value="EXCLUDE">Leave them out</option>
            <option value="ERROR">Stop if a value is missing</option>
          </select>
        </label>
      </details>
    </section>
  );
};

const CodedGroupEditor = (props: {
  readonly form: CodedGroupForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly onChange: (form: CodedGroupForm) => void;
}) => (
  <section aria-label="Group by coded value" data-testid="construction-reshape-coded-group" className="grid gap-3 rounded-lg border border-slate-200 p-3">
    <div>
      <h4 className="text-sm font-semibold text-slate-900">Group by coded value</h4>
      <p className="mt-1 text-sm text-slate-600">One row per distinct system, version, and code. Repeated copies of a code in one source record count once.</p>
    </div>
    {!props.supported ? <p role="status" className="text-sm text-amber-900">{props.reason}</p> : null}
    <label className="grid gap-1 text-sm font-medium text-slate-800">
      Coding field
      <select
        data-testid="construction-coded-group-path"
        value={props.form.codingPath}
        disabled={props.disabled || !props.supported}
        onChange={(event) => props.onChange({ ...props.form, codingPath: event.target.value })}
        className="rounded border border-slate-300 bg-white px-2 py-2"
      >
        {(props.stage.codedGroupChoices ?? []).map((choice) => (
          <option key={`${choice.occurrenceId}:${choice.codingPath}`} value={choice.codingPath}>{codingPathLabel(choice.codingPath)}</option>
        ))}
      </select>
    </label>
    <p className="text-sm text-slate-600">The result has Code system, Code version, Code, and Source records. Current columns leave this table when you apply the step.</p>
    <details className="rounded border border-slate-200 p-2 text-sm">
      <summary className="cursor-pointer font-medium text-slate-800">Advanced: records without a complete code</summary>
      <label className="mt-2 grid gap-1 text-slate-700">
        When a source record has no code or system
        <select
          data-testid="construction-coded-group-missing"
          value={props.form.missingKeyPolicy}
          disabled={props.disabled || !props.supported}
          onChange={(event) => {
            const value = event.target.value;
            if (value === 'GROUP' || value === 'EXCLUDE' || value === 'ERROR') {
              props.onChange({ ...props.form, missingKeyPolicy: value });
            }
          }}
          className="rounded border border-slate-300 bg-white px-2 py-2"
        >
          <option value="GROUP">Put it in a missing-code row</option>
          <option value="EXCLUDE">Leave it out of this result</option>
          <option value="ERROR">Stop if a code is missing</option>
        </select>
      </label>
    </details>
  </section>
);

const ExpandEditor = (props: {
  readonly form: ExpandForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly lockInput?: boolean;
  readonly sourceInput?: ConstructionCapabilitiesResponse['expandSourceInput'];
  readonly sourceProjections?: Construction['sourceProjections'];
  readonly onChange: (form: ExpandForm) => void;
}) => {
  const columns = listColumnsFor(props.stage);
  const boundChoiceIds = new Set((props.form.expandSources ?? []).map((binding) => binding.choiceId));
  const reusableAlreadyProjectedArray = (choice: { readonly fieldPath: string }): boolean => {
    const existing = existingColumnForFieldPath({
      sourceProjections: props.sourceProjections,
      columns: props.stage.columns,
      fieldPath: choice.fieldPath,
    });
    return existing !== undefined
      && props.stage.columns.some((column) => column.id === existing.columnId && isListColumn(column));
  };
  const allowedSourceChoices = (props.sourceInput?.supported ?? false)
    ? (props.sourceInput?.choices ?? []).filter((choice) => choice.isPopulated
      && !boundChoiceIds.has(choice.choiceId)
      && !reusableAlreadyProjectedArray(choice))
    : [];
  const nameConflict = props.form.ordinal.kind === 'include'
    && props.form.outputName.trim().toLowerCase() === props.form.ordinal.outputName.trim().toLowerCase();
  const outputColumns = expandOutputsFor(props.form, props.stage);
  const outputNamesValid = outputColumns.length > 0 && outputNamesAreValid(outputColumns);
  const input = columns.find((column) => column.id === props.form.inputColumnId);
  const selectedBinding = (props.form.expandSources ?? []).find((binding) => binding.columnId === props.form.inputColumnId);
  const selectedInputValue = selectedBinding
    && (props.sourceInput?.choices ?? []).some((choice) => choice.choiceId === selectedBinding.choiceId)
    ? `source:${selectedBinding.choiceId}`
    : props.form.inputColumnId;
  const updateInput = (value: string) => {
    const previousId = props.form.inputColumnId;
    const keptBindings = (props.form.expandSources ?? []).filter((binding) => binding.columnId !== previousId);
    if (value.startsWith('source:')) {
      const choice = (props.sourceInput?.choices ?? []).find((candidate) => candidate.choiceId === value.slice('source:'.length));
      if (!choice) return;
      const columnId = choice.columnId || createOpaqueId('expand-input');
      if (columnId === props.form.inputColumnId && keptBindings.length === (props.form.expandSources ?? []).length) return;
      const usedNames = new Set(props.stage.columns.map((column) => column.name.toLowerCase()));
      usedNames.delete(normalizedName(columns.find((column) => column.id === previousId)?.name ?? ''));
      const baseName = uniqueName(normalizedName(choice.fieldPath), usedNames);
      props.onChange({
        ...props.form,
        inputColumnId: columnId,
        outputName: uniqueName(`${normalizedName(baseName)}_item`, usedNames),
        outputLabel: `${choice.label} item`,
        expandSources: [...keptBindings, { choiceId: choice.choiceId, columnId }],
      });
      return;
    }
    const column = columns.find((candidate) => candidate.id === value);
    if (!column) return;
    props.onChange({
      ...props.form,
      inputColumnId: column.id,
      outputName: uniqueName(`${normalizedName(column.name)}_item`,
        new Set(props.stage.columns.filter((other) => other.id !== column.id).map((other) => other.name.toLowerCase()))),
      outputLabel: `${column.label} item`,
      ...(keptBindings.length ? { expandSources: keptBindings } : { expandSources: undefined }),
    });
  };

  return (
    <section aria-label="Expand repeated values" data-testid="construction-reshape-expand" className="grid content-start gap-3">
      <p data-testid="construction-reshape-expand-effect" className="m-0 text-sm text-slate-600">
        Each value in {input?.label ?? 'the selected list'} becomes one row for its input record. {props.form.outputLabel.trim() || 'The item column'} contains that value; other fields repeat.
      </p>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 && allowedSourceChoices.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned any list-valued columns for this stage.</p>
      ) : (
        <>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            Repeated field
            <select aria-label="Repeated field" value={selectedInputValue} disabled={props.disabled || !props.supported || props.lockInput} onChange={(event) => updateInput(event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
              <option value="">Choose a repeated field</option>
              {columns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown item type'})</option>)}
              {allowedSourceChoices.length > 0 ? (
                <optgroup label="Signed FHIR array fields">
                  {allowedSourceChoices.map((choice) => (
                    <option key={`source:${choice.choiceId}`} value={`source:${choice.choiceId}`} data-testid={`construction-reshape-expand-source-option-${choice.fieldPath}`}>
                      {choice.label} ({choice.logicalType} items)
                    </option>
                  ))}
                </optgroup>
              ) : null}
            </select>
          </label>
          <p data-testid="construction-reshape-expand-empty-effect" className="m-0 text-sm text-slate-600">
            For an empty or missing list, {props.form.emptyPolicy === 'PRESERVE_PARENT'
              ? 'keep the original row with an empty item.'
              : props.form.emptyPolicy === 'EXCLUDE'
                ? 'leave out the original row.'
                : props.form.emptyPolicy === 'ERROR'
                  ? 'stop the operation with an error.'
                  : 'choose how to handle the original row in Advanced options.'}
          </p>
          <details data-testid="construction-reshape-expand-advanced" className="rounded-lg border border-slate-200">
            <summary className="cursor-pointer px-3 py-2 text-sm font-medium text-slate-700">Advanced options</summary>
            <div className="grid gap-3 p-3 pt-0">
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                Item column label
                <input aria-label="Expanded item label" value={props.form.outputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, outputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
              </label>
              <label className="flex items-start gap-2 rounded border border-slate-200 px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  aria-label="Include item position"
                  checked={props.form.ordinal.kind === 'include'}
                  disabled={props.disabled || !props.supported}
                  onChange={(event) => props.onChange({
                    ...props.form,
                    ordinal: event.currentTarget.checked
                      ? { kind: 'include', outputColumnId: createOpaqueId('expand-ordinal'), outputName: uniqueName(`${normalizedName(input?.name ?? 'item')}_position`, new Set(outputColumns.map((column) => column.name.toLowerCase()))), outputLabel: 'Item position, zero-based' }
                      : { kind: 'none' },
                  })}
                  className="mt-0.5"
                />
                <span><span className="block font-medium text-slate-800">Include item position</span><span className="block text-xs text-slate-500">Add a zero-based position column for each item.</span></span>
              </label>
              {props.form.ordinal.kind === 'include' ? (
                <div className="grid gap-3 rounded border border-slate-200 p-3 sm:grid-cols-2">
                  <label className="grid gap-1 text-sm font-medium text-slate-700">
                    Position column name
                    <input aria-label="Position column name" value={props.form.ordinal.outputName} disabled={props.disabled || !props.supported} onChange={(event) => {
                      const ordinal = props.form.ordinal;
                      if (ordinal.kind === 'include') props.onChange({ ...props.form, ordinal: { ...ordinal, outputName: event.currentTarget.value } });
                    }} className="rounded border border-slate-300 px-2 py-1.5" />
                  </label>
                  <label className="grid gap-1 text-sm font-medium text-slate-700">
                    Position column label
                    <input aria-label="Position column label" value={props.form.ordinal.outputLabel} disabled={props.disabled || !props.supported} onChange={(event) => {
                      const ordinal = props.form.ordinal;
                      if (ordinal.kind === 'include') props.onChange({ ...props.form, ordinal: { ...ordinal, outputLabel: event.currentTarget.value } });
                    }} className="rounded border border-slate-300 px-2 py-1.5" />
                  </label>
                </div>
              ) : null}
              <label className="grid gap-1 text-sm font-medium text-slate-700">
                When a list is empty
                <select aria-label="Empty list policy" value={props.form.emptyPolicy ?? ''} disabled={props.disabled || !props.supported} onChange={(event) => {
                  const policy = emptyListPolicyFromInput(event.currentTarget.value);
                  if (policy) props.onChange({ ...props.form, emptyPolicy: policy });
                }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                  <option value="">Choose what happens</option>
                  <option value="ERROR">Stop with an error</option>
                  <option value="EXCLUDE">Drop the original row</option>
                  <option value="PRESERVE_PARENT">Keep the row with a missing item</option>
                </select>
                <span className="text-xs font-normal text-slate-500">This also applies when the repeated value is missing.</span>
              </label>
            </div>
          </details>
        </>
      )}
      {nameConflict || !outputNamesValid ? (
        <p role="status" className="text-sm text-amber-900">Choose unique output names and add a label for every new column.</p>
      ) : null}
    </section>
  );
};

const emptyListPolicyFromInput = (value: string): EmptyListPolicy | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'EXCLUDE': return 'EXCLUDE';
    case 'PRESERVE_PARENT': return 'PRESERVE_PARENT';
    default: return undefined;
  }
};

const pivotInputStage = (stage: ReshapeStage, form: PivotForm, choices: ReadonlyArray<SourceInputChoice>): ReshapeStage =>
  stageWithSourceInputs({ stage, choices, selections: form.pivotSources ?? [], kind: 'PIVOT' });

const unpivotInputStage = (stage: ReshapeStage, form: UnpivotForm, choices: ReadonlyArray<{ readonly choiceId: string; readonly fieldPath: string; readonly label: string; readonly logicalType: string }>): ReshapeStage =>
  stageWithSourceInputs({ stage, choices, selections: form.unpivotSources ?? [], kind: 'UNPIVOT' });

const expandInputStage = (stage: ReshapeStage, form: ExpandForm, choices: ReadonlyArray<{ readonly choiceId: string; readonly fieldPath: string; readonly label: string; readonly logicalType: string }>): ReshapeStage => {
  const binding = (form.expandSources ?? []).find((candidate) => candidate.columnId === form.inputColumnId);
  return stageWithSourceInputs({ stage, choices, selections: binding ? [binding] : [], kind: 'EXPAND' });
};

const UnpivotEditor = (props: {
  readonly form: UnpivotForm;
  readonly stage: ReshapeStage;
  readonly sourceProjections?: Construction['sourceProjections'];
  readonly sourceInput?: ReshapeCapabilities['unpivotSourceInput'];
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly expandSupported: boolean;
  readonly showInputSelection: boolean;
  readonly onExpandListColumn: (columnId: string) => void;
  readonly onChange: (form: UnpivotForm) => void;
}) => {
  const listColumns = listColumnsFor(props.stage);
  const outputNamesValid = outputNamesAreValid(unpivotOutputsFor(props.form, props.stage));
  const toggleInput = (column: ReshapeColumn, checked: boolean) => {
    const inputs = checked
      ? [...props.form.inputs, { columnId: column.id, key: defaultKeyForColumn(column, props.form.inputs) }]
      : props.form.inputs.filter((input) => input.columnId !== column.id);
    const kept = (props.form.unpivotSources ?? []).filter((binding) => inputs.some((input) => input.columnId === binding.columnId));
    props.onChange({ ...props.form, inputs, ...(kept.length ? { unpivotSources: kept } : { unpivotSources: undefined }) });
  };
  const updateInputKey = (columnId: string, key: ConstructionTableScalar) => props.onChange({
    ...props.form,
    inputs: props.form.inputs.map((input) => input.columnId === columnId ? { ...input, key } : input),
  });
  const boundChoiceIds = new Set((props.form.unpivotSources ?? []).map((binding) => binding.choiceId));
  const reusableAlreadyProjected = (choice: { readonly fieldPath: string }): boolean =>
    existingColumnForFieldPath({
      sourceProjections: props.sourceProjections,
      columns: props.stage.columns,
      fieldPath: choice.fieldPath,
    }) !== undefined;
  const availableSourceChoices = (props.sourceInput?.supported ?? false)
    ? (props.sourceInput?.choices ?? []).filter((choice) => choice.isPopulated
      && !boundChoiceIds.has(choice.choiceId)
      && !reusableAlreadyProjected(choice))
    : [];
  const sourceUnavailableReason = props.sourceInput && !props.sourceInput.supported &&
      props.sourceInput.reasonCode !== 'IMPORTED_SOURCE_PROJECTION_UNAVAILABLE'
    ? [props.sourceInput.reasonCode, props.sourceInput.reason]
      .map((part) => part?.trim() ?? '')
      .filter((part) => part !== '')
      .join(' · ')
    : undefined;
  const addSourceField = () => {
    const choice = availableSourceChoices[0];
    if (!choice) return;
    const columnId = choice.columnId || createOpaqueId('unpivot-input');
    if (props.form.inputs.some((input) => input.columnId === columnId)) return;
    const virtualColumn: ReshapeColumn = { id: columnId, name: columnId, label: choice.label, type: choice.logicalType, cardinality: 'optional_one' };
    props.onChange({
      ...props.form,
      inputs: [...props.form.inputs, { columnId, key: defaultKeyForColumn(virtualColumn, props.form.inputs) }],
      unpivotSources: [...(props.form.unpivotSources ?? []), { choiceId: choice.choiceId, columnId }],
    });
  };
  const rebindInput = (index: number, value: string) => {
    const current = props.form.inputs[index];
    if (!current) return;
    const withoutCurrent = (props.form.unpivotSources ?? []).filter((binding) => binding.columnId !== current.columnId);
    if (value.startsWith('source:')) {
      const choice = (props.sourceInput?.choices ?? []).find((candidate) => candidate.choiceId === value.slice('source:'.length));
      if (!choice) return;
      const columnId = choice.columnId || createOpaqueId('unpivot-input');
      props.onChange({
        ...props.form,
        inputs: props.form.inputs.map((input, position) => position === index ? { ...input, columnId } : input),
        unpivotSources: [...withoutCurrent, { choiceId: choice.choiceId, columnId }],
      });
      return;
    }
    props.onChange({
      ...props.form,
      inputs: props.form.inputs.map((input, position) => position === index ? { ...input, columnId: value } : input),
      ...(withoutCurrent.length ? { unpivotSources: withoutCurrent } : { unpivotSources: undefined }),
    });
  };
  const inputCount = props.form.inputs.length;
  const rowEffect = inputCount === 0
    ? listColumns.length > 0
      ? 'Choose single-value fields. Split list columns before stacking.'
      : ''
    : props.form.nullRowPolicy === 'PRESERVE'
      ? `${inputCount} ${inputCount === 1 ? 'row' : 'rows'} per original row. Missing values keep their row.`
      : `Up to ${inputCount} ${inputCount === 1 ? 'row' : 'rows'} per original row. Loom skips a row when its selected value is missing.`;

  const laneUnpivotBindings = props.form.unpivotSources ?? [];
  const laneOnlyUnpivotBinding = laneUnpivotBindings.length === 1 ? laneUnpivotBindings[0] : undefined;
  const unpivotSourceEmptiness = classifySourcePickerEmptiness(
    props.sourceInput?.supported ? props.sourceInput.choices : [],
    sourcePickerBoundSet({
      bindings: laneUnpivotBindings,
      isOwn: (binding) => laneOnlyUnpivotBinding !== undefined && binding.choiceId === laneOnlyUnpivotBinding.choiceId,
      ownVisible: laneOnlyUnpivotBinding !== undefined
        && props.form.inputs.some((row) => row.columnId === laneOnlyUnpivotBinding.columnId),
    }),
  );

  return (
    <section aria-label="Unpivot columns into rows" data-testid="construction-reshape-unpivot" className="grid content-start gap-3">
      <p className="m-0 text-sm text-slate-600">
        Choose the fields to stack. Each selected field can make a separate row. By default, <strong>{props.form.keyOutputLabel}</strong> holds the field name and <strong>{props.form.valueOutputLabel}</strong> holds its value. The other columns repeat on each new row.
      </p>
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {props.showInputSelection ? (
        <fieldset className="grid gap-1" disabled={props.disabled || !props.supported}>
          <legend className="m-0 text-sm font-semibold text-slate-800">Columns to turn into rows</legend>
          <div className={fieldPickerList}>
            {scalarColumnsFor(props.stage).map((column) => {
              const input = props.form.inputs.find((candidate) => candidate.columnId === column.id);
              return (
                <label key={column.id} className={fieldPickerToggleRow}>
                  <input type="checkbox" aria-label={`Unpivot ${column.label}`} checked={Boolean(input)} disabled={props.disabled || !props.supported} onChange={(event) => toggleInput(column, event.currentTarget.checked)} />
                  {column.label}
                </label>
              );
            })}
          </div>
        </fieldset>
      ) : null}
      {listColumns.length > 0 ? (
        <ListColumnExpandChoices
          columns={listColumns}
          supported={props.expandSupported}
          disabled={props.disabled}
          onExpand={props.onExpandListColumn}
        />
      ) : null}
      {rowEffect ? <p data-testid="construction-unpivot-effect" className="m-0 text-sm text-slate-600">{rowEffect}</p> : null}
      {sourceUnavailableReason ? (
        <p role="status" data-testid="construction-reshape-unpivot-source-unavailable" className="m-0 text-sm text-amber-900">{sourceUnavailableReason}</p>
      ) : null}
      {props.sourceInput?.supported && availableSourceChoices.length > 0 ? (
        <button
          type="button"
          data-testid="construction-reshape-unpivot-add-source-field"
          disabled={props.disabled || !props.supported}
          onClick={addSourceField}
          className="justify-self-start rounded border border-blue-300 bg-white px-3 py-1.5 text-sm font-medium text-blue-800 disabled:opacity-50"
        >
          Add a field that is not in the table
        </button>
      ) : null}
      {props.form.inputs.length > 0 ? (
        <p data-testid="construction-reshape-unpivot-source-kind-note" className="m-0 text-xs text-slate-500">
          Selected value columns must share a compatible type.
        </p>
      ) : null}
      {props.form.inputs.length > 0 ? <section aria-label="How fields become rows" className="grid min-w-0 gap-2 border-t border-slate-200 pt-2">
        <h4 className="text-sm font-semibold text-slate-900">How fields become rows</h4>
        <p className="m-0 text-sm text-slate-600">
          <strong>{props.form.keyOutputLabel}</strong> identifies the source field on each new row.
          {' '}<strong>{props.form.valueOutputLabel}</strong> holds that field’s value. Each entry under {props.form.keyOutputLabel} must be unique.
        </p>
        <table className="w-full table-fixed border-y border-slate-200 text-left text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-xs text-slate-600">
              <th scope="col" className="px-2 py-1.5">Source field</th>
              <th scope="col" className="px-2 py-1.5">{props.form.keyOutputLabel}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {props.form.inputs.map((input) => (
              <tr key={input.columnId}>
                <th scope="row" className="break-words px-2 py-1.5 font-normal">
                  {props.stage.columns.find((column) => column.id === input.columnId)?.label ?? 'Selected field'}
                </th>
                <td className="break-words px-2 py-1.5">
                  {scalarLabel(input.key)}
                  {input.key.kind === 'NULL' || input.key.kind === 'MISSING'
                    ? <span className="ml-1 text-xs text-amber-900">Choose a value in Customize mappings</span>
                    : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <details>
          <summary className="cursor-pointer py-1 text-sm font-medium text-blue-800">Customize mappings</summary>
          {props.form.inputs.map((input, index) => {
            const column = props.stage.columns.find((candidate) => candidate.id === input.columnId);
            return <div key={`${input.columnId}-${index}`} className="grid min-w-0 gap-2 border-b border-slate-100 py-2">
              <label className="grid gap-1 text-sm font-medium text-slate-800">
                <span>Source field</span>
                <select
                  aria-label={`Unpivot input ${index + 1} field`}
                  data-testid={`construction-reshape-unpivot-input-${index}-column`}
                  value={input.columnId}
                  disabled={props.disabled || !props.supported}
                  onChange={(event) => rebindInput(index, event.currentTarget.value)}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                >
                  {!column ? (
                    <option value={input.columnId}>{props.form.unpivotSources?.some((binding) => binding.columnId === input.columnId) ? 'Selected field (from your data)' : 'Missing column'}</option>
                  ) : null}
                  <optgroup label="Fields in this table">
                    {props.stage.columns
                      .filter((candidate) => !(props.form.unpivotSources ?? []).some((binding) => binding.columnId === candidate.id) || candidate.id === input.columnId)
                      .map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.label}</option>)}
                  </optgroup>
                  <optgroup label="Fields from your data">
                    {availableSourceChoices.map((choice) => (
                      <option key={choice.choiceId} data-testid={`construction-reshape-unpivot-input-${index}-source-option`} value={`source:${choice.choiceId}`} title={choice.fieldPath}>
                        {choice.label} ({choice.logicalType})
                      </option>
                    ))}
                  </optgroup>
                </select>
              </label>
              <ScalarKeyEditor input={input} disabled={props.disabled || !props.supported} onChange={(key) => updateInputKey(input.columnId, key)} />
            </div>;
          })}
        </details>
      </section> : null}
      {props.sourceInput?.supported && unpivotSourceEmptiness.emptyClass !== 'none-listed' ? (
        <SourcePickerExplanation emptiness={unpivotSourceEmptiness} />
      ) : null}
      <label className="grid gap-1 text-sm font-medium text-slate-700">
        If a selected value is missing
        <select aria-label="Unpivot null row policy" value={props.form.nullRowPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = event.currentTarget.value; if (policy === 'DROP' || policy === 'PRESERVE') props.onChange({ ...props.form, nullRowPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
          <option value="DROP">Leave that row out</option>
          <option value="PRESERVE">Keep the row with a missing value</option>
        </select>
      </label>
      {props.form.inputs.length === 0 && listColumns.length === 0 ? <p role="status" className="text-sm text-amber-900">Choose at least one input column.</p> : null}
      {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">The output column names or labels are invalid.</p> : null}
    </section>
  );
};

const ScalarKeyEditor = (props: {
  readonly input: UnpivotInputForm;
  readonly disabled: boolean;
  readonly onChange: (key: ConstructionTableScalar) => void;
}) => {
  const value = scalarLabel(props.input.key);
  return (
    <div className="grid gap-2 sm:grid-cols-[minmax(0,8rem)_minmax(0,1fr)]">
      <label className="grid gap-1 text-xs font-medium text-slate-600">
        Row label type
        <select aria-label={`Unpivot key type ${props.input.columnId}`} value={props.input.key.kind} disabled={props.disabled} onChange={(event) => {
          const kind = scalarKindFromInput(event.currentTarget.value);
          if (kind) props.onChange(convertScalarKind(kind, props.input.key));
        }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
          <option value="STRING">Text</option><option value="INTEGER">Whole number</option><option value="DECIMAL">Decimal number</option><option value="BOOLEAN">True or false</option>{props.input.key.kind === 'NULL' ? <option value="NULL" disabled>Null</option> : null}{props.input.key.kind === 'MISSING' ? <option value="MISSING" disabled>Missing</option> : null}
        </select>
      </label>
      {props.input.key.kind === 'BOOLEAN' ? (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Row label
          <select aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText('BOOLEAN', event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"><option value="true">true</option><option value="false">false</option></select>
        </label>
      ) : props.input.key.kind === 'NULL' || props.input.key.kind === 'MISSING' ? (
        <p role="status" className="self-end text-xs text-amber-900">Choose a row label type and enter a value.</p>
      ) : (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Row label
          <input aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText(props.input.key.kind, event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
        </label>
      )}
    </div>
  );
};
