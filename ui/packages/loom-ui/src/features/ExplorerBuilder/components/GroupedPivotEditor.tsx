import React, { useState } from 'react';
import { ChoiceSelect } from './ChoiceSelect';
import { OrderedChoiceEditor } from './OrderedChoiceEditor';
import { OutputNameFields } from './OutputNameFields';
import type {
  PivotCategoryChoice,
  PivotCategoryDiscovery,
  PivotCategoryPair,
  PivotFormState,
  TableShapeEditorChoices,
} from './tableShapeModel';
import { choiceFor, pairForPivot, referenceFor, sameChoiceReference, samePivotPair } from './tableShapeModel';

const MAX_VISIBLE_CATEGORIES = 50;

export interface GroupedPivotEditorProps {
  readonly pivot: PivotFormState;
  readonly choices: TableShapeEditorChoices;
  readonly categoryDiscovery: PivotCategoryDiscovery;
  readonly disabled: boolean;
  readonly onChange: (pivot: PivotFormState) => void;
  readonly onCategoryValueChange: (pivot: PivotFormState) => void;
  readonly onRequestCategoryDiscovery: (pair: PivotCategoryPair) => void;
}

const isInPair = (
  discovery: PivotCategoryDiscovery,
  pair: PivotCategoryPair | undefined,
): boolean => {
  if (discovery.kind === 'not-requested' || !pair) return discovery.kind === 'not-requested';
  return samePivotPair(discovery.pair, pair);
};

