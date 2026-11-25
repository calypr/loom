import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import { useCreateExplorerAuthoringMutation, useDeleteExplorerAuthoringMutation, type useGetExplorerAuthoringExplorersQuery, type useGetExplorerAuthoringCapabilityV2Query } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import type { BuilderAuthoringState, DraftTable } from '../model/authoring';
import type { BuilderAction } from '../model/reducer';
import type { useCommandQueueActions } from '../session/useCommandQueueActions';

type UseTableExplorerCrudOptions = {
  readonly state: BuilderAuthoringState;
  readonly table: DraftTable | undefined;
  readonly draftAccess: { getState(): BuilderAuthoringState };
  readonly dispatch: Dispatch<BuilderAction>;
  readonly recipeMutationAllowed: () => boolean;
  readonly firstTableActionPending: MutableRefObject<boolean>;
  readonly tableCreatorOpen: boolean;
  readonly setFirstTableName: Dispatch<SetStateAction<string>>;
  readonly setTableCreatorOpen: Dispatch<SetStateAction<boolean>>;
  readonly rememberSelectedTable: (outputId: string) => void;
  readonly applyCommands: ReturnType<typeof useCommandQueueActions>['applyCommands'];
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly selectExplorer: (explorerId: string) => void;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly explorers: Pick<ReturnType<typeof useGetExplorerAuthoringExplorersQuery>, 'data' | 'refetch'>;
  readonly capabilities: Pick<ReturnType<typeof useGetExplorerAuthoringCapabilityV2Query>, 'data'>;
  readonly effectiveExplorerId: string;
  readonly invalidateCompile: () => void;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
  readonly opaqueId: (prefix: 'output' | 'tab') => string;
};

type UseTableExplorerCrudResult = {
  readonly addTable: () => void;
  readonly duplicateTable: () => void;
  readonly renameTable: (outputId: string, value: string) => void;
  readonly renameTableById: (outputId: string) => void;
  readonly deleteSelectedTable: () => void;
  readonly createCustomExplorer: (title: string, fromCurrent: boolean) => Promise<void>;
  readonly deleteCurrentExplorer: () => Promise<void>;
  readonly createStatus: ReturnType<typeof useCreateExplorerAuthoringMutation>[1];
  readonly deleteStatus: ReturnType<typeof useDeleteExplorerAuthoringMutation>[1];
};

export const useTableExplorerCrud = ({
  state, table, draftAccess, dispatch, recipeMutationAllowed, firstTableActionPending,
  tableCreatorOpen, setFirstTableName, setTableCreatorOpen, rememberSelectedTable, applyCommands,
  projectId, authResourcePath, selectExplorer, onExplorerChange, explorers, capabilities,
  effectiveExplorerId, invalidateCompile, setMessage, opaqueId,
}: UseTableExplorerCrudOptions): UseTableExplorerCrudResult => {
  const [createExplorer, createStatus] = useCreateExplorerAuthoringMutation();
  const [deleteExplorer, deleteStatus] = useDeleteExplorerAuthoringMutation();
  const addTable = () => {
    if (!recipeMutationAllowed()) return;
    if (
      firstTableActionPending.current ||
      draftAccess.getState().tables.length === 0
    ) return;
    if (!tableCreatorOpen) setFirstTableName('');
    setTableCreatorOpen(true);
  };
  const duplicateTable = () => {
    if (!recipeMutationAllowed()) return;
    if (!table) return;
    if (!table.document.rootResourceType) {
      const outputId = opaqueId('output');
      const title = `${table.title} copy`;
      dispatch({
        type: 'addTable',
        table: {
          ...table,
          outputId,
          tabId: opaqueId('tab'),
          title,
          document: {
            ...table.document,
            output: { ...table.document.output, id: outputId, title },
          },
        },
      });
      rememberSelectedTable(outputId);
      return;
    }
    void applyCommands([
      {
        type: 'DUPLICATE_TABLE',
        sourceOutputId: table.outputId,
        title: `${table.title} copy`,
      },
    ]);
  };
  const renameTable = (outputId: string, value: string) => {
    const title = value.trim();
    if (!title) return;
    const target = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!target?.document.rootResourceType) {
      dispatch({ type: 'renameTable', outputId, title });
      return;
    }
    void applyCommands([{ type: 'RENAME_TABLE', outputId, title }]);
  };
  const renameTableById = (outputId: string) => {
    if (!recipeMutationAllowed()) return;
    const target = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!target) return;
    const title = window.prompt('Table name', target.title)?.trim();
    if (title && title !== target.title) renameTable(outputId, title);
  };
  const deleteSelectedTable = () => {
    if (!recipeMutationAllowed()) return;
    if (!table) return;
    if (!window.confirm(`Delete ${table.title}?`)) return;
    if (table.document.rootResourceType) {
      void applyCommands([
        { type: 'DELETE_TABLE', outputId: table.outputId },
      ]);
      return;
    }
    const fallbackOutputId = state.tables.find(
      (candidate) => candidate.outputId !== table.outputId,
    )?.outputId;
    dispatch({ type: 'removeTable', outputId: table.outputId });
    if (fallbackOutputId) rememberSelectedTable(fallbackOutputId);
  };
  const createCustomExplorer = async (title: string, fromCurrent: boolean) => {
    try {
      const created = await createExplorer({
        project: projectId,
        authResourcePath,
        name: title,
        title,
        ...(fromCurrent ? { sourceExplorerId: state.explorerId } : {}),
      }).unwrap();
      selectExplorer(created.explorerId);
      if (!onExplorerChange) void explorers.refetch();
      setMessage(undefined);
    } catch (error) {
      const apiError = error as ExplorerAuthoringApiError;
      const suffix = apiError.code ? ` (${apiError.code})` : '';
      setMessage(`Explorer creation failed: ${apiError.message}${suffix}`);
    }
  };
  const deleteCurrentExplorer = async () => {
    if (!capabilities.data?.features.deleteExplorer) return;
    const current = (explorers.data ?? []).find(
      (explorer) => explorer.explorerId === effectiveExplorerId,
    );
    const fallback =
      (explorers.data ?? []).find(
        (explorer) =>
          explorer.explorerId !== effectiveExplorerId &&
          explorer.explorerId === 'dataframe-default',
      ) ??
      (explorers.data ?? []).find(
        (explorer) => explorer.explorerId !== effectiveExplorerId,
      );
    if (!current || !fallback) return;
    if (
      !window.confirm(
        `Delete Explorer "${current.title}"? This permanently removes its configuration and cannot be undone.`,
      )
    )
      return;
    try {
      await deleteExplorer({
        project: projectId,
        explorerId: effectiveExplorerId,
        authResourcePath,
        requestId: `delete-explorer-${effectiveExplorerId}`,
      }).unwrap();
      invalidateCompile();
      selectExplorer(fallback.explorerId);
      if (!onExplorerChange) await explorers.refetch();
      setMessage(undefined);
    } catch (error) {
      const apiError = error as ExplorerAuthoringApiError;
      const suffix = apiError.code ? ` (${apiError.code})` : '';
      setMessage(`Explorer deletion failed: ${apiError.message}${suffix}`);
    }
  };
  return { addTable, duplicateTable, renameTable, renameTableById, deleteSelectedTable,
    createCustomExplorer, deleteCurrentExplorer, createStatus, deleteStatus };
};
