import React, { useMemo, useState } from 'react';
import type {
  ConstructionCombineCatalog,
  ConstructionCombineCandidateIntent,
  ConstructionCombineColumn,
  ConstructionCombineInputRef,
  ConstructionCombineKind,
  ConstructionCombineKey,
  ConstructionCombineOperation,
  ConstructionCombineOutputColumn,
  ConstructionCombineProjection,
  ConstructionCombineSource,
  ConstructionCombinePublishedRevision,
  ConstructionCombineWorkspaceOutput,
  ConstructionCombineStep,
} from './combineEditorTypes';

type InputDraft = { readonly inputKey: string };
const APPEND_EMPTY_INPUT = Symbol('append-empty-for-this-table');
const APPEND_EMPTY_OPTION = 'empty-for-this-table';
const APPEND_COLUMN_OPTION_PREFIX = 'column:';
type KeyDraft = { readonly leftColumnId: string; readonly rightColumnId: string };
type OutputDraft = {
  readonly id: string;
  readonly name: string;
  readonly label: string;
  readonly inputColumnIds: ReadonlyArray<string | typeof APPEND_EMPTY_INPUT>;
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
  readonly workspaceInputs?: ReadonlyArray<ConstructionCombineWorkspaceOutput>;
  readonly constructionVersion: number;
  readonly editingStep?: ConstructionCombineStep;
  readonly disabled: boolean;
  readonly loadingMore?: boolean;
  readonly loadMoreError?: string;
  readonly onLoadMore?: () => void;
  readonly onRetryCatalog?: () => void;
  readonly onCandidateChange: (intent: ConstructionCombineCandidateIntent | undefined) => void;
}

const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

const revisionKey = (revision: Pick<ConstructionCombinePublishedRevision, 'tableId' | 'revisionId' | 'outputId'>): string =>
  JSON.stringify([revision.tableId, revision.revisionId, revision.outputId]);

const workspaceOutputKey = (outputId: string): string => JSON.stringify(['WORKSPACE_OUTPUT', outputId]);

const sourceKey = (source: ConstructionCombineSource): string =>
  source.kind === 'TABLE_REVISION' ? revisionKey(source) : workspaceOutputKey(source.outputId);

const inputRefKey = (input: ConstructionCombineInputRef): string =>
  input.kind === 'TABLE_REVISION' ? revisionKey(input) : workspaceOutputKey(input.outputId);

const toInputRef = (source: ConstructionCombineSource): ConstructionCombineInputRef => source.kind === 'TABLE_REVISION'
  ? { kind: 'TABLE_REVISION', tableId: source.tableId, revisionId: source.revisionId, outputId: source.outputId }
  : { kind: 'WORKSPACE_OUTPUT', outputId: source.outputId };

const sourceTitle = (source: ConstructionCombineSource): string =>
  source.kind === 'TABLE_REVISION' ? source.outputTitle : source.title;

const sourceDescriptor = (source: ConstructionCombineSource): string => source.kind === 'TABLE_REVISION'
  ? `${source.rowMeaning} · pinned revision ${source.revisionId}`
  : `${source.title} · current draft`;

const emptyDraft = (stepId = createOpaqueId('combine')): CombineDraft => ({
  stepId,
  inputs: [{ inputKey: '' }, { inputKey: '' }],
  keys: [{ leftColumnId: '', rightColumnId: '' }],
  joinType: 'INNER',
  membershipMode: 'INCLUDE',
  outputs: [],
});

const draftFromStep = (step: ConstructionCombineStep): CombineDraft => {
  const combine = step.operation.combine;
  const key = combine.kind === 'KEY_JOIN' || combine.kind === 'MEMBERSHIP'
    ? combine.keys
    : [];
  return {
    stepId: step.id,
    kind: combine.kind,
    inputs: step.inputs.map((input) => ({ inputKey: inputRefKey(input) })),
    keys: key.length > 0 ? key.map((pair) => ({ ...pair })) : [{ leftColumnId: '', rightColumnId: '' }],
    joinType: combine.kind === 'KEY_JOIN' ? combine.joinType : 'INNER',
    membershipMode: combine.kind === 'MEMBERSHIP' ? combine.membershipMode : 'INCLUDE',
    outputs: step.outputs.map((output) => ({
      id: output.id,
      name: output.name,
      label: output.label,
      inputColumnIds: step.inputs.map((_, inputIndex) => {
        const sourceId = combine.projections.find((projection) =>
          projection.outputColumnId === output.id && projection.inputIndex === inputIndex,
        )?.inputColumnId;
        return sourceId ?? (combine.kind === 'APPEND' ? APPEND_EMPTY_INPUT : '');
      }),
    })),
  };
};

