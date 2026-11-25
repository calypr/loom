import { useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { Construction, ConstructionOperation, ConstructionStep } from '../../../contracts/types';
import { derivedOccurrences, selectedTable, type BuilderAuthoringState, type DraftTable } from './model';
import { constructionOperationFamilies, type ConstructionHistorySelection, type ConstructionOperationFamily } from '../constructionWorkspace/ConstructionWorkspace';
import { createReshapeEditorEntry, publicScalarListColumnIds, type ConstructionReshapeEditorEntry, type ReshapeEntryKind, type ReshapeForm } from '../constructionOperations/ConstructionReshapeEditor';
import { constructionAppendStageFor } from '../constructionWorkspace/constructionStages';
import { admitAuthoringRequest } from './admission';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import type { useCombineCatalog } from './useCombineCatalog';
import type { useCommandQueue } from './useCommandQueue';
import type { useRecipeEditingOwner } from '../recipeEditing/recipeEditingOwner';
import type { AddColumnsView } from './useAddColumns';

type UseConstructionNavigationOptions = {
  readonly ownerKey: string;
  readonly state: BuilderAuthoringState;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
};

type ConstructionNavigationActionsOptions = {
  readonly table: DraftTable | undefined;
  readonly construction: Construction | undefined;
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'capabilities' | 'cancel' | 'onCandidateChange'>;
  readonly combineCatalog: Pick<ReturnType<typeof useCombineCatalog>, 'cancel' | 'isOwnerCurrent' | 'loadPage'>;
  readonly setConstructionHistorySelection: Dispatch<SetStateAction<ConstructionHistorySelection>>;
  readonly setEditingConstructionStepId: Dispatch<SetStateAction<string | undefined>>;
  readonly setRowIntentSession: Dispatch<SetStateAction<boolean>>;
  readonly setFeatureMode: Dispatch<SetStateAction<'catalog' | 'graph'>>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
  readonly applyCommandsWithResult: ReturnType<typeof useCommandQueue>['applyCommandsWithResult'];
  readonly recipeMutationAllowed: () => boolean;
  readonly rememberOperationFocus: () => void;
  readonly selectAddColumnsView: (view: AddColumnsView) => void;
  readonly canStartCombine: boolean;
  readonly selectedColumnIds: ReadonlyArray<string>;
  readonly recipeOwner: Pick<ReturnType<typeof useRecipeEditingOwner>, 'state'>;
  readonly setRecipeNotice: Dispatch<SetStateAction<string | undefined>>;
  readonly rowIntentSession: boolean;
  readonly setSourceRowsOpen: Dispatch<SetStateAction<boolean>>;
  readonly editingConstructionStepId: string | undefined;
  readonly previousDraftRevisionId: string | undefined;
  readonly setColumnSelection: (next: { readonly kind: 'empty' }) => void;
  readonly applyCommands: ReturnType<typeof useCommandQueue>['applyCommands'];
  readonly editingConstructionStep: ConstructionStep | undefined;
};

const editableConstructionFamily = (
  operation: ConstructionOperation,
): Extract<ConstructionOperationFamily, 'KEEP_ROWS' | 'CALCULATE' | 'RESHAPE' | 'COMBINE'> | undefined => {
  switch (operation.kind) {
    case 'FILTER':
    case 'RELATED_ELIGIBILITY': return 'KEEP_ROWS';
    case 'DERIVE': return 'CALCULATE';
    case 'PIVOT':
    case 'CODED_PIVOT':
    case 'UNPIVOT':
    case 'GROUP':
    case 'CODED_GROUP':
    case 'EXPAND':
    case 'RELATED_EXPAND': return 'RESHAPE';
    case 'RELATED_SOURCE': return undefined;
    case 'RELATED_FIELD': return undefined;
    case 'COMBINE': return operation.combine.kind === 'KEY_JOIN' || operation.combine.kind === 'APPEND' || operation.combine.kind === 'MEMBERSHIP'
      ? 'COMBINE'
      : undefined;
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

export const useConstructionNavigation = ({ ownerKey, state, latestState }: UseConstructionNavigationOptions) => {
  const [activeConstructionSelection, setActiveConstructionSelection] = useState<{
    readonly ownerKey: string;
    readonly explorerId: string;
    readonly outputId: string;
    readonly family: ConstructionOperationFamily;
  }>();
  const [reshapeEntry, setReshapeEntry] = useState(0);
  const [reshapeEntryKind, setReshapeEntryKind] = useState<ReshapeEntryKind>('choose');
  const [reshapeFormKind, setReshapeFormKind] = useState<ReshapeForm['kind']>('choose');
  const [reshapeInitialEntry, setReshapeInitialEntry] = useState<{
    readonly editorKey: string;
    readonly entry: ConstructionReshapeEditorEntry;
  }>();
  const activeConstructionFamily = activeConstructionSelection?.ownerKey === ownerKey &&
    activeConstructionSelection.explorerId === state.explorerId &&
    activeConstructionSelection.outputId === state.selectedOutputId
    ? activeConstructionSelection.family
    : undefined;
  const setActiveConstructionFamily = (
    next: ConstructionOperationFamily | undefined |
      ((current: ConstructionOperationFamily | undefined) => ConstructionOperationFamily | undefined),
  ) => {
    const currentState = latestState.current;
    const outputId = currentState.selectedOutputId;
    if (!outputId) {
      setActiveConstructionSelection(undefined);
      return;
    }
    setActiveConstructionSelection((current) => {
      const currentFamily = current?.ownerKey === ownerKey &&
        current.explorerId === currentState.explorerId &&
        current.outputId === outputId
        ? current.family
        : undefined;
      const family = typeof next === 'function' ? next(currentFamily) : next;
      return family
        ? { ownerKey, explorerId: currentState.explorerId, outputId, family }
        : undefined;
    });
  };
  const actionsFor = ({
    table, construction, constructionLifecycle, combineCatalog, setConstructionHistorySelection,
    setEditingConstructionStepId, setRowIntentSession, setFeatureMode, setMessage, applyCommandsWithResult,
    recipeMutationAllowed, rememberOperationFocus, selectAddColumnsView, canStartCombine,
    selectedColumnIds, recipeOwner, setRecipeNotice, rowIntentSession, setSourceRowsOpen,
    editingConstructionStepId, previousDraftRevisionId, setColumnSelection, applyCommands, editingConstructionStep
  }: ConstructionNavigationActionsOptions) => {
    const reshapeEditorKeyFor = (entry: number, kind: ReshapeEntryKind, stageId: string) =>
      `${ownerKey}:${state.catalog.snapshotToken}:${state.draftVersion}:${state.draftDigest}:${table?.outputId ?? ''}:${stageId}:${entry}:${kind}`;
    const startCombine = async () => {
      const current = latestState.current;
      const currentTable = selectedTable(current);
      const requestOwner = { ownerKey, explorerId: current.explorerId };
      const rootNodeId = currentTable?.document.rootResourceType
        ? derivedOccurrences(currentTable, current.catalog)[0]?.nodeId
        : undefined;
      if (!currentTable || !rootNodeId || !current.catalog.snapshotToken) {
        setMessage('Choose a rooted table before starting a new table from published versions.');
        return;
      }

      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      setReshapeEntryKind('choose');
      setReshapeFormKind('choose');

      const existingTitles = new Set(current.tables.map((candidate) => candidate.title.trim().toLocaleLowerCase()));
      const baseTitle = currentTable.title.trim() ? `${currentTable.title.trim()} combined` : 'Combined table';
      let title = baseTitle;
      let suffix = 2;
      while (existingTitles.has(title.toLocaleLowerCase())) {
        title = `${baseTitle} ${suffix++}`;
      }

      const created = await applyCommandsWithResult([{
        type: 'CREATE_TABLE',
        title,
        rootNodeId,
      }]);
      if (!combineCatalog.isOwnerCurrent(requestOwner)) return;
      const createdTable = created?.results.find(
        (result) => result.type === 'TABLE_CREATED' && Boolean(result.outputId),
      );
      if (!createdTable?.outputId) {
        return;
      }
      setFeatureMode('catalog');
      setActiveConstructionFamily('COMBINE');
      void combineCatalog.loadPage();
    };

    const selectConstructionFamily = (family: ConstructionOperationFamily) => {
      if (!recipeMutationAllowed()) return;
      rememberOperationFocus();
      const willOpen = activeConstructionFamily !== family;
      setRowIntentSession(false);
      if (family === 'ADD_COLUMNS' && willOpen) {
        const admission = admitAuthoringRequest(state.tables, { kind: 'add-columns-proposal', outputId: table?.outputId ?? '' });
        if (admission.kind === 'refused') {
          setMessage(admission.refusal.message);
          return;
        }
        selectAddColumnsView('all');
      }
      if (family === 'COMBINE' && willOpen) {
        if (!canStartCombine) return;
        void startCombine();
        return;
      }
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      setReshapeEntryKind('choose');
      setReshapeFormKind('choose');
      setActiveConstructionFamily((current) => current === family ? undefined : family);
    };
    const chooseRelatedRows = () => {
      if (!recipeMutationAllowed()) return;
      rememberOperationFocus();
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      setRowIntentSession(true);
      setReshapeEntryKind('related-expand');
      setReshapeFormKind('related-expand');
      setReshapeEntry((current) => current + 1);
      setActiveConstructionFamily('RESHAPE');
    };
    const chooseReshapeRows = (
      kind: 'group' | 'source-group' | 'coded-group' | 'expand' | 'expand-signed' | 'categories' | 'pivot' | 'coded-pivot' | 'unpivot',
      preferredColumnIds?: ReadonlyArray<string>,
    ) => {
      if (!recipeMutationAllowed()) return;
      if (kind === 'expand' && preferredColumnIds?.length !== 1) return;
      rememberOperationFocus();
      if (kind !== 'expand') setRowIntentSession(true);

      let entry: ConstructionReshapeEditorEntry | undefined;
      let entryStageId: string | undefined;
      if ((kind === 'expand' || kind === 'expand-signed' || kind === 'group') && table && constructionLifecycle.capabilities.status === 'ready') {
        const response = constructionLifecycle.capabilities.response;
        const stage = response.selectedStage;
        const appendStageId = constructionAppendStageFor(response.baseConstruction, table.document.rows);
        const supported = stage.id === appendStageId && stage.capabilities.some((candidate) =>
          candidate.kind === (kind === 'expand' ? 'EXPAND' : 'GROUP') && candidate.supported,
        );
        if (kind === 'expand') {
          const requestedColumnId = preferredColumnIds?.[0];
          if (!supported || !publicScalarListColumnIds(stage).includes(requestedColumnId ?? '')) return;
          entry = createReshapeEditorEntry({
            construction: construction ?? response.baseConstruction,
            capabilities: response,
            kind,
            selectedColumns: preferredColumnIds,
          });
          if (!entry) return;
          entryStageId = stage.id;
        } else if (kind === 'expand-signed') {

          const signedSupported = stage.id === appendStageId
            && (response.expandSourceInput?.supported ?? false)
            && (response.expandSourceInput?.choices ?? []).some((choice) => choice.isPopulated);
          if (!signedSupported) return;
        } else if (supported) {
          entry = createReshapeEditorEntry({
            construction: construction ?? response.baseConstruction,
            capabilities: response,
            kind,
            selectedColumns: selectedColumnIds,
          });
          entryStageId = stage.id;
        }
      } else if (kind === 'expand' || kind === 'expand-signed') {
        return;
      }

      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      const editorKind = kind === 'expand-signed' ? 'expand' : kind;
      setReshapeEntryKind(editorKind);
      setReshapeFormKind(kind === 'categories' ? 'choose' : kind === 'expand-signed' ? 'expand' : kind);
      const nextEntry = reshapeEntry + 1;
      if (entry && entryStageId) {
        if (entry.candidateIntent) constructionLifecycle.onCandidateChange(entry.candidateIntent);
        else constructionLifecycle.cancel();
        setReshapeInitialEntry({
          editorKey: reshapeEditorKeyFor(nextEntry, editorKind, entryStageId),
          entry,
        });
      } else {
        setReshapeInitialEntry(undefined);
      }
      setReshapeEntry(nextEntry);
      setActiveConstructionFamily('RESHAPE');
    };
    const selectConstructionHistory = (selection: ConstructionHistorySelection) => {
      if (recipeOwner.state.status !== 'saved') {
        setRecipeNotice('Apply or Cancel the active recipe change before editing another step.'); return;
      }
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setRowIntentSession(false);
      setConstructionHistorySelection(selection);
      setEditingConstructionStepId(undefined);
      setActiveConstructionFamily(undefined);
    };
    const editConstructionStep = (stepId: string) => {
      if (!recipeMutationAllowed()) return;
      const step = construction?.steps.find((candidate) => candidate.id === stepId);
      if (!step) return;
      rememberOperationFocus();
      if (step.operation.kind === 'RELATED_SOURCE' || step.operation.kind === 'RELATED_FIELD') {
        constructionLifecycle.cancel();
        setConstructionHistorySelection({ kind: 'step', stepId });
        setEditingConstructionStepId(stepId);
        setActiveConstructionFamily(undefined);
        return;
      }
      const family = editableConstructionFamily(step.operation);
      if (!family) return;
      setRowIntentSession(false);
      constructionLifecycle.cancel();
      if (family === 'COMBINE') {
        void combineCatalog.loadPage();
      } else {
        combineCatalog.cancel();
      }
      setConstructionHistorySelection({ kind: 'step', stepId });
      setEditingConstructionStepId(stepId);
      setActiveConstructionFamily(family);
      if (family === 'RESHAPE') setReshapeFormKind('choose');
      setReshapeInitialEntry(undefined);
    };
    const closeConstructionWorkspace = () => {
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setEditingConstructionStepId(undefined);
      setActiveConstructionFamily(undefined);
      setRowIntentSession(false);
      setReshapeEntryKind('choose');
      setReshapeFormKind('choose');
      setReshapeInitialEntry(undefined);
    };
    const cancelConstructionEditor = closeConstructionWorkspace;
    const switchRowIntent = (_intent: 'records' | 'reshape') => {
      if (!rowIntentSession) return;
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      setReshapeEntryKind('choose');
      setReshapeFormKind('choose');
      setReshapeInitialEntry(undefined);
      setActiveConstructionFamily(undefined);
    };
    const closeRowIntentSession = () => {
      setSourceRowsOpen(false);
      if (!rowIntentSession) return;
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
      setReshapeEntryKind('choose');
      setReshapeFormKind('choose');
      setReshapeInitialEntry(undefined);
      setActiveConstructionFamily(undefined);
      setRowIntentSession(false);
    };
    const removeConstructionStep = (stepId: string) => {
      if (!recipeMutationAllowed()) return;
      if (!construction?.steps.some((step) => step.id === stepId)) return;
      combineCatalog.cancel();
      constructionLifecycle.onCandidateChange({
        candidateConstruction: {
          ...construction,
          steps: construction.steps.filter((step) => step.id !== stepId),
        },
        removeStepIds: [stepId],
      });
      setConstructionHistorySelection({ kind: 'step', stepId });

      if (editingConstructionStepId !== stepId) setEditingConstructionStepId(undefined);
      setActiveConstructionFamily(undefined);
    };
    const restorePreviousDraft = async () => {
      if (!recipeMutationAllowed()) return;
      if (!previousDraftRevisionId) return;
      constructionLifecycle.cancel();
      combineCatalog.cancel();
      setActiveConstructionFamily(undefined);
      setEditingConstructionStepId(undefined);
      setConstructionHistorySelection({ kind: 'source' });
      setColumnSelection({ kind: 'empty' });
      await applyCommands([{
        type: 'RESTORE_DRAFT_REVISION',
        draftRevisionId: previousDraftRevisionId,
      }]);
    };

    const activeOperation = constructionOperationFamilies.find(
      (candidate) => candidate.family === activeConstructionFamily,
    );
    const activeReshapeEditorKey = table && constructionLifecycle.capabilities.status === 'ready'
      ? `${reshapeEditorKeyFor(
          reshapeEntry,
          reshapeEntryKind,
          constructionLifecycle.capabilities.response.selectedStage.id,
        )}:${!editingConstructionStep && reshapeFormKind === 'choose' ? selectedColumnIds.join(',') : ''}`
      : undefined;
    const activeReshapeInitialEntry = reshapeInitialEntry && reshapeInitialEntry.editorKey === activeReshapeEditorKey
      ? reshapeInitialEntry.entry
      : undefined;

    const activeReshapeInitialKind = reshapeEntryKind === 'expand' && !activeReshapeInitialEntry && reshapeFormKind !== 'expand'
      ? 'choose'
      : reshapeEntryKind;
    const showReshapeColumnSelection = activeOperation?.family === 'RESHAPE' &&
      (reshapeFormKind === 'choose' || reshapeFormKind === 'unpivot' || reshapeFormKind === 'coded-pivot');
    const hideGenericReshapeSelection = activeOperation?.family === 'RESHAPE' && !showReshapeColumnSelection;
    return { startCombine, selectConstructionFamily, chooseRelatedRows, chooseReshapeRows,
    selectConstructionHistory, editConstructionStep, closeConstructionWorkspace, cancelConstructionEditor,
    switchRowIntent, closeRowIntentSession, removeConstructionStep, restorePreviousDraft,
    activeOperation, activeReshapeEditorKey, activeReshapeInitialEntry, activeReshapeInitialKind,
    showReshapeColumnSelection, hideGenericReshapeSelection };
  };
  return { activeConstructionFamily, setActiveConstructionFamily, setActiveConstructionSelection,
    reshapeEntryKind, setReshapeEntryKind, reshapeFormKind, setReshapeFormKind, actionsFor };
};
