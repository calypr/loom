import React, { useEffect, useMemo, useRef, useState } from 'react';
import type {
  ConstructionCombineCatalog,
  ConstructionCombineCandidateIntent,
  ConstructionCombineInputRef,
  ConstructionCombineKind,
  ConstructionCombineKey,
  ConstructionCombineOperation,
  ConstructionCombineOutputColumn,
  ConstructionCombineProjection,
  ConstructionCombinePublishedColumn,
  ConstructionCombinePublishedRevision,
  ConstructionCombineStep,
} from './combineEditorTypes';

type InputDraft = { readonly revisionKey: string };
type KeyDraft = { readonly leftColumnId: string; readonly rightColumnId: string };
type OutputDraft = {
  readonly id: string;
  readonly name: string;
  readonly label: string;
  readonly inputColumnIds: ReadonlyArray<string>;
};

interface CombineDraft {
  readonly stepId: string;
  readonly kind?: ConstructionCombineKind;
  readonly inputs: ReadonlyArray<InputDraft>;
  readonly keys: ReadonlyArray<KeyDraft>;
  readonly joinType: 'INNER' | 'LEFT';
  readonly membershipMode: 'INCLUDE' | 'EXCLUDE';
  readonly outputs: ReadonlyArray<OutputDraft>;
}

export interface ConstructionCombineEditorProps {
  readonly catalog: ConstructionCombineCatalog;
  readonly constructionVersion: number;
  readonly editingStep?: ConstructionCombineStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (intent: ConstructionCombineCandidateIntent | undefined) => void;
}

const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

const revisionKey = (revision: Pick<ConstructionCombinePublishedRevision, 'tableId' | 'revisionId' | 'outputId'>): string =>
  JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]);

const toInputRef = (revision: ConstructionCombinePublishedRevision): ConstructionCombineInputRef => ({
  kind: 'TABLE_REVISION',
  tableId: revision.tableId,
  revisionId: revision.revisionId,
  outputId: revision.outputId,
});

const emptyDraft = (stepId = createOpaqueId('combine')): CombineDraft => ({
  stepId,
  inputs: [{ revisionKey: '' }, { revisionKey: '' }],
  keys: [{ leftColumnId: '', rightColumnId: '' }],
  joinType: 'INNER',
  membershipMode: 'INCLUDE',
  outputs: [],
});

const draftFromStep = (step: ConstructionCombineStep): CombineDraft => {
  const key = step.operation.combine.kind === 'KEY_JOIN' || step.operation.combine.kind === 'MEMBERSHIP'
    ? step.operation.combine.keys
    : [];
  return {
    stepId: step.id,
    kind: step.operation.combine.kind,
    inputs: step.inputs.map((input) => ({ revisionKey: revisionKey(input) })),
    keys: key.length > 0 ? key.map((pair) => ({ ...pair })) : [{ leftColumnId: '', rightColumnId: '' }],
    joinType: step.operation.combine.kind === 'KEY_JOIN' ? step.operation.combine.joinType : 'INNER',
    membershipMode: step.operation.combine.kind === 'MEMBERSHIP' ? step.operation.combine.membershipMode : 'INCLUDE',
    outputs: step.outputs.map((output) => ({
      id: output.id,
      name: output.name,
      label: output.label,
      inputColumnIds: step.inputs.map((_, inputIndex) =>
        step.operation.combine.projections.find((projection) =>
          projection.outputColumnId === output.id && projection.inputIndex === inputIndex,
        )?.inputColumnId ?? '',
      ),
    })),
  };
};

const columnLabel = (column: ConstructionCombinePublishedColumn): string =>
  `${column.label || column.name} (${column.type || column.clickhouseType}${column.nullable ? ', nullable' : ''}${column.repeated ? ', repeated' : ''})`;

