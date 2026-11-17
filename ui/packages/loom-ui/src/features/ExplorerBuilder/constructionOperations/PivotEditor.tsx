import { useId, useState } from 'react';
import { createOpaqueId, isNumericColumn, sourceLabel as pivotSourceLabel } from './editorShared';
import type { ConstructionTableScalar } from '../../../types';
import {
  allDiscoveredPivotCategoriesSelected, createPivotCategoryForm, outputNamesAreValid,
  pivotDiscoveryMatches, pivotOriginalPairApplies, pivotOutputsFor, PIVOT_DISCOVERY_MAX_CATEGORIES,
  scalarColumnsFor, scalarIdentity, scalarLabel,
  type ConstructionReshapePivotCategory, type ConstructionReshapePivotDiscovery,
  type PivotCategoryForm, type PivotForm, type ReshapeColumn, type ReshapeStage, type SourceInputChoice,
} from './reshapeForms';

const pivotDuplicatePolicyFromInput = (value: string): PivotForm['duplicatePolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'SUM': return 'SUM';
    case 'MIN': return 'MIN';
    case 'MAX': return 'MAX';
    default: return undefined;
  }
};

const pivotMissingPolicyFromInput = (value: string): PivotForm['missingCellPolicy'] | undefined => {
  switch (value) {
    case 'NULL': return 'NULL';
    case 'ERROR': return 'ERROR';
    default: return undefined;
  }
};

const pivotUnlistedPolicyFromInput = (value: string): PivotForm['unlistedCategoryPolicy'] | undefined => {
  switch (value) {
    case 'ERROR': return 'ERROR';
    case 'EXCLUDE_WITH_EVIDENCE': return 'EXCLUDE_WITH_EVIDENCE';
    default: return undefined;
  }
};

