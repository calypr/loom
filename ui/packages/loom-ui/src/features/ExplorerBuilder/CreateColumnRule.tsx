import { useState, type ReactNode } from 'react';
import type { ImportedSourceParameters, SourceRuleCreate } from '../../types';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';
import { isCategoryCodingRule, isIdentifierColumnRule, type ColumnRuleOccurrence, type ColumnRulePreset } from './sourceColumnRule';

const ruleNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function CreateColumnRule({ kind, occurrence, preset, initialParameters, identifierSystems, identifierInventoryLoading, identifierInventoryUnavailable, fieldPathChoices, disabled, reviewing, onReview, onCancel, renderReview }: {
  readonly kind: SourceRuleCreate['kind'];
  readonly occurrence: ColumnRuleOccurrence;
  readonly preset: ColumnRulePreset;
  readonly initialParameters: ImportedSourceParameters;
  readonly identifierSystems?: ReadonlyArray<string>;
  readonly identifierInventoryLoading?: boolean;
  readonly identifierInventoryUnavailable?: boolean;
  readonly fieldPathChoices?: ReadonlyArray<string>;
  readonly disabled: boolean;
  readonly reviewing: boolean;
  readonly onReview: () => void;
  readonly onCancel: () => void;
  readonly renderReview: (rule: SourceRuleCreate, leaveReview: () => void) => ReactNode;
}) {
  const [parameters, setParameters] = useState(initialParameters);
  const [columnLimit, setColumnLimit] = useState("128");
  const limit = Number(columnLimit);
  const rule = parameters.dynamic;
  const catalog = parameters.catalog;
  const validName = ruleNamePattern.test(rule?.name ?? catalog?.name ?? '');
  const validPrefix = !rule?.columnPrefix || ruleNamePattern.test(rule.columnPrefix);
  const matchesPreset = rule && (preset.id === 'identifier' ? isIdentifierColumnRule(rule) : preset.id === 'categoryCoding' && isCategoryCodingRule(rule));
  const validCatalogLimit = Number.isInteger(limit) && limit >= 1 && limit <= 128;
  const canReview = Boolean(validName && validPrefix && (matchesPreset || (catalog?.includePaths?.length && validCatalogLimit)));

  return (
    <section className="grid gap-3 px-3 pb-3">
      <h3 className="text-sm font-semibold text-slate-900">Columns from {preset.label.toLowerCase()}</h3>
      <p className="text-xs text-slate-600">{occurrence.label}{rule ? ` · ${rule.source.select}` : ''}</p>
      {preset.id === 'identifier' && identifierSystems ? <section className="grid gap-1 text-xs" aria-label="Observed identifier systems">
        <p className="font-medium text-slate-800">Identifier systems observed on {occurrence.resourceType} records</p>
        {identifierInventoryLoading ? <p role="status">Loading identifier systems…</p>
          : identifierInventoryUnavailable ? <p role="status">Observed identifier systems are unavailable.</p>
          : identifierSystems.length ? <ul className="max-h-32 list-disc overflow-auto pl-4">
            {identifierSystems.map(system => <li key={system} className="break-all">{system}</li>)}
          </ul> : <p>No identifier systems were observed for this resource type.</p>}
      </section> : null}
      <SourceStepParameterEditor parameters={parameters} onChange={setParameters} disabled={disabled || reviewing}
        structuredColumnRule fieldPathChoices={fieldPathChoices} />
      {catalog ? <label className="grid gap-1 text-xs">Maximum columns
        <input type="number" className="w-24 rounded border border-slate-300 px-2 py-1" min={1} max={128} step={1}
          value={columnLimit} disabled={disabled || reviewing} aria-invalid={!validCatalogLimit}
          onChange={(event) => setColumnLimit(event.currentTarget.value)} />
      </label> : null}
      {!validName || !validPrefix ? (
        <p className="text-xs text-amber-800">Use letters, numbers, and underscores in the rule name and prefix. Start with a letter or underscore.</p>
      ) : null}
      {reviewing ? renderReview(
        { kind, ...(occurrence.id ? { occurrenceId: occurrence.id } : {}), ...(catalog ? { maxColumns: limit } : {}), parameters },
        onCancel,
      ) : (
        <button type="button" disabled={disabled || !canReview}
          className="justify-self-end rounded border border-slate-300 bg-white px-3 py-1 text-sm disabled:opacity-50"
          onClick={onReview}>
          Review column rule
        </button>
      )}
    </section>
  );
}
