import React, { useEffect, useState } from 'react';
import { useLoomClient } from '../../../react';
import type {
  ConstructionChoice,
  ConstructionChoiceSearchResponse,
  SemanticInventoryItem,
} from '../../../types';

const INVENTORY_LIMIT = 50;
const MAX_ROUTE_SEARCHES = 8;
export const MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS = 4;

export interface PairedColumnSuggestion {
  readonly requestId: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly contextToken: string;
  readonly buildId: string;
  readonly item: SemanticInventoryItem;
  readonly choices: ConstructionChoiceSearchResponse;
}

export type PairedColumnSuggestionsState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly suggestions: ReadonlyArray<PairedColumnSuggestion> }
  | { readonly status: 'error'; readonly message: string };

const meaningfulConceptLabel = (item: SemanticInventoryItem): string =>
  item.display.trim() || item.code.trim();

const isSuggestionCandidate = (item: SemanticInventoryItem): boolean =>
  Boolean(item.code.trim()) &&
  Boolean(meaningfulConceptLabel(item)) &&
  (item.readiness.status === 'READY' || item.readiness.status === 'READY_WITH_WARNING');

const sameLabel = (left: string, right: string): boolean =>
  left.trim().normalize('NFKC').toLocaleLowerCase() ===
  right.trim().normalize('NFKC').toLocaleLowerCase();

const choiceMatchesItem = (choice: ConstructionChoice, item: SemanticInventoryItem): boolean =>
  choice.source.kind === 'SEMANTIC' &&
  choice.source.conceptId === item.conceptId &&
  choice.source.bindingId === item.bindingId &&
  choice.source.resourceType === item.resourceType &&
  choice.options.some((option) => option.support === 'SUPPORTED');

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : 'Loom could not load paired coded concepts.';

