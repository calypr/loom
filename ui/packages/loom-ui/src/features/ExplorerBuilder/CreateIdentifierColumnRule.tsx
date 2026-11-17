import { useState, type ReactNode } from 'react';
import type { ImportedSourceParameters, SourceRuleCreate } from '../../types';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';
import { isIdentifierColumnRule } from './sourceColumnRule';

const ruleNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

const initialRule: ImportedSourceParameters = {
  dynamic: {
    name: 'identifiers',
    source: { select: 'root.identifier[]' },
    key: { select: 'item.system' },
    value: { select: 'item.value' },
  },
};

export function CreateIdentifierColumnRule({ disabled, reviewing, onReview, onCancel, renderReview }: {
  readonly disabled: boolean;
  readonly reviewing: boolean;
  readonly onReview: () => void;
  readonly onCancel: () => void;
  readonly renderReview: (rule: SourceRuleCreate, leaveReview: () => void) => ReactNode;
}) {
  const [parameters, setParameters] = useState(initialRule);
  const rule = parameters.dynamic;
  const validName = ruleNamePattern.test(rule?.name ?? '');
  const validPrefix = !rule?.columnPrefix || ruleNamePattern.test(rule.columnPrefix);
  const canReview = Boolean(rule && isIdentifierColumnRule(rule) && validName && validPrefix);

  return (
    <section className="grid gap-3 px-3 pb-3">
      <h3 className="text-sm font-semibold text-slate-900">Columns from identifiers</h3>
      <SourceStepParameterEditor parameters={parameters} onChange={setParameters} disabled={disabled || reviewing}
        structuredIdentifierRule />
      {!validName || !validPrefix ? (
        <p className="text-xs text-amber-800">Use letters, numbers, and underscores in the rule name and prefix. Start with a letter or underscore.</p>
      ) : null}
      {reviewing ? renderReview(
        { kind: 'DYNAMIC_COLUMN', parameters },
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
