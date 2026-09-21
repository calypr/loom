import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LoomRequestError } from '../../../api';
import { useLoomClient } from '../../../react';
import type {
  ConstructionChoice,
  FeatureCatalogBrowseResponse,
  FeatureCatalogItem,
  FeatureCatalogSection,
} from '../../../types';
import {
  catalogItemAvailability,
  catalogItemConstructionChoice,
  catalogItemDefaultForm,
  catalogItemKey,
  catalogItemLabel,
  catalogItemSearchSource,
  type CatalogChoiceGroup,
  type CatalogChoiceIntent,
  type CatalogItem,
} from '../catalogItems';
import { CatalogSelectionDialog } from './CatalogSelectionDialog';

const PAGE_SIZE = 50;
const MAX_SELECTIONS = 100;
const CATALOG_SECTIONS = ['FIELDS', 'CONCEPTS', 'NEEDS_REVIEW'] as const;

export interface CatalogRouteContext {
  readonly occurrenceId: string;
  readonly nodeId: string;
}

type CatalogPage = {
  readonly cursor?: string;
  readonly response: FeatureCatalogBrowseResponse;
};

type CatalogLoadState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready' }
  | { readonly status: 'error'; readonly message: string };

type CatalogSectionState = {
  readonly status: CatalogLoadState;
  readonly pages: ReadonlyArray<CatalogPage>;
  readonly pageIndex: number;
};

type CatalogSections = Record<FeatureCatalogSection, CatalogSectionState>;

type CatalogContextIdentity = {
  readonly contextToken: string;
  readonly buildId: string;
};

type SelectedCatalogItem = CatalogContextIdentity & {
  readonly item: CatalogItem;
  readonly section: FeatureCatalogSection;
};

const emptySection = (): CatalogSectionState => ({
  status: { status: 'idle' },
  pages: [],
  pageIndex: 0,
});

const emptySections = (): CatalogSections => ({
  FIELDS: emptySection(),
  CONCEPTS: emptySection(),
  NEEDS_REVIEW: emptySection(),
});

const readinessPresentation = (readiness: FeatureCatalogItem['readiness']) => {
  switch (readiness.status) {
    case 'READY':
      return { label: 'Ready to add', badge: 'bg-emerald-50 text-emerald-800' };
    case 'READY_WITH_WARNING':
      return { label: 'Ready with warning', badge: 'bg-amber-100 text-amber-900' };
    case 'NEEDS_MAPPING':
      return { label: 'Needs mapping', badge: 'bg-orange-100 text-orange-900' };
    case 'UNSUPPORTED':
      return { label: 'Unsupported', badge: 'bg-slate-200 text-slate-700' };
    default: {
      const exhaustive: never = readiness.status;
      return exhaustive;
    }
  }
};

const catalogWarning = (
  response: FeatureCatalogBrowseResponse | undefined,
): string | undefined => {
  if (!response) return undefined;
  if (response.state !== 'complete') {
    if (response.state === 'unknown' || response.state === 'not_started') {
      return 'Loom has not built the feature catalog for this dataset yet.';
    }
    return `The feature catalog is ${response.state}. Results may be unavailable until it is complete.`;
  }
  if (response.sourceAvailability === 'unproven') {
    return 'Loom could not verify every retained source collection. Results may be incomplete.';
  }
  if (response.sourceAvailability === 'unknown') {
    return 'Loom cannot verify whether this catalog covers every retained source resource.';
  }
  return undefined;
};

