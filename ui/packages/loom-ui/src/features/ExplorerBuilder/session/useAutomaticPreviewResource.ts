import { useMemo, useState, useSyncExternalStore } from 'react';
import type { QueryRefetchOptions } from '../../../data/resource';
import { createAutomaticPreview, type AutomaticPreviewIdentity, type AutomaticPreviewRun } from './automaticPreview';

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
  const [owner] = useState(createAutomaticPreview);
  owner.update(request, refresh);
  const resource = useMemo(() => owner.createResource(enabled), [owner, request?.key, enabled]);
  const snapshot = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return owner.read(resource, snapshot);
};
