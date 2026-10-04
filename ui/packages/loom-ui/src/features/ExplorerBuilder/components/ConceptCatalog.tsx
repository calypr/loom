import React, { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import type {
  ConstructionChoiceOption,
  ConstructionChoiceSearchSource,
  ExplorerBuilderCatalog,
  FieldChoiceSource,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
} from '../../../types';
import {
  catalogChoiceIntent,
  catalogItemAvailability,
  catalogItemKey,
  catalogItemLabel,
  catalogItemConstructionChoice,
  catalogItemDefaultForm,
  fieldCatalogItems,
  isRelatedFieldCatalogItem,
  semanticCatalogItems,
  type CatalogChoiceGroup,
  type CatalogChoiceIntent,
  type CatalogItem,
} from '../catalogItems';
import {
  CatalogSelectionDialog,
  type CatalogInitialSelection,
  type GroupedRowValuePolicyControl,
  type RouteCoverage,
} from './CatalogSelectionDialog';
import type { PairedColumnSuggestion } from '../constructionWorkspace/PairedColumnSuggestions';

const PAGE_SIZE = 50;
const ROUTE_PAGE_SIZE = 10;
const MAX_SELECTIONS = 100;

export interface CatalogRouteContext {
  readonly occurrenceId: string;
  readonly nodeId: string;
}

export interface CatalogSourceProjectionAvailability {
  readonly available: boolean;
  readonly reason: string;
}

export interface CatalogRelatedSourceAvailability {
  readonly supported: boolean;
  readonly reason?: string;
}

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

const choiceGroupsForRelatedSource = (
  groups: ReadonlyArray<CatalogChoiceGroup>,
  rowRoot: string,
  availability: CatalogRelatedSourceAvailability | undefined,
): ReadonlyArray<CatalogChoiceGroup> => {
  if (!availability) return groups;
  return groups.map((group) => {
    if (group.item.kind !== 'FIELD' || !isRelatedFieldCatalogItem(group.item, rowRoot)) {
      return group;
    }
    if (!availability.supported) return { ...group, choices: [] };
    return {
      ...group,
      choices: group.choices.flatMap((choice) => {
        const options = choice.options.filter((option) =>
          (option.form === 'ALL' || option.form === 'COUNT' || option.form === 'PRESENCE') &&
          catalogChoiceIntent({ item: group.item, choice, form: option.form, rowRoot }).relatedSource !== undefined
        );
        return options.length > 0 ? [{ ...choice, options }] : [];
      }),
    };
  });
};

const addActionMessage = (intents: ReadonlyArray<CatalogChoiceIntent>): string =>
  intents.some((intent) => intent.relatedSource)
    ? 'Related-source proposal submitted. Review the preview before applying.'
    : `${intents.length} ${intents.length === 1 ? 'feature was' : 'features were'} added to your table.`;

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
    case 'COUNT':
      return 'Matching record count';
    case 'PRESENCE':
      return 'Has matching record';
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
        {group.nextCursor
          ? 'More routes are available. Select this source to browse them.'
          : 'The route search reached its safety limit. Additional routes may exist.'}
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
  selectionDisabledReasonId,
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
  readonly selectionDisabledReasonId?: string;
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
  const describedBy = [
    disabled && disabledReasonId,
    selectionDisabled && selectionDisabledReasonId,
  ].filter(Boolean).join(' ') || undefined;
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
    <article className="py-2 first:pt-0">
      <div className="flex items-start gap-2">
        <input
          type="checkbox"
          aria-label={selectionLabel}
          aria-describedby={describedBy}
          checked={checked}
          disabled={disabled || selectionDisabled || !availability.selectable}
          onChange={onToggle}
          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-700"
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <h3 className="font-semibold text-slate-900">{label}</h3>
              <p className="text-xs text-slate-600">
                {item.kind === 'FIELD' ? (
                  <><span>{item.constructionChoice.source.resourceType}</span> · {item.candidate.logicalType}</>
                ) : (
                  <><span>{item.item.resourceType}</span> · <span>{item.item.occurrences.toLocaleString()} observed source {item.item.occurrences === 1 ? 'occurrence' : 'occurrences'}</span></>
                )}
              </p>
            </div>
            {item.kind === 'SEMANTIC' ? (
              <span className={`shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium ${readinessPresentation(item.item.readiness).badge}`}>
                {readinessPresentation(item.item.readiness).label}
              </span>
            ) : null}
          </div>
          {item.kind === 'SEMANTIC' && !availability.selectable ? (
            <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
              {availability.reason}
            </p>
          ) : null}
          <details className="mt-2 text-xs text-slate-600">
            <summary className="cursor-pointer font-medium text-blue-700">Inspect meaning, evidence, and construction choices</summary>
            <p className="mt-2 break-all font-mono">
              {item.kind === 'FIELD'
                ? `${item.candidate.fieldPath} · ${item.candidate.logicalType} · ${item.constructionChoice.source.cardinality}`
                : semanticCodeLabel(item.item)}
            </p>
            {item.kind === 'SEMANTIC' ? (
              <p className="mt-1">
                {item.item.display || 'Observed coded value'} · {item.item.valueType || 'value type not provided'} from {[item.item.resourceType, item.item.sourcePath].filter(Boolean).join('.') || 'source path not provided'}
              </p>
            ) : fieldConcepts.length > 0 ? (
              <p className="mt-1">Observed concept evidence is available for {fieldConcepts.length} {fieldConcepts.length === 1 ? 'code' : 'codes'} in this field.</p>
            ) : null}
            {choice && !choicesNeedTableContext ? (
              <p className="mt-1">{choice.presentation.summary} · Available results: {choice.options.map(constructionFormLabel).join(', ')}</p>
            ) : choicesNeedTableContext ? (
              <p className="mt-1">Table-specific routes and output forms need to be resolved before adding this source.</p>
            ) : null}
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

const ConceptCatalogContent = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  resourceType,
  sourceNodeId,
  routeContext,
  layout = 'workspace',
  catalog,
  disabled = false,
  disabledReason,
  sourceProjectionAvailability,
  relatedSourceAvailability,
  suppressUnavailableNotices = false,
  initialSelection,
  initialFieldSource,
  pairedColumnSuggestion,
  groupedRowValuePolicy,
  onPairedColumnSuggestionHandled,
  onAddSelected,
  onInspectRouteCoverage,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly resourceType?: string;
  readonly sourceNodeId?: string;
  readonly routeContext?: CatalogRouteContext;
  readonly layout?: 'workspace' | 'panel';
  readonly catalog: ExplorerBuilderCatalog;
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly sourceProjectionAvailability?: CatalogSourceProjectionAvailability;
  readonly relatedSourceAvailability?: CatalogRelatedSourceAvailability;
  readonly suppressUnavailableNotices?: boolean;
  readonly initialSelection?: CatalogInitialSelection;
  readonly initialFieldSource?: Pick<FieldChoiceSource, 'candidateId' | 'nodeId' | 'path'>;
  readonly pairedColumnSuggestion?: PairedColumnSuggestion;
  readonly groupedRowValuePolicy?: GroupedRowValuePolicyControl;
  readonly onPairedColumnSuggestionHandled?: (requestId: string) => void;
  readonly onAddSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => Promise<'preview-ready' | 'preview-pending' | void>;
  readonly onInspectRouteCoverage?: (selection: CatalogChoiceIntent, signal: AbortSignal) => Promise<RouteCoverage>;
}) => {
  const client = useLoomClient();
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [pageNavigation, setPageNavigation] = useState<{
    readonly scopeKey: string;
    readonly cursors: ReadonlyArray<string | undefined>;
    readonly index: number;
  }>();
  const [searchGeneration, setSearchGeneration] = useState(0);
  const [selected, setSelected] = useState<ReadonlyMap<string, CatalogItem>>(
    () => new Map(),
  );
  const [choiceDetails, setChoiceDetails] = useState<ReadonlyMap<string, ChoiceDetailsState>>(
    () => new Map(),
  );
  const [adding, setAdding] = useState(false);
  const [actionMessage, setActionMessage] = useState<string>();
  const [pendingSelection, setPendingSelection] = useState<ReadonlyArray<CatalogChoiceGroup>>();
  const [loadingMoreRoutes, setLoadingMoreRoutes] = useState<string>();
  const [routeLoadError, setRouteLoadError] = useState<{ readonly key: string; readonly message: string }>();
  const disabledReasonId = useId();
  const visibleDisabledReasonId = disabled && disabledReason?.trim()
    ? disabledReasonId
    : undefined;
  const activeChoiceRequest = useRef<AbortController | undefined>(undefined);
  const selectionContext = useRef<string | undefined>(undefined);
  const handledSuggestionRequests = useRef<Set<string>>(new Set());
  const contextKey = JSON.stringify([
    project,
    explorerId,
    snapshotToken,
    outputId,
    rowRoot,
    resourceType ?? '*',
    sourceNodeId ?? '',
    routeContext?.occurrenceId ?? '',
    routeContext?.nodeId ?? '',
    sourceProjectionAvailability?.available ?? true,
    sourceProjectionAvailability?.reason ?? '',
    relatedSourceAvailability?.supported ?? true,
    relatedSourceAvailability?.reason ?? '',
  ]);
  const catalogRequestScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    rowRoot,
    resourceType ?? '',
    query,
    searchGeneration,
  ]);
  const currentPageNavigation = pageNavigation?.scopeKey === catalogRequestScopeKey
    ? pageNavigation
    : { scopeKey: catalogRequestScopeKey, cursors: [undefined], index: 0 };
  const cursor = currentPageNavigation.cursors[currentPageNavigation.index];
  const pageCache = useRef<{
    readonly client: typeof client;
    readonly scopeKey: string;
    readonly pages: Map<string, SemanticInventoryBrowseResponse>;
  }>({ client, scopeKey: catalogRequestScopeKey, pages: new Map() });
  if (pageCache.current.client !== client || pageCache.current.scopeKey !== catalogRequestScopeKey) {
    pageCache.current = { client, scopeKey: catalogRequestScopeKey, pages: new Map() };
  }
  const catalogPageCacheKey = JSON.stringify([catalogRequestScopeKey, cursor ?? null]);
  const catalogQuery = useQuery(async (signal) => {
    if (signal.aborted) return undefined;
    const cached = pageCache.current.pages.get(catalogPageCacheKey);
    if (cached) return cached;
    const response = await client.browseSemanticInventory(
      {
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        rowRoot,
        resourceType,
        query,
        cursor,
        limit: PAGE_SIZE,
        requestId: `feature-catalog-${window.crypto.randomUUID()}`,
      },
    );
    if (!signal.aborted) {
      if (selectionContext.current && selectionContext.current !== response.contextToken) {
        setSelected(new Map());
        setPendingSelection(undefined);
        setActionMessage('The dataset catalog changed. Review and select the features again.');
      }
      selectionContext.current = response.contextToken;
      pageCache.current.pages.set(catalogPageCacheKey, response);
    }
    return response;
  }, [
    authResourcePath,
    catalogPageCacheKey,
    client,
    cursor,
    explorerId,
    project,
    query,
    resourceType,
    rowRoot,
    searchGeneration,
    snapshotToken,
  ], Boolean(snapshotToken && rowRoot));
  const loadState: CatalogLoadState = !snapshotToken || !rowRoot
    ? { status: 'idle' }
    : catalogQuery.error
      ? { status: 'error', message: catalogQuery.error instanceof Error
        ? catalogQuery.error.message
        : 'Loom could not load the feature catalog.' }
      : catalogQuery.isLoading
        ? { status: 'loading' }
        : catalogQuery.data
          ? { status: 'ready' }
          : { status: 'idle' };
  const canAddFromSourceProjection = sourceProjectionAvailability?.available ?? true;
  const sourceProjectionReason = sourceProjectionAvailability?.reason.trim();
  const relatedSourceAvailabilityId = useId();
  const canAddCatalogItem = (item: CatalogItem): boolean => {
    if (relatedSourceAvailability && isRelatedFieldCatalogItem(item, rowRoot)) {
      return relatedSourceAvailability.supported;
    }
    return canAddFromSourceProjection;
  };


  useEffect(() => {
    const suggestion = pairedColumnSuggestion;
    if (!suggestion || handledSuggestionRequests.current.has(suggestion.requestId)) return;
    handledSuggestionRequests.current.add(suggestion.requestId);
    onPairedColumnSuggestionHandled?.(suggestion.requestId);

    if (
      suggestion.snapshotToken !== snapshotToken ||
      suggestion.outputId !== outputId ||
      suggestion.choices.snapshotToken !== snapshotToken ||
      suggestion.choices.outputId !== outputId
    ) {
      setActionMessage('The table changed. Choose the paired concept again for the current table.');
      return;
    }

    const choices = suggestion.choices.choices.filter((choice) =>
      choice.source.kind === 'SEMANTIC' &&
      choice.source.conceptId === suggestion.item.conceptId &&
      choice.source.bindingId === suggestion.item.bindingId &&
      choice.source.resourceType === suggestion.item.resourceType &&
      choice.options.some((option) => option.support === 'SUPPORTED'),
    );
    const firstChoice = choices[0];
    if (!firstChoice || !catalogItemAvailability({
      kind: 'SEMANTIC',
      item: suggestion.item,
      constructionChoice: firstChoice,
    }).selectable) {
      setActionMessage('Loom no longer provides a supported route and result form for this paired concept.');
      return;
    }

    const item: CatalogItem = {
      kind: 'SEMANTIC',
      item: suggestion.item,
      constructionChoice: firstChoice,
    };
    setSelected(new Map([[catalogItemKey(item), item]]));
    setPendingSelection([{
      item,
      choices,
      complete: suggestion.choices.complete,
      truncated: suggestion.choices.truncated,
      ...(suggestion.choices.nextCursor ? { nextCursor: suggestion.choices.nextCursor } : {}),
      semanticContext: {
        contextToken: suggestion.contextToken,
        buildId: suggestion.buildId,
      },
    }]);
    setActionMessage(undefined);
  }, [
    onPairedColumnSuggestionHandled,
    outputId,
    pairedColumnSuggestion,
    snapshotToken,
  ]);

  const response = catalogQuery.data;
  const pageIndex = currentPageNavigation.index;
  const warning = response ? availabilityMessage(response) : undefined;
  const canBrowseConcepts = response?.state === 'complete';
  const fieldItems = useMemo(
    () => fieldCatalogItems(catalog, rowRoot, resourceType, query, sourceNodeId).filter(
      (item) =>
        item.kind === 'FIELD' &&
        (!routeContext || item.candidate.nodeId === routeContext.nodeId),
    ),
    [catalog, query, resourceType, routeContext, rowRoot, sourceNodeId],
  );
  const appliedInitialSelection = useRef(false);
  useEffect(() => {
    if (appliedInitialSelection.current || !initialFieldSource) return;
    const item = fieldItems.find((candidate) => candidate.kind === 'FIELD' &&
      candidate.candidate.candidateId === initialFieldSource.candidateId &&
      candidate.candidate.nodeId === initialFieldSource.nodeId &&
      candidate.candidate.fieldPath === initialFieldSource.path);
    if (!item) return;
    appliedInitialSelection.current = true;
    setSelected((current) => current.size > 0 ? current : new Map([[catalogItemKey(item), item]]));
  }, [fieldItems, initialFieldSource]);
  const semanticItems = useMemo(
    () => semanticCatalogItems(response?.entries ?? [], resourceType, sourceNodeId),
    [resourceType, response?.entries, sourceNodeId],
  );
  const hasUnscopedConcepts = Boolean(sourceNodeId && response?.entries.some(
    (item) => item.constructionChoice === undefined,
  ));
  const selectedItems = useMemo(() => selectedAsArray(selected), [selected]);
  const toggleSelection = (item: CatalogItem) => {
    if (!canAddCatalogItem(item) || !catalogItemAvailability(item).selectable) return;
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

  const resolveChoiceGroup = async (
    item: CatalogItem,
    signal?: AbortSignal,
    cursor?: string,
    semanticContext?: { readonly contextToken: string; readonly buildId: string },
  ): Promise<CatalogChoiceGroup> => {
    const existingChoice = catalogItemConstructionChoice(item);
    if (
      !cursor &&
      existingChoice &&
      !routeContext &&
      existingChoice.source.resourceType === rowRoot &&
      existingChoice.route.length === 0
    ) {
      return { item, choices: [existingChoice], complete: true, truncated: false };
    }
    if (item.kind === 'SEMANTIC' && !response) {
      if (!semanticContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
    }
    let source: ConstructionChoiceSearchSource;
    if (item.kind === 'FIELD') {
      source = { kind: 'FIELD', candidateId: item.candidate.candidateId };
    } else {
      const effectiveContext = semanticContext ?? (response ? {
        contextToken: response.contextToken,
        buildId: response.buildId,
      } : undefined);
      if (!effectiveContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
      source = {
        kind: 'SEMANTIC',
        contextToken: effectiveContext.contextToken,
        buildId: effectiveContext.buildId,
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
      limit: ROUTE_PAGE_SIZE,
      ...(cursor ? { cursor } : {}),
      requestId: `construction-choices-${window.crypto.randomUUID()}`,
    }, signal);
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
      nextCursor: resolved.nextCursor,
      ...(item.kind === 'SEMANTIC' ? {
        semanticContext: semanticContext ?? (response ? {
          contextToken: response.contextToken,
          buildId: response.buildId,
        } : undefined),
      } : {}),
    };
  };

  const loadMoreChoices = async (group: CatalogChoiceGroup) => {
    if (!group.nextCursor || loadingMoreRoutes) return;
    const key = catalogItemKey(group.item);
    setLoadingMoreRoutes(key);
    setRouteLoadError(undefined);
    try {
      const page = await resolveChoiceGroup(group.item, undefined, group.nextCursor, group.semanticContext);
      const seen = new Set(group.choices.map((choice) => choice.choiceId));
      const updated = {
        ...group,
        choices: [
          ...group.choices,
          ...page.choices.filter((choice) => !seen.has(choice.choiceId)),
        ],
        complete: page.complete,
        truncated: page.truncated,
        nextCursor: page.nextCursor,
      };
      setPendingSelection((current) => current?.map((candidate) =>
        catalogItemKey(candidate.item) === key ? updated : candidate,
      ));
      setChoiceDetails((current) => new Map(current).set(
        choiceDetailsKey(group.item),
        { status: 'ready', group: updated },
      ));
    } catch (error) {
      setRouteLoadError({
        key,
        message: error instanceof Error ? error.message : 'Loom could not load more routes.',
      });
    } finally {
      setLoadingMoreRoutes(undefined);
    }
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
    if (!onAddSelected || !selections.length) return;
    const intents = pendingSelection?.length === selections.length
      ? selections.map((selection, index) => {
          const group = pendingSelection[index];
          const choice = group?.choices.find(
            (candidate) => candidate.choiceId === selection.constructionChoice.choiceId,
          );
          return group && choice
            ? {
                ...catalogChoiceIntent({
                item: group.item,
                choice,
                form: selection.constructionChoice.form,
                rowValuePolicy: selection.constructionChoice.rowValuePolicy,
                ...(relatedSourceAvailability?.supported ? { rowRoot } : {}),
                }),
                ...(selection.contributorPredicate ? { contributorPredicate: selection.contributorPredicate } : {}),
              }
            : selection;
        })
      : selections;
    if (
      !pendingSelection ||
      pendingSelection.length !== intents.length ||
      !pendingSelection.every((group, index) => {
        const intent = intents[index];
        if (!intent || !canAddCatalogItem(group.item)) return false;
        return isRelatedFieldCatalogItem(group.item, rowRoot) && relatedSourceAvailability?.supported
          ? intent.relatedSource !== undefined
          : canAddFromSourceProjection;
      })
    ) return;
    setAdding(true);
    setActionMessage(undefined);
    try {
      const result = await onAddSelected(intents);
      setSelected(new Map());
      setPendingSelection(undefined);
      setActionMessage(result === 'preview-ready' || result === 'preview-pending'
        ? 'Review the proposed rows, then apply the columns.'
        : addActionMessage(intents));
    } catch (error) {
      setActionMessage(error instanceof Error ? error.message : 'Loom could not add the selected features.');
    } finally {
      setAdding(false);
    }
  };

  const openSelection = async () => {
    if (!selectedItems.length || !onAddSelected || !selectedItems.every(canAddCatalogItem)) return;
    setRouteLoadError(undefined);
    const controller = new AbortController();
    activeChoiceRequest.current?.abort();
    activeChoiceRequest.current = controller;
    setAdding(true);
    setActionMessage(undefined);
    try {
      const groups = await Promise.all(selectedItems.map(
        (item) => resolveChoiceGroup(item, controller.signal),
      ));
      if (controller.signal.aborted) return;
      const dialogGroups = choiceGroupsForRelatedSource(
        groups,
        rowRoot,
        relatedSourceAvailability,
      );
      setChoiceDetails((previous) => {
        const next = new Map(previous);
        for (const group of groups) {
          next.set(choiceDetailsKey(group.item), { status: 'ready', group });
        }
        return next;
      });
      const direct = groups.flatMap((group, index) => {
        const dialogGroup = dialogGroups[index];
        if (!group.complete || group.truncated || dialogGroup?.choices.length !== 1) return [];
        const dialogChoice = dialogGroup.choices[0]!;
        const choice = group.choices.find(
          (candidate) => candidate.choiceId === dialogChoice.choiceId,
        );
        const form = catalogItemDefaultForm(dialogChoice);
        return choice && form && dialogChoice.options.length === 1
          ? [catalogChoiceIntent({
              item: group.item,
              choice,
              form,
              ...(relatedSourceAvailability?.supported ? { rowRoot } : {}),
            })]
          : [];
      });
      if (direct.length === groups.length) {
        if (!groups.every((group, index) => {
          const intent = direct[index];
          if (!intent || !canAddCatalogItem(group.item)) return false;
          return isRelatedFieldCatalogItem(group.item, rowRoot) && relatedSourceAvailability?.supported
            ? intent.relatedSource !== undefined
            : canAddFromSourceProjection;
        })) {
          setPendingSelection(groups);
          return;
        }
        const result = await onAddSelected(direct);
        setSelected(new Map());
        setActionMessage(result === 'preview-ready' || result === 'preview-pending'
          ? 'Review the proposed rows, then apply the columns.'
          : addActionMessage(direct));
      } else {
        setPendingSelection(groups);
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      setActionMessage(
        error instanceof Error
          ? error.message
          : 'Loom could not resolve the selected feature routes.',
      );
    } finally {
      if (activeChoiceRequest.current === controller) {
        activeChoiceRequest.current = undefined;
      }
      if (!controller.signal.aborted) setAdding(false);
    }
  };

  const submitSearch = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextQuery = queryInput.trim();
    setQuery(nextQuery);
    setPageNavigation(undefined);
    setActionMessage(undefined);
    setSearchGeneration((generation) => generation + 1);
  };

  return (
    <section className="min-w-0 rounded-xl border border-slate-200 bg-white shadow-sm">
      {pendingSelection ? (
        <CatalogSelectionDialog
          groups={choiceGroupsForRelatedSource(
            pendingSelection,
            rowRoot,
            relatedSourceAvailability,
          )}
          rowRoot={rowRoot}
          initialSelection={initialSelection}
          groupedRowValuePolicy={groupedRowValuePolicy}
          busy={adding || loadingMoreRoutes !== undefined}
          loadingMoreRoutes={loadingMoreRoutes}
          routeLoadError={routeLoadError}
          onLoadMoreRoutes={(group) => void loadMoreChoices(group)}
          onInspectRouteCoverage={onInspectRouteCoverage}
          onCancel={() => setPendingSelection(undefined)}
          onConfirm={(selections) => void commitSelections(selections)}
        />
      ) : null}
      <div className="border-b border-slate-200 px-3 py-3">
        <h2 className="text-base font-semibold text-slate-950">Add coded value columns</h2>
        <p className="mt-1 text-xs text-slate-600">
          {resourceType
            ? `Find coded values on the selected ${resourceType} graph node.`
            : `Find coded values across the authorized dataset, starting from ${rowRoot} rows.`}
        </p>
        <form className="mt-2 flex gap-2" onSubmit={submitSearch}>
          <label className="sr-only" htmlFor="feature-catalog-search">Search coded values and raw fields</label>
          <input
            id="feature-catalog-search"
            type="search"
            aria-label="Search features by field name, concept, or code"
            value={queryInput}
            onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder="Search a code or label"
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
        <details className="mt-2 text-xs text-slate-600">
          <summary className="cursor-pointer font-medium text-blue-800">How coded columns work</summary>
          <p className="mt-1">The code label names the column; its paired value fills the cells. Loom keeps the exact code, system, and source behind the label.</p>
          <p className="mt-1">Concept counts describe observed source occurrences. This catalog does not report a per-code denominator or coverage across the current table rows.</p>
        </details>
      </div>

      {warning ? (
        <div className="mx-4 mt-4 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-950 sm:mx-5" role="status">
          {warning}
        </div>
      ) : null}
      {suppressUnavailableNotices && relatedSourceAvailability?.supported === false ? (
        <span id={relatedSourceAvailabilityId} className="sr-only">
          Related fields are unavailable until the saved source fields are repaired.
        </span>
      ) : null}
      {(response?.state === 'complete' && response.sourceAvailability === 'verified') ||
        (!suppressUnavailableNotices && (relatedSourceAvailability?.supported === false || sourceProjectionAvailability)) ? (
        <details className="mx-3 mt-2 text-xs text-slate-600">
          <summary className="cursor-pointer font-medium text-blue-800">
            {!suppressUnavailableNotices && (relatedSourceAvailability?.supported === false || sourceProjectionAvailability?.available === false)
              ? 'Some column sources are unavailable · see why'
              : 'Availability and coverage notes'}
          </summary>
          {response?.state === 'complete' && response.sourceAvailability === 'verified' ? (
            <p className="mt-2" role="status">Source availability is verified for this inventory. Per-code denominators and coverage of current table rows are not provided.</p>
          ) : null}
          {relatedSourceAvailability?.supported === false && !suppressUnavailableNotices ? (
            <p id={relatedSourceAvailabilityId} className="mt-2" role="status">
              Adding fields from related resources is unavailable here: {relatedSourceAvailability.reason?.trim() || 'Loom has not confirmed that this stage supports related-source fields.'}
            </p>
          ) : null}
          {sourceProjectionAvailability && !sourceProjectionAvailability.available && !suppressUnavailableNotices ? (
            <p className="mt-2" role="status">
              Add from source is unavailable here: {sourceProjectionReason || 'Loom has not confirmed that source columns retain this stage’s row identity.'} You can still inspect fields, concepts, evidence, and the source choices Loom provides.
            </p>
          ) : sourceProjectionAvailability?.available && !suppressUnavailableNotices ? (
            <p className="mt-2" role="status">You can add columns while keeping the current rows. Preview the result to see which rows have values.</p>
          ) : null}
        </details>
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
        ? 'grid min-h-[14rem]'
        : 'grid min-h-[24rem] lg:grid-cols-[minmax(0,1.25fr)_minmax(19rem,0.75fr)]'}>
        <div className={layout === 'panel'
          ? 'min-w-0 border-b border-slate-200 p-4'
          : 'min-w-0 border-b border-slate-200 p-4 lg:border-b-0 lg:border-r sm:p-5'}>
          <section aria-labelledby="feature-catalog-concepts-title">
            <div className="flex items-center justify-between gap-3">
              <h3 id="feature-catalog-concepts-title" className="text-sm font-semibold text-slate-800">
                {query ? `Coded values for “${query}”` : resourceType ? `Coded values on ${resourceType}` : 'Coded values across the dataset'}
              </h3>
              <span className="text-xs text-slate-500">{semanticItems.length} on this page</span>
            </div>
            {hasUnscopedConcepts ? (
              <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-950" role="status" data-testid="construction-operation-unscoped-concepts">
                Some concepts are hidden because Loom did not identify their source node.
              </p>
            ) : null}
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
              onClick={() => setPageNavigation((current) => {
                const navigation = current?.scopeKey === catalogRequestScopeKey
                  ? current
                  : { scopeKey: catalogRequestScopeKey, cursors: [undefined], index: 0 };
                return { ...navigation, index: Math.max(0, navigation.index - 1) };
              })}
              className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Previous
            </button>
            <span className="text-xs text-slate-500">Page {pageIndex + 1}</span>
            <button
              type="button"
              disabled={!response?.nextCursor || loadState.status === 'loading'}
              onClick={() => {
                setPageNavigation((current) => {
                  const navigation = current?.scopeKey === catalogRequestScopeKey
                    ? current
                    : { scopeKey: catalogRequestScopeKey, cursors: [undefined], index: 0 };
                  if (navigation.index + 1 < navigation.cursors.length) {
                    return { ...navigation, index: navigation.index + 1 };
                  }
                  return response?.nextCursor
                    ? {
                      ...navigation,
                      cursors: [...navigation.cursors, response.nextCursor],
                      index: navigation.index + 1,
                    }
                    : navigation;
                });
              }}
              className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Next
            </button>
          </div>
          </section>

          <details className="mt-6 border-t border-slate-200 pt-5" data-testid="feature-catalog-raw-fields" open={layout !== 'panel' ? true : undefined}>
            <summary className="cursor-pointer text-sm font-semibold text-slate-700">Raw FHIR fields (advanced)</summary>
            <p className="mt-2 text-xs text-slate-600">Use a source path when you need a row ID, date, grouping field, or a value without a coded pairing.</p>
          <section className="mt-3" aria-labelledby="feature-catalog-fields-title">
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
                  selectionDisabled={!canAddCatalogItem(item)}
                  disabledReasonId={visibleDisabledReasonId}
                  selectionDisabledReasonId={
                    relatedSourceAvailability?.supported === false &&
                    isRelatedFieldCatalogItem(item, rowRoot)
                      ? relatedSourceAvailabilityId
                      : undefined
                  }
                  onToggle={() => toggleSelection(item)}
                  choiceDetails={choiceDetails.get(choiceDetailsKey(item))}
                  onInspectChoices={() => inspectChoices(item)}
                />
              ))}
            </div>
          </section>
          </details>
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
              disabled={disabled || adding || selected.size === 0 || !onAddSelected || !selectedItems.every(canAddCatalogItem)}
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

export const ConceptCatalog = (props: React.ComponentProps<typeof ConceptCatalogContent>) => (
  <ConceptCatalogContent
    key={JSON.stringify([
      props.project, props.explorerId, props.authResourcePath ?? '', props.snapshotToken,
      props.outputId, props.rowRoot, props.resourceType ?? '', props.sourceNodeId ?? '',
      props.routeContext?.occurrenceId ?? '', props.routeContext?.nodeId ?? '',
    ])}
    {...props}
  />
);