export const PivotEditor = (props: {
  readonly sourceChoices: ReadonlyArray<SourceInputChoice>;
  readonly form: PivotForm;
  readonly stage: ReshapeStage;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly discovery?: ConstructionReshapePivotDiscovery;
  readonly discoveryAwaited: boolean;
  readonly requireEveryDiscoveredCategory: boolean;
  readonly canDiscover: boolean;
  readonly showRowSelection: boolean;
  readonly onDiscover: () => void;
  readonly onManualCategorySelection: (form: PivotForm) => void;
  readonly onForgetPairSelection: (previous: PivotForm, next: PivotForm) => void;
  readonly onChange: (form: PivotForm) => void;
}) => {
  const discoveryHelpId = useId();
  const [categorySearch, setCategorySearch] = useState('');
  const columns = scalarColumnsFor(props.stage);
  const repeatedColumns = props.stage.columns.filter((column) => column.cardinality === 'many');
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const discoveryMode = props.form.discovery !== undefined;
  const pairUnchanged = pivotOriginalPairApplies(props.form);
  const categoriesKnown = !discoveryMode && (pairUnchanged || discoveryComplete);
  const discoveryEligible = !discoveryMode
    && props.canDiscover
    && props.supported
    && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0)
    && props.form.categoryColumnId !== ''
    && props.form.valueColumnId !== ''
    && !categoriesKnown;
  const hostDiscoveryPending = discoveryEligible
    && discoveryMatches
    && props.discovery?.status === 'loading';
  const discoveryPending = hostDiscoveryPending || (props.discoveryAwaited && !categoriesKnown);
  const categoriesAvailable = discoveryComplete ? props.discovery.categories : [];
  const missingDiscoveredCategories = props.requireEveryDiscoveredCategory
    && discoveryComplete
    && !allDiscoveredPivotCategoriesSelected(props.form, props.stage.id, props.discovery);
  const listedKeys = new Set(categoriesAvailable.map((category) => scalarIdentity(category.key)));
  const existingNotListed = pairUnchanged
    ? props.form.categories.filter((category) => !listedKeys.has(scalarIdentity(category.key)))
    : [];
  const categorySearchTerm = categorySearch.trim().toLowerCase();
  const matchesCategorySearch = (label: string, key: ConstructionTableScalar): boolean =>
    `${label} ${scalarLabel(key)}`.toLowerCase().includes(categorySearchTerm);
  const shownCategories = categoriesAvailable.filter((category) => matchesCategorySearch(category.label, category.key));
  const shownExistingNotListed = existingNotListed.filter((category) => matchesCategorySearch(scalarLabel(category.key), category.key));
  const selectedCategoryIdentities = new Set(props.form.categories.map((category) => scalarIdentity(category.key)));
  const selectedCategoryLabels = props.form.categories.map((category) =>
    categoriesAvailable.find((available) => scalarIdentity(available.key) === scalarIdentity(category.key))?.label
      ?? scalarLabel(category.key),
  );
  const displayedSelectedCategoryLabels = selectedCategoryLabels.slice(0, 3);
  const additionalSelectedCategoryCount = selectedCategoryLabels.length - displayedSelectedCategoryLabels.length;
  const showSelectedCategoryLabels = selectedCategoryLabels.length <= 10;
  const shownCategoryIdentities = new Set([
    ...shownCategories.map((category) => scalarIdentity(category.key)),
    ...shownExistingNotListed.map((category) => scalarIdentity(category.key)),
  ]);
  const hasUnselectedShownCategory = shownCategories.some((category) => !selectedCategoryIdentities.has(scalarIdentity(category.key)));
  const hasSelectedShownCategory = props.form.categories.some((category) => shownCategoryIdentities.has(scalarIdentity(category.key)));
  const shownCategoryCount = shownCategories.length + shownExistingNotListed.length;
  const categoryCount = categoriesAvailable.length + existingNotListed.length;
  const groupColumns = props.form.groupKeyIds.flatMap((id) => {
    const column = columns.find((candidate) => candidate.id === id);
    return column ? [column] : [];
  });
  const sourceChoiceForColumn = (columnId: string): SourceInputChoice | undefined => {
    const binding = props.form.pivotSources?.find((candidate) => candidate.columnId === columnId);
    return binding ? props.sourceChoices.find((candidate) => candidate.choiceId === binding.choiceId) : undefined;
  };
  const pivotColumnLabel = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice ? pivotSourceLabel(sourceChoice) : column.label;
  };
  const pivotColumnTitle = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice?.fieldPath ?? (column.name === column.label ? column.name : `${column.name} · ${column.label}`);
  };
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const numericValue = isNumericColumn(columns.find((column) => column.id === props.form.valueColumnId) ?? { id: '', name: '', label: '', cardinality: 'optional_one' });
  const categoryColumn = columns.find((column) => column.id === props.form.categoryColumnId);
  const selectableValueColumns = columns.filter((column) => !props.form.groupKeyIds.includes(column.id) && column.id !== props.form.categoryColumnId);
  const valueColumn = columns.find((column) => column.id === props.form.valueColumnId);
  const categoryColumnLabel = categoryColumn ? pivotColumnLabel(categoryColumn) : 'Column names';
  const valueColumnLabel = valueColumn ? pivotColumnLabel(valueColumn) : 'Cell values';
  const rowColumnLabels = groupColumns.map(pivotColumnLabel);
  const roleSummary = rowColumnLabels.length > 0 && categoryColumn && valueColumn
    ? `One row per ${rowColumnLabels.join(', ')}. Each ${categoryColumnLabel} value creates a column containing ${valueColumnLabel}.`
    : 'Example: One row per Patient ID. Each Visit type value creates a column containing Score.';

  const [columnFilter, setColumnFilter] = useState('');
  const chooseInput = (role: 'group' | 'category' | 'value', selected: string) => {
    if (role === 'group' && selected === '') return;
    if (role === 'category' || role === 'value') {
      setCategorySearch('');
      setColumnFilter('');
    }
    const choice = props.sourceChoices.find((candidate) => `source:${candidate.choiceId}` === selected);
    const existing = choice && props.form.pivotSources?.find((binding) => binding.choiceId === choice.choiceId);
    const columnId = choice ? existing?.columnId ?? createOpaqueId('pivot-input') : selected;
    if ((role === 'category' && columnId === props.form.categoryColumnId)
      || (role === 'value' && columnId === props.form.valueColumnId)) return;
    const bindings = choice && !existing ? [...(props.form.pivotSources ?? []), { choiceId: choice.choiceId, columnId }] : props.form.pivotSources ?? [];
    const nextValueColumnId = role === 'value' ? columnId : role === 'category' && columnId === props.form.valueColumnId ? '' : props.form.valueColumnId;
    const nextValueColumn = columns.find((column) => column.id === nextValueColumnId);
    const duplicatePolicy = !props.form.originalPair && role === 'value'
      ? (isNumericColumn(nextValueColumn ?? { id: '', name: '', label: '', cardinality: 'optional_one' }) ? 'SUM' : 'MIN')
      : props.form.duplicatePolicy;
    const preserveCategories = role === 'group';
    const next = {
      ...props.form,
      groupKeyIds: role === 'group' ? [...new Set([...props.form.groupKeyIds, columnId])] : props.form.groupKeyIds,
      categoryColumnId: role === 'category' ? columnId : props.form.categoryColumnId,
      valueColumnId: nextValueColumnId,
      duplicatePolicy,
      categories: preserveCategories ? props.form.categories : [],
      categoriesPair: preserveCategories ? props.form.categoriesPair : undefined,
      originalPair: preserveCategories ? props.form.originalPair : undefined,
    };
    const used = new Set([...next.groupKeyIds, next.categoryColumnId, next.valueColumnId]);
    const nextForm = { ...next, pivotSources: bindings.filter((binding) => used.has(binding.columnId)) };
    if (!preserveCategories) props.onForgetPairSelection(props.form, nextForm);
    props.onChange(nextForm);
  };
  const columnFilterText = columnFilter.trim().toLowerCase();
  const filteredGroupColumns = columns.filter((column) => !columnFilterText || column.label.toLowerCase().includes(columnFilterText) || (column.type ?? '').toLowerCase().includes(columnFilterText));

  const toggleGroup = (columnId: string, checked: boolean) => {
    const groupKeyIds = checked
      ? [...props.form.groupKeyIds, columnId]
      : props.form.groupKeyIds.filter((id) => id !== columnId);
    const used = new Set([...groupKeyIds, props.form.categoryColumnId, props.form.valueColumnId]);
    props.onChange({ ...props.form, groupKeyIds, pivotSources: props.form.pivotSources?.filter((binding) => used.has(binding.columnId)) });
  };

  const sourceOptions = props.sourceChoices.filter((choice) => choice.isPopulated && !props.form.pivotSources?.some((binding) => binding.choiceId === choice.choiceId));
  const updateCategory = (identity: string, update: Partial<PivotCategoryForm>) => {
    props.onChange({
      ...props.form,
      categories: props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category, ...update } : category),
    });
  };

  const toggleCategory = (available: ConstructionReshapePivotCategory, checked: boolean) => {
    const identity = scalarIdentity(available.key);
    const existing = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
    const categories = checked
      ? existing
        ? props.form.categories.map((category) => scalarIdentity(category.key) === identity ? { ...category } : category)
        : [...props.form.categories, createPivotCategoryForm(available, new Set([
            ...props.stage.columns.map((column) => column.name.toLowerCase()),
            ...props.form.categories.map((category) => category.name.toLowerCase()),
          ]))]
      : props.form.categories.filter((category) => scalarIdentity(category.key) !== identity);
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const selectShownCategories = () => {
    const selectedIdentities = new Set(selectedCategoryIdentities);
    const categories = [...props.form.categories];
    const usedNames = new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.categories.map((category) => category.name.toLowerCase()),
    ]);
    for (const available of shownCategories) {
      const identity = scalarIdentity(available.key);
      if (selectedIdentities.has(identity)) continue;
      const category = createPivotCategoryForm(available, usedNames);
      categories.push(category);
      selectedIdentities.add(identity);
      usedNames.add(category.name.toLowerCase());
    }
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  const clearShownCategories = () => {
    const categories = props.form.categories.filter((category) => !shownCategoryIdentities.has(scalarIdentity(category.key)));
    if (categories.length === props.form.categories.length) return;
    const next = {
      ...props.form,
      categories,
      categoriesPair: { categoryColumnId: props.form.categoryColumnId, valueColumnId: props.form.valueColumnId },
    };
    props.onManualCategorySelection(next);
    props.onChange(next);
  };

  return (
    <section aria-label="Pivot categories into columns" data-testid="construction-reshape-pivot" className="construction-reshape-pivot-editor grid content-start gap-3">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}
      {columns.length === 0 && props.sourceChoices.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Loom has not returned scalar fields for this stage.</p>
      ) : (
        <>
          {props.showRowSelection ? (
          <fieldset className="grid min-w-0 content-start gap-3" disabled={props.disabled || !props.supported}>
            <legend className="m-0 text-sm font-semibold text-slate-900">One row per</legend>
            <p className="m-0 text-xs text-slate-600">These fields identify each output row. Each category value becomes a new column.</p>
            <div className="grid gap-2">
              <label className="grid gap-1 text-xs font-medium text-slate-700">
                Search fields
                <input aria-label="Search pivot row fields" value={columnFilter} onChange={(event) => setColumnFilter(event.currentTarget.value)} placeholder="Search columns" className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm" />
              </label>
              <div className="grid max-h-72 gap-x-3 overflow-y-auto sm:grid-cols-2">
                {filteredGroupColumns.map((column) => {
                  const label = pivotColumnLabel(column);
                  return (
                    <label key={column.id} title={pivotColumnTitle(column)} className="flex min-w-0 items-start gap-2 border-b border-slate-100 px-3 py-2 text-sm text-slate-700 last:border-b-0 hover:bg-slate-50">
                      <input
                        type="checkbox"
                        aria-label={`Pivot group ${label}`}
                        checked={props.form.groupKeyIds.includes(column.id)}
                        disabled={props.disabled || !props.supported || column.id === props.form.categoryColumnId || column.id === props.form.valueColumnId}
                        onChange={(event) => toggleGroup(column.id, event.currentTarget.checked)}
                      />
                      <span className="min-w-0 break-words">{label}</span>
                    </label>
                  );
                })}
                {filteredGroupColumns.length === 0 ? <p className="px-1 text-xs text-slate-500">{columns.length === 0 ? 'No columns are available.' : 'No columns match this search.'}</p> : null}
              </div>
              {sourceOptions.length > 0 ? <select aria-label="Add pivot group field" value="" disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('group', event.currentTarget.value)} className="justify-self-start rounded border border-slate-300 bg-white px-2 py-1.5">
                <option value="">Add another field</option>
                {sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}
              </select> : null}
              {props.form.groupKeyIds.length === 0 && props.supported ? <p role="status" className="m-0 text-xs text-amber-900">Choose one or more fields to identify each row{props.form.pivotSources?.length ? '. Choose a row identity before finding categories from source fields.' : '.'}</p> : null}
            </div>
          </fieldset>
          ) : null}
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
              New column names
              <select aria-label="Pivot category field" value={props.form.categoryColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('category', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                <option value="">Choose a column-name field</option>
                {columns.filter((column) => !props.form.groupKeyIds.includes(column.id)).map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)}</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}</optgroup> : null}
              </select>
            </label>
            <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
              Cell values
              <select aria-label="Pivot values field" value={props.form.valueColumnId} disabled={props.disabled || !props.supported} onChange={(event) => chooseInput('value', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                <option value="">Choose a cell-values field</option>
                {selectableValueColumns.map((column) => <option key={column.id} value={column.id} title={pivotColumnTitle(column)}>{pivotColumnLabel(column)} ({column.type ?? 'unknown type'})</option>)}
                {sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)} ({choice.logicalType})</option>)}</optgroup> : null}
              </select>
            </label>
          </div>
          <p data-testid="construction-reshape-pivot-role-summary" className="text-sm text-slate-600">
            {roleSummary}
          </p>
          <div className="grid gap-1 rounded border border-slate-200 p-3">
            <label className="flex items-start gap-2 text-sm font-medium text-slate-800">
              <input
                type="checkbox"
                aria-label="Discover categories when proposing"
                aria-describedby={discoveryHelpId}
                checked={discoveryMode}
                disabled={props.disabled || !props.supported}
                onChange={(event) => props.onChange({
                  ...props.form,
                  discovery: event.currentTarget.checked ? { maxCategories: PIVOT_DISCOVERY_MAX_CATEGORIES } : undefined,
                  categories: event.currentTarget.checked ? [] : props.form.categories,
                  categoriesPair: event.currentTarget.checked ? undefined : props.form.categoriesPair,
                  originalPair: event.currentTarget.checked ? undefined : props.form.originalPair,
                })}
              />
              <span>Discover categories when proposing</span>
            </label>
            <p id={discoveryHelpId} className="m-0 text-xs text-slate-600">
              {discoveryMode
                ? `Categories are discovered when this pivot is proposed, up to ${props.form.discovery?.maxCategories ?? PIVOT_DISCOVERY_MAX_CATEGORIES}. They freeze at that point, so turn discovery on and propose the pivot again after the data changes. Manual category selection is unavailable while this is on.`
                : 'Choose categories manually from the values found in New column names. To refresh them from current data, turn on discovery and propose the pivot again.'}
            </p>
          </div>
          {repeatedColumns.length > 0 ? (
            <details className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-950">
              <summary className="cursor-pointer">{repeatedColumns.length} {repeatedColumns.length === 1 ? 'column contains a list' : 'columns contain lists'}</summary>
              <p className="mt-1">Use “Make one row per list value” to expand these columns before choosing Pivot fields.</p>
            </details>
          ) : null}
          {!discoveryMode && !props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery is unavailable for this stage.</p> : null}
          {!discoveryMode && props.canDiscover && props.supported && !categoriesKnown && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0) && (props.form.categoryColumnId === '' || props.form.valueColumnId === '') ? <p role="status" className="text-sm text-slate-600">Choose fields for New column names and Cell values to find categories.</p> : null}
          {discoveryPending ? (
            <div
              role="status"
              aria-live="polite"
              aria-busy="true"
              data-testid="construction-reshape-pivot-discovery-status"
              data-stage-id={props.stage.id}
              data-category-column-id={props.form.categoryColumnId}
              data-value-column-id={props.form.valueColumnId}
              className="grid gap-1 rounded bg-slate-50 px-3 py-2 text-sm text-slate-600"
            >
              <p className="m-0">Finding categories…</p>
              <p className="m-0 text-xs">
                Searching category values for New column names “{categoryColumnLabel}” and Cell values “{valueColumnLabel}”. Your entries stay as they are.
              </p>
            </div>
          ) : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'failed' ? <div role="status" className="text-sm text-amber-900"><p>{props.discovery.reason}</p><button type="button" onClick={props.onDiscover} disabled={props.disabled || !discoveryEligible} className="mt-1 text-blue-800 underline">Retry finding categories</button></div> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'limit-exceeded' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason} (Maximum supported categories: {props.discovery.limit}.)</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'missing-unsupported' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {props.discovery && discoveryMatches && props.discovery.status === 'no-values' ? <p role="status" className="text-sm text-amber-900">{props.discovery.reason}</p> : null}
          {categoriesKnown ? (
            <>
              <p role="status" data-testid="construction-reshape-pivot-category-summary" className="rounded bg-slate-50 px-3 py-2 text-sm text-slate-700">
                Selected {selectedCategoryLabels.length} of {categoryCount} categories{selectedCategoryLabels.length === 0 ? ': none' : showSelectedCategoryLabels ? `: ${displayedSelectedCategoryLabels.join(', ')}${additionalSelectedCategoryCount > 0 ? `, and ${additionalSelectedCategoryCount} more` : ''}` : '. Review the list to change them.'}
              </p>
              {discoveryComplete && categoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair. Choose another New column names field or adjust the rows before continuing.</p> : null}
              {missingDiscoveredCategories ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">Select every discovered category, or filter rows before pivoting.</p> : null}
              <p role="status" aria-live="polite" data-testid="construction-reshape-pivot-category-status" className="text-sm text-slate-600">
                {props.form.categories.length} selected · Showing {shownCategoryCount} of {categoryCount} category values
              </p>
              <details data-testid="construction-reshape-pivot-categories" className="rounded border border-slate-200 p-3">
                <summary className="cursor-pointer text-sm font-semibold text-slate-800">Change selected categories</summary>
                <fieldset className="mt-3 grid gap-3" disabled={props.disabled || !props.supported}>
                  <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-end">
                    <label className="grid gap-1 text-sm font-medium text-slate-700">
                      Search category values
                      <input
                        type="search"
                        aria-label="Search category values"
                        value={categorySearch}
                        onChange={(event) => setCategorySearch(event.currentTarget.value)}
                        placeholder="Search by label or value"
                        className="rounded border border-slate-300 bg-white px-2 py-1.5"
                      />
                    </label>
                    <button
                      type="button"
                      aria-label="Select shown categories"
                      disabled={props.disabled || !props.supported || !hasUnselectedShownCategory}
                      onClick={selectShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Select shown categories
                    </button>
                    <button
                      type="button"
                      aria-label="Clear shown categories"
                      disabled={props.disabled || !props.supported || !hasSelectedShownCategory}
                      onClick={clearShownCategories}
                      className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:text-slate-400"
                    >
                      Clear shown categories
                    </button>
                  </div>
                  {shownCategories.map((available) => {
                    const identity = scalarIdentity(available.key);
                    const current = props.form.categories.find((category) => scalarIdentity(category.key) === identity);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Include category ${available.label}`} checked={Boolean(current)} disabled={props.disabled || !props.supported} onChange={(event) => toggleCategory(available, event.currentTarget.checked)} />
                          {available.label}
                        </label>
                      </div>
                    );
                  })}
                  {shownExistingNotListed.map((category) => {
                    const identity = scalarIdentity(category.key);
                    return (
                      <div key={identity} className={`grid gap-2 rounded p-2 ${discoveryComplete ? 'border border-amber-200 bg-amber-50' : 'bg-slate-50'}`}>
                        <label className="flex items-center gap-2 text-sm font-medium text-slate-800">
                          <input type="checkbox" aria-label={`Keep saved category ${scalarLabel(category.key)}`} checked disabled={props.disabled || !props.supported} onChange={(event) => { if (!event.currentTarget.checked) toggleCategory({ key: category.key, label: scalarLabel(category.key) }, false); }} />
                          {scalarLabel(category.key)} {discoveryComplete ? <span className="font-normal text-amber-900">Not found in the latest category list</span> : null}
                        </label>
                      </div>
                    );
                  })}
                  {categoriesAvailable.length === 0 && existingNotListed.length === 0 ? <p role="status" className="text-sm text-slate-600">No categories were found for this pair.</p> : null}
                  {categoryCount > 0 && shownCategoryCount === 0 ? <p role="status" className="text-sm text-slate-600">No categories match this search.</p> : null}
                </fieldset>
              </details>
            </>
          ) : null}
          <p role="status" data-testid="construction-reshape-pivot-policy-summary" className="text-sm text-slate-600">
            Duplicate values: {pivotDuplicateEffect(props.form.duplicatePolicy)}. Empty cells: {pivotMissingCellEffect(props.form.missingCellPolicy)}. Unselected categories: {pivotUnlistedCategoryEffect(props.form.unlistedCategoryPolicy)}.
          </p>
          <details data-testid="construction-reshape-pivot-advanced" className="rounded border border-slate-200 p-3">
            <summary className="cursor-pointer text-sm font-semibold text-slate-800">Advanced settings</summary>
            <div className="mt-3 grid gap-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has duplicate values
                  <select aria-label="Pivot duplicate policy" value={props.form.duplicatePolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotDuplicatePolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, duplicatePolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="SUM" disabled={!numericValue}>Add them together</option>
                    <option value="MIN">Keep the smallest</option>
                    <option value="MAX">Keep the largest</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a group has no value
                  <select aria-label="Pivot missing cell policy" value={props.form.missingCellPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotMissingPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, missingCellPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="NULL">Leave the new cell empty</option>
                    <option value="ERROR">Stop with an error</option>
                  </select>
                </label>
                <label className="grid gap-1 text-sm font-medium text-slate-700">
                  If a row has an unselected category
                  <select aria-label="Pivot unlisted category policy" value={props.form.unlistedCategoryPolicy} disabled={props.disabled || !props.supported} onChange={(event) => { const policy = pivotUnlistedPolicyFromInput(event.currentTarget.value); if (policy) props.onChange({ ...props.form, unlistedCategoryPolicy: policy }); }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                    <option value="ERROR">Stop with an error</option>
                    <option value="EXCLUDE_WITH_EVIDENCE" disabled>Skip it and report it</option>
                  </select>
                </label>
              </div>
              <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">
                New pivots require every category present in the rows to be selected. Skipping unselected categories is unavailable.
              </p>
              {props.form.categories.length > 0 ? (
                <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
                  <legend className="px-1 text-sm font-semibold text-slate-800">Output column names and labels</legend>
                  {props.form.categories.map((category) => {
                    const identity = scalarIdentity(category.key);
                    const label = categoriesAvailable.find((available) => scalarIdentity(available.key) === identity)?.label
                      ?? scalarLabel(category.key);
                    return (
                      <div key={identity} className="grid gap-2 rounded bg-slate-50 p-2">
                        <p className="text-sm font-medium text-slate-700">{label}</p>
                        <PivotCategoryOutput category={category} disabled={props.disabled || !props.supported} onChange={(update) => updateCategory(identity, update)} />
                      </div>
                    );
                  })}
                </fieldset>
              ) : null}
              {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give each output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
            </div>
          </details>
        </>
      )}
    </section>
  );
};

const pivotDuplicateEffect = (policy: PivotForm['duplicatePolicy']): string => {
  switch (policy) {
    case 'ERROR': return 'stop the pivot with an error';
    case 'SUM': return 'be added together';
    case 'MIN': return 'keep the smallest value';
    case 'MAX': return 'keep the largest value';
    default: {
      const exhaustive: never = policy;
      return exhaustive;
    }
  }
};

const pivotMissingCellEffect = (policy: PivotForm['missingCellPolicy']): string =>
  policy === 'NULL' ? 'stay empty' : 'stop the pivot with an error';

const pivotUnlistedCategoryEffect = (policy: PivotForm['unlistedCategoryPolicy']): string =>
  policy === 'ERROR' ? 'stop the pivot with an error' : 'be skipped and reported';

const PivotCategoryOutput = (props: {
  readonly category: PivotCategoryForm;
  readonly disabled: boolean;
  readonly onChange: (update: Partial<PivotCategoryForm>) => void;
}) => (
  <div className="grid gap-2 sm:grid-cols-2">
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column name
      <input aria-label={`Pivot output name ${scalarLabel(props.category.key)}`} value={props.category.name} disabled={props.disabled} onChange={(event) => props.onChange({ name: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
    <label className="grid gap-1 text-xs font-medium text-slate-600">
      New column label
      <input aria-label={`Pivot output label ${scalarLabel(props.category.key)}`} value={props.category.label} disabled={props.disabled} onChange={(event) => props.onChange({ label: event.currentTarget.value })} className="rounded border border-slate-300 px-2 py-1.5 text-sm" />
    </label>
  </div>
);
