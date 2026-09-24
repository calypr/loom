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
  if (option.form === 'OWNER_RECORDS') return 'Keep each matching record';
  return `${option.shape} · ${option.preservation} · ${option.form}`;
};

export const CatalogSelectionDialog = ({
  groups,
  busy,
  showRouteDetails,
  onCancel,
  onConfirm,
}: {
  readonly groups: ReadonlyArray<CatalogChoiceGroup>;
  readonly busy: boolean;
  readonly showRouteDetails: boolean;
  readonly onCancel: () => void;
  readonly onConfirm: (selections: ReadonlyArray<CatalogChoiceIntent>) => void;
}) => {
  const [choiceIDs, setChoiceIDs] = useState<ReadonlyMap<string, string>>(
    () => new Map(
      groups.flatMap((group) =>
        group.choices.length === 1 && !group.requiresSourceChoice
          ? [[catalogItemKey(group.item), group.choices[0]!.choiceId]]
          : [],
      ),
    ),
  );
  const [forms, setForms] = useState<ReadonlyMap<string, ConstructionChoiceForm>>(
    () => new Map(
      groups.flatMap((group) =>
        group.choices.flatMap((choice) => {
          const form = catalogItemDefaultForm(choice);
          return form ? [[choice.choiceId, form] as const] : [];
        }),
      ),
    ),
  );

  const complete = groups.every((group) => {
    const choice = group.choices.find(
      (candidate) => candidate.choiceId === choiceIDs.get(catalogItemKey(group.item)),
    );
    const form = choice ? forms.get(choice.choiceId) : undefined;
    return Boolean(
      choice &&
      form &&
      choice.options.some((option) => option.form === form)
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
      return [
        {
          constructionChoice: { choiceId: choice.choiceId, form },
          title: catalogItemLabel(group.item),
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
        className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-xl bg-white p-5 shadow-2xl"
        role="dialog"
      >
        <h2 id="catalog-selection-dialog-title" className="text-xl font-semibold text-slate-950">
          Choose column sources and output forms
        </h2>
        <p className="mt-1 text-sm text-slate-600">
          Choose where each column gets its values, then choose whether Loom keeps or reduces them. Each route follows every relationship shown; if a shared resource links to multiple rows, its connected values may appear for each row. Loom lists only routes with values in the current table data.
        </p>
        <div className="mt-4 space-y-3">
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
                  <p className="mt-2 text-sm text-amber-900">
                    {group.truncated
                      ? 'No route with current values was found within the automatic search limit. Review the full route graph in Advanced.'
                      : 'No authorized route with current values was found for this column.'}
                  </p>
                </article>
              );
            }
            const selectedForm = choice ? forms.get(choice.choiceId) : undefined;
            return (
              <article key={key} className="rounded-lg border border-slate-200 p-3">
                <h3 className="font-semibold text-slate-900">{catalogItemLabel(item)}</h3>
                {group.rowsWithValue === undefined ? null : (
                  <p className="mt-2 text-sm font-medium text-emerald-900">
                    {group.rowsWithValue.toLocaleString()} current table rows contain a value through this route.
                  </p>
                )}
                {group.truncated ? (
                  <p className="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-950" role="status">
                    Loom reached the automatic route-search limit. These routes have values, but other routes may also exist. Review the full route graph in Advanced.
                  </p>
                ) : null}
                {group.requiresSourceChoice || group.choices.length > 1 ? (
                  <fieldset className="mt-3 space-y-2" disabled={busy}>
                    <legend className="text-sm font-medium text-slate-800">Choose where this column gets its values</legend>
                    {group.choices.map((routeChoice, routeIndex) => (
                      <label key={routeChoice.choiceId} className="flex cursor-pointer items-start gap-2 rounded-md border border-slate-200 p-2.5">
                        <input
                          type="radio"
                          name={`construction-route-${key}`}
                          aria-label={`${catalogItemLabel(item)} route ${routeIndex + 1}: ${routeChoice.presentation.summary}`}
                          checked={choice?.choiceId === routeChoice.choiceId}
                          onChange={() => setChoiceIDs((current) => new Map(current).set(key, routeChoice.choiceId))}
                          className="mt-1 h-4 w-4 border-slate-300 text-blue-700"
                        />
                        <span className="min-w-0 flex-1">
                          <span className="block text-sm font-semibold text-slate-900">{routeChoice.presentation.summary}</span>
                          <span className="mt-1 block text-xs text-slate-600">
                            {routeChoice.route.length === 0
                              ? 'Value is on the same record as each table row.'
                              : routeChoice.route.map((step) => `${step.toResourceType} records connected to ${step.fromResourceType}`).join(' · ')}
                          </span>
                        </span>
                      </label>
                    ))}
                  </fieldset>
                ) : null}
                {choice ? <p className="mt-2 text-sm text-slate-600">
                  {choice.presentation.summary}
                </p> : null}
                {choice ? <details className="mt-2 text-xs text-slate-600">
                  <summary className="cursor-pointer font-medium text-blue-700">Source details</summary>
                  <dl className="mt-2 grid gap-1 rounded-md bg-slate-50 p-2 font-mono">
                    {choice.presentation.facts.map((fact) => (
                      <div key={`${fact.label}:${fact.value}`}>
                        <dt className="inline text-slate-500">{fact.label} </dt>
                        <dd className="inline break-all">{fact.value}</dd>
                      </div>
                    ))}
                  </dl>
                </details> : null}
                {showRouteDetails && choice ? <div className="mt-2 rounded-md bg-blue-50 p-2 text-xs text-slate-700">
                  <span className="font-semibold text-blue-900">Route: </span>
                  {choice.route.length === 0
                    ? 'Same resource as each table row'
                    : choice.route.map((step) =>
                        `${step.fromResourceType} → ${step.toResourceType} via ${step.relationship} (${step.storageDirection.toLowerCase()})`,
                      ).join(' · ')}
                </div> : null}
                {choice ? <fieldset className="mt-3 space-y-2" disabled={busy}>
                  <legend className="text-sm font-medium text-slate-800">Compiler-proved output forms</legend>
                  {choice.options.map((option) => {
                    const label = optionLabel(option);
                    return (
                      <label key={option.form} className="flex cursor-pointer items-start gap-2 rounded-md border border-slate-200 p-2.5">
                        <input
                          type="radio"
                          name={`construction-choice-${choice.choiceId}`}
                          aria-label={`${catalogItemLabel(item)}: ${label}`}
                          checked={selectedForm === option.form}
                          onChange={() => setForms((current) => new Map(current).set(choice.choiceId, option.form))}
                          className="mt-1 h-4 w-4 border-slate-300 text-blue-700"
                        />
                        <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-center gap-2 text-sm font-semibold text-slate-900">
                            {label}
                            {option.decision === 'DEFAULT' ? (
                              <span className="rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-blue-800">Default</span>
                            ) : null}
                          </span>
                          <span className="mt-1 block text-xs text-slate-600">{option.reason}</span>
                          <span className="mt-1 block text-[10px] uppercase tracking-wide text-slate-500">
                            {option.decision} · {option.rowEffect} · {option.support}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </fieldset> : null}
              </article>
            );
          })}
        </div>
        <div className="mt-5 flex justify-end gap-2 border-t border-slate-200 pt-4">
          <button type="button" disabled={busy} onClick={onCancel} className="rounded-md border border-slate-300 px-4 py-2 text-sm font-semibold text-slate-700 disabled:opacity-50">
            Cancel
          </button>
          <button type="button" disabled={busy || !complete} onClick={confirm} className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50">
            {busy ? 'Adding features…' : `Add ${groups.length} selected ${groups.length === 1 ? 'feature' : 'features'}`}
          </button>
        </div>
      </section>
    </div>
  );
};
