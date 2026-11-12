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
      className="rounded-xl border border-indigo-200 bg-indigo-50/50 p-3"
    >
      <header className="mb-2">
        <h2 className="text-sm font-semibold text-slate-950">Imported source</h2>
        <p className="mt-0.5 text-xs text-slate-600">
          These recipe steps run before the table’s construction steps.
        </p>
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
        <ol className="m-0 flex min-w-0 list-none items-stretch gap-1.5 overflow-x-auto p-0 pb-1" data-testid="imported-source-step-list">
          {steps.map((step, index) => {
            const disabledAncestor = disabledAncestorFor(step);
            const parent = step.parentId ? stepsById.get(step.parentId) : undefined;
            const statusLabel = !step.enabled
              ? 'Disabled'
              : disabledAncestor
                ? 'Inactive · parent off'
                : 'Enabled';
            const currentSettings = [
              ...(step.maxColumns === undefined ? [] : [`Max columns: ${step.maxColumns}`]),
              ...(step.valueMode === undefined ? [] : [
                `Value mode: ${isSupportedValueMode(step.valueMode, valueModesForStep(step))
                  ? valueModeLabels[step.valueMode]
                  : step.valueMode}`,
              ]),
            ];
            return (
              <li key={step.id} data-parent-id={step.parentId} className="w-64 shrink-0">
                <button
                  type="button"
                  aria-label={`Source step ${index + 1}: ${step.title}`}
                  aria-expanded={editor?.stepId === step.id}
                  aria-pressed={editor?.stepId === step.id}
                  data-testid={`imported-source-step-${step.id}`}
                  data-source-kind={step.kind}
                  disabled={isDisabled}
                  onClick={() => {
                    setActionError(undefined);
                    setEditor(editor?.stepId === step.id ? undefined : editorStateFor(step));
                  }}
                  className={`grid h-full w-full grid-cols-[auto_1fr_auto] items-start gap-x-2 rounded-md border px-2.5 py-2 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                    editor?.stepId === step.id
                      ? 'border-indigo-400 bg-white'
                      : 'border-slate-200 bg-white hover:border-indigo-300'
                  }`}
                >
                  <span aria-hidden="true" className="pt-0.5 text-xs font-semibold text-indigo-800">
                    {index + 1}.
                  </span>
                  <span className="min-w-0 break-words">
                    <span className="block text-sm font-medium text-slate-900">{step.title}</span>
                    {parent ? (
                      <span className="mt-0.5 block text-xs text-indigo-800" data-source-parent-label={parent.title}>
                        Under {parent.title}
                      </span>
                    ) : null}
                    <span className="mt-0.5 block text-xs text-slate-600">{step.summary}</span>
                    {currentSettings.length > 0 ? (
                      <span className="mt-0.5 block text-xs font-medium text-slate-700" data-testid={`imported-source-step-settings-${step.id}`}>
                        {currentSettings.join(' · ')}
                      </span>
                    ) : null}
                  </span>
                  <span
                    title={disabledAncestor ? `Inactive because ${disabledAncestor.title} is disabled` : undefined}
                    className={`rounded px-1.5 py-0.5 text-xs ${step.enabled && !disabledAncestor ? 'bg-emerald-100 text-emerald-900' : 'bg-slate-100 text-slate-600'}`}
                  >
                    {statusLabel}
                  </span>
                </button>
              </li>
            );
          })}
        </ol>
      )}
      {selectedStep && editor ? (
        <form
          aria-label={`Source step ${selectedStep.title} editor`}
          data-testid="imported-source-step-editor"
          className="mt-3 rounded-lg border border-indigo-200 bg-white p-3"
          onSubmit={(event) => {
            event.preventDefault();
            void apply();
          }}
        >
          <h3 className="text-sm font-semibold text-slate-900">{selectedStep.title}</h3>
          {selectedDisabledAncestor ? (
            <p role="status" className="mt-1 text-xs text-slate-600">
              Inactive because {selectedDisabledAncestor.title} is disabled. This step’s settings are retained and do not run until the parent is enabled.
            </p>
          ) : null}
          {selectedStep.editable ? (
            <div className="mt-2 grid gap-3 sm:grid-cols-2">
              <label className="flex items-center gap-2 text-sm text-slate-800 sm:col-span-2">
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
          <div className="mt-3 flex justify-end gap-2">
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
