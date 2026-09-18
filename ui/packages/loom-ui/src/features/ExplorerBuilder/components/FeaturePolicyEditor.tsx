import React, { useEffect, useState } from 'react';
import type {
  ExplorerBuilderCandidate,
  ExplorerBuilderColumn,
  ExplorerColumnSource,
} from '../../../types';

export const RelatedFeatureCreator = ({
  resourceLabel,
  disabled,
  onAdd,
}: {
  readonly resourceLabel: string;
  readonly disabled: boolean;
  readonly onAdd: (source: ExplorerColumnSource, title: string) => void;
}) => (
  <div className="flex items-center gap-1.5">
    <span className="text-[11px] font-medium text-slate-600">Add relationship feature</span>
    <button
      type="button"
      disabled={disabled}
      className="rounded border border-slate-300 bg-white px-2 py-1 text-[11px] font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-40"
      onClick={() =>
        onAdd(
          { kind: 'aggregate', aggregate: { operation: 'COUNT' } },
          `${resourceLabel} count`,
        )
      }
    >
      Count
    </button>
    <button
      type="button"
      disabled={disabled}
      className="rounded border border-slate-300 bg-white px-2 py-1 text-[11px] font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-40"
      onClick={() =>
        onAdd(
          { kind: 'aggregate', aggregate: { operation: 'EXISTS' } },
          `Has ${resourceLabel}`,
        )
      }
    >
      Yes / no
    </button>
  </div>
);

const nestedValueLabels = {
  VALUE: 'Single value',
  INDEXED: 'Indexed value',
  FIRST: 'First value',
  ALL: 'All values',
  DISTINCT: 'Unique values',
} as const;

const resourceAggregateLabels = {
  COUNT: 'Count matching resources',
  EXISTS: 'Whether any resource matches',
} as const;

const fieldAggregateLabels = {
  COUNT: 'Count values',
  COUNT_DISTINCT: 'Count unique values',
  DISTINCT_VALUES: 'Collect unique values',
  MIN: 'Minimum value',
  MAX: 'Maximum value',
  EXISTS: 'Whether any value exists',
} as const;

const relatedValueReductionLabels = {
  REQUIRE_ONE: 'Require zero or one value',
  COLLECT: 'Collect every value',
  FIRST_BY_RESOURCE_KEY: 'First record by stable resource key',
  COUNT: fieldAggregateLabels.COUNT,
  DISTINCT_VALUES: fieldAggregateLabels.DISTINCT_VALUES,
  COUNT_DISTINCT: fieldAggregateLabels.COUNT_DISTINCT,
  MIN: fieldAggregateLabels.MIN,
  MAX: fieldAggregateLabels.MAX,
  EXISTS: fieldAggregateLabels.EXISTS,
  FIRST_ORDERED: 'Value nearest a date',
} as const;

type ResourceAggregateOperation = keyof typeof resourceAggregateLabels;
type FieldAggregateOperation = keyof typeof fieldAggregateLabels;
type RelatedValueReduction = keyof typeof relatedValueReductionLabels;

type TemporalAggregateSource = Extract<
  ExplorerColumnSource,
  { kind: 'aggregate' }
>['aggregate'] & { operation: 'FIRST_ORDERED' };
type AggregateSource = Extract<ExplorerColumnSource, { kind: 'aggregate' }>;
type UnitNormalizationDraft = NonNullable<AggregateSource['aggregate']['unitNormalization']>;

const approvedUnitPolicyOptions = [
  ['to-centimeters', 'Convert measurements to centimeters'],
  ['to-kilograms', 'Convert measurements to kilograms'],
  ['to-celsius', 'Convert measurements to Celsius'],
  ['to-fahrenheit', 'Convert measurements to Fahrenheit'],
] as const;

