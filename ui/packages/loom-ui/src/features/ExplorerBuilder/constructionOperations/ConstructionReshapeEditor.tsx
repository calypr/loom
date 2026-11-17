import { createOpaqueId, normalizedName, uniqueName, editorContextKey, stageWithSourceInputs, type CodedPivotContext } from './editorShared';
import React, { useCallback, useId, useRef, useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionTableScalar,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../types';
import { RelatedExpandEditor, type RelatedExpandQueryOwner } from './RelatedExpandEditor';
import { CodedPivotEditor } from './CodedPivotEditor';
import { GroupEditor } from './GroupEditor';
import { PivotEditor } from './PivotEditor';
import { ListColumnExpandChoices } from './ListColumnExpandChoices';
import { classifySourcePickerEmptiness, sourcePickerBoundSet } from './sourcePickerExplanations';
import { existingColumnForFieldPath } from './existingFieldReuse';
import { SourcePickerExplanation } from '../components/SourcePickerExplanation';
import {
  constructionInputStageFor,
  isSourceProjectionStage,
  sourceInputMatchesStage,
} from '../constructionWorkspace/constructionStages';
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
  type PivotCategoryMemory,
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
} from './reshapeForms';

export {
  CONSTRUCTION_RESHAPE_EDITABLE_KINDS,
  hasPublicScalarListColumn,
  publicScalarListColumnIds,
} from './reshapeForms';
export type {
  CandidateIntent,
  CompletePivotDiscovery,
  ConstructionReshapeEditorEntry,
  ConstructionReshapePivotCategory,
  ConstructionReshapePivotDiscovery,
  ConstructionReshapePivotDiscoveryRequest,
  ConstructionReshapeStep,
  ReshapeEntryKind,
  ReshapeForm,
} from './reshapeForms';

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

