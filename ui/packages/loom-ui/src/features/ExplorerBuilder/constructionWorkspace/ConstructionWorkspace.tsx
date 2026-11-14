import React, { useCallback, useEffect, useRef, useState } from 'react';
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
  readonly scopeKey?: string;
}

export interface ConstructionHistoryProps {
  readonly resourceType: string;
  readonly sourceControl?: React.ReactNode;
  readonly sourceSteps?: React.ReactNode;
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
  calculateUnavailableReason = 'Calculation is unavailable for the current table stage.',
  onSelect,
}: {
  readonly activeFamily?: ConstructionOperationFamily;
  readonly disabled?: boolean;
  readonly addColumnsDisabled?: boolean;
  readonly combineDisabled?: boolean;
  readonly calculateAvailable?: boolean;
  readonly calculateUnavailableReason?: string;
  readonly onSelect: (family: ConstructionOperationFamily) => void;
}) => {
  const [moreActionsOpen, setMoreActionsOpen] = useState(false);
  const moreActionsRef = useDismissibleLayer<HTMLDivElement>(moreActionsOpen, setMoreActionsOpen);
  const moreActionsButtonRef = useRef<HTMLButtonElement>(null);
  const primaryFamilies = allOperationFamilies.filter(({ family }) =>
    family === 'ADD_COLUMNS' || family === 'KEEP_ROWS'
  );
  const secondaryFamilies = allOperationFamilies.filter(({ family }) =>
    family === 'CALCULATE' || family === 'RESHAPE' || family === 'COMBINE'
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
        disabled={disabled || (family === 'ADD_COLUMNS' && addColumnsDisabled) || (family === 'COMBINE' && combineDisabled) || (family === 'CALCULATE' && !calculateAvailable)}
        onClick={() => {
          if (inMenu) setMoreActionsOpen(false);
          onSelect(family);
        }}
        title={family === 'CALCULATE' && !calculateAvailable ? calculateUnavailableReason : description}
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
            {!calculateAvailable ? (
              <p role="status" className="px-2.5 py-1 text-[11px] text-slate-500">
                {calculateUnavailableReason}
              </p>
            ) : null}
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
  sourceSteps,
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
    while (steps[index]?.hierarchy === 'COLUMN_ADJUSTMENT' && steps[index]?.scopeKey === step.scopeKey) {
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

  const renderStep = (step: ConstructionHistoryStep, number: number) => {
    const isSelected = selected.kind === 'step' && selected.stepId === step.id;
    return (
      <li key={step.id} className="min-w-0">
        <div className={`construction-query-step flex min-w-0 flex-wrap items-center gap-1 rounded px-2 py-1 ${isSelected ? 'border-blue-400 bg-blue-50' : 'border-slate-200 bg-white'}`}>
          <span aria-hidden="true" className={`mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${isSelected ? 'bg-blue-700 text-white' : 'bg-slate-100 text-slate-600'}`}>
            {number}
          </span>
          <button
            type="button"
            aria-pressed={isSelected}
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
            className={`min-w-0 flex-1 rounded px-1 py-0.5 text-left ${isSelected ? 'text-blue-950' : 'text-slate-800 hover:text-blue-950'}`}
          >
            <span className="block break-words text-sm font-medium">{step.title}</span>
            <span className={isSelected ? "mt-0.5 block break-words text-xs text-slate-600" : "sr-only"}>{step.summary}</span>
          </button>
          {isSelected && ((onEditStep && step.editable) || onRemoveStep) ? (
            <div className="flex shrink-0 flex-wrap gap-1 border-l border-slate-200 pl-2">
              {onEditStep && step.editable ? (
                <button type="button" disabled={disabled} data-testid={`construction-edit-step-${step.id}`} onClick={() => onEditStep(step.id)} className="rounded px-2 py-1 text-xs font-medium text-emerald-800 hover:bg-emerald-50 disabled:opacity-45">Edit</button>
              ) : null}
              {onRemoveStep ? (
                <button type="button" disabled={disabled} data-testid={`construction-remove-step-${step.id}`} aria-label={step.removeActionLabel} title={step.removeActionLabel} onClick={() => onRemoveStep(step.id)} className="rounded px-2 py-1 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-45">{step.removeActionLabel ? 'Remove connection' : 'Remove'}</button>
              ) : null}
            </div>
          ) : null}
        </div>
      </li>
    );
  };
  let nextStepNumber = 1;
  const renderedHistoryGroups = historyGroups.map((group) => {
    if (group.kind === 'step') {
      const number = nextStepNumber;
      nextStepNumber += 1;
      return renderStep(group.step, number);
    }
    const firstStepNumber = nextStepNumber;
    nextStepNumber += group.steps.length;
    return (
      <li
        key={`column-adjustments-${group.firstStepId}`}
        data-testid={`construction-history-adjustment-group-${group.firstStepId}`}
        className="min-w-0"
      >
        <details open={selected.kind === 'step' && group.steps.some((step) => step.id === selected.stepId)}>
          <summary className="cursor-pointer rounded bg-slate-100 px-2 py-1 text-sm text-slate-700 hover:bg-blue-50">
            <h4 className="m-0 inline text-sm font-medium">
              Column adjustments <span className="font-normal">({group.steps.length})</span>
            </h4>
            <span className="ml-1 text-xs text-slate-500">{firstStepNumber}–{firstStepNumber + group.steps.length - 1}</span>
          </summary>
          <ol start={firstStepNumber} className="m-0 mt-1 flex min-w-0 list-none flex-wrap gap-1 border-l-2 border-blue-200 pl-2">
            {group.steps.map((step, index) => renderStep(step, firstStepNumber + index))}
          </ol>
        </details>
      </li>
    );
  });

  return (
    <section aria-label="Dataframe steps" data-testid="construction-history" className="construction-history min-w-0 bg-white px-2 py-2">
      <header className="mb-1 flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h2 className="m-0 text-sm font-semibold text-slate-950">Dataframe steps</h2>
        <p className="m-0 text-xs text-slate-500">Source → recipe → transformations</p>
      </header>
      <div className="mb-1 flex min-w-0 flex-wrap items-center gap-2">
        <span className="shrink-0 text-[11px] font-semibold uppercase tracking-wide text-slate-500">Source</span>
        {sourceControl ? (
          <div className="min-w-0 max-w-full" onClickCapture={(event) => {
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
            className={`min-w-0 max-w-full break-words rounded px-2 py-1 text-left text-sm ${
              selected.kind === 'source' ? 'bg-blue-100 font-semibold text-blue-950' : 'text-slate-700 hover:bg-blue-50'
            }`}
          >
            {resourceType || 'Source records'}
          </button>}
      </div>
      {sourceSteps}
      {steps.length === 0 ? (
        <p data-testid="construction-history-empty" className="m-0 mt-1 text-xs text-slate-500">
          {sourceSteps ? 'Add another step to transform this dataframe.' : 'Add a step to shape this dataframe.'}
        </p>
      ) : (
        <>
          <h3 className="mb-1 mt-2 text-xs font-medium text-slate-500">Transformations</h3>
          <ol className="m-0 flex min-w-0 list-none flex-wrap gap-1 p-0">
            {renderedHistoryGroups}
          </ol>
        </>
      )}
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
  calculateUnavailableReason,
  onSelectFamily,
  history,
  rowSetup,
  navigation,
  controllers,
  composition = 'inspector-right',
  network,
  presentationActionRef,
  preview,
  editor,
  inspection,
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
  readonly calculateUnavailableReason?: string;
  readonly onSelectFamily: (family: ConstructionOperationFamily) => void;
  readonly history?: ConstructionHistoryProps;
  readonly rowSetup?: React.ReactNode;
  readonly navigation?: React.ReactNode;
  /** Mounted outside disclosures so controller-owned portals remain available. */
  readonly controllers?: React.ReactNode;
  /** Internal comparison variant; both use identical slots and handlers. */
  readonly composition?: 'inspector-right' | 'inspector-below';
  readonly network?: React.ReactNode;
  readonly rowExpression?: React.ReactNode;
  readonly presentationActionRef?: { current: ((view: 'both' | 'network' | 'row') => void) | undefined };
  readonly preview: React.ReactNode;
  readonly editor?: React.ReactNode;
  readonly inspection?: React.ReactNode;
  readonly previewStatus: 'empty' | 'stale' | 'previewing' | 'needs-repair' | 'error' | 'ready';
  readonly previewReceiptId?: string;
  readonly previewOutputId?: string;
  readonly proposalId?: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
}) => {
  const editorFocusRef = useRef<HTMLDivElement>(null);
  if (presentationActionRef) presentationActionRef.current = () => { editorFocusRef.current?.focus(); };
  const viewportRef = useCallback((node: HTMLDivElement | null) => {
    if (!node) return;
    const measure = () => {
      const bounds = node.getBoundingClientRect();
      const top = Math.max(0, bounds.top + window.scrollY);
      const trailing = Math.max(12, Math.ceil(document.documentElement.scrollHeight - bounds.bottom - window.scrollY));
      node.style.setProperty('--construction-top', `${top}px`);
      node.style.setProperty('--construction-bottom', `${trailing}px`);
    };
    measure();
    window.addEventListener('resize', measure);
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure);
    observer?.observe(document.body);
    return () => {
      window.removeEventListener('resize', measure);
      observer?.disconnect();
    };
  }, []);
  return (
  <div
    ref={viewportRef}
    data-testid="construction-workspace"
    data-draft-version={draftVersion}
    data-draft-digest={draftDigest}
    className="construction-workspace mx-auto flex w-full max-w-[1920px] flex-col gap-2"
  >
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
      <div className="construction-controller-host">{controllers}</div>
      <div data-composition={composition} data-has-editor={editor || inspection ? 'true' : undefined}
        data-has-network={network ? 'true' : undefined} className="construction-result-region">
        <div className="construction-main-panel">
        {network ? <section aria-label="Related records" className="construction-authored-region">
          <div ref={editorFocusRef} tabIndex={-1} className="construction-authored-content">{network}</div>
        </section> : null}

        <section
          aria-label="Table result"
          data-testid="construction-preview"
          data-preview-status={previewStatus}
          data-preview-receipt-id={previewReceiptId ?? ''}
          data-preview-output-id={previewOutputId ?? ''}
          data-preview-proposal-id={proposalId ?? ''}
          data-current-draft-version={draftVersion}
          data-current-draft-digest={draftDigest}
          className="construction-result-preview min-w-0 bg-white"
        >
          <p className="border-b bg-slate-50 px-2 py-1 text-xs font-semibold" data-testid="recipe-result-identity">{proposalId ? 'Proposed recipe result' : 'Saved recipe result'}</p>
          {preview}
        </section>
        </div>
        <aside aria-label="Recipe editor" className="construction-side-panel">
          <div className="construction-source-controls">{rowSetup}{history?.sourceSteps}</div>
          {editor ? <section aria-label="Change editor" className="construction-active-editor min-h-0 min-w-0">{editor}</section> : null}
          <div hidden={Boolean(editor)} className="construction-scope-navigation">
            {!editor && inspection ? <div aria-label="Recipe fact inspector" className="construction-fact-inspector min-h-0 min-w-0">{inspection}</div> : null}
            {navigation ?? (!editor ? <ConstructionActionBar
              activeFamily={activeFamily} disabled={actionsDisabled}
              addColumnsDisabled={addColumnsDisabled} combineDisabled={combineDisabled}
              calculateAvailable={calculateAvailable} calculateUnavailableReason={calculateUnavailableReason}
              onSelect={onSelectFamily}
            /> : null)}
          </div>
          {history && !navigation ? <details className="construction-history-disclosure">
            <summary>Ordered operation history ({history.steps.length})</summary>
            <ConstructionHistory {...history} sourceSteps={undefined} sourceControl={undefined} />
          </details> : null}
        </aside>
      </div>
  </div>
  );
};
