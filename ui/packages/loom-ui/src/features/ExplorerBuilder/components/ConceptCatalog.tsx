import React, { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useLoomClient, useQuery } from '../../../react';
import type {
  ConstructionChoice,
  ConstructionChoiceOption,
  ConstructionChoiceSearchSource,
  ExplorerBuilderCatalog,
  FieldChoiceSource,
  SemanticInventoryBrowseResponse,
  SemanticInventoryItem,
  SemanticInventorySelectionMode,
} from '../../../types';
import {
  CODED_SEMANTIC_RULE_HINTS,
  catalogChoiceIntent,
  catalogItemAvailability,
  catalogItemKey,
  catalogItemLabel,
  catalogItemConstructionChoice,
  catalogSourceOptions,
  fieldCatalogItems,
  isRelatedFieldCatalogItem,
  semanticCatalogItems,
  semanticItemMatchesAnyRule,
  type CatalogChoiceGroup,
  type CatalogChoiceIntent,
  type CatalogItem,
} from '../catalogItems';
import {
  CatalogSelectionOptions,
  type CatalogInitialSelection,
  type GroupedRowValuePolicyControl,
  type RouteCoverage,
} from './CatalogSelectionOptions';
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

export type ConceptCatalogView = 'all' | 'coded' | 'fields';

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

type CatalogSourceGroup = {
  readonly nodeId: string;
  readonly resourceType: string;
  readonly items: ReadonlyArray<CatalogItem>;
};

const catalogSourceGroups = (
  items: ReadonlyArray<CatalogItem>,
  rowRoot: string,
): ReadonlyArray<CatalogSourceGroup> => {
  const groups = new Map<string, { nodeId: string; resourceType: string; items: CatalogItem[] }>();
  for (const item of items) {
    const source = catalogItemConstructionChoice(item)?.source;
    const resourceType = item.kind === 'FIELD' ? item.constructionChoice.source.resourceType : item.item.resourceType;
    const nodeId = source?.nodeId ?? (item.kind === 'FIELD' ? item.candidate.nodeId : resourceType);
    const existing = groups.get(nodeId);
    if (existing) {
      existing.items.push(item);
    } else {
      groups.set(nodeId, { nodeId, resourceType, items: [item] });
    }
  }
  return [...groups.values()].sort((left, right) => {
    if (left.resourceType === rowRoot && right.resourceType !== rowRoot) return -1;
    if (right.resourceType === rowRoot && left.resourceType !== rowRoot) return 1;
    if (left.resourceType !== right.resourceType) return left.resourceType < right.resourceType ? -1 : 1;
    return left.nodeId < right.nodeId ? -1 : left.nodeId > right.nodeId ? 1 : 0;
  });
};

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

const unavailableSelectionMessage = (items: ReadonlyArray<CatalogItem>): string => {
  const labels = items.map(catalogItemLabel);
  return labels.length === 1
    ? `${labels[0]} is not available for this table and was removed from your selection.`
    : `${labels.join(', ')} are not available for this table and were removed from your selection.`;
};

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
  const [detailsOpen, setDetailsOpen] = useState(false);
  const detailsId = useId();
  const [detailsPosition, setDetailsPosition] = useState({ left: 0, top: 24, width: 320, maxHeight: 448 });
  const openDetails = (trigger: HTMLElement) => {
    const rect = trigger.getBoundingClientRect();
    const width = Math.max(0, Math.min(320, window.innerWidth - 32));
    const horizontalMargin = 16;
    const viewportLeft = Math.max(horizontalMargin, Math.min(window.innerWidth - width - horizontalMargin, rect.left));
    const maxHeight = Math.max(96, Math.min(448, window.innerHeight * 0.7));
    const spaceBelow = window.innerHeight - rect.bottom - 8;
    const openBelow = spaceBelow >= Math.min(160, maxHeight);
    const height = openBelow ? Math.min(maxHeight, spaceBelow) : Math.min(maxHeight, Math.max(0, rect.top - 8));
    const viewportTop = openBelow ? rect.bottom : rect.top - height;
    setDetailsPosition({ left: viewportLeft - rect.left, top: viewportTop - rect.top, width, maxHeight: height });
    setDetailsOpen(true);
  };
  const detailsPopover = (
      <div
        className="relative z-20 inline-flex shrink-0 pointer-events-auto"
        onMouseEnter={(event) => openDetails(event.currentTarget)}
        onMouseLeave={(event) => {
          if (!event.currentTarget.contains(document.activeElement)) setDetailsOpen(false);
        }}
        onFocus={(event) => openDetails(event.currentTarget)}
        onBlur={(event) => {
          if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) {
            setDetailsOpen(false);
          }
        }}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.stopPropagation();
            setDetailsOpen(false);
          }
        }}
      >
        <button
          type="button"
          aria-label={`Details for ${label}`}
          aria-describedby={detailsId}
          aria-expanded={detailsOpen}
          onClick={(event) => openDetails(event.currentTarget.parentElement ?? event.currentTarget)}
          className="flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold text-slate-500 hover:bg-slate-100 hover:text-slate-900 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-700"
        >
          <span aria-hidden="true">i</span>
        </button>
        <div
          id={detailsId}
          role="tooltip"
          hidden={!detailsOpen}
          style={detailsPosition}
          className="absolute z-30 overflow-auto rounded-lg border border-slate-300 bg-white p-3 text-xs text-slate-700 shadow-xl"
        >
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
        </div>
      </div>
  );
  return (
    <article className={`relative rounded-lg border p-3 transition-shadow hover:z-20 focus-within:z-20 ${checked
      ? 'border-blue-500 bg-blue-50/60 shadow-sm'
      : 'border-slate-200 bg-white hover:border-slate-300 hover:shadow-sm'}`}>
      <button
        type="button"
        aria-label={selectionLabel}
        aria-describedby={describedBy}
        aria-pressed={checked}
        disabled={disabled || selectionDisabled || !availability.selectable}
        onClick={onToggle}
        className="absolute inset-0 z-0 rounded-lg focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-700 disabled:cursor-not-allowed"
      >
        <span className="sr-only">{selectionLabel}</span>
      </button>
      <div className={`relative z-10 flex items-start gap-2 pointer-events-none ${disabled || selectionDisabled || !availability.selectable ? 'opacity-60' : ''}`}>
        {checked ? <span aria-hidden="true" className="mt-0.5 shrink-0 font-semibold text-blue-800">✓</span> : null}
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-start justify-between gap-2">
            <span className="min-w-0">
              <span className="block font-semibold text-slate-900">{label}</span>
              <span data-testid="catalog-item-metadata" className="flex flex-wrap items-center gap-x-1 text-xs text-slate-600">
                {item.kind === 'FIELD' ? (
                  <><span>{item.constructionChoice.source.resourceType}</span><span aria-hidden="true">·</span><span>{item.candidate.logicalType}</span></>
                ) : (
                  <><span>{item.item.resourceType}</span><span aria-hidden="true">·</span><span>{item.item.occurrences.toLocaleString()} observed source {item.item.occurrences === 1 ? 'occurrence' : 'occurrences'}</span></>
                )}
                {detailsPopover}
              </span>
            </span>
            {item.kind === 'SEMANTIC' ? (
              <span className={`shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium ${readinessPresentation(item.item.readiness).badge}`}>
                {readinessPresentation(item.item.readiness).label}
              </span>
            ) : null}
          </span>
        </span>
      </div>
      {item.kind === 'SEMANTIC' && !availability.selectable ? (
        <p className="relative z-10 mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950 pointer-events-none" role="status">
          {availability.reason}
        </p>
      ) : null}

    </article>
  );
};

