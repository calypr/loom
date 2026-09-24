import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LoomRequestError } from '../../../api';
import { useLoomClient } from '../../../react';
import type {
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
  type CatalogChoiceGroup,
  type CatalogChoiceIntent,
  type CatalogItem,
} from '../catalogItems';
import { CatalogSelectionDialog } from './CatalogSelectionDialog';
import { PivotFamilyCatalogRow, type PivotAnalysis } from './PivotFamilyCatalogRow';
import { pivotFamilies, type ConfiguredPivotContext, type PivotFamily } from '../pivotFamilies';

const CATALOG_BATCH_SIZE = 500;
const CATALOG_POLL_BASE_INTERVAL_MS = 1_000;
const CATALOG_POLL_MAX_INTERVAL_MS = 5_000;
const MAX_SELECTIONS = 100;
const CATALOG_SECTIONS = ['FIELDS', 'CONCEPTS', 'NEEDS_REVIEW'] as const;
const CATALOG_BROWSE_SECTIONS = ['FIELDS', 'CONCEPTS'] as const;
const CATALOG_TABS = ['ALL', 'CODE_SETS', ...CATALOG_BROWSE_SECTIONS] as const;
const CATALOG_TAB_LABELS = {
  ALL: 'All',
  CODE_SETS: 'Code sets',
  FIELDS: 'Fields',
  CONCEPTS: 'Concepts',
} satisfies Record<typeof CATALOG_TABS[number], string>;

type CatalogBrowseSection = typeof CATALOG_BROWSE_SECTIONS[number];
type CatalogTab = typeof CATALOG_TABS[number];

const catalogTabSlug = (section: CatalogTab): string => section.toLowerCase().replace('_', '-');

export interface CatalogRouteContext {
  readonly occurrenceId: string;
  readonly nodeId: string;
}

type LoadedCatalogSection = {
  readonly contextToken: string;
  readonly buildId: string;
  readonly state: 'complete';
  readonly sourceAvailability: 'verified';
  readonly section: FeatureCatalogSection;
  readonly entries: ReadonlyArray<CatalogItem>;
};

