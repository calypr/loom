import { isDraftDesynchronized, isStaleSnapshot } from './reconcileRecovery';

export const isDraftRestoreError = (code: string | undefined): boolean =>
  isDraftDesynchronized(code) || isStaleSnapshot(code);
