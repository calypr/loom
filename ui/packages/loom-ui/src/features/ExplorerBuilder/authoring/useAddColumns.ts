import { useMemo, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type React from 'react';
import type { useLoomClient } from '../../../react';
import type { Construction, ConstructionStep, ConstructionChoiceSelection, ConstructionCapabilitiesResponse, ExplorerBuilderCommand } from '../../../types';
import type { BuilderAuthoringState, DraftTable } from './model';
import { selectedTable } from './model';
import { admitAuthoringRequest } from './admission';
import { catalogSourceOptions, type CatalogChoiceIntent } from '../catalogItems';
import { constructionAppendStageFor } from '../constructionWorkspace/constructionStages';
import { relatedSourceOutputLabel } from '../constructionWorkspace/relatedSourceOutputLabel';
import { sameConstructionRoute } from '../populationRoutes';
import { hasPairedPreviewRows } from './workspaceGuards';
import { useBrowserSubscription } from '../hooks/useBrowserSubscription';
import type { ConceptCatalogView } from '../components/ConceptCatalog';
import type { ConstructionHistorySelection, ConstructionOperationFamily } from '../constructionWorkspace/ConstructionWorkspace';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import type { useCommandQueue } from './useCommandQueue';
import type { useCombineCatalog } from './useCombineCatalog';
import type { PreviewLimit } from './previewRecovery';
import type { AutomaticPreviewIdentity } from '../useAutomaticPreview';
import type { AppliedPreviewHandoff } from '../constructionWorkspace/appliedChoicePreview';

type UseAddColumnsOptions = {
  readonly state: BuilderAuthoringState;
  readonly table: DraftTable | undefined;
  readonly selectedRouteContext: { readonly occurrenceId: string; readonly nodeId: string } | undefined;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'capabilities' | 'beginApply' | 'cancel' | 'proposeCandidateAndWait'>;
  readonly hasUnsupportedSavedSourceColumns: boolean;
  readonly editingConstructionStep: ConstructionStep | undefined;
  readonly activeConstructionFamily: ConstructionOperationFamily | undefined;
  readonly ownerKey: string;
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly previewLimit: PreviewLimit;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly loomClient: ReturnType<typeof useLoomClient>;
  readonly sourceStageDescriptors: ConstructionCapabilitiesResponse['stages'] | undefined;
  readonly rowValuePolicy: NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;
  readonly cancelingConstructionProposalRef: MutableRefObject<boolean>;
  readonly appliedChoicePreview: MutableRefObject<AppliedPreviewHandoff | undefined>;
  readonly automaticPreviewRequest: MutableRefObject<AutomaticPreviewIdentity | undefined>;
  readonly applyCommandsWithResult: ReturnType<typeof useCommandQueue>['applyCommandsWithResult'];
  readonly combineCatalog: Pick<ReturnType<typeof useCombineCatalog>, 'cancel'>;
  readonly setActiveConstructionFamily: (next: undefined) => void;
  readonly setConstructionHistorySelection: Dispatch<SetStateAction<ConstructionHistorySelection>>;
  readonly setEditingConstructionStepId: Dispatch<SetStateAction<string | undefined>>;
  readonly setRowIntentSession: Dispatch<SetStateAction<boolean>>;
  readonly opaqueId: (prefix: 'step') => string;
};

export const useAddColumns = ({
  state,
  table,
  selectedRouteContext,
  constructionLifecycle,
  hasUnsupportedSavedSourceColumns,
  editingConstructionStep,
  activeConstructionFamily,
  ownerKey,
  projectId,
  authResourcePath,
  previewLimit,
  latestState,
  loomClient,
  sourceStageDescriptors,
  rowValuePolicy,
  cancelingConstructionProposalRef,
  appliedChoicePreview,
  automaticPreviewRequest,
  applyCommandsWithResult,
  combineCatalog,
  setActiveConstructionFamily,
  setConstructionHistorySelection,
  setEditingConstructionStepId,
  setRowIntentSession,
  opaqueId,
}: UseAddColumnsOptions) => {
  const addColumnsRequest = useRef<AbortController | undefined>(undefined);
  const [addColumnsView, setAddColumnsView] = useState<Exclude<ConceptCatalogView, 'all'>>('fields');
  const [addColumnsViewRevision, setAddColumnsViewRevision] = useState(0);
  const [addColumnsSource, setAddColumnsSource] = useState<{
    readonly context: string;
    readonly key: string;
  }>();

  useBrowserSubscription((_onStoreChange) => () => {
    addColumnsRequest.current?.abort();
    addColumnsRequest.current = undefined;
  }, [state.explorerId, table?.outputId, state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);
  const addSourceOptions = useMemo(() => {
    const sources = catalogSourceOptions(
      state.catalog,
      table?.document.rootResourceType ?? '',
      selectedRouteContext?.nodeId,
    );
    const current = constructionLifecycle.capabilities;
    const currentStage = current.status === 'ready' ? current.response.selectedStage : undefined;
    const appendStageIsCurrent = current.status === 'ready' && currentStage?.id ===
      constructionAppendStageFor(current.response.baseConstruction, table?.document.rows);
    const activeRelatedRecord = current.status === 'ready' &&
      !hasUnsupportedSavedSourceColumns && !editingConstructionStep && appendStageIsCurrent &&
      currentStage?.capabilities.some((capability) => capability.kind === 'RELATED_FIELD' && capability.supported)
      ? currentStage.activeRelatedRecord
      : undefined;
    const relatedSourceSupported = !hasUnsupportedSavedSourceColumns && !editingConstructionStep && appendStageIsCurrent &&
      currentStage?.rowIdentityColumn &&
      currentStage.capabilities.some((capability) => capability.kind === 'RELATED_SOURCE' && capability.supported);
    const visibleSources = addColumnsView === 'fields' && !relatedSourceSupported
      ? sources.filter((source) => source.kind === 'ROOT')
      : sources;
    const exactRelatedOption = addColumnsView === 'fields' && activeRelatedRecord
      ? {
          kind: 'EXACT_RELATED' as const,
          key: `exact:${currentStage?.id}:${activeRelatedRecord.targetNodeId}`,
          label: `${activeRelatedRecord.targetResourceType} — current related record`,
          resourceType: activeRelatedRecord.targetResourceType,
          sourceNodeId: undefined,
        }
      : undefined;
    const resourceSources = exactRelatedOption
      ? visibleSources.filter((source) =>
          source.kind !== 'RELATED' || source.sourceNodeId !== activeRelatedRecord?.targetNodeId)
      : visibleSources;
    const searchableSources = [{ kind: 'ALL' as const, key: 'all', label: 'All accessible resources' }, ...resourceSources];
    return exactRelatedOption ? [...searchableSources, exactRelatedOption] : searchableSources;
  }, [state.catalog, table?.document.rootResourceType, table?.document.rows, selectedRouteContext?.nodeId, constructionLifecycle.capabilities, addColumnsView, hasUnsupportedSavedSourceColumns, editingConstructionStep]);
  const addSourceContext = JSON.stringify([
    state.explorerId,
    state.catalog.snapshotToken,
    table?.outputId ?? '',
    selectedRouteContext?.occurrenceId ?? '',
  ]);
  const storedAddSourceKey = addColumnsSource?.context === addSourceContext ? addColumnsSource.key : undefined;
  const selectedAddSource = addSourceOptions.find(
    (source) => source.kind !== 'ALL' && source.key === storedAddSourceKey,
  ) ?? addSourceOptions.find((option) => option.kind === 'ROOT') ?? addSourceOptions[0];
  const concreteAddSources = addSourceOptions.filter((source) => source.kind !== 'ALL');
  const relatedSourceTypeCounts = addSourceOptions.reduce((counts, source) => {
    if (source.kind === 'RELATED') {
      counts.set(source.resourceType, (counts.get(source.resourceType) ?? 0) + 1);
    }
    return counts;
  }, new Map<string, number>());
  const addSourceOptionLabel = (source: (typeof concreteAddSources)[number]) =>
    source.kind === 'RELATED' && (relatedSourceTypeCounts.get(source.resourceType) ?? 0) > 1
      ? `${source.label} (${source.sourceNodeId})`
      : source.label;
  const selectAddColumnsView = (view: Exclude<ConceptCatalogView, 'all'>) => {
    if (view === 'coded' && selectedAddSource?.kind === 'EXACT_RELATED') {
      const rootSource = addSourceOptions.find((source) => source.kind === 'ROOT');
      if (rootSource) setAddColumnsSource({ context: addSourceContext, key: rootSource.key });
    }
    if (addColumnsView !== view) setAddColumnsViewRevision((revision) => revision + 1);
    setAddColumnsView(view);
  };
  const handleAddColumnsTabKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const nextView = event.currentTarget.dataset.view === 'coded' ? 'fields' : 'coded';
    selectAddColumnsView(nextView);
    event.currentTarget.parentElement
      ?.querySelector<HTMLButtonElement>(`[data-view="${nextView}"]`)
      ?.focus();
  };
  useBrowserSubscription((_onStoreChange) => () => {
    addColumnsRequest.current?.abort();
    addColumnsRequest.current = undefined;
  }, [selectedAddSource?.key, activeConstructionFamily]);
  const addSourceRouteContext = selectedAddSource?.sourceNodeId !== undefined && selectedAddSource.sourceNodeId === selectedRouteContext?.nodeId
    ? selectedRouteContext
    : undefined;
  const buildRelatedSourceCandidate = (
    selection: CatalogChoiceIntent,
    previous?: { readonly construction: Construction; readonly step: ConstructionStep },
  ) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const relatedSource = selection.relatedSource;
    if (!relatedSource) throw new Error('Choose a related field first.');
    const capabilities = constructionLifecycle.capabilities;
    if (capabilities.status !== 'ready') {
      throw new Error('Loom is still checking whether this stage can add a related field.');
    }
    const { selectedStage, baseConstruction } = capabilities.response;
    if (
      editingConstructionStep ||
      selectedStage.id !== constructionAppendStageFor(baseConstruction, table?.document.rows)
    ) {
      throw new Error(
        'Close the saved-step editor before adding a related field; proposals must follow the current final step.',
      );
    }
    const support = selectedStage.capabilities.find(
      (capability) => capability.kind === 'RELATED_SOURCE',
    );
    if (!support?.supported || !selectedStage.rowIdentityColumn) {
      throw new Error(
        support?.reason || 'Loom has not proved that this stage retains a source row anchor.',
      );
    }
    const { choice, candidate } = relatedSource;
    const source = choice.source;
    const form = selection.constructionChoice.form;
    const contributorPredicate = selection.contributorPredicate;
    const supportedForm = choice.options.find((option) =>
      option.form === form &&
      option.support === 'SUPPORTED',
    );
    if (
      source.kind !== 'FIELD' ||
      source.candidateId !== candidate.candidateId ||
      source.nodeId !== candidate.nodeId ||
      source.path !== candidate.fieldPath ||
      source.cardinality !== candidate.cardinality ||
      (source.resourceType === table.document.rootResourceType && choice.route.length === 0) ||
      !supportedForm ||
      (form !== 'ALL' && form !== 'COUNT' && form !== 'PRESENCE') ||
      (source.cardinality !== 'optional_one' && source.cardinality !== 'required_one')
    ) {
      throw new Error('Loom did not provide a supported scalar related field choice for this stage.');
    }

    const stepId = opaqueId('step');
    const outputColumnId = `related-${window.crypto.randomUUID()}`;
    const baseName = (form === 'COUNT'
      ? `related_${source.resourceType}_count`
      : form === 'PRESENCE'
        ? `has_related_${source.resourceType}`
        : `related_${source.resourceType}_${source.path}`)
      .replace(/[^A-Za-z0-9_]/g, '_')
      .replace(/_+/g, '_')
      .replace(/^([0-9])/, '_$1');
    const previousColumns = previous?.step.outputs ?? selectedStage.columns.map((column) => ({
      id: column.id,
      name: column.name,
      label: column.label,
      ...(column.type === undefined ? {} : { type: column.type }),
    }));
    const usedNames = new Set(previousColumns.map((column) => column.name.toLowerCase()));
    let outputName = baseName;
    for (let suffix = 2; usedNames.has(outputName.toLowerCase()); suffix += 1) {
      outputName = `${baseName}_${suffix}`;
    }
    const previousStepId = previous?.step.id ?? (selectedStage.operation ? selectedStage.id : undefined);
    const step = {
      id: stepId,
      inputs: [previousStepId
        ? { kind: 'STEP_OUTPUT' as const, stepId: previousStepId }
        : { kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'RELATED_SOURCE',
        relatedSource: {
          anchorColumnId: selectedStage.rowIdentityColumn,
          choiceId: choice.choiceId,
          sourceOccurrenceId: source.nodeId,
          source: {
            kind: 'FIELD',
            candidateId: source.candidateId,
            nodeId: source.nodeId,
            resourceType: source.resourceType,
            path: source.path,
            cardinality: source.cardinality,
            logicalType: candidate.logicalType,
          },
          route: choice.route,
          contributorRule: {
            policy: 'ALL_MATCHES',
            ...(contributorPredicate
              ? { predicate: contributorPredicate }
              : {}),
          },
          form,
          ...(form === 'ALL' && selection.constructionChoice.rowValuePolicy
            ? { rowValuePolicy: selection.constructionChoice.rowValuePolicy }
            : {}),
          outputColumnId,
        },
      },
      outputs: [
        ...previousColumns,
        {
          id: outputColumnId,
          name: outputName,
          label: relatedSourceOutputLabel(
            source.resourceType, candidate.fieldPath, candidate.label, form, contributorPredicate,
          ),
          type: form === 'COUNT' ? 'integer' : form === 'PRESENCE' ? 'boolean' : candidate.logicalType,
        },
      ],
    } satisfies ConstructionStep;
    const currentConstruction = previous?.construction ?? baseConstruction;
    return {
      intent: {
        candidateConstruction: {
          ...currentConstruction,
          steps: [...currentConstruction.steps, step],
        },
        changedStepId: step.id,
      },
      step,
      outputColumnName: outputName,
    };
  };
  const inspectRelatedRouteCoverage = async (selection: CatalogChoiceIntent, signal: AbortSignal) => {
    const relatedCandidate = selection.relatedSource ? buildRelatedSourceCandidate(selection) : undefined;
    if (selection.constructionChoice.form === 'ALL' && !relatedCandidate) {
      const current = latestState.current;
      if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {
        throw new Error('The current table is still loading.');
      }
      const admission = admitAuthoringRequest(current.tables, { kind: 'add-columns-proposal', outputId: table.outputId });
      if (admission.kind === 'refused') throw new Error(admission.refusal.message);
      const response = await loomClient.proposeConstructionChoices({
        project: projectId,
        explorerId: current.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        commandId: window.crypto.randomUUID(),
        snapshotToken: current.catalog.snapshotToken,
        expectedDraftVersion: current.draftVersion,
        expectedDraftDigest: current.draftDigest,
        outputId: table.outputId,
        constructionChoices: [{ ...selection.constructionChoice,
          ...(sourceStageDescriptors?.at(-1)?.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported) ? { rowValuePolicy: 'ALL' as const } : {}), ...(selection.title ? { title: selection.title } : {}) }],
        limit: 25,
      }, signal);
      const latest = latestState.current;
      if (
        signal.aborted ||
        response.snapshotToken !== current.catalog.snapshotToken ||
        response.draftVersion !== current.draftVersion ||
        response.draftDigest !== current.draftDigest ||
        response.outputId !== table.outputId ||
        latest.draftVersion !== current.draftVersion ||
        latest.draftDigest !== current.draftDigest ||
        selectedTable(latest)?.outputId !== table.outputId
      ) throw new Error('The table changed while Loom checked paired values.');
      const columnId = response.candidateColumnIds[0];
      const rows = response.preview.rows;
      if (!hasPairedPreviewRows(response.previewStatus, columnId, rows)) {
        throw new Error('Loom could not measure paired values in this preview.');
      }
      let empty = 0;
      let one = 0;
      let many = 0;
      for (const row of rows) {
        const values = row[columnId];
        if (values === null || values === undefined) empty += 1;
        else if (Array.isArray(values)) {
          const present = values.filter((value) => value !== null && value !== undefined).length;
          if (present === 0) empty += 1;
          else if (present === 1) one += 1;
          else many += 1;
        } else throw new Error('Loom did not return a paired value list for this route.');
      }
      return {
        kind: 'VALUES' as const, empty, one, many, displayedRows: rows.length,
        sampled: response.preview.sampled !== false || response.preview.partialValidation === true,
      };
    }
    if (!relatedCandidate || (selection.constructionChoice.form !== 'COUNT' && selection.constructionChoice.form !== 'ALL')) {
      throw new Error('Route coverage is unavailable for this result form.');
    }
    const candidate = relatedCandidate;
    const current = latestState.current;
    if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {
      throw new Error('The current table is still loading.');
    }
    const response = await loomClient.proposeConstruction({
      project: projectId,
      explorerId: current.explorerId,
      ...(authResourcePath ? { authResourcePath } : {}),
      snapshotToken: current.catalog.snapshotToken,
      expectedDraftVersion: current.draftVersion,
      expectedDraftDigest: current.draftDigest,
      outputId: table.outputId,
      ...candidate.intent,
      limit: 25,
      requestId: `construction-route-coverage-${window.crypto.randomUUID()}`,
    }, signal);
    const latest = latestState.current;
    if (
      signal.aborted ||
      response.snapshotToken !== current.catalog.snapshotToken ||
      response.draftVersion !== current.draftVersion ||
      response.draftDigest !== current.draftDigest ||
      response.outputId !== table.outputId ||
      latest.draftVersion !== current.draftVersion ||
      latest.draftDigest !== current.draftDigest ||
      selectedTable(latest)?.outputId !== table.outputId
    ) throw new Error('The table changed while Loom checked matching records.');
    const preview = response.preview;
    const rows = preview?.rows;
    if (response.previewStatus !== 'READY' || !preview || !rows ||
      !preview.columns.some((column) => column.column === candidate.outputColumnName)) {
      throw new Error('Loom could not measure matching records in this preview.');
    }
    let zero = 0;
    let one = 0;
    let many = 0;
    if (selection.constructionChoice.form === 'ALL') {
      let empty = 0;
      for (const row of rows) {
        const values = row[candidate.outputColumnName];
        if (values === null || values === undefined) empty += 1;
        else if (Array.isArray(values)) {
          const present = values.filter((value) => value !== null && value !== undefined).length;
          if (present === 0) empty += 1;
          else if (present === 1) one += 1;
          else many += 1;
        } else throw new Error('Loom did not return related values for this route.');
      }
      return {
        kind: 'VALUES' as const, empty, one, many, displayedRows: rows.length,
        sampled: preview.sampled !== false || preview.partialValidation === true,
      };
    }
    for (const row of rows) {
      const count = row[candidate.outputColumnName];
      if (typeof count !== 'number' || !Number.isInteger(count) || count < 0) {
        throw new Error('Loom did not return valid match counts for this route.');
      }
      if (count === 0) zero += 1;
      else if (count === 1) one += 1;
      else many += 1;
    }
    return {
      zero, one, many, displayedRows: rows.length,
      sampled: preview.sampled !== false || preview.partialValidation === true,
    };
  };
  const applyConstructionProposal = async (
    expectedProposal?: Parameters<typeof constructionLifecycle.beginApply>[0],
    fromColumnPicker = false,
  ): Promise<boolean> => {
    if (cancelingConstructionProposalRef.current) {
      if (fromColumnPicker) constructionLifecycle.cancel();
      return false;
    }
    const apply = constructionLifecycle.beginApply(expectedProposal);
    if (!apply || !table) {
      if (fromColumnPicker) constructionLifecycle.cancel();
      return false;
    }
    const { proposalId } = apply;
    appliedChoicePreview.current = undefined;
    const previewIdentity = automaticPreviewRequest.current;
    const constructionApplyIdentity = previewIdentity && previewIdentity.outputId === table.outputId
      ? { ...previewIdentity, proposalId }
      : undefined;
    const appliedResult = await applyCommandsWithResult([{
      type: 'APPLY_CONSTRUCTION_PROPOSAL',
      outputId: table.outputId,
      proposalId,
    }], undefined, constructionApplyIdentity);
    const applied = Boolean(appliedResult);
    const settledCurrentOwner = apply.finish(applied);
    if (applied && settledCurrentOwner) {
      const closeEditor = !fromColumnPicker || activeConstructionFamily === 'ADD_COLUMNS';
      if (closeEditor) {
        combineCatalog.cancel();
        setActiveConstructionFamily(undefined);
        setConstructionHistorySelection({ kind: 'source' });
        setEditingConstructionStepId(undefined);
        setRowIntentSession(false);
      }
    } else if (!applied && settledCurrentOwner && fromColumnPicker) {
      constructionLifecycle.cancel();
    }
    return applied;
  };
  const relatedSourceCandidateIntentFor = (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => {
    const relatedSelections = selections.filter((selection) => selection.relatedSource);
    if (relatedSelections.length === 0) return undefined;
    if (relatedSelections.length !== selections.length) {
      throw new Error('Add related fields together only when every selected field follows the same exact route.');
    }
    const firstSelection = relatedSelections[0];
    const firstRelatedSource = firstSelection?.relatedSource;
    if (!firstSelection || !firstRelatedSource || relatedSelections.some((selection) =>
      !selection.relatedSource ||
      !sameConstructionRoute(selection.relatedSource.choice.route, firstRelatedSource.choice.route)
    )) {
      throw new Error('Add related fields together only when every selected field follows the same exact route.');
    }
    let candidate = buildRelatedSourceCandidate(firstSelection);
    for (const selection of relatedSelections.slice(1)) {
      candidate = buildRelatedSourceCandidate(selection, {
        construction: candidate.intent.candidateConstruction,
        step: candidate.step,
      });
    }
    return candidate.intent;
  };
  const previewRelatedSourceColumns = async (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const candidateIntent = relatedSourceCandidateIntentFor(selections);
    if (!candidateIntent) throw new Error('Choose a related field before previewing columns.');
    await constructionLifecycle.proposeCandidateAndWait(candidateIntent);
    return 'preview-ready' as const;
  };
  const addSelectedFeatures = async (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const candidateIntent = relatedSourceCandidateIntentFor(selections);
    if (candidateIntent) {
      const readyProposal = await constructionLifecycle.proposeCandidateAndWait(candidateIntent);
      const applied = await applyConstructionProposal(readyProposal, true);
      if (!applied) throw new Error('Loom could not save these related columns. The table was not changed.');
      return;
    }
    const current = latestState.current;
    const proposalOwnerKey = ownerKey;
    const proposalLimit = previewLimit;
    if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest) {
      throw new Error('The current table is still loading. Try adding the columns again.');
    }
    const admission = admitAuthoringRequest(current.tables, { kind: 'add-columns-proposal', outputId: table.outputId });
    if (admission.kind === 'refused') throw new Error(admission.refusal.message);
    addColumnsRequest.current?.abort();
    const controller = new AbortController();
    addColumnsRequest.current = controller;
    try {
      const response = await loomClient.proposeConstructionChoices({
        project: projectId,
        explorerId: current.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        commandId: window.crypto.randomUUID(),
        snapshotToken: current.catalog.snapshotToken,
        expectedDraftVersion: current.draftVersion,
        expectedDraftDigest: current.draftDigest,
        outputId: table.outputId,
        constructionChoices: selections.map((selection) => ({
          ...selection.constructionChoice,
          ...(sourceStageDescriptors?.at(-1)?.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported) ? { rowValuePolicy } : {}),
          ...(selection.title ? { title: selection.title } : {}),
        })),
        limit: proposalLimit,
      }, controller.signal);
      const latest = latestState.current;
      if (
        controller.signal.aborted ||
        ownerKey !== proposalOwnerKey ||
        previewLimit !== proposalLimit ||
        latest.explorerId !== current.explorerId ||
        latest.catalog.snapshotToken !== response.snapshotToken ||
        latest.draftVersion !== response.draftVersion ||
        latest.draftDigest !== response.draftDigest ||
        selectedTable(latest)?.outputId !== response.outputId
      ) throw new Error('The table changed while its column preview was loading. Choose the columns again.');
      if (response.preview.rows === null || response.preview.outputId !== table.outputId) {
        throw new Error('Loom did not render rows for these columns. The table was not changed.');
      }
      const appliedResult = await applyCommandsWithResult(response.constructionChoices.map((choice) => ({
        type: 'APPLY_CONSTRUCTION_CHOICE',
        outputId: response.outputId,
        constructionChoice: { choiceId: choice.choiceId, form: choice.form, ...(choice.frameId ? { frameId: choice.frameId } : {}), ...(choice.rowValuePolicy ? { rowValuePolicy: choice.rowValuePolicy } : {}) },
        ...(choice.title ? { title: choice.title } : {}),
      } satisfies ExplorerBuilderCommand)), response.commandId);
      if (!appliedResult) {
        appliedChoicePreview.current = undefined;
        throw new Error('Loom could not save these coded-value columns. The table was not changed.');
      }
      appliedChoicePreview.current = {
        ownerKey: proposalOwnerKey,
        draftVersion: appliedResult.draftVersion,
        previewRequestVersion: latestState.current.previewRequestVersion,
        outputId: response.outputId,
        limit: proposalLimit,
        snapshotToken: response.snapshotToken,
        candidateWorkspaceDigest: response.candidateWorkspaceDigest,
        preview: response.preview,
      };
      if (activeConstructionFamily === 'ADD_COLUMNS') {
        setConstructionHistorySelection({ kind: 'source' });
        setEditingConstructionStepId(undefined);
        setActiveConstructionFamily(undefined);
      }
    } finally {
      if (addColumnsRequest.current === controller) addColumnsRequest.current = undefined;
    }
  };

  const addColumnsAdmission = table
    ? admitAuthoringRequest(state.tables, { kind: 'add-columns-proposal', outputId: table.outputId })
    : { kind: 'admitted' as const };
  const addColumnsReason = addColumnsAdmission.kind === 'refused'
    ? addColumnsAdmission.refusal.message
    : undefined;
  return {
    selectedAddSource,
    concreteAddSources,
    setAddColumnsSource,
    addSourceContext,
    addSourceOptionLabel,
    addSourceRouteContext,
    addColumnsViewRevision,
    selectAddColumnsView,
    inspectRelatedRouteCoverage,
    applyConstructionProposal,
    previewRelatedSourceColumns,
    addSelectedFeatures,
    addColumnsReason,
  };
};
