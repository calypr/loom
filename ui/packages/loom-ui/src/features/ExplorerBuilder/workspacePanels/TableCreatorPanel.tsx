import React from 'react';
import type { ExplorerBuilderCatalog } from '../../../types';
import { ConstructionUndoButton } from '../constructionWorkspace/ConstructionWorkspace';
import { RowRootPicker } from '../components/RowRootPicker';

type FirstTableProgress =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'running';
      readonly resourceType: string;
      readonly phase: 'checking' | 'creating' | 'adding-id' | 'previewing';
    };

export const TableCreatorPanel = ({
  isFirstTable,
  previousDraftRevisionId,
  pendingCommands,
  publishing,
  reconciliationPending,
  firstTableName,
  onFirstTableNameChange,
  firstTableProgress,
  catalog,
  onChooseRoot,
  onUndo,
  onCancel,
}: {
  readonly isFirstTable: boolean;
  readonly previousDraftRevisionId?: string;
  readonly pendingCommands: number;
  readonly publishing: boolean;
  readonly reconciliationPending: boolean;
  readonly firstTableName: string;
  readonly onFirstTableNameChange: (name: string) => void;
  readonly firstTableProgress: FirstTableProgress;
  readonly catalog: ExplorerBuilderCatalog;
  readonly onChooseRoot: (nodeId: string) => void;
  readonly onUndo: () => void;
  readonly onCancel?: () => void;
}) => (
  <section className="rounded-xl border border-blue-200 bg-white px-6 py-8 shadow-sm">
    <h2 className="text-xl font-semibold text-slate-900">
      {isFirstTable ? 'Build your first table' : 'Build another table'}
    </h2>
    {isFirstTable && previousDraftRevisionId ? (
      <div className="mt-3">
        <ConstructionUndoButton
          onUndo={onUndo}
          disabled={pendingCommands > 0 || publishing || reconciliationPending}
        />
      </div>
    ) : null}
    <p className="mt-2 max-w-xl text-sm text-slate-600">
      Choose a populated record type. Loom will add its direct ID column and
      load a preview. The table name is optional.
    </p>
    <div className="mt-5 max-w-sm">
      <label className="block text-sm font-medium text-slate-800" htmlFor="first-table-name">
        Table name (optional)
      </label>
      <input
        id="first-table-name"
        value={firstTableName}
        onChange={(event) => onFirstTableNameChange(event.currentTarget.value)}
        placeholder="Defaults to the record type"
        disabled={firstTableProgress.kind === 'running'}
        className="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm outline-blue-500 focus:border-blue-500 disabled:bg-slate-100"
      />
    </div>
    <div className="mt-5 max-w-3xl">
      <RowRootPicker
        catalog={catalog}
        disabled={
          pendingCommands > 0 ||
          reconciliationPending ||
          firstTableProgress.kind === 'running'
        }
        onChoose={onChooseRoot}
      />
    </div>
    {firstTableProgress.kind === 'running' ? (
      <p role="status" className="mt-4 text-sm text-blue-800">
        {firstTableProgress.phase === 'checking'
          ? `Checking ${firstTableProgress.resourceType} fields…`
          : firstTableProgress.phase === 'creating'
            ? `Creating ${firstTableProgress.resourceType} table…`
            : firstTableProgress.phase === 'adding-id'
              ? 'Adding the ID column…'
              : 'Loading the preview…'}
      </p>
    ) : null}
    {!isFirstTable ? (
      <div className="mt-4 flex justify-end">
        <button
          type="button"
          className="rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
          disabled={firstTableProgress.kind === 'running'}
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
    ) : null}
  </section>
);
