import React, { useEffect, useState } from 'react';
import type {
  AggregateOperationCapability,
  TemporalFieldChoice,
  TemporalReductionCapability,
  UnitNormalizationCapability,
  UnitNormalizationPresetCapability,
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

type TemporalAggregateSource = Extract<
  ExplorerColumnSource,
  { kind: 'aggregate' }
>['aggregate'] & { operation: 'FIRST_ORDERED' };
type AggregateSource = Extract<ExplorerColumnSource, { kind: 'aggregate' }>;
type AggregateOperation = AggregateSource['aggregate']['operation'];
type AggregateMenuOption =
  | {
      readonly kind: 'capability';
      readonly operation: AggregateOperation;
      readonly label: string;
      readonly capability: AggregateOperationCapability;
    }
  | {
      readonly kind: 'legacy';
      readonly operation: AggregateOperation;
      readonly label: string;
    }
  | {
      readonly kind: 'relatedSelection';
      readonly operation: 'FIRST_BY_RESOURCE_KEY';
      readonly label: string;
    };
type UnitNormalizationDraft = NonNullable<AggregateSource['aggregate']['unitNormalization']>;

const aggregateOperationLabels = {
  COUNT: 'Count values or records',
  COUNT_DISTINCT: 'Count unique values',
  DISTINCT_VALUES: 'Collect unique values',
  EXISTS: 'Check whether a value exists',
  MIN: 'Minimum value',
  MAX: 'Maximum value',
  SUM: 'Sum numeric values',
  MEAN: 'Average numeric values',
  CONTAINS_ALL: 'Contains every required value',
  REQUIRE_ONE: 'Require zero or one value',
  COLLECT: 'Collect every value',
  FIRST_ORDERED: 'Value nearest a date',
} satisfies Record<AggregateOperationCapability['operation'], string>;

const legacyResourceAggregateOptions = [
  { kind: 'legacy', operation: 'COUNT', label: 'Count matching resources' },
  { kind: 'legacy', operation: 'EXISTS', label: 'Whether any resource matches' },
] satisfies ReadonlyArray<AggregateMenuOption>;

const legacyFieldAggregateOptions = [
  { kind: 'legacy', operation: 'COUNT', label: 'Count values' },
  { kind: 'legacy', operation: 'COUNT_DISTINCT', label: 'Count unique values' },
  { kind: 'legacy', operation: 'DISTINCT_VALUES', label: 'Collect unique values' },
  { kind: 'legacy', operation: 'MIN', label: 'Minimum value' },
  { kind: 'legacy', operation: 'MAX', label: 'Maximum value' },
  { kind: 'legacy', operation: 'EXISTS', label: 'Whether any value exists' },
] satisfies ReadonlyArray<AggregateMenuOption>;

const legacyRelatedAggregateOptions = [
  { kind: 'legacy', operation: 'REQUIRE_ONE', label: 'Require zero or one value' },
  { kind: 'legacy', operation: 'COLLECT', label: 'Collect every value' },
  { kind: 'legacy', operation: 'COUNT', label: 'Count values' },
  { kind: 'legacy', operation: 'DISTINCT_VALUES', label: 'Collect unique values' },
  { kind: 'legacy', operation: 'COUNT_DISTINCT', label: 'Count unique values' },
  { kind: 'legacy', operation: 'MIN', label: 'Minimum value' },
  { kind: 'legacy', operation: 'MAX', label: 'Maximum value' },
  { kind: 'legacy', operation: 'EXISTS', label: 'Whether any value exists' },
  { kind: 'legacy', operation: 'FIRST_ORDERED', label: 'Value nearest a date' },
] satisfies ReadonlyArray<AggregateMenuOption>;

const firstByResourceKeyOption: AggregateMenuOption = {
  kind: 'relatedSelection',
  operation: 'FIRST_BY_RESOURCE_KEY',
  label: 'First record by stable resource key',
};

const capabilityMenuOption = (
  capability: AggregateOperationCapability,
): AggregateMenuOption => ({
  kind: 'capability',
  operation: capability.operation,
  label: aggregateOperationLabels[capability.operation],
  capability,
});

const legacyAggregateOptions = (
  path: string | undefined,
  related: boolean,
): ReadonlyArray<AggregateMenuOption> => {
  if (!path) return legacyResourceAggregateOptions;
  if (related) return [firstByResourceKeyOption, ...legacyRelatedAggregateOptions];
  return legacyFieldAggregateOptions;
};

const aggregateOptions = (
  candidate: ExplorerBuilderCandidate | undefined,
  rowContext: AggregateOperationCapability['rowContext'] | undefined,
  path: string | undefined,
  related: boolean,
): ReadonlyArray<AggregateMenuOption> => {
  if (!path || !candidate?.aggregateOperations) {
    return legacyAggregateOptions(path, related);
  }
  const operations = rowContext
    ? candidate.aggregateOperations.filter((capability) => capability.rowContext === rowContext)
    : [];
  return [
    ...(related ? [firstByResourceKeyOption] : []),
    ...operations.map(capabilityMenuOption),
  ];
};

const optionRequiresValues = (option: AggregateMenuOption): boolean =>
  option.kind === 'capability' &&
  option.capability.requiresConfiguration?.includes('requiredValues') === true;

const optionIsDisabled = (
  option: AggregateMenuOption,
  selectedAggregate?: AggregateSource['aggregate'],
): boolean => {
  if (option.kind !== 'capability') return false;
  if (!option.capability.supported) return true;
  if (!optionRequiresValues(option)) return false;
  const requiredValues = selectedAggregate && 'requiredValues' in selectedAggregate
    ? selectedAggregate.requiredValues
    : undefined;
  return !(
    selectedAggregate?.operation === option.operation &&
    (requiredValues?.length ?? 0) > 0
  );
};

const optionLabel = (
  option: AggregateMenuOption,
  selectedAggregate?: AggregateSource['aggregate'],
): string => {
  if (option.kind !== 'capability') return option.label;
  if (!option.capability.supported) {
    return `${option.label} — unavailable: ${option.capability.reason ?? 'Not supported for this input.'}`;
  }
  if (optionRequiresValues(option) && !optionIsDisabled(option, selectedAggregate)) {
    return option.label;
  }
  if (optionRequiresValues(option)) return `${option.label} — required values needed`;
  return option.label;
};

const selectedOperationCapability = (
  options: ReadonlyArray<AggregateMenuOption>,
  operation: AggregateOperation,
): AggregateOperationCapability | undefined => {
  const selected = options.find((option) => option.operation === operation);
  return selected?.kind === 'capability' ? selected.capability : undefined;
};

const capabilityDetails = (
  capability: AggregateOperationCapability,
): React.ReactNode => (
  <div className="grid gap-0.5 text-slate-600">
    <p>Result shape: {resultShape(capability)}</p>
    {capability.missingValueSemantics ? (
      <p>Missing values: {sentence(capability.missingValueSemantics)}</p>
    ) : null}
    {capability.contributorSemantics ? (
      <p>What counts: {sentence(capability.contributorSemantics)}</p>
    ) : null}
    {!capability.supported && capability.reason ? (
      <p role="status" className="text-amber-800">Unavailable: {capability.reason}</p>
    ) : null}
    {capability.requiresConfiguration?.length ? (
      <p>
        Configuration needed: {capability.requiresConfiguration.map((value) =>
          value === 'temporal' ? 'date selection' : value === 'requiredValues' ? 'required values' : value,
        ).join(', ')}.
      </p>
    ) : null}
  </div>
);

const resultTypeLabels: Readonly<Record<string, string>> = {
  boolean: 'yes-or-no value',
  code: 'coded value',
  date: 'date',
  date_time: 'date and time',
  decimal: 'decimal number',
  integer: 'whole number',
  string: 'text value',
};

const resultShape = (capability: AggregateOperationCapability): string => {
  if (!capability.resultLogicalType || !capability.resultCardinality) {
    return 'The result shape is not available.';
  }
  const valueType =
    resultTypeLabels[capability.resultLogicalType.toLowerCase()] ?? 'value';
  switch (capability.resultCardinality) {
    case 'ONE':
      return `One ${valueType}`;
    case 'OPTIONAL_ONE':
      return `Zero or one ${valueType}`;
    case 'MANY':
      return `A list of ${valueType}s`;
  }
};

const sentence = (value: string): string =>
  value.length > 0 ? `${value[0]?.toUpperCase()}${value.slice(1)}.` : value;

const unitPresetKey = (preset: UnitNormalizationPresetCapability): string =>
  JSON.stringify([preset.policyId, preset.version]);

const UnitNormalizationEditor = ({
  capability,
  current,
  disabled,
  onApply,
}: {
  readonly capability: UnitNormalizationCapability;
  readonly current?: UnitNormalizationDraft;
  readonly disabled: boolean;
  readonly onApply: (value: UnitNormalizationDraft | undefined) => void;
}) => {
  const selectedPreset = capability.presets.find(
    (preset) => preset.policyId === current?.policyId && preset.version === current?.version,
  ) ?? capability.presets.find((preset) => preset.available) ?? capability.presets[0];
  const [selectedKey, setSelectedKey] = useState(
    selectedPreset ? unitPresetKey(selectedPreset) : '',
  );
  const selected = capability.presets.find((preset) => unitPresetKey(preset) === selectedKey);

  return (
    <fieldset className="mt-1 grid w-full gap-2 rounded border border-violet-200 bg-violet-50/60 p-2 text-[11px] text-slate-700">
      <legend className="px-1 font-semibold text-violet-900">Normalize measurement units</legend>
      <label className="flex min-w-0 flex-col gap-0.5 font-medium">
        <span>Conversion</span>
        <select
          aria-label="Unit conversion preset"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={selectedKey}
          disabled={disabled}
          onChange={(event) => setSelectedKey(event.currentTarget.value)}
        >
          {capability.presets.map((preset) => (
            <option
              key={unitPresetKey(preset)}
              value={unitPresetKey(preset)}
              disabled={!preset.available}
            >
              Convert to {preset.target.code} · {preset.policyId} v{preset.version}
              {!preset.available ? ` — unavailable: ${preset.reason ?? capability.reason ?? 'Not available.'}` : ''}
            </option>
          ))}
        </select>
      </label>
      {!capability.available && capability.reason ? (
        <p role="status" className="text-amber-800">Unavailable: {capability.reason}</p>
      ) : null}
      <div className="flex gap-2">
        <button
          type="button"
          className="rounded bg-violet-700 px-2.5 py-1 font-semibold text-white hover:bg-violet-800 disabled:opacity-40"
          disabled={disabled || !selected?.available}
          onClick={() => selected && onApply({ policyId: selected.policyId, version: selected.version })}
        >
          Apply normalization
        </button>
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
  capability,
  disabled,
  onApply,
}: {
  readonly path: string;
  readonly current?: TemporalAggregateSource;
  readonly capability: TemporalReductionCapability;
  readonly disabled: boolean;
  readonly onApply: (source: ExplorerColumnSource) => void;
}) => {
  const temporal = current?.temporal;
  const timestampFields: ReadonlyArray<TemporalFieldChoice> = capability.timestampFields;
  const anchorFields: ReadonlyArray<TemporalFieldChoice> = capability.anchorFields;
  const [timestampPath, setTimestampPath] = useState(
    temporal?.timestampPath ?? timestampFields[0]?.fieldPath ?? '',
  );
  const [anchorPath, setAnchorPath] = useState(
    temporal?.anchorPath ?? anchorFields[0]?.fieldPath ?? '',
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
  const ready = Boolean(
    capability.available && timestampPath && anchorPath && Number.isInteger(lookbackDays),
  );

  return (
    <fieldset className="mt-1 grid w-full grid-cols-2 gap-2 rounded border border-blue-200 bg-blue-50/60 p-2 text-[11px] text-slate-700">
      <legend className="px-1 font-semibold text-blue-900">Date-aware value selection</legend>
      <label className="flex min-w-0 flex-col gap-0.5 font-medium">
        <span>Record date</span>
        <select
          aria-label="Record date"
          className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
          value={timestampPath}
          disabled={disabled || !capability.available}
          onChange={(event) => setTimestampPath(event.currentTarget.value)}
        >
          <option value="">Choose a date field</option>
          {timestampFields.map((field) => (
            <option key={field.candidateId} value={field.fieldPath}>
              {field.label} · {field.resourceType} · {field.fieldPath}
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
          disabled={disabled || !capability.available}
          onChange={(event) => setAnchorPath(event.currentTarget.value)}
        >
          <option value="">Choose a row date</option>
          {anchorFields.map((field) => (
            <option key={field.candidateId} value={field.fieldPath}>
              {field.label} · {field.resourceType} · {field.fieldPath}
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
      {!capability.available ? (
        <p role="status" className="col-span-2 text-amber-800">
          Unavailable: {capability.reason ?? 'No temporal choices are available.'}
        </p>
      ) : null}
      {capability.available && (timestampFields.length === 0 || anchorFields.length === 0) ? (
        <p role="status" className="col-span-2 text-amber-800">
          {capability.reason ?? 'The server did not provide a complete set of temporal choices.'}
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
  related,
  rowContext,
  resourceLabel,
  disabled,
  onSourceChange,
  onContributorChange,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
  readonly related: boolean;
  readonly rowContext?: AggregateOperationCapability['rowContext'];
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
    const relatedOptions = source.field.relatedSelection
      ? aggregateOptions(candidate, rowContext, source.field.path, true)
      : [];

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
                const option = relatedOptions.find(
                  (candidateOption) => candidateOption.operation === event.currentTarget.value,
                );
                if (!option || option.kind === 'relatedSelection') return;
                if (option.operation === 'FIRST_ORDERED') {
                  setDraftTemporalPath(source.field.path);
                  return;
                }
                if (optionIsDisabled(option)) return;
                onSourceChange({
                  kind: 'aggregate',
                  aggregate: {
                    operation: option.operation,
                    path: source.field.path,
                  },
                });
              }}
            >
              {relatedOptions.map((option) => (
                <option
                  key={option.operation}
                  value={option.operation}
                  disabled={optionIsDisabled(option)}
                >
                  {optionLabel(option)}
                </option>
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
        {draftTemporalPath === source.field.path && candidate ? (
          <TemporalReductionEditor
            path={source.field.path}
            capability={candidate.transformations.temporalReduction}
            disabled={disabled}
            onApply={(nextSource) => {
              setDraftTemporalPath(undefined);
              onSourceChange(nextSource);
            }}
          />
        ) : null}
        {draftTemporalPath === source.field.path && !candidate ? (
          <p role="status" className="text-amber-800">
            Temporal choices are unavailable until the server resolves this candidate.
          </p>
        ) : null}
      </div>
    );
  }

  if (column.source.kind === 'aggregate') {
    const aggregateSource = column.source;
    const path = aggregateSource.aggregate.path;
    const operation = aggregateSource.aggregate.operation;
    const options = aggregateOptions(candidate, rowContext, path, related);
    const operationCapability = selectedOperationCapability(options, operation);
    const temporalCapability = candidate?.transformations.temporalReduction;
    const unitCapability = candidate?.transformations.unitNormalization;
    const editingTemporal = operation === 'FIRST_ORDERED' || draftTemporalPath === path;
    const unitNormalization = aggregateSource.aggregate.unitNormalization;
    const summary = operationCapability
      ? undefined
      : path
        ? `${operation === 'FIRST_ORDERED' ? 'Selects one dated value' : 'Reduces values'} from ${path} across matching ${resourceLabel} resources.`
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
            disabled={disabled}
            onChange={(event) => {
              const selectedOption = options.find(
                (option) => option.operation === event.currentTarget.value,
              );
              if (!selectedOption || optionIsDisabled(selectedOption, aggregateSource.aggregate)) return;
              if (selectedOption.kind === 'relatedSelection') {
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
              const nextOperation = selectedOption.operation;
              if (nextOperation === 'FIRST_ORDERED') {
                if (path) setDraftTemporalPath(path);
                return;
              }
              setDraftTemporalPath(undefined);
              onSourceChange({
                kind: 'aggregate',
                aggregate: path
                  ? { operation: nextOperation, path }
                  : { operation: nextOperation },
              });
            }}
          >
            {options.map((option) => (
              <option
                key={option.operation}
                value={option.operation}
                disabled={optionIsDisabled(option, aggregateSource.aggregate)}
              >
                {optionLabel(option, aggregateSource.aggregate)}
              </option>
            ))}
          </select>
        </label>
        {operationCapability ? capabilityDetails(operationCapability) : summary ? <span>{summary}</span> : null}
        {unitCapability ? (
          <button
            type="button"
            className="rounded border border-violet-300 bg-white px-2 py-0.5 font-semibold text-violet-800 hover:bg-violet-50 disabled:opacity-40"
            disabled={disabled}
            onClick={() => setEditingUnitNormalization((value) => !value)}
          >
            {unitNormalization ? 'Edit unit normalization' : 'Normalize units'}
          </button>
        ) : null}
        {unitCapability && (editingUnitNormalization || unitNormalization) ? (
          <UnitNormalizationEditor
            capability={unitCapability}
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
        {editingTemporal && path && temporalCapability ? (
          <TemporalReductionEditor
            path={path}
            current={operation === 'FIRST_ORDERED' ? aggregateSource.aggregate as TemporalAggregateSource : undefined}
            capability={temporalCapability}
            disabled={disabled}
            onApply={(source) => {
              setDraftTemporalPath(undefined);
              onSourceChange(source);
            }}
          />
        ) : null}
        {editingTemporal && path && !temporalCapability ? (
          <p role="status" className="text-amber-800">
            Temporal choices are unavailable until the server resolves this candidate.
          </p>
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