export const PairedColumnSuggestions = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  columns,
  disabled = false,
  onSelectSuggestion,
  onBrowseAll,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly columns: ReadonlyArray<{ readonly id: string; readonly label: string }>;
  readonly disabled?: boolean;
  readonly onSelectSuggestion: (suggestion: PairedColumnSuggestion) => void;
  readonly onBrowseAll: () => void;
}) => {
  const client = useLoomClient();
  const [state, setState] = useState<PairedColumnSuggestionsState>({ status: 'idle' });
  const authoredLabels = columns.map((column) => column.label);

  useEffect(() => {
    if (!snapshotToken || !outputId || !rowRoot) {
      setState({ status: 'idle' });
      return;
    }

    const controller = new AbortController();
    setState({ status: 'loading' });

    void client.browseSemanticInventory({
      project,
      explorerId,
      ...(authResourcePath ? { authResourcePath } : {}),
      snapshotToken,
      rowRoot,
      limit: INVENTORY_LIMIT,
      requestId: `paired-column-inventory-${window.crypto.randomUUID()}`,
    }, controller.signal)
      .then(async (inventory) => {
        if (controller.signal.aborted) return;
        if (inventory.state !== 'complete') {
          setState({ status: 'ready', suggestions: [] });
          return;
        }

        const candidates = inventory.entries
          .filter(isSuggestionCandidate)
          .filter((item, index, items) =>
            items.findIndex((candidate) =>
              candidate.conceptId === item.conceptId && candidate.bindingId === item.bindingId,
            ) === index,
          )
          .filter((item) => !authoredLabels.some((label) => sameLabel(label, meaningfulConceptLabel(item))))
          .slice(0, MAX_ROUTE_SEARCHES);

        const resolved = await Promise.all(candidates.map(async (item) => {
          try {
            const choices = await client.searchConstructionChoices({
              project,
              explorerId,
              ...(authResourcePath ? { authResourcePath } : {}),
              snapshotToken,
              outputId,
              source: {
                kind: 'SEMANTIC',
                contextToken: inventory.contextToken,
                buildId: inventory.buildId,
                conceptId: item.conceptId,
                bindingId: item.bindingId,
              },
              limit: 10,
              requestId: `paired-column-choices-${window.crypto.randomUUID()}`,
            }, controller.signal);
            if (controller.signal.aborted) return undefined;
            if (choices.snapshotToken !== snapshotToken || choices.outputId !== outputId) return undefined;
            if (!choices.choices.some((choice) => choiceMatchesItem(choice, item))) return undefined;
            return {
              requestId: `paired-column-selection-${window.crypto.randomUUID()}`,
              snapshotToken,
              outputId,
              contextToken: inventory.contextToken,
              buildId: inventory.buildId,
              item,
              choices,
            } satisfies PairedColumnSuggestion;
          } catch {
            return undefined;
          }
        }));

        if (controller.signal.aborted) return;
        const suggestions = resolved
          .filter((suggestion): suggestion is PairedColumnSuggestion => suggestion !== undefined)
          .slice(0, MAX_VISIBLE_PAIRED_COLUMN_SUGGESTIONS);
        setState({ status: 'ready', suggestions });

      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setState({ status: 'error', message: errorMessage(error) });
      });

    return () => controller.abort();
  }, [
    authResourcePath,
    authoredLabels.join('\u0000'),
    client,
    explorerId,
    outputId,
    project,
    rowRoot,
    snapshotToken,
  ]);

  if (!rowRoot) return null;

  return (
    <section
      aria-label="Ready-to-add paired concepts"
      data-testid="paired-column-suggestions"
      className="mt-2 rounded-lg border border-indigo-100 bg-indigo-50/40 px-3 py-2.5"
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold text-indigo-950">Add paired value columns</h3>
          <p className="mt-0.5 text-[11px] text-slate-600">
            Add the value stored with a code to each matching row. Choose a column to review its route and how missing or repeated matches are handled.
          </p>
        </div>
        <button
          type="button"
          data-testid="paired-column-suggestions-browse-all"
          disabled={disabled}
          onClick={onBrowseAll}
          className="shrink-0 rounded border border-indigo-200 bg-white px-2 py-1 text-xs font-semibold text-indigo-800 hover:bg-indigo-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Find another coded value
        </button>
      </div>

      {state.status === 'loading' ? (
        <p className="mt-2 text-xs text-slate-500" role="status">Finding paired concepts supported for this table…</p>
      ) : null}
      {state.status === 'error' ? (
        <p className="mt-2 text-xs text-amber-900" role="status">{state.message}</p>
      ) : null}
      {state.status === 'ready' && state.suggestions.length === 0 ? (
        <p className="mt-2 text-xs text-slate-500" role="status">No coded pairings with a supported route and result form were found on this page.</p>
      ) : null}
      {state.status === 'ready' && state.suggestions.length > 0 ? (
        <ul className="mt-2 flex flex-wrap gap-2">
          {state.suggestions.map((suggestion) => {
            const label = meaningfulConceptLabel(suggestion.item);
            return (
              <li key={`${suggestion.item.conceptId}:${suggestion.item.bindingId}`}>
                <button
                  type="button"
                  data-testid={`paired-column-suggestion-${suggestion.item.conceptId}`}
                  aria-label={`Add ${label} as a column`}
                  disabled={disabled}
                  onClick={() => onSelectSuggestion(suggestion)}
                  className="flex max-w-full flex-col items-start rounded-md border border-indigo-200 bg-white px-2.5 py-1.5 text-left hover:border-indigo-400 hover:bg-indigo-50 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <span className="max-w-full truncate text-xs font-semibold text-slate-900">{label}</span>
                  <span className="text-[10px] text-slate-600">
                    Value from {suggestion.item.resourceType} · {suggestion.item.valueType || 'type unknown'}
                  </span>
                  <span className="mt-0.5 text-[10px] font-medium text-indigo-800">
                    {suggestion.item.readiness.status === 'READY_WITH_WARNING'
                      ? `Warning: ${suggestion.item.readiness.message}`
                      : 'Add column →'}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </section>
  );
};
