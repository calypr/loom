import { fieldPickerList, fieldPickerMetadata, fieldPickerToggleRow } from '../components/fieldPickerStyles';
import { useId, useState } from 'react';
import { createOpaqueId, isNumericColumn, sourceLabel as pivotSourceLabel } from './editorShared';
import type { ConstructionTableScalar } from '../../../types';
import {
  allDiscoveredPivotCategoriesSelected, createPivotCategoryForm, outputNamesAreValid,
  pivotDiscoveryMatches, pivotOriginalPairApplies, pivotOutputsFor, PIVOT_DISCOVERY_MAX_CATEGORIES,
  isScalarColumn, listColumnsFor, scalarColumnsFor, scalarIdentity, scalarLabel,
  type ConstructionReshapePivotCategory, type ConstructionReshapePivotDiscovery,
  type PivotCategoryForm, type PivotForm, type ReshapeColumn, type ReshapeStage, type SourceInputChoice,
} from './reshapeForms';

export type PivotListSelection = { readonly role: 'category' | 'value'; readonly columnId: string };

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
  readonly populationStatusFor: (columnId: string) => 'POPULATED' | 'EMPTY' | 'UNKNOWN';
  readonly populationChecking: boolean;
  readonly populationUnavailable: boolean;
  readonly onExpandListColumn?: (columnId: string) => void;
  readonly pendingList?: PivotListSelection;
  readonly onPendingListChange: (selection: PivotListSelection | undefined) => void;
  readonly onDiscover: () => void;
  readonly onManualCategorySelection: (form: PivotForm) => void;
  readonly onForgetPairSelection: (previous: PivotForm, next: PivotForm) => void;
  readonly onChange: (form: PivotForm) => void;
}) => {
  const discoveryHelpId = useId();
  const [categorySearch, setCategorySearch] = useState('');
  const columns = scalarColumnsFor(props.stage);
  const emptyRole = [props.form.categoryColumnId, props.form.valueColumnId]
    .some((columnId) => columnId && props.populationStatusFor(columnId) === 'EMPTY');
  const repeatedColumns = props.stage.columns.filter((column) => column.cardinality === 'many');
  const listColumns = props.onExpandListColumn ? listColumnsFor(props.stage) : [];
  const listColumnIds = new Set(listColumns.map((column) => column.id));
  const labelCounts = new Map<string, number>();
  for (const column of props.stage.columns) {
    labelCounts.set(column.label, (labelCounts.get(column.label) ?? 0) + 1);
  }
  const listColumn = listColumns.find((column) => column.id === props.pendingList?.columnId);
  const listRole = listColumn ? props.pendingList?.role : undefined;
  const discoveryMatches = pivotDiscoveryMatches(props.form, props.stage.id, props.discovery);
  const discoveryComplete = discoveryMatches && props.discovery.status === 'complete';
  const discoveryMode = props.form.discovery !== undefined;
  const pairUnchanged = pivotOriginalPairApplies(props.form);
  const categoriesKnown = !discoveryMode && (pairUnchanged || discoveryComplete);
  const discoveryEligible = !discoveryMode
    && props.canDiscover
    && props.supported
    && !listColumn
    && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0)
    && props.form.categoryColumnId !== ''
    && props.form.valueColumnId !== ''
    && !emptyRole
    && !props.populationChecking
    && !props.populationUnavailable
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
    const label = sourceChoice ? pivotSourceLabel(sourceChoice) : column.label;
    return props.populationStatusFor(column.id) === 'EMPTY' ? `${label} — No populated values` : label;
  };
  const pivotColumnTitle = (column: ReshapeColumn): string => {
    const sourceChoice = sourceChoiceForColumn(column.id);
    return sourceChoice?.fieldPath ?? (column.name === column.label ? column.name : `${column.name} · ${column.label}`);
  };
  const outputNamesValid = outputNamesAreValid(pivotOutputsFor(props.form, props.stage));
  const categoryColumn = columns.find((column) => column.id === props.form.categoryColumnId);
  const unavailableReason = (column: ReshapeColumn, role: 'category' | 'value') => {
    if (props.form.groupKeyIds.includes(column.id)) return 'used to identify rows';
    if (column.id === (role === 'category' ? props.form.valueColumnId : props.form.categoryColumnId)) {
      return role === 'category' ? 'used for cell values' : 'used for column names';
    }
    if (listColumnIds.has(column.id)) {
      return listRole && listRole !== role ? 'choose a single-value field here' : undefined;
    }
    if (!props.supported) return 'split a list before pivoting';
    if (!isScalarColumn(column)) return column.cardinality === 'many' ? 'expand list first' : 'requires a scalar field';
    return undefined;
  };
  const renderInputColumn = (column: ReshapeColumn, role: 'category' | 'value') => {
    const reason = unavailableReason(column, role);
    const label = pivotColumnLabel(column);
    const isList = listColumnIds.has(column.id);
    const duplicateLabel = (labelCounts.get(column.label) ?? 0) > 1;
    return <option key={column.id} value={column.id} disabled={Boolean(reason) || props.populationStatusFor(column.id) === 'EMPTY'} title={pivotColumnTitle(column)}>
      {label}{duplicateLabel ? ` (${column.name})` : ''}{isList ? ' — List' : role === 'value' ? ` (${column.type ?? 'unknown type'})` : ''}{reason ? ` — ${reason}` : ''}
    </option>;
  };
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
  const selectPivotInput = (role: 'category' | 'value', selected: string) => {
    const list = listColumns.find((column) => column.id === selected);
    if (list) {
      if (unavailableReason(list, role)) return;
      props.onPendingListChange({ role, columnId: list.id });
      chooseInput(role, '');
    } else {
      if (selected && !props.supported) return;
      if (listRole === role) props.onPendingListChange(undefined);
      chooseInput(role, selected);
    }
  };
  const renderSplitAction = (role: 'category' | 'value') => listRole === role && listColumn ? (
    <div className="grid gap-2 rounded border border-slate-200 bg-slate-50 p-2 text-xs text-slate-700">
      <p>Use each item in <strong>{pivotColumnLabel(listColumn)}</strong> separately. Each list item gets its own row, and the other values repeat.</p>
      <button type="button" disabled={props.disabled}
        onClick={() => props.onExpandListColumn?.(listColumn.id)}
        className="justify-self-start rounded border border-blue-300 bg-white px-2 py-1 font-semibold text-blue-800">
        Preview split
      </button>
    </div>
  ) : null;
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
      {props.populationChecking ? <p role="status" aria-live="polite" className="text-sm text-slate-600">Checking which fields contain values…</p> : null}
      {props.populationUnavailable ? <p role="status" className="text-sm text-amber-900">Field population could not be confirmed. Category discovery is unavailable until the field status can be checked.</p> : null}
      {emptyRole ? <p role="status" className="text-sm text-amber-900">Choose fields that contain values for both column names and cell values.</p> : null}
      {columns.length === 0 && listColumns.length === 0 && props.sourceChoices.length === 0 ? (
        <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">No fields are available for Pivot at this stage.</p>
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
              <div className={fieldPickerList}>
                {filteredGroupColumns.map((column) => {
                  const label = pivotColumnLabel(column);
                  return (
                    <label key={column.id} title={pivotColumnTitle(column)} className={fieldPickerToggleRow}>
                      <input
                        type="checkbox"
                        aria-label={`Pivot group ${label}`}
                        checked={props.form.groupKeyIds.includes(column.id)}
                        disabled={props.disabled || !props.supported || column.id === props.form.categoryColumnId || column.id === props.form.valueColumnId}
                        onChange={(event) => toggleGroup(column.id, event.currentTarget.checked)}
                      />
                      <span className="min-w-0 flex-1 truncate">{column.label || column.name}</span>
                      {column.type ? <span className={fieldPickerMetadata}>{column.type}</span> : null}
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
          <section aria-label="Pivot fields" className="grid gap-3 rounded-md border border-slate-200 bg-white p-3">
            <h4 className="text-sm font-semibold text-slate-900">Choose pivot fields</h4>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid content-start gap-2">
                <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
                  New column names
                  <select aria-label="Pivot category field" value={listRole === 'category' ? listColumn?.id : props.form.categoryColumnId} disabled={props.disabled || (!props.supported && listColumns.length === 0)} onChange={(event) => selectPivotInput('category', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                    <option value="">Choose a column-name field</option>
                    {props.stage.columns.map((column) => renderInputColumn(column, 'category'))}
                    {props.supported && sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)}</option>)}</optgroup> : null}
                  </select>
                </label>
                {renderSplitAction('category')}
              </div>
              <div className="grid content-start gap-2">
                <label className="grid min-w-0 gap-1 text-xs font-medium text-slate-700">
                  Cell values
                  <select aria-label="Pivot values field" value={listRole === 'value' ? listColumn?.id : props.form.valueColumnId} disabled={props.disabled || (!props.supported && listColumns.length === 0)} onChange={(event) => selectPivotInput('value', event.currentTarget.value)} className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm">
                    <option value="">Choose a cell-values field</option>
                    {props.stage.columns.map((column) => renderInputColumn(column, 'value'))}
                    {props.supported && sourceOptions.length > 0 ? <optgroup label="Source fields">{sourceOptions.map((choice) => <option key={choice.choiceId} value={`source:${choice.choiceId}`} title={choice.fieldPath}>{pivotSourceLabel(choice)} ({choice.logicalType})</option>)}</optgroup> : null}
                  </select>
                </label>
                {renderSplitAction('value')}
              </div>
            </div>
            {!listColumn ? <p data-testid="construction-reshape-pivot-role-summary" className="m-0 text-xs text-slate-600">
              {roleSummary}
            </p> : null}
          </section>
          {!listColumn ? <section aria-label="Pivot categories" className="grid gap-3 border-t border-slate-200 pt-3">
          <h4 className="text-sm font-semibold text-slate-900">Categories</h4>
          <div className="grid gap-1 rounded-md bg-slate-50 px-3 py-2">
            <label className="flex items-start gap-2 text-sm font-medium text-slate-800">
              <input
                type="checkbox"
                aria-label="Discover categories when proposing"
                aria-describedby={discoveryHelpId}
                checked={discoveryMode}
                disabled={props.disabled || !props.supported || emptyRole || props.populationChecking || props.populationUnavailable}
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
                : 'Leave this off to review and select categories before proposing.'}
            </p>
          </div>
          {!discoveryMode && !props.canDiscover && !categoriesKnown ? <p role="status" className="text-sm text-amber-900">Category discovery is unavailable for this stage.</p> : null}
          {!listColumn && !discoveryMode && props.canDiscover && props.supported && !categoriesKnown && (!props.form.pivotSources?.length || props.form.groupKeyIds.length > 0) && (props.form.categoryColumnId === '' || props.form.valueColumnId === '') ? <p role="status" className="text-sm text-slate-600">Select both pivot fields to see available categories.</p> : null}
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
          </section> : null}
          {props.form.categories.length > 0 ? (
            <details className="rounded border border-slate-200 p-3">
              <summary className="cursor-pointer text-sm font-semibold text-slate-800">Output column names and labels</summary>
                <fieldset className="grid gap-3 rounded border border-slate-200 p-3" disabled={props.disabled || !props.supported}>
                  <legend className="sr-only">Output column names and labels</legend>
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
            </details>
          ) : null}
          {!outputNamesValid ? <p role="status" className="text-sm text-amber-900">Give each output a unique column name using letters, numbers, or underscores, and add a label.</p> : null}
        </>
      )}
    </section>
  );
};

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