export const GroupedPivotEditor = ({
  pivot,
  choices,
  categoryDiscovery,
  disabled,
  onChange,
  onCategoryValueChange,
  onRequestCategoryDiscovery,
}: GroupedPivotEditorProps) => {
  const [categoryFilter, setCategoryFilter] = useState('');
  const pair = pairForPivot(pivot);
  const currentDiscovery = isInPair(categoryDiscovery, pair)
    ? categoryDiscovery
    : { kind: 'not-requested' as const };
  const categories = currentDiscovery.kind === 'complete' ? currentDiscovery.categories : [];
  const matchingCategories = categories.filter((category) => {
    const query = categoryFilter.trim().toLocaleLowerCase();
    return query === '' || [category.label, category.suggestedOutput.column, category.suggestedOutput.label]
      .some((value) => value.toLocaleLowerCase().includes(query));
  });
  const visibleCategories = matchingCategories.slice(0, MAX_VISIBLE_CATEGORIES);
  const canRequest = pair !== undefined && !disabled && currentDiscovery.kind !== 'loading';

  const selectedFor = (category: PivotCategoryChoice) =>
    pivot.includedCategories.find((selection) => selection.category.choiceId === category.choiceId);

  const updateSelectedOutput = (
    category: PivotCategoryChoice,
    output: { readonly column: string; readonly label: string },
  ) => onChange({
    ...pivot,
    includedCategories: pivot.includedCategories.map((selection) =>
      selection.category.choiceId === category.choiceId ? { ...selection, output } : selection,
    ),
  });

  const requestDiscovery = () => {
    if (pair) onRequestCategoryDiscovery(pair);
  };

  const savedSelectionsNotDiscovered = pivot.includedCategories.filter(
    (selection) => !categories.some((category) => category.choiceId === selection.category.choiceId),
  );

  return (
    <section aria-label="Grouped pivot configuration" data-testid="ui04-pivot-configuration" className="grid gap-3 rounded-lg border border-slate-200 p-3">
      <div>
        <h3 className="font-semibold text-slate-900">Grouped pivot</h3>
        <p className="mt-1 text-xs text-slate-600">Choose group keys, then discover and freeze categories from the selected value column.</p>
      </div>
      <OrderedChoiceEditor
        label="Pivot group columns"
        testId="ui04-pivot-group-columns"
        choices={choices.groupColumns}
        selected={pivot.groupColumns}
        disabled={disabled}
        onChange={(groupColumns) => onChange({ ...pivot, groupColumns })}
      />
      <ChoiceSelect
        label="Pivot category column"
        testId="ui04-pivot-category-column"
        choices={choices.categoryColumns}
        value={pivot.categoryColumn}
        placeholder="Choose a category column"
        disabled={disabled}
        onChange={(categoryColumn) => {
          if (sameChoiceReference(categoryColumn, pivot.categoryColumn)) return;
          onCategoryValueChange({ ...pivot, categoryColumn, includedCategories: [] });
        }}
      />
      <ChoiceSelect
        label="Pivot value column"
        testId="ui04-pivot-value-column"
        choices={choices.valueColumns}
        value={pivot.valueColumn}
        placeholder="Choose a value column"
        disabled={disabled}
        onChange={(valueColumn) => {
          if (sameChoiceReference(valueColumn, pivot.valueColumn)) return;
          onCategoryValueChange({ ...pivot, valueColumn, includedCategories: [] });
        }}
      />

      <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
        <legend className="px-1 text-sm font-medium text-slate-800">Discover and freeze categories</legend>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            data-testid="ui04-pivot-discover-categories"
            className="rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
            disabled={!canRequest}
            onClick={requestDiscovery}
          >
            {currentDiscovery.kind === 'loading'
              ? 'Loading categories…'
              : currentDiscovery.kind === 'complete'
                ? 'Refresh categories'
                : currentDiscovery.kind === 'unavailable'
                  ? 'Try category discovery again'
                  : 'Discover categories'}
          </button>
          {!pair ? (
            <span className="text-xs text-slate-600">Choose both a category column and value column first.</span>
          ) : null}
        </div>
        {currentDiscovery.kind === 'loading' ? (
          <p role="status" data-testid="ui04-pivot-category-discovery-state" className="text-sm text-slate-700">
            Loading categories for the selected columns.
          </p>
        ) : currentDiscovery.kind === 'unavailable' ? (
          <p role="status" data-testid="ui04-pivot-category-discovery-state" className="text-sm text-amber-900">
            {currentDiscovery.reason}
          </p>
        ) : currentDiscovery.kind === 'complete' ? (
          <p role="status" data-testid="ui04-pivot-category-discovery-state" className="text-sm text-slate-700">
            Discovered {currentDiscovery.categories.length} categories.
          </p>
        ) : (
          <p role="status" data-testid="ui04-pivot-category-discovery-state" className="text-sm text-slate-600">
            Categories are not discovered for this column pair yet.
          </p>
        )}

        {categories.length > 0 ? (
          <>
            <label className="grid gap-1 text-sm font-medium text-slate-800">
              <span>Filter discovered categories</span>
              <input
                aria-label="Filter discovered categories"
                data-testid="ui04-pivot-category-filter"
                className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
                value={categoryFilter}
                disabled={disabled}
                onChange={(event) => setCategoryFilter(event.currentTarget.value)}
              />
            </label>
            <ul className="grid gap-2" data-testid="ui04-pivot-frozen-categories">
              {visibleCategories.map((category, index) => {
                const selected = selectedFor(category);
                return (
                  <li key={category.choiceId} className="rounded border border-slate-200 p-2">
                    <label className="flex items-start gap-2 text-sm text-slate-800">
                      <input
                        type="checkbox"
                        aria-label={`Include ${category.label} category`}
                        data-testid={`ui04-pivot-category-${index + 1}`}
                        checked={selected !== undefined}
                        disabled={disabled || (category.availability.kind === 'unsupported' && !selected)}
                        onChange={(event) => {
                          if (category.availability.kind === 'unsupported' && event.currentTarget.checked) return;
                          if (event.currentTarget.checked) {
                            if (!selected) onChange({
                              ...pivot,
                              includedCategories: [
                                ...pivot.includedCategories,
                                { category: referenceFor(category), output: category.suggestedOutput },
                              ],
                            });
                          } else {
                            onChange({
                              ...pivot,
                              includedCategories: pivot.includedCategories.filter(
                                (selection) => selection.category.choiceId !== category.choiceId,
                              ),
                            });
                          }
                        }}
                      />
                      <span className="grid gap-0.5">
                        <span>{category.label}</span>
                        <span className="text-xs text-slate-600">
                          Suggested output: {category.suggestedOutput.column} ({category.suggestedOutput.label})
                        </span>
                        {category.availability.kind === 'unsupported' ? (
                          <span className="text-xs text-amber-900">{category.availability.reason}</span>
                        ) : null}
                      </span>
                    </label>
                    {selected ? (
                      <div className="mt-2 pl-6">
                        <OutputNameFields
                          label={`${category.label} output`}
                          testId={`ui04-pivot-category-output-${index + 1}`}
                          value={selected.output}
                          disabled={disabled}
                          onChange={(output) => updateSelectedOutput(category, output)}
                        />
                      </div>
                    ) : null}
                  </li>
                );
              })}
            </ul>
            {matchingCategories.length > visibleCategories.length ? (
              <p className="text-xs text-slate-600" data-testid="ui04-pivot-category-overflow">
                Showing the first {visibleCategories.length} of {matchingCategories.length} matching discovered categories.
              </p>
            ) : null}
            {matchingCategories.length === 0 ? (
              <p className="text-sm text-slate-600">No discovered categories match this filter.</p>
            ) : null}
          </>
        ) : currentDiscovery.kind === 'complete' ? (
          <p className="text-sm text-slate-600">The server returned no categories for this column pair.</p>
        ) : null}

        {savedSelectionsNotDiscovered.length > 0 ? (
          <ul aria-label="Saved categories not in current discovery" className="grid gap-2">
            {savedSelectionsNotDiscovered.map((selection, index) => (
              <li key={selection.category.choiceId} className="rounded border border-amber-200 bg-amber-50 p-2">
                <p className="text-xs text-amber-900">Saved category is not present in the current discovery.</p>
                <OutputNameFields
                  label={`Saved category ${index + 1} output`}
                  testId={`ui04-pivot-saved-category-output-${index + 1}`}
                  value={selection.output}
                  disabled={disabled}
                  onChange={(output) => onChange({
                    ...pivot,
                    includedCategories: pivot.includedCategories.map((item) =>
                      item.category.choiceId === selection.category.choiceId ? { ...item, output } : item,
                    ),
                  })}
                />
                <button
                  type="button"
                  aria-label={`Remove saved category ${index + 1}`}
                  className="mt-2 rounded border border-amber-300 px-2 py-1 text-xs font-medium text-amber-900 disabled:opacity-50"
                  disabled={disabled}
                  onClick={() => onChange({
                    ...pivot,
                    includedCategories: pivot.includedCategories.filter(
                      (item) => item.category.choiceId !== selection.category.choiceId,
                    ),
                  })}
                >
                  Remove saved category
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </fieldset>

      <ChoiceSelect
        label="Duplicate cell policy"
        testId="ui04-pivot-duplicate-policy"
        choices={choices.duplicatePolicies}
        value={pivot.duplicatePolicy}
        placeholder="Choose how duplicate cells are handled"
        disabled={disabled}
        onChange={(duplicatePolicy) => onChange({ ...pivot, duplicatePolicy })}
      />
      <ChoiceSelect
        label="Missing cell policy"
        testId="ui04-pivot-missing-policy"
        choices={choices.missingCellPolicies}
        value={pivot.missingCellPolicy}
        placeholder="Choose how missing cells are handled"
        disabled={disabled}
        onChange={(missingCellPolicy) => onChange({ ...pivot, missingCellPolicy })}
      />
      <ChoiceSelect
        label="Unlisted category policy"
        testId="ui04-pivot-unlisted-policy"
        choices={choices.unlistedCategoryPolicies}
        value={pivot.unlistedCategoryPolicy}
        placeholder="Choose how new categories are handled"
        disabled={disabled}
        onChange={(unlistedCategoryPolicy) => onChange({ ...pivot, unlistedCategoryPolicy })}
      />
    </section>
  );
};
