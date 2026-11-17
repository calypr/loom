import { createOpaqueId, isNumericColumn, normalizedName, uniqueName, sourceLabel as pivotSourceLabel, editorContextKey, stageWithSourceInputs } from './editorShared';
import React, { useCallback, useId, useMemo, useRef, useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import { useQuery } from '../../../react';
import type { LoomClient } from '../../../api';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionStageDescriptor,
  ConstructionTableScalar,
  ConstructionStep,
  ExplorerBuilderCatalog,
  FrameSourceOption,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import { RelatedExpandEditor, type RelatedExpandQueryOwner } from './RelatedExpandEditor';
import { CodedPivotEditor } from './CodedPivotEditor';
import { semanticConceptLabel } from '../catalogItems';
import { classifySourcePickerEmptiness, sourcePickerBoundSet } from './sourcePickerExplanations';
import { existingColumnForFieldPath } from './existingFieldReuse';
import { SourcePickerExplanation } from '../components/SourcePickerExplanation';
import {
  constructionInputForStage,
  constructionInputStageFor,
  isSourceProjectionStage,
  sourceInputMatchesStage,
} from '../constructionWorkspace/constructionStages';
import {
  addGroupKey,
  aggregateInputColumnsFor,
  aggregateNameFor,
  allDiscoveredPivotCategoriesSelected,
  candidateIntentFor,
  capabilityFor,
  codedCategoryKey,
  codedFamilyKey,
  codedGroupStepFromForm,
  codingPathLabel,
  convertScalarKind,
  createPivotCategoryForm,
  defaultKeyForColumn,
  ensureGroupCodedPassthroughs,
  expandOutputsFor,
  expandStepFromForm,
  fieldChoiceIdentity,
  formForStep,
  getUsedGroupNames,
  groupFieldChoiceForKey,
  groupFieldChoicesForStage,
  groupFieldKeyForChoice,
  groupFieldSourcePresentation,
  groupOutputColumns,
  groupRowValueBindingIdentity,
  groupRowValueChoicesForStage,
  groupSemanticKeyForChoice,
  groupStepFromForm,
  initialExpandForm,
  initialGroupForm,
  initialPivotForm,
  initialSourceGroupForm,
  initialUnpivotForm,
  isListColumn,
  listColumnsFor,
  makeAggregate,
  numericColumnsFor,
  operationLabel,
  outputNamesAreValid,
  pivotCategoriesAreKnown,
  pivotCategoryMemoryKey,
  pivotDiscoveryMatches,
  pivotDiscoveryRequestIdentity,
  pivotOriginalPairApplies,
  pivotOutputsFor,
  pivotSelectionIdentity,
  pivotStepFromForm,
  PIVOT_DISCOVERY_MAX_CATEGORIES,
  relatedRouteIdsForGroupFieldKeys,
  replaceGroupAggregate,
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
  type GroupAggregate,
  type GroupAggregateKind,
  type GroupCodedCategory,
  type GroupFieldChoice,
  type GroupFieldKey,
  type GroupFieldSourceChoice,
  type GroupForm,
  type GroupKey,
  type GroupRowValue,
  type GroupRowValueChoice,
  type PivotCategoryForm,
  type PivotCategoryMemory,
  type PivotForm,
  type ReshapeCapabilities,
  type ReshapeColumn,
  type ReshapeEntryKind,
  type ReshapeForm,
  type ReshapeStage,
  type SemanticGroupChoice,
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
  readonly codedPivotContext?: {
    readonly client: Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>;
    readonly project: string;
    readonly explorerId: string;
    readonly authResourcePath?: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly rowRoot: string;
  };
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

const INITIAL_VISIBLE_CODED_CHOICES = 10;
const CODED_INVENTORY_READINESS_POLL_INTERVAL_MS = 1_000;

const waitForCodedInventoryReadinessPoll = (signal: AbortSignal): Promise<void> => new Promise((resolve) => {
  if (signal.aborted) {
    resolve();
    return;
  }
  let timer: number | undefined;
  const finish = () => {
    if (timer !== undefined) window.clearTimeout(timer);
    signal.removeEventListener('abort', finish);
    resolve();
  };
  timer = window.setTimeout(finish, CODED_INVENTORY_READINESS_POLL_INTERVAL_MS);
  signal.addEventListener('abort', finish, { once: true });
});

const availableGroupCodedEntry = (
  item: SemanticInventoryItem,
  sources: ReadonlyArray<FrameSourceOption>,
) => {
  const source = sources.find((candidate) => candidate.resourceType === item.resourceType
    && candidate.bindingId === item.bindingId && candidate.sourcePath === item.sourcePath);
  const semanticSource = item.constructionChoice?.source;
  if (!source || !item.code || item.codingVersion || !item.constructionChoice || item.constructionChoice.route.length !== 0
      || semanticSource?.kind !== 'SEMANTIC' || semanticSource.resourceType !== source.resourceType
      || semanticSource.bindingId !== source.bindingId || semanticSource.sourcePath !== source.sourcePath
      || semanticSource.code !== item.code || (semanticSource.system ?? '') !== item.system
      || item.readiness.status !== 'READY' && item.readiness.status !== 'READY_WITH_WARNING') return undefined;
  return { item, source, familyKey: codedFamilyKey(source.bindingId, source.sourcePath) };
};

type GroupCodedSourceDiscovery = {
  readonly scopeKey: string;
  readonly sources: ReadonlyArray<FrameSourceOption>;
  readonly complete: boolean;
  readonly truncated: boolean;
  readonly loading: boolean;
};

type GroupCodedInventoryReadiness = {
  readonly scopeKey: string;
  readonly contextToken: string;
  readonly buildId: string;
  readonly state: SemanticInventoryBrowseResponse['state'];
};

type GroupCodedInventoryFamily = {
  readonly source: FrameSourceOption;
  readonly entries: SemanticInventoryItem[];
  readonly seenCursors: Set<string>;
  started: boolean;
  cursor?: string;
  exhausted: boolean;
};

type GroupCodedInventoryCheckpoint = {
  readonly scopeKey: string;
  readonly families: Map<string, GroupCodedInventoryFamily>;
  contextToken?: string;
  buildId?: string;
  initialized: boolean;
  loadedRound: number;
};

type GroupCodedInventoryView = {
  readonly scopeKey: string;
  readonly sourceKey: string;
  readonly entries: ReadonlyArray<SemanticInventoryItem>;
  readonly hasMore: boolean;
  readonly complete: boolean;
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

const ListColumnExpandChoices = (props: {
  readonly columns: ReadonlyArray<ReshapeColumn>;
  readonly supported: boolean;
  readonly disabled: boolean;
  readonly onExpand?: (columnId: string) => void;
  readonly inlineGrainAction?: boolean;
}) => {
  if (props.columns.length === 0) return null;
  const choices = props.supported && props.onExpand ? (
    <ul className="m-0 mt-2 grid list-none gap-1 p-0 sm:grid-cols-2">
      {props.columns.map((column) => (
        <li key={column.id}>
          <button type="button" aria-label={`Expand ${column.label}`} disabled={props.disabled}
            onClick={() => props.onExpand?.(column.id)}
            className="w-full rounded px-2 py-1 text-left text-xs text-slate-700 hover:bg-blue-50 disabled:opacity-50">
            Split {column.label} into rows
          </button>
        </li>
      ))}
    </ul>
  ) : <p className="m-0 mt-2 text-xs text-slate-600">These repeated fields can’t be split into rows at this stage.</p>;
  if (props.inlineGrainAction) {
    return (
      <section data-testid="construction-reshape-list-column-settings" aria-label="Repeated field actions" className="rounded border border-slate-200 bg-slate-50 px-2 py-2">
        <h3 className="m-0 text-xs font-semibold text-slate-800">Repeated fields</h3>
        <p className="mb-0 mt-1 text-xs text-slate-600">
          Splitting a repeated field changes the input grain: each list item becomes its own row and values from other fields repeat.
        </p>
        {choices}
      </section>
    );
  }
  return (
    <details data-testid="construction-reshape-list-column-settings" className="rounded border border-slate-200 px-2 py-1.5">
      <summary className="cursor-pointer text-xs font-medium text-blue-800">List column settings</summary>
      {choices}
    </details>
  );
};

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

const readableFieldCombination = (labels: ReadonlyArray<string>): string => {
  if (labels.length < 2) return labels[0] ?? '';
  if (labels.length === 2) return `${labels[0]} and ${labels[1]}`;
  return `${labels.slice(0, -1).join(', ')}, and ${labels.at(-1)}`;
};

const compactSemanticScope = (scopeLabel: string): string => scopeLabel.split(' · ').slice(-2).join(' · ');

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

const GroupRowValuesEditor = (props: {
  readonly form: GroupForm;
  readonly inputColumns: ReadonlyArray<ReshapeColumn>;
  readonly groupKeyInputColumnIds: ReadonlySet<string>;
  readonly groupRowValueChoices: ReadonlyArray<GroupRowValueChoice>;
  readonly disabled: boolean;
  readonly supported: boolean;
  readonly embedded?: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const [rowValueSearch, setRowValueSearch] = useState('');
  const normalizedRowValueSearch = rowValueSearch.trim().toLocaleLowerCase();
  const selectedRowValueChoiceIds = new Set(props.form.rowValues.flatMap((value) =>
    'fieldSource' in value && value.fieldSource.kind === 'CHOICE' ? [value.fieldSource.choiceId] : [],
  ));
  const selectedRowValueBindingIdentities = new Set(props.form.rowValues.flatMap((value) => {
    if (!('fieldSource' in value)) return [];
    const identity = groupRowValueBindingIdentity(value.fieldSource);
    return identity ? [identity] : [];
  }));
  const selectedRowValueInputIDs = new Set(props.form.rowValues.flatMap((value) =>
    'inputColumnId' in value ? [value.inputColumnId] : [],
  ));
  const visibleRowValueChoices = props.groupRowValueChoices.filter((choice) =>
    choice.fieldSource.kind === 'CHOICE' && !selectedRowValueChoiceIds.has(choice.fieldSource.choiceId) &&
    !(choice.inputColumnId && props.groupKeyInputColumnIds.has(choice.inputColumnId)) &&
    !(choice.inputColumnId && selectedRowValueInputIDs.has(choice.inputColumnId)) &&
    !(fieldChoiceIdentity(choice) && selectedRowValueBindingIdentities.has(fieldChoiceIdentity(choice)!)) &&
    (!normalizedRowValueSearch || `${choice.label} ${choice.scopeLabel ?? ''} ${choice.canonicalPath} ${choice.sourceRole}`.toLocaleLowerCase().includes(normalizedRowValueSearch)));
  // Resolved input columns already have exact stage authority, even when a
  // pinned source cannot expose live FHIR field choices. Keep these separate
  // from signed choices rather than inventing a raw-field binding.
  const signedInputColumnIds = new Set(props.groupRowValueChoices.flatMap((choice) => choice.inputColumnId ? [choice.inputColumnId] : []));
  const visibleInputColumns = props.inputColumns.filter((column) =>
    !props.groupKeyInputColumnIds.has(column.id) && !selectedRowValueInputIDs.has(column.id) && !signedInputColumnIds.has(column.id) &&
    (!normalizedRowValueSearch || `${column.label} ${column.name}`.toLocaleLowerCase().includes(normalizedRowValueSearch)));
  const addInputColumn = (column: ReshapeColumn) => {
    if (props.form.codedCategories.length > 0) return;
    const name = uniqueName(normalizedName(column.label), getUsedGroupNames(props.form));
    const rowValue: GroupRowValue = {
      inputColumnId: column.id, outputColumnId: createOpaqueId('group-row-value'), policy: 'ALL',
      name, label: column.label, displayLabel: column.label, scopeLabel: 'Current Group input column', valueType: column.type ?? 'string',
    };
    props.onChange({ ...props.form, rowValues: [...props.form.rowValues, rowValue] });
  };
  const changeRowValue = (outputColumnId: string, update: Partial<Pick<GroupRowValue, 'name' | 'label' | 'policy'>>) => {
    props.onChange({
      ...props.form,
      rowValues: props.form.rowValues.map((value) => value.outputColumnId === outputColumnId ? { ...value, ...update } : value),
    });
  };
  const addGroupRowValue = (choice: GroupRowValueChoice) => {
    if (choice.fieldSource.kind !== 'CHOICE' || props.form.codedCategories.length > 0) return;
    const name = uniqueName(normalizedName(choice.label), getUsedGroupNames(props.form));
    const common = {
      outputColumnId: createOpaqueId('group-row-value'), policy: 'ALL' as const,
      name, label: choice.label, displayLabel: choice.label,
      scopeLabel: choice.scopeLabel ?? 'Current Group input', valueType: choice.valueType ?? 'string',
    };
    const rowValue: GroupRowValue = choice.inputColumnId
      ? { inputColumnId: choice.inputColumnId, ...common }
      : { fieldSource: choice.fieldSource, ...common };
    props.onChange({ ...props.form, rowValues: [...props.form.rowValues, rowValue] });
  };
  const removeGroupRowValue = (outputColumnId: string) => props.onChange({
    ...props.form,
    rowValues: props.form.rowValues.filter((value) => value.outputColumnId !== outputColumnId),
  });

  return (
    <fieldset data-testid="construction-group-add-columns" className={props.embedded ? 'grid gap-3 border-t border-slate-100 pt-3' : 'grid gap-3'} disabled={props.disabled || !props.supported}>
      <legend className={props.embedded ? 'sr-only' : 'm-0 text-sm font-semibold text-slate-900'}>Values from contributing records</legend>
      {props.embedded ? <h3 className="m-0 text-sm font-semibold text-slate-900">Record values <span className="font-normal text-slate-500">(optional)</span></h3> : null}
      <p className="m-0 text-xs text-slate-600">These do not change which rows form a group. They copy a field from the records inside each group into that group row.</p>
      {props.form.rowValues.length > 0 ? (
        <div data-testid="construction-group-retained-values" className="grid gap-1">
        {props.form.rowValues.map((value, index) => (
          <div key={value.outputColumnId} data-testid="construction-group-row-value" className="grid gap-2.5 border-b border-slate-100 py-3 last:border-b-0">
            <div className="min-w-0">
              <p className="m-0 truncate text-sm font-medium text-slate-800" title={value.displayLabel}>{value.displayLabel}</p>
              <p className="m-0 truncate text-xs text-slate-500" title={value.scopeLabel}>{value.scopeLabel}</p>
            </div>
            <div className="grid gap-2 sm:grid-cols-[8rem_minmax(0,1fr)_auto] sm:items-end">
            <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
              Keep values
              <select
                aria-label={`Keep values ${index + 1}`}
                value={value.policy}
                disabled={props.disabled || !props.supported}
                onChange={(event) => {
                  const policy = event.currentTarget.value;
                  if (policy === 'ALL' || policy === 'ONE') changeRowValue(value.outputColumnId, { policy });
                }}
                className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
              >
                <option value="ALL">All values</option>
                <option value="ONE">One value</option>
              </select>
            </label>
            <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
              Output name
              <input
                aria-label={`Added column name ${index + 1}`}
                value={value.name}
                disabled={props.disabled || !props.supported}
                onChange={(event) => changeRowValue(value.outputColumnId, { name: event.currentTarget.value })}
                className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
              />
            </label>
            <button type="button" aria-label={`Remove added column ${index + 1}`} disabled={props.disabled || !props.supported}
              onClick={() => removeGroupRowValue(value.outputColumnId)}
              className="justify-self-start whitespace-nowrap px-1 py-1.5 text-xs font-medium text-red-700 hover:underline">Remove</button>
            </div>
          </div>
        ))}
        </div>
      ) : null}
      <div data-testid="construction-group-add-columns-browser" className="grid gap-3">
        <div className="grid gap-2">
          <label className="grid max-w-xl gap-1 text-xs font-medium text-slate-700">
            Search fields to add
            <input
              type="search"
              aria-label="Search fields to add"
              data-testid="construction-group-add-columns-search"
              value={rowValueSearch}
              onChange={(event) => setRowValueSearch(event.currentTarget.value)}
              className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
            />
          </label>
          {rowValueSearch ? (
            <button type="button" data-testid="construction-group-add-columns-clear-search" onClick={() => setRowValueSearch('')}
              className="justify-self-start text-xs font-medium text-blue-800 hover:underline">Clear search</button>
          ) : null}
          <div data-testid="construction-group-add-columns-field-list" className="grid max-h-72 gap-x-3 overflow-y-auto sm:grid-cols-2">
            {visibleRowValueChoices.map((choice) => {
              const choiceId = choice.fieldSource.kind === 'CHOICE' ? choice.fieldSource.choiceId : '';
              return (
                <label key={`row-value:${choiceId}`} className="flex min-w-0 items-start gap-2 border-b border-slate-100 px-3 py-2 text-sm text-slate-700 last:border-b-0 hover:bg-slate-50">
                  <input
                    type="checkbox"
                    value={choiceId}
                    data-testid={`construction-group-add-column-choice-${choiceId}`}
                    aria-label={`Add column ${choice.label}`}
                    checked={false}
                    disabled={props.disabled || !props.supported || props.form.codedCategories.length > 0}
                    onChange={(event) => { if (event.currentTarget.checked) addGroupRowValue(choice); }}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{choice.label}</span>
                    <span className="block truncate text-xs text-slate-500" title={choice.scopeLabel}>{choice.scopeLabel ?? `${choice.sourceRole} · ${choice.resourceType}.${choice.canonicalPath}`}</span>
                  </span>
                </label>
              );
            })}
            {visibleInputColumns.map((column) => (
              <label key={`row-input:${column.id}`} className="flex min-w-0 items-start gap-2 border-b border-slate-100 px-3 py-2 text-sm text-slate-700 last:border-b-0 hover:bg-slate-50">
                <input type="checkbox" value={column.id}
                  data-testid={`construction-group-add-column-input-${column.id}`}
                  aria-label={`Add column ${column.label}`} checked={false}
                  disabled={props.disabled || !props.supported || props.form.codedCategories.length > 0}
                  onChange={(event) => { if (event.currentTarget.checked) addInputColumn(column); }}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{column.label}</span>
                  <span className="block truncate text-xs text-slate-500" title={column.name}>Current Group input column · {column.name}</span>
                </span>
              </label>
            ))}
            {normalizedRowValueSearch && visibleRowValueChoices.length === 0 && visibleInputColumns.length === 0 ? (
              <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No other fields match this search.</p>
            ) : null}
            {!normalizedRowValueSearch && visibleRowValueChoices.length === 0 && visibleInputColumns.length === 0 ? (
              <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No additional scalar fields are available for this Group input.</p>
            ) : null}
          </div>
        </div>
      </div>
    </fieldset>
  );
};

const GroupEditor = (props: {
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly groupFieldChoices: ReadonlyArray<GroupFieldChoice>;
  readonly groupRowValueChoices: ReadonlyArray<GroupRowValueChoice>;
  readonly rowValuesOnly?: boolean;
  readonly purpose?: 'group' | 'summarize';
  readonly semanticGroupChoices: ReadonlyArray<SemanticGroupChoice>;
  readonly semanticGroupChoicesIncomplete: boolean;
  readonly semanticGroupSupported: boolean;
  readonly codedPivotContext?: NonNullable<ConstructionReshapeEditorProps['codedPivotContext']>;
  readonly codedPivotSupported: boolean;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly expandSupported: boolean;
  readonly onExpandListColumn?: (columnId: string) => void;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const scalarColumns = scalarColumnsFor(props.stage);
  const usedNames = getUsedGroupNames(props.form);
  const [groupSearch, setGroupSearch] = useState('');
  const [activeTab, setActiveTab] = useState<'group' | 'summaries' | 'contributors'>(
    props.form.keys.length === 0 && props.form.fieldKeys.length === 0 && props.form.aggregates.length > 0
      ? 'summaries' : 'group',
  );
  const tabsId = useId();
  const groupOnly = !props.rowValuesOnly && props.purpose === 'group';
  const summarizeOnly = !props.rowValuesOnly && props.purpose === 'summarize';
  const normalizedGroupSearch = groupSearch.trim().toLocaleLowerCase();
  const groupKeyInputColumnIds = new Set([
    ...props.form.keys.map((key) => key.inputColumnId),
    ...props.form.fieldKeys.flatMap((key) => {
      const choice = groupFieldChoiceForKey(props.groupFieldChoices, key);
      return choice && 'inputColumnId' in choice && choice.inputColumnId ? [choice.inputColumnId] : [];
    }),
  ]);
  const contributorInputColumns = props.supported
    ? scalarColumns.filter((column) => column.id !== props.stage.rowIdentityColumn && column.name !== props.stage.rowIdentityColumn)
    : [];
  if (props.rowValuesOnly) {
    return <GroupRowValuesEditor
      form={props.form}
      inputColumns={contributorInputColumns}
      groupKeyInputColumnIds={groupKeyInputColumnIds}
      groupRowValueChoices={props.groupRowValueChoices}
      disabled={props.disabled}
      supported={props.supported}
      onChange={props.onChange}
    />;
  }
  const semanticKey = props.form.semanticKey;
  const semanticCategory = semanticKey?.semanticCategory;
  const codingIdentityKey = props.form.fieldKeys.find((key) => key.kind === 'CODING_IDENTITY');
  const hasCodedKey = Boolean(semanticKey || codingIdentityKey || props.form.codedCategories.length > 0);
  const directSourceStage = isSourceProjectionStage(props.stage);
  const selectedSemanticChoice = semanticCategory?.kind === 'CHOICE'
    ? props.semanticGroupChoices.find((choice) => choice.choiceId === semanticCategory.choiceId)
    : undefined;
  const selectedSemanticChoiceId = semanticCategory?.kind === 'CHOICE'
    ? semanticCategory.choiceId
    : undefined;
  const selectedScalarColumns = scalarColumns.filter((column) => props.form.keys.some((key) => key.inputColumnId === column.id));
  const visibleScalarColumns = scalarColumns.filter((column) =>
    !props.form.keys.some((key) => key.inputColumnId === column.id) &&
    (!normalizedGroupSearch || column.label.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const visibleSemanticChoices = semanticCategory?.kind === 'BINDING'
    ? []
    : props.semanticGroupChoices.filter((choice) => choice.choiceId !== selectedSemanticChoiceId &&
      (!normalizedGroupSearch || `${choice.label} ${choice.resourceType}`.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const showSavedSemanticKey = Boolean(semanticKey && (semanticCategory?.kind === 'BINDING' || !selectedSemanticChoice));
  const sourceFieldChoices = props.groupFieldChoices.filter((choice): choice is GroupFieldSourceChoice => choice.kind === 'FIELD_SOURCE');
  const fieldChoiceForKey = (key: GroupFieldKey): GroupFieldSourceChoice | undefined => groupFieldChoiceForKey(sourceFieldChoices, key);
  const selectedRelatedRouteIds = new Set(relatedRouteIdsForGroupFieldKeys(sourceFieldChoices, props.form.fieldKeys));
  const hasConflictingRelatedRoutes = selectedRelatedRouteIds.size > 1;
  const selectedRelatedFieldChoices = props.form.fieldKeys
    .map(fieldChoiceForKey)
    .filter((choice): choice is GroupFieldSourceChoice => Boolean(choice?.relatedOccurrence));
  const countRowsHelp = selectedRelatedFieldChoices.some((choice) => choice.valueKind === 'CODING_IDENTITY')
    ? 'Counts each contributing code occurrence. Repeated identical codes count separately; kept empty rows also count.'
    : selectedRelatedFieldChoices.some((choice) => choice.valueKind === 'SCALAR')
      ? 'Counts each matching related record; kept empty rows also count.'
      : 'Counts every input row.';
  const savedFieldBindingOutputIds = new Set(props.form.fieldKeys.flatMap((key) =>
    key.fieldSource.kind === 'BINDING' ? [key.outputColumnId] : [],
  ));
  const selectedSourceChoiceIds = new Set(props.form.fieldKeys.flatMap((key) => key.fieldSource.kind === 'CHOICE' ? [key.fieldSource.choiceId] : []));
  const visibleSourceFieldChoices = sourceFieldChoices.filter((choice) =>
    choice.fieldSource.kind === 'CHOICE' && !selectedSourceChoiceIds.has(choice.fieldSource.choiceId) &&
    !(choice.matchedOutputKeyId && savedFieldBindingOutputIds.has(choice.matchedOutputKeyId)) &&
    !(choice.valueKind === 'SCALAR' && choice.inputColumnId && scalarColumns.some((column) => column.id === choice.inputColumnId)) &&
    (!normalizedGroupSearch || `${choice.label} ${choice.scopeLabel ?? ''} ${choice.canonicalPath ?? ''} ${choice.sourceRole ?? ''}`.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const semanticLabelCounts = new Map<string, number>();
  for (const column of [...selectedScalarColumns, ...visibleScalarColumns]) {
    const label = column.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const choice of [...(selectedSemanticChoice ? [selectedSemanticChoice] : []), ...visibleSemanticChoices]) {
    const label = choice.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const key of props.form.fieldKeys) {
    const label = key.displayLabel.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const choice of visibleSourceFieldChoices) {
    const label = choice.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  if (semanticKey && showSavedSemanticKey) {
    const label = semanticKey.displayLabel.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  const selectedGroupLabels = [
    ...scalarColumns.filter((column) => props.form.keys.some((key) => key.inputColumnId === column.id)).map((column) => column.label),
    ...props.form.fieldKeys.map((key) => key.displayLabel),
    ...(semanticKey ? [semanticKey.displayLabel] : []),
    ...props.form.codedCategories.map((category) => category.display),
  ];
  const changeAggregate = (index: number, aggregate: GroupAggregate) => {
    const aggregates = props.form.aggregates.map((current, aggregateIndex) => aggregateIndex === index ? aggregate : current);
    props.onChange({
      ...props.form,
      aggregates,
      ...(props.form.codedCategories.length > 0 ? {
        codedPassthroughs: ensureGroupCodedPassthroughs(props.form, props.stage, [
          ...props.form.keys.map((key) => key.inputColumnId),
          ...aggregates.flatMap((item) => item.operation === 'COUNT_ROWS' ? [] : [item.inputColumnId]),
          ...props.form.rowValues.flatMap((value) => 'inputColumnId' in value ? [value.inputColumnId] : []),
        ]),
      } : {}),
    });
  };
  const removeSemanticKey = () => {
    const { semanticKey: _removed, ...formWithoutSemanticKey } = props.form;
    props.onChange({ ...formWithoutSemanticKey, keyOrder: props.form.keyOrder.filter((id) => id !== semanticKey?.outputColumnId) });
  };
  const removeFieldKey = (outputColumnId: string) => {
    const fieldKeys = props.form.fieldKeys.filter((key) => key.outputColumnId !== outputColumnId);
    const relatedRouteIds = relatedRouteIdsForGroupFieldKeys(sourceFieldChoices, fieldKeys);
    props.onChange({
      ...props.form,
      fieldKeys,
      keyOrder: props.form.keyOrder.filter((id) => id !== outputColumnId),
      unmatchedSourceKeyOutputIds: props.form.unmatchedSourceKeyOutputIds?.filter((id) => id !== outputColumnId),
      ...(relatedRouteIds.length > 1 ? { conflictingRelatedRouteIds: relatedRouteIds } : { conflictingRelatedRouteIds: undefined }),
    });
  };
  const addFieldKey = (choice: GroupFieldSourceChoice) => {
    const key = groupFieldKeyForChoice(props.form, choice);
    if (!key) return;
    props.onChange({ ...props.form, fieldKeys: [...props.form.fieldKeys, key], keyOrder: [...props.form.keyOrder, key.outputColumnId] });
  };
  const renderScalarChoice = (column: ReshapeColumn, checked: boolean) => (
    <label key={`scalar:${column.id}`} className="flex min-w-0 items-center gap-2 rounded px-2 py-1.5 text-sm text-slate-700 hover:bg-slate-50">
      <input
        type="checkbox"
        value={column.id}
        checked={checked}
        aria-label={`Group by ${column.label}`}
            disabled={props.disabled || !props.supported}
        onChange={() => props.onChange(checked
          ? {
              ...props.form,
              keys: props.form.keys.filter((key) => key.inputColumnId !== column.id),
              keyOrder: props.form.keyOrder.filter((id) => !props.form.keys.some((key) => key.inputColumnId === column.id && key.outputColumnId === id)),
            }
          : addGroupKey(props.form, props.stage, column.id))}
      />
      <span className="min-w-0 truncate" title={column.label}>{column.label}</span>
    </label>
  );
  const renderSemanticChoice = (choice: SemanticGroupChoice, checked: boolean) => {
    const duplicateLabel = (semanticLabelCounts.get(choice.label.trim().toLocaleLowerCase()) ?? 0) > 1;
    const disambiguation = duplicateLabel ? compactSemanticScope(choice.scopeLabel) : undefined;
    const disabled = props.disabled || !props.supported || !directSourceStage || !props.semanticGroupSupported ||
      props.form.codedCategories.length > 0 && !checked || Boolean(codingIdentityKey) && !checked || Boolean(semanticKey) && !checked;
    return (
      <div key={`semantic:${choice.choiceId}`} data-testid="construction-group-semantic-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className="flex min-w-0 items-start gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            value={choice.choiceId}
            data-semantic-choice-id={choice.choiceId}
            data-testid={`construction-group-semantic-choice-${choice.choiceId}`}
            checked={checked}
            aria-label={`Group by ${choice.label}${disambiguation ? ` (${disambiguation})` : ''}`}
            disabled={disabled}
            aria-describedby={semanticKey && !checked ? 'construction-group-coded-key-limit' : undefined}
            onChange={(event) => {
              if (!event.currentTarget.checked) {
                removeSemanticKey();
                return;
              }
              if (!semanticKey && !codingIdentityKey && props.form.codedCategories.length === 0) {
                const next = groupSemanticKeyForChoice(props.form, choice);
                props.onChange({ ...props.form, semanticKey: next, keyOrder: [...props.form.keyOrder, next.outputColumnId] });
              }
            }}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{choice.label}</span>
            {disambiguation ? <span className="block truncate text-xs text-slate-500" title={choice.scopeLabel}>{disambiguation}</span> : null}
          </span>
        </label>
        <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-field-details-${choice.choiceId}`}>
          <summary className="cursor-pointer">Details</summary>
          <p className="mb-0 mt-1 break-words">{choice.scopeLabel}</p>
        </details>
      </div>
    );
  };
  const renderSelectedFieldKey = (key: GroupFieldKey) => {
    const matchedChoice = fieldChoiceForKey(key);
    const unmatchedBinding = key.fieldSource.kind === 'BINDING' && !matchedChoice;
    const sourceDescription = matchedChoice
      ? matchedChoice.ownerResourceType
      : groupFieldSourcePresentation(key.fieldSource)?.ownerResourceType;
    const detailDescription = matchedChoice
      ? [matchedChoice.relatedOccurrence?.routeLabel, matchedChoice.scopeLabel].filter((part): part is string => Boolean(part)).join(' · ')
      : key.scopeLabel;
    return (
      <div key={`field-source:${key.outputColumnId}`} data-testid="construction-group-field-source-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className="flex min-w-0 items-start gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            value={key.outputColumnId}
            checked
            aria-label={`Group by ${key.displayLabel}`}
            disabled={props.disabled || !props.supported}
            onChange={() => removeFieldKey(key.outputColumnId)}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{key.displayLabel}</span>
            {sourceDescription ? <span className="block break-words text-xs text-slate-500">{sourceDescription}</span> : null}
            {unmatchedBinding ? <span data-testid={`construction-group-stale-field-${key.outputColumnId}`} className="block text-xs font-medium text-amber-800">
              Saved source field has no current capability match. Remove it or refresh the Group capabilities before applying changes.
            </span> : null}
          </span>
        </label>
        {detailDescription ? (
          <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-saved-field-details-${key.outputColumnId}`}>
            <summary className="cursor-pointer">Details</summary>
            <p className="mb-0 mt-1 break-words">{detailDescription}</p>
          </details>
        ) : null}
      </div>
    );
  };
  const renderFieldChoice = (choice: GroupFieldSourceChoice) => {
    const routeId = choice.relatedOccurrence?.routeId;
    const differentRoute = Boolean(routeId && (
      hasConflictingRelatedRoutes || selectedRelatedRouteIds.size > 0 && !selectedRelatedRouteIds.has(routeId)
    ));
    const disabled = props.disabled || !props.supported ||
      (choice.valueKind === 'CODING_IDENTITY' && hasCodedKey) ||
      (choice.valueKind === 'SCALAR' && props.form.codedCategories.length > 0) || differentRoute;
    const choiceId = choice.fieldSource.kind === 'CHOICE' ? choice.fieldSource.choiceId : '';
    const sourceDescription = choice.ownerResourceType;
    const detailDescription = [choice.relatedOccurrence?.routeLabel, choice.scopeLabel]
      .filter((part): part is string => Boolean(part)).join(' · ');
    const describedBy = [
      choice.valueKind === 'CODING_IDENTITY' && hasCodedKey ? 'construction-group-coded-key-limit' : undefined,
      differentRoute ? 'construction-group-related-route-limit' : undefined,
    ].filter((id): id is string => Boolean(id)).join(' ') || undefined;
    return (
      <div key={`source-choice:${choiceId}`} data-testid="construction-group-field-source-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className="flex min-w-0 items-start gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            value={choiceId}
            data-testid={`construction-group-field-choice-${choiceId}`}
            checked={false}
            aria-label={`Group by ${choice.label}`}
            disabled={disabled}
            aria-describedby={describedBy}
            onChange={(event) => { if (event.currentTarget.checked) addFieldKey(choice); }}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{choice.label}</span>
            <span className="block break-words text-xs text-slate-500">{sourceDescription}</span>
            {differentRoute ? <span data-testid={`construction-group-different-route-${choiceId}`} className="block text-xs font-medium text-amber-800">
              {hasConflictingRelatedRoutes
                ? 'Saved fields use multiple related routes. Remove fields until one route remains before choosing another related field.'
                : 'Available from a different related route. Remove the selected route field to choose this one.'}
            </span> : null}
          </span>
        </label>
        {detailDescription ? (
          <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-choice-details-${choiceId}`}>
            <summary className="cursor-pointer">Details</summary>
            <p className="mb-0 mt-1 break-words">{detailDescription}</p>
          </details>
        ) : null}
      </div>
    );
  };
  const renderSavedSemanticKey = () => {
    if (!semanticKey || !semanticCategory) return null;
    const semanticValue = semanticCategory.kind === 'BINDING'
      ? `binding:${semanticCategory.bindingId}:${semanticCategory.ruleVersion}`
      : semanticCategory.choiceId;
    const labelKey = semanticKey.displayLabel.trim().toLocaleLowerCase();
    const disambiguation = (semanticLabelCounts.get(labelKey) ?? 0) > 1
      ? compactSemanticScope(semanticKey.scopeLabel)
      : undefined;
    return (
      <div data-testid="construction-group-semantic-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className="flex min-w-0 items-start gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            value={semanticValue}
            checked
            aria-label={`Group by ${semanticKey.displayLabel}${disambiguation ? ` (${disambiguation})` : ''}`}
            disabled={props.disabled || !props.supported || !directSourceStage || !props.semanticGroupSupported}
            onChange={removeSemanticKey}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{semanticKey.displayLabel}</span>
            {disambiguation ? <span className="block truncate text-xs text-slate-500" title={semanticKey.scopeLabel}>{disambiguation}</span> : null}
          </span>
        </label>
        <details className="ml-6 mt-1 text-xs text-slate-600" data-testid="construction-group-saved-field-details">
          <summary className="cursor-pointer">Details</summary>
          <p className="mb-0 mt-1 break-words">{semanticKey.scopeLabel}</p>
        </details>
      </div>
    );
  };

  return (
    <section aria-label="Summarize into groups" data-testid="construction-reshape-group" className="construction-reshape-group-editor grid content-start gap-4">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}

      {groupOnly ? null : (
      <div role="tablist" aria-label="Group editor sections" className="flex flex-wrap gap-1 border-b border-slate-200 pb-2">
        {([
          ['group', 'Group by', selectedGroupLabels.length],
          ['summaries', 'Summaries', props.form.aggregates.length],
        ] as const).map(([tab, label, count]) => (
          <button
            key={tab}
            id={`${tabsId}-tab-${tab}`}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            aria-controls={`${tabsId}-panel-${tab}`}
            data-testid={`construction-group-tab-${tab}`}
            onClick={() => setActiveTab(tab)}
            className={activeTab === tab
              ? 'rounded border border-slate-300 bg-blue-50 px-3 py-1.5 text-sm font-medium text-blue-900'
              : 'rounded border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50'}
          >
            {label}{count > 0 ? ` (${count})` : ''}
          </button>
        ))}
      </div>
      )}
      <div
        id={`${tabsId}-panel-group`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-group`}
        className={groupOnly || activeTab === 'group' ? 'grid items-start gap-4' : 'hidden'}
      >
        <fieldset className="grid min-w-0 content-start gap-3" disabled={props.disabled || !props.supported}>
          <legend className="m-0 text-sm font-semibold text-slate-900">Group by</legend>
          <p className="m-0 text-xs text-slate-600">These fields define the groups. Each unique combination becomes one output row.</p>
          <div data-testid="construction-group-selected-keys" className="grid gap-1">
            {selectedScalarColumns.map((column) => renderScalarChoice(column, true))}
            {props.form.fieldKeys.map(renderSelectedFieldKey)}
            {selectedSemanticChoice ? renderSemanticChoice(selectedSemanticChoice, true) : null}
            {showSavedSemanticKey ? renderSavedSemanticKey() : null}
          </div>
          {sourceFieldChoices.some((choice) => choice.relatedOccurrence) ? (
            <p id="construction-group-related-route-limit" data-testid="construction-group-related-route-guidance" role="status" className="m-0 rounded bg-slate-50 px-2 py-1.5 text-xs text-slate-600">
              Related fields can be combined with root fields and fields from one matching source route. Fields from other routes stay visible but disabled.
            </p>
          ) : null}
          {hasConflictingRelatedRoutes || props.form.conflictingRelatedRouteIds?.length ? (
            <p data-testid="construction-group-related-route-conflict" role="alert" className="m-0 rounded bg-amber-50 px-2 py-1.5 text-xs text-amber-900">
              This saved Group combines fields from multiple related routes. The saved fields remain selected, but Apply is blocked until you remove fields so only one related route remains.
            </p>
          ) : null}
          {props.form.unmatchedSourceKeyOutputIds?.length ? (
            <p data-testid="construction-group-stale-binding-warning" role="alert" className="m-0 rounded bg-amber-50 px-2 py-1.5 text-xs text-amber-900">
              A saved source field has no current capability match. Its saved binding is preserved below, and Apply is blocked until you remove it or refresh this Group’s capabilities.
            </p>
          ) : null}
          <div data-testid="construction-group-by-browser" className="grid gap-2">
              <label className="grid gap-1 text-xs font-medium text-slate-700">
                Search fields
                <input
                  type="search"
                  aria-label="Search fields to group by"
                  data-testid="construction-group-by-search"
                  value={groupSearch}
                  onChange={(event) => setGroupSearch(event.currentTarget.value)}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                />
              </label>
              {groupSearch ? (
                <button type="button" data-testid="construction-group-by-clear-search" onClick={() => setGroupSearch('')}
                  className="justify-self-start text-xs font-medium text-blue-800 hover:underline">Clear search</button>
              ) : null}
              <div data-testid="construction-group-by-field-list" className="grid max-h-72 gap-x-3 overflow-y-auto sm:grid-cols-2">
                {visibleScalarColumns.map((column) => renderScalarChoice(column, false))}
                {visibleSourceFieldChoices.map(renderFieldChoice)}
                {visibleSemanticChoices.map((choice) => renderSemanticChoice(choice, false))}
                {normalizedGroupSearch && visibleScalarColumns.length === 0 && visibleSourceFieldChoices.length === 0 && visibleSemanticChoices.length === 0 ? (
                  <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No other fields match this search.</p>
                ) : null}
                {!normalizedGroupSearch && selectedScalarColumns.length === 0 && !selectedSemanticChoice && !showSavedSemanticKey &&
                  props.form.fieldKeys.length === 0 && visibleScalarColumns.length === 0 && visibleSourceFieldChoices.length === 0 && visibleSemanticChoices.length === 0 ? (
                  <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">
                    No eligible fields are available to group by. You can still summarize all input rows.
                  </p>
                ) : null}
              </div>
          </div>
          {props.semanticGroupChoicesIncomplete ? (
            <p role="status" className="m-0 text-xs text-amber-800">Some coded fields may be missing from this list; search only covers the fields currently available.</p>
          ) : null}
          {hasCodedKey ? (
            <p id="construction-group-coded-key-limit" role="status" className="m-0 text-xs text-slate-600">
              A Group can use one coded identity field. Remove {semanticKey?.displayLabel ?? codingIdentityKey?.displayLabel ?? props.form.codedCategories[0]?.display ?? 'the coded field'} before choosing a different coded field.
            </p>
          ) : null}
          {props.form.codedCategories.length > 0 ? (
            <p role="status" className="m-0 text-xs text-slate-600">Remove the selected recorded-code values before choosing a coded field.</p>
          ) : null}
          {selectedGroupLabels.length > 0 || props.form.aggregates.length > 0 ? (
            <p data-testid="construction-group-grain-summary" role="status" aria-live="polite" className="m-0 rounded bg-blue-50 px-2 py-1.5 text-sm text-slate-700">
              {selectedGroupLabels.length > 0
                ? `One row per unique combination of ${readableFieldCombination(selectedGroupLabels)}.`
                : 'With no fields selected, one row summarizes all input rows.'}
            </p>
          ) : null}
          {selectedGroupLabels.length > 0 ? (
            <label className="grid gap-1 text-xs font-medium text-slate-700">
              Rows with a missing group key
              <select
                aria-label="Missing group key policy"
                value={props.form.missingKeyPolicy}
                disabled={props.disabled || !props.supported}
                onChange={(event) => {
                  const policy = event.currentTarget.value;
                  if (policy === 'GROUP' || policy === 'EXCLUDE' || policy === 'ERROR') {
                    props.onChange({ ...props.form, missingKeyPolicy: policy });
                  }
                }}
                className="justify-self-start rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
              >
                <option value="GROUP">Keep in one missing-key group</option>
                <option value="EXCLUDE">Exclude from summaries</option>
                <option value="ERROR">Stop the operation</option>
              </select>
            </label>
          ) : null}
          {listColumnsFor(props.stage).length > 0 ? (
            <ListColumnExpandChoices
              columns={listColumnsFor(props.stage)}
              supported={props.expandSupported}
              disabled={props.disabled}
              inlineGrainAction
              onExpand={props.onExpandListColumn}
            />
          ) : null}
          {props.codedPivotSupported && props.codedPivotContext ? (
            <details data-testid="construction-group-recorded-values-advanced" className="rounded border border-slate-200 p-2">
              <summary className="cursor-pointer text-sm font-medium text-blue-800">Advanced: choose specific recorded values</summary>
              <p className="mb-2 mt-1 text-xs text-slate-600">Use this when you need selected code values rather than grouping by the full coded field.</p>
              <GroupCodedValuePicker
                context={props.codedPivotContext}
                form={props.form}
                stage={props.stage}
                disabled={props.disabled || !props.supported || Boolean(props.form.semanticKey) || Boolean(codingIdentityKey)}
                onChange={props.onChange}
              />
            </details>
          ) : null}
          {selectedGroupLabels.length > 0 || props.form.aggregates.length > 0 ? (
            <GroupRowValuesEditor
              embedded
              form={props.form}
              inputColumns={contributorInputColumns}
              groupKeyInputColumnIds={groupKeyInputColumnIds}
              groupRowValueChoices={props.groupRowValueChoices}
              disabled={props.disabled}
              supported={props.supported}
              onChange={props.onChange}
            />
          ) : null}
        </fieldset>

      </div>

      <fieldset
        id={`${tabsId}-panel-summaries`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-summaries`}
        className={!groupOnly && activeTab === 'summaries' ? 'grid min-w-0 content-start gap-3' : 'hidden'}
        disabled={props.disabled || !props.supported}
      >
          <legend className="m-0 text-sm font-semibold text-slate-900">Summaries</legend>
          <p className="m-0 text-xs text-slate-600">
            {props.form.aggregates.length === 0
              ? 'No summaries. Group keys alone produce one row per distinct combination.'
              : 'Add another summary if this group needs more calculated outputs.'}
          </p>
          {props.form.aggregates.map((aggregate, index) => {
            const inputColumns = aggregateInputColumnsFor(props.stage, aggregate.operation);
            const operation: GroupAggregateKind = aggregate.operation;
            return (
              <div key={aggregate.outputColumnId} data-testid={`construction-reshape-aggregate-${index}`} className="grid gap-2.5 border-b border-slate-100 py-3 last:border-b-0">
                <div className="grid gap-2 sm:grid-cols-2">
                  <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                    Summary
                    <select
                      aria-label={`Summary ${index + 1}`}
                      value={operation}
                      disabled={props.disabled || !props.supported}
                      onChange={(event) => {
                        const selected = groupAggregateKindFromInput(event.currentTarget.value);
                        if (!selected) return;
                        const replacement = replaceGroupAggregate(aggregate, selected, props.stage, getUsedGroupNames(props.form, aggregate.outputColumnId));
                        changeAggregate(index, replacement);
                      }}
                      className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                    >
                      <option value="COUNT_ROWS">Count rows</option>
                      <option value="COUNT_NON_NULL">Count present values</option>
                      <option value="COUNT_DISTINCT">Count distinct values</option>
                      <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
                      <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
                    </select>
                  </label>
                  {aggregate.operation !== 'COUNT_ROWS' ? (
                    <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                      Field
                      <select
                        aria-label={`Summary field ${index + 1}`}
                        value={aggregate.inputColumnId}
                        disabled={props.disabled || !props.supported}
                        onChange={(event) => {
                          const input = inputColumns.find((column) => column.id === event.currentTarget.value);
                          if (!input) return;
                          changeAggregate(index, {
                            ...aggregate,
                            inputColumnId: input.id,
                            name: uniqueName(aggregateNameFor(aggregate.operation, input), getUsedGroupNames(props.form, aggregate.outputColumnId)),
                            label: `${operationLabel(aggregate.operation)} of ${input.label}`,
                          });
                        }}
                        className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                      >
                        {inputColumns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                      </select>
                      {inputColumns.length === 0 ? <span role="status" className="text-xs font-normal normal-case tracking-normal text-amber-900">No compatible columns were returned for this summary.</span> : null}
                    </label>
                  ) : (
                    <p className="m-0 self-end pb-2 text-xs font-normal normal-case tracking-normal text-slate-500">{countRowsHelp}</p>
                  )}
                </div>
                <div className="flex items-end gap-2">
                  <label className="grid min-w-0 flex-1 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                    Output name
                    <input
                      aria-label={`Summary output name ${index + 1}`}
                      value={aggregate.name}
                      disabled={props.disabled || !props.supported}
                      onChange={(event) => changeAggregate(index, { ...aggregate, name: event.currentTarget.value })}
                      className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                    />
                  </label>
                  <button type="button" aria-label={`Remove summary ${index + 1}`} disabled={props.disabled || !props.supported} onClick={() => props.onChange({ ...props.form, aggregates: props.form.aggregates.filter((_, itemIndex) => itemIndex !== index) })} className="mb-0.5 whitespace-nowrap rounded px-2 py-1 text-xs font-medium text-slate-500 hover:bg-red-50 hover:text-red-700">Remove</button>
                </div>
              </div>
            );
          })}
          <label className="grid max-w-xs gap-1 text-xs font-medium text-slate-700">
            {props.form.aggregates.length === 0 ? 'Choose a summary' : 'Add another summary'}
            <select
              aria-label="Add summary"
              data-testid="construction-reshape-add-summary"
              value=""
              disabled={props.disabled || !props.supported}
              onChange={(event) => {
                const operation = groupAggregateKindFromInput(event.currentTarget.value);
                if (!operation) return;
                props.onChange({
                  ...props.form,
                  aggregates: [...props.form.aggregates, makeAggregate(operation, props.stage, usedNames)],
                });
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
            >
              <option value="">{props.form.aggregates.length === 0 ? 'Select a summary to add' : 'Select another summary'}</option>
              <option value="COUNT_ROWS">Count rows</option>
              <option value="COUNT_NON_NULL">Count present values</option>
              <option value="COUNT_DISTINCT">Count distinct values</option>
              <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
              <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
            </select>
          </label>
      </fieldset>

      {!outputNamesAreValid(groupOutputColumns(props.form, props.stage)) ? (
        <p role="status" className="text-sm text-amber-900">Give every output a unique column name using letters, numbers, or underscores, and add a label.</p>
      ) : null}
    </section>
  );
};

const GroupCodedValuePicker = (props: {
  readonly context: NonNullable<ConstructionReshapeEditorProps['codedPivotContext']>;
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly disabled: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const { client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot } = props.context;
  const [pageRound, setPageRound] = useState(0);
  const [sourceProgress, setSourceProgress] = useState<GroupCodedSourceDiscovery>();
  const [inventoryProgress, setInventoryProgress] = useState<GroupCodedInventoryView>();
  const [readinessProgress, setReadinessProgress] = useState<GroupCodedInventoryReadiness>();
  const checkpointRef = useRef<GroupCodedInventoryCheckpoint | undefined>(undefined);
  const requestedRoundRef = useRef(0);
  const currentForm = useRef(props.form);
  currentForm.current = props.form;
  const changeForm = useRef(props.onChange);
  changeForm.current = props.onChange;

  const sourceScopeKey = JSON.stringify([project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot]);
  const readinessQuery = useQuery(async (signal) => {
    const browseReadiness = () => client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, rowRoot, limit: 1,
    }, signal);
    let response = await browseReadiness();
    while (response.state === 'running' && !signal.aborted) {
      setReadinessProgress({
        scopeKey: sourceScopeKey,
        contextToken: response.contextToken,
        buildId: response.buildId,
        state: response.state,
      });
      await waitForCodedInventoryReadinessPoll(signal);
      if (!signal.aborted) response = await browseReadiness();
    }
    return {
      scopeKey: sourceScopeKey,
      contextToken: response.contextToken,
      buildId: response.buildId,
      state: response.state,
    } satisfies GroupCodedInventoryReadiness;
  }, [client, sourceScopeKey]);
  const readiness = readinessQuery.data?.scopeKey === sourceScopeKey
    ? readinessQuery.data
    : readinessProgress?.scopeKey === sourceScopeKey ? readinessProgress : undefined;
  const inventoryReady = Boolean(readiness?.buildId && readiness.state === 'complete');
  const inventoryScopeKey = JSON.stringify([sourceScopeKey, readiness?.contextToken, readiness?.buildId]);
  const sourcesQuery = useQuery(async (signal) => {
    const sources = new Map<string, FrameSourceOption>();
    const seenCursors = new Set<string>();
    let cursor: string | undefined;
    let complete = false;
    let truncated = false;
    do {
      const response = await client.browseFrameSourceOptions({
        project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: rowRoot, limit: 50,
        ...(cursor ? { cursor } : {}),
      }, signal);
      if (response.snapshotToken !== snapshotToken || response.outputId !== outputId) {
        throw new Error('Coded source fields were returned for a different table snapshot. Reload the table and retry.');
      }
      for (const source of response.sources) {
        if (source.route.length === 0 && source.resourceType === rowRoot) sources.set(source.choiceId, source);
      }
      complete = response.complete;
      truncated = truncated || response.truncated;
      cursor = response.nextCursor;
      const progress = {
        scopeKey: inventoryScopeKey,
        sources: [...sources.values()],
        complete: complete && !cursor,
        truncated,
        loading: Boolean(cursor),
      } satisfies GroupCodedSourceDiscovery;
      if (!signal.aborted) setSourceProgress(progress);
      if (cursor && seenCursors.has(cursor)) throw new Error('Coded source fields could not be completely listed. Reopen the editor to retry.');
      if (cursor) seenCursors.add(cursor);
    } while (cursor && !signal.aborted);
    return {
      scopeKey: inventoryScopeKey,
      sources: [...sources.values()],
      complete: complete && !cursor,
      truncated,
      loading: false,
    } satisfies GroupCodedSourceDiscovery;
  }, [client, inventoryScopeKey], inventoryReady);

  const sourceDiscovery = sourcesQuery.data?.scopeKey === inventoryScopeKey
    ? sourcesQuery.data
    : sourceProgress?.scopeKey === inventoryScopeKey ? sourceProgress : undefined;
  const sources = sourceDiscovery?.sources ?? [];
  const sourceKey = JSON.stringify(sources.map((source) => [source.choiceId, source.bindingId, source.sourcePath]));
  const currentInventoryScopeKey = useRef(inventoryScopeKey);
  currentInventoryScopeKey.current = inventoryScopeKey;
  const inventoryQuery = useQuery(async (signal) => {
    let checkpoint = checkpointRef.current;
    if (!checkpoint || checkpoint.scopeKey !== inventoryScopeKey
        || checkpoint.contextToken !== readiness?.contextToken || checkpoint.buildId !== readiness?.buildId) {
      checkpoint = {
        scopeKey: inventoryScopeKey,
        families: new Map(),
        contextToken: readiness?.contextToken,
        buildId: readiness?.buildId,
        initialized: false,
        loadedRound: 0,
      };
      checkpointRef.current = checkpoint;
    }
    const currentCheckpoint = checkpoint;
    for (const source of sources) {
      const existing = currentCheckpoint.families.get(source.choiceId);
      if (existing && (existing.source.resourceType !== source.resourceType
          || existing.source.bindingId !== source.bindingId || existing.source.sourcePath !== source.sourcePath)) {
        throw new Error('The direct coded source changed while it was loading. Reload the table and retry.');
      }
      if (!existing) currentCheckpoint.families.set(source.choiceId, {
        source, entries: [], seenCursors: new Set<string>(), started: false, exhausted: false,
      });
    }
    const makeView = (): GroupCodedInventoryView => {
      const families = [...currentCheckpoint.families.values()];
      return {
        scopeKey: inventoryScopeKey,
        sourceKey,
        entries: families.flatMap((family) => family.entries),
        hasMore: families.some((family) => Boolean(family.cursor)),
        complete: sourceDiscovery?.complete === true && !sourceDiscovery.truncated && families.every((family) => family.exhausted),
      };
    };
    const publish = () => setInventoryProgress(makeView());
    const fetchPage = async (family: GroupCodedInventoryFamily) => {
      if (signal.aborted || family.exhausted) return;
      const response = await client.browseSemanticInventory({
        project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
        sourceChoiceId: family.source.choiceId,
        ...(family.cursor ? { cursor: family.cursor } : {}),
        limit: 50,
      }, signal);
      if (signal.aborted) return;
      if (response.state !== 'complete') {
        throw new Error(`Coded values for ${family.source.title} are not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.`);
      }
      if (currentCheckpoint.contextToken !== response.contextToken
          || currentCheckpoint.buildId !== response.buildId) {
        throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
      }
      family.entries.push(...response.entries.filter((item) => item.resourceType === family.source.resourceType
        && item.bindingId === family.source.bindingId && item.sourcePath === family.source.sourcePath));
      family.cursor = response.nextCursor;
      family.exhausted = !response.nextCursor;
      family.started = true;
      if (response.nextCursor) {
        if (family.seenCursors.has(response.nextCursor)) {
          throw new Error(`Coded values for ${family.source.title} could not be completely loaded. Reopen the editor to retry.`);
        }
        family.seenCursors.add(response.nextCursor);
      }
    };
    const unstarted = [...currentCheckpoint.families.values()].filter((family) => !family.started);
    if (unstarted.length > 0) {
      await Promise.all(unstarted.map(fetchPage));
      if (signal.aborted) return makeView();
      publish();
    }
    if (!currentCheckpoint.initialized || unstarted.length > 0) {
      while (!signal.aborted) {
        const visibleKeys = new Set(makeView().entries.flatMap((item) => {
          const entry = availableGroupCodedEntry(item, sources);
          return entry ? [codedCategoryKey(entry.familyKey, item.system, item.code)] : [];
        }));
        if (visibleKeys.size >= INITIAL_VISIBLE_CODED_CHOICES) break;
        const pending = [...currentCheckpoint.families.values()].filter((family) => !family.exhausted);
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        publish();
      }
      if (signal.aborted) return makeView();
      currentCheckpoint.initialized = true;
      currentCheckpoint.loadedRound = pageRound;
    } else if (pageRound > currentCheckpoint.loadedRound) {
      for (let round = currentCheckpoint.loadedRound; round < pageRound && !signal.aborted; round += 1) {
        const pending = [...currentCheckpoint.families.values()].filter((family) => Boolean(family.cursor));
        if (pending.length === 0) break;
        await Promise.all(pending.map(fetchPage));
        if (signal.aborted) return makeView();
        currentCheckpoint.loadedRound = round + 1;
        publish();
      }
    }
    return makeView();
  }, [client, inventoryScopeKey, readiness?.contextToken, readiness?.buildId, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.truncated, sourceDiscovery?.loading, pageRound],
  inventoryReady && ((sourceDiscovery !== undefined && sources.length > 0) || sourceDiscovery?.complete === true));

  const inventoryView = inventoryQuery.data?.scopeKey === inventoryScopeKey && inventoryQuery.data.sourceKey === sourceKey
    ? inventoryQuery.data
    : inventoryProgress?.scopeKey === inventoryScopeKey && inventoryProgress.sourceKey === sourceKey ? inventoryProgress : undefined;
  const inventory = inventoryView?.entries ?? [];
  const available = useMemo(() => {
    const entries = new Map<string, NonNullable<ReturnType<typeof availableGroupCodedEntry>>>();
    for (const item of inventory) {
      const entry = availableGroupCodedEntry(item, sources);
      if (entry) entries.set(codedCategoryKey(entry.familyKey, item.system, item.code), entry);
    }
    return [...entries.values()];
  }, [inventory, sources]);

  const savedCategoryKey = props.form.codedCategories.filter((category) => category.choice === 'saved')
    .map((category) => category.key).join('\u0001');
  const savedCategoryQuery = useQuery(async (signal) => {
    const unresolved = currentForm.current.codedCategories.filter((category) => category.choice === 'saved');
    const hydrated = await Promise.all(unresolved.map(async (category) => {
      const source = sources.find((candidate) => candidate.resourceType === rowRoot
        && codedFamilyKey(candidate.bindingId, candidate.sourcePath) === category.familyKey);
      if (!source) return { key: category.key, value: undefined, pending: !sourceDiscovery?.complete };
      try {
        const response = await client.browseSemanticInventory({
          project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
          sourceChoiceId: source.choiceId, query: category.code, limit: 50,
        }, signal);
        if (response.state !== 'complete') {
          throw new Error('The saved coded value is not confirmed by a complete inventory. Wait for the catalog to be ready, then reopen the editor.');
        }
        if (response.contextToken !== readiness?.contextToken || response.buildId !== readiness?.buildId) {
          throw new Error('The coded-value inventory changed while it was loading. Reload the table and retry.');
        }
        const item = response.entries.find((candidate) => candidate.system === category.system && candidate.code === category.code
          && availableGroupCodedEntry(candidate, [source]));
        return { key: category.key, value: item?.constructionChoice ? {
          sourceChoiceId: source.choiceId,
          categoryChoiceId: item.constructionChoice.choiceId,
          display: semanticConceptLabel(item),
          valueType: item.valueType,
        } : undefined, pending: false };
      } catch (reason: unknown) {
        return {
          key: category.key,
          value: undefined,
          pending: false,
          error: reason instanceof Error ? reason.message : 'A saved coded value could not be reauthorized.',
        };
      }
    }));
    if (signal.aborted || currentInventoryScopeKey.current !== inventoryScopeKey) return { unavailableKeys: [], error: undefined };
    const byKey = new Map(hydrated.flatMap((item) => item.value ? [[item.key, item.value] as const] : []));
    const form = currentForm.current;
    const savedKeys = new Set(form.codedCategories.filter((category) => category.choice === 'saved').map((category) => category.key));
    let changed = false;
    const codedCategories = form.codedCategories.map((category): GroupCodedCategory => {
      if (category.choice !== 'saved') return category;
      const item = byKey.get(category.key);
      if (!item) return category;
      changed = true;
      return {
        ...category,
        choice: 'ready',
        sourceChoiceId: item.sourceChoiceId,
        categoryChoiceId: item.categoryChoiceId,
        display: item.display,
        valueType: item.valueType,
      };
    });
    if (changed) changeForm.current({ ...form, codedCategories });
    return {
      unavailableKeys: hydrated.filter((item) => savedKeys.has(item.key) && !item.value && !item.pending).map((item) => item.key),
      error: hydrated.find((item) => item.error)?.error,
    };
  }, [client, inventoryScopeKey, sourceKey, sourceDiscovery?.complete, sourceDiscovery?.loading, savedCategoryKey],
  inventoryReady && sourceDiscovery !== undefined && savedCategoryKey.length > 0);
  const unavailableSavedKeys = new Set(savedCategoryQuery.data?.unavailableKeys ?? []);

  const selectedFamily = props.form.codedCategories[0]?.familyKey;
  const removeSelectedCategory = (category: GroupCodedCategory) => {
    props.onChange({
      ...props.form,
      codedCategories: props.form.codedCategories.filter((candidate) => candidate.key !== category.key),
      keys: props.form.keys.filter((groupKey) => groupKey.outputColumnId !== category.groupOutputColumnId),
    });
  };
  const toggle = (entry: (typeof available)[number]) => {
    const { item, source, familyKey } = entry;
    const key = codedCategoryKey(familyKey, item.system, item.code);
    const prior = props.form.codedCategories.find((category) => category.key === key);
    if (prior) {
      removeSelectedCategory(prior);
      return;
    }
    if (selectedFamily && selectedFamily !== familyKey || props.form.codedCategories.length >= 50 || !item.constructionChoice) return;
    const label = semanticConceptLabel(item);
    const usedNames = getUsedGroupNames(props.form);
    const groupOutputName = uniqueName(item.code, usedNames);
    const helperOutputName = uniqueName(item.code, new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.codedCategories.map((category) => category.helperOutputName.toLowerCase()),
    ]));
    const groupKey: GroupKey = {
      inputColumnId: createOpaqueId('coded-group-input'),
      outputColumnId: createOpaqueId('coded-group-output'),
      name: groupOutputName,
      label,
    };
    const codedCategory: GroupCodedCategory = {
      choice: 'ready',
      key,
      familyKey,
      system: item.system,
      code: item.code,
      display: label,
      valueType: item.valueType,
      sourceChoiceId: source.choiceId,
      categoryChoiceId: item.constructionChoice.choiceId,
      helperOutputColumnId: groupKey.inputColumnId,
      groupOutputColumnId: groupKey.outputColumnId,
      groupOutputName: groupKey.name,
      groupOutputLabel: groupKey.label,
      helperOutputName,
      helperOutputLabel: label,
    };
    const codedCategories = [...props.form.codedCategories, codedCategory];
    const nextForm = { ...props.form, codedCategories, keys: [...props.form.keys, groupKey] };
    props.onChange({
      ...nextForm,
      codedPassthroughs: ensureGroupCodedPassthroughs(nextForm, props.stage, [
        ...nextForm.keys.flatMap((key) => codedCategories.some((category) => category.helperOutputColumnId === key.inputColumnId) ? [] : [key.inputColumnId]),
        ...nextForm.aggregates.flatMap((aggregate) => aggregate.operation === 'COUNT_ROWS' ? [] : [aggregate.inputColumnId]),
        ...nextForm.rowValues.flatMap((value) => 'inputColumnId' in value ? [value.inputColumnId] : []),
      ]),
    });
  };
  const loadNextPage = () => {
    if (!inventoryView?.hasMore || inventoryQuery.isLoading || pageRound !== requestedRoundRef.current) return;
    requestedRoundRef.current = pageRound + 1;
    setPageRound(pageRound + 1);
  };
  const onChoicesScroll = (event: React.UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    if (element.scrollHeight - element.scrollTop - element.clientHeight <= 24) loadNextPage();
  };
  const sourceListIncomplete = Boolean(sourceDiscovery && !sourceDiscovery.loading && (!sourceDiscovery.complete || sourceDiscovery.truncated));
  const error = readinessQuery.error ?? sourcesQuery.error ?? inventoryQuery.error ?? savedCategoryQuery.data?.error;
  const errorText = error instanceof Error ? error.message : error ? 'Coded values could not be loaded.' : undefined;
  const choicesLoading = readinessQuery.isLoading || sourcesQuery.isLoading || inventoryQuery.isLoading || savedCategoryQuery.isLoading;
  if (inventoryReady && !choicesLoading && !errorText && !sourceListIncomplete && inventoryView?.complete && available.length === 0
      && props.form.codedCategories.length === 0) return null;

  return (
    <div data-testid="construction-group-coded-values" className="grid gap-2 rounded-lg border border-blue-200 bg-blue-50/50 p-3">
      <div>
        <p className="m-0 text-sm font-semibold text-slate-800">Coded values</p>
        <p className="mb-0 mt-1 text-xs text-slate-600">Choose coded values to group by.</p>
      </div>
      {readinessQuery.isLoading && !readiness ? <p role="status" className="m-0 text-xs text-slate-600">Checking coded-value availability…</p> : null}
      {!readinessQuery.error && readiness && !inventoryReady ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {!readiness.buildId
            ? 'No current coded-value catalog is available.'
            : `The coded-value catalog is ${readiness.state}. Choices will appear when it is complete.`}
        </p>
      ) : null}
      {inventoryReady && choicesLoading ? <p role="status" className="m-0 text-xs text-slate-600">Loading coded values…</p> : null}
      {errorText ? <p role="alert" className="m-0 text-xs text-red-700">{errorText}</p> : null}
      {sourceListIncomplete && available.length > 0 ? <p role="status" className="m-0 text-xs text-amber-900">The direct source-field list may be incomplete; additional coded choices may exist.</p> : null}
      {selectedFamily && props.form.codedCategories.some((category) => category.familyKey !== selectedFamily) ? (
        <p role="status" className="m-0 text-xs text-amber-900">These selected concepts come from different source fields. A group can use coded values from one direct source field at a time; remove the conflicting selection to continue.</p>
      ) : selectedFamily ? (
        <p className="m-0 text-xs text-slate-600">Selected source field: {sources.find((source) => codedFamilyKey(source.bindingId, source.sourcePath) === selectedFamily)?.title ?? 'saved direct source'}. Choose coded values from this source field.</p>
      ) : null}
        <div
        aria-label="Coded value choices by direct source field"
        className="grid max-h-[min(14rem,45vh)] grid-cols-1 gap-3 overflow-y-auto"
        onScroll={onChoicesScroll}
        role="region"
        tabIndex={0}
      >
        {sources.map((source) => {
          const sourceEntries = available.filter((entry) => entry.source.choiceId === source.choiceId);
          if (sourceEntries.length === 0 && !inventoryView?.complete) return null;
          return (
            <section key={source.choiceId} aria-label={`Coded values from ${source.title}`} className="grid content-start gap-1">
              <h5 className="m-0 text-xs font-semibold text-slate-700">{source.title}</h5>
              {sourceEntries.map((entry) => {
                const { item, familyKey } = entry;
                const key = codedCategoryKey(familyKey, item.system, item.code);
                const selected = props.form.codedCategories.some((category) => category.key === key);
                const familyConflict = Boolean(selectedFamily && selectedFamily !== familyKey);
                return (
                  <label key={key} data-testid="construction-group-coded-option" className={`flex min-h-10 items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
                    <input
                      type="checkbox"
                      aria-label={`Group by coded value: ${semanticConceptLabel(item)}`}
                      checked={selected}
                      disabled={props.disabled || familyConflict || props.form.codedCategories.length >= 50 && !selected}
                      onChange={() => toggle(entry)}
                    />
                    <span><span className="block font-medium text-slate-800">{semanticConceptLabel(item)}</span><span className="block text-xs text-slate-500">{item.sourcePath} · {item.valueType} · {item.occurrences.toLocaleString()} observed</span></span>
                  </label>
                );
              })}
            </section>
          );
        })}
        {props.form.codedCategories.filter((category) => !available.some((entry) =>
          category.key === codedCategoryKey(entry.familyKey, entry.item.system, entry.item.code))).map((category) => {
          const familyConflict = Boolean(selectedFamily && selectedFamily !== category.familyKey);
          return (
            <label key={category.key} data-testid="construction-group-coded-option" className={`flex items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
              <input
                type="checkbox"
                aria-label={`Group by coded value: ${category.display}`}
                checked
                disabled={props.disabled}
                onChange={() => removeSelectedCategory(category)}
              />
              <span>
                <span className="block font-medium text-slate-800">{category.display}</span>
                <span className="block text-xs text-slate-500">{category.system} · {category.code} · selected</span>
                {unavailableSavedKeys.has(category.key) ? <span role="status" className="block text-xs text-amber-800">This coded value is no longer available. Remove it to continue.</span> : null}
              </span>
            </label>
          );
        })}
        {inventoryView?.hasMore ? <p className="m-0 text-xs text-slate-500">Scroll for more choices from these source fields.</p> : null}
      </div>
      {inventoryReady && !choicesLoading && !errorText && available.length === 0 ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {sourceListIncomplete
            ? 'No ready coded values were found among the listed direct source fields; the source list may be incomplete.'
            : inventoryView?.complete
              ? 'No ready coded values are available from these direct source fields.'
              : 'Coded values from direct source fields are still loading.'}
        </p>
      ) : null}
      {props.form.codedCategories.length > 0 ? <p className="m-0 text-xs text-slate-600">{props.form.codedCategories.length} coded value{props.form.codedCategories.length === 1 ? '' : 's'} selected.</p> : null}
    </div>
  );
};

const groupAggregateKindFromInput = (value: string): GroupAggregateKind | undefined => {
  switch (value) {
    case 'COUNT_ROWS': return 'COUNT_ROWS';
    case 'COUNT_NON_NULL': return 'COUNT_NON_NULL';
    case 'COUNT_DISTINCT': return 'COUNT_DISTINCT';
    case 'SUM': return 'SUM';
    case 'MEAN': return 'MEAN';
    default: return undefined;
  }
};

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

const pivotDuplicatePolicyFromInput = (value: string): PivotForm['duplicatePolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'SUM': return 'SUM';
    case 'MIN': return 'MIN';
    case 'MAX': return 'MAX';
    default: return undefined;
  }
};

const pivotMissingPolicyFromInput = (value: string): PivotForm['missingCellPolicy'] | undefined => {
  switch (value) {
    case 'NULL': return 'NULL';
    case 'ERROR': return 'ERROR';
    default: return undefined;
  }
};

const pivotUnlistedPolicyFromInput = (value: string): PivotForm['unlistedCategoryPolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'EXCLUDE_WITH_EVIDENCE': return 'EXCLUDE_WITH_EVIDENCE';
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

const PivotEditor = (props: {
  readonly sourceChoices: ReadonlyArray<SourceInputChoice>;
  readonly form: PivotForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly discovery?: ConstructionReshapePivotDiscovery;
  readonly discoveryAwaited: boolean;
  readonly requireEveryDiscoveredCategory: boolean;
  readonly canDiscover: boolean;
  readonly showRowSelection: boolean;
  readonly onDiscover: () => void;
  readonly onManualCategorySelection: (form: PivotForm) => void;
  readonly onForgetPairSelection: (previous: PivotForm, next: PivotForm) => void;
  readonly onChange: (form: PivotForm) => void;
}) => {
  const discoveryHelpId = useId();
  const [categorySearch, setCategorySearch] = useState('');
  const columns = scalarColumnsFor(props.stage);
  const repeatedColumns = props.stage.columns.filter((column) => column.cardinality === 'many');
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const discoveryMode = props.form.discovery !== undefined;
  const pairUnchanged = pivotOriginalPairApplies(props.form);
  const categoriesKnown = !discoveryMode && (pairUnchanged || discoveryComplete);
  const discoveryEligible = !discoveryMode
    && props.canDiscover
    && props.supported
    && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0)
    && props.form.categoryColumnId !== ''
    && props.form.valueColumnId !== ''
    && !categoriesKnown;
  const hostDiscoveryPending = discoveryEligible
    && discoveryMatches
    && props.discovery?.status === 'loading';
  const discoveryPending = hostDiscoveryPending || (props.discoveryAwaited && !categoriesKnown);
  const categoriesAvailable = discoveryComplete ? props.discovery.categories : [];
  const missingDiscoveredCategories = props.requireEveryDiscoveredCategory
    && discoveryComplete
    && !allDiscoveredPivotCategoriesSelected(props.form, props.stage.id, props.discovery);
  const listedKeys = new Set(categoriesAvailable.map((category) => scalarIdentity(category.key)));
  const existingNotListed = pairUnchanged
    ? props.form.categories.filter((category) => !listedKeys.has(scalarIdentity(category.key)))
    : [];
  const categorySearchTerm = categorySearch.trim().toLowerCase();
  const matchesCategorySearch = (label: string, key: ConstructionTableScalar): boolean =>
    `${label} ${scalarLabel(key)}`.toLowerCase().includes(categorySearchTerm);
  const shownCategories = categoriesAvailable.filter((category) => matchesCategorySearch(category.label, category.key));
  const shownExistingNotListed = existingNotListed.filter((category) => matchesCategorySearch(scalarLabel(category.key), category.key));
  const selectedCategoryIdentities = new Set(props.form.categories.map((category) => scalarIdentity(category.key)));
  const selectedCategoryLabels = props.form.categories.map((category) =>
    categoriesAvailable.find((available) => scalarIdentity(available.key) === scalarIdentity(category.key))?.label
      ?? scalarLabel(category.key),
  );
  const displayedSelectedCategoryLabels = selectedCategoryLabels.slice(0, 3);
  const additionalSelectedCategoryCount = selectedCategoryLabels.length - displayedSelectedCategoryLabels.length;
  const showSelectedCategoryLabels = selectedCategoryLabels.length <= 10;
  const shownCategoryIdentities = new Set([
    ...shownCategories.map((category) => scalarIdentity(category.key)),
    ...shownExistingNotListed.map((category) => scalarIdentity(category.key)),
  ]);
  const hasUnselectedShownCategory = shownCategories.some((category) => !selectedCategoryIdentities.has(scalarIdentity(category.key)));
  const hasSelectedShownCategory = props.form.categories.some((category) => shownCategoryIdentities.has(scalarIdentity(category.key)));
  const shownCategoryCount = shownCategories.length + shownExistingNotListed.length;
  const categoryCount = categoriesAvailable.length + existingNotListed.length;
  const groupColumns = props.form.groupKeyIds.flatMap((id) => {
    const column = columns.find((candidate) => candidate.id === id);
    return column ? [column] : [];
  });
  const sourceChoiceForColumn = (columnId: string): SourceInputChoice | undefined => {
    const binding = props.form.pivotSources?.find((candidate) => candidate.columnId === columnId);
    return binding ? props.sourceChoices.find((candidate) => candidate.choiceId === binding.choiceId) : undefined;
  };
  const pivotColumnLabel = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice ? pivotSourceLabel(sourceChoice) : column.label;
  };
  const pivotColumnTitle = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice?.fieldPath ?? (column.name === column.label ? column.name : `${column.name} · ${column.label}`);
  };
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const numericValue = isNumericColumn(columns.find((column) => column.id === props.form.valueColumnId) ?? { id: '', name: '', label: '', cardinality: 'optional_one' });
  const categoryColumn = columns.find((column) => column.id === props.form.categoryColumnId);
  const selectableValueColumns = columns.filter((column) => !props.form.groupKeyIds.includes(column.id) && column.id !== props.form.categoryColumnId);
  const valueColumn = columns.find((column) => column.id === props.form.valueColumnId);
  const categoryColumnLabel = categoryColumn ? pivotColumnLabel(categoryColumn) : 'Column names';
  const valueColumnLabel = valueColumn ? pivotColumnLabel(valueColumn) : 'Cell values';
  const rowColumnLabels = groupColumns.map(pivotColumnLabel);
  const roleSummary = rowColumnLabels.length > 0 && categoryColumn && valueColumn
    ? `One row per ${rowColumnLabels.join(', ')}. Each ${categoryColumnLabel} value creates a column containing ${valueColumnLabel}.`
    : 'Example: One row per Patient ID. Each Visit type value creates a column containing Score.';

  const [columnFilter, setColumnFilter] = useState('');
  const chooseInput = (role: 'group' | 'category' | 'value', selected: string) => {
    if (role === 'group' && selected === '') return;
    if (role === 'category' || role === 'value') {
      setCategorySearch('');
      setColumnFilter('');
    }
    const choice = props.sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
    const existing = choice && props.form.pivotSources?.find((binding) => binding.choiceId === choice.choiceId);
    const columnId = choice ? existing?.columnId ?? createOpaqueId('pivot-input') : selected;
    if ((role === 'category' && columnId === props.form.categoryColumnId)
      || (role === 'value' && columnId === props.form.valueColumnId)) return;
    const bindings = choice && !existing ? [...(props.form.pivotSources ?? []), { choiceId: choice.choiceId, columnId }] : props.form.pivotSources ?? [];
    const nextValueColumnId = role === 'value' ? columnId : role === 'category' && columnId === props.form.valueColumnId ? '' : props.form.valueColumnId;
    const nextValueColumn = columns.find((column) => column.id === nextValueColumnId);
    const duplicatePolicy = !props.form.originalPair && role === 'value'
      ? (isNumericColumn(nextValueColumn ?? { id: '', name: '', label: '', cardinality: 'optional_one' }) ? 'SUM' : 'MIN')
      : props.form.duplicatePolicy;
    const preserveCategories = role === 'group';
    const next = {
      ...props.form,
      groupKeyIds: role === 'group' ? [...new Set([...props.form.groupKeyIds, columnId])] : props.form.groupKeyIds,
      categoryColumnId: role === 'category' ? columnId : props.form.categoryColumnId,
      valueColumnId: nextValueColumnId,
      duplicatePolicy,
      categories: preserveCategories ? props.form.categories : [],
      categoriesPair: preserveCategories ? props.form.categoriesPair : undefined,
      originalPair: preserveCategories ? props.form.originalPair : undefined,
    };
    const used = new Set([...next.groupKeyIds, next.categoryColumnId, next.valueColumnId]);
    const nextForm = { ...next, pivotSources: bindings.filter((binding) => used.has(binding.columnId)) };
    if (!preserveCategories) props.onForgetPairSelection(props.form, nextForm);
    props.onChange(nextForm);
  };
  const columnFilterText = columnFilter.trim().toLowerCase();
  const filteredGroupColumns = columns.filter((column) => !columnFilterText || column.label.toLowerCase().includes(columnFilterText) || (column.type ?? '').toLowerCase().includes(columnFilterText));

  const toggleGroup = (columnId: string, checked: boolean) => {
    const groupKeyIds = checked
      ? [...props.form.groupKeyIds, columnId]
      : props.form.groupKeyIds.filter((id) => id !== columnId);
    const used = new Set([...groupKeyIds, props.form.categoryColumnId, props.form.valueColumnId]);
    props.onChange({ ...props.form, groupKeyIds, pivotSources: props.form.pivotSources?.filter((binding) => used.has(binding.columnId)) });
  };

  const sourceOptions = props.sourceChoices.filter((choice) => choice.isPopulated && !props.form.pivotSources?.some((binding) => binding.choiceId === choice.choiceId));
  const updateCategory = (identity: string, update: Partial<PivotCategoryForm>) => {
    props.onChange({
      ...props.form,
      categories: props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category, ...update } : category),
    });
  };

  const toggleCategory = (available: ConstructionReshapePivotCategory, checked: boolean) => {
    const identity = scalarIdentity(available.key);
    const existing = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
    const categories = checked
      ? existing
        ? props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category } : category)
        : [...props.form.categories, createPivotCategoryForm(available, new Set([
            ...props.stage.columns.map((column) => column.name.toLowerCase()),
            ...props.form.categories.map((category) => category.name.toLowerCase()),
          ]))]
      : props.form.categories.filter((category) => scalarIdentity(category.key) !== identity);
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const selectShownCategories = () => {
    const selectedIdentities = new Set(selectedCategoryIdentities);
    const categories = [...props.form.categories];
    const usedNames = new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.categories.map((category) => category.name.toLowerCase()),
    ]);
    for (const available of shownCategories) {
      const identity = scalarIdentity(available.key);
      if (selectedIdentities.has(identity)) continue;
      const category = createPivotCategoryForm(available, usedNames);
      categories.push(category);
      selectedIdentities.add(identity);
      usedNames.add(category.name.toLowerCase());
    }
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const clearShownCategories = () => {
    const categories = props.form.categories.filter((category) => !shownCategoryIdentities.has(scalarIdentity(category.key)));
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  return (
    <section aria-label="Pivot categories into columns" data-testid="construction-reshape-pivot" className="construction-reshape-pivot-editor grid content-start gap-3">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 && props.sourceChoices.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned scalar fields for this stage.</p>
      ) : (
        <>
          {props.showRowSelection ? (
          <fieldset className="grid min-w-0 content-start gap-3" disabled={props.disabled || !props.supported}>
            <legend className="m-0 text-sm font-semibold text-slate-900">One row per</legend>
            <p className="m-0 text-xs text-slate-600">These fields identify each output row. Each category value becomes a new column.</p>
            <div className="grid gap-2">
              <label className="grid gap-1 text-xs font-medium text-slate-700">
                Search fields
                <input aria-label="Search pivot row fields" value={columnFilter} onChange={(event) => setColumnFilter(event.currentTarget.value)} placeholder="Search columns" className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm" />
              </label>
              <div className="grid max-h-72 gap-x-3 overflow-y-auto sm:grid-cols-2">
                {filteredGroupColumns.map((column) => {
                  const label = pivotColumnLabel(column);
                  return (
                    <label key={column.id} title={pivotColumnTitle(column)} className="flex min-w-0 items-start gap-2 border-b border-slate-100 px-3 py-2 text-sm text-slate-700 last:border-b-0 hover:bg-slate-50">
                      <input
                        type="checkbox"
                        aria-label={`Pivot group ${label}`}
                        checked={props.form.groupKeyIds.includes(column.id)}
                        disabled={props.disabled || !props.supported || column.id === props.form.categoryColumnId || column.id === props.form.valueColumnId}
                        onChange={(event) => toggleGroup(column.id, event.currentTarget.checked)}
                      />
                      <span className="min-w-0 break-words">{label}</span>
                    </label>
                  );
                })}
                {filteredGroupColumns.length === 0 ? <p className="px-1 text-xs text-slate-500">{columns.length === 0 ? 'No columns are available.' : 'No columns match this search.'}</p> : null}
              </div>
              {sourceOptions.length > 0 ? <select aria-label="Add pivot group field" value="" disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('group', event.currentTarget.value)} className="justify-self-start rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Add another field</option>
                {sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}
              </select> : null}
              {props.form.groupKeyIds.length === 0 && props.supported ? <p role="status" className="m-0 text-xs text-amber-900">Choose one or more fields to identify each row{props.form.pivotSources?.length ? '. Choose a row identity before finding categories from source fields.' : '.'}</p> : null}
            </div>
          </fieldset>
          ) : null}
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
              New column names
              <select aria-label="Pivot category field" value={props.form.categoryColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('category', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                <option value="">Choose a column-name field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id)).map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)}</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}</optgroup> : null}
              </select>
            </label>
            <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
              Cell values
              <select aria-label="Pivot values field" value={props.form.valueColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('value', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                <option value="">Choose a cell-values field</option>
                {selectableValueColumns.map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)} ({column.type ?? 'unknown type'})</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)} ({choice.logicalType})</option>)}</optgroup> : null}
              </select>
            </label>
          </div>
          <p data-testid="construction-reshape-pivot-role-summary" className="text-sm text-slate-600">
            {roleSummary}
          </p>
          <div className="grid gap-1 rounded border border-slate-200 p-3">
            <label className="flex items-start gap-2 text-sm font-medium text-slate-800">
              <input
                type="checkbox"
                aria-label="Discover categories when proposing"
                aria-describedby={discoveryHelpId}
                checked={discoveryMode}
                disabled={props.disabled || !props.supported}
                onChange={(event) => props.onChange({
                  ...props.form,
                  discovery: event.currentTarget.checked ? { maxCategories: PIVOT_DISCOVERY_MAX_CATEGORIES } : undefined,
                  categories: event.currentTarget.checked ? [] : props.form.categories,
                  categoriesPair: event.currentTarget.checked ? undefined : props.form.categoriesPair,
                  originalPair: event.currentTarget.checked ? undefined : props.form.originalPair,
                })}
              />
              <span>Discover categories when proposing</span>
            </label>
            <p id={discoveryHelpId} className="m-0 text-xs text-slate-600">
              {discoveryMode
                ? `Categories are discovered when this pivot is proposed, up to ${props.form.discovery?.maxCategories ?? PIVOT_DISCOVERY_MAX_CATEGORIES}. They freeze at that point, so turn discovery on and propose the pivot again after the data changes. Manual category selection is unavailable while this is on.`
                : 'Choose categories manually from the values found in New column names. To refresh them from current data, turn on discovery and propose the pivot again.'}
            </p>
          </div>
          {repeatedColumns.length > 0 ? (
            <details className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-950">
              <summary className="cursor-pointer">{repeatedColumns.length} {repeatedColumns.length === 1 ? 'column contains a list' : 'columns contain lists'}</summary>
              <p className="mt-1">Use “Make one row per list value” to expand these columns before choosing Pivot fields.</p>
            </details>
          ) : null}
          {!discoveryMode && !props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery is unavailable for this stage.</p> : null}
          {!discoveryMode && props.canDiscover && props.supported && !categoriesKnown && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0) && (props.form.categoryColumnId === '' || props.form.valueColumnId === '') ? <p role="status" className="text-sm text-slate-600">Choose fields for New column names and Cell values to find categories.</p> : null}
          {discoveryPending ? (
            <div
              role="status"
              aria-live="polite"
              aria-busy="true"
              data-testid="construction-reshape-pivot-discovery-status"
              data-stage-id={props.stage.id}
              data-category-column-id={props.form.categoryColumnId}
              data-value-column-id={props.form.valueColumnId}
              className="grid gap-1 rounded bg-slate-50 px-3 py-2 text-sm text-slate-600"
            >
              <p className="m-0">Finding categories…</p>
              <p className="m-0 text-xs">
                Searching category values for New column names “{categoryColumnLabel}” and Cell values “{valueColumnLabel}”. Your entries stay as they are.
              </p>
            </div>
          ) : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'failed' ? <div role="status" className="text-sm text-amber-900"><p>{props.discovery.reason}</p><button type="button" onClick={props.onDiscover} disabled={props.disabled || !discoveryEligible} className="mt-1 text-blue-800 underline">Retry finding categories</button></div> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'limit-exceeded' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason} (Maximum supported categories: {props.discovery.limit}.)</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'missing-unsupported' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'no-values' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {categoriesKnown ? (
            <>
              <p role="status" data-testid="construction-reshape-pivot-category-summary" className="rounded bg-slate-50 px-3 py-2 text-sm text-slate-700">
                Selected {selectedCategoryLabels.length} of {categoryCount} categories{selectedCategoryLabels.length === 0 ? ': none' : showSelectedCategoryLabels ? `: ${displayedSelectedCategoryLabels.join(', ')}${additionalSelectedCategoryCount > 0 ? `, and ${additionalSelectedCategoryCount} more` : ''}` : '. Review the list to change them.'}
              </p>
              {discoveryComplete && categoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair. Choose another New column names field or adjust the rows before continuing.</p> : null}
              {missingDiscoveredCategories ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Select every discovered category, or filter rows before pivoting.</p> : null}
              <p role="status" aria-live="polite" data-testid="construction-reshape-pivot-category-status" className="text-sm text-slate-600">
                {props.form.categories.length} selected · Showing {shownCategoryCount} of {categoryCount} category values
              </p>
              <details data-testid="construction-reshape-pivot-categories" className="rounded border border-slate-200 p-3">
                <summary className="cursor-pointer text-sm font-semibold text-slate-800">Change selected categories</summary>
                <fieldset className="mt-3 grid gap-3" disabled={props.disabled || !props.supported}>
                  <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-end">
                    <label className="grid gap-1 text-sm font-medium text-slate-700">
                      Search category values
                      <input
                        type="search"
                        aria-label="Search category values"
                        value={categorySearch}
                        onChange={(event) => setCategorySearch(event.currentTarget.value)}
                        placeholder="Search by label or value"
                        className="rounded border border-slate-300 bg-white px-2 py-1.5"
                      />
                    </label>
                    <button
                      type="button"
                      aria-label="Select shown categories"
                      disabled={props.disabled || !props.supported || !hasUnselectedShownCategory}
                      onClick={selectShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Select shown categories
                    </button>
                    <button
                      type="button"
                      aria-label="Clear shown categories"
                      disabled={props.disabled || !props.supported || !hasSelectedShownCategory}
                      onClick={clearShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Clear shown categories
                    </button>
                  </div>
                  {shownCategories.map((available) => {
                    const identity = scalarIdentity(available.key);
                    const current = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Include category ${available.label}`} checked={Boolean(current)} disabled={props.disabled || !props.supported} onChange={(event) => toggleCategory(available, event.currentTarget.checked)} />
                          {available.label}
                        </label>
                      </div>
                    );
                  })}
                  {shownExistingNotListed.map((category) => {
                    const identity = scalarIdentity(category.key);
                    return (
                      <div key={identity} className={`grid gap-2 rounded p-2 ${discoveryComplete ? 'border border-amber-200 bg-amber-50' : 'bg-slate-50'}`}>
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Keep saved category ${scalarLabel(category.key)}`} checked disabled={props.disabled || !props.supported} onChange={(event) => { if (!event.currentTarget.checked) toggleCategory({ key: category.key, label: scalarLabel(category.key) }, false); }} />
                          {scalarLabel(category.key)} {discoveryComplete ? <span className="font-normal text-amber-900">Not found in the latest category list</span> : null}
                        </label>
                      </div>
                    );
                  })}
                  {categoriesAvailable.length === 0 && existingNotListed.length === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair.</p> : null}
                  {categoryCount > 0 && shownCategoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories match this search.</p> : null}
                </fieldset>
              </details>
            </>
          ) : null}
          <p role="status" data-testid="construction-reshape-pivot-policy-summary" className="text-sm text-slate-600">
            Duplicate values: {pivotDuplicateEffect(props.form.duplicatePolicy)}. Empty cells: {pivotMissingCellEffect(props.form.missingCellPolicy)}. Unselected categories: {pivotUnlistedCategoryEffect(props.form.unlistedCategoryPolicy)}.
          </p>
          <details data-testid="construction-reshape-pivot-advanced" className="rounded border border-slate-200 p-3">
            <summary className="cursor-pointer text-sm font-semibold text-slate-800">Advanced settings</summary>
            <div className="mt-3 grid gap-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has duplicate values
                  <select aria-label="Pivot duplicate policy" value={props.form.duplicatePolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotDuplicatePolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, duplicatePolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="SUM" disabled={!numericValue}>Add them together</option>
                    <option value="MIN">Keep the smallest</option>
                    <option value="MAX">Keep the largest</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has no value
                  <select aria-label="Pivot missing cell policy" value={props.form.missingCellPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotMissingPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, missingCellPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="NULL">Leave the new cell empty</option>
                    <option value="ERROR">Stop with an error</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a row has an unselected category
                  <select aria-label="Pivot unlisted category policy" value={props.form.unlistedCategoryPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotUnlistedPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, unlistedCategoryPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="EXCLUDE_WITH_EVIDENCE" disabled>Skip it and report it</option>
                  </select>
                </label>
              </div>
              <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">
                New pivots require every category present in the rows to be selected. Skipping unselected categories is unavailable.
              </p>
              {props.form.categories.length > 0 ? (
                <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
                  <legend className="px-1 text-sm font-semibold text-slate-800">Output column names and labels</legend>
                  {props.form.categories.map((category) => {
                    const identity = scalarIdentity(category.key);
                    const label = categoriesAvailable.find((available) => scalarIdentity(available.key) === identity)?.label
                      ?? scalarLabel(category.key);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <p className="text-sm font-medium text-slate-700">{label}</p>
                        <PivotCategoryOutput category={category} disabled={props.disabled || !props.supported} onChange={(update) => updateCategory(identity, update)} />
                      </div>
                    );
                  })}
                </fieldset>
              ) : null}
              {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give each output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
            </div>
          </details>
        </>
      )}
    </section>
  );
};

const pivotDuplicateEffect = (policy: PivotForm['duplicatePolicy']): string => {
  switch (policy) {
    case 'ERROR': return 'stop the pivot with an error';
    case 'SUM': return 'be added together';
    case 'MIN': return 'keep the smallest value';
    case 'MAX': return 'keep the largest value';
    default: {
      const exhaustive: never = policy;
      return exhaustive;
    }
  }
};

const pivotMissingCellEffect = (policy: PivotForm['missingCellPolicy']): string =>
  policy === 'NULL' ? 'stay empty' : 'stop the pivot with an error';

const pivotUnlistedCategoryEffect = (policy: PivotForm['unlistedCategoryPolicy']): string =>
  policy === 'ERROR' ? 'stop the pivot with an error' : 'be skipped and reported';

const PivotCategoryOutput = (props: {
  readonly category: PivotCategoryForm;
  readonly disabled: boolean;
  readonly onChange: (update: Partial<PivotCategoryForm>) => void;
}) => (
  <div className="grid gap-2 sm:grid-cols-2">
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column name
      <input aria-label={`Pivot output name ${scalarLabel(props.category.key)}`} value={props.category.name} disabled={props.disabled} onChange={(event) => props.onChange({ name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column label
      <input aria-label={`Pivot output label ${scalarLabel(props.category.key)}`} value={props.category.label} disabled={props.disabled} onChange={(event) => props.onChange({ label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
  </div>
);

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
