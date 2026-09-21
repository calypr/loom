import React, { useEffect, useRef, useState } from 'react';
import { ChoiceSelect } from './ChoiceSelect';
import { DerivedColumnsEditor } from './DerivedColumnsEditor';
import { GroupedPivotEditor } from './GroupedPivotEditor';
import { UnpivotEditor } from './UnpivotEditor';
import type {
  ChoiceReference,
  OutputDescriptorSupport,
  OutputNameDraft,
  PivotCategoryDiscovery,
  PivotCategoryPair,
  TableShapeEditorChoices,
  TableShapeProposalIntent,
} from './tableShapeModel';
import { choiceFor, proposalIntentFor, proposalIntentToForm, samePivotPair } from './tableShapeModel';

interface PendingCategoryRequest {
  readonly pair: PivotCategoryPair;
}

const discoveryForReset = (discovery: PivotCategoryDiscovery): PivotCategoryDiscovery =>
  discovery.kind === 'loading' ? { kind: 'not-requested' } : discovery;

const firstSupportedOutput = <Kind extends string>(
  support: OutputDescriptorSupport<Kind>,
): OutputNameDraft | undefined =>
  support.kind === 'supported'
    ? support.suggestions.find((suggestion) => suggestion.availability.kind === 'supported')?.suggestedOutput
    : undefined;

const withOutputSuggestions = (
  current: OutputNameDraft,
  suggestion: OutputNameDraft | undefined,
): OutputNameDraft => suggestion
  ? {
      column: current.column.trim() === '' ? suggestion.column : current.column,
      label: current.label.trim() === '' ? suggestion.label : current.label,
    }
  : current;

const withUnpivotSuggestions = (
  unpivot: ReturnType<typeof proposalIntentToForm>['unpivot'],
  choices: TableShapeEditorChoices,
) => ({
  ...unpivot,
  keyOutput: withOutputSuggestions(unpivot.keyOutput, firstSupportedOutput(choices.unpivotKeyOutput)),
  valueOutput: withOutputSuggestions(unpivot.valueOutput, firstSupportedOutput(choices.unpivotValueOutput)),
});

export interface TableShapeEditorProps {
  readonly savedProposalKey: string;
  readonly savedProposalIntent: TableShapeProposalIntent;
  readonly choices: TableShapeEditorChoices;
  readonly recoverableError?: string;
  readonly disabled?: boolean;
  readonly onApply: (proposalIntent: TableShapeProposalIntent) => void;
  readonly onRequestCategoryDiscovery: (pair: PivotCategoryPair) => void;
  readonly onCancel: () => void;
}

