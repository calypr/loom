import { useState, type ReactNode } from 'react';
import type { ImportedSourceParameters, SourceRuleCreate } from '../../types';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';
import { isIdentifierColumnRule, type ColumnRuleOccurrence, type ColumnRulePreset } from './sourceColumnRule';

const ruleNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function CreateColumnRule({ kind, occurrence, preset, initialParameters, disabled, reviewing, onReview, onCancel, renderReview }: {
  readonly kind: SourceRuleCreate['kind'];
  readonly occurrence: ColumnRuleOccurrence;
  readonly preset: ColumnRulePreset;
  readonly initialParameters: ImportedSourceParameters;
  readonly disabled: boolean;
  readonly reviewing: boolean;
  readonly onReview: () => void;
  readonly onCancel: () => void;
  readonly renderReview: (rule: SourceRuleCreate, leaveReview: () => void) => ReactNode;
}) {
  const [parameters, setParameters] = useState(initialParameters);
  const rule = parameters.dynamic;
  const validName = ruleNamePattern.test(rule?.name ?? '');
  const validPrefix = !rule?.columnPrefix || ruleNamePattern.test(rule.columnPrefix);
  const canReview = Boolean(preset.id === 'identifier' && rule && isIdentifierColumnRule(rule) && validName && validPrefix);

  return (
    <section className="grid gap-3 px-3 pb-3">
      <h3 className="text-sm font-semibold text-slate-900">Columns from {preset.label.toLowerCase()}</h3>
      <p className="text-xs text-slate-600">{occurrence.label} · {rule?.source.select}</p>
      <SourceStepParameterEditor parameters={parameters} onChange={setParameters} disabled={disabled || reviewing}
        structuredColumnRule />
      {!validName || !validPrefix ? (
        <p className="text-xs text-amber-800">Use letters, numbers, and underscores in the rule name and prefix. Start with a letter or underscore.</p>
      ) : null}
      {reviewing ? renderReview(
        { kind, ...(occurrence.id ? { occurrenceId: occurrence.id } : {}), parameters },
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
