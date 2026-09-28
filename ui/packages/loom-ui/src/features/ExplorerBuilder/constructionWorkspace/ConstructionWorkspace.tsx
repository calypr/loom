import React from 'react';

export type ConstructionOperationFamily =
  | 'ADD_COLUMNS'
  | 'KEEP_ROWS'
  | 'CALCULATE'
  | 'RESHAPE'
  | 'COMBINE';

export interface ConstructionWorkspaceTable {
  readonly outputId: string;
  readonly title: string;
}

export type ConstructionHistorySelection =
  | { readonly kind: 'source' }
  | { readonly kind: 'step'; readonly stepId: string };

export interface ConstructionHistoryStep {
  readonly id: string;
  readonly title: string;
  readonly summary: string;
  readonly editable?: boolean;
}

export interface ConstructionHistoryProps {
  readonly steps: ReadonlyArray<ConstructionHistoryStep>;
  readonly selected: ConstructionHistorySelection;
  readonly disabled?: boolean;
  readonly onSelect: (selection: ConstructionHistorySelection) => void;
  readonly onEditStep?: (stepId: string) => void;
  readonly onRemoveStep?: (stepId: string) => void;
}

const allOperationFamilies = [
  {
    family: 'ADD_COLUMNS',
    label: 'Add columns',
    description: 'Bring more information into each row.',
  },
  {
    family: 'KEEP_ROWS',
    label: 'Filter rows',
    description: 'Choose which rows appear in the table output.',
  },
  {
    family: 'CALCULATE',
    label: 'Calculate',
    description: 'Create a value from existing columns.',
  },
  {
    family: 'RESHAPE',
    label: 'Reshape',
    description: 'Change what rows and columns represent.',
  },
  {
    family: 'COMBINE',
    label: 'Combine',
    description: 'Use another named table.',
  },
] satisfies ReadonlyArray<{
  readonly family: ConstructionOperationFamily;
  readonly label: string;
  readonly description: string;
}>;

export const constructionOperationFamilies = allOperationFamilies;
const actionFamilies = allOperationFamilies.filter(
  ({ family }) => family !== 'CALCULATE' && family !== 'COMBINE',
);

export const ConstructionActionBar = ({
  activeFamily,
  disabled = false,
  onSelect,
}: {
  readonly activeFamily?: ConstructionOperationFamily;
  readonly disabled?: boolean;
  readonly onSelect: (family: ConstructionOperationFamily) => void;
}) => (
  <nav aria-label="Table actions" className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
    {actionFamilies.map(({ family, label, description }) => (
      <button
        key={family}
        type="button"
        aria-label={`${label}: ${description}`}
        aria-pressed={activeFamily === family}
        data-testid={`construction-action-${family.toLowerCase().replace('_', '-')}`}
        disabled={disabled}
        onClick={() => onSelect(family)}
        className={`min-w-0 rounded-lg border px-3 py-2.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
          activeFamily === family
            ? 'border-emerald-700 bg-emerald-50 text-emerald-950 ring-1 ring-emerald-700'
            : 'border-slate-200 bg-white text-slate-900 hover:border-emerald-300 hover:bg-emerald-50/50'
        }`}
      >
        <span className="block text-sm font-semibold">{label}</span>
        <span className="mt-0.5 block text-xs font-normal text-slate-600">
          {description}
        </span>
      </button>
    ))}
  </nav>
);