const columnLabel = (column: ConstructionCombineColumn): string =>
  `${column.label || column.name} (${column.type || column.clickhouseType || 'unknown'}${column.nullable ? ', nullable' : ''}${column.repeated ? ', repeated' : ''})`;

const joinableScalarBaseType = (clickhouseType: string): string | undefined => {
  const type = clickhouseType.trim();
  if (!type) return undefined;
  const base = type.startsWith('Nullable(') && type.endsWith(')')
    ? type.slice('Nullable('.length, -1)
    : type;
  if (base.startsWith('Nullable(') || base.startsWith('Array(')) return undefined;
  return base === 'String' || base === 'Bool' || /^(U?Int|Decimal|Date)/.test(base)
    ? base
    : undefined;
};

const joinCompatibilityKey = (column: ConstructionCombineColumn): string | undefined =>
  column.joinCompatibilityKey ?? (column.clickhouseType ? joinableScalarBaseType(column.clickhouseType) : undefined);

const isJoinableColumn = (column: ConstructionCombineColumn, allowNullable = false): boolean => {
  const clickhouseType = column.clickhouseType?.trim();
  const physicalNullable = Boolean(clickhouseType?.startsWith('Nullable(') && clickhouseType.endsWith(')'));
  const nullableShapeMatches = !clickhouseType || column.nullable === physicalNullable;
  return nullableShapeMatches && !column.repeated && Boolean(joinCompatibilityKey(column)) && (allowNullable || !column.nullable);
};

const isMatchKeyColumn = (column: ConstructionCombineColumn, kind: ConstructionCombineKind): boolean =>
  isJoinableColumn(column, kind === 'KEY_JOIN' || kind === 'MEMBERSHIP');

const appendPhysicalType = (value: string): { readonly base: string; readonly nullable: boolean } | undefined => {
  const type = value.trim();
  const nullable = type.startsWith('Nullable(') && type.endsWith(')');
  const base = nullable ? type.slice('Nullable('.length, -1) : type;
  if (!['String', 'UUID', 'Date', 'DateTime64(3)', 'Bool', 'Int64', 'Float64'].includes(base)) return undefined;
  return { base, nullable };
};

const appendPhysicalTypeForLogicalType = (value: string): string | undefined => {
  switch (value.trim().toLowerCase()) {
    case 'string':
    case 'code': return 'String';
    case 'uuid': return 'UUID';
    case 'date': return 'Date';
    case 'date_time':
    case 'date-time':
    case 'datetime': return 'DateTime64(3)';
    case 'boolean': return 'Bool';
    case 'integer': return 'Int64';
    case 'decimal': return 'Float64';
    default: return undefined;
  }
};

const appendCompatibilityKey = (column: ConstructionCombineColumn): string | undefined => {
  if (column.appendCompatibilityKey) return column.appendCompatibilityKey;
  if (!column.clickhouseType || column.repeated) return undefined;
  const physical = appendPhysicalType(column.clickhouseType);
  if (!physical || physical.base !== appendPhysicalTypeForLogicalType(column.type)) return undefined;
  return `${column.type.trim().toLowerCase()}:${physical.base}`;
};

const isAppendableColumn = (column: ConstructionCombineColumn): boolean => {
  return Boolean(appendCompatibilityKey(column));
};

const appendCompatible = (
  left: ConstructionCombineColumn,
  right: ConstructionCombineColumn,
): boolean => {
  const leftKey = appendCompatibilityKey(left);
  const rightKey = appendCompatibilityKey(right);
  return Boolean(leftKey && leftKey === rightKey);
};

const appendOptionValue = (sourceId: string | typeof APPEND_EMPTY_INPUT): string =>
  sourceId === APPEND_EMPTY_INPUT ? APPEND_EMPTY_OPTION : sourceId ? `${APPEND_COLUMN_OPTION_PREFIX}${sourceId}` : '';

