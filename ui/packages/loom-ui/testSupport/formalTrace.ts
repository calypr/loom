import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { expect } from 'vitest';
import type { useConstructionLifecycle } from '../src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle';

type Lifecycle = ReturnType<typeof useConstructionLifecycle>;
type LifecycleHandle = { readonly current: Lifecycle };

export type FormalTraceModel = 'BuilderPreview' | 'ConstructionLifecycle' | 'ReconcileLifecycle';

export type ProposalAttempt = 'P1' | 'P2';

export type FormalTraceOwner =
  | { readonly kind: 'proposal'; readonly attempt: ProposalAttempt }
  | { readonly kind: 'preview'; readonly attempt: ProposalAttempt }
  | { readonly kind: 'apply'; readonly attempt: ProposalAttempt }
  | { readonly kind: 'discovery'; readonly attempt: 'D1' };

type HookFormalObservation = {
  readonly status: string;
  readonly tablePreviewStatus?: string;
  readonly proposalId: string | null;
  readonly proposalOutputId: string | null;
  readonly proposalSnapshotToken: string | null;
  readonly proposalDraftVersion: number | null;
  readonly proposalDraftDigest: string | null;
  readonly previewReceiptId: string | null;
  readonly previewOutputId: string | null;
  readonly canApply: boolean;
  readonly proposalIdPresent: boolean;
  readonly previewReceiptPresent: boolean;
  readonly rowsPresent: boolean;
  readonly receiptPresent: boolean;
};

export type ReconcileTraceSnapshot = {
  readonly status: string;
  readonly activeRequestKey: 'A' | 'B' | null;
  readonly current: {
    readonly modelKey: 'W1' | 'W2';
    readonly draftVersion: number;
    readonly draftDigest: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly explorerId: string;
    readonly provenance: {
      readonly draftIdentity: 'rendered-dom';
      readonly outputId: 'rendered-dom';
      readonly snapshotToken: 'matching-reconcile-request';
      readonly explorerId: 'matching-reconcile-request';
    };
  };
  readonly request: {
    readonly modelKey: 'A' | 'B';
    readonly rawRequestId: string;
    readonly workspaceKey: 'W1' | 'W2';
    readonly draftVersion: number;
    readonly draftDigest: string;
    readonly snapshotToken: string;
    readonly outputId: string;
    readonly explorerId: string;
  } | null;
  readonly receipt: {
    readonly modelKey: 'R1' | 'R2';
    readonly receiptId: string;
    readonly requestKey: 'A' | 'B';
    readonly workspaceKey: 'W1' | 'W2';
    readonly outputModelKey: 'O1' | 'O2';
    readonly outputId: string;
  } | null;
  readonly returned: {
    readonly requestKey: 'A' | 'B';
    readonly receiptModelKey: 'R1' | 'R2';
    readonly receiptId: string;
    readonly workspaceKey: 'W1' | 'W2';
    readonly outputModelKey: 'O1' | 'O2';
    readonly outputId: string;
    readonly outputIds: ReadonlyArray<string>;
  } | null;
};

type ReconcileFormalObservation = {
  readonly status: string;
  readonly reconcile: Omit<ReconcileTraceSnapshot, 'status'>;
};

export type FormalObservation = HookFormalObservation | ReconcileFormalObservation;

type FormalTraceAction = {
  readonly label: string;
  readonly event: string;
  readonly observation: FormalObservation;
  readonly note?: string;
  readonly owner?: FormalTraceOwner | null;
  readonly expectedProposal?: ProposalAttempt;
};

type FormalTraceUnmodeledEvent = {
  readonly kind: string;
  readonly note: string;
  readonly classification: 'unsupported';
  readonly reason: string;
};

export type FormalTrace = {
  readonly name: string;
  readonly expectedFailure: boolean;
  readonly model: FormalTraceModel;
  initialObservation: FormalObservation | undefined;
  initialAction?: { readonly label: string; readonly event: string; readonly note?: string };
  readonly actions: FormalTraceAction[];
  readonly unmodeledEvents: FormalTraceUnmodeledEvent[];
};

export type TraceSourceProfile = 'formal' | 'journey';

