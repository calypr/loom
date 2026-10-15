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
  readonly resourceType: string;
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
    label: 'Add a calculated column',
    description: 'Create a value from existing columns.',
  },
  {
    family: 'RESHAPE',
    label: 'Reshape',
    description: 'Change what rows and columns represent.',
  },
  {
    family: 'COMBINE',
    label: 'Combine tables',
    description: 'Create a new table by joining or stacking table versions.',
  },
] satisfies ReadonlyArray<{
  readonly family: ConstructionOperationFamily;
  readonly label: string;
  readonly description: string;
}>;

export const constructionOperationFamilies = allOperationFamilies;
export const ConstructionUndoButton = ({ onUndo, disabled = false }: {
  readonly onUndo: () => void;
  readonly disabled?: boolean;
}) => (
  <button
    type="button"
    data-testid="construction-undo"
    aria-label="Undo last saved draft change"
    disabled={disabled}
    onClick={onUndo}
    className="rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
  >
    Undo
  </button>
);
export const ConstructionActionBar = ({
  activeFamily,
  disabled = false,
  addColumnsDisabled = false,
  combineDisabled = false,
  calculateAvailable = false,
  onSelect,
}: {
  readonly activeFamily?: ConstructionOperationFamily;
  readonly disabled?: boolean;
  readonly addColumnsDisabled?: boolean;
  readonly combineDisabled?: boolean;
  readonly calculateAvailable?: boolean;
  readonly onSelect: (family: ConstructionOperationFamily) => void;
}) => (
  <nav aria-label="Table actions" className="flex flex-wrap items-center gap-2">
    {allOperationFamilies.filter(({ family }) =>
      family === 'ADD_COLUMNS' || family === 'KEEP_ROWS' || family === 'COMBINE' ||
      (family === 'CALCULATE' && calculateAvailable)
    ).map(({ family, label, description }) => (
      <button
        key={family}
        type="button"
        aria-label={`${label}: ${description}`}
        aria-pressed={activeFamily === family}
        data-testid={`construction-action-${family.toLowerCase().replace('_', '-')}`}
        disabled={disabled || (family === 'ADD_COLUMNS' && addColumnsDisabled) || (family === 'COMBINE' && combineDisabled)}
        onClick={() => onSelect(family)}
        title={description}
        className={`min-w-0 rounded-md border px-3 py-2 text-left text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
          activeFamily === family
            ? 'border-emerald-700 bg-emerald-50 text-emerald-950 ring-1 ring-emerald-700'
            : 'border-slate-200 bg-white text-slate-900 hover:border-emerald-300 hover:bg-emerald-50/50'
        }`}
      >
        {label}
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
}: {
  readonly tables: ReadonlyArray<ConstructionWorkspaceTable>;
  readonly selectedOutputId?: string;
  readonly disabled?: boolean;
  readonly onSelectTable: (outputId: string) => void;
  readonly onNewTable: () => void;
  readonly onDuplicateTable: () => void;
  readonly onDeleteTable: () => void;
  readonly onRenameTable: (outputId: string) => void;
}) => {
  const selectedTable = tables.find((table) => table.outputId === selectedOutputId);
  return (
  <nav aria-label="Tables" className="flex flex-wrap items-center gap-3">
    <div className="flex shrink-0 items-center gap-2">
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
    <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
      {tables.map((table) => (
        <div key={table.outputId} className="flex max-w-64 shrink-0 items-center">
          <button
            type="button"
            aria-current={table.outputId === selectedOutputId ? 'page' : undefined}
            aria-pressed={table.outputId === selectedOutputId}
            data-testid={`construction-table-${table.outputId}`}
            onClick={() => onSelectTable(table.outputId)}
            disabled={disabled}
            className={`flex min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-sm transition-colors disabled:opacity-50 ${
              table.outputId === selectedOutputId
                ? 'bg-emerald-100 font-semibold text-emerald-950'
                : 'text-slate-700 hover:bg-white'
            }`}
          >
            <span aria-hidden="true" className="shrink-0 text-emerald-800">▤</span>
            <span className="min-w-0 truncate">{table.title || table.outputId}</span>
          </button>
        </div>
      ))}
    </div>
    <div className="flex shrink-0 items-center gap-2 border-l border-slate-200 pl-3">
      <button
        type="button"
        aria-label={`Rename ${selectedTable?.title || selectedTable?.outputId || 'table'}`}
        data-testid={selectedOutputId ? `construction-rename-table-${selectedOutputId}` : undefined}
        disabled={disabled || !selectedTable}
        onClick={() => selectedTable && onRenameTable(selectedTable.outputId)}
        className="rounded px-1 py-1 text-xs font-medium text-slate-700 hover:bg-white disabled:opacity-45"
      >
        Rename
      </button>
      <button
        type="button"
        aria-label="Duplicate table"
        data-testid="construction-duplicate-table"
        disabled={disabled || !selectedOutputId}
        onClick={onDuplicateTable}
        className="rounded px-1 py-1 text-xs font-medium text-slate-700 hover:bg-white disabled:cursor-not-allowed disabled:opacity-45"
      >
        Duplicate
      </button>
      <button
        type="button"
        aria-label="Delete table"
        data-testid="construction-delete-table"
        disabled={disabled || !selectedOutputId}
        onClick={onDeleteTable}
        className="rounded px-1 py-1 text-xs font-medium text-slate-700 hover:bg-red-50 hover:text-red-800 disabled:cursor-not-allowed disabled:opacity-45"
      >
        Delete
      </button>
    </div>
  </nav>
  );
};

export const ConstructionHistory = ({
  resourceType,
  steps,
  selected,
  disabled = false,
  onSelect,
  onEditStep,
  onRemoveStep,
}: ConstructionHistoryProps) => {
  if (steps.length === 0) return null;

  return (
    <section aria-label="Row operations" data-testid="construction-history" className="mt-1 border-t border-slate-200 pt-1">
      <h2 className="sr-only">Row operations</h2>
      <ol className="m-0 flex list-none items-center gap-1 overflow-x-auto p-0">
        <li className="shrink-0">
          <button
            type="button"
            aria-pressed={selected.kind === 'source'}
            data-testid="construction-history-source"
            disabled={disabled}
            onClick={() => onSelect({ kind: 'source' })}
            className={`rounded-md px-2.5 py-1 text-left text-xs whitespace-nowrap ${
              selected.kind === 'source' ? 'bg-emerald-100 font-semibold text-emerald-950' : 'text-slate-700 hover:bg-white/70'
            }`}
          >
            {resourceType}
          </button>
        </li>
        {steps.map((step) => (
          <li key={step.id} className="flex shrink-0 items-center gap-1">
            <span aria-hidden="true" className="px-1 text-xs text-slate-400">→</span>
            <div className="flex items-center rounded-md border border-slate-200 bg-white/60">
              <button
                type="button"
                aria-pressed={selected.kind === 'step' && selected.stepId === step.id}
                data-testid={`construction-history-step-${step.id}`}
                disabled={disabled}
                title={step.summary}
                onClick={() => onSelect({ kind: 'step', stepId: step.id })}
                className={`rounded-md px-2.5 py-1 text-left text-xs whitespace-nowrap ${
                  selected.kind === 'step' && selected.stepId === step.id
                    ? 'bg-emerald-100 font-semibold text-emerald-950'
                    : 'text-slate-700 hover:bg-white/70'
                }`}
              >
                <span className="font-medium">{step.title}</span>
                {selected.kind === 'step' && selected.stepId === step.id ? <span className="sr-only">{step.summary}</span> : null}
              </button>
              {selected.kind === 'step' && selected.stepId === step.id && ((onEditStep && step.editable) || onRemoveStep) ? (
                <div className="flex gap-1 border-l border-slate-200 px-1">
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
  actionsDisabled,
  activeFamily,
  addColumnsDisabled = false,
  combineDisabled = false,
  calculateAvailable = false,
  onSelectFamily,
  history,
  rowSetup,
  preview,
  editor,
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
  readonly actionsDisabled?: boolean;
  readonly activeFamily?: ConstructionOperationFamily;
  readonly addColumnsDisabled?: boolean;
  readonly combineDisabled?: boolean;
  readonly calculateAvailable?: boolean;
  readonly onSelectFamily: (family: ConstructionOperationFamily) => void;
  readonly history?: ConstructionHistoryProps;
  readonly rowSetup?: React.ReactNode;
  readonly preview: React.ReactNode;
  readonly editor?: React.ReactNode;
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
    className={editor
      ? 'mx-auto grid max-w-6xl grid-cols-1 gap-3'
      : 'mx-auto grid max-w-[1920px] grid-cols-1 gap-1'}
  >
    {!editor ? <div className="min-w-0 rounded-xl border border-slate-200 bg-[#edf2ed] px-3 py-2">
      <ConstructionTableNavigation
        tables={tables}
        selectedOutputId={selectedOutputId}
        disabled={tableActionsDisabled}
        onSelectTable={onSelectTable}
        onNewTable={onNewTable}
        onDuplicateTable={onDuplicateTable}
        onDeleteTable={onDeleteTable}
        onRenameTable={onRenameTable}
      />
    </div> : null}

    <section className="min-w-0 space-y-3">
      {!editor ? <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Configure table">
        {rowSetup ? <div aria-label="Define table rows" data-testid="construction-row-setup">{rowSetup}</div> : null}
        <ConstructionActionBar
          activeFamily={activeFamily}
          disabled={actionsDisabled}
          addColumnsDisabled={addColumnsDisabled}
          combineDisabled={combineDisabled}
          calculateAvailable={calculateAvailable}
          onSelect={onSelectFamily}
        />
      </div> : null}



      <div className="grid min-w-0 items-start gap-3">
        {editor ? <section aria-label="Change editor" className="min-w-0">{editor}</section> : null}
        <section
          aria-label="Table result"
          data-testid="construction-preview"
          data-preview-status={previewStatus}
          data-preview-receipt-id={previewReceiptId ?? ''}
          data-preview-output-id={previewOutputId ?? ''}
          data-preview-proposal-id={proposalId ?? ''}
          data-current-draft-version={draftVersion}
          data-current-draft-digest={draftDigest}
          className="min-w-0 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm"
        >
          {history ? <ConstructionHistory {...history} /> : null}
          {preview}
        </section>
      </div>

    </section>
  </div>
);
