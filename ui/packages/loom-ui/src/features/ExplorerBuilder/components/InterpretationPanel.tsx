import React, { useEffect, useMemo, useState } from 'react';
import type { PreviewInterpretationCandidateArgs } from '../../../api';
import { useLoomClient } from '../../../react';
import type {
  ConfiguredColumnContextResponse,
  InterpretationPreviewResponse,
  InterpretationRevisionSummary,
} from '../../../interpretation';
import type {
  ExplorerBuilderColumn,
  ExplorerBuilderCommand,
} from '../../../types';

export type InterpretationContextState =
  | { readonly status: 'loading' }
  | { readonly status: 'error'; readonly message: string }
  | { readonly status: 'ready'; readonly response: ConfiguredColumnContextResponse };

type ReviewState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly result: InterpretationPreviewResponse; readonly revision: InterpretationRevisionSummary }
  | { readonly status: 'error'; readonly message: string };

const errorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string') return error.message;
  return 'Loom could not complete the interpretation request.';
};

const valueLabel = (value: unknown): string => {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

const revisionDetails = (revision: InterpretationRevisionSummary) => (
  <details className="mt-1 rounded border border-slate-200 bg-slate-50 px-2 py-1 text-[11px] text-slate-600">
    <summary className="cursor-pointer font-medium">Revision details</summary>
    <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-0.5">
      <dt>Revision</dt><dd className="break-all font-mono">{revision.id}</dd>
      <dt>Author</dt><dd>{revision.author}</dd>
      <dt>Created</dt><dd>{revision.createdAt}</dd>
    </dl>
  </details>
);

const sampleValues = (values: Readonly<Record<string, unknown>>) => (
  <div className="space-y-0.5">
    {Object.entries(values).map(([key, value]) => (
      <div key={key}><span className="font-mono text-slate-500">{key}</span>: {valueLabel(value)}</div>
    ))}
  </div>
);

export const InterpretationPanel = ({
  project,
  explorerId,
  authResourcePath,
  outputId,
  column,
  contextState,
  snapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
  disabled,
  onApply,
  onApplied,
  onContextRefresh,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly outputId: string;
  readonly column: ExplorerBuilderColumn;
  readonly contextState: InterpretationContextState;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly disabled: boolean;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
  readonly onApplied?: () => void;
  readonly onContextRefresh: () => void;
}) => {
  const loomClient = useLoomClient();
  const [review, setReview] = useState<ReviewState>({ status: 'idle' });
  const [selectedLibraryId, setSelectedLibraryId] = useState('');
  const [createMode, setCreateMode] = useState<'new' | 'revise'>('new');
  const [libraryName, setLibraryName] = useState('');
  const [explanation, setExplanation] = useState('');
  const [creating, setCreating] = useState(false);
  const [createMessage, setCreateMessage] = useState<string>();
  const context = contextState.status === 'ready' ? contextState.response : undefined;
  const columnContext = context?.columns.find((item) => item.outputId === outputId && item.column === column.column);
  const resolution = columnContext?.resolution;
  const applicableLibraries = useMemo(() => {
    if (contextState.status !== 'ready' || resolution?.state !== 'READY') return [];
    const applicableRevisionIds = new Set(resolution.applicableRevisionIds);
    return contextState.response.libraries.filter(
      (library) => library.head && applicableRevisionIds.has(library.head.id),
    );
  }, [contextState, resolution]);
  const pinnedRevision = column.interpretation?.pinned
    ? context?.pinnedRevisions.find((revision) => revision.id === column.interpretation?.pinned?.revisionId)
    : undefined;
  const interpretationReady = contextState.status === 'ready' && resolution?.state === 'READY';

  useEffect(() => {
    if (applicableLibraries.some((item) => item.id === selectedLibraryId)) return;
    setSelectedLibraryId(applicableLibraries[0]?.id ?? '');
  }, [applicableLibraries, selectedLibraryId]);

  useEffect(() => {
    setReview((current) => current.status === 'ready' ? { status: 'idle' } : current);
  }, [column.column, expectedDraftDigest, expectedDraftVersion, outputId, snapshotToken]);

  const reviewRevision = async (revision: InterpretationRevisionSummary) => {
    if (disabled || !interpretationReady) return;
    setReview({ status: 'loading' });
    try {
      const args: PreviewInterpretationCandidateArgs = {
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        expectedDraftVersion,
        expectedDraftDigest,
        outputId,
        column: column.column,
        revisionId: revision.id,
        limit: 100,
      };
      const result = await loomClient.previewInterpretationCandidate(args);
      setReview({ status: 'ready', result, revision });
    } catch (error) {
      setReview({ status: 'error', message: errorMessage(error) });
    }
  };

  const createRevision = async () => {
    if (!interpretationReady || disabled || (createMode === 'new' && !libraryName.trim()) || (createMode === 'revise' && !selectedLibraryId) || !explanation.trim()) return;
    const selected = applicableLibraries.find((item) => item.id === selectedLibraryId);
    const targetLibraryId = createMode === 'revise' ? selected?.id : libraryName.trim();
    if (!targetLibraryId || (createMode === 'revise' && !selected)) return;
    setCreating(true);
    setCreateMessage(undefined);
    try {
      await loomClient.createInterpretationRevisionFromColumn({
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        expectedDraftVersion,
        expectedDraftDigest,
        outputId,
        column: column.column,
        libraryId: targetLibraryId,
        ...(createMode === 'revise' && selected?.headRevisionId ? { parentRevisionId: selected.headRevisionId } : {}),
        explanation: explanation.trim(),
      });
      setLibraryName('');
      setExplanation('');
      setCreateMessage('Saved to the reusable library. The feature remains unchanged until you review and apply it.');
      onContextRefresh();
    } catch (error) {
      setCreateMessage(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  const applyReview = async () => {
    if (review.status !== 'ready' || disabled) return;
    const applied = await onApply({
      type: 'APPLY_INTERPRETATION_CANDIDATE',
      outputId,
      column: column.column,
      interpretationCandidate: {
        candidateReceiptId: review.result.candidateReceiptId,
        revisionId: review.result.revisionId,
      },
    });
    if (applied) {
      setReview({ status: 'idle' });
      onApplied?.();
    }
  };

  return (
    <div className="col-span-full mt-1 rounded border border-indigo-100 bg-indigo-50/40 px-2 py-2 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <span className="font-semibold text-indigo-950">Interpretation</span>
          <span className="ml-2 text-slate-600">
            {column.interpretation?.pinned ? `Pinned revision ${column.interpretation.pinned.revisionId}` : 'Current feature meaning is inline'}
          </span>
        </div>
        <span className="text-slate-500">{column.label}</span>
      </div>
      {contextState.status === 'loading' ? <p role="status" className="mt-2 text-slate-500">Loading saved interpretation context…</p> : null}
      {contextState.status === 'error' ? <p role="alert" className="mt-2 text-red-700">Interpretation context could not be loaded: {contextState.message}</p> : null}
      {contextState.status === 'ready' && !columnContext ? <p role="alert" className="mt-2 text-amber-800">Loom did not return context for this saved column.</p> : null}
      {resolution && resolution.state !== 'READY' ? <p role="status" className="mt-2 text-slate-600">{resolution.reason}</p> : null}
      {column.interpretation?.pinned ? (
        <div className="mt-1 text-slate-600">
          <p>This feature keeps its exact revision and will not follow a library head.</p>
          {pinnedRevision ? <div className="mt-1"><p>{pinnedRevision.explanation} · authored by {pinnedRevision.author}</p>{revisionDetails(pinnedRevision)}</div> : null}
        </div>
      ) : null}
      <details className="mt-2 rounded border border-indigo-100 bg-white/70 px-2 py-1">
        <summary className="cursor-pointer font-medium text-indigo-900">Reusable mappings</summary>
        {contextState.status === 'ready' && resolution?.state === 'READY' && applicableLibraries.length === 0 ? (
          <p className="mt-2 text-slate-500">No reusable interpretations are available yet.</p>
        ) : null}
        <div className="mt-2 space-y-1">
          {applicableLibraries.map((item) => {
            const head = item.head;
            if (!head) return null;
            return (
              <div key={item.id} className="flex flex-wrap items-start justify-between gap-2 rounded border border-slate-200 px-2 py-1">
                <div className="min-w-0">
                  <div className="font-medium text-slate-800">{item.id}</div>
                  <div className="text-slate-600">{head.explanation}</div>
                  {revisionDetails(head)}
                </div>
                <button type="button" className="rounded bg-indigo-700 px-2 py-1 font-medium text-white hover:bg-indigo-800 disabled:opacity-50" disabled={disabled || !interpretationReady} onClick={() => void reviewRevision(head)}>Review</button>
              </div>
            );
          })}
        </div>
        <div className="mt-3 border-t border-slate-200 pt-2">
          <div className="font-medium text-slate-800">Create reusable mapping</div>
          <p className="mt-1 text-slate-500">Loom derives the mapping from this saved column.</p>
          {column.interpretation?.pinned ? <p className="mt-1 text-amber-800">This feature is already pinned. Choose an inline feature to create a mapping from a saved column.</p> : null}
          <div className="mt-2 grid gap-2 sm:grid-cols-2">
            <label className="grid gap-1 text-slate-600">Revision mode<select className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={createMode} onChange={(event) => setCreateMode(event.currentTarget.value === 'revise' ? 'revise' : 'new')} disabled={disabled || creating || !interpretationReady || Boolean(column.interpretation?.pinned)}><option value="new">New library</option><option value="revise">Revise applicable head</option></select></label>
            {createMode === 'new' ? <label className="grid gap-1 text-slate-600">Library name<input className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={libraryName} onChange={(event) => setLibraryName(event.currentTarget.value)} placeholder="vitals" disabled={disabled || creating || !interpretationReady || Boolean(column.interpretation?.pinned)} /></label> : <label className="grid gap-1 text-slate-600">Library head<select className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={selectedLibraryId} onChange={(event) => setSelectedLibraryId(event.currentTarget.value)} disabled={disabled || creating || !interpretationReady || Boolean(column.interpretation?.pinned)}><option value="">Select a mapping</option>{applicableLibraries.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}</select></label>}
          </div>
          <label className="mt-2 grid gap-1 text-slate-600">Explanation<textarea className="rounded border border-slate-300 px-2 py-1 text-slate-800" rows={2} value={explanation} onChange={(event) => setExplanation(event.currentTarget.value)} placeholder="What this feature means" disabled={disabled || creating || !interpretationReady || Boolean(column.interpretation?.pinned)} /></label>
          <button type="button" className="mt-2 rounded border border-indigo-300 bg-white px-2 py-1 font-medium text-indigo-800 hover:bg-indigo-50 disabled:opacity-50" disabled={disabled || creating || !interpretationReady || Boolean(column.interpretation?.pinned) || (createMode === 'new' ? !libraryName.trim() : !selectedLibraryId) || !explanation.trim()} onClick={() => void createRevision()}>{creating ? 'Saving…' : 'Save reusable mapping'}</button>
          {createMessage ? <p className="mt-1 text-slate-600">{createMessage}</p> : null}
        </div>
      </details>
      {review.status === 'loading' ? <p className="mt-2 text-indigo-800">Reviewing affected rows…</p> : null}
      {review.status === 'error' ? <p className="mt-2 text-red-700">{review.message}</p> : null}
      {review.status === 'ready' ? (
        <div className="mt-2 rounded border border-indigo-200 bg-white p-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="font-medium text-slate-800">Review: Current → With this mapping</div>
            <div className="flex gap-1"><button type="button" className="rounded border border-slate-300 px-2 py-1 text-slate-700" onClick={() => setReview({ status: 'idle' })}>Cancel</button><button type="button" className="rounded bg-indigo-700 px-2 py-1 font-medium text-white disabled:opacity-50" disabled={disabled} onClick={() => void applyReview()}>Apply</button></div>
          </div>
          <p className="mt-1 text-slate-600">{review.result.completeness === 'INCOMPLETE' ? 'Sample only: the bounded review did not exhaust the output.' : 'The full output was exhausted for this review.'}</p>
          <p className="mt-1 text-slate-600">Compared {review.result.counts.compared} · Changed {review.result.counts.changed} · Resolved {review.result.counts.resolved} · Unresolved {review.result.counts.unresolved}</p>
          <div className="mt-2 overflow-x-auto"><table className="min-w-full text-left text-[11px]"><thead><tr className="border-b border-slate-200"><th className="px-1 py-1">Row</th><th className="px-1 py-1">Current</th><th className="px-1 py-1">With this mapping</th><th className="px-1 py-1">State</th></tr></thead><tbody>{review.result.samples.map((sample) => <tr key={sample.rowId} className="border-b border-slate-100"><td className="px-1 py-1 font-mono">{sample.rowId}</td><td className="px-1 py-1">{sampleValues(sample.before)}</td><td className="px-1 py-1">{sampleValues(sample.after)}</td><td className="px-1 py-1">{sample.state}</td></tr>)}</tbody></table></div>
        </div>
      ) : null}
    </div>
  );
};
