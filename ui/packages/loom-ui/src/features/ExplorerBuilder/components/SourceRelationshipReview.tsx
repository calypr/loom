import { useRef, useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import type { LoomClient } from '../../../api';
import type {
  ExplorerBuilderCommand,
  ExplorerBuilderPreviewResult,
  SourceRelationshipChange,
  SourceRelationshipProposal,
  SourceRelationshipProposalWithPreview,
} from '../../../types';

type PreparedSourceProposal = Pick<SourceRelationshipProposal, 'candidateDocument' | 'proposalId'> &
  Partial<Pick<SourceRelationshipProposal, 'affectedColumns' | 'notices'>>;

interface BaseProps {
  readonly client: LoomClient;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly disabled: boolean;
  readonly onApply: (commands: ExplorerBuilderCommand[]) => Promise<boolean>;
  readonly onCancel: () => void;
  readonly relationshipDescription?: string;
  readonly requestControllerRef?: { current: AbortController | undefined };
  readonly onPreviewStart?: () => {
    ready: (proposal: SourceRelationshipProposalWithPreview, preview: ExplorerBuilderPreviewResult) => boolean;
    failed: (message: string) => void;
  } | undefined;
  readonly onPreviewReady?: (
    proposal: SourceRelationshipProposalWithPreview,
    preview: ExplorerBuilderPreviewResult,
  ) => void;
  readonly onStatusChange?: (status: 'previewing' | 'applying' | 'failed', message?: string) => void;
  readonly sharedPreview?: boolean;
  readonly preparing?: boolean;
  readonly prepared?: { readonly proposal: PreparedSourceProposal; readonly preview: ExplorerBuilderPreviewResult };
  readonly preparationError?: string;
  readonly columnLabels?: ReadonlyMap<string, string>;
}

type Props = BaseProps & (
  { readonly change: SourceRelationshipChange; readonly sourceStep?: never; readonly sourceSteps?: never } |
  { readonly change?: never; readonly sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']>; readonly sourceSteps?: never } |
  { readonly change?: never; readonly sourceStep?: never; readonly sourceSteps: ReadonlyArray<NonNullable<ExplorerBuilderCommand['sourceStep']>> }
);

// The owner mounts this inspector with the full draft/snapshot/output identity
// as its key. Cancel revokes in-flight responses, including a second Preview.
export function SourceRelationshipReview(props: Props) {
  const [result, setResult] = useState<{ proposal: SourceRelationshipProposalWithPreview; preview: ExplorerBuilderPreviewResult }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const sequence = useRef(0);
  const pending = useRef<AbortController | undefined>(undefined);
  const cancel = () => {
    sequence.current += 1;
    const controller = pending.current;
    controller?.abort();
    pending.current = undefined;
    if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
    props.onCancel();
  };
  useEscapeToClose(!busy, cancel);
  const preview = async () => {
    const ownership = props.onPreviewStart?.();
    if (props.onPreviewStart && !ownership) return;
    const token = ++sequence.current;
    const previous = pending.current;
    previous?.abort();
    if (props.requestControllerRef && props.requestControllerRef.current === previous) props.requestControllerRef.current = undefined;
    const controller = new AbortController();
    pending.current = controller;
    if (props.requestControllerRef) props.requestControllerRef.current = controller;
    setBusy(true); setError(undefined); setResult(undefined);
    if (!props.onPreviewStart) props.onStatusChange?.('previewing');
    try {
      const proposal = await props.client.proposeSourceRelationship({
        project: props.project, explorerId: props.explorerId, authResourcePath: props.authResourcePath,
        snapshotToken: props.snapshotToken, expectedDraftVersion: props.draftVersion,
        expectedDraftDigest: props.draftDigest, outputId: props.outputId, ...(props.change ? { change: props.change } : props.sourceSteps ? { sourceSteps: [...props.sourceSteps] } : { sourceStep: props.sourceStep }),
      }, controller.signal);
      if (token !== sequence.current || controller.signal.aborted) return;
      if (proposal.outputId !== props.outputId || proposal.snapshotToken !== props.snapshotToken ||
          proposal.draftVersion !== props.draftVersion || proposal.draftDigest !== props.draftDigest) {
        throw new Error('The draft changed. Cancel and review the current related records.');
      }
      const rows = proposal.preview;
      if (rows.receiptId !== proposal.candidateReceiptId || rows.outputId !== props.outputId || rows.rows === null) throw new Error('The preview does not belong to this proposed change.');
      if (token !== sequence.current || controller.signal.aborted) return;
      if (ownership && !ownership.ready(proposal, rows)) return;
      setResult({ proposal, preview: rows });
      props.onPreviewReady?.(proposal, rows);
    } catch (cause) {
      if (token === sequence.current && !controller.signal.aborted) {
        const message = cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.';
        setError(message); ownership?.failed(message);
        if (!props.onPreviewStart) props.onStatusChange?.('failed', message);
      }
    } finally {
      if (pending.current === controller) pending.current = undefined;
      if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
      if (token === sequence.current) setBusy(false);
    }
  };
  const apply = async () => {
    const candidate = props.prepared ?? result;
    if (!candidate || busy || props.preparing || props.disabled) return;
    setBusy(true); setError(undefined);
    props.onStatusChange?.('applying');
    try {
      if (await props.onApply([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: props.outputId, proposalId: candidate.proposal.proposalId }])) cancel();
      else setError('The change was not saved. Review the current draft before trying again.');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The change was not saved.');
    } finally { setBusy(false); }
  };
  const visibleResult = props.prepared ?? result;
  const externallyPreparing = props.preparing ?? false;
  const visibleError = props.preparationError ?? error;
  const sourceEdit = Boolean(props.sourceStep || props.sourceSteps);
  const creatingColumnRule = props.sourceStep?.createIdentifierRule === true;
  const addedColumnIds = creatingColumnRule && visibleResult
    ? new Set(visibleResult.proposal.affectedColumns) : undefined;
  const addedColumns = addedColumnIds
    ? visibleResult?.proposal.candidateDocument.columns.filter(column => column.columnId !== undefined && addedColumnIds.has(column.columnId)) : undefined;
  const summary = sourceEdit
    ? props.sourceSteps ? `${props.sourceSteps.length} source changes` : 'Source change'
    : `${props.change?.action === 'ADD' ? 'Add' : props.change?.action === 'REMOVE' ? 'Remove' : 'Update'}${props.relationshipDescription ? `: ${props.relationshipDescription}` : ''}`;
  return <section aria-label={sourceEdit ? 'Proposed source step' : 'Proposed related records'} className="flex flex-wrap items-center justify-end gap-1">
    {addedColumns ? <p className="w-full text-xs text-slate-600">
      {addedColumns.length ? `${addedColumns.length} identifier ${addedColumns.length === 1 ? 'column' : 'columns'} in the preview.`
        : 'No identifier columns were found in this preview.'}
    </p> : null}
    {visibleError ? <p role="alert" className="mr-auto text-xs text-red-800">{visibleError}</p> : sourceEdit ? null : <p className="mr-auto min-w-0 truncate text-xs text-slate-700" title={summary}>{summary}</p>}
    <button type="button" disabled={busy || externallyPreparing || props.disabled} onClick={() => void preview()} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">{externallyPreparing ? 'Preparing…' : busy ? 'Working…' : creatingColumnRule ? 'Preview column rule' : sourceEdit ? 'Preview source step' : 'Preview relationship'}</button>
    <button type="button" data-testid={sourceEdit ? 'imported-source-step-apply' : undefined} disabled={!visibleResult || busy || externallyPreparing || props.disabled} onClick={() => void apply()} className="h-8 rounded bg-blue-700 px-3 text-sm text-white disabled:opacity-50">{creatingColumnRule ? 'Apply column rule' : sourceEdit ? 'Apply source step' : 'Apply relationship'}</button>
    <button type="button" disabled={busy && !!visibleResult} onClick={cancel} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">Cancel</button>
  </section>;
}
