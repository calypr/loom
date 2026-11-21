import { useRef, useState, type Dispatch } from 'react';
import type { ExplorerBuilderColumn, ExplorerBuilderCommand, ProjectedField, SourceRelationshipChange } from '../../../types';
import { routeNode, type BuilderAuthoringState, type DraftTable } from './model';
import { fieldCatalogItems } from '../catalogItems';
import type { BuilderAction } from './reducer';
import type { useSourceRecipe } from './useSourceRecipe';
import type { useRecipeEditingOwner } from '../recipeEditing/recipeEditingOwner';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import type { useRecipeFocus } from './useRecipeFocus';

type TraversalActionsOptions = {
  readonly state: BuilderAuthoringState;
  readonly table: DraftTable | undefined;
  readonly sourceProposal: ReturnType<typeof useSourceRecipe>['sourceProposal'];
  readonly relationshipOwner: string;
  readonly recipeOwner: ReturnType<typeof useRecipeEditingOwner>;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'proposal'>;
  readonly lastAcceptedOutputPreview: ReturnType<typeof useSourceRecipe>['lastAcceptedOutputPreview'];
  readonly pendingRelationship: SourceRelationshipChange | undefined;
  readonly cancelRecipeRelationship: (restoreFocus?: boolean) => boolean;
  readonly dispatch: Dispatch<BuilderAction>;
  readonly savedNetwork: ReturnType<typeof useSourceRecipe>['savedNetwork'];
  readonly recipeMutationAllowed: () => boolean;
  readonly rememberRecipeFocus: ReturnType<typeof useRecipeFocus>['rememberRecipeFocus'];
  readonly recipeEditToken: ReturnType<typeof useSourceRecipe>['recipeEditToken'];
  readonly onSwitchSavedOccurrence: (occurrenceId: string, restoredEditor: boolean) => void;
  readonly applyCommands: (commands: ExplorerBuilderCommand[]) => void;
  readonly canEditColumnPolicy: (column: ExplorerBuilderColumn) => boolean;
  readonly pendingCommands: number;
  readonly publishing: boolean;
  readonly setRecipeNotice: (notice: string | undefined) => void;
  readonly sourceStepEditDirty?: { current: boolean };
  readonly sourceStepEditCancelAction?: { current: (() => void) | undefined };
};

