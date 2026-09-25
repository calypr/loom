import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useLoomClient } from '../../../react';
import type {
  ConstructionChoiceOption,
  ConstructionChoiceSearchSource,
  ExplorerBuilderCatalog,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import {
  catalogItemAvailability,
  catalogItemKey,
  catalogItemLabel,
  catalogItemConstructionChoice,
  catalogItemDefaultForm,
  fieldCatalogItems,
  semanticCatalogItems,
  type CatalogChoiceGroup,
  type CatalogChoiceIntent,
  type CatalogItem,
} from '../catalogItems';
import { CatalogSelectionDialog } from './CatalogSelectionDialog';

const PAGE_SIZE = 50;
const MAX_SELECTIONS = 100;

export interface CatalogRouteContext {
  readonly occurrenceId: string;
  readonly nodeId: string;
}

export interface CatalogSourceProjectionAvailability {
  readonly available: boolean;
  readonly reason: string;
}

type CatalogPage = {
  readonly cursor?: string;
  readonly response: SemanticInventoryBrowseResponse;
};

type CatalogLoadState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready' }
  | { readonly status: 'error'; readonly message: string };

type ChoiceDetailsState =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly group: CatalogChoiceGroup }
  | { readonly status: 'error'; readonly message: string };

const semanticLabel = (item: SemanticInventoryItem): string =>
  item.display.trim() || item.code.trim() || item.sourcePath || item.resourceType;

const semanticCodeLabel = (item: SemanticInventoryItem): string =>
  [item.system, item.code, item.codingVersion].filter(Boolean).join(' · ') ||
  'Structural binding';