export const TableShapeEditor = ({
  savedProposalKey,
  savedProposalIntent,
  choices,
  recoverableError,
  disabled = false,
  onApply,
  onRequestCategoryDiscovery,
  onCancel,
}: TableShapeEditorProps) => {
  const [form, setForm] = useState(() => proposalIntentToForm(savedProposalIntent));
  const [categoryDiscovery, setCategoryDiscovery] = useState<PivotCategoryDiscovery>(
    () => discoveryForReset(choices.pivotCategoryDiscovery),
  );
  const pendingCategoryRequest = useRef<PendingCategoryRequest | null>(null);
  const nextLocalId = useRef(0);
  const latestSavedProposal = useRef(savedProposalIntent);
  const latestChoices = useRef(choices);
  const latestForm = useRef(form);
  latestSavedProposal.current = savedProposalIntent;
  latestChoices.current = choices;
  latestForm.current = form;

  useEffect(() => {
    setForm(proposalIntentToForm(savedProposalIntent));
    pendingCategoryRequest.current = null;
    setCategoryDiscovery(discoveryForReset(latestChoices.current.pivotCategoryDiscovery));
  }, [savedProposalKey]);

  useEffect(() => {
    const incoming = choices.pivotCategoryDiscovery;
    const request = pendingCategoryRequest.current;
    if (!request) return;

    const currentPair = formPair(latestForm.current);
    if (!currentPair || !samePivotPair(currentPair, request.pair)) return;
    if (incoming.kind === 'loading') {
      if (samePivotPair(incoming.pair, request.pair)) setCategoryDiscovery(incoming);
      return;
    }
    if (
      (incoming.kind === 'complete' || incoming.kind === 'unavailable') &&
      samePivotPair(incoming.pair, request.pair)
    ) {
      pendingCategoryRequest.current = null;
      setCategoryDiscovery(incoming);
    }
  }, [choices.pivotCategoryDiscovery]);

  const modeChoice = choiceFor(choices.reshapeModes, form.mode);
  const proposalIntent = proposalIntentFor({ form, choices, categoryDiscovery });

  const requestCategoryDiscovery = (pair: PivotCategoryPair) => {
    pendingCategoryRequest.current = { pair };
    setCategoryDiscovery({ kind: 'loading', pair });
    onRequestCategoryDiscovery(pair);
  };

  const cancel = () => {
    setForm(proposalIntentToForm(latestSavedProposal.current));
    pendingCategoryRequest.current = null;
    setCategoryDiscovery(discoveryForReset(latestChoices.current.pivotCategoryDiscovery));
    onCancel();
  };

  return (
    <section aria-label="Table shape editor" data-testid="ui04-table-shape-editor" className="grid gap-4 rounded-xl border border-slate-200 bg-white p-4 text-slate-800 shadow-sm">
      <div>
        <h2 className="text-base font-semibold text-slate-900">Table shape</h2>
        <p className="mt-1 text-sm text-slate-600">Choose a server-supported reshape and author output names.</p>
      </div>
      {recoverableError ? (
        <p role="alert" data-testid="ui04-table-shape-error" className="rounded border border-red-200 bg-red-50 p-3 text-sm text-red-900">
          {recoverableError}
        </p>
      ) : null}
      <ChoiceSelect
        label="Table shape"
        testId="ui04-reshape-mode"
        choices={choices.reshapeModes}
        value={form.mode}
        placeholder="Choose a table shape"
        disabled={disabled}
        onChange={(mode) => {
          const nextMode = choiceFor(choices.reshapeModes, mode)?.mode;
          setForm((previous) => ({
            ...previous,
            mode,
            unpivot: nextMode === 'UNPIVOT'
              ? withUnpivotSuggestions(previous.unpivot, choices)
              : previous.unpivot,
          }));
        }}
      />

      {modeChoice?.mode === 'GROUPED_PIVOT' ? (
        <GroupedPivotEditor
          pivot={form.pivot}
          choices={choices}
          categoryDiscovery={categoryDiscovery}
          disabled={disabled}
          onChange={(pivot) => setForm((previous) => ({ ...previous, pivot }))}
          onCategoryValueChange={(pivot) => {
            setForm((previous) => ({ ...previous, pivot }));
            pendingCategoryRequest.current = null;
            setCategoryDiscovery({ kind: 'not-requested' });
          }}
          onRequestCategoryDiscovery={requestCategoryDiscovery}
        />
      ) : null}

      {modeChoice?.mode === 'UNPIVOT' ? (
        <UnpivotEditor
          unpivot={form.unpivot}
          choices={choices}
          disabled={disabled}
          onChange={(unpivot) => setForm((previous) => ({ ...previous, unpivot }))}
        />
      ) : null}

      <DerivedColumnsEditor
        columns={form.derivedColumns}
        mode={modeChoice?.mode}
        choices={choices}
        disabled={disabled}
        onChange={(derivedColumns) => setForm((previous) => ({ ...previous, derivedColumns }))}
        onAdd={() => setForm((previous) => ({
          ...previous,
          derivedColumns: [...previous.derivedColumns, {
            localId: `draft-${nextLocalId.current++}`,
            output: withOutputSuggestions(
              { column: '', label: '' },
              choices.derivedOutputSuggestions.find(
                (suggestion) => suggestion.availability.kind === 'supported',
              )?.suggestedOutput,
            ),
            operator: null,
            leftOperand: null,
            rightOperand: null,
            missingInputPolicy: null,
            divisionByZeroPolicy: null,
          }],
        }))}
      />

      <div className="flex flex-wrap justify-end gap-2">
        <button
          type="button"
          className="rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium hover:bg-slate-50"
          data-testid="ui04-cancel-table-shape"
          onClick={cancel}
        >
          Cancel
        </button>
        <button
          type="button"
          className="rounded bg-blue-700 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:opacity-50"
          data-testid="ui04-apply-table-shape"
          disabled={disabled || !proposalIntent}
          onClick={() => proposalIntent && onApply(proposalIntent)}
        >
          Apply table shape
        </button>
      </div>
    </section>
  );
};

const formPair = (form: ReturnType<typeof proposalIntentToForm>): PivotCategoryPair | undefined => {
  const categoryColumn: ChoiceReference<'column'> | null = form.pivot.categoryColumn;
  const valueColumn: ChoiceReference<'column'> | null = form.pivot.valueColumn;
  return categoryColumn && valueColumn ? { categoryColumn, valueColumn } : undefined;
};


export type {
  ChoiceAvailability,
  ChoiceReference,
  OutputNameDraft,
  PivotCategoryDiscovery,
  PivotCategoryPair,
  ReshapeMode,
  ServerChoice,
  TableShapeEditorChoices,
  TableShapeProposalIntent,
} from './tableShapeModel';
