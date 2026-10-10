import React from 'react';
import { choiceFor, referenceFor } from './tableShapeModel';
import type { ChoiceReference, ServerChoice } from './tableShapeModel';

export interface OrderedChoiceEditorProps<Kind extends string> {
  readonly label: string;
  readonly testId: string;
  readonly choices: ReadonlyArray<ServerChoice<Kind>>;
  readonly selected: ReadonlyArray<ChoiceReference<Kind>>;
  readonly disabled?: boolean;
  readonly onChange: (selected: ReadonlyArray<ChoiceReference<Kind>>) => void;
}

export const OrderedChoiceEditor = <Kind extends string,>({
  label,
  testId,
  choices,
  selected,
  disabled = false,
  onChange,
}: OrderedChoiceEditorProps<Kind>) => (
  <fieldset className="grid gap-2 rounded border border-slate-200 p-3">
    <legend className="px-1 text-sm font-medium text-slate-800">{label}</legend>
    <ul className="grid gap-1">
      {choices.map((choice, index) => {
        const reference = referenceFor(choice);
        const checked = selected.some((item) => item.choiceId === choice.choiceId);
        const choiceDisabled = disabled || (
          choice.availability.kind === 'unsupported' && !checked
        );
        return (
          <li key={choice.choiceId}>
            <label className="flex items-start gap-2 rounded px-1 py-1 text-sm text-slate-800">
              <input
                type="checkbox"
                aria-label={`${label}: ${choice.label}`}
                data-testid={`${testId}-choice-${index + 1}`}
                checked={checked}
                disabled={choiceDisabled}
                onChange={(event) => {
                  if (choice.availability.kind === 'unsupported' && event.currentTarget.checked) {
                    return;
                  }
                  if (event.currentTarget.checked) {
                    if (!checked) onChange([...selected, reference]);
                  } else {
                    onChange(selected.filter((item) => item.choiceId !== choice.choiceId));
                  }
                }}
              />
              <span className="grid gap-0.5">
                <span>{choice.label}</span>
                {choice.availability.kind === 'unsupported' ? (
                  <span className="text-xs text-amber-900">{choice.availability.reason}</span>
                ) : null}
              </span>
            </label>
          </li>
        );
      })}
    </ul>
    {selected.length > 0 ? (
      <ol aria-label={`${label} order`} data-testid={`${testId}-order`} className="grid gap-1">
        {selected.map((reference, index) => {
          const choice = choiceFor(choices, reference);
          const choiceLabel = choice?.label ?? 'Saved selection is not in the current server choices';
          return (
            <li key={`${reference.kind}:${reference.choiceId}`} className="flex items-center gap-2 text-sm">
              <span className="min-w-0 flex-1">{index + 1}. {choiceLabel}</span>
              <button
                type="button"
                aria-label={`Move ${choiceLabel} up`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled || index === 0}
                onClick={() => {
                  const previous = selected[index - 1];
                  const current = selected[index];
                  if (!previous || !current) return;
                  const reordered = selected.slice();
                  reordered[index - 1] = current;
                  reordered[index] = previous;
                  onChange(reordered);
                }}
              >
                Move up
              </button>
              <button
                type="button"
                aria-label={`Move ${choiceLabel} down`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled || index === selected.length - 1}
                onClick={() => {
                  const next = selected[index + 1];
                  const current = selected[index];
                  if (!next || !current) return;
                  const reordered = selected.slice();
                  reordered[index] = next;
                  reordered[index + 1] = current;
                  onChange(reordered);
                }}
              >
                Move down
              </button>
              <button
                type="button"
                aria-label={`Remove ${choiceLabel}`}
                className="rounded border border-slate-300 px-2 py-1 disabled:opacity-40"
                disabled={disabled}
                onClick={() => onChange(selected.filter((item) => item.choiceId !== reference.choiceId))}
              >
                Remove
              </button>
            </li>
          );
        })}
      </ol>
    ) : null}
  </fieldset>
);
