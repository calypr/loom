import React from 'react';
import type { ChoiceAvailability, OutputNameDraft } from './tableShapeModel';

export interface OutputNameSuggestionView {
  readonly choiceId: string;
  readonly label: string;
  readonly availability: ChoiceAvailability;
  readonly suggestedOutput: OutputNameDraft;
  readonly resultTypeLabel?: string;
}

export interface OutputNameFieldsProps {
  readonly label: string;
  readonly testId: string;
  readonly value: OutputNameDraft;
  readonly disabled?: boolean;
  readonly suggestions?: ReadonlyArray<OutputNameSuggestionView>;
  readonly onChange: (value: OutputNameDraft) => void;
}

export const OutputNameFields = ({
  label,
  testId,
  value,
  disabled = false,
  suggestions = [],
  onChange,
}: OutputNameFieldsProps) => (
  <div className="grid gap-2">
    <label className="grid gap-1 text-sm font-medium text-slate-800">
      <span>{label} column</span>
      <input
        aria-label={`${label} column`}
        data-testid={`${testId}-column`}
        className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
        value={value.column}
        disabled={disabled}
        onChange={(event) => onChange({ ...value, column: event.currentTarget.value })}
      />
    </label>
    <label className="grid gap-1 text-sm font-medium text-slate-800">
      <span>{label} label</span>
      <input
        aria-label={`${label} label`}
        data-testid={`${testId}-label`}
        className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
        value={value.label}
        disabled={disabled}
        onChange={(event) => onChange({ ...value, label: event.currentTarget.value })}
      />
    </label>
    {suggestions.length > 0 ? (
      <ul aria-label={`${label} server suggestions`} className="grid gap-1 text-xs text-slate-600">
        {suggestions.map((suggestion) => (
          <li key={suggestion.choiceId} className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              aria-label={`Use ${suggestion.label} output suggestion for ${label}`}
              data-testid={`${testId}-suggestion-${suggestion.choiceId}`}
              className="rounded border border-slate-300 bg-white px-2 py-1 font-medium hover:bg-slate-50 disabled:opacity-50"
              disabled={disabled || suggestion.availability.kind === 'unsupported'}
              onClick={() => onChange(suggestion.suggestedOutput)}
            >
              Use suggestion: {suggestion.label}
            </button>
            {suggestion.resultTypeLabel ? <span>Server result type: {suggestion.resultTypeLabel}</span> : null}
            {suggestion.availability.kind === 'unsupported' ? (
              <span className="text-amber-900">{suggestion.availability.reason}</span>
            ) : null}
          </li>
        ))}
      </ul>
    ) : null}
  </div>
);
