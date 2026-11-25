import type { CodedPivotContext } from './codedPivotContext';
import { fieldPickerList, fieldPickerToggleRow } from '../components/fieldPickerStyles';
import { useId, useState } from 'react';
import { createOpaqueId, normalizedName, uniqueName } from '../model/editorShared';
import { GroupCodedValuePicker } from './GroupCodedValuePicker';
import { ListColumnExpandChoices } from './ListColumnExpandChoices';
import { isSourceProjectionStage } from '../model/constructionStages';
import {
  addGroupKey, aggregateInputColumnsFor, aggregateNameFor, ensureGroupCodedPassthroughs,
  fieldChoiceIdentity, getUsedGroupNames, groupFieldChoiceForKey, groupFieldKeyForChoice,
  groupFieldSourcePresentation, groupOutputColumns, groupRowValueBindingIdentity,
  groupSemanticKeyForChoice, listColumnsFor, makeAggregate, numericColumnsFor,
  operationLabel, outputNamesAreValid, relatedRouteIdsForGroupFieldKeys,
  replaceGroupAggregate, scalarColumnsFor,
  type GroupAggregate, type GroupAggregateKind, type GroupFieldChoice, type GroupFieldKey,
  type GroupFieldSourceChoice, type GroupForm, type GroupRowValue, type GroupRowValueChoice,
  type ReshapeColumn, type ReshapeStage, type SemanticGroupChoice,
} from '../model/reshapeForms';

const readableFieldCombination = (labels: ReadonlyArray<string>): string => {
  if (labels.length < 2) return labels[0] ?? '';
  if (labels.length === 2) return `${labels[0]} and ${labels[1]}`;
  return `${labels.slice(0, -1).join(', ')}, and ${labels.at(-1)}`;
};

const compactSemanticScope = (scopeLabel: string): string => scopeLabel.split(' · ').slice(-2).join(' · ');

const groupAggregateKindFromInput = (value: string): GroupAggregateKind | undefined => {
  switch (value) {
    case 'COUNT_ROWS': return 'COUNT_ROWS';
    case 'COUNT_NON_NULL': return 'COUNT_NON_NULL';
    case 'COUNT_DISTINCT': return 'COUNT_DISTINCT';
    case 'SUM': return 'SUM';
    case 'MEAN': return 'MEAN';
    default: return undefined;
  }
};

