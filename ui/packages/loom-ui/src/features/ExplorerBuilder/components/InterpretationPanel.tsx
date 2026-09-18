import React, { useEffect, useMemo, useState } from 'react';
import type {
  CreateInterpretationRevisionArgs,
  PreviewInterpretationCandidateArgs,
} from '../../../api';
import { useLoomClient } from '../../../react';
import type {
  InterpretationApplicability,
  InterpretationLibraryView,
  InterpretationPreviewResponse,
  InterpretationRevision,
  InterpretationRule,
} from '../../../interpretation';
import type {
  ExplorerBuilderCatalog,
  ExplorerBuilderColumn,
  ExplorerBuilderCommand,
} from '../../../types';
import type { InterpretationBinding } from '../authoring/interpretationCandidate';

type ReviewState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly result: InterpretationPreviewResponse; readonly revision: InterpretationRevision }
  | { readonly status: 'error'; readonly message: string };

const errorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string') return error.message;
  return 'Loom could not complete the interpretation request.';
};

const sourceLabel = (column: ExplorerBuilderColumn): string => {
  switch (column.source.kind) {
    case 'field':
      return column.source.field.path;
    case 'aggregate':
      return [column.source.aggregate.operation, column.source.aggregate.path].filter(Boolean).join(' · ');
    case 'projectId':
      return 'Project identifier';
    default:
      return column.source.kind;
  }
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

const ruleForColumn = (column: ExplorerBuilderColumn, binding: InterpretationBinding | undefined): InterpretationRule | undefined => {
  if (!binding) return undefined;
  return {
    id: `rule-${column.column}`,
    match: {
      resourceType: binding.resourceType,
      logicalType: binding.logicalType,
      cardinality: binding.cardinality,
      ...(binding.sourceCanonical ? { sourceCanonical: binding.sourceCanonical } : {}),
      ...(binding.sourceProfile ? { sourceProfile: binding.sourceProfile } : {}),
      ...(binding.owningScope ? { owningScope: binding.owningScope } : {}),
      ...(binding.system ? { system: binding.system } : {}),
      ...(binding.code ? { code: binding.code } : {}),
      ...(binding.extensionUrlPath ? { extensionUrlPath: [...binding.extensionUrlPath] } : {}),
    },
    definition: {
      source: column.source,
      ...(column.contributor ? { contributor: column.contributor } : {}),
    },
  };
};

const applicabilityForColumn = (binding: InterpretationBinding | undefined): InterpretationApplicability => {
  if (!binding) return {};
  return {
    resourceTypes: [binding.resourceType],
    logicalTypes: [binding.logicalType],
    cardinalities: [binding.cardinality],
  };
};

const revisionDetails = (revision: InterpretationRevision) => (
  <details className="mt-1 rounded border border-slate-200 bg-slate-50 px-2 py-1 text-[11px] text-slate-600">
    <summary className="cursor-pointer font-medium">Binding details</summary>
    <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-0.5">
      <dt>Revision</dt><dd className="break-all font-mono">{revision.id}</dd>
      <dt>Author</dt><dd>{revision.author}</dd>
      <dt>Rules</dt><dd>{revision.rules.length}</dd>
      <dt>Applicability</dt><dd>{revision.applicability.resourceTypes?.join(', ') ?? 'Any resource'}</dd>
    </dl>
  </details>
);

const matches = (allowed: ReadonlyArray<string> | undefined, value: string | undefined): boolean =>
  !allowed || allowed.length === 0 || (value !== undefined && allowed.includes(value));

const revisionAppliesToColumn = (
  revision: InterpretationRevision,
  column: ExplorerBuilderColumn,
  catalog: ExplorerBuilderCatalog,
  binding: InterpretationBinding | undefined,
): boolean => {
  if (!binding) return false;
  const resourceType = binding.resourceType;
  if (!matches(revision.applicability.resourceTypes, resourceType) ||
      !matches(revision.applicability.sourceProfiles, binding.sourceProfile) ||
      !matches(revision.applicability.sourceCanonical, binding.sourceCanonical) ||
      !matches(revision.applicability.logicalTypes, binding.logicalType) ||
      !matches(revision.applicability.cardinalities, binding.cardinality) ||
      !matches(revision.applicability.schemaDigests, catalog.resolvedSchemaDigest)) return false;
  return revision.rules.some((rule) => {
    const match = rule.match;
    return matches(match.resourceType ? [match.resourceType] : undefined, resourceType) &&
      matches(match.logicalType ? [match.logicalType] : undefined, binding.logicalType) &&
      matches(match.cardinality ? [match.cardinality] : undefined, binding.cardinality) &&
      matches(match.sourceCanonical ? [match.sourceCanonical] : undefined, binding.sourceCanonical) &&
      matches(match.sourceProfile ? [match.sourceProfile] : undefined, binding.sourceProfile) &&
      matches(match.owningScope ? [match.owningScope] : undefined, binding.owningScope) &&
      matches(match.system ? [match.system] : undefined, binding.system) &&
      matches(match.code ? [match.code] : undefined, binding.code) &&
      (!match.extensionUrlPath || JSON.stringify(match.extensionUrlPath) === JSON.stringify(binding.extensionUrlPath ?? []));
  });
};

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
  catalog,
  binding,
  snapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
  disabled,
  onApply,
  onApplied,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly outputId: string;
  readonly column: ExplorerBuilderColumn;
  readonly catalog: ExplorerBuilderCatalog;
  readonly binding?: InterpretationBinding;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly disabled: boolean;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
  readonly onApplied?: () => void;
}) => {
  const loomClient = useLoomClient();
  const [libraries, setLibraries] = useState<ReadonlyArray<InterpretationLibraryView>>([]);
  const [libraryLoading, setLibraryLoading] = useState(true);
  const [libraryError, setLibraryError] = useState<string>();
  const [pinnedRevision, setPinnedRevision] = useState<InterpretationRevision>();
  const [pinnedRevisionError, setPinnedRevisionError] = useState<string>();
  const [review, setReview] = useState<ReviewState>({ status: 'idle' });
  const [selectedLibraryId, setSelectedLibraryId] = useState('');
  const [createMode, setCreateMode] = useState<'new' | 'revise'>('new');
  const [libraryName, setLibraryName] = useState('');
  const [explanation, setExplanation] = useState('');
  const [creating, setCreating] = useState(false);
  const [createMessage, setCreateMessage] = useState<string>();
  const rule = useMemo(() => ruleForColumn(column, binding), [binding, column]);
  const applicableLibraries = useMemo(
    () => libraries.filter((item) => item.head && revisionAppliesToColumn(item.head, column, catalog, binding)),
    [binding, catalog, column, libraries],
  );

  const loadLibraries = async () => {
    setLibraryLoading(true);
    setLibraryError(undefined);
    try {
      const value = await loomClient.listInterpretationLibraries({ project });
      setLibraries(value);
      setSelectedLibraryId((current) => current || value[0]?.library.id || '');
    } catch (error) {
      setLibraryError(errorMessage(error));
    } finally {
      setLibraryLoading(false);
    }
  };

  useEffect(() => {
    void loadLibraries();
  }, [loomClient, project]);

  useEffect(() => {
    if (applicableLibraries.some((item) => item.library.id === selectedLibraryId)) return;
    setSelectedLibraryId(applicableLibraries[0]?.library.id ?? '');
  }, [applicableLibraries, selectedLibraryId]);

  useEffect(() => {
    const revisionId = column.interpretation?.pinned?.revisionId;
    setPinnedRevision(undefined);
    if (!revisionId) {
      setPinnedRevision(undefined);
      setPinnedRevisionError(undefined);
      return;
    }
    let active = true;
    setPinnedRevisionError(undefined);
    void loomClient.getInterpretationRevision({ project, revisionId }).then(
      (revision) => {
        if (active) setPinnedRevision(revision);
      },
      (error: unknown) => {
        if (active) setPinnedRevisionError(errorMessage(error));
      },
    );
    return () => {
      active = false;
    };
  }, [column.interpretation?.pinned?.revisionId, loomClient, project]);

  useEffect(() => {
    setReview((current) => current.status === 'ready' ? { status: 'idle' } : current);
  }, [column.column, expectedDraftDigest, expectedDraftVersion, outputId, snapshotToken]);

  const reviewRevision = async (revision: InterpretationRevision) => {
    if (disabled) return;
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
    if (disabled || (createMode === 'new' && !libraryName.trim()) || (createMode === 'revise' && !selectedLibraryId) || !explanation.trim() || !rule) return;
    setCreating(true);
    setCreateMessage(undefined);
    const selected = applicableLibraries.find((item) => item.library.id === selectedLibraryId);
    const targetLibraryID = createMode === 'revise' ? selected?.library.id : libraryName.trim();
    if (!targetLibraryID || (createMode === 'revise' && !selected)) {
      setCreating(false);
      return;
    }
    const args: CreateInterpretationRevisionArgs = {
      project,
      authResourcePath,
      libraryId: targetLibraryID,
      ...(createMode === 'revise' && selected?.library.headRevisionId ? { parentRevisionId: selected.library.headRevisionId } : {}),
      applicability: applicabilityForColumn(binding),
      rules: [rule],
      explanation: explanation.trim(),
    };
    try {
      await loomClient.createInterpretationRevision(args);
      setLibraryName('');
      setExplanation('');
      setCreateMessage('Saved to the reusable library. The feature remains unchanged until you review and apply it.');
      await loadLibraries();
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
        <span className="text-slate-500">{sourceLabel(column)}</span>
      </div>
      {column.interpretation?.pinned ? (
        <div className="mt-1 text-slate-600">
          <p>This feature keeps its exact revision and will not follow a library head.</p>
          {pinnedRevisionError ? <p className="text-red-700">Could not load the pinned revision: {pinnedRevisionError}</p> : null}
          {pinnedRevision ? <div className="mt-1"><p>{pinnedRevision.explanation} · authored by {pinnedRevision.author}</p>{revisionDetails(pinnedRevision)}</div> : null}
        </div>
      ) : null}
      <details className="mt-2 rounded border border-indigo-100 bg-white/70 px-2 py-1">
        <summary className="cursor-pointer font-medium text-indigo-900">Reusable mappings</summary>
        {libraryLoading ? <p className="mt-2 text-slate-500">Loading mappings…</p> : null}
        {libraryError ? <p className="mt-2 text-red-700">{libraryError}</p> : null}
        {!libraryLoading && !libraryError && applicableLibraries.length === 0 ? (
          <p className="mt-2 text-slate-500">No reusable interpretations are available yet.</p>
        ) : null}
        <div className="mt-2 space-y-1">
          {applicableLibraries.map((item) => {
            const head = item.head;
            return (
              <div key={item.library.id} className="flex flex-wrap items-start justify-between gap-2 rounded border border-slate-200 px-2 py-1">
                <div className="min-w-0">
                  <div className="font-medium text-slate-800">{item.library.id}</div>
                  {head ? <div className="text-slate-600">{head.explanation}</div> : <div className="text-slate-500">No head revision yet.</div>}
                  {head ? revisionDetails(head) : null}
                </div>
                {head ? <button type="button" className="rounded bg-indigo-700 px-2 py-1 font-medium text-white hover:bg-indigo-800 disabled:opacity-50" disabled={disabled} onClick={() => void reviewRevision(head)}>Review</button> : null}
              </div>
            );
          })}
        </div>
        <div className="mt-3 border-t border-slate-200 pt-2">
          <div className="font-medium text-slate-800">Create reusable mapping</div>
          <p className="mt-1 text-slate-500">Loom derives the typed binding from this feature and the catalog. No expression is entered here.</p>
          {column.interpretation?.pinned ? <p className="mt-1 text-amber-800">This feature is already pinned. Create from its exact revision after loading that revision, or use an inline feature.</p> : null}
          <div className="mt-2 grid gap-2 sm:grid-cols-2">
            <label className="grid gap-1 text-slate-600">Revision mode<select className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={createMode} onChange={(event) => setCreateMode(event.currentTarget.value === 'revise' ? 'revise' : 'new')} disabled={disabled || creating || Boolean(column.interpretation?.pinned)}><option value="new">New library</option><option value="revise">Revise applicable head</option></select></label>
            {createMode === 'new' ? <label className="grid gap-1 text-slate-600">Library name<input className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={libraryName} onChange={(event) => setLibraryName(event.currentTarget.value)} placeholder="vitals" disabled={disabled || creating || Boolean(column.interpretation?.pinned)} /></label> : <label className="grid gap-1 text-slate-600">Library head<select className="rounded border border-slate-300 px-2 py-1 text-slate-800" value={selectedLibraryId} onChange={(event) => setSelectedLibraryId(event.currentTarget.value)} disabled={disabled || creating || Boolean(column.interpretation?.pinned)}><option value="">Select a mapping</option>{applicableLibraries.map((item) => <option key={item.library.id} value={item.library.id}>{item.library.id}</option>)}</select></label>}
          </div>
          <label className="mt-2 grid gap-1 text-slate-600">Explanation<textarea className="rounded border border-slate-300 px-2 py-1 text-slate-800" rows={2} value={explanation} onChange={(event) => setExplanation(event.currentTarget.value)} placeholder="What this feature means" disabled={disabled || creating || Boolean(column.interpretation?.pinned)} /></label>
          <button type="button" className="mt-2 rounded border border-indigo-300 bg-white px-2 py-1 font-medium text-indigo-800 hover:bg-indigo-50 disabled:opacity-50" disabled={disabled || creating || Boolean(column.interpretation?.pinned) || (createMode === 'new' ? !libraryName.trim() : !selectedLibraryId) || !explanation.trim() || !rule || !binding} onClick={() => void createRevision()}>{creating ? 'Saving…' : 'Save reusable mapping'}</button>
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