const CatalogItemRow = ({
  item,
  section,
  catalogState,
  checked,
  disabled,
  onToggle,
}: {
  readonly item: CatalogItem;
  readonly section: FeatureCatalogSection;
  readonly catalogState: FeatureCatalogBrowseResponse['state'];
  readonly checked: boolean;
  readonly disabled: boolean;
  readonly onToggle: () => void;
}) => {
  const label = catalogItemLabel(item);
  const availability = catalogItemAvailability(item, section, catalogState);
  const readiness = readinessPresentation(item.readiness);
  const kindLabel = item.kind === 'DIRECT_FIELD' ? 'Field' : 'Concept';
  const optionCount = item.constructionChoice?.options.length;
  return (
    <article className="py-3 first:pt-0">
      <div className="flex items-start gap-3">
        <input
          type="checkbox"
          aria-label={`Select ${label}`}
          aria-describedby={!availability.selectable ? `${item.featureId}-availability` : undefined}
          checked={checked}
          disabled={disabled || !availability.selectable}
          onChange={onToggle}
          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-700"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <h4 className="font-semibold text-slate-900">{label}</h4>
              <p className="mt-1 text-sm text-slate-600">{item.description}</p>
              <p className="mt-1 text-xs text-slate-500">
                {item.resourceType} · {item.valueType} · {item.cardinality}
              </p>
            </div>
            <div className="flex flex-wrap justify-end gap-1">
              <span className="rounded-full bg-blue-50 px-2 py-1 text-[11px] font-semibold text-blue-800">
                {kindLabel}
              </span>
              <span className={`rounded-full px-2 py-1 text-[11px] font-semibold ${readiness.badge}`}>
                {readiness.label}
              </span>
              <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] font-semibold text-slate-700">
                {item.occurrences.toLocaleString()} {item.occurrences === 1 ? 'record' : 'records'}
              </span>
              {optionCount === undefined ? null : (
                <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] font-semibold text-slate-700">
                  {optionCount} compiler-proved {optionCount === 1 ? 'form' : 'forms'}
                </span>
              )}
            </div>
          </div>
          {!availability.selectable ? (
            <p
              id={`${item.featureId}-availability`}
              className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950"
              role="status"
            >
              {availability.reason}
            </p>
          ) : null}
          <details className="mt-2 text-xs text-slate-600">
            <summary className="cursor-pointer font-medium text-blue-700">Source details</summary>
            <dl className="mt-2 grid gap-1 rounded-md bg-slate-50 p-2 font-mono">
              {item.sourceDetails.map((fact, index) => (
                <div key={`${fact.label}-${index}`}>
                  <dt className="inline text-slate-500">{fact.label} </dt>
                  <dd className="inline break-all">{fact.value}</dd>
                </div>
              ))}
              {item.readiness.status !== 'READY' ? (
                <div>
                  <dt className="inline text-slate-500">Availability </dt>
                  <dd className="inline break-all">{item.readiness.message}</dd>
                </div>
              ) : null}
            </dl>
          </details>
        </div>
      </div>
    </article>
  );
};