const GroupRowValuesEditor = (props: {
  readonly form: GroupForm;
  readonly inputColumns: ReadonlyArray<ReshapeColumn>;
  readonly groupKeyInputColumnIds: ReadonlySet<string>;
  readonly groupRowValueChoices: ReadonlyArray<GroupRowValueChoice>;
  readonly disabled: boolean;
  readonly supported: boolean;
  readonly embedded?: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const [rowValueSearch, setRowValueSearch] = useState('');
  const normalizedRowValueSearch = rowValueSearch.trim().toLocaleLowerCase();
  const selectedRowValueChoiceIds = new Set(props.form.rowValues.flatMap((value) =>
    'fieldSource' in value && value.fieldSource.kind === 'CHOICE' ? [value.fieldSource.choiceId] : [],
  ));
  const selectedRowValueBindingIdentities = new Set(props.form.rowValues.flatMap((value) => {
    if (!('fieldSource' in value)) return [];
    const identity = groupRowValueBindingIdentity(value.fieldSource);
    return identity ? [identity] : [];
  }));
  const selectedRowValueInputIDs = new Set(props.form.rowValues.flatMap((value) =>
    'inputColumnId' in value ? [value.inputColumnId] : [],
  ));
  const visibleRowValueChoices = props.groupRowValueChoices.filter((choice) =>
    choice.fieldSource.kind === 'CHOICE' && !selectedRowValueChoiceIds.has(choice.fieldSource.choiceId) &&
    !(choice.inputColumnId && props.groupKeyInputColumnIds.has(choice.inputColumnId)) &&
    !(choice.inputColumnId && selectedRowValueInputIDs.has(choice.inputColumnId)) &&
    !(fieldChoiceIdentity(choice) && selectedRowValueBindingIdentities.has(fieldChoiceIdentity(choice)!)) &&
    (!normalizedRowValueSearch || `${choice.label} ${choice.scopeLabel ?? ''} ${choice.canonicalPath} ${choice.sourceRole}`.toLocaleLowerCase().includes(normalizedRowValueSearch)));

  const signedInputColumnIds = new Set(props.groupRowValueChoices.flatMap((choice) => choice.inputColumnId ? [choice.inputColumnId] : []));
  const visibleInputColumns = props.inputColumns.filter((column) =>
    !props.groupKeyInputColumnIds.has(column.id) && !selectedRowValueInputIDs.has(column.id) && !signedInputColumnIds.has(column.id) &&
    (!normalizedRowValueSearch || `${column.label} ${column.name}`.toLocaleLowerCase().includes(normalizedRowValueSearch)));
  const addInputColumn = (column: ReshapeColumn) => {
    if (props.form.codedCategories.length > 0) return;
    const name = uniqueName(normalizedName(column.label), getUsedGroupNames(props.form));
    const rowValue: GroupRowValue = {
      inputColumnId: column.id, outputColumnId: createOpaqueId('group-row-value'), policy: 'ALL',
      name, label: column.label, displayLabel: column.label, scopeLabel: 'Current Group input column', valueType: column.type ?? 'string',
    };
    props.onChange({ ...props.form, rowValues: [...props.form.rowValues, rowValue] });
  };
  const changeRowValue = (outputColumnId: string, update: Partial<Pick<GroupRowValue, 'name' | 'label' | 'policy'>>) => {
    props.onChange({
      ...props.form,
      rowValues: props.form.rowValues.map((value) => value.outputColumnId === outputColumnId ? { ...value, ...update } : value),
    });
  };
  const addGroupRowValue = (choice: GroupRowValueChoice) => {
    if (choice.fieldSource.kind !== 'CHOICE' || props.form.codedCategories.length > 0) return;
    const name = uniqueName(normalizedName(choice.label), getUsedGroupNames(props.form));
    const common = {
      outputColumnId: createOpaqueId('group-row-value'), policy: 'ALL' as const,
      name, label: choice.label, displayLabel: choice.label,
      scopeLabel: choice.scopeLabel ?? 'Current Group input', valueType: choice.valueType ?? 'string',
    };
    const rowValue: GroupRowValue = choice.inputColumnId
      ? { inputColumnId: choice.inputColumnId, ...common }
      : { fieldSource: choice.fieldSource, ...common };
    props.onChange({ ...props.form, rowValues: [...props.form.rowValues, rowValue] });
  };
  const removeGroupRowValue = (outputColumnId: string) => props.onChange({
    ...props.form,
    rowValues: props.form.rowValues.filter((value) => value.outputColumnId !== outputColumnId),
  });

  return (
    <fieldset data-testid="construction-group-add-columns" className={props.embedded ? 'grid gap-3 border-t border-slate-100 pt-3' : 'grid gap-3'} disabled={props.disabled || !props.supported}>
      <legend className={props.embedded ? 'sr-only' : 'm-0 text-sm font-semibold text-slate-900'}>Values from contributing records</legend>
      {props.embedded ? <h3 className="m-0 text-sm font-semibold text-slate-900">Record values <span className="font-normal text-slate-500">(optional)</span></h3> : null}
      <p className="m-0 text-xs text-slate-600">These do not change which rows form a group. They copy a field from the records inside each group into that group row.</p>
      {props.form.rowValues.length > 0 ? (
        <div data-testid="construction-group-retained-values" className="grid gap-1">
        {props.form.rowValues.map((value, index) => (
          <div key={value.outputColumnId} data-testid="construction-group-row-value" className="grid gap-2.5 border-b border-slate-100 py-3 last:border-b-0">
            <div className="min-w-0">
              <p className="m-0 truncate text-sm font-medium text-slate-800" title={value.displayLabel}>{value.displayLabel}</p>
              <p className="m-0 truncate text-xs text-slate-500" title={value.scopeLabel}>{value.scopeLabel}</p>
            </div>
            <div className="grid gap-2 sm:grid-cols-[8rem_minmax(0,1fr)_auto] sm:items-end">
            <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
              Keep values
              <select
                aria-label={`Keep values ${index + 1}`}
                value={value.policy}
                disabled={props.disabled || !props.supported}
                onChange={(event) => {
                  const policy = event.currentTarget.value;
                  if (policy === 'ALL' || policy === 'ONE') changeRowValue(value.outputColumnId, { policy });
                }}
                className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
              >
                <option value="ALL">All values</option>
                <option value="ONE">One value</option>
              </select>
            </label>
            <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
              Output name
              <input
                aria-label={`Added column name ${index + 1}`}
                value={value.name}
                disabled={props.disabled || !props.supported}
                onChange={(event) => changeRowValue(value.outputColumnId, { name: event.currentTarget.value })}
                className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
              />
            </label>
            <button type="button" aria-label={`Remove added column ${index + 1}`} disabled={props.disabled || !props.supported}
              onClick={() => removeGroupRowValue(value.outputColumnId)}
              className="justify-self-start whitespace-nowrap px-1 py-1.5 text-xs font-medium text-red-700 hover:underline">Remove</button>
            </div>
          </div>
        ))}
        </div>
      ) : null}
      <div data-testid="construction-group-add-columns-browser" className="grid gap-3">
        <div className="grid gap-2">
          <label className="grid max-w-xl gap-1 text-xs font-medium text-slate-700">
            Search fields to add
            <input
              type="search"
              aria-label="Search fields to add"
              data-testid="construction-group-add-columns-search"
              value={rowValueSearch}
              onChange={(event) => setRowValueSearch(event.currentTarget.value)}
              className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
            />
          </label>
          {rowValueSearch ? (
            <button type="button" data-testid="construction-group-add-columns-clear-search" onClick={() => setRowValueSearch('')}
              className="justify-self-start text-xs font-medium text-blue-800 hover:underline">Clear search</button>
          ) : null}
          <div data-testid="construction-group-add-columns-field-list" className={fieldPickerList}>
            {visibleRowValueChoices.map((choice) => {
              const choiceId = choice.fieldSource.kind === 'CHOICE' ? choice.fieldSource.choiceId : '';
              return (
                <label key={`row-value:${choiceId}`} className={fieldPickerToggleRow}>
                  <input
                    type="checkbox"
                    value={choiceId}
                    data-testid={`construction-group-add-column-choice-${choiceId}`}
                    aria-label={`Add column ${choice.label}`}
                    checked={false}
                    disabled={props.disabled || !props.supported || props.form.codedCategories.length > 0}
                    onChange={(event) => { if (event.currentTarget.checked) addGroupRowValue(choice); }}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{choice.label}</span>
                    <span className="block truncate text-xs text-slate-500" title={choice.scopeLabel}>{choice.scopeLabel ?? `${choice.sourceRole} · ${choice.resourceType}.${choice.canonicalPath}`}</span>
                  </span>
                </label>
              );
            })}
            {visibleInputColumns.map((column) => (
              <label key={`row-input:${column.id}`} className={fieldPickerToggleRow}>
                <input type="checkbox" value={column.id}
                  data-testid={`construction-group-add-column-input-${column.id}`}
                  aria-label={`Add column ${column.label}`} checked={false}
                  disabled={props.disabled || !props.supported || props.form.codedCategories.length > 0}
                  onChange={(event) => { if (event.currentTarget.checked) addInputColumn(column); }}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{column.label}</span>
                  <span className="block truncate text-xs text-slate-500" title={column.name}>Current Group input column · {column.name}</span>
                </span>
              </label>
            ))}
            {normalizedRowValueSearch && visibleRowValueChoices.length === 0 && visibleInputColumns.length === 0 ? (
              <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No other fields match this search.</p>
            ) : null}
            {!normalizedRowValueSearch && visibleRowValueChoices.length === 0 && visibleInputColumns.length === 0 ? (
              <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No additional scalar fields are available for this Group input.</p>
            ) : null}
          </div>
        </div>
      </div>
    </fieldset>
  );
};

export const GroupEditor = (props: {
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly groupFieldChoices: ReadonlyArray<GroupFieldChoice>;
  readonly groupRowValueChoices: ReadonlyArray<GroupRowValueChoice>;
  readonly rowValuesOnly?: boolean;
  readonly purpose?: 'group' | 'summarize';
  readonly semanticGroupChoices: ReadonlyArray<SemanticGroupChoice>;
  readonly semanticGroupChoicesIncomplete: boolean;
  readonly semanticGroupSupported: boolean;
  readonly codedPivotContext?: CodedPivotContext;
  readonly codedPivotSupported: boolean;
  readonly supported: boolean;
  readonly reason: string;
  readonly disabled: boolean;
  readonly expandSupported: boolean;
  readonly onExpandListColumn?: (columnId: string) => void;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const scalarColumns = scalarColumnsFor(props.stage);
  const usedNames = getUsedGroupNames(props.form);
  const [groupSearch, setGroupSearch] = useState('');
  const [activeTab, setActiveTab] = useState<'group' | 'summaries' | 'contributors'>(
    props.form.keys.length === 0 && props.form.fieldKeys.length === 0 && props.form.aggregates.length > 0
      ? 'summaries' : 'group',
  );
  const tabsId = useId();
  const groupOnly = !props.rowValuesOnly && props.purpose === 'group';
  const summarizeOnly = !props.rowValuesOnly && props.purpose === 'summarize';
  const normalizedGroupSearch = groupSearch.trim().toLocaleLowerCase();
  const groupKeyInputColumnIds = new Set([
    ...props.form.keys.map((key) => key.inputColumnId),
    ...props.form.fieldKeys.flatMap((key) => {
      const choice = groupFieldChoiceForKey(props.groupFieldChoices, key);
      return choice && 'inputColumnId' in choice && choice.inputColumnId ? [choice.inputColumnId] : [];
    }),
  ]);
  const contributorInputColumns = props.supported
    ? scalarColumns.filter((column) => column.id !== props.stage.rowIdentityColumn && column.name !== props.stage.rowIdentityColumn)
    : [];
  if (props.rowValuesOnly) {
    return <GroupRowValuesEditor
      form={props.form}
      inputColumns={contributorInputColumns}
      groupKeyInputColumnIds={groupKeyInputColumnIds}
      groupRowValueChoices={props.groupRowValueChoices}
      disabled={props.disabled}
      supported={props.supported}
      onChange={props.onChange}
    />;
  }
  const semanticKey = props.form.semanticKey;
  const semanticCategory = semanticKey?.semanticCategory;
  const codingIdentityKey = props.form.fieldKeys.find((key) => key.kind === 'CODING_IDENTITY');
  const hasCodedKey = Boolean(semanticKey || codingIdentityKey || props.form.codedCategories.length > 0);
  const directSourceStage = isSourceProjectionStage(props.stage);
  const selectedSemanticChoice = semanticCategory?.kind === 'CHOICE'
    ? props.semanticGroupChoices.find((choice) => choice.choiceId === semanticCategory.choiceId)
    : undefined;
  const selectedSemanticChoiceId = semanticCategory?.kind === 'CHOICE'
    ? semanticCategory.choiceId
    : undefined;
  const selectedScalarColumns = scalarColumns.filter((column) => props.form.keys.some((key) => key.inputColumnId === column.id));
  const visibleScalarColumns = scalarColumns.filter((column) =>
    !props.form.keys.some((key) => key.inputColumnId === column.id) &&
    (!normalizedGroupSearch || column.label.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const visibleSemanticChoices = semanticCategory?.kind === 'BINDING'
    ? []
    : props.semanticGroupChoices.filter((choice) => choice.choiceId !== selectedSemanticChoiceId &&
      (!normalizedGroupSearch || `${choice.label} ${choice.resourceType}`.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const showSavedSemanticKey = Boolean(semanticKey && (semanticCategory?.kind === 'BINDING' || !selectedSemanticChoice));
  const sourceFieldChoices = props.groupFieldChoices.filter((choice): choice is GroupFieldSourceChoice => choice.kind === 'FIELD_SOURCE');
  const fieldChoiceForKey = (key: GroupFieldKey): GroupFieldSourceChoice | undefined => groupFieldChoiceForKey(sourceFieldChoices, key);
  const selectedRelatedRouteIds = new Set(relatedRouteIdsForGroupFieldKeys(sourceFieldChoices, props.form.fieldKeys));
  const hasConflictingRelatedRoutes = selectedRelatedRouteIds.size > 1;
  const selectedRelatedFieldChoices = props.form.fieldKeys
    .map(fieldChoiceForKey)
    .filter((choice): choice is GroupFieldSourceChoice => Boolean(choice?.relatedOccurrence));
  const countRowsHelp = selectedRelatedFieldChoices.some((choice) => choice.valueKind === 'CODING_IDENTITY')
    ? 'Counts each contributing code occurrence. Repeated identical codes count separately; kept empty rows also count.'
    : selectedRelatedFieldChoices.some((choice) => choice.valueKind === 'SCALAR')
      ? 'Counts each matching related record; kept empty rows also count.'
      : 'Counts every input row.';
  const savedFieldBindingOutputIds = new Set(props.form.fieldKeys.flatMap((key) =>
    key.fieldSource.kind === 'BINDING' ? [key.outputColumnId] : [],
  ));
  const selectedSourceChoiceIds = new Set(props.form.fieldKeys.flatMap((key) => key.fieldSource.kind === 'CHOICE' ? [key.fieldSource.choiceId] : []));
  const visibleSourceFieldChoices = sourceFieldChoices.filter((choice) =>
    choice.fieldSource.kind === 'CHOICE' && !selectedSourceChoiceIds.has(choice.fieldSource.choiceId) &&
    !(choice.matchedOutputKeyId && savedFieldBindingOutputIds.has(choice.matchedOutputKeyId)) &&
    !(choice.valueKind === 'SCALAR' && choice.inputColumnId && scalarColumns.some((column) => column.id === choice.inputColumnId)) &&
    (!normalizedGroupSearch || `${choice.label} ${choice.scopeLabel ?? ''} ${choice.canonicalPath ?? ''} ${choice.sourceRole ?? ''}`.toLocaleLowerCase().includes(normalizedGroupSearch)));
  const semanticLabelCounts = new Map<string, number>();
  for (const column of [...selectedScalarColumns, ...visibleScalarColumns]) {
    const label = column.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const choice of [...(selectedSemanticChoice ? [selectedSemanticChoice] : []), ...visibleSemanticChoices]) {
    const label = choice.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const key of props.form.fieldKeys) {
    const label = key.displayLabel.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  for (const choice of visibleSourceFieldChoices) {
    const label = choice.label.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  if (semanticKey && showSavedSemanticKey) {
    const label = semanticKey.displayLabel.trim().toLocaleLowerCase();
    semanticLabelCounts.set(label, (semanticLabelCounts.get(label) ?? 0) + 1);
  }
  const selectedGroupLabels = [
    ...scalarColumns.filter((column) => props.form.keys.some((key) => key.inputColumnId === column.id)).map((column) => column.label),
    ...props.form.fieldKeys.map((key) => key.displayLabel),
    ...(semanticKey ? [semanticKey.displayLabel] : []),
    ...props.form.codedCategories.map((category) => category.display),
  ];
  const changeAggregate = (index: number, aggregate: GroupAggregate) => {
    const aggregates = props.form.aggregates.map((current, aggregateIndex) => aggregateIndex === index ? aggregate : current);
    props.onChange({
      ...props.form,
      aggregates,
      ...(props.form.codedCategories.length > 0 ? {
        codedPassthroughs: ensureGroupCodedPassthroughs(props.form, props.stage, [
          ...props.form.keys.map((key) => key.inputColumnId),
          ...aggregates.flatMap((item) => item.operation === 'COUNT_ROWS' ? [] : [item.inputColumnId]),
          ...props.form.rowValues.flatMap((value) => 'inputColumnId' in value ? [value.inputColumnId] : []),
        ]),
      } : {}),
    });
  };
  const removeSemanticKey = () => {
    const { semanticKey: _removed, ...formWithoutSemanticKey } = props.form;
    props.onChange({ ...formWithoutSemanticKey, keyOrder: props.form.keyOrder.filter((id) => id !== semanticKey?.outputColumnId) });
  };
  const removeFieldKey = (outputColumnId: string) => {
    const fieldKeys = props.form.fieldKeys.filter((key) => key.outputColumnId !== outputColumnId);
    const relatedRouteIds = relatedRouteIdsForGroupFieldKeys(sourceFieldChoices, fieldKeys);
    props.onChange({
      ...props.form,
      fieldKeys,
      keyOrder: props.form.keyOrder.filter((id) => id !== outputColumnId),
      unmatchedSourceKeyOutputIds: props.form.unmatchedSourceKeyOutputIds?.filter((id) => id !== outputColumnId),
      ...(relatedRouteIds.length > 1 ? { conflictingRelatedRouteIds: relatedRouteIds } : { conflictingRelatedRouteIds: undefined }),
    });
  };
  const addFieldKey = (choice: GroupFieldSourceChoice) => {
    const key = groupFieldKeyForChoice(props.form, choice);
    if (!key) return;
    props.onChange({ ...props.form, fieldKeys: [...props.form.fieldKeys, key], keyOrder: [...props.form.keyOrder, key.outputColumnId] });
  };
  const renderScalarChoice = (column: ReshapeColumn, checked: boolean) => (
    <label key={`scalar:${column.id}`} className={fieldPickerToggleRow}>
      <input
        type="checkbox"
        value={column.id}
        checked={checked}
        aria-label={`Group by ${column.label}`}
            disabled={props.disabled || !props.supported}
        onChange={() => props.onChange(checked
          ? {
              ...props.form,
              keys: props.form.keys.filter((key) => key.inputColumnId !== column.id),
              keyOrder: props.form.keyOrder.filter((id) => !props.form.keys.some((key) => key.inputColumnId === column.id && key.outputColumnId === id)),
            }
          : addGroupKey(props.form, props.stage, column.id))}
      />
      <span className="min-w-0 truncate" title={column.label}>{column.label}</span>
    </label>
  );
  const renderSemanticChoice = (choice: SemanticGroupChoice, checked: boolean) => {
    const duplicateLabel = (semanticLabelCounts.get(choice.label.trim().toLocaleLowerCase()) ?? 0) > 1;
    const disambiguation = duplicateLabel ? compactSemanticScope(choice.scopeLabel) : undefined;
    const disabled = props.disabled || !props.supported || !directSourceStage || !props.semanticGroupSupported ||
      props.form.codedCategories.length > 0 && !checked || Boolean(codingIdentityKey) && !checked || Boolean(semanticKey) && !checked;
    return (
      <div key={`semantic:${choice.choiceId}`} data-testid="construction-group-semantic-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className={fieldPickerToggleRow}>
          <input
            type="checkbox"
            value={choice.choiceId}
            data-semantic-choice-id={choice.choiceId}
            data-testid={`construction-group-semantic-choice-${choice.choiceId}`}
            checked={checked}
            aria-label={`Group by ${choice.label}${disambiguation ? ` (${disambiguation})` : ''}`}
            disabled={disabled}
            aria-describedby={semanticKey && !checked ? 'construction-group-coded-key-limit' : undefined}
            onChange={(event) => {
              if (!event.currentTarget.checked) {
                removeSemanticKey();
                return;
              }
              if (!semanticKey && !codingIdentityKey && props.form.codedCategories.length === 0) {
                const next = groupSemanticKeyForChoice(props.form, choice);
                props.onChange({ ...props.form, semanticKey: next, keyOrder: [...props.form.keyOrder, next.outputColumnId] });
              }
            }}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{choice.label}</span>
            {disambiguation ? <span className="block truncate text-xs text-slate-500" title={choice.scopeLabel}>{disambiguation}</span> : null}
          </span>
        </label>
        <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-field-details-${choice.choiceId}`}>
          <summary className="cursor-pointer">Details</summary>
          <p className="mb-0 mt-1 break-words">{choice.scopeLabel}</p>
        </details>
      </div>
    );
  };
  const renderSelectedFieldKey = (key: GroupFieldKey) => {
    const matchedChoice = fieldChoiceForKey(key);
    const unmatchedBinding = key.fieldSource.kind === 'BINDING' && !matchedChoice;
    const sourceDescription = matchedChoice
      ? matchedChoice.ownerResourceType
      : groupFieldSourcePresentation(key.fieldSource)?.ownerResourceType;
    const detailDescription = matchedChoice
      ? [matchedChoice.relatedOccurrence?.routeLabel, matchedChoice.scopeLabel].filter((part): part is string => Boolean(part)).join(' · ')
      : key.scopeLabel;
    return (
      <div key={`field-source:${key.outputColumnId}`} data-testid="construction-group-field-source-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className={fieldPickerToggleRow}>
          <input
            type="checkbox"
            value={key.outputColumnId}
            checked
            aria-label={`Group by ${key.displayLabel}`}
            disabled={props.disabled || !props.supported}
            onChange={() => removeFieldKey(key.outputColumnId)}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{key.displayLabel}</span>
            {sourceDescription ? <span className="block break-words text-xs text-slate-500">{sourceDescription}</span> : null}
            {unmatchedBinding ? <span data-testid={`construction-group-stale-field-${key.outputColumnId}`} className="block text-xs font-medium text-amber-800">
              Saved source field has no current capability match. Remove it or refresh the Group capabilities before applying changes.
            </span> : null}
          </span>
        </label>
        {detailDescription ? (
          <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-saved-field-details-${key.outputColumnId}`}>
            <summary className="cursor-pointer">Details</summary>
            <p className="mb-0 mt-1 break-words">{detailDescription}</p>
          </details>
        ) : null}
      </div>
    );
  };
  const renderFieldChoice = (choice: GroupFieldSourceChoice) => {
    const routeId = choice.relatedOccurrence?.routeId;
    const differentRoute = Boolean(routeId && (
      hasConflictingRelatedRoutes || selectedRelatedRouteIds.size > 0 && !selectedRelatedRouteIds.has(routeId)
    ));
    const disabled = props.disabled || !props.supported ||
      (choice.valueKind === 'CODING_IDENTITY' && hasCodedKey) ||
      (choice.valueKind === 'SCALAR' && props.form.codedCategories.length > 0) || differentRoute;
    const choiceId = choice.fieldSource.kind === 'CHOICE' ? choice.fieldSource.choiceId : '';
    const sourceDescription = choice.ownerResourceType;
    const detailDescription = [choice.relatedOccurrence?.routeLabel, choice.scopeLabel]
      .filter((part): part is string => Boolean(part)).join(' · ');
    const describedBy = [
      choice.valueKind === 'CODING_IDENTITY' && hasCodedKey ? 'construction-group-coded-key-limit' : undefined,
      differentRoute ? 'construction-group-related-route-limit' : undefined,
    ].filter((id): id is string => Boolean(id)).join(' ') || undefined;
    return (
      <div key={`source-choice:${choiceId}`} data-testid="construction-group-field-source-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className={fieldPickerToggleRow}>
          <input
            type="checkbox"
            value={choiceId}
            data-testid={`construction-group-field-choice-${choiceId}`}
            checked={false}
            aria-label={`Group by ${choice.label}`}
            disabled={disabled}
            aria-describedby={describedBy}
            onChange={(event) => { if (event.currentTarget.checked) addFieldKey(choice); }}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{choice.label}</span>
            <span className="block break-words text-xs text-slate-500">{sourceDescription}</span>
            {differentRoute ? <span data-testid={`construction-group-different-route-${choiceId}`} className="block text-xs font-medium text-amber-800">
              {hasConflictingRelatedRoutes
                ? 'Saved fields use multiple related routes. Remove fields until one route remains before choosing another related field.'
                : 'Available from a different related route. Remove the selected route field to choose this one.'}
            </span> : null}
          </span>
        </label>
        {detailDescription ? (
          <details className="ml-6 mt-1 text-xs text-slate-600" data-testid={`construction-group-choice-details-${choiceId}`}>
            <summary className="cursor-pointer">Details</summary>
            <p className="mb-0 mt-1 break-words">{detailDescription}</p>
          </details>
        ) : null}
      </div>
    );
  };
  const renderSavedSemanticKey = () => {
    if (!semanticKey || !semanticCategory) return null;
    const semanticValue = semanticCategory.kind === 'BINDING'
      ? `binding:${semanticCategory.bindingId}:${semanticCategory.ruleVersion}`
      : semanticCategory.choiceId;
    const labelKey = semanticKey.displayLabel.trim().toLocaleLowerCase();
    const disambiguation = (semanticLabelCounts.get(labelKey) ?? 0) > 1
      ? compactSemanticScope(semanticKey.scopeLabel)
      : undefined;
    return (
      <div data-testid="construction-group-semantic-option" className="rounded px-2 py-1.5 hover:bg-slate-50">
        <label className={fieldPickerToggleRow}>
          <input
            type="checkbox"
            value={semanticValue}
            checked
            aria-label={`Group by ${semanticKey.displayLabel}${disambiguation ? ` (${disambiguation})` : ''}`}
            disabled={props.disabled || !props.supported || !directSourceStage || !props.semanticGroupSupported}
            onChange={removeSemanticKey}
          />
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-slate-800">{semanticKey.displayLabel}</span>
            {disambiguation ? <span className="block truncate text-xs text-slate-500" title={semanticKey.scopeLabel}>{disambiguation}</span> : null}
          </span>
        </label>
        <details className="ml-6 mt-1 text-xs text-slate-600" data-testid="construction-group-saved-field-details">
          <summary className="cursor-pointer">Details</summary>
          <p className="mb-0 mt-1 break-words">{semanticKey.scopeLabel}</p>
        </details>
      </div>
    );
  };

  return (
    <section aria-label="Summarize into groups" data-testid="construction-reshape-group" className="construction-reshape-group-editor grid content-start gap-4">
      {!props.supported ? <p role="status" className="rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{props.reason}</p> : null}

      {groupOnly ? null : (
      <div role="tablist" aria-label="Group editor sections" className="flex flex-wrap gap-1 border-b border-slate-200 pb-2">
        {([
          ['group', 'Group by', selectedGroupLabels.length],
          ['summaries', 'Summaries', props.form.aggregates.length],
        ] as const).map(([tab, label, count]) => (
          <button
            key={tab}
            id={`${tabsId}-tab-${tab}`}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            aria-controls={`${tabsId}-panel-${tab}`}
            data-testid={`construction-group-tab-${tab}`}
            onClick={() => setActiveTab(tab)}
            className={activeTab === tab
              ? 'rounded border border-slate-300 bg-blue-50 px-3 py-1.5 text-sm font-medium text-blue-900'
              : 'rounded border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50'}
          >
            {label}{count > 0 ? ` (${count})` : ''}
          </button>
        ))}
      </div>
      )}
      <div
        id={`${tabsId}-panel-group`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-group`}
        className={groupOnly || activeTab === 'group' ? 'grid items-start gap-4' : 'hidden'}
      >
        <fieldset className="grid min-w-0 content-start gap-3" disabled={props.disabled || !props.supported}>
          <legend className="m-0 text-sm font-semibold text-slate-900">Group by</legend>
          <p className="m-0 text-xs text-slate-600">These fields define the groups. Each unique combination becomes one output row.</p>
          <div data-testid="construction-group-selected-keys" className="grid gap-1">
            {selectedScalarColumns.map((column) => renderScalarChoice(column, true))}
            {props.form.fieldKeys.map(renderSelectedFieldKey)}
            {selectedSemanticChoice ? renderSemanticChoice(selectedSemanticChoice, true) : null}
            {showSavedSemanticKey ? renderSavedSemanticKey() : null}
          </div>
          {sourceFieldChoices.some((choice) => choice.relatedOccurrence) ? (
            <p id="construction-group-related-route-limit" data-testid="construction-group-related-route-guidance" role="status" className="m-0 rounded bg-slate-50 px-2 py-1.5 text-xs text-slate-600">
              Related fields can be combined with root fields and fields from one matching source route. Fields from other routes stay visible but disabled.
            </p>
          ) : null}
          {hasConflictingRelatedRoutes || props.form.conflictingRelatedRouteIds?.length ? (
            <p data-testid="construction-group-related-route-conflict" role="alert" className="m-0 rounded bg-amber-50 px-2 py-1.5 text-xs text-amber-900">
              This saved Group combines fields from multiple related routes. The saved fields remain selected, but Apply is blocked until you remove fields so only one related route remains.
            </p>
          ) : null}
          {props.form.unmatchedSourceKeyOutputIds?.length ? (
            <p data-testid="construction-group-stale-binding-warning" role="alert" className="m-0 rounded bg-amber-50 px-2 py-1.5 text-xs text-amber-900">
              A saved source field has no current capability match. Its saved binding is preserved below, and Apply is blocked until you remove it or refresh this Group’s capabilities.
            </p>
          ) : null}
          <div data-testid="construction-group-by-browser" className="grid gap-2">
              <label className="grid gap-1 text-xs font-medium text-slate-700">
                Search fields
                <input
                  type="search"
                  aria-label="Search fields to group by"
                  data-testid="construction-group-by-search"
                  value={groupSearch}
                  onChange={(event) => setGroupSearch(event.currentTarget.value)}
                  className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm"
                />
              </label>
              {groupSearch ? (
                <button type="button" data-testid="construction-group-by-clear-search" onClick={() => setGroupSearch('')}
                  className="justify-self-start text-xs font-medium text-blue-800 hover:underline">Clear search</button>
              ) : null}
              <div data-testid="construction-group-by-field-list" className={fieldPickerList}>
                {visibleScalarColumns.map((column) => renderScalarChoice(column, false))}
                {visibleSourceFieldChoices.map(renderFieldChoice)}
                {visibleSemanticChoices.map((choice) => renderSemanticChoice(choice, false))}
                {normalizedGroupSearch && visibleScalarColumns.length === 0 && visibleSourceFieldChoices.length === 0 && visibleSemanticChoices.length === 0 ? (
                  <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">No other fields match this search.</p>
                ) : null}
                {!normalizedGroupSearch && selectedScalarColumns.length === 0 && !selectedSemanticChoice && !showSavedSemanticKey &&
                  props.form.fieldKeys.length === 0 && visibleScalarColumns.length === 0 && visibleSourceFieldChoices.length === 0 && visibleSemanticChoices.length === 0 ? (
                  <p role="status" className="m-0 px-2 py-1 text-sm text-slate-600">
                    No eligible fields are available to group by. You can still summarize all input rows.
                  </p>
                ) : null}
              </div>
          </div>
          {props.semanticGroupChoicesIncomplete ? (
            <p role="status" className="m-0 text-xs text-amber-800">Some coded fields may be missing from this list; search only covers the fields currently available.</p>
          ) : null}
          {hasCodedKey ? (
            <p id="construction-group-coded-key-limit" role="status" className="m-0 text-xs text-slate-600">
              A Group can use one coded identity field. Remove {semanticKey?.displayLabel ?? codingIdentityKey?.displayLabel ?? props.form.codedCategories[0]?.display ?? 'the coded field'} before choosing a different coded field.
            </p>
          ) : null}
          {props.form.codedCategories.length > 0 ? (
            <p role="status" className="m-0 text-xs text-slate-600">Remove the selected recorded-code values before choosing a coded field.</p>
          ) : null}
          {selectedGroupLabels.length > 0 || props.form.aggregates.length > 0 ? (
            <p data-testid="construction-group-grain-summary" role="status" aria-live="polite" className="m-0 rounded bg-blue-50 px-2 py-1.5 text-sm text-slate-700">
              {selectedGroupLabels.length > 0
                ? `One row per unique combination of ${readableFieldCombination(selectedGroupLabels)}.`
                : 'With no fields selected, one row summarizes all input rows.'}
            </p>
          ) : null}
          {selectedGroupLabels.length > 0 ? (
            <label className="grid gap-1 text-xs font-medium text-slate-700">
              Rows with a missing group key
              <select
                aria-label="Missing group key policy"
                value={props.form.missingKeyPolicy}
                disabled={props.disabled || !props.supported}
                onChange={(event) => {
                  const policy = event.currentTarget.value;
                  if (policy === 'GROUP' || policy === 'EXCLUDE' || policy === 'ERROR') {
                    props.onChange({ ...props.form, missingKeyPolicy: policy });
                  }
                }}
                className="justify-self-start rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
              >
                <option value="GROUP">Keep in one missing-key group</option>
                <option value="EXCLUDE">Exclude from summaries</option>
                <option value="ERROR">Stop the operation</option>
              </select>
            </label>
          ) : null}
          {listColumnsFor(props.stage).length > 0 ? (
            <ListColumnExpandChoices
              columns={listColumnsFor(props.stage)}
              supported={props.expandSupported}
              disabled={props.disabled}
              inlineGrainAction
              onExpand={props.onExpandListColumn}
            />
          ) : null}
          {props.codedPivotSupported && props.codedPivotContext ? (
            <details data-testid="construction-group-recorded-values-advanced" className="rounded border border-slate-200 p-2">
              <summary className="cursor-pointer text-sm font-medium text-blue-800">Advanced: choose specific recorded values</summary>
              <p className="mb-2 mt-1 text-xs text-slate-600">Use this when you need selected code values rather than grouping by the full coded field.</p>
              <GroupCodedValuePicker
                context={props.codedPivotContext}
                form={props.form}
                stage={props.stage}
                disabled={props.disabled || !props.supported || Boolean(props.form.semanticKey) || Boolean(codingIdentityKey)}
                onChange={props.onChange}
              />
            </details>
          ) : null}
          {selectedGroupLabels.length > 0 || props.form.aggregates.length > 0 ? (
            <GroupRowValuesEditor
              embedded
              form={props.form}
              inputColumns={contributorInputColumns}
              groupKeyInputColumnIds={groupKeyInputColumnIds}
              groupRowValueChoices={props.groupRowValueChoices}
              disabled={props.disabled}
              supported={props.supported}
              onChange={props.onChange}
            />
          ) : null}
        </fieldset>

      </div>

      <fieldset
        id={`${tabsId}-panel-summaries`}
        role="tabpanel"
        aria-labelledby={`${tabsId}-tab-summaries`}
        className={!groupOnly && activeTab === 'summaries' ? 'grid min-w-0 content-start gap-3' : 'hidden'}
        disabled={props.disabled || !props.supported}
      >
          <legend className="m-0 text-sm font-semibold text-slate-900">Summaries</legend>
          <p className="m-0 text-xs text-slate-600">
            {props.form.aggregates.length === 0
              ? 'No summaries. Group keys alone produce one row per distinct combination.'
              : 'Add another summary if this group needs more calculated outputs.'}
          </p>
          {props.form.aggregates.map((aggregate, index) => {
            const inputColumns = aggregateInputColumnsFor(props.stage, aggregate.operation);
            const operation: GroupAggregateKind = aggregate.operation;
            return (
              <div key={aggregate.outputColumnId} data-testid={`construction-reshape-aggregate-${index}`} className="grid gap-2.5 border-b border-slate-100 py-3 last:border-b-0">
                <div className="grid gap-2 sm:grid-cols-2">
                  <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                    Summary
                    <select
                      aria-label={`Summary ${index + 1}`}
                      value={operation}
                      disabled={props.disabled || !props.supported}
                      onChange={(event) => {
                        const selected = groupAggregateKindFromInput(event.currentTarget.value);
                        if (!selected) return;
                        const replacement = replaceGroupAggregate(aggregate, selected, props.stage, getUsedGroupNames(props.form, aggregate.outputColumnId));
                        changeAggregate(index, replacement);
                      }}
                      className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                    >
                      <option value="COUNT_ROWS">Count rows</option>
                      <option value="COUNT_NON_NULL">Count present values</option>
                      <option value="COUNT_DISTINCT">Count distinct values</option>
                      <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
                      <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
                    </select>
                  </label>
                  {aggregate.operation !== 'COUNT_ROWS' ? (
                    <label className="grid min-w-0 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                      Field
                      <select
                        aria-label={`Summary field ${index + 1}`}
                        value={aggregate.inputColumnId}
                        disabled={props.disabled || !props.supported}
                        onChange={(event) => {
                          const input = inputColumns.find((column) => column.id === event.currentTarget.value);
                          if (!input) return;
                          changeAggregate(index, {
                            ...aggregate,
                            inputColumnId: input.id,
                            name: uniqueName(aggregateNameFor(aggregate.operation, input), getUsedGroupNames(props.form, aggregate.outputColumnId)),
                            label: `${operationLabel(aggregate.operation)} of ${input.label}`,
                          });
                        }}
                        className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                      >
                        {inputColumns.map((column) => <option key={column.id} value={column.id}>{column.label} ({column.type ?? 'unknown type'})</option>)}
                      </select>
                      {inputColumns.length === 0 ? <span role="status" className="text-xs font-normal normal-case tracking-normal text-amber-900">No compatible columns were returned for this summary.</span> : null}
                    </label>
                  ) : (
                    <p className="m-0 self-end pb-2 text-xs font-normal normal-case tracking-normal text-slate-500">{countRowsHelp}</p>
                  )}
                </div>
                <div className="flex items-end gap-2">
                  <label className="grid min-w-0 flex-1 gap-1 text-[11px] font-medium uppercase tracking-wide text-slate-500">
                    Output name
                    <input
                      aria-label={`Summary output name ${index + 1}`}
                      value={aggregate.name}
                      disabled={props.disabled || !props.supported}
                      onChange={(event) => changeAggregate(index, { ...aggregate, name: event.currentTarget.value })}
                      className="min-w-0 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal normal-case tracking-normal text-slate-900"
                    />
                  </label>
                  <button type="button" aria-label={`Remove summary ${index + 1}`} disabled={props.disabled || !props.supported} onClick={() => props.onChange({ ...props.form, aggregates: props.form.aggregates.filter((_, itemIndex) => itemIndex !== index) })} className="mb-0.5 whitespace-nowrap rounded px-2 py-1 text-xs font-medium text-slate-500 hover:bg-red-50 hover:text-red-700">Remove</button>
                </div>
              </div>
            );
          })}
          <label className="grid max-w-xs gap-1 text-xs font-medium text-slate-700">
            {props.form.aggregates.length === 0 ? 'Choose a summary' : 'Add another summary'}
            <select
              aria-label="Add summary"
              data-testid="construction-reshape-add-summary"
              value=""
              disabled={props.disabled || !props.supported}
              onChange={(event) => {
                const operation = groupAggregateKindFromInput(event.currentTarget.value);
                if (!operation) return;
                props.onChange({
                  ...props.form,
                  aggregates: [...props.form.aggregates, makeAggregate(operation, props.stage, usedNames)],
                });
              }}
              className="rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
            >
              <option value="">{props.form.aggregates.length === 0 ? 'Select a summary to add' : 'Select another summary'}</option>
              <option value="COUNT_ROWS">Count rows</option>
              <option value="COUNT_NON_NULL">Count present values</option>
              <option value="COUNT_DISTINCT">Count distinct values</option>
              <option value="SUM" disabled={numericColumnsFor(props.stage).length === 0}>Sum numeric values</option>
              <option value="MEAN" disabled={numericColumnsFor(props.stage).length === 0}>Mean numeric values</option>
            </select>
          </label>
      </fieldset>

      {!outputNamesAreValid(groupOutputColumns(props.form, props.stage)) ? (
        <p role="status" className="text-sm text-amber-900">Give every output a unique column name using letters, numbers, or underscores, and add a label.</p>
      ) : null}
    </section>
  );
};
