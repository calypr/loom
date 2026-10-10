import { useRef } from 'react';
import { useKeyedQuery, type QueryRefetchOptions } from '../../react';
import type { ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult } from '../../types';
import type { PreviewLimit } from './authoring/previewRecovery';

export type AutomaticPreviewIdentity = {
  readonly key: string;
  readonly ownerKey: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly previewRequestVersion: number;
  readonly outputId: string;
  readonly limit: PreviewLimit;
};

export type AutomaticPreviewRun = {
  readonly identity: AutomaticPreviewIdentity;
  readonly receipt: ExplorerBuilderCompileResult;
  readonly preview: ExplorerBuilderPreviewResult;
};

export const useAutomaticPreview = ({
  request,
  enabled,
  refresh,
}: {
  readonly request: AutomaticPreviewIdentity | undefined;
  readonly enabled: boolean;
  readonly refresh: (
    request: AutomaticPreviewIdentity,
    signal: AbortSignal,
    options?: QueryRefetchOptions,
  ) => Promise<AutomaticPreviewRun | undefined>;
}) => {
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const requestRef = useRef(request);
  requestRef.current = request;
  const settledRef = useRef<
    | { readonly key: string; readonly run: AutomaticPreviewRun | undefined }
    | { readonly key: string; readonly error: unknown }
    | undefined
  >(undefined);
  const query = useKeyedQuery(
    request?.key,
    async (signal, options) => {
      if (!request) throw new Error('Automatic preview started without a saved draft identity.');
      if (!options?.reload && settledRef.current?.key === request.key) {
        if ('error' in settledRef.current) throw settledRef.current.error;
        return settledRef.current.run;
      }
      try {
        const run = await refreshRef.current(request, signal, options);
        if (!signal.aborted && requestRef.current?.key === request.key) {
          settledRef.current = { key: request.key, run };
        }
        return run;
      } catch (error) {
        if (!signal.aborted && requestRef.current?.key === request.key) {
          settledRef.current = { key: request.key, error };
        }
        throw error;
      }
    },
    enabled,
  );
  const settled = settledRef.current;
  return {
    ...query,
    data: query.data ?? (
      settled && settled.key === request?.key && 'run' in settled ? settled.run : undefined
    ),
    refetch: (options?: QueryRefetchOptions) => {
      if (request) settledRef.current = undefined;
      return query.refetch(options);
    },
  };
};
