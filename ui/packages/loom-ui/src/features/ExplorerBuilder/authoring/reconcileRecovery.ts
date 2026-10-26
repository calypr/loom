import type { ExplorerAuthoringApiError } from '../../../api';
import type { ExplorerBuilderCompileResult } from '../../../types';

const staleSnapshotCodes = new Set([
  'STALE_CATALOG_SNAPSHOT',
  'STALE_SNAPSHOT',
  'SNAPSHOT_STALE',
  'STALE_RECEIPT',
  'RECEIPT_STALE',
  'COMPILE_RECEIPT_NOT_FOUND',
  'RECEIPT_RECOMPILE_REQUIRED',
]);

const draftDesynchronizedCodes = new Set([
  'DRAFT_CONFLICT',
  'INVALID_EXPLORER_COMMAND_RESULT',
]);

export const isStaleSnapshot = (code: string | undefined): boolean =>
  staleSnapshotCodes.has(code ?? '');

export const isDraftDesynchronized = (code: string | undefined): boolean =>
  draftDesynchronizedCodes.has(code ?? '');

export type ReconcileRecoveryAction =
  | 'refresh-and-retry'
  | 'refresh-and-stop'
  | 'retry'
  | 'repair';

export type ReconcileRecoveryOptions = {
  readonly attempt: number;
  readonly maxAttempts: number;
  readonly staleRefreshes: number;
  readonly maxStaleRefreshes: number;
};

export const reconcileRecoveryAction = (
  error: ExplorerAuthoringApiError,
  options: ReconcileRecoveryOptions,
): ReconcileRecoveryAction => {
  if (isStaleSnapshot(error.code)) {
    return options.staleRefreshes < options.maxStaleRefreshes
      ? 'refresh-and-retry'
      : 'repair';
  }
  if (isDraftDesynchronized(error.code)) return 'refresh-and-stop';
  if (error.retryable && options.attempt < options.maxAttempts) return 'retry';
  return 'repair';
};

export type ReconcileResultIdentity = {
  readonly generation: number;
  readonly currentGeneration: number;
  readonly submittedExplorerId: string;
  readonly currentExplorerId: string;
  readonly submittedFingerprint: string;
  readonly currentFingerprint: string;
};

export const reconcileResultIsCurrent = (
  identity: ReconcileResultIdentity,
): boolean =>
  identity.generation === identity.currentGeneration &&
  identity.submittedExplorerId === identity.currentExplorerId &&
  identity.submittedFingerprint === identity.currentFingerprint;

export type ReconcileReceiptIdentity = {
  readonly snapshotToken: string;
  readonly outputId?: string;
};

export const reconcileReceiptMatchesRequest = (
  value: ExplorerBuilderCompileResult,
  request: ReconcileReceiptIdentity,
): boolean =>
  value.snapshotToken === request.snapshotToken &&
  (request.outputId === undefined ||
    value.outputs.some((output) => output.outputId === request.outputId));