export const useTraversalProjectedFields = () => {
  const defaultSourceStepEditDirty = useRef(false);
  const defaultSourceStepEditCancelAction = useRef<(() => void) | undefined>(undefined);
  const [occurrenceInspectorHost, setOccurrenceInspectorHost] = useState<HTMLElement | null>(null);
  const [editingRecipeColumn, setEditingRecipeColumn] = useState<{ owner: string; columnId: string }>();
  const [sourceStepInspectorHost, setSourceStepInspectorHost] = useState<HTMLDivElement | null>(null);
  const [activeSourceStepId, setActiveSourceStepId] = useState<string>();
  const columnPolicyRequest = useRef<AbortController | undefined>(undefined);
  const sourceStepEditAction = useRef<((stepId: string) => void) | undefined>(undefined);
  const sourceStepSwitchOccurrenceAction = useRef<((occurrenceId: string) => boolean) | undefined>(undefined);
  const sourceFieldEditAction = useRef<((stepId: string, fieldId: string) => void) | undefined>(undefined);
  const sourceOutputEditAction = useRef<((stepId: string, outputKey: string) => void) | undefined>(undefined);
  const rowSettingsOpenAction = useRef<(() => void) | undefined>(undefined);
  const rowSettingsCloseAction = useRef<(() => void) | undefined>(undefined);
  const [candidateInspection, setCandidateInspection] = useState<{ key: string; occurrenceId: string }>();
  const actionsFor = ({
    state, table, sourceProposal, relationshipOwner, recipeOwner, constructionLifecycle,
    lastAcceptedOutputPreview, pendingRelationship, cancelRecipeRelationship, dispatch,
    savedNetwork, recipeMutationAllowed, rememberRecipeFocus, recipeEditToken,
    applyCommands, canEditColumnPolicy, pendingCommands, publishing, setRecipeNotice, onSwitchSavedOccurrence,
    sourceStepEditDirty: sourceStepEditDirtyArg = defaultSourceStepEditDirty,
    sourceStepEditCancelAction: sourceStepEditCancelActionArg = defaultSourceStepEditCancelAction,
  }: TraversalActionsOptions) => {
    const sourceStepEditDirty = sourceStepEditDirtyArg;
    const sourceStepEditCancelAction = sourceStepEditCancelActionArg;
    const populatedFieldItems = table
      ? fieldCatalogItems(state.catalog, table.document.rootResourceType ?? '', undefined, '', undefined, true)
        .filter((item) => item.kind === 'FIELD')
      : [];
    const populatedFieldCandidates = populatedFieldItems.flatMap((item) => item.kind === 'FIELD' ? [item.candidate] : []);
    const sourceCandidate = sourceProposal?.owner === relationshipOwner ? sourceProposal : undefined;
    const sourceReviewColumnLabels = table ? new Map(table.document.columns.map((column) => [column.columnId ?? column.column, column.label || column.column])) : undefined;
    const rootCandidate = recipeOwner.state.target?.kind === 'root' ? recipeOwner.state.candidate : undefined;
    const candidatePreview = rootCandidate?.preview ?? sourceCandidate?.preview ?? (constructionLifecycle.proposal.status === 'ready' ||
      constructionLifecycle.proposal.status === 'applying'
      ? constructionLifecycle.proposal.preview
      : undefined);
    const lastOutputPreview = lastAcceptedOutputPreview.current;
    const retainedOutputPreviewReceipt = lastOutputPreview?.owner === relationshipOwner && lastOutputPreview.outputId === table?.outputId &&
      lastOutputPreview.snapshotToken === state.catalog.snapshotToken && lastOutputPreview.draftVersion === state.draftVersion &&
      lastOutputPreview.draftDigest === state.draftDigest ? lastOutputPreview.receiptId : undefined;
    const generatedOutputRevision = candidatePreview?.receiptId ?? retainedOutputPreviewReceipt ??
      `saved:${state.catalog.snapshotToken}:${state.draftVersion}:${state.draftDigest}`;
    const proposalResponse = 'response' in constructionLifecycle.proposal
      ? constructionLifecycle.proposal.response
      : undefined;
    const proposalPresentationTable = table && rootCandidate
      ? { ...table, document: rootCandidate.candidateDocument! }
      : table && sourceCandidate
      ? { ...table, document: sourceCandidate.proposal.candidateDocument }
      : table && proposalResponse
      ? {
          ...table,
          document: {
            ...table.document,
            construction: proposalResponse.candidateConstruction,
          },
        }
      : undefined;

    const candidateInspectionKey = candidatePreview && proposalPresentationTable
      ? JSON.stringify([relationshipOwner, candidatePreview.receiptId, candidatePreview.outputId]) : undefined;
    const inspectedOccurrenceId = candidateInspectionKey && proposalPresentationTable
      ? candidateInspection?.key === candidateInspectionKey && routeNode(proposalPresentationTable.document.route, candidateInspection.occurrenceId)
        ? candidateInspection.occurrenceId
        : routeNode(proposalPresentationTable.document.route, state.selectedOccurrenceId) ? state.selectedOccurrenceId : proposalPresentationTable.document.route.occurrenceId
      : state.selectedOccurrenceId;
    const selectTraversalScope = (occurrenceId: string) => {
      const editing = recipeOwner.getState();
      const relationshipReviewActive = Boolean(
        pendingRelationship &&
        (editing.target?.kind === 'connection' || editing.target?.kind === 'relationship'),
      );
      if (editing.status === 'applying' && relationshipReviewActive) return;
      const candidateOccurrence = Boolean(
        candidateInspectionKey && proposalPresentationTable &&
        routeNode(proposalPresentationTable.document.route, occurrenceId),
      );
      const savedOccurrence = Boolean(table && routeNode(table.document.route, occurrenceId));
      if (candidateInspectionKey && proposalPresentationTable) {
        if (!candidateOccurrence) return;
        setCandidateInspection({ key: candidateInspectionKey, occurrenceId });
        if (!savedOccurrence) return;
      }
      if (relationshipReviewActive && savedOccurrence) {
        const sameSavedInspection = occurrenceId === state.selectedOccurrenceId &&
          (!candidateInspectionKey || occurrenceId === inspectedOccurrenceId);
        if (!sameSavedInspection) {
          if (!cancelRecipeRelationship(false)) return;
          setCandidateInspection(undefined);
        }
      } else if (candidateInspectionKey && proposalPresentationTable) {
        return;
      }
      if (savedOccurrence && occurrenceId !== state.selectedOccurrenceId &&
          (editing.status === 'saved' || editing.target?.kind === 'sourceStep')) {
        onSwitchSavedOccurrence(occurrenceId, sourceStepSwitchOccurrenceAction.current?.(occurrenceId) ?? false);
      }
      dispatch({ type: 'selectOccurrence', occurrenceId });
    };
    const editTraversalColumn = (columnId: string) => {
      const binding = savedNetwork?.owner === relationshipOwner ? savedNetwork.result.network.bindings.find((item) => item.columnId === columnId) : undefined;
      if (binding?.sourceStepId) sourceStepEditAction.current?.(binding.sourceStepId);
      else {
        if (!recipeMutationAllowed()) return;
        rememberRecipeFocus();
        const token = recipeOwner.begin({ kind: 'column', columnId });
        if (!token) return;
        recipeEditToken.current = token;
        setEditingRecipeColumn({ owner: relationshipOwner, columnId });
      }
    };
    const traversalTable = proposalPresentationTable ?? table;
    const traversalNetwork = rootCandidate?.candidateNetwork ?? sourceCandidate?.proposal.candidateNetwork ?? (savedNetwork?.owner === relationshipOwner ? savedNetwork.result.network : undefined);
    const traversalRowIdentity = rootCandidate?.candidateRowIdentity ?? sourceCandidate?.proposal.candidateRowIdentity ?? (proposalPresentationTable ? undefined : savedNetwork?.owner === relationshipOwner ? savedNetwork.result.rowIdentity : undefined);
    const traversalSourceSteps = traversalTable?.document.importedSource?.steps ?? [];
    const resourceTypeChoices = Array.from(new Set((state.catalog.nodes ?? []).filter((node) => node.populated).map((node) => node.resourceType))).sort();
    const sourcePathChoices = Array.from(new Set(populatedFieldCandidates.flatMap((candidate) => {
      const boundaries = candidate.repeatedBoundaries ?? [];
      const paths = boundaries.length
        ? boundaries.map((boundary) => boundary.path)
        : candidate.repeated && (!candidate.fieldPath.includes('[]') || candidate.fieldPath.endsWith('[]'))
          ? [candidate.fieldPath] : [];
      return paths.filter((path) => !path.includes('[][]'))
        .map((path) => `root.${path.endsWith('[]') ? path : `${path}[]`}`);
    }))).sort();
    const fieldPathChoices = Array.from(new Set(populatedFieldCandidates.map((candidate) => candidate.fieldPath))).sort();
    const fieldKindChoices = Array.from(new Set(populatedFieldCandidates.map((candidate) => candidate.logicalType))).sort();
    const catalogOutputsForStep = (stepId: string) => {
      const step = traversalSourceSteps.find((candidate) => candidate.id === stepId && candidate.kind === 'CATALOG_PROJECTION');
      if (!step?.parameters?.catalog) return [];
      const overrides = step.parameters.catalog.outputs ?? [];
      const bindings = traversalNetwork?.bindings.filter((binding) => binding.status === 'EXACT' && binding.sourceStepId === stepId && binding.outputKey) ?? [];
      const emitted = bindings.flatMap((binding) => {
        const matches = traversalTable?.document.columns.filter((column) => (column.columnId ?? column.column) === binding.columnId) ?? [];
        if (matches.length !== 1 || !binding.outputKey) return [];
        const override = overrides.find((item) => item.fieldPath === binding.outputKey);
        return [{ outputKey: binding.outputKey, columnId: binding.columnId, defaultName: matches[0].label || matches[0].column, enabled: override?.enabled ?? true }];
      });
      const emittedKeys = new Set(emitted.map((output) => output.outputKey));
      const persistedDisabled = overrides.filter((output) => !output.enabled && !emittedKeys.has(output.fieldPath))
        .map((output) => ({ outputKey: output.fieldPath, defaultName: output.name ?? output.fieldPath, enabled: false }));
      return [...emitted, ...persistedDisabled];
    };
    const pivotOutputsForStep = (stepId: string) => {
      const step = traversalSourceSteps.find((candidate) => candidate.id === stepId && candidate.kind === 'PIVOT');
      if (!step?.parameters?.pivot) return [];
      const overrides = step.parameters.pivot.outputs ?? [];
      const bindings = traversalNetwork?.bindings.filter((binding) => binding.status === 'EXACT' && binding.sourceStepId === stepId && binding.outputKey) ?? [];
      const emitted = bindings.flatMap((binding) => {
        const matches = traversalTable?.document.columns.filter((column) => (column.columnId ?? column.column) === binding.columnId) ?? [];
        if (matches.length !== 1 || !binding.outputKey) return [];
        const override = overrides.find((item) => item.key === binding.outputKey);
        return [{ outputKey: binding.outputKey, columnId: binding.columnId, defaultName: matches[0].label || matches[0].column, enabled: override?.enabled ?? true }];
      });
      const emittedKeys = new Set(emitted.map((output) => output.outputKey));
      const persistedDisabled = overrides.filter((output) => !output.enabled && !emittedKeys.has(output.key))
        .map((output) => ({ outputKey: output.key, defaultName: output.name || output.key, enabled: false }));
      return [...emitted, ...persistedDisabled];
    };
    const traversalOperationLabels = new Map(traversalSourceSteps.flatMap((step) => {
      const parameters = step.parameters;
      const name = parameters?.dynamic?.name ?? parameters?.extension?.name ?? parameters?.pivot?.name ?? parameters?.catalog?.name ?? parameters?.identity?.name;
      return name ? [[step.id, `${step.kind === 'DYNAMIC_COLUMN' ? 'Columns from source values' : step.kind === 'PIVOT' ? 'Pivot' : step.kind === 'CATALOG_PROJECTION' ? 'Populated fields' : step.kind === 'IDENTITY' ? 'Row identity' : 'Extension columns'}: ${name}`] as const] : [];
    }));
    const savedProjectedFieldInventory = savedNetwork?.owner === relationshipOwner && savedNetwork.result.network.outputId === table?.outputId
      ? savedNetwork.result.network.projectedFields ?? [] : undefined;
    const projectedFieldsForStep = (stepId: string) => {
      const inventory = savedProjectedFieldInventory ?? [];
      const occurrenceIds = new Set(inventory.filter((field) => field.sourceStepId === stepId && field.occurrenceId).map((field) => field.occurrenceId));
      if (occurrenceIds.size > 1) return [];
      const step = traversalSourceSteps.find((candidate) => candidate.id === stepId);
      if (!step) return [];
      const matchingOccurrences = savedNetwork?.result.network.occurrences.filter((occurrence) => occurrence.sourceStepId === step.parentId) ?? [];
      const fallbackOccurrenceId = matchingOccurrences.length === 1
        ? matchingOccurrences[0].occurrenceId
        : !step.parentId ? table?.document.route.occurrenceId : undefined;
      const occurrenceId = [...occurrenceIds][0] ?? fallbackOccurrenceId;
      return occurrenceId ? inventory.filter((field) =>
        field.occurrenceId === occurrenceId && (field.sourceStepId === stepId || field.owner === 'AUTHORED'),
      ) : [];
    };
    const occurrenceForStep = (stepId: string) => {
      const step = traversalSourceSteps.find((candidate) => candidate.id === stepId);
      if (!step) return undefined;
      if (!step.parentId) return table?.document.route.occurrenceId;
      const parents = traversalSourceSteps.filter((candidate) => candidate.id === step.parentId);
      return parents.length === 1 ? parents[0].occurrenceId : undefined;
    };
    const projectedFieldRemovalDisabledReason = pendingCommands > 0
      ? 'Wait for the current table update to finish before removing a projected field.'
      : publishing ? 'Wait for publishing to finish before removing a projected field.'
        : !savedProjectedFieldInventory ? 'Load the current table field inventory before removing a projected field.'
          : recipeOwner.state.status === 'saved' ? undefined
            : recipeOwner.state.status === 'editing' && recipeOwner.state.target?.kind === 'sourceStep' && !sourceStepEditDirty.current
              ? undefined
              : sourceStepEditDirty.current
                ? 'Apply or Cancel the pending source-step edits before removing a projected field.'
                : 'Apply or Cancel the active recipe change before removing a projected field.';
    const editableProjectedFieldIds = new Set((table?.document.columns ?? []).flatMap((column) =>
      column.columnId && canEditColumnPolicy(column) ? [column.columnId] : []));
    const projectedFieldRemovalCommand = (field: ProjectedField): ExplorerBuilderCommand | undefined => {
      if (!table || !savedProjectedFieldInventory?.includes(field)) return;
      const { columnId, column } = field;
      const outputId = table.outputId;
      if (columnId && field.removal !== 'UNAVAILABLE' &&
          savedProjectedFieldInventory.filter((candidate) => candidate.columnId === columnId).length === 1) {
        return { type: 'REMOVE_PROJECTED_FIELD', outputId, columnId };
      }
      if (field.owner === 'PINNED_SOURCE') return;
      const matches = table.document.columns.filter((candidate) => candidate.column === column);
      if (matches.length === 1 && matches[0].columnId === columnId && matches[0].source.kind !== 'imported') {
        return { type: 'REMOVE_COLUMN', outputId, column };
      }
    };
    const canRemoveProjectedField = (field: ProjectedField) => Boolean(projectedFieldRemovalCommand(field));
    const removeProjectedField = (field: ProjectedField) => {
      const command = projectedFieldRemovalCommand(field);
      if (!command) {
        setRecipeNotice('This field no longer has a supported removal action. Refresh the field inventory and try again.');
        return;
      }
      if (projectedFieldRemovalDisabledReason || !table) return;
      if (recipeOwner.state.status !== 'saved') sourceStepEditCancelAction.current?.();
      if (recipeOwner.getState().status !== 'saved' || !recipeMutationAllowed()) return;
      setRecipeNotice(undefined);
      rememberRecipeFocus();
      applyCommands([command]);
    };
    const renameProjectedField = (columnId: string, label: string) => {
      if (projectedFieldRemovalDisabledReason || !table || !label.trim()) return;
      const matches = table.document.columns.filter((column) => column.columnId === columnId);
      if (matches.length !== 1) return;
      if (recipeOwner.state.status !== 'saved') {
        if (recipeOwner.state.status !== 'editing' || recipeOwner.state.target?.kind !== 'sourceStep' || sourceStepEditDirty.current) return;
        sourceStepEditCancelAction.current?.();
      }
      if (recipeOwner.getState().status !== 'saved' || !recipeMutationAllowed()) return;
      const column = matches[0];
      applyCommands([{ type: 'UPDATE_COLUMN', outputId: table.outputId, column: column.column,
        columnValue: { ...column, label: label.trim() } }]);
    };
    const editProjectedField = (field: ProjectedField) => {
      if (projectedFieldRemovalDisabledReason) return;
      if (field.sourceStepId && field.sourceFieldId) {
        sourceFieldEditAction.current?.(field.sourceStepId, field.sourceFieldId);
        return;
      }
      if (!field.columnId || !editableProjectedFieldIds.has(field.columnId)) return;
      if (recipeOwner.getState().status !== 'saved') sourceStepEditCancelAction.current?.();
      if (recipeOwner.getState().status === 'saved') editTraversalColumn(field.columnId);
    };
    const editTraversalRows = () => {
      if (!recipeMutationAllowed() || !table) return;
      rememberRecipeFocus();
      if (table.document.importedRecipe) {
        const sourceStep = table.document.importedSource?.steps.find((step) => step.kind === 'EXPANSION')
          ?? table.document.importedSource?.steps.find((step) => step.kind === 'OUTPUT');
        if (sourceStep) { sourceStepEditAction.current?.(sourceStep.id); return; }
      }
      rowSettingsOpenAction.current?.();
    };
    return { sourceCandidate, sourceReviewColumnLabels, rootCandidate, candidatePreview, generatedOutputRevision, proposalResponse, proposalPresentationTable, inspectedOccurrenceId, selectTraversalScope, editTraversalColumn, traversalTable, traversalNetwork, traversalRowIdentity, resourceTypeChoices, sourcePathChoices, fieldPathChoices, fieldKindChoices, catalogOutputsForStep, pivotOutputsForStep, traversalOperationLabels, editTraversalRows, projectedFieldsForStep, occurrenceForStep, removeProjectedField, canRemoveProjectedField, editProjectedField, renameProjectedField, editableProjectedFieldIds, projectedFieldRemovalDisabledReason };
  };
  return { occurrenceInspectorHost, setOccurrenceInspectorHost, editingRecipeColumn, setEditingRecipeColumn,
    sourceStepInspectorHost, setSourceStepInspectorHost, activeSourceStepId, setActiveSourceStepId,
    columnPolicyRequest, sourceStepEditAction, sourceStepSwitchOccurrenceAction, sourceFieldEditAction, sourceOutputEditAction,
    rowSettingsOpenAction, rowSettingsCloseAction, actionsFor };
};
