import { useRef, useState, type ReactNode } from 'react';
import { useEscapeToClose } from './hooks/useEscapeToClose';
import { createPortal } from 'react-dom';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';
import type { ImportedSourceParameters, ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../types';

type ImportedSourceStep = NonNullable<ExplorerBuilderDocument['importedSource']>['steps'][number];
type ValueMode = 'AUTO' | 'VALUE' | 'FIRST' | 'ALL' | 'DISTINCT';
const valueModeLabels: Readonly<Record<ValueMode, string>> = {
  AUTO: 'Automatic',
  VALUE: 'Single value',
  FIRST: 'First value',
  ALL: 'All values',
  DISTINCT: 'Distinct values',
};

const valueModesForStep = (step: ImportedSourceStep): ReadonlyArray<ValueMode> | undefined => {
  if (step.valueMode === undefined) return undefined;
  switch (step.kind) {
    case 'PIVOT':
      return ['VALUE', 'FIRST', 'ALL', 'DISTINCT'];
    case 'CATALOG_PROJECTION':
    case 'DYNAMIC_COLUMN':
      return ['AUTO', 'FIRST', 'ALL', 'DISTINCT'];
    default:
      return undefined;
  }
};

const isSupportedValueMode = (
  value: string | undefined,
  supportedModes: ReadonlyArray<ValueMode> | undefined,
): value is ValueMode =>
  value !== undefined && supportedModes?.some((candidate) => candidate === value) === true;

const sourceStepLabel = (step: ImportedSourceStep): string => {
  const parameters = step.parameters;
  if (parameters?.identity) return `Row identity · ${parameters.identity.name}`;
  if (parameters?.catalog) return `Catalog projection · ${parameters.catalog.name}`;
  if (parameters?.dynamic) return `Dynamic columns · ${parameters.dynamic.name}`;
  if (parameters?.extension) return `Extension columns · ${parameters.extension.name}`;
  if (parameters?.pivot) return `Pivot · ${parameters.pivot.name}`;
  if (parameters?.expansion) return `Expand items as ${parameters.expansion.as}`;
  if (parameters?.traversal) return `Related ${parameters.traversal.toResourceType.replace(/([a-z])([A-Z])/g, '$1 $2')} records · ${parameters.traversal.name}`;
  if (step.kind !== 'TRAVERSAL') return step.title;
  const target = /^Traversal to (.+?)(?: via .+)?$/.exec(step.title)?.[1];
  // The plan supplies resource names, not clinical relationship descriptions.
  // Keep unfamiliar names literal; never infer a clinical meaning from a path.
  return target ? `Related ${target.replace(/([a-z])([A-Z])/g, '$1 $2')} records` : step.title;
};

type SourceStepEntry = { readonly step: ImportedSourceStep; readonly number: number };
type SourceStepGroup = { readonly id: string; readonly columns: boolean; readonly entries: SourceStepEntry[] };

// Group only adjacent projections in the same source scope. Traversals and row
// operations remain boundaries; flattening these groups preserves recipe order.
export const groupSourceSteps = (steps: ReadonlyArray<ImportedSourceStep>): SourceStepGroup[] => {
  const groups: SourceStepGroup[] = [];
  const columnKinds = new Set(['FIELD_GROUP', 'CATALOG_PROJECTION', 'DYNAMIC_COLUMN', 'EXTENSION_COLUMN', 'PIVOT']);
  for (const [index, step] of steps.entries()) {
    const columns = columnKinds.has(step.kind);
    const previous = groups.at(-1);
    if (columns && previous?.columns && previous.entries[0]?.step.parentId === step.parentId) {
      previous.entries.push({ step, number: index + 1 });
    } else {
      groups.push({ id: step.id, columns, entries: [{ step, number: index + 1 }] });
    }
  }
  return groups;
};

interface ImportedSourceStepEditorValues {
  readonly stepId: string;
  readonly enabled: boolean;
  readonly maxColumns: string;
  readonly valueMode: string;
  readonly parameters?: ImportedSourceParameters;
  readonly parametersChanged: boolean;
  readonly selectedFieldId?: string;
  readonly fieldFocusRequest?: number;
  readonly selectedOutputKey?: string;
  readonly outputFocusRequest?: number;
}
interface ImportedSourceStepEditorState extends ImportedSourceStepEditorValues {
  readonly activeStepId?: string;
  readonly dependentEdits?: ReadonlyArray<ImportedSourceStepEditorValues>;
}

interface ImportedSourceStepsProps {
  readonly outputId: string;
  readonly importedSource: ExplorerBuilderDocument['importedSource'];
  readonly disabled?: boolean;
  readonly navigatorOnly?: boolean;
  readonly reviewOwnsCancel?: boolean;
  readonly fieldEditActionRef?: { current: ((stepId: string, fieldId: string) => void) | undefined };
  readonly outputEditActionRef?: { current: ((stepId: string, outputKey: string) => void) | undefined };
  readonly catalogOutputsForStep?: (stepId: string) => ReadonlyArray<{ readonly outputKey: string; readonly columnId?: string; readonly defaultName: string; readonly enabled: boolean }>;
  readonly pivotOutputsForStep?: (stepId: string) => ReadonlyArray<{ readonly outputKey: string; readonly columnId?: string; readonly defaultName: string; readonly enabled: boolean }>;
  readonly outputRevision?: string;
  readonly resourceTypeChoices?: ReadonlyArray<string>;
  readonly sourcePathChoices?: ReadonlyArray<string>;
  readonly fieldPathChoices?: ReadonlyArray<string>;
  readonly fieldKindChoices?: ReadonlyArray<string>;
  readonly editActionRef?: { current: ((stepId: string) => void) | undefined };
  readonly onBeginEdit?: (stepId: string) => boolean;
  readonly onActiveOperationChange?: (stepId: string) => void;
  readonly onCancelEdit?: () => void;
  readonly onDraftChange?: () => void;
  readonly editorHost?: HTMLElement | null;
  readonly reviewHost?: HTMLElement | null;
  readonly renderReview?: (sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']>, onCancel: () => void, sourceSteps?: ReadonlyArray<NonNullable<ExplorerBuilderCommand['sourceStep']>>) => ReactNode;
  readonly onRefresh: () => Promise<boolean>;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
}

const editorStateFor = (step: ImportedSourceStep): ImportedSourceStepEditorState => ({
  stepId: step.id,
  activeStepId: step.id,
  enabled: step.enabled,
  maxColumns: step.maxColumns === undefined ? '' : String(step.maxColumns),
  valueMode: step.valueMode ?? '',
  parameters: step.parameters,
  parametersChanged: false,
});

export const ImportedSourceSteps = ({
  outputId,
  importedSource,
  disabled = false,
  navigatorOnly = false,
  reviewOwnsCancel = false,
  onRefresh,
  onApply,
  renderReview,
  editActionRef, fieldEditActionRef, outputEditActionRef, catalogOutputsForStep, pivotOutputsForStep, outputRevision, onBeginEdit, onActiveOperationChange, onCancelEdit, onDraftChange, editorHost, reviewHost, resourceTypeChoices, sourcePathChoices, fieldPathChoices, fieldKindChoices,
}: ImportedSourceStepsProps) => {
  const presentEditor = (node: ReactNode) => editorHost ? createPortal(node, editorHost) : node;
  const presentReview = (node: ReactNode) => reviewHost ? createPortal(node, reviewHost) : node;
  const [editor, setEditor] = useState<ImportedSourceStepEditorState>();
  const fieldFocusRequest = useRef(0);
  const [refreshing, setRefreshing] = useState(false);
  const [applying, setApplying] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const steps = importedSource?.steps;
  const closeEditor = () => { setEditor(undefined); onCancelEdit?.(); };
  const openEditor = (stepId: string, fieldId?: string, outputKey?: string) => {
    const step = steps?.find((candidate) => candidate.id === stepId);
    if (!step || disabled) return;
    if (editor && renderReview) {
      stageOrSelectOperation(stepId, fieldId, outputKey);
      return;
    }
    if (outputKey !== undefined) {
      const outputs = step.kind === 'PIVOT' ? pivotOutputsForStep?.(stepId) : step.kind === 'CATALOG_PROJECTION' ? catalogOutputsForStep?.(stepId) : undefined;
      if (!outputs?.some((output) => output.outputKey === outputKey)) return;
    }
    const dependent = editor?.dependentEdits?.find((change) => change.stepId === stepId);
    const parameters = editor?.stepId === stepId ? editor.parameters : dependent?.parameters ?? step.parameters;
    if (fieldId && !parameters?.fieldGroup?.fields.some((field) => field.fieldId === fieldId)) return;
    if (dependent && editor) {
      onActiveOperationChange?.(stepId);
      setEditor({ ...editor, activeStepId: stepId, dependentEdits: editor.dependentEdits?.map((change) => change.stepId === stepId
        ? { ...change, ...(fieldId ? { selectedFieldId: fieldId, fieldFocusRequest: ++fieldFocusRequest.current } : {}), ...(outputKey ? { selectedOutputKey: outputKey, outputFocusRequest: ++fieldFocusRequest.current } : {}) } : change) });
      return;
    }
    if (editor?.stepId === stepId) {
      onActiveOperationChange?.(stepId);
      if (fieldId) setEditor({ ...editor, activeStepId: stepId, selectedFieldId: fieldId, fieldFocusRequest: ++fieldFocusRequest.current });
      else if (outputKey) setEditor({ ...editor, activeStepId: stepId, selectedOutputKey: outputKey, outputFocusRequest: ++fieldFocusRequest.current });
      else if (editor.activeStepId !== stepId) setEditor({ ...editor, activeStepId: stepId });
      return;
    }
    if (onBeginEdit?.(stepId) === false) return;
    onActiveOperationChange?.(stepId);
    setEditor({ ...editorStateFor(step), selectedFieldId: fieldId, fieldFocusRequest: fieldId ? ++fieldFocusRequest.current : undefined,
      selectedOutputKey: outputKey, outputFocusRequest: outputKey ? ++fieldFocusRequest.current : undefined });
  };
  const relationships = importedSource?.relationships;
  const removedOccurrences = new Set(relationships?.removals ?? []);
  const effectiveEnabled = (step: ImportedSourceStep) => step.enabled && !(step.occurrenceId && removedOccurrences.has(step.occurrenceId));
  const stepsById = new Map<string, ImportedSourceStep>();
  for (const step of steps ?? []) stepsById.set(step.id, step);
  const selectedStep = steps?.find((step) => step.id === editor?.stepId);
  const dependentEdits = editor?.dependentEdits ?? [];
  const activeStepId = editor?.activeStepId ?? editor?.stepId;
  const activeStep = activeStepId === selectedStep?.id ? selectedStep : stepsById.get(activeStepId ?? '');
  const activeDependent = activeStepId === selectedStep?.id ? undefined : dependentEdits.find((change) => change.stepId === activeStepId);
  const activeValues = activeDependent ?? editor;
  const activeParent = activeStep?.parentId ? stepsById.get(activeStep.parentId) : undefined;
  const activeValueModes = activeStep ? valueModesForStep(activeStep) : undefined;
  const selectedParent = selectedStep?.parentId ? stepsById.get(selectedStep.parentId) : undefined;
  const valueModes = selectedStep ? valueModesForStep(selectedStep) : undefined;
  const disabledAncestorFor = (step: ImportedSourceStep): ImportedSourceStep | undefined => {
    const visited = new Set<string>([step.id]);
    let parent = step.parentId ? stepsById.get(step.parentId) : undefined;
    while (parent) {
      if (visited.has(parent.id)) return parent;
      if (!effectiveEnabled(parent)) return parent;
      visited.add(parent.id);
      parent = parent.parentId ? stepsById.get(parent.parentId) : undefined;
    }
    return undefined;
  };
  const selectedDisabledAncestor = selectedStep ? disabledAncestorFor(selectedStep) : undefined;
  const changedMaxColumns = selectedStep && editor &&
    selectedStep.maxColumns !== undefined &&
    editor.maxColumns !== String(selectedStep.maxColumns);
  const parsedMaxColumns = editor && editor.maxColumns.trim() !== ''
    ? Number(editor.maxColumns)
    : undefined;
  const validMaxColumns = !changedMaxColumns || (
    parsedMaxColumns !== undefined &&
    Number.isSafeInteger(parsedMaxColumns) &&
    parsedMaxColumns > 0
  );

  const currentSourceStepChange: NonNullable<ExplorerBuilderCommand['sourceStep']> | undefined = editor && selectedStep ? {
      stepId: selectedStep.id,
      enabled: editor.enabled,
      ...(editor.parametersChanged && editor.parameters ? { parameters: editor.parameters } : {}),
      ...(changedMaxColumns && parsedMaxColumns !== undefined
        ? { maxColumns: parsedMaxColumns }
        : {}),
      ...(isSupportedValueMode(selectedStep.valueMode, valueModes) && editor.valueMode !== selectedStep.valueMode
        ? { valueMode: editor.valueMode }
        : {}),
  } : undefined;

  const activeChangedMaxColumns = activeStep && activeValues && activeStep.maxColumns !== undefined && activeValues.maxColumns !== String(activeStep.maxColumns);
  const activeParsedMaxColumns = activeValues && activeValues.maxColumns.trim() !== '' ? Number(activeValues.maxColumns) : undefined;
  const validActiveMaxColumns = !activeChangedMaxColumns || (activeParsedMaxColumns !== undefined && Number.isSafeInteger(activeParsedMaxColumns) && activeParsedMaxColumns > 0);
  const validDependentEdits = dependentEdits.every((change) => {
    const step = stepsById.get(change.stepId);
    return step && (step.maxColumns === undefined || (Number.isSafeInteger(Number(change.maxColumns)) && Number(change.maxColumns) > 0));
  });
  const dependentChanges = dependentEdits.flatMap((change): NonNullable<ExplorerBuilderCommand['sourceStep']>[] => {
    const step = stepsById.get(change.stepId);
    if (!step) return [];
    return [{ stepId: step.id, enabled: change.enabled,
      ...(change.parametersChanged && change.parameters ? { parameters: change.parameters } : {}),
      ...(step.maxColumns !== undefined && change.maxColumns !== String(step.maxColumns) ? { maxColumns: Number(change.maxColumns) } : {}),
      ...(isSupportedValueMode(step.valueMode, valueModesForStep(step)) && change.valueMode !== step.valueMode ? { valueMode: change.valueMode } : {}),
    }];
  });
  const sourceReview = selectedStep?.editable && renderReview && currentSourceStepChange && validMaxColumns && validDependentEdits && !(selectedStep.occurrenceId && removedOccurrences.has(selectedStep.occurrenceId))
    ? renderReview(currentSourceStepChange, closeEditor, dependentChanges.length ? [currentSourceStepChange, ...dependentChanges] : undefined) : null;
  const stageOrSelectOperation = (stepId: string, fieldId?: string, outputKey?: string) => {
    if (!editor || isDisabled) return;
    const step = stepsById.get(stepId);
    if (!step?.editable) return;
    const primary = step.id === editor.stepId;
    const dependent = dependentEdits.find((change) => change.stepId === step.id);
    const parameters = primary ? editor.parameters : dependent?.parameters ?? step.parameters;
    if (fieldId !== undefined && !parameters?.fieldGroup?.fields.some((field) => field.fieldId === fieldId)) return;
    if (outputKey !== undefined) {
      const outputs = step.kind === 'PIVOT' ? pivotOutputsForStep?.(stepId) : step.kind === 'CATALOG_PROJECTION' ? catalogOutputsForStep?.(stepId) : undefined;
      if (!outputs?.some((output) => output.outputKey === outputKey)) return;
    }
    setActionError(undefined);
    const fieldFocusRequestId = fieldId ? ++fieldFocusRequest.current : undefined;
    const outputFocusRequestId = outputKey ? ++fieldFocusRequest.current : undefined;
    const selectedFieldId = fieldId;
    const selectedOutputKey = outputKey;
    if (primary) {
      if (fieldId && !editor.parameters?.fieldGroup?.fields.some((field) => field.fieldId === fieldId)) return;
      onActiveOperationChange?.(step.id);
      setEditor({ ...editor, activeStepId: step.id,
        ...(fieldId ? { selectedFieldId, fieldFocusRequest: fieldFocusRequestId } : {}),
        ...(outputKey ? { selectedOutputKey, outputFocusRequest: outputFocusRequestId } : {}) });
      return;
    }
    if (dependent) {
      if (fieldId && !dependent.parameters?.fieldGroup?.fields.some((field) => field.fieldId === fieldId)) return;
      onActiveOperationChange?.(step.id);
      setEditor({ ...editor, activeStepId: step.id, dependentEdits: dependentEdits.map((change) => change.stepId === step.id && fieldId
        ? { ...change, selectedFieldId, fieldFocusRequest: fieldFocusRequestId }
        : change.stepId === step.id && outputKey ? { ...change, selectedOutputKey, outputFocusRequest: outputFocusRequestId } : change) });
      return;
    }
    if (!step.parameters) return;
    if (dependentEdits.length >= 127) {
      setActionError('A shared preview can include up to 128 source operations. Remove one from this change before adding another.');
      return;
    }
    onDraftChange?.();
    const staged = editorStateFor(step);
    onActiveOperationChange?.(step.id);
    setEditor({ ...editor, activeStepId: step.id, dependentEdits: [...dependentEdits, {
      ...staged,
      ...(fieldId ? { selectedFieldId, fieldFocusRequest: fieldFocusRequestId } : {}),
      ...(outputKey ? { selectedOutputKey, outputFocusRequest: outputFocusRequestId } : {}),
    }] });
  };
  useEscapeToClose(Boolean(editor) && !sourceReview, closeEditor);
  if (editActionRef) editActionRef.current = (stepId) => editor && renderReview
    ? stageOrSelectOperation(stepId) : openEditor(stepId);
  if (fieldEditActionRef) fieldEditActionRef.current = (stepId, fieldId) => editor && renderReview
    ? stageOrSelectOperation(stepId, fieldId) : openEditor(stepId, fieldId);
  if (outputEditActionRef) outputEditActionRef.current = (stepId, outputKey) => editor && renderReview
    ? stageOrSelectOperation(stepId, undefined, outputKey) : openEditor(stepId, undefined, outputKey);
  const updateActive = (patch: Partial<ImportedSourceStepEditorValues>) => {
    if (!editor || !activeStep) return;
    if (activeStep.id === editor.stepId) { onDraftChange?.(); setEditor({ ...editor, ...patch }); }
    else updateDependent(activeStep.id, patch);
  };
  const updateDependent = (stepId: string, patch: Partial<ImportedSourceStepEditorValues>) => {
    if (!editor) return;
    onDraftChange?.();
    setEditor({ ...editor, dependentEdits: dependentEdits.map((change) => change.stepId === stepId ? { ...change, ...patch } : change) });
  };

  const refresh = async () => {
    setRefreshing(true);
    setActionError(undefined);
    try {
      if (!await onRefresh()) {
        setActionError('Source steps did not load. Check Builder diagnostics and try again.');
      }
    } catch {
      setActionError('Source steps could not be loaded. Try again.');
    } finally {
      setRefreshing(false);
    }
  };

  const apply = async () => {
    if (dependentChanges.length) { setActionError('Preview and apply coupled edits together through the shared review.'); return; }
    if (!editor || !selectedStep || !selectedStep.editable || !validMaxColumns || !currentSourceStepChange) return;
    setApplying(true);
    setActionError(undefined);
    const sourceStep = currentSourceStepChange;
    try {
      const applied = await onApply({ type: 'UPDATE_IMPORTED_SOURCE_STEP', outputId, sourceStep });
      if (applied) {
        setEditor(undefined);
      } else {
        setActionError('The change did not apply. Check Builder diagnostics and adjust the source step.');
      }
    } catch {
      setActionError('The source step could not be updated. Try again.');
    } finally {
      setApplying(false);
    }
  };

  const isDisabled = disabled || refreshing || applying;

  return (
    <section
      aria-label="Imported source steps"
      aria-busy={refreshing || applying}
      data-testid="imported-source-steps"
      className="min-w-0"
    >
      {!navigatorOnly ? <header className="mb-1 flex items-baseline gap-2">
        <h3 className="m-0 text-xs font-medium text-slate-500">Recipe steps</h3>
        <span className="text-xs text-slate-500">{steps ? `${steps.length} steps · select to edit` : 'Imported source'}</span>
      </header> : null}
      {!navigatorOnly && relationships ? <section aria-label="Project source relationships" className="mb-2 rounded border border-blue-200 bg-blue-50 p-2 text-xs">
        <p className="font-semibold">Project source changes · original recipe pin preserved</p>
        <ul>{relationships.additions?.map((addition) => <li key={addition.occurrenceId}>
          {removedOccurrences.has(addition.occurrenceId) ? 'Removed' : 'Added'} {addition.resourceType} via {addition.relationship} · {relationships.matchOverrides?.find((override) => override.occurrenceId === addition.occurrenceId)?.matchMode ?? addition.matchMode}
          {addition.fields?.map((field) => <span key={field.columnId}> · {field.label}: {field.valueMode === 'ALL' ? 'all matching values as a list' : field.valueMode === 'FIRST' ? 'first matching value' : 'distinct matching values as a list'}</span>)}
        </li>)}</ul>
        {relationships.removals?.length ? <p>{relationships.removals.length} source relationship(s) removed. Their pinned settings are retained for inspection.</p> : null}
      </section> : null}
      {actionError ? <p className="mb-2 text-sm text-red-700" role="alert">{actionError}</p> : null}
      {navigatorOnly && (steps === undefined || steps.some((step) => !step.parameters)) ? <div className="mb-2">
        <button type="button" data-testid="imported-source-refresh" disabled={isDisabled} onClick={() => void refresh()}
          className="rounded border border-slate-300 bg-white px-3 py-1.5 text-sm text-blue-800 disabled:opacity-50">
          {refreshing ? 'Loading editable source settings…' : 'Load editable source settings'}
        </button>
      </div> : null}
      {navigatorOnly ? null : steps === undefined ? (
        <div>
          <button
            type="button"
            data-testid="imported-source-refresh"
            disabled={isDisabled}
            onClick={() => void refresh()}
            className="rounded-md border border-indigo-300 bg-white px-3 py-1.5 text-sm font-medium text-indigo-900 hover:bg-indigo-100 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {refreshing ? 'Loading source steps…' : 'Show source steps'}
          </button>
        </div>
      ) : steps.length === 0 ? (
        <p className="text-sm text-slate-600">This imported recipe has no source steps.</p>
      ) : (
        <ol className="m-0 flex min-w-0 list-none flex-wrap items-start gap-1 p-0" data-testid="imported-source-step-list">
          {groupSourceSteps(steps).map((group) => {
            const renderStep = ({ step, number }: SourceStepEntry) => {
            const disabledAncestor = disabledAncestorFor(step);
            const parent = step.parentId ? stepsById.get(step.parentId) : undefined;
            const statusLabel = step.occurrenceId && removedOccurrences.has(step.occurrenceId) ? 'Removed by project source change' : !step.enabled
              ? 'Disabled'
              : disabledAncestor
                ? 'Inactive · parent off'
                : 'Enabled';
            const content = <>
              <span aria-hidden="true" className="pt-0.5 text-xs font-semibold text-indigo-800">
                {number}.
              </span>
              <span className="min-w-0 break-words text-sm">{sourceStepLabel(step)}</span>
              <span
                title={disabledAncestor ? `Inactive because ${sourceStepLabel(disabledAncestor)} is disabled` : undefined}
                className={effectiveEnabled(step) && !disabledAncestor ? 'sr-only' : 'shrink-0 text-xs text-slate-500'}
              >
                {statusLabel}
              </span>
            </>;
            const interactive = !isDisabled && step.editable;
            return (
              <li key={step.id} data-parent-id={step.parentId} className="min-w-0 max-w-full">
                {interactive ? <button
                  type="button"
                  aria-label={`Source step ${number}: ${sourceStepLabel(step)}`}
                  aria-expanded={editor?.stepId === step.id}
                  aria-pressed={editor?.stepId === step.id}
                  data-testid={`imported-source-step-${step.id}`}
                  data-source-kind={step.kind}
                  onClick={() => {
                    setActionError(undefined);
                    if (editor?.stepId === step.id) closeEditor(); else openEditor(step.id);
                  }}
                  title={[step.title, parent ? `Under ${parent.title}` : undefined, statusLabel].filter(Boolean).join(' · ')}
                  className={`inline-flex max-w-full items-baseline gap-1.5 rounded px-2 py-1 text-left transition-colors ${
                    editor?.stepId === step.id
                      ? 'bg-blue-100 text-blue-950 ring-1 ring-blue-400'
                      : 'bg-slate-100 text-slate-700 hover:bg-blue-50'
                  }`}
                >
                  {content}
                </button> : <span data-testid={`imported-source-step-${step.id}`} data-source-kind={step.kind}
                  className="inline-flex max-w-full items-baseline gap-1.5 px-2 py-1 text-left text-slate-600 opacity-70">
                  {content}
                </span>}
              </li>
            );
            };
            if (!group.columns) return group.entries.map(renderStep);
            const first = group.entries[0];
            const last = group.entries.at(-1);
            if (!first || !last) return null;
            const parent = first.step.parentId ? stepsById.get(first.step.parentId) : undefined;
            const containsSelection = group.entries.some(({ step }) => step.id === selectedStep?.id);
            const inactiveCount = group.entries.filter(({ step }) => !effectiveEnabled(step) || disabledAncestorFor(step)).length;
            return (
              <li key={group.id} className="min-w-0 max-w-full">
                <details data-testid={`source-step-group-${group.id}`} className="min-w-0">
                  <summary className={`cursor-pointer rounded px-2 py-1 text-sm text-slate-700 hover:bg-blue-50 ${containsSelection ? 'bg-blue-100' : 'bg-slate-100'}`}>
                    <span className="font-medium">Columns</span> · {group.entries.length}
                    <span className="ml-1 text-xs text-slate-500">({first.number}–{last.number})</span>
                    {parent ? <span className="ml-1 text-xs text-slate-600">· {sourceStepLabel(parent)}</span> : null}
                    {inactiveCount ? <span className="ml-1 text-xs text-slate-500">· {inactiveCount} inactive</span> : null}
                  </summary>
                  <ol className="m-0 mt-1 flex list-none flex-wrap gap-1 border-l-2 border-blue-200 pl-2">
                    {group.entries.map(renderStep)}
                  </ol>
                </details>
              </li>
            );
          })}
        </ol>
      )}
      {editor && (!selectedStep || !activeStep || !activeValues) ? presentEditor(
        <div className="construction-inspector-frame min-w-0 p-3">
          <p role="alert" className="text-sm text-amber-900">The primary or selected source operation is no longer available. Close this editor and reload the source steps before editing.</p>
          <button type="button" disabled={isDisabled} onClick={closeEditor} className="rounded-md border border-slate-300 px-3 py-1.5 text-sm">Close source editor</button>
        </div>
      ) : selectedStep && activeStep && activeValues && editor ? presentEditor(
        <form
          aria-label={`Source step ${sourceStepLabel(activeStep)} editor`}
          data-testid="imported-source-step-editor"
          className="construction-inspector-frame min-w-0"
          onSubmit={(event) => {
            event.preventDefault();
            if (!renderReview) void apply();
          }}
        >
          <div className="construction-inspector-body space-y-2 px-3 py-2">
          <section aria-label={`Operation settings for ${sourceStepLabel(activeStep)}`} className="space-y-1.5">
            <div className="flex items-start justify-between gap-2">
              <h3 className="m-0 text-[13px] font-semibold tracking-tight text-slate-900">{sourceStepLabel({ ...activeStep, parameters: activeValues.parameters })}</h3>
              <button type="button" aria-label="Back from source step editor" disabled={isDisabled} onClick={closeEditor}
                className="inline-flex shrink-0 items-center gap-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50">
                <span aria-hidden="true">←</span> Back
              </button>
            </div>
            {activeParent ? <p className="m-0 text-xs text-indigo-800">Under {sourceStepLabel(activeParent)}</p> : null}
            {disabledAncestorFor(activeStep) ? <p role="status" className="text-xs text-slate-600">
              Inactive because {sourceStepLabel(disabledAncestorFor(activeStep)!)} is disabled. This step’s settings are retained and do not run until the parent is enabled.
            </p> : null}
            {activeStep.editable && activeValues.parameters ? <SourceStepParameterEditor key={activeStep.id} selectedFieldId={activeValues.selectedFieldId} fieldFocusRequest={activeValues.fieldFocusRequest}
              selectedOutputKey={activeValues.selectedOutputKey} outputFocusRequest={activeValues.outputFocusRequest}
              catalogOutputs={catalogOutputsForStep?.(activeStep.id) ?? []} pivotOutputs={pivotOutputsForStep?.(activeStep.id) ?? []}
              outputRevision={outputRevision}
              parameters={activeValues.parameters} disabled={isDisabled} resourceTypeChoices={resourceTypeChoices} sourcePathChoices={sourcePathChoices} fieldPathChoices={fieldPathChoices} fieldKindChoices={fieldKindChoices} onChange={(parameters) => updateActive({ parameters, parametersChanged: true })} /> : null}
            {activeStep.editable ? <div className="flex flex-wrap items-end gap-x-4 gap-y-2">
              {activeStep.maxColumns !== undefined ? <label className="grid gap-0.5 text-xs text-slate-700">Maximum columns
                <input type="number" min={1} step={1} value={activeValues.maxColumns} disabled={isDisabled} aria-invalid={!validActiveMaxColumns}
                  data-testid="imported-source-max-columns" onChange={(event) => updateActive({ maxColumns: event.currentTarget.value })} className="rounded-md border border-slate-300 px-2 py-1 text-xs" />
              </label> : null}
              {isSupportedValueMode(activeStep.valueMode, activeValueModes) && activeValueModes ? <label className="grid gap-0.5 text-xs text-slate-700">Value mode
                <select value={activeValues.valueMode} disabled={isDisabled} data-testid="imported-source-value-mode"
                  onChange={(event) => updateActive({ valueMode: event.currentTarget.value })} className="rounded-md border border-slate-300 bg-white px-2 py-1 text-xs">
                  {activeValueModes.map((mode) => <option key={mode} value={mode}>{valueModeLabels[mode]}</option>)}
                </select>
              </label> : null}
            </div> : <p className="text-sm text-slate-600">This source step is read-only.</p>}
            {activeStep.kind === 'CATALOG_PROJECTION' && activeParent?.kind === 'TRAVERSAL' && (activeValues.valueMode === 'FIRST' || activeValues.valueMode === 'ALL') ?
              <p className="text-xs text-slate-600">Related records are ordered by their original record key. Items inside each record keep their array order.</p> : null}
            {activeDependent ? <button type="button" className="w-fit rounded-md px-1.5 py-0.5 text-[11px] font-medium text-blue-700 hover:bg-blue-50 hover:text-blue-900"
              aria-label={`Remove ${sourceStepLabel(activeStep)} from this change`} disabled={isDisabled}
              onClick={() => {
                onDraftChange?.();
                setEditor({ ...editor, activeStepId: editor.stepId, dependentEdits: dependentEdits.filter((change) => change.stepId !== activeStep.id) });
                onActiveOperationChange?.(editor.stepId);
              }}>Remove from change</button> : null}
            {activeStep.id === selectedStep?.id && !validDependentEdits ? <p role="alert" className="text-xs text-red-800">Each staged maximum must be a positive whole number.</p> : null}
          </section>
          </div>
          {sourceReview ? presentReview(<div className="construction-inspector-review">{sourceReview}</div>) : null}
          {!reviewOwnsCancel || !sourceReview ? <div className="construction-inspector-footer flex justify-end gap-2">
            <button
              type="button"
              disabled={isDisabled}
              onClick={closeEditor}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Close
            </button>
            {activeStep.editable && !renderReview ? (
              <button
                type="submit"
                data-testid="imported-source-step-apply"
                disabled={isDisabled || !validActiveMaxColumns || !!(activeStep.occurrenceId && removedOccurrences.has(activeStep.occurrenceId))}
                className="rounded-md bg-indigo-700 px-3 py-1.5 text-sm font-semibold text-white hover:bg-indigo-800 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {applying ? 'Applying…' : 'Apply source step'}
              </button>
            ) : null}
          </div> : null}
        </form>
      ) : null}
    </section>
  );
};
