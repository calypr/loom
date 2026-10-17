import React, { useEffect, useRef, useState } from 'react';
import { useDismissibleLayer } from '../components/useDismissibleLayer';

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
  readonly removeActionLabel?: string;
  readonly editable?: boolean;
  readonly hierarchy?: 'COLUMN_ADJUSTMENT';
}

export interface ConstructionHistoryProps {
  readonly resourceType: string;
  readonly sourceControl?: React.ReactNode;
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
}) => {
  const [moreActionsOpen, setMoreActionsOpen] = useState(false);
  const moreActionsRef = useDismissibleLayer<HTMLDivElement>(moreActionsOpen, setMoreActionsOpen);
  const moreActionsButtonRef = useRef<HTMLButtonElement>(null);
  const primaryFamilies = allOperationFamilies.filter(({ family }) =>
    family === 'ADD_COLUMNS' || family === 'KEEP_ROWS'
  );
  const secondaryFamilies = allOperationFamilies.filter(({ family }) =>
    family === 'COMBINE' ||
    (family === 'CALCULATE' && calculateAvailable)
  );
  const renderAction = (family: ConstructionOperationFamily, inMenu = false) => {
    const operation = allOperationFamilies.find((candidate) => candidate.family === family);
    if (!operation) return null;
    const { label, description } = operation;
    return (
      <button
        key={family}
        type="button"
        aria-label={`${family === 'CALCULATE' ? 'Add a calculated column' : family === 'COMBINE' ? 'Combine tables' : label}: ${description}`}
        aria-pressed={activeFamily === family}
        data-testid={`construction-action-${family.toLowerCase().replace('_', '-')}`}
        disabled={disabled || (family === 'ADD_COLUMNS' && addColumnsDisabled) || (family === 'COMBINE' && combineDisabled)}
        onClick={() => {
          if (inMenu) setMoreActionsOpen(false);
          onSelect(family);
        }}
        title={description}
        className={inMenu
          ? 'rounded px-2.5 py-1.5 text-left text-xs font-medium text-slate-700 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50'
          : `h-8 min-w-0 rounded-md px-3 text-left text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
            activeFamily === family
              ? 'bg-blue-100 text-blue-950'
              : 'bg-transparent text-slate-700 hover:bg-blue-50'
          }`}
      >
        {label}
      </button>
    );
  };

  return (
    <nav aria-label="Table actions" className="flex flex-wrap items-center gap-1.5">
      {primaryFamilies.map(({ family }) => renderAction(family))}
      <div
        ref={moreActionsRef}
        className="relative"
        onKeyDown={(event) => {
          if (event.key !== 'Escape' || !moreActionsOpen) return;
          event.preventDefault();
          event.stopPropagation();
          setMoreActionsOpen(false);
          moreActionsButtonRef.current?.focus();
        }}
      >
        <button
          ref={moreActionsButtonRef}
          type="button"
          aria-haspopup="true"
          aria-expanded={moreActionsOpen}
          aria-label="More actions"
          data-testid="construction-more-actions"
          disabled={disabled}
          onClick={() => setMoreActionsOpen((open) => !open)}
          className="h-8 rounded-md bg-transparent px-3 text-xs font-medium text-slate-600 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
        >
          More actions
        </button>
        {moreActionsOpen ? (
          <div
            role="group"
            aria-label="More table actions"
            data-testid="construction-more-actions-menu"
            className="construction-more-actions-menu absolute right-0 top-full z-30 mt-1 grid min-w-40 rounded-md border border-slate-200 bg-white p-1 shadow-lg"
          >
            {secondaryFamilies.map(({ family }) => renderAction(family, true))}
          </div>
        ) : null}
      </div>
    </nav>
  );
};

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
  const [tableActionsOpen, setTableActionsOpen] = useState(false);
  const tableActionsRef = useDismissibleLayer<HTMLDivElement>(tableActionsOpen, setTableActionsOpen);
  const tableActionsButtonRef = useRef<HTMLButtonElement>(null);
  const [tableActionsPosition, setTableActionsPosition] = useState<{
    readonly top: number;
    readonly right: number;
  }>();
  useEffect(() => {
    if (!tableActionsOpen) {
      setTableActionsPosition(undefined);
      return undefined;
    }
    const updatePosition = () => {
      const button = tableActionsButtonRef.current;
      if (!button) return;
      const bounds = button.getBoundingClientRect();
      const below = bounds.bottom + 4;
      const top = below + 120 <= window.innerHeight - 8
        ? below
        : Math.max(8, bounds.top - 124);
      setTableActionsPosition({
        top,
        right: Math.max(8, window.innerWidth - bounds.right),
      });
    };
    updatePosition();
    window.addEventListener('resize', updatePosition);
    window.addEventListener('scroll', updatePosition, true);
    return () => {
      window.removeEventListener('resize', updatePosition);
      window.removeEventListener('scroll', updatePosition, true);
    };
  }, [tableActionsOpen]);
  return (
  <nav aria-label="Tables" className="construction-table-tabs flex min-w-0 flex-wrap items-center gap-3 py-1">
    <h2 className="sr-only">Tables</h2>
    <button
      type="button"
      data-testid="construction-new-table"
      aria-label="New table"
      title="New table"
      className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-transparent text-lg font-normal leading-none text-blue-700 hover:bg-blue-50 disabled:opacity-50"
      disabled={disabled}
      onClick={onNewTable}
    >
      +
    </button>
    <div className="flex min-w-0 flex-1 items-center gap-3 overflow-x-auto">
      {tables.map((table) => {
        const selected = table.outputId === selectedOutputId;
        const title = table.title || table.outputId;
        return (
          <div key={table.outputId} className="flex max-w-64 shrink-0 items-center gap-1">
            <button
              type="button"
              aria-current={selected ? 'page' : undefined}
              aria-pressed={selected}
              data-testid={`construction-table-${table.outputId}`}
              onClick={() => {
                setTableActionsOpen(false);
                onSelectTable(table.outputId);
              }}
              disabled={disabled}
              className={`construction-table-tab bg-transparent h-8 min-w-0 border-b-2 px-2 text-left text-sm transition-colors disabled:opacity-50 ${
                selected
                  ? 'font-semibold text-slate-950'
                  : 'text-slate-600 hover:text-slate-900'
              }`}
            >
              <span className="min-w-0 truncate">{title}</span>
            </button>
            {selected ? (
              <div
                ref={tableActionsRef}
                className="relative shrink-0"
                onKeyDown={(event) => {
                  if (event.key !== 'Escape' || !tableActionsOpen) return;
                  event.preventDefault();
                  event.stopPropagation();
                  setTableActionsOpen(false);
                  tableActionsButtonRef.current?.focus();
                }}
              >
                <button
                  type="button"
                  ref={tableActionsButtonRef}
                  aria-label={`Actions for ${title}`}
                  aria-haspopup="true"
                  aria-expanded={tableActionsOpen}
                  data-testid={`construction-table-actions-${table.outputId}`}
                  disabled={disabled}
                  onClick={() => setTableActionsOpen((open) => !open)}
                  className="flex h-7 w-7 items-center justify-center rounded-md text-lg leading-none text-slate-600 hover:bg-slate-100 hover:text-slate-950 disabled:opacity-50"
                  title={`Actions for ${title}`}
                >
                  ⋯
                </button>
                {tableActionsOpen ? (
                  <div
                    data-testid="construction-table-actions-menu"
                    className="fixed z-[120] grid min-w-36 rounded-md border border-slate-200 bg-white p-1 shadow-lg"
                    style={tableActionsPosition ?? { visibility: 'hidden' }}
                  >
                    <button
                      type="button"
                      aria-label={`Rename ${title}`}
                      data-testid={`construction-rename-table-${table.outputId}`}
                      disabled={disabled}
                      onClick={() => {
                        setTableActionsOpen(false);
                        onRenameTable(table.outputId);
                      }}
                      className="rounded px-2 py-1.5 text-left text-xs text-slate-700 hover:bg-slate-50 disabled:opacity-45"
                    >
                      Rename
                    </button>
                    <button
                      type="button"
                      aria-label="Duplicate table"
                      data-testid="construction-duplicate-table"
                      disabled={disabled}
                      onClick={() => {
                        setTableActionsOpen(false);
                        onDuplicateTable();
                      }}
                      className="rounded px-2 py-1.5 text-left text-xs text-slate-700 hover:bg-slate-50 disabled:opacity-45"
                    >
                      Duplicate
                    </button>
                    <button
                      type="button"
                      aria-label="Delete table"
                      data-testid="construction-delete-table"
                      disabled={disabled}
                      onClick={() => {
                        setTableActionsOpen(false);
                        onDeleteTable();
                      }}
                      className="rounded px-2 py-1.5 text-left text-xs text-red-700 hover:bg-red-50 disabled:opacity-45"
                    >
                      Delete
                    </button>
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  </nav>
  );
};

export const ConstructionHistory = ({
  resourceType,
  sourceControl,
  steps,
  selected,
  disabled = false,
  onSelect,
  onEditStep,
  onRemoveStep,
}: ConstructionHistoryProps) => {
  if (steps.length === 0 && !sourceControl && !resourceType) return null;

  type HistoryGroup =
    | { readonly kind: 'step'; readonly step: ConstructionHistoryStep }
    | {
        readonly kind: 'column-adjustments';
        readonly firstStepId: string;
        readonly steps: ReadonlyArray<ConstructionHistoryStep>;
      };

  const historyGroups: HistoryGroup[] = [];
  for (let index = 0; index < steps.length;) {
    const step = steps[index];
    if (!step) break;
    if (step.hierarchy !== 'COLUMN_ADJUSTMENT') {
      historyGroups.push({ kind: 'step', step });
      index += 1;
      continue;
    }

    const groupSteps: ConstructionHistoryStep[] = [step];
    index += 1;
    while (steps[index]?.hierarchy === 'COLUMN_ADJUSTMENT') {
      const adjustment = steps[index];
      if (!adjustment) break;
      groupSteps.push(adjustment);
      index += 1;
    }
    historyGroups.push({
      kind: 'column-adjustments',
      firstStepId: step.id,
      steps: groupSteps,
    });
  }

  const renderStep = (step: ConstructionHistoryStep, showArrow: boolean) => (
    <li key={step.id} className="flex shrink-0 items-center gap-1">
      {showArrow ? <span aria-hidden="true" className="px-1 text-xs text-slate-400">→</span> : null}
      <div className={`construction-query-step flex items-center border-b-2 ${selected.kind === 'step' && selected.stepId === step.id ? 'border-blue-700' : 'border-transparent'}`}>
        <button
          type="button"
          aria-pressed={selected.kind === 'step' && selected.stepId === step.id}
          data-testid={`construction-history-step-${step.id}`}
          disabled={disabled}
          title={step.summary}
          onClick={() => {
            if (onEditStep && step.editable) {
              onEditStep(step.id);
            } else {
              onSelect({ kind: 'step', stepId: step.id });
            }
          }}
          className={`rounded px-2 py-1 text-left text-xs whitespace-nowrap ${
            selected.kind === 'step' && selected.stepId === step.id
              ? 'bg-blue-100 font-semibold text-blue-950'
              : 'bg-transparent text-slate-700 hover:bg-blue-50 hover:text-slate-950'
          }`}
        >
          <span className="block max-w-48 truncate font-medium">{step.title}</span>
          {selected.kind === 'step' && selected.stepId === step.id ? <span className="sr-only">{step.summary}</span> : null}
        </button>
        {selected.kind === 'step' && selected.stepId === step.id && ((onEditStep && step.editable) || onRemoveStep) ? (
          <div className="flex gap-1 border-l border-slate-200 px-1">
            {onEditStep && step.editable ? (
              <button type="button" disabled={disabled} data-testid={`construction-edit-step-${step.id}`} onClick={() => onEditStep(step.id)} className="rounded px-1.5 py-1 text-[11px] text-emerald-800 hover:bg-emerald-50 disabled:opacity-45">Edit</button>
            ) : null}
            {onRemoveStep ? (
              <button type="button" disabled={disabled} data-testid={`construction-remove-step-${step.id}`} aria-label={step.removeActionLabel} title={step.removeActionLabel} onClick={() => onRemoveStep(step.id)} className="rounded px-1.5 py-1 text-[11px] text-red-700 hover:bg-red-50 disabled:opacity-45">{step.removeActionLabel ? 'Remove connection' : 'Remove'}</button>
            ) : null}
          </div>
        ) : null}
      </div>
    </li>
  );

  return (
    <section aria-label="Row operations" data-testid="construction-history" className="min-w-0">
      <h2 className="sr-only">Row operations</h2>
      <ol className="m-0 flex list-none items-center gap-1 overflow-x-auto p-0">
        <li className="shrink-0">
          {sourceControl ? (
            <div onClickCapture={(event) => {
              if (event.target instanceof Element && event.target.closest('[data-construction-source-selection-trigger]')) {
                onSelect({ kind: 'source' });
              }
            }}>
              {sourceControl}
            </div>
          ) : <button
            type="button"
            aria-pressed={selected.kind === 'source'}
            data-testid="construction-history-source"
            disabled={disabled}
            onClick={() => onSelect({ kind: 'source' })}
            className={`rounded px-2 py-1 text-left text-xs whitespace-nowrap ${
              selected.kind === 'source' ? 'bg-blue-100 font-semibold text-blue-950' : 'text-slate-700 hover:bg-blue-50'
            }`}
          >
            {resourceType}
          </button>}
        </li>
        {historyGroups.map((group) => group.kind === 'step'
          ? renderStep(group.step, true)
          : (
            <li key={`column-adjustments-${group.firstStepId}`} className="flex shrink-0 items-start gap-1">
              <span aria-hidden="true" className="px-1 pt-1 text-xs text-slate-400">→</span>
              <details
                open={group.steps.some((step) => selected.kind === 'step' && selected.stepId === step.id) || undefined}
                data-testid={`construction-history-adjustment-group-${group.firstStepId}`}
                className="group min-w-0"
              >
                <summary className="cursor-pointer list-none rounded px-2 py-1 text-left text-xs font-medium text-slate-700 hover:bg-blue-50">
                  <span aria-hidden="true" className="mr-1 inline-block text-slate-500 transition-transform group-open:rotate-90">▸</span>
                  Column adjustments ({group.steps.length})
                </summary>
                <ol className="m-0 flex list-none items-center gap-1 p-0">
                  {group.steps.map((step, index) => renderStep(step, index > 0))}
                </ol>
              </details>
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
    className="mx-auto grid w-full max-w-[1920px] grid-cols-1 gap-2"
  >
    {!editor ? <ConstructionTableNavigation
        tables={tables}
        selectedOutputId={selectedOutputId}
        disabled={tableActionsDisabled}
        onSelectTable={onSelectTable}
        onNewTable={onNewTable}
        onDuplicateTable={onDuplicateTable}
        onDeleteTable={onDeleteTable}
        onRenameTable={onRenameTable}
      /> : null}
      {(history || !editor) ? <div className="construction-query-toolbar flex min-w-0 flex-wrap items-center gap-3 py-2">
        {history ? <div className="min-w-0 flex-1"><ConstructionHistory {...history} sourceControl={rowSetup} /></div> : null}
        {!editor ? <div className="ml-auto shrink-0" role="group" aria-label="Configure table">
        <ConstructionActionBar
          activeFamily={activeFamily}
          disabled={actionsDisabled}
          addColumnsDisabled={addColumnsDisabled}
          combineDisabled={combineDisabled}
          calculateAvailable={calculateAvailable}
          onSelect={onSelectFamily}
        />
        </div> : null}
      </div> : null}
      <div className="min-w-0">
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
          className="min-w-0 bg-white"
        >
          {preview}
        </section>
      </div>
  </div>
);