const ConceptCatalogContent = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  tableContextKey = '',
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
  view = 'all',
  sourceControls,
  suppressUnavailableNotices = false,
  semanticSelectionMode,
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
  readonly tableContextKey?: string;
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
  readonly view?: ConceptCatalogView;
  readonly sourceControls?: React.ReactNode;
  readonly suppressUnavailableNotices?: boolean;
  readonly semanticSelectionMode?: SemanticInventorySelectionMode;
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
  const searchAcrossSources = query.trim().length > 0;
  const browseResourceType = searchAcrossSources ? undefined : resourceType;
  const browseSourceNodeId = searchAcrossSources ? undefined : sourceNodeId;
  const browseRouteContext = searchAcrossSources ? undefined : routeContext;
  const [rawFieldsExpanded, setRawFieldsExpanded] = useState(layout !== 'panel');
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
  const [selectionPhase, setSelectionPhase] = useState<'idle' | 'loading-options' | 'submitting'>('idle');
  const submitting = selectionPhase === 'submitting';
  const loadingOptions = selectionPhase === 'loading-options';
  const [actionMessage, setActionMessage] = useState<string>();
  const [pendingSelection, setPendingSelection] = useState<ReadonlyArray<CatalogChoiceGroup>>();
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [loadingMoreRoutes, setLoadingMoreRoutes] = useState<string>();
  const [routeLoadError, setRouteLoadError] = useState<{ readonly key: string; readonly message: string }>();
  const disabledReasonId = useId();
  const visibleDisabledReasonId = disabled && disabledReason?.trim()
    ? disabledReasonId
    : undefined;
  const activeChoiceRequest = useRef<AbortController | undefined>(undefined);
  const activeMoreRouteRequest = useRef<{
    readonly controller: AbortController;
    readonly tableContextKey: string;
  } | undefined>(undefined);
  const currentTableContextKey = useRef(tableContextKey);
  currentTableContextKey.current = tableContextKey;
  const selectionContext = useRef<string | undefined>(undefined);
  const semanticContextRef = useRef<{
    readonly scopeKey: string;
    readonly client: typeof client;
    readonly value?: { readonly contextToken: string; readonly buildId: string };
  }>({ scopeKey: '', client });
  const previousView = useRef(view);
  const previousTableContext = useRef(tableContextKey);
  const handledSuggestionRequests = useRef<Set<string>>(new Set());
  const cancelLoadMoreRoutes = () => {
    activeMoreRouteRequest.current?.controller.abort();
    activeMoreRouteRequest.current = undefined;
    setLoadingMoreRoutes(undefined);
    setRouteLoadError(undefined);
  };
  useEffect(() => {
    if (previousView.current === view) return;
    previousView.current = view;
    setQuery('');
    setQueryInput('');
    setPageNavigation(undefined);
  }, [view]);
  useEffect(() => {
    if (previousTableContext.current === tableContextKey) return;
    previousTableContext.current = tableContextKey;
    activeChoiceRequest.current?.abort();
    activeChoiceRequest.current = undefined;
    cancelLoadMoreRoutes();
    setSelectionPhase('idle');
    setSelected(new Map());
    setChoiceDetails(new Map());
    setPendingSelection(undefined);
    setAdvancedOpen(false);
    setActionMessage('The table changed. Choose fields for the current table.');
  }, [tableContextKey]);
  const contextKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    outputId,
    tableContextKey,
    rowRoot,
    browseResourceType ?? '*',
    browseSourceNodeId ?? '',
    browseRouteContext?.occurrenceId ?? '',
    browseRouteContext?.nodeId ?? '',
    semanticSelectionMode ?? '',
    sourceProjectionAvailability?.available ?? true,
    sourceProjectionAvailability?.reason ?? '',
    relatedSourceAvailability?.supported ?? true,
    relatedSourceAvailability?.reason ?? '',
  ]);
  const semanticContextScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    outputId,
    rowRoot,
    semanticSelectionMode ?? '',
  ]);
  if (semanticContextRef.current.scopeKey !== semanticContextScopeKey || semanticContextRef.current.client !== client) {
    semanticContextRef.current = { scopeKey: semanticContextScopeKey, client };
  }
  const catalogRequestScopeKey = JSON.stringify([
    project,
    explorerId,
    authResourcePath ?? '',
    snapshotToken,
    rowRoot,
    outputId,
    browseResourceType ?? '',
    semanticSelectionMode ?? '',
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
        outputId,
        selectionMode: semanticSelectionMode,
        resourceType: browseResourceType,
        query,
        cursor,
        limit: PAGE_SIZE,
        requestId: `feature-catalog-${window.crypto.randomUUID()}`,
      },
      signal,
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
    outputId,
    project,
    query,
    browseResourceType,
    rowRoot,
    searchGeneration,
    snapshotToken,
  ], Boolean(snapshotToken && rowRoot && view !== 'fields'));
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
    if (view !== 'all' && item.kind === 'SEMANTIC' &&
      !semanticItemMatchesAnyRule(item.item, CODED_SEMANTIC_RULE_HINTS)) return false;
    if (relatedSourceAvailability && isRelatedFieldCatalogItem(item, rowRoot)) {
      return relatedSourceAvailability.supported;
    }
    return canAddFromSourceProjection;
  };


  useEffect(() => {
    const suggestion = pairedColumnSuggestion;
    if (view === 'fields' || !suggestion || handledSuggestionRequests.current.has(suggestion.requestId)) return;
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
    setAdvancedOpen(true);
    setActionMessage(undefined);
  }, [
    onPairedColumnSuggestionHandled,
    outputId,
    pairedColumnSuggestion,
    snapshotToken,
    view,
  ]);

  const semanticResponse = catalogQuery.data ?? pageCache.current.pages.get(catalogPageCacheKey);
  if (semanticResponse) {
    semanticContextRef.current = {
      scopeKey: semanticContextScopeKey,
      client,
      value: { contextToken: semanticResponse.contextToken, buildId: semanticResponse.buildId },
    };
  }
  const response = view === 'fields' ? undefined : semanticResponse;
  const pageIndex = currentPageNavigation.index;
  const warning = response ? availabilityMessage(response) : undefined;
  const canBrowseConcepts = response?.state === 'complete';
  const choiceDetailsKey = (item: CatalogItem): string =>
    JSON.stringify([
      contextKey,
      item.kind === 'SEMANTIC' ? semanticResponse?.contextToken ?? '' : '',
      catalogItemKey(item),
    ]);
  const choicesForCurrentTable = (group: CatalogChoiceGroup): ReadonlyArray<ConstructionChoice> =>
    choiceGroupsForRelatedSource([group], rowRoot, relatedSourceAvailability)[0]?.choices ?? [];
  const hasNoCompilerRoute = (item: CatalogItem): boolean => {
    const details = choiceDetails.get(choiceDetailsKey(item));
    return details?.status === 'ready' && choicesForCurrentTable(details.group).length === 0 &&
      details.group.complete && !details.group.truncated && !details.group.nextCursor;
  };
  const fieldItems = useMemo(
    () => {
      if (view === 'coded') return [];
      const reachableSourceNodeIds = new Set(catalogSourceOptions(catalog, rowRoot)
        .flatMap((source) => source.kind === 'RELATED' ? [source.sourceNodeId] : []));
      const catalogFields = fieldCatalogItems(
        catalog,
        rowRoot,
        browseResourceType,
        query,
        browseSourceNodeId,
        view === 'fields',
      ).filter((item) => item.kind === 'FIELD' && item.candidate.populated === true);
      return catalogFields.filter(
        (item) => item.kind === 'FIELD' &&
          !hasNoCompilerRoute(item) &&
          (!browseRouteContext || item.candidate.nodeId === browseRouteContext.nodeId) &&
          (browseRouteContext || item.constructionChoice.source.resourceType === rowRoot || reachableSourceNodeIds.has(item.candidate.nodeId)) &&
          canAddCatalogItem(item) && catalogItemAvailability(item).selectable,
      );
    },
    [browseResourceType, browseRouteContext, browseSourceNodeId, catalog, choiceDetails, contextKey, query, rowRoot, relatedSourceAvailability, sourceProjectionAvailability, view],
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
    () => semanticCatalogItems(
      response?.entries ?? [],
      browseResourceType,
      browseSourceNodeId,
      view === 'coded' ? CODED_SEMANTIC_RULE_HINTS : undefined,
    )
      .filter((item) => view !== 'fields')
      .filter((item) => !hasNoCompilerRoute(item))
      .filter((item) => canAddCatalogItem(item) && catalogItemAvailability(item).selectable),
    [browseResourceType, browseSourceNodeId, canAddFromSourceProjection, choiceDetails, contextKey, view, relatedSourceAvailability, response?.entries, rowRoot],
  );
  const fieldGroups = useMemo(
    () => catalogSourceGroups(fieldItems, rowRoot),
    [fieldItems, rowRoot],
  );
  const semanticGroups = useMemo(
    () => catalogSourceGroups(semanticItems, rowRoot),
    [semanticItems, rowRoot],
  );
  const selectedItems = useMemo(() => selectedAsArray(selected), [selected]);
  const toggleSelection = (item: CatalogItem) => {
    if (submitting) return;
    if (!canAddCatalogItem(item) || !catalogItemAvailability(item).selectable) return;
    const key = catalogItemKey(item);
    setSelected((current) => {
      const next = new Map(current);
      if (next.has(key)) next.delete(key);
      else if (next.size < MAX_SELECTIONS) next.set(key, item);
      return next;
    });
    setPendingSelection(undefined);
    setAdvancedOpen(false);
    cancelLoadMoreRoutes();
    const pendingChoiceRequest = activeChoiceRequest.current;
    activeChoiceRequest.current = undefined;
    pendingChoiceRequest?.abort();
    if (pendingChoiceRequest) setSelectionPhase('idle');
    setActionMessage(undefined);
  };

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
      !browseRouteContext &&
      existingChoice.source.resourceType === rowRoot &&
      existingChoice.route.length === 0
    ) {
      return { item, choices: [existingChoice], complete: true, truncated: false };
    }
    const retainedSemanticContext = semanticContextRef.current.scopeKey === semanticContextScopeKey && semanticContextRef.current.client === client
      ? semanticContextRef.current.value
      : undefined;
    if (item.kind === 'SEMANTIC' && !semanticResponse) {
      if (!semanticContext && !retainedSemanticContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
    }
    let source: ConstructionChoiceSearchSource;
    if (item.kind === 'FIELD') {
      source = { kind: 'FIELD', candidateId: item.candidate.candidateId };
    } else {
      const effectiveContext = semanticContext ?? (semanticResponse ? {
        contextToken: semanticResponse.contextToken,
        buildId: semanticResponse.buildId,
      } : retainedSemanticContext);
      if (!effectiveContext) {
        throw new Error('Loom has not provided a complete concept catalog for this result.');
      }
      if (item.item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        if (!existingChoice) throw new Error('Loom did not provide a signed choice for this coded family.');
        source = {
          kind: 'SEMANTIC',
          contextToken: effectiveContext.contextToken,
          buildId: effectiveContext.buildId,
          conceptId: item.item.conceptId,
          bindingId: item.item.bindingId,
          selectionMode: item.item.selectionMode,
          choiceId: existingChoice.choiceId,
        };
      } else {
        source = {
          kind: 'SEMANTIC',
          contextToken: effectiveContext.contextToken,
          buildId: effectiveContext.buildId,
          conceptId: item.item.conceptId,
          bindingId: item.item.bindingId,
        };
      }
    }
    const choicesById = new Map<string, ConstructionChoice>();
    const seenCursors = new Set(cursor ? [cursor] : []);
    let pageCursor = cursor;
    let nextCursor: string | undefined;
    let resolved: Awaited<ReturnType<typeof client.searchConstructionChoices>> | undefined;
    let choices: ReadonlyArray<ConstructionChoice> = [];
    do {
      if (signal?.aborted) throw new Error('Construction choice search was cancelled.');
      resolved = await client.searchConstructionChoices({
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        outputId,
        ...(browseRouteContext ? { occurrenceId: browseRouteContext.occurrenceId } : {}),
        source,
        limit: ROUTE_PAGE_SIZE,
        ...(pageCursor ? { cursor: pageCursor } : {}),
        requestId: `construction-choices-${window.crypto.randomUUID()}`,
      }, signal);
      if (
        resolved.snapshotToken !== snapshotToken ||
        resolved.outputId !== outputId
      ) {
        throw new Error('Loom returned construction choices for another table or catalog snapshot.');
      }
      for (const choice of resolved.choices) {
        if (!choicesById.has(choice.choiceId)) choicesById.set(choice.choiceId, choice);
      }
      choices = [...choicesById.values()];
      nextCursor = resolved.nextCursor;
      const usableChoices = item.kind === 'FIELD'
        ? choiceGroupsForRelatedSource([{
            item,
            choices,
            complete: resolved.complete,
            truncated: resolved.truncated,
            ...(resolved.nextCursor ? { nextCursor: resolved.nextCursor } : {}),
          }], rowRoot, relatedSourceAvailability)[0]?.choices ?? []
        : choices;
      if (usableChoices.length > 0) {
        if (nextCursor && seenCursors.has(nextCursor)) nextCursor = undefined;
        break;
      }
      if (nextCursor && seenCursors.has(nextCursor)) {
        throw new Error('Construction choice pagination repeated a page.');
      }
      if (!nextCursor) break;
      seenCursors.add(nextCursor);
      pageCursor = nextCursor;
    } while (nextCursor);
    if (!resolved) throw new Error('Loom did not return construction choices.');
    const finalGroup: CatalogChoiceGroup = {
      item,
      choices,
      complete: resolved.complete,
      truncated: resolved.truncated,
      ...(nextCursor ? { nextCursor } : {}),
    };
    if (!resolved.complete && !nextCursor && choicesForCurrentTable(finalGroup).length === 0) {
      throw new Error('Loom could not finish checking construction choices.');
    }
    return {
      item,
      choices,
      complete: resolved.complete,
      truncated: resolved.truncated,
      ...(nextCursor ? { nextCursor } : {}),
      ...(item.kind === 'SEMANTIC' ? {
        semanticContext: semanticContext ?? (semanticResponse ? {
          contextToken: semanticResponse.contextToken,
          buildId: semanticResponse.buildId,
        } : retainedSemanticContext),
      } : {}),
    };
  };

  const resolveSelectedGroups = async (
    items: ReadonlyArray<CatalogItem>,
    signal: AbortSignal,
  ): Promise<{ readonly groups: ReadonlyArray<CatalogChoiceGroup>; readonly unavailable: ReadonlyArray<CatalogItem> } | undefined> => {
    const groups = await Promise.all(items.map(async (item) => {
      const cached = choiceDetails.get(choiceDetailsKey(item));
      if (cached?.status === 'ready') return cached.group;
      return resolveChoiceGroup(item, signal);
    }));
    if (signal.aborted) return undefined;
    setChoiceDetails((previous) => {
      const next = new Map(previous);
      for (const group of groups) {
        next.set(choiceDetailsKey(group.item), { status: 'ready', group });
      }
      return next;
    });
    const unavailable = groups.filter((group) =>
      choicesForCurrentTable(group).length === 0 && group.complete &&
      !group.truncated && !group.nextCursor,
    ).map((group) => group.item);
    return {
      groups: groups.filter((group) => !unavailable.some((item) => catalogItemKey(item) === catalogItemKey(group.item))),
      unavailable,
    };
  };

  const defaultIntentForGroup = (group: CatalogChoiceGroup): CatalogChoiceIntent | undefined => {
    if (initialSelection?.condition) return undefined;
    const issuedChoice = catalogItemConstructionChoice(group.item);
    const savedChoice = initialSelection
      ? group.choices.find((choice) => choice.choiceId === initialSelection.choiceId)
      : undefined;
    const matchingIssuedChoice = issuedChoice
      ? group.choices.find((choice) => choice.choiceId === issuedChoice.choiceId)
      : undefined;
    const uniqueChoice = group.complete && !group.truncated && !group.nextCursor && group.choices.length === 1
      ? group.choices[0]
      : undefined;
    const choice = initialSelection
      ? savedChoice
      : matchingIssuedChoice ?? uniqueChoice;
    if (!choice) return undefined;
    const savedForm = initialSelection?.choiceId === choice.choiceId
      ? choice.options.find((option) => option.form === initialSelection.form && option.support === 'SUPPORTED')
      : undefined;
    const defaults = choice.options.filter((option) =>
      option.decision === 'DEFAULT' && option.support === 'SUPPORTED',
    );
    const form = savedForm?.form ?? (defaults.length === 1 ? defaults[0]?.form : undefined);
    if (!form) return undefined;
    const intent = catalogChoiceIntent({
      item: group.item,
      choice,
      form,
      rowRoot,
      ...(form === 'ALL' && groupedRowValuePolicy?.value === 'ONE'
        ? { rowValuePolicy: groupedRowValuePolicy.value }
        : {}),
    });
    if (isRelatedFieldCatalogItem(group.item, rowRoot)) {
      return relatedSourceAvailability?.supported === false || !intent.relatedSource
        ? undefined
        : intent;
    }
    return canAddFromSourceProjection ? intent : undefined;
  };

  const loadMoreChoices = async (group: CatalogChoiceGroup) => {
    if (!group.nextCursor || loadingMoreRoutes) return;
    const key = catalogItemKey(group.item);
    activeMoreRouteRequest.current?.controller.abort();
    const request = { controller: new AbortController(), tableContextKey };
    activeMoreRouteRequest.current = request;
    setLoadingMoreRoutes(key);
    setRouteLoadError(undefined);
    try {
      const page = await resolveChoiceGroup(group.item, request.controller.signal, group.nextCursor, group.semanticContext);
      if (
        request.controller.signal.aborted ||
        activeMoreRouteRequest.current !== request ||
        currentTableContextKey.current !== request.tableContextKey
      ) return;
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
      if (
        request.controller.signal.aborted ||
        activeMoreRouteRequest.current !== request ||
        currentTableContextKey.current !== request.tableContextKey
      ) return;
      setRouteLoadError({
        key,
        message: error instanceof Error ? error.message : 'Loom could not load more routes.',
      });
    } finally {
      if (activeMoreRouteRequest.current === request) {
        activeMoreRouteRequest.current = undefined;
        setLoadingMoreRoutes(undefined);
      }
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

  const submitIntents = async (intents: ReadonlyArray<CatalogChoiceIntent>) => {
    if (!onAddSelected || !intents.length) return;
    setSelectionPhase('submitting');
    setActionMessage(undefined);
    try {
      const result = await onAddSelected(intents);
      setSelected(new Map());
      setPendingSelection(undefined);
      setAdvancedOpen(false);
      setActionMessage(result === 'preview-ready' || result === 'preview-pending'
        ? 'Review the proposed rows, then apply the columns.'
        : addActionMessage(intents));
    } catch (error) {
      setActionMessage(error instanceof Error ? error.message : 'Loom could not add the selected features.');
    } finally {
      setSelectionPhase('idle');
    }
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
    await submitIntents(intents);
  };

  const loadAdvancedOptions = async () => {
    if (!selectedItems.length || !onAddSelected || !selectedItems.every(canAddCatalogItem)) return;
    setAdvancedOpen(true);
    if (pendingSelection?.length === selectedItems.length) return;
    setRouteLoadError(undefined);
    const controller = new AbortController();
    activeChoiceRequest.current?.abort();
    activeChoiceRequest.current = controller;
    setSelectionPhase('loading-options');
    setActionMessage(undefined);
    try {
      const resolution = await resolveSelectedGroups(selectedItems, controller.signal);
      if (!resolution || controller.signal.aborted) return;
      const unavailableKeys = new Set(resolution.unavailable.map(catalogItemKey));
      if (unavailableKeys.size > 0) {
        setSelected((current) => new Map([...current].filter(([key]) => !unavailableKeys.has(key))));
        setActionMessage(unavailableSelectionMessage(resolution.unavailable));
      }
      setPendingSelection(resolution.groups.length > 0 ? resolution.groups : undefined);
      if (resolution.groups.length === 0) setAdvancedOpen(false);
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
        setSelectionPhase('idle');
      }
    }
  };

  const openAdvancedOptions = () => {
    setActionMessage(undefined);
    void loadAdvancedOptions();
  };

  const openSelection = async () => {
    if (selectionPhase !== 'idle' || !selectedItems.length || !onAddSelected || !selectedItems.every(canAddCatalogItem)) return;
    setRouteLoadError(undefined);
    setActionMessage(undefined);
    const controller = new AbortController();
    activeChoiceRequest.current?.abort();
    activeChoiceRequest.current = controller;
    setSelectionPhase('loading-options');
    try {
      const resolution = await resolveSelectedGroups(selectedItems, controller.signal);
      if (!resolution || controller.signal.aborted) return;
      const unavailableKeys = new Set(resolution.unavailable.map(catalogItemKey));
      if (unavailableKeys.size > 0) {
        setSelected((current) => new Map([...current].filter(([key]) => !unavailableKeys.has(key))));
        setActionMessage(unavailableSelectionMessage(resolution.unavailable));
      }
      const groups = resolution.groups;
      setPendingSelection(groups.length > 0 ? groups : undefined);
      if (groups.length === 0) {
        setAdvancedOpen(false);
        return;
      }
      if (unavailableKeys.size > 0) {
        setAdvancedOpen(true);
        return;
      }
      const intents = groups.map(defaultIntentForGroup);
      const resolvedIntents = intents.filter(
        (intent): intent is CatalogChoiceIntent => intent !== undefined,
      );
      if (resolvedIntents.length === groups.length) {
        activeChoiceRequest.current = undefined;
        await submitIntents(resolvedIntents);
        return;
      }
      setAdvancedOpen(true);
    } catch (error) {
      if (controller.signal.aborted) return;
      setAdvancedOpen(true);
      setActionMessage(error instanceof Error
        ? error.message
        : 'Loom could not resolve the selected feature routes.');
    } finally {
      if (activeChoiceRequest.current === controller) {
        activeChoiceRequest.current = undefined;
        setSelectionPhase('idle');
      }
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

  const fieldSection = (
    <section className="mt-3" aria-label={view === 'fields' ? 'Dataset fields' : 'Available fields'}>
      {view !== 'fields' ? (
        <div className="flex items-center justify-between gap-3">
          <h3 id="feature-catalog-fields-title" className="text-sm font-semibold text-slate-800">
            {resourceType
              ? `Fields on ${resourceType}`
              : query ? 'Fields matching this search' : `Fields on ${rowRoot}`}
          </h3>
          <span className="text-xs text-slate-500">{fieldItems.length} available</span>
        </div>
      ) : null}
      <div className="mt-3 divide-y divide-slate-200">
        {fieldItems.length === 0 ? (
          <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-600">
            {view === 'fields'
              ? query ? 'No populated dataset fields match this search.' : 'No populated dataset fields are available for this output.'
              : 'No fields match this search.'}
          </p>
        ) : fieldGroups.map((group) => (
          <section key={group.nodeId} data-source-node-id={group.nodeId} className="py-2 first:pt-0">
            <h4 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500">
              Fields from {group.resourceType}
            </h4>
            {group.items.map((item) => (
              <CatalogItemRow
                key={catalogItemKey(item)}
                item={item}
                rowRoot={rowRoot}
                currentResourceType={resourceType}
                hasRouteContext={browseRouteContext !== undefined}
                checked={selected.has(catalogItemKey(item))}
                disabled={disabled || submitting || selected.size >= MAX_SELECTIONS && !selected.has(catalogItemKey(item))}
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
          </section>
        ))}
      </div>
    </section>
  );

  return (
    <section className="min-w-0 rounded-xl border border-slate-200 bg-white shadow-sm">
      <form className="flex gap-2 border-b border-slate-200 px-3 py-2" onSubmit={submitSearch}>
          <label className="sr-only" htmlFor="feature-catalog-search">
            {view === 'fields' ? 'Search dataset fields by name or type' : 'Search coded values by code or label'}
          </label>
          <input
            id="feature-catalog-search"
            type="search"
            aria-label={view === 'fields' ? 'Search dataset fields by name or type' : 'Search coded values by code or label'}
            value={queryInput}
            onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder={view === 'fields' ? 'Search a field name or type' : 'Search a code or label'}
            className="min-w-0 flex-1 rounded-md border border-slate-300 px-3 py-2 text-sm outline-blue-500 focus:border-blue-500"
          />
          <button
            type="submit"
            disabled={disabled || loadState.status === 'loading'}
            className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Search
          </button>
          {view === 'fields' ? (
            <span className="shrink-0 self-center text-xs text-slate-500" aria-live="polite">
              {fieldItems.length} available
            </span>
          ) : null}
        </form>
      {sourceControls ? (
        <div className="border-b border-slate-200 px-3 py-2">
          {sourceControls}
        </div>
      ) : null}

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
      {!suppressUnavailableNotices && relatedSourceAvailability?.supported === false ? (
        <p id={relatedSourceAvailabilityId} className="mx-3 mt-2 text-xs text-amber-900" role="status">
          Related fields are unavailable here: {relatedSourceAvailability.reason?.trim() || 'Loom has not confirmed that this stage supports related-source fields.'}
        </p>
      ) : null}
      {!suppressUnavailableNotices && sourceProjectionAvailability && !sourceProjectionAvailability.available ? (
        <p className="mx-3 mt-2 text-xs text-amber-900" role="status">
          Source fields are unavailable here: {sourceProjectionReason || 'Loom has not confirmed that source columns retain this stage’s row identity.'}
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
      {!pendingSelection && selected.size === 0 && actionMessage ? (
        <p className="mx-3 mt-2 text-sm text-slate-700" role="status">{actionMessage}</p>
      ) : null}
      {selected.size > 0 ? (
        <div className="mx-3 mt-3">
          <button
            type="button"
            aria-expanded={advancedOpen}
            disabled={disabled || submitting}
            onClick={() => {
              if (advancedOpen) {
                activeChoiceRequest.current?.abort();
                activeChoiceRequest.current = undefined;
                cancelLoadMoreRoutes();
                setPendingSelection(undefined);
                setSelectionPhase('idle');
                setAdvancedOpen(false);
                setActionMessage(undefined);
                return;
              }
              void openAdvancedOptions();
            }}
            className="text-sm font-medium text-blue-800 underline underline-offset-2 disabled:text-slate-400"
          >
            {advancedOpen ? 'Close advanced options' : 'Advanced options'}
          </button>
          {advancedOpen ? (
            <div className="mt-2 rounded-lg border border-slate-200 bg-white p-3">
              {loadingOptions && !pendingSelection ? (
                <p role="status" className="text-sm text-slate-600">Loading route and result form choices…</p>
              ) : pendingSelection ? (
                <CatalogSelectionOptions
                  groups={choiceGroupsForRelatedSource(
                    pendingSelection,
                    rowRoot,
                    relatedSourceAvailability,
                  )}
                  rowRoot={rowRoot}
                  initialSelection={initialSelection}
                  groupedRowValuePolicy={groupedRowValuePolicy}
                  busy={submitting || loadingMoreRoutes !== undefined}
                  loadingMoreRoutes={loadingMoreRoutes}
                  routeLoadError={routeLoadError}
                  submissionError={actionMessage}
                  onLoadMoreRoutes={(group) => void loadMoreChoices(group)}
                  onInspectRouteCoverage={onInspectRouteCoverage}
                  onCancel={() => {
                    activeChoiceRequest.current?.abort();
                    cancelLoadMoreRoutes();
                    setPendingSelection(undefined);
                    setAdvancedOpen(false);
                    setActionMessage(undefined);
                  }}
                  onConfirm={(selections) => void commitSelections(selections)}
                />
              ) : actionMessage ? (
                <p role="alert" className="text-sm text-red-800">{actionMessage}</p>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}

      <div className={`${layout === 'panel' ? 'grid min-h-[14rem]' : 'grid min-h-[24rem]'} ${selected.size > 0 ? 'pb-32 md:pb-0 md:pr-[20rem]' : ''}`}>
        <div className={layout === 'panel'
          ? 'min-w-0 border-b border-slate-200 p-4'
          : 'min-w-0 border-b border-slate-200 p-4 sm:p-5'}>
          {view !== 'fields' ? <section aria-label="Coded values">
          <div className="divide-y divide-slate-200">
            {loadState.status === 'loading' && !response ? (
                <p className="py-8 text-center text-sm text-slate-500">Loading concepts…</p>
            ) : null}
              {response && semanticItems.length === 0 ? (
                <p className="rounded-lg border border-dashed border-slate-300 bg-slate-50 px-4 py-8 text-center text-sm text-slate-600">
                {view === 'coded' && response.nextCursor
                  ? 'No coded values on this page. Use Next to continue through the catalog.'
                  : 'No concepts match this search.'}
              </p>
            ) : null}
              {semanticGroups.map((group) => (
                <section key={group.nodeId} data-source-node-id={group.nodeId} className="py-2 first:pt-0">
                  <h4 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-500">
                    Coded values from {group.resourceType}
                  </h4>
                  {group.items.map((item) => (
                    <CatalogItemRow
                      key={catalogItemKey(item)}
                      item={item}
                      rowRoot={rowRoot}
                      currentResourceType={resourceType}
                      hasRouteContext={browseRouteContext !== undefined}
                      checked={selected.has(catalogItemKey(item))}
                      disabled={disabled || submitting || !canBrowseConcepts || selected.size >= MAX_SELECTIONS && !selected.has(catalogItemKey(item))}
                      selectionDisabled={!canAddCatalogItem(item)}
                      disabledReasonId={visibleDisabledReasonId}
                      onToggle={() => toggleSelection(item)}
                      choiceDetails={choiceDetails.get(choiceDetailsKey(item))}
                      onInspectChoices={() => inspectChoices(item)}
                    />
                  ))}
                </section>
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
          </section> : null}

          {view === 'fields' ? (
            <div data-testid="feature-catalog-raw-fields" className="p-4">
              {fieldSection}
            </div>
          ) : view === 'all' ? (
            <details className="mt-6 border-t border-slate-200 pt-5" data-testid="feature-catalog-raw-fields" open={layout !== 'panel' ? true : undefined}
              onToggle={(event) => { if (layout === 'panel') setRawFieldsExpanded(event.currentTarget.open); }}>
              <summary className="cursor-pointer text-sm font-semibold text-slate-700">Fields</summary>
              <p className="mt-2 text-xs text-slate-600">Choose a dataset field such as a row ID, date, grouping field, or value without a coded pairing.</p>
              {fieldSection}
            </details>
          ) : null}
        </div>

        {selected.size > 0 ? (
          <aside aria-label="Selected feature action" className="fixed inset-x-4 bottom-4 z-30 grid w-auto gap-2 rounded-lg border border-slate-200 bg-white p-3 shadow-lg md:inset-x-auto md:right-4 md:top-1/2 md:bottom-auto md:w-[min(18rem,calc(100vw-2rem))] md:-translate-y-1/2 lg:translate-x-0">
            <p className="m-0 text-sm font-medium text-slate-700" role="status">
              {selected.size} {selected.size === 1 ? 'feature' : 'features'} selected
            </p>
            {selectedItems.length > 0 ? (
              <ul aria-label="Selected fields and coded values" className="grid max-h-28 gap-1 overflow-auto rounded-md border border-slate-200 bg-slate-50 p-2 text-xs">
                {selectedItems.map((item) => {
                  const label = catalogItemLabel(item);
                  return (
                    <li key={catalogItemKey(item)} className="flex items-center gap-2">
                      <span className="shrink-0 rounded bg-slate-100 px-1.5 py-0.5 font-medium text-slate-700">
                        {item.kind === 'SEMANTIC' ? 'Coded value' : 'Field'}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-slate-800">{label}</span>
                      <button
                        type="button"
                        aria-label={`Remove ${label} from selected features`}
                        disabled={disabled || submitting}
                        onClick={() => toggleSelection(item)}
                        className="shrink-0 text-blue-800 underline disabled:opacity-50"
                      >
                        Remove
                      </button>
                    </li>
                  );
                })}
              </ul>
            ) : null}
            {!advancedOpen ? (
              <button
                type="button"
                disabled={disabled || submitting || loadingOptions || selected.size === 0 || !onAddSelected || !selectedItems.every(canAddCatalogItem)}
                onClick={() => void openSelection()}
                className="rounded-md bg-blue-700 px-4 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50">
                {selected.size === 0
                  ? 'Add selected features'
                  : `Add ${selected.size} selected ${selected.size === 1 ? 'feature' : 'features'}`}
              </button>
            ) : null}
            {loadingOptions && !advancedOpen ? (
              <p className="m-0 text-sm text-slate-600" role="status">Checking route and form defaults…</p>
            ) : null}
            {actionMessage && !advancedOpen ? (
              <p
                className={`m-0 text-sm ${selected.size > 0 ? 'font-medium text-red-800' : 'text-slate-700'}`}
                role={selected.size > 0 ? 'alert' : 'status'}
              >
                {actionMessage}
              </p>
            ) : null}
          </aside>
        ) : null}
      </div>
    </section>
  );
};

export const ConceptCatalog = (props: React.ComponentProps<typeof ConceptCatalogContent>) => (
  <ConceptCatalogContent
    key={JSON.stringify([
      props.project, props.explorerId, props.authResourcePath ?? '', props.snapshotToken,
      props.outputId, props.rowRoot,
    ])}
    {...props}
  />
);