export interface ConstructionReshapeEditorProps {
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

type PivotDiscoveryRequestOwner = NonNullable<ConstructionReshapeEditorProps['onDiscoverCategories']>;
type PendingPivotDiscovery = {
  readonly identity: string;
  readonly revision: number;
  readonly owner: PivotDiscoveryRequestOwner;
};
type ScheduledPivotDiscovery = PendingPivotDiscovery & { readonly timer: number };
// Editor-owned view of "a discovery for this exact request identity has been asked
// for and no result was adopted yet". Hosts may lag or omit status:'loading', so the
// in-progress UI cannot depend on props.pivotDiscovery alone. `seen` is the host state
// already acknowledged when the request was issued; a newer host object ends the
// in-flight window even for a terminal status.
type AwaitingPivotDiscovery = PendingPivotDiscovery & {
  readonly contextKey: string;
  readonly seen: ConstructionReshapePivotDiscovery | undefined;
};

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
  useEscapeToClose(Boolean(props.onCancel), props.onCancel ?? (() => {}));
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
  const contextKey = editorContextKey(capabilities, editingStep, (props.selectedColumns ?? []).join(','), props.initialKind ?? '');
  const [form, setForm] = useState<ReshapeForm>(() => editingStep
    ? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind)
    : props.initialEntry?.form
      ?? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind));
  const [formContextKey, setFormContextKey] = useState(contextKey);
  const [contractUnavailable, setContractUnavailable] = useState(false);
  const automaticallySelectedPivotPairs = useRef(new Set<string>());
  const manuallySelectedPivotPairs = useRef(new Set<string>());
  const pivotCategoryMemory = useRef(new Map<string, PivotCategoryMemory>());
  const pivotDiscoveryRevision = useRef(0);
  const requestedPivotDiscovery = useRef<PendingPivotDiscovery | undefined>(undefined);
  const scheduledPivotDiscovery = useRef<ScheduledPivotDiscovery | undefined>(undefined);
  const [awaitingPivotDiscovery, setAwaitingPivotDiscovery] = useState<AwaitingPivotDiscovery | undefined>(undefined);
  const rememberAwaitingPivotDiscovery = useCallback((next: AwaitingPivotDiscovery) => {
    setAwaitingPivotDiscovery((current) => (
      current?.identity === next.identity
        && current?.revision === next.revision
        && current?.owner === next.owner
        && current?.contextKey === next.contextKey
        && current?.seen === next.seen
        ? current
        : next
    ));
  }, []);
  const clearAwaitingPivotDiscovery = useCallback((identity: string, revision: number) => {
    setAwaitingPivotDiscovery((current) => (
      current && current.identity === identity && current.revision === revision ? undefined : current
    ));
  }, []);
  const pivotDiscoveryActivationTimer = useRef<number | undefined>(undefined);
  const pivotDiscoveryRootMounted = useRef(false);
  const currentFormRef = useRef(form);
  currentFormRef.current = form;
  const currentContextKeyRef = useRef(contextKey);
  currentContextKeyRef.current = contextKey;
  const currentFormContextKeyRef = useRef(formContextKey);
  currentFormContextKeyRef.current = formContextKey;
  const currentDiscoveryOwnerRef = useRef(props.onDiscoverCategories);
  currentDiscoveryOwnerRef.current = props.onDiscoverCategories;
  const currentStageIdRef = useRef(stage.id);
  currentStageIdRef.current = stage.id;
  const currentConstructionRef = useRef(construction);
  currentConstructionRef.current = construction;
  const currentDisabledRef = useRef(disabled);
  currentDisabledRef.current = disabled;
  const updateFormRef = useRef<((next: ReshapeForm, discovery?: ConstructionReshapePivotDiscovery) => void) | undefined>(undefined);
  const schedulePivotDiscoveryRef = useRef<((next: PivotForm) => void) | undefined>(undefined);
  const currentPivotDiscoveryRef = useRef<ConstructionReshapePivotDiscovery | undefined>(undefined);
  currentPivotDiscoveryRef.current = formContextKey === contextKey ? props.pivotDiscovery : undefined;
  const reportInitialStateRef = useRef<(() => void) | undefined>(undefined);
  const reportedInitialContextKey = useRef<string | undefined>(undefined);
  const pivotDiscoveryRootRef = useCallback((element: HTMLElement | null) => {
    if (element) {
      reportInitialStateRef.current?.();
      pivotDiscoveryRootMounted.current = true;
      pivotDiscoveryActivationTimer.current = window.setTimeout(() => {
        pivotDiscoveryActivationTimer.current = undefined;
        const current = currentFormRef.current;
        if (current.kind !== 'pivot') return;
        const cachedDiscovery = currentPivotDiscoveryRef.current;
        if (cachedDiscovery?.status === 'complete'
            && !pivotCategoriesAreKnown(current, stage.id, cachedDiscovery)
            && adoptPivotDiscovery(pivotDiscoveryRequestIdentity(current, stage.id, contextKey), cachedDiscovery)) return;
        if (!disabled) schedulePivotDiscoveryRef.current?.(current);
      }, 0);
      return;
    }
    pivotDiscoveryRootMounted.current = false;
    if (pivotDiscoveryActivationTimer.current !== undefined) {
      window.clearTimeout(pivotDiscoveryActivationTimer.current);
      pivotDiscoveryActivationTimer.current = undefined;
    }
    const identityChanged = currentContextKeyRef.current !== contextKey
      || currentDiscoveryOwnerRef.current !== props.onDiscoverCategories;
    const scheduled = scheduledPivotDiscovery.current;
    if (scheduled) {
      window.clearTimeout(scheduled.timer);
      scheduledPivotDiscovery.current = undefined;
    }
    if (identityChanged) {
      pivotDiscoveryRevision.current += 1;
    }
  }, [contextKey, disabled, props.initialEntry, props.onDiscoverCategories, stage.id]);
  const [reshapeTab, setReshapeTab] = useState<'pivot' | 'unpivot'>('pivot');
  const [groupPurpose, setGroupPurpose] = useState<'group' | 'summarize' | undefined>();
  if (formContextKey !== contextKey) {
    const initialForm = editingStep
      ? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind)
      : props.initialEntry?.form
        ?? formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind);
    automaticallySelectedPivotPairs.current.clear();
    manuallySelectedPivotPairs.current.clear();
    pivotCategoryMemory.current.clear();
    requestedPivotDiscovery.current = undefined;
    scheduledPivotDiscovery.current = undefined;
    pivotDiscoveryRevision.current += 1;
    setAwaitingPivotDiscovery(undefined);
    setForm(initialForm);
    setFormContextKey(contextKey);
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
  // A host-published loading state stays handled by PivotEditor's own check. Beyond
  // that, an editor-issued request counts as in flight until the host publishes
  // something newer for the current key or a result for its exact identity is
  // adopted, because hosts may lag or omit status:'loading' entirely.
  const pivotDiscoveryInFlight = ((): boolean => {
    const owner = props.onDiscoverCategories;
    if (!owner || disabled || form.kind !== 'pivot' || !pivotDiscoveryRootMounted.current) return false;
    if (formContextKey !== contextKey || !pivotSupport.supported) return false;
    if (awaitingPivotDiscovery?.contextKey !== contextKey
        || awaitingPivotDiscovery.owner !== owner
        || awaitingPivotDiscovery.revision !== pivotDiscoveryRevision.current
        || awaitingPivotDiscovery.identity !== pivotDiscoveryRequestIdentity(form, stage.id, contextKey)) return false;
    if (pivotDiscovery === awaitingPivotDiscovery.seen) return true;
    return !pivotDiscoveryMatches(form, stage.id, pivotDiscovery) || pivotDiscovery.status === 'loading';
  })();

  reportInitialStateRef.current = () => {
    if (props.initialEntry) {
      reportedInitialContextKey.current = undefined;
      return;
    }
    if (reportedInitialContextKey.current === contextKey) return;
    reportedInitialContextKey.current = contextKey;
    const initialForm = formForStep(construction, capabilities, editingStep, groupStage, props.selectedColumns ?? [], props.initialKind);
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
  };

  const capabilityForForm = (next: ReshapeForm) => {
    switch (next.kind) {
      case 'group': return groupCodedValuesSupported ? { supported: true, reason: '' } : groupSupport;
      case 'source-group': return { supported: sourceGroupSupported, reason: sourceGroupReason };
      case 'coded-group': return codedGroupSupport;
      case 'expand': return expandSupport.supported || (capabilities.expandSourceInput?.supported ?? false)
        ? { supported: true, reason: '' }
        : expandSupport;
      case 'related-expand': return relatedExpandSupport;
      case 'pivot': return pivotSupport;
      case 'coded-pivot': return codedPivotSupport;
      case 'unpivot': return unpivotSupport;
      default: return undefined;
    }
  };

  const evaluate = (
    next: ReshapeForm,
    discovery: ConstructionReshapePivotDiscovery | undefined = pivotDiscovery,
  ): CandidateEvaluation => {
    const support = capabilityForForm(next);
    if (!support?.supported) return { kind: 'incomplete' };
    const pivotCategoriesKnown = next.kind === 'pivot' && pivotCategoriesAreKnown(next, stage.id, discovery);
    if (
      next.kind === 'pivot'
      && editingStep?.operation.kind !== 'PIVOT'
      && pivotCategoriesKnown
      && !allDiscoveredPivotCategoriesSelected(next, stage.id, discovery)
    ) return { kind: 'incomplete' };
    return candidateFor({
      form: next,
      construction,
      capabilities,
      stage: next.kind === 'group' ? groupStage : stage,
      editingStep,
      pivotCategoriesKnown,
    });
  };

  const autoSelectDiscoveredCategories = (
    pivot: PivotForm,
    discovery: ConstructionReshapePivotDiscovery | undefined,
  ): PivotForm => {
    if (pivot.discovery) return pivot;
    if (formContextKey !== contextKey
        || pivotOriginalPairApplies(pivot)
        || !pivotDiscoveryMatches(pivot, stage.id, discovery)
        || discovery.status !== 'complete') return pivot;

    const selectionIdentity = pivotSelectionIdentity(pivot, stage.id);
    const manualSelectionApplies = manuallySelectedPivotPairs.current.has(selectionIdentity)
      && pivot.categories.length > 0 && pivot.categoriesPair !== undefined;
    if (automaticallySelectedPivotPairs.current.has(selectionIdentity) || manualSelectionApplies) return pivot;

    automaticallySelectedPivotPairs.current.add(selectionIdentity);
    const savedCategories = editingStep?.operation.kind === 'PIVOT'
      && editingStep.operation.pivot.categoryColumnId === pivot.categoryColumnId
      && editingStep.operation.pivot.valueColumnId === pivot.valueColumnId
      ? initialPivotForm(stage, [], editingStep).categories : [];
    const excluded = pivotCategoryMemory.current.get(pivotCategoryMemoryKey(pivot, stage.id, contextKey))?.excluded;
    const usedNames = new Set([
      ...stage.columns.map((column) => column.name.toLowerCase()),
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
    discovery: ConstructionReshapePivotDiscovery | undefined = pivotDiscovery,
  ) => {
    const next = nextForm.kind === 'pivot' ? autoSelectDiscoveredCategories(nextForm, discovery) : nextForm;
    const previousRequestIdentity = form.kind === 'pivot'
      ? pivotDiscoveryRequestIdentity(form, stage.id, contextKey)
      : undefined;
    const nextRequestIdentity = next.kind === 'pivot'
      ? pivotDiscoveryRequestIdentity(next, stage.id, contextKey)
      : undefined;
    if (next.kind === 'pivot' && next.discovery && form.kind === 'pivot' && !form.discovery) {
      const scheduled = scheduledPivotDiscovery.current;
      if (scheduled && scheduled.identity === previousRequestIdentity) {
        window.clearTimeout(scheduled.timer);
        scheduledPivotDiscovery.current = undefined;
        clearAwaitingPivotDiscovery(scheduled.identity, scheduled.revision);
      }
    }
    if (form.kind === 'pivot' && next.kind === 'pivot' && previousRequestIdentity !== nextRequestIdentity) {
      automaticallySelectedPivotPairs.current.delete(pivotSelectionIdentity(form, stage.id));
    }
    if (previousRequestIdentity !== nextRequestIdentity) {
      pivotDiscoveryRevision.current += 1;
      const scheduled = scheduledPivotDiscovery.current;
      if (scheduled) {
        window.clearTimeout(scheduled.timer);
        scheduledPivotDiscovery.current = undefined;
      }
    }
    setForm(next);
    props.onFormKindChange?.(next.kind);
    const evaluation = evaluate(next, discovery);
    setContractUnavailable(evaluation.kind === 'schema-pending');
    onCandidateChange(evaluation.kind === 'ready' ? evaluation.intent : undefined);
    if (next.kind === 'pivot') schedulePivotDiscoveryRef.current?.(next);
  };
  updateFormRef.current = updateForm;

  const adoptPivotDiscovery = (identity: string, discovery: CompletePivotDiscovery): boolean => {
    const current = currentFormRef.current;
    if (!pivotDiscoveryRootMounted.current
        || currentContextKeyRef.current !== contextKey
        || currentFormContextKeyRef.current !== contextKey
        || currentDiscoveryOwnerRef.current !== props.onDiscoverCategories
        || currentStageIdRef.current !== stage.id
        || current.kind !== 'pivot'
        || pivotDiscoveryRequestIdentity(current, stage.id, contextKey) !== identity
        || !pivotDiscoveryMatches(current, stage.id, discovery)) return false;
    clearAwaitingPivotDiscovery(identity, pivotDiscoveryRevision.current);
    updateFormRef.current?.(current, discovery);
    return true;
  };

  const expandListColumn = (columnId: string) => {
    if (!expandSupport.supported || !listColumnsFor(stage).some((column) => column.id === columnId)) return;
    updateForm(initialExpandForm(stage, undefined, columnId));
  };

  const chooseSourceField = (choice: SourceInputChoice) => {
    const next = initialSourceGroupForm(construction, [choice, ...sourceGroupChoices.filter((candidate) => candidate.choiceId !== choice.choiceId)]);
    if (next) updateForm(next);
  };

  const rememberManualPivotSelection = (pivot: PivotForm) => {
    if (pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;
    manuallySelectedPivotPairs.current.add(pivotSelectionIdentity(pivot, stage.id));
    if (form.kind !== 'pivot') return;
    const memoryKey = pivotCategoryMemoryKey(pivot, stage.id, contextKey);
    const memory = pivotCategoryMemory.current.get(memoryKey) ?? { excluded: new Set<string>() };
    const previousSelected = new Set(form.categories.map((category) => scalarIdentity(category.key)));
    const nextSelected = new Set(pivot.categories.map((category) => scalarIdentity(category.key)));
    for (const identity of previousSelected) {
      if (!nextSelected.has(identity)) memory.excluded.add(identity);
    }
    for (const identity of nextSelected) {
      memory.excluded.delete(identity);
    }
    pivotCategoryMemory.current.set(memoryKey, memory);
  };

  const forgetPivotPairSelection = (previous: PivotForm, next: PivotForm) => {
    for (const identity of [
      pivotSelectionIdentity(previous, stage.id),
      pivotSelectionIdentity(next, stage.id),
    ]) {
      automaticallySelectedPivotPairs.current.delete(identity);
      manuallySelectedPivotPairs.current.delete(identity);
    }
    pivotCategoryMemory.current.delete(pivotCategoryMemoryKey(previous, stage.id, contextKey));
    pivotCategoryMemory.current.delete(pivotCategoryMemoryKey(next, stage.id, contextKey));
  };

  const requestPivotCategories = (pivot: PivotForm, force = false) => {
    const owner = props.onDiscoverCategories;
    if (!owner || disabled || formContextKey !== contextKey || !pivotSupport.supported
        || pivot.discovery
        || (pivot.pivotSources?.length && pivot.groupKeyIds.length === 0)
        || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;

    const identity = pivotDiscoveryRequestIdentity(pivot, stage.id, contextKey);
    const revision = pivotDiscoveryRevision.current;
    const requested = requestedPivotDiscovery.current;
    if (!force && requested?.identity === identity && requested.revision === revision && requested.owner === owner) return;

    const scheduled = scheduledPivotDiscovery.current;
    if (scheduled) {
      window.clearTimeout(scheduled.timer);
      scheduledPivotDiscovery.current = undefined;
    }
    requestedPivotDiscovery.current = { identity, revision, owner };
    rememberAwaitingPivotDiscovery({ identity, revision, owner, contextKey, seen: pivotDiscovery });
    const request: ConstructionReshapePivotDiscoveryRequest = {
      stageId: stage.id,
      ...(pivot.pivotSources?.length ? {
        pivotStepId: pivot.stepId,
        groupKeyIds: [...pivot.groupKeyIds],
        pivotSources: pivot.pivotSources,
        candidateConstruction: currentConstructionRef.current,
      } : {}),
      categoryColumnId: pivot.categoryColumnId,
      valueColumnId: pivot.valueColumnId,
    };
    owner(request, (discovery) => {
      if (pivotDiscoveryRevision.current !== revision
          || currentDiscoveryOwnerRef.current !== owner) return;
      adoptPivotDiscovery(identity, discovery);
    });
  };

  const schedulePivotDiscovery = (pivot: PivotForm) => {
    const owner = props.onDiscoverCategories;
    if (!owner || disabled || formContextKey !== contextKey || !pivotSupport.supported
        || pivot.discovery
        || (pivot.pivotSources?.length && pivot.groupKeyIds.length === 0)
        || pivot.categoryColumnId === '' || pivot.valueColumnId === '') return;

    const identity = pivotDiscoveryRequestIdentity(pivot, stage.id, contextKey);
    if (pivotDiscoveryMatches(pivot, stage.id, pivotDiscovery) && pivotDiscovery.status === 'complete') return;
    if (pivotCategoriesAreKnown(pivot, stage.id, pivotDiscovery)) {
      const scheduled = scheduledPivotDiscovery.current;
      if (scheduled?.identity === identity) {
        window.clearTimeout(scheduled.timer);
        scheduledPivotDiscovery.current = undefined;
      }
      clearAwaitingPivotDiscovery(identity, pivotDiscoveryRevision.current);
      return;
    }
    const revision = pivotDiscoveryRevision.current;
    const requested = requestedPivotDiscovery.current;
    if (requested?.identity === identity && requested.revision === revision && requested.owner === owner) return;
    const pending = scheduledPivotDiscovery.current;
    if (pending?.identity === identity && pending.revision === revision && pending.owner === owner) return;
    if (pending) window.clearTimeout(pending.timer);

    rememberAwaitingPivotDiscovery({ identity, revision, owner, contextKey, seen: pivotDiscovery });
    const timer = window.setTimeout(() => {
      if (scheduledPivotDiscovery.current?.timer !== timer) return;
      scheduledPivotDiscovery.current = undefined;
      const current = currentFormRef.current;
      if (pivotDiscoveryRevision.current !== revision
          || currentContextKeyRef.current !== contextKey
          || !pivotDiscoveryRootMounted.current
          || currentFormContextKeyRef.current !== contextKey
          || currentDiscoveryOwnerRef.current !== owner
          || currentDisabledRef.current
          || current.kind !== 'pivot'
          || currentStageIdRef.current !== stage.id
          || pivotDiscoveryRequestIdentity(current, stage.id, contextKey) !== identity) return;
      requestPivotCategories(current);
    }, 250);
    scheduledPivotDiscovery.current = { identity, revision, owner, timer };
  };
  schedulePivotDiscoveryRef.current = schedulePivotDiscovery;

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

      {!editingStep && (form.kind === 'pivot' || form.kind === 'unpivot' || form.kind === 'coded-pivot') ? (
        <div role="tablist" aria-label="Reshape operation" className="flex gap-1 border-b border-slate-200">
          {([['pivot', 'Pivot'], ['unpivot', 'Unpivot']] as const).map(([tab, label]) => (
            <button key={tab} type="button" role="tab" id={`${id}-reshape-tab-${tab}`}
              aria-selected={tab === 'pivot' ? form.kind !== 'unpivot' : form.kind === 'unpivot'}
              data-testid={`construction-reshape-tab-${tab}`}
              disabled={disabled}
              onClick={() => {
                setReshapeTab(tab);
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

      {!props.rowValuesOnly && form.kind !== 'choose' && form.kind !== 'unsupported' && form.kind !== 'group' ? (
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
        <ExpandEditor
          key={`${id}:expand:${contextKey}`}
          form={form}
          stage={expandInputStage(stage, form, capabilities.expandSourceInput?.choices ?? [])}
          sourceInput={capabilities.expandSourceInput}
          sourceProjections={construction.sourceProjections}
          supported={expandSupport.supported || (capabilities.expandSourceInput?.supported ?? false)}
          reason={expandSupport.reason}
          disabled={disabled}
          onChange={(next) => updateForm(next)}
        />
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
          requireEveryDiscoveredCategory={editingStep?.operation.kind !== 'PIVOT'}
          canDiscover={Boolean(props.onDiscoverCategories)}
          showRowSelection={!props.useExternalColumnSelection}
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
            <label key={field.choice.choiceId} className="flex min-w-0 items-start gap-2 rounded px-2 py-1.5 hover:bg-slate-50">
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
        outputName: `${normalizedName(baseName)}_item`,
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
      outputName: `${normalizedName(column.name)}_item`,
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
            <select aria-label="Repeated field" value={selectedInputValue} disabled={props.disabled || !props.supported} onChange={(event) => updateInput(event.currentTarget.value)} className="rounded border border-slate-300 bg-white px-2 py-1.5">
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
              <div className="grid gap-3 rounded border border-slate-200 p-3 sm:grid-cols-2">
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  Item column name
                  <input aria-label="Expanded item name" value={props.form.outputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, outputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  Item column label
                  <input aria-label="Expanded item label" value={props.form.outputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, outputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" />
                </label>
              </div>
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
      : 'Choose columns to see the row count.'
    : `${inputCount} new ${inputCount === 1 ? 'row' : 'rows'} per original row. ${props.form.nullRowPolicy === 'PRESERVE'
      ? 'Missing values keep their row.'
      : 'Missing values are dropped.'}`;

  // ROW-SOURCE-06 audit: the source-level explanation is a property of the
  // signed source state, not of any input row, so it renders (and is derived)
  // independently of the selected input rows, including with zero inputs.
  // Visibility-aware (ROUND-2 contract): the lane's sole binding is excluded
  // while it renders in a row's picker, so a field bound by the only melt row
  // stays selectable and unexplained instead of being falsely told to remove it.
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
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {props.showInputSelection ? (
        <fieldset className="grid gap-1" disabled={props.disabled || !props.supported}>
          <legend className="m-0 text-sm font-semibold text-slate-800">Columns to turn into rows</legend>
          <div className="grid gap-1 sm:grid-cols-2">
            {scalarColumnsFor(props.stage).map((column) => {
              const input = props.form.inputs.find((candidate) => candidate.columnId === column.id);
              return (
                <label key={column.id} className="flex items-center gap-2 rounded px-2 py-1.5 text-sm text-slate-800 hover:bg-slate-50">
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
      <p data-testid="construction-unpivot-effect" className="m-0 text-sm text-slate-600">
        {rowEffect}
      </p>
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
      <details data-testid="construction-unpivot-advanced" className="rounded border border-slate-200 p-3">
        <summary className="cursor-pointer text-sm font-medium text-blue-800">Advanced options: field names and missing values</summary>
        <div className="mt-3 grid gap-3">
          {props.form.inputs.map((input, index) => {
            const column = props.stage.columns.find((candidate) => candidate.id === input.columnId);
            return <div key={`${input.columnId}-${index}`} className="grid gap-2 rounded bg-slate-50 p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
              <label className="grid gap-1 text-sm font-medium text-slate-800">
                <span>{column?.label ?? 'Selected field'}</span>
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
          {props.sourceInput?.supported ? (
            <SourcePickerExplanation emptiness={unpivotSourceEmptiness} />
          ) : null}
          <div className="grid gap-3 sm:grid-cols-2">
            <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
              <legend className="px-1 text-sm font-semibold text-slate-800">New key column</legend>
              <label className="grid gap-1 text-sm font-medium text-slate-700">Column name<input aria-label="Unpivot key output name" value={props.form.keyOutputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, keyOutputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
              <label className="grid gap-1 text-sm font-medium text-slate-700">Column label<input aria-label="Unpivot key output label" value={props.form.keyOutputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, keyOutputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
            </fieldset>
            <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
              <legend className="px-1 text-sm font-semibold text-slate-800">New value column</legend>
              <label className="grid gap-1 text-sm font-medium text-slate-700">Column name<input aria-label="Unpivot value output name" value={props.form.valueOutputName} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, valueOutputName: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
              <label className="grid gap-1 text-sm font-medium text-slate-700">Column label<input aria-label="Unpivot value output label" value={props.form.valueOutputLabel} disabled={props.disabled || !props.supported} onChange={(event) => props.onChange({ ...props.form, valueOutputLabel: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5" /></label>
            </fieldset>
          </div>
          <label className="grid gap-1 text-sm font-medium text-slate-700">
            If a selected value is missing
            <select aria-label="Unpivot null row policy" value={props.form.nullRowPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = event.currentTarget.value; if (policy === 'DROP' || policy === 'PRESERVE') props.onChange({ ...props.form, nullRowPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
              <option value="DROP">Leave that row out</option>
              <option value="PRESERVE">Keep the row with a missing value</option>
            </select>
          </label>
        </div>
      </details>
      {props.form.inputs.length === 0 && listColumns.length === 0 ? <p role="status" className="text-sm text-amber-900">Choose at least one input column.</p> : null}
      {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give every output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
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
        Key type
        <select aria-label={`Unpivot key type ${props.input.columnId}`} value={props.input.key.kind} disabled={props.disabled} onChange={(event) => {
          const kind = scalarKindFromInput(event.currentTarget.value);
          if (kind) props.onChange(convertScalarKind(kind, props.input.key));
        }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
          <option value="STRING">Text</option><option value="INTEGER">Whole number</option><option value="DECIMAL">Decimal number</option><option value="BOOLEAN">True or false</option><option value="NULL">Null (not allowed as a key)</option><option value="MISSING">Missing (not allowed as a key)</option>
        </select>
      </label>
      {props.input.key.kind === 'BOOLEAN' ? (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Key value
          <select aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText('BOOLEAN', event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"><option value="true">true</option><option value="false">false</option></select>
        </label>
      ) : props.input.key.kind === 'NULL' || props.input.key.kind === 'MISSING' ? (
        <p role="status" className="self-end text-xs text-amber-900">A null or missing key is not allowed. Choose a concrete key type.</p>
      ) : (
        <label className="grid gap-1 text-xs font-medium text-slate-600">
          Key value
          <input aria-label={`Unpivot key value ${props.input.columnId}`} value={value} disabled={props.disabled} onChange={(event) => { const next = scalarFromText(props.input.key.kind, event.currentTarget.value); if (next) props.onChange(next); }} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
        </label>
      )}
    </div>
  );
};
