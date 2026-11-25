import React, { useCallback, useRef, useState } from 'react';
import { useDismissibleLayer } from '../../dom/useDismissibleLayer';
import { useAnchoredPosition } from '../../dom/useAnchoredPosition';

import type { ConstructionWorkspaceTable } from '../../model/constructionWorkspaceTable';


export const ConstructionTableNavigation = ({
  tables,
  selectedOutputId,
  disabled = false,
  compact = false,
  onSelectTable,
  onNewTable,
  onDuplicateTable,
  onDeleteTable,
  onRenameTable,
}: {
  readonly tables: ReadonlyArray<ConstructionWorkspaceTable>;
  readonly selectedOutputId?: string;
  readonly disabled?: boolean;
  readonly compact?: boolean;
  readonly onSelectTable: (outputId: string) => void;
  readonly onNewTable: () => void;
  readonly onDuplicateTable: () => void;
  readonly onDeleteTable: () => void;
  readonly onRenameTable: (outputId: string) => void;
}) => {
  const selectedTable = tables.find((table) => table.outputId === selectedOutputId);
  const revealSelectedTab = useCallback((tab: HTMLDivElement | null) => {
    const strip = tab?.parentElement;
    if (!tab || !strip || !compact) return;
    const tabBounds = tab.getBoundingClientRect();
    const stripBounds = strip.getBoundingClientRect();
    if (tabBounds.left < stripBounds.left) strip.scrollLeft += tabBounds.left - stripBounds.left;
    else if (tabBounds.right > stripBounds.right) strip.scrollLeft += tabBounds.right - stripBounds.right;
  }, [compact]);
  const [tableActionsOpen, setTableActionsOpen] = useState(false);
  const tableActionsRef = useDismissibleLayer<HTMLDivElement>(tableActionsOpen, setTableActionsOpen);
  const tableActionsButtonRef = useRef<HTMLButtonElement>(null);
  const tableActionsPosition = useAnchoredPosition(tableActionsButtonRef, tableActionsOpen);
  return (
  <nav aria-label="Tables" className={`construction-table-tabs flex min-w-0 items-center py-1 ${compact ? 'gap-1' : 'flex-wrap gap-3'}`}>
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
    <div className={`flex min-w-0 flex-1 items-center overflow-x-auto ${compact ? 'construction-compact-tab-strip gap-1' : 'gap-3'}`}>
      {tables.map((table) => {
        const selected = table.outputId === selectedOutputId;
        const title = table.title || table.outputId;
        return (
          <div key={table.outputId} ref={selected ? revealSelectedTab : undefined} className={`flex shrink-0 items-center gap-1 ${compact ? 'max-w-48' : 'max-w-64'}`}>
            <button
              type="button"
              aria-current={selected ? 'page' : undefined}
              aria-pressed={selected}
              title={compact ? title : undefined}
              data-testid={`construction-table-${table.outputId}`}
              onClick={() => {
                setTableActionsOpen(false);
                onSelectTable(table.outputId);
              }}
              disabled={disabled}
              className={`construction-table-tab bg-transparent min-w-0 truncate border-b-2 px-2 text-left transition-colors disabled:opacity-50 ${compact ? 'h-7 text-xs' : 'h-8 text-sm'} ${
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