const appendSourceId = (optionValue: string): string | typeof APPEND_EMPTY_INPUT => {
  if (optionValue === APPEND_EMPTY_OPTION) return APPEND_EMPTY_INPUT;
  return optionValue.startsWith(APPEND_COLUMN_OPTION_PREFIX)
    ? optionValue.slice(APPEND_COLUMN_OPTION_PREFIX.length)
    : '';
};

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
  workspaceInputs: ReadonlyArray<ConstructionCombineWorkspaceOutput> = [],
): BuiltCandidate => {
  if (!draft.kind) return { issue: 'Choose how the tables should be combined.' };

  const revisions = catalog.kind === 'ready' ? catalog.revisions : [];
  const workspaceSources: ConstructionCombineWorkspaceOutput[] = [...workspaceInputs];
  const byKey = new Map<string, ConstructionCombineSource>([
    ...revisions.map((revision) => [revisionKey(revision), revision] as const),
    ...workspaceSources.map((source) => [sourceKey(source), source] as const),
  ]);
  const inputs = draft.inputs.map((input) => byKey.get(input.inputKey));
  if (inputs.some((input) => !input)) {
    if (catalog.kind === 'loading' && draft.inputs.some((input) => !input.inputKey || input.inputKey.startsWith('["TABLE_REVISION"'))) {
      return { issue: 'Published table details are still loading.' };
    }
    return { issue: 'Choose an available draft output or published version for each input.' };
  }
  const selectedInputs = inputs as ConstructionCombineSource[];
  if (new Set(selectedInputs.map(sourceKey)).size !== selectedInputs.length) {
    return { issue: 'Choose a different output for each input.' };
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
      if (!left || !right) return { issue: 'A selected matching field is missing from its input schema.' };
      if (!isMatchKeyColumn(left, draft.kind) || !isMatchKeyColumn(right, draft.kind) || joinCompatibilityKey(left) !== joinCompatibilityKey(right)) {
        return { issue: 'Matching fields must have the same supported scalar type.' };
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
      if (output.inputColumnIds.length !== selectedInputs.length || output.inputColumnIds.some((id) => id === '')) {
        return { issue: 'Choose a scalar field or explicitly choose “Empty for this table” for each input.' };
      }
      const mappedSources: { readonly column: ConstructionCombineColumn; readonly inputIndex: number }[] = [];
      for (const [inputIndex, columnId] of output.inputColumnIds.entries()) {
        if (columnId === APPEND_EMPTY_INPUT) continue;
        const column = selectedInputs[inputIndex].columns.find((candidate) => candidate.id === columnId);
        if (!column) return { issue: 'A selected output field is missing from its input schema.' };
        mappedSources.push({ column, inputIndex });
      }
      if (mappedSources.length === 0) return { issue: 'Choose a source field in at least one table for each output.' };
      const sourceColumns = mappedSources.map(({ column }) => column);
      if (sourceColumns.some((column) => !isAppendableColumn(column))) {
        return { issue: 'Stacked fields must use the same supported scalar type; repeated fields are not supported.' };
      }
      const first = sourceColumns[0];
      if (sourceColumns.slice(1).some((column) => !appendCompatible(first, column))) {
        return { issue: 'Fields stacked into one output must have the same logical and physical scalar type.' };
      }
      const nullable = output.inputColumnIds.some((id) => id === APPEND_EMPTY_INPUT) || sourceColumns.some((column) => column.nullable);
      outputColumns.push({ id: output.id, name, label, type: first.type, nullable });
      mappedSources.forEach(({ column, inputIndex }) => {
        projections.push({ outputColumnId: output.id, inputIndex, inputColumnId: column.id });
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
    if (!sourceColumn) return { issue: 'A selected output field is missing from its input schema.' };
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
  readonly kind: Extract<ConstructionCombineKind, 'KEY_JOIN' | 'APPEND' | 'MEMBERSHIP'>;
  readonly title: string;
  readonly description: string;
}> = [
  { kind: 'KEY_JOIN', title: 'Match rows', description: 'Bring fields together when selected values match.' },
  { kind: 'APPEND', title: 'Stack rows', description: 'Put tables with matching fields under each other.' },
  { kind: 'MEMBERSHIP', title: 'Keep or exclude matches', description: 'Keep rows from the first table based on values found in another.' },
];

export const ConstructionCombineEditor = ({
  catalog,
  workspaceInputs = [],
  constructionVersion,
  editingStep,
  disabled,
  loadingMore = false,
  loadMoreError,
  onLoadMore,
  onRetryCatalog,
  onCandidateChange,
}: ConstructionCombineEditorProps) => {
  const [draft, setDraft] = useState<CombineDraft>(() => editingStep ? draftFromStep(editingStep) : emptyDraft());
  const revisions = catalog.kind === 'ready' ? catalog.revisions : [];
  const sources: ReadonlyArray<ConstructionCombineSource> = [...revisions, ...workspaceInputs];
  const sourceByKey = useMemo(() => new Map(sources.map((source) => [sourceKey(source), source])), [catalog, workspaceInputs]);
  const built = useMemo(
    () => buildCandidate(draft, catalog, constructionVersion, workspaceInputs),
    [catalog, constructionVersion, draft, workspaceInputs],
  );
  const updateDraft = (next: CombineDraft) => {
    setDraft(next);
    onCandidateChange(buildCandidate(next, catalog, constructionVersion, workspaceInputs).intent);
  };

  const selected = draft.inputs.map((input) => sourceByKey.get(input.inputKey));
  const savedInputsMissing = Boolean(editingStep && catalog.kind === 'ready' && selected.some((input) => !input));
  const kind = draft.kind;
  const setInput = (inputIndex: number, key: string) => updateDraft({
      ...draft,
      inputs: draft.inputs.map((input, index) => index === inputIndex ? { inputKey: key } : input),
    });
  const addInput = () => updateDraft({
    ...draft,
    inputs: [...draft.inputs, { inputKey: '' }],
    outputs: draft.outputs.map((output) => ({ ...output, inputColumnIds: [...output.inputColumnIds, ''] })),
  });
  const removeInput = (inputIndex: number) => updateDraft({
    ...draft,
    inputs: draft.inputs.filter((_, index) => index !== inputIndex),
    outputs: draft.outputs.map((output) => ({
      ...output,
      inputColumnIds: output.inputColumnIds.filter((_, index) => index !== inputIndex),
    })),
  });
  const changeKind = (nextKind: ConstructionCombineKind) => updateDraft({
    ...draft,
    kind: nextKind,
    inputs: draft.inputs.slice(0, 2),
    keys: [{ leftColumnId: '', rightColumnId: '' }],
    outputs: [],
    joinType: 'INNER',
    membershipMode: 'INCLUDE',
  });
  const addKey = () => updateDraft({
    ...draft,
    keys: [...draft.keys, { leftColumnId: '', rightColumnId: '' }],
  });
  const updateKey = (keyIndex: number, side: 'leftColumnId' | 'rightColumnId', value: string) => updateDraft({
    ...draft,
    keys: draft.keys.map((key, index) => index === keyIndex ? { ...key, [side]: value } : key),
  });
  const removeKey = (keyIndex: number) => updateDraft({
    ...draft,
    keys: draft.keys.filter((_, index) => index !== keyIndex),
  });
  const addOutput = () => updateDraft({
    ...draft,
    outputs: [...draft.outputs, {
      id: createOpaqueId('combine_output'),
      name: '',
      label: '',
      inputColumnIds: draft.inputs.map(() => ''),
    }],
  });
  const updateOutput = (outputId: string, patch: Partial<OutputDraft>) => updateDraft({
    ...draft,
    outputs: draft.outputs.map((output) => output.id === outputId ? { ...output, ...patch } : output),
  });
  const mapOutput = (outputId: string, inputIndex: number, columnId: string | typeof APPEND_EMPTY_INPUT) => updateDraft({
    ...draft,
    outputs: draft.outputs.map((output) => output.id === outputId
      ? { ...output, inputColumnIds: output.inputColumnIds.map((id, index) => index === inputIndex ? columnId : id) }
      : output),
  });
  const removeOutput = (outputId: string) => updateDraft({
    ...draft,
    outputs: draft.outputs.filter((output) => output.id !== outputId),
  });

  const updateChoices = (inputIndex: number): ReadonlyArray<ConstructionCombinePublishedRevision> => {
    const pinned = selected[inputIndex];
    if (!pinned || pinned.kind !== 'TABLE_REVISION') return [];
    return revisions.filter((revision) =>
      revision.tableId === pinned.tableId && revision.outputId === pinned.outputId && revision.isCurrent,
    );
  };

  const sourceOptions = (output: OutputDraft, inputIndex: number): ReadonlyArray<ConstructionCombineColumn> => {
    const source = selected[inputIndex];
    if (!source) return [];
    if (kind === 'APPEND') {
      const anchor = output.inputColumnIds.reduce<ConstructionCombineColumn | undefined>((found, columnId, index) => {
        if (found || !columnId || columnId === APPEND_EMPTY_INPUT) return found;
        return selected[index]?.columns.find((column) => column.id === columnId);
      }, undefined);
      const scalarColumns = source.columns.filter(isAppendableColumn);
      return anchor ? scalarColumns.filter((column) => appendCompatible(anchor, column) || column.id === output.inputColumnIds[inputIndex]) : scalarColumns;
    }
    if (kind === 'KEY_JOIN' && draft.joinType === 'LEFT' && inputIndex === 1) {
      return source.columns.filter((column) => !column.repeated);
    }
    return source.columns;
  };

  const canUseSources = sources.length > 0;
  const selectionUnavailable = (catalog.kind === 'loading' || catalog.kind === 'failed')
    && (workspaceInputs.length === 0 || Boolean(editingStep?.inputs.some((input) => input.kind === 'TABLE_REVISION')));

  return (
    <section className="space-y-6" data-testid="construction-combine-editor" aria-label="Combine tables">
      <header>
        <h2 className="text-base font-semibold text-slate-900">Combine tables</h2>
        <p className="mt-1 text-sm text-slate-600">Choose current draft outputs or exact published versions, then select how their rows and fields fit together.</p>
      </header>

      {catalog.kind === 'loading' && <p role="status" className="text-sm text-slate-600">Loading available published tables…</p>}
      {catalog.kind === 'failed' && <p role="alert" className="text-sm text-red-700">Could not load published tables: {catalog.message}</p>}
      {catalog.kind === 'failed' && onRetryCatalog ? <button type="button" onClick={onRetryCatalog} disabled={disabled} className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium">Try again</button> : null}
      {savedInputsMissing ? (
        <p role={catalog.kind === 'ready' && !catalog.nextCursor ? 'alert' : 'status'} className="text-sm text-amber-900">
          {catalog.kind === 'ready' && !catalog.nextCursor
            ? 'A saved input version is unavailable in the currently authorized published tables.'
            : 'Load more published versions to find the saved input versions before editing this step.'}
        </p>
      ) : null}
      {catalog.kind === 'ready' && revisions.length === 0 && workspaceInputs.length === 0 && !catalog.nextCursor && (
        <p role="status" className="rounded-md bg-slate-50 p-3 text-sm text-slate-600">No published table outputs are available in this workspace yet.</p>
      )}

      <fieldset disabled={disabled || selectionUnavailable || !canUseSources || savedInputsMissing} className="space-y-6 disabled:opacity-75">
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

        {kind === 'APPEND' ? (
          <p className="rounded-md bg-blue-50 p-3 text-sm text-blue-900">
            For each output, choose matching scalar fields or explicitly select “Empty for this table”. Empty inputs become nulls and make the output nullable. Repeated fields are not supported.
          </p>
        ) : null}

        {kind && (
          <div className="space-y-3">
            <div>
              <h3 className="text-sm font-semibold text-slate-800">Choose the input tables</h3>
              <p className="mt-1 text-xs text-slate-600">Published versions stay pinned; draft inputs resolve from the current saved workspace.</p>
            </div>
            <div className="grid gap-3 lg:grid-cols-2">
              {draft.inputs.map((input, inputIndex) => {
                const pinned = selected[inputIndex];
                const newer = updateChoices(inputIndex).filter((candidate) => revisionKey(candidate) !== input.inputKey);
                const usedKeys = new Set(draft.inputs.filter((_, index) => index !== inputIndex).map((candidate) => candidate.inputKey));
                return (
                  <article key={`input-${inputIndex}`} className="rounded-lg border border-slate-200 bg-white p-3">
                    <div className="flex items-start justify-between gap-3">
                      <label className="min-w-0 flex-1 text-sm font-medium text-slate-800" htmlFor={`combine-input-${inputIndex}`}>
                        Input table {inputIndex + 1}
                        <select
                          id={`combine-input-${inputIndex}`}
                          aria-label={`Input table ${inputIndex + 1}`}
                          className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2.5 py-2 text-sm"
                          value={input.inputKey}
                          onChange={(event) => setInput(inputIndex, event.target.value)}
                        >
                          <option value="">Choose a table output</option>
                          {workspaceInputs.length > 0 ? (
                            <optgroup label="Current draft tables">
                              {workspaceInputs.map((source) => {
                                const optionKey = workspaceOutputKey(source.outputId);
                                return <option key={optionKey} value={optionKey} disabled={usedKeys.has(optionKey)}>{source.title} · current draft</option>;
                              })}
                            </optgroup>
                          ) : null}
                          {revisions.length > 0 ? <optgroup label="Published versions">{revisions.map((revision) => {
                            const optionKey = revisionKey(revision);
                            const isUnavailable = usedKeys.has(optionKey);
                            return (
                              <option key={optionKey} value={optionKey} disabled={isUnavailable}>
                                {revision.tableTitle} · {revision.rowMeaning} · {revision.outputTitle} · revision {revision.revisionId}{revision.isCurrent ? ' · current' : ''}
                              </option>
                            );
                          })}</optgroup> : null}
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
                          <span className="font-medium text-slate-800">{sourceDescriptor(pinned)}</span>
                          {pinned.kind === 'TABLE_REVISION' && pinned.isCurrent && <span className="rounded bg-emerald-50 px-1.5 py-0.5 text-emerald-800">Current published version</span>}
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
              <p className="mt-1 text-xs text-slate-600">
                {kind === 'KEY_JOIN'
                  ? 'Select fields with the same scalar type. A NULL key never matches another NULL key. Add more than one pair when a match uses multiple values.'
                  : 'Select fields with the same type. Missing values never match. “Rows with a match” leaves out rows with missing matching values; “Rows without a match” keeps them. Add more than one pair when a match uses multiple values.'}
              </p>
            </div>
            {draft.keys.map((key, keyIndex) => {
              const leftOptions = selected[0]?.columns.filter((column) => isMatchKeyColumn(column, kind)) ?? [];
              const leftColumn = selected[0]?.columns.find((column) => column.id === key.leftColumnId);
              const rightOptions = selected[1]?.columns.filter((column) =>
                isMatchKeyColumn(column, kind) && (!leftColumn || joinCompatibilityKey(column) === joinCompatibilityKey(leftColumn)),
              ) ?? [];
              return (
                <div key={`key-${keyIndex}`} className="grid gap-2 sm:grid-cols-[1fr_auto_1fr_auto] sm:items-end">
                  <label className="text-xs font-medium text-slate-700" htmlFor={`combine-key-left-${keyIndex}`}>
                    {selected[0] ? sourceTitle(selected[0]) : 'Input 1'} field
                    <select id={`combine-key-left-${keyIndex}`} aria-label={`Matching pair ${keyIndex + 1} first field`} value={key.leftColumnId} onChange={(event) => updateKey(keyIndex, 'leftColumnId', event.target.value)} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                      <option value="">Choose a field</option>
                      {leftOptions.map((column) => <option key={column.id} value={column.id}>{column.label || column.name} · {joinCompatibilityKey(column)}</option>)}
                    </select>
                  </label>
                  <span className="pb-2 text-center text-xs text-slate-500">matches</span>
                  <label className="text-xs font-medium text-slate-700" htmlFor={`combine-key-right-${keyIndex}`}>
                    {selected[1] ? sourceTitle(selected[1]) : 'Input 2'} field
                    <select id={`combine-key-right-${keyIndex}`} aria-label={`Matching pair ${keyIndex + 1} second field`} value={key.rightColumnId} onChange={(event) => updateKey(keyIndex, 'rightColumnId', event.target.value)} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                      <option value="">Choose a field</option>
                      {rightOptions.map((column) => <option key={column.id} value={column.id}>{column.label || column.name} · {joinCompatibilityKey(column)}</option>)}
                    </select>
                  </label>
                  {draft.keys.length > 1 && (
                    <button type="button" onClick={() => removeKey(keyIndex)} className="rounded px-2 py-2 text-xs text-red-700 hover:bg-red-50" aria-label={`Remove matching pair ${keyIndex + 1}`}>Remove</button>
                  )}
                </div>
              );
            })}
            <button type="button" onClick={addKey} className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50">Add another matching pair</button>
            {kind === 'KEY_JOIN' && (
              <label className="block max-w-sm text-xs font-medium text-slate-700" htmlFor="combine-join-type">
                If a row in the first table has no match
                <select id="combine-join-type" aria-label="If a row in the first table has no match" value={draft.joinType} onChange={(event) => updateDraft({ ...draft, joinType: event.target.value as 'INNER' | 'LEFT' })} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm">
                  <option value="INNER">Drop it (match both tables)</option>
                  <option value="LEFT">Keep it and leave added fields empty</option>
                </select>
              </label>
            )}
            {kind === 'MEMBERSHIP' && (
              <label className="block max-w-sm text-xs font-medium text-slate-700" htmlFor="combine-membership-mode">
                Which rows should stay?
                <select
                  id="combine-membership-mode"
                  aria-label="Which rows should stay?"
                  value={draft.membershipMode}
                  onChange={(event) => {
                    const mode = event.target.value;
                    if (mode === 'INCLUDE' || mode === 'EXCLUDE') {
                      updateDraft({ ...draft, membershipMode: mode });
                    }
                  }}
                  className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm"
                >
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
                    {draft.inputs.map((_, inputIndex) => {
                      const pinned = selected[inputIndex];
                      const choices = sourceOptions(output, inputIndex);
                      const sourceId = output.inputColumnIds[inputIndex] ?? '';
                      return (
                        <label key={inputIndex} className="text-xs font-medium text-slate-700" htmlFor={`combine-output-source-${output.id}-${inputIndex}`}>
                          {kind === 'APPEND'
                            ? `Matching field in input ${inputIndex + 1}`
                            : `Source field in input ${inputIndex + 1}${pinned ? ` · ${sourceTitle(pinned)}` : ''}`}
                          <select id={`combine-output-source-${output.id}-${inputIndex}`} aria-label={kind === 'APPEND' ? `Output field ${outputIndex + 1} matching field in input ${inputIndex + 1}` : `Output field ${outputIndex + 1} source field in input ${inputIndex + 1}`} value={kind === 'APPEND' ? appendOptionValue(sourceId) : typeof sourceId === 'string' ? sourceId : ''} onChange={(event) => mapOutput(output.id, inputIndex, kind === 'APPEND' ? appendSourceId(event.target.value) : event.target.value)} disabled={!pinned} className="mt-1 block w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm disabled:bg-slate-100">
                            <option value="">Choose a field</option>
                            {kind === 'APPEND' ? <option value={APPEND_EMPTY_OPTION}>Empty for this table</option> : null}
                            {choices.map((column) => <option key={column.id} value={kind === 'APPEND' ? `${APPEND_COLUMN_OPTION_PREFIX}${column.id}` : column.id}>{column.label || column.name} · {column.type || column.clickhouseType}{column.nullable ? ' · nullable' : ''}</option>)}
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

      {catalog.kind === 'ready' && catalog.nextCursor ? (
        <div className="space-y-1">
          <button type="button" onClick={onLoadMore} disabled={disabled || loadingMore || !onLoadMore} className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 disabled:opacity-50">
            {loadingMore ? 'Loading more published versions…' : 'Load more published versions'}
          </button>
          {loadMoreError ? <p role="alert" className="text-sm text-red-700">Could not load more published versions: {loadMoreError}</p> : null}
        </div>
      ) : null}

      {canUseSources && (
        <p role="status" aria-live="polite" className={`rounded-md p-3 text-sm ${built.intent ? 'bg-emerald-50 text-emerald-900' : 'bg-slate-50 text-slate-700'}`}>
          {built.intent ? 'Preview updates automatically as you configure this table.' : built.issue}
        </p>
      )}
    </section>
  );
};
