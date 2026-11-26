import { isDraftDesynchronized, isStaleSnapshot } from '../model/reconcileRecovery';

export const isDraftRestoreError = (code: string | undefined): boolean =>
  isDraftDesynchronized(code) || isStaleSnapshot(code);
