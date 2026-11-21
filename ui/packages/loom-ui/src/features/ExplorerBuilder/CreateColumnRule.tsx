import { useState, type ReactNode } from 'react';
import type { ImportedSourceParameters, SourceRuleCreate } from '../../types';
import { fieldPickerList, fieldPickerToggleRow } from './components/fieldPickerStyles';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';
import { isCategoryCodingRule, isIdentifierColumnRule, type ColumnRuleOccurrence, type ColumnRulePreset } from './sourceColumnRule';

const defaultColumnLimit = 128;
const ruleNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function CreateColumnRule({ kind, occurrence, preset, initialParameters, identifierSystems, categorySystems, identifierInventoryLoading, identifierInventoryUnavailable, extensionSourcePaths, fieldPathChoices, disabled, reviewing, inline, onCancel, renderReview }: {
  readonly kind: SourceRuleCreate['kind'];
  readonly occurrence: ColumnRuleOccurrence;
  readonly preset: ColumnRulePreset;
  readonly initialParameters: ImportedSourceParameters;
  readonly identifierSystems?: ReadonlyArray<string>;
  readonly categorySystems?: ReadonlyArray<string>;
  readonly identifierInventoryLoading?: boolean;
  readonly identifierInventoryUnavailable?: boolean;
  readonly extensionSourcePaths?: ReadonlyArray<string>;
  readonly fieldPathChoices?: ReadonlyArray<string>;
  readonly disabled: boolean;
  readonly reviewing: boolean;
  readonly inline?: { readonly active: boolean; readonly onActivate: () => void };
  readonly onCancel: () => void;
  readonly renderReview: (rule: SourceRuleCreate, leaveReview: () => void, disabled: boolean, label: string, automatic?: boolean) => ReactNode;
}) {
  const [parameters, setParameters] = useState(initialParameters);
  const [systemOverrides, setSystemOverrides] = useState<ReadonlySet<string>>(() => new Set());
  const [columnLimit, setColumnLimit] = useState(String(defaultColumnLimit));
  const limit = Number(columnLimit);
  const rule = parameters.dynamic;
  const extension = parameters.extension;
  const catalog = parameters.catalog;
  const selectedCatalogPath = initialParameters.catalog?.includePaths?.length === 1
    ? initialParameters.catalog.includePaths[0] : undefined;
  const validName = ruleNamePattern.test(rule?.name ?? extension?.name ?? catalog?.name ?? '');
  const prefix = rule?.columnPrefix ?? extension?.columnPrefix;
  const validPrefix = !prefix || ruleNamePattern.test(prefix);
  const matchesPreset = rule && (preset.id === 'identifier' ? isIdentifierColumnRule(rule) : preset.id === 'categoryCoding' && isCategoryCodingRule(rule));
  const validCatalogLimit = Number.isInteger(limit) && limit >= 1 && limit <= 128;
  const validExtension = extension?.source.select && extensionSourcePaths?.includes(extension.source.select);
  const comparableParameters = (value: ImportedSourceParameters) => JSON.stringify(value, (_, item: unknown) =>
    Array.isArray(item) && item.length === 0 ? undefined : item,
  );
  const category = preset.id === 'categoryCoding';
  const systemChoices = category ? categorySystems : preset.id === 'identifier' ? identifierSystems : undefined;
  const systems = systemChoices ?? [];
  const outputKey = (system: string) => rule?.key?.call === 'last_segment'
    ? system.split(/[/#]/).filter(Boolean).at(-1) ?? '' : system;
  const chooseIdentifiers = !category && systemChoices !== undefined;
  const selectByDefault = category && !inline;
  const isSystemSelected = (system: string) => selectByDefault !== systemOverrides.has(system);
  const includedSystems = systems.filter(isSystemSelected);
  const includedKeys = new Set(includedSystems.map(outputKey));
  const excludedKeys = [...new Set(systems.filter(system => !isSystemSelected(system)).map(outputKey))];
  const namingConflict = excludedKeys.some((key) => includedKeys.has(key));
  const ruleParameters = rule && excludedKeys.length
    ? { dynamic: { ...rule, outputs: excludedKeys.map((key) => ({ key, enabled: false })) } }
    : parameters;
  const changed = comparableParameters(parameters) !== comparableParameters(initialParameters) ||
    Boolean(catalog && limit !== defaultColumnLimit) || (chooseIdentifiers || inline ? includedSystems.length > 0 : excludedKeys.length > 0);
  const canReview = Boolean(validName && validPrefix && !namingConflict && (!systemChoices || includedSystems.length) && (matchesPreset || validExtension || (catalog?.includePaths?.length && validCatalogLimit)));

  const changeSettings = (update: () => void) => {
    if (reviewing) onCancel();
    inline?.onActivate();
    update();
  };

  const ruleSettings = <>
    <SourceStepParameterEditor parameters={parameters} onChange={(next) => changeSettings(() => setParameters(next))} disabled={disabled}
      structuredColumnRule fieldPathChoices={fieldPathChoices} extensionSourcePaths={extensionSourcePaths} />
    {catalog ? <label className="grid gap-1 text-xs">Maximum columns
      <input type="number" className="w-24 rounded border border-slate-300 px-2 py-1" min={1} max={128} step={1}
        value={columnLimit} disabled={disabled} aria-invalid={!validCatalogLimit}
        onChange={(event) => { const value = event.currentTarget.value; changeSettings(() => setColumnLimit(value)); }} />
    </label> : null}
  </>;

  if (selectedCatalogPath) {
    return renderReview(
      { kind, ...(occurrence.id ? { occurrenceId: occurrence.id } : {}), maxColumns: defaultColumnLimit, parameters: initialParameters },
      onCancel,
      disabled,
      'Preview field',
    );
  }

  return (
    <section className={inline ? "grid gap-2" : "grid gap-3 px-3 pb-3"}>
      {!inline ? <><h3 className="text-sm font-semibold text-slate-900">{catalog ? 'Build a multi-field rule' : `Columns from ${preset.label.toLowerCase()}`}</h3>
      <p className="text-xs text-slate-600">{occurrence.label}{rule ? ` · ${rule.source.select}` : ''}</p></> : null}
      {preset.id === 'extension' ? <p className="text-xs text-slate-600">
        Paths observed on {occurrence.resourceType} records. Preview shows columns generated for {occurrence.label}.
      </p> : null}
      {systemChoices ? <fieldset className="grid gap-1 text-xs" aria-label={category ? 'Category systems to include' : 'Identifier systems to include'} disabled={disabled}>
        <legend className={inline ? 'sr-only' : 'font-semibold text-slate-800'}>{category ? 'Choose category columns' : 'Choose identifier columns'}</legend>
        {systems.length ? <div className="flex items-center justify-between gap-2">
          <span>{includedSystems.length} of {systems.length} systems selected</span>
          <button type="button" className="text-blue-800 underline"
            onClick={() => changeSettings(() => setSystemOverrides(
              Boolean(includedSystems.length) === selectByDefault ? new Set(systems) : new Set(),
            ))}>
            {includedSystems.length ? 'Clear selection' : 'Select all'}
          </button>
        </div> : null}
        {!category && identifierInventoryLoading ? <p role="status">Loading identifier systems…</p>
          : !category && identifierInventoryUnavailable ? <p role="status">Observed identifier systems are unavailable.</p>
          : systems.length ? <ul className={fieldPickerList}>
            {systems.map(system => <li key={system}>
              <label className={fieldPickerToggleRow} title={system}>
                <input type="checkbox" aria-label={`Include ${system}`} checked={isSystemSelected(system)}
                  onChange={(event) => {
                    const next = new Set(systemOverrides);
                    if (event.currentTarget.checked === selectByDefault) next.delete(system);
                    else next.add(system);
                    changeSettings(() => setSystemOverrides(next));
                  }} />
                <span className="min-w-0 flex-1 break-all">{category ? outputKey(system) || system : system}</span>
              </label>
            </li>)}
          </ul> : <p>No {category ? 'category' : 'identifier'} systems were observed for this resource type.</p>}
        {category && systems.length > 0 && !includedSystems.length ? <p role="status">Choose at least one category system.</p> : null}
        {namingConflict ? <p role="status">These systems share a column name. {category ? 'Select or clear them together.' : 'Use full identifier system names to select them separately.'}</p> : null}
      </fieldset> : null}
      {inline ? (inline.active && includedSystems.length ? <details><summary className="cursor-pointer text-xs text-blue-800">Rule settings</summary><div className="mt-2 grid gap-3">{ruleSettings}</div></details> : null)
        : <div className="grid gap-3">{ruleSettings}</div>}
      {!validName || !validPrefix ? (
        <p className="text-xs text-amber-800">Use letters, numbers, and underscores in the rule name and prefix. Start with a letter or underscore.</p>
      ) : null}
      {(!inline || (inline.active && includedSystems.length > 0)) && (reviewing || changed || canReview) && (!chooseIdentifiers || includedSystems.length > 0) ? <div className="sticky bottom-0 z-10 border-t border-slate-200 bg-white py-2">{renderReview(
        { kind, ...(occurrence.id ? { occurrenceId: occurrence.id } : {}), ...(catalog ? { maxColumns: limit } : {}), parameters: ruleParameters },
        onCancel,
        disabled || !canReview,
        changed ? 'Review column rule' : 'Add column rule',
        changed,
      )}</div> : null}
    </section>
  );
}
