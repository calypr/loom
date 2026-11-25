import { BuilderToolbarSessionProvider } from '../../session/BuilderToolbarSession';
import { AuthoredTraversalSessionProvider } from '../../session/AuthoredTraversalSession';
import { ImportedSourceStepsSessionProvider } from '../../session/ImportedSourceStepsSession';
import { TraversalScopeNavigatorSessionProvider } from '../../session/TraversalScopeNavigatorSession';
import { ConstructionWorkspaceSessionProvider } from '../../session/ConstructionWorkspaceSession';
import { RowDefinitionEditorSessionProvider } from '../../session/RowDefinitionEditorSession';
import { sameCatalogSnapshot, sameDraftVersion, sameDraftDigest } from '../../../../contracts/identity';
import { OperationEditorSessionProvider } from '../../session/OperationEditorSession';
import { columnRuleDefaults } from '../../model/sourceColumnRule';
import { WorkspaceUiProvider } from '../../session/useWorkspaceUi';
import React from 'react';
import { createPortal } from 'react-dom';
import type { SelectionRevision } from '../../../../contracts/selection';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCompileResult, ConstructionStep } from '../../../../contracts/types';
import { BuilderToolbar, NewExplorerControl } from '../toolbar/BuilderToolbar';
import { DatasetReviewPanel } from '../review/DatasetReviewPanel';
import { sameRecipeEditingIdentity } from '../../session/recipeEditingOwner';
import { CreateColumnRule } from '../sourceRules/CreateColumnRule';
import { SourceRelationshipReview } from '../review/SourceRelationshipReview';
import { AuthoredTraversalEditor } from '../traversal/AuthoredTraversalEditor';
import { TraversalScopeNavigator } from '../traversal/TraversalScopeNavigator';
import { RecipeColumnPolicyInspector } from '../sourceRules/RecipeColumnPolicyInspector';
import { PreviewTable } from '../preview/PreviewTable';
import { PopulationPanel } from '../population/PopulationPanel';
import { RowChangePreviewPanel } from '../review/RowChangePreviewPanel';
import { RowDefinitionPanel } from '../rowDefinition/RowDefinitionPanel';
import { RowDefinitionSettingsPanel } from '../rowDefinition/RowDefinitionSettingsPanel';
import { useBrowserSubscription } from '../../hooks/useBrowserSubscription';
import { FrameSourcePanel } from '../construction/FrameSourcePanel';
import { derivedOccurrences, routeNode } from '../../model/authoring';
import { shouldShowBuilderLoadError } from '../../authoring/builderLoadError';
import { BuilderSessionProvider } from '../../session/BuilderSessionProvider';
import { useBuilderSessionDraft } from '../../session/useBuilderSessionDraft';
import { useDirtyBeforeUnload } from '../../hooks/useDirtyBeforeUnload';
import { usePortalHost } from '../../hooks/usePortalHost';
import { ConstructionWorkspace, ConstructionActionBar } from '../construction/ConstructionWorkspace';
import { ConstructionProposalPanel } from '../review/ConstructionProposalPanel';
import { ConstructionProposalPreview } from '../preview/ConstructionProposalPreview';
import { ImportedSourceSteps } from '../sourceRules/ImportedSourceSteps';
import type { RelatedSourceStep } from '../reshape/RelatedSourceStepEditor';
import { type RelatedFieldStep } from '../reshape/RelatedFieldEditor';
import { TableCreatorPanel } from './TableCreatorPanel';
import { OperationEditorHost } from './OperationEditorHost';
import { RelatedStepEditors } from './RelatedStepEditors';
import { builderOwnerKeyFor, useBuilderWorkspaceScope } from '../../session/builderWorkspaceScope';
import { isUnsupportedUnknownSourceProjectionDiagnostic, useBuilderWorkspaceActions } from '../../session/useBuilderWorkspaceActions';
const receiptContainsOutputIds = (
  receipt: ExplorerBuilderCompileResult | undefined,
  outputIds: ReadonlyArray<string>,
): boolean => receipt !== undefined && outputIds.every((outputId) =>
  receipt.outputs.some((output) => output.outputId === outputId),
);

const isRelatedFieldStep = (step: ConstructionStep): step is RelatedFieldStep =>
  step.operation.kind === 'RELATED_FIELD';








type BuilderWorkspaceProps = {
  readonly organization?: string;
  readonly project: string;
  readonly explorerId?: string;
  readonly populationSelection?: SelectionRevision;
  readonly populationSelectionLoading?: boolean;
  readonly populationSelectionError?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly workspaceComposition?: React.ComponentProps<typeof ConstructionWorkspace>['composition'];
  readonly featureFocus?: {
    readonly outputId: string;
    readonly column: string;
    readonly label?: string;
  };
};

function BuilderWorkspaceScope(props: BuilderWorkspaceProps) {
  const scope = useBuilderWorkspaceScope(props);
  const { projectId, authResourcePath, effectiveExplorerId, ownerKey,
    selectedExplorerId, setSelectedExplorerId, explorerSummaries } = scope;
  const draft = useBuilderSessionDraft({ projectId, authResourcePath, effectiveExplorerId, ownerKey });
  if (selectedExplorerId === 'dataframe-default' &&
      (draft.builder.error as { code?: string } | undefined)?.code === 'DEFAULT_DATAFRAME_UNAVAILABLE') {
    const fallback = explorerSummaries.find((candidate) => candidate.explorerId !== 'dataframe-default');
    if (fallback) setSelectedExplorerId(fallback.explorerId);
  }
  return (
    <BuilderSessionProvider draft={draft} transport={scope.loomClient}>
      <WorkspaceUiProvider>
        <BuilderWorkspaceContent {...props} scope={scope} />
      </WorkspaceUiProvider>
    </BuilderSessionProvider>
  );
}

