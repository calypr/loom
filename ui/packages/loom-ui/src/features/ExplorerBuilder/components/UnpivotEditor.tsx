import React from 'react';
import { ChoiceSelect } from './ChoiceSelect';
import { OrderedChoiceEditor } from './OrderedChoiceEditor';
import { OutputNameFields } from './OutputNameFields';
import type { TableShapeEditorChoices, UnpivotFormState } from './tableShapeModel';

export interface UnpivotEditorProps {
  readonly unpivot: UnpivotFormState;
  readonly choices: TableShapeEditorChoices;
  readonly disabled: boolean;
  readonly onChange: (unpivot: UnpivotFormState) => void;
}

export const UnpivotEditor = ({ unpivot, choices, disabled, onChange }: UnpivotEditorProps) => (
  <section aria-label="Unpivot configuration" data-testid="ui04-unpivot-configuration" className="grid gap-3 rounded-lg border border-slate-200 p-3">
    <div>
      <h3 className="font-semibold text-slate-900">Unpivot</h3>
      <p className="mt-1 text-xs text-slate-600">Select inputs in output order and author names for the key and value columns.</p>
    </div>
    <OrderedChoiceEditor
      label="Unpivot input columns"
      testId="ui04-unpivot-input-columns"
      choices={choices.unpivotColumns}
      selected={unpivot.inputColumns}
      disabled={disabled}
      onChange={(inputColumns) => onChange({ ...unpivot, inputColumns })}
    />
    <div className="grid gap-2 rounded border border-slate-200 p-3">
      <p className="text-xs text-slate-600">
        {choices.unpivotKeyOutput.kind === 'supported'
          ? `Server result type: ${choices.unpivotKeyOutput.resultTypeLabel}`
          : choices.unpivotKeyOutput.reason}
      </p>
      <OutputNameFields
        label="Unpivot key output"
        testId="ui04-unpivot-key-output"
        value={unpivot.keyOutput}
        disabled={disabled}
        suggestions={choices.unpivotKeyOutput.kind === 'supported'
          ? choices.unpivotKeyOutput.suggestions
          : []}
        onChange={(keyOutput) => onChange({ ...unpivot, keyOutput })}
      />
    </div>
    <div className="grid gap-2 rounded border border-slate-200 p-3">
      <p className="text-xs text-slate-600">
        {choices.unpivotValueOutput.kind === 'supported'
          ? `Server result type: ${choices.unpivotValueOutput.resultTypeLabel}`
          : choices.unpivotValueOutput.reason}
      </p>
      <OutputNameFields
        label="Unpivot value output"
        testId="ui04-unpivot-value-output"
        value={unpivot.valueOutput}
        disabled={disabled}
        suggestions={choices.unpivotValueOutput.kind === 'supported'
          ? choices.unpivotValueOutput.suggestions
          : []}
        onChange={(valueOutput) => onChange({ ...unpivot, valueOutput })}
      />
    </div>
    <ChoiceSelect
      label="Unpivot null-row policy"
      testId="ui04-unpivot-null-row-policy"
      choices={choices.unpivotNullRowPolicies}
      value={unpivot.nullRowPolicy}
      placeholder="Choose how rows with no value are handled"
      disabled={disabled}
      onChange={(nullRowPolicy) => onChange({ ...unpivot, nullRowPolicy })}
    />
  </section>
);