const UnitNormalizationEditor = ({
  current,
  disabled,
  onApply,
}: {
  readonly current?: UnitNormalizationDraft;
  readonly disabled: boolean;
  readonly onApply: (value: UnitNormalizationDraft | undefined) => void;
}) => {
  const [policyID, setPolicyID] = useState(current?.policyId ?? approvedUnitPolicyOptions[0][0]);

  return (
    <fieldset className="mt-1 grid w-full gap-2 rounded border border-violet-200 bg-violet-50/60 p-2 text-[11px] text-slate-700">
      <legend className="px-1 font-semibold text-violet-900">Normalize measurement units</legend>
      <label className="flex min-w-0 flex-col gap-0.5 font-medium">
        <span>Conversion</span>
        <select aria-label="Unit conversion preset" className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal" value={policyID} disabled={disabled} onChange={(event) => setPolicyID(event.currentTarget.value)}>
          {approvedUnitPolicyOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select>
      </label>
      <p className="text-slate-600">Loom identifies the Quantity unit fields and applies the approved conversion for each measurement.</p>
      <div className="flex gap-2">
        <button type="button" className="rounded bg-violet-700 px-2.5 py-1 font-semibold text-white hover:bg-violet-800 disabled:opacity-40" disabled={disabled} onClick={() => onApply({ policyId: policyID, version: '1' })}>Apply normalization</button>
        {current ? <button type="button" className="rounded border border-slate-300 bg-white px-2.5 py-1 font-semibold text-slate-700 hover:bg-slate-50" disabled={disabled} onClick={() => onApply(undefined)}>Remove normalization</button> : null}
      </div>
    </fieldset>
  );
};

type ContributorDraft = NonNullable<ExplorerBuilderColumn['contributor']>;

const candidateSupportsEquality = (candidate: ExplorerBuilderCandidate) =>
  ['string', 'token', 'id', 'uri', 'url', 'canonical', 'code'].includes(
    candidate.logicalType.toLowerCase(),
  );

const candidateUsesCodeValue = (candidate: ExplorerBuilderCandidate) => {
  const path = candidate.fieldPath.toLowerCase();
  return candidate.logicalType.toLowerCase() === 'code' || path === 'code' || path.endsWith('.code');
};

const contributorValue = (contributor?: ContributorDraft) => {
  if (contributor?.value?.kind === 'STRING') return contributor.value.string;
  if (contributor?.value?.kind === 'CODE') return contributor.value.code.code;
  return '';
};

const ContributorEditor = ({
  current,
  candidates,
  featureLabel,
  resourceLabel,
  disabled,
  onApply,
}: {
  readonly current?: ContributorDraft;
  readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly featureLabel: string;
  readonly resourceLabel: string;
  readonly disabled: boolean;
  readonly onApply: (contributor: ContributorDraft | undefined) => void;
}) => {
  const [candidateID, setCandidateID] = useState(current?.candidateId ?? '');
  const [operator, setOperator] = useState<'EXISTS' | 'EQUALS'>(current?.operator ?? 'EXISTS');
  const [value, setValue] = useState(contributorValue(current));
  const selected = candidates.find((candidate) => candidate.candidateId === candidateID);
  const currentCandidate = candidates.find((candidate) => candidate.candidateId === current?.candidateId);

  useEffect(() => {
    setCandidateID(current?.candidateId ?? '');
    setOperator(current?.operator ?? 'EXISTS');
    setValue(contributorValue(current));
  }, [current]);

  const existsPredicate = (candidate: ExplorerBuilderCandidate): ContributorDraft => ({
    candidateId: candidate.candidateId,
    operator: 'EXISTS',
    ...(candidate.repeated ? { quantifier: 'ANY' as const } : {}),
  });
  const equalityPredicate = (candidate: ExplorerBuilderCandidate): ContributorDraft => ({
    candidateId: candidate.candidateId,
    operator: 'EQUALS',
    ...(candidate.repeated ? { quantifier: 'ANY' as const } : {}),
    value: candidateUsesCodeValue(candidate)
      ? { kind: 'CODE', code: { code: value } }
      : { kind: 'STRING', string: value },
  });
  const exactMeaning = current && currentCandidate
    ? current.operator === 'EQUALS'
      ? `Only ${resourceLabel} records where ${currentCandidate.label} equals “${contributorValue(current)}” contribute to this feature.`
      : `Only ${resourceLabel} records with ${currentCandidate.label} contribute to this feature.`
    : `Every matching ${resourceLabel} record contributes to this feature.`;

  return (
    <fieldset className="grid min-w-72 gap-1.5 rounded border border-slate-200 bg-slate-50/70 p-2">
      <legend className="px-1 font-semibold text-slate-700">Contributing records</legend>
      <label className="grid gap-0.5 font-medium text-slate-700">
        <span>Use records where</span>
        <select
          aria-label={`Contributors for ${featureLabel}`}
          className="max-w-72 rounded border border-slate-300 bg-white px-1.5 py-1 text-xs font-normal"
          value={candidateID}
          disabled={disabled}
          onChange={(event) => {
            const nextID = event.currentTarget.value;
            setCandidateID(nextID);
            setOperator('EXISTS');
            setValue('');
            const candidate = candidates.find(({ candidateId }) => candidateId === nextID);
            onApply(candidate ? existsPredicate(candidate) : undefined);
          }}
        >
          <option value="">All matching {resourceLabel} records</option>
          {candidates.map((candidate) => (
            <option key={candidate.candidateId} value={candidate.candidateId}>{candidate.label}</option>
          ))}
        </select>
      </label>
      {selected ? (
        <label className="grid gap-0.5 font-medium text-slate-700">
          <span>Condition</span>
          <select
            aria-label={`Contributor condition for ${featureLabel}`}
            className="rounded border border-slate-300 bg-white px-1.5 py-1 text-xs font-normal"
            value={operator}
            disabled={disabled}
            onChange={(event) => {
              const next = event.currentTarget.value as 'EXISTS' | 'EQUALS';
              setOperator(next);
              if (next === 'EXISTS') onApply(existsPredicate(selected));
            }}
          >
            <option value="EXISTS">Has a value</option>
            {candidateSupportsEquality(selected) ? <option value="EQUALS">Equals a value</option> : null}
          </select>
        </label>
      ) : null}
      {selected && operator === 'EQUALS' ? (
        <div className="flex items-end gap-2">
          <label className="grid flex-1 gap-0.5 font-medium text-slate-700">
            <span>{candidateUsesCodeValue(selected) ? 'Code' : 'Value'}</span>
            <input
              aria-label={`Contributor value for ${featureLabel}`}
              className="rounded border border-slate-300 bg-white px-1.5 py-1 text-xs font-normal"
              value={value}
              disabled={disabled}
              onChange={(event) => setValue(event.currentTarget.value)}
            />
          </label>
          <button
            type="button"
            className="rounded bg-slate-800 px-2.5 py-1 font-semibold text-white hover:bg-slate-900 disabled:opacity-40"
            disabled={disabled || (candidateUsesCodeValue(selected) && value.trim() === '')}
            onClick={() => onApply(equalityPredicate(selected))}
          >
            Apply condition
          </button>
        </div>
      ) : null}
      <p className="text-slate-600">{exactMeaning}</p>
    </fieldset>
  );
};

const TemporalReductionEditor = ({
  path,
  current,
  timestampCandidates,
  anchorCandidates,
  disabled,
  onApply,
}: {
  readonly path: string;
  readonly current?: TemporalAggregateSource;
  readonly timestampCandidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly anchorCandidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly disabled: boolean;
  readonly onApply: (source: ExplorerColumnSource) => void;
}) => {
  const temporal = current?.temporal;
  const [timestampPath, setTimestampPath] = useState(
    temporal?.timestampPath ?? timestampCandidates[0]?.fieldPath ?? '',
  );
  const [anchorPath, setAnchorPath] = useState(
    temporal?.anchorPath ?? anchorCandidates[0]?.fieldPath ?? '',
  );
  const [lookbackDays, setLookbackDays] = useState(
    Math.max(0, Math.round(-(temporal?.lowerOffsetSeconds ?? -31_536_000) / 86_400)),
  );
  const [direction, setDirection] = useState<'ASC' | 'DESC'>(
    temporal?.direction ?? 'DESC',
  );
  const [tiePolicy, setTiePolicy] = useState<'REQUIRE_UNIQUE' | 'RESOURCE_KEY'>(
    temporal?.tiePolicy ?? 'REQUIRE_UNIQUE',
  );
  const ready = Boolean(timestampPath && anchorPath && Number.isInteger(lookbackDays));

  return (
    <fieldset className="mt-1 grid w-full grid-cols-2 gap-2 rounded border border-blue-200 bg-blue-50/60 p-2 text-[11px] text-slate-700">
      <legend className="px-1 font-semibold text-blue-900">Date-aware value selection</legend>
      <label className="flex min-w-0 flex-col gap-0.5 font-medium">
        <span>Record date</span>
        <select
          aria-label="Record date"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={timestampPath}
          disabled={disabled}
          onChange={(event) => setTimestampPath(event.currentTarget.value)}
        >
          <option value="">Choose a date field</option>
          {timestampCandidates.map((candidate) => (
            <option key={candidate.candidateId} value={candidate.fieldPath}>
              {candidate.label} · {candidate.fieldPath}
            </option>
          ))}
        </select>
      </label>
      <label className="flex min-w-0 flex-col gap-0.5 font-medium">
        <span>Compare with row date</span>
        <select
          aria-label="Compare with row date"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={anchorPath}
          disabled={disabled}
          onChange={(event) => setAnchorPath(event.currentTarget.value)}
        >
          <option value="">Choose a row date</option>
          {anchorCandidates.map((candidate) => (
            <option key={candidate.candidateId} value={candidate.fieldPath}>
              {candidate.label} · {candidate.fieldPath}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-0.5 font-medium">
        <span>Choose</span>
        <select
          aria-label="Date selection direction"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={direction}
          disabled={disabled}
          onChange={(event) => setDirection(event.currentTarget.value as 'ASC' | 'DESC')}
        >
          <option value="DESC">Latest value</option>
          <option value="ASC">Earliest value</option>
        </select>
      </label>
      <label className="flex flex-col gap-0.5 font-medium">
        <span>Look back (days)</span>
        <input
          aria-label="Look back days"
          type="number"
          min={0}
          step={1}
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={lookbackDays}
          disabled={disabled}
          onChange={(event) => setLookbackDays(event.currentTarget.valueAsNumber)}
        />
      </label>
      <label className="col-span-2 flex items-center gap-1.5 font-medium">
        <span>If dates tie</span>
        <select
          aria-label="Equal date handling"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={tiePolicy}
          disabled={disabled}
          onChange={(event) => setTiePolicy(event.currentTarget.value as 'REQUIRE_UNIQUE' | 'RESOURCE_KEY')}
        >
          <option value="REQUIRE_UNIQUE">Stop and ask me to resolve it</option>
          <option value="RESOURCE_KEY">Choose deterministically by resource key</option>
        </select>
      </label>
      <p className="col-span-2 text-slate-600">
        {direction === 'DESC' ? 'Latest' : 'Earliest'} {path} dated from {lookbackDays} days before through the row date.
      </p>
      {timestampCandidates.length === 0 || anchorCandidates.length === 0 ? (
        <p className="col-span-2 text-amber-800">
          This selection needs one date field on the related record and one on the row resource.
        </p>
      ) : null}
      <button
        type="button"
        className="col-span-2 justify-self-start rounded bg-blue-700 px-2.5 py-1 font-semibold text-white hover:bg-blue-800 disabled:opacity-40"
        disabled={disabled || !ready || lookbackDays < 0}
        onClick={() =>
          onApply({
            kind: 'aggregate',
            aggregate: {
              operation: 'FIRST_ORDERED',
              path,
              temporal: {
                timestampPath,
                anchorPath,
                lowerOffsetSeconds: -lookbackDays * 86_400,
                upperOffsetSeconds: 0,
                lowerInclusive: true,
                upperInclusive: true,
                direction,
                precision: 'INSTANT',
                tiePolicy,
              },
            },
          })
        }
      >
        Apply date selection
      </button>
    </fieldset>
  );
};

const projectionExplanation = (
  mode: keyof typeof nestedValueLabels,
  resourceLabel: string,
): string => {
  switch (mode) {
    case 'ALL':
      return `Keeps every repeated value within each ${resourceLabel}.`;
    case 'DISTINCT':
      return `Keeps unique repeated values within each ${resourceLabel}; order and frequency are discarded.`;
    case 'FIRST':
      return `Keeps the first repeated value within each ${resourceLabel}; later values are discarded.`;
    case 'INDEXED':
      return `Keeps values by their stable position within each ${resourceLabel}.`;
    case 'VALUE':
      return `Keeps the scalar value from each ${resourceLabel}.`;
  }
};

export const FeaturePolicyEditor = ({
  column,
  candidate,
  candidates,
  anchorCandidates,
  related,
  resourceLabel,
  disabled,
  onSourceChange,
  onContributorChange,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly anchorCandidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly related: boolean;
  readonly resourceLabel: string;
  readonly disabled: boolean;
  readonly onSourceChange: (source: ExplorerColumnSource) => void;
  readonly onContributorChange: (
    contributor: ExplorerBuilderColumn['contributor'],
  ) => void;
}) => {
  const [draftTemporalPath, setDraftTemporalPath] = useState<string>();
  const [editingUnitNormalization, setEditingUnitNormalization] = useState(false);
  if (column.source.kind === 'field') {
    const source = column.source;
    const currentMode = source.field.projectionMode ?? 'FIRST';
    const projectionModes = candidate?.projectionModes ?? [currentMode];
    const uniqueModes = [...new Set(projectionModes)];

    return (
      <div className="col-span-full flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
        {related && source.field.relatedSelection ? (
          <label className="flex items-center gap-1.5 font-medium text-slate-700">
            <span>Across records</span>
            <select
              aria-label={`Across related ${resourceLabel} records for ${column.label}`}
              className="max-w-64 rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
              value="FIRST_BY_RESOURCE_KEY"
              disabled={disabled}
              onChange={(event) => {
                const operation = event.currentTarget.value;
                if (operation === 'FIRST_BY_RESOURCE_KEY') return;
                if (operation === 'FIRST_ORDERED') {
                  setDraftTemporalPath(source.field.path);
                  return;
                }
                onSourceChange({
                  kind: 'aggregate',
                  aggregate: {
                    operation: operation as Exclude<
                      RelatedValueReduction,
                      'FIRST_BY_RESOURCE_KEY' | 'FIRST_ORDERED'
                    >,
                    path: source.field.path,
                  },
                });
              }}
            >
              {Object.entries(relatedValueReductionLabels).map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </select>
          </label>
        ) : null}
        {uniqueModes.length > 1 ? (
          <label className="flex items-center gap-1.5 font-medium text-slate-700">
            <span>Repeated values</span>
            <select
              aria-label={`Repeated values for ${column.label}`}
              className="rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
              value={currentMode}
              disabled={disabled}
              onChange={(event) =>
                onSourceChange({
                  ...source,
                  field: {
                    ...source.field,
                    projectionMode: event.currentTarget.value as keyof typeof nestedValueLabels,
                  },
                })
              }
            >
              {uniqueModes.map((mode) => (
                <option key={mode} value={mode}>
                  {nestedValueLabels[mode]}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <span>{projectionExplanation(currentMode, resourceLabel)}</span>
        {source.field.relatedSelection ? (
          <label className="flex items-start gap-1 text-amber-800">
            <input
              type="checkbox"
              aria-label={`Allow first related value for ${column.label}`}
              checked={source.field.relatedSelection.acknowledged}
              disabled={disabled}
              onChange={(event) =>
                onSourceChange({
                  ...source,
                  field: {
                    ...source.field,
                    relatedSelection: {
                      kind: 'first-by-resource-key',
                      acknowledged: event.currentTarget.checked,
                    },
                  },
                })
              }
            />
            Keep only the first related record by resource key. Other records are omitted.
          </label>
        ) : null}
        {draftTemporalPath === source.field.path ? (
          <TemporalReductionEditor
            path={source.field.path}
            timestampCandidates={candidates.filter(
              (candidateOption) => candidateOption.logicalType.toLowerCase() === 'date_time',
            )}
            anchorCandidates={anchorCandidates}
            disabled={disabled}
            onApply={(nextSource) => {
              setDraftTemporalPath(undefined);
              onSourceChange(nextSource);
            }}
          />
        ) : null}
      </div>
    );
  }

  if (column.source.kind === 'aggregate') {
    const aggregateSource = column.source;
    const path = aggregateSource.aggregate.path;
    const operation = aggregateSource.aggregate.operation;
    const options = path && related ? relatedValueReductionLabels : path ? fieldAggregateLabels : resourceAggregateLabels;
    const editingTemporal = operation === 'FIRST_ORDERED' || draftTemporalPath === path;
    const unitNormalization = aggregateSource.aggregate.unitNormalization;
    const hasUnitEvidence = Boolean(
      candidate &&
      !candidate.repeated &&
      ['integer', 'decimal', 'number'].includes(candidate.logicalType.toLowerCase()) &&
      candidate.conceptCandidates?.some((concept) => (concept.observedUnits?.length ?? 0) > 0),
    );
    const canNormalizeUnits = Boolean(path && hasUnitEvidence && ['MIN', 'MAX', 'REQUIRE_ONE', 'COLLECT', 'DISTINCT_VALUES', 'FIRST_ORDERED'].includes(operation));
    const summary = path
      ? `${operation === 'FIRST_ORDERED' ? 'Selects one dated value' : fieldAggregateLabels[operation as FieldAggregateOperation] ?? 'Reduces values'} from ${path} across matching ${resourceLabel} resources.`
      : `${operation === 'COUNT' ? 'Counts' : 'Checks for'} matching ${resourceLabel} resources.`;

    return (
      <div className="col-span-full flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
        <label className="flex items-center gap-1.5 font-medium text-slate-700">
          <span>{path && related ? 'Across records' : 'Calculation'}</span>
          <select
            aria-label={path && related
              ? `Across related ${resourceLabel} records for ${column.label}`
              : `Calculation for ${column.label}`}
            className="rounded border border-slate-300 bg-white px-1.5 py-0.5 text-xs font-normal"
            value={operation}
            disabled={disabled || operation === 'CONTAINS_ALL'}
            onChange={(event) => {
              const nextOperation = event.currentTarget.value as
                | ResourceAggregateOperation
                | FieldAggregateOperation
                | RelatedValueReduction
                | 'FIRST_BY_RESOURCE_KEY';
              if (nextOperation === 'FIRST_BY_RESOURCE_KEY') {
                if (!path) return;
                onSourceChange({
                  kind: 'field',
                  field: {
                    path,
                    projectionMode: candidate?.defaultProjectionMode ?? 'VALUE',
                    relatedSelection: {
                      kind: 'first-by-resource-key',
                      acknowledged: false,
                    },
                  },
                });
                return;
              }
              if (nextOperation === 'FIRST_ORDERED') {
                if (path) setDraftTemporalPath(path);
                return;
              }
              setDraftTemporalPath(undefined);
              onSourceChange({
                kind: 'aggregate',
                aggregate: path
                  ? { operation: nextOperation as FieldAggregateOperation, path }
                  : { operation: nextOperation as ResourceAggregateOperation },
              });
            }}
          >
            {Object.entries(options).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
            {operation === 'CONTAINS_ALL' ? (
              <option value="CONTAINS_ALL">Contains every required value</option>
            ) : null}
          </select>
        </label>
        <span>{summary}</span>
        {canNormalizeUnits ? (
          <button
            type="button"
            className="rounded border border-violet-300 bg-white px-2 py-0.5 font-semibold text-violet-800 hover:bg-violet-50 disabled:opacity-40"
            disabled={disabled}
            onClick={() => setEditingUnitNormalization((value) => !value)}
          >
            {unitNormalization ? 'Edit unit normalization' : 'Normalize units'}
          </button>
        ) : null}
        {canNormalizeUnits && (editingUnitNormalization || unitNormalization) ? (
          <UnitNormalizationEditor
            current={unitNormalization}
            disabled={disabled}
            onApply={(nextUnitNormalization) => {
              setEditingUnitNormalization(false);
              onSourceChange({
                kind: 'aggregate',
                aggregate: { ...aggregateSource.aggregate, unitNormalization: nextUnitNormalization },
              } as ExplorerColumnSource);
            }}
          />
        ) : null}
        {editingTemporal && path ? (
          <TemporalReductionEditor
            path={path}
            current={operation === 'FIRST_ORDERED' ? aggregateSource.aggregate as TemporalAggregateSource : undefined}
            timestampCandidates={candidates.filter(
              (candidateOption) => candidateOption.logicalType.toLowerCase() === 'date_time',
            )}
            anchorCandidates={anchorCandidates}
            disabled={disabled}
            onApply={(source) => {
              setDraftTemporalPath(undefined);
              onSourceChange(source);
            }}
          />
        ) : null}
        <ContributorEditor
          current={column.contributor}
          candidates={candidates}
          featureLabel={column.label}
          resourceLabel={resourceLabel}
          disabled={disabled}
          onApply={onContributorChange}
        />
      </div>
    );
  }

  return null;
};