const requiredEnvironmentValue = (name: string): string => {
  const value = process.env[name];
  if (!value) throw new Error(`formal trace output requires ${name}`);
  return value;
};

const traceSourceProfile = (): TraceSourceProfile => {
  const value = requiredEnvironmentValue('LOOM_TRACE_SOURCE_PROFILE');
  if (value === 'formal' || value === 'journey') return value;
  throw new Error(`unsupported LOOM_TRACE_SOURCE_PROFILE: ${value}`);
};

let activeTrace: FormalTrace | undefined;
const traces: FormalTrace[] = [];

const observationFor = (lifecycle: LifecycleHandle): FormalObservation => {
  const proposal = lifecycle.current.proposal;
  const hasResponse = 'response' in proposal;
  const hasPreview = 'preview' in proposal;
  const response = hasResponse ? proposal.response : undefined;
  const preview = hasPreview ? proposal.preview : undefined;
  return {
    status: proposal.status,
    proposalId: response?.proposalId ?? null,
    proposalOutputId: response?.outputId ?? null,
    proposalSnapshotToken: response?.snapshotToken ?? null,
    proposalDraftVersion: response?.draftVersion ?? null,
    proposalDraftDigest: response?.draftDigest ?? null,
    previewReceiptId: preview?.receiptId ?? null,
    previewOutputId: preview?.outputId ?? null,
    canApply: lifecycle.current.canApply,
    proposalIdPresent: hasResponse && Boolean(proposal.response.proposalId),
    previewReceiptPresent: hasPreview && Boolean(proposal.preview.receiptId),
    rowsPresent: hasPreview && Array.isArray(proposal.preview.rows),
    receiptPresent: hasPreview && Boolean(proposal.preview.receiptId),
  };
};

const reconcileObservationFor = (snapshot: ReconcileTraceSnapshot): FormalObservation => {
  const { status, ...reconcile } = snapshot;
  return { status, reconcile };
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

export const traceInitialReconcileObservation = (
  snapshot: ReconcileTraceSnapshot,
  label: string,
  event: string,
  note?: string,
): void => {
  if (!activeTrace || activeTrace.model !== 'ReconcileLifecycle') return;
  activeTrace.initialObservation = reconcileObservationFor(snapshot);
  activeTrace.initialAction = { label, event, ...(note ? { note } : {}) };
};

const traceActionWithObservation = (
  label: string,
  event: string,
  observation: FormalObservation,
  note?: string,
  owner?: FormalTraceOwner | null,
  expectedProposal?: ProposalAttempt,
): void => {
  if (!activeTrace) return;
  activeTrace.actions.push({
    label,
    event,
    observation,
    ...(note ? { note } : {}),
    ...(owner !== undefined ? { owner } : {}),
    ...(expectedProposal ? { expectedProposal } : {}),
  });
};

export const traceAction = (
  label: string,
  lifecycle: LifecycleHandle,
  event: string,
  note?: string,
  owner?: FormalTraceOwner | null,
  expectedProposal?: ProposalAttempt,
): void => {
  traceActionWithObservation(label, event, observationFor(lifecycle), note, owner, expectedProposal);
};

export const traceReconcileAction = (
  label: string,
  event: string,
  snapshot: ReconcileTraceSnapshot,
): void => {
  if (!activeTrace || activeTrace.model !== 'ReconcileLifecycle') return;
  traceActionWithObservation(label, event, reconcileObservationFor(snapshot));
};

export const traceEmbeddedPreviewAccepted = (
  lifecycle: LifecycleHandle,
  owner: FormalTraceOwner,
  label: string,
  note?: string,
): void => {
  traceAction(label, lifecycle, 'proposal-response', note, owner);
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
    sourceProfile: traceSourceProfile(),
    sourceCommit: requiredEnvironmentValue('LOOM_SOURCE_COMMIT'),
    contractSourceCommit: requiredEnvironmentValue('LOOM_CONTRACT_SOURCE_COMMIT'),
    sourceFingerprint: requiredEnvironmentValue('LOOM_SOURCE_FINGERPRINT'),
    traces,
  }, null, 2)}\n`);
};
