import React, { useEffect, useRef, useState } from 'react';
import type {
  AggregateOrdering,
  AggregateOperationCapability,
  ContributorWindow,
  TemporalFieldChoice,
  TemporalReductionCapability,
  UnitNormalizationCapability,
  UnitNormalizationPresetCapability,
  ColumnTransformationChange,
  ExplorerBuilderCandidate,
  ExplorerBuilderColumn,
  ExplorerColumnSource,
} from '../../../types';

type ExactCategoryRecode = Extract<
  NonNullable<ExplorerBuilderColumn['valueTransformation']>,
  { readonly kind: 'EXACT_CATEGORY_RECODE' }
>['exactCategoryRecode'];

type DraftCategoryMapping = ExactCategoryRecode['mappings'][number] & {
  readonly draftId: number;
};

const ExactCategoryRecodeEditor = ({
  column,
  candidate,
  disabled,
  onChange,
}: {
  readonly column: ExplorerBuilderColumn;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly disabled: boolean;
  readonly onChange: (change: ColumnTransformationChange) => void;
}) => {
  const current = column.valueTransformation?.kind === 'EXACT_CATEGORY_RECODE'
    ? column.valueTransformation.exactCategoryRecode
    : undefined;
  const capability = column.source.kind === 'codedValue'
    ? candidate?.valueTransformations?.codedValueRecoding
    : candidate?.valueTransformations?.exactCategoryRecode;
  const nextDraftId = useRef(0);
  const toDraftMappings = (values: ExactCategoryRecode['mappings'] = []) =>
    values.map((mapping) => ({ ...mapping, draftId: nextDraftId.current++ }));
  const [mappings, setMappings] = useState<ReadonlyArray<DraftCategoryMapping>>(
    () => toDraftMappings(current?.mappings),
  );
  const [unknownPolicy, setUnknownPolicy] = useState<ExactCategoryRecode['unknownPolicy']>(
    () => current?.unknownPolicy ?? 'ERROR',
  );

  useEffect(() => {
    setMappings(toDraftMappings(current?.mappings));
    setUnknownPolicy(current?.unknownPolicy ?? 'ERROR');
  }, [column.column, current]);

  if (!capability?.available) {
    return (
      <div role="status" className="basis-full text-[11px] text-amber-800">
        <span>{capability?.reason ?? 'Category recoding is unavailable until the server resolves this column capability.'}</span>
        {current ? (
          <button
            type="button"
            className="ml-2 underline disabled:opacity-40"
            disabled={disabled}
            onClick={() => onChange({ kind: 'REMOVE' })}
          >
            Remove saved recoding
          </button>
        ) : null}
      </div>
    );
  }

  return (
    <details className="basis-full text-[11px] text-slate-700">
      <summary className="cursor-pointer font-medium">
        {current ? 'Edit exact category recoding' : 'Recode exact category values'}
      </summary>
      <div className="mt-1 space-y-1 rounded border border-slate-200 bg-white p-2">
        <div className="max-h-24 space-y-1 overflow-y-auto">
          {mappings.map((mapping, index) => (
            <div key={mapping.draftId} className="flex items-center gap-1">
              <input
                aria-label={`Recorded category ${index + 1} for ${column.label}`}
                className="min-w-0 flex-1 rounded border border-slate-300 px-1.5 py-1"
                value={mapping.from}
                disabled={disabled}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setMappings((previous) => previous.map((item, itemIndex) =>
                    itemIndex === index ? { ...item, from: value } : item,
                  ));
                }}
              />
              <span aria-hidden="true">→</span>
              <input
                aria-label={`Replacement value ${index + 1} for ${column.label}`}
                className="min-w-0 flex-1 rounded border border-slate-300 px-1.5 py-1"
                value={mapping.to}
                disabled={disabled}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setMappings((previous) => previous.map((item, itemIndex) =>
                    itemIndex === index ? { ...item, to: value } : item,
                  ));
                }}
              />
              <button
                type="button"
                aria-label={`Remove category mapping ${index + 1} for ${column.label}`}
                className="rounded px-1 text-slate-500 hover:bg-slate-100 disabled:opacity-40"
                disabled={disabled || mappings.length < 2}
                onClick={() => setMappings((previous) => previous.filter((_, itemIndex) => itemIndex !== index))}
              >
                ×
              </button>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            className="rounded border border-slate-300 px-2 py-1 font-medium hover:bg-slate-50 disabled:opacity-40"
            disabled={disabled}
            onClick={() => setMappings((previous) => [...previous, { draftId: nextDraftId.current++, from: '', to: '' }])}
          >
            Add mapping
          </button>
          <label className="flex items-center gap-1">
            <span>Unmapped values</span>
            <select
              aria-label={`Unmapped value policy for ${column.label}`}
              className="rounded border border-slate-300 bg-white px-1 py-1"
              value={unknownPolicy}
              disabled={disabled}
              onChange={(event) => setUnknownPolicy(
                event.currentTarget.value === 'KEEP_ORIGINAL' ? 'KEEP_ORIGINAL' : 'ERROR',
              )}
            >
              <option value="ERROR">Report an error</option>
              <option value="KEEP_ORIGINAL">Keep original value</option>
            </select>
          </label>
          <button
            type="button"
            className="rounded bg-blue-700 px-2 py-1 font-semibold text-white hover:bg-blue-800 disabled:opacity-40"
            disabled={disabled || mappings.length === 0}
            onClick={() => onChange({
              kind: 'SET',
              transformation: {
                kind: 'EXACT_CATEGORY_RECODE',
                exactCategoryRecode: { mappings: mappings.map(({ from, to }) => ({ from, to })), unknownPolicy },
              },
            })}
          >
            Save recoding
          </button>
          {current ? (
            <button
              type="button"
              className="rounded border border-red-300 px-2 py-1 font-medium text-red-700 hover:bg-red-50 disabled:opacity-40"
              disabled={disabled}
              onClick={() => onChange({ kind: 'REMOVE' })}
            >
              Remove recoding
            </button>
          ) : null}
        </div>
      </div>
    </details>
  );
};

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
type UnitNormalizationDraft = NonNullable<
  Extract<AggregateSource['aggregate'], { unitNormalization?: unknown }>['unitNormalization']
