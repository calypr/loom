import React, { useState } from 'react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionChoice,
  ConstructionChoiceForm,
  ConstructionChoiceSource,
  ContributorPredicate,
  ConstructionOperation,
  ConstructionProposalRequest,
  ConstructionStep,
  ExplorerBuilderCatalog,
} from '../../../types';
import { ConceptCatalog } from '../components/ConceptCatalog';
import { catalogSourceOptions, type CatalogChoiceIntent } from '../catalogItems';
import { relatedSourceOutputLabel } from '../constructionWorkspace/relatedSourceOutputLabel';
import { relationshipLabel } from '../constructionWorkspace/routeDisplay';

type RelatedSourceOperation = Extract<ConstructionOperation, { readonly kind: 'RELATED_SOURCE' }>;
type RelatedSourceForm = RelatedSourceOperation['relatedSource']['form'];
type RelatedSourceIntent = NonNullable<CatalogChoiceIntent['relatedSource']>;
type SupportedRelatedSourceIntent = Omit<RelatedSourceIntent, 'choice'> & {
  readonly choice: Omit<ConstructionChoice, 'source'> & {
    readonly source: Extract<ConstructionChoiceSource, { readonly kind: 'FIELD' }> & {
      readonly cardinality: 'optional_one' | 'required_one';
    };
  };
};
export type RelatedSourceStep = Omit<ConstructionStep, 'operation'> & {
  readonly operation: RelatedSourceOperation;
};
type CandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds'
>;
type OutputColumn = ConstructionStep['outputs'][number];

const isPhysicalColumnName = (name: string): boolean =>
  /^[A-Za-z_][A-Za-z0-9_]*$/.test(name);

const suggestedOutputName = (
  resourceType: string,
  source: RelatedSourceIntent['candidate'],
  form: RelatedSourceForm,
  columns: ConstructionCapabilitiesResponse['selectedStage']['columns'],
  outputColumnId: string,
): string => {
  const base = (form === 'COUNT'
    ? `related_${resourceType}_count`
    : form === 'PRESENCE'
      ? `has_related_${resourceType}`
      : `related_${resourceType}_${source.fieldPath}`)
    .replace(/[^A-Za-z0-9_]/g, '_')
    .replace(/_+/g, '_')
    .replace(/^([0-9])/, '_$1');
  const usedNames = new Set(
    columns
      .filter((column) => column.id !== outputColumnId)
      .map((column) => column.name.toLowerCase()),
  );
  let name = base;
  for (let suffix = 2; usedNames.has(name.toLowerCase()); suffix += 1) {
    name = `${base}_${suffix}`;
  }
  return name;
};

const relatedSourceSupport = (
  capabilities: ConstructionCapabilitiesResponse,
): { readonly supported: true } | { readonly supported: false; readonly reason: string } => {
  const support = capabilities.selectedStage.capabilities.find(
    (capability) => capability.kind === 'RELATED_SOURCE',
  );
  if (support?.supported && capabilities.selectedStage.rowIdentityColumn) {
    return { supported: true };
  }
  return {
    supported: false,
    reason: [support?.reasonCode, support?.reason]
      .filter((reason): reason is string => Boolean(reason?.trim()))
      .join(': ') || 'Loom has not proved that this stage retains a source row anchor.',
  };
};

const outputColumnFor = (
  step: RelatedSourceStep,
  operation: RelatedSourceOperation,
): OutputColumn | undefined => step.outputs.find(
  (column) => column.id === operation.relatedSource.outputColumnId,
);

const relatedChoiceIsSupported = (
  selection: RelatedSourceIntent,
  rowRoot: string,
  form: ConstructionChoiceForm,
): selection is SupportedRelatedSourceIntent => {
  const { choice, candidate } = selection;
  const source = choice.source;
  return source.kind === 'FIELD' &&
    source.candidateId === candidate.candidateId &&
    source.nodeId === candidate.nodeId &&
    source.path === candidate.fieldPath &&
    source.cardinality === candidate.cardinality &&
    (source.cardinality === 'optional_one' || source.cardinality === 'required_one') &&
    (source.resourceType !== rowRoot || choice.route.length > 0) &&
    (form === 'ALL' || form === 'COUNT' || form === 'PRESENCE') &&
    choice.options.some((option) =>
      option.form === form &&
      option.support === 'SUPPORTED',
    );
};

