import type { ExplorerAuthoringApiError } from '../../../api';
import type { ExplorerBuilderCompileResult } from '../../../types';
import {
  isDraftDesynchronized,
  isStaleSnapshot,
  reconcileReceiptMatchesRequest,
  reconcileRecoveryAction,
  reconcileResultIsCurrent,
} from './reconcileRecovery';

const failure = (
  code: string,
  retryable = false,
): ExplorerAuthoringApiError => ({
  status: 409,
  code,
  message: code,
  retryable,
});

const recoveryOptions = (overrides: Partial<{
  readonly attempt: number;
  readonly maxAttempts: number;
  readonly staleRefreshes: number;
  readonly maxStaleRefreshes: number;
}> = {}) => ({
  attempt: 1,
  maxAttempts: 3,
  staleRefreshes: 0,
  maxStaleRefreshes: 2,
  ...overrides,
});

const compileResult = (
  overrides: Partial<ExplorerBuilderCompileResult> = {},
): ExplorerBuilderCompileResult => ({
  snapshotToken: 'S1',
  draftVersion: 3,
  draftDigest: 'D1',
  outputs: [{ outputId: 'patients' }],
  ...overrides,
} as ExplorerBuilderCompileResult);

describe('Builder reconcile recovery', () => {
  it('refreshes a stale snapshot at most twice, then repairs', () => {
    const stale = failure('STALE_SNAPSHOT');
    expect(reconcileRecoveryAction(stale, recoveryOptions())).toBe('refresh-and-retry');
    expect(reconcileRecoveryAction(stale, recoveryOptions({ staleRefreshes: 1 }))).toBe('refresh-and-retry');
    expect(reconcileRecoveryAction(stale, recoveryOptions({ staleRefreshes: 2 }))).toBe('repair');
  });

  it('refreshes and stops on a draft conflict', () => {
    expect(reconcileRecoveryAction(failure('DRAFT_CONFLICT'), recoveryOptions())).toBe('refresh-and-stop');
    expect(reconcileRecoveryAction(failure('INVALID_EXPLORER_COMMAND_RESULT'), recoveryOptions())).toBe('refresh-and-stop');
  });

  it('retries transient failures while attempts remain', () => {
    const transient = failure('BACKEND_UNAVAILABLE', true);
    expect(reconcileRecoveryAction(transient, recoveryOptions())).toBe('retry');
    expect(reconcileRecoveryAction(transient, recoveryOptions({ attempt: 2 }))).toBe('retry');
    expect(reconcileRecoveryAction(transient, recoveryOptions({ attempt: 3 }))).toBe('repair');
  });

  it('repairs non-retryable failures', () => {
    expect(reconcileRecoveryAction(failure('VALIDATION_FAILED'), recoveryOptions())).toBe('repair');
  });

  it('classifies stale snapshot and draft conflict codes', () => {
    expect(isStaleSnapshot('STALE_CATALOG_SNAPSHOT')).toBe(true);
    expect(isStaleSnapshot('DRAFT_CONFLICT')).toBe(false);
    expect(isDraftDesynchronized('DRAFT_CONFLICT')).toBe(true);
    expect(isDraftDesynchronized('STALE_SNAPSHOT')).toBe(false);
  });
});

describe('Builder reconcile result acceptance', () => {
  it('accepts only the current generation, explorer, and workspace fingerprint', () => {
    const current = {
      generation: 4,
      currentGeneration: 4,
      submittedExplorerId: 'patients',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F1',
      currentFingerprint: 'F1',
    };
    expect(reconcileResultIsCurrent(current)).toBe(true);
    expect(reconcileResultIsCurrent({ ...current, currentGeneration: 5 })).toBe(false);
    expect(reconcileResultIsCurrent({ ...current, currentExplorerId: 'other' })).toBe(false);
    expect(reconcileResultIsCurrent({ ...current, currentFingerprint: 'F2' })).toBe(false);
  });

  it('accepts a receipt only when it matches the submitted draft and output', () => {
    const request = {
      snapshotToken: 'S1',
      outputId: 'patients',
    };
    expect(reconcileReceiptMatchesRequest(compileResult(), request)).toBe(true);
    expect(reconcileReceiptMatchesRequest(compileResult({ snapshotToken: 'S2' }), request)).toBe(false);
    expect(reconcileReceiptMatchesRequest(compileResult({ outputs: [{ outputId: 'other', columns: [] }] }), request)).toBe(false);
  });
});
