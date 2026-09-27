import React, { useState } from 'react';
import type {
  ConstructionChoice,
  ConstructionChoiceForm,
} from '../../../types';
import {
  catalogItemDefaultForm,
  catalogItemKey,
  catalogItemLabel,
  type CatalogChoiceIntent,
  type CatalogChoiceGroup,
} from '../catalogItems';

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
    case 'VALUE': return 'Place the value from the selected record in the table.';
    case 'FIRST': return 'When there are several values, use the first one.';
    case 'ALL': return 'Keep every value found for the selected field.';
    case 'DISTINCT': return 'Keep one copy of each value.';
    case 'COUNT': return 'Count matching records, with each record counted once.';
    case 'PRESENCE': return 'Show whether at least one matching record exists.';
    case 'OWNER_RECORDS': return 'Keep matching records together for this row.';
  }
};

const readableRelationship = (
  relationship: string,
  endpointResourceTypes: ReadonlyArray<string>,
): string => {
  const withoutResourceSuffix = endpointResourceTypes.reduce((current, resourceType) => {
    const suffix = `_${resourceType}`;
    const hyphenSuffix = `-${resourceType}`;
    if (current.endsWith(suffix)) return current.slice(0, -suffix.length);
    if (current.endsWith(hyphenSuffix)) return current.slice(0, -hyphenSuffix.length);
    return current;
  }, relationship);
  return (withoutResourceSuffix || relationship)
  .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
  .replace(/[_-]+/g, ' ')
  .replace(/\s+/g, ' ')
  .trim()
  .replace(/^./, (first) => first.toUpperCase());
};

const routeLabel = (route: ConstructionChoice['route']): string => {
  if (route.length === 0) return 'Same resource as each table row';
  const resources = [route[0]!.fromResourceType, ...route.map((step) => step.toResourceType)];
  const relationships = route.map((step) => readableRelationship(
    step.relationship,
    [step.fromResourceType, step.toResourceType],
  )).join(' then ');
  const path = resources.join(' to ');
  return route.length === 1
    ? `Direct relationship: ${path} via ${relationships}`
    : `${route.length}-relationship path: ${path} via ${relationships}`;
};

const routeTechnicalDetails = (route: ConstructionChoice['route']): string => route.length === 0
  ? 'This field is on the same resource as each table row.'
  : route.map((step) =>
      `${step.fromResourceType} to ${step.toResourceType} via ${step.relationship}; storage ${step.storageDirection.toLowerCase()}; match ${step.matchMode.toLowerCase()}`,
    ).join(' · ');

type ConditionDraft = { readonly mode: 'ALL' | 'EXISTS' | 'EQUALS'; readonly value: string };
export type CatalogInitialSelection = {
  readonly choiceId: string;
  readonly form: ConstructionChoiceForm;
  readonly condition?: ConditionDraft;
};

export const CatalogSelectionDialog = ({
  groups,
  initialSelection,
  busy,
  loadingMoreRoutes,
  routeLoadError,
  onLoadMoreRoutes,
  onCancel,
  onConfirm,
}: {
  readonly groups: ReadonlyArray<CatalogChoiceGroup>;
  readonly initialSelection?: CatalogInitialSelection;
  readonly busy: boolean;
  readonly loadingMoreRoutes?: string;
  readonly routeLoadError?: { readonly key: string; readonly message: string };
  readonly onLoadMoreRoutes: (group: CatalogChoiceGroup) => void;
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

  return (
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
                      const renderRouteChoice = ({ routeChoice }: typeof orderedChoices[number]) => {
                        const label = routeLabel(routeChoice.route);
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
                              <span className="min-w-0 flex-1 text-sm font-semibold text-slate-900">{label}</span>
                            </label>
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
                          {shortestChoices.map(renderRouteChoice)}
                          {otherChoices.length > 0 ? (
                            <details className="rounded-md border border-slate-200 px-3 py-2">
                              <summary className="cursor-pointer text-sm font-semibold text-blue-800">
                                Other relationship paths ({otherChoices.length})
                              </summary>
                              <div className="mt-2 space-y-2">
                                {otherChoices.map(renderRouteChoice)}
                              </div>
                            </details>
                          ) : null}
                        </>
                      );
                    })()}
                  </fieldset>
                ) : null}
                {group.nextCursor ? (
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
                    <span className="font-semibold text-blue-900">Path: </span>{routeLabel(choice.route)}
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
  );
};
