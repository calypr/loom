import type { ExplorerBuilderCompileResult } from '../../../types';
import {
  reconcileReceiptMatchesRequest,
  reconcileResultIsCurrent,
  type ReconcileReceiptIdentity,
  type ReconcileResultIdentity,
} from './reconcileRecovery';

export type ReconcileAcceptance = 'obsolete' | 'mismatch' | 'diagnostics' | 'accepted';

export const reconcileAcceptance = (
  value: ExplorerBuilderCompileResult,
  identity: ReconcileResultIdentity,
  request: ReconcileReceiptIdentity,
): ReconcileAcceptance => {
  if (!reconcileResultIsCurrent(identity)) return 'obsolete';
  if (!reconcileReceiptMatchesRequest(value, request)) return 'mismatch';
  if (value.diagnostics.some((diagnostic) => diagnostic.severity === 'error')) return 'diagnostics';
  return 'accepted';
};
