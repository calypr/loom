import React, { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type {
  ConstructionChoice,
  ConstructionChoiceForm,
  ConstructionChoiceSelection,
} from '../../../types';
import {
  catalogItemDefaultForm,
  catalogChoiceIntent,
  catalogItemKey,
  catalogItemLabel,
  isRelatedFieldCatalogItem,
  type CatalogChoiceIntent,
  type CatalogChoiceGroup,
} from '../catalogItems';
import { routePath } from '../constructionWorkspace/routeDisplay';
import { TraversalPath } from '../constructionWorkspace/TraversalPath';

const optionLabel = (option: ConstructionChoice['options'][number]): string => {
  switch (option.form) {
    case 'VALUE': return 'Use the matching value';
    case 'FIRST': return 'Use the first value';
    case 'ALL': return 'Keep all matching values';
    case 'DISTINCT': return 'Keep unique values';
    case 'COUNT': return 'Count matching records';
    case 'PRESENCE': return 'Show whether a match exists';
    case 'OWNER_RECORDS': return 'Keep each matching record';
  }
};

const optionDescription = (option: ConstructionChoice['options'][number]): string => {
  switch (option.form) {
    case 'VALUE': return 'Each row gets the matching field value.';
    case 'FIRST': return 'Each row gets the first matching value.';
    case 'ALL': return 'Each row gets the matching values as a list; no matches produce an empty list.';
    case 'DISTINCT': return 'Each row gets one copy of each matching value.';
    case 'COUNT': return 'Each row gets the number of matching records (0 if none), counting each record once.';
    case 'PRESENCE': return 'Each row shows whether a match exists (false if none).';
    case 'OWNER_RECORDS': return 'Each row keeps its matching records together.';
  }
};

const routeLabel = (route: ConstructionChoice['route']): string => routePath(route);

const routeTechnicalDetails = (route: ConstructionChoice['route']): string => route.length === 0
  ? 'This field is on the same resource as each table row.'
  : route.map((step) =>
      `${step.fromResourceType} to ${step.toResourceType} via ${step.relationship}; storage ${step.storageDirection.toLowerCase()}; match ${step.matchMode.toLowerCase()}`,
    ).join(' · ');

const routeCoverageForm = (
  item: CatalogChoiceGroup['item'],
  choice: ConstructionChoice,
  rowRoot: string,
  groupedRows: boolean,
  initialSelection?: CatalogInitialSelection,
): 'COUNT' | 'ALL' | undefined => {
  if (item.kind === 'FIELD' && isRelatedFieldCatalogItem(item, rowRoot)) {
    const savedForm = choice.choiceId === initialSelection?.choiceId ? initialSelection.form : undefined;
    if ((savedForm === 'COUNT' || savedForm === 'ALL') &&
      choice.options.some((option) => option.form === savedForm && option.support === 'SUPPORTED')) return savedForm;
    if (groupedRows && choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
    if (choice.options.some((option) => option.form === 'COUNT' && option.support === 'SUPPORTED')) return 'COUNT';
    if (choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
    return undefined;
  }
  if (item.kind === 'SEMANTIC' && choice.route.length > 0 &&
    choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
  return undefined;
};

type ConditionDraft = { readonly mode: 'ALL' | 'EXISTS' | 'EQUALS'; readonly value: string };
export type RouteMatchCoverage = {
  readonly zero: number;
  readonly one: number;
  readonly many: number;
  readonly displayedRows: number;
  readonly sampled: boolean;
};
export type RouteValueCoverage = {
  readonly kind: 'VALUES';
  readonly empty: number;
  readonly one: number;
  readonly many: number;
  readonly displayedRows: number;
  readonly sampled: boolean;
};
export type RouteCoverage = RouteMatchCoverage | RouteValueCoverage;
type RouteCoverageState =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly coverage: RouteCoverage }
  | { readonly status: 'error'; readonly message: string };
export type CatalogInitialSelection = {
  readonly choiceId: string;
  readonly form: ConstructionChoiceForm;
  readonly condition?: ConditionDraft;
};

type ConstructionRowValuePolicy = NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;

export type GroupedRowValuePolicyControl = Readonly<{
  value: ConstructionRowValuePolicy;
  onChange: (value: ConstructionRowValuePolicy) => void;
}>;

export const CatalogSelectionDialog = ({
  groups,
  rowRoot,
  initialSelection,
  groupedRowValuePolicy,
  busy,
  loadingMoreRoutes,
  routeLoadError,
  onLoadMoreRoutes,
  onInspectRouteCoverage,
  onCancel,
  onConfirm,
}: {
  readonly groups: ReadonlyArray<CatalogChoiceGroup>;
  readonly rowRoot: string;
  readonly initialSelection?: CatalogInitialSelection;
  readonly groupedRowValuePolicy?: GroupedRowValuePolicyControl;
  readonly busy: boolean;
  readonly loadingMoreRoutes?: string;
  readonly routeLoadError?: { readonly key: string; readonly message: string };
  readonly onLoadMoreRoutes: (group: CatalogChoiceGroup) => void;
  readonly onInspectRouteCoverage?: (selection: CatalogChoiceIntent, signal: AbortSignal) => Promise<RouteCoverage>;
  readonly onCancel: () => void;
  readonly onConfirm: (selections: ReadonlyArray<CatalogChoiceIntent>) => void;
}) => {
  const [choiceIDs, setChoiceIDs] = useState<ReadonlyMap<string, string>>(
    () => new Map(
      groups.flatMap((group) => {
        const savedChoice = group.choices.find((choice) => choice.choiceId === initialSelection?.choiceId);
        const defaultChoice = group.choices.length === 1 && !group.truncated && !group.nextCursor
          ? group.choices[0]
          : undefined;
        const choiceId = savedChoice?.choiceId ?? defaultChoice?.choiceId;
        return choiceId ? [[catalogItemKey(group.item), choiceId]] : [];
      }),
    ),
  );
  const [forms, setForms] = useState<ReadonlyMap<string, ConstructionChoiceForm>>(
    () => new Map(
      groups.flatMap((group) =>
        group.choices.flatMap((choice) => {
          const form = choice.choiceId === initialSelection?.choiceId &&
            choice.options.some((option) => option.form === initialSelection.form)
            ? initialSelection.form
            : catalogItemDefaultForm(choice);
          return form ? [[choice.choiceId, form] as const] : [];
        }),
      ),
    ),
  );
  const [conditions, setConditions] = useState<ReadonlyMap<string, ConditionDraft>>(
    () => initialSelection?.condition
      ? new Map([[initialSelection.choiceId, initialSelection.condition]])
      : new Map(),
  );
  const [unresolvedConditions, setUnresolvedConditions] = useState<ReadonlyMap<string, ConditionDraft>>(
    () => new Map(),
  );
  const [routeCoverage, setRouteCoverage] = useState<ReadonlyMap<string, RouteCoverageState>>(() => new Map());
  const coverageRequests = useRef<Map<string, AbortController>>(new Map());
  const inspectRouteCoverage = (group: CatalogChoiceGroup, choice: ConstructionChoice) => {
    const form = routeCoverageForm(group.item, choice, rowRoot, groupedRowValuePolicy !== undefined, initialSelection);
    if (!onInspectRouteCoverage || !form || coverageRequests.current.has(choice.choiceId)) return;
    const selection = catalogChoiceIntent({ item: group.item, choice, form, rowRoot });
    if (form === 'COUNT' && !selection.relatedSource) return;
    const controller = new AbortController();
    coverageRequests.current.set(choice.choiceId, controller);
    setRouteCoverage((current) => new Map(current).set(choice.choiceId, { status: 'loading' }));
    void onInspectRouteCoverage(selection, controller.signal).then((coverage) => {
      if (!controller.signal.aborted) {
        setRouteCoverage((current) => new Map(current).set(choice.choiceId, { status: 'ready', coverage }));
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setRouteCoverage((current) => new Map(current).set(choice.choiceId, {
          status: 'error',
          message: error instanceof Error ? error.message : 'Route coverage is unavailable.',
        }));
      }
    }).finally(() => {
      if (coverageRequests.current.get(choice.choiceId) === controller) coverageRequests.current.delete(choice.choiceId);
    });
  };

  useEffect(() => {
    const automatic = groups.flatMap((group) => {
      const shortest = Math.min(...group.choices.map((choice) => choice.route.length));
      return group.choices.filter((choice) => choice.route.length === shortest).map((choice) => ({ group, choice }));
    }).slice(0, 4);
    automatic.forEach(({ group, choice }) => inspectRouteCoverage(group, choice));
    return () => coverageRequests.current.forEach((controller) => controller.abort());
  }, []);

  const complete = groups.every((group) => {
    const itemKey = catalogItemKey(group.item);
    const choice = group.choices.find(
      (candidate) => candidate.choiceId === choiceIDs.get(itemKey),
    );
    const form = choice ? forms.get(choice.choiceId) : undefined;
    const option = choice?.options.find((candidate) => candidate.form === form);
    const condition = choice ? conditions.get(choice.choiceId) : undefined;
    return Boolean(
      !unresolvedConditions.has(itemKey) &&
      choice &&
      form &&
      option &&
      (!condition || condition.mode === 'ALL' || option.contributorPredicateOperators?.includes(condition.mode)) &&
      (condition?.mode !== 'EQUALS' || Boolean(condition.value.trim()))
    );
  });

  const confirm = () => {
    if (!complete) return;
    const selections = groups.flatMap((group) => {
      const choice = group.choices.find(
        (candidate) => candidate.choiceId === choiceIDs.get(catalogItemKey(group.item)),
      );
      const form = choice ? forms.get(choice.choiceId) : undefined;
      if (!choice || !form) return [];
      const option = choice.options.find((candidate) => candidate.form === form);
      const condition = conditions.get(choice.choiceId);
      return [
        {
          constructionChoice: { choiceId: choice.choiceId, form },
          title: catalogItemLabel(group.item),
          ...(choice.source.kind === 'FIELD' && option?.contributorPredicateOperators?.includes('EXISTS') && condition?.mode === 'EXISTS'
            ? { contributorPredicate: { candidateId: choice.source.candidateId, operator: 'EXISTS' as const } }
            : choice.source.kind === 'FIELD' && option?.contributorPredicateOperators?.includes('EQUALS') && condition?.mode === 'EQUALS'
              ? { contributorPredicate: {
                  candidateId: choice.source.candidateId,
                  operator: 'EQUALS' as const,
                  value: group.item.kind === 'FIELD' && group.item.candidate.logicalType.toLowerCase() === 'code'
                    ? { kind: 'CODE' as const, code: { code: condition.value } }
                    : { kind: 'STRING' as const, string: condition.value },
                } }
              : {}),
        },
      ];
    });
    if (selections.length === groups.length) onConfirm(selections);
  };

  return createPortal((
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-slate-950/45 p-4" role="presentation">
      <section
        aria-labelledby="catalog-selection-dialog-title"
        aria-modal="true"
        className="flex max-h-[90vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl bg-white p-5 shadow-2xl"
        role="dialog"
      >
        <h2 id="catalog-selection-dialog-title" className="text-xl font-semibold text-slate-950">
          Choose how to add these fields
        </h2>
        <p className="mt-1 text-sm text-slate-600">
          Choose where each field comes from and how it should appear in the table. When there are multiple paths, Loom leaves the choice open for you.
        </p>
        {groupedRowValuePolicy ? (
          <label className="mt-3 flex flex-wrap items-center gap-2 text-sm text-slate-700">
            <span className="font-medium">Values per grouped row</span>
            <select
              aria-label="Values per grouped row"
              data-testid="catalog-grouped-row-value-policy"
              value={groupedRowValuePolicy.value}
              disabled={busy}
              onChange={(event) => {
                const value = event.currentTarget.value;
                if (value === 'ALL' || value === 'ONE') groupedRowValuePolicy.onChange(value);
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5"
            >
              <option value="ALL">Keep all distinct values</option>
              <option value="ONE">Require one distinct value</option>
            </select>
            <span className="text-xs text-slate-500">Missing values stay empty. If values disagree, switch to all distinct values and retry.</span>
          </label>
        ) : null}
        <div className="mt-4 min-h-0 space-y-3 overflow-y-auto pr-1">
          {groups.map((group) => {
            const { item } = group;
            const key = catalogItemKey(item);
            const choice = group.choices.find(
              (candidate) => candidate.choiceId === choiceIDs.get(key),
            );
            if (group.choices.length === 0) {
              return (
                <article key={key} className="rounded-lg border border-slate-200 p-3">
                  <h3 className="font-semibold text-slate-900">{catalogItemLabel(item)}</h3>
                  <p className="mt-2 text-sm text-amber-900">No verified path or table value is available for this field.</p>
                  {group.nextCursor ? (
                    <button type="button" disabled={busy} onClick={() => onLoadMoreRoutes(group)} className="mt-3 rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-blue-700 disabled:opacity-50">
                      {loadingMoreRoutes === key ? 'Checking for more paths…' : 'Load more paths'}
                    </button>
                  ) : null}
                  {routeLoadError?.key === key ? <p role="alert" className="mt-2 text-sm text-red-800">{routeLoadError.message}</p> : null}
                </article>
              );
            }
            const selectedForm = choice ? forms.get(choice.choiceId) : undefined;
            const selectedOption = choice?.options.find((option) => option.form === selectedForm);
            const predicateOperators = selectedOption?.contributorPredicateOperators ?? [];
            const unresolvedCondition = unresolvedConditions.get(key);
            const selectedCondition = choice ? conditions.get(choice.choiceId) : undefined;
            const invalidCondition = unresolvedCondition ?? (
              selectedCondition && selectedCondition.mode !== 'ALL' && !predicateOperators.includes(selectedCondition.mode)
                ? selectedCondition
                : undefined
            );
            return (
              <article key={key} className="rounded-lg border border-slate-200 p-3">
                <h3 className="font-semibold text-slate-900">
                  {group.choices[0]?.source.resourceType ? `${group.choices[0].source.resourceType} · ` : ''}{catalogItemLabel(item)}
                </h3>
                {group.truncated ? (
                  <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
                    {group.nextCursor
                      ? 'More relationship paths are available. Load them if the path you need is not shown.'
                      : 'Path search reached its limit. Additional relationship paths may exist.'}
                  </p>
                ) : null}
                {group.choices.length > 1 || group.truncated || Boolean(group.nextCursor) ? (
                  <fieldset className="mt-3 space-y-2" disabled={busy}>
                    <legend className="text-sm font-medium text-slate-800">Choose a relationship path for this field</legend>
                    {(() => {
                      const orderedChoices = group.choices
                        .map((routeChoice, originalIndex) => ({ routeChoice, originalIndex }))
                        .sort((left, right) => left.routeChoice.route.length - right.routeChoice.route.length || left.originalIndex - right.originalIndex);
                      const shortestLength = orderedChoices[0]?.routeChoice.route.length ?? 0;
                      const shortestChoices = orderedChoices.filter(({ routeChoice }) => routeChoice.route.length === shortestLength);
                      const otherChoices = orderedChoices.filter(({ routeChoice }) => routeChoice.route.length > shortestLength);
                      const selectedChoice = orderedChoices.find(({ routeChoice }) => routeChoice.choiceId === choice?.choiceId);
                      const visibleChoices = selectedChoice ? [selectedChoice] : shortestChoices;
                      const alternateChoices = selectedChoice
                        ? orderedChoices.filter(({ routeChoice }) => routeChoice.choiceId !== selectedChoice.routeChoice.choiceId)
                        : otherChoices;
                      const renderRouteChoice = ({ routeChoice }: typeof orderedChoices[number]) => {
                        const label = routeLabel(routeChoice.route);
                        const matchCoverage = routeCoverage.get(routeChoice.choiceId);
                        const coverageForm = routeCoverageForm(item, routeChoice, rowRoot, groupedRowValuePolicy !== undefined, initialSelection);
                        const canInspectMatches = Boolean(onInspectRouteCoverage && coverageForm);
                        const coverageLabel = item.kind === 'SEMANTIC'
                          ? 'paired values'
                          : isRelatedFieldCatalogItem(item, rowRoot) ? 'related values' : 'contributing values';
                        return (
                          <div key={routeChoice.choiceId} className="rounded-md border border-slate-200 p-2.5">
                            <label className="flex cursor-pointer items-start gap-2">
                              <input
                                type="radio"
                                name={`construction-route-${key}`}
                                aria-label={`${catalogItemLabel(item)}: ${label}`}
                                checked={choice?.choiceId === routeChoice.choiceId}
                                onChange={() => {
                                  const currentForm = choice ? forms.get(choice.choiceId) : undefined;
                                  const retainedForm = currentForm && routeChoice.options.some(
                                    (option) => option.form === currentForm && option.support === 'SUPPORTED',
                                  )
                                    ? currentForm
                                    : undefined;
                                  const nextForm = retainedForm ?? catalogItemDefaultForm(routeChoice);
                                  const currentCondition = unresolvedCondition ?? (choice ? conditions.get(choice.choiceId) : undefined);
                                  const nextOption = routeChoice.options.find((option) => option.form === nextForm);
                                  const canTransferCondition = !currentCondition ||
                                    currentCondition.mode === 'ALL' ||
                                    Boolean(nextOption?.contributorPredicateOperators?.includes(currentCondition.mode));
                                  setChoiceIDs((current) => new Map(current).set(key, routeChoice.choiceId));
                                  setForms((current) => {
                                    const next = new Map(current);
                                    if (nextForm) next.set(routeChoice.choiceId, nextForm);
                                    else next.delete(routeChoice.choiceId);
                                    return next;
                                  });
                                  setConditions((current) => {
                                    const next = new Map(current);
                                    if (currentCondition && canTransferCondition) {
                                      next.set(routeChoice.choiceId, currentCondition);
                                    } else {
                                      next.delete(routeChoice.choiceId);
                                    }
                                    return next;
                                  });
                                  setUnresolvedConditions((current) => {
                                    const next = new Map(current);
                                    if (currentCondition && currentCondition.mode !== 'ALL' && !canTransferCondition) {
                                      next.set(key, currentCondition);
                                    } else {
                                      next.delete(key);
                                    }
                                    return next;
                                  });
                                }}
                                className="mt-1 h-4 w-4 border-slate-300 text-blue-700"
                              />
                              <span className="min-w-0 flex-1"><TraversalPath route={routeChoice.route} referenceRoute={choice?.route ?? orderedChoices[0]?.routeChoice.route} /></span>
                            </label>

                            {canInspectMatches ? (
                              <div data-testid={`catalog-route-coverage-${routeChoice.choiceId}`} className="ml-6 mt-2 text-xs text-slate-700">
                                {matchCoverage?.status === 'ready' ? (
                                  'kind' in matchCoverage.coverage ? (
                                    <p>
                                      In {matchCoverage.coverage.displayedRows} displayed {matchCoverage.coverage.displayedRows === 1 ? 'row' : 'rows'}: {matchCoverage.coverage.empty} without this value, {matchCoverage.coverage.one} with one value, {matchCoverage.coverage.many} with two or more values.
                                      {item.kind === 'SEMANTIC'
                                        ? ' This counts paired values, not matching records.'
                                        : isRelatedFieldCatalogItem(item, rowRoot)
                                          ? ' This counts values from matching related records, not matching records.'
                                          : ' This counts distinct contributing values, not matching records.'}
                                      {matchCoverage.coverage.sampled ? ' This is a sample; full-table coverage has not been measured.' : ''}
                                    </p>
                                  ) : (
                                    <p>
                                      In {matchCoverage.coverage.displayedRows} displayed {matchCoverage.coverage.displayedRows === 1 ? 'row' : 'rows'}: {matchCoverage.coverage.zero} with no match, {matchCoverage.coverage.one} with one, {matchCoverage.coverage.many} with two or more.
                                      {matchCoverage.coverage.sampled ? ' This is a sample; full-table coverage has not been measured.' : ''}
                                    </p>
                                  )
                                ) : matchCoverage?.status === 'loading' ? (
                                  <p role="status">Checking {coverageForm === 'ALL' ? coverageLabel : 'matching records'} in preview rows…</p>
                                ) : (
                                  <>
                                    {matchCoverage?.status === 'error' ? <p role="status">{matchCoverage.message}</p> : null}
                                    <button type="button" disabled={busy} onClick={() => inspectRouteCoverage(group, routeChoice)} className="font-medium text-blue-800 underline underline-offset-2 disabled:text-slate-400">
                                      {matchCoverage?.status === 'error' ? 'Retry coverage check' : coverageForm === 'ALL' ? `Check ${coverageLabel}` : 'Check matching rows'}
                                    </button>
                                  </>
                                )}
                              </div>
                            ) : routeChoice.route.length > 0 ? (
                              <p className="ml-6 mt-2 text-xs text-slate-500">Current-row coverage appears after preview.</p>
                            ) : null}
                            <details className="ml-6 mt-2 text-xs text-slate-600">
                              <summary className="cursor-pointer font-medium text-blue-700">Technical path details</summary>
                              <p className="mt-2 font-medium">{routeChoice.presentation.summary}</p>
                              <p className="mt-2">{routeTechnicalDetails(routeChoice.route)}</p>
                              {routeChoice.route.some((step) => step.matchMode === 'OPTIONAL') ? (
                                <p className="mt-1">This path permits rows with no matching related record.</p>
                              ) : null}
                              <dl className="mt-2 grid gap-1 rounded-md bg-slate-50 p-2">
                                {routeChoice.presentation.facts.map((fact) => (
                                  <div key={`${fact.label}:${fact.value}`}>
                                    <dt className="inline text-slate-500">{fact.label} </dt>
                                    <dd className="inline break-all">{fact.value}</dd>
                                  </div>
                                ))}
                              </dl>
                            </details>
                          </div>
                        );
                      };
                      return (
                        <>
                          {visibleChoices.map(renderRouteChoice)}
                          {alternateChoices.length > 0 || (selectedChoice && group.nextCursor) ? (
                            <details data-testid={`catalog-route-alternatives-${key}`} className="rounded-md border border-slate-200 px-3 py-2">
                              <summary className="cursor-pointer text-sm font-semibold text-blue-800">
                                {selectedChoice
                                  ? alternateChoices.length > 0
                                    ? `Change relationship path (${alternateChoices.length} alternatives)`
                                    : 'Find more relationship paths'
                                  : `Other relationship paths (${alternateChoices.length})`}
                              </summary>
                              <div className="mt-2 space-y-2">
                                {alternateChoices.map(renderRouteChoice)}
                                {selectedChoice && group.nextCursor ? (
                                  <button type="button" disabled={busy} onClick={() => onLoadMoreRoutes(group)} className="justify-self-start rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-blue-700 disabled:opacity-50">
                                    {loadingMoreRoutes === key ? 'Checking for more paths…' : 'Load more paths'}
                                  </button>
                                ) : null}
                              </div>
                            </details>
                          ) : null}
                        </>
                      );
                    })()}
                  </fieldset>
                ) : null}
                {group.nextCursor && !choice ? (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => onLoadMoreRoutes(group)}
                    className="mt-3 rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-blue-700 disabled:opacity-50"
                  >
                    {loadingMoreRoutes === key ? 'Checking for more paths…' : 'Load more paths'}
                  </button>
                ) : null}
                {routeLoadError?.key === key ? (
                  <p role="alert" className="mt-2 text-sm text-red-800">{routeLoadError.message}</p>
                ) : null}
                {choice && group.choices.length === 1 ? (
                  <div className="mt-2 rounded-md bg-blue-50 p-2 text-sm text-slate-700">
                    <TraversalPath route={choice.route} />
                    <details className="mt-2 text-xs text-slate-600">
                      <summary className="cursor-pointer font-medium text-blue-700">Technical path details</summary>
                      <p className="mt-2 font-medium">{choice.presentation.summary}</p>
                      <p className="mt-2">{routeTechnicalDetails(choice.route)}</p>
                      {choice.route.some((step) => step.matchMode === 'OPTIONAL') ? (
                        <p className="mt-1">This path permits rows with no matching related record.</p>
                      ) : null}
                      <dl className="mt-2 grid gap-1 rounded-md bg-white p-2">
                        {choice.presentation.facts.map((fact) => (
                          <div key={`${fact.label}:${fact.value}`}>
                            <dt className="inline text-slate-500">{fact.label} </dt>
                            <dd className="inline break-all">{fact.value}</dd>
                          </div>
                        ))}
                      </dl>
                    </details>
                  </div>
                ) : null}
                {choice ? <fieldset className="mt-3 space-y-2" disabled={busy}>
                  <legend className="text-sm font-medium text-slate-800">Choose how this field appears in the table</legend>
                  {choice.options.map((option) => {
                    const label = optionLabel(option);
                    return (
                      <div key={option.form} className="rounded-md border border-slate-200 p-2.5">
                        <label className="flex cursor-pointer items-start gap-2">
                          <input
                            type="radio"
                            name={`construction-choice-${choice.choiceId}`}
                            aria-label={`${catalogItemLabel(item)}: ${label}`}
                            checked={selectedForm === option.form}
                            onChange={() => {
                              setForms((current) => new Map(current).set(choice.choiceId, option.form));
                              if (unresolvedCondition && (unresolvedCondition.mode === 'ALL' || option.contributorPredicateOperators?.includes(unresolvedCondition.mode))) {
                                setConditions((current) => new Map(current).set(choice.choiceId, unresolvedCondition));
                                setUnresolvedConditions((current) => {
                                  const next = new Map(current);
                                  next.delete(key);
                                  return next;
                                });
                              }
                            }}
                            className="mt-1 h-4 w-4 border-slate-300 text-blue-700"
                          />
                          <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-center gap-2 text-sm font-semibold text-slate-900">
                            {label}
                            {option.decision === 'DEFAULT' ? (
                              <span className="rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-blue-800">Default</span>
                            ) : null}
                          </span>
                          <span className="mt-1 block text-xs text-slate-600">{optionDescription(option)}</span>
                          <span className="mt-1 block text-xs text-slate-600">What to expect: {option.reason}</span>
                          </span>
                        </label>
                        <details className="ml-6 mt-1 text-xs text-slate-600">
                          <summary className="cursor-pointer font-medium text-blue-700">Technical form details</summary>
                          <dl className="mt-1 grid gap-1 rounded-md bg-slate-50 p-2">
                            <div><dt className="inline text-slate-500">Form </dt><dd className="inline">{option.form}</dd></div>
                            <div><dt className="inline text-slate-500">Shape </dt><dd className="inline">{option.shape}</dd></div>
                            <div><dt className="inline text-slate-500">Preservation </dt><dd className="inline">{option.preservation}</dd></div>
                            <div><dt className="inline text-slate-500">Decision </dt><dd className="inline">{option.decision}</dd></div>
                            <div><dt className="inline text-slate-500">Row effect </dt><dd className="inline">{option.rowEffect}</dd></div>
                            <div><dt className="inline text-slate-500">Support </dt><dd className="inline">{option.support}</dd></div>
                          </dl>
                        </details>
                      </div>
                    );
                  })}
                </fieldset> : null}
                {invalidCondition ? (
                  <p role="alert" className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950">
                    The {invalidCondition.mode === 'EQUALS'
                      ? `exact-value condition for ${catalogItemLabel(item)} (${invalidCondition.value})`
                      : `${catalogItemLabel(item)} value-exists condition`} is not supported by this route and result form. Choose a supported matching rule or use all related records before adding this field.
                    <button
                      type="button"
                      className="ml-2 font-semibold underline"
                      onClick={() => {
                        if (!choice) return;
                        setConditions((current) => new Map(current).set(choice.choiceId, { mode: 'ALL', value: '' }));
                        setUnresolvedConditions((current) => {
                          const next = new Map(current);
                          next.delete(key);
                          return next;
                        });
                      }}
                    >Use all related records</button>
                  </p>
                ) : null}
                {choice?.route.length && item.kind === 'FIELD' && (predicateOperators.length > 0 || invalidCondition) ? (
                  <details data-testid={`catalog-matching-advanced-${key}`} className="mt-3 rounded-md border border-slate-200 px-3 py-2">
                    <summary className="cursor-pointer text-sm font-medium text-slate-800">
                      Matching records: {selectedCondition?.mode === 'EXISTS'
                        ? `only records with ${catalogItemLabel(item)}`
                        : selectedCondition?.mode === 'EQUALS'
                          ? `only records where ${catalogItemLabel(item)} equals ${selectedCondition.value}`
                          : 'all related records'} · Change
                    </summary>
                  <fieldset className="mt-3 space-y-2" disabled={busy}>
                    <legend className="text-sm font-medium text-slate-800">Matching records</legend>
                    <p className="text-xs text-slate-600">This condition selects related records. It does not remove table rows.</p>
                    {([
                      ['ALL', 'All related records'],
                      ...(predicateOperators.includes('EXISTS')
                        ? [['EXISTS', `Only records with ${catalogItemLabel(item)}`] as const]
                        : []),
                      ...(predicateOperators.includes('EQUALS')
                        ? [['EQUALS', `Only records where ${catalogItemLabel(item)} equals`] as const]
                        : []),
                    ] as const).map(([mode, label]) => (
                      <label key={mode} className="flex items-center gap-2 text-sm text-slate-800">
                        <input
                          type="radio"
                          name={`construction-condition-${choice.choiceId}`}
                          checked={(conditions.get(choice.choiceId)?.mode ?? 'ALL') === mode}
                          onChange={() => {
                            setConditions((current) => new Map(current).set(choice.choiceId, {
                              mode,
                              value: current.get(choice.choiceId)?.value ?? '',
                            }));
                            setUnresolvedConditions((current) => {
                              const next = new Map(current);
                              next.delete(key);
                              return next;
                            });
                          }}
                        />
                        {label}
                      </label>
                    ))}
                    {conditions.get(choice.choiceId)?.mode === 'EQUALS' ? (
                      <input
                        aria-label={`${catalogItemLabel(item)} exact value`}
                        value={conditions.get(choice.choiceId)?.value ?? ''}
                        onChange={(event) => {
                          const value = event.currentTarget.value;
                          setConditions((current) => new Map(current).set(choice.choiceId, {
                            mode: 'EQUALS', value,
                          }));
                        }}
                        placeholder="Exact value"
                        className="w-full rounded border border-slate-300 px-2 py-1.5 text-sm"
                      />
                    ) : null}
                  </fieldset>
                  </details>
                ) : null}
              </article>
            );
          })}
        </div>
        <div className="mt-4 flex shrink-0 justify-end gap-2 border-t border-slate-200 pt-4">
          <button type="button" disabled={busy} onClick={onCancel} className="rounded-md border border-slate-300 px-4 py-2 text-sm font-semibold text-slate-700 disabled:opacity-50">
            Cancel
          </button>
          <button type="button" disabled={busy || !complete} onClick={confirm} className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">
            {busy ? 'Adding columns…' : `Add ${groups.length} ${groups.length === 1 ? 'column' : 'columns'}`}
          </button>
        </div>
      </section>
    </div>
  ), document.body);
};
