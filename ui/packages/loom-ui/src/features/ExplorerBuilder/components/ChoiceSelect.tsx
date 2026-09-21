import React from 'react';
import { choiceFor, referenceFor } from './tableShapeModel';
import type { ChoiceReference, ServerChoice } from './tableShapeModel';

export interface ChoiceSelectProps<Kind extends string> {
  readonly label: string;
  readonly testId: string;
  readonly choices: ReadonlyArray<ServerChoice<Kind>>;
  readonly value: ChoiceReference<Kind> | null;
  readonly placeholder: string;
  readonly disabled?: boolean;
  readonly onChange: (choice: ChoiceReference<Kind> | null) => void;
}

export const ChoiceSelect = <Kind extends string,>({
  label,
  testId,
  choices,
  value,
  placeholder,
  disabled = false,
  onChange,
}: ChoiceSelectProps<Kind>) => {
  const selectedChoice = choiceFor(choices, value);
  const missingSavedChoice = value !== null && selectedChoice === undefined;
  const unsupportedChoices = choices.filter(
    (choice) => choice.availability.kind === 'unsupported',
  );

  return (
    <div className="grid gap-1">
      <label className="grid gap-1 text-sm font-medium text-slate-800">
        <span>{label}</span>
        <select
          aria-label={label}
          data-testid={testId}
          className="w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm font-normal"
          value={value?.choiceId ?? ''}
          disabled={disabled}
          onChange={(event) => {
            const nextId = event.currentTarget.value;
            if (nextId === '') {
              onChange(null);
              return;
            }
            const nextChoice = choices.find((choice) => choice.choiceId === nextId);
            if (nextChoice?.availability.kind === 'supported') {
              onChange(referenceFor(nextChoice));
            }
          }}
        >
          <option value="">{placeholder}</option>
          {missingSavedChoice && value ? (
            <option value={value.choiceId} disabled>Saved selection is not in the current server choices</option>
          ) : null}
          {choices.map((choice) => (
            <option
              key={choice.choiceId}
              value={choice.choiceId}
              disabled={choice.availability.kind === 'unsupported'}
            >
              {choice.availability.kind === 'unsupported'
                ? `${choice.label} — unavailable: ${choice.availability.reason}`
                : choice.label}
            </option>
          ))}
        </select>
      </label>
      {missingSavedChoice ? (
        <p role="status" className="text-xs text-amber-900">
          The saved selection is not present in the current server choices.
        </p>
      ) : null}
      {unsupportedChoices.length > 0 ? (
        <ul aria-label={`${label} unavailable choices`} data-testid={`${testId}-unsupported`} className="space-y-1 text-xs text-amber-900">
          {unsupportedChoices.map((choice) => choice.availability.kind === 'unsupported' ? (
            <li key={choice.choiceId}>
              <span className="font-medium">{choice.label}: </span>
              <span>{choice.availability.reason}</span>
            </li>
          ) : null)}
        </ul>
      ) : null}
    </div>
  );
};
