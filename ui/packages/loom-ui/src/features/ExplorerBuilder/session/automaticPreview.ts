import type { DraftIdentity } from '../../../contracts/identity';
import type { ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import type { QueryResult } from '../../../data/queries';
import { resourceFor, type QueryRefetchOptions } from '../../../data/resource';
import type { PreviewLimit } from '../model/previewRecovery';

export type AutomaticPreviewIdentity = DraftIdentity & {
  readonly key: string;
  readonly previewRequestVersion: number;
  readonly outputId: string;
  readonly limit: PreviewLimit;
};

export type AutomaticPreviewRun = {
  readonly identity: AutomaticPreviewIdentity;
  readonly receipt: ExplorerBuilderCompileResult;
  readonly preview: ExplorerBuilderPreviewResult;
};

export type PreviewRequest = {
  readonly outputId: string;
  readonly limit: PreviewLimit;
  readonly receiptRefreshes: number;
};

export type AutomaticPreviewExecution = {
  cancelled: boolean;
  compileGeneration?: number;
  compileRequest?: { readonly abort: () => void };
  previewGeneration?: number;
  previewRequest?: { readonly abort: () => void };
};

type Refresh = (
  request: AutomaticPreviewIdentity,
  signal: AbortSignal,
  options?: QueryRefetchOptions,
) => Promise<AutomaticPreviewRun | undefined>;

type PreviewResource = ReturnType<typeof resourceFor<AutomaticPreviewRun | undefined>>;

export function createAutomaticPreview() {
  let current: { request: AutomaticPreviewIdentity | undefined; refresh: Refresh } | undefined;
  let settled:
    | { readonly key: string; readonly run: AutomaticPreviewRun | undefined }
    | { readonly key: string; readonly error: unknown }
    | undefined;

  return {
    update(request: AutomaticPreviewIdentity | undefined, refresh: Refresh) {
      current = { request, refresh };
    },
    createResource(enabled: boolean): PreviewResource {
      return resourceFor(async (signal, options) => {
        const { request, refresh } = current ?? {};
        if (!request || !refresh) throw new Error('Automatic preview started without a saved draft identity.');
        if (!options?.reload && settled?.key === request.key) {
          if ('error' in settled) throw settled.error;
          return settled.run;
        }
        try {
          const run = await refresh(request, signal, options);
          if (!signal.aborted && current?.request?.key === request.key) settled = { key: request.key, run };
          return run;
        } catch (error) {
          if (!signal.aborted && current?.request?.key === request.key) settled = { key: request.key, error };
          throw error;
        }
      }, Boolean(current?.request?.key) && enabled);
    },
    read(
      resource: PreviewResource,
      snapshot: ReturnType<PreviewResource['getSnapshot']> = resource.getSnapshot(),
    ): QueryResult<AutomaticPreviewRun | undefined> {
      const request = current?.request;
      return {
        ...snapshot,
        isLoading: snapshot.loading,
        isFetching: snapshot.loading,
        data: snapshot.data ?? (settled && settled.key === request?.key && 'run' in settled ? settled.run : undefined),
        refetch: (options?: QueryRefetchOptions) => {
          if (request) settled = undefined;
          return resource.refresh(options);
        },
      };
    },
  };
}
