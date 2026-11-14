import { useState } from 'react';
import type { ExplorerBuilderCandidate, ExplorerBuilderColumn } from '../../../types';
import { rowValuePolicyLabel } from './rowRecipeProjection';

type FieldSource = Extract<ExplorerBuilderColumn['source'], { kind: 'field' }>;
type ProjectionMode = NonNullable<FieldSource['field']['projectionMode']>;

export interface RecipeColumnPolicyInspectorProps {
  /** Key this inspector by shared owner + column identity; draft form state is local to that edit. */
  readonly column: ExplorerBuilderColumn;
  readonly sourceNodeId: string;
  readonly candidate?: ExplorerBuilderCandidate;
  readonly disabled?: boolean;
  readonly busy?: boolean;
  readonly cancelDisabled?: boolean;
  readonly canApply?: boolean;
  readonly onDraftChange?: () => void;
  readonly error?: string;
  readonly onChange: (column: ExplorerBuilderColumn) => void;
  readonly onCancel: () => void;
  /** Supply only when a typed, receipt-backed preview flow exists. No preview is fabricated locally. */
  readonly onPreview?: (column: ExplorerBuilderColumn) => void;
}

/** Metadata-bounded ordinary field policy edit; the whole original column is preserved. */
export function RecipeColumnPolicyInspector(props: RecipeColumnPolicyInspectorProps) {
  const { column, candidate, sourceNodeId, disabled = false, busy = false } = props;
  const source = column.source;
  const currentMode = source.kind === 'field' ? source.field.projectionMode : undefined;
  const [selectedMode, setSelectedMode] = useState<ProjectionMode | undefined>(currentMode);
  const metadataMatches = source.kind === 'field' && candidate?.nodeId === sourceNodeId && candidate.fieldPath === source.field.path;
  const modes = metadataMatches ? candidate.projectionModes : [];
  const selectedAllowed = selectedMode !== undefined && modes.includes(selectedMode);
  const changed = selectedMode !== currentMode;
  const unavailable = source.kind === 'imported'
    ? 'Pinned source values must be edited through their exact project-local source step.'
    : source.kind !== 'field'
      ? 'This source policy uses a specialized inspector. Its existing configuration is preserved.'
      : !metadataMatches
        ? 'Load the field catalog for this exact source occurrence before changing its value policy.'
        : modes.length < 2
          ? 'The current catalog supports no alternative value policy for this field.'
          : undefined;
  const proposedColumn = (): ExplorerBuilderColumn | undefined => source.kind === 'field' && selectedAllowed
    ? { ...column, source: { ...source, field: { ...source.field, projectionMode: selectedMode } } }
    : undefined;
  return <section aria-label={`Column value policy: ${column.label}`} data-testid="recipe-column-policy-inspector"
    data-column-id={column.columnId ?? column.column} className="space-y-2 rounded border bg-white p-2 text-xs">
    <h3 className="font-semibold">Include {column.label}</h3>
    <p>Current policy: {currentMode ? rowValuePolicyLabel(currentMode) : 'configured source policy'}. Row identity is preserved.</p>
    {source.kind === 'field' && source.field.relatedSelection ? <p>Across related records: keep only the first record by resource key. This existing selection is {source.field.relatedSelection.acknowledged ? 'acknowledged' : 'awaiting acknowledgement'} and remains unchanged by the value policy.</p> : null}
    {unavailable ? <p role="status">{unavailable}</p> : null}
    {source.kind === 'field' && metadataMatches ? <label className="flex flex-wrap items-center gap-2">
      <span>Value policy</span>
      <select aria-label={`Value policy for ${column.label}`} value={selectedMode ?? ''} disabled={disabled || busy || !!unavailable}
        onChange={(event) => { props.onDraftChange?.(); setSelectedMode(event.target.value as ProjectionMode); }}>
        {!currentMode ? <option value="">Keep the current default</option> : !modes.includes(currentMode) ? <option value={currentMode} disabled>{rowValuePolicyLabel(currentMode)} (saved; currently unavailable)</option> : null}
        {modes.map((mode) => <option key={mode} value={mode}>{rowValuePolicyLabel(mode)}</option>)}
      </select>
    </label> : null}
    {selectedMode === 'ALL' || selectedMode === 'DISTINCT' ? <p>Values remain a list. Related-record expansion repeats this list on each input/record pair. A per-match scalar is a separate column choice.</p> : null}
    {selectedMode === 'FIRST' ? <p>First value omits additional values. It does not mean one distinct value.</p> : null}
    {props.error ? <p role="alert">{props.error}</p> : null}
    <div className="flex gap-2">
      <button type="button" className="rounded border px-2 py-1" disabled={disabled || busy || !!unavailable || !selectedAllowed || !changed}
        onClick={() => { const proposed = proposedColumn(); if (proposed) (props.onPreview ?? props.onChange)(proposed); }}>
        {busy ? 'Working…' : props.onPreview ? 'Preview column policy' : 'Save column policy'}
      </button>
      {props.onPreview ? <button type="button" className="rounded border bg-blue-700 px-2 py-1 text-white" disabled={disabled || busy || !props.canApply}
        onClick={() => { const proposed = proposedColumn(); if (proposed) props.onChange(proposed); }}>Apply column policy</button> : null}
      <button type="button" className="rounded border px-2 py-1" disabled={props.cancelDisabled} onClick={props.onCancel}>Cancel column edit</button>
    </div>
    {!props.onPreview ? <p>This ordinary source edit saves directly through the existing column command. The result preview refreshes after saving.</p> : null}
  </section>;
}
