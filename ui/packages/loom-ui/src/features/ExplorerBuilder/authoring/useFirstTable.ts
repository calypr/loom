import { useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { useGetExplorerCandidateSuggestionsV2Mutation } from '../../../react';
import type { BuilderAuthoringState } from '../model/authoring';
import type { useCommandQueue } from './useCommandQueue';
import { planInitialTable, type InitialTableCandidateEvidence, type InitialTablePlan } from '../model/initialTablePlan';

type FirstTableProgress =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'running';
      readonly resourceType: string;
      readonly phase: 'checking' | 'creating' | 'adding-id' | 'previewing';
    };

type UseFirstTableOptions = {
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly getSuggestions: ReturnType<typeof useGetExplorerCandidateSuggestionsV2Mutation>[0];
  readonly projectId: string;
  readonly authResourcePath?: string;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

type FirstTableActionsOptions = {
  readonly applyCommandsWithResult: ReturnType<typeof useCommandQueue>['applyCommandsWithResult'];
};

export const useFirstTable = ({
  latestState, getSuggestions, projectId, authResourcePath, setMessage,
}: UseFirstTableOptions) => {
  const [firstTableName, setFirstTableName] = useState('');
  const [tableCreatorOpen, setTableCreatorOpen] = useState(false);
  const [firstTableProgress, setFirstTableProgress] =
    useState<FirstTableProgress>({ kind: 'idle' });
  const firstTableActionPending = useRef(false);
  const actionsFor = ({ applyCommandsWithResult }: FirstTableActionsOptions) => {
    const createFirstTableFromRoot = async (nodeId: string) => {
      if (firstTableActionPending.current) return;
      const current = latestState.current;
      const root = current.catalog.nodes.find((node) => node.nodeId === nodeId);
      if (!root || !current.catalog.snapshotToken) return;

      let tableCreated = false;
      firstTableActionPending.current = true;
      setFirstTableProgress({
        kind: 'running',
        resourceType: root.resourceType,
        phase: 'checking',
      });
      try {
        let evidence: InitialTableCandidateEvidence = { kind: 'unavailable' };
        try {
          const value = await getSuggestions({
            project: projectId,
            explorerId: current.explorerId,
            authResourcePath,
            snapshotToken: current.catalog.snapshotToken,
            nodeId: root.nodeId,
            requestId: `first-table-suggestions-${window.crypto.randomUUID()}`,
          }).unwrap();
          if (
            value.snapshotToken === current.catalog.snapshotToken &&
            value.nodeId === root.nodeId
          ) {
            evidence = { kind: 'available', candidates: value.candidates };
          }
        } catch {
          // A root table is still useful when Loom cannot prove an identity field.
        }

        const plan: InitialTablePlan = planInitialTable(
          root,
          firstTableName,
          evidence,
        );
        setFirstTableProgress({
          kind: 'running',
          resourceType: root.resourceType,
          phase: 'creating',
        });
        const created = await applyCommandsWithResult([{
          type: 'CREATE_TABLE',
          title: plan.title,
          rootNodeId: plan.root.nodeId,
        }]);
        const createdTable = created?.results.find(
          (result) =>
            result.type === 'TABLE_CREATED' &&
            Boolean(result.outputId) &&
            Boolean(result.occurrenceId),
        );
        if (!createdTable?.outputId || !createdTable.occurrenceId) {
          setMessage(`Loom could not create a ${root.resourceType} table.`);
          return;
        }
        tableCreated = true;
        setFirstTableName('');

        if (plan.kind === 'root-only') {
          const reason = plan.reason === 'CATALOG_UNAVAILABLE'
            ? 'Loom could not verify a safe identity column from the current catalog.'
            : plan.reason === 'AMBIGUOUS_DIRECT_ID'
              ? 'Loom found more than one executable direct ID field.'
              : 'Loom did not find a direct scalar string ID field.';
          setMessage(
            `${plan.title} was created with ${root.resourceType} rows. ${reason} Add a column to preview the table.`,
          );
          return;
        }

        setFirstTableProgress({
          kind: 'running',
          resourceType: root.resourceType,
          phase: 'adding-id',
        });
        const added = await applyCommandsWithResult([{
          type: 'ADD_COLUMN',
          outputId: createdTable.outputId,
          occurrenceId: createdTable.occurrenceId,
          candidateId: plan.candidate.candidateId,
          projectionMode: 'VALUE',
          initialPresentation: 'TABLE',
          title: `${root.resourceType} ID`,
        }]);
        if (!added) {
          setMessage(
            `${plan.title} was created, but Loom could not add the catalog-verified ID column. Review the Builder diagnostics and try again.`,
          );
          return;
        }

      } finally {
        if (tableCreated) setTableCreatorOpen(false);
        firstTableActionPending.current = false;
        setFirstTableProgress({ kind: 'idle' });
      }
    };

    return { createFirstTableFromRoot };
  };
  return { firstTableName, setFirstTableName, tableCreatorOpen, setTableCreatorOpen,
    firstTableProgress, firstTableActionPending, actionsFor };
};