const isJoinableType = (clickhouseType: string): boolean => {
  const type = clickhouseType.trim();
  if (!type || type.startsWith('Nullable(') || type.startsWith('Array(')) return false;
  return type === 'String' || type === 'Bool' || /^(U?Int|Decimal|Date)/.test(type);
};

const isJoinableColumn = (column: ConstructionCombinePublishedColumn): boolean =>
  !column.nullable && !column.repeated && isJoinableType(column.clickhouseType);

const appendCompatible = (
  left: ConstructionCombinePublishedColumn,
  right: ConstructionCombinePublishedColumn,
): boolean => left.type === right.type && left.clickhouseType === right.clickhouseType && left.nullable === right.nullable;

const uniqueOutputName = (candidate: string, outputs: ReadonlyArray<OutputDraft>): string => {
  const base = candidate.trim().replace(/[^A-Za-z0-9_]+/g, '_').replace(/^[^A-Za-z_]+/, '') || 'column';
  if (!outputs.some((output) => output.name === base)) return base;
  let suffix = 2;
  while (outputs.some((output) => output.name === `${base}_${suffix}`)) suffix += 1;
  return `${base}_${suffix}`;
};

interface BuiltCandidate {
  readonly intent?: ConstructionCombineCandidateIntent;
  readonly issue: string;
}

