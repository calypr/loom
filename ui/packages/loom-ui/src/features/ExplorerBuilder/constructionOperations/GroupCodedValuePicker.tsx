import type { CodedPivotContext } from '../session/codedPivotContext';
import React from 'react';
import { useGroupCodedInventory } from '../session/useGroupCodedInventory';
import { semanticConceptLabel } from '../model/catalogItems';
import { createOpaqueId, uniqueName } from '../model/editorShared';
import { codedCategoryKey, codedFamilyKey, ensureGroupCodedPassthroughs, getUsedGroupNames, type GroupCodedCategory, type GroupForm, type GroupKey, type ReshapeStage } from '../model/reshapeForms';

export const GroupCodedValuePicker = (props: {
  readonly context: CodedPivotContext;
  readonly form: GroupForm;
  readonly stage: ReshapeStage;
  readonly disabled: boolean;
  readonly onChange: (form: GroupForm) => void;
}) => {
  const {
    available, choicesLoading, errorText, inventoryReady, inventoryView, loadNextPage,
    readiness, readinessQuery, sourceListIncomplete, sources, unavailableSavedKeys,
  } = useGroupCodedInventory(props);

  const selectedFamily = props.form.codedCategories[0]?.familyKey;
  const removeSelectedCategory = (category: GroupCodedCategory) => {
    props.onChange({
      ...props.form,
      codedCategories: props.form.codedCategories.filter((candidate) => candidate.key !== category.key),
      keys: props.form.keys.filter((groupKey) => groupKey.outputColumnId !== category.groupOutputColumnId),
    });
  };
  const toggle = (entry: (typeof available)[number]) => {
    const { item, source, familyKey } = entry;
    const key = codedCategoryKey(familyKey, item.system, item.code);
    const prior = props.form.codedCategories.find((category) => category.key === key);
    if (prior) {
      removeSelectedCategory(prior);
      return;
    }
    if (selectedFamily && selectedFamily !== familyKey || props.form.codedCategories.length >= 50 || !item.constructionChoice) return;
    const label = semanticConceptLabel(item);
    const usedNames = getUsedGroupNames(props.form);
    const groupOutputName = uniqueName(item.code, usedNames);
    const helperOutputName = uniqueName(item.code, new Set([
      ...props.stage.columns.map((column) => column.name.toLowerCase()),
      ...props.form.codedCategories.map((category) => category.helperOutputName.toLowerCase()),
    ]));
    const groupKey: GroupKey = {
      inputColumnId: createOpaqueId('coded-group-input'),
      outputColumnId: createOpaqueId('coded-group-output'),
      name: groupOutputName,
      label,
    };
    const codedCategory: GroupCodedCategory = {
      choice: 'ready',
      key,
      familyKey,
      system: item.system,
      code: item.code,
      display: label,
      valueType: item.valueType,
      sourceChoiceId: source.choiceId,
      categoryChoiceId: item.constructionChoice.choiceId,
      helperOutputColumnId: groupKey.inputColumnId,
      groupOutputColumnId: groupKey.outputColumnId,
      groupOutputName: groupKey.name,
      groupOutputLabel: groupKey.label,
      helperOutputName,
      helperOutputLabel: label,
    };
    const codedCategories = [...props.form.codedCategories, codedCategory];
    const nextForm = { ...props.form, codedCategories, keys: [...props.form.keys, groupKey] };
    props.onChange({
      ...nextForm,
      codedPassthroughs: ensureGroupCodedPassthroughs(nextForm, props.stage, [
        ...nextForm.keys.flatMap((key) => codedCategories.some((category) => category.helperOutputColumnId === key.inputColumnId) ? [] : [key.inputColumnId]),
        ...nextForm.aggregates.flatMap((aggregate) => aggregate.operation === 'COUNT_ROWS' ? [] : [aggregate.inputColumnId]),
        ...nextForm.rowValues.flatMap((value) => 'inputColumnId' in value ? [value.inputColumnId] : []),
      ]),
    });
  };
  const onChoicesScroll = (event: React.UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    if (element.scrollHeight - element.scrollTop - element.clientHeight <= 24) loadNextPage();
  };
  if (inventoryReady && !choicesLoading && !errorText && !sourceListIncomplete && inventoryView?.complete && available.length === 0
      && props.form.codedCategories.length === 0) return null;

  return (
    <div data-testid="construction-group-coded-values" className="grid gap-2 rounded-lg border border-blue-200 bg-blue-50/50 p-3">
      <div>
        <p className="m-0 text-sm font-semibold text-slate-800">Coded values</p>
        <p className="mb-0 mt-1 text-xs text-slate-600">Choose coded values to group by.</p>
      </div>
      {readinessQuery.isLoading && !readiness ? <p role="status" className="m-0 text-xs text-slate-600">Checking coded-value availability…</p> : null}
      {!readinessQuery.error && readiness && !inventoryReady ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {!readiness.buildId
            ? 'No current coded-value catalog is available.'
            : `The coded-value catalog is ${readiness.state}. Choices will appear when it is complete.`}
        </p>
      ) : null}
      {inventoryReady && choicesLoading ? <p role="status" className="m-0 text-xs text-slate-600">Loading coded values…</p> : null}
      {errorText ? <p role="alert" className="m-0 text-xs text-red-700">{errorText}</p> : null}
      {sourceListIncomplete && available.length > 0 ? <p role="status" className="m-0 text-xs text-amber-900">The direct source-field list may be incomplete; additional coded choices may exist.</p> : null}
      {selectedFamily && props.form.codedCategories.some((category) => category.familyKey !== selectedFamily) ? (
        <p role="status" className="m-0 text-xs text-amber-900">These selected concepts come from different source fields. A group can use coded values from one direct source field at a time; remove the conflicting selection to continue.</p>
      ) : selectedFamily ? (
        <p className="m-0 text-xs text-slate-600">Selected source field: {sources.find((source) => codedFamilyKey(source.bindingId, source.sourcePath) === selectedFamily)?.title ?? 'saved direct source'}. Choose coded values from this source field.</p>
      ) : null}
        <div
        aria-label="Coded value choices by direct source field"
        className="grid max-h-[min(14rem,45vh)] grid-cols-1 gap-3 overflow-y-auto"
        onScroll={onChoicesScroll}
        role="region"
        tabIndex={0}
      >
        {sources.map((source) => {
          const sourceEntries = available.filter((entry) => entry.source.choiceId === source.choiceId);
          if (sourceEntries.length === 0 && !inventoryView?.complete) return null;
          return (
            <section key={source.choiceId} aria-label={`Coded values from ${source.title}`} className="grid content-start gap-1">
              <h5 className="m-0 text-xs font-semibold text-slate-700">{source.title}</h5>
              {sourceEntries.map((entry) => {
                const { item, familyKey } = entry;
                const key = codedCategoryKey(familyKey, item.system, item.code);
                const selected = props.form.codedCategories.some((category) => category.key === key);
                const familyConflict = Boolean(selectedFamily && selectedFamily !== familyKey);
                return (
                  <label key={key} data-testid="construction-group-coded-option" className={`flex min-h-10 items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
                    <input
                      type="checkbox"
                      aria-label={`Group by coded value: ${semanticConceptLabel(item)}`}
                      checked={selected}
                      disabled={props.disabled || familyConflict || props.form.codedCategories.length >= 50 && !selected}
                      onChange={() => toggle(entry)}
                    />
                    <span><span className="block font-medium text-slate-800">{semanticConceptLabel(item)}</span><span className="block text-xs text-slate-500">{item.sourcePath} · {item.valueType} · {item.occurrences.toLocaleString()} observed</span></span>
                  </label>
                );
              })}
            </section>
          );
        })}
        {props.form.codedCategories.filter((category) => !available.some((entry) =>
          category.key === codedCategoryKey(entry.familyKey, entry.item.system, entry.item.code))).map((category) => {
          const familyConflict = Boolean(selectedFamily && selectedFamily !== category.familyKey);
          return (
            <label key={category.key} data-testid="construction-group-coded-option" className={`flex items-start gap-2 rounded border border-slate-200 bg-white px-2 py-1.5 text-sm ${familyConflict ? 'opacity-60' : 'hover:bg-slate-50'}`}>
              <input
                type="checkbox"
                aria-label={`Group by coded value: ${category.display}`}
                checked
                disabled={props.disabled}
                onChange={() => removeSelectedCategory(category)}
              />
              <span>
                <span className="block font-medium text-slate-800">{category.display}</span>
                <span className="block text-xs text-slate-500">{category.system} · {category.code} · selected</span>
                {unavailableSavedKeys.has(category.key) ? <span role="status" className="block text-xs text-amber-800">This coded value is no longer available. Remove it to continue.</span> : null}
              </span>
            </label>
          );
        })}
        {inventoryView?.hasMore ? <p className="m-0 text-xs text-slate-500">Scroll for more choices from these source fields.</p> : null}
      </div>
      {inventoryReady && !choicesLoading && !errorText && available.length === 0 ? (
        <p role="status" className="m-0 text-xs text-slate-600">
          {sourceListIncomplete
            ? 'No ready coded values were found among the listed direct source fields; the source list may be incomplete.'
            : inventoryView?.complete
              ? 'No ready coded values are available from these direct source fields.'
              : 'Coded values from direct source fields are still loading.'}
        </p>
      ) : null}
      {props.form.codedCategories.length > 0 ? <p className="m-0 text-xs text-slate-600">{props.form.codedCategories.length} coded value{props.form.codedCategories.length === 1 ? '' : 's'} selected.</p> : null}
    </div>
  );
};
