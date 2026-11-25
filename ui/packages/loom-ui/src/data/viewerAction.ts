import type { LoomOutputRequest, LoomOutputResult } from '../client/api';
import type { ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../contracts/types';

export type ViewerRuntimeAction = NonNullable<ExplorerRuntimeOutputV1['actions']>[number];

export interface LoomViewerActionContext {
  readonly project: string;
  readonly runtime: ExplorerRuntimeV1;
  readonly output: ExplorerRuntimeOutputV1;
  readonly action: ViewerRuntimeAction;
  readonly request: LoomOutputRequest;
  readonly result?: LoomOutputResult;
}

export type LoomViewerActionHandler = (context: LoomViewerActionContext, signal: AbortSignal) => Promise<void> | void;

export function invokeViewerAction(handler: LoomViewerActionHandler, context: LoomViewerActionContext) {
  return handler(context, new AbortController().signal);
}