const buildCandidate = (
  draft: CombineDraft,
  catalog: ConstructionCombineCatalog,
  constructionVersion: number,
): BuiltCandidate => {
  if (catalog.kind !== 'ready') return { issue: 'Published table details are not ready yet.' };
  if (!draft.kind) return { issue: 'Choose how the tables should be combined.' };

  const byKey = new Map(catalog.revisions.map((revision) => [revisionKey(revision), revision]));
  const inputs = draft.inputs.map((input) => byKey.get(input.revisionKey));
  if (inputs.some((input) => !input)) return { issue: 'Choose a published version for each input table.' };
  const selectedInputs = inputs as ConstructionCombinePublishedRevision[];
  if (new Set(selectedInputs.map(revisionKey)).size !== selectedInputs.length) {
    return { issue: 'Choose a different published output for each input.' };
  }

  const expectedInputCount = draft.kind === 'APPEND' ? selectedInputs.length : 2;
  if (draft.kind !== 'APPEND' && selectedInputs.length !== expectedInputCount) {
    return { issue: 'This operation needs exactly two input tables.' };
  }
  if (draft.kind === 'APPEND' && selectedInputs.length < 2) {
    return { issue: 'Stacking rows needs at least two input tables.' };
  }
  if (draft.kind === 'KEY_JOIN' || draft.kind === 'MEMBERSHIP') {
    const keys: ConstructionCombineKey[] = [];
    const leftSeen = new Set<string>();
    const rightSeen = new Set<string>();
    for (const key of draft.keys) {
      if (!key.leftColumnId || !key.rightColumnId) return { issue: 'Choose both fields for every matching pair.' };
      if (leftSeen.has(key.leftColumnId) || rightSeen.has(key.rightColumnId)) {
        return { issue: 'Use each matching field only once.' };
      }
      const left = selectedInputs[0].columns.find((column) => column.id === key.leftColumnId);
      const right = selectedInputs[1].columns.find((column) => column.id === key.rightColumnId);
      if (!left || !right) return { issue: 'A selected matching field is missing from its pinned table version.' };
      if (!isJoinableColumn(left) || !isJoinableColumn(right) || left.clickhouseType !== right.clickhouseType) {
        return { issue: 'Matching fields must have the same non-null scalar type.' };
      }
      leftSeen.add(key.leftColumnId);
      rightSeen.add(key.rightColumnId);
      keys.push({ leftColumnId: key.leftColumnId, rightColumnId: key.rightColumnId });
    }
    if (keys.length === 0) return { issue: 'Add at least one matching field pair.' };
  }

  if (draft.outputs.length === 0) return { issue: 'Add at least one output field.' };
  const names = new Set<string>();
  const outputColumns: ConstructionCombineOutputColumn[] = [];
  const projections: ConstructionCombineProjection[] = [];
  for (const output of draft.outputs) {
    const name = output.name.trim();
    const label = output.label.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) return { issue: 'Output names must start with a letter or underscore and contain only letters, numbers, and underscores.' };
    if (names.has(name)) return { issue: `The output name “${name}” is used more than once.` };
    if (!label) return { issue: 'Give every output field a label.' };
    names.add(name);

    if (draft.kind === 'APPEND') {
      if (output.inputColumnIds.length !== selectedInputs.length || output.inputColumnIds.some((id) => !id)) {
        return { issue: 'Choose one matching field from every input table for each output.' };
      }
      const sourceColumns = output.inputColumnIds.map((columnId, inputIndex) =>
        selectedInputs[inputIndex].columns.find((column) => column.id === columnId),
      );
      if (sourceColumns.some((column) => !column)) return { issue: 'A selected output field is missing from its pinned table version.' };
      const first = sourceColumns[0] as ConstructionCombinePublishedColumn;
      if (sourceColumns.slice(1).some((column) => !appendCompatible(first, column as ConstructionCombinePublishedColumn))) {
        return { issue: 'Fields stacked into one output must have the same type and nullability.' };
      }
      outputColumns.push({ id: output.id, name, label, type: first.type, nullable: first.nullable });
      sourceColumns.forEach((column, inputIndex) => {
        projections.push({ outputColumnId: output.id, inputIndex, inputColumnId: (column as ConstructionCombinePublishedColumn).id });
      });
      continue;
    }

    const projectedInputIndexes = draft.kind === 'MEMBERSHIP' ? [0] : [0, 1];
    const projection = output.inputColumnIds
      .map((inputColumnId, inputIndex) => ({ inputColumnId, inputIndex }))
      .filter(({ inputColumnId, inputIndex }) => projectedInputIndexes.includes(inputIndex) && inputColumnId);
    if (projection.length !== 1) return { issue: 'Choose one source field for each output.' };
    const source = projection[0];
    const sourceColumn = selectedInputs[source.inputIndex].columns.find((column) => column.id === source.inputColumnId);
    if (!sourceColumn) return { issue: 'A selected output field is missing from its pinned table version.' };
    if (draft.kind === 'KEY_JOIN' && draft.joinType === 'LEFT' && source.inputIndex === 1 && sourceColumn.repeated) {
      return { issue: 'A left match can add nullable fields, but it cannot add a repeated field from the right table.' };
    }
    const nullable = sourceColumn.nullable || (draft.kind === 'KEY_JOIN' && draft.joinType === 'LEFT' && source.inputIndex === 1);
    outputColumns.push({ id: output.id, name, label, type: sourceColumn.type, nullable });
    projections.push({ outputColumnId: output.id, inputIndex: source.inputIndex, inputColumnId: sourceColumn.id });
  }

  let combine: ConstructionCombineOperation;
  if (draft.kind === 'KEY_JOIN') {
    combine = {
      kind: 'KEY_JOIN',
      keys: draft.keys.map((key) => ({ leftColumnId: key.leftColumnId, rightColumnId: key.rightColumnId })),
      projections,
      joinType: draft.joinType,
      rightMatchPolicy: 'PRESERVE_ALL',
    };
  } else if (draft.kind === 'APPEND') {
    combine = { kind: 'APPEND', projections };
  } else {
    combine = {
      kind: 'MEMBERSHIP',
      keys: draft.keys.map((key) => ({ leftColumnId: key.leftColumnId, rightColumnId: key.rightColumnId })),
      projections,
      membershipMode: draft.membershipMode,
    };
  }

  const step: ConstructionCombineStep = {
    id: draft.stepId,
    inputs: selectedInputs.map(toInputRef),
    operation: { kind: 'COMBINE', combine },
    outputs: outputColumns,
  };
  return {
    issue: '',
    intent: {
      candidateConstruction: { version: constructionVersion, steps: [step] },
      changedStepId: step.id,
    },
  };
};

