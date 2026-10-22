import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect } from 'vitest';
import type { useConstructionLifecycle } from '../src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle';

type Lifecycle = ReturnType<typeof useConstructionLifecycle>;
type LifecycleHandle = { readonly current: Lifecycle };

export type FormalTraceModel = 'BuilderPreview' | 'ConstructionLifecycle' | 'ReconcileLifecycle';

export type FormalObservation = {
  readonly status: string;
  readonly canApply: boolean;
  readonly proposalIdPresent: boolean;
  readonly previewReceiptPresent: boolean;
  readonly rowsPresent: boolean;
  readonly receiptPresent: boolean;
};

type FormalTraceAction = {
  readonly label: string;
  readonly event: string;
  readonly observation: FormalObservation;
  readonly note?: string;
};

type FormalTraceUnmodeledEvent = {
  readonly kind: string;
  readonly note: string;
};

export type FormalTrace = {
  readonly name: string;
  readonly expectedFailure: boolean;
  readonly model: FormalTraceModel;
  initialObservation: FormalObservation | undefined;
  readonly actions: FormalTraceAction[];
  readonly unmodeledEvents: FormalTraceUnmodeledEvent[];
};

const SOURCE_COMMIT = process.env.LOOM_SOURCE_COMMIT ?? 'dc331783dde3f61e764bab3a421b66b28698c9d4';

let activeTrace: FormalTrace | undefined;
const traces: FormalTrace[] = [];

const observationFor = (lifecycle: LifecycleHandle): FormalObservation => {
  const proposal = lifecycle.current.proposal;
  const hasResponse = 'response' in proposal;
  const hasPreview = 'preview' in proposal;
  return {
    status: proposal.status,
    canApply: lifecycle.current.canApply,
    proposalIdPresent: hasResponse && Boolean(proposal.response.proposalId),
    previewReceiptPresent: hasPreview && Boolean(proposal.preview.receiptId),
    rowsPresent: hasPreview && Array.isArray(proposal.preview.rows),
    receiptPresent: hasPreview && Boolean(proposal.preview.receiptId),
  };
};

export const startTrace = (model: FormalTraceModel = 'BuilderPreview'): void => {
  const name = expect.getState().currentTestName ?? '';
  const testTitle = name.split(' > ').at(-1) ?? name;
  activeTrace = {
    name,
    expectedFailure: testTitle.startsWith('finding:'),
    model,
    initialObservation: undefined,
    actions: [],
    unmodeledEvents: [],
  };
};

export const traceInitialObservation = (lifecycle: LifecycleHandle): void => {
  if (!activeTrace) return;
  activeTrace.initialObservation = observationFor(lifecycle);
};

export const traceInitialObservationValue = (observation: FormalObservation): void => {
  if (!activeTrace) return;
  activeTrace.initialObservation = observation;
};

const traceActionWithObservation = (
  label: string,
  event: string,
  observation: FormalObservation,
  note?: string,
): void => {
  if (!activeTrace) return;
  activeTrace.actions.push({
    label,
    event,
    observation,
    ...(note ? { note } : {}),
  });
};

export const traceAction = (
  label: string,
  lifecycle: LifecycleHandle,
  event: string,
  note?: string,
): void => {
  traceActionWithObservation(label, event, observationFor(lifecycle), note);
};

export const traceObservedAction = (
  label: string,
  event: string,
  observation: FormalObservation,
  note?: string,
): void => {
  traceActionWithObservation(label, event, observation, note);
};

const EMBEDDED_PREVIEW_ABSTRACT_OBSERVATION: FormalObservation = {
  status: 'previewing',
  canApply: false,
  proposalIdPresent: false,
  previewReceiptPresent: false,
  rowsPresent: false,
  receiptPresent: false,
};

export const traceEmbeddedPreviewExpansion = (lifecycle: LifecycleHandle): void => {
  const note = 'embedded proposal preview abstract split';
  traceActionWithObservation('ProposalComplete', 'proposal-response', EMBEDDED_PREVIEW_ABSTRACT_OBSERVATION, note);
  traceActionWithObservation('StartPreview', 'preview', EMBEDDED_PREVIEW_ABSTRACT_OBSERVATION, note);
  traceAction('PreviewComplete', lifecycle, 'preview-response', note);
};

export const traceUnmodeled = (kind: string, note: string): void => {
  if (!activeTrace) return;
  activeTrace.unmodeledEvents.push({ kind, note });
};

export const finishTrace = (): void => {
  if (!activeTrace) return;
  const name = expect.getState().currentTestName ?? activeTrace.name;
  traces.push({ ...activeTrace, name });
  activeTrace = undefined;
};

export const writeTraces = (): void => {
  const output = process.env.LOOM_BUILDER_TRACE_OUTPUT;
  if (!output) return;
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, `${JSON.stringify({
    schemaVersion: 1,
    sourceCommit: SOURCE_COMMIT,
    traces,
  }, null, 2)}\n`);
};