export const ConstructionTableNavigation = ({
  tables,
  selectedOutputId,
  disabled = false,
  onSelectTable,
  onNewTable,
  onDuplicateTable,
  onDeleteTable,
  onRenameTable,
  onMoveTable,
}: {
  readonly tables: ReadonlyArray<ConstructionWorkspaceTable>;
  readonly selectedOutputId?: string;
  readonly disabled?: boolean;
  readonly onSelectTable: (outputId: string) => void;
  readonly onNewTable: () => void;
  readonly onDuplicateTable: () => void;
  readonly onDeleteTable: () => void;
  readonly onRenameTable: (outputId: string) => void;
  readonly onMoveTable: (outputId: string, beforeOutputId?: string) => void;
}) => (
  <nav aria-label="Tables" className="grid gap-1">
    <div className="mb-1 flex items-center justify-between gap-2 px-2">
      <h2 className="text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500">
        Tables
      </h2>
      <button
        type="button"
        data-testid="construction-new-table"
        className="rounded border border-slate-300 bg-white px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
        disabled={disabled}
        onClick={onNewTable}
      >
        New table
      </button>
    </div>
    {tables.map((table, index) => (
      <div key={table.outputId} className="group flex min-w-0 items-center gap-0.5">
        <button
          type="button"
          aria-current={table.outputId === selectedOutputId ? 'page' : undefined}
          aria-pressed={table.outputId === selectedOutputId}
          data-testid={`construction-table-${table.outputId}`}
          onClick={() => onSelectTable(table.outputId)}
          disabled={disabled}
          className={`flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-2.5 text-left text-sm transition-colors disabled:opacity-50 ${
            table.outputId === selectedOutputId
              ? 'bg-emerald-100 font-semibold text-emerald-950'
              : 'text-slate-700 hover:bg-white'
          }`}
        >
          <span aria-hidden="true" className="shrink-0 text-emerald-800">▤</span>
          <span className="min-w-0 truncate">{table.title || table.outputId}</span>
        </button>
        <button
          type="button"
          aria-label={`Rename ${table.title || table.outputId}`}
          title="Rename table"
          data-testid={`construction-rename-table-${table.outputId}`}
          disabled={disabled}
          onClick={() => onRenameTable(table.outputId)}
          className="rounded px-1.5 py-1 text-xs text-slate-400 hover:bg-white hover:text-slate-700 disabled:opacity-40"
        >
          …
        </button>
        <div className="flex shrink-0">
          <button
            type="button"
            aria-label={`Move ${table.title || table.outputId} up`}
            title="Move table up"
            disabled={disabled || index === 0}
            onClick={() => onMoveTable(table.outputId, tables[index - 1]?.outputId)}
            className="rounded px-1 py-1 text-xs text-slate-500 hover:bg-white disabled:opacity-30"
          >
            ↑
          </button>
          <button
            type="button"
            aria-label={`Move ${table.title || table.outputId} down`}
            title="Move table down"
            disabled={disabled || index === tables.length - 1}
            onClick={() => onMoveTable(table.outputId, tables[index + 2]?.outputId)}
            className="rounded px-1 py-1 text-xs text-slate-500 hover:bg-white disabled:opacity-30"
          >
            ↓
          </button>
        </div>
      </div>
    ))}
    <div className="mt-2 flex gap-2 border-t border-slate-200 px-2 pt-3">
      <button
        type="button"
        aria-label="Duplicate table"
        data-testid="construction-duplicate-table"
        disabled={disabled || !selectedOutputId}
        onClick={onDuplicateTable}
        className="rounded border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-45"
      >
        Duplicate
      </button>
      <button
        type="button"
        aria-label="Delete table"
        data-testid="construction-delete-table"
        disabled={disabled || !selectedOutputId || tables.length < 2}
        onClick={onDeleteTable}
        className="rounded border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-700 hover:border-red-300 hover:bg-red-50 hover:text-red-800 disabled:cursor-not-allowed disabled:opacity-45"
      >
        Delete
      </button>
    </div>
  </nav>
);

