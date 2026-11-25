import type { BuilderMutationResult } from '../../../react';
import { createStore } from '../../../data/createStore';
import type { BuilderAuthoringState } from '../model/authoring';
import { planInitialTable, type InitialTableCandidateEvidence, type InitialTablePlan } from '../model/initialTablePlan';
import type { bindCommandQueue } from './commandQueue';

type FirstTableProgress =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'running';
      readonly resourceType: string;
      readonly phase: 'checking' | 'creating' | 'adding-id' | 'previewing';
    };

type ApplyCommands = ReturnType<typeof bindCommandQueue>;
type Update<Value> = Value | ((value: Value) => Value);

export function createFirstTable() {
  const store = createStore<{ tableCreatorOpen: boolean; firstTableProgress: FirstTableProgress }>({
    tableCreatorOpen: false,
    firstTableProgress: { kind: 'idle' },
  });
  return {
    getSnapshot: store.getSnapshot,
    subscribe: store.subscribe,
    isFirstTableActionPending: () => store.getSnapshot().firstTableProgress.kind === 'running',
    setFirstTableProgress(firstTableProgress: FirstTableProgress) {
      store.setSnapshot({ ...store.getSnapshot(), firstTableProgress });
    },
    setTableCreatorOpen(update: Update<boolean>) {
      const current = store.getSnapshot();
      const tableCreatorOpen = typeof update === 'function' ? update(current.tableCreatorOpen) : update;
      if (current.tableCreatorOpen !== tableCreatorOpen) store.setSnapshot({ ...current, tableCreatorOpen });
    },
  };
}

export function createFirstTableFromRootFor(
  { isFirstTableActionPending, setFirstTableProgress, setTableCreatorOpen }: ReturnType<typeof createFirstTable>,
  { draftAccess, getSuggestions, scope, setMessage, firstTableName, setFirstTableName }: {
    readonly draftAccess: { getState(): BuilderAuthoringState };
    readonly getSuggestions: BuilderMutationResult<'suggestions'>[0];
    readonly scope: { readonly project: string; readonly authResourcePath?: string };
    readonly setMessage: (message: string | undefined) => void;
    readonly firstTableName: string;
    readonly setFirstTableName: (value: string) => void;
  },
): (nodeId: string, applyCommandsWithResult: ApplyCommands) => Promise<void> {
  const { project: projectId, authResourcePath } = scope;
  return async function createFirstTableFromRoot(
    nodeId: string,
    applyCommandsWithResult: ApplyCommands,
  ) {
    if (isFirstTableActionPending()) return;
    const current = draftAccess.getState();
    const root = current.catalog.nodes.find((node) => node.nodeId === nodeId);
    if (!root || !current.catalog.snapshotToken) return;

    let tableCreated = false;
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
          requestId: `first-table-suggestions-${globalThis.crypto.randomUUID()}`,
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
      setFirstTableProgress({ kind: 'idle' });
    }
  };
}
