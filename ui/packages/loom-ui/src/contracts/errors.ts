import type { ExplorerAuthoringDiagnostic } from './types';

export interface ExplorerAuthoringApiError {
  readonly status: number | 'FETCH_ERROR' | 'CUSTOM_ERROR';
  readonly code?: string;
  readonly message: string;
  readonly diagnostics?: ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;
  readonly retryable?: boolean;
}