type CatalogSectionState =
  | { readonly state: 'idle' }
  | { readonly state: 'loading' }
  | {
      readonly state: 'ready';
      readonly response: LoadedCatalogSection;
    }
  | {
      readonly state: 'error';
      readonly message: string;
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

type SelectableCatalogEntry = CatalogContextIdentity & {
  readonly item: CatalogItem;
  readonly section: FeatureCatalogSection;
  readonly key: string;
};

type CatalogSelectionState = 'none' | 'partial' | 'all';

type CatalogResultRow =
  | { readonly kind: 'item'; readonly item: CatalogItem }
  | { readonly kind: 'pivotFamily'; readonly family: PivotFamily };

const catalogPollDelayMs = (runningResponseCount: number): number =>
  Math.min(CATALOG_POLL_BASE_INTERVAL_MS * 2 ** runningResponseCount, CATALOG_POLL_MAX_INTERVAL_MS);

const waitForCatalogPoll = (signal: AbortSignal, delayMs: number): Promise<void> => new Promise((resolve, reject) => {
  if (signal.aborted) {
    reject(new DOMException('Catalog loading was cancelled.', 'AbortError'));
    return;
  }
  const timer = window.setTimeout(() => {
    signal.removeEventListener('abort', abort);
    resolve();
  }, delayMs);
  const abort = () => {
    window.clearTimeout(timer);
    reject(new DOMException('Catalog loading was cancelled.', 'AbortError'));
  };
  signal.addEventListener('abort', abort, { once: true });
});

const populatedCoverage = (item: CatalogItem): boolean => {
  switch (item.coverage.state) {
    case 'VERIFIED':
      return true;
    case 'INDEXED':
      return item.coverage.rowsWithValue > 0;
    case 'PENDING':
      return false;
    default: {
      const exhaustive: never = item.coverage;
      return exhaustive;
    }
  }
};

const availableCatalogItem = (
  item: CatalogItem,
  section: FeatureCatalogSection,
): boolean => {
  if (section === 'NEEDS_REVIEW' || !populatedCoverage(item)) return false;
  if (!catalogItemAvailability(item, section, 'complete').selectable) return false;
  const choice = catalogItemConstructionChoice(item);
  return choice !== undefined && catalogItemDefaultForm(choice) !== undefined;
};

const emptySection = (): CatalogSectionState => ({
  state: 'idle',
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

const CatalogItemRow = ({
  item,
  section,
  catalogState,
  checked,
  disabled,
  alreadyAdded = false,
  onToggle,
}: {
  readonly item: CatalogItem;
  readonly section: FeatureCatalogSection;
  readonly catalogState: FeatureCatalogBrowseResponse['state'];
  readonly checked: boolean;
  readonly disabled: boolean;
  readonly alreadyAdded?: boolean;
  readonly onToggle: () => void;
}) => {
  const label = catalogItemLabel(item);
  const availability = catalogItemAvailability(item, section, catalogState);
  const readiness = readinessPresentation(item.readiness);
  const kindLabel = item.kind === 'DIRECT_FIELD' ? 'Field' : 'Concept';
  const optionCount = item.constructionChoice?.options.length;
  return (
    <article className="rounded-md border border-slate-200 bg-white px-3 py-2.5 transition-colors hover:border-slate-300 hover:bg-slate-50/70">
      <div className="flex items-start gap-2.5">
        <input
          type="checkbox"
          aria-label={`Select ${label}`}
          aria-describedby={alreadyAdded
            ? `${item.featureId}-already-added`
            : !availability.selectable ? `${item.featureId}-availability` : undefined}
          checked={checked}
          disabled={disabled || alreadyAdded || !availability.selectable}
          onChange={onToggle}
          className="mt-0.5 h-5 w-5 shrink-0 rounded border-slate-300 text-blue-700 focus-visible:ring-2 focus-visible:ring-blue-500"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
            <h4 className="min-w-0 flex-1 text-sm font-semibold leading-5 text-slate-900">{label}</h4>
            <div className="flex flex-wrap gap-1">
              <span className="rounded-full bg-blue-50 px-1.5 py-0.5 text-[10px] font-semibold text-blue-800">
                {kindLabel}
              </span>
              <span className={`rounded-full px-1.5 py-0.5 text-[10px] font-semibold ${readiness.badge}`}>
                {readiness.label}
              </span>
              <span className="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold text-slate-700">
                {item.occurrences.toLocaleString()} matches
                {item.sourceRecords === undefined ? null : (
                  <> · {item.sourceRecords.toLocaleString()} {item.sourceRecords === 1 ? 'record' : 'records'}</>
                )}
              </span>
              {item.coverage.state === 'INDEXED' ? (
                <span className="rounded-full bg-emerald-50 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-900">
                  {item.coverage.rowsWithValue.toLocaleString()} rows have values
                </span>
              ) : item.coverage.state === 'VERIFIED' ? (
                <span className="rounded-full bg-emerald-50 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-900">
                  At least one row has a value
                </span>
              ) : null}
              {optionCount === undefined ? null : (
                <span className="rounded-full bg-slate-100 px-1.5 py-0.5 text-[10px] font-semibold text-slate-700">
                  {optionCount} {optionCount === 1 ? 'form' : 'forms'}
                </span>
              )}
            </div>
          </div>
          <p className="mt-0.5 truncate text-xs leading-4 text-slate-600" title={item.description}>{item.description}</p>
          <p className="mt-0.5 truncate text-[11px] leading-4 text-slate-500" title={`${item.resourceType} · ${item.valueType} · ${item.cardinality}`}>
            {item.resourceType} · {item.valueType} · {item.cardinality}
          </p>
          {alreadyAdded ? (
            <p id={`${item.featureId}-already-added`} className="mt-1 text-xs font-medium text-blue-800">Already added. Edit this column in Preview.</p>
          ) : null}
          {!availability.selectable ? (
            <p
              id={`${item.featureId}-availability`}
              className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950"
              role="status"
            >
              {availability.reason}
            </p>
          ) : null}
          <details className="mt-1 text-[11px] text-slate-600">
            <summary className="min-h-6 cursor-pointer font-medium text-blue-700">Source details</summary>
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
  configuredPivot,
  onAnalyzePivot,
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
  readonly configuredPivot?: ConfiguredPivotContext;
  readonly onAnalyzePivot?: (familyId: string, selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<PivotAnalysis>;
  readonly onAddSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
    commandId?: string,
    expectedDraftDigest?: string,
  ) => Promise<void>;
}) => {
  const client = useLoomClient();
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [activeSection, setActiveSection] = useState<CatalogTab>('ALL');
  const [sections, setSections] = useState<CatalogSections>(emptySections);
  const [selected, setSelected] = useState<ReadonlyMap<string, SelectedCatalogItem>>(
    () => new Map(),
  );
  const [activeCatalogRequestKey, setActiveCatalogRequestKey] = useState<string>();
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
    outputId,
    rowRoot,
  ]);
  const filterKey = JSON.stringify([
    resourceType ?? '',
    routeContext?.nodeId ?? '',
    routeContext?.occurrenceId ?? '',
  ]);
  const currentCatalogRequestKey = JSON.stringify([catalogContextKey, filterKey, query]);

  const loadSection = useCallback((
    section: FeatureCatalogSection,
    searchQuery: string,
  ) => {
    activeRequests.current[section]?.abort();
    const controller = new AbortController();
    activeRequests.current[section] = controller;
    setSections((current) => ({
      ...current,
      [section]: {
        state: 'loading',
      },
    }));

    const collectSection = async (): Promise<LoadedCatalogSection> => {
      let staleCursorRestarted = false;
      let runningResponseCount = 0;
      for (;;) {
        const entries: FeatureCatalogItem[] = [];
        const identities = new Set<string>();
        const cursors = new Set<string>();
        let cursor: string | undefined;
        let expectedContext: CatalogContextIdentity | undefined;
        let restart = false;

        for (;;) {
          let response: FeatureCatalogBrowseResponse;
          try {
            response = await client.browseFeatureCatalog({
              project,
              explorerId,
              authResourcePath,
              snapshotToken,
              outputId,
              section,
              ...(resourceType ? { resourceType } : {}),
              ...(section === 'FIELDS' && routeNodeId ? { nodeId: routeNodeId } : {}),
              query: searchQuery,
              ...(cursor ? { cursor } : {}),
              limit: CATALOG_BATCH_SIZE,
              requestId: `feature-catalog-${window.crypto.randomUUID()}`,
            }, controller.signal);
          } catch (error: unknown) {
            if (
              cursor &&
              !staleCursorRestarted &&
              error instanceof LoomRequestError &&
              error.status === 409 &&
              error.code === 'STALE_CATALOG_CURSOR'
            ) {
              staleCursorRestarted = true;
              restart = true;
              break;
            }
            throw error;
          }

          if (response.section !== section) {
            throw new Error(`Loom returned ${response.section} results for the ${section} section.`);
          }
          if (response.state === 'running') {
            await waitForCatalogPoll(controller.signal, catalogPollDelayMs(runningResponseCount));
            runningResponseCount += 1;
            restart = true;
            break;
          }
          if (response.state !== 'complete') {
            throw new Error(`Loom reported that the feature catalog is ${response.state}. Retry the catalog load.`);
          }
          if (response.sourceAvailability !== 'verified') {
            throw new Error('Loom could not verify all source data for this catalog. Retry the catalog load.');
          }
          for (const entry of response.entries) {
            if (
              section === 'NEEDS_REVIEW' ||
              !populatedCoverage(entry) ||
              !catalogItemAvailability(entry, section, 'complete').selectable
            ) {
              continue;
            }
            const choice = catalogItemConstructionChoice(entry);
            if (!choice || catalogItemDefaultForm(choice) === undefined) {
              throw new Error('Loom returned an available column without its compiler default. Retry the catalog load.');
            }
          }

          const responseContext = {
            contextToken: response.contextToken,
            buildId: response.buildId,
          };
          if (
            expectedContext &&
            (expectedContext.contextToken !== responseContext.contextToken ||
              expectedContext.buildId !== responseContext.buildId)
          ) {
            throw new Error('The feature catalog changed while Loom was loading it. Search again.');
          }
          expectedContext = responseContext;
          for (const entry of response.entries) {
            const identity = catalogItemKey(entry);
            if (identities.has(identity)) {
              throw new Error(`Loom returned duplicate feature ${identity}.`);
            }
            identities.add(identity);
            entries.push(entry);
          }
          if (!response.nextCursor) {
            return {
              contextToken: response.contextToken,
              buildId: response.buildId,
              state: 'complete',
              sourceAvailability: 'verified',
              section: response.section,
              entries,
            };
          }
          if (cursors.has(response.nextCursor)) {
            throw new Error('The feature catalog cursor did not advance.');
          }
          cursors.add(response.nextCursor);
          cursor = response.nextCursor;
        }

        if (restart) continue;
      }
    };

    void collectSection()
      .then((response) => {
        if (controller.signal.aborted) return;
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
        setSections((current) => ({
          ...current,
          [section]: {
            state: 'ready',
            response,
          },
        }));
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        const message = error instanceof Error
          ? error.message
          : 'Loom could not load the feature catalog.';
        for (const activeController of Object.values(activeRequests.current)) {
          activeController?.abort();
        }
        setSections({
          FIELDS: { state: 'error', message },
          CONCEPTS: { state: 'error', message },
          NEEDS_REVIEW: { state: 'error', message },
        });
      });
  }, [
    authResourcePath,
    client,
    explorerId,
    outputId,
    project,
    resourceType,
    routeNodeId,
    rowRoot,
    snapshotToken,
  ]);

  const loadSections = useCallback((searchQuery: string) => {
    setActiveCatalogRequestKey(JSON.stringify([catalogContextKey, filterKey, searchQuery]));
    for (const section of CATALOG_SECTIONS) {
      loadSection(section, searchQuery);
    }
  }, [catalogContextKey, filterKey, loadSection]);

  useEffect(() => {
    setSelected(new Map());
    setPendingSelection(undefined);
    setActionMessage(undefined);
    sectionContexts.current = {};
  }, [catalogContextKey, filterKey]);

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
    (section) => sections[section].state === 'loading',
  );
  const allSectionsComplete = CATALOG_SECTIONS.every(
    (section) => sections[section].state === 'ready',
  );
  const activeCatalogResponse = activeCatalogRequestKey === currentCatalogRequestKey;
  const catalogComplete = activeCatalogResponse && allSectionsComplete;
  const catalogError = activeCatalogResponse ? CATALOG_SECTIONS
    .map((section) => sections[section])
    .find((sectionState) => sectionState.state === 'error') : undefined;
  const catalogErrorMessage = catalogError?.state === 'error'
    ? catalogError.message
    : undefined;
  const catalogPreparing = !catalogComplete && catalogErrorMessage === undefined;
  const availableItemsBySection = useMemo<Record<CatalogBrowseSection, ReadonlyArray<CatalogItem>>>(() => {
    const fields = sections.FIELDS;
    const concepts = sections.CONCEPTS;
    return {
      FIELDS: fields.state === 'ready'
        ? fields.response.entries.filter((item) => availableCatalogItem(item, 'FIELDS'))
        : [],
      CONCEPTS: concepts.state === 'ready'
        ? concepts.response.entries.filter((item) => availableCatalogItem(item, 'CONCEPTS'))
        : [],
    };
  }, [sections]);
  const suggestedPivotFamilies = useMemo(
    () => pivotFamilies(availableItemsBySection.CONCEPTS, configuredPivot),
    [availableItemsBySection.CONCEPTS, configuredPivot],
  );
  const configuredPivotFeatureIds = useMemo(
    () => new Set(suggestedPivotFamilies.flatMap((family) => family.codes.filter((code) => code.configured).map((code) => code.item.featureId))),
    [suggestedPivotFamilies],
  );
  const selectedFeatureIds = useMemo(
    () => new Set([...selected.values()].map(({ item }) => item.featureId)),
    [selected],
  );
  const groupedPivotFamilies = useMemo(
    () => onAddSelected && onAnalyzePivot
      ? suggestedPivotFamilies.filter((family) => family.codes.length > 1 && family.codes.every((code) => !selectedFeatureIds.has(code.item.featureId)))
      : [],
    [onAddSelected, onAnalyzePivot, selectedFeatureIds, suggestedPivotFamilies],
  );
  const groupedPivotFeatureIds = useMemo(
    () => new Set(groupedPivotFamilies.flatMap((family) => family.codes.map((code) => code.item.featureId))),
    [groupedPivotFamilies],
  );
  const selectableCatalogEntries = useMemo<ReadonlyArray<SelectableCatalogEntry>>(
    () => disabled || !catalogComplete
      ? []
      : CATALOG_SECTIONS.flatMap((section) => {
          const sectionState = sections[section];
          if (sectionState.state !== 'ready') return [];
          const { response } = sectionState;
          return response.entries.flatMap((item): ReadonlyArray<SelectableCatalogEntry> => {
            if (!availableCatalogItem(item, section)) return [];
            if (section === 'CONCEPTS' && groupedPivotFeatureIds.has(item.featureId)) return [];
            if (section === 'CONCEPTS' && configuredPivotFeatureIds.has(item.featureId)) return [];
            return [{
              item,
              section,
              contextToken: response.contextToken,
              buildId: response.buildId,
              key: catalogItemKey(item),
            }];
          });
        }),
    [catalogComplete, configuredPivotFeatureIds, disabled, groupedPivotFeatureIds, sections],
  );
  const selectedLoadedCount = selectableCatalogEntries.reduce(
    (count, entry) => count + (selected.has(entry.key) ? 1 : 0),
    0,
  );
  const allSelectableEntriesSelected = selectableCatalogEntries.length > 0 &&
    selectableCatalogEntries.every((entry) => selected.has(entry.key));
  const catalogSelectionState: CatalogSelectionState = allSelectableEntriesSelected
    ? 'all'
    : selectedLoadedCount > 0
      ? 'partial'
      : 'none';
  const selectAllRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = catalogSelectionState === 'partial';
    }
  }, [catalogSelectionState]);

  const toggleSelection = (
    item: CatalogItem,
    section: FeatureCatalogSection,
    response: LoadedCatalogSection,
  ) => {
    if (!availableCatalogItem(item, section)) return;
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

  const toggleSelectAll = () => {
    if (!selectableCatalogEntries.length) return;
    if (catalogSelectionState === 'all') {
      setSelected((current) => {
        const next = new Map(current);
        for (const entry of selectableCatalogEntries) next.delete(entry.key);
        return next;
      });
      setActionMessage(undefined);
      return;
    }

    const missingEntries = selectableCatalogEntries.filter((entry) => !selected.has(entry.key));
    const remainingCapacity = Math.max(0, MAX_SELECTIONS - selected.size);
    const entriesToAdd = missingEntries.slice(0, remainingCapacity);
    setSelected((current) => {
      const next = new Map(current);
      for (const entry of entriesToAdd) {
        next.set(entry.key, {
          item: entry.item,
          section: entry.section,
          contextToken: entry.contextToken,
          buildId: entry.buildId,
        });
      }
      return next;
    });
    setActionMessage(missingEntries.length > entriesToAdd.length
      ? `The ${MAX_SELECTIONS}-column selection limit was reached. Select fewer columns before adding more.`
      : undefined);
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

  const selectedChoiceGroups = (): ReadonlyArray<CatalogChoiceGroup> =>
    selectedItems.map(({ item }) => {
      const choice = catalogItemConstructionChoice(item);
      if (!choice || catalogItemDefaultForm(choice) === undefined) {
        throw new Error(`${catalogItemLabel(item)} no longer has a verified compiler choice. Reload the catalog and try again.`);
      }
      return {
        item,
        choices: [choice],
        complete: true,
        truncated: false,
        requiresSourceChoice: false,
        ...(item.coverage.state === 'INDEXED'
          ? { rowsWithValue: item.coverage.rowsWithValue }
          : {}),
      };
    });

  const openSelection = async () => {
    if (!selectedItems.length || !onAddSelected) return;
    setAdding(true);
    setActionMessage(undefined);
    try {
      const selections = selectedChoiceGroups().map(({ item, choices }) => {
        const [choice] = choices;
        const form = choice ? catalogItemDefaultForm(choice) : undefined;
        if (!choice || !form) {
          throw new Error(`${catalogItemLabel(item)} has no unique compiler default. Reload the catalog and try again.`);
        }
        return {
          constructionChoice: { choiceId: choice.choiceId, form },
          title: catalogItemLabel(item),
        };
      });
      await onAddSelected(selections);
      setSelected(new Map());
      setActionMessage(
        `${selections.length} ${selections.length === 1 ? 'feature was' : 'features were'} added to your table.`,
      );
    } catch (error) {
      setActionMessage(error instanceof Error ? error.message : 'Loom could not add the selected features.');
    } finally {
      setAdding(false);
    }
  };

  const openChoiceCustomization = () => {
    if (!selectedItems.length) return;
    setActionMessage(undefined);
    try {
      setPendingSelection(selectedChoiceGroups());
    } catch (error) {
      setActionMessage(error instanceof Error ? error.message : 'Loom could not load the selected feature choices.');
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
    section: CatalogBrowseSection,
    title: string,
    description: string,
  ) => {
    const sectionState = sections[section];
    if (!catalogComplete || sectionState.state !== 'ready') return null;
    const response = sectionState.response;
    const items = availableItemsBySection[section];
    const familyByFeatureId = new Map<string, PivotFamily>();
    for (const family of groupedPivotFamilies) {
      for (const code of family.codes) familyByFeatureId.set(code.item.featureId, family);
    }
    const renderedFamilyIds = new Set<string>();
    const rows = items.flatMap((item): ReadonlyArray<CatalogResultRow> => {
      if (section !== 'CONCEPTS') return [{ kind: 'item', item }];
      const family = familyByFeatureId.get(item.featureId);
      if (!family) return [{ kind: 'item', item }];
      if (renderedFamilyIds.has(family.id)) return [];
      renderedFamilyIds.add(family.id);
      return [{ kind: 'pivotFamily', family }];
    });
    return (
      <section
        key={section}
        className="min-w-0"
        aria-labelledby={`feature-catalog-${section.toLowerCase()}-title`}
      >
        <div className="mb-2 flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h3
              id={`feature-catalog-${section.toLowerCase()}-title`}
              className="truncate text-sm font-semibold text-slate-800"
            >{title}</h3>
            <p className="truncate text-xs text-slate-600">{description}</p>
          </div>
          <span className="shrink-0 text-xs text-slate-500">{items.length.toLocaleString()} available</span>
        </div>
        <div className="space-y-1.5">
          {items.length === 0 ? (
            <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-center text-sm text-slate-600">
              No available {title.toLowerCase()} match this search.
            </p>
          ) : null}
          {rows.map((row) => {
            if (row.kind === 'pivotFamily') {
              if (!onAnalyzePivot || !onAddSelected) return null;
              return (
                <PivotFamilyCatalogRow
                  key={`${row.family.id}:${row.family.codes.map((code) => code.item.featureId).join('|')}:${row.family.codes.filter((code) => selectedFeatureIds.has(code.item.featureId)).map((code) => code.item.featureId).join('|')}`}
                  family={row.family}
                  disabled={disabled || adding}
                  queuedFeatureIds={selectedFeatureIds}
                  onAnalyzeSelected={onAnalyzePivot}
                  onAddSelected={onAddSelected}
                />
              );
            }
            const { item } = row;
            const key = catalogItemKey(item);
            return (
              <CatalogItemRow
                key={key}
                item={item}
                section={section}
                catalogState={response.state}
                checked={selected.has(key)}
                alreadyAdded={section === 'CONCEPTS' && configuredPivotFeatureIds.has(item.featureId)}
                disabled={
                  disabled ||
                  (selected.size >= MAX_SELECTIONS && !selected.has(key))
                }
                onToggle={() => toggleSelection(item, section, response)}
              />
            );
          })}
        </div>
      </section>
    );
  };

  const renderCodeSetSection = () => {
    const sectionState = sections.CONCEPTS;
    if (!catalogComplete || sectionState.state !== 'ready') return null;
    const response = sectionState.response;
    return (
      <section className="min-w-0" aria-labelledby="feature-catalog-code-sets-title">
        <div className="mb-2">
          <h3 id="feature-catalog-code-sets-title" className="text-sm font-semibold text-slate-800">
            Code sets for the current table rows
          </h3>
          <p className="text-xs text-slate-600">
            These code values were observed for this table. Multi-code coverage is checked on the preview sample before adding columns.
          </p>
        </div>
        <div className="space-y-1.5">
          {suggestedPivotFamilies.length === 0 ? (
            <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-6 text-center text-sm text-slate-600">
              No observed code sets match this search.
            </p>
          ) : null}
          {suggestedPivotFamilies.map((family) => {
            if (family.codes.length > 1 && onAnalyzePivot && onAddSelected) {
              return (
                <PivotFamilyCatalogRow
                  key={`${family.id}:${family.codes.map((code) => code.item.featureId).join('|')}:${family.codes.filter((code) => selectedFeatureIds.has(code.item.featureId)).map((code) => code.item.featureId).join('|')}`}
                  family={family}
                  disabled={disabled || adding}
                  queuedFeatureIds={selectedFeatureIds}
                  onAnalyzeSelected={onAnalyzePivot}
                  onAddSelected={onAddSelected}
                />
              );
            }
            return family.codes.map(({ item }) => {
              const key = catalogItemKey(item);
              return (
                <CatalogItemRow
                  key={key}
                  item={item}
                  section="CONCEPTS"
                  catalogState={response.state}
                  checked={selected.has(key)}
                  alreadyAdded={configuredPivotFeatureIds.has(item.featureId)}
                  disabled={disabled || adding || (selected.size >= MAX_SELECTIONS && !selected.has(key))}
                  onToggle={() => toggleSelection(item, 'CONCEPTS', response)}
                />
              );
            });
          })}
        </div>
      </section>
    );
  };

  return (
    <section className="min-w-0 rounded-xl border border-slate-200 bg-white shadow-sm">
      {pendingSelection ? (
        <CatalogSelectionDialog
          groups={pendingSelection}
          busy={adding}
          showRouteDetails={Boolean(routeContext)}
          onCancel={() => setPendingSelection(undefined)}
          onConfirm={(selections) => void commitSelections(selections)}
        />
      ) : null}
      <div className="border-b border-slate-200 px-3 py-3 sm:px-4">
        <div className="flex flex-wrap items-end justify-between gap-x-3 gap-y-1">
          <div>
            <p className="text-[10px] font-semibold uppercase tracking-wide text-blue-700">Find features</p>
            <h2 className="text-base font-semibold text-slate-950">Search fields and concepts</h2>
          </div>
          <p className="text-xs text-slate-600">
          {resourceType && routeContext
            ? `Search available fields and concepts on the selected ${resourceType} graph node.`
            : `Search available fields and concepts for ${rowRoot} rows.`}
          </p>
        </div>
        <form className="mt-2 flex gap-2" onSubmit={submitSearch}>
          <label className="sr-only" htmlFor="feature-catalog-search">Search features</label>
          <input
            id="feature-catalog-search"
            type="search"
            aria-label="Search features by field name, concept, or code"
            value={queryInput}
            onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder="Search a field, concept, or code"
            className="min-h-11 min-w-0 flex-1 rounded-md border border-slate-300 px-3 py-2 text-sm outline-blue-500 focus:border-blue-500"
          />
          <button
            type="submit"
            disabled={disabled || anySectionLoading || catalogPreparing}
            className="min-h-11 rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Search
          </button>
        </form>
        {catalogComplete ? (
          <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
            <div className="inline-flex rounded-lg border border-slate-200 bg-slate-50 p-1" role="tablist" aria-label="Feature type">
              {CATALOG_TABS.map((section) => {
                const count = section === 'ALL'
                  ? availableItemsBySection.FIELDS.length + availableItemsBySection.CONCEPTS.length
                  : section === 'CODE_SETS'
                    ? suggestedPivotFamilies.length
                    : availableItemsBySection[section].length;
                const tabSlug = catalogTabSlug(section);
                return (
                <button
                  key={section}
                  type="button"
                  role="tab"
                  id={`feature-catalog-${tabSlug}-tab`}
                  aria-controls={`feature-catalog-${tabSlug}`}
                  aria-selected={activeSection === section}
                  onClick={() => setActiveSection(section)}
                  className={`min-h-9 rounded-md px-3 text-xs font-semibold transition-colors ${activeSection === section ? 'bg-white text-blue-800 shadow-sm' : 'text-slate-600 hover:text-slate-900'}`}
                >
                  {CATALOG_TAB_LABELS[section]}
                  <span className="ml-1.5 text-[10px] font-medium text-slate-500">{count.toLocaleString()}</span>
                </button>
                );
              })}
            </div>
            <label className="flex min-h-11 items-center gap-2 rounded-lg px-2 text-xs font-semibold text-slate-800 hover:bg-slate-50">
              <input
                ref={selectAllRef}
                type="checkbox"
                aria-label="Select all individual columns"
                aria-checked={catalogSelectionState === 'partial' ? 'mixed' : catalogSelectionState === 'all'}
                checked={catalogSelectionState === 'all'}
                disabled={disabled || selectableCatalogEntries.length === 0}
                onChange={toggleSelectAll}
                className="h-5 w-5 rounded border-slate-300 text-blue-700"
              />
              Select all {selectableCatalogEntries.length.toLocaleString()} individual {selectableCatalogEntries.length === 1 ? 'column' : 'columns'}
            </label>
            {groupedPivotFamilies.length > 0 ? (
              <span className="text-[11px] text-slate-500">Choose code values inside each code set.</span>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className={layout === 'panel'
        ? 'grid min-h-[27rem]'
        : 'grid min-h-[27rem] lg:h-[min(58vh,36rem)] lg:min-h-[25rem] lg:grid-cols-[minmax(0,1fr)_minmax(16rem,0.42fr)]'}>
        <div className={layout === 'panel'
          ? 'min-w-0 border-b border-slate-200 p-3'
          : 'min-w-0 border-b border-slate-200 p-3 lg:flex lg:min-h-0 lg:flex-col lg:border-b-0 lg:border-r'}>
          <div className="max-h-[45vh] min-h-0 space-y-2 overflow-y-auto overscroll-contain lg:max-h-none lg:flex-1">
            {catalogPreparing ? (
              <p className="rounded-md bg-slate-50 px-3 py-4 text-center text-sm text-slate-600" role="status">
                Preparing available columns…
              </p>
            ) : catalogErrorMessage ? (
              <div className="rounded-md border border-red-300 bg-red-50 px-3 py-3 text-sm text-red-900">
                <p role="alert">{catalogErrorMessage}</p>
                <button
                  type="button"
                  disabled={disabled || anySectionLoading}
                  onClick={() => loadSections(appliedQuery.current)}
                  className="mt-2 rounded-md border border-red-400 px-3 py-1.5 font-semibold hover:bg-red-100 disabled:opacity-50"
                >
                  Retry catalog load
                </button>
              </div>
            ) : (
              <>
                <div
                  id={`feature-catalog-${catalogTabSlug(activeSection)}`}
                  role="tabpanel"
                  aria-labelledby={`feature-catalog-${catalogTabSlug(activeSection)}-tab`}
                >
                  {activeSection === 'CODE_SETS' ? renderCodeSetSection() : null}
                  {activeSection === 'ALL' || activeSection === 'FIELDS'
                    ? renderCatalogSection(
                        'FIELDS',
                        resourceType ? `Fields on ${resourceType}` : `Fields on ${rowRoot}`,
                        'Primitive fields with values on at least one selected row.',
                      ) : null}
                  {activeSection === 'ALL' || activeSection === 'CONCEPTS'
                    ? renderCatalogSection(
                        'CONCEPTS',
                        query
                          ? `Concepts matching “${query}”`
                          : routeContext && resourceType
                            ? `Concepts on ${resourceType}`
                            : 'Concepts across the dataset',
                        'Coded concepts with values on at least one selected row.',
                      ) : null}
                </div>
              </>
            )}
          </div>
        </div>

        <aside className="flex min-h-0 flex-col bg-slate-50/70 p-3">
          <div className="flex items-center justify-between gap-2">
            <h3 className="font-semibold text-slate-900">Selected features</h3>
            <span className="rounded-full bg-blue-100 px-2.5 py-1 text-xs font-semibold text-blue-800"><span>{selected.size}</span> / {MAX_SELECTIONS}</span>
          </div>
          <p className="mt-0.5 text-[11px] text-slate-600">Selections stay here when you switch sections or search.</p>
          <div className="mt-2 max-h-56 min-h-0 space-y-1 overflow-y-auto overscroll-contain lg:flex-1">
            {selectedItems.length === 0 ? (
              <div className="rounded-lg border border-dashed border-slate-300 bg-white px-3 py-6 text-center text-xs text-slate-500">
                Select fields or concepts to build your feature list.
              </div>
            ) : selectedItems.map(({ item }) => {
              const key = catalogItemKey(item);
              return (
                <div key={key} className="flex items-start gap-2 rounded-md border border-slate-200 bg-white px-2 py-1.5">
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
                    className="min-h-8 min-w-8 rounded text-lg leading-5 text-slate-500 hover:bg-slate-100 hover:text-slate-900"
                  >
                    ×
                  </button>
                </div>
              );
            })}
          </div>
          <div className="mt-2 border-t border-slate-200 pt-2">
          {!pendingSelection ? (
            <button
              type="button"
              disabled={disabled || adding || selected.size === 0 || !onAddSelected}
              onClick={() => void openSelection()}
              className="min-h-11 w-full rounded-md bg-blue-700 px-4 py-2.5 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {selected.size === 0
                ? 'Add selected features'
                : `Add ${selected.size} selected ${selected.size === 1 ? 'feature' : 'features'}`}
            </button>
          ) : null}
          {!pendingSelection && selectedItems.length > 0 ? (
            <button
              type="button"
              disabled={disabled || adding || !onAddSelected}
              onClick={openChoiceCustomization}
              className="mt-1 min-h-10 w-full rounded-md border border-slate-300 bg-white px-4 py-2 text-sm font-semibold text-slate-700 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Advanced choices
            </button>
          ) : null}
          {actionMessage ? (
            <p className="mt-3 text-sm text-slate-700" role="status">{actionMessage}</p>
          ) : null}
          </div>
        </aside>
      </div>
    </section>
  );
};
