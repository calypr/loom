import { useCallback, useRef, useState } from 'react';
import { useEscapeToClose } from '../hooks/useEscapeToClose';
import type { SourceRuleCreate } from '../../../types';
import { LoomRequestError, type LoomClient } from '../../../api';
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
  readonly ruleEdit?: boolean;
  readonly autoPreview?: boolean;
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
  readonly createRuleReview?: { readonly label: string; readonly active: boolean };
  readonly preparing?: boolean;
  readonly prepared?: { readonly proposal: PreparedSourceProposal; readonly preview: ExplorerBuilderPreviewResult };
  readonly preparationError?: string;
  readonly columnLabels?: ReadonlyMap<string, string>;
}

type Props = BaseProps & (
  { readonly change: SourceRelationshipChange; readonly sourceStep?: never; readonly sourceSteps?: never; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']>; readonly sourceSteps?: never; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep?: never; readonly sourceSteps: ReadonlyArray<NonNullable<ExplorerBuilderCommand['sourceStep']>>; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep?: never; readonly sourceSteps?: never; readonly sourceRule: SourceRuleCreate }
);

export function SourceRelationshipReview(props: Props) {
  const [result, setResult] = useState<{ proposal: SourceRelationshipProposalWithPreview; preview: ExplorerBuilderPreviewResult }>();
  const [requestPhase, setPhase] = useState<'idle' | 'previewing' | 'applying'>('idle');
  const automaticPreview = Boolean(props.ruleEdit || props.autoPreview);
  const reviewActive = props.createRuleReview?.active ?? true;
  const phase = reviewActive ? requestPhase : 'idle';
  const busy = phase !== 'idle';
  const [previewCanceled, setPreviewCanceled] = useState(false);
  const [error, setError] = useState<string>();
  const sequence = useRef(0);
  const pending = useRef<AbortController | undefined>(undefined);
  const automaticTimer = useRef<number | undefined>(undefined);
  const cancel = () => {
    window.clearTimeout(automaticTimer.current);
    sequence.current += 1;
    const controller = pending.current;
    controller?.abort();
    pending.current = undefined;
    if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
    setPhase('idle');
    setPreviewCanceled(true);
    setResult(undefined);
    setError(undefined);
    props.onCancel();
  };
  useEscapeToClose(reviewActive && (props.createRuleReview ? phase !== 'applying' : !busy), cancel);
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
    setPreviewCanceled(false);
    setPhase('previewing'); setError(undefined); setResult(undefined);
    if (!props.onPreviewStart) props.onStatusChange?.('previewing');
    try {
      const proposal = await props.client.proposeSourceRelationship({
        project: props.project, explorerId: props.explorerId, authResourcePath: props.authResourcePath,
        snapshotToken: props.snapshotToken, expectedDraftVersion: props.draftVersion,
        expectedDraftDigest: props.draftDigest, outputId: props.outputId, ...(props.change ? { change: props.change } : props.sourceRule ? { sourceRule: props.sourceRule } : props.sourceSteps ? { sourceSteps: [...props.sourceSteps] } : { sourceStep: props.sourceStep }),
      }, controller.signal);
      if (token !== sequence.current || controller.signal.aborted) return;
      if (proposal.outputId !== props.outputId || proposal.snapshotToken !== props.snapshotToken ||
          proposal.draftVersion !== props.draftVersion || proposal.draftDigest !== props.draftDigest) {
        throw new Error('The draft changed. Cancel and review the current related records.');
      }
      const dynamicRule = props.sourceRule?.parameters.dynamic;
      const excludedKeys = new Set(dynamicRule?.outputs?.filter((output) => !output.enabled).map((output) => output.key));
      if (dynamicRule && excludedKeys.size) {
        const addition = proposal.candidateDocument.importedSource?.ruleAdditions?.find((rule) =>
          rule.occurrenceId === props.sourceRule?.occurrenceId && rule.parameters.dynamic?.name === dynamicRule.name,
        );
        const bindings = proposal.candidateNetwork.bindings.filter((binding) => binding.sourceStepId === addition?.stepId);
        if (!addition || !bindings.length || bindings.some((binding) =>
          binding.status !== 'EXACT' || !binding.outputKey || excludedKeys.has(binding.outputKey),
        )) throw new Error('Loom could not verify the selected columns in this preview. The rule has not been saved.');
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
        const detail = cause instanceof LoomRequestError && cause.requestId
          ? `${message} Request ID ${cause.requestId}.` : message;
        setError(detail); ownership?.failed(detail);
        if (!props.onPreviewStart) props.onStatusChange?.('failed', message);
      }
    } finally {
      if (pending.current === controller) pending.current = undefined;
      if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
      if (token === sequence.current) setPhase('idle');
    }
  };
  const previewRequest = useRef(preview);
  previewRequest.current = preview;
  const automaticStarted = useRef(false);
  const schedulePreview = useCallback((node: HTMLElement | null) => {
    if (!node) {
      window.clearTimeout(automaticTimer.current);
      if (pending.current) {
        pending.current.abort();
        automaticStarted.current = false;
      }
      return;
    }
    if (!automaticPreview || props.disabled || automaticStarted.current) return;
    automaticTimer.current = window.setTimeout(() => {
      automaticStarted.current = true;
      void previewRequest.current();
    }, 350);
  }, [automaticPreview, props.disabled]);

  const apply = async () => {
    const candidate = props.prepared ?? result;
    if (!candidate || busy || props.preparing || props.disabled) return;
    setPhase('applying'); setError(undefined);
    props.onStatusChange?.('applying');
    try {
      if (await props.onApply([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: props.outputId, proposalId: candidate.proposal.proposalId }])) cancel();
      else setError('The change was not saved. Review the current draft before trying again.');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The change was not saved.');
    } finally { setPhase('idle'); }
  };
  const visibleResult = reviewActive ? props.prepared ?? result : undefined;
  const externallyPreparing = props.preparing ?? false;
  const visibleError = reviewActive ? props.preparationError ?? error : undefined;
  const sourceEdit = Boolean(props.sourceRule || props.sourceStep || props.sourceSteps);
  const creatingColumnRule = Boolean(props.sourceRule);
  const addedColumnIds = creatingColumnRule && visibleResult
    ? new Set(visibleResult.proposal.affectedColumns) : undefined;
  const addedColumns = addedColumnIds
    ? visibleResult?.proposal.candidateDocument.columns.filter(column => column.columnId !== undefined && addedColumnIds.has(column.columnId)) : undefined;
  const summary = sourceEdit
    ? props.sourceSteps ? `${props.sourceSteps.length} source changes` : 'Source change'
    : `${props.change?.action === 'ADD' ? 'Add' : props.change?.action === 'REMOVE' ? 'Remove' : 'Update'}${props.relationshipDescription ? `: ${props.relationshipDescription}` : ''}`;
  return <section ref={automaticPreview ? schedulePreview : undefined} aria-label={sourceEdit ? 'Proposed source step' : 'Proposed related records'} className="flex flex-wrap items-center justify-end gap-1">
    {addedColumns ? <section aria-label="Generated columns" className="grid w-full gap-1 text-xs text-slate-600">
      <h4 className="font-semibold text-slate-800">Columns this rule will generate</h4>
      {addedColumns.length ? <ul className="max-h-40 divide-y divide-slate-100 overflow-auto border-y border-slate-200">
        {addedColumns.map(column => <li key={column.columnId} className="px-2 py-1.5 text-slate-800">{column.label || column.column}</li>)}
      </ul> : <p>No columns were generated in this preview.</p>}
    </section> : null}
    {visibleError ? <p role="alert" className="mr-auto text-xs text-red-800">{visibleError}</p> : sourceEdit ? null : <p className="mr-auto min-w-0 truncate text-xs text-slate-700" title={summary}>{summary}</p>}
    {automaticPreview ? (
      !visibleResult && !visibleError && !props.disabled ? <p role="status" className="mr-auto text-xs text-slate-600">{previewCanceled ? 'Preview canceled.' : 'Updating preview…'}</p>
        : visibleError ? <button type="button" disabled={busy || props.disabled} onClick={() => void preview()}
            className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">Retry preview</button> : null
    ) : (
      <button type="button" disabled={busy || externallyPreparing || props.disabled} onClick={() => void preview()} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">{externallyPreparing ? 'Preparing…' : phase === 'previewing' ? (props.createRuleReview ? 'Preparing preview…' : 'Working…') : creatingColumnRule ? props.createRuleReview?.label ?? 'Preview column rule' : sourceEdit ? 'Preview source step' : 'Preview relationship'}</button>
    )}
    {!(props.createRuleReview || props.ruleEdit) || visibleResult ? <button type="button" data-testid={sourceEdit ? 'imported-source-step-apply' : undefined} disabled={!visibleResult || busy || externallyPreparing || props.disabled} onClick={() => void apply()} className="h-8 rounded bg-blue-700 px-3 text-sm text-white disabled:opacity-50">{phase === 'applying' && (props.createRuleReview || props.ruleEdit) ? 'Saving…' : creatingColumnRule ? 'Apply column rule' : props.ruleEdit ? 'Apply changes' : sourceEdit ? 'Apply source step' : 'Apply relationship'}</button> : null}
    {!props.createRuleReview || automaticPreview || busy || visibleError || visibleResult ? <button type="button" disabled={phase === 'applying' || (busy && !!visibleResult)} onClick={cancel} className="h-8 rounded border border-slate-300 bg-white px-3 text-sm">Cancel</button> : null}
  </section>;
}