const readinessPresentation = (readiness: SemanticInventoryItem['readiness']) => {
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

const availabilityMessage = (
  response: SemanticInventoryBrowseResponse,
): string | undefined => {
  if (response.state !== 'complete') {
    return response.state === 'unknown' || response.state === 'not_started'
      ? 'Loom has not built the concept inventory for this dataset yet.'
      : `The concept inventory is ${response.state}. Semantic results cannot be added until it is complete.`;
  }
  if (response.sourceAvailability === 'unproven') {
    return 'Loom found concepts, but some declared resource collections could not be verified. Semantic search results may be incomplete.';
  }
  if (response.sourceAvailability === 'unknown') {
    return 'Loom cannot verify whether this inventory covers every retained source resource.';
  }
  return undefined;
};

const selectedAsArray = (selected: ReadonlyMap<string, CatalogItem>) =>
  [...selected.values()];

const sourceDetails = (item: CatalogItem): ReadonlyArray<readonly [string, string]> => {
  const choice = catalogItemConstructionChoice(item);
  if (choice) {
    const facts: ReadonlyArray<readonly [string, string]> = choice.presentation.facts
      .map((fact): readonly [string, string] => [fact.label, fact.value]);
    return [
      ['Meaning', choice.presentation.summary],
      ...facts,
    ];
  }
  return item.kind === 'SEMANTIC'
    ? [['Availability', item.item.readiness.message]]
    : [];
};

const constructionFormLabel = (
  option: ConstructionChoiceOption,
): string => {
  switch (option.form) {
    case 'VALUE':
      return 'One value';
    case 'FIRST':
      return 'First value';
    case 'ALL':
      return 'All values';
    case 'DISTINCT':
      return 'Distinct values';
    case 'OWNER_RECORDS':
      return 'Matching records';
    default: {
      const exhaustive: never = option.form;
      return exhaustive;
    }
  }
};

const constructionRouteLabel = (
  choice: NonNullable<ReturnType<typeof catalogItemConstructionChoice>>,
  rowRoot: string,
  currentResourceType?: string,
): string => {
  if (choice.route.length === 0) {
    const contextResourceType = currentResourceType ?? rowRoot;
    if (choice.source.resourceType === contextResourceType) {
      return currentResourceType
        ? `No extra route steps from the selected ${contextResourceType} occurrence`
        : `Server choice uses the ${rowRoot} root resource; no route steps`;
    }
    return `No route steps were returned from ${choice.source.resourceType} to the current ${contextResourceType} context`;
  }
  return choice.route
    .map((step) => `${step.fromResourceType} → ${step.toResourceType} via ${step.relationship} (${step.matchMode.toLowerCase()}, ${step.storageDirection.toLowerCase()})`)
    .join(' · ');
};

const ChoiceDetails = ({
  group,
  rowRoot,
  currentResourceType,
}: {
  readonly group: CatalogChoiceGroup;
  readonly rowRoot: string;
  readonly currentResourceType?: string;
}) => (
  <div className="mt-2 space-y-2" aria-label={`Construction choices for ${catalogItemLabel(group.item)}`}>
    {group.truncated ? (
      <p className="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
        The server returned valid choices but reports that more routes may exist.
      </p>
    ) : null}
    {group.choices.length === 0 ? (
      <p className="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
        {group.truncated
          ? 'The first page has no construction choice. More routes may exist.'
          : 'No supported construction was returned for these table rows.'}
      </p>
    ) : group.choices.map((choice) => (
      <article key={choice.choiceId} className="rounded-md border border-blue-100 bg-blue-50/60 p-2">
        <p className="font-semibold text-slate-800">{choice.presentation.summary}</p>
        <p className="mt-1 text-slate-600">{constructionRouteLabel(choice, rowRoot, currentResourceType)}</p>
        <ul className="mt-2 space-y-1">
          {choice.options.map((option) => (
            <li key={option.form} className="rounded bg-white px-2 py-1.5">
              <span className="font-semibold text-slate-800">
                {constructionFormLabel(option)} · {option.shape.toLowerCase()} · {option.preservation.toLowerCase()}
              </span>
              <span className="ml-1 text-slate-600">{option.reason}</span>
              <span className="block text-[10px] uppercase tracking-wide text-slate-500">
                {option.decision === 'DEFAULT' ? 'Server default' : 'Requires a choice'} · preserves row grain
              </span>
            </li>
          ))}
        </ul>
        {choice.presentation.facts.length ? (
          <dl className="mt-2 grid gap-1 font-mono">
            {choice.presentation.facts.map((fact) => (
              <div key={`${fact.label}:${fact.value}`}>
                <dt className="inline text-slate-500">{fact.label} </dt>
                <dd className="inline break-all">{fact.value}</dd>
              </div>
            ))}
          </dl>
        ) : null}
      </article>
    ))}
  </div>
);

const CatalogItemRow = ({
  item,
  rowRoot,
  currentResourceType,
  hasRouteContext,
  checked,
  disabled,
  selectionDisabled,
  disabledReasonId,
  onToggle,
  choiceDetails,
  onInspectChoices,
}: {
  readonly item: CatalogItem;
  readonly rowRoot: string;
  readonly currentResourceType?: string;
  readonly hasRouteContext: boolean;
  readonly checked: boolean;
  readonly disabled: boolean;
  readonly selectionDisabled: boolean;
  readonly disabledReasonId?: string;
  readonly onToggle: () => void;
  readonly choiceDetails?:
    | { readonly status: 'loading' }
    | { readonly status: 'ready'; readonly group: CatalogChoiceGroup }
    | { readonly status: 'error'; readonly message: string };
  readonly onInspectChoices: () => void;
}) => {
  const label = catalogItemLabel(item);
  const selectionLabel = item.kind === 'FIELD'
    ? `Select ${item.constructionChoice.source.resourceType}.${item.candidate.fieldPath.replace(/^root\./, '')}`
    : `Select ${label}`;
  const availability = catalogItemAvailability(item);
  const details = sourceDetails(item);
  const choice = catalogItemConstructionChoice(item);
  const choicesNeedTableContext = Boolean(
    choice && (
      hasRouteContext ||
      choice.source.resourceType !== rowRoot ||
      choice.route.length > 0
    ),
  );
  const fieldConcepts = item.kind === 'FIELD'
    ? item.candidate.conceptCandidates ?? []
    : [];
  return (
    <article className="py-3 first:pt-0">
      <div className="flex items-start gap-3">
        <input
          type="checkbox"
          aria-label={selectionLabel}
          aria-describedby={disabled && disabledReasonId ? disabledReasonId : undefined}
          checked={checked}
          disabled={disabled || selectionDisabled || !availability.selectable}
          onChange={onToggle}
          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-700"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <h3 className="font-semibold text-slate-900">{label}</h3>
              {item.kind === 'FIELD' ? (
                <p className="break-all font-mono text-xs text-slate-600">
                  {item.candidate.fieldPath} · {item.candidate.logicalType} · {item.constructionChoice.source.cardinality}
                </p>
              ) : (
                <p className="break-all font-mono text-xs text-slate-600">{semanticCodeLabel(item.item)}</p>
              )}
            </div>
            <div className="flex flex-wrap justify-end gap-1">
              {item.kind === 'FIELD' ? (
                <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] font-semibold text-slate-700">
                  {item.constructionChoice.source.resourceType}
                </span>
              ) : null}
              <span className="rounded-full bg-blue-50 px-2 py-1 text-[11px] font-semibold text-blue-800">
                {item.kind === 'FIELD' ? 'Field' : 'Concept'}
              </span>
              {item.kind === 'FIELD' ? (
                <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] font-semibold text-slate-700">
                  {item.constructionChoice.options.length} compiler-proved {item.constructionChoice.options.length === 1 ? 'form' : 'forms'}
                </span>
              ) : (
                <>
                  <span className={`rounded-full px-2 py-1 text-[11px] font-semibold ${readinessPresentation(item.item.readiness).badge}`}>
                    {readinessPresentation(item.item.readiness).label}
                  </span>
                  <span className="rounded-full bg-slate-100 px-2 py-1 text-[11px] font-semibold text-slate-700">
                    {item.item.occurrences.toLocaleString()} observed source {item.item.occurrences === 1 ? 'occurrence' : 'occurrences'}
                  </span>
                </>
              )}
            </div>
          </div>
          {item.kind === 'SEMANTIC' ? (
            <p className="mt-1 text-xs text-slate-600">
              {item.item.display || 'Observed coded value'} · {item.item.valueType || 'value type not provided'} from {[item.item.resourceType, item.item.sourcePath].filter(Boolean).join('.') || 'source path not provided'}
            </p>
          ) : fieldConcepts.length > 0 ? (
            <p className="mt-1 text-xs text-slate-600">
              Observed concept evidence is available for {fieldConcepts.length} {fieldConcepts.length === 1 ? 'code' : 'codes'} in this field.
            </p>
          ) : null}
          {choice && !choicesNeedTableContext ? (
            <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-600">
              <span>{choice.presentation.summary}</span>
              <span aria-hidden="true">·</span>
              <span>Server-supported forms: {choice.options.map(constructionFormLabel).join(', ')}</span>
            </div>
          ) : choicesNeedTableContext ? (
            <p className="mt-1 text-xs text-slate-600">
              Table-specific routes and output forms need to be resolved before adding this source.
            </p>
          ) : null}
          {item.kind === 'SEMANTIC' && !availability.selectable ? (
            <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
              {availability.reason}
            </p>
          ) : null}
          <details className="mt-2 text-xs text-slate-600">
            <summary className="cursor-pointer font-medium text-blue-700">Inspect meaning, evidence, and construction choices</summary>
            <dl className="mt-2 grid gap-1 rounded-md bg-slate-50 p-2 font-mono">
              {details.map(([name, value]) => (
                <div key={name}>
                  <dt className="inline text-slate-500">{name} </dt>
                  <dd className="inline break-all">{value}</dd>
                </div>
              ))}
              {item.kind === 'SEMANTIC' && item.item.readiness.status !== 'READY' ? (
                <div>
                  <dt className="inline text-slate-500">Readiness </dt>
                  <dd className="inline break-all">{item.item.readiness.message}</dd>
                </div>
              ) : null}
            </dl>
            {item.kind === 'SEMANTIC' ? (
              <div className="mt-2 rounded-md border border-slate-200 bg-white p-2">
                <p className="font-semibold text-slate-800">Observed source evidence</p>
                <p className="mt-1">{item.item.occurrences.toLocaleString()} source occurrences were observed for this code. The inventory response does not provide a denominator or coverage of the current table rows.</p>
                <p className="mt-1">Source scope: {item.item.resourceType} · {item.item.owningScope || 'scope not specified'}.</p>
                <p>Completeness: {item.item.completeness ?? 'not reported'}.</p>
                {item.item.observedUnits?.length ? (
                  <p className="break-all">
                    Observed units: {item.item.observedUnits.join(', ')}{item.item.observedUnitsTruncated ? ' · additional units exist' : ''}
                  </p>
                ) : <p>Observed units were not reported for this code.</p>}
                {item.item.examples?.length ? (
                  <p className="break-all">
                    Observed examples: {item.item.examples.join(', ')}{item.item.examplesTruncated ? ' · additional examples exist' : ''}
                  </p>
                ) : item.item.examplesTruncated ? (
                  <p>Examples were omitted from this response; additional examples exist.</p>
                ) : <p>Observed examples are not available for this code.</p>}
              </div>
            ) : fieldConcepts.length > 0 ? (
              <div className="mt-2 space-y-2 rounded-md border border-slate-200 bg-white p-2">
                <p className="font-semibold text-slate-800">Observed codes in this field</p>
                {fieldConcepts.map((concept, index) => (
                  <article key={`${concept.sourceResourceType}:${concept.sourcePath ?? ''}:${concept.system ?? ''}:${concept.code ?? ''}:${index}`} className="border-t border-slate-100 pt-2 first:border-t-0 first:pt-0">
                    <p className="font-medium text-slate-800">
                      {[concept.display, concept.system, concept.code].filter(Boolean).join(' · ') || 'Observed code without display metadata'}
                    </p>
                    <p>Scope: {concept.sourceResourceType}{concept.owningScope ? ` · ${concept.owningScope}` : ''} · {concept.completeness.toLowerCase()} evidence</p>
                    <p>{concept.population.toLocaleString()} observed source {concept.population === 1 ? 'occurrence' : 'occurrences'}; no denominator or current-table coverage is provided.</p>
                    {concept.observedUnits?.length ? (
                      <p>Observed units: {concept.observedUnits.join(', ')}{concept.observedUnitsTruncated ? ' · additional units exist' : ''}</p>
                    ) : <p>Observed units were not reported for this code.</p>}
                    {concept.examples?.length ? (
                      <p>Observed examples: {concept.examples.join(', ')}{concept.examplesTruncated ? ' · more examples exist' : ''}</p>
                    ) : <p>Observed examples are not available for this code.</p>}
                  </article>
                ))}
              </div>
            ) : null}
            {choicesNeedTableContext ? (
              <div className="mt-2">
                <button
                  type="button"
                  className="rounded border border-blue-200 bg-white px-2 py-1 font-semibold text-blue-800 hover:bg-blue-50 disabled:opacity-50"
                  disabled={disabled || choiceDetails?.status === 'loading'}
                  onClick={onInspectChoices}
                >
                  {choiceDetails?.status === 'loading' ? 'Loading table-specific choices…' : 'Load choices for these table rows'}
                </button>
                {choiceDetails?.status === 'error' ? (
                  <p className="mt-2 text-red-800" role="alert">{choiceDetails.message}</p>
                ) : null}
                {choiceDetails?.status === 'ready' ? (
                  <ChoiceDetails
                    group={choiceDetails.group}
                    rowRoot={rowRoot}
                    currentResourceType={currentResourceType}
                  />
                ) : null}
              </div>
            ) : choice ? (
              <ChoiceDetails
                group={{ item, choices: [choice], complete: true, truncated: false }}
                rowRoot={rowRoot}
                currentResourceType={currentResourceType}
              />
            ) : (
              <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
                No server-supported construction was included for this result.
              </p>
            )}
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
  catalog,
  disabled = false,
  disabledReason,
  sourceProjectionAvailability,
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
  readonly catalog: ExplorerBuilderCatalog;
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly sourceProjectionAvailability?: CatalogSourceProjectionAvailability;
  readonly onAddSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => Promise<void>;
}) => {
  const client = useLoomClient();
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [pages, setPages] = useState<ReadonlyArray<CatalogPage>>([]);
  const [pageIndex, setPageIndex] = useState(0);
  const [loadState, setLoadState] = useState<CatalogLoadState>({ status: 'idle' });
  const [selected, setSelected] = useState<ReadonlyMap<string, CatalogItem>>(
    () => new Map(),
  );
  const [choiceDetails, setChoiceDetails] = useState<ReadonlyMap<string, ChoiceDetailsState>>(
    () => new Map(),
  );
  const [adding, setAdding] = useState(false);
  const [actionMessage, setActionMessage] = useState<string>();
  const [pendingSelection, setPendingSelection] = useState<ReadonlyArray<CatalogChoiceGroup>>();
  const disabledReasonId = useId();
  const visibleDisabledReasonId = disabled && disabledReason?.trim()
    ? disabledReasonId
    : undefined;
  const activeRequest = useRef<AbortController | undefined>(undefined);
  const selectionContext = useRef<string | undefined>(undefined);
  const contextKey = JSON.stringify([
    project,
    explorerId,
    snapshotToken,
    outputId,
    rowRoot,
    resourceType ?? '*',
    routeContext?.occurrenceId ?? '',
    routeContext?.nodeId ?? '',
    sourceProjectionAvailability?.available ?? true,
    sourceProjectionAvailability?.reason ?? '',
  ]);
  const canAddFromSourceProjection = sourceProjectionAvailability?.available ?? true;
  const sourceProjectionReason = sourceProjectionAvailability?.reason.trim();

  const loadPage = useCallback(
    (searchQuery: string, cursor?: string, replace = false) => {
      activeRequest.current?.abort();
      const controller = new AbortController();
      activeRequest.current = controller;
      setLoadState({ status: 'loading' });
      setActionMessage(undefined);
      void client
        .browseSemanticInventory(
          {
            project,
            explorerId,
            authResourcePath,
            snapshotToken,
            rowRoot,
            resourceType,
            query: searchQuery,
            cursor,
            limit: PAGE_SIZE,
            requestId: `feature-catalog-${window.crypto.randomUUID()}`,
          },
          controller.signal,
        )
        .then((response) => {
          if (controller.signal.aborted) return;
          if (selectionContext.current && selectionContext.current !== response.contextToken) {
            setSelected(new Map());
            setPendingSelection(undefined);
            setActionMessage('The dataset catalog changed. Review and select the features again.');
          }
          selectionContext.current = response.contextToken;
          setPages((current) =>
            replace ? [{ cursor, response }] : [...current, { cursor, response }],
          );
          setPageIndex((current) => (replace ? 0 : current + 1));
          setLoadState({ status: 'ready' });
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          setLoadState({
            status: 'error',
            message: error instanceof Error
              ? error.message
              : 'Loom could not load the feature catalog.',
          });
        });
    },
    [authResourcePath, client, explorerId, project, resourceType, rowRoot, snapshotToken],
  );

  useEffect(() => {
    setPages([]);
    setPageIndex(0);
    setSelected(new Map());
    setChoiceDetails(new Map());
    setPendingSelection(undefined);
    setActionMessage(undefined);
    selectionContext.current = undefined;
    if (snapshotToken && rowRoot) loadPage('', undefined, true);
    return () => activeRequest.current?.abort();
  }, [contextKey, loadPage, rowRoot, snapshotToken]);

  const page = pages[pageIndex];
  const response = page?.response;
  const warning = response ? availabilityMessage(response) : undefined;
  const canBrowseConcepts = response?.state === 'complete';
  const fieldItems = useMemo(
    () => fieldCatalogItems(catalog, rowRoot, resourceType, query).filter(
      (item) =>
        item.kind === 'FIELD' &&
        (!routeContext || item.candidate.nodeId === routeContext.nodeId),
    ),
    [catalog, query, resourceType, routeContext, rowRoot],
  );
  const semanticItems = useMemo(
    () => semanticCatalogItems(response?.entries ?? [], resourceType),
    [resourceType, response?.entries],
  );
  const selectedItems = useMemo(() => selectedAsArray(selected), [selected]);
  const toggleSelection = (item: CatalogItem) => {
    if (!catalogItemAvailability(item).selectable) return;
    const key = catalogItemKey(item);
    setSelected((current) => {
      const next = new Map(current);
      if (next.has(key)) next.delete(key);
      else if (next.size < MAX_SELECTIONS) next.set(key, item);
      return next;
    });
    setActionMessage(undefined);
  };

  const choiceDetailsKey = (item: CatalogItem): string =>
    JSON.stringify([contextKey, response?.contextToken ?? '', catalogItemKey(item)]);

  const resolveChoiceGroup = async (item: CatalogItem): Promise<CatalogChoiceGroup> => {
    const existingChoice = catalogItemConstructionChoice(item);
    if (!existingChoice) {
      return { item, choices: [], complete: true, truncated: false };
    }
    if (
      !routeContext &&
      existingChoice.source.resourceType === rowRoot &&
      existingChoice.route.length === 0
    ) {
      return { item, choices: [existingChoice], complete: true, truncated: false };
    }
    if (item.kind === 'SEMANTIC' && !response) {
      throw new Error('Loom has not provided a complete concept catalog for this result.');
    }
    let source: ConstructionChoiceSearchSource;
    if (item.kind === 'FIELD') {
      source = { kind: 'FIELD', candidateId: item.candidate.candidateId };
    } else {
      if (!response) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
      source = {
        kind: 'SEMANTIC',
        contextToken: response.contextToken,
        buildId: response.buildId,
        conceptId: item.item.conceptId,
        bindingId: item.item.bindingId,
      };
    }
    const resolved = await client.searchConstructionChoices({
      project,
      explorerId,
      authResourcePath,
      snapshotToken,
      outputId,
      ...(routeContext ? { occurrenceId: routeContext.occurrenceId } : {}),
      source,
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
  };

  const inspectChoices = (item: CatalogItem) => {
    const key = choiceDetailsKey(item);
    const current = choiceDetails.get(key);
    if (current?.status === 'loading' || current?.status === 'ready') return;
    setChoiceDetails((previous) => new Map(previous).set(key, { status: 'loading' }));
    void resolveChoiceGroup(item)
      .then((group) => {
        setChoiceDetails((previous) => new Map(previous).set(key, { status: 'ready', group }));
      })
      .catch((error: unknown) => {
        setChoiceDetails((previous) => new Map(previous).set(key, {
          status: 'error',
          message: error instanceof Error
            ? error.message
            : 'Loom could not load table-specific construction choices.',
        }));
      });
  };

  const commitSelections = async (selections: ReadonlyArray<CatalogChoiceIntent>) => {
    if (!canAddFromSourceProjection || !onAddSelected || !selections.length) return;
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
    if (!canAddFromSourceProjection || !selectedItems.length || !onAddSelected) return;
    setAdding(true);
    setActionMessage(undefined);
    try {
      const groups = await Promise.all(selectedItems.map(resolveChoiceGroup));
      setChoiceDetails((previous) => {
        const next = new Map(previous);
        for (const group of groups) {
          next.set(choiceDetailsKey(group.item), { status: 'ready', group });
        }
        return next;
      });
      const direct = groups.flatMap((group) => {
        if (!group.complete || group.truncated || group.choices.length !== 1) return [];
        const choice = group.choices[0]!;
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
    setQuery(nextQuery);
    loadPage(nextQuery, undefined, true);
  };

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
            : `Search ${rowRoot} fields first, or search concepts and fields across the authorized dataset.`}
        </p>
        <p className="mt-1 text-xs text-slate-500">
          Concept counts describe observed source occurrences. This catalog does not report a per-code denominator or coverage across the current table rows.
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
            disabled={disabled || loadState.status === 'loading'}
            className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Search
          </button>
        </form>
      </div>

      {warning ? (
        <div className="mx-4 mt-4 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-950 sm:mx-5" role="status">
          {warning}
        </div>
      ) : null}
      {response?.state === 'complete' && response.sourceAvailability === 'verified' ? (
        <p className="mx-4 mt-4 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-950 sm:mx-5" role="status">
          Source availability is verified for this inventory. Per-code denominators and coverage of current table rows are not provided.
        </p>
      ) : null}
      {sourceProjectionAvailability && !sourceProjectionAvailability.available ? (
        <p className="mx-4 mt-4 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-950 sm:mx-5" role="status">
          Add from source is unavailable here: {sourceProjectionReason || 'Loom has not confirmed that source columns retain this stage’s row identity.'} You can still inspect fields, concepts, evidence, and the source choices Loom provides.
        </p>
      ) : sourceProjectionAvailability?.available ? (
        <p className="mx-4 mt-4 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-sm text-blue-950 sm:mx-5" role="status">
          This stage retains source row identity. Selections here become source-projection columns and flow through the saved row-preserving steps.
        </p>
      ) : null}
      {disabled && disabledReason?.trim() ? (
        <p id={disabledReasonId} className="mx-4 mt-3 rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700 sm:mx-5" role="status">
          {disabledReason.trim()}
        </p>
      ) : null}
      {loadState.status === 'error' ? (
        <div className="mx-4 mt-4 rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-900 sm:mx-5" role="alert">
          {loadState.message}
        </div>
      ) : null}

      <div className={layout === 'panel'
        ? 'grid min-h-[32rem]'
        : 'grid min-h-[38rem] lg:grid-cols-[minmax(0,1.25fr)_minmax(19rem,0.75fr)]'}>
        <div className={layout === 'panel'
          ? 'min-w-0 border-b border-slate-200 p-4'
          : 'min-w-0 border-b border-slate-200 p-4 lg:border-b-0 lg:border-r sm:p-5'}>
          <section aria-labelledby="feature-catalog-fields-title">
          <div className="flex items-center justify-between gap-3">
              <h3 id="feature-catalog-fields-title" className="text-sm font-semibold text-slate-800">Fields {resourceType ? `on ${resourceType}` : query ? 'matching this search' : `on ${rowRoot}`}</h3>
              <span className="text-xs text-slate-500">{fieldItems.length} available</span>
            </div>
            <div className="mt-3 divide-y divide-slate-200">
              {fieldItems.length === 0 ? (
                <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-600">
                  No fields match this search.
            </p>
              ) : fieldItems.map((item) => (
                <CatalogItemRow
                  key={catalogItemKey(item)}
                  item={item}
                  rowRoot={rowRoot}
                  currentResourceType={resourceType}
                  hasRouteContext={routeContext !== undefined}
                  checked={selected.has(catalogItemKey(item))}
                  disabled={disabled || selected.size >= MAX_SELECTIONS && !selected.has(catalogItemKey(item))}
                  selectionDisabled={!canAddFromSourceProjection}
                  disabledReasonId={visibleDisabledReasonId}
                  onToggle={() => toggleSelection(item)}
                  choiceDetails={choiceDetails.get(choiceDetailsKey(item))}
                  onInspectChoices={() => inspectChoices(item)}
                />
              ))}
          </div>
          </section>

          <section className="mt-6 border-t border-slate-200 pt-5" aria-labelledby="feature-catalog-concepts-title">
            <div className="flex items-center justify-between gap-3">
              <h3 id="feature-catalog-concepts-title" className="text-sm font-semibold text-slate-800">
                {query ? `Concepts for “${query}”` : resourceType ? `Concepts on ${resourceType}` : 'Concepts across the dataset'}
              </h3>
              <span className="text-xs text-slate-500">{semanticItems.length} on this page</span>
            </div>
          <div className="mt-3 divide-y divide-slate-200">
            {loadState.status === 'loading' && !response ? (
                <p className="py-8 text-center text-sm text-slate-500">Loading concepts…</p>
            ) : null}
              {response && semanticItems.length === 0 ? (
                <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-600">
                No concepts match this search.
              </p>
            ) : null}
              {semanticItems.map((item) => (
                <CatalogItemRow
                  key={catalogItemKey(item)}
                  item={item}
                  rowRoot={rowRoot}
                  currentResourceType={resourceType}
                  hasRouteContext={routeContext !== undefined}
                  checked={selected.has(catalogItemKey(item))}
                  disabled={disabled || !canBrowseConcepts || selected.size >= MAX_SELECTIONS && !selected.has(catalogItemKey(item))}
                  selectionDisabled={!canAddFromSourceProjection}
                  disabledReasonId={visibleDisabledReasonId}
                  onToggle={() => toggleSelection(item)}
                  choiceDetails={choiceDetails.get(choiceDetailsKey(item))}
                  onInspectChoices={() => inspectChoices(item)}
                    />
              ))}
                        </div>
          <div className="mt-4 flex items-center justify-between border-t border-slate-200 pt-3">
            <button
              type="button"
              disabled={pageIndex === 0 || loadState.status === 'loading'}
              onClick={() => setPageIndex((value) => Math.max(0, value - 1))}
              className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Previous
            </button>
            <span className="text-xs text-slate-500">Page {pageIndex + 1}</span>
            <button
              type="button"
              disabled={!response?.nextCursor || loadState.status === 'loading'}
              onClick={() => {
                if (pages[pageIndex + 1]) setPageIndex((value) => value + 1);
                else if (response?.nextCursor) loadPage(query, response.nextCursor);
              }}
              className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Next
            </button>
          </div>
          </section>
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
            ) : selectedItems.map((item) => (
              <div key={catalogItemKey(item)} className="flex items-start gap-2 rounded-md border border-slate-200 bg-white p-2.5">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-slate-900">{catalogItemLabel(item)}</p>
                  <p className="truncate text-[11px] text-slate-500">{item.kind === 'FIELD' ? 'Field' : semanticCodeLabel(item.item)}</p>
                </div>
                <button
                  type="button"
                  aria-label={`Remove ${catalogItemLabel(item)}`}
                  onClick={() => toggleSelection(item)}
                  className="rounded px-1.5 text-lg leading-5 text-slate-500 hover:bg-slate-100 hover:text-slate-900"
                >
                  ×
                </button>
              </div>
            ))}
          </div>
          {!pendingSelection ? (
          <button
            type="button"
              disabled={disabled || adding || selected.size === 0 || !onAddSelected || !canAddFromSourceProjection}
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