const operationChoices: ReadonlyArray<{
  readonly kind: ConstructionCombineKind;
  readonly title: string;
  readonly description: string;
}> = [
  { kind: 'KEY_JOIN', title: 'Match rows', description: 'Bring fields together when selected values match.' },
  { kind: 'APPEND', title: 'Stack rows', description: 'Put tables with matching fields under each other.' },
  { kind: 'MEMBERSHIP', title: 'Keep or exclude matches', description: 'Keep rows from the first table based on values found in another.' },
];

export const ConstructionCombineEditor = ({
  catalog,
  constructionVersion,
  editingStep,
  disabled,
  onCandidateChange,
}: ConstructionCombineEditorProps) => {
  const [draft, setDraft] = useState<CombineDraft>(() => editingStep ? draftFromStep(editingStep) : emptyDraft());
  const onCandidateChangeRef = useRef(onCandidateChange);
  useEffect(() => {
    onCandidateChangeRef.current = onCandidateChange;
  }, [onCandidateChange]);
  useEffect(() => {
    setDraft(editingStep ? draftFromStep(editingStep) : emptyDraft());
  }, [editingStep?.id]);

  const built = useMemo(
    () => buildCandidate(draft, catalog, constructionVersion),
    [catalog, constructionVersion, draft],
  );
  useEffect(() => onCandidateChangeRef.current(built.intent), [built.intent]);

  const revisions = catalog.kind === 'ready' ? catalog.revisions : [];
  const revisionByKey = useMemo(() => new Map(revisions.map((revision) => [revisionKey(revision), revision])), [revisions]);
  const selected = draft.inputs.map((input) => revisionByKey.get(input.revisionKey));
  const kind = draft.kind;
  const setInput = (inputIndex: number, key: string) => {
    setDraft((current) => ({
      ...current,
      inputs: current.inputs.map((input, index) => index === inputIndex ? { revisionKey: key } : input),
    }));
  };
  const addInput = () => setDraft((current) => ({
    ...current,
    inputs: [...current.inputs, { revisionKey: '' }],
    outputs: current.outputs.map((output) => ({ ...output, inputColumnIds: [...output.inputColumnIds, ''] })),
  }));
  const removeInput = (inputIndex: number) => setDraft((current) => ({
    ...current,
    inputs: current.inputs.filter((_, index) => index !== inputIndex),
    outputs: current.outputs.map((output) => ({
      ...output,
      inputColumnIds: output.inputColumnIds.filter((_, index) => index !== inputIndex),
    })),
  }));
  const changeKind = (nextKind: ConstructionCombineKind) => setDraft((current) => ({
    ...current,
    kind: nextKind,
    inputs: nextKind === 'APPEND' && current.inputs.length < 2
      ? [...current.inputs, { revisionKey: '' }]
      : current.inputs.slice(0, 2),
    keys: [{ leftColumnId: '', rightColumnId: '' }],
    outputs: [],
    joinType: 'INNER',
    membershipMode: 'INCLUDE',
  }));
  const addKey = () => setDraft((current) => ({
    ...current,
    keys: [...current.keys, { leftColumnId: '', rightColumnId: '' }],
  }));
  const updateKey = (keyIndex: number, side: 'leftColumnId' | 'rightColumnId', value: string) => setDraft((current) => ({
    ...current,
    keys: current.keys.map((key, index) => index === keyIndex ? { ...key, [side]: value } : key),
  }));
  const addOutput = () => setDraft((current) => ({
    ...current,
    outputs: [...current.outputs, {
      id: createOpaqueId('combine_output'),
      name: '',
      label: '',
      inputColumnIds: current.inputs.map(() => ''),
    }],
  }));
  const updateOutput = (outputId: string, patch: Partial<OutputDraft>) => setDraft((current) => ({
    ...current,
    outputs: current.outputs.map((output) => output.id === outputId ? { ...output, ...patch } : output),
  }));
  const mapOutput = (outputId: string, inputIndex: number, columnId: string) => setDraft((current) => ({
    ...current,
    outputs: current.outputs.map((output) => output.id === outputId
      ? { ...output, inputColumnIds: output.inputColumnIds.map((id, index) => index === inputIndex ? columnId : id) }
      : output),
  }));
  const removeOutput = (outputId: string) => setDraft((current) => ({
    ...current,
    outputs: current.outputs.filter((output) => output.id !== outputId),
  }));

  const updateChoices = (inputIndex: number): ReadonlyArray<ConstructionCombinePublishedRevision> => {
    const pinned = selected[inputIndex];
    if (!pinned) return [];
    return revisions.filter((revision) =>
      revision.tableId === pinned.tableId && revision.outputId === pinned.outputId && revision.isCurrent,
    );
  };

  const sourceOptions = (output: OutputDraft, inputIndex: number): ReadonlyArray<ConstructionCombinePublishedColumn> => {
    const revision = selected[inputIndex];
    if (!revision) return [];
    if (kind === 'APPEND' && inputIndex > 0) {
      const firstRevision = selected[0];
      const firstColumn = firstRevision?.columns.find((column) => column.id === output.inputColumnIds[0]);
      if (!firstColumn) return revision.columns;
      return revision.columns.filter((column) => appendCompatible(firstColumn, column));
    }
    if (kind === 'KEY_JOIN' && draft.joinType === 'LEFT' && inputIndex === 1) {
      return revision.columns.filter((column) => !column.repeated);
    }
    return revision.columns;
  };

  return (
    <section className="space-y-6" data-testid="construction-combine-editor" aria-label="Combine tables">
      <header>
        <h2 className="text-base font-semibold text-slate-900">Combine published tables</h2>
        <p className="mt-1 text-sm text-slate-600">Choose exact table versions, then select how their rows and fields fit together.</p>
      </header>

      {catalog.kind === 'loading' && <p role="status" className="text-sm text-slate-600">Loading available published tables…</p>}
      {catalog.kind === 'failed' && <p role="alert" className="text-sm text-red-700">Could not load published tables: {catalog.message}</p>}
      {catalog.kind === 'ready' && revisions.length === 0 && (
        <p role="status" className="rounded-md bg-slate-50 p-3 text-sm text-slate-600">No published table outputs are available in this workspace yet.</p>
      )}

      <fieldset disabled={disabled || catalog.kind !== 'ready' || revisions.length === 0} className="space-y-6 disabled:opacity-75">
        <legend className="sr-only">Choose a combine operation</legend>
        <div>
          <h3 className="text-sm font-semibold text-slate-800">What should the new table do?</h3>
          <div className="mt-2 grid gap-2 md:grid-cols-3">
            {operationChoices.map((choice) => (
              <button
                key={choice.kind}
                type="button"
                aria-pressed={kind === choice.kind}
                aria-label={choice.title}
                aria-describedby={`construction-combine-choice-description-${choice.kind.toLowerCase()}`}
                onClick={() => kind !== choice.kind && changeKind(choice.kind)}
                className={`rounded-lg border p-3 text-left transition ${kind === choice.kind ? 'border-blue-600 bg-blue-50 ring-1 ring-blue-600' : 'border-slate-300 bg-white hover:border-slate-500'}`}
                data-testid={`construction-combine-choice-${choice.kind.toLowerCase()}`}
              >
                <span className="block text-sm font-semibold text-slate-900">{choice.title}</span>
                <span id={`construction-combine-choice-description-${choice.kind.toLowerCase()}`} className="mt-1 block text-xs text-slate-600">{choice.description}</span>
              </button>
            ))}
          </div>
          <p className="mt-2 text-xs text-slate-500">Changing the action keeps the first two table choices and clears the matching fields and output mappings.</p>
        </div>

        {kind && (
          <div className="space-y-3">
            <div>
              <h3 className="text-sm font-semibold text-slate-800">Choose the input tables</h3>
              <p className="mt-1 text-xs text-slate-600">Each input stays pinned to the published version you select.</p>
            </div>
            <div className="grid gap-3 lg:grid-cols-2">
              {draft.inputs.map((input, inputIndex) => {
                const pinned = selected[inputIndex];
                const newer = updateChoices(inputIndex).filter((candidate) => revisionKey(candidate) !== input.revisionKey);
                const usedKeys = new Set(draft.inputs.filter((_, index) => index !== inputIndex).map((candidate) => candidate.revisionKey));
                return (
                  <article key={`input-${inputIndex}`} className="rounded-lg border border-slate-200 bg-white p-3">
                    <div className="flex items-start justify-between gap-3">
                      <label className="min-w-0 flex-1 text-sm font-medium text-slate-800" htmlFor={`combine-input-${inputIndex}`}>
                        Input table {inputIndex + 1}
                        <select
                          id={`combine-input-${inputIndex}`}
                          aria-label={`Input table ${inputIndex + 1}`}
                          className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2.5 py-2 text-sm"
                          value={input.revisionKey}
                          onChange={(event) => setInput(inputIndex, event.target.value)}
                        >
                          <option value="">Choose a published output</option>
                          {revisions.map((revision) => {
                            const optionKey = revisionKey(revision);
                            const isUnavailable = usedKeys.has(optionKey);
                            return (
                              <option key={optionKey} value={optionKey} disabled={isUnavailable}>
                                {revision.tableTitle} · {revision.rowMeaning} · {revision.outputTitle} · revision {revision.revisionId}{revision.isCurrent ? ' · current' : ''}
                              </option>
                            );
                          })}
                        </select>
                      </label>
                      {kind === 'APPEND' && inputIndex >= 2 && (
                        <button type="button" className="mt-6 rounded px-2 py-1 text-sm text-red-700 hover:bg-red-50" onClick={() => removeInput(inputIndex)} aria-label={`Remove input table ${inputIndex + 1}`}>
                          Remove
                        </button>
                      )}
                    </div>
                    {pinned && (
                      <div className="mt-3 space-y-2">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-600">
                          <span className="font-medium text-slate-800">{pinned.rowMeaning} · pinned revision {pinned.revisionId}</span>
                          {pinned.isCurrent && <span className="rounded bg-emerald-50 px-1.5 py-0.5 text-emerald-800">Current</span>}
                          {newer.map((candidate) => (
                            <button
                              key={revisionKey(candidate)}
                              type="button"
                              className="rounded border border-blue-300 px-2 py-1 font-medium text-blue-800 hover:bg-blue-50"
                              aria-label={`Update input table ${inputIndex + 1} to revision ${candidate.revisionId}`}
                              onClick={() => setInput(inputIndex, revisionKey(candidate))}
                            >
                              Update input to revision {candidate.revisionId}
                            </button>
                          ))}
                        </div>
                        <details className="rounded bg-slate-50 px-2.5 py-2">
                          <summary className="cursor-pointer text-xs font-medium text-slate-700">View pinned schema ({pinned.columns.length} fields)</summary>
                          <ul className="mt-2 divide-y divide-slate-200 text-xs text-slate-700">
                            {pinned.columns.map((column) => <li key={column.id} className="py-1.5"><span className="font-medium">{column.label || column.name}</span><span className="ml-2 text-slate-500">{column.name} · {columnLabel(column)}</span></li>)}
                          </ul>
                        </details>
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
            {kind === 'APPEND' && (
              <button type="button" onClick={addInput} className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50">
                Add another table
              </button>
            )}
          </div>
        )}

        {kind && (kind === 'KEY_JOIN' || kind === 'MEMBERSHIP') && selected[0] && selected[1] && (
          <div className="space-y-3 rounded-lg border border-slate-200 p-3">
            <div>
              <h3 className="text-sm font-semibold text-slate-800">Which fields identify matching rows?</h3>
              <p className="mt-1 text-xs text-slate-600">Select fields with the same non-null scalar type. Add more than one pair when a match uses multiple values.</p>
            </div>
            {draft.keys.map((key, keyIndex) => {
              const leftOptions = selected[0]?.columns.filter(isJoinableColumn) ?? [];
              const leftColumn = selected[0]?.columns.find((column) => column.id === key.leftColumnId);
              const rightOptions = selected[1]?.columns.filter((column) =>
                isJoinableColumn(column) && (!leftColumn || column.clickhouseType === leftColumn.clickhouseType),
              ) ?? [];
              return (
                <div key={`key-${keyIndex}`} className="grid gap-2 sm:grid-cols-[1fr_auto_1fr_auto] sm:items-end">
                  <label className="text-xs font-medium text-slate-700" htmlFor={`combine-key-left-${keyIndex}`}>
                    {selected[0]?.outputTitle} field
                    <select id={`combine-key-left-${keyIndex}`} aria-label={`Matching pair ${keyIndex + 1} first field`} value={key.leftColumnId} onChange={(event) => updateKey(keyIndex, 'leftColumnId', event.target.value)} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                      <option value="">Choose a field</option>
                      {leftOptions.map((column) => <option key={column.id} value={column.id}>{column.label || column.name} · {column.clickhouseType}</option>)}
                    </select>
                  </label>
                  <span className="pb-2 text-center text-xs text-slate-500">matches</span>
                  <label className="text-xs font-medium text-slate-700" htmlFor={`combine-key-right-${keyIndex}`}>
                    {selected[1]?.outputTitle} field
                    <select id={`combine-key-right-${keyIndex}`} aria-label={`Matching pair ${keyIndex + 1} second field`} value={key.rightColumnId} onChange={(event) => updateKey(keyIndex, 'rightColumnId', event.target.value)} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                      <option value="">Choose a field</option>
                      {rightOptions.map((column) => <option key={column.id} value={column.id}>{column.label || column.name} · {column.clickhouseType}</option>)}
                    </select>
                  </label>
                  {draft.keys.length > 1 && (
                    <button type="button" onClick={() => setDraft((current) => ({ ...current, keys: current.keys.filter((_, index) => index !== keyIndex) }))} className="rounded px-2 py-2 text-xs text-red-700 hover:bg-red-50" aria-label={`Remove matching pair ${keyIndex + 1}`}>Remove</button>
                  )}
                </div>
              );
            })}
            <button type="button" onClick={addKey} className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50">Add another matching pair</button>
            {kind === 'KEY_JOIN' && (
              <label className="block max-w-sm text-xs font-medium text-slate-700" htmlFor="combine-join-type">
                If a row in the first table has no match
                <select id="combine-join-type" aria-label="If a row in the first table has no match" value={draft.joinType} onChange={(event) => setDraft((current) => ({ ...current, joinType: event.target.value as 'INNER' | 'LEFT' }))} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                  <option value="INNER">Drop it (match both tables)</option>
                  <option value="LEFT">Keep it and leave added fields empty</option>
                </select>
              </label>
            )}
            {kind === 'MEMBERSHIP' && (
              <label className="block max-w-sm text-xs font-medium text-slate-700" htmlFor="combine-membership-mode">
                Which rows should stay?
                <select id="combine-membership-mode" aria-label="Which rows should stay?" value={draft.membershipMode} onChange={(event) => setDraft((current) => ({ ...current, membershipMode: event.target.value as 'INCLUDE' | 'EXCLUDE' }))} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                  <option value="INCLUDE">Rows with a match</option>
                  <option value="EXCLUDE">Rows without a match</option>
                </select>
              </label>
            )}
          </div>
        )}

        {kind && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-end justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold text-slate-800">Choose fields for the new table</h3>
                <p className="mt-1 text-xs text-slate-600">Output names and labels stay editable. Source choices come from the pinned schemas above.</p>
              </div>
              <button type="button" onClick={addOutput} className="rounded border border-blue-300 px-3 py-1.5 text-sm font-medium text-blue-800 hover:bg-blue-50">Add output field</button>
            </div>
            {draft.outputs.length === 0 && <p className="rounded-md bg-slate-50 p-3 text-sm text-slate-600">Add fields to define what the combined table will contain.</p>}
            <div className="space-y-3">
              {draft.outputs.map((output, outputIndex) => (
                <article key={output.id} className="rounded-lg border border-slate-200 bg-white p-3">
                  <div className="grid gap-2 md:grid-cols-[1fr_1fr_auto]">
                    <label className="text-xs font-medium text-slate-700" htmlFor={`combine-output-name-${output.id}`}>
                      Output name
                      <input id={`combine-output-name-${output.id}`} aria-label={`Output field ${outputIndex + 1} name`} value={output.name} onChange={(event) => updateOutput(output.id, { name: event.target.value })} className="mt-1 block w-full rounded-md border border-slate-300 px-2 py-2 text-sm" placeholder="e.g. visit_date" />
                    </label>
                    <label className="text-xs font-medium text-slate-700" htmlFor={`combine-output-label-${output.id}`}>
                      Display label
                      <input id={`combine-output-label-${output.id}`} aria-label={`Output field ${outputIndex + 1} label`} value={output.label} onChange={(event) => updateOutput(output.id, { label: event.target.value })} className="mt-1 block w-full rounded-md border border-slate-300 px-2 py-2 text-sm" placeholder="e.g. Visit date" />
                    </label>
                    <button type="button" onClick={() => removeOutput(output.id)} className="self-end rounded px-2 py-2 text-sm text-red-700 hover:bg-red-50" aria-label={`Remove output field ${outputIndex + 1}`}>Remove</button>
                  </div>
                  <div className="mt-3 grid gap-2 md:grid-cols-2">
                    {(kind === 'MEMBERSHIP' ? [0] : draft.inputs.map((_, index) => index)).map((inputIndex) => {
                      const pinned = selected[inputIndex];
                      const choices = sourceOptions(output, inputIndex);
                      const sourceId = output.inputColumnIds[inputIndex] ?? '';
                      return (
                        <label key={inputIndex} className="text-xs font-medium text-slate-700" htmlFor={`combine-output-source-${output.id}-${inputIndex}`}>
                          {kind === 'APPEND'
                            ? `Matching field in input ${inputIndex + 1}`
                            : `Source field in input ${inputIndex + 1}${pinned ? ` · ${pinned.outputTitle}` : ''}`}
                          <select id={`combine-output-source-${output.id}-${inputIndex}`} aria-label={kind === 'APPEND' ? `Output field ${outputIndex + 1} matching field in input ${inputIndex + 1}` : `Output field ${outputIndex + 1} source field in input ${inputIndex + 1}`} value={sourceId} onChange={(event) => mapOutput(output.id, inputIndex, event.target.value)} disabled={!pinned} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm disabled:bg-slate-100">
                            <option value="">Choose a field</option>
                            {choices.map((column) => <option key={column.id} value={column.id}>{column.label || column.name} · {column.type || column.clickhouseType}{column.nullable ? ' · nullable' : ''}</option>)}
                          </select>
                        </label>
                      );
                    })}
                  </div>
                </article>
              ))}
            </div>
          </div>
        )}
      </fieldset>

      {catalog.kind === 'ready' && revisions.length > 0 && (
        <p role="status" aria-live="polite" className={`rounded-md p-3 text-sm ${built.intent ? 'bg-emerald-50 text-emerald-900' : 'bg-slate-50 text-slate-700'}`}>
          {built.intent ? 'Ready to preview this combined table.' : built.issue}
        </p>
      )}
    </section>
  );
};