const replaceStepAndOutputMetadata = (
  construction: Construction,
  stepId: string,
  replacementStep: ConstructionStep,
  outputColumn: OutputColumn,
): Construction | undefined => {
  const targetIndex = construction.steps.findIndex((step) => step.id === stepId);
  if (targetIndex < 0) return undefined;
  return {
    ...construction,
    steps: construction.steps.map((step, index) => {
      if (index === targetIndex) return replacementStep;
      if (index < targetIndex) return step;
      return {
        ...step,
        outputs: step.outputs.map((column) =>
          column.id === outputColumn.id ? { ...column, ...outputColumn } : column,
        ),
      };
    }),
  };
};

export const RelatedSourceStepEditor = ({
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  catalog,
  construction,
  capabilities,
  step,
  disabled,
  onCandidateChange,
  onCancel,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly catalog: ExplorerBuilderCatalog;
  readonly construction: Construction;
  readonly capabilities: ConstructionCapabilitiesResponse;
  readonly step: RelatedSourceStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (candidate: CandidateIntent | undefined) => void;
  readonly onCancel: () => void;
}) => {
  const relatedSource = step.operation.relatedSource;
  const savedOutput = outputColumnFor(step, step.operation);
  const [outputName, setOutputName] = useState(savedOutput?.name ?? 'related_field');
  const [outputLabel, setOutputLabel] = useState(savedOutput?.label ?? 'Related field');
  const [nameEdited, setNameEdited] = useState(false);
  const [labelEdited, setLabelEdited] = useState(false);
  const [selectedSource, setSelectedSource] = useState<RelatedSourceIntent>();
  const [selectedForm, setSelectedForm] = useState<RelatedSourceForm>(relatedSource.form);
  const [selectedPredicate, setSelectedPredicate] = useState<ContributorPredicate | undefined>(relatedSource.contributorRule.predicate);
  const support = relatedSourceSupport(capabilities);
  const sourceOptions = catalogSourceOptions(catalog, rowRoot).filter(
    (option) => option.kind === 'RELATED' || option.kind === 'SAVED_OCCURRENCE',
  );
  const [sourceKey, setSourceKey] = useState(
    sourceOptions.find((option) => option.sourceNodeId === relatedSource.sourceOccurrenceId)?.key ??
      sourceOptions[0]?.key,
  );
  const activeSource = sourceOptions.find((option) => option.key === sourceKey);
  const savedPredicate = relatedSource.contributorRule.predicate;
  const initialSelection = {
    choiceId: relatedSource.choiceId,
    form: relatedSource.form,
    ...(savedPredicate ? { condition: {
      mode: savedPredicate.operator,
      value: savedPredicate.value?.kind === 'STRING'
        ? savedPredicate.value.string
        : savedPredicate.value?.kind === 'CODE'
          ? savedPredicate.value.code.code
          : '',
    } } : {}),
  };

  const candidateFor = (
    nextName: string,
    nextLabel: string,
    sourceSelection = selectedSource,
    form = selectedForm,
    predicate = selectedPredicate,
  ): CandidateIntent | undefined => {
    const normalizedName = nextName.trim();
    const normalizedLabel = nextLabel.trim();
    const stage = capabilities.selectedStage;
    const outputId = relatedSource.outputColumnId;
    if (
      !isPhysicalColumnName(normalizedName) ||
      !normalizedLabel ||
      stage.columns.some((column) =>
        column.id !== outputId && column.name.toLowerCase() === normalizedName.toLowerCase(),
      ) ||
      (sourceSelection && (!support.supported || !relatedChoiceIsSupported(sourceSelection, rowRoot, form)))
    ) return undefined;

    const sourceChanged = sourceSelection !== undefined && (
      form !== relatedSource.form ||
      JSON.stringify(predicate) !== JSON.stringify(relatedSource.contributorRule.predicate) ||
      sourceSelection.choice.choiceId !== relatedSource.choiceId ||
      sourceSelection.choice.source.kind !== 'FIELD' ||
      sourceSelection.choice.source.candidateId !== relatedSource.source.candidateId ||
      sourceSelection.choice.source.nodeId !== relatedSource.source.nodeId ||
      sourceSelection.choice.source.path !== relatedSource.source.path ||
      JSON.stringify(sourceSelection.choice.route) !== JSON.stringify(relatedSource.route)
    );
    const metadataChanged = normalizedName !== savedOutput?.name ||
      normalizedLabel !== savedOutput?.label;
    if (!sourceChanged && !metadataChanged) return undefined;

    const outputColumn: OutputColumn = {
      id: outputId,
      name: normalizedName,
      label: normalizedLabel,
      ...((sourceChanged && sourceSelection)
        ? { type: form === 'COUNT' ? 'integer' : form === 'PRESENCE' ? 'boolean' : sourceSelection.candidate.logicalType }
        : savedOutput?.type === undefined ? {} : { type: savedOutput.type }),
    };
    const selectedField = sourceSelection?.choice.source;
    const operation: RelatedSourceOperation = sourceChanged && sourceSelection && selectedField?.kind === 'FIELD'
      ? {
          kind: 'RELATED_SOURCE',
          relatedSource: {
            anchorColumnId: stage.rowIdentityColumn ?? relatedSource.anchorColumnId,
            choiceId: sourceSelection.choice.choiceId,
            sourceOccurrenceId: selectedField.nodeId,
            source: {
              kind: 'FIELD',
              candidateId: selectedField.candidateId,
              nodeId: selectedField.nodeId,
              resourceType: selectedField.resourceType,
              path: selectedField.path,
              cardinality: selectedField.cardinality,
              logicalType: sourceSelection.candidate.logicalType,
            },
            route: sourceSelection.choice.route,
            contributorRule: { policy: 'ALL_MATCHES', ...(predicate ? { predicate } : {}) },
            form,
            outputColumnId: outputId,
          },
        }
      : step.operation;
    const replacementStep: ConstructionStep = {
      ...step,
      operation,
      outputs: [
        ...stage.columns.map((column) => ({
          id: column.id,
          name: column.name,
          label: column.label,
          ...(column.type === undefined ? {} : { type: column.type }),
        })),
        outputColumn,
      ],
    };
    const candidateConstruction = replaceStepAndOutputMetadata(
      construction,
      step.id,
      replacementStep,
      outputColumn,
    );
    if (!candidateConstruction) return undefined;
    return {
      candidateConstruction,
      changedStepId: step.id,
    };
  };

  const updateOutputName = (nextName: string) => {
    setNameEdited(true);
    setOutputName(nextName);
    onCandidateChange(candidateFor(nextName, outputLabel));
  };

  const updateOutputLabel = (nextLabel: string) => {
    setLabelEdited(true);
    setOutputLabel(nextLabel);
    onCandidateChange(candidateFor(outputName, nextLabel));
  };

  const useSelectedField = async (selections: ReadonlyArray<CatalogChoiceIntent>) => {
    if (selections.length !== 1 || !selections[0]?.relatedSource) {
      throw new Error('Choose one Loom-supported related field and route.');
    }
    const selected = selections[0].relatedSource;
    const form = selections[0].constructionChoice.form;
    const predicate = selections[0].contributorPredicate;
    if ((form !== 'ALL' && form !== 'COUNT' && form !== 'PRESENCE') || !relatedChoiceIsSupported(selected, rowRoot, form)) {
      throw new Error('Loom did not return a supported scalar related field choice for this stage.');
    }
    const nextName = nameEdited
      ? outputName
      : suggestedOutputName(
          selected.choice.source.resourceType,
          selected.candidate,
          form,
          capabilities.selectedStage.columns,
          relatedSource.outputColumnId,
        );
    const nextLabel = labelEdited
      ? outputLabel
      : relatedSourceOutputLabel(
          selected.choice.source.resourceType,
          selected.candidate.fieldPath,
          selected.candidate.label,
          form,
          predicate,
        );
    setSelectedSource(selected);
    setSelectedForm(form);
    setSelectedPredicate(predicate);
    setOutputName(nextName);
    setOutputLabel(nextLabel);
    onCandidateChange(candidateFor(nextName, nextLabel, selected, form, predicate));
  };

  const nameIsValid = isPhysicalColumnName(outputName.trim()) &&
    !capabilities.selectedStage.columns.some((column) =>
      column.id !== relatedSource.outputColumnId &&
      column.name.toLowerCase() === outputName.trim().toLowerCase(),
    );
  const labelIsValid = Boolean(outputLabel.trim());

  return (
    <section
      aria-label="Related source editor"
      data-testid="related-source-step-editor"
      className="grid gap-4"
    >
      <div>
        <h3 className="font-semibold text-slate-900">Edit related field</h3>
        <p className="mt-1 text-sm text-slate-600">
          Keep the saved step in place. Loom will check a preview before you apply changes.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          Output column name
          <input
            aria-label="Output column name"
            value={outputName}
            disabled={disabled}
            onChange={(event) => updateOutputName(event.currentTarget.value)}
            className="rounded border border-slate-300 px-2 py-1.5"
          />
          {!nameIsValid ? (
            <span className="text-xs font-normal text-red-700">Use a unique name with letters, numbers, and underscores.</span>
          ) : null}
        </label>
        <label className="grid gap-1 text-sm font-medium text-slate-800">
          Output column label
          <input
            aria-label="Output column label"
            value={outputLabel}
            disabled={disabled}
            onChange={(event) => updateOutputLabel(event.currentTarget.value)}
            className="rounded border border-slate-300 px-2 py-1.5"
          />
          {!labelIsValid ? (
            <span className="text-xs font-normal text-red-700">Enter a label for this column.</span>
          ) : null}
        </label>
      </div>

      <div>
        <h4 className="font-semibold text-slate-900">Source field and route</h4>
        <p className="mt-1 text-sm text-slate-600">
          Current source: {relatedSource.source.resourceType}.{relatedSource.source.path}
        </p>
        <p className="mt-1 text-sm text-slate-600">
          Current result: {relatedSource.form === 'COUNT'
            ? 'Count matching records'
            : relatedSource.form === 'PRESENCE'
              ? 'Whether a match exists'
              : 'All matching values'}
        </p>
        <p className="mt-1 text-sm text-slate-600">
          Current route: {relatedSource.route.length > 0
            ? relatedSource.route.map((edge) => `${edge.fromResourceType} → ${edge.toResourceType} via ${relationshipLabel(edge)}`).join(' · ')
            : 'Same resource as each table row'}
        </p>
        <p className="mt-1 text-sm text-slate-600">
          Current matching rule: {relatedSource.contributorRule.predicate?.operator === 'EQUALS'
            ? `Only records where the selected field equals ${relatedSource.contributorRule.predicate.value?.kind === 'STRING'
              ? relatedSource.contributorRule.predicate.value.string
              : relatedSource.contributorRule.predicate.value?.code?.code ?? ''}`
            : relatedSource.contributorRule.predicate?.operator === 'EXISTS'
              ? 'Only records with a value in the selected field'
              : 'All related records'}
        </p>
        <label className="mt-3 grid gap-1 text-sm font-medium text-slate-800">
          Related source to inspect
          <select
            aria-label="Related source to inspect"
            data-testid="related-source-step-source"
            value={activeSource?.key ?? ''}
            disabled={disabled || !support.supported}
            onChange={(event) => {
              setSourceKey(event.currentTarget.value);
              setSelectedSource(undefined);
              onCandidateChange(undefined);
            }}
            className="rounded border border-slate-300 bg-white px-2 py-1.5"
          >
            {sourceOptions.map((option) => (
              <option key={option.key} value={option.key}>{option.label}</option>
            ))}
          </select>
        </label>
        <ConceptCatalog
          key={`${snapshotToken}:${outputId}:${step.id}:${activeSource?.key ?? ''}`}
          project={project}
          explorerId={explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={snapshotToken}
          outputId={outputId}
          rowRoot={rowRoot}
          resourceType={activeSource?.resourceType}
          sourceNodeId={activeSource?.sourceNodeId}
          layout="panel"
          catalog={catalog}
          disabled={disabled || !support.supported}
          disabledReason={support.supported ? undefined : support.reason}
          sourceProjectionAvailability={{
            available: false,
            reason: 'This saved step uses a related source, not source-projection fields.',
          }}
          relatedSourceAvailability={support}
          suppressUnavailableNotices
          initialSelection={initialSelection}
          initialFieldSource={relatedSource.source}
          onAddSelected={useSelectedField}
        />
      </div>

      <div className="flex justify-end border-t border-slate-200 pt-3">
        <button
          type="button"
          data-testid="related-source-cancel-edit"
          disabled={disabled}
          onClick={onCancel}
          className="rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
        >
          Cancel edit
        </button>
      </div>
    </section>
  );
};