export const ConstructionHistory = ({
  steps,
  selected,
  disabled = false,
  onSelect,
  onEditStep,
  onRemoveStep,
}: ConstructionHistoryProps) => {
  if (steps.length === 0) return null;

  return (
    <section aria-label="Construction history" data-testid="construction-history" className="mt-8">
      <h2 className="mb-2 px-2 text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500">
        How this table is made
      </h2>
      <ol className="grid gap-1 border-l border-slate-300 pl-3">
        <li>
          <button
            type="button"
            aria-pressed={selected.kind === 'source'}
            data-testid="construction-history-source"
            disabled={disabled}
            onClick={() => onSelect({ kind: 'source' })}
            className={`w-full rounded px-2 py-2 text-left text-xs ${
              selected.kind === 'source' ? 'bg-white font-semibold text-emerald-950' : 'text-slate-700 hover:bg-white/70'
            }`}
          >
            <span className="block text-[10px] uppercase tracking-wide text-slate-500">Source</span>
            Starting records
          </button>
        </li>
        {steps.map((step, index) => (
          <li key={step.id}>
            <div className="rounded px-2 py-1">
              <button
                type="button"
                aria-pressed={selected.kind === 'step' && selected.stepId === step.id}
                data-testid={`construction-history-step-${step.id}`}
                disabled={disabled}
                onClick={() => onSelect({ kind: 'step', stepId: step.id })}
                className={`w-full rounded px-1 py-1 text-left text-xs ${
                  selected.kind === 'step' && selected.stepId === step.id
                    ? 'bg-white font-semibold text-emerald-950'
                    : 'text-slate-700 hover:bg-white/70'
                }`}
              >
                <span className="block text-[10px] uppercase tracking-wide text-slate-500">Step {index + 1}</span>
                <span className="block font-medium">{step.title}</span>
                <span className="mt-0.5 block text-slate-500">{step.summary}</span>
              </button>
              {selected.kind === 'step' && selected.stepId === step.id && ((onEditStep && step.editable) || onRemoveStep) ? (
                <div className="mt-1 flex gap-1">
                  {onEditStep && step.editable ? (
                    <button type="button" disabled={disabled} data-testid={`construction-edit-step-${step.id}`} onClick={() => onEditStep(step.id)} className="rounded px-1.5 py-1 text-[11px] text-emerald-800 hover:bg-emerald-50 disabled:opacity-45">Edit</button>
                  ) : null}
                  {onRemoveStep ? (
                    <button type="button" disabled={disabled} data-testid={`construction-remove-step-${step.id}`} onClick={() => onRemoveStep(step.id)} className="rounded px-1.5 py-1 text-[11px] text-red-700 hover:bg-red-50 disabled:opacity-45">Remove</button>
                  ) : null}
                </div>
              ) : null}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
};

export const ConstructionWorkspace = ({
  tables,
  selectedOutputId,
  tableActionsDisabled,
  onSelectTable,
  onNewTable,
  onDuplicateTable,
  onDeleteTable,
  onRenameTable,
  onMoveTable,
  title,
  rowMeaning,
  onUndo,
  undoDisabled = false,
  previewRowCount,
  previewSampled,
  previewColumnCount,
  actionsDisabled,
  activeFamily,
  onSelectFamily,
  history,
  rowSetup,
  preview,
  editor,
  setup,
  previewStatus,
  previewReceiptId,
  previewOutputId,
  proposalId,
  draftVersion,
  draftDigest,
}: {
  readonly tables: ReadonlyArray<ConstructionWorkspaceTable>;
  readonly selectedOutputId?: string;
  readonly tableActionsDisabled?: boolean;
  readonly onSelectTable: (outputId: string) => void;
  readonly onNewTable: () => void;
  readonly onDuplicateTable: () => void;
  readonly onDeleteTable: () => void;
  readonly onRenameTable: (outputId: string) => void;
  readonly onMoveTable: (outputId: string, beforeOutputId?: string) => void;
  readonly title: string;
  readonly rowMeaning: string;
  readonly onUndo?: () => void;
  readonly undoDisabled?: boolean;
  readonly previewRowCount?: number;
  readonly previewSampled?: boolean;
  readonly previewColumnCount?: number;
  readonly actionsDisabled?: boolean;
  readonly activeFamily?: ConstructionOperationFamily;
  readonly onSelectFamily: (family: ConstructionOperationFamily) => void;
  readonly history?: ConstructionHistoryProps;
  readonly rowSetup?: React.ReactNode;
  readonly preview: React.ReactNode;
  readonly editor?: React.ReactNode;
  readonly setup?: React.ReactNode;
  readonly previewStatus: 'empty' | 'stale' | 'previewing' | 'needs-repair' | 'error' | 'ready';
  readonly previewReceiptId?: string;
  readonly previewOutputId?: string;
  readonly proposalId?: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
}) => (
  <div
    data-testid="construction-workspace"
    data-draft-version={draftVersion}
    data-draft-digest={draftDigest}
    className="mx-auto grid max-w-[1920px] grid-cols-1 gap-3 xl:grid-cols-[15rem_minmax(0,1fr)]"
  >
    <aside className="min-w-0 rounded-xl border border-slate-200 bg-[#edf2ed] p-3 xl:sticky xl:top-3 xl:max-h-[calc(100dvh-1.5rem)] xl:self-start xl:overflow-y-auto">
      <ConstructionTableNavigation
        tables={tables}
        selectedOutputId={selectedOutputId}
        disabled={tableActionsDisabled}
        onSelectTable={onSelectTable}
        onNewTable={onNewTable}
        onDuplicateTable={onDuplicateTable}
        onDeleteTable={onDeleteTable}
        onRenameTable={onRenameTable}
        onMoveTable={onMoveTable}
      />
      {history ? <ConstructionHistory {...history} /> : null}
    </aside>

    <section className="min-w-0 space-y-4">
      <header className="flex flex-wrap items-start gap-3 rounded-xl border border-slate-200 bg-white px-4 py-4 shadow-sm sm:px-5">
        <div className="min-w-0 flex-1">
          <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500">Dataset workspace</p>
          <h1 className="mt-1 truncate text-2xl font-semibold tracking-tight text-slate-950">{title}</h1>
          <p className="mt-1 text-sm text-slate-600">{rowMeaning}</p>
        </div>
        <div className="flex items-center gap-4">
          <dl className="flex gap-5 text-right text-xs text-slate-500">
            <div>
              <dt>{previewRowCount !== undefined && previewSampled === false ? 'Total rows' : 'Preview rows'}</dt>
              <dd className="text-lg font-semibold text-slate-900">{previewRowCount === undefined ? '—' : previewRowCount.toLocaleString()}</dd>
              {previewRowCount !== undefined && previewSampled ? <p className="text-[10px] text-slate-500">Full count not measured</p> : null}
            </div>
            <div>
              <dt>Columns</dt>
              <dd className="text-lg font-semibold text-slate-900">{previewColumnCount ?? '—'}</dd>
            </div>
          </dl>
          {onUndo ? (
            <button
              type="button"
              data-testid="construction-undo"
              aria-label="Undo last saved draft change"
              disabled={undoDisabled}
              onClick={onUndo}
              className="rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Undo
            </button>
          ) : null}
        </div>
      </header>

      {rowSetup ? <section aria-label="Define table rows" data-testid="construction-row-setup" className="min-w-0">{rowSetup}</section> : null}

      <ConstructionActionBar
        activeFamily={activeFamily}
        disabled={actionsDisabled}
        onSelect={onSelectFamily}
      />

      <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[minmax(0,1.65fr)_minmax(21rem,1fr)]">
        <section
          aria-label="Table result"
          data-testid="construction-preview"
          data-preview-status={previewStatus}
          data-preview-receipt-id={previewReceiptId ?? ''}
          data-preview-output-id={previewOutputId ?? ''}
          data-preview-proposal-id={proposalId ?? ''}
          data-current-draft-version={draftVersion}
          data-current-draft-digest={draftDigest}
          className="order-last min-w-0 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm xl:order-first"
        >
          {preview}
        </section>
        <aside aria-label="Proposed change" className="order-first min-w-0 xl:order-last">
          {editor ?? (
            <section className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
              <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500">Ready to explore</p>
              <h2 className="mt-1 text-lg font-semibold text-slate-950">Choose a table action</h2>
              <p className="mt-1 text-sm text-slate-600">Each action opens guided choices and a backend-reviewed proposal before it changes this table.</p>
            </section>
          )}
        </aside>
      </div>

      {setup ? <div className="min-w-0">{setup}</div> : null}
    </section>
  </div>
);