export const ConceptCatalog = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  resourceType,
  routeContext,
  layout = 'workspace',
  disabled = false,
  onAddSelected,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly resourceType?: string;
  readonly routeContext?: CatalogRouteContext;
  readonly layout?: 'workspace' | 'panel';
  readonly disabled?: boolean;
  readonly onAddSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => Promise<void>;
}) => {
  const client = useLoomClient();
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [sections, setSections] = useState<CatalogSections>(emptySections);
  const [selected, setSelected] = useState<ReadonlyMap<string, SelectedCatalogItem>>(
    () => new Map(),
  );
  const [adding, setAdding] = useState(false);
  const [actionMessage, setActionMessage] = useState<string>();
  const [pendingSelection, setPendingSelection] = useState<ReadonlyArray<CatalogChoiceGroup>>();
  const activeRequests = useRef<Partial<Record<FeatureCatalogSection, AbortController>>>({});
  const sectionContexts = useRef<Partial<Record<FeatureCatalogSection, CatalogContextIdentity>>>({});
  const appliedQuery = useRef('');
  const routeNodeId = routeContext?.nodeId;
  const catalogContextKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    rowRoot,
  ]);
  const filterKey = JSON.stringify([resourceType ?? '', routeContext?.nodeId ?? '']);

  const loadSectionPage = useCallback((
    section: FeatureCatalogSection,
    searchQuery: string,
    cursor?: string,
    replace = false,
  ) => {
    activeRequests.current[section]?.abort();
    const controller = new AbortController();
    activeRequests.current[section] = controller;
    setSections((current) => {
      const previous = current[section];
      return {
        ...current,
        [section]: {
          status: { status: 'loading' },
          pages: replace ? [] : previous.pages,
          pageIndex: replace ? 0 : previous.pageIndex,
        },
      };
    });

    void client.browseFeatureCatalog({
      project,
      explorerId,
      authResourcePath,
      snapshotToken,
      rowRoot,
      section,
      ...(resourceType ? { resourceType } : {}),
      ...(section === 'FIELDS' && routeNodeId ? { nodeId: routeNodeId } : {}),
      query: searchQuery,
      ...(cursor ? { cursor } : {}),
      limit: PAGE_SIZE,
      requestId: `feature-catalog-${window.crypto.randomUUID()}`,
    }, controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return;
        if (response.section !== section) {
          throw new Error(`Loom returned ${response.section} results for the ${section} section.`);
        }
        const nextContext = {
          contextToken: response.contextToken,
          buildId: response.buildId,
        };
        const previousContext = sectionContexts.current[section];
        if (
          previousContext &&
          (previousContext.contextToken !== nextContext.contextToken ||
            previousContext.buildId !== nextContext.buildId)
        ) {
          setSelected(new Map());
          setPendingSelection(undefined);
          setActionMessage('The feature catalog changed. Review and select the features again.');
        }
        sectionContexts.current[section] = nextContext;
        setSections((current) => {
          const previous = current[section];
          const page = { cursor, response };
          const pages = replace ? [page] : [...previous.pages, page];
          return {
            ...current,
            [section]: {
              status: { status: 'ready' },
              pages,
              pageIndex: replace ? 0 : pages.length - 1,
            },
          };
        });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (
          cursor &&
          error instanceof LoomRequestError &&
          error.status === 409 &&
          error.code === 'STALE_CATALOG_CURSOR'
        ) {
          loadSectionPage(section, searchQuery, undefined, true);
          return;
        }
        setSections((current) => ({
          ...current,
          [section]: {
            ...current[section],
            status: {
              status: 'error',
              message: error instanceof Error
                ? error.message
                : 'Loom could not load this feature catalog section.',
            },
          },
        }));
      });
  }, [
    authResourcePath,
    client,
    explorerId,
    project,
    resourceType,
    routeNodeId,
    rowRoot,
    snapshotToken,
  ]);

  const loadSections = useCallback((searchQuery: string) => {
    for (const section of CATALOG_SECTIONS) {
      loadSectionPage(section, searchQuery, undefined, true);
    }
  }, [loadSectionPage]);

  useEffect(() => {
    setSelected(new Map());
    setPendingSelection(undefined);
    setActionMessage(undefined);
    sectionContexts.current = {};
  }, [catalogContextKey]);

  useEffect(() => {
    setSections(emptySections());
    loadSections(appliedQuery.current);
    return () => {
      for (const controller of Object.values(activeRequests.current)) {
        controller?.abort();
      }
    };
  }, [catalogContextKey, filterKey, loadSections]);

  const selectedItems = useMemo(() => [...selected.values()], [selected]);
  const anySectionLoading = CATALOG_SECTIONS.some(
    (section) => sections[section].status.status === 'loading',
  );

  const toggleSelection = (
    item: CatalogItem,
    section: FeatureCatalogSection,
    response: FeatureCatalogBrowseResponse,
  ) => {
    if (!catalogItemAvailability(item, section, response.state).selectable) return;
    const key = catalogItemKey(item);
    setSelected((current) => {
      const next = new Map(current);
      if (next.has(key)) {
        next.delete(key);
      } else if (next.size < MAX_SELECTIONS) {
        next.set(key, {
          item,
          section,
          contextToken: response.contextToken,
          buildId: response.buildId,
        });
      }
      return next;
    });
    setActionMessage(undefined);
  };

  const removeSelection = (key: string) => {
    setSelected((current) => {
      const next = new Map(current);
      next.delete(key);
      return next;
    });
    setActionMessage(undefined);
  };

  const commitSelections = async (selections: ReadonlyArray<CatalogChoiceIntent>) => {
    if (!onAddSelected || !selections.length) return;
    setAdding(true);
    setActionMessage(undefined);
    try {
      await onAddSelected(selections);
      setSelected(new Map());
      setPendingSelection(undefined);
      setActionMessage(
        `${selections.length} ${selections.length === 1 ? 'feature was' : 'features were'} added to your table.`,
      );
    } catch (error) {
      setActionMessage(error instanceof Error ? error.message : 'Loom could not add the selected features.');
    } finally {
      setAdding(false);
    }
  };

  const openSelection = async () => {
    if (!selectedItems.length || !onAddSelected) return;
    setAdding(true);
    setActionMessage(undefined);
    try {
      const groups = await Promise.all(selectedItems.map(async (selection): Promise<CatalogChoiceGroup> => {
        const { item } = selection;
        const existingChoice = catalogItemConstructionChoice(item);
        if (
          !routeContext &&
          existingChoice &&
          existingChoice.source.resourceType === rowRoot &&
          existingChoice.route.length === 0
        ) {
          return { item, choices: [existingChoice], complete: true, truncated: false };
        }
        const resolved = await client.searchConstructionChoices({
          project,
          explorerId,
          authResourcePath,
          snapshotToken,
          outputId,
          ...(routeContext ? { occurrenceId: routeContext.occurrenceId } : {}),
          source: catalogItemSearchSource(item, selection),
          limit: PAGE_SIZE,
          requestId: `construction-choices-${window.crypto.randomUUID()}`,
        });
        if (
          resolved.snapshotToken !== snapshotToken ||
          resolved.outputId !== outputId
        ) {
          throw new Error('Loom returned construction choices for another table or catalog snapshot.');
        }
        return {
          item,
          choices: resolved.choices,
          complete: resolved.complete,
          truncated: resolved.truncated,
        };
      }));
      const direct = groups.flatMap((group) => {
        if (!group.complete || group.truncated || group.choices.length !== 1) return [];
        const [choice] = group.choices;
        if (!choice) return [];
        const form = catalogItemDefaultForm(choice);
        return form && choice.options.length === 1
          ? [{
              constructionChoice: { choiceId: choice.choiceId, form },
              title: catalogItemLabel(group.item),
            }]
          : [];
      });
      if (direct.length === groups.length) {
        await onAddSelected(direct);
        setSelected(new Map());
        setActionMessage(
          `${direct.length} ${direct.length === 1 ? 'feature was' : 'features were'} added to your table.`,
        );
      } else {
        setPendingSelection(groups);
      }
    } catch (error) {
      setActionMessage(
        error instanceof Error
          ? error.message
          : 'Loom could not resolve the selected feature routes.',
      );
    } finally {
      setAdding(false);
    }
  };

  const submitSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextQuery = queryInput.trim();
    appliedQuery.current = nextQuery;
    setQuery(nextQuery);
    setActionMessage(undefined);
    loadSections(nextQuery);
  };

  const renderCatalogSection = (
    section: FeatureCatalogSection,
    title: string,
    description: string,
    warningOverride?: string,
  ) => {
    const sectionState = sections[section];
    const page = sectionState.pages[sectionState.pageIndex];
    const response = page?.response;
    const items = response?.entries ?? [];
    const warning = warningOverride ?? catalogWarning(response);
    const sectionError = sectionState.status.status === 'error'
      ? sectionState.status.message
      : undefined;
    return (
      <section
        key={section}
        className="border-t border-slate-200 pt-5 first:border-t-0 first:pt-0"
        aria-labelledby={`feature-catalog-${section.toLowerCase()}-title`}
      >
        <div className="flex items-start justify-between gap-3">
          <div>
            <h3
              id={`feature-catalog-${section.toLowerCase()}-title`}
              className="text-sm font-semibold text-slate-800"
            >
              {title}
            </h3>
            <p className="mt-1 text-xs text-slate-600">{description}</p>
          </div>
          <span className="shrink-0 text-xs text-slate-500">
            {response ? `${items.length} on this page` : 'Loading'}
          </span>
        </div>
        {warning ? (
          <p className="mt-3 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-950" role="status">
            {warning}
          </p>
        ) : null}
        {sectionError ? (
          <p className="mt-3 rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-900" role="alert">
            {sectionError}
          </p>
        ) : null}
        <div className="mt-3 divide-y divide-slate-200">
          {!response && sectionState.status.status === 'loading' ? (
            <p className="py-6 text-center text-sm text-slate-500">Loading {title.toLowerCase()}…</p>
          ) : null}
          {response && items.length === 0 ? (
            <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-center text-sm text-slate-600">
              No {title.toLowerCase()} match this search.
            </p>
          ) : null}
          {response ? items.map((item) => {
            const key = catalogItemKey(item);
            return (
              <CatalogItemRow
                key={key}
                item={item}
                section={section}
                catalogState={response.state}
                checked={selected.has(key)}
                disabled={
                  disabled ||
                  (selected.size >= MAX_SELECTIONS && !selected.has(key))
                }
                onToggle={() => toggleSelection(item, section, response)}
              />
            );
          }) : null}
        </div>
        <div className="mt-3 flex items-center justify-between border-t border-slate-200 pt-3">
          <button
            type="button"
            disabled={sectionState.pageIndex === 0 || sectionState.status.status === 'loading'}
            onClick={() => setSections((current) => ({
              ...current,
              [section]: {
                ...current[section],
                pageIndex: Math.max(0, current[section].pageIndex - 1),
              },
            }))}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
          >
            Previous
          </button>
          <span className="text-xs text-slate-500">
            Page {sectionState.pageIndex + 1}
          </span>
          <button
            type="button"
            disabled={!response?.nextCursor || sectionState.status.status === 'loading'}
            onClick={() => {
              if (sectionState.pages[sectionState.pageIndex + 1]) {
                setSections((current) => ({
                  ...current,
                  [section]: {
                    ...current[section],
                    pageIndex: current[section].pageIndex + 1,
                  },
                }));
              } else if (response?.nextCursor) {
                loadSectionPage(section, query, response.nextCursor);
              }
            }}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
          >
            Next
          </button>
        </div>
      </section>
    );
  };

  const reviewPage = sections.NEEDS_REVIEW.pages[sections.NEEDS_REVIEW.pageIndex];
  const conceptPage = sections.CONCEPTS.pages[sections.CONCEPTS.pageIndex];
  const reviewWarning = catalogWarning(reviewPage?.response) ??
    catalogWarning(conceptPage?.response);

  return (
    <section className="min-w-0 rounded-xl border border-slate-200 bg-white shadow-sm">
      {pendingSelection ? (
        <CatalogSelectionDialog
          groups={pendingSelection}
          busy={adding}
          onCancel={() => setPendingSelection(undefined)}
          onConfirm={(selections) => void commitSelections(selections)}
        />
      ) : null}
      <div className="border-b border-slate-200 px-4 py-4 sm:px-5">
        <p className="text-xs font-semibold uppercase tracking-wide text-blue-700">Find features</p>
        <h2 className="mt-1 text-xl font-semibold text-slate-950">Search fields and concepts</h2>
        <p className="mt-1 text-sm text-slate-600">
          {resourceType
            ? `Search fields and concepts on the selected ${resourceType} graph node.`
            : `Search ${rowRoot} fields, concepts, or items that need review.`}
        </p>
        <form className="mt-4 flex gap-2" onSubmit={submitSearch}>
          <label className="sr-only" htmlFor="feature-catalog-search">Search features</label>
          <input
            id="feature-catalog-search"
            type="search"
            aria-label="Search features by field name, concept, or code"
            value={queryInput}
            onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder="Search a field, concept, or code"
            className="min-w-0 flex-1 rounded-md border border-slate-300 px-3 py-2 text-sm outline-blue-500 focus:border-blue-500"
          />
          <button
            type="submit"
            disabled={disabled || anySectionLoading}
            className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Search
          </button>
        </form>
      </div>

      <div className={layout === 'panel'
        ? 'grid min-h-[32rem]'
        : 'grid min-h-[38rem] lg:grid-cols-[minmax(0,1.25fr)_minmax(19rem,0.75fr)]'}>
        <div className={layout === 'panel'
          ? 'min-w-0 border-b border-slate-200 p-4'
          : 'min-w-0 border-b border-slate-200 p-4 lg:border-b-0 lg:border-r sm:p-5'}>
          <div className="space-y-6">
            {renderCatalogSection(
              'FIELDS',
              resourceType ? `Fields on ${resourceType}` : `Fields on ${rowRoot}`,
              'Primitive values that Loom marks as direct features for this table.',
            )}
            {renderCatalogSection(
              'CONCEPTS',
              query ? `Concepts matching “${query}”` : 'Concepts across the dataset',
              'Server-labeled concepts with an available output form.',
            )}
            {renderCatalogSection(
              'NEEDS_REVIEW',
              'Needs review',
              'These items need mapping or source verification before they can be added.',
              reviewWarning,
            )}
          </div>
        </div>

        <aside className="bg-slate-50/70 p-4 sm:p-5">
          <div className="flex items-center justify-between gap-2">
            <h3 className="font-semibold text-slate-900">Selected features</h3>
            <span className="rounded-full bg-blue-100 px-2 py-1 text-xs font-semibold text-blue-800">{selected.size}</span>
          </div>
          <p className="mt-1 text-xs text-slate-600">Selections stay here while you search and change pages.</p>
          <div className="mt-4 space-y-2">
            {selectedItems.length === 0 ? (
              <div className="rounded-lg border border-dashed border-slate-300 bg-white px-3 py-8 text-center text-sm text-slate-500">
                Select fields or concepts to build your feature list.
              </div>
            ) : selectedItems.map(({ item }) => {
              const key = catalogItemKey(item);
              return (
                <div key={key} className="flex items-start gap-2 rounded-md border border-slate-200 bg-white p-2.5">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-slate-900">{catalogItemLabel(item)}</p>
                    <p className="truncate text-[11px] text-slate-500">
                      {item.kind === 'DIRECT_FIELD' ? 'Field' : 'Concept'} · {item.resourceType}
                    </p>
                  </div>
                  <button
                    type="button"
                    aria-label={`Remove ${catalogItemLabel(item)}`}
                    onClick={() => removeSelection(key)}
                    className="rounded px-1.5 text-lg leading-5 text-slate-500 hover:bg-slate-100 hover:text-slate-900"
                  >
                    ×
                  </button>
                </div>
              );
            })}
          </div>
          {!pendingSelection ? (
            <button
              type="button"
              disabled={disabled || adding || selected.size === 0 || !onAddSelected}
              onClick={() => void openSelection()}
              className="mt-4 w-full rounded-md bg-blue-700 px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {selected.size === 0
                ? 'Add selected features'
                : `Add ${selected.size} selected ${selected.size === 1 ? 'feature' : 'features'}`}
            </button>
          ) : null}
          {actionMessage ? (
            <p className="mt-3 text-sm text-slate-700" role="status">{actionMessage}</p>
          ) : null}
        </aside>
      </div>
    </section>
  );
};