const focusFirstTableName = () => {
  window.setTimeout(() => document.getElementById('first-table-name')?.focus(), 0);
};

const confirmWorkspaceAction = (message: string) => window.confirm(message);

const BuilderWorkspaceContent = ({
  populationSelection,
  populationSelectionLoading = false,
  populationSelectionError,
  onExplorerChange,
  featureFocus,
  workspaceComposition,
  scope,
}: BuilderWorkspaceProps & { readonly scope: ReturnType<typeof useBuilderWorkspaceScope> }) => {
  const {
    explorers, explorerQueryEnabled, builder, workspaceHostRef, emptyExplorerProject, message,
    createStatus, createCustomExplorer, refetchBuilder, capabilities, state, selectTableFromUser,
    reviewOpen, setReviewOpen, publishing, busy, featureMode, tableToolbarHost, table, construction,
    constructionLifecycle, setEditingConstructionStepId, setRowIntentSession,
    applyCommandsWithResult, recipeMutationAllowed, canStartCombine, recipeOwner, setRecipeNotice,
    setSourceRowsOpen, previousDraftRevisionId, applyCommands, editingConstructionStep,
    hasUnsupportedSavedSourceColumns, previewIsCurrent, pendingCommands, builderDataKey,
    firstTableProgress, loomClient, projectId, authResourcePath, activePopulationSelection,
    cancelingConstructionProposal, graphSource, activeConstructionFamily, sourceStepEditAction,
    advancedRuleEditorReturn, pendingSavedRuleRemoval, setPendingSavedRuleRemoval,
    addSelectedFeatures, sourceRelationshipRequest, recipeEditToken, relatedExpandQueryOwnerRef,
    ownerKey, setActiveConstructionFamily, tablePreview, applyConstructionProposal,
    beginConstructionCancellation, finishConstructionCancellation, dispatch, sourceStepEditDirty,
    sourceStepEditCancelAction, candidateForPreviewColumn, connectingRelationship,
    setRootReplacementInspectorHost, rootChangeState, rootRecipeAssessment, applyRowChangePreview,
    cancelRootChange, editingRecipeColumn, setSourceStepInspectorHost, setSourceProposal,
    sourceRowsOpen, setOccurrenceInspectorHost, recipeNotice, occurrenceInspectorHost,
    rowSettingsOpenAction, setTableToolbarHost, setSourceRowsInspectorHost, firstTableName,
    setFirstTableName, createFirstTableFromRoot, setTableCreatorOpen, focusDatasetReviewTarget,
    blockingDiagnostics, primaryDiagnostic, unsupportedColumns,
    unsupportedSourceProjectionDiagnostic, incomplete, suggestionsFailure, suggestionIdentity,
    suggestionRequestKey, setSuggestionsFailure, ensureSuggestions, suggestionHostRef,
    setActiveSourceStepId, tableCreatorOpen, addColumnsReason, sourceRowsInspectorHost,
    rowSettingsCloseAction, rootReplacementInspectorHost, rootChangeIdentityForTable,
    refreshRootChange, activePopulationSelectionLoading, activePopulationSelectionError,
    populationVariantPending, setClearedPopulationSelection, populationSelectionContextKey,
    excludePopulationMember, constructionHistorySelection, sourceStepInspectorHost,
    sourceChangeReviewHost, sourceFieldEditAction, sourceOutputEditAction,
    sourceStepSwitchOccurrenceAction, automaticPreviewFailureMessage, previewLimit, setPreviewLimit,
    setSourceChangeReviewHost, reviewFocusTarget, selectConstructionFamily,
    selectConstructionHistory, editConstructionStep, closeConstructionWorkspace,
    removeConstructionStep, restorePreviousDraft, capabilityStage, capabilityIsForAppendStage,
    calculateUnavailableReason, relationshipOwner, loadRecipeNetwork, pendingRelationship,
    cancelRecipeRelationship, sourceReviewStatus, beginSourceReview, proposeColumnPolicy,
    applySourceRecipe, activeRuleReview, reviewingAdvancedColumns, ruleOccurrence, ruleFieldPaths,
    advancedOccurrenceId, advancedRules, unassignedFrameIds, savedFramesForNode,
    operationEditorSession, persistedConstructionHistory, rowDefinitionEditorSession,
    sourceCandidate, sourceReviewColumnLabels, rootCandidate, candidatePreview,
    proposalPresentationTable, inspectedOccurrenceId, workspaceCandidatePreview,
    authoredTraversalSession, traversalNavigatorSession, importedSourceStepsSession,
    constructionWorkspaceSession, toolbarSession, removeProjectedField, traversalTable,
  } = useBuilderWorkspaceActions({
    populationSelection, populationSelectionLoading, populationSelectionError, onExplorerChange,
    featureFocus, scope, focusFirstTableName, confirmWorkspaceAction,
  });
  useDirtyBeforeUnload(state.dirty || pendingCommands > 0);
  const toolbarHost = usePortalHost('explorer-builder-toolbar-host');
  useBrowserSubscription((_onStoreChange) => {
    if (!reviewFocusTarget || state.selectedOutputId !== reviewFocusTarget.outputId) return () => {};
    const focusTarget = reviewFocusTarget;
    const timer = window.setTimeout(() => {
      if (focusTarget.kind === 'column') {
        document
          .querySelector<HTMLElement>('[data-feature-focus="true"]')
          ?.scrollIntoView({ block: 'center' });
        return;
      }
      if (focusTarget.kind === 'row') {
        const selector = focusTarget.control === 'row-type'
          ? '[aria-label="Search row types"]'
          : '[aria-label="One row per"]';
        const control = document.querySelector<HTMLElement>(selector);
        control?.closest('section')?.scrollIntoView({ block: 'center' });
        control?.focus();
        return;
      }
      const targetTable = state.tables.find((candidate) => candidate.outputId === focusTarget.outputId);
      const selector = targetTable?.document.rootResourceType
        ? '[aria-label="Search columns"]'
        : '[aria-label="Search row types"]';
      const control = document.querySelector<HTMLElement>(selector);
      control?.closest('section')?.scrollIntoView({ block: 'center' });
      control?.focus();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [reviewFocusTarget, state.selectedOutputId, state.tables], Boolean(reviewFocusTarget && state.selectedOutputId === reviewFocusTarget.outputId));
  if (
    explorers.isLoading ||
    (explorerQueryEnabled && builder.isLoading)
  ) {
    return (
      <main ref={workspaceHostRef} className="p-6" role="status">
        Loading the selected Explorer configuration…
      </main>
    );
  }
  if (emptyExplorerProject) {
    return (
      <main ref={workspaceHostRef} className="min-h-full p-6">
        <section
          aria-labelledby="empty-explorer-project-title"
          className="mx-auto max-w-xl rounded-lg border border-slate-200 bg-white p-6 shadow-sm"
        >
          <h1
            id="empty-explorer-project-title"
            className="text-lg font-semibold text-slate-900"
          >
            Create your first Explorer
          </h1>
          <p className="mt-2 text-sm text-slate-600">
            This project does not have an Explorer yet. Create one to start
            building its first table.
          </p>
          {message ? (
            <p role="alert" className="mt-3 text-sm text-red-700">
              {message}
            </p>
          ) : null}
          <div className="mt-4">
            <NewExplorerControl
              mode="blank-only"
              busy={createStatus.isLoading}
              onCreateExplorer={(title, fromCurrent) =>
                void createCustomExplorer(title, fromCurrent)
              }
            />
          </div>
        </section>
      </main>
    );
  }
  if (shouldShowBuilderLoadError(Boolean(explorers.error), Boolean(builder.data))) {
    return (
      <main ref={workspaceHostRef} className="p-6" role="alert">
        <p>Couldn’t load this dataset.</p>
        <button
          type="button"
          onClick={() => {
            void explorers.refetch();
            if (explorerQueryEnabled) void refetchBuilder();
          }}
        >
          Try again
        </button>
      </main>
    );
  }

  const toolbar = (
    <BuilderToolbarSessionProvider value={toolbarSession}>
      <BuilderToolbar
        columnCreationSupported
        tableToolbarHost={featureMode === 'graph' ? tableToolbarHost : null}
      />
    </BuilderToolbarSessionProvider>
  );

  const editingRelatedSourceStep: RelatedSourceStep | undefined = editingConstructionStep?.operation.kind === 'RELATED_SOURCE'
    ? editingConstructionStep as RelatedSourceStep
    : undefined;
  const editingRelatedFieldStep = editingConstructionStep && isRelatedFieldStep(editingConstructionStep)
    ? editingConstructionStep
    : undefined;
  const canApplyConstructionProposal = constructionLifecycle.canApply &&
    !cancelingConstructionProposal &&
    pendingCommands === 0 &&
    !publishing &&
    state.reconciliation !== 'pending';
  const operationEditor = table && operationEditorSession ? (
    <OperationEditorSessionProvider value={operationEditorSession}>
      <OperationEditorHost
        configuredAdvancedColumns={<section aria-label="Configured column rules" className="grid min-w-0 content-start gap-2"
          ref={(node) => { if (node && table.document.importedRecipe) loadRecipeNetwork(); }}>
          <h3 className="sr-only">Saved rules</h3>
          {advancedRules.length ? <div className="grid gap-3">
            {advancedRules.map(({ step, fields }) => (
              <section key={step.id} aria-label={step.title} className="border-b border-slate-100 pb-2">
                <div className="flex items-center justify-between gap-2 pb-1 text-xs">
                  <h4 className="font-semibold">{step.title}</h4>
                  {step.editable ? <button type="button" className="shrink-0 text-blue-800 underline"
                    aria-label={`Configure ${step.title}`}
                    disabled={pendingCommands > 0 || publishing || reviewingAdvancedColumns}
                    onClick={() => {
                      if (!sourceStepEditAction.current || !recipeMutationAllowed()) return;
                      advancedRuleEditorReturn.current = step.id;
                      closeConstructionWorkspace();
                      sourceStepEditAction.current(step.id);
                    }}>Configure rule</button> : null}
                </div>
                {fields.length ? <ul aria-label={`Columns from ${step.title}`} className="max-h-56 divide-y divide-slate-100 overflow-auto border-y border-slate-200 text-xs">
                  {fields.map((field) => {
                    const removalKey = JSON.stringify([relationshipOwner, state.draftVersion, state.draftDigest, advancedOccurrenceId, step.id, field.columnId]);
                    const removalDisabled = pendingCommands > 0 || publishing || reviewingAdvancedColumns || state.reconciliation === 'pending';
                    return <li key={field.columnId ?? field.column} className="grid min-w-0 gap-1 px-2 py-1.5">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="min-w-0 flex-1 truncate" title={field.label}>{field.label}</span>
                        {field.columnId && field.removal === 'DISABLE_PINNED_OUTPUT' && pendingSavedRuleRemoval !== removalKey
                          ? <button type="button" className="shrink-0 text-slate-600 hover:text-slate-900"
                            aria-label={`Remove ${field.label}`} disabled={removalDisabled}
                            onClick={() => setPendingSavedRuleRemoval(removalKey)}>Remove</button> : null}
                      </div>
                      {field.columnId && field.removal === 'DISABLE_PINNED_OUTPUT' && pendingSavedRuleRemoval === removalKey ? (
                        <div className="flex flex-wrap justify-end gap-x-3 gap-y-1">
                          <button type="button" className="font-medium text-red-700 hover:underline" aria-label={`Confirm removal of ${field.label}`}
                            disabled={removalDisabled} onClick={() => {
                              setPendingSavedRuleRemoval(undefined);
                              removeProjectedField(field);
                            }}>Remove column</button>
                          <button type="button" className="text-slate-600 hover:underline"
                            onClick={() => setPendingSavedRuleRemoval(undefined)}>Cancel</button>
                        </div>
                      ) : null}
                    </li>;
                  })}
                </ul> : null}
              </section>
            ))}
          </div> : savedFramesForNode.length ? null : <p className="text-xs text-slate-600">No column rules configured on this node.</p>}
          {savedFramesForNode.length ? (
            <FrameSourcePanel
              key={`${table.outputId}:${state.catalog.snapshotToken}:${graphSource?.occurrenceId}`}
              project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
              snapshotToken={state.catalog.snapshotToken} outputId={table.outputId}
              rowRoot={table.document.rootResourceType} frames={savedFramesForNode} unassignedFrameIds={unassignedFrameIds}
              columns={table.document.columns} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
              allowNewSource={false}
              onSet={(choice, form) => applyCommands([{ type: 'SET_FRAME_SOURCE', outputId: table.outputId, frameChoiceId: choice.choiceId, form }])}
              onReplace={(frameId, choice, form) => applyCommands([{ type: 'REPLACE_FRAME_SOURCE', outputId: table.outputId, frameId, frameChoiceId: choice.choiceId, form }])}
              onRemove={(frameId) => applyCommands([{ type: 'REMOVE_FRAME_SOURCE', outputId: table.outputId, frameId }])}
              onAddSelected={addSelectedFeatures}
              onRemoveColumn={(column) => applyCommands([{ type: 'REMOVE_COLUMN', outputId: table.outputId, column }])}
            />
          ) : null}
        </section>}
        renderColumnRule={(preset, inventory, path, onClear, inline) => {
          const defaults = columnRuleDefaults[preset.id];
          const initialParameters = preset.id === 'extension' && defaults?.extension && path
            ? { extension: { ...defaults.extension, source: { select: path } } }
            : preset.id === 'catalog' && defaults?.catalog && path
              ? { catalog: { ...defaults.catalog, includePaths: [path] } }
              : defaults;
          if (!ruleOccurrence || !initialParameters) return null;
          return (
            <CreateColumnRule
              key={`${relationshipOwner}:${ruleOccurrence.id}:${preset.id}:${path ?? ''}`}
              kind={preset.kind} occurrence={ruleOccurrence} preset={preset} initialParameters={initialParameters}
              identifierSystems={inventory.systems} categorySystems={inventory.categorySystems} identifierInventoryLoading={inventory.loading}
              extensionSourcePaths={preset.id === 'extension' ? inventory.extensionPaths : undefined}
              identifierInventoryUnavailable={inventory.unavailable}
              fieldPathChoices={preset.id === 'catalog' ? ruleFieldPaths : undefined}
              reviewing={(!inline || inline.active) && activeRuleReview?.ruleKind === preset.kind && activeRuleReview.occurrenceId === ruleOccurrence.id}
              inline={inline}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing || Boolean(inline?.disabled)}
              onCancel={() => { if (cancelRecipeRelationship(false) && !inline) onClear?.(); }}
              renderReview={(sourceRule, onCancel, disabled, label, automatic) => <SourceRelationshipReview
                key={JSON.stringify(sourceRule)}
                client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
                outputId={table.outputId} disabled={disabled}
                requestControllerRef={sourceRelationshipRequest}
                sourceRule={sourceRule} sharedPreview autoPreview={automatic || (preset.id === 'catalog' && Boolean(path))} createRuleReview={{ label, active: Boolean(activeRuleReview) }}
                onPreviewStart={() => {
                  if (!activeRuleReview) {
                    if (!recipeMutationAllowed()) return undefined;
                    recipeEditToken.current = recipeOwner.begin({ kind: 'createColumnRule', ruleKind: preset.kind, occurrenceId: ruleOccurrence.id });
                  }
                  return beginSourceReview();
                }}
                onApply={async (commands) => {
                  const saved = await applySourceRecipe(commands);
                  if (saved) closeConstructionWorkspace();
                  return saved;
                }}
                onCancel={onCancel} />}
            />
          );
        }}
      />
    </OperationEditorSessionProvider>
  ) : undefined;
  const relatedStepEditors = table && (editingRelatedSourceStep || editingRelatedFieldStep) ? (
    <RelatedStepEditors
      table={table}
      state={state}
      ownerKey={ownerKey}
      construction={construction}
      lifecycle={constructionLifecycle}
      sourceStep={editingRelatedSourceStep}
      fieldStep={editingRelatedFieldStep}
      project={projectId}
      authResourcePath={authResourcePath}
      pendingCommands={pendingCommands}
      publishing={publishing}
      onCancelSource={() => {
        constructionLifecycle.cancel();
        setEditingConstructionStepId(undefined);
        setActiveConstructionFamily(undefined);
      }}
      onCancelField={() => {
        constructionLifecycle.cancel();
        setEditingConstructionStepId(undefined);
      }}
    />
  ) : undefined;
  const proposalPanel = (
    <ConstructionProposalPanel
      state={constructionLifecycle.proposal}
      baseConstruction={construction ?? (constructionLifecycle.capabilities.status === 'ready' ? constructionLifecycle.capabilities.response.baseConstruction : undefined)}
      currentInput={previewIsCurrent && tablePreview && capabilityIsForAppendStage && capabilityStage
        ? { preview: tablePreview, stageId: capabilityStage.id }
        : undefined}
      canApply={canApplyConstructionProposal}
      onApply={() => void applyConstructionProposal()}
      onCancel={() => {
        const queryOwner = relatedExpandQueryOwnerRef.current;
        const closeProposal = closeConstructionWorkspace;
        if (!queryOwner) {
          closeProposal();
          return;
        }
        if (!beginConstructionCancellation()) return;
        void (async () => {
          let ownsEditor = false;
          try {
            await queryOwner.pauseAndDrain();
            ownsEditor = relatedExpandQueryOwnerRef.current === queryOwner;
          } finally {
            finishConstructionCancellation();
          }
          if (ownsEditor) closeProposal();
        })();
      }}
      onRetry={constructionLifecycle.retry}
    />
  );
  const workspaceEditor = operationEditor || relatedStepEditors || constructionLifecycle.proposal.status !== 'idle'
    ? <div className="construction-mutation-inspector">
        <div className="construction-mutation-fields">{operationEditor}{relatedStepEditors}</div>
        {proposalPanel}
      </div>
    : undefined;
  const pendingChangeReview = table && pendingRelationship ? <SourceRelationshipReview key={`${relationshipOwner}:${JSON.stringify(pendingRelationship)}`}
    columnLabels={sourceReviewColumnLabels}
    client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
    snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
    outputId={table.outputId} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
    change={pendingRelationship} sharedPreview onStatusChange={sourceReviewStatus} onPreviewStart={beginSourceReview}
    relationshipDescription={(() => {
      if (pendingRelationship.action === 'ADD') {
        const parent = routeNode(table.document.route, pendingRelationship.parentOccurrenceId);
        const edge = state.catalog.edges.find((item) => item.edgeId === pendingRelationship.edgeId);
        const target = state.catalog.nodes.find((item) => item.nodeId === edge?.toNodeId);
        return parent && edge && target ? `${parent.resourceType} → ${target.resourceType} via ${edge.label}` : undefined;
      }
      const occurrence = routeNode(table.document.route, pendingRelationship.occurrenceId);
      return occurrence ? `${occurrence.resourceType} via ${occurrence.relationship}` : undefined;
    })()}
    preparing={connectingRelationship}
    preparationError={recipeOwner.state.error}
    requestControllerRef={sourceRelationshipRequest}
    {...(sourceCandidate ? { prepared: { proposal: sourceCandidate.proposal, preview: sourceCandidate.preview } } : {})}
    onApply={applySourceRecipe} onCancel={cancelRecipeRelationship} /> : undefined;
  const staleEditIdentity = {
    project: state.project,
    explorerId: state.explorerId,
    outputId: table?.outputId ?? '',
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
  };
  const staleSourceEdit = recipeOwner.state.status === 'stale' &&
    recipeOwner.state.target?.kind !== 'root' &&
    (Boolean(pendingRelationship) ||
      Boolean(recipeOwner.state.token && !sameRecipeEditingIdentity(recipeOwner.state.token.identity, staleEditIdentity)));
  const sourceRecipeInspector = staleSourceEdit ? undefined
    : recipeOwner.state.target?.kind === 'root' ? (recipeOwner.state.target.mode === 'REPLACE'
      ? <div ref={setRootReplacementInspectorHost} aria-label="Starting-record replacement inspector" />
      : rootCandidate || rootChangeState.kind === 'idle' ? <RowChangePreviewPanel candidateRoot={rootRecipeAssessment.current?.candidateRootResourceType ?? table?.document.rootResourceType ?? 'records'}
          status={rootCandidate ? 'ready' : recipeOwner.state.status === 'failed' || recipeOwner.state.status === 'stale' ? 'error' : 'loading'}
          preview={rootCandidate?.preview} error={recipeOwner.state.error} sharedPreview
          disabled={pendingCommands > 0 || recipeOwner.state.status === 'applying'}
          onApply={() => void applyRowChangePreview()} onCancel={cancelRootChange} /> : undefined)
    : table && (editingRecipeColumn?.owner === relationshipOwner || recipeOwner.state.target?.kind === 'sourceStep') ? <>
      {recipeOwner.state.target?.kind === 'sourceStep' ? <div className="construction-inspector-portal-host" ref={setSourceStepInspectorHost} aria-label="Source step inspector" /> : null}
      {editingRecipeColumn?.owner === relationshipOwner ? (() => {
        const column = table.document.columns.find((item) => (item.columnId ?? item.column) === editingRecipeColumn.columnId);
        const occurrence = column ? derivedOccurrences(table, state.catalog).find((item) => item.id === column.occurrenceId) : undefined;
        return column ? <RecipeColumnPolicyInspector key={`${relationshipOwner}:${editingRecipeColumn.columnId}`}
          column={column} sourceNodeId={occurrence?.nodeId ?? ''} candidate={candidateForPreviewColumn(column)}
          disabled={pendingCommands > 0 || publishing || state.reconciliation === 'pending' || recipeOwner.state.status === 'stale'}
          onCancel={cancelRecipeRelationship}
          busy={recipeOwner.state.status === 'previewing' || recipeOwner.state.status === 'applying'}
          error={recipeOwner.state.error}
          cancelDisabled={recipeOwner.state.status === 'applying'}
          canApply={recipeOwner.state.status === 'preview-ready' && Boolean(sourceCandidate)}
          onDraftChange={() => {
            if (recipeEditToken.current) recipeEditToken.current = recipeOwner.changed(recipeEditToken.current);
            setSourceProposal(undefined);
          }}
          onPreview={(columnValue) => proposeColumnPolicy(column, columnValue)}
          onChange={() => { if (sourceCandidate) void applySourceRecipe([{ type: 'APPLY_COLUMN_POLICY_PROPOSAL', outputId: table.outputId,
            proposalId: sourceCandidate.preview.receiptId }]); }} /> : null;
      })() : null}
  </> : undefined;
  const canInspectOccurrence = Boolean(table && recipeOwner.state.status === 'saved' && !sourceRowsOpen && !sourceRecipeInspector && !workspaceEditor &&
    routeNode(table.document.route, inspectedOccurrenceId));
  const occurrenceInspection = canInspectOccurrence && table ? (
    <>
      <div className="min-w-0" ref={setOccurrenceInspectorHost} />
    </>
  ) : undefined;
  const recordNetwork = table ? (
    <div className="flex min-h-0 min-w-0 flex-col gap-2" ref={(node) => { if (node) loadRecipeNetwork(); }}>
      {recipeNotice ? <p role="alert" className="text-xs text-amber-900">{recipeNotice}</p> : null}
      <AuthoredTraversalSessionProvider value={authoredTraversalSession!}>
        <AuthoredTraversalEditor
          showInspector={canInspectOccurrence}
          inspectorHost={occurrenceInspectorHost}
          showOccurrenceNavigation={false}
          presentation="compact"
          onTableToolbarHostChange={setTableToolbarHost}
        />
      </AuthoredTraversalSessionProvider>
    </div>
  ) : undefined;

  const candidatePreviewElement = candidatePreview ? (
    <ConstructionProposalPreview
      preview={candidatePreview}
      presentationTable={proposalPresentationTable}
    />
  ) : undefined;
  const externalWorkspaceEditor = sourceRecipeInspector ?? (sourceRowsOpen ? <div className="construction-inspector-portal-host" ref={setSourceRowsInspectorHost} aria-label="Source row settings inspector" /> : workspaceEditor);
  const tableCreatorPanel = (
    <TableCreatorPanel
      isFirstTable={state.tables.length === 0}
      previousDraftRevisionId={previousDraftRevisionId}
      pendingCommands={pendingCommands}
      publishing={publishing}
      reconciliationPending={state.reconciliation === 'pending'}
      firstTableName={firstTableName}
      onFirstTableNameChange={setFirstTableName}
      firstTableProgress={firstTableProgress}
      catalog={state.catalog}
      onChooseRoot={(nodeId) => void createFirstTableFromRoot(nodeId, applyCommandsWithResult)}
      onUndo={() => void restorePreviousDraft()}
      onCancel={() => {
        setTableCreatorOpen(false);
        setFirstTableName('');
      }}
    />
  );

  return (
    <main ref={workspaceHostRef} className="min-h-full bg-slate-50 p-2 text-slate-900 sm:p-3">
      {toolbarHost ? createPortal(toolbar, toolbarHost) : toolbar}
      {reviewOpen ? (
        <DatasetReviewPanel
          tables={state.tables}
          catalog={state.catalog}
          receipt={receiptContainsOutputIds(state.receipt, state.tables.map((candidate) => candidate.outputId))
            ? state.receipt
            : undefined}
          preview={receiptContainsOutputIds(state.receipt, state.tables.map((candidate) => candidate.outputId))
            ? state.preview
            : undefined}
          diagnostics={state.diagnostics}
          reconciliation={state.reconciliation}
          onFocus={focusDatasetReviewTarget}
          onClose={() => setReviewOpen(false)}
        />
      ) : null}
      <div className="mx-auto max-w-[1920px] space-y-3">
        {(message ||
          blockingDiagnostics ||
          hasUnsupportedSavedSourceColumns ||
          state.reconciliation === 'stale') && (
          <section
            className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-900"
            role="alert"
          >
            <div className="font-semibold">
              {blockingDiagnostics
                ? 'Builder needs attention'
                : state.reconciliation === 'stale'
                  ? 'Catalog or receipt changed'
                  : 'Builder needs attention'}
            </div>
            <p>
              {hasUnsupportedSavedSourceColumns &&
              (!primaryDiagnostic || isUnsupportedUnknownSourceProjectionDiagnostic(primaryDiagnostic))
                ? 'Some saved source fields have an unsupported type. Remove them to restore Preview and field selection.'
                : primaryDiagnostic?.message ?? message}
            </p>
            {hasUnsupportedSavedSourceColumns ? (
              <ul className="mt-2 space-y-2">
                {unsupportedColumns.map((column) => (
                  <li
                    key={`${column.outputId}:${column.column}`}
                    className="flex flex-wrap items-center gap-2 rounded-md border border-red-200 bg-white/70 px-2 py-2"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="font-semibold">{column.tableTitle}</span>
                      <span className="mx-1" aria-hidden="true">·</span>
                      <span className="font-mono">{column.label}</span>
                    </span>
                    <button
                      type="button"
                      className="rounded border border-slate-300 bg-white px-2.5 py-1 font-semibold text-slate-800 hover:bg-slate-50 disabled:opacity-50"
                      aria-label={`Open ${column.tableTitle} table to review ${column.label}`}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onClick={() => selectTableFromUser(column.outputId)}
                    >
                      Open {column.tableTitle}
                    </button>
                    <button
                      type="button"
                      className="rounded border border-red-300 bg-white px-2.5 py-1 font-semibold text-red-800 hover:bg-red-50 disabled:opacity-50"
                      aria-label={`Remove ${column.label} from ${column.tableTitle}`}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onClick={() => void applyCommands([{
                        type: 'REMOVE_COLUMN',
                        outputId: column.outputId,
                        column: column.column,
                      }])}
                    >
                      Remove
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
            {unsupportedSourceProjectionDiagnostic ? (
              <details className="mt-2 rounded border border-red-200 bg-white/70 px-2 py-1.5">
                <summary className="cursor-pointer font-medium">
                  Technical details · Code: {unsupportedSourceProjectionDiagnostic.code}
                </summary>
                <p className="mt-1 break-words font-mono text-[11px]">
                  {unsupportedSourceProjectionDiagnostic.message}
                </p>
                {unsupportedSourceProjectionDiagnostic.requestId ? (
                  <p className="mt-1 font-mono text-[11px]">
                    Request ID: {unsupportedSourceProjectionDiagnostic.requestId}
                  </p>
                ) : null}
              </details>
            ) : primaryDiagnostic?.code ? (
              <p className="mt-1">
                Technical details · Code: {primaryDiagnostic.code}
              </p>
            ) : null}
            {(state.reconciliation === 'stale' ||
              state.reconciliation === 'repair') &&
            state.tables.length > 0 &&
            !incomplete ? (
              <button
                type="button"
                className="mt-2 rounded border border-blue-300 bg-white px-2.5 py-1 font-semibold text-blue-800 hover:bg-blue-50"
                onClick={() => dispatch({ type: 'requestRecompile' })}
              >
                Recompile
              </button>
            ) : null}
          </section>
        )}
        {suggestionsFailure?.key === suggestionIdentity ? (
          <div className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950"
            role="alert" data-testid="builder-suggestions-error">
            <p>{suggestionsFailure.message}</p>
            <button type="button" className="mt-2 font-semibold text-blue-800 underline"
              data-testid="builder-suggestions-retry"
              onClick={() => {
                suggestionRequestKey.current = '';
                setSuggestionsFailure(undefined);
                ensureSuggestions();
              }}>
              Retry finding columns
            </button>
          </div>
        ) : null}
        {state.tables.length === 0 ? tableCreatorPanel : (
          <>
            <span key={suggestionIdentity} ref={suggestionHostRef} hidden />
            {staleSourceEdit ? <span hidden ref={(element) => {
              const staleEdit = recipeOwner.getState();
              if (!element || staleEdit.status !== 'stale' || !cancelRecipeRelationship(false)) return;
              setRecipeNotice(staleEdit.error);
              advancedRuleEditorReturn.current = undefined;
              sourceStepEditDirty.current = false;
              setActiveSourceStepId(undefined);
              setSourceRowsOpen(false);
              setActiveConstructionFamily(undefined);
              setEditingConstructionStepId(undefined);
              setRowIntentSession(false);
            }} /> : null}
            {tableCreatorOpen ? tableCreatorPanel : null}
            {table ? (
              <ConstructionWorkspaceSessionProvider value={constructionWorkspaceSession!}>
                <ConstructionWorkspace
                  composition={workspaceComposition}
                  network={recordNetwork}
                  navigation={traversalTable ? <div className="space-y-3">
                    <TraversalScopeNavigatorSessionProvider value={traversalNavigatorSession!}>
                      <TraversalScopeNavigator
                        showScopeNavigation={false}
                        resultActions={<ConstructionActionBar variant="compact" activeFamily={activeConstructionFamily}
                          families={['ADD_COLUMNS', 'KEEP_ROWS', 'CALCULATE', 'RESHAPE']}
                          disabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved' || state.reconciliation === 'pending'}
                          addColumnsDisabled={Boolean(addColumnsReason)}
                          addColumnsReason={addColumnsReason}
                          calculateAvailable={Boolean(capabilityIsForAppendStage && capabilityStage?.capabilities.some((candidate) => candidate.kind === 'DERIVE' && candidate.supported))}
                          calculateUnavailableReason={calculateUnavailableReason} onSelect={selectConstructionFamily} />}
                        combineActions={<ConstructionActionBar variant="compact" activeFamily={activeConstructionFamily}
                          families={['COMBINE']}
                          disabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved' || state.reconciliation === 'pending'}
                          combineDisabled={!canStartCombine}
                          onSelect={selectConstructionFamily} />} />
                    </TraversalScopeNavigatorSessionProvider>
                  </div> : undefined}
                  rowSetup={(
                    <RowDefinitionEditorSessionProvider key={`${table.outputId}:${state.catalog.snapshotToken}:${builder.data?.draftVersion ?? ''}:${builder.data?.draftDigest ?? ''}`} value={rowDefinitionEditorSession!}>
                      <RowDefinitionSettingsPanel
                        inspectorHost={sourceRowsInspectorHost}
                        operationInspectorPresentation="workspace"
                        openActionRef={rowSettingsOpenAction}
                        closeActionRef={rowSettingsCloseAction}
                        replacementPresentation="shared" replacementInspectorHost={rootReplacementInspectorHost}
                        presentation="compact"
                        triggerPresentation="hidden"
                        triggerLabel={table.document.rootResourceType || 'Choose records'}
                        renderRootSettings={(onRootChange, onResolveRootChange, onReplaceRoot) => (
                          <RowDefinitionPanel
                            catalog={state.catalog}
                            table={table}
                            disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                            rootChangeState={rootChangeState}
                            currentIdentity={rootChangeIdentityForTable(table.outputId)}
                            onChange={onRootChange}
                            onResolve={onResolveRootChange}
                            onRefresh={refreshRootChange}
                            onKeepCurrent={cancelRootChange}
                            onReplace={onReplaceRoot}
                          />
                        )}
                        startingCollectionSettings={(
                          activePopulationSelection ||
                          table.document.population ||
                          activePopulationSelectionLoading ||
                          activePopulationSelectionError
                        ) ? (
                          <PopulationPanel
                            table={table}
                            selection={activePopulationSelection}
                            loading={activePopulationSelectionLoading}
                            error={activePopulationSelectionError}
                            project={projectId}
                            explorerId={state.explorerId}
                            authResourcePath={authResourcePath}
                            snapshotToken={state.catalog.snapshotToken}
                            draftVersion={state.draftVersion}
                            draftDigest={state.draftDigest}
                            receiptId={state.receipt?.receiptId}
                            disabled={activePopulationSelectionLoading || populationVariantPending || pendingCommands > 0 || state.reconciliation === 'pending'}
                            onAttach={(routeChoiceId) => void applyCommands([{
                              type: 'SET_TABLE_POPULATION',
                              outputId: table.outputId,
                              selectionRevisionId: activePopulationSelection?.id,
                              routeChoiceId,
                            }])}
                            onClear={() => {
                              if (activePopulationSelection) setClearedPopulationSelection({
                                contextKey: populationSelectionContextKey,
                                client: loomClient,
                                selectionID: activePopulationSelection.id,
                              });
                              void applyCommands([{
                                type: 'CLEAR_TABLE_POPULATION',
                                outputId: table.outputId,
                              }]);
                            }}
                            onExclude={(ref, route) => void excludePopulationMember(ref, route)}
                            onApplyMemberRemoval={(proposalId) => applyCommands([{
                              type: 'APPLY_POPULATION_MEMBER_PROPOSAL',
                              outputId: table.outputId,
                              proposalId,
                            }])}
                          />
                        ) : null}
                        rowOperationEditor={undefined}
                      />
                    </RowDefinitionEditorSessionProvider>
                  )}
                  history={{
                    resourceType: table.document.rootResourceType,
                    steps: persistedConstructionHistory,
                    selected: constructionHistorySelection,
                    disabled: pendingCommands > 0 || publishing || recipeOwner.state.status !== 'saved',
                    onSelect: selectConstructionHistory,
                    onEditStep: editConstructionStep,
                    onRemoveStep: removeConstructionStep,
                    ...(table.document.importedRecipe ? {
                      sourceSteps: (
                        <ImportedSourceStepsSessionProvider key={JSON.stringify([
                            builderDataKey,
                            state.explorerId,
                            table.outputId,
                            state.draftVersion,
                            state.draftDigest,
                          ])} value={importedSourceStepsSession!}>
                          <ImportedSourceSteps
                            editorHost={sourceStepInspectorHost}
                            reviewHost={sourceChangeReviewHost}
                            reviewInEditor={advancedRuleEditorReturn.current !== undefined}
                            editActionRef={sourceStepEditAction}
                            fieldEditActionRef={sourceFieldEditAction} navigatorOnly reviewOwnsCancel
                            outputEditActionRef={sourceOutputEditAction}
                            cancelEditActionRef={sourceStepEditCancelAction}
                            switchOccurrenceActionRef={sourceStepSwitchOccurrenceAction}
                            renderReview={(sourceStep, onCancel, sourceSteps) => <SourceRelationshipReview
                              columnLabels={sourceReviewColumnLabels}
                              ruleEdit={advancedRuleEditorReturn.current !== undefined}
                              key={`${relationshipOwner}:source:${JSON.stringify(sourceSteps ?? sourceStep)}`}
                              client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                              snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
                              outputId={table.outputId} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                              {...(sourceSteps ? { sourceSteps } : { sourceStep })} sharedPreview onStatusChange={sourceReviewStatus} onPreviewStart={beginSourceReview}
                              onApply={applySourceRecipe} onCancel={onCancel} />}
                          />
                        </ImportedSourceStepsSessionProvider>
                      ),
                    } : {}),
                  }}
                  preview={
                    workspaceCandidatePreview ? (
                      candidatePreviewElement
                    ) : (
                      <PreviewTable
                        preview={tablePreview}
                        previewErrorMessage={automaticPreviewFailureMessage}
                        table={table}
                        limit={previewLimit}
                        onLimitChange={(limit) => {
                          setPreviewLimit(limit);
                        }}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                        onRowLineage={tablePreview ? (rowId, offset, signal) => loomClient.rowLineage({
                          project: projectId,
                          explorerId: state.explorerId,
                          authResourcePath,
                          receiptId: tablePreview.receiptId,
                          outputId: tablePreview.outputId,
                          rowId,
                          offset,
                          limit: 25,
                        }, signal) : undefined}
                      />
                    )
                  }
                  editor={externalWorkspaceEditor}
                  inspection={occurrenceInspection}
                  changeReview={pendingChangeReview}
                  onChangeReviewHostChange={setSourceChangeReviewHost}
                />
              </ConstructionWorkspaceSessionProvider>
            ) : null}
          </>
        )}
      </div>
    </main>
  );
};

const BuilderWorkspace = (
  props: BuilderWorkspaceProps,
) => {
  const projectId = props.organization
    ? `${props.organization}/${props.project}`
    : props.project;
  const authResourcePath = props.organization
    ? `/programs/${props.organization}/projects/${props.project}`
    : undefined;
  const ownerKey = builderOwnerKeyFor(
    projectId,
    authResourcePath,
    !props.explorerId || props.explorerId === 'default' ? 'dataframe-default' : props.explorerId,
  );
  return <BuilderWorkspaceScope key={ownerKey} {...props} />;
};

export default BuilderWorkspace;