>;
type WindowableAggregate = Extract<
  AggregateSource['aggregate'],
  { operation: 'COUNT' | 'EXISTS' | 'MIN' | 'MAX' | 'SUM' | 'MEAN' }
>;
type WindowEditorOperation = WindowableAggregate['operation'] | 'FIRST_ORDERED';
type WindowEditorValue = {
  readonly contributorWindow: ContributorWindow;
  readonly ordering?: AggregateOrdering;
};

const unitNormalizationOperations = new Set<AggregateOperation>([
  'MIN',
  'MAX',
  'SUM',
  'MEAN',
  'DISTINCT_VALUES',
  'REQUIRE_ONE',
  'COLLECT',
  'FIRST_ORDERED',
]);

const contributorWindowOperations = new Set<AggregateOperation>([
  'COUNT',
  'EXISTS',
  'MIN',
  'MAX',
  'SUM',
  'MEAN',
]);

const isWindowEditorOperation = (
  operation: AggregateOperation,
): operation is WindowEditorOperation =>
  operation === 'FIRST_ORDERED' || contributorWindowOperations.has(operation);

const aggregateForOperation = (
  current: AggregateSource['aggregate'],
  operation: AggregateOperation,
): AggregateSource['aggregate'] | undefined => {
  const path = current.path;
  const contributorWindow = 'contributorWindow' in current
    ? current.contributorWindow
    : undefined;
  const unitNormalization = 'unitNormalization' in current
    ? current.unitNormalization
    : undefined;

  switch (operation) {
    case 'COUNT':
    case 'EXISTS':
      return {
        operation,
        ...(path === undefined ? {} : { path }),
        ...(contributorWindow ? { contributorWindow } : {}),
      };
    case 'MIN':
    case 'MAX':
    case 'SUM':
    case 'MEAN':
      if (path === undefined) return undefined;
      return {
        operation,
        path,
        ...(contributorWindow ? { contributorWindow } : {}),
        ...(unitNormalization ? { unitNormalization } : {}),
      };
    case 'COUNT_DISTINCT':
      return path === undefined ? undefined : { operation, path };
    case 'DISTINCT_VALUES':
    case 'REQUIRE_ONE':
    case 'COLLECT':
      if (path === undefined) return undefined;
      return {
        operation,
        path,
        ...(unitNormalization ? { unitNormalization } : {}),
      };
    case 'CONTAINS_ALL':
      return current.operation === 'CONTAINS_ALL' ? current : undefined;
    case 'FIRST_ORDERED':
      return undefined;
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

const aggregateWithWindow = (
  operation: WindowEditorOperation,
  path: string,
  current: AggregateSource['aggregate'] | undefined,
  value: WindowEditorValue,
): AggregateSource['aggregate'] | undefined => {
  const unitNormalization = current && 'unitNormalization' in current
    ? current.unitNormalization
    : undefined;

  switch (operation) {
    case 'COUNT':
    case 'EXISTS':
      return { operation, path, contributorWindow: value.contributorWindow };
    case 'MIN':
    case 'MAX':
    case 'SUM':
    case 'MEAN':
      return {
        operation,
        path,
        contributorWindow: value.contributorWindow,
        ...(unitNormalization ? { unitNormalization } : {}),
      };
    case 'FIRST_ORDERED':
      if (!value.ordering) return undefined;
      return {
        operation,
        path,
        contributorWindow: value.contributorWindow,
        ordering: value.ordering,
        ...(unitNormalization ? { unitNormalization } : {}),
      };
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

const aggregateWithUnitNormalization = (
  aggregate: AggregateSource['aggregate'],
  unitNormalization: UnitNormalizationDraft | undefined,
): AggregateSource['aggregate'] => {
  switch (aggregate.operation) {
    case 'MIN':
    case 'MAX':
    case 'SUM':
    case 'MEAN':
    case 'DISTINCT_VALUES':
    case 'REQUIRE_ONE':
    case 'COLLECT':
    case 'FIRST_ORDERED':
      return { ...aggregate, unitNormalization };
    case 'COUNT':
    case 'EXISTS':
    case 'COUNT_DISTINCT':
    case 'CONTAINS_ALL':
      return aggregate;
    default: {
      const exhaustive: never = aggregate;
      return exhaustive;
    }
  }
};

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

const ContributorWindowEditor = ({
  path,
  operation,
  current,
  capability,
  disabled,
  onApply,
  onCancel,
}: {
  readonly path: string;
  readonly operation: WindowEditorOperation;
  readonly current?: AggregateSource['aggregate'];
  readonly capability: TemporalReductionCapability;
  readonly disabled: boolean;
  readonly onApply: (value: WindowEditorValue) => void;
  readonly onCancel: () => void;
}) => {
  const contributorWindow = current && 'contributorWindow' in current
    ? current.contributorWindow
    : undefined;
  const ordering = current?.operation === 'FIRST_ORDERED'
    ? current.ordering
    : undefined;
  const timestampFields: ReadonlyArray<TemporalFieldChoice> = capability.timestampFields;
  const anchorFields: ReadonlyArray<TemporalFieldChoice> = capability.anchorFields;
  const [timestampPath, setTimestampPath] = useState(
    contributorWindow?.timestampPath ?? timestampFields[0]?.fieldPath ?? '',
  );
  const [anchorPath, setAnchorPath] = useState(
    contributorWindow?.anchorPath ?? anchorFields[0]?.fieldPath ?? '',
  );
  const [lookbackDays, setLookbackDays] = useState(
    Math.max(0, Math.round(-(contributorWindow?.lowerOffsetSeconds ?? -31_536_000) / 86_400)),
  );
  const [lowerInclusive, setLowerInclusive] = useState(contributorWindow?.lowerInclusive ?? true);
  const [upperInclusive, setUpperInclusive] = useState(contributorWindow?.upperInclusive ?? true);
  const [direction, setDirection] = useState<'ASC' | 'DESC'>(
    ordering?.direction ?? 'DESC',
  );
  const [tiePolicy, setTiePolicy] = useState<'REQUIRE_UNIQUE' | 'RESOURCE_KEY'>(
    ordering?.tiePolicy ?? 'REQUIRE_UNIQUE',
  );
  const timestampReady = timestampFields.some((field) => field.fieldPath === timestampPath);
  const anchorReady = anchorFields.some((field) => field.fieldPath === anchorPath);
  const ready = capability.available && timestampReady && anchorReady &&
    Number.isSafeInteger(lookbackDays) && lookbackDays >= 0;
  const isFirstOrdered = operation === 'FIRST_ORDERED';

  return (
    <fieldset className="mt-1 grid w-full grid-cols-2 gap-2 rounded border border-blue-200 bg-blue-50/60 p-2 text-[11px] text-slate-700">
      <legend className="px-1 font-semibold text-blue-900">
        {isFirstOrdered ? 'Date selection' : 'Contributor date window'}
      </legend>
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
      {isFirstOrdered ? (
        <label className="flex flex-col gap-0.5 font-medium">
          <span>Choose</span>
          <select
            aria-label="Date selection direction"
            className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
            value={direction}
            disabled={disabled}
            onChange={(event) => {
              const value = event.currentTarget.value;
              if (value === 'ASC' || value === 'DESC') setDirection(value);
            }}
          >
            <option value="DESC">Latest value</option>
            <option value="ASC">Earliest value</option>
          </select>
        </label>
      ) : null}
      <label className="flex flex-col gap-0.5 font-medium">
        <span>Look back days</span>
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
      <label className="flex items-center gap-1.5 font-medium">
        <input
          type="checkbox"
          checked={lowerInclusive}
          disabled={disabled}
          onChange={(event) => setLowerInclusive(event.currentTarget.checked)}
        />
        <span>Include start boundary</span>
      </label>
      <label className="flex items-center gap-1.5 font-medium">
        <input
          type="checkbox"
          checked={upperInclusive}
          disabled={disabled}
          onChange={(event) => setUpperInclusive(event.currentTarget.checked)}
        />
        <span>Include end boundary</span>
      </label>
      {isFirstOrdered ? (
        <label className="col-span-2 flex items-center gap-1.5 font-medium">
          <span>If dates tie</span>
          <select
            aria-label="Equal date handling"
            className="rounded border border-slate-300 bg-white px-1.5 py-1 font-normal"
            value={tiePolicy}
            disabled={disabled}
            onChange={(event) => {
              const value = event.currentTarget.value;
              if (value === 'REQUIRE_UNIQUE' || value === 'RESOURCE_KEY') setTiePolicy(value);
            }}
          >
            <option value="REQUIRE_UNIQUE">Stop and ask me to resolve it</option>
            <option value="RESOURCE_KEY">Choose deterministically by resource key</option>
          </select>
        </label>
      ) : null}
      <p className="col-span-2 text-slate-600">
        {isFirstOrdered ? `${direction === 'DESC' ? 'Latest' : 'Earliest'} ` : ''}{path} dated from {lookbackDays} days before the row date to the row date; start is {lowerInclusive ? 'inclusive' : 'exclusive'} and end is {upperInclusive ? 'inclusive' : 'exclusive'}.
      </p>
      {!capability.available ? (
        <p role="status" className="col-span-2 text-amber-800">
          Unavailable: {capability.reason ?? 'No temporal choices are available.'}
        </p>
      ) : null}
      {capability.available && (!timestampReady || !anchorReady) ? (
        <p role="status" className="col-span-2 text-amber-800">
          {capability.reason ?? 'The server did not provide a complete set of temporal choices.'}
        </p>
      ) : null}
      <div className="col-span-2 flex gap-2">
        <button
          type="button"
          className="rounded bg-blue-700 px-2.5 py-1 font-semibold text-white hover:bg-blue-800 disabled:opacity-40"
          disabled={disabled || !ready}
          onClick={() => {
            const windowValue: ContributorWindow = {
              timestampPath,
              anchorPath,
              lowerOffsetSeconds: -lookbackDays * 86_400,
              upperOffsetSeconds: 0,
              lowerInclusive,
              upperInclusive,
              precision: 'INSTANT',
            };
            onApply(isFirstOrdered
              ? {
                contributorWindow: windowValue,
                ordering: { timestampPath, direction, tiePolicy },
              }
              : { contributorWindow: windowValue });
          }}
        >
          {isFirstOrdered ? 'Apply date selection' : 'Apply date window'}
        </button>
        <button
          type="button"
          className="rounded border border-slate-300 bg-white px-2.5 py-1 font-semibold text-slate-700 hover:bg-slate-50"
          onClick={onCancel}
        >
          Cancel date window
        </button>
      </div>
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
  onTransformationChange,
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
  readonly onTransformationChange: (change: ColumnTransformationChange) => void;
  readonly onContributorChange: (
    contributor: ExplorerBuilderColumn['contributor'],
  ) => void;
}) => {
  const [draftWindowOperation, setDraftWindowOperation] = useState<WindowEditorOperation>();
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
      <div className="col-span-full space-y-2">
        <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-values">
          <legend className="px-1 text-xs font-semibold text-slate-800">Values</legend>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
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
                      setDraftWindowOperation('FIRST_ORDERED');
                      return;
                    }
                    if (optionIsDisabled(option)) return;
                    setDraftWindowOperation(undefined);
                    const aggregate = aggregateForOperation(
                      { operation: 'COUNT', path: source.field.path },
                      option.operation,
                    );
                    if (aggregate) onSourceChange({ kind: 'aggregate', aggregate });
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
            <ExactCategoryRecodeEditor
              column={column}
              candidate={candidate}
              disabled={disabled}
              onChange={onTransformationChange}
            />
          </div>
        </fieldset>
        <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-time-units">
          <legend className="px-1 text-xs font-semibold text-slate-800">Time and units</legend>
          {draftWindowOperation === 'FIRST_ORDERED' && candidate ? (
            <ContributorWindowEditor
              path={source.field.path}
              operation="FIRST_ORDERED"
              current={undefined}
              capability={candidate.transformations.temporalReduction}
              disabled={disabled}
              onApply={(value) => {
                const aggregate = aggregateWithWindow(
                  'FIRST_ORDERED',
                  source.field.path,
                  undefined,
                  value,
                );
                if (!aggregate) return;
                setDraftWindowOperation(undefined);
                onSourceChange({ kind: 'aggregate', aggregate });
              }}
              onCancel={() => setDraftWindowOperation(undefined)}
            />
          ) : null}
          {draftWindowOperation === 'FIRST_ORDERED' && !candidate ? (
            <p role="status" className="text-amber-800">
              Temporal choices are unavailable until the server resolves this candidate.
            </p>
          ) : null}
          {draftWindowOperation === undefined ? (
            <p className="text-[11px] text-slate-600">
              Choose a date-aware value selection to configure its time window here.
            </p>
          ) : null}
        </fieldset>
      </div>
    );
  }

  if (column.source.kind === 'aggregate') {
    const aggregateSource = column.source;
    const currentAggregate = aggregateSource.aggregate;
    const path = currentAggregate.path;
    const operation = draftWindowOperation ?? currentAggregate.operation;
    const options = aggregateOptions(candidate, rowContext, path, related);
    const operationCapability = selectedOperationCapability(options, operation);
    const temporalCapability = candidate?.transformations.temporalReduction;
    const unitCapability = candidate?.transformations.unitNormalization;
    const editingWindow = draftWindowOperation !== undefined;
    const canConfigureWindow = operation === 'FIRST_ORDERED' ||
      (related && contributorWindowOperations.has(operation));
    const unitNormalization = 'unitNormalization' in currentAggregate
      ? currentAggregate.unitNormalization
      : undefined;
    const contributorWindow = 'contributorWindow' in currentAggregate
      ? currentAggregate.contributorWindow
      : undefined;
    const summary = operationCapability
      ? undefined
      : path
        ? `${operation === 'FIRST_ORDERED' ? 'Selects one dated value' : 'Reduces values'} from ${path} across matching ${resourceLabel} resources.`
        : `${operation === 'COUNT' ? 'Counts' : 'Checks for'} matching ${resourceLabel} resources.`;

    return (
      <div className="col-span-full space-y-2">
        <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-values">
          <legend className="px-1 text-xs font-semibold text-slate-800">Values</legend>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
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
                  if (!selectedOption || optionIsDisabled(selectedOption, currentAggregate)) return;
                  if (selectedOption.kind === 'relatedSelection') {
                    if (!path) return;
                    setDraftWindowOperation(undefined);
                    setEditingUnitNormalization(false);
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
                    if (path) setDraftWindowOperation('FIRST_ORDERED');
                    return;
                  }
                  setDraftWindowOperation(undefined);
                  setEditingUnitNormalization(false);
                  const aggregate = aggregateForOperation(currentAggregate, nextOperation);
                  if (aggregate) onSourceChange({ kind: 'aggregate', aggregate });
                }}
              >
                {options.map((option) => (
                  <option
                    key={option.operation}
                    value={option.operation}
                    disabled={optionIsDisabled(option, currentAggregate)}
                  >
                    {optionLabel(option, currentAggregate)}
                  </option>
                ))}
              </select>
            </label>
            {operationCapability ? capabilityDetails(operationCapability) : summary ? <span>{summary}</span> : null}
            <ContributorEditor
              current={column.contributor}
              candidates={candidates}
              featureLabel={column.label}
              resourceLabel={resourceLabel}
              disabled={disabled}
              onApply={onContributorChange}
            />
            <ExactCategoryRecodeEditor
              column={column}
              candidate={candidate}
              disabled={disabled}
              onChange={onTransformationChange}
            />
          </div>
        </fieldset>
        <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-time-units">
          <legend className="px-1 text-xs font-semibold text-slate-800">Time and units</legend>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-600">
            {unitCapability && unitNormalizationOperations.has(operation) ? (
              <button
                type="button"
                className="rounded border border-violet-300 bg-white px-2 py-0.5 font-semibold text-violet-800 hover:bg-violet-50 disabled:opacity-40"
                disabled={disabled}
                onClick={() => setEditingUnitNormalization((value) => !value)}
              >
                {unitNormalization ? 'Edit unit normalization' : 'Normalize units'}
              </button>
            ) : null}
            {unitCapability && unitNormalizationOperations.has(operation) &&
            (editingUnitNormalization || unitNormalization) ? (
              <UnitNormalizationEditor
                capability={unitCapability}
                current={unitNormalization}
                disabled={disabled}
                onApply={(nextUnitNormalization) => {
                  setEditingUnitNormalization(false);
                  onSourceChange({
                    kind: 'aggregate',
                    aggregate: aggregateWithUnitNormalization(
                      currentAggregate,
                      nextUnitNormalization,
                    ),
                  });
                }}
              />
            ) : null}
            {canConfigureWindow && path && temporalCapability && !editingWindow &&
            isWindowEditorOperation(operation) ? (
              <button
                type="button"
                className="rounded border border-blue-300 bg-white px-2 py-0.5 font-semibold text-blue-800 hover:bg-blue-50 disabled:opacity-40"
                disabled={disabled || !temporalCapability.available}
                onClick={() => setDraftWindowOperation(operation)}
              >
                {contributorWindow ? 'Edit date window' : 'Add date window'}
              </button>
            ) : null}
            {editingWindow && path && temporalCapability &&
            isWindowEditorOperation(operation) ? (
              <ContributorWindowEditor
                path={path}
                operation={operation}
                current={currentAggregate}
                capability={temporalCapability}
                disabled={disabled}
                onApply={(value) => {
                  const aggregate = aggregateWithWindow(
                    operation,
                    path,
                    currentAggregate,
                    value,
                  );
                  if (!aggregate) return;
                  setDraftWindowOperation(undefined);
                  onSourceChange({ kind: 'aggregate', aggregate });
                }}
                onCancel={() => setDraftWindowOperation(undefined)}
              />
            ) : null}
            {canConfigureWindow && path && !temporalCapability ? (
              <p role="status" className="text-amber-800">
                Temporal choices are unavailable until the server resolves this candidate.
              </p>
            ) : null}
            {canConfigureWindow && path && temporalCapability && !temporalCapability.available ? (
              <p role="status" className="text-amber-800">
                Unavailable: {temporalCapability.reason ?? 'No temporal choices are available.'}
              </p>
            ) : null}
            {!canConfigureWindow && !unitNormalizationOperations.has(operation) ? (
              <p className="text-[11px] text-slate-600">No time or unit choice is available for this value selection.</p>
            ) : null}
          </div>
        </fieldset>
      </div>
    );
  }

  return (
    <div className="col-span-full space-y-2">
      <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-values">
        <legend className="px-1 text-xs font-semibold text-slate-800">Values</legend>
        <ExactCategoryRecodeEditor
          column={column}
          candidate={candidate}
          disabled={disabled}
          onChange={onTransformationChange}
        />
      </fieldset>
      <fieldset className="grid gap-1 rounded-md border border-slate-200 p-2" data-testid="feature-policy-time-units">
        <legend className="px-1 text-xs font-semibold text-slate-800">Time and units</legend>
        <p className="text-[11px] text-slate-600">No time or unit choice is available for this value source.</p>
      </fieldset>
    </div>
  );
};
