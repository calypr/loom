import { useState } from 'react';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../types';

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

interface ImportedSourceStepEditorState {
  readonly stepId: string;
  readonly enabled: boolean;
  readonly maxColumns: string;
  readonly valueMode: string;
}

interface ImportedSourceStepsProps {
  readonly outputId: string;
  readonly importedSource: ExplorerBuilderDocument['importedSource'];
  readonly disabled?: boolean;
  readonly onRefresh: () => Promise<boolean>;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
}

const editorStateFor = (step: ImportedSourceStep): ImportedSourceStepEditorState => ({
  stepId: step.id,
  enabled: step.enabled,
  maxColumns: step.maxColumns === undefined ? '' : String(step.maxColumns),
  valueMode: step.valueMode ?? '',
});

export const ImportedSourceSteps = ({
  outputId,
  importedSource,
  disabled = false,
  onRefresh,
  onApply,
}: ImportedSourceStepsProps) => {
  const [editor, setEditor] = useState<ImportedSourceStepEditorState>();
  const [refreshing, setRefreshing] = useState(false);
  const [applying, setApplying] = useState(false);
  const [actionError, setActionError] = useState<string>();
  const steps = importedSource?.steps;
  const stepsById = new Map<string, ImportedSourceStep>();
  for (const step of steps ?? []) stepsById.set(step.id, step);
  const selectedStep = steps?.find((step) => step.id === editor?.stepId);
  const selectedParent = selectedStep?.parentId ? stepsById.get(selectedStep.parentId) : undefined;
  const valueModes = selectedStep ? valueModesForStep(selectedStep) : undefined;
  const disabledAncestorFor = (step: ImportedSourceStep): ImportedSourceStep | undefined => {
    const visited = new Set<string>([step.id]);
    let parent = step.parentId ? stepsById.get(step.parentId) : undefined;
    while (parent) {
      if (visited.has(parent.id)) return parent;
      if (!parent.enabled) return parent;
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
    if (!editor || !selectedStep || !selectedStep.editable || !validMaxColumns) return;
    setApplying(true);
    setActionError(undefined);
    const sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']> = {
      stepId: selectedStep.id,
      enabled: editor.enabled,
      ...(changedMaxColumns && parsedMaxColumns !== undefined
        ? { maxColumns: parsedMaxColumns }
        : {}),
      ...(isSupportedValueMode(selectedStep.valueMode, valueModes) && editor.valueMode !== selectedStep.valueMode
        ? { valueMode: editor.valueMode }
        : {}),
    };
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
      <header className="mb-1 flex items-baseline gap-2">
        <h3 className="m-0 text-xs font-medium text-slate-500">Recipe steps</h3>
        <span className="text-xs text-slate-500">{steps ? `${steps.length} steps · select to edit` : 'Imported source'}</span>
      </header>
      {actionError ? <p className="mb-2 text-sm text-red-700" role="alert">{actionError}</p> : null}
      {steps === undefined ? (
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
            const statusLabel = !step.enabled
              ? 'Disabled'
              : disabledAncestor
                ? 'Inactive · parent off'
                : 'Enabled';
            return (
              <li key={step.id} data-parent-id={step.parentId} className="min-w-0 max-w-full">
                <button
                  type="button"
                  aria-label={`Source step ${number}: ${sourceStepLabel(step)}`}
                  aria-expanded={editor?.stepId === step.id}
                  aria-pressed={editor?.stepId === step.id}
                  data-testid={`imported-source-step-${step.id}`}
                  data-source-kind={step.kind}
                  disabled={isDisabled}
                  onClick={() => {
                    setActionError(undefined);
                    setEditor(editor?.stepId === step.id ? undefined : editorStateFor(step));
                  }}
                  title={[step.title, parent ? `Under ${parent.title}` : undefined, statusLabel].filter(Boolean).join(' · ')}
                  className={`inline-flex max-w-full items-baseline gap-1.5 rounded px-2 py-1 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                    editor?.stepId === step.id
                      ? 'bg-blue-100 text-blue-950 ring-1 ring-blue-400'
                      : 'bg-slate-100 text-slate-700 hover:bg-blue-50'
                  }`}
                >
                  <span aria-hidden="true" className="pt-0.5 text-xs font-semibold text-indigo-800">
                    {number}.
                  </span>
                  <span className="min-w-0 break-words text-sm">{sourceStepLabel(step)}</span>
                  <span
                    title={disabledAncestor ? `Inactive because ${sourceStepLabel(disabledAncestor)} is disabled` : undefined}
                    className={step.enabled && !disabledAncestor ? 'sr-only' : 'shrink-0 text-xs text-slate-500'}
                  >
                    {statusLabel}
                  </span>
                </button>
              </li>
            );
            };
            if (!group.columns) return group.entries.map(renderStep);
            const first = group.entries[0];
            const last = group.entries.at(-1);
            if (!first || !last) return null;
            const parent = first.step.parentId ? stepsById.get(first.step.parentId) : undefined;
            const containsSelection = group.entries.some(({ step }) => step.id === selectedStep?.id);
            const inactiveCount = group.entries.filter(({ step }) => !step.enabled || disabledAncestorFor(step)).length;
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
      {selectedStep && editor ? (
        <form
          aria-label={`Source step ${sourceStepLabel(selectedStep)} editor`}
          data-testid="imported-source-step-editor"
          className="mt-2 border-t border-slate-200 pt-2"
          onSubmit={(event) => {
            event.preventDefault();
            void apply();
          }}
        >
          <h3 className="m-0 text-sm font-semibold text-slate-900">{sourceStepLabel(selectedStep)}</h3>
          {selectedParent ? <p className="m-0 text-xs text-indigo-800">Under {sourceStepLabel(selectedParent)}</p> : null}
          <p className="m-0 mt-1 text-xs text-slate-600">{selectedStep.kind === 'TRAVERSAL'
            ? selectedStep.summary === 'Optional relationship with nested source steps.'
              ? 'Use fields from these related records. A match is optional.'
              : selectedStep.summary === 'Required relationship with nested source steps.'
                ? 'Use fields from these related records. A match is required.'
                : selectedStep.summary
            : selectedStep.summary}</p>
          {selectedStep.kind === 'TRAVERSAL' ? <details className="mt-1 text-xs text-slate-500">
            <summary className="cursor-pointer">Relationship details</summary>
            <p className="m-0 break-words">{selectedStep.title}</p>
            <p className="m-0">{selectedStep.summary}</p>
          </details> : null}
          {selectedDisabledAncestor ? (
            <p role="status" className="mt-1 text-xs text-slate-600">
              Inactive because {sourceStepLabel(selectedDisabledAncestor)} is disabled. This step’s settings are retained and do not run until the parent is enabled.
            </p>
          ) : null}
          {selectedStep.editable ? (
            <div className="mt-2 flex flex-wrap items-end gap-x-4 gap-y-2">
              <label className="flex items-center gap-2 text-sm text-slate-800">
                <input
                  type="checkbox"
                  checked={editor.enabled}
                  disabled={isDisabled}
                  onChange={(event) => setEditor({ ...editor, enabled: event.currentTarget.checked })}
                />
                Enable this source step
              </label>
              {selectedStep.maxColumns !== undefined ? (
                <label className="grid gap-1 text-sm text-slate-800">
                  Maximum columns
                  <input
                    type="number"
                    min={1}
                    step={1}
                    value={editor.maxColumns}
                    disabled={isDisabled}
                    aria-invalid={!validMaxColumns}
                    data-testid="imported-source-max-columns"
                    onChange={(event) => setEditor({ ...editor, maxColumns: event.currentTarget.value })}
                    className="rounded border border-slate-300 px-2 py-1.5"
                  />
                </label>
              ) : null}
              {isSupportedValueMode(selectedStep.valueMode, valueModes) && valueModes ? (
                <label className="grid gap-1 text-sm text-slate-800">
                  Value mode
                  <select
                    value={editor.valueMode}
                    disabled={isDisabled}
                    data-testid="imported-source-value-mode"
                    onChange={(event) => setEditor({ ...editor, valueMode: event.currentTarget.value })}
                    className="rounded border border-slate-300 bg-white px-2 py-1.5"
                  >
                    {valueModes.map((mode) => <option key={mode} value={mode}>{valueModeLabels[mode]}</option>)}
                  </select>
                </label>
              ) : null}
            </div>
          ) : (
            <p className="mt-2 text-sm text-slate-600">This source step is read-only.</p>
          )}
          <div className="mt-2 flex justify-end gap-2">
            <button
              type="button"
              disabled={isDisabled}
              onClick={() => setEditor(undefined)}
              className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Close
            </button>
            {selectedStep.editable ? (
              <button
                type="submit"
                data-testid="imported-source-step-apply"
                disabled={isDisabled || !validMaxColumns}
                className="rounded-md bg-indigo-700 px-3 py-1.5 text-sm font-semibold text-white hover:bg-indigo-800 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {applying ? 'Applying…' : 'Apply source step'}
              </button>
            ) : null}
          </div>
        </form>
      ) : null}
    </section>
  );
};
